package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newCatalogStore(t *testing.T) *store.Store {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:8081", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st
}

func TestCatalogPublicVideoList(t *testing.T) {
	st := newCatalogStore(t)
	videos, total, err := st.ListVideos(context.Background(), store.VideoFilter{
		PublicOnly: true,
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list videos: %v", err)
	}
	if total != 8 || len(videos) != 8 {
		t.Fatalf("expected 8 public videos, got total=%d len=%d", total, len(videos))
	}
	if videos[0].ID != "vid_short_export" {
		t.Fatalf("expected newest video first, got %s", videos[0].ID)
	}
	for _, video := range videos {
		if video.Status != "published" || video.Visibility != "public" {
			t.Fatalf("unexpected public video: %+v", video)
		}
	}
}

func TestCatalogVideoFilters(t *testing.T) {
	st := newCatalogStore(t)
	ctx := context.Background()

	videos, total, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Format:     "video",
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list videos: %v", err)
	}
	if total != 5 || len(videos) != 5 {
		t.Fatalf("expected 5 videos, got total=%d len=%d", total, len(videos))
	}

	shorts, total, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Format:     "short",
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list shorts: %v", err)
	}
	if total != 3 || len(shorts) != 3 {
		t.Fatalf("expected 3 shorts, got total=%d len=%d", total, len(shorts))
	}

	protocol, total, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Categories: []string{"Protocol"},
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list protocol videos: %v", err)
	}
	if total != 2 || len(protocol) != 2 {
		t.Fatalf("expected 2 protocol videos, got total=%d len=%d", total, len(protocol))
	}

	channelVideos, total, err := st.ListVideos(ctx, store.VideoFilter{
		ChannelID:  "ch_keel_lab",
		PublicOnly: true,
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list channel videos: %v", err)
	}
	if total != 4 || len(channelVideos) != 4 {
		t.Fatalf("expected 4 channel videos, got total=%d len=%d", total, len(channelVideos))
	}

	demoVideos, total, err := st.ListVideos(ctx, store.VideoFilter{
		ChannelID:  "ch_demo",
		PublicOnly: true,
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list demo channel videos: %v", err)
	}
	if total != 1 || len(demoVideos) != 1 || demoVideos[0].ID != "vid_demo_001" {
		t.Fatalf("expected only the public demo video, got total=%d %+v", total, demoVideos)
	}

	multiCategory, total, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Categories: []string{"Protocol", "Moderation"},
		Sort:       "latest",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("list multi-category videos: %v", err)
	}
	if total != 4 || len(multiCategory) != 4 {
		t.Fatalf("expected 4 multi-category videos, got total=%d len=%d", total, len(multiCategory))
	}

	trending, _, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Sort:       "trending",
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("list trending videos: %v", err)
	}
	if len(trending) != 1 || trending[0].ID != "vid_demo_001" {
		t.Fatalf("unexpected trending video: %+v", trending)
	}

	page, total, err := st.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Sort:       "latest",
		Limit:      2,
		Offset:     2,
	})
	if err != nil {
		t.Fatalf("list paginated videos: %v", err)
	}
	if total != 8 || len(page) != 2 {
		t.Fatalf("expected total=8 len=2, got total=%d len=%d", total, len(page))
	}
}

func TestCatalogVideoFields(t *testing.T) {
	st := newCatalogStore(t)
	video, err := st.GetVideo(context.Background(), "vid_demo_001")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if video.PublicID != "ksKCMmvOxwQ" {
		t.Fatalf("unexpected public id: %+v", video)
	}
	if video.Format != "video" || video.Category != "Documentaries" {
		t.Fatalf("unexpected catalog fields: %+v", video)
	}
	byPublicID, err := st.GetVideo(context.Background(), "ksKCMmvOxwQ")
	if err != nil {
		t.Fatalf("get video by public id: %v", err)
	}
	if byPublicID.ID != video.ID {
		t.Fatalf("public id resolved to %s, expected %s", byPublicID.ID, video.ID)
	}
	if len(video.Tags) != 3 || video.Tags[1] != "lighthouse" {
		t.Fatalf("unexpected tags: %+v", video.Tags)
	}
	if video.Views != 1_842_000 || video.Likes != 182_400 || video.CommentsCount != 1_284 {
		t.Fatalf("unexpected counters: %+v", video)
	}
}

func TestCatalogChannelStats(t *testing.T) {
	st := newCatalogStore(t)
	ctx := context.Background()

	demo, err := st.GetChannel(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("get demo channel: %v", err)
	}
	if demo.Subscribers != 2_400_000 || !demo.Verified {
		t.Fatalf("unexpected demo channel: %+v", demo)
	}
	if demo.VideoCount != 1 || demo.TotalViews != 1_842_000 {
		t.Fatalf("unexpected demo channel stats: %+v", demo)
	}

	lab, err := st.GetChannel(ctx, "ch_keel_lab")
	if err != nil {
		t.Fatalf("get lab channel: %v", err)
	}
	if lab.VideoCount != 4 || lab.TotalViews != 1_151_000 {
		t.Fatalf("unexpected lab channel stats: %+v", lab)
	}
}

func TestCatalogSearch(t *testing.T) {
	st := newCatalogStore(t)
	ctx := context.Background()

	videos, total, err := st.SearchVideos(ctx, "lighthouse", "", 20, 0)
	if err != nil {
		t.Fatalf("search videos: %v", err)
	}
	if total != 1 || len(videos) != 1 || videos[0].ID != "vid_demo_001" {
		t.Fatalf("unexpected lighthouse results: total=%d %+v", total, videos)
	}

	keelVideos, total, err := st.SearchVideos(ctx, "keel", "", 20, 0)
	if err != nil {
		t.Fatalf("search keel videos: %v", err)
	}
	if total != 2 || len(keelVideos) != 2 {
		t.Fatalf("unexpected keel video results: total=%d %+v", total, keelVideos)
	}

	wildcard, total, err := st.SearchVideos(ctx, "%", "", 20, 0)
	if err != nil {
		t.Fatalf("search wildcard: %v", err)
	}
	if total != 0 || len(wildcard) != 0 {
		t.Fatalf("expected wildcard to be literal, got total=%d %+v", total, wildcard)
	}

	moderation, total, err := st.SearchVideos(ctx, "moderation", "", 20, 0)
	if err != nil {
		t.Fatalf("search moderation videos: %v", err)
	}
	if total != 2 || len(moderation) != 2 {
		t.Fatalf("unexpected moderation video results: total=%d %+v", total, moderation)
	}

	channels, total, err := st.SearchChannels(ctx, "keel", 20, 0)
	if err != nil {
		t.Fatalf("search channels: %v", err)
	}
	if total != 1 || len(channels) != 1 || channels[0].ID != "ch_keel_lab" {
		t.Fatalf("unexpected channel results: total=%d %+v", total, channels)
	}
	if channels[0].VideoCount != 4 || channels[0].TotalViews != 1_151_000 {
		t.Fatalf("unexpected channel search stats: %+v", channels[0])
	}
}

func TestCatalogRelatedVideos(t *testing.T) {
	st := newCatalogStore(t)
	ctx := context.Background()

	related, total, err := st.ListRelatedVideos(ctx, "vid_even_keel", "Protocol", 10, 0)
	if err != nil {
		t.Fatalf("related videos: %v", err)
	}
	if total != 7 || len(related) != 7 {
		t.Fatalf("expected 7 related videos, got total=%d len=%d", total, len(related))
	}
	if related[0].ID != "vid_short_keel" {
		t.Fatalf("expected category match first, got %s", related[0].ID)
	}
	for _, video := range related {
		if video.ID == "vid_even_keel" {
			t.Fatal("related videos must exclude the current video")
		}
	}

	fallback, _, err := st.ListRelatedVideos(ctx, "vid_demo_001", "Documentaries", 10, 0)
	if err != nil {
		t.Fatalf("related fallback: %v", err)
	}
	if len(fallback) == 0 || fallback[0].ID != "vid_even_keel" {
		t.Fatalf("expected most-viewed fallback, got %+v", fallback)
	}
}

func TestCatalogCategories(t *testing.T) {
	st := newCatalogStore(t)
	counts, err := st.ListCategoryCounts(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	expected := map[string]int{
		"Documentaries": 1,
		"Protocol":      2,
		"Moderation":    2,
		"Studio":        2,
		"Federation":    1,
	}
	if len(counts) != len(expected) {
		t.Fatalf("unexpected category count length: %d", len(counts))
	}
	for _, count := range counts {
		if count.VideoCount != expected[count.Name] {
			t.Fatalf("unexpected count for %s: %d", count.Name, count.VideoCount)
		}
	}
}

func TestCatalogChannelActorID(t *testing.T) {
	st := newCatalogStore(t)
	actorID, err := st.GetChannelActorID(context.Background(), "ch_demo")
	if err != nil {
		t.Fatalf("get channel actor: %v", err)
	}
	if actorID != "usr_demo" {
		t.Fatalf("unexpected actor id: %s", actorID)
	}
	if _, err := st.GetChannelActorID(context.Background(), "ch_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
