package notifications_test

import (
	"context"
	"testing"

	"github.com/keel-foundation/keel-core/internal/notifications"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestUploadFailedNotification(t *testing.T) {
	conn := testutil.Open(t)
	st := store.New(conn)
	ctx := context.Background()
	if err := seed.Run(ctx, st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := notifications.New(st)

	if err := service.UploadFailed(ctx, "vid_demo_001", "transcode failed"); err != nil {
		t.Fatalf("failure notification: %v", err)
	}
	if err := service.UploadFailed(ctx, "vid_demo_001", "transcode failed"); err != nil {
		t.Fatalf("duplicate failure notification: %v", err)
	}

	items, total, err := st.ListNotifications(ctx, "usr_demo", false, 20, 0)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one deduplicated failure notification: total=%d %+v", total, items)
	}
	item := items[0]
	if item.Kind != "upload.failed" || item.Href != "/studio/videos/vid_demo_001/edit" {
		t.Fatalf("unexpected failure notification: %+v", item)
	}
	if item.Body != "The Last Lighthouse could not be processed: transcode failed" {
		t.Fatalf("unexpected failure body: %+v", item)
	}
}

func TestVideoPublishedNotification(t *testing.T) {
	conn := testutil.Open(t)
	st := store.New(conn)
	ctx := context.Background()
	if err := seed.Run(ctx, st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := notifications.New(st)

	if err := service.VideoPublished(ctx, "vid_demo_001"); err != nil {
		t.Fatalf("publish notification: %v", err)
	}
	if err := service.VideoPublished(ctx, "vid_demo_001"); err != nil {
		t.Fatalf("duplicate publish notification: %v", err)
	}

	items, total, err := st.ListNotifications(ctx, "usr_demo", false, 20, 0)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one deduplicated notification: total=%d %+v", total, items)
	}
	item := items[0]
	if item.Kind != "upload.published" || item.Href != "/studio/videos/vid_demo_001/edit" {
		t.Fatalf("unexpected published notification: %+v", item)
	}
	if item.ChannelID != "ch_demo" || item.Read {
		t.Fatalf("unexpected published notification scope: %+v", item)
	}
}
