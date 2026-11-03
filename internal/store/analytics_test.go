package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func analyticsStore(t *testing.T) *store.Store {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st
}

func viewerFor(actorID string) store.Viewer {
	return store.Viewer{Key: "actor:" + actorID, ActorID: &actorID}
}

func TestStartViewDeduplicatesAndTracksProgress(t *testing.T) {
	st := analyticsStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second)

	first, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "watch", start)
	if err != nil {
		t.Fatalf("start view: %v", err)
	}
	if !first.Session.Counted || first.Views != 684_001 {
		t.Fatalf("unexpected first view: %+v", first)
	}
	repeat, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "watch", start.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("repeat view: %v", err)
	}
	if repeat.Session.Counted || repeat.Views != first.Views {
		t.Fatalf("expected deduplicated repeat view: %+v", repeat)
	}

	progress, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID:       first.Session.ID,
		EventID:         "evt-1",
		PositionSeconds: 5,
	}, start.Add(5*time.Second))
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if progress.WatchedDelta != 5 || progress.Session.WatchedSeconds != 5 {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	duplicate, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID:       first.Session.ID,
		EventID:         "evt-1",
		PositionSeconds: 12,
	}, start.Add(6*time.Second))
	if err != nil {
		t.Fatalf("duplicate progress: %v", err)
	}
	if duplicate.WatchedDelta != 0 || duplicate.Session.WatchedSeconds != 5 {
		t.Fatalf("duplicate progress changed state: %+v", duplicate)
	}
	completed, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID:       first.Session.ID,
		EventID:         "evt-2",
		PositionSeconds: 14,
		Completed:       true,
	}, start.Add(10*time.Second))
	if err != nil {
		t.Fatalf("complete progress: %v", err)
	}
	if !completed.Completed || completed.Session.WatchedSeconds != 14 {
		t.Fatalf("unexpected completion: %+v", completed)
	}

	from := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	summary, err := st.StudioAnalyticsSummary(ctx, "ch_keel_lab", from, from)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Views != 1 || summary.Sessions != 2 || summary.Completions != 1 ||
		summary.CompletionRate != 0.5 || summary.WatchSeconds != 14 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(summary.Sources) != 1 || summary.Sources[0].Source != "watch" {
		t.Fatalf("unexpected sources: %+v", summary.Sources)
	}
	points, err := st.StudioAnalyticsTimeseries(ctx, "ch_keel_lab", from, from)
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	if len(points) != 1 || points[0].Views != 1 || points[0].Completions != 1 {
		t.Fatalf("unexpected timeseries: %+v", points)
	}
	videoSummary, videoPoints, err := st.StudioVideoAnalytics(ctx, "ch_keel_lab", "vid_even_keel", from, from)
	if err != nil {
		t.Fatalf("video analytics: %v", err)
	}
	if videoSummary.Views != 1 || len(videoPoints) != 1 {
		t.Fatalf("unexpected video analytics: %+v %+v", videoSummary, videoPoints)
	}
	if _, _, err := st.StudioVideoAnalytics(ctx, "ch_demo", "vid_even_keel", from, from); err == nil {
		t.Fatal("expected cross-channel analytics rejection")
	}
}

func TestDiscoveryEventsAndAnalytics(t *testing.T) {
	st := analyticsStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	result, err := st.RecordDiscoveryEvents(ctx, viewerFor("usr_demo"), []store.DiscoveryEventInput{
		{EventID: "imp-1", VideoID: "vid_even_keel", EventType: "impression", Source: "feed", Position: 1},
		{EventID: "click-1", VideoID: "vid_even_keel", EventType: "click", Source: "feed", Position: 1},
		{EventID: "missing-1", VideoID: "vid_missing", EventType: "impression", Source: "feed"},
		{EventID: "private-1", VideoID: "vid_private_001", EventType: "impression", Source: "feed"},
	}, now)
	if err != nil {
		t.Fatalf("record discovery events: %v", err)
	}
	if result.Accepted != 2 || result.Rejected != 2 || result.Duplicates != 0 {
		t.Fatalf("unexpected discovery result: %+v", result)
	}
	duplicate, err := st.RecordDiscoveryEvents(ctx, viewerFor("usr_demo"), []store.DiscoveryEventInput{
		{EventID: "imp-1", VideoID: "vid_even_keel", EventType: "impression", Source: "feed"},
	}, now)
	if err != nil {
		t.Fatalf("duplicate discovery event: %v", err)
	}
	if duplicate.Duplicates != 1 || duplicate.Accepted != 0 {
		t.Fatalf("unexpected duplicate result: %+v", duplicate)
	}
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	summary, err := st.StudioAnalyticsSummary(ctx, "ch_keel_lab", from, from)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Impressions != 1 || summary.Clicks != 1 || summary.ClickThroughRate != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestPruneTelemetryKeepsDailyAggregates(t *testing.T) {
	st := analyticsStore(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -100).Truncate(time.Second)
	view, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "feed", old)
	if err != nil {
		t.Fatalf("start old view: %v", err)
	}
	if _, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID: view.Session.ID, EventID: "old-event", PositionSeconds: 5,
	}, old.Add(5*time.Second)); err != nil {
		t.Fatalf("old progress: %v", err)
	}
	if _, err := st.RecordDiscoveryEvents(ctx, viewerFor("usr_demo"), []store.DiscoveryEventInput{
		{EventID: "old-imp", VideoID: "vid_even_keel", EventType: "impression", Source: "feed"},
	}, old); err != nil {
		t.Fatalf("old discovery event: %v", err)
	}
	recent, err := st.StartView(ctx, viewerFor("usr_keel_lab"), "vid_demo_001", "watch", time.Now().UTC())
	if err != nil {
		t.Fatalf("recent view: %v", err)
	}
	pruned, err := st.PruneTelemetry(ctx, time.Now().UTC().AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("prune telemetry: %v", err)
	}
	if pruned.Sessions != 1 || pruned.ViewEvents != 1 || pruned.Discovery != 1 {
		t.Fatalf("unexpected prune result: %+v", pruned)
	}
	if _, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID: view.Session.ID, EventID: "after-prune", PositionSeconds: 6,
	}, old.Add(6*time.Second)); err == nil {
		t.Fatal("expected pruned session to be unavailable")
	}
	if _, err := st.ProgressView(ctx, viewerFor("usr_keel_lab"), "vid_demo_001", store.ViewProgressInput{
		SessionID: recent.Session.ID, EventID: "recent-event", PositionSeconds: 1,
	}, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("recent session should remain: %v", err)
	}
	oldDay := time.Date(old.Year(), old.Month(), old.Day(), 0, 0, 0, 0, time.UTC)
	summary, err := st.StudioAnalyticsSummary(ctx, "ch_keel_lab", oldDay, oldDay)
	if err != nil {
		t.Fatalf("old summary: %v", err)
	}
	if summary.Views != 1 || summary.WatchSeconds != 5 || summary.Impressions != 1 {
		t.Fatalf("daily aggregates were lost: %+v", summary)
	}
}

func TestViewDedupWindowExpires(t *testing.T) {
	st := analyticsStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second)
	first, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "watch", start)
	if err != nil {
		t.Fatalf("first view: %v", err)
	}
	second, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "watch", start.Add(31*time.Minute))
	if err != nil {
		t.Fatalf("second view: %v", err)
	}
	if !second.Session.Counted || second.Views != first.Views+1 {
		t.Fatalf("expected a new counted view after the window: %+v", second)
	}
}

func TestProgressIgnoresSeeksAndCountsCompletionOnce(t *testing.T) {
	st := analyticsStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Second)
	view, err := st.StartView(ctx, viewerFor("usr_demo"), "vid_even_keel", "watch", start)
	if err != nil {
		t.Fatalf("start view: %v", err)
	}
	seek, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID: view.Session.ID, EventID: "seek", PositionSeconds: 12,
	}, start.Add(5*time.Second))
	if err != nil {
		t.Fatalf("seek progress: %v", err)
	}
	if seek.WatchedDelta != 0 || seek.Session.WatchedSeconds != 0 {
		t.Fatalf("seek should not count as watch time: %+v", seek)
	}
	first, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID: view.Session.ID, EventID: "done-1", PositionSeconds: 14, Completed: true,
	}, start.Add(20*time.Second))
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}
	if !first.Completed {
		t.Fatalf("expected completion: %+v", first)
	}
	if _, err := st.ProgressView(ctx, viewerFor("usr_demo"), "vid_even_keel", store.ViewProgressInput{
		SessionID: view.Session.ID, EventID: "done-2", PositionSeconds: 14, Completed: true,
	}, start.Add(21*time.Second)); err != nil {
		t.Fatalf("second completion: %v", err)
	}
	from := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	summary, err := st.StudioAnalyticsSummary(ctx, "ch_keel_lab", from, from)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Completions != 1 {
		t.Fatalf("completion should be counted once: %+v", summary)
	}
}
