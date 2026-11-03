package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type startViewBody struct {
	VideoID   string  `json:"video_id"`
	Views     int64   `json:"views"`
	SessionID string  `json:"session_id"`
	Counted   bool    `json:"counted"`
	Watched   float64 `json:"watched_seconds"`
	Completed bool    `json:"completed"`
}

type progressViewBody struct {
	VideoID   string  `json:"video_id"`
	SessionID string  `json:"session_id"`
	Views     int64   `json:"views"`
	Watched   float64 `json:"watched_seconds"`
	Delta     float64 `json:"watched_delta_seconds"`
	Completed bool    `json:"completed"`
}

type analyticsSummaryBody struct {
	ChannelID        string                  `json:"channel_id"`
	From             string                  `json:"from"`
	To               string                  `json:"to"`
	Views            int64                   `json:"views"`
	Sessions         int64                   `json:"sessions"`
	WatchSeconds     float64                 `json:"watch_seconds"`
	Completions      int64                   `json:"completions"`
	CompletionRate   float64                 `json:"completion_rate"`
	Impressions      int64                   `json:"impressions"`
	Clicks           int64                   `json:"clicks"`
	ClickThroughRate float64                 `json:"click_through_rate"`
	Sources          []store.AnalyticsSource `json:"sources"`
}

type analyticsTimeseriesBody struct {
	ChannelID string                 `json:"channel_id"`
	Points    []store.AnalyticsPoint `json:"points"`
}

type videoAnalyticsBody struct {
	VideoID string                 `json:"video_id"`
	Summary analyticsSummaryBody   `json:"summary"`
	Points  []store.AnalyticsPoint `json:"points"`
}

type telemetryBody struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Rejected   int `json:"rejected"`
}

func newAnalyticsHandlers(t *testing.T) (http.Handler, http.Handler, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	anonymous := api.New(api.Options{
		Store:   st,
		Auth:    auth.NewDev("usr_demo", "required-token"),
		Origins: []string{"http://localhost:3000"},
	})
	signedIn := api.New(api.Options{
		Store:   st,
		Auth:    auth.NewDev("usr_demo", ""),
		Origins: []string{"http://localhost:3000"},
	})
	return anonymous, signedIn, st
}

func TestAnonymousViewDeduplicationProgressAndAnalytics(t *testing.T) {
	anonymous, signedIn, _ := newAnalyticsHandlers(t)
	rec := doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view", map[string]any{
		"source": "feed",
	}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous view: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	first := decode[startViewBody](t, rec)
	if first.SessionID == "" || !first.Counted || first.Views != 1_842_001 {
		t.Fatalf("unexpected first view: %+v", first)
	}
	visitor := findCookie(rec.Result().Cookies(), "keeltube_visitor")
	if visitor == nil || !visitor.HttpOnly {
		t.Fatalf("expected an HttpOnly visitor cookie: %+v", visitor)
	}
	cookies := []*http.Cookie{visitor}

	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view", map[string]any{
		"source": "feed",
	}, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat view: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	repeat := decode[startViewBody](t, rec)
	if repeat.Counted || repeat.Views != first.Views {
		t.Fatalf("expected deduplicated view: %+v", repeat)
	}

	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view/progress", map[string]any{
		"session_id": first.SessionID, "event_id": "evt-1", "position_seconds": 3,
	}, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("progress: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	progress := decode[progressViewBody](t, rec)
	if progress.Watched != 3 || progress.Delta != 3 || progress.Completed {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view/progress", map[string]any{
		"session_id": first.SessionID, "event_id": "evt-1", "position_seconds": 7,
	}, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate progress: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	duplicate := decode[progressViewBody](t, rec)
	if duplicate.Delta != 0 || duplicate.Watched != 3 {
		t.Fatalf("duplicate progress changed state: %+v", duplicate)
	}
	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view/progress", map[string]any{
		"session_id": first.SessionID, "event_id": "evt-2", "position_seconds": 8, "completed": true,
	}, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("completion: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	completed := decode[progressViewBody](t, rec)
	if !completed.Completed || completed.Watched != 8 {
		t.Fatalf("unexpected completion: %+v", completed)
	}

	today := time.Now().UTC().Format("2006-01-02")
	rec = doJSON(t, signedIn, http.MethodGet,
		"/api/v1/studio/analytics/summary?from="+today+"&to="+today, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	summary := decode[analyticsSummaryBody](t, rec)
	if summary.Views != 1 || summary.Sessions != 2 || summary.WatchSeconds != 8 ||
		summary.Completions != 1 || summary.CompletionRate != 0.5 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(summary.Sources) != 1 || summary.Sources[0].Source != "feed" {
		t.Fatalf("unexpected sources: %+v", summary.Sources)
	}

	rec = doJSON(t, signedIn, http.MethodGet, "/api/v1/studio/stats", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("studio stats: expected 200, got %d", rec.Code)
	}
	stats := decode[store.StudioStats](t, rec)
	if stats.AnalyticsViews != 1 || stats.AnalyticsSessions != 2 ||
		stats.WatchSeconds != 8 || stats.WatchHours != 8.0/3600 ||
		stats.Completions != 1 || stats.CompletionRate != 0.5 {
		t.Fatalf("unexpected studio stats: %+v", stats)
	}

	rec = doJSON(t, signedIn, http.MethodGet,
		"/api/v1/studio/analytics/timeseries?from="+today+"&to="+today, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("timeseries: expected 200, got %d", rec.Code)
	}
	timeseries := decode[analyticsTimeseriesBody](t, rec)
	if len(timeseries.Points) != 1 || timeseries.Points[0].Views != 1 {
		t.Fatalf("unexpected timeseries: %+v", timeseries)
	}
	rec = doJSON(t, signedIn, http.MethodGet, "/api/v1/studio/analytics/timeseries", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("default timeseries: expected 200, got %d", rec.Code)
	}
	defaultTimeseries := decode[analyticsTimeseriesBody](t, rec)
	if len(defaultTimeseries.Points) != 28 {
		t.Fatalf("expected 28 default points, got %d", len(defaultTimeseries.Points))
	}

	rec = doJSON(t, signedIn, http.MethodGet,
		"/api/v1/studio/videos/vid_demo_001/analytics?from="+today+"&to="+today, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("video analytics: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	videoAnalytics := decode[videoAnalyticsBody](t, rec)
	if videoAnalytics.VideoID != "vid_demo_001" || videoAnalytics.Summary.Views != 1 {
		t.Fatalf("unexpected video analytics: %+v", videoAnalytics)
	}
	rec = doJSON(t, signedIn, http.MethodGet,
		"/api/v1/studio/videos/vid_even_keel/analytics?from="+today+"&to="+today, nil, nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-channel analytics 404, got %d", rec.Code)
	}
}

func TestTelemetryEventsAndRangeValidation(t *testing.T) {
	anonymous, signedIn, _ := newAnalyticsHandlers(t)
	rec := doJSON(t, anonymous, http.MethodPost, "/api/v1/telemetry/events", map[string]any{
		"events": []map[string]any{
			{"event_id": "imp-1", "video_id": "vid_demo_001", "event_type": "impression", "source": "feed", "position": 1},
			{"event_id": "click-1", "video_id": "vid_demo_001", "event_type": "click", "source": "feed", "position": 1},
			{"event_id": "imp-1", "video_id": "vid_demo_001", "event_type": "impression", "source": "feed", "position": 2},
			{"event_id": "missing-1", "video_id": "vid_missing", "event_type": "impression", "source": "feed"},
		},
	}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("telemetry: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	result := decode[telemetryBody](t, rec)
	if result.Accepted != 2 || result.Duplicates != 1 || result.Rejected != 1 {
		t.Fatalf("unexpected telemetry result: %+v", result)
	}
	visitor := findCookie(rec.Result().Cookies(), "keeltube_visitor")
	if visitor == nil {
		t.Fatal("expected anonymous telemetry to set a visitor cookie")
	}

	today := time.Now().UTC().Format("2006-01-02")
	rec = doJSON(t, signedIn, http.MethodGet,
		"/api/v1/studio/analytics/summary?from="+today+"&to="+today, nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: expected 200, got %d", rec.Code)
	}
	summary := decode[analyticsSummaryBody](t, rec)
	if summary.Impressions != 1 || summary.Clicks != 1 || summary.ClickThroughRate != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	for _, path := range []string{
		"/api/v1/studio/analytics/summary?from=bad",
		"/api/v1/studio/analytics/summary?from=2026-11-03&to=2026-11-01",
		"/api/v1/studio/analytics/summary?from=2020-01-01&to=2026-11-01",
	} {
		rec = doJSON(t, signedIn, http.MethodGet, path, nil, nil, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: expected 422, got %d", path, rec.Code)
		}
	}

	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/telemetry/events", map[string]any{
		"events": []map[string]any{
			{"event_id": "bad-1", "video_id": "vid_demo_001", "event_type": "impression", "source": "bogus"},
		},
	}, nil, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid source: expected 422, got %d", rec.Code)
	}
}

func TestProgressRejectsAnotherVisitorSession(t *testing.T) {
	anonymous, _, _ := newAnalyticsHandlers(t)
	rec := doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view", map[string]any{}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("view: expected 200, got %d", rec.Code)
	}
	first := decode[startViewBody](t, rec)
	firstCookie := findCookie(rec.Result().Cookies(), "keeltube_visitor")

	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view", map[string]any{}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second view: expected 200, got %d", rec.Code)
	}
	secondCookie := findCookie(rec.Result().Cookies(), "keeltube_visitor")
	if firstCookie == nil || secondCookie == nil || firstCookie.Value == secondCookie.Value {
		t.Fatal("expected distinct anonymous visitor cookies")
	}
	rec = doJSON(t, anonymous, http.MethodPost, "/api/v1/videos/vid_demo_001/view/progress", map[string]any{
		"session_id": first.SessionID, "event_id": "evt-1", "position_seconds": 1,
	}, []*http.Cookie{secondCookie}, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-visitor progress rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}
