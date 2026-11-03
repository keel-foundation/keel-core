package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newHandler(t *testing.T, token string) http.Handler {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:8081", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	identityService := identity.New(st, mailer.NewLog(io.Discard), testIdentityConfig())
	authenticator := auth.Fallback{
		Primary:        identityService,
		Secondary:      auth.NewDev("usr_demo", token),
		AllowSecondary: true,
	}
	return api.New(api.Options{
		Store:      st,
		Auth:       authenticator,
		Identity:   identityService,
		Origins:    []string{"http://localhost:3000"},
		RateWindow: time.Minute,
	})
}

func testIdentityConfig() identity.Config {
	cfg := identity.DefaultConfig()
	cfg.ExposeCodes = true
	cfg.CookieSecure = false
	cfg.SessionTTL = time.Hour
	cfg.ResendCooldown = 0
	cfg.MaxLoginAttempts = 3
	return cfg
}

func request(t *testing.T, handler http.Handler, path, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return value
}

func TestHealth(t *testing.T) {
	handler := newHandler(t, "")
	rec := request(t, handler, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decode[map[string]string](t, rec)
	if body["status"] != "ok" || body["service"] != "core" {
		t.Fatalf("unexpected health: %v", body)
	}
}

func TestVideoAndChannelEndpoints(t *testing.T) {
	handler := newHandler(t, "")

	cases := []struct {
		path   string
		status int
		field  string
		want   string
	}{
		{"/api/v1/videos/vid_demo_001", http.StatusOK, "id", "vid_demo_001"},
		{"/api/v1/videos/demo", http.StatusOK, "id", "vid_demo_001"},
		{"/api/v1/channels/ch_demo", http.StatusOK, "id", "ch_demo"},
		{"/api/v1/channels/demo", http.StatusOK, "id", "ch_demo"},
		{"/api/v1/videos/vid_missing", http.StatusNotFound, "", ""},
		{"/api/v1/channels/ch_missing", http.StatusNotFound, "", ""},
	}

	for _, tc := range cases {
		rec := request(t, handler, tc.path, "")
		if rec.Code != tc.status {
			t.Fatalf("%s: expected %d, got %d", tc.path, tc.status, rec.Code)
		}
		if tc.field != "" {
			body := decode[map[string]any](t, rec)
			if body[tc.field] != tc.want {
				t.Fatalf("%s: unexpected %s: %v", tc.path, tc.field, body[tc.field])
			}
		}
	}
}

func TestDemoPayload(t *testing.T) {
	handler := newHandler(t, "")
	rec := request(t, handler, "/api/v1/demo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Video   map[string]any `json:"video"`
		Channel map[string]any `json:"channel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode demo payload: %v", err)
	}
	if payload.Video["id"] != "vid_demo_001" || payload.Channel["id"] != "ch_demo" {
		t.Fatalf("unexpected demo payload: %+v", payload)
	}
}

func TestDevPrincipal(t *testing.T) {
	open := newHandler(t, "")
	rec := request(t, open, "/api/v1/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	principal := decode[auth.Principal](t, rec)
	if principal.ID != "usr_demo" || principal.Role != "creator" {
		t.Fatalf("unexpected principal: %+v", principal)
	}

	devToken, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate dev token: %v", err)
	}
	protected := newHandler(t, devToken)
	if rec := request(t, protected, "/api/v1/me", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", rec.Code)
	}
}

func TestCORSOnlyReflectsConfiguredOrigins(t *testing.T) {
	handler := newHandler(t, "")

	rec := request(t, handler, "/health", "https://evil.example")
	if header := rec.Header().Get("Access-Control-Allow-Origin"); header != "" {
		t.Fatalf("unexpected CORS header for disallowed origin: %q", header)
	}

	rec = request(t, handler, "/health", "http://localhost:3000")
	if header := rec.Header().Get("Access-Control-Allow-Origin"); header != "http://localhost:3000" {
		t.Fatalf("unexpected CORS header: %q", header)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	preflight.Header.Set("Origin", "http://localhost:3000")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, preflight)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected CORS methods on preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentialed CORS, got %q", got)
	}
}
