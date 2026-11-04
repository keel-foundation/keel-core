package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	defaultNotificationLimit = 20
	maxNotificationLimit     = 50
)

type notificationsResponse struct {
	Items       []store.Notification `json:"items"`
	Limit       int                  `json:"limit"`
	Offset      int                  `json:"offset"`
	Total       int                  `json:"total"`
	HasMore     bool                 `json:"has_more"`
	UnreadCount int                  `json:"unread_count"`
}

type unreadNotificationsResponse struct {
	UnreadCount int `json:"unread_count"`
}

type markAllNotificationsResponse struct {
	Updated     int64 `json:"updated"`
	UnreadCount int   `json:"unread_count"`
}

type notificationIngestRequest struct {
	ActorID   string          `json:"actor_id"`
	ChannelID string          `json:"channel_id,omitempty"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Href      string          `json:"href"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	DedupeKey string          `json:"dedupe_key,omitempty"`
	CreatedAt *time.Time      `json:"created_at,omitempty"`
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultNotificationLimit, maxNotificationLimit)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	unreadOnly := false
	if raw := strings.TrimSpace(r.URL.Query().Get("unread")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeNotificationError(w, &validationError{
				Field:   "unread",
				Message: "unread must be true or false",
			})
			return
		}
		unreadOnly = value
	}
	notifications, total, err := s.store.ListNotifications(
		r.Context(), principal.ID, unreadOnly, limit, offset,
	)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	unread, err := s.store.CountUnreadNotifications(r.Context(), principal.ID)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notificationsResponse{
		Items:       notifications,
		Limit:       limit,
		Offset:      offset,
		Total:       total,
		HasMore:     offset+len(notifications) < total,
		UnreadCount: unread,
	})
}

func (s *Server) unreadNotificationCount(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	unread, err := s.store.CountUnreadNotifications(r.Context(), principal.ID)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, unreadNotificationsResponse{UnreadCount: unread})
}

func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	notification, err := s.store.MarkNotificationRead(
		r.Context(), principal.ID, r.PathValue("id"), time.Now().UTC(),
	)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, notification)
}

func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	updated, err := s.store.MarkAllNotificationsRead(
		r.Context(), principal.ID, time.Now().UTC(),
	)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, markAllNotificationsResponse{
		Updated:     updated,
		UnreadCount: 0,
	})
}

func (s *Server) ingestNotification(w http.ResponseWriter, r *http.Request) {
	setPrivateCache(w)
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "A valid internal service token is required.",
		})
		return
	}
	if s.notifications == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Notifications are not configured.",
		})
		return
	}
	var body notificationIngestRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeNotificationError(w, err)
		return
	}
	if body.ChannelID != "" {
		channel, err := s.store.GetChannel(r.Context(), body.ChannelID)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"detail": "channel_id does not identify a known channel.",
				"field":  "channel_id",
			})
			return
		}
		if err != nil {
			writeNotificationError(w, err)
			return
		}
		if body.ActorID == "" {
			body.ActorID = channel.ActorID
		}
		if channel.ActorID != body.ActorID {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"detail": "channel_id is not owned by actor_id.",
				"field":  "channel_id",
			})
			return
		}
	}
	if body.ActorID == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "actor_id or channel_id is required.",
			"field":  "actor_id",
		})
		return
	}
	if _, err := s.store.GetActor(r.Context(), body.ActorID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "actor_id does not identify a known actor.",
			"field":  "actor_id",
		})
		return
	} else if err != nil {
		writeNotificationError(w, err)
		return
	}
	input := store.NotificationInput{
		ActorID:   body.ActorID,
		ChannelID: body.ChannelID,
		Kind:      body.Kind,
		Title:     body.Title,
		Body:      body.Body,
		Href:      body.Href,
		Payload:   body.Payload,
		DedupeKey: body.DedupeKey,
	}
	if body.CreatedAt != nil {
		input.CreatedAt = body.CreatedAt.UTC()
	}
	notification, created, err := s.notifications.Create(r.Context(), input)
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, notification)
}

func (s *Server) authorizeInternal(r *http.Request) bool {
	if s.internalToken == "" {
		return false
	}
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return auth.ConstantTimeEqual(strings.TrimSpace(token), s.internalToken)
}

func writeNotificationError(w http.ResponseWriter, err error) {
	var validation *validationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": validation.Message,
			"field":  validation.Field,
		})
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	case errors.Is(err, store.ErrInvalidInput):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": err.Error()})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "Conflict"})
	default:
		log.Printf("notification request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
	}
}
