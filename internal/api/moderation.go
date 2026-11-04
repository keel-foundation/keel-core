package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	defaultModerationLimit       = 50
	maxModerationLimit           = 100
	maxModerationStatementLength = 4000
)

var moderationCaseStatuses = map[string]struct{}{
	"open":      {},
	"reviewing": {},
	"decided":   {},
	"appealed":  {},
	"closed":    {},
}

var moderationActionStatuses = map[string]struct{}{
	"active":   {},
	"reversed": {},
	"expired":  {},
}

var moderationAppealStatuses = map[string]struct{}{
	"pending": {},
	"decided": {},
}

var moderationAppealGrounds = map[string]struct{}{
	"new_evidence":          {},
	"procedural_error":      {},
	"policy_misapplication": {},
	"identity":              {},
	"other":                 {},
}

type moderationAppealRequest struct {
	Grounds   string `json:"grounds"`
	Statement string `json:"statement"`
}

func moderationIdentity(principal auth.Principal) moderation.Identity {
	return moderation.Identity{ActorID: principal.ID, Role: principal.Role}
}

func (s *Server) requireModeration(w http.ResponseWriter) (moderation.Service, bool) {
	if s.moderation == nil || !s.moderation.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Moderation service is not configured.",
		})
		return nil, false
	}
	return s.moderation, true
}

func (s *Server) moderationSummary(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.Summary(r.Context(), moderationIdentity(principal), channel.ID)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationCases(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	status, limit, offset, err := moderationListParams(r, moderationCaseStatuses)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	response, err := service.ListCases(r.Context(), moderationIdentity(principal), channel.ID, status, limit, offset)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationCase(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.GetCase(r.Context(), moderationIdentity(principal), channel.ID, r.PathValue("id"))
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationActions(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	status, limit, offset, err := moderationListParams(r, moderationActionStatuses)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	response, err := service.ListActions(r.Context(), moderationIdentity(principal), channel.ID, status, limit, offset)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationAction(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.GetAction(r.Context(), moderationIdentity(principal), channel.ID, r.PathValue("id"))
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationAppeals(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	status, limit, offset, err := moderationListParams(r, moderationAppealStatuses)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	response, err := service.ListAppeals(r.Context(), moderationIdentity(principal), channel.ID, status, limit, offset)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationAppeal(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.GetAppeal(r.Context(), moderationIdentity(principal), channel.ID, r.PathValue("id"))
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationLedger(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultModerationLimit, maxModerationLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	response, err := service.Ledger(r.Context(), moderationIdentity(principal), channel.ID, limit, offset)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationLedgerVerify(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	response, err := service.VerifyLedger(r.Context(), moderationIdentity(principal), channel.ID)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationLedgerExport(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format == "" {
		format = "jsonl"
	}
	if format != "jsonl" && format != "csv" {
		writeStudioError(w, &validationError{
			Field:   "format",
			Message: "format must be jsonl or csv",
		})
		return
	}
	response, err := service.ExportLedger(r.Context(), moderationIdentity(principal), channel.ID, format)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func (s *Server) moderationFileAppeal(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireModeration(w)
	if !ok {
		return
	}
	var body moderationAppealRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	if err := validateModerationAppeal(body); err != nil {
		writeStudioError(w, err)
		return
	}
	response, err := service.FileAppeal(
		r.Context(),
		moderationIdentity(principal),
		channel.ID,
		r.PathValue("id"),
		body.Grounds,
		strings.TrimSpace(body.Statement),
	)
	if err != nil {
		writeModerationError(w, err)
		return
	}
	writeModerationResponse(w, response)
}

func moderationListParams(
	r *http.Request,
	allowed map[string]struct{},
) (string, int, int, error) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" {
		if _, ok := allowed[status]; !ok {
			return "", 0, 0, &validationError{
				Field:   "status",
				Message: "status is invalid",
			}
		}
	}
	limit, err := queryLimit(r, "limit", defaultModerationLimit, maxModerationLimit)
	if err != nil {
		return "", 0, 0, err
	}
	offset, err := queryOffset(r)
	if err != nil {
		return "", 0, 0, err
	}
	return status, limit, offset, nil
}

func validateModerationAppeal(body moderationAppealRequest) error {
	if _, ok := moderationAppealGrounds[body.Grounds]; !ok {
		return &validationError{
			Field:   "grounds",
			Message: "grounds is invalid",
		}
	}
	statement := strings.TrimSpace(body.Statement)
	length := len([]rune(statement))
	if length == 0 || length > maxModerationStatementLength {
		return &validationError{
			Field:   "statement",
			Message: "statement must be between 1 and 4000 characters",
		}
	}
	if store.HasControlCharacters(statement) {
		return &validationError{
			Field:   "statement",
			Message: "statement contains unsupported control characters",
		}
	}
	return nil
}

func writeModerationResponse(w http.ResponseWriter, response *moderation.Response) {
	if response.Header != nil {
		for _, name := range []string{"Content-Type", "Content-Disposition"} {
			if value := response.Header.Get(name); value != "" {
				w.Header().Set(name, value)
			}
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(response.Body)
}

func writeModerationError(w http.ResponseWriter, err error) {
	var upstream *moderation.Error
	if errors.As(err, &upstream) {
		switch {
		case upstream.StatusCode == http.StatusUnauthorized:
			log.Printf("moderation service rejected the gateway credential: %s", upstream.Detail)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"detail": "Moderation service credentials are invalid.",
			})
		case upstream.StatusCode >= 400 && upstream.StatusCode < 500:
			writeJSON(w, upstream.StatusCode, map[string]string{
				"detail": upstream.Detail,
			})
		default:
			log.Printf("moderation service failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"detail": "Moderation service is unavailable.",
			})
		}
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"detail": "Moderation service timed out.",
		})
		return
	}
	log.Printf("moderation request failed: %v", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{
		"detail": "Moderation service is unavailable.",
	})
}
