package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	defaultLibraryLimit      = 20
	maxLibraryLimit          = 50
	defaultCommentLimit      = 100
	maxCommentLimit          = 500
	defaultSubscriptionLimit = 20
	maxSubscriptionLimit     = 50
	maxCommentLength         = 2000
)

type subscriptionResponse struct {
	ChannelID   string `json:"channel_id"`
	Subscribed  bool   `json:"subscribed"`
	Subscribers int64  `json:"subscribers"`
}

type reactionRequest struct {
	Kind string `json:"kind"`
}

type reactionResponse struct {
	VideoID string  `json:"video_id"`
	Kind    *string `json:"kind"`
	Likes   int64   `json:"likes"`
}

type saveResponse struct {
	VideoID string `json:"video_id"`
	Saved   bool   `json:"saved"`
}

type commentRequest struct {
	Body     string `json:"body"`
	ParentID string `json:"parent_id,omitempty"`
}

type commentLikeResponse struct {
	CommentID string `json:"comment_id"`
	Liked     bool   `json:"liked"`
	Likes     int64  `json:"likes"`
}

func (s *Server) requirePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	setPrivateCache(w)
	if s.auth == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "A valid session is required.",
		})
		return auth.Principal{}, false
	}
	principal, err := s.auth.Authenticate(r)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "A valid session is required.",
			})
			return auth.Principal{}, false
		}
		log.Printf("engagement auth failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
		return auth.Principal{}, false
	}
	return principal, true
}

func (s *Server) optionalPrincipal(r *http.Request) (auth.Principal, bool) {
	if s.auth == nil {
		return auth.Principal{}, false
	}
	principal, err := s.auth.Authenticate(r)
	if err != nil {
		return auth.Principal{}, false
	}
	return principal, true
}

func (s *Server) invalidateDiscovery(actorID string) {
	if s.discovery == nil || actorID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.discovery.InvalidateActor(ctx, actorID); err != nil {
		log.Printf("discovery: invalidate actor %s: %v", actorID, err)
	}
}

func (s *Server) engagementVideo(w http.ResponseWriter, r *http.Request) (store.Video, bool) {
	video, err := s.store.GetVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return store.Video{}, false
	}
	if !s.ensureVideoAccess(w, r, video) {
		return store.Video{}, false
	}
	return video, true
}

func (s *Server) engagementComment(w http.ResponseWriter, r *http.Request) (store.Comment, bool) {
	comment, err := s.store.GetComment(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return store.Comment{}, false
	}
	video, err := s.store.GetVideo(r.Context(), comment.VideoID)
	if err != nil {
		s.writeStoreError(w, err)
		return store.Comment{}, false
	}
	if !s.ensureVideoAccess(w, r, video) {
		return store.Comment{}, false
	}
	return comment, true
}

func (s *Server) writeEngagementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "Conflict"})
	case errors.Is(err, store.ErrInvalidParent):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "Replies are limited to one level and must share the video.",
			"field":  "parent_id",
		})
	case errors.Is(err, store.ErrInvalidReaction):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "kind must be like or dislike",
			"field":  "kind",
		})
	case errors.Is(err, store.ErrInvalidLibraryTab):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "tab must be saved, liked, or history",
			"field":  "tab",
		})
	default:
		log.Printf("engagement request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
	}
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	subscribed, subscribers, err := s.store.Subscribe(
		r.Context(), principal.ID, channelID, time.Now().UTC(),
	)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, subscriptionResponse{
		ChannelID:   channelID,
		Subscribed:  subscribed,
		Subscribers: subscribers,
	})
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	subscribed, subscribers, err := s.store.Unsubscribe(r.Context(), principal.ID, channelID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, subscriptionResponse{
		ChannelID:   channelID,
		Subscribed:  subscribed,
		Subscribers: subscribers,
	})
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	channel, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	subscribed, err := s.store.IsSubscribed(r.Context(), principal.ID, channelID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionResponse{
		ChannelID:   channelID,
		Subscribed:  subscribed,
		Subscribers: channel.Subscribers,
	})
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultSubscriptionLimit, maxSubscriptionLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	channels, total, err := s.store.ListSubscribedChannels(r.Context(), principal.ID, limit, offset)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(channels, limit, offset, total))
}

func (s *Server) subscriptionFeed(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	filter, err := parseVideoFilter(r, "latest", "video", "", "")
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	filter.SubscribedBy = principal.ID
	videos, total, err := s.store.ListVideos(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(videos, filter.Limit, filter.Offset, total))
}

func (s *Server) setReaction(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	var body reactionRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind != "like" && kind != "dislike" {
		writeCatalogError(w, &validationError{
			Field:   "kind",
			Message: "kind must be like or dislike",
		})
		return
	}
	state, err := s.store.SetReaction(r.Context(), principal.ID, video.ID, kind, time.Now().UTC())
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, reactionResponse{
		VideoID: video.ID,
		Kind:    state.Kind,
		Likes:   state.Likes,
	})
}

func (s *Server) clearReaction(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	state, err := s.store.ClearReaction(r.Context(), principal.ID, video.ID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, reactionResponse{
		VideoID: video.ID,
		Kind:    state.Kind,
		Likes:   state.Likes,
	})
}

func (s *Server) getReaction(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	state, err := s.store.GetReaction(r.Context(), principal.ID, video.ID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reactionResponse{
		VideoID: video.ID,
		Kind:    state.Kind,
		Likes:   state.Likes,
	})
}

func (s *Server) saveVideo(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	saved, err := s.store.SaveVideo(r.Context(), principal.ID, video.ID, time.Now().UTC())
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, saveResponse{VideoID: video.ID, Saved: saved})
}

func (s *Server) unsaveVideo(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	saved, err := s.store.UnsaveVideo(r.Context(), principal.ID, video.ID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.invalidateDiscovery(principal.ID)
	writeJSON(w, http.StatusOK, saveResponse{VideoID: video.ID, Saved: saved})
}

func (s *Server) getSave(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	saved, err := s.store.IsSaved(r.Context(), principal.ID, video.ID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saveResponse{VideoID: video.ID, Saved: saved})
}

func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := s.store.ClearHistory(r.Context(), principal.ID); err != nil {
		s.writeEngagementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		tab = "saved"
	}
	if tab != "saved" && tab != "liked" && tab != "history" {
		writeCatalogError(w, &validationError{
			Field:   "tab",
			Message: "tab must be saved, liked, or history",
		})
		return
	}
	limit, err := queryLimit(r, "limit", defaultLibraryLimit, maxLibraryLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	videos, total, err := s.store.ListLibrary(r.Context(), principal.ID, tab, limit, offset)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(videos, limit, offset, total))
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sort == "" {
		sort = "top"
	}
	if sort != "top" && sort != "newest" {
		writeCatalogError(w, &validationError{
			Field:   "sort",
			Message: "sort must be top or newest",
		})
		return
	}
	limit, err := queryLimit(r, "limit", defaultCommentLimit, maxCommentLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	comments, total, err := s.store.ListComments(r.Context(), video.ID, sort, limit, offset)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	if isPublicVideo(video) {
		setPublicCache(w)
	} else {
		setPrivateCache(w)
	}
	writeJSON(w, http.StatusOK, newPage(comments, limit, offset, total))
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	var body commentRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Body)
	if text == "" {
		writeCatalogError(w, &validationError{
			Field:   "body",
			Message: "body is required",
		})
		return
	}
	if utf8.RuneCountInString(text) > maxCommentLength {
		writeCatalogError(w, &validationError{
			Field:   "body",
			Message: "body must be 2000 characters or fewer",
		})
		return
	}
	commentID, err := auth.NewID("cmt")
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	now := time.Now().UTC()
	var parentID *string
	if value := strings.TrimSpace(body.ParentID); value != "" {
		parentID = &value
	}
	comment, err := s.store.CreateComment(r.Context(), store.Comment{
		ID:        commentID,
		VideoID:   video.ID,
		ParentID:  parentID,
		ActorID:   principal.ID,
		Body:      text,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	s.notifyComment(r.Context(), comment, video, parentID)
	writeJSON(w, http.StatusCreated, comment)
}

func (s *Server) notifyComment(
	ctx context.Context,
	comment store.Comment,
	video store.Video,
	parentID *string,
) {
	if s.notifications == nil {
		return
	}
	var parent *store.Comment
	if parentID != nil {
		value, err := s.store.GetComment(ctx, *parentID)
		if err != nil {
			log.Printf("notifications: load parent comment %s: %v", *parentID, err)
			return
		}
		parent = &value
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.notifications.Comment(notifyCtx, comment, video, parent); err != nil {
		log.Printf("notifications: comment %s: %v", comment.ID, err)
	}
}

func (s *Server) likeComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	comment, ok := s.engagementComment(w, r)
	if !ok {
		return
	}
	likes, liked, err := s.store.LikeComment(
		r.Context(), comment.ID, principal.ID, time.Now().UTC(),
	)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commentLikeResponse{
		CommentID: comment.ID,
		Liked:     liked,
		Likes:     likes,
	})
}

func (s *Server) unlikeComment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	comment, ok := s.engagementComment(w, r)
	if !ok {
		return
	}
	likes, liked, err := s.store.UnlikeComment(r.Context(), comment.ID, principal.ID)
	if err != nil {
		s.writeEngagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commentLikeResponse{
		CommentID: comment.ID,
		Liked:     liked,
		Likes:     likes,
	})
}
