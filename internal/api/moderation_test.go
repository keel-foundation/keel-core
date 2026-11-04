package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newModerationHandler(t *testing.T, devToken, moderationURL string) (http.Handler, *store.Store) {
	t.Helper()
	return newModerationHandlerWithService(
		t,
		devToken,
		moderation.NewClient(moderationURL, "gateway-token", time.Second),
	)
}

func newModerationHandlerWithService(
	t *testing.T,
	devToken string,
	service moderation.Service,
) (http.Handler, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(
		context.Background(),
		st,
		"http://localhost:3000/media/ksKCMmvOxwQ.mp4",
		"http://localhost:3000",
		"",
		"",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	identityService := identity.New(st, mailer.NewLog(io.Discard), testIdentityConfig())
	authenticator := auth.Fallback{
		Primary:        identityService,
		Secondary:      auth.NewDev("usr_demo", devToken),
		AllowSecondary: true,
	}
	return api.New(api.Options{
		Store:      st,
		Auth:       authenticator,
		Identity:   identityService,
		Moderation: service,
		Origins:    []string{"http://localhost:3000"},
		RateWindow: time.Minute,
	}), st
}

func moderationRequest(
	t *testing.T,
	handler http.Handler,
	method, path, token, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestCreatorModerationGatewayRequiresSession(t *testing.T) {
	handler, _ := newModerationHandler(t, "dev-token", "http://127.0.0.1:1")
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/summary", "", "",
	)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestCreatorModerationGatewaySummaryUsesSessionChannel(t *testing.T) {
	var gotPath, gotChannel, gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChannel = r.URL.Query().Get("channel_id")
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"channel_id":"ch_demo","open_cases":3}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/studio/moderation/summary?channel_id=ch_other",
		"dev-token",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/creator/summary" {
		t.Fatalf("unexpected upstream path: %s", gotPath)
	}
	if gotChannel != "ch_demo" {
		t.Fatalf("expected session channel ch_demo, got %q", gotChannel)
	}
	if gotAuthorization != "Bearer gateway-token" {
		t.Fatalf("unexpected upstream authorization: %s", gotAuthorization)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["open_cases"] != float64(3) {
		t.Fatalf("unexpected response: %v", payload)
	}
}

func TestCreatorModerationGatewaySendsOIDCClaims(t *testing.T) {
	var gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"channel_id":"ch_demo"}`))
	}))
	defer upstream.Close()

	client := moderation.NewClient(upstream.URL, "static-token", time.Second)
	client.WithClaims(
		"0123456789abcdef0123456789abcdef",
		"keel-core",
		"keel-moderation",
		time.Minute,
	)
	handler, _ := newModerationHandlerWithService(t, "dev-token", client)
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/summary", "dev-token", "",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotAuthorization == "" || gotAuthorization == "Bearer static-token" {
		t.Fatalf("expected signed claims, got %q", gotAuthorization)
	}

	parts := strings.Split(strings.TrimPrefix(gotAuthorization, "Bearer "), ".")
	if len(parts) != 3 {
		t.Fatalf("expected three JWT parts, got %d", len(parts))
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct {
		Issuer     string   `json:"iss"`
		Audience   string   `json:"aud"`
		Subject    string   `json:"sub"`
		Role       string   `json:"role"`
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Issuer != "keel-core" || claims.Audience != "keel-moderation" {
		t.Fatalf("unexpected issuer/audience: %+v", claims)
	}
	if claims.Subject != "usr_demo" || claims.Role != "creator" {
		t.Fatalf("unexpected subject/role: %+v", claims)
	}
	if len(claims.ChannelIDs) != 1 || claims.ChannelIDs[0] != "ch_demo" {
		t.Fatalf("unexpected channel claims: %+v", claims.ChannelIDs)
	}
}

func TestCreatorModerationGatewayListFilters(t *testing.T) {
	var gotStatus, gotLimit, gotOffset, gotChannel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStatus = r.URL.Query().Get("status")
		gotLimit = r.URL.Query().Get("limit")
		gotOffset = r.URL.Query().Get("offset")
		gotChannel = r.URL.Query().Get("channel_id")
		_, _ = w.Write([]byte(`{"cases":[],"total":0}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/studio/moderation/cases?status=open&limit=10&offset=2",
		"dev-token",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"status":     "open",
		"limit":      "10",
		"offset":     "2",
		"channel_id": "ch_demo",
	}
	got := map[string]string{
		"status":     gotStatus,
		"limit":      gotLimit,
		"offset":     gotOffset,
		"channel_id": gotChannel,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s: got %q want %q", key, got[key], value)
		}
	}
}

func TestCreatorModerationGatewayRejectsInvalidFilters(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	cases := []string{
		"/api/v1/studio/moderation/cases?status=bogus",
		"/api/v1/studio/moderation/cases?limit=0",
		"/api/v1/studio/moderation/cases?limit=101",
		"/api/v1/studio/moderation/cases?offset=-1",
		"/api/v1/studio/moderation/actions?status=pending",
		"/api/v1/studio/moderation/appeals?status=active",
		"/api/v1/studio/moderation/ledger/export?format=xml",
	}
	for _, path := range cases {
		rec := moderationRequest(t, handler, http.MethodGet, path, "dev-token", "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: expected 422, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("upstream should not be called for invalid filters, got %d calls", got)
	}
}

func TestCreatorModerationGatewayGetCasePath(t *testing.T) {
	var gotPath, gotChannel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChannel = r.URL.Query().Get("channel_id")
		_, _ = w.Write([]byte(`{"id":"case_001"}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/cases/case_001", "dev-token", "",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/creator/cases/case_001" {
		t.Fatalf("unexpected upstream path: %s", gotPath)
	}
	if gotChannel != "ch_demo" {
		t.Fatalf("unexpected channel: %s", gotChannel)
	}
}

func TestCreatorModerationGatewayFileAppeal(t *testing.T) {
	var gotPath, gotChannel string
	var gotBody map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChannel = r.URL.Query().Get("channel_id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"apl_001","status":"pending"}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/studio/moderation/actions/act_001/appeal",
		"dev-token",
		`{"grounds":"new_evidence","statement":"The location is a public landmark."}`,
	)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/actions/act_001/appeal" {
		t.Fatalf("unexpected upstream path: %s", gotPath)
	}
	if gotChannel != "ch_demo" {
		t.Fatalf("unexpected channel: %s", gotChannel)
	}
	if gotBody["grounds"] != "new_evidence" {
		t.Fatalf("unexpected grounds: %v", gotBody)
	}
	if gotBody["statement"] != "The location is a public landmark." {
		t.Fatalf("unexpected statement: %v", gotBody)
	}
}

func TestCreatorModerationGatewayRejectsInvalidAppeal(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	cases := []string{
		`{"grounds":"bogus","statement":"A statement."}`,
		`{"grounds":"other","statement":""}`,
		`{"grounds":"other","statement":"   "}`,
		`{"grounds":"other","statement":"` + strings.Repeat("x", 4001) + `"}`,
		`{"grounds":"other","statement":"bad\u0000value"}`,
		`{"grounds":"other","statement":"A statement.","extra":true}`,
	}
	for _, body := range cases {
		rec := moderationRequest(
			t,
			handler,
			http.MethodPost,
			"/api/v1/studio/moderation/actions/act_001/appeal",
			"dev-token",
			body,
		)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 422 or 400, got %d: %s", rec.Code, rec.Body.String())
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("upstream should not be called for invalid appeals, got %d calls", got)
	}
}

func TestCreatorModerationGatewayMapsUpstreamConflict(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"An appeal is already pending."}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/studio/moderation/actions/act_001/appeal",
		"dev-token",
		`{"grounds":"other","statement":"A statement."}`,
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["detail"] != "An appeal is already pending." {
		t.Fatalf("unexpected detail: %v", payload)
	}
}

func TestCreatorModerationGatewayMapsCredentialFailureToBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"A valid role token is required."}`))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/summary", "dev-token", "",
	)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreatorModerationGatewayMapsUnavailableToBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := upstream.URL
	upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", url)
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/summary", "dev-token", "",
	)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreatorModerationGatewayLedgerExportHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "csv" {
			t.Fatalf("unexpected format: %s", got)
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="creator-ledger.csv"`)
		_, _ = w.Write([]byte("seq,type\n1,demo.reset\n"))
	}))
	defer upstream.Close()

	handler, _ := newModerationHandler(t, "dev-token", upstream.URL)
	rec := moderationRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/studio/moderation/ledger/export?format=csv",
		"dev-token",
		"",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Fatalf("unexpected content type: %s", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="creator-ledger.csv"` {
		t.Fatalf("unexpected content disposition: %s", got)
	}
	if rec.Body.String() != "seq,type\n1,demo.reset\n" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestCreatorModerationGatewayReturnsServiceUnavailableWhenUnconfigured(t *testing.T) {
	handler, _ := newModerationHandler(t, "dev-token", "")
	rec := moderationRequest(
		t, handler, http.MethodGet, "/api/v1/studio/moderation/summary", "dev-token", "",
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
