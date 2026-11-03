package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keel-foundation/keel-core/internal/store"
)

type subscriptionBody struct {
	ChannelID   string `json:"channel_id"`
	Subscribed  bool   `json:"subscribed"`
	Subscribers int64  `json:"subscribers"`
}

type reactionBody struct {
	VideoID string  `json:"video_id"`
	Kind    *string `json:"kind"`
	Likes   int64   `json:"likes"`
}

type saveBody struct {
	VideoID string `json:"video_id"`
	Saved   bool   `json:"saved"`
}

type viewBody struct {
	VideoID string `json:"video_id"`
	Views   int64  `json:"views"`
}

type commentLikeBody struct {
	CommentID string `json:"comment_id"`
	Liked     bool   `json:"liked"`
	Likes     int64  `json:"likes"`
}

type commentPage struct {
	Items   []store.Comment `json:"items"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	Total   int             `json:"total"`
	HasMore bool            `json:"has_more"`
}

func doJSON(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body any,
	cookies []*http.Cookie,
	csrf string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	return rec
}

func TestEngagementSubscriptionAndLibrary(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "engagement@example.com", testPassword(t))
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	csrf := csrfCookie.Value

	rec := getWithCookies(t, handler, "/api/v1/channels/ch_keel_lab/subscription", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get subscription: expected 200, got %d", rec.Code)
	}
	initial := decode[subscriptionBody](t, rec)
	if initial.Subscribed || initial.Subscribers != 840_000 {
		t.Fatalf("unexpected initial subscription: %+v", initial)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/channels/ch_keel_lab/subscription", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	subscribed := decode[subscriptionBody](t, rec)
	if !subscribed.Subscribed || subscribed.Subscribers != initial.Subscribers+1 {
		t.Fatalf("unexpected subscribe result: %+v", subscribed)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/subscriptions", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list subscriptions: expected 200, got %d", rec.Code)
	}
	channels := decode[catalogChannelPage](t, rec)
	if channels.Total != 1 || len(channels.Items) != 1 || channels.Items[0].ID != "ch_keel_lab" {
		t.Fatalf("unexpected subscriptions: %+v", channels)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/feed", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscription feed: expected 200, got %d", rec.Code)
	}
	feed := decode[catalogVideoPage](t, rec)
	if feed.Total != 2 || len(feed.Items) != 2 {
		t.Fatalf("unexpected subscription feed: %+v", feed)
	}
	for _, video := range feed.Items {
		if video.ChannelID != "ch_keel_lab" {
			t.Fatalf("unexpected subscription feed video: %+v", video)
		}
	}

	rec = getWithCookies(t, handler, "/api/v1/channels/ch_keel_lab", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel: expected 200, got %d", rec.Code)
	}
	channel := decode[store.Channel](t, rec)
	if channel.Subscribers != initial.Subscribers+1 {
		t.Fatalf("unexpected channel subscribers: %+v", channel)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/channels/ch_keel_lab/subscription", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat subscribe: expected 200, got %d", rec.Code)
	}
	repeated := decode[subscriptionBody](t, rec)
	if repeated.Subscribers != subscribed.Subscribers {
		t.Fatalf("repeat subscribe changed the count: %+v", repeated)
	}

	rec = doJSON(t, handler, http.MethodDelete, "/api/v1/channels/ch_keel_lab/subscription", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("unsubscribe: expected 200, got %d", rec.Code)
	}
	unsubscribed := decode[subscriptionBody](t, rec)
	if unsubscribed.Subscribed || unsubscribed.Subscribers != initial.Subscribers {
		t.Fatalf("unexpected unsubscribe result: %+v", unsubscribed)
	}

	rec = getWithCookies(t, handler, "/api/v1/me/feed", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty subscription feed: expected 200, got %d", rec.Code)
	}
	emptyFeed := decode[catalogVideoPage](t, rec)
	if emptyFeed.Total != 0 || len(emptyFeed.Items) != 0 {
		t.Fatalf("expected an empty subscription feed: %+v", emptyFeed)
	}

	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "like",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("like: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	liked := decode[reactionBody](t, rec)
	if liked.Kind == nil || *liked.Kind != "like" || liked.Likes != 42_101 {
		t.Fatalf("unexpected like: %+v", liked)
	}

	rec = getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("public video after like: expected 200, got %d", rec.Code)
	}
	publicVideo := decode[store.Video](t, rec)
	if publicVideo.Likes != 42_101 {
		t.Fatalf("unexpected public likes: %+v", publicVideo)
	}

	rec = getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL/reaction", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get reaction: expected 200, got %d", rec.Code)
	}
	storedReaction := decode[reactionBody](t, rec)
	if storedReaction.Kind == nil || *storedReaction.Kind != "like" {
		t.Fatalf("unexpected stored reaction: %+v", storedReaction)
	}

	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "dislike",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("dislike: expected 200, got %d", rec.Code)
	}
	disliked := decode[reactionBody](t, rec)
	if disliked.Kind == nil || *disliked.Kind != "dislike" || disliked.Likes != 42_100 {
		t.Fatalf("unexpected dislike: %+v", disliked)
	}

	rec = doJSON(t, handler, http.MethodDelete, "/api/v1/videos/aB3dEf9hJkL/reaction", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear reaction: expected 200, got %d", rec.Code)
	}
	cleared := decode[reactionBody](t, rec)
	if cleared.Kind != nil || cleared.Likes != 42_100 {
		t.Fatalf("unexpected cleared reaction: %+v", cleared)
	}

	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/save", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: expected 200, got %d", rec.Code)
	}
	saved := decode[saveBody](t, rec)
	if !saved.Saved {
		t.Fatalf("unexpected save: %+v", saved)
	}
	rec = getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL/save", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get save: expected 200, got %d", rec.Code)
	}
	savedState := decode[saveBody](t, rec)
	if !savedState.Saved {
		t.Fatalf("unexpected saved state: %+v", savedState)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/aB3dEf9hJkL/view", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("view: expected 200, got %d", rec.Code)
	}
	viewed := decode[viewBody](t, rec)
	if viewed.Views != 684_001 {
		t.Fatalf("unexpected views: %+v", viewed)
	}
	rec = getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("public video after view: expected 200, got %d", rec.Code)
	}
	publicVideo = decode[store.Video](t, rec)
	if publicVideo.Views != 684_001 {
		t.Fatalf("unexpected public views: %+v", publicVideo)
	}

	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "like",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("relike: expected 200, got %d", rec.Code)
	}

	for _, tab := range []string{"liked", "saved", "history"} {
		rec = getWithCookies(t, handler, "/api/v1/me/library?tab="+tab, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("library %s: expected 200, got %d", tab, rec.Code)
		}
		page := decode[catalogVideoPage](t, rec)
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "vid_even_keel" {
			t.Fatalf("unexpected %s library: %+v", tab, page)
		}
	}

	rec = doJSON(t, handler, http.MethodDelete, "/api/v1/me/history", nil, cookies, csrf)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear history: expected 204, got %d", rec.Code)
	}
	rec = getWithCookies(t, handler, "/api/v1/me/library?tab=history", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("library history after clear: expected 200, got %d", rec.Code)
	}
	clearedHistory := decode[catalogVideoPage](t, rec)
	if clearedHistory.Total != 0 || len(clearedHistory.Items) != 0 {
		t.Fatalf("expected an empty history: %+v", clearedHistory)
	}

	rec = doJSON(t, handler, http.MethodDelete, "/api/v1/videos/aB3dEf9hJkL/save", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("unsave: expected 200, got %d", rec.Code)
	}
	rec = getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL/save", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get save after unsave: expected 200, got %d", rec.Code)
	}
	unsavedState := decode[saveBody](t, rec)
	if unsavedState.Saved {
		t.Fatalf("unexpected unsaved state: %+v", unsavedState)
	}
	rec = getWithCookies(t, handler, "/api/v1/me/library?tab=saved", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("library saved: expected 200, got %d", rec.Code)
	}
	empty := decode[catalogVideoPage](t, rec)
	if empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("expected an empty saved library: %+v", empty)
	}
}

func TestEngagementComments(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "comments@example.com", testPassword(t))
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	csrf := csrfCookie.Value

	rec := doJSON(t, handler, http.MethodPost, "/api/v1/videos/ksKCMmvOxwQ/comments", map[string]string{
		"body": "A specific comment.",
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	comment := decode[store.Comment](t, rec)
	if comment.ID == "" || comment.Author != "Ada Lovelace" || comment.Handle != "@adalovelace" {
		t.Fatalf("unexpected comment: %+v", comment)
	}

	rec = getWithCookies(t, handler, "/api/v1/videos/ksKCMmvOxwQ", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("public video after comment: expected 200, got %d", rec.Code)
	}
	commentedVideo := decode[store.Video](t, rec)
	if commentedVideo.CommentsCount != 1_285 {
		t.Fatalf("unexpected comments count: %+v", commentedVideo)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/ksKCMmvOxwQ/comments", map[string]string{
		"body":      "A reply.",
		"parent_id": comment.ID,
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reply := decode[store.Comment](t, rec)
	if reply.ParentID == nil || *reply.ParentID != comment.ID {
		t.Fatalf("unexpected reply: %+v", reply)
	}

	rec = getWithCookies(t, handler, "/api/v1/videos/ksKCMmvOxwQ", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("public video after reply: expected 200, got %d", rec.Code)
	}
	commentedVideo = decode[store.Video](t, rec)
	if commentedVideo.CommentsCount != 1_286 {
		t.Fatalf("unexpected comments count after reply: %+v", commentedVideo)
	}

	commentLikes := func() int64 {
		rec := getWithCookies(t, handler, "/api/v1/videos/ksKCMmvOxwQ/comments", cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("list comments for likes: expected 200, got %d", rec.Code)
		}
		page := decode[commentPage](t, rec)
		for _, item := range page.Items {
			if item.ID == comment.ID {
				return item.Likes
			}
		}
		t.Fatalf("comment %s not found in the list", comment.ID)
		return -1
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/ksKCMmvOxwQ/comments", map[string]string{
		"body":      "Too deep.",
		"parent_id": reply.ID,
	}, cookies, csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("third level: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = getWithCookies(t, handler, "/api/v1/videos/ksKCMmvOxwQ/comments", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: expected 200, got %d", rec.Code)
	}
	page := decode[commentPage](t, rec)
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("unexpected comments: %+v", page)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/comments/"+comment.ID+"/like", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("like comment: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	liked := decode[commentLikeBody](t, rec)
	if !liked.Liked || liked.Likes != 1 {
		t.Fatalf("unexpected comment like: %+v", liked)
	}
	if got := commentLikes(); got != 1 {
		t.Fatalf("comment list did not reflect the like: %d", got)
	}

	rec = doJSON(t, handler, http.MethodDelete, "/api/v1/comments/"+comment.ID+"/like", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("unlike comment: expected 200, got %d", rec.Code)
	}
	unliked := decode[commentLikeBody](t, rec)
	if unliked.Liked || unliked.Likes != 0 {
		t.Fatalf("unexpected comment unlike: %+v", unliked)
	}
	if got := commentLikes(); got != 0 {
		t.Fatalf("comment list did not reflect the unlike: %d", got)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/ksKCMmvOxwQ/comments", map[string]string{
		"body": "",
	}, cookies, csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty comment: expected 422, got %d", rec.Code)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/videos/nO8pQr1sTuV/comments", map[string]string{
		"body": "A private comment.",
	}, cookies, csrf)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private video comment: expected 404, got %d", rec.Code)
	}
}

func TestEngagementRequiresAuth(t *testing.T) {
	handler := newHandler(t, "dev-token")
	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/me/subscriptions", nil},
		{http.MethodGet, "/api/v1/me/feed", nil},
		{http.MethodGet, "/api/v1/me/library", nil},
		{http.MethodDelete, "/api/v1/me/history", nil},
		{http.MethodPost, "/api/v1/channels/ch_keel_lab/subscription", nil},
		{http.MethodDelete, "/api/v1/channels/ch_keel_lab/subscription", nil},
		{http.MethodGet, "/api/v1/videos/aB3dEf9hJkL/reaction", nil},
		{http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{"kind": "like"}},
		{http.MethodDelete, "/api/v1/videos/aB3dEf9hJkL/reaction", nil},
		{http.MethodGet, "/api/v1/videos/aB3dEf9hJkL/save", nil},
		{http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/save", nil},
		{http.MethodDelete, "/api/v1/videos/aB3dEf9hJkL/save", nil},
		{http.MethodPost, "/api/v1/videos/aB3dEf9hJkL/view", nil},
		{http.MethodPost, "/api/v1/videos/ksKCMmvOxwQ/comments", map[string]string{"body": "hello"}},
		{http.MethodPost, "/api/v1/comments/cmt_missing/like", nil},
		{http.MethodDelete, "/api/v1/comments/cmt_missing/like", nil},
	}
	for _, tc := range cases {
		rec := doJSON(t, handler, tc.method, tc.path, tc.body, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %d: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestEngagementCSRF(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "csrf@example.com", testPassword(t))

	rec := doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "like",
	}, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: expected 403, got %d", rec.Code)
	}

	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "like",
	}, cookies, csrfCookie.Value)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid CSRF: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEngagementCORSAllowsEngagementMethods(t *testing.T) {
	handler := newHandler(t, "")
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/videos/aB3dEf9hJkL/reaction", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "PUT") || !strings.Contains(methods, "DELETE") {
		t.Fatalf("expected PUT and DELETE in allowed methods, got %q", methods)
	}
}

func TestEngagementValidationAndAccess(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "validation@example.com", testPassword(t))
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	csrf := csrfCookie.Value

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"invalid reaction", http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{"kind": "love"}, http.StatusUnprocessableEntity},
		{"invalid library tab", http.MethodGet, "/api/v1/me/library?tab=bogus", nil, http.StatusUnprocessableEntity},
		{"invalid comment sort", http.MethodGet, "/api/v1/videos/aB3dEf9hJkL/comments?sort=bogus", nil, http.StatusUnprocessableEntity},
		{"invalid feed limit", http.MethodGet, "/api/v1/me/feed?limit=0", nil, http.StatusUnprocessableEntity},
		{"missing video reaction", http.MethodPut, "/api/v1/videos/vid_missing/reaction", map[string]string{"kind": "like"}, http.StatusNotFound},
		{"missing video save", http.MethodPut, "/api/v1/videos/vid_missing/save", nil, http.StatusNotFound},
		{"missing video view", http.MethodPost, "/api/v1/videos/vid_missing/view", nil, http.StatusNotFound},
		{"missing video comments", http.MethodGet, "/api/v1/videos/vid_missing/comments", nil, http.StatusNotFound},
		{"removed video reaction", http.MethodPut, "/api/v1/videos/eF4gHi6jKlM/reaction", map[string]string{"kind": "like"}, http.StatusGone},
		{"removed video comments", http.MethodGet, "/api/v1/videos/eF4gHi6jKlM/comments", nil, http.StatusGone},
		{"private video reaction", http.MethodPut, "/api/v1/videos/nO8pQr1sTuV/reaction", map[string]string{"kind": "like"}, http.StatusNotFound},
		{"private video save", http.MethodPut, "/api/v1/videos/nO8pQr1sTuV/save", nil, http.StatusNotFound},
		{"private video view", http.MethodPost, "/api/v1/videos/nO8pQr1sTuV/view", nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := doJSON(t, handler, tc.method, tc.path, tc.body, cookies, csrf)
		if rec.Code != tc.want {
			t.Fatalf("%s: expected %d, got %d: %s", tc.name, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestEngagementCacheHeaders(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "cache@example.com", testPassword(t))

	privatePaths := []string{
		"/api/v1/me/feed",
		"/api/v1/me/library",
		"/api/v1/me/subscriptions",
		"/api/v1/videos/aB3dEf9hJkL/reaction",
		"/api/v1/videos/aB3dEf9hJkL/save",
	}
	for _, path := range privatePaths {
		rec := getWithCookies(t, handler, path, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("%s: expected private cache, got %q", path, got)
		}
	}

	rec := getWithCookies(t, handler, "/api/v1/videos/aB3dEf9hJkL/comments", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("public comments: expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("public comments: expected public cache, got %q", got)
	}
}
