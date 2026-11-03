package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

func TestNotificationCreateListAndRead(t *testing.T) {
	st := newEngagementStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, _, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID: "", Kind: "comment.video", Title: "Invalid",
	}); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("invalid notification: expected ErrInvalidInput, got %v", err)
	}

	first, created, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID:   "usr_demo",
		ChannelID: "ch_demo",
		Kind:      "comment.video",
		Title:     "New comment on The Last Lighthouse",
		Body:      "Ada: a specific comment",
		Href:      "/watch/ksKCMmvOxwQ",
		DedupeKey: "comment:cmt_1:usr_demo",
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	if !created || first.ID == "" || first.Read || first.ChannelID != "ch_demo" {
		t.Fatalf("unexpected notification: %+v created=%v", first, created)
	}

	duplicate, created, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID:   "usr_demo",
		ChannelID: "ch_demo",
		Kind:      "comment.video",
		Title:     "New comment on The Last Lighthouse",
		DedupeKey: "comment:cmt_1:usr_demo",
		CreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("duplicate notification: %v", err)
	}
	if created || duplicate.ID != first.ID {
		t.Fatalf("expected the duplicate to return the existing row: %+v created=%v", duplicate, created)
	}

	second, created, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID:   "usr_demo",
		Kind:      "upload.published",
		Title:     "Your video is published",
		CreatedAt: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create second notification: %v", err)
	}
	if !created {
		t.Fatal("expected a new notification")
	}

	other, created, err := st.CreateNotification(ctx, store.NotificationInput{
		ActorID: "usr_keel_lab", Kind: "comment.video", Title: "Other creator",
		CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create other actor notification: %+v created=%v err=%v", other, created, err)
	}

	notifications, total, err := st.ListNotifications(ctx, "usr_demo", false, 20, 0)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if total != 2 || len(notifications) != 2 {
		t.Fatalf("unexpected notifications: total=%d %+v", total, notifications)
	}
	if notifications[0].ID != second.ID {
		t.Fatalf("expected newest notification first: %+v", notifications)
	}

	unread, err := st.CountUnreadNotifications(ctx, "usr_demo")
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if unread != 2 {
		t.Fatalf("unexpected unread count: %d", unread)
	}

	read, err := st.MarkNotificationRead(ctx, "usr_demo", first.ID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if !read.Read || read.ReadAt == nil {
		t.Fatalf("expected the notification to be read: %+v", read)
	}
	if _, err := st.MarkNotificationRead(ctx, "usr_demo", other.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-actor mark read: expected ErrNotFound, got %v", err)
	}
	unread, err = st.CountUnreadNotifications(ctx, "usr_demo")
	if err != nil {
		t.Fatalf("count unread after read: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unexpected unread count after read: %d", unread)
	}

	unreadOnly, total, err := st.ListNotifications(ctx, "usr_demo", true, 20, 0)
	if err != nil {
		t.Fatalf("list unread: %v", err)
	}
	if total != 1 || len(unreadOnly) != 1 || unreadOnly[0].ID != second.ID {
		t.Fatalf("unexpected unread list: total=%d %+v", total, unreadOnly)
	}

	updated, err := st.MarkAllNotificationsRead(ctx, "usr_demo", now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	if updated != 1 {
		t.Fatalf("unexpected updated count: %d", updated)
	}
	unread, err = st.CountUnreadNotifications(ctx, "usr_demo")
	if err != nil {
		t.Fatalf("count unread after mark all: %v", err)
	}
	if unread != 0 {
		t.Fatalf("expected zero unread, got %d", unread)
	}
}
