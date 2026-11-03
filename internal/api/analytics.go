package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

const analyticsDateLayout = "2006-01-02"

type analyticsSummaryResponse struct {
	ChannelID string `json:"channel_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	store.AnalyticsSummary
}

type analyticsTimeseriesResponse struct {
	ChannelID string                 `json:"channel_id"`
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	Points    []store.AnalyticsPoint `json:"points"`
}

type videoAnalyticsResponse struct {
	ChannelID string                 `json:"channel_id"`
	VideoID   string                 `json:"video_id"`
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	Summary   store.AnalyticsSummary `json:"summary"`
	Points    []store.AnalyticsPoint `json:"points"`
}

func (s *Server) studioAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	from, to, err := parseAnalyticsRange(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	summary, err := s.store.StudioAnalyticsSummary(r.Context(), channel.ID, from, to)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analyticsSummaryResponse{
		ChannelID:        channel.ID,
		From:             from.Format(analyticsDateLayout),
		To:               to.Format(analyticsDateLayout),
		AnalyticsSummary: summary,
	})
}

func (s *Server) studioAnalyticsTimeseries(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	from, to, err := parseAnalyticsRange(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	points, err := s.store.StudioAnalyticsTimeseries(r.Context(), channel.ID, from, to)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analyticsTimeseriesResponse{
		ChannelID: channel.ID,
		From:      from.Format(analyticsDateLayout),
		To:        to.Format(analyticsDateLayout),
		Points:    points,
	})
}

func (s *Server) studioVideoAnalytics(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	from, to, err := parseAnalyticsRange(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	summary, points, err := s.store.StudioVideoAnalytics(
		r.Context(), channel.ID, video.ID, from, to,
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, videoAnalyticsResponse{
		ChannelID: channel.ID,
		VideoID:   video.ID,
		From:      from.Format(analyticsDateLayout),
		To:        to.Format(analyticsDateLayout),
		Summary:   summary,
		Points:    points,
	})
}

func parseAnalyticsRange(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -27)
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, err := time.Parse(analyticsDateLayout, raw)
		if err != nil {
			return time.Time{}, time.Time{}, &validationError{
				Field:   "to",
				Message: "to must use YYYY-MM-DD",
			}
		}
		to = parsed.UTC()
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, err := time.Parse(analyticsDateLayout, raw)
		if err != nil {
			return time.Time{}, time.Time{}, &validationError{
				Field:   "from",
				Message: "from must use YYYY-MM-DD",
			}
		}
		from = parsed.UTC()
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, &validationError{
			Field:   "from",
			Message: "from must be on or before to",
		}
	}
	if to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, &validationError{
			Field:   "from",
			Message: "the analytics range must be at most 366 days",
		}
	}
	return from, to, nil
}
