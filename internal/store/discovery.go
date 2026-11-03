package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type VideoEmbeddingSource struct {
	Video              Video
	ChannelName        string
	EmbeddingHash      string
	EmbeddingUpdatedAt *time.Time
}

type ActorSignal struct {
	Kind     string
	VideoID  string
	Title    string
	Category string
	At       time.Time
}

type VideoSimilarity struct {
	Video      Video
	Similarity float64
}

func VectorLiteral(vector []float32) string {
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range vector {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String()
}

func ParseVector(value string) ([]float32, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return []float32{}, nil
	}
	parts := strings.Split(value, ",")
	vector := make([]float32, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector element %d: %w", index, err)
		}
		vector[index] = float32(parsed)
	}
	return vector, nil
}

func scanVideoWithExtras(scanner rowScanner, extras ...any) (Video, error) {
	var video Video
	var playbackJSON, policyJSON, tagsJSON []byte
	var publishedAt sql.NullTime
	destinations := []any{
		&video.ID, &video.PublicID, &video.ChannelID, &video.Title, &video.Description,
		&video.Status, &video.Visibility, &video.ContentHash, &video.DurationSeconds,
		&playbackJSON, &video.License, &policyJSON, &video.Format, &video.Category,
		&tagsJSON, &video.Views, &video.Likes, &video.CommentsCount, &video.CreatedAt,
		&publishedAt, &video.UpdatedAt,
	}
	destinations = append(destinations, extras...)
	if err := scanner.Scan(destinations...); err != nil {
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
	return video, nil
}

func (s *Store) ListVideosForEmbedding(ctx context.Context, model string) ([]VideoEmbeddingSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+videoColumnsV+`, c.name, e.content_hash, e.updated_at
		FROM videos v
		JOIN channels c ON c.id = v.channel_id
		LEFT JOIN video_embeddings e ON e.video_id = v.id AND e.model = $1
		WHERE v.status IN ('published', 'demonetized') AND v.visibility = 'public'
		ORDER BY v.id
	`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := make([]VideoEmbeddingSource, 0)
	for rows.Next() {
		var channelName string
		var contentHash sql.NullString
		var updatedAt sql.NullTime
		video, err := scanVideoWithExtras(rows, &channelName, &contentHash, &updatedAt)
		if err != nil {
			return nil, err
		}
		source := VideoEmbeddingSource{Video: video, ChannelName: channelName}
		if contentHash.Valid {
			source.EmbeddingHash = contentHash.String
		}
		if updatedAt.Valid {
			source.EmbeddingUpdatedAt = &updatedAt.Time
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) UpsertVideoEmbedding(
	ctx context.Context,
	videoID, model string,
	dimensions int,
	vector []float32,
	contentHash string,
	at time.Time,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO video_embeddings (
			video_id, model, dimensions, embedding, content_hash, updated_at
		)
		VALUES ($1, $2, $3, $4::vector, $5, $6)
		ON CONFLICT (video_id) DO UPDATE SET
			model = EXCLUDED.model,
			dimensions = EXCLUDED.dimensions,
			embedding = EXCLUDED.embedding,
			content_hash = EXCLUDED.content_hash,
			updated_at = EXCLUDED.updated_at
	`, videoID, model, dimensions, VectorLiteral(vector), contentHash, at)
	return err
}

func (s *Store) GetVideoEmbedding(ctx context.Context, videoID, model string) ([]float32, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `
		SELECT embedding::text FROM video_embeddings
		WHERE video_id = $1 AND model = $2
	`, videoID, model).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	vector, err := ParseVector(value)
	if err != nil {
		return nil, false, err
	}
	return vector, true, nil
}

func (s *Store) GetVideoEmbeddings(ctx context.Context, videoIDs []string, model string) (map[string][]float32, error) {
	result := make(map[string][]float32, len(videoIDs))
	if len(videoIDs) == 0 {
		return result, nil
	}
	idsJSON, err := json.Marshal(videoIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT video_id, embedding::text
		FROM video_embeddings
		WHERE model = $1
		  AND video_id IN (SELECT jsonb_array_elements_text($2::jsonb))
	`, model, string(idsJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var videoID, value string
		if err := rows.Scan(&videoID, &value); err != nil {
			return nil, err
		}
		vector, err := ParseVector(value)
		if err != nil {
			return nil, err
		}
		result[videoID] = vector
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) GetActorEmbedding(ctx context.Context, actorID, model string) ([]float32, time.Time, bool, error) {
	var value string
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT embedding::text, updated_at FROM actor_embeddings
		WHERE actor_id = $1 AND model = $2
	`, actorID, model).Scan(&value, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	vector, err := ParseVector(value)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return vector, updatedAt, true, nil
}

func (s *Store) UpsertActorEmbedding(
	ctx context.Context,
	actorID, model string,
	dimensions int,
	vector []float32,
	at time.Time,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO actor_embeddings (actor_id, model, dimensions, embedding, updated_at)
		VALUES ($1, $2, $3, $4::vector, $5)
		ON CONFLICT (actor_id) DO UPDATE SET
			model = EXCLUDED.model,
			dimensions = EXCLUDED.dimensions,
			embedding = EXCLUDED.embedding,
			updated_at = EXCLUDED.updated_at
	`, actorID, model, dimensions, VectorLiteral(vector), at)
	return err
}

func (s *Store) DeleteActorEmbedding(ctx context.Context, actorID, model string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM actor_embeddings WHERE actor_id = $1 AND model = $2
	`, actorID, model)
	return err
}

func (s *Store) ListActorSignals(ctx context.Context, actorID string, limit int) ([]ActorSignal, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, video_id, title, category, at FROM (
			SELECT 'watch' AS kind, wh.video_id, v.title, v.category, wh.watched_at AS at
			FROM watch_history wh
			JOIN videos v ON v.id = wh.video_id
			WHERE wh.actor_id = $1
			UNION ALL
			SELECT 'like', vr.video_id, v.title, v.category, vr.updated_at
			FROM video_reactions vr
			JOIN videos v ON v.id = vr.video_id
			WHERE vr.actor_id = $1 AND vr.kind = 'like'
			UNION ALL
			SELECT 'save', sv.video_id, v.title, v.category, sv.created_at
			FROM saved_videos sv
			JOIN videos v ON v.id = sv.video_id
			WHERE sv.actor_id = $1
		) signals
		ORDER BY at DESC
		LIMIT $2
	`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	signals := make([]ActorSignal, 0, limit)
	for rows.Next() {
		var signal ActorSignal
		if err := rows.Scan(&signal.Kind, &signal.VideoID, &signal.Title, &signal.Category, &signal.At); err != nil {
			return nil, err
		}
		signals = append(signals, signal)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return signals, nil
}

func (s *Store) ListDislikedVideoIDs(ctx context.Context, actorID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT video_id FROM video_reactions WHERE actor_id = $1 AND kind = 'dislike'
	`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			return nil, err
		}
		ids = append(ids, videoID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) ListSubscribedChannelIDs(ctx context.Context, actorID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT channel_id FROM subscriptions WHERE actor_id = $1
	`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		ids = append(ids, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) NearestVideos(
	ctx context.Context,
	vector []float32,
	model string,
	limit int,
	excludeIDs []string,
) ([]VideoSimilarity, error) {
	if limit <= 0 {
		limit = 100
	}
	excludeJSON, err := json.Marshal(excludeIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+videoColumnsV+`, 1 - (e.embedding <=> $1::vector) AS similarity
		FROM videos v
		JOIN video_embeddings e ON e.video_id = v.id
		WHERE e.model = $2
		  AND v.status IN ('published', 'demonetized')
		  AND v.visibility = 'public'
		  AND v.id NOT IN (SELECT jsonb_array_elements_text($3::jsonb))
		ORDER BY e.embedding <=> $1::vector
		LIMIT $4
	`, VectorLiteral(vector), model, string(excludeJSON), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	videos := make([]VideoSimilarity, 0, limit)
	for rows.Next() {
		var similarity float64
		video, err := scanVideoWithExtras(rows, &similarity)
		if err != nil {
			return nil, err
		}
		videos = append(videos, VideoSimilarity{Video: video, Similarity: similarity})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return videos, nil
}
