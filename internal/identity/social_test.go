package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newSocialTestService(t *testing.T) (*SocialService, *Service, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	messages := &captureMailer{}
	identityService := New(st, messages, testConfig())
	social := NewSocialService(st, identityService, SocialConfig{
		Providers: map[string]SocialProvider{
			"google": {Name: "google", DisplayName: "Google", ClientID: "client", OIDC: true},
		},
		CallbackBase: "http://localhost:8080",
	})
	return social, identityService, st
}

func TestSocialIdentityCreatesAnAccountAndReusesIt(t *testing.T) {
	social, _, st := newSocialTestService(t)
	ctx := context.Background()
	provider := social.cfg.Providers["google"]
	profile := socialProfile{
		Subject: "google-subject-1",
		Name:    "New Creator",
		Email:   "new@example.com",
	}

	account, actor, channel, err := social.resolveAccount(ctx, provider, profile, time.Now().UTC(), RequestMeta{})
	if err != nil {
		t.Fatalf("resolve new social account: %v", err)
	}
	if account.PasswordHash != "" {
		t.Fatalf("social account should not have a password, got %q", account.PasswordHash)
	}
	if account.EmailVerifiedAt == nil {
		t.Fatal("social account should be verified")
	}
	if actor.DisplayName != "New Creator" || channel.Name != "New Creator" {
		t.Fatalf("unexpected profile: actor=%+v channel=%+v", actor, channel)
	}
	identityRecord, err := st.GetAccountIdentity(ctx, "google", "google-subject-1")
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if identityRecord.Email != "new@example.com" {
		t.Fatalf("unexpected identity email: %q", identityRecord.Email)
	}

	again, againActor, againChannel, err := social.resolveAccount(
		ctx, provider, profile, time.Now().UTC(), RequestMeta{},
	)
	if err != nil {
		t.Fatalf("resolve existing social account: %v", err)
	}
	if again.ID != account.ID || againActor.ID != actor.ID || againChannel.ID != channel.ID {
		t.Fatalf("social identity did not reuse the account")
	}
}

func TestSocialVerifiedEmailLinksToExistingPasswordAccount(t *testing.T) {
	social, identityService, st := newSocialTestService(t)
	ctx := context.Background()
	registered, _ := registerUser(t, identityService)
	provider := social.cfg.Providers["google"]

	account, _, _, err := social.resolveAccount(ctx, provider, socialProfile{
		Subject:       "google-subject-2",
		Name:          "Existing Creator",
		Email:         registered.Email,
		EmailVerified: true,
	}, time.Now().UTC(), RequestMeta{})
	if err != nil {
		t.Fatalf("link social account: %v", err)
	}
	if account.ID != registered.User.AccountID {
		t.Fatalf("expected existing account %s, got %s", registered.User.AccountID, account.ID)
	}
	if _, err := st.GetAccountIdentity(ctx, "google", "google-subject-2"); err != nil {
		t.Fatalf("expected linked identity: %v", err)
	}
}

func TestSocialUnverifiedEmailDoesNotLinkOrClaimTheAddress(t *testing.T) {
	social, identityService, _ := newSocialTestService(t)
	ctx := context.Background()
	registered, _ := registerUser(t, identityService)
	provider := social.cfg.Providers["google"]

	account, _, _, err := social.resolveAccount(ctx, provider, socialProfile{
		Subject:       "google-subject-3",
		Name:          "Other Creator",
		Email:         registered.Email,
		EmailVerified: false,
	}, time.Now().UTC(), RequestMeta{})
	if err != nil {
		t.Fatalf("create social account: %v", err)
	}
	if account.ID == registered.User.AccountID {
		t.Fatal("unverified email must not link to an existing account")
	}
	if account.Email == registered.Email || account.Email == "" {
		t.Fatalf("expected a synthetic email, got %q", account.Email)
	}
}

func TestOIDCIDTokenSignatureAndClaimsAreVerified(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
		writeJSONTest(w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": "key-1",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(exponent),
			}},
		})
	}))
	defer server.Close()

	social, _, _ := newSocialTestService(t)
	provider := SocialProvider{
		Name:         "google",
		ClientID:     "client",
		Issuer:       "https://accounts.google.com",
		JWKSURL:      server.URL,
		OIDC:         true,
		SubjectClaim: "sub",
	}
	now := time.Now().UTC()
	token := signTestToken(t, privateKey, map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            "client",
		"sub":            "subject-1",
		"email":          "person@example.com",
		"email_verified": true,
		"nonce":          "nonce-1",
		"iat":            now.Unix(),
		"exp":            now.Add(time.Minute).Unix(),
	}, "key-1")

	claims, err := social.verifyIDToken(context.Background(), provider, token, "nonce-1", now)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims["sub"] != "subject-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	if _, err := social.verifyIDToken(context.Background(), provider, token, "wrong", now); !errors.Is(err, ErrSocialClaims) {
		t.Fatalf("expected nonce rejection, got %v", err)
	}
	if _, err := social.verifyIDToken(context.Background(), provider, token, "nonce-1", now.Add(2*time.Minute)); !errors.Is(err, ErrSocialClaims) {
		t.Fatalf("expected expiry rejection, got %v", err)
	}
}

func TestSocialConfigRequiresCompleteAppleCredentials(t *testing.T) {
	_, err := SocialConfigFromEnv(func(name string) string {
		if name == "KEELTUBE_APPLE_CLIENT_ID" {
			return "apple-client"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected Apple credential error")
	}
}

func TestSanitizeReturnToRejectsOffSiteTargets(t *testing.T) {
	for _, value := range []string{"", "https://evil.example", "//evil.example", "/\nLocation"} {
		if got := sanitizeReturnTo(value); got != "/" {
			t.Fatalf("sanitize(%q) = %q", value, got)
		}
	}
	if got := sanitizeReturnTo("/studio/videos"); got != "/studio/videos" {
		t.Fatalf("unexpected return target: %q", got)
	}
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any, kid string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func writeJSONTest(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
