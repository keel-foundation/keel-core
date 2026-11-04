package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/keel-foundation/keel-core/internal/federation"
	"github.com/keel-foundation/keel-core/internal/store"
)

const maxFederationBody = 1 << 20

type federationFollowRequest struct {
	ActorURL string `json:"actor_url"`
}

type federationPeerStatusRequest struct {
	ActorURL string `json:"actor_url"`
	Status   string `json:"status"`
}

func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Federation is not enabled.",
		})
		return
	}
	document, err := s.federation.WebFinger(r.Context(), r.URL.Query().Get("resource"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/jrd+json; charset=utf-8", document)
}

func (s *Server) federationActor(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Federation is not enabled.",
		})
		return
	}
	document, err := s.federation.ActorDocument(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", document)
}

func (s *Server) federationOutbox(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	document, err := s.federation.Outbox(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", document)
}

func (s *Server) federationFollowers(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	document, err := s.federation.FollowersCollection(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", document)
}

func (s *Server) federationFollowing(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	document, err := s.federation.FollowingCollection(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", document)
}

func (s *Server) federationVideo(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	document, err := s.federation.VideoObject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", document)
}

func (s *Server) federationPolicy(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	writeDocument(w, "application/activity+json; charset=utf-8", s.federation.PolicyDocument())
}

func (s *Server) federationInbox(w http.ResponseWriter, r *http.Request) {
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFederationBody))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	status, err := s.federation.HandleInbox(r.Context(), r.PathValue("id"), r, body)
	if err != nil {
		if status == 0 {
			status = http.StatusBadRequest
		}
		writeFederationErrorStatus(w, status, err)
		return
	}
	w.WriteHeader(status)
}

func (s *Server) federationSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "A valid internal service token is required."})
		return
	}
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	snapshot, err := s.federation.Snapshot(r.Context())
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) federationFollow(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "A valid internal service token is required."})
		return
	}
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	var body federationFollowRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeFederationError(w, err)
		return
	}
	following, err := s.federation.FollowRemote(r.Context(), strings.TrimSpace(body.ActorURL))
	if err != nil {
		writeFederationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, following)
}

func (s *Server) federationUnfollow(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "A valid internal service token is required."})
		return
	}
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	var body federationFollowRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeFederationError(w, err)
		return
	}
	if err := s.federation.UnfollowRemote(r.Context(), strings.TrimSpace(body.ActorURL)); err != nil {
		writeFederationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) federationPeerStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "A valid internal service token is required."})
		return
	}
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	var body federationPeerStatusRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeFederationError(w, err)
		return
	}
	if err := s.federation.SetPeerStatus(r.Context(), strings.TrimSpace(body.ActorURL), body.Status); err != nil {
		writeFederationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) federationProcessDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "A valid internal service token is required."})
		return
	}
	if s.federation == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Federation is not enabled."})
		return
	}
	if err := s.federation.ProcessDeliveriesNow(r.Context()); err != nil {
		writeFederationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeDocument(w http.ResponseWriter, contentType string, document any) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func writeFederationError(w http.ResponseWriter, err error) {
	writeFederationErrorStatus(w, federationStatus(err), err)
}

func federationStatus(err error) int {
	switch {
	case errors.Is(err, federation.ErrActorNotFound):
		return http.StatusNotFound
	case errors.Is(err, federation.ErrBlockedPeer):
		return http.StatusForbidden
	case errors.Is(err, federation.ErrRemoteUnavailable):
		return http.StatusBadGateway
	case errors.Is(err, federation.ErrDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusUnprocessableEntity
	}
}

func writeFederationErrorStatus(w http.ResponseWriter, status int, err error) {
	if status == http.StatusInternalServerError {
		log.Printf("federation request failed: %v", err)
		writeJSON(w, status, map[string]string{"detail": "Internal server error"})
		return
	}
	detail := err.Error()
	switch {
	case status == http.StatusUnauthorized:
		detail = "A valid HTTP signature is required."
	case status == http.StatusForbidden:
		detail = "Federation access is forbidden."
	case status >= 500:
		detail = "The federation operation could not be completed."
	}
	writeJSON(w, status, map[string]string{"detail": detail})
}
