package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrInvalidParent = errors.New("invalid parent comment")

type Comment struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	ParentID  *string   `json:"parent_id,omitempty"`
	ActorID   string    `json:"actor_id"`
	Author    string    `json:"author"`
	Handle    string    `json:"handle"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Body      string    `json:"body"`
	Likes     int64     `json:"likes"`
	Pinned    bool      `json:"pinned"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func scanComment(scanner rowScanner) (Comment, error) {
	var comment Comment
	var parentID sql.NullString
	err := scanner.Scan(
		&comment.ID, &comment.VideoID, &parentID, &comment.ActorID, &comment.Author,
		&comment.Handle, &comment.AvatarURL, &comment.Body, &comment.Likes,
		&comment.Pinned, &comment.Status, &comment.CreatedAt, &comment.UpdatedAt,
	)
	if err != nil {
		return Comment{}, err
	}
	if parentID.Valid {
		value := parentID.String
		comment.ParentID = &value
	}
	return comment, nil
}

func (s *Store) CreateComment(ctx context.Context, comment Comment) (Comment, error) {
	var parentID any
	if comment.ParentID != nil && *comment.ParentID != "" {
		parentID = *comment.ParentID
	} else {
		comment.ParentID = nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if parentID != nil {
		var parentVideoID string
		var parentParentID sql.NullString
		var status string
		err := tx.QueryRowContext(ctx, `
			SELECT video_id, parent_id, status FROM comments WHERE id = $1 FOR UPDATE
		`, parentID).Scan(&parentVideoID, &parentParentID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return Comment{}, ErrInvalidParent
		}
		if err != nil {
			return Comment{}, err
		}
		if parentVideoID != comment.VideoID || parentParentID.Valid || status != "visible" {
			return Comment{}, ErrInvalidParent
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comments (
			id, video_id, parent_id, actor_id, body, status, pinned,
			likes_count, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 'visible', FALSE, 0, $6, $6)
	`, comment.ID, comment.VideoID, parentID, comment.ActorID, comment.Body,
		comment.CreatedAt); err != nil {
		return Comment{}, mapWriteError(err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE videos SET comments_count = comments_count + 1 WHERE id = $1
	`, comment.VideoID)
	if err != nil {
		return Comment{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Comment{}, err
	}
	if updated == 0 {
		return Comment{}, ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return Comment{}, err
	}
	return s.GetComment(ctx, comment.ID)
}

func (s *Store) GetComment(ctx context.Context, id string) (Comment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.video_id, c.parent_id, c.actor_id, a.display_name, a.handle,
		       COALESCE(ch.avatar_url, ''), c.body, c.likes_count, c.pinned,
		       c.status, c.created_at, c.updated_at
		FROM comments c
		JOIN actors a ON a.id = c.actor_id
		LEFT JOIN channels ch ON ch.id = (
			SELECT id FROM channels WHERE actor_id = a.id ORDER BY created_at ASC LIMIT 1
		)
		WHERE c.id = $1
	`, id)
	comment, err := scanComment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, err
	}
	return comment, nil
}

func (s *Store) ListComments(ctx context.Context, videoID, sort string, limit, offset int) ([]Comment, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	order := "c.pinned DESC, c.likes_count DESC, c.created_at DESC, c.id DESC"
	if sort == "newest" {
		order = "c.created_at DESC, c.id DESC"
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM comments WHERE video_id = $1 AND status = 'visible'
	`, videoID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.video_id, c.parent_id, c.actor_id, a.display_name, a.handle,
		       COALESCE(ch.avatar_url, ''), c.body, c.likes_count, c.pinned,
		       c.status, c.created_at, c.updated_at
		FROM comments c
		JOIN actors a ON a.id = c.actor_id
		LEFT JOIN channels ch ON ch.id = (
			SELECT id FROM channels WHERE actor_id = a.id ORDER BY created_at ASC LIMIT 1
		)
		WHERE c.video_id = $1 AND c.status = 'visible'
		ORDER BY `+order+`
		LIMIT $2 OFFSET $3
	`, videoID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	comments := make([]Comment, 0, limit)
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, 0, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return comments, total, nil
}

func (s *Store) LikeComment(ctx context.Context, commentID, actorID string, at time.Time) (int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM comments WHERE id = $1 FOR UPDATE`, commentID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err != nil {
		return 0, false, err
	}
	if status != "visible" {
		return 0, false, ErrNotFound
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO comment_likes (comment_id, actor_id, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (comment_id, actor_id) DO NOTHING
	`, commentID, actorID, at)
	if err != nil {
		return 0, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if inserted > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE comments SET likes_count = likes_count + 1 WHERE id = $1`, commentID,
		); err != nil {
			return 0, false, err
		}
	}

	var likes int64
	if err := tx.QueryRowContext(ctx,
		`SELECT likes_count FROM comments WHERE id = $1`, commentID,
	).Scan(&likes); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return likes, true, nil
}

func (s *Store) UnlikeComment(ctx context.Context, commentID, actorID string) (int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM comments WHERE id = $1 FOR UPDATE`, commentID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err != nil {
		return 0, false, err
	}
	if status != "visible" {
		return 0, false, ErrNotFound
	}

	result, err := tx.ExecContext(ctx, `
		DELETE FROM comment_likes WHERE comment_id = $1 AND actor_id = $2
	`, commentID, actorID)
	if err != nil {
		return 0, false, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if deleted > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE comments SET likes_count = GREATEST(0, likes_count - 1) WHERE id = $1`, commentID,
		); err != nil {
			return 0, false, err
		}
	}

	var likes int64
	if err := tx.QueryRowContext(ctx,
		`SELECT likes_count FROM comments WHERE id = $1`, commentID,
	).Scan(&likes); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return likes, false, nil
}

func (s *Store) ListStudioComments(ctx context.Context, channelID string, limit, offset int) ([]Comment, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM comments c
		JOIN videos v ON v.id = c.video_id
		WHERE v.channel_id = $1 AND c.status = 'visible'
	`, channelID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.video_id, c.parent_id, c.actor_id, a.display_name, a.handle,
		       COALESCE(ch.avatar_url, ''), c.body, c.likes_count, c.pinned,
		       c.status, c.created_at, c.updated_at
		FROM comments c
		JOIN videos v ON v.id = c.video_id
		JOIN actors a ON a.id = c.actor_id
		LEFT JOIN channels ch ON ch.id = (
			SELECT id FROM channels WHERE actor_id = a.id ORDER BY created_at ASC LIMIT 1
		)
		WHERE v.channel_id = $1 AND c.status = 'visible'
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $2 OFFSET $3
	`, channelID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	comments := make([]Comment, 0, limit)
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, 0, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return comments, total, nil
}

func (s *Store) SetCommentPinned(
	ctx context.Context,
	channelID, commentID string,
	pinned bool,
	at time.Time,
) (Comment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var videoID string
	var parentID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT c.video_id, c.parent_id
		FROM comments c
		JOIN videos v ON v.id = c.video_id
		WHERE c.id = $1 AND v.channel_id = $2 AND c.status = 'visible'
		FOR UPDATE
	`, commentID, channelID).Scan(&videoID, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, err
	}
	if parentID.Valid {
		return Comment{}, ErrInvalidInput
	}
	if pinned {
		if _, err := tx.ExecContext(ctx, `
			UPDATE comments SET pinned = FALSE, updated_at = $2
			WHERE video_id = $1 AND pinned = TRUE AND id <> $3
		`, videoID, at, commentID); err != nil {
			return Comment{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE comments SET pinned = $2, updated_at = $3 WHERE id = $1
	`, commentID, pinned, at); err != nil {
		return Comment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Comment{}, err
	}
	return s.GetComment(ctx, commentID)
}

func (s *Store) RemoveComment(ctx context.Context, channelID, commentID string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var videoID string
	err = tx.QueryRowContext(ctx, `
		SELECT c.video_id
		FROM comments c
		JOIN videos v ON v.id = c.video_id
		WHERE c.id = $1 AND v.channel_id = $2
		FOR UPDATE
	`, commentID, channelID).Scan(&videoID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	var visible int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM comments
		WHERE (id = $1 OR parent_id = $1) AND status = 'visible'
	`, commentID).Scan(&visible); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE comments
		SET status = 'removed', pinned = FALSE, updated_at = $2
		WHERE id = $1 OR parent_id = $1
	`, commentID, at); err != nil {
		return err
	}
	if visible > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE videos
			SET comments_count = GREATEST(comments_count - $2, 0)
			WHERE id = $1
		`, videoID, visible); err != nil {
			return err
		}
	}
	return tx.Commit()
}
