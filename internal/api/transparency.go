package api

import (
	"net/http"
	"strings"
)

// transparencyReport proxies the public moderation transparency report. It
// carries no viewer session or moderator credential.
func (s *Server) transparencyReport(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	until := strings.TrimSpace(r.URL.Query().Get("until"))
	response, err := service.Transparency(r.Context(), since, until)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}
