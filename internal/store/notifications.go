package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const notificationColumns = `id, actor_id, channel_id, kind, title, body, href, payload, read_at, created_at`

type Notification struct {
	ID        string          `json:"id"`
	ActorID   string          `json:"-"`
	ChannelID string          `json:"channel_id,omitempty"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Href      string          `json:"href"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Read      bool            `json:"read"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type NotificationInput struct {
	ActorID   string          `json:"actor_id"`
	ChannelID string          `json:"channel_id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Href      string          `json:"href"`
	Payload   json.RawMessage `json:"payload"`
	DedupeKey string          `json:"dedupe_key"`
	CreatedAt time.Time       `json:"created_at"`
}

func scanNotification(scanner rowScanner) (Notification, error) {
	var notification Notification
	var channelID sql.NullString
	var payload []byte
	var readAt sql.NullTime
	if err := scanner.Scan(
		&notification.ID, &notification.ActorID, &channelID, &notification.Kind,
		&notification.Title, &notification.Body, &notification.Href, &payload,
		&readAt, &notification.CreatedAt,
	); err != nil {
		return Notification{}, err
	}
	if channelID.Valid {
		notification.ChannelID = channelID.String
	}
	if len(payload) > 0 {
		notification.Payload = json.RawMessage(payload)
	}
	if readAt.Valid {
		value := readAt.Time
		notification.ReadAt = &value
		notification.Read = true
	}
	return notification, nil
}

// CreateNotification inserts a notification idempotently when DedupeKey is set.
// The boolean result reports whether a new row was created.
func (s *Store) CreateNotification(ctx context.Context, input NotificationInput) (Notification, bool, error) {
	if input.ActorID == "" || input.Kind == "" || input.Title == "" {
		return Notification{}, false, ErrInvalidInput
	}
	id, err := NewID("ntf_")
	if err != nil {
		return Notification{}, false, err
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var channelID any
	if input.ChannelID != "" {
		channelID = input.ChannelID
	}
	var dedupeKey any
	if input.DedupeKey != "" {
		dedupeKey = input.DedupeKey
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO notifications (
			id, actor_id, channel_id, kind, title, body, href, payload, dedupe_key, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT DO NOTHING
		RETURNING `+notificationColumns,
		id, input.ActorID, channelID, input.Kind, input.Title, input.Body,
		input.Href, payload, dedupeKey, createdAt,
	)
	notification, err := scanNotification(row)
	if err == nil {
		return notification, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Notification{}, false, err
	}
	if input.DedupeKey == "" {
		return Notification{}, false, ErrConflict
	}
	row = s.db.QueryRowContext(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications
		WHERE actor_id = $1 AND dedupe_key = $2
	`, input.ActorID, input.DedupeKey)
	notification, err = scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, false, ErrNotFound
	}
	if err != nil {
		return Notification{}, false, err
	}
	return notification, false, nil
}

func (s *Store) ListNotifications(
	ctx context.Context,
	actorID string,
	unreadOnly bool,
	limit, offset int,
) ([]Notification, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := "actor_id = $1"
	if unreadOnly {
		where += " AND read_at IS NULL"
	}
	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notifications WHERE "+where, actorID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications
		WHERE `+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, actorID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	notifications := make([]Notification, 0, limit)
	for rows.Next() {
		notification, err := scanNotification(rows)
		if err != nil {
			return nil, 0, err
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return notifications, total, nil
}

func (s *Store) CountUnreadNotifications(ctx context.Context, actorID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notifications WHERE actor_id = $1 AND read_at IS NULL
	`, actorID).Scan(&count)
	return count, err
}

func (s *Store) MarkNotificationRead(
	ctx context.Context,
	actorID, notificationID string,
	at time.Time,
) (Notification, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE notifications
		SET read_at = COALESCE(read_at, $3)
		WHERE id = $1 AND actor_id = $2
		RETURNING `+notificationColumns,
		notificationID, actorID, at,
	)
	notification, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, ErrNotFound
	}
	if err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, actorID string, at time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE notifications SET read_at = $2
		WHERE actor_id = $1 AND read_at IS NULL
	`, actorID, at)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
