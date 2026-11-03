package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func actorViewer(actorID string) store.Viewer {
	return store.Viewer{Key: "actor:" + actorID, ActorID: &actorID}
}

func newDiscoveryStore(t *testing.T) *store.Store {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(
		context.Background(), st, "http://localhost:8081", "http://localhost:3000", "", "",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st
}

func newDiscoveryEngine(st *store.Store) *discovery.Engine {
	return discovery.NewEngine(
		st,
		discovery.NewHashEmbedder(discovery.DefaultDimensions),
		discovery.DefaultConfig(),
	)
}

func TestFeedColdStart(t *testing.T) {
	st := newDiscoveryStore(t)
	engine := newDiscoveryEngine(st)

	result, err := engine.Feed(context.Background(), "", 20, 0, "video", "")
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if result.Personalized {
		t.Fatal("cold start must not be personalized")
	}
	if result.Total != 5 || len(result.Items) != 5 {
		t.Fatalf("unexpected cold start feed: total=%d items=%d", result.Total, len(result.Items))
	}
	for _, item := range result.Items {
		if item.Reason == nil {
			t.Fatalf("expected a reason: %+v", item)
		}
		if item.Format != "video" || item.Visibility != "public" {
			t.Fatalf("unexpected cold start video: %+v", item)
		}
	}
}

func TestFeedPersonalized(t *testing.T) {
	st := newDiscoveryStore(t)
	engine := newDiscoveryEngine(st)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, _, err := st.Subscribe(ctx, "usr_demo", "ch_keel_lab", now); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := st.SetReaction(ctx, "usr_demo", "vid_even_keel", "like", now); err != nil {
		t.Fatalf("like: %v", err)
	}
	if _, err := st.SaveVideo(ctx, "usr_demo", "vid_even_keel", now); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := st.StartView(ctx, actorViewer("usr_demo"), "vid_even_keel", "watch", now); err != nil {
		t.Fatalf("view: %v", err)
	}
	if _, err := st.SetReaction(ctx, "usr_demo", "vid_demo_001", "dislike", now); err != nil {
		t.Fatalf("dislike: %v", err)
	}
	if err := engine.RefreshAllVideos(ctx); err != nil {
		t.Fatalf("refresh embeddings: %v", err)
	}

	result, err := engine.Feed(ctx, "usr_demo", 20, 0, "video", "")
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !result.Personalized {
		t.Fatal("expected a personalized feed")
	}
	if result.Total == 0 || len(result.Items) == 0 {
		t.Fatal("expected recommendations")
	}
	foundSubscribed := false
	for _, item := range result.Items {
		if item.ID == "vid_demo_001" {
			t.Fatal("disliked video must be excluded")
		}
		if item.ID == "vid_even_keel" {
			foundSubscribed = true
			if item.Reason == nil || item.Reason.Code != "subscription" {
				t.Fatalf("expected a subscription reason, got %+v", item.Reason)
			}
		}
	}
	if !foundSubscribed {
		t.Fatal("expected the subscribed channel video in the feed")
	}

	page, err := engine.Feed(ctx, "usr_demo", 1, 1, "video", "")
	if err != nil {
		t.Fatalf("paged feed: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one paged item, got %d", len(page.Items))
	}
}

func TestExplorePersonalized(t *testing.T) {
	st := newDiscoveryStore(t)
	engine := newDiscoveryEngine(st)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, _, err := st.Subscribe(ctx, "usr_demo", "ch_keel_lab", now); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := st.SaveVideo(ctx, "usr_demo", "vid_even_keel", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := st.SetReaction(ctx, "usr_demo", "vid_even_keel", "like", now.Add(-time.Hour)); err != nil {
		t.Fatalf("like: %v", err)
	}
	if _, err := st.StartView(ctx, actorViewer("usr_demo"), "vid_even_keel", "watch", now); err != nil {
		t.Fatalf("view: %v", err)
	}
	if err := engine.RefreshAllVideos(ctx); err != nil {
		t.Fatalf("refresh embeddings: %v", err)
	}

	result, err := engine.Explore(ctx, "usr_demo", 8, 4)
	if err != nil {
		t.Fatalf("explore: %v", err)
	}
	if !result.Personalized {
		t.Fatal("expected personalized explore sections")
	}
	if result.Featured == nil {
		t.Fatal("expected a featured recommendation")
	}
	codes := make(map[string]bool)
	for _, collection := range result.Collections {
		if collection.Reason == nil {
			t.Fatalf("expected a section reason: %+v", collection)
		}
		codes[collection.Reason.Code] = true
		if len(collection.Videos) == 0 {
			t.Fatalf("empty collection: %+v", collection)
		}
	}
	for _, code := range []string{
		"recently_watched",
		"because_you_watched",
		"category",
		"subscription",
	} {
		if !codes[code] {
			t.Fatalf("missing %s section: %+v", code, codes)
		}
	}
}

func TestInvalidateActor(t *testing.T) {
	st := newDiscoveryStore(t)
	engine := newDiscoveryEngine(st)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := st.StartView(ctx, actorViewer("usr_demo"), "vid_even_keel", "watch", now); err != nil {
		t.Fatalf("view: %v", err)
	}
	if err := engine.RefreshAllVideos(ctx); err != nil {
		t.Fatalf("refresh embeddings: %v", err)
	}
	if _, err := engine.Feed(ctx, "usr_demo", 20, 0, "video", ""); err != nil {
		t.Fatalf("feed: %v", err)
	}
	_, _, found, err := st.GetActorEmbedding(ctx, "usr_demo", "hash-embedder")
	if err != nil {
		t.Fatalf("get actor embedding: %v", err)
	}
	if !found {
		t.Fatal("expected a cached actor embedding")
	}
	if err := engine.InvalidateActor(ctx, "usr_demo"); err != nil {
		t.Fatalf("invalidate actor: %v", err)
	}
	_, _, found, err = st.GetActorEmbedding(ctx, "usr_demo", "hash-embedder")
	if err != nil {
		t.Fatalf("get actor embedding after invalidate: %v", err)
	}
	if found {
		t.Fatal("expected the cached actor embedding to be deleted")
	}
	if _, err := engine.Feed(ctx, "usr_demo", 20, 0, "video", ""); err != nil {
		t.Fatalf("feed after invalidate: %v", err)
	}
}
