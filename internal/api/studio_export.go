package api

import (
	"net/http"
	"strings"
	"time"
)

var uploadStatuses = map[string]struct{}{
	"pending":    {},
	"uploading":  {},
	"scanning":   {},
	"processing": {},
	"ready":      {},
	"blocked":    {},
	"failed":     {},
	"aborted":    {},
	"completed":  {},
}

func (s *Server) listStudioUploads(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultStudioLimit, maxStudioLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" {
		if _, ok := uploadStatuses[status]; !ok {
			writeStudioError(w, &validationError{Field: "status", Message: "status is invalid"})
			return
		}
	}
	uploads, total, err := s.store.ListUploads(r.Context(), channel.ID, status, limit, offset)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(uploads, limit, offset, total))
}

func (s *Server) studioComments(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultStudioLimit, maxStudioLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	comments, total, err := s.store.ListStudioComments(r.Context(), channel.ID, limit, offset)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(comments, limit, offset, total))
}

func (s *Server) pinStudioComment(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	comment, err := s.store.SetCommentPinned(
		r.Context(), channel.ID, r.PathValue("id"), true, time.Now().UTC(),
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) unpinStudioComment(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	comment, err := s.store.SetCommentPinned(
		r.Context(), channel.ID, r.PathValue("id"), false, time.Now().UTC(),
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) removeStudioComment(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if err := s.store.RemoveComment(
		r.Context(), channel.ID, r.PathValue("id"), time.Now().UTC(),
	); err != nil {
		writeStudioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
