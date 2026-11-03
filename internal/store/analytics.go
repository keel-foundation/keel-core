package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrInvalidTrafficSource = errors.New("invalid traffic source")
	ErrInvalidTelemetry     = errors.New("invalid telemetry")
)

var TrafficSources = []string{
	"feed",
	"explore",
	"search",
	"channel",
	"watch",
	"shorts",
	"external",
	"unknown",
}

func ValidTrafficSource(source string) bool {
	for _, value := range TrafficSources {
		if value == source {
			return true
		}
	}
	return false
}

func NormalizeTrafficSource(source string) (string, error) {
	if source == "" {
		return "unknown", nil
	}
	if !ValidTrafficSource(source) {
		return "", ErrInvalidTrafficSource
	}
	return source, nil
}

type Viewer struct {
	Key     string
	ActorID *string
}

type ViewSession struct {
	ID                  string     `json:"id"`
	VideoID             string     `json:"video_id"`
	ChannelID           string     `json:"channel_id"`
	ActorID             *string    `json:"actor_id,omitempty"`
	ViewerKey           string     `json:"-"`
	Source              string     `json:"source"`
	StartedAt           time.Time  `json:"started_at"`
	LastEventAt         time.Time  `json:"last_event_at"`
	LastPositionSeconds float64    `json:"last_position_seconds"`
	MaxPositionSeconds  float64    `json:"max_position_seconds"`
	WatchedSeconds      float64    `json:"watched_seconds"`
	DurationSeconds     int        `json:"duration_seconds"`
	Completed           bool       `json:"completed"`
	Counted             bool       `json:"counted"`
	EndedAt             *time.Time `json:"ended_at,omitempty"`
}

type ViewStartResult struct {
	Session ViewSession `json:"session"`
	Views   int64       `json:"views"`
}

type ViewProgressInput struct {
	SessionID       string  `json:"session_id"`
	EventID         string  `json:"event_id"`
	PositionSeconds float64 `json:"position_seconds"`
	Completed       bool    `json:"completed"`
}

type ViewProgressResult struct {
	Session      ViewSession `json:"session"`
	Views        int64       `json:"views"`
	WatchedDelta float64     `json:"watched_delta_seconds"`
	Completed    bool        `json:"completed"`
}

type DiscoveryEventInput struct {
	EventID   string `json:"event_id"`
	VideoID   string `json:"video_id"`
	EventType string `json:"event_type"`
	Source    string `json:"source"`
	Position  int    `json:"position"`
}

type DiscoveryEventResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Rejected   int `json:"rejected"`
}

type AnalyticsSource struct {
	Source       string  `json:"source"`
	Views        int64   `json:"views"`
	Sessions     int64   `json:"sessions"`
	WatchSeconds float64 `json:"watch_seconds"`
	WatchHours   float64 `json:"watch_hours"`
	Completions  int64   `json:"completions"`
	Impressions  int64   `json:"impressions"`
	Clicks       int64   `json:"clicks"`
}

type AnalyticsSummary struct {
	Views            int64             `json:"views"`
	Sessions         int64             `json:"sessions"`
	WatchSeconds     float64           `json:"watch_seconds"`
	WatchHours       float64           `json:"watch_hours"`
	Completions      int64             `json:"completions"`
	CompletionRate   float64           `json:"completion_rate"`
	Impressions      int64             `json:"impressions"`
	Clicks           int64             `json:"clicks"`
	ClickThroughRate float64           `json:"click_through_rate"`
	Sources          []AnalyticsSource `json:"sources"`
}

type AnalyticsPoint struct {
	Day          string  `json:"day"`
	Views        int64   `json:"views"`
	Sessions     int64   `json:"sessions"`
	WatchSeconds float64 `json:"watch_seconds"`
	WatchHours   float64 `json:"watch_hours"`
	Completions  int64   `json:"completions"`
	Impressions  int64   `json:"impressions"`
	Clicks       int64   `json:"clicks"`
}

type TelemetryPruneResult struct {
	Sessions   int64 `json:"sessions"`
	ViewEvents int64 `json:"view_events"`
	Discovery  int64 `json:"discovery_events"`
}

func (s *Store) StartView(ctx context.Context, viewer Viewer, videoID, source string, at time.Time) (ViewStartResult, error) {
	if viewer.Key == "" {
		return ViewStartResult{}, fmt.Errorf("%w: viewer key is required", ErrInvalidTelemetry)
	}
	source, err := NormalizeTrafficSource(source)
	if err != nil {
		return ViewStartResult{}, err
	}
	sessionID, err := NewID("vws_")
	if err != nil {
		return ViewStartResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ViewStartResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		viewer.Key+":"+videoID,
	); err != nil {
		return ViewStartResult{}, err
	}

	var channelID string
	var durationSeconds int
	if err := tx.QueryRowContext(ctx, `
		SELECT channel_id, duration_seconds FROM videos WHERE id = $1
	`, videoID).Scan(&channelID, &durationSeconds); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ViewStartResult{}, ErrNotFound
		}
		return ViewStartResult{}, err
	}

	var prior bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM video_view_sessions
			WHERE viewer_key = $1 AND video_id = $2 AND started_at > $3
		)
	`, viewer.Key, videoID, at.Add(-30*time.Minute)).Scan(&prior); err != nil {
		return ViewStartResult{}, err
	}
	counted := !prior
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO video_view_sessions (
			id, video_id, channel_id, actor_id, viewer_key, source, started_at,
			last_event_at, last_position_seconds, max_position_seconds, watched_seconds,
			duration_seconds, completed, counted, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 0, 0, 0, $8, FALSE, $9, $7)
	`, sessionID, videoID, channelID, viewer.ActorID, viewer.Key, source, at,
		durationSeconds, counted,
	); err != nil {
		return ViewStartResult{}, err
	}

	if viewer.ActorID != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO watch_history (actor_id, video_id, watched_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (actor_id, video_id) DO UPDATE SET watched_at = EXCLUDED.watched_at
		`, *viewer.ActorID, videoID, at); err != nil {
			return ViewStartResult{}, err
		}
	}

	if !counted {
		if err := upsertDailyAnalytics(ctx, tx, videoID, channelID, at, source, 0, 1, 0, 0, 0, 0); err != nil {
			return ViewStartResult{}, err
		}
	}

	var views int64
	if counted {
		if err := tx.QueryRowContext(ctx, `
			UPDATE videos SET views = views + 1 WHERE id = $1 RETURNING views
		`, videoID).Scan(&views); err != nil {
			return ViewStartResult{}, err
		}
		if err := upsertDailyAnalytics(ctx, tx, videoID, channelID, at, source, 1, 1, 0, 0, 0, 0); err != nil {
			return ViewStartResult{}, err
		}
	} else if err := tx.QueryRowContext(ctx,
		`SELECT views FROM videos WHERE id = $1`, videoID,
	).Scan(&views); err != nil {
		return ViewStartResult{}, err
	}

	session, err := getViewSession(ctx, tx, sessionID, videoID, viewer.Key, false)
	if err != nil {
		return ViewStartResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ViewStartResult{}, err
	}
	return ViewStartResult{Session: session, Views: views}, nil
}

func (s *Store) ProgressView(ctx context.Context, viewer Viewer, videoID string, input ViewProgressInput, at time.Time) (ViewProgressResult, error) {
	if viewer.Key == "" || input.SessionID == "" || input.EventID == "" || len(input.EventID) > 128 {
		return ViewProgressResult{}, fmt.Errorf("%w: invalid progress event", ErrInvalidTelemetry)
	}
	if math.IsNaN(input.PositionSeconds) || math.IsInf(input.PositionSeconds, 0) || input.PositionSeconds < 0 {
		return ViewProgressResult{}, fmt.Errorf("%w: invalid position", ErrInvalidTelemetry)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ViewProgressResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	session, err := getViewSession(ctx, tx, input.SessionID, videoID, viewer.Key, true)
	if err != nil {
		return ViewProgressResult{}, err
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM video_view_events WHERE session_id = $1 AND event_id = $2
		)
	`, session.ID, input.EventID).Scan(&duplicate); err != nil {
		return ViewProgressResult{}, err
	}
	if duplicate {
		var views int64
		if err := tx.QueryRowContext(ctx,
			`SELECT views FROM videos WHERE id = $1`, videoID,
		).Scan(&views); err != nil {
			return ViewProgressResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return ViewProgressResult{}, err
		}
		return ViewProgressResult{Session: session, Views: views, Completed: session.Completed}, nil
	}

	position := input.PositionSeconds
	if session.DurationSeconds > 0 && position > float64(session.DurationSeconds) {
		position = float64(session.DurationSeconds)
	}
	elapsed := at.Sub(session.LastEventAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	delta := position - session.LastPositionSeconds
	maxDelta := math.Max(10, elapsed*2)
	watchedDelta := 0.0
	if delta > 0 && delta <= maxDelta {
		watchedDelta = delta
	}
	if session.DurationSeconds > 0 {
		remaining := float64(session.DurationSeconds) - session.WatchedSeconds
		if remaining < 0 {
			remaining = 0
		}
		if watchedDelta > remaining {
			watchedDelta = remaining
		}
	}
	completed := session.Completed || input.Completed ||
		(session.DurationSeconds > 0 && position >= float64(session.DurationSeconds)-1)
	maxPosition := math.Max(session.MaxPositionSeconds, position)
	watchedSeconds := session.WatchedSeconds + watchedDelta
	var endedAt any
	if completed {
		endedAt = at
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE video_view_sessions
		SET last_event_at = $2, last_position_seconds = $3, max_position_seconds = $4,
		    watched_seconds = $5, completed = $6, ended_at = COALESCE(ended_at, $7)
		WHERE id = $1
	`, session.ID, at, position, maxPosition, watchedSeconds, completed, endedAt); err != nil {
		return ViewProgressResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO video_view_events (
			session_id, event_id, video_id, channel_id, position_seconds,
			watched_delta_seconds, completed, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, session.ID, input.EventID, session.VideoID, session.ChannelID, position,
		watchedDelta, completed, at,
	); err != nil {
		return ViewProgressResult{}, err
	}
	if watchedDelta > 0 {
		if err := upsertDailyAnalytics(ctx, tx, session.VideoID, session.ChannelID, at, session.Source, 0, 0, watchedDelta, 0, 0, 0); err != nil {
			return ViewProgressResult{}, err
		}
	}
	if completed && !session.Completed {
		if err := upsertDailyAnalytics(ctx, tx, session.VideoID, session.ChannelID, at, session.Source, 0, 0, 0, 1, 0, 0); err != nil {
			return ViewProgressResult{}, err
		}
	}

	var views int64
	if err := tx.QueryRowContext(ctx,
		`SELECT views FROM videos WHERE id = $1`, session.VideoID,
	).Scan(&views); err != nil {
		return ViewProgressResult{}, err
	}
	session.LastEventAt = at
	session.LastPositionSeconds = position
	session.MaxPositionSeconds = maxPosition
	session.WatchedSeconds = watchedSeconds
	session.Completed = completed
	if completed && session.EndedAt == nil {
		session.EndedAt = &at
	}
	if err := tx.Commit(); err != nil {
		return ViewProgressResult{}, err
	}
	return ViewProgressResult{
		Session: session, Views: views, WatchedDelta: watchedDelta, Completed: completed,
	}, nil
}

func (s *Store) RecordDiscoveryEvents(ctx context.Context, viewer Viewer, events []DiscoveryEventInput, at time.Time) (DiscoveryEventResult, error) {
	if viewer.Key == "" || len(events) > 100 {
		return DiscoveryEventResult{}, fmt.Errorf("%w: invalid telemetry batch", ErrInvalidTelemetry)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DiscoveryEventResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result := DiscoveryEventResult{}
	for _, event := range events {
		if event.EventID == "" || len(event.EventID) > 128 ||
			(event.EventType != "impression" && event.EventType != "click") ||
			event.Position < 0 || event.Position > 10000 {
			result.Rejected++
			continue
		}
		source, err := NormalizeTrafficSource(event.Source)
		if err != nil {
			result.Rejected++
			continue
		}
		var channelID string
		if err := tx.QueryRowContext(ctx, `
			SELECT channel_id FROM videos
			WHERE id = $1
			  AND status IN ('published', 'demonetized')
			  AND visibility IN ('public', 'unlisted')
		`, event.VideoID).Scan(&channelID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				result.Rejected++
				continue
			}
			return DiscoveryEventResult{}, err
		}
		inserted, err := tx.ExecContext(ctx, `
			INSERT INTO discovery_events (
				event_id, viewer_key, video_id, channel_id, event_type, source,
				position, occurred_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			ON CONFLICT (viewer_key, event_id) DO NOTHING
		`, event.EventID, viewer.Key, event.VideoID, channelID, event.EventType,
			source, event.Position, at,
		)
		if err != nil {
			return DiscoveryEventResult{}, err
		}
		affected, err := inserted.RowsAffected()
		if err != nil {
			return DiscoveryEventResult{}, err
		}
		if affected == 0 {
			result.Duplicates++
			continue
		}
		result.Accepted++
		impressions := int64(0)
		clicks := int64(0)
		if event.EventType == "impression" {
			impressions = 1
		} else {
			clicks = 1
		}
		if err := upsertDailyAnalytics(ctx, tx, event.VideoID, channelID, at, source, 0, 0, 0, 0, impressions, clicks); err != nil {
			return DiscoveryEventResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DiscoveryEventResult{}, err
	}
	return result, nil
}

func (s *Store) StudioAnalyticsSummary(ctx context.Context, channelID string, from, to time.Time) (AnalyticsSummary, error) {
	return s.analyticsSummary(ctx, "channel_id = $1", []any{channelID}, from, to)
}

func (s *Store) StudioAnalyticsTimeseries(ctx context.Context, channelID string, from, to time.Time) ([]AnalyticsPoint, error) {
	return s.analyticsTimeseries(ctx, "channel_id = $1", []any{channelID}, from, to)
}

func (s *Store) StudioVideoAnalytics(ctx context.Context, channelID, videoID string, from, to time.Time) (AnalyticsSummary, []AnalyticsPoint, error) {
	if _, err := s.GetStudioVideo(ctx, channelID, videoID); err != nil {
		return AnalyticsSummary{}, nil, err
	}
	where := "channel_id = $1 AND video_id = $2"
	args := []any{channelID, videoID}
	summary, err := s.analyticsSummary(ctx, where, args, from, to)
	if err != nil {
		return AnalyticsSummary{}, nil, err
	}
	points, err := s.analyticsTimeseries(ctx, where, args, from, to)
	if err != nil {
		return AnalyticsSummary{}, nil, err
	}
	return summary, points, nil
}

func (s *Store) PruneTelemetry(ctx context.Context, before time.Time) (TelemetryPruneResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TelemetryPruneResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result := TelemetryPruneResult{}
	if deleted, err := tx.ExecContext(ctx,
		`DELETE FROM discovery_events WHERE occurred_at < $1`, before,
	); err != nil {
		return TelemetryPruneResult{}, err
	} else if result.Discovery, err = deleted.RowsAffected(); err != nil {
		return TelemetryPruneResult{}, err
	}
	if deleted, err := tx.ExecContext(ctx,
		`DELETE FROM video_view_events WHERE created_at < $1`, before,
	); err != nil {
		return TelemetryPruneResult{}, err
	} else if result.ViewEvents, err = deleted.RowsAffected(); err != nil {
		return TelemetryPruneResult{}, err
	}
	if deleted, err := tx.ExecContext(ctx,
		`DELETE FROM video_view_sessions WHERE started_at < $1`, before,
	); err != nil {
		return TelemetryPruneResult{}, err
	} else if result.Sessions, err = deleted.RowsAffected(); err != nil {
		return TelemetryPruneResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TelemetryPruneResult{}, err
	}
	return result, nil
}

func getViewSession(ctx context.Context, tx *sql.Tx, sessionID, videoID, viewerKey string, lock bool) (ViewSession, error) {
	query := `
		SELECT id, video_id, channel_id, actor_id, viewer_key, source, started_at,
		       last_event_at, last_position_seconds, max_position_seconds, watched_seconds,
		       duration_seconds, completed, counted, ended_at
		FROM video_view_sessions
		WHERE id = $1 AND video_id = $2 AND viewer_key = $3`
	if lock {
		query += " FOR UPDATE"
	}
	var session ViewSession
	var actorID sql.NullString
	var endedAt sql.NullTime
	err := tx.QueryRowContext(ctx, query, sessionID, videoID, viewerKey).Scan(
		&session.ID, &session.VideoID, &session.ChannelID, &actorID, &session.ViewerKey,
		&session.Source,
		&session.StartedAt, &session.LastEventAt, &session.LastPositionSeconds,
		&session.MaxPositionSeconds, &session.WatchedSeconds, &session.DurationSeconds,
		&session.Completed, &session.Counted, &endedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ViewSession{}, ErrNotFound
	}
	if err != nil {
		return ViewSession{}, err
	}
	if actorID.Valid {
		session.ActorID = &actorID.String
	}
	if endedAt.Valid {
		session.EndedAt = &endedAt.Time
	}
	return session, nil
}

func upsertDailyAnalytics(
	ctx context.Context,
	tx *sql.Tx,
	videoID, channelID string,
	at time.Time,
	source string,
	views, sessions int64,
	watchSeconds float64,
	completions, impressions, clicks int64,
) error {
	day := time.Date(at.UTC().Year(), at.UTC().Month(), at.UTC().Day(), 0, 0, 0, 0, time.UTC)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO video_daily_analytics (
			video_id, channel_id, day, source, views, sessions, watch_seconds,
			completions, impressions, clicks
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (video_id, day, source) DO UPDATE SET
			views = video_daily_analytics.views + EXCLUDED.views,
			sessions = video_daily_analytics.sessions + EXCLUDED.sessions,
			watch_seconds = video_daily_analytics.watch_seconds + EXCLUDED.watch_seconds,
			completions = video_daily_analytics.completions + EXCLUDED.completions,
			impressions = video_daily_analytics.impressions + EXCLUDED.impressions,
			clicks = video_daily_analytics.clicks + EXCLUDED.clicks
	`, videoID, channelID, day, source, views, sessions, watchSeconds, completions, impressions, clicks)
	return err
}

func (s *Store) analyticsSummary(ctx context.Context, where string, args []any, from, to time.Time) (AnalyticsSummary, error) {
	fromPlaceholder := len(args) + 1
	toPlaceholder := len(args) + 2
	queryArgs := append(append([]any{}, args...), from, to)
	summary := AnalyticsSummary{Sources: []AnalyticsSource{}}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(views), 0), COALESCE(SUM(sessions), 0),
		       COALESCE(SUM(watch_seconds), 0), COALESCE(SUM(completions), 0),
		       COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM video_daily_analytics
		WHERE %s AND day BETWEEN $%d AND $%d
	`, where, fromPlaceholder, toPlaceholder), queryArgs...).Scan(
		&summary.Views, &summary.Sessions, &summary.WatchSeconds, &summary.Completions,
		&summary.Impressions, &summary.Clicks,
	); err != nil {
		return AnalyticsSummary{}, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT source, COALESCE(SUM(views), 0), COALESCE(SUM(sessions), 0),
		       COALESCE(SUM(watch_seconds), 0), COALESCE(SUM(completions), 0),
		       COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM video_daily_analytics
		WHERE %s AND day BETWEEN $%d AND $%d
		GROUP BY source
		ORDER BY source
	`, where, fromPlaceholder, toPlaceholder), queryArgs...)
	if err != nil {
		return AnalyticsSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var source AnalyticsSource
		if err := rows.Scan(
			&source.Source, &source.Views, &source.Sessions, &source.WatchSeconds,
			&source.Completions, &source.Impressions, &source.Clicks,
		); err != nil {
			return AnalyticsSummary{}, err
		}
		source.WatchHours = source.WatchSeconds / 3600
		summary.Sources = append(summary.Sources, source)
	}
	if err := rows.Err(); err != nil {
		return AnalyticsSummary{}, err
	}
	summary.WatchHours = summary.WatchSeconds / 3600
	if summary.Sessions > 0 {
		summary.CompletionRate = float64(summary.Completions) / float64(summary.Sessions)
	}
	if summary.Impressions > 0 {
		summary.ClickThroughRate = float64(summary.Clicks) / float64(summary.Impressions)
	}
	return summary, nil
}

func (s *Store) analyticsTimeseries(ctx context.Context, where string, args []any, from, to time.Time) ([]AnalyticsPoint, error) {
	fromPlaceholder := len(args) + 1
	toPlaceholder := len(args) + 2
	queryArgs := append(append([]any{}, args...), from, to)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT day, COALESCE(SUM(views), 0), COALESCE(SUM(sessions), 0),
		       COALESCE(SUM(watch_seconds), 0), COALESCE(SUM(completions), 0),
		       COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM video_daily_analytics
		WHERE %s AND day BETWEEN $%d AND $%d
		GROUP BY day
		ORDER BY day
	`, where, fromPlaceholder, toPlaceholder), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := make(map[string]AnalyticsPoint)
	for rows.Next() {
		var day time.Time
		var point AnalyticsPoint
		if err := rows.Scan(
			&day, &point.Views, &point.Sessions, &point.WatchSeconds,
			&point.Completions, &point.Impressions, &point.Clicks,
		); err != nil {
			return nil, err
		}
		point.Day = day.UTC().Format("2006-01-02")
		point.WatchHours = point.WatchSeconds / 3600
		byDay[point.Day] = point
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	points := make([]AnalyticsPoint, 0)
	for day := from.UTC(); !day.After(to.UTC()); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		point, ok := byDay[key]
		if !ok {
			point = AnalyticsPoint{Day: key}
		}
		points = append(points, point)
	}
	return points, nil
}
