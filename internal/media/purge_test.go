package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestPurgeVideoDeletesObjectsAndDatabaseRows(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.ArchiveStudioVideo(ctx, "ch_demo", "vid_demo_001", now); err != nil {
		t.Fatalf("archive: %v", err)
	}
	assets := []store.MediaAsset{
		{VideoID: "vid_demo_001", Kind: "mp4", ObjectKey: "media/ch_demo/vid_demo_001/video.mp4", ContentType: "video/mp4", UpdatedAt: now},
		{VideoID: "vid_demo_001", Kind: "hls", ObjectKey: "media/ch_demo/vid_demo_001/hls/", ContentType: "application/vnd.apple.mpegurl", UpdatedAt: now},
		{VideoID: "vid_demo_001", Kind: "thumbnail", ObjectKey: "media/ch_demo/vid_demo_001/thumbnail.jpg", ContentType: "image/jpeg", UpdatedAt: now},
	}
	for _, asset := range assets {
		if err := st.UpsertMediaAsset(ctx, asset); err != nil {
			t.Fatalf("asset %s: %v", asset.Kind, err)
		}
	}
	if _, err := st.CreateSubtitleTrack(ctx, store.SubtitleTrack{
		VideoID: "vid_demo_001", ChannelID: "ch_demo", Language: "en", Label: "English",
		Kind: "subtitles", ObjectKey: "media/ch_demo/vid_demo_001/subtitles/track.vtt",
		Status: "ready", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("subtitle: %v", err)
	}

	fake := &fakeStorage{}
	engine := NewWithStorage(st, fake, testMediaConfig(t))
	if err := engine.PurgeVideo(ctx, "ch_demo", "usr_demo", "vid_demo_001"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(fake.prefixes) != 1 || fake.prefixes[0] != "media/ch_demo/vid_demo_001/" {
		t.Fatalf("unexpected deleted prefixes: %v", fake.prefixes)
	}
	if _, err := st.GetVideo(ctx, "vid_demo_001"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected video to be gone, got %v", err)
	}
	if _, found, err := st.GetMediaAsset(ctx, "vid_demo_001", "mp4"); err != nil || found {
		t.Fatalf("expected no media asset, found=%v err=%v", found, err)
	}
	var purges int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_purges WHERE video_id = 'vid_demo_001'`,
	).Scan(&purges); err != nil {
		t.Fatalf("count purges: %v", err)
	}
	if purges != 1 {
		t.Fatalf("expected one purge audit row, got %d", purges)
	}
}

func TestPurgeVideoRequiresArchivedVideo(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	engine := NewWithStorage(st, &fakeStorage{}, testMediaConfig(t))
	err := engine.PurgeVideo(context.Background(), "ch_demo", "usr_demo", "vid_demo_001")
	if !errors.Is(err, store.ErrNotArchived) {
		t.Fatalf("expected ErrNotArchived, got %v", err)
	}
}

func TestPurgeVideoRejectsAnotherCreatorsVideo(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	engine := NewWithStorage(st, &fakeStorage{}, testMediaConfig(t))
	err := engine.PurgeVideo(context.Background(), "ch_other", "usr_other", "vid_demo_001")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
