package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newEngagementStore(t *testing.T) *store.Store {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(
		context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", "",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st
}

func TestEngagementSubscriptions(t *testing.T) {
	st := newEngagementStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	channel, err := st.GetChannel(ctx, "ch_keel_lab")
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	base := channel.Subscribers

	subscribed, subscribers, err := st.Subscribe(ctx, "usr_demo", "ch_keel_lab", now)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !subscribed || subscribers != base+1 {
		t.Fatalf("unexpected subscribe result: subscribed=%v subscribers=%d", subscribed, subscribers)
	}

	subscribed, subscribers, err = st.Subscribe(ctx, "usr_demo", "ch_keel_lab", now)
	if err != nil {
		t.Fatalf("repeat subscribe: %v", err)
	}
	if !subscribed || subscribers != base+1 {
		t.Fatalf("repeat subscribe changed the count: subscribed=%v subscribers=%d", subscribed, subscribers)
	}

	isSubscribed, err := st.IsSubscribed(ctx, "usr_demo", "ch_keel_lab")
	if err != nil {
		t.Fatalf("is subscribed: %v", err)
	}
	if !isSubscribed {
		t.Fatal("expected a subscription")
	}

	channels, total, err := st.ListSubscribedChannels(ctx, "usr_demo", 20, 0)
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if total != 1 || len(channels) != 1 || channels[0].ID != "ch_keel_lab" {
		t.Fatalf("unexpected subscriptions: total=%d %+v", total, channels)
	}

	videos, total, err := st.ListVideos(ctx, store.VideoFilter{
		SubscribedBy: "usr_demo",
		PublicOnly:   true,
		Format:       "video",
		Sort:         "latest",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("subscription feed: %v", err)
	}
	if total != 2 || len(videos) != 2 {
		t.Fatalf("unexpected subscription feed: total=%d %+v", total, videos)
	}
	for _, video := range videos {
		if video.ChannelID != "ch_keel_lab" {
			t.Fatalf("unexpected subscription feed video: %+v", video)
		}
	}

	subscribed, subscribers, err = st.Unsubscribe(ctx, "usr_demo", "ch_keel_lab")
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if subscribed || subscribers != base {
		t.Fatalf("unexpected unsubscribe result: subscribed=%v subscribers=%d", subscribed, subscribers)
	}

	subscribed, subscribers, err = st.Unsubscribe(ctx, "usr_demo", "ch_keel_lab")
	if err != nil {
		t.Fatalf("repeat unsubscribe: %v", err)
	}
	if subscribed || subscribers != base {
		t.Fatalf("repeat unsubscribe changed the count: subscribed=%v subscribers=%d", subscribed, subscribers)
	}

	emptyFeed, total, err := st.ListVideos(ctx, store.VideoFilter{
		SubscribedBy: "usr_demo",
		PublicOnly:   true,
		Format:       "video",
		Sort:         "latest",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("empty subscription feed: %v", err)
	}
	if total != 0 || len(emptyFeed) != 0 {
		t.Fatalf("expected an empty subscription feed, got total=%d %+v", total, emptyFeed)
	}

	if _, _, err := st.Subscribe(ctx, "usr_demo", "ch_missing", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing channel: expected ErrNotFound, got %v", err)
	}
}

func TestEngagementReactions(t *testing.T) {
	st := newEngagementStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	video, err := st.GetVideo(ctx, "vid_even_keel")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	base := video.Likes

	state, err := st.SetReaction(ctx, "usr_demo", video.ID, "like", now)
	if err != nil {
		t.Fatalf("like: %v", err)
	}
	if state.Kind == nil || *state.Kind != "like" || state.Likes != base+1 {
		t.Fatalf("unexpected like state: %+v", state)
	}

	state, err = st.SetReaction(ctx, "usr_demo", video.ID, "like", now)
	if err != nil {
		t.Fatalf("repeat like: %v", err)
	}
	if state.Kind == nil || *state.Kind != "like" || state.Likes != base+1 {
		t.Fatalf("repeat like changed the count: %+v", state)
	}

	state, err = st.SetReaction(ctx, "usr_demo", video.ID, "dislike", now)
	if err != nil {
		t.Fatalf("dislike: %v", err)
	}
	if state.Kind == nil || *state.Kind != "dislike" || state.Likes != base {
		t.Fatalf("unexpected dislike state: %+v", state)
	}

	state, err = st.GetReaction(ctx, "usr_demo", video.ID)
	if err != nil {
		t.Fatalf("get reaction: %v", err)
	}
	if state.Kind == nil || *state.Kind != "dislike" || state.Likes != base {
		t.Fatalf("unexpected stored reaction: %+v", state)
	}

	state, err = st.ClearReaction(ctx, "usr_demo", video.ID)
	if err != nil {
		t.Fatalf("clear reaction: %v", err)
	}
	if state.Kind != nil || state.Likes != base {
		t.Fatalf("unexpected cleared reaction: %+v", state)
	}

	state, err = st.ClearReaction(ctx, "usr_demo", video.ID)
	if err != nil {
		t.Fatalf("repeat clear reaction: %v", err)
	}
	if state.Kind != nil || state.Likes != base {
		t.Fatalf("repeat clear changed the count: %+v", state)
	}

	if _, err := st.SetReaction(ctx, "usr_demo", video.ID, "bogus", now); !errors.Is(err, store.ErrInvalidReaction) {
		t.Fatalf("invalid reaction: expected ErrInvalidReaction, got %v", err)
	}
}

func TestEngagementLibrary(t *testing.T) {
	st := newEngagementStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	video, err := st.GetVideo(ctx, "vid_even_keel")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}

	saved, err := st.SaveVideo(ctx, "usr_demo", video.ID, now)
	if err != nil {
		t.Fatalf("save video: %v", err)
	}
	if !saved {
		t.Fatal("expected the video to be saved")
	}
	isSaved, err := st.IsSaved(ctx, "usr_demo", video.ID)
	if err != nil {
		t.Fatalf("is saved: %v", err)
	}
	if !isSaved {
		t.Fatal("expected a saved video")
	}
	videos, total, err := st.ListLibrary(ctx, "usr_demo", "saved", 20, 0)
	if err != nil {
		t.Fatalf("list saved: %v", err)
	}
	if total != 1 || len(videos) != 1 || videos[0].ID != video.ID {
		t.Fatalf("unexpected saved library: total=%d %+v", total, videos)
	}

	if _, err := st.SetReaction(ctx, "usr_demo", video.ID, "like", now); err != nil {
		t.Fatalf("like video: %v", err)
	}
	liked, total, err := st.ListLibrary(ctx, "usr_demo", "liked", 20, 0)
	if err != nil {
		t.Fatalf("list liked: %v", err)
	}
	if total != 1 || len(liked) != 1 || liked[0].ID != video.ID {
		t.Fatalf("unexpected liked library: total=%d %+v", total, liked)
	}

	actorID := "usr_demo"
	viewer := store.Viewer{Key: "actor:" + actorID, ActorID: &actorID}
	view, err := st.StartView(ctx, viewer, video.ID, "watch", now)
	if err != nil {
		t.Fatalf("record view: %v", err)
	}
	if view.Views != video.Views+1 || !view.Session.Counted {
		t.Fatalf("unexpected view result: %+v", view)
	}
	history, total, err := st.ListLibrary(ctx, "usr_demo", "history", 20, 0)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if total != 1 || len(history) != 1 || history[0].ID != video.ID {
		t.Fatalf("unexpected history library: total=%d %+v", total, history)
	}

	repeat, err := st.StartView(ctx, viewer, video.ID, "watch", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("repeat view: %v", err)
	}
	if repeat.Views != video.Views+1 || repeat.Session.Counted {
		t.Fatalf("expected deduplicated repeat view, got %+v", repeat)
	}
	_, total, err = st.ListLibrary(ctx, "usr_demo", "history", 20, 0)
	if err != nil {
		t.Fatalf("list history after repeat: %v", err)
	}
	if total != 1 {
		t.Fatalf("history should dedupe a video, got total=%d", total)
	}

	if err := st.ClearHistory(ctx, "usr_demo"); err != nil {
		t.Fatalf("clear history: %v", err)
	}
	_, total, err = st.ListLibrary(ctx, "usr_demo", "history", 20, 0)
	if err != nil {
		t.Fatalf("list history after clear: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected an empty history, got total=%d", total)
	}

	saved, err = st.UnsaveVideo(ctx, "usr_demo", video.ID)
	if err != nil {
		t.Fatalf("unsave video: %v", err)
	}
	if saved {
		t.Fatal("expected the video to be unsaved")
	}
	videos, total, err = st.ListLibrary(ctx, "usr_demo", "saved", 20, 0)
	if err != nil {
		t.Fatalf("list saved after unsave: %v", err)
	}
	if total != 0 || len(videos) != 0 {
		t.Fatalf("expected an empty saved library, got total=%d %+v", total, videos)
	}

	if _, _, err := st.ListLibrary(ctx, "usr_demo", "bogus", 20, 0); !errors.Is(err, store.ErrInvalidLibraryTab) {
		t.Fatalf("invalid tab: expected ErrInvalidLibraryTab, got %v", err)
	}
}

func TestEngagementComments(t *testing.T) {
	st := newEngagementStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	video, err := st.GetVideo(ctx, "vid_demo_001")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	other, err := st.GetVideo(ctx, "vid_even_keel")
	if err != nil {
		t.Fatalf("get other video: %v", err)
	}
	base := video.CommentsCount

	top, err := st.CreateComment(ctx, store.Comment{
		ID:        "cmt_test_top",
		VideoID:   video.ID,
		ActorID:   "usr_demo",
		Body:      "A specific comment.",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create top comment: %v", err)
	}
	if top.Author != "Mara Vale" || top.Handle != "@maravale" || top.ParentID != nil {
		t.Fatalf("unexpected top comment: %+v", top)
	}
	video, err = st.GetVideo(ctx, video.ID)
	if err != nil {
		t.Fatalf("get video after comment: %v", err)
	}
	if video.CommentsCount != base+1 {
		t.Fatalf("unexpected comments count: %d", video.CommentsCount)
	}

	reply, err := st.CreateComment(ctx, store.Comment{
		ID:        "cmt_test_reply",
		VideoID:   video.ID,
		ParentID:  &top.ID,
		ActorID:   "usr_keel_lab",
		Body:      "A reply.",
		CreatedAt: now.Add(time.Second),
		UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("create reply: %v", err)
	}
	if reply.ParentID == nil || *reply.ParentID != top.ID || reply.Author != "Keel Lab" {
		t.Fatalf("unexpected reply: %+v", reply)
	}

	if _, err := st.CreateComment(ctx, store.Comment{
		ID:        "cmt_test_reply_2",
		VideoID:   video.ID,
		ParentID:  &reply.ID,
		ActorID:   "usr_demo",
		Body:      "Too deep.",
		CreatedAt: now,
		UpdatedAt: now,
	}); !errors.Is(err, store.ErrInvalidParent) {
		t.Fatalf("third level: expected ErrInvalidParent, got %v", err)
	}

	if _, err := st.CreateComment(ctx, store.Comment{
		ID:        "cmt_test_cross",
		VideoID:   other.ID,
		ParentID:  &top.ID,
		ActorID:   "usr_demo",
		Body:      "Wrong video.",
		CreatedAt: now,
		UpdatedAt: now,
	}); !errors.Is(err, store.ErrInvalidParent) {
		t.Fatalf("cross-video reply: expected ErrInvalidParent, got %v", err)
	}

	comments, total, err := st.ListComments(ctx, video.ID, "newest", 20, 0)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if total != 2 || len(comments) != 2 {
		t.Fatalf("unexpected comments: total=%d %+v", total, comments)
	}
	if comments[0].ID != reply.ID || comments[1].ID != top.ID {
		t.Fatalf("unexpected newest order: %+v", comments)
	}

	likes, liked, err := st.LikeComment(ctx, top.ID, "usr_keel_lab", now)
	if err != nil {
		t.Fatalf("like comment: %v", err)
	}
	if !liked || likes != 1 {
		t.Fatalf("unexpected comment like: liked=%v likes=%d", liked, likes)
	}
	likes, liked, err = st.LikeComment(ctx, top.ID, "usr_keel_lab", now)
	if err != nil {
		t.Fatalf("repeat like comment: %v", err)
	}
	if !liked || likes != 1 {
		t.Fatalf("repeat like changed the count: liked=%v likes=%d", liked, likes)
	}
	likes, liked, err = st.UnlikeComment(ctx, top.ID, "usr_keel_lab")
	if err != nil {
		t.Fatalf("unlike comment: %v", err)
	}
	if liked || likes != 0 {
		t.Fatalf("unexpected comment unlike: liked=%v likes=%d", liked, likes)
	}
	likes, liked, err = st.UnlikeComment(ctx, top.ID, "usr_keel_lab")
	if err != nil {
		t.Fatalf("repeat unlike comment: %v", err)
	}
	if liked || likes != 0 {
		t.Fatalf("repeat unlike changed the count: liked=%v likes=%d", liked, likes)
	}

	if _, _, err := st.LikeComment(ctx, "cmt_missing", "usr_demo", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing comment: expected ErrNotFound, got %v", err)
	}
}
