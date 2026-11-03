package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type notificationPageBody struct {
	Items       []store.Notification `json:"items"`
	Limit       int                  `json:"limit"`
	Offset      int                  `json:"offset"`
	Total       int                  `json:"total"`
	HasMore     bool                 `json:"has_more"`
	UnreadCount int                  `json:"unread_count"`
}

type unreadNotificationBody struct {
	UnreadCount int `json:"unread_count"`
}

type markAllNotificationsBody struct {
	Updated     int64 `json:"updated"`
	UnreadCount int   `json:"unread_count"`
}

func notificationCSRF(t *testing.T, cookies []*http.Cookie) string {
	t.Helper()
	cookie := findCookie(cookies, "keeltube_csrf")
	if cookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	return cookie.Value
}

func postInternalNotification(
	t *testing.T,
	handler http.Handler,
	token string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal internal notification: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost, "/internal/v1/notifications", bytes.NewReader(encoded),
	)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	return rec
}

func TestNotificationsListAndMarkRead(t *testing.T) {
	handler, st := newHandlerWithStore(t, "")
	registered, cookies := registerAndVerify(t, handler, "notifications@example.com", testPassword(t))
	csrf := notificationCSRF(t, cookies)
	ctx := context.Background()
	now := time.Now().UTC()

	first, _, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID:   registered.User.ID,
		ChannelID: registered.User.ChannelID,
		Kind:      "comment.video",
		Title:     "New comment on your video",
		Body:      "Ada: a comment",
		Href:      "/watch/example",
		DedupeKey: "test:first:" + registered.User.ID,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create first notification: %v", err)
	}
	if _, _, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID:   registered.User.ID,
		Kind:      "upload.published",
		Title:     "Your video is published",
		CreatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("create second notification: %v", err)
	}

	rec := getWithCookies(t, handler, "/api/v1/me/notifications", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list notifications: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[notificationPageBody](t, rec)
	if page.Total != 2 || len(page.Items) != 2 || page.UnreadCount != 2 || page.HasMore {
		t.Fatalf("unexpected notification page: %+v", page)
	}
	if page.Items[0].Title != "Your video is published" {
		t.Fatalf("expected newest first: %+v", page.Items)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/notifications?unread=true", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list unread notifications: expected 200, got %d", rec.Code)
	}
	unreadPage := decode[notificationPageBody](t, rec)
	if unreadPage.Total != 2 || len(unreadPage.Items) != 2 {
		t.Fatalf("unexpected unread page: %+v", unreadPage)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/notifications/unread-count", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread count: expected 200, got %d", rec.Code)
	}
	if decoded := decode[unreadNotificationBody](t, rec); decoded.UnreadCount != 2 {
		t.Fatalf("unexpected unread count: %+v", decoded)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/me/notifications/"+first.ID+"/read", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	read := decode[store.Notification](t, rec)
	if !read.Read || read.ReadAt == nil {
		t.Fatalf("expected read notification: %+v", read)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/notifications?unread=true", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list unread after read: expected 200, got %d", rec.Code)
	}
	unreadPage = decode[notificationPageBody](t, rec)
	if unreadPage.Total != 1 || unreadPage.UnreadCount != 1 {
		t.Fatalf("unexpected unread page after read: %+v", unreadPage)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/me/notifications/read-all", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark all read: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if decoded := decode[markAllNotificationsBody](t, rec); decoded.Updated != 1 || decoded.UnreadCount != 0 {
		t.Fatalf("unexpected mark all result: %+v", decoded)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/notifications/unread-count", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread count after mark all: expected 200, got %d", rec.Code)
	}
	if decoded := decode[unreadNotificationBody](t, rec); decoded.UnreadCount != 0 {
		t.Fatalf("expected zero unread: %+v", decoded)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/me/notifications/ntf_missing/read", nil, cookies, csrf)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing notification: expected 404, got %d", rec.Code)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/notifications?unread=maybe", cookies)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid unread filter: expected 422, got %d", rec.Code)
	}
}

func TestNotificationInternalIngest(t *testing.T) {
	handler, st := newHandlerWithOptions(t, "", "internal-secret")

	rec := postInternalNotification(t, handler, "", map[string]any{
		"actor_id": "usr_demo", "kind": "appeal.received", "title": "Appeal received",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing internal token: expected 401, got %d", rec.Code)
	}
	rec = postInternalNotification(t, handler, "wrong", map[string]any{
		"actor_id": "usr_demo", "kind": "appeal.received", "title": "Appeal received",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong internal token: expected 401, got %d", rec.Code)
	}

	body := map[string]any{
		"actor_id":   "usr_demo",
		"channel_id": "ch_demo",
		"kind":       "appeal.received",
		"title":      "Appeal received",
		"body":       "A reviewer will decide by 8 November.",
		"href":       "/studio/moderation/appeals",
		"dedupe_key": "appeal:apl_1:received",
	}
	rec = postInternalNotification(t, handler, "internal-secret", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest notification: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decode[store.Notification](t, rec)
	if created.ID == "" || created.Read || created.Kind != "appeal.received" {
		t.Fatalf("unexpected ingested notification: %+v", created)
	}

	rec = postInternalNotification(t, handler, "internal-secret", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate ingest: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if duplicate := decode[store.Notification](t, rec); duplicate.ID != created.ID {
		t.Fatalf("expected the duplicate to return the existing notification: %+v", duplicate)
	}

	rec = postInternalNotification(t, handler, "internal-secret", map[string]any{
		"actor_id": "usr_missing", "kind": "appeal.received", "title": "Appeal received",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown actor: expected 422, got %d", rec.Code)
	}

	rec = postInternalNotification(t, handler, "internal-secret", map[string]any{
		"actor_id": "usr_demo", "channel_id": "ch_keel_lab",
		"kind": "appeal.received", "title": "Appeal received",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("channel ownership mismatch: expected 422, got %d", rec.Code)
	}

	notifications, total, err := st.ListNotifications(context.Background(), "usr_demo", false, 20, 0)
	if err != nil {
		t.Fatalf("list ingested notifications: %v", err)
	}
	if total != 1 || len(notifications) != 1 || notifications[0].ID != created.ID {
		t.Fatalf("unexpected ingested notification rows: total=%d %+v", total, notifications)
	}

	disabled, _ := newHandlerWithStore(t, "")
	rec = postInternalNotification(t, disabled, "internal-secret", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled internal endpoint: expected 404, got %d", rec.Code)
	}
}

func TestCommentCreatesNotifications(t *testing.T) {
	handler, st := newHandlerWithStore(t, "")
	owner, ownerCookies := registerAndVerify(t, handler, "owner@example.com", testPassword(t))
	commenter, commenterCookies := registerAndVerify(t, handler, "commenter@example.com", testPassword(t))
	ownerCSRF := notificationCSRF(t, ownerCookies)
	commenterCSRF := notificationCSRF(t, commenterCookies)
	ctx := context.Background()
	now := time.Now().UTC()

	video := store.Video{
		ID:              "vid_notify_001",
		PublicID:        "nOtIfY001xx",
		ChannelID:       owner.User.ChannelID,
		Title:           "Notification test video",
		Description:     "Created by the notification test.",
		Status:          "published",
		Visibility:      "public",
		ContentHash:     "sha256:" + strings.Repeat("a", 64),
		DurationSeconds: 10,
		Playback:        store.Playback{MP4URL: "https://example.com/video.mp4"},
		License:         "standard",
		Format:          "video",
		Category:        "Documentaries",
		Tags:            []string{"test"},
		CreatedAt:       now,
		PublishedAt:     &now,
		UpdatedAt:       now,
	}
	if err := st.UpsertVideo(ctx, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	rec := doJSON(t, handler, http.MethodPost, "/api/v1/videos/"+video.ID+"/comments", map[string]string{
		"body": "First comment.",
	}, commenterCookies, commenterCSRF)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	comment := decode[store.Comment](t, rec)

	notifications, total, err := st.ListNotifications(ctx, owner.User.ID, false, 20, 0)
	if err != nil {
		t.Fatalf("list owner notifications: %v", err)
	}
	if total != 1 || len(notifications) != 1 {
		t.Fatalf("expected one owner notification: total=%d %+v", total, notifications)
	}
	if notifications[0].Kind != "comment.video" || notifications[0].Href != "/watch/"+video.PublicID {
		t.Fatalf("unexpected comment notification: %+v", notifications[0])
	}
	if notifications[0].Body != "Ada Lovelace: First comment." {
		t.Fatalf("unexpected notification body: %+v", notifications[0])
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/"+video.ID+"/comments", map[string]string{
		"body":      "A reply from the owner.",
		"parent_id": comment.ID,
	}, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create owner reply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	notifications, total, err = st.ListNotifications(ctx, commenter.User.ID, false, 20, 0)
	if err != nil {
		t.Fatalf("list commenter notifications: %v", err)
	}
	if total != 1 || len(notifications) != 1 {
		t.Fatalf("expected one reply notification: total=%d %+v", total, notifications)
	}
	if notifications[0].Kind != "comment.reply" || notifications[0].Title != "New reply to your comment" {
		t.Fatalf("unexpected reply notification: %+v", notifications[0])
	}

	ownerNotifications, total, err := st.ListNotifications(ctx, owner.User.ID, false, 20, 0)
	if err != nil {
		t.Fatalf("relist owner notifications: %v", err)
	}
	if total != 1 || len(ownerNotifications) != 1 {
		t.Fatalf("owner should not be notified of their own reply: total=%d %+v", total, ownerNotifications)
	}
}
