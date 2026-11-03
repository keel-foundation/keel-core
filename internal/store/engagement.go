package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvalidReaction   = errors.New("invalid reaction")
	ErrInvalidLibraryTab = errors.New("invalid library tab")
)

type ReactionState struct {
	Kind  *string `json:"kind"`
	Likes int64   `json:"likes"`
}

func (s *Store) Subscribe(ctx context.Context, actorID, channelID string, at time.Time) (bool, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO subscriptions (actor_id, channel_id, created_at)
		SELECT $1, id, $3 FROM channels WHERE id = $2
		ON CONFLICT (actor_id, channel_id) DO NOTHING
	`, actorID, channelID, at)
	if err != nil {
		return false, 0, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, 0, err
	}

	var subscribers int64
	err = tx.QueryRowContext(ctx,
		`SELECT subscribers FROM channels WHERE id = $1`, channelID,
	).Scan(&subscribers)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, ErrNotFound
	}
	if err != nil {
		return false, 0, err
	}

	if inserted > 0 {
		err = tx.QueryRowContext(ctx, `
			UPDATE channels SET subscribers = subscribers + 1
			WHERE id = $1
			RETURNING subscribers
		`, channelID).Scan(&subscribers)
		if err != nil {
			return false, 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, subscribers, nil
}

func (s *Store) Unsubscribe(ctx context.Context, actorID, channelID string) (bool, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM subscriptions WHERE actor_id = $1 AND channel_id = $2
	`, actorID, channelID)
	if err != nil {
		return false, 0, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, 0, err
	}

	var subscribers int64
	err = tx.QueryRowContext(ctx,
		`SELECT subscribers FROM channels WHERE id = $1`, channelID,
	).Scan(&subscribers)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, ErrNotFound
	}
	if err != nil {
		return false, 0, err
	}

	if deleted > 0 {
		err = tx.QueryRowContext(ctx, `
			UPDATE channels SET subscribers = GREATEST(0, subscribers - 1)
			WHERE id = $1
			RETURNING subscribers
		`, channelID).Scan(&subscribers)
		if err != nil {
			return false, 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return false, subscribers, nil
}

func (s *Store) IsSubscribed(ctx context.Context, actorID, channelID string) (bool, error) {
	var subscribed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM subscriptions WHERE actor_id = $1 AND channel_id = $2
		)
	`, actorID, channelID).Scan(&subscribed)
	return subscribed, err
}

func (s *Store) ListSubscribedChannels(ctx context.Context, actorID string, limit, offset int) ([]Channel, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE actor_id = $1`, actorID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+channelColumns+`, `+channelStatsColumns+`
		FROM channels
		JOIN (
			SELECT channel_id, created_at AS subscribed_at
			FROM subscriptions
			WHERE actor_id = $1
		) s ON s.channel_id = channels.id
		ORDER BY s.subscribed_at DESC, channels.id ASC
		LIMIT $2 OFFSET $3
	`, actorID, limit, offset)
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

func (s *Store) SetReaction(ctx context.Context, actorID, videoID, kind string, at time.Time) (ReactionState, error) {
	if kind != "like" && kind != "dislike" {
		return ReactionState{}, ErrInvalidReaction
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReactionState{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var previous string
	err = tx.QueryRowContext(ctx, `
		SELECT kind FROM video_reactions
		WHERE actor_id = $1 AND video_id = $2
		FOR UPDATE
	`, actorID, videoID).Scan(&previous)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		result, err := tx.ExecContext(ctx, `
			INSERT INTO video_reactions (actor_id, video_id, kind, created_at, updated_at)
			SELECT $1, id, $3, $4, $4 FROM videos WHERE id = $2
			ON CONFLICT (actor_id, video_id) DO NOTHING
		`, actorID, videoID, kind, at)
		if err != nil {
			return ReactionState{}, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return ReactionState{}, err
		}
		if inserted > 0 && kind == "like" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE videos SET likes = likes + 1 WHERE id = $1`, videoID,
			); err != nil {
				return ReactionState{}, err
			}
		}
	case err != nil:
		return ReactionState{}, err
	case previous != kind:
		if _, err := tx.ExecContext(ctx, `
			UPDATE video_reactions SET kind = $3, updated_at = $4
			WHERE actor_id = $1 AND video_id = $2
		`, actorID, videoID, kind, at); err != nil {
			return ReactionState{}, err
		}
		switch {
		case previous == "like" && kind == "dislike":
			if _, err := tx.ExecContext(ctx,
				`UPDATE videos SET likes = GREATEST(0, likes - 1) WHERE id = $1`, videoID,
			); err != nil {
				return ReactionState{}, err
			}
		case previous == "dislike" && kind == "like":
			if _, err := tx.ExecContext(ctx,
				`UPDATE videos SET likes = likes + 1 WHERE id = $1`, videoID,
			); err != nil {
				return ReactionState{}, err
			}
		}
	}

	var likes int64
	err = tx.QueryRowContext(ctx, `SELECT likes FROM videos WHERE id = $1`, videoID).Scan(&likes)
	if errors.Is(err, sql.ErrNoRows) {
		return ReactionState{}, ErrNotFound
	}
	if err != nil {
		return ReactionState{}, err
	}

	if err := tx.Commit(); err != nil {
		return ReactionState{}, err
	}
	return ReactionState{Kind: &kind, Likes: likes}, nil
}

func (s *Store) ClearReaction(ctx context.Context, actorID, videoID string) (ReactionState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReactionState{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var previous string
	err = tx.QueryRowContext(ctx, `
		DELETE FROM video_reactions
		WHERE actor_id = $1 AND video_id = $2
		RETURNING kind
	`, actorID, videoID).Scan(&previous)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return ReactionState{}, err
	case previous == "like":
		if _, err := tx.ExecContext(ctx,
			`UPDATE videos SET likes = GREATEST(0, likes - 1) WHERE id = $1`, videoID,
		); err != nil {
			return ReactionState{}, err
		}
	}

	var likes int64
	err = tx.QueryRowContext(ctx, `SELECT likes FROM videos WHERE id = $1`, videoID).Scan(&likes)
	if errors.Is(err, sql.ErrNoRows) {
		return ReactionState{}, ErrNotFound
	}
	if err != nil {
		return ReactionState{}, err
	}

	if err := tx.Commit(); err != nil {
		return ReactionState{}, err
	}
	return ReactionState{Kind: nil, Likes: likes}, nil
}

func (s *Store) GetReaction(ctx context.Context, actorID, videoID string) (ReactionState, error) {
	var kind sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT kind FROM video_reactions WHERE actor_id = $1 AND video_id = $2
	`, actorID, videoID).Scan(&kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReactionState{}, err
	}

	var likes int64
	err = s.db.QueryRowContext(ctx, `SELECT likes FROM videos WHERE id = $1`, videoID).Scan(&likes)
	if errors.Is(err, sql.ErrNoRows) {
		return ReactionState{}, ErrNotFound
	}
	if err != nil {
		return ReactionState{}, err
	}

	state := ReactionState{Likes: likes}
	if kind.Valid {
		value := kind.String
		state.Kind = &value
	}
	return state, nil
}

func (s *Store) SaveVideo(ctx context.Context, actorID, videoID string, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO saved_videos (actor_id, video_id, created_at)
		SELECT $1, id, $3 FROM videos WHERE id = $2
		ON CONFLICT (actor_id, video_id) DO NOTHING
	`, actorID, videoID, at)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM videos WHERE id = $1)`, videoID,
		).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, ErrNotFound
		}
	}
	return true, nil
}

func (s *Store) UnsaveVideo(ctx context.Context, actorID, videoID string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM saved_videos WHERE actor_id = $1 AND video_id = $2
	`, actorID, videoID); err != nil {
		return false, err
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM videos WHERE id = $1)`, videoID,
	).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	return false, nil
}

func (s *Store) IsSaved(ctx context.Context, actorID, videoID string) (bool, error) {
	var saved bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM saved_videos WHERE actor_id = $1 AND video_id = $2
		)
	`, actorID, videoID).Scan(&saved)
	return saved, err
}

func (s *Store) RecordView(ctx context.Context, actorID, videoID string, at time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO watch_history (actor_id, video_id, watched_at)
		SELECT $1, id, $3 FROM videos WHERE id = $2
		ON CONFLICT (actor_id, video_id) DO UPDATE SET watched_at = EXCLUDED.watched_at
	`, actorID, videoID, at); err != nil {
		return 0, err
	}

	var views int64
	err = tx.QueryRowContext(ctx, `
		UPDATE videos SET views = views + 1
		WHERE id = $1
		RETURNING views
	`, videoID).Scan(&views)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return views, nil
}

func (s *Store) ClearHistory(ctx context.Context, actorID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watch_history WHERE actor_id = $1`, actorID)
	return err
}

func (s *Store) ListLibrary(ctx context.Context, actorID, tab string, limit, offset int) ([]Video, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var source string
	switch tab {
	case "saved":
		source = `SELECT video_id, created_at AS sort_at FROM saved_videos WHERE actor_id = $1`
	case "liked":
		source = `SELECT video_id, updated_at AS sort_at FROM video_reactions WHERE actor_id = $1 AND kind = 'like'`
	case "history":
		source = `SELECT video_id, watched_at AS sort_at FROM watch_history WHERE actor_id = $1`
	default:
		return nil, 0, ErrInvalidLibraryTab
	}

	where := `v.status IN ('published', 'demonetized')
		AND (v.visibility IN ('public', 'unlisted')
			OR EXISTS (
				SELECT 1 FROM channels c
				WHERE c.id = v.channel_id AND c.actor_id = $1
			))`

	var total int
	countSQL := `SELECT COUNT(*) FROM videos v JOIN (` + source + `) x ON x.video_id = v.id WHERE ` + where
	if err := s.db.QueryRowContext(ctx, countSQL, actorID).Scan(&total); err != nil {
		return nil, 0, err
	}

	dataSQL := `SELECT ` + videoColumns + ` FROM videos v JOIN (` + source + `) x ON x.video_id = v.id WHERE ` + where +
		` ORDER BY x.sort_at DESC, v.id DESC LIMIT $2 OFFSET $3`
	rows, err := s.db.QueryContext(ctx, dataSQL, actorID, limit, offset)
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
