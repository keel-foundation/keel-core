package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	maxPlaylistTitle       = 200
	maxPlaylistDescription = 5000
	defaultPlaylistLimit   = 20
	maxPlaylistLimit       = 100
)

type playlistRequest struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type playlistItemRequest struct {
	VideoID string `json:"video_id"`
}

func normalizePlaylistInput(body playlistRequest) (string, string, string, string, error) {
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = "playlist"
	}
	if kind != "playlist" && kind != "collection" {
		return "", "", "", "", &validationError{Field: "kind", Message: "kind must be playlist or collection"}
	}
	title := strings.TrimSpace(body.Title)
	if utf8.RuneCountInString(title) == 0 || utf8.RuneCountInString(title) > maxPlaylistTitle {
		return "", "", "", "", &validationError{Field: "title", Message: "title must be between 1 and 200 characters"}
	}
	description := strings.TrimSpace(body.Description)
	if utf8.RuneCountInString(description) > maxPlaylistDescription {
		return "", "", "", "", &validationError{Field: "description", Message: "description must be at most 5000 characters"}
	}
	if store.HasControlCharacters(title) || store.HasControlCharacters(description) {
		return "", "", "", "", &validationError{Field: "title", Message: "playlist contains unsupported control characters"}
	}
	visibility := strings.TrimSpace(body.Visibility)
	if visibility == "" {
		visibility = "private"
	}
	if !store.ValidVisibility(visibility) {
		return "", "", "", "", &validationError{Field: "visibility", Message: "visibility must be public, unlisted, or private"}
	}
	return kind, title, description, visibility, nil
}

func (s *Server) listMyPlaylists(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	playlists, err := s.store.ListPlaylists(r.Context(), principal.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": playlists})
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	var body playlistRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	kind, title, description, visibility, err := normalizePlaylistInput(body)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	playlist, err := s.store.CreatePlaylist(
		r.Context(), principal.ID, kind, title, description, visibility, time.Now().UTC(),
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, playlist)
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	playlist, err := s.store.GetPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if !s.playlistVisible(w, r, playlist) {
		return
	}
	writeJSON(w, http.StatusOK, playlist)
}

func (s *Server) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	var body playlistRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	_, title, description, visibility, err := normalizePlaylistInput(body)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	playlist, err := s.store.UpdatePlaylist(
		r.Context(), r.PathValue("id"), principal.ID, title, description, visibility,
		time.Now().UTC(),
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlist)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePlaylist(r.Context(), r.PathValue("id"), principal.ID); err != nil {
		writeStudioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPlaylistItems(w http.ResponseWriter, r *http.Request) {
	playlist, err := s.store.GetPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if !s.playlistVisible(w, r, playlist) {
		return
	}
	limit, err := queryLimit(r, "limit", defaultPlaylistLimit, maxPlaylistLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	viewerID := ""
	if principal, ok := s.optionalPrincipal(r); ok {
		viewerID = principal.ID
	}
	items, total, err := s.store.ListPlaylistItems(
		r.Context(), playlist.ID, viewerID, limit, offset,
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(items, limit, offset, total))
}

func (s *Server) addPlaylistItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	var body playlistItemRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	videoID := strings.TrimSpace(body.VideoID)
	if videoID == "" {
		writeStudioError(w, &validationError{Field: "video_id", Message: "video_id is required"})
		return
	}
	playlist, err := s.store.AddPlaylistItem(
		r.Context(), r.PathValue("id"), principal.ID, videoID, time.Now().UTC(),
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlist)
}

func (s *Server) removePlaylistItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := s.store.RemovePlaylistItem(
		r.Context(), r.PathValue("id"), principal.ID, r.PathValue("video_id"), time.Now().UTC(),
	); err != nil {
		writeStudioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) playlistVisible(w http.ResponseWriter, r *http.Request, playlist store.Playlist) bool {
	if playlist.Visibility == "public" || playlist.Visibility == "unlisted" {
		return true
	}
	principal, ok := s.optionalPrincipal(r)
	if ok && principal.ID == playlist.ActorID {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	return false
}
