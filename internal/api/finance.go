package api

import (
	"net/http"
)

const (
	defaultFinanceLimit = 50
	maxFinanceLimit     = 100
)

func (s *Server) studioMonetization(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	monetization, err := s.store.GetMonetization(r.Context(), channel.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, monetization)
}

func (s *Server) studioEarnings(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultFinanceLimit, maxFinanceLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	earnings, total, err := s.store.ListEarnings(r.Context(), channel.ID, limit, offset)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(earnings, limit, offset, total))
}

func (s *Server) studioPayouts(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultFinanceLimit, maxFinanceLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	payouts, total, err := s.store.ListPayouts(r.Context(), channel.ID, limit, offset)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(payouts, limit, offset, total))
}
