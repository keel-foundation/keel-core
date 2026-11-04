package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/moderation"
)

func TestReportIngestsVideoSignalWithServiceToken(t *testing.T) {
	var gotPath, gotAuthorization string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"case":{"id":"case_report"},"signal":{"id":"sig_report"}}`))
	}))
	defer upstream.Close()

	client := moderation.NewClient(upstream.URL, "gateway-token", time.Second).
		WithSignalToken("ingest-token")
	handler, _ := newModerationHandlerWithService(t, "dev-token", client)

	body := `{"subject_type":"video","subject_id":"ksKCMmvOxwQ","reason":"copyright","details":"The clip contains unlicensed footage."}`
	rec := moderationRequest(t, handler, http.MethodPost, "/api/v1/reports", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/signals" {
		t.Fatalf("expected /v1/signals, got %q", gotPath)
	}
	if gotAuthorization != "Bearer ingest-token" {
		t.Fatalf("expected ingest token, got %q", gotAuthorization)
	}
	if gotBody["subject_id"] != "vid_demo_001" {
		t.Fatalf("expected internal video id, got %#v", gotBody["subject_id"])
	}
	if gotBody["channel_id"] != "ch_demo" {
		t.Fatalf("expected channel ch_demo, got %#v", gotBody["channel_id"])
	}
	if gotBody["reason"] != "copyright" {
		t.Fatalf("expected copyright reason, got %#v", gotBody["reason"])
	}
}

func TestReportRejectsInvalidReason(t *testing.T) {
	handler, _ := newModerationHandler(t, "dev-token", "http://127.0.0.1:1")
	body := `{"subject_type":"video","subject_id":"ksKCMmvOxwQ","reason":"bad","details":"reason"}`
	rec := moderationRequest(t, handler, http.MethodPost, "/api/v1/reports", "", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReportRequiresModerationService(t *testing.T) {
	handler, _ := newModerationHandler(t, "dev-token", "")
	body := `{"subject_type":"video","subject_id":"ksKCMmvOxwQ","reason":"spam","details":"details"}`
	rec := moderationRequest(t, handler, http.MethodPost, "/api/v1/reports", "", body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
