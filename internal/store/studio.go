package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidState = errors.New("invalid state")
	ErrInvalidInput = errors.New("invalid input")
)

const (
	uploadColumns = `id, video_id, channel_id, actor_id, s3_upload_id, bucket, object_key,
		filename, content_type, size_bytes, part_size, content_sha256, status, error,
		scan_verdict, scan_reason, expires_at, completed_at, created_at, updated_at`
	jobColumns = `id, kind, upload_id, video_id, status, attempts, max_attempts, run_after,
		locked_at, locked_by, last_error, payload, created_at, updated_at`
	jobColumnsJ = `j.id, j.kind, j.upload_id, j.video_id, j.status, j.attempts, j.max_attempts, j.run_after,
		j.locked_at, j.locked_by, j.last_error, j.payload, j.created_at, j.updated_at`
	versionColumns  = `id, video_id, channel_id, label, trim, cuts, audio, volume, created_by, created_at`
	subtitleColumns = `id, video_id, channel_id, language, label, kind, object_key, status, auto, created_at, updated_at`
	assetColumns    = `video_id, kind, object_key, content_type, size_bytes, created_at, updated_at`
)

type StudioVideoFilter struct {
	Status     string
	Visibility string
	Format     string
	Category   string
	Query      string
	Sort       string
	Limit      int
	Offset     int
}

type VideoMetadataInput struct {
	Title       string
	Description string
	Visibility  string
	Category    string
	License     string
	Tags        []string
	Format      string
}

type VideoRights struct {
	Declaration string `json:"declaration,omitempty"`
	Holder      string `json:"holder,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

type VideoTrim struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type VideoCut struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type VideoAudioTrack struct {
	Name   string  `json:"name"`
	URL    string  `json:"url"`
	Volume float64 `json:"volume"`
}

type VideoVersion struct {
	ID        string           `json:"id"`
	VideoID   string           `json:"video_id"`
	ChannelID string           `json:"channel_id"`
	Label     string           `json:"label"`
	Trim      VideoTrim        `json:"trim"`
	Cuts      []VideoCut       `json:"cuts"`
	Audio     *VideoAudioTrack `json:"audio,omitempty"`
	Volume    float64          `json:"volume"`
	CreatedBy string           `json:"created_by"`
	CreatedAt time.Time        `json:"created_at"`
}

type UploadIntent struct {
	ID            string     `json:"id"`
	VideoID       string     `json:"video_id"`
	ChannelID     string     `json:"channel_id"`
	ActorID       string     `json:"actor_id"`
	S3UploadID    string     `json:"s3_upload_id"`
	Bucket        string     `json:"bucket"`
	ObjectKey     string     `json:"object_key"`
	Filename      string     `json:"filename"`
	ContentType   string     `json:"content_type"`
	SizeBytes     int64      `json:"size_bytes"`
	PartSize      int64      `json:"part_size"`
	ContentSHA256 string     `json:"content_sha256,omitempty"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
	ScanVerdict   string     `json:"scan_verdict,omitempty"`
	ScanReason    string     `json:"scan_reason,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type MediaJob struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	UploadID    string          `json:"upload_id"`
	VideoID     string          `json:"video_id"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	RunAfter    time.Time       `json:"run_after"`
	LockedAt    *time.Time      `json:"locked_at,omitempty"`
	LockedBy    string          `json:"locked_by,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type MediaQueueStats struct {
	Queued       int64      `json:"queued"`
	Running      int64      `json:"running"`
	Succeeded    int64      `json:"succeeded"`
	Failed       int64      `json:"failed"`
	OldestQueued *time.Time `json:"oldest_queued,omitempty"`
}

type MediaScan struct {
	ID            string          `json:"id"`
	UploadID      string          `json:"upload_id"`
	VideoID       string          `json:"video_id"`
	Scanner       string          `json:"scanner"`
	Verdict       string          `json:"verdict"`
	Findings      json.RawMessage `json:"findings"`
	ContentSHA256 string          `json:"content_sha256,omitempty"`
	SizeBytes     int64           `json:"size_bytes"`
	CreatedAt     time.Time       `json:"created_at"`
}

type MediaAsset struct {
	VideoID     string    `json:"video_id"`
	Kind        string    `json:"kind"`
	ObjectKey   string    `json:"object_key"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SubtitleTrack struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	ChannelID string    `json:"channel_id"`
	Language  string    `json:"language"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	ObjectKey string    `json:"object_key"`
	Status    string    `json:"status"`
	Auto      bool      `json:"auto"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type StudioStats struct {
	VideoCount        int64   `json:"video_count"`
	PublishedCount    int64   `json:"published_count"`
	ProcessingCount   int64   `json:"processing_count"`
	BlockedUploads    int64   `json:"blocked_uploads"`
	Views             int64   `json:"views"`
	Likes             int64   `json:"likes"`
	Comments          int64   `json:"comments"`
	Subscribers       int64   `json:"subscribers"`
	Revenue           float64 `json:"revenue"`
	AnalyticsViews    int64   `json:"analytics_views"`
	AnalyticsSessions int64   `json:"analytics_sessions"`
	WatchSeconds      float64 `json:"watch_seconds"`
	WatchHours        float64 `json:"watch_hours"`
	Completions       int64   `json:"completions"`
	CompletionRate    float64 `json:"completion_rate"`
	Impressions       int64   `json:"impressions"`
	Clicks            int64   `json:"clicks"`
	ClickThroughRate  float64 `json:"click_through_rate"`
	Recent            []Video `json:"recent"`
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewID(prefix string) (string, error) {
	body := make([]byte, 16)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(body), nil
}

func NewPublicID() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 11)
	raw := make([]byte, 11)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func insertVideo(ctx context.Context, q queryRower, video Video) (Video, error) {
	if video.PublicID == "" {
		publicID, err := NewPublicID()
		if err != nil {
			return Video{}, err
		}
		video.PublicID = publicID
	}
	values, err := videoValues(video)
	if err != nil {
		return Video{}, err
	}
	row := q.QueryRowContext(ctx, videoInsert+` RETURNING `+videoColumns, values...)
	return scanVideo(row)
}

func (s *Store) CreateStudioVideo(ctx context.Context, video Video) (Video, error) {
	for attempt := 0; attempt < 5; attempt++ {
		created, err := insertVideo(ctx, s.db, video)
		if isUniqueViolation(err) {
			video.PublicID = ""
			continue
		}
		return created, err
	}
	return Video{}, fmt.Errorf("allocate public video id: %w", ErrConflict)
}

func (s *Store) ListStudioVideos(ctx context.Context, channelID string, filter StudioVideoFilter) ([]Video, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	clauses := []string{"channel_id = $1"}
	args := []any{channelID}
	if filter.Status != "" {
		args = append(args, filter.Status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.Visibility != "" {
		args = append(args, filter.Visibility)
		clauses = append(clauses, fmt.Sprintf("visibility = $%d", len(args)))
	}
	if filter.Format != "" {
		args = append(args, filter.Format)
		clauses = append(clauses, fmt.Sprintf("format = $%d", len(args)))
	}
	if filter.Category != "" {
		args = append(args, filter.Category)
		clauses = append(clauses, fmt.Sprintf("category = $%d", len(args)))
	}
	if filter.Query != "" {
		args = append(args, filter.Query)
		clauses = append(clauses, fmt.Sprintf(
			"(strpos(lower(title), lower($%d)) > 0 OR strpos(lower(coalesce(description, '')), lower($%d)) > 0 OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(tags) AS tag WHERE strpos(lower(tag), lower($%d)) > 0))",
			len(args), len(args), len(args),
		))
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM videos WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "updated_at DESC, id DESC"
	switch filter.Sort {
	case "oldest":
		order = "created_at ASC, id ASC"
	case "published":
		order = "published_at DESC NULLS LAST, updated_at DESC, id DESC"
	case "views":
		order = "views DESC, updated_at DESC, id DESC"
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+videoColumns+`
		FROM videos
		WHERE `+where+`
		ORDER BY `+order+`
		LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	videos := make([]Video, 0, filter.Limit)
	for rows.Next() {
		video, err := scanVideo(rows)
		if err != nil {
			return nil, 0, err
		}
		videos = append(videos, video)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return videos, total, nil
}

func (s *Store) GetStudioVideo(ctx context.Context, channelID, id string) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+videoColumns+`
		FROM videos
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		LIMIT 1
	`, channelID, id)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Video{}, ErrNotFound
	}
	return video, err
}

func (s *Store) UpdateStudioVideo(ctx context.Context, channelID, id string, input VideoMetadataInput, expected *time.Time, expectedRange bool, at time.Time) (Video, error) {
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return Video{}, err
	}
	args := []any{
		channelID, id, input.Title, input.Description, input.Visibility,
		input.Category, input.License, tagsJSON, input.Format, at,
	}
	query := `
		UPDATE videos
		SET title = $3, description = $4, visibility = $5, category = $6,
		    license = $7, tags = $8, format = $9, updated_at = $10
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)`
	if expected != nil {
		args = append(args, *expected)
		if expectedRange {
			query += fmt.Sprintf(" AND updated_at >= $%d AND updated_at < $%d + interval '1 second'", len(args), len(args))
		} else {
			query += fmt.Sprintf(" AND updated_at = $%d", len(args))
		}
	}
	query += ` RETURNING ` + videoColumns
	video, err := scanVideo(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrConflict
	}
	return video, err
}

func (s *Store) PublishStudioVideo(ctx context.Context, channelID, id string, at time.Time) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET status = 'published',
		    published_at = COALESCE(published_at, $3),
		    updated_at = $3
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		  AND status IN ('draft', 'processing')
		  AND (COALESCE(playback->>'mp4_url', '') <> '' OR COALESCE(playback->>'hls_url', '') <> '')
		  AND COALESCE(policy->>'scan_verdict', '') NOT IN ('review', 'block')
		  AND COALESCE(policy->>'upload_status', '') NOT IN ('review', 'blocked')
		RETURNING `+videoColumns,
		channelID, id, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrInvalidState
	}
	return video, err
}

func (s *Store) UnpublishStudioVideo(ctx context.Context, channelID, id string, at time.Time) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET visibility = 'private', updated_at = $3
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		  AND status IN ('published', 'demonetized')
		RETURNING `+videoColumns,
		channelID, id, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrInvalidState
	}
	return video, err
}

func (s *Store) CreateUpload(ctx context.Context, video Video, upload UploadIntent) (UploadIntent, error) {
	if upload.ID == "" {
		id, err := NewID("upl_")
		if err != nil {
			return UploadIntent{}, err
		}
		upload.ID = id
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UploadIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()

	createdVideo, err := insertVideo(ctx, tx, video)
	if err != nil {
		return UploadIntent{}, err
	}
	upload.VideoID = createdVideo.ID
	row := tx.QueryRowContext(ctx, `
		INSERT INTO media_uploads (
			id, video_id, channel_id, actor_id, s3_upload_id, bucket, object_key,
			filename, content_type, size_bytes, part_size, content_sha256, status,
			error, scan_verdict, scan_reason, expires_at, completed_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING `+uploadColumns,
		upload.ID, upload.VideoID, upload.ChannelID, upload.ActorID, upload.S3UploadID,
		upload.Bucket, upload.ObjectKey, upload.Filename, upload.ContentType,
		upload.SizeBytes, upload.PartSize, upload.ContentSHA256, upload.Status,
		upload.Error, upload.ScanVerdict, upload.ScanReason, upload.ExpiresAt,
		upload.CompletedAt, upload.CreatedAt, upload.UpdatedAt,
	)
	intent, err := scanUpload(row)
	if err != nil {
		return UploadIntent{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadIntent{}, err
	}
	return intent, nil
}

func scanUpload(scanner rowScanner) (UploadIntent, error) {
	var upload UploadIntent
	var completedAt sql.NullTime
	err := scanner.Scan(
		&upload.ID, &upload.VideoID, &upload.ChannelID, &upload.ActorID, &upload.S3UploadID,
		&upload.Bucket, &upload.ObjectKey, &upload.Filename, &upload.ContentType,
		&upload.SizeBytes, &upload.PartSize, &upload.ContentSHA256, &upload.Status,
		&upload.Error, &upload.ScanVerdict, &upload.ScanReason, &upload.ExpiresAt,
		&completedAt, &upload.CreatedAt, &upload.UpdatedAt,
	)
	if completedAt.Valid {
		upload.CompletedAt = &completedAt.Time
	}
	return upload, err
}

func (s *Store) GetUpload(ctx context.Context, channelID, id string) (UploadIntent, error) {
	return s.getUpload(ctx, `SELECT `+uploadColumns+` FROM media_uploads WHERE channel_id = $1 AND id = $2`, channelID, id)
}

func (s *Store) GetUploadByID(ctx context.Context, id string) (UploadIntent, error) {
	return s.getUpload(ctx, `SELECT `+uploadColumns+` FROM media_uploads WHERE id = $1`, id)
}

func (s *Store) getUpload(ctx context.Context, query string, args ...any) (UploadIntent, error) {
	upload, err := scanUpload(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return UploadIntent{}, ErrNotFound
	}
	return upload, err
}

func (s *Store) ListExpiredUploads(ctx context.Context, now time.Time, limit int) ([]UploadIntent, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+uploadColumns+`
		FROM media_uploads
		WHERE status IN ('pending', 'uploading') AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uploads := make([]UploadIntent, 0)
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

func (s *Store) UpdateUploadStatus(ctx context.Context, id, status, errorMessage, scanVerdict, scanReason string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_uploads
		SET status = $2, error = $3, scan_verdict = $4, scan_reason = $5, updated_at = $6
		WHERE id = $1
	`, id, status, errorMessage, scanVerdict, scanReason, at)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkUploadCompleted(ctx context.Context, id, contentSHA string, at time.Time) (UploadIntent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UploadIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		UPDATE media_uploads
		SET status = 'scanning', content_sha256 = $2, completed_at = $3, updated_at = $3
		WHERE id = $1 AND status IN ('pending', 'uploading')
		RETURNING `+uploadColumns,
		id, contentSHA, at,
	)
	upload, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		current, getErr := scanUpload(tx.QueryRowContext(ctx, `
			SELECT `+uploadColumns+` FROM media_uploads WHERE id = $1
		`, id))
		if getErr != nil {
			return UploadIntent{}, getErr
		}
		if err := tx.Commit(); err != nil {
			return UploadIntent{}, err
		}
		return current, nil
	}
	if err != nil {
		return UploadIntent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_jobs (kind, upload_id, video_id, status, max_attempts, run_after, created_at, updated_at)
		VALUES ('process', $1, $2, 'queued', 5, $3, $3, $3)
		ON CONFLICT DO NOTHING
	`, upload.ID, upload.VideoID, at); err != nil {
		return UploadIntent{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadIntent{}, err
	}
	return upload, nil
}

func (s *Store) AbortUpload(ctx context.Context, channelID, id, errorMessage string, at time.Time) (UploadIntent, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE media_uploads
		SET status = 'aborted', error = $3, updated_at = $4
		WHERE channel_id = $1 AND id = $2 AND status IN ('pending', 'uploading')
		RETURNING `+uploadColumns,
		channelID, id, errorMessage, at,
	)
	upload, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetUpload(ctx, channelID, id); getErr != nil {
			return UploadIntent{}, getErr
		}
		return UploadIntent{}, ErrInvalidState
	}
	if err != nil {
		return UploadIntent{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE videos SET status = 'draft', updated_at = $2 WHERE id = $1
	`, upload.VideoID, at); err != nil {
		return UploadIntent{}, err
	}
	return upload, nil
}

func (s *Store) EnqueueMediaJob(ctx context.Context, kind, uploadID, videoID string, payload json.RawMessage, at time.Time) error {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_jobs (kind, upload_id, video_id, status, max_attempts, run_after, payload, created_at, updated_at)
		VALUES ($1, $2, $3, 'queued', 5, $4, $5, $4, $4)
		ON CONFLICT DO NOTHING
	`, kind, uploadID, videoID, at, payload)
	return err
}

func (s *Store) ClaimMediaJob(ctx context.Context, workerID string, now time.Time, lease time.Duration) (MediaJob, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM media_jobs
			WHERE (status = 'queued' AND run_after <= $1)
			   OR (status = 'running' AND locked_at < $2)
			ORDER BY media_jobs.id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE media_jobs j
		SET status = 'running', attempts = j.attempts + 1,
		    locked_at = $1, locked_by = $3, updated_at = $1
		FROM candidate c
		WHERE j.id = c.id
		RETURNING `+jobColumnsJ,
		now, now.Add(-lease), workerID,
	)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaJob{}, false, nil
	}
	if err != nil {
		return MediaJob{}, false, err
	}
	return job, true, nil
}

func scanJob(scanner rowScanner) (MediaJob, error) {
	var job MediaJob
	var lockedAt sql.NullTime
	var payload []byte
	err := scanner.Scan(
		&job.ID, &job.Kind, &job.UploadID, &job.VideoID, &job.Status, &job.Attempts,
		&job.MaxAttempts, &job.RunAfter, &lockedAt, &job.LockedBy, &job.LastError,
		&payload, &job.CreatedAt, &job.UpdatedAt,
	)
	if lockedAt.Valid {
		job.LockedAt = &lockedAt.Time
	}
	if len(payload) > 0 {
		job.Payload = json.RawMessage(payload)
	}
	return job, err
}

func (s *Store) FinishMediaJob(ctx context.Context, id int64, workerID string, attempts int, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_jobs
		SET status = 'succeeded', locked_at = NULL, locked_by = '', updated_at = $4
		WHERE id = $1 AND locked_by = $2 AND attempts = $3 AND status = 'running'
	`, id, workerID, attempts, at)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RetryMediaJob(
	ctx context.Context,
	id int64,
	workerID string,
	attempts int,
	errorMessage string,
	retryAt, at time.Time,
) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `
		UPDATE media_jobs
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
		    run_after = $4, locked_at = NULL, locked_by = '', last_error = $5, updated_at = $6
		WHERE id = $1 AND locked_by = $2 AND attempts = $3 AND status = 'running'
		RETURNING status
	`, id, workerID, attempts, retryAt, errorMessage, at).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

func (s *Store) RenewMediaJobLease(ctx context.Context, id int64, workerID string, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_jobs
		SET locked_at = $3, updated_at = $3
		WHERE id = $1 AND locked_by = $2 AND status = 'running'
	`, id, workerID, at)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return updated > 0, nil
}

func (s *Store) MediaQueueStats(ctx context.Context) (MediaQueueStats, error) {
	var stats MediaQueueStats
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM media_jobs GROUP BY status`)
	if err != nil {
		return MediaQueueStats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return MediaQueueStats{}, err
		}
		switch status {
		case "queued":
			stats.Queued = count
		case "running":
			stats.Running = count
		case "succeeded":
			stats.Succeeded = count
		case "failed":
			stats.Failed = count
		}
	}
	if err := rows.Err(); err != nil {
		return MediaQueueStats{}, err
	}
	if err := rows.Close(); err != nil {
		return MediaQueueStats{}, err
	}
	var oldest sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(run_after) FROM media_jobs WHERE status = 'queued'
	`).Scan(&oldest); err != nil {
		return MediaQueueStats{}, err
	}
	if oldest.Valid {
		value := oldest.Time
		stats.OldestQueued = &value
	}
	return stats, nil
}

func (s *Store) PruneMediaJobs(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtextextended('keel_media_retention', 0))`,
	).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM media_jobs
		WHERE id IN (
			SELECT id FROM media_jobs
			WHERE status IN ('succeeded', 'failed') AND updated_at < $1
			ORDER BY updated_at
			LIMIT $2
		)
	`, before, limit)
	if err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) DeleteMediaAsset(ctx context.Context, videoID, kind string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM media_assets WHERE video_id = $1 AND kind = $2
	`, videoID, kind)
	return err
}

func (s *Store) RecordMediaScan(ctx context.Context, scan MediaScan) error {
	if scan.ID == "" {
		id, err := NewID("scn_")
		if err != nil {
			return err
		}
		scan.ID = id
	}
	if len(scan.Findings) == 0 {
		scan.Findings = json.RawMessage(`[]`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_scans (
			id, upload_id, video_id, scanner, verdict, findings, content_sha256, size_bytes, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, scan.ID, scan.UploadID, scan.VideoID, scan.Scanner, scan.Verdict,
		scan.Findings, scan.ContentSHA256, scan.SizeBytes, scan.CreatedAt)
	return err
}

func (s *Store) ListMediaScans(ctx context.Context, uploadID string) ([]MediaScan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, upload_id, video_id, scanner, verdict, findings, content_sha256, size_bytes, created_at
		FROM media_scans
		WHERE upload_id = $1
		ORDER BY created_at DESC, id DESC
	`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scans := make([]MediaScan, 0)
	for rows.Next() {
		var scan MediaScan
		var findings []byte
		if err := rows.Scan(
			&scan.ID, &scan.UploadID, &scan.VideoID, &scan.Scanner, &scan.Verdict,
			&findings, &scan.ContentSHA256, &scan.SizeBytes, &scan.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(findings) > 0 {
			scan.Findings = json.RawMessage(findings)
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}

func (s *Store) IsHashBlocked(ctx context.Context, contentSHA string) (string, string, bool, error) {
	var kind, reason string
	err := s.db.QueryRowContext(ctx, `
		SELECT kind, reason FROM media_blocklist WHERE content_sha256 = $1
	`, strings.ToLower(contentSHA)).Scan(&kind, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return kind, reason, true, nil
}

func (s *Store) FindDuplicateVideoByHash(ctx context.Context, contentSHA, excludeVideoID string) (Video, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+videoColumns+`
		FROM videos
		WHERE content_hash = 'sha256:' || $1
		  AND $1 <> ''
		  AND id <> $2
		  AND status IN ('published', 'demonetized')
		ORDER BY published_at DESC NULLS LAST, updated_at DESC
		LIMIT 1
	`, contentSHA, excludeVideoID)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Video{}, false, nil
	}
	if err != nil {
		return Video{}, false, err
	}
	return video, true, nil
}

func (s *Store) UpsertMediaAsset(ctx context.Context, asset MediaAsset) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_assets (video_id, kind, object_key, content_type, size_bytes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (video_id, kind) DO UPDATE SET
			object_key = EXCLUDED.object_key,
			content_type = EXCLUDED.content_type,
			size_bytes = EXCLUDED.size_bytes,
			updated_at = EXCLUDED.updated_at
	`, asset.VideoID, asset.Kind, asset.ObjectKey, asset.ContentType, asset.SizeBytes, asset.UpdatedAt)
	return err
}

func (s *Store) GetMediaAsset(ctx context.Context, videoID, kind string) (MediaAsset, bool, error) {
	var asset MediaAsset
	err := s.db.QueryRowContext(ctx, `
		SELECT `+assetColumns+` FROM media_assets WHERE video_id = $1 AND kind = $2
	`, videoID, kind).Scan(
		&asset.VideoID, &asset.Kind, &asset.ObjectKey, &asset.ContentType,
		&asset.SizeBytes, &asset.CreatedAt, &asset.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaAsset{}, false, nil
	}
	if err != nil {
		return MediaAsset{}, false, err
	}
	return asset, true, nil
}

func (s *Store) UpdateVideoPolicy(ctx context.Context, videoID string, policy Policy, at time.Time) error {
	body, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE videos SET policy = $2, updated_at = $3 WHERE id = $1
	`, videoID, body, at)
	return err
}

func (s *Store) FinalizeVideoProcessing(ctx context.Context, videoID, channelID, contentSHA string, durationSeconds int, playback Playback, policy Policy, publish bool, at time.Time) (Video, error) {
	playbackJSON, err := json.Marshal(playback)
	if err != nil {
		return Video{}, err
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return Video{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET content_hash = CASE WHEN $3 = '' THEN content_hash ELSE 'sha256:' || $3 END,
		    duration_seconds = $4,
		    playback = CASE
		        WHEN COALESCE(playback->>'captions_url', '') <> ''
		        THEN jsonb_set($5::jsonb, '{captions_url}', to_jsonb(playback->>'captions_url'), true)
		        ELSE $5::jsonb
		    END,
		    policy = $6,
		    status = CASE WHEN $7 THEN 'published' ELSE 'draft' END,
		    published_at = CASE WHEN $7 THEN COALESCE(published_at, $8) ELSE published_at END,
		    updated_at = $8
		WHERE id = $1 AND channel_id = $2
		RETURNING `+videoColumns,
		videoID, channelID, contentSHA, durationSeconds, playbackJSON, policyJSON, publish, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Video{}, ErrNotFound
	}
	return video, err
}

func (s *Store) ListVideoVersions(ctx context.Context, channelID, videoID string) ([]VideoVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+versionColumns+`
		FROM video_versions
		WHERE channel_id = $1 AND video_id = $2
		ORDER BY created_at DESC, id DESC
	`, channelID, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]VideoVersion, 0)
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func scanVersion(scanner rowScanner) (VideoVersion, error) {
	var version VideoVersion
	var trimJSON, cutsJSON, audioJSON []byte
	err := scanner.Scan(
		&version.ID, &version.VideoID, &version.ChannelID, &version.Label, &trimJSON,
		&cutsJSON, &audioJSON, &version.Volume, &version.CreatedBy, &version.CreatedAt,
	)
	if err != nil {
		return VideoVersion{}, err
	}
	if err := json.Unmarshal(trimJSON, &version.Trim); err != nil {
		return VideoVersion{}, err
	}
	if err := json.Unmarshal(cutsJSON, &version.Cuts); err != nil {
		return VideoVersion{}, err
	}
	if version.Cuts == nil {
		version.Cuts = []VideoCut{}
	}
	if len(audioJSON) > 0 && string(audioJSON) != "null" {
		var audio VideoAudioTrack
		if err := json.Unmarshal(audioJSON, &audio); err != nil {
			return VideoVersion{}, err
		}
		version.Audio = &audio
	}
	return version, nil
}

func (s *Store) CreateVideoVersion(ctx context.Context, version VideoVersion) (VideoVersion, error) {
	if _, err := s.GetStudioVideo(ctx, version.ChannelID, version.VideoID); err != nil {
		return VideoVersion{}, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM video_versions WHERE video_id = $1
	`, version.VideoID).Scan(&count); err != nil {
		return VideoVersion{}, err
	}
	if count >= 500 {
		return VideoVersion{}, fmt.Errorf("%w: version limit reached", ErrInvalidInput)
	}
	if version.ID == "" {
		id, err := NewID("ver_")
		if err != nil {
			return VideoVersion{}, err
		}
		version.ID = id
	}
	trimJSON, err := json.Marshal(version.Trim)
	if err != nil {
		return VideoVersion{}, err
	}
	cuts := version.Cuts
	if cuts == nil {
		cuts = []VideoCut{}
	}
	cutsJSON, err := json.Marshal(cuts)
	if err != nil {
		return VideoVersion{}, err
	}
	var audioJSON any
	if version.Audio != nil {
		body, err := json.Marshal(version.Audio)
		if err != nil {
			return VideoVersion{}, err
		}
		audioJSON = body
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO video_versions (
			id, video_id, channel_id, label, trim, cuts, audio, volume, created_by, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+versionColumns,
		version.ID, version.VideoID, version.ChannelID, version.Label, trimJSON,
		cutsJSON, audioJSON, version.Volume, version.CreatedBy, version.CreatedAt,
	)
	return scanVersion(row)
}

func (s *Store) GetVideoVersion(ctx context.Context, channelID, videoID, versionID string) (VideoVersion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+versionColumns+`
		FROM video_versions
		WHERE channel_id = $1 AND video_id = $2 AND id = $3
	`, channelID, videoID, versionID)
	version, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VideoVersion{}, ErrNotFound
	}
	return version, err
}

func (s *Store) ListSubtitles(ctx context.Context, channelID, videoID string) ([]SubtitleTrack, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+subtitleColumns+`
		FROM video_subtitles
		WHERE channel_id = $1 AND video_id = $2
		ORDER BY created_at ASC, id ASC
	`, channelID, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tracks := make([]SubtitleTrack, 0)
	for rows.Next() {
		track, err := scanSubtitle(rows)
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}

func scanSubtitle(scanner rowScanner) (SubtitleTrack, error) {
	var track SubtitleTrack
	err := scanner.Scan(
		&track.ID, &track.VideoID, &track.ChannelID, &track.Language, &track.Label,
		&track.Kind, &track.ObjectKey, &track.Status, &track.Auto, &track.CreatedAt,
		&track.UpdatedAt,
	)
	return track, err
}

func (s *Store) GetSubtitleTrack(ctx context.Context, videoID, trackID string) (SubtitleTrack, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+subtitleColumns+`
		FROM video_subtitles
		WHERE video_id = $1 AND id = $2
	`, videoID, trackID)
	track, err := scanSubtitle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubtitleTrack{}, ErrNotFound
	}
	return track, err
}

func (s *Store) SetVideoCaptionsURL(ctx context.Context, videoID, captionsURL string, at time.Time) error {
	return s.SetVideoPlaybackURL(ctx, videoID, "captions_url", captionsURL, at)
}

func (s *Store) SetVideoThumbnailURL(ctx context.Context, videoID, thumbnailURL string, at time.Time) error {
	return s.SetVideoPlaybackURL(ctx, videoID, "thumbnail_url", thumbnailURL, at)
}

func (s *Store) SetVideoPlaybackURL(ctx context.Context, videoID, field, value string, at time.Time) error {
	switch field {
	case "captions_url", "thumbnail_url", "mp4_url", "hls_url":
	default:
		return fmt.Errorf("%w: invalid playback field", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE videos
		SET playback = jsonb_set(playback, ARRAY[$2], to_jsonb($3::text), true), updated_at = $4
		WHERE id = $1
	`, videoID, field, value, at)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSubtitleTrack(ctx context.Context, track SubtitleTrack) (SubtitleTrack, error) {
	if _, err := s.GetStudioVideo(ctx, track.ChannelID, track.VideoID); err != nil {
		return SubtitleTrack{}, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM video_subtitles WHERE video_id = $1
	`, track.VideoID).Scan(&count); err != nil {
		return SubtitleTrack{}, err
	}
	if count >= 50 {
		return SubtitleTrack{}, fmt.Errorf("%w: subtitle track limit reached", ErrInvalidInput)
	}
	if track.ID == "" {
		id, err := NewID("sub_")
		if err != nil {
			return SubtitleTrack{}, err
		}
		track.ID = id
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO video_subtitles (
			id, video_id, channel_id, language, label, kind, object_key, status, auto, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+subtitleColumns,
		track.ID, track.VideoID, track.ChannelID, track.Language, track.Label,
		track.Kind, track.ObjectKey, track.Status, track.Auto, track.CreatedAt, track.UpdatedAt,
	)
	return scanSubtitle(row)
}

func (s *Store) CompleteSubtitleTrack(ctx context.Context, channelID, videoID, trackID string, at time.Time) (SubtitleTrack, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE video_subtitles
		SET status = 'ready', updated_at = $4
		WHERE channel_id = $1 AND video_id = $2 AND id = $3 AND status = 'pending'
		RETURNING `+subtitleColumns,
		channelID, videoID, trackID, at,
	)
	track, err := scanSubtitle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubtitleTrack{}, ErrNotFound
	}
	return track, err
}

func (s *Store) DeleteSubtitleTrack(ctx context.Context, channelID, videoID, trackID string) (SubtitleTrack, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM video_subtitles
		WHERE channel_id = $1 AND video_id = $2 AND id = $3
		RETURNING `+subtitleColumns,
		channelID, videoID, trackID,
	)
	track, err := scanSubtitle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SubtitleTrack{}, ErrNotFound
	}
	return track, err
}

func (s *Store) StudioStats(ctx context.Context, channelID string) (StudioStats, error) {
	var stats StudioStats
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status IN ('published', 'demonetized')),
			COUNT(*) FILTER (WHERE status = 'processing'),
			COALESCE(SUM(views), 0),
			COALESCE(SUM(likes), 0),
			COALESCE(SUM(comments_count), 0)
		FROM videos
		WHERE channel_id = $1
	`, channelID).Scan(
		&stats.VideoCount, &stats.PublishedCount, &stats.ProcessingCount,
		&stats.Views, &stats.Likes, &stats.Comments,
	); err != nil {
		return StudioStats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT subscribers FROM channels WHERE id = $1
	`, channelID).Scan(&stats.Subscribers); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StudioStats{}, ErrNotFound
		}
		return StudioStats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_uploads
		WHERE channel_id = $1 AND status = 'blocked'
	`, channelID).Scan(&stats.BlockedUploads); err != nil {
		return StudioStats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(views), 0), COALESCE(SUM(sessions), 0),
		       COALESCE(SUM(watch_seconds), 0), COALESCE(SUM(completions), 0),
		       COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM video_daily_analytics
		WHERE channel_id = $1
	`, channelID).Scan(
		&stats.AnalyticsViews, &stats.AnalyticsSessions, &stats.WatchSeconds,
		&stats.Completions, &stats.Impressions, &stats.Clicks,
	); err != nil {
		return StudioStats{}, err
	}
	stats.WatchHours = stats.WatchSeconds / 3600
	if stats.AnalyticsSessions > 0 {
		stats.CompletionRate = float64(stats.Completions) / float64(stats.AnalyticsSessions)
	}
	if stats.Impressions > 0 {
		stats.ClickThroughRate = float64(stats.Clicks) / float64(stats.Impressions)
	}
	recent, _, err := s.ListStudioVideos(ctx, channelID, StudioVideoFilter{Limit: 5})
	if err != nil {
		return StudioStats{}, err
	}
	stats.Recent = recent
	return stats, nil
}

func (s *Store) SetVideoStatus(ctx context.Context, videoID, status string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE videos SET status = $2, updated_at = $3 WHERE id = $1
	`, videoID, status, at)
	return err
}

func (s *Store) ListUploads(ctx context.Context, channelID, status string, limit, offset int) ([]UploadIntent, int, error) {
	clauses := []string{"channel_id = $1"}
	args := []any{channelID}
	if status != "" {
		args = append(args, status)
		clauses = append(clauses, "status = $2")
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_uploads WHERE `+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	limitPlaceholder := len(args) + 1
	offsetPlaceholder := len(args) + 2
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+uploadColumns+`
		FROM media_uploads
		WHERE `+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT $`+fmt.Sprint(limitPlaceholder)+` OFFSET $`+fmt.Sprint(offsetPlaceholder)+`
	`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	uploads := make([]UploadIntent, 0, limit)
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			return nil, 0, err
		}
		uploads = append(uploads, upload)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return uploads, total, nil
}

func (s *Store) ArchiveStudioVideo(ctx context.Context, channelID, id string, at time.Time) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET status = 'archived', visibility = 'private',
		    archived_at = $3, scheduled_at = NULL, updated_at = $3
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		  AND status <> 'archived'
		RETURNING `+videoColumns,
		channelID, id, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrInvalidState
	}
	return video, err
}

func (s *Store) RestoreStudioVideo(ctx context.Context, channelID, id string, at time.Time) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET status = 'draft', visibility = 'private',
		    archived_at = NULL, updated_at = $3
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		  AND status = 'archived'
		RETURNING `+videoColumns,
		channelID, id, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrInvalidState
	}
	return video, err
}

func (s *Store) ScheduleStudioVideo(
	ctx context.Context,
	channelID, id string,
	scheduledAt, at time.Time,
) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET scheduled_at = $3, updated_at = $4
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		  AND status IN ('draft', 'processing')
		  AND archived_at IS NULL
		RETURNING `+videoColumns,
		channelID, id, scheduledAt, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrInvalidState
	}
	return video, err
}

func (s *Store) ClearStudioSchedule(ctx context.Context, channelID, id string, at time.Time) (Video, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE videos
		SET scheduled_at = NULL, updated_at = $3
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		RETURNING `+videoColumns,
		channelID, id, at,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetStudioVideo(ctx, channelID, id); getErr != nil {
			return Video{}, getErr
		}
		return Video{}, ErrNotFound
	}
	return video, err
}

func (s *Store) PublishScheduledVideos(ctx context.Context, now time.Time) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE videos
		SET status = 'published',
		    published_at = COALESCE(published_at, $1),
		    scheduled_at = NULL,
		    updated_at = $1
		WHERE scheduled_at IS NOT NULL AND scheduled_at <= $1
		  AND status IN ('draft', 'processing')
		  AND (COALESCE(playback->>'mp4_url', '') <> '' OR COALESCE(playback->>'hls_url', '') <> '')
		  AND COALESCE(policy->>'scan_verdict', '') NOT IN ('review', 'block')
		  AND COALESCE(policy->>'upload_status', '') NOT IN ('review', 'blocked')
		RETURNING `+videoColumns,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	videos := make([]Video, 0)
	for rows.Next() {
		video, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		videos = append(videos, video)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return videos, nil
}

var (
	ErrPurgeBusy   = errors.New("video cannot be purged while media work is active")
	ErrNotArchived = errors.New("only archived videos can be purged")
)

type VideoPurgePlan struct {
	Video     Video
	Assets    []MediaAsset
	Subtitles []SubtitleTrack
	Uploads   []UploadIntent
}

type MediaPurge struct {
	ID          string
	VideoID     string
	PublicID    string
	ChannelID   string
	ActorID     string
	ObjectCount int
	PurgedAt    time.Time
}

// PrepareVideoPurge locks an archived, creator-owned video and removes queued
// media jobs so no worker can start new processing while object storage is
// being destroyed. Running jobs and active uploads return ErrPurgeBusy.
func (s *Store) PrepareVideoPurge(ctx context.Context, channelID, id string) (VideoPurgePlan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VideoPurgePlan{}, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT `+videoColumns+`
		FROM videos
		WHERE channel_id = $1 AND (id = $2 OR public_id = $2)
		FOR UPDATE
	`, channelID, id)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VideoPurgePlan{}, ErrNotFound
	}
	if err != nil {
		return VideoPurgePlan{}, err
	}
	if video.Status != "archived" {
		return VideoPurgePlan{}, ErrNotArchived
	}

	lockedJobs, err := tx.QueryContext(ctx, `
		SELECT id FROM media_jobs WHERE video_id = $1 FOR UPDATE
	`, video.ID)
	if err != nil {
		return VideoPurgePlan{}, err
	}
	_ = lockedJobs.Close()

	var runningJobs int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_jobs WHERE video_id = $1 AND status = 'running'
	`, video.ID).Scan(&runningJobs); err != nil {
		return VideoPurgePlan{}, err
	}
	if runningJobs > 0 {
		return VideoPurgePlan{}, ErrPurgeBusy
	}
	var activeUploads int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_uploads
		WHERE video_id = $1 AND status IN ('pending', 'uploading', 'scanning', 'processing')
	`, video.ID).Scan(&activeUploads); err != nil {
		return VideoPurgePlan{}, err
	}
	if activeUploads > 0 {
		return VideoPurgePlan{}, ErrPurgeBusy
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM media_jobs WHERE video_id = $1`, video.ID); err != nil {
		return VideoPurgePlan{}, err
	}

	assets, err := purgeAssets(ctx, tx, video.ID)
	if err != nil {
		return VideoPurgePlan{}, err
	}
	subtitles, err := purgeSubtitles(ctx, tx, video.ID)
	if err != nil {
		return VideoPurgePlan{}, err
	}
	uploads, err := purgeUploads(ctx, tx, video.ID)
	if err != nil {
		return VideoPurgePlan{}, err
	}
	if err := tx.Commit(); err != nil {
		return VideoPurgePlan{}, err
	}
	return VideoPurgePlan{
		Video:     video,
		Assets:    assets,
		Subtitles: subtitles,
		Uploads:   uploads,
	}, nil
}

func purgeAssets(ctx context.Context, tx *sql.Tx, videoID string) ([]MediaAsset, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+assetColumns+` FROM media_assets WHERE video_id = $1 ORDER BY kind
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]MediaAsset, 0)
	for rows.Next() {
		var asset MediaAsset
		if err := rows.Scan(
			&asset.VideoID, &asset.Kind, &asset.ObjectKey, &asset.ContentType,
			&asset.SizeBytes, &asset.CreatedAt, &asset.UpdatedAt,
		); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func purgeSubtitles(ctx context.Context, tx *sql.Tx, videoID string) ([]SubtitleTrack, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+subtitleColumns+` FROM video_subtitles WHERE video_id = $1 ORDER BY created_at
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subtitles := make([]SubtitleTrack, 0)
	for rows.Next() {
		track, err := scanSubtitle(rows)
		if err != nil {
			return nil, err
		}
		subtitles = append(subtitles, track)
	}
	return subtitles, rows.Err()
}

func purgeUploads(ctx context.Context, tx *sql.Tx, videoID string) ([]UploadIntent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+uploadColumns+` FROM media_uploads WHERE video_id = $1 ORDER BY created_at
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uploads := make([]UploadIntent, 0)
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

// DeletePurgedVideo removes the video and every child row. Object storage must
// be cleaned first; this method only deletes database state.
func (s *Store) DeletePurgedVideo(ctx context.Context, channelID, videoID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var runningJobs int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_jobs WHERE video_id = $1 AND status = 'running'
	`, videoID).Scan(&runningJobs); err != nil {
		return err
	}
	if runningJobs > 0 {
		return ErrPurgeBusy
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_jobs WHERE video_id = $1`, videoID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM videos WHERE id = $1 AND channel_id = $2
	`, videoID, channelID)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) RecordMediaPurge(ctx context.Context, purge MediaPurge) error {
	if purge.ID == "" {
		id, err := NewID("purge_")
		if err != nil {
			return err
		}
		purge.ID = id
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_purges (
			id, video_id, public_id, channel_id, actor_id, object_count, purged_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, purge.ID, purge.VideoID, purge.PublicID, purge.ChannelID, purge.ActorID,
		purge.ObjectCount, purge.PurgedAt)
	return err
}
