package identity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

var (
	ErrSocialUnavailable = errors.New("social provider is not configured")
	ErrSocialState       = errors.New("social authorization state is invalid or expired")
	ErrSocialExchange    = errors.New("social provider token exchange failed")
	ErrSocialClaims      = errors.New("social provider claims are invalid")
)

const (
	defaultSocialStateTTL  = 10 * time.Minute
	defaultSocialHTTPTime  = 10 * time.Second
	defaultSocialCallback  = "http://localhost:8080"
	socialStateCleanupTime = time.Hour
)

type SocialProvider struct {
	Name         string
	DisplayName  string
	ClientID     string
	ClientSecret string
	Issuer       string
	AuthURL      string
	TokenURL     string
	JWKSURL      string
	UserInfoURL  string
	Scopes       []string
	OIDC         bool
	PKCE         bool
	BasicAuth    bool
	EmailClaim   string
	SubjectClaim string
	NameClaim    string
	AvatarClaim  string
	ExtraAuth    url.Values
	AppleSecret  *AppleClientSecret
}

type AppleClientSecret struct {
	TeamID     string
	KeyID      string
	PrivateKey *ecdsa.PrivateKey
}

type SocialConfig struct {
	Providers     map[string]SocialProvider
	CallbackBase  string
	StateTTL      time.Duration
	HTTPTimeout   time.Duration
	AllowedOrigin string
}

type SocialService struct {
	store    *store.Store
	identity *Service
	cfg      SocialConfig
	client   *http.Client
	now      func() time.Time
}

func NewSocialService(st *store.Store, identityService *Service, cfg SocialConfig) *SocialService {
	if cfg.StateTTL <= 0 {
		cfg.StateTTL = defaultSocialStateTTL
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = defaultSocialHTTPTime
	}
	cfg.CallbackBase = strings.TrimRight(cfg.CallbackBase, "/")
	return &SocialService{
		store:    st,
		identity: identityService,
		cfg:      cfg,
		client:   &http.Client{Timeout: cfg.HTTPTimeout},
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *SocialService) Providers() []map[string]string {
	names := make([]string, 0, len(s.cfg.Providers))
	for name := range s.cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	providers := make([]map[string]string, 0, len(names))
	for _, name := range names {
		provider := s.cfg.Providers[name]
		providers = append(providers, map[string]string{
			"id":   name,
			"name": provider.DisplayName,
		})
	}
	return providers
}

func (s *SocialService) Provider(name string) (SocialProvider, bool) {
	provider, ok := s.cfg.Providers[strings.ToLower(strings.TrimSpace(name))]
	return provider, ok
}

func (s *SocialService) Start(
	ctx context.Context,
	name string,
	returnTo string,
) (string, time.Time, error) {
	provider, ok := s.Provider(name)
	if !ok || provider.ClientID == "" {
		return "", time.Time{}, ErrSocialUnavailable
	}
	state, err := randomURLToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	verifier := ""
	challenge := ""
	if provider.PKCE {
		verifier, err = randomURLToken(48)
		if err != nil {
			return "", time.Time{}, err
		}
		digest := sha256.Sum256([]byte(verifier))
		challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	}
	nonce, err := randomURLToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	_, _ = s.store.PruneOAuthStates(ctx, now.Add(-s.cfg.StateTTL))
	expires := now.Add(s.cfg.StateTTL)
	record := store.OAuthState{
		StateHash: auth.HashToken(state),
		Provider:  provider.Name,
		Verifier:  verifier,
		Nonce:     nonce,
		ReturnTo:  sanitizeReturnTo(returnTo),
		ExpiresAt: expires,
		CreatedAt: now,
	}
	if err := s.store.CreateOAuthState(ctx, record); err != nil {
		return "", time.Time{}, err
	}

	redirectURI := s.callbackURL(provider.Name)
	query := url.Values{}
	query.Set("client_id", provider.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(provider.Scopes, " "))
	query.Set("state", state)
	if provider.OIDC {
		query.Set("nonce", nonce)
	}
	if challenge != "" {
		query.Set("code_challenge", challenge)
		query.Set("code_challenge_method", "S256")
	}
	for key, values := range provider.ExtraAuth {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	location := provider.AuthURL
	if strings.Contains(location, "?") {
		location += "&" + query.Encode()
	} else {
		location += "?" + query.Encode()
	}
	return location, expires, nil
}

func (s *SocialService) Complete(
	ctx context.Context,
	name, stateValue, code string,
	form url.Values,
	meta RequestMeta,
) (SessionResult, string, error) {
	provider, ok := s.Provider(name)
	if !ok || provider.ClientID == "" {
		return SessionResult{}, "/", ErrSocialUnavailable
	}
	stateValue = strings.TrimSpace(stateValue)
	code = strings.TrimSpace(code)
	if stateValue == "" || code == "" {
		return SessionResult{}, "/", ErrSocialState
	}
	now := s.now()
	state, err := s.store.ConsumeOAuthState(ctx, auth.HashToken(stateValue), now)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SessionResult{}, "/", ErrSocialState
		}
		return SessionResult{}, "/", err
	}
	if state.Provider != provider.Name {
		return SessionResult{}, "/", ErrSocialState
	}
	returnTo := sanitizeReturnTo(state.ReturnTo)

	secret, err := s.clientSecret(provider, now)
	if err != nil {
		return SessionResult{}, returnTo, err
	}
	token, err := s.exchange(ctx, provider, code, state.Verifier, s.callbackURL(provider.Name), secret)
	if err != nil {
		return SessionResult{}, returnTo, err
	}

	var claims map[string]any
	if provider.OIDC {
		if token.IDToken == "" {
			return SessionResult{}, returnTo, ErrSocialClaims
		}
		claims, err = s.verifyIDToken(ctx, provider, token.IDToken, state.Nonce, now)
		if err != nil {
			return SessionResult{}, returnTo, err
		}
	} else {
		claims, err = s.fetchUserInfo(ctx, provider, token.AccessToken)
		if err != nil {
			return SessionResult{}, returnTo, err
		}
	}
	profile := s.profile(provider, claims, form)

	account, actor, channel, err := s.resolveAccount(ctx, provider, profile, now, meta)
	if err != nil {
		return SessionResult{}, returnTo, err
	}
	session, err := s.identity.createSession(ctx, account, actor, channel, provider.Name, meta, now)
	if err != nil {
		return SessionResult{}, returnTo, err
	}
	s.identity.record(ctx, account.ID, "auth.social_login", meta, now, map[string]any{
		"provider": provider.Name,
	})
	return session, returnTo, nil
}

type socialProfile struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

func (s *SocialService) profile(
	provider SocialProvider,
	claims map[string]any,
	form url.Values,
) socialProfile {
	subject := claimString(claims, provider.SubjectClaimOrDefault())
	email := strings.ToLower(strings.TrimSpace(claimString(claims, provider.EmailClaimOrDefault())))
	verified := claimBool(claims, "email_verified")
	name := strings.TrimSpace(claimString(claims, provider.NameClaimOrDefault()))
	avatar := strings.TrimSpace(claimString(claims, provider.AvatarClaimOrDefault()))

	if provider.Name == "apple" {
		if raw := strings.TrimSpace(form.Get("user")); raw != "" {
			var payload struct {
				Name struct {
					FirstName string `json:"firstName"`
					LastName  string `json:"lastName"`
				} `json:"name"`
				Email string `json:"email"`
			}
			if json.Unmarshal([]byte(raw), &payload) == nil {
				full := strings.TrimSpace(payload.Name.FirstName + " " + payload.Name.LastName)
				if name == "" {
					name = full
				}
				if email == "" {
					email = strings.ToLower(strings.TrimSpace(payload.Email))
				}
			}
		}
	}
	if provider.Name == "x" {
		if username := claimString(claims, "username"); name == "" {
			name = username
		}
	}
	if name == "" && email != "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	if name == "" {
		name = provider.DisplayName + " creator"
	}
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	return socialProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: verified,
		Name:          name,
		AvatarURL:     avatar,
	}
}

func (s *SocialService) resolveAccount(
	ctx context.Context,
	provider SocialProvider,
	profile socialProfile,
	now time.Time,
	meta RequestMeta,
) (store.Account, store.Actor, store.Channel, error) {
	if profile.Subject == "" {
		return store.Account{}, store.Actor{}, store.Channel{}, ErrSocialClaims
	}
	account, err := s.store.GetAccountByIdentity(ctx, provider.Name, profile.Subject)
	if err == nil {
		actor, channel, err := s.identity.identityFor(ctx, account)
		if err != nil {
			return store.Account{}, store.Actor{}, store.Channel{}, err
		}
		_ = s.store.LinkSocialIdentity(ctx, store.AccountIdentity{
			ID:          "",
			AccountID:   account.ID,
			Provider:    provider.Name,
			Subject:     profile.Subject,
			Email:       profile.Email,
			DisplayName: profile.Name,
			AvatarURL:   profile.AvatarURL,
			CreatedAt:   now,
			UpdatedAt:   now,
		}, nil)
		return account, actor, channel, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}

	// A provider-verified email may link to an existing password account. An
	// unverified address is never used for linking; the provider identity gets
	// a synthetic address instead so it cannot claim another account.
	if profile.Email != "" && profile.EmailVerified {
		existing, err := s.store.GetAccountByEmail(ctx, profile.Email)
		if err == nil {
			identityID, idErr := auth.NewID("sid")
			if idErr != nil {
				return store.Account{}, store.Actor{}, store.Channel{}, idErr
			}
			verifiedAt := now
			if linkErr := s.store.LinkSocialIdentity(ctx, store.AccountIdentity{
				ID:          identityID,
				AccountID:   existing.ID,
				Provider:    provider.Name,
				Subject:     profile.Subject,
				Email:       profile.Email,
				DisplayName: profile.Name,
				AvatarURL:   profile.AvatarURL,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, &verifiedAt); linkErr != nil {
				if errors.Is(linkErr, store.ErrConflict) {
					return s.reloadSocialAccount(ctx, provider, profile)
				}
				return store.Account{}, store.Actor{}, store.Channel{}, linkErr
			}
			existing.EmailVerifiedAt = &verifiedAt
			actor, channel, err := s.identity.identityFor(ctx, existing)
			if err != nil {
				return store.Account{}, store.Actor{}, store.Channel{}, err
			}
			return existing, actor, channel, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return store.Account{}, store.Actor{}, store.Channel{}, err
		}
	}

	email := profile.Email
	verifiedAt := (*time.Time)(nil)
	if email == "" || !profile.EmailVerified {
		email = syntheticEmail(provider.Name, profile.Subject)
		value := now
		verifiedAt = &value
	} else {
		value := now
		verifiedAt = &value
	}

	actorID, err := auth.NewID("usr")
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	channelID, err := auth.NewID("ch")
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	accountID, err := auth.NewID("acct")
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	identityID, err := auth.NewID("sid")
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	handle, err := s.identity.uniqueHandle(ctx, profile.Name)
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	actor := store.Actor{
		ID:          actorID,
		Type:        "person",
		Handle:      handle,
		DisplayName: profile.Name,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	channel := store.Channel{
		ID:        channelID,
		ActorID:   actorID,
		Handle:    handle,
		Name:      profile.Name,
		AvatarURL: profile.AvatarURL,
		CreatedAt: now,
		UpdatedAt: now,
	}
	account = store.Account{
		ID:              accountID,
		ActorID:         actorID,
		Email:           email,
		PasswordHash:    "",
		EmailVerifiedAt: verifiedAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	identity := store.AccountIdentity{
		ID:          identityID,
		AccountID:   accountID,
		Provider:    provider.Name,
		Subject:     profile.Subject,
		Email:       profile.Email,
		DisplayName: profile.Name,
		AvatarURL:   profile.AvatarURL,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateSocialIdentity(ctx, actor, channel, account, identity); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return s.reloadSocialAccount(ctx, provider, profile)
		}
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	s.identity.record(ctx, account.ID, "auth.social_registered", meta, now, map[string]any{
		"provider": provider.Name,
	})
	return account, actor, channel, nil
}

func (s *SocialService) reloadSocialAccount(
	ctx context.Context,
	provider SocialProvider,
	profile socialProfile,
) (store.Account, store.Actor, store.Channel, error) {
	account, err := s.store.GetAccountByIdentity(ctx, provider.Name, profile.Subject)
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	actor, channel, err := s.identity.identityFor(ctx, account)
	if err != nil {
		return store.Account{}, store.Actor{}, store.Channel{}, err
	}
	return account, actor, channel, nil
}

func (s *SocialService) callbackURL(provider string) string {
	return s.cfg.CallbackBase + "/api/v1/auth/social/" + url.PathEscape(provider) + "/callback"
}

type socialToken struct {
	AccessToken string
	IDToken     string
	TokenType   string
	ExpiresIn   int
}

func (s *SocialService) exchange(
	ctx context.Context,
	provider SocialProvider,
	code, verifier, redirectURI, secret string,
) (socialToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", provider.ClientID)
	if secret != "" {
		form.Set("client_secret", secret)
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, provider.TokenURL, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return socialToken{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if provider.BasicAuth {
		request.SetBasicAuth(provider.ClientID, secret)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return socialToken{}, fmt.Errorf("%w: %v", ErrSocialExchange, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return socialToken{}, fmt.Errorf("%w: %v", ErrSocialExchange, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return socialToken{}, fmt.Errorf("%w: provider returned %d", ErrSocialExchange, response.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return socialToken{}, fmt.Errorf("%w: %v", ErrSocialExchange, err)
	}
	if payload.Error != "" || payload.AccessToken == "" {
		return socialToken{}, ErrSocialExchange
	}
	return socialToken{
		AccessToken: payload.AccessToken,
		IDToken:     payload.IDToken,
		TokenType:   payload.TokenType,
		ExpiresIn:   payload.ExpiresIn,
	}, nil
}

func (s *SocialService) fetchUserInfo(
	ctx context.Context,
	provider SocialProvider,
	accessToken string,
) (map[string]any, error) {
	if provider.UserInfoURL == "" {
		return nil, ErrSocialClaims
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: provider returned %d", ErrSocialClaims, response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return data, nil
	}
	return payload, nil
}

func (s *SocialService) verifyIDToken(
	ctx context.Context,
	provider SocialProvider,
	rawToken, nonce string,
	now time.Time,
) (map[string]any, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, ErrSocialClaims
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrSocialClaims
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, ErrSocialClaims
	}
	if header.Algorithm == "" || header.Algorithm == "none" {
		return nil, ErrSocialClaims
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrSocialClaims
	}
	var claims map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return nil, ErrSocialClaims
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrSocialClaims
	}
	keys, err := s.jwks(ctx, provider, false)
	if err != nil {
		return nil, err
	}
	key := selectJWK(keys, header.KeyID)
	if key == nil {
		keys, err = s.jwks(ctx, provider, true)
		if err != nil {
			return nil, err
		}
		key = selectJWK(keys, header.KeyID)
	}
	if key == nil {
		return nil, ErrSocialClaims
	}
	if err := verifyJWTSignature(header.Algorithm, key, parts[0]+"."+parts[1], signature); err != nil {
		return nil, ErrSocialClaims
	}
	if err := validateSocialClaims(claims, provider, nonce, now); err != nil {
		return nil, err
	}
	return claims, nil
}

func validateSocialClaims(
	claims map[string]any,
	provider SocialProvider,
	nonce string,
	now time.Time,
) error {
	issuer := claimString(claims, "iss")
	if issuer != provider.Issuer && !(provider.Name == "google" && issuer == "accounts.google.com") {
		return ErrSocialClaims
	}
	if !audienceContains(claims["aud"], provider.ClientID) {
		return ErrSocialClaims
	}
	exp, ok := claimTime(claims["exp"])
	if !ok || !now.Before(exp.Add(60*time.Second)) {
		return ErrSocialClaims
	}
	if notBefore, ok := claimTime(claims["nbf"]); ok && now.Add(60*time.Second).Before(notBefore) {
		return ErrSocialClaims
	}
	if nonce != "" && claimString(claims, "nonce") != nonce {
		return ErrSocialClaims
	}
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksCacheEntry struct {
	keys      []jwk
	expiresAt time.Time
	fetchedAt time.Time
}

var (
	socialJWKSCache   = map[string]jwksCacheEntry{}
	socialJWKSCacheMu sync.Mutex
)

func (s *SocialService) jwks(
	ctx context.Context,
	provider SocialProvider,
	force bool,
) ([]jwk, error) {
	if provider.JWKSURL == "" {
		return nil, ErrSocialClaims
	}
	socialJWKSCacheMu.Lock()
	entry, found := socialJWKSCache[provider.JWKSURL]
	socialJWKSCacheMu.Unlock()
	now := s.now()
	if found && !force && now.Before(entry.expiresAt) {
		return entry.keys, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: jwks returned %d", ErrSocialClaims, response.StatusCode)
	}
	var payload struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSocialClaims, err)
	}
	cacheFor := 5 * time.Minute
	if maxAge := response.Header.Get("Cache-Control"); strings.Contains(maxAge, "no-cache") {
		cacheFor = time.Minute
	}
	socialJWKSCacheMu.Lock()
	socialJWKSCache[provider.JWKSURL] = jwksCacheEntry{
		keys:      payload.Keys,
		expiresAt: now.Add(cacheFor),
		fetchedAt: now,
	}
	socialJWKSCacheMu.Unlock()
	return payload.Keys, nil
}

func selectJWK(keys []jwk, kid string) *jwk {
	for index := range keys {
		if kid != "" && keys[index].Kid == kid {
			return &keys[index]
		}
	}
	if kid == "" && len(keys) == 1 {
		return &keys[0]
	}
	return nil
}

func verifyJWTSignature(algorithm string, key *jwk, signingInput string, signature []byte) error {
	digest := sha256.Sum256([]byte(signingInput))
	switch algorithm {
	case "RS256":
		publicKey, err := rsaPublicKey(key)
		if err != nil {
			return err
		}
		return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
	case "ES256":
		publicKey, err := ecdsaPublicKey(key)
		if err != nil {
			return err
		}
		if len(signature) != 64 {
			return ErrSocialClaims
		}
		r := new(big.Int).SetBytes(signature[:32])
		s := new(big.Int).SetBytes(signature[32:])
		if !ecdsa.Verify(publicKey, digest[:], r, s) {
			return ErrSocialClaims
		}
		return nil
	default:
		return ErrSocialClaims
	}
}

func rsaPublicKey(key *jwk) (*rsa.PublicKey, error) {
	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, ErrSocialClaims
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, ErrSocialClaims
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 + int(value)
	}
	if exponent == 0 {
		return nil, ErrSocialClaims
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulusBytes), E: exponent}, nil
}

func ecdsaPublicKey(key *jwk) (*ecdsa.PublicKey, error) {
	if key.Crv != "P-256" {
		return nil, ErrSocialClaims
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(key.X)
	if err != nil {
		return nil, ErrSocialClaims
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(key.Y)
	if err != nil {
		return nil, ErrSocialClaims
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	if !elliptic.P256().IsOnCurve(x, y) {
		return nil, ErrSocialClaims
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

func (s *SocialService) clientSecret(provider SocialProvider, now time.Time) (string, error) {
	if provider.AppleSecret == nil {
		return provider.ClientSecret, nil
	}
	return appleClientSecret(provider.ClientID, provider.AppleSecret, now)
}

func appleClientSecret(clientID string, cfg *AppleClientSecret, now time.Time) (string, error) {
	header := map[string]any{"alg": "ES256", "kid": cfg.KeyID, "typ": "JWT"}
	claims := map[string]any{
		"iss": cfg.TeamID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerBytes) + "." +
		base64.RawURLEncoding.EncodeToString(claimsBytes)
	digest := sha256.Sum256([]byte(signingInput))
	r, sValue, err := ecdsa.Sign(rand.Reader, cfg.PrivateKey, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	sValue.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func SocialConfigFromEnv(getenv func(string) string) (SocialConfig, error) {
	cfg := SocialConfig{
		Providers:    map[string]SocialProvider{},
		CallbackBase: envValue(getenv, "KEELTUBE_SOCIAL_CALLBACK_BASE_URL", defaultSocialCallback),
		StateTTL:     defaultSocialStateTTL,
		HTTPTimeout:  defaultSocialHTTPTime,
	}
	appURL := envValue(getenv, "KEELTUBE_APP_URL", "http://localhost:3000")
	cfg.AllowedOrigin = strings.TrimRight(appURL, "/")
	if raw := strings.TrimSpace(getenv("KEELTUBE_SOCIAL_STATE_TTL_SECONDS")); raw != "" {
		seconds, err := time.ParseDuration(raw + "s")
		if err != nil || seconds < time.Minute || seconds > time.Hour {
			return SocialConfig{}, fmt.Errorf("KEELTUBE_SOCIAL_STATE_TTL_SECONDS must be between 60 and 3600")
		}
		cfg.StateTTL = seconds
	}

	providers := map[string]SocialProvider{
		"google": {
			Name:         "google",
			DisplayName:  "Google",
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			JWKSURL:      "https://www.googleapis.com/oauth2/v3/certs",
			Issuer:       "https://accounts.google.com",
			Scopes:       []string{"openid", "email", "profile"},
			OIDC:         true,
			PKCE:         true,
			EmailClaim:   "email",
			SubjectClaim: "sub",
			NameClaim:    "name",
			AvatarClaim:  "picture",
		},
		"apple": {
			Name:         "apple",
			DisplayName:  "Apple",
			AuthURL:      "https://appleid.apple.com/auth/authorize",
			TokenURL:     "https://appleid.apple.com/auth/token",
			JWKSURL:      "https://appleid.apple.com/auth/keys",
			Issuer:       "https://appleid.apple.com",
			Scopes:       []string{"name", "email"},
			OIDC:         true,
			PKCE:         false,
			EmailClaim:   "email",
			SubjectClaim: "sub",
			NameClaim:    "name",
			AvatarClaim:  "picture",
			ExtraAuth:    url.Values{"response_mode": {"form_post"}},
		},
		"x": {
			Name:         "x",
			DisplayName:  "X",
			AuthURL:      "https://twitter.com/i/oauth2/authorize",
			TokenURL:     "https://twitter.com/i/oauth2/token",
			UserInfoURL:  "https://api.twitter.com/2/users/me?user.fields=id,name,username,profile_image_url,verified",
			Scopes:       []string{"tweet.read", "users.read", "offline.access"},
			OIDC:         false,
			PKCE:         true,
			BasicAuth:    true,
			EmailClaim:   "email",
			SubjectClaim: "id",
			NameClaim:    "name",
			AvatarClaim:  "profile_image_url",
		},
	}

	for name, defaults := range providers {
		prefix := "KEELTUBE_" + strings.ToUpper(name)
		clientID := strings.TrimSpace(getenv(prefix + "_CLIENT_ID"))
		if clientID == "" {
			continue
		}
		provider := defaults
		provider.ClientID = clientID
		provider.ClientSecret = strings.TrimSpace(getenv(prefix + "_CLIENT_SECRET"))
		provider.Issuer = envValue(getenv, prefix+"_ISSUER", provider.Issuer)
		provider.AuthURL = envValue(getenv, prefix+"_AUTH_URL", provider.AuthURL)
		provider.TokenURL = envValue(getenv, prefix+"_TOKEN_URL", provider.TokenURL)
		provider.JWKSURL = envValue(getenv, prefix+"_JWKS_URL", provider.JWKSURL)
		provider.UserInfoURL = envValue(getenv, prefix+"_USERINFO_URL", provider.UserInfoURL)
		provider.EmailClaim = envValue(getenv, prefix+"_EMAIL_CLAIM", provider.EmailClaim)
		provider.SubjectClaim = envValue(getenv, prefix+"_SUBJECT_CLAIM", provider.SubjectClaim)
		provider.NameClaim = envValue(getenv, prefix+"_NAME_CLAIM", provider.NameClaim)
		provider.AvatarClaim = envValue(getenv, prefix+"_AVATAR_CLAIM", provider.AvatarClaim)
		if rawScopes := strings.TrimSpace(getenv(prefix + "_SCOPES")); rawScopes != "" {
			provider.Scopes = splitScopes(rawScopes)
		}
		if rawPKCE := strings.TrimSpace(getenv(prefix + "_PKCE")); rawPKCE != "" {
			provider.PKCE = strings.EqualFold(rawPKCE, "true") || rawPKCE == "1"
		}
		if rawBasic := strings.TrimSpace(getenv(prefix + "_BASIC_AUTH")); rawBasic != "" {
			provider.BasicAuth = strings.EqualFold(rawBasic, "true") || rawBasic == "1"
		}
		if name == "apple" && provider.ClientSecret == "" {
			appleSecret, err := appleSecretFromEnv(getenv)
			if err != nil {
				return SocialConfig{}, err
			}
			provider.AppleSecret = appleSecret
		}
		if provider.OIDC && (provider.Issuer == "" || provider.JWKSURL == "" || provider.AuthURL == "" || provider.TokenURL == "") {
			return SocialConfig{}, fmt.Errorf("%s provider is missing OIDC endpoints", name)
		}
		if !provider.OIDC && (provider.UserInfoURL == "" || provider.AuthURL == "" || provider.TokenURL == "") {
			return SocialConfig{}, fmt.Errorf("%s provider is missing OAuth endpoints", name)
		}
		cfg.Providers[name] = provider
	}
	return cfg, nil
}

func appleSecretFromEnv(getenv func(string) string) (*AppleClientSecret, error) {
	teamID := strings.TrimSpace(getenv("KEELTUBE_APPLE_TEAM_ID"))
	keyID := strings.TrimSpace(getenv("KEELTUBE_APPLE_KEY_ID"))
	rawKey := strings.TrimSpace(getenv("KEELTUBE_APPLE_PRIVATE_KEY"))
	keyPath := strings.TrimSpace(getenv("KEELTUBE_APPLE_PRIVATE_KEY_PATH"))
	if rawKey == "" && keyPath != "" {
		contents, err := os.ReadFile(filepath.Clean(keyPath))
		if err != nil {
			return nil, fmt.Errorf("read KEELTUBE_APPLE_PRIVATE_KEY_PATH: %w", err)
		}
		rawKey = string(contents)
	}
	if teamID == "" || keyID == "" || rawKey == "" {
		return nil, fmt.Errorf(
			"Apple sign-in requires KEELTUBE_APPLE_CLIENT_SECRET or TEAM_ID, KEY_ID, and PRIVATE_KEY",
		)
	}
	rawKey = strings.ReplaceAll(rawKey, `\n`, "\n")
	block, _ := pem.Decode([]byte(rawKey))
	if block == nil {
		return nil, fmt.Errorf("KEELTUBE_APPLE_PRIVATE_KEY is not a PEM key")
	}
	var key *ecdsa.PrivateKey
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		typed, ok := parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("KEELTUBE_APPLE_PRIVATE_KEY must be an EC P-256 private key")
		}
		key = typed
	} else if typed, sec1Err := x509.ParseECPrivateKey(block.Bytes); sec1Err == nil {
		key = typed
	} else {
		return nil, fmt.Errorf("KEELTUBE_APPLE_PRIVATE_KEY must be an EC P-256 private key")
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("KEELTUBE_APPLE_PRIVATE_KEY must use the P-256 curve")
	}
	return &AppleClientSecret{TeamID: teamID, KeyID: keyID, PrivateKey: key}, nil
}

func (p SocialProvider) SubjectClaimOrDefault() string {
	if p.SubjectClaim != "" {
		return p.SubjectClaim
	}
	return "sub"
}

func (p SocialProvider) EmailClaimOrDefault() string {
	if p.EmailClaim != "" {
		return p.EmailClaim
	}
	return "email"
}

func (p SocialProvider) NameClaimOrDefault() string {
	if p.NameClaim != "" {
		return p.NameClaim
	}
	return "name"
}

func (p SocialProvider) AvatarClaimOrDefault() string {
	if p.AvatarClaim != "" {
		return p.AvatarClaim
	}
	return "picture"
}

func claimString(claims map[string]any, key string) string {
	if key == "" {
		return ""
	}
	value, ok := claims[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func claimBool(claims map[string]any, key string) bool {
	value, ok := claims[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || typed == "1"
	default:
		return false
	}
}

func claimTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		seconds, err := typed.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(seconds, 0).UTC(), true
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case string:
		parsed, err := time.Parse(time.RFC3339, typed)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	default:
		return time.Time{}, false
	}
}

func audienceContains(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return typed == expected
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == expected {
				return true
			}
		}
	}
	return false
}

func randomURLToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func sanitizeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	if strings.ContainsAny(value, "\r\n") {
		return "/"
	}
	return value
}

func syntheticEmail(provider, subject string) string {
	digest := sha256.Sum256([]byte(provider + "\x00" + subject))
	return "social+" + provider + "+" + fmt.Sprintf("%x", digest[:12]) + "@users.keeltube.invalid"
}

func splitScopes(value string) []string {
	values := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' '
	})
	result := make([]string, 0, len(values))
	for _, item := range values {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func envValue(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}
