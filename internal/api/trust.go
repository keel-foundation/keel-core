package api

import (
	"errors"
	"net/http"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

func (s *Server) videoTrustRecord(w http.ResponseWriter, r *http.Request) {
	video, err := s.store.GetVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.publicRecordAccess(w, r, video) {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.PublicVideoRecord(r.Context(), video.ID)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) publicRecordAccess(w http.ResponseWriter, r *http.Request, video store.Video) bool {
	if video.Status == "removed" || isPublicVideo(video) ||
		((video.Status == "published" || video.Status == "demonetized") && video.Visibility == "unlisted") {
		return true
	}
	principal, err := s.auth.Authenticate(r)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"detail": "Internal server error",
			})
			return false
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return false
	}
	actorID, err := s.store.GetChannelActorID(r.Context(), video.ChannelID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if actorID != principal.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return false
	}
	return true
}
