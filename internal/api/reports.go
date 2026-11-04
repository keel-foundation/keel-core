package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/store"
)

const maxReportDetails = 4000

var reportReasons = map[string]struct{}{
	"spam":          {},
	"scam":          {},
	"piracy":        {},
	"impersonation": {},
	"harassment":    {},
	"csam":          {},
	"copyright":     {},
	"terrorism":     {},
	"other":         {},
}

type reportRequest struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Reason      string `json:"reason"`
	Details     string `json:"details"`
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	if s.moderation == nil || !s.moderation.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Moderation service is not configured.",
		})
		return
	}

	var body reportRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	subjectType := strings.TrimSpace(body.SubjectType)
	subjectID := strings.TrimSpace(body.SubjectID)
	reason := strings.TrimSpace(body.Reason)
	details := strings.TrimSpace(body.Details)

	if subjectType != "video" && subjectType != "channel" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "subject_type must be video or channel",
			"field":  "subject_type",
		})
		return
	}
	if subjectID == "" || len(subjectID) > 80 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "subject_id must be between 1 and 80 characters",
			"field":  "subject_id",
		})
		return
	}
	if _, ok := reportReasons[reason]; !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "reason is invalid",
			"field":  "reason",
		})
		return
	}
	if utf8.RuneCountInString(details) == 0 || utf8.RuneCountInString(details) > maxReportDetails {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "details must be between 1 and 4000 characters",
			"field":  "details",
		})
		return
	}
	if store.HasControlCharacters(details) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "details contains unsupported control characters",
			"field":  "details",
		})
		return
	}

	channelID := ""
	moderationSubjectID := subjectID
	if subjectType == "video" {
		video, err := s.store.GetVideo(r.Context(), subjectID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !s.ensureVideoAccess(w, r, video) {
			return
		}
		channelID = video.ChannelID
		moderationSubjectID = video.ID
	} else {
		channel, err := s.store.GetChannel(r.Context(), subjectID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		channelID = channel.ID
		moderationSubjectID = channel.ID
	}

	reporterID := ""
	if principal, ok := s.optionalPrincipal(r); ok {
		reporterID = principal.ID
	}
	now := time.Now().UTC()
	report, err := s.store.CreateReport(r.Context(), store.Report{
		SubjectType: subjectType,
		SubjectID:   moderationSubjectID,
		ChannelID:   channelID,
		Reason:      reason,
		Details:     details,
		ReporterID:  reporterID,
		Status:      "received",
		CreatedAt:   now,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	response, reportErr := s.moderation.Report(
		r.Context(),
		channelID,
		subjectType,
		moderationSubjectID,
		reason,
		details,
		reporterID,
	)
	if reportErr != nil {
		_, _ = s.store.UpdateReportOutcome(
			r.Context(), report.ID, "failed", "", "", reportErr.Error(), time.Now().UTC(),
		)
		if errors.Is(reportErr, moderation.ErrSignalTokenMissing) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"detail": "Report ingestion is not configured.",
			})
			return
		}
		writeModerationError(w, reportErr)
		return
	}

	caseID, signalID := moderationRecordIDs(response.Body)
	_, _ = s.store.UpdateReportOutcome(
		r.Context(), report.ID, "case_opened", caseID, signalID, "", time.Now().UTC(),
	)
	writeReportResponse(w, report.ID, "case_opened", response.Body)
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return
	}
	report, err := s.store.GetReport(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if report.ReporterID == "" || report.ReporterID != principal.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) listMyReports(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
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
	reports, total, err := s.store.ListReports(r.Context(), principal.ID, limit, offset)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(reports, limit, offset, total))
}

func moderationRecordIDs(body []byte) (string, string) {
	var payload struct {
		Case struct {
			ID string `json:"id"`
		} `json:"case"`
		Signal struct {
			ID string `json:"id"`
		} `json:"signal"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	return payload.Case.ID, payload.Signal.ID
}

func writeReportResponse(w http.ResponseWriter, reportID, status string, moderationBody []byte) {
	var payload map[string]any
	if err := json.Unmarshal(moderationBody, &payload); err != nil || payload == nil {
		payload = map[string]any{}
	}
	payload["report_id"] = reportID
	payload["status"] = status
	writeJSON(w, http.StatusCreated, payload)
}
