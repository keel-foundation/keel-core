package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/media"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type studioPageBody struct {
	Items   []store.Video `json:"items"`
	Limit   int           `json:"limit"`
	Offset  int           `json:"offset"`
	Total   int           `json:"total"`
	HasMore bool          `json:"has_more"`
}

type studioVersionPage struct {
	Items []store.VideoVersion `json:"items"`
}

type studioVideoDetailBody struct {
	store.Video
	Rights    *store.VideoRights    `json:"rights"`
	Subtitles []store.SubtitleTrack `json:"subtitles"`
	Versions  []store.VideoVersion  `json:"versions"`
}

func TestStudioVideoCRUDAndVersions(t *testing.T) {
	handler, st := newHandlerWithStore(t, "")

	rec := doJSON(t, handler, http.MethodGet, "/api/v1/studio/videos", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list studio videos: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[studioPageBody](t, rec)
	if page.Total < 6 {
		t.Fatalf("expected seeded studio videos, got total=%d", page.Total)
	}
	foundDraft := false
	for _, video := range page.Items {
		if video.ID == "vid_draft_001" {
			foundDraft = true
		}
	}
	if !foundDraft {
		t.Fatal("expected the seeded draft video in the studio list")
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/videos/vid_even_keel", nil, nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected ownership 404, got %d", rec.Code)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos", map[string]any{
		"title": "New draft", "description": "Created by test", "visibility": "private",
		"category": "Studio", "license": "cc-by", "format": "video", "tags": []string{"test"},
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create studio video: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decode[store.Video](t, rec)
	if created.ID == "" || created.PublicID == "" || created.Status != "draft" || created.ChannelID != "ch_demo" {
		t.Fatalf("unexpected created video: %+v", created)
	}

	rec = doJSON(t, handler, http.MethodPatch, "/api/v1/studio/videos/"+created.ID, map[string]any{
		"title": "Updated draft", "visibility": "unlisted", "category": "Studio",
		"license": "cc-by", "format": "video", "tags": []string{"updated"},
	}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("update studio video: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	updated := decode[store.Video](t, rec)
	if updated.Title != "Updated draft" || updated.Visibility != "unlisted" {
		t.Fatalf("unexpected updated video: %+v", updated)
	}

	if err := st.UpdateVideoPolicy(context.Background(), created.ID, store.Policy{
		PolicyVersion:     "studio-0.1",
		RightsDeclaration: "original",
		RightsHolder:      "Ada Lovelace",
		RightsNotes:       "Owned footage.",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("update video policy: %v", err)
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/videos/"+created.ID, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get studio video detail: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	detail := decode[studioVideoDetailBody](t, rec)
	if detail.ID != created.ID || detail.Title != "Updated draft" {
		t.Fatalf("unexpected detail video: %+v", detail)
	}
	if detail.Rights == nil || detail.Rights.Declaration != "original" || detail.Rights.Holder != "Ada Lovelace" {
		t.Fatalf("unexpected rights aggregate: %+v", detail.Rights)
	}
	if len(detail.Subtitles) != 0 || len(detail.Versions) != 0 {
		t.Fatalf("unexpected empty edit aggregate: %+v", detail)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos/"+created.ID+"/versions", map[string]any{
		"label": "First cut", "trim": map[string]any{"start": 0, "end": 8},
		"cuts": []map[string]any{{"start": 2, "end": 3}}, "volume": 1,
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create version: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	version := decode[store.VideoVersion](t, rec)
	if version.ID == "" || version.VideoID != created.ID {
		t.Fatalf("unexpected version: %+v", version)
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/videos/"+created.ID+"/versions", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list versions: expected 200, got %d", rec.Code)
	}
	versions := decode[studioVersionPage](t, rec)
	if len(versions.Items) != 1 || versions.Items[0].ID != version.ID {
		t.Fatalf("unexpected versions: %+v", versions)
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/videos/"+created.ID, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get studio video detail with version: expected 200, got %d", rec.Code)
	}
	detail = decode[studioVideoDetailBody](t, rec)
	if len(detail.Versions) != 1 || detail.Versions[0].ID != version.ID {
		t.Fatalf("detail did not include the version: %+v", detail.Versions)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos/"+created.ID+"/publish", nil, nil, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("publish without playback: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/stats", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("studio stats: expected 200, got %d", rec.Code)
	}
	stats := decode[store.StudioStats](t, rec)
	if stats.VideoCount < 7 || stats.Subscribers != 2_400_000 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/studio/channel", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("studio channel: expected 200, got %d", rec.Code)
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/uploads", map[string]any{
		"title": "Upload", "filename": "clip.mp4", "content_type": "video/mp4", "size_bytes": 1024,
	}, nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload without media config: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos/"+created.ID+"/subtitles", map[string]any{
		"language": "en", "label": "English", "kind": "subtitles",
		"filename": "english.vtt", "content_type": "text/vtt", "size_bytes": 1024,
	}, nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("subtitle upload without media config: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos/"+created.ID+"/thumbnail", map[string]any{
		"filename": "thumb.png", "content_type": "image/png", "size_bytes": 1024,
	}, nil, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("thumbnail upload without media config: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

type fakeStudioMedia struct{}

func (fakeStudioMedia) CreateUpload(context.Context, string, string, media.CreateUploadInput) (media.UploadSession, error) {
	return media.UploadSession{}, nil
}

func (fakeStudioMedia) CompleteUpload(context.Context, string, string, []media.CompletedPart, string) (store.UploadIntent, error) {
	return store.UploadIntent{}, nil
}

func (fakeStudioMedia) AbortUpload(context.Context, string, string) error { return nil }

func (fakeStudioMedia) RefreshUploadParts(context.Context, string, string, []int32) ([]media.PresignedPart, time.Time, error) {
	return nil, time.Time{}, nil
}

func (fakeStudioMedia) CreateSubtitleUpload(context.Context, string, string, string, string, string, string, string, int64) (media.SubtitleUpload, error) {
	return media.SubtitleUpload{}, nil
}

func (fakeStudioMedia) CompleteSubtitleUpload(context.Context, string, string, string) (store.SubtitleTrack, error) {
	return store.SubtitleTrack{}, nil
}

func (fakeStudioMedia) DeleteSubtitle(context.Context, string, string, string) error { return nil }

func (fakeStudioMedia) CreateThumbnailUpload(context.Context, string, string, string, string, int64) (media.ThumbnailUpload, error) {
	return media.ThumbnailUpload{}, nil
}

func (fakeStudioMedia) CompleteThumbnailUpload(context.Context, string, string, string) error {
	return nil
}

func (fakeStudioMedia) PresignGet(_ context.Context, key string) (string, error) {
	return "https://storage.example/" + key, nil
}

func (fakeStudioMedia) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("#EXTM3U\n")), nil
}

func newMediaHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := api.New(api.Options{
		Store:      st,
		Auth:       auth.NewDev("usr_demo", ""),
		Media:      fakeStudioMedia{},
		Origins:    []string{"http://localhost:3000"},
		RateWindow: time.Minute,
	})
	return handler, st
}

func TestMediaGatewayAccessAndPlayback(t *testing.T) {
	handler, st := newMediaHandler(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, asset := range []store.MediaAsset{
		{VideoID: "vid_demo_001", Kind: "mp4", ObjectKey: "media/vid_demo_001/video.mp4", ContentType: "video/mp4", UpdatedAt: now},
		{VideoID: "vid_demo_001", Kind: "hls", ObjectKey: "media/vid_demo_001/hls/", ContentType: "application/vnd.apple.mpegurl", UpdatedAt: now},
		{VideoID: "vid_demo_001", Kind: "thumbnail", ObjectKey: "media/vid_demo_001/thumbnail.jpg", ContentType: "image/jpeg", UpdatedAt: now},
		{VideoID: "vid_private_001", Kind: "mp4", ObjectKey: "media/vid_private_001/video.mp4", ContentType: "video/mp4", UpdatedAt: now},
	} {
		if err := st.UpsertMediaAsset(ctx, asset); err != nil {
			t.Fatalf("upsert asset: %v", err)
		}
	}

	rec := doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/video.mp4", nil, nil, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") == "" {
		t.Fatalf("expected media redirect, got %d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	track, err := st.CreateSubtitleTrack(ctx, store.SubtitleTrack{
		VideoID: "vid_demo_001", ChannelID: "ch_demo", Language: "en", Label: "English",
		Kind: "subtitles", ObjectKey: "media/vid_demo_001/subtitles/en.vtt", Status: "ready",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create subtitle track: %v", err)
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/subtitles/"+track.ID+".vtt", nil, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("expected subtitle redirect, got %d", rec.Code)
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/hls/master.m3u8", nil, nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "#EXTM3U\n" {
		t.Fatalf("expected manifest, got %d body=%q", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/hls/1080p/seg_00001.ts", nil, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("expected segment redirect, got %d", rec.Code)
	}

	rec = doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/hls/1080p/../secret.ts", nil, nil, "")
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected traversal rejection, got %d", rec.Code)
	}

	anonymous := api.New(api.Options{
		Store: st, Auth: auth.NewDev("usr_demo", "token"), Media: fakeStudioMedia{},
		Origins: []string{"http://localhost:3000"}, RateWindow: time.Minute,
	})
	rec = doJSON(t, anonymous, http.MethodGet, "/api/v1/media/vid_private_001/video.mp4", nil, nil, "")
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
		t.Fatalf("expected private media denial, got %d", rec.Code)
	}
}

func TestMediaGatewayRequiresAsset(t *testing.T) {
	handler, _ := newMediaHandler(t)
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/media/vid_demo_001/video.mp4", nil, nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing asset 404, got %d", rec.Code)
	}
}

func TestStudioRequiresAuthentication(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	handler := api.New(api.Options{Store: st, Origins: []string{"http://localhost:3000"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/studio/videos", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestStudioCSRFAndSessionAccess(t *testing.T) {
	handler := newHandler(t, "")
	_, cookies := registerAndVerify(t, handler, "studio-csrf@example.com", testPassword(t))
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos", map[string]any{
		"title": "CSRF draft", "visibility": "private", "category": "Studio",
		"license": "cc-by", "format": "video",
	}, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF rejection, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, handler, http.MethodPost, "/api/v1/studio/videos", map[string]any{
		"title": "CSRF draft", "visibility": "private", "category": "Studio",
		"license": "cc-by", "format": "video",
	}, cookies, csrfCookie.Value)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected CSRF-authorized create, got %d: %s", rec.Code, rec.Body.String())
	}
}
