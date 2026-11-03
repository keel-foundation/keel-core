package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/store"
)

const visitorCookieName = "keeltube_visitor"

const visitorCookieTTL = 365 * 24 * time.Hour

type viewerIdentity struct {
	Key     string
	ActorID *string
}

type startViewRequest struct {
	Source string `json:"source"`
}

type viewProgressRequest struct {
	SessionID       string  `json:"session_id"`
	EventID         string  `json:"event_id"`
	PositionSeconds float64 `json:"position_seconds"`
	Completed       bool    `json:"completed"`
}

type startViewResponse struct {
	VideoID        string  `json:"video_id"`
	Views          int64   `json:"views"`
	SessionID      string  `json:"session_id"`
	Counted        bool    `json:"counted"`
	WatchedSeconds float64 `json:"watched_seconds"`
	Completed      bool    `json:"completed"`
}

type viewProgressResponse struct {
	VideoID        string  `json:"video_id"`
	SessionID      string  `json:"session_id"`
	Views          int64   `json:"views"`
	WatchedSeconds float64 `json:"watched_seconds"`
	WatchedDelta   float64 `json:"watched_delta_seconds"`
	Completed      bool    `json:"completed"`
}

type telemetryBatchRequest struct {
	Events []store.DiscoveryEventInput `json:"events"`
}

func (s *Server) startView(w http.ResponseWriter, r *http.Request) {
	setPrivateCache(w)
	viewer, ok := s.viewerIdentity(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	var request startViewRequest
	if err := decodeOptionalJSONBody(w, r, &request); err != nil {
		writeTelemetryError(w, err)
		return
	}
	result, err := s.store.StartView(
		r.Context(),
		store.Viewer{Key: viewer.Key, ActorID: viewer.ActorID},
		video.ID,
		request.Source,
		time.Now().UTC(),
	)
	if err != nil {
		writeTelemetryError(w, err)
		return
	}
	if viewer.ActorID != nil {
		s.invalidateDiscovery(*viewer.ActorID)
	}
	writeJSON(w, http.StatusOK, startViewResponse{
		VideoID:        video.ID,
		Views:          result.Views,
		SessionID:      result.Session.ID,
		Counted:        result.Session.Counted,
		WatchedSeconds: result.Session.WatchedSeconds,
		Completed:      result.Session.Completed,
	})
}

func (s *Server) progressView(w http.ResponseWriter, r *http.Request) {
	setPrivateCache(w)
	viewer, ok := s.viewerIdentity(w, r)
	if !ok {
		return
	}
	video, ok := s.engagementVideo(w, r)
	if !ok {
		return
	}
	var request viewProgressRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeTelemetryError(w, err)
		return
	}
	result, err := s.store.ProgressView(
		r.Context(),
		store.Viewer{Key: viewer.Key, ActorID: viewer.ActorID},
		video.ID,
		store.ViewProgressInput{
			SessionID:       request.SessionID,
			EventID:         request.EventID,
			PositionSeconds: request.PositionSeconds,
			Completed:       request.Completed,
		},
		time.Now().UTC(),
	)
	if err != nil {
		writeTelemetryError(w, err)
		return
	}
	if viewer.ActorID != nil && result.Completed {
		s.invalidateDiscovery(*viewer.ActorID)
	}
	writeJSON(w, http.StatusOK, viewProgressResponse{
		VideoID:        video.ID,
		SessionID:      result.Session.ID,
		Views:          result.Views,
		WatchedSeconds: result.Session.WatchedSeconds,
		WatchedDelta:   result.WatchedDelta,
		Completed:      result.Completed,
	})
}

func (s *Server) recordTelemetryEvents(w http.ResponseWriter, r *http.Request) {
	setPrivateCache(w)
	viewer, ok := s.viewerIdentity(w, r)
	if !ok {
		return
	}
	var request telemetryBatchRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeTelemetryError(w, err)
		return
	}
	if len(request.Events) == 0 || len(request.Events) > 100 {
		writeTelemetryError(w, &validationError{
			Field:   "events",
			Message: "events must contain between 1 and 100 entries",
		})
		return
	}
	for _, event := range request.Events {
		if strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 128 {
			writeTelemetryError(w, &validationError{
				Field:   "events.event_id",
				Message: "event_id must be between 1 and 128 characters",
			})
			return
		}
		if event.EventType != "impression" && event.EventType != "click" {
			writeTelemetryError(w, &validationError{
				Field:   "events.event_type",
				Message: "event_type must be impression or click",
			})
			return
		}
		if event.Source != "" && !store.ValidTrafficSource(event.Source) {
			writeTelemetryError(w, &validationError{
				Field:   "events.source",
				Message: "source is invalid",
			})
			return
		}
		if event.Position < 0 || event.Position > 10000 {
			writeTelemetryError(w, &validationError{
				Field:   "events.position",
				Message: "position must be between 0 and 10000",
			})
			return
		}
	}
	result, err := s.store.RecordDiscoveryEvents(
		r.Context(),
		store.Viewer{Key: viewer.Key, ActorID: viewer.ActorID},
		request.Events,
		time.Now().UTC(),
	)
	if err != nil {
		writeTelemetryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) viewerIdentity(w http.ResponseWriter, r *http.Request) (viewerIdentity, bool) {
	if s.auth != nil {
		principal, err := s.auth.Authenticate(r)
		if err == nil {
			actorID := principal.ID
			return viewerIdentity{Key: "actor:" + actorID, ActorID: &actorID}, true
		}
		if !errors.Is(err, auth.ErrUnauthorized) {
			log.Printf("telemetry auth failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"detail": "Internal server error",
			})
			return viewerIdentity{}, false
		}
		if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "A valid session is required.",
			})
			return viewerIdentity{}, false
		}
	}
	cfg := identity.DefaultConfig()
	if s.identity != nil {
		cfg = s.identity.Config()
	}
	visitorID := ""
	if cookie, err := r.Cookie(visitorCookieName); err == nil && validVisitorID(cookie.Value) {
		visitorID = cookie.Value
	}
	if visitorID == "" {
		var err error
		visitorID, err = newVisitorID()
		if err != nil {
			log.Printf("telemetry visitor id: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"detail": "Internal server error",
			})
			return viewerIdentity{}, false
		}
		http.SetCookie(w, &http.Cookie{
			Name:     visitorCookieName,
			Value:    visitorID,
			Path:     "/",
			Domain:   cfg.CookieDomain,
			MaxAge:   int(visitorCookieTTL.Seconds()),
			Expires:  time.Now().UTC().Add(visitorCookieTTL),
			HttpOnly: true,
			Secure:   cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return viewerIdentity{Key: "visitor:" + auth.HashToken(visitorID)}, true
}

func newVisitorID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "vis_" + hex.EncodeToString(buf), nil
}

func validVisitorID(value string) bool {
	if !strings.HasPrefix(value, "vis_") || len(value) != 36 {
		return false
	}
	_, err := hex.DecodeString(value[4:])
	return err == nil
}

func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return &validationError{Field: "body", Message: "invalid JSON body"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &validationError{Field: "body", Message: "request body must contain one JSON object"}
	}
	return nil
}

func writeTelemetryError(w http.ResponseWriter, err error) {
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
	case errors.Is(err, store.ErrInvalidTrafficSource):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"detail": "source is invalid",
		})
	case errors.Is(err, store.ErrInvalidTelemetry):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"detail": "Invalid telemetry event.",
		})
	default:
		log.Printf("telemetry request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
	}
}
