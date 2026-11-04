package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	Categories = []string{"Documentaries", "Protocol", "Moderation", "Studio", "Federation"}
	Formats    = []string{"video", "short"}
	VideoSorts = []string{"latest", "oldest", "trending", "views"}
)

const (
	videoColumns = `id, public_id, channel_id, title, description, status, visibility, content_hash, duration_seconds, playback, license, policy, format, category, tags, views, likes, comments_count, created_at, published_at, scheduled_at, archived_at, updated_at`
	videoInsert  = `INSERT INTO videos (
		id, public_id, channel_id, title, description, status, visibility, content_hash,
		duration_seconds, playback, license, policy, format, category, tags,
		views, likes, comments_count, created_at, published_at, scheduled_at,
		archived_at, updated_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`
	videoColumnsV       = `v.id, v.public_id, v.channel_id, v.title, v.description, v.status, v.visibility, v.content_hash, v.duration_seconds, v.playback, v.license, v.policy, v.format, v.category, v.tags, v.views, v.likes, v.comments_count, v.created_at, v.published_at, v.scheduled_at, v.archived_at, v.updated_at`
	channelColumns      = `id, actor_id, handle, name, description, avatar_url, banner_url, links, subscribers, verified, created_at, updated_at`
	channelStatsColumns = `(SELECT COUNT(*) FROM videos v
		WHERE v.channel_id = channels.id
		  AND v.status IN ('published', 'demonetized')
		  AND v.visibility = 'public') AS video_count,
		(SELECT COALESCE(SUM(v.views), 0) FROM videos v
		WHERE v.channel_id = channels.id
		  AND v.status IN ('published', 'demonetized')
		  AND v.visibility = 'public') AS total_views`
)

type VideoFilter struct {
	ChannelID    string
	Categories   []string
	Format       string
	Sort         string
	Limit        int
	Offset       int
	PublicOnly   bool
	SubscribedBy string
}

type CategoryCount struct {
	Name       string
	VideoCount int
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVideo(scanner rowScanner) (Video, error) {
	var video Video
	var playbackJSON, policyJSON, tagsJSON []byte
	var publishedAt, scheduledAt, archivedAt sql.NullTime
	err := scanner.Scan(
		&video.ID, &video.PublicID, &video.ChannelID, &video.Title, &video.Description,
		&video.Status, &video.Visibility, &video.ContentHash, &video.DurationSeconds,
		&playbackJSON, &video.License, &policyJSON, &video.Format, &video.Category,
		&tagsJSON, &video.Views, &video.Likes, &video.CommentsCount, &video.CreatedAt,
		&publishedAt, &scheduledAt, &archivedAt, &video.UpdatedAt,
	)
	if err != nil {
		return Video{}, err
	}
	if err := json.Unmarshal(playbackJSON, &video.Playback); err != nil {
		return Video{}, err
	}
	if len(policyJSON) > 0 && string(policyJSON) != "null" {
		var policy Policy
		if err := json.Unmarshal(policyJSON, &policy); err != nil {
			return Video{}, err
		}
		video.Policy = &policy
	}
	if len(tagsJSON) > 0 && string(tagsJSON) != "null" {
		if err := json.Unmarshal(tagsJSON, &video.Tags); err != nil {
			return Video{}, err
		}
	}
	if video.Tags == nil {
		video.Tags = []string{}
	}
	if publishedAt.Valid {
		video.PublishedAt = &publishedAt.Time
	}
	if scheduledAt.Valid {
		video.ScheduledAt = &scheduledAt.Time
	}
	if archivedAt.Valid {
		video.ArchivedAt = &archivedAt.Time
	}
	return video, nil
}

func scanChannelWithStats(scanner rowScanner) (Channel, error) {
	var channel Channel
	var linksJSON []byte
	err := scanner.Scan(
		&channel.ID, &channel.ActorID, &channel.Handle, &channel.Name, &channel.Description,
		&channel.AvatarURL, &channel.BannerURL, &linksJSON, &channel.Subscribers,
		&channel.Verified, &channel.CreatedAt, &channel.UpdatedAt, &channel.VideoCount,
		&channel.TotalViews,
	)
	if err != nil {
		return Channel{}, err
	}
	if len(linksJSON) > 0 && string(linksJSON) != "null" {
		if err := json.Unmarshal(linksJSON, &channel.Links); err != nil {
			return Channel{}, err
		}
	}
	if channel.Links == nil {
		channel.Links = []ChannelLink{}
	}
	return channel, nil
}

func (s *Store) ListVideos(ctx context.Context, filter VideoFilter) ([]Video, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	clauses := []string{"1 = 1"}
	args := make([]any, 0, 6)
	if filter.PublicOnly {
		clauses = append(clauses, "v.status IN ('published', 'demonetized')", "v.visibility = 'public'")
	}
	if filter.SubscribedBy != "" {
		args = append(args, filter.SubscribedBy)
		clauses = append(clauses, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM subscriptions s WHERE s.channel_id = v.channel_id AND s.actor_id = $%d)",
			len(args),
		))
	}
	if filter.ChannelID != "" {
		args = append(args, filter.ChannelID)
		clauses = append(clauses, fmt.Sprintf("v.channel_id = $%d", len(args)))
	}
	if len(filter.Categories) > 0 {
		placeholders := make([]string, 0, len(filter.Categories))
		for _, category := range filter.Categories {
			args = append(args, category)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "v.category IN ("+strings.Join(placeholders, ", ")+")")
	}
	if filter.Format != "" {
		args = append(args, filter.Format)
		clauses = append(clauses, fmt.Sprintf("v.format = $%d", len(args)))
	}
	where := strings.Join(clauses, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM videos v WHERE "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	dataArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	query := "SELECT " + videoColumns + " FROM videos v WHERE " + where +
		" ORDER BY " + videoOrder(filter.Sort) +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, dataArgs...)
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

func (s *Store) SearchVideos(ctx context.Context, query, format, channelID string, limit, offset int) ([]Video, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	clauses := []string{
		"v.status IN ('published', 'demonetized')",
		"v.visibility = 'public'",
		`(strpos(lower(v.title), lower($1)) > 0
		  OR strpos(lower(coalesce(v.description, '')), lower($1)) > 0
		  OR strpos(lower(v.category), lower($1)) > 0
		  OR EXISTS (
		      SELECT 1 FROM jsonb_array_elements_text(v.tags) AS tag
		      WHERE strpos(lower(tag), lower($1)) > 0
		  ))`,
	}
	args := []any{query}
	if format != "" {
		args = append(args, format)
		clauses = append(clauses, fmt.Sprintf("v.format = $%d", len(args)))
	}
	if channelID != "" {
		args = append(args, channelID)
		clauses = append(clauses, fmt.Sprintf("v.channel_id = $%d", len(args)))
	}
	where := strings.Join(clauses, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM videos v WHERE "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	dataArgs := append(append([]any{}, args...), limit, offset)
	querySQL := "SELECT " + videoColumns + " FROM videos v WHERE " + where +
		" ORDER BY v.views DESC, v.published_at DESC NULLS LAST, v.id DESC" +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, querySQL, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	videos := make([]Video, 0, limit)
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

func (s *Store) SearchChannels(ctx context.Context, query string, limit, offset int) ([]Channel, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	where := `(strpos(lower(name), lower($1)) > 0
		OR strpos(lower(handle), lower($1)) > 0
		OR strpos(lower(coalesce(description, '')), lower($1)) > 0)`

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM channels WHERE "+where, query,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+channelColumns+", "+channelStatsColumns+" FROM channels WHERE "+where+
			" ORDER BY subscribers DESC, name ASC LIMIT $2 OFFSET $3",
		query, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	channels := make([]Channel, 0, limit)
	for rows.Next() {
		channel, err := scanChannelWithStats(rows)
		if err != nil {
			return nil, 0, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

func (s *Store) ListRelatedVideos(ctx context.Context, videoID, category string, limit, offset int) ([]Video, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	where := "v.id <> $1 AND v.status IN ('published', 'demonetized') AND v.visibility = 'public'"
	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM videos v WHERE "+where, videoID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+videoColumns+" FROM videos v WHERE "+where+
			" ORDER BY (v.category = $2) DESC, v.views DESC, v.published_at DESC NULLS LAST, v.id DESC"+
			" LIMIT $3 OFFSET $4",
		videoID, category, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	videos := make([]Video, 0, limit)
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

func (s *Store) ListCategoryCounts(ctx context.Context) ([]CategoryCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT category, COUNT(*)
		FROM videos
		WHERE status IN ('published', 'demonetized') AND visibility = 'public'
		GROUP BY category
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int, len(Categories))
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		counts[name] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]CategoryCount, 0, len(Categories))
	for _, category := range Categories {
		result = append(result, CategoryCount{Name: category, VideoCount: counts[category]})
	}
	return result, nil
}

func (s *Store) GetChannelActorID(ctx context.Context, channelID string) (string, error) {
	var actorID string
	err := s.db.QueryRowContext(ctx, `SELECT actor_id FROM channels WHERE id = $1`, channelID).Scan(&actorID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return actorID, nil
}

func videoOrder(sort string) string {
	switch sort {
	case "oldest":
		return "v.published_at ASC NULLS LAST, v.id ASC"
	case "trending":
		return "v.views DESC, v.likes DESC, v.published_at DESC NULLS LAST, v.id DESC"
	case "views":
		return "v.views DESC, v.published_at DESC NULLS LAST, v.id DESC"
	default:
		return "v.published_at DESC NULLS LAST, v.id DESC"
	}
}
