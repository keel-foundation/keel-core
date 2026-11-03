package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestSeedAndReadBack(t *testing.T) {
	conn := testutil.Open(t)
	st := store.New(conn)
	ctx := context.Background()

	if err := seed.Run(ctx, st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	actor, err := st.GetActor(ctx, "usr_demo")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if actor.Handle != "@maravale" || actor.DisplayName != "Mara Vale" {
		t.Fatalf("unexpected actor: %+v", actor)
	}

	channel, err := st.GetChannel(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if channel.ActorID != actor.ID || channel.Name != "Mara Vale" {
		t.Fatalf("unexpected channel: %+v", channel)
	}
	if channel.Subscribers != 2_400_000 || !channel.Verified {
		t.Fatalf("unexpected channel catalog fields: %+v", channel)
	}
	if channel.VideoCount != 1 || channel.TotalViews != 1_842_000 {
		t.Fatalf("unexpected channel stats: %+v", channel)
	}

	byHandle, err := st.GetChannel(ctx, "@maravale")
	if err != nil {
		t.Fatalf("get channel by handle: %v", err)
	}
	if byHandle.ID != channel.ID {
		t.Fatalf("unexpected channel by handle: %+v", byHandle)
	}
	byBareHandle, err := st.GetChannel(ctx, "maravale")
	if err != nil {
		t.Fatalf("get channel by bare handle: %v", err)
	}
	if byBareHandle.ID != channel.ID {
		t.Fatalf("unexpected channel by bare handle: %+v", byBareHandle)
	}

	video, err := st.GetVideo(ctx, "vid_demo_001")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if video.Title != "The Last Lighthouse" || video.Status != "published" {
		t.Fatalf("unexpected video: %+v", video)
	}
	if video.PublicID != "ksKCMmvOxwQ" {
		t.Fatalf("unexpected public video id: %+v", video)
	}
	if video.Format != "video" || video.Category != "Documentaries" {
		t.Fatalf("unexpected video catalog fields: %+v", video)
	}
	if len(video.Tags) != 3 || video.Tags[0] != "documentary" {
		t.Fatalf("unexpected video tags: %+v", video.Tags)
	}
	if video.Views != 1_842_000 || video.Likes != 182_400 || video.CommentsCount != 1_284 {
		t.Fatalf("unexpected video counters: %+v", video)
	}
	if video.Playback.MP4URL != "http://localhost:3000/media/ksKCMmvOxwQ.mp4" {
		t.Fatalf("unexpected playback: %+v", video.Playback)
	}
	if video.Playback.CaptionsURL != "http://localhost:3000/media/mock-captions.vtt" {
		t.Fatalf("unexpected captions URL: %+v", video.Playback)
	}
	if video.Policy == nil || video.Policy.PolicyVersion != "demo-0.1" {
		t.Fatalf("unexpected policy: %+v", video.Policy)
	}
	if video.PublishedAt == nil {
		t.Fatal("expected published_at to be set")
	}

	removed, err := st.GetVideo(ctx, "vid_removed_001")
	if err != nil || removed.Status != "removed" {
		t.Fatalf("unexpected removed fixture: %+v, %v", removed, err)
	}
	private, err := st.GetVideo(ctx, "vid_private_001")
	if err != nil || private.Visibility != "private" {
		t.Fatalf("unexpected private fixture: %+v, %v", private, err)
	}

	if _, err := st.GetVideo(ctx, "vid_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := st.GetChannel(ctx, "ch_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
