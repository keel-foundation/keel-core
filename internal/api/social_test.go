package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestSocialOAuthSignInCreatesSessionAndIsSingleUse(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-token",
				"token_type":   "bearer",
				"expires_in":   3600,
			})
		case "/users/me":
			if r.Header.Get("Authorization") != "Bearer access-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":                "x-123",
					"name":              "X Creator",
					"username":          "xcreator",
					"profile_image_url": "https://example.com/avatar.png",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer providerServer.Close()

	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	identityService := identity.New(st, mailer.NewLog(testWriter{t}), testIdentityConfig())
	social := identity.NewSocialService(st, identityService, identity.SocialConfig{
		CallbackBase: "http://localhost:8080",
		Providers: map[string]identity.SocialProvider{
			"x": {
				Name:         "x",
				DisplayName:  "X",
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				AuthURL:      providerServer.URL + "/authorize",
				TokenURL:     providerServer.URL + "/token",
				UserInfoURL:  providerServer.URL + "/users/me",
				Scopes:       []string{"users.read"},
				PKCE:         true,
				SubjectClaim: "id",
				NameClaim:    "name",
				AvatarClaim:  "profile_image_url",
			},
		},
	})
	handler := api.New(api.Options{
		Store:      st,
		Auth:       auth.NewDev("usr_demo", ""),
		Identity:   identityService,
		Social:     social,
		Origins:    []string{"http://localhost:3000"},
		RateWindow: time.Minute,
	})

	rec := request(t, handler, "/api/v1/auth/social/x/start?format=json&return_to=%2Fstudio", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var start struct {
		AuthorizationURL string    `json:"authorization_url"`
		ExpiresAt        time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	parsed, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" || parsed.Query().Get("code_challenge") == "" {
		t.Fatalf("missing state or PKCE challenge: %s", start.AuthorizationURL)
	}

	callback := "/api/v1/auth/social/x/callback?format=json&state=" +
		url.QueryEscape(state) + "&code=code-1"
	rec = request(t, handler, callback, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	var session identity.SessionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.Provider != "x" || session.User.Name != "X Creator" || session.User.ID == "" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("expected session cookies")
	}
	if _, err := st.GetAccountIdentity(context.Background(), "x", "x-123"); err != nil {
		t.Fatalf("identity was not persisted: %v", err)
	}

	replay := request(t, handler, callback, "")
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("expected state replay rejection, got %d %s", replay.Code, replay.Body.String())
	}
}

func TestSocialProvidersEndpointListsConfiguredProviders(t *testing.T) {
	handler, _ := newHandlerWithStore(t, "")
	rec := request(t, handler, "/api/v1/auth/social/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"providers":[]`) {
		t.Fatalf("expected an empty provider list, got %s", rec.Body.String())
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
