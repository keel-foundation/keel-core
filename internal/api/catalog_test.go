package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

type catalogVideoPage struct {
	Items   []store.Video `json:"items"`
	Limit   int           `json:"limit"`
	Offset  int           `json:"offset"`
	Total   int           `json:"total"`
	HasMore bool          `json:"has_more"`
}

type catalogChannelPage struct {
	Items   []store.Channel `json:"items"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	Total   int             `json:"total"`
	HasMore bool            `json:"has_more"`
}

type catalogSearchResponse struct {
	Videos   catalogVideoPage   `json:"videos"`
	Channels catalogChannelPage `json:"channels"`
}

type catalogExploreCollection struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Videos      catalogVideoPage `json:"videos"`
}

type catalogExploreResponse struct {
	Featured    *store.Video               `json:"featured"`
	New         catalogVideoPage           `json:"new"`
	Trending    catalogVideoPage           `json:"trending"`
	Collections []catalogExploreCollection `json:"collections"`
}

type catalogCategory struct {
	Name       string `json:"name"`
	VideoCount int    `json:"video_count"`
}

type catalogCategoriesResponse struct {
	Categories []catalogCategory `json:"categories"`
}

func catalogGet(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestCatalogFeed(t *testing.T) {
	handler := newHandler(t, "")
	rec := catalogGet(t, handler, "/api/v1/feed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[catalogVideoPage](t, rec)
	if page.Total != 5 || len(page.Items) != 5 {
		t.Fatalf("expected 5 feed videos, got total=%d len=%d", page.Total, len(page.Items))
	}
	if page.HasMore {
		t.Fatal("expected no next page")
	}
	for _, video := range page.Items {
		if video.Format != "video" || video.Visibility != "public" {
			t.Fatalf("unexpected feed video: %+v", video)
		}
	}
}

func TestCatalogVideosValidation(t *testing.T) {
	handler := newHandler(t, "")
	cases := []struct {
		path  string
		field string
	}{
		{"/api/v1/videos?limit=0", "limit"},
		{"/api/v1/videos?limit=51", "limit"},
		{"/api/v1/videos?offset=-1", "offset"},
		{"/api/v1/videos?sort=bogus", "sort"},
		{"/api/v1/videos?format=bogus", "format"},
		{"/api/v1/videos?category=bogus", "category"},
	}
	for _, tc := range cases {
		rec := catalogGet(t, handler, tc.path, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: expected 422, got %d", tc.path, rec.Code)
		}
		body := decode[map[string]any](t, rec)
		if body["field"] != tc.field {
			t.Fatalf("%s: expected field %q, got %v", tc.path, tc.field, body["field"])
		}
	}
}

func TestCatalogExplore(t *testing.T) {
	handler := newHandler(t, "")
	rec := catalogGet(t, handler, "/api/v1/explore", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decode[catalogExploreResponse](t, rec)
	if body.Featured == nil || body.Featured.ID != "vid_demo_001" {
		t.Fatalf("unexpected featured video: %+v", body.Featured)
	}
	if len(body.New.Items) != 8 || len(body.Trending.Items) != 8 {
		t.Fatalf("unexpected explore lists: new=%d trending=%d", len(body.New.Items), len(body.Trending.Items))
	}
	if body.New.Total != 8 || body.Trending.Total != 8 {
		t.Fatalf("unexpected explore totals: new=%d trending=%d", body.New.Total, body.Trending.Total)
	}
	if len(body.Collections) != 3 {
		t.Fatalf("expected 3 collections, got %d", len(body.Collections))
	}
	collectionSizes := map[string]int{}
	for _, collection := range body.Collections {
		collectionSizes[collection.ID] = len(collection.Videos.Items)
	}
	if collectionSizes["even-keel"] != 4 || collectionSizes["field-notes"] != 1 || collectionSizes["build-in-public"] != 3 {
		t.Fatalf("unexpected collection sizes: %+v", collectionSizes)
	}

	limited := catalogGet(t, handler, "/api/v1/explore?limit=3&collection_limit=2", "")
	if limited.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", limited.Code)
	}
	limitedBody := decode[catalogExploreResponse](t, limited)
	if len(limitedBody.New.Items) != 3 || len(limitedBody.Trending.Items) != 3 {
		t.Fatalf("unexpected limited explore lists: new=%d trending=%d", len(limitedBody.New.Items), len(limitedBody.Trending.Items))
	}
	for _, collection := range limitedBody.Collections {
		if len(collection.Videos.Items) > 2 {
			t.Fatalf("collection %s exceeded limit: %d", collection.ID, len(collection.Videos.Items))
		}
	}
}

func TestCatalogVideoVisibility(t *testing.T) {
	token, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate dev token: %v", err)
	}
	handler := newHandler(t, token)

	public := catalogGet(t, handler, "/api/v1/videos/vid_demo_001", "")
	if public.Code != http.StatusOK {
		t.Fatalf("public video: expected 200, got %d", public.Code)
	}
	video := decode[store.Video](t, public)
	if video.Format != "video" || video.Category != "Documentaries" || len(video.Tags) != 3 {
		t.Fatalf("unexpected public video fields: %+v", video)
	}

	if rec := catalogGet(t, handler, "/api/v1/videos/vid_private_001", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("private video anonymous: expected 404, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_private_001", token); rec.Code != http.StatusOK {
		t.Fatalf("private video owner: expected 200, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_removed_001", ""); rec.Code != http.StatusGone {
		t.Fatalf("removed video: expected 410, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_draft_001", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("draft video anonymous: expected 404, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_draft_001", token); rec.Code != http.StatusOK {
		t.Fatalf("draft video owner: expected 200, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_processing_001", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("processing video anonymous: expected 404, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_processing_001", token); rec.Code != http.StatusOK {
		t.Fatalf("processing video owner: expected 200, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing video: expected 404, got %d", rec.Code)
	}
}

func TestCatalogChannelAndVideos(t *testing.T) {
	handler := newHandler(t, "")

	rec := catalogGet(t, handler, "/api/v1/channels/ch_demo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	channel := decode[store.Channel](t, rec)
	if channel.Subscribers != 2_400_000 || !channel.Verified {
		t.Fatalf("unexpected channel: %+v", channel)
	}
	if channel.VideoCount != 1 || channel.TotalViews != 1_842_000 {
		t.Fatalf("unexpected channel stats: %+v", channel)
	}

	videos := catalogGet(t, handler, "/api/v1/channels/ch_keel_lab/videos", "")
	if videos.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", videos.Code)
	}
	videoPage := decode[catalogVideoPage](t, videos)
	if videoPage.Total != 2 || len(videoPage.Items) != 2 {
		t.Fatalf("expected 2 channel videos, got total=%d len=%d", videoPage.Total, len(videoPage.Items))
	}
	for _, video := range videoPage.Items {
		if video.Format != "video" {
			t.Fatalf("expected video format, got %+v", video)
		}
	}

	shorts := catalogGet(t, handler, "/api/v1/channels/ch_keel_lab/videos?format=short", "")
	if shorts.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", shorts.Code)
	}
	shortPage := decode[catalogVideoPage](t, shorts)
	if shortPage.Total != 2 || len(shortPage.Items) != 2 {
		t.Fatalf("expected 2 channel shorts, got total=%d len=%d", shortPage.Total, len(shortPage.Items))
	}

	if rec := catalogGet(t, handler, "/api/v1/channels/ch_missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing channel: expected 404, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/channels/ch_missing/videos", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing channel videos: expected 404, got %d", rec.Code)
	}
}

func TestCatalogSearch(t *testing.T) {
	handler := newHandler(t, "")

	rec := catalogGet(t, handler, "/api/v1/search?q=lighthouse", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decode[catalogSearchResponse](t, rec)
	if body.Videos.Total != 1 || body.Videos.Items[0].ID != "vid_demo_001" || body.Channels.Total != 0 {
		t.Fatalf("unexpected lighthouse search: %+v", body)
	}

	keel := catalogGet(t, handler, "/api/v1/search?q=keel", "")
	if keel.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", keel.Code)
	}
	keelBody := decode[catalogSearchResponse](t, keel)
	if keelBody.Videos.Total != 2 || keelBody.Channels.Total != 1 {
		t.Fatalf("unexpected keel search: videos=%d channels=%d", keelBody.Videos.Total, keelBody.Channels.Total)
	}

	channelsOnly := catalogGet(t, handler, "/api/v1/search?q=keel&type=channels", "")
	if channelsOnly.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", channelsOnly.Code)
	}
	channelsBody := decode[catalogSearchResponse](t, channelsOnly)
	if channelsBody.Videos.Total != 0 || len(channelsBody.Videos.Items) != 0 || channelsBody.Channels.Total != 1 {
		t.Fatalf("unexpected channel search: %+v", channelsBody)
	}
	if channelsBody.Channels.Items[0].VideoCount != 4 {
		t.Fatalf("unexpected channel search stats: %+v", channelsBody.Channels.Items[0])
	}

	shortsOnly := catalogGet(t, handler, "/api/v1/search?q=keel&type=shorts", "")
	if shortsOnly.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", shortsOnly.Code)
	}
	shortsBody := decode[catalogSearchResponse](t, shortsOnly)
	if shortsBody.Videos.Total != 1 || shortsBody.Channels.Total != 0 {
		t.Fatalf("unexpected short search: %+v", shortsBody)
	}

	if rec := catalogGet(t, handler, "/api/v1/search", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing query: expected 422, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/search?q=keel&type=bogus", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid type: expected 422, got %d", rec.Code)
	}
}

func TestCatalogShortsAndCategories(t *testing.T) {
	handler := newHandler(t, "")

	rec := catalogGet(t, handler, "/api/v1/shorts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	shorts := decode[catalogVideoPage](t, rec)
	if shorts.Total != 3 || len(shorts.Items) != 3 {
		t.Fatalf("expected 3 shorts, got total=%d len=%d", shorts.Total, len(shorts.Items))
	}
	for _, video := range shorts.Items {
		if video.Format != "short" {
			t.Fatalf("unexpected short: %+v", video)
		}
	}

	categories := catalogGet(t, handler, "/api/v1/categories", "")
	if categories.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", categories.Code)
	}
	body := decode[catalogCategoriesResponse](t, categories)
	expected := map[string]int{
		"Documentaries": 1,
		"Protocol":      2,
		"Moderation":    2,
		"Studio":        2,
		"Federation":    1,
	}
	if len(body.Categories) != len(expected) {
		t.Fatalf("unexpected categories: %+v", body.Categories)
	}
	for _, category := range body.Categories {
		if category.VideoCount != expected[category.Name] {
			t.Fatalf("unexpected count for %s: %d", category.Name, category.VideoCount)
		}
	}
}

func TestCatalogRelated(t *testing.T) {
	token, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate dev token: %v", err)
	}
	handler := newHandler(t, token)

	rec := catalogGet(t, handler, "/api/v1/videos/vid_even_keel/related", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[catalogVideoPage](t, rec)
	if page.Total != 7 || len(page.Items) != 7 {
		t.Fatalf("expected 7 related videos, got total=%d len=%d", page.Total, len(page.Items))
	}
	if page.Items[0].ID != "vid_short_keel" {
		t.Fatalf("expected category match first, got %s", page.Items[0].ID)
	}
	for _, video := range page.Items {
		if video.ID == "vid_even_keel" {
			t.Fatal("related videos must exclude the current video")
		}
	}

	if rec := catalogGet(t, handler, "/api/v1/videos/vid_private_001/related", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("private related anonymous: expected 404, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_private_001/related", token); rec.Code != http.StatusOK {
		t.Fatalf("private related owner: expected 200, got %d", rec.Code)
	}
	if rec := catalogGet(t, handler, "/api/v1/videos/vid_missing/related", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing related: expected 404, got %d", rec.Code)
	}
}
