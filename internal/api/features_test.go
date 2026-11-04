package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/store"
)

func apiRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
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

func TestStudioChannelProfileUpdate(t *testing.T) {
	token, _ := auth.NewToken()
	handler, _ := newHandlerWithStore(t, token)
	body := `{"name":"Mara Vale Studio","description":"Updated profile","banner_url":"https://example.org/banner.jpg","links":[{"label":"Site","url":"https://example.org"}]}`
	rec := apiRequest(t, handler, http.MethodPatch, "/api/v1/studio/channel", token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update channel: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	channel := decode[store.Channel](t, rec)
	if channel.Name != "Mara Vale Studio" || channel.BannerURL != "https://example.org/banner.jpg" || len(channel.Links) != 1 {
		t.Fatalf("unexpected channel: %+v", channel)
	}
}

func TestVideoArchiveRestoreScheduleAndStudioSearch(t *testing.T) {
	token, _ := auth.NewToken()
	handler, _ := newHandlerWithStore(t, token)

	archived := apiRequest(t, handler, http.MethodDelete, "/api/v1/studio/videos/vid_draft_001", token, "")
	if archived.Code != http.StatusOK {
		t.Fatalf("archive: expected 200, got %d: %s", archived.Code, archived.Body.String())
	}
	archivedVideo := decode[store.Video](t, archived)
	if archivedVideo.Status != "archived" || archivedVideo.ArchivedAt == nil {
		t.Fatalf("unexpected archived video: %+v", archivedVideo)
	}

	restored := apiRequest(t, handler, http.MethodPost, "/api/v1/studio/videos/vid_draft_001/restore", token, "")
	if restored.Code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", restored.Code, restored.Body.String())
	}
	if decode[store.Video](t, restored).Status != "draft" {
		t.Fatal("expected restored draft")
	}

	scheduledAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	scheduled := apiRequest(t, handler, http.MethodPost, "/api/v1/studio/videos/vid_draft_001/schedule", token, `{"scheduled_at":"`+scheduledAt+`"}`)
	if scheduled.Code != http.StatusOK {
		t.Fatalf("schedule: expected 200, got %d: %s", scheduled.Code, scheduled.Body.String())
	}
	if decode[store.Video](t, scheduled).ScheduledAt == nil {
		t.Fatal("expected scheduled_at")
	}
	cleared := apiRequest(t, handler, http.MethodDelete, "/api/v1/studio/videos/vid_draft_001/schedule", token, "")
	if cleared.Code != http.StatusOK || decode[store.Video](t, cleared).ScheduledAt != nil {
		t.Fatalf("clear schedule failed: %d %s", cleared.Code, cleared.Body.String())
	}

	search := apiRequest(t, handler, http.MethodGet, "/api/v1/studio/videos?q=lighthouse", token, "")
	if search.Code != http.StatusOK {
		t.Fatalf("studio search: expected 200, got %d", search.Code)
	}
	page := decode[struct {
		Items []store.Video `json:"items"`
		Total int           `json:"total"`
	}](t, search)
	if page.Total == 0 || page.Items[0].Title != "The Last Lighthouse" {
		t.Fatalf("unexpected studio search: %+v", page)
	}
}

func TestCommentPinAndRemove(t *testing.T) {
	token, _ := auth.NewToken()
	handler, st := newHandlerWithStore(t, token)
	commentID, _ := store.NewID("cmt_")
	now := time.Now().UTC()
	if _, err := st.CreateComment(context.Background(), store.Comment{
		ID: commentID, VideoID: "vid_demo_001", ActorID: "usr_demo",
		Body: "Pin me.", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	pinned := apiRequest(t, handler, http.MethodPost, "/api/v1/studio/comments/"+commentID+"/pin", token, "")
	if pinned.Code != http.StatusOK || !decode[store.Comment](t, pinned).Pinned {
		t.Fatalf("pin failed: %d %s", pinned.Code, pinned.Body.String())
	}
	unpinned := apiRequest(t, handler, http.MethodDelete, "/api/v1/studio/comments/"+commentID+"/pin", token, "")
	if unpinned.Code != http.StatusOK || decode[store.Comment](t, unpinned).Pinned {
		t.Fatalf("unpin failed: %d %s", unpinned.Code, unpinned.Body.String())
	}
	removed := apiRequest(t, handler, http.MethodDelete, "/api/v1/studio/comments/"+commentID, token, "")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove failed: %d %s", removed.Code, removed.Body.String())
	}
}

func TestPlaylistsLifecycle(t *testing.T) {
	token, _ := auth.NewToken()
	handler, _ := newHandlerWithStore(t, token)
	created := apiRequest(t, handler, http.MethodPost, "/api/v1/me/playlists", token, `{"title":"Watchlist","description":"Later","visibility":"private","kind":"playlist"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create playlist: %d %s", created.Code, created.Body.String())
	}
	playlist := decode[store.Playlist](t, created)
	added := apiRequest(t, handler, http.MethodPost, "/api/v1/playlists/"+playlist.ID+"/videos", token, `{"video_id":"ksKCMmvOxwQ"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("add item: %d %s", added.Code, added.Body.String())
	}
	items := apiRequest(t, handler, http.MethodGet, "/api/v1/playlists/"+playlist.ID+"/videos", token, "")
	if items.Code != http.StatusOK {
		t.Fatalf("list items: %d", items.Code)
	}
	if total := decode[struct {
		Total int `json:"total"`
	}](t, items).Total; total != 1 {
		t.Fatalf("expected one item, got %d", total)
	}
	deleted := apiRequest(t, handler, http.MethodDelete, "/api/v1/playlists/"+playlist.ID, token, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete playlist: %d", deleted.Code)
	}
}

func TestReportStatusAndTrustRecord(t *testing.T) {
	var gotTrust string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/public/videos/vid_demo_001/record" {
			gotTrust = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"video_id":"vid_demo_001","cases":[],"actions":[],"appeals":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"case":{"id":"case_report"},"signal":{"id":"sig_report"}}`))
	}))
	defer upstream.Close()
	client := moderation.NewClient(upstream.URL, "gateway-token", time.Second).WithSignalToken("ingest-token")
	handler, _ := newModerationHandlerWithService(t, "dev-token", client)

	rec := apiRequest(t, handler, http.MethodPost, "/api/v1/reports", "dev-token",
		`{"subject_type":"video","subject_id":"ksKCMmvOxwQ","reason":"copyright","details":"Unlicensed footage."}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	reportID, _ := payload["report_id"].(string)
	if reportID == "" || payload["status"] != "case_opened" {
		t.Fatalf("unexpected report payload: %v", payload)
	}
	status := apiRequest(t, handler, http.MethodGet, "/api/v1/reports/"+reportID, "dev-token", "")
	if status.Code != http.StatusOK {
		t.Fatalf("report status: %d %s", status.Code, status.Body.String())
	}
	if decode[store.Report](t, status).ModerationCaseID != "case_report" {
		t.Fatal("expected moderation case id")
	}

	trust := apiRequest(t, handler, http.MethodGet, "/api/v1/videos/ksKCMmvOxwQ/trust", "", "")
	if trust.Code != http.StatusOK || gotTrust == "" {
		t.Fatalf("trust record: %d %s", trust.Code, trust.Body.String())
	}
}

func TestBulkChannelsSearchFilterAndSuggestions(t *testing.T) {
	handler := newHandler(t, "")
	channels := apiRequest(t, handler, http.MethodGet, "/api/v1/channels?ids=ch_demo,ch_keel_lab", "", "")
	if channels.Code != http.StatusOK {
		t.Fatalf("channels: %d", channels.Code)
	}
	if total := decode[struct {
		Total int `json:"total"`
	}](t, channels).Total; total != 2 {
		t.Fatalf("expected two channels, got %d", total)
	}
	search := apiRequest(t, handler, http.MethodGet, "/api/v1/search?q=keel&channel_id=ch_keel_lab", "", "")
	if search.Code != http.StatusOK {
		t.Fatalf("search: %d", search.Code)
	}
	searchPayload := decode[struct {
		Videos struct {
			Items []store.Video `json:"items"`
		} `json:"videos"`
	}](t, search)
	for _, video := range searchPayload.Videos.Items {
		if video.ChannelID != "ch_keel_lab" {
			t.Fatalf("search channel filter leaked %s", video.ChannelID)
		}
	}
	suggestions := apiRequest(t, handler, http.MethodGet, "/api/v1/search/suggestions?q=keel", "", "")
	if suggestions.Code != http.StatusOK {
		t.Fatalf("suggestions: %d", suggestions.Code)
	}
	semantic := apiRequest(t, handler, http.MethodGet, "/api/v1/search/semantic?q=lighthouse", "", "")
	if semantic.Code != http.StatusOK {
		t.Fatalf("semantic search: %d %s", semantic.Code, semantic.Body.String())
	}
}

func TestFinancePayoutEventCreatesNotification(t *testing.T) {
	token, _ := auth.NewToken()
	handler, _ := newHandlerWithOptions(t, token, "internal-token")
	event := `{"channel_id":"ch_demo","payout_id":"pay_test","period":"December 2026","amount":100,"status":"paid"}`
	rec := apiRequest(t, handler, http.MethodPost, "/internal/v1/finance/payouts", "internal-token", event)
	if rec.Code != http.StatusOK {
		t.Fatalf("finance event: %d %s", rec.Code, rec.Body.String())
	}
	notifications := apiRequest(t, handler, http.MethodGet, "/api/v1/me/notifications", token, "")
	if notifications.Code != http.StatusOK {
		t.Fatalf("notifications: %d", notifications.Code)
	}
	payload := decode[struct {
		Items []store.Notification `json:"items"`
	}](t, notifications)
	found := false
	for _, notification := range payload.Items {
		if notification.Kind == "finance.payout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected finance payout notification: %+v", payload.Items)
	}
}
