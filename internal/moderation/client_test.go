package moderation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testIdentity = Identity{ActorID: "usr_demo", Role: "creator"}

func TestClientSummaryUsesCreatorScopeAndCredential(t *testing.T) {
	var gotPath, gotChannel, gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChannel = r.URL.Query().Get("channel_id")
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"channel_id":"ch_demo","open_cases":2}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "gateway-token", time.Second)
	response, err := client.Summary(context.Background(), testIdentity, "ch_demo")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if gotPath != "/v1/creator/summary" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotChannel != "ch_demo" {
		t.Fatalf("unexpected channel: %s", gotChannel)
	}
	if gotAuthorization != "Bearer gateway-token" {
		t.Fatalf("unexpected authorization: %s", gotAuthorization)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["open_cases"] != float64(2) {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestClientListCasesPassesFilters(t *testing.T) {
	var gotQuery map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = map[string]string{
			"status":     r.URL.Query().Get("status"),
			"video_id":   r.URL.Query().Get("video_id"),
			"limit":      r.URL.Query().Get("limit"),
			"offset":     r.URL.Query().Get("offset"),
			"channel_id": r.URL.Query().Get("channel_id"),
		}
		_, _ = w.Write([]byte(`{"cases":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	if _, err := client.ListCases(context.Background(), testIdentity, "ch_maravale", "open", "vid_demo_001", 25, 5); err != nil {
		t.Fatalf("list cases: %v", err)
	}
	want := map[string]string{
		"status":     "open",
		"video_id":   "vid_demo_001",
		"limit":      "25",
		"offset":     "5",
		"channel_id": "ch_maravale",
	}
	for key, value := range want {
		if gotQuery[key] != value {
			t.Fatalf("%s: got %q want %q", key, gotQuery[key], value)
		}
	}
}

func TestClientFileAppealSendsBodyAndScope(t *testing.T) {
	var gotPath, gotChannel string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChannel = r.URL.Query().Get("channel_id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"apl_1","status":"pending"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "gateway-token", time.Second)
	response, err := client.FileAppeal(
		context.Background(), testIdentity, "ch_demo", "act_1", "new_evidence", "The landmark is public.",
	)
	if err != nil {
		t.Fatalf("file appeal: %v", err)
	}
	if gotPath != "/v1/actions/act_1/appeal" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotChannel != "ch_demo" {
		t.Fatalf("unexpected channel: %s", gotChannel)
	}
	if gotBody["grounds"] != "new_evidence" || gotBody["statement"] != "The landmark is public." {
		t.Fatalf("unexpected body: %v", gotBody)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
}

func TestClientReturnsTypedUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"An appeal is already pending."}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	_, err := client.FileAppeal(context.Background(), testIdentity, "ch_demo", "act_1", "other", "Statement")
	var upstream *Error
	if !errors.As(err, &upstream) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if upstream.StatusCode != http.StatusConflict {
		t.Fatalf("unexpected status: %d", upstream.StatusCode)
	}
	if upstream.Detail != "An appeal is already pending." {
		t.Fatalf("unexpected detail: %q", upstream.Detail)
	}
}

func TestClientExportForwardsContentHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "csv" {
			t.Fatalf("unexpected format: %s", got)
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="creator-ledger.csv"`)
		_, _ = w.Write([]byte("seq,type\n1,demo.reset\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	response, err := client.ExportLedger(context.Background(), testIdentity, "ch_demo", "csv")
	if err != nil {
		t.Fatalf("export ledger: %v", err)
	}
	if response.Header.Get("Content-Type") != "text/csv" {
		t.Fatalf("unexpected content type: %s", response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Content-Disposition") != `attachment; filename="creator-ledger.csv"` {
		t.Fatalf("unexpected content disposition: %s", response.Header.Get("Content-Disposition"))
	}
}

func TestClientDisabledWithoutBaseURL(t *testing.T) {
	client := NewClient("", "token", time.Second)
	if client.Enabled() {
		t.Fatal("expected disabled client")
	}
	if _, err := client.Summary(context.Background(), testIdentity, "ch_demo"); err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestClientWithClaimsSendsSignedOIDCClaims(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"channel_id":"ch_maravale"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "static-token", time.Second)
	client.WithClaims(secret, "keel-core", "keel-moderation", time.Minute)
	if _, err := client.Summary(
		context.Background(),
		Identity{ActorID: "usr_maravale", Role: "creator"},
		"ch_maravale",
	); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if gotAuthorization == "Bearer static-token" || gotAuthorization == "" {
		t.Fatalf("expected signed claims, got %q", gotAuthorization)
	}

	token := strings.TrimPrefix(gotAuthorization, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected three JWT parts, got %d", len(parts))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Fatalf("unexpected header: %v", header)
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		Issuer     string   `json:"iss"`
		Audience   string   `json:"aud"`
		Subject    string   `json:"sub"`
		Role       string   `json:"role"`
		ChannelIDs []string `json:"channel_ids"`
		IssuedAt   int64    `json:"iat"`
		ExpiresAt  int64    `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Issuer != "keel-core" || claims.Audience != "keel-moderation" {
		t.Fatalf("unexpected issuer/audience: %+v", claims)
	}
	if claims.Subject != "usr_maravale" || claims.Role != "creator" {
		t.Fatalf("unexpected subject/role: %+v", claims)
	}
	if len(claims.ChannelIDs) != 1 || claims.ChannelIDs[0] != "ch_maravale" {
		t.Fatalf("unexpected channel claims: %+v", claims.ChannelIDs)
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		t.Fatalf("expected future expiry: %+v", claims)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)); expected != parts[2] {
		t.Fatalf("signature mismatch")
	}
}

func TestClientClaimsRequireSubjectAndChannel(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "", time.Second)
	client.WithClaims("0123456789abcdef0123456789abcdef", "keel-core", "keel-moderation", time.Minute)

	if _, err := client.Summary(context.Background(), Identity{Role: "creator"}, "ch_demo"); err == nil {
		t.Fatal("expected missing subject error")
	}
	if _, err := client.Summary(
		context.Background(), Identity{ActorID: "usr_demo", Role: "creator"}, "",
	); err == nil {
		t.Fatal("expected missing channel error")
	}
}

func TestClientTimeoutIsDeadlineExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 20*time.Millisecond)
	if _, err := client.Summary(context.Background(), testIdentity, "ch_demo"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestClientLimitsResponseSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", maxResponseBytes+1)), maxResponseBytes+1)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	if _, err := client.Summary(context.Background(), testIdentity, "ch_demo"); err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestClientReportUsesSignalToken(t *testing.T) {
	var gotPath, gotAuthorization string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"case":{"id":"case_1"},"signal":{"id":"sig_1"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "gateway-token", time.Second).WithSignalToken("signal-token")
	response, err := client.Report(
		context.Background(), "ch_demo", "video", "vid_demo_001", "spam", "Looks automated.", "usr_viewer",
	)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if gotPath != "/v1/signals" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotAuthorization != "Bearer signal-token" {
		t.Fatalf("unexpected authorization: %s", gotAuthorization)
	}
	if gotBody["channel_id"] != "ch_demo" || gotBody["subject_id"] != "vid_demo_001" {
		t.Fatalf("unexpected body: %v", gotBody)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
}
