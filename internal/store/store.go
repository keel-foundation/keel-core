package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

type Actor struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	PublicKey   string    `json:"public_key,omitempty"`
	InboxURL    string    `json:"inbox_url,omitempty"`
	OutboxURL   string    `json:"outbox_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Channel struct {
	ID          string    `json:"id"`
	ActorID     string    `json:"actor_id"`
	Handle      string    `json:"handle"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Subscribers int64     `json:"subscribers"`
	Verified    bool      `json:"verified"`
	VideoCount  int64     `json:"video_count"`
	TotalViews  int64     `json:"total_views"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Playback struct {
	MP4URL       string `json:"mp4_url"`
	HLSURL       string `json:"hls_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	CaptionsURL  string `json:"captions_url,omitempty"`
}

type Policy struct {
	PolicyVersion      string `json:"policy_version,omitempty"`
	MonetizationStatus string `json:"monetization_status,omitempty"`
	UploadStatus       string `json:"upload_status,omitempty"`
	ScanVerdict        string `json:"scan_verdict,omitempty"`
	ScanReason         string `json:"scan_reason,omitempty"`
	RightsDeclaration  string `json:"rights_declaration,omitempty"`
	RightsHolder       string `json:"rights_holder,omitempty"`
	RightsNotes        string `json:"rights_notes,omitempty"`
}

type Video struct {
	ID              string     `json:"id"`
	PublicID        string     `json:"public_id"`
	ChannelID       string     `json:"channel_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description,omitempty"`
	Status          string     `json:"status"`
	Visibility      string     `json:"visibility"`
	ContentHash     string     `json:"content_hash"`
	DurationSeconds int        `json:"duration_seconds"`
	Playback        Playback   `json:"playback"`
	License         string     `json:"license"`
	Policy          *Policy    `json:"policy,omitempty"`
	Format          string     `json:"format"`
	Category        string     `json:"category"`
	Tags            []string   `json:"tags"`
	Views           int64      `json:"views"`
	Likes           int64      `json:"likes"`
	CommentsCount   int64      `json:"comments_count"`
	CreatedAt       time.Time  `json:"created_at"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) GetActor(ctx context.Context, id string) (Actor, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, handle, display_name, public_key, inbox_url, outbox_url, created_at, updated_at
		FROM actors
		WHERE id = $1
	`, id)
	var actor Actor
	err := row.Scan(
		&actor.ID, &actor.Type, &actor.Handle, &actor.DisplayName, &actor.PublicKey,
		&actor.InboxURL, &actor.OutboxURL, &actor.CreatedAt, &actor.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrNotFound
	}
	if err != nil {
		return Actor{}, err
	}
	return actor, nil
}

func (s *Store) GetChannel(ctx context.Context, id string) (Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelColumns+`, `+channelStatsColumns+` FROM channels WHERE id = $1`,
		id,
	)
	channel, err := scanChannelWithStats(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}
	return channel, nil
}

func (s *Store) GetVideo(ctx context.Context, id string) (Video, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+videoColumns+` FROM videos WHERE id = $1 OR public_id = $1 LIMIT 1`,
		id,
	)
	video, err := scanVideo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Video{}, ErrNotFound
	}
	if err != nil {
		return Video{}, err
	}
	return video, nil
}

func (s *Store) UpsertActor(ctx context.Context, actor Actor) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO actors (id, type, handle, display_name, public_key, inbox_url, outbox_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			type = EXCLUDED.type,
			handle = EXCLUDED.handle,
			display_name = EXCLUDED.display_name,
			public_key = EXCLUDED.public_key,
			inbox_url = EXCLUDED.inbox_url,
			outbox_url = EXCLUDED.outbox_url,
			updated_at = EXCLUDED.updated_at
	`, actor.ID, actor.Type, actor.Handle, actor.DisplayName, actor.PublicKey,
		actor.InboxURL, actor.OutboxURL, actor.CreatedAt, actor.UpdatedAt,
	)
	return err
}

func (s *Store) UpsertChannel(ctx context.Context, channel Channel) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channels (
			id, actor_id, handle, name, description, avatar_url, subscribers, verified,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			actor_id = EXCLUDED.actor_id,
			handle = EXCLUDED.handle,
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			avatar_url = EXCLUDED.avatar_url,
			subscribers = EXCLUDED.subscribers,
			verified = EXCLUDED.verified,
			updated_at = EXCLUDED.updated_at
	`, channel.ID, channel.ActorID, channel.Handle, channel.Name, channel.Description,
		channel.AvatarURL, channel.Subscribers, channel.Verified, channel.CreatedAt,
		channel.UpdatedAt,
	)
	return err
}

func videoValues(video Video) ([]any, error) {
	playback, err := json.Marshal(video.Playback)
	if err != nil {
		return nil, err
	}
	var policy any
	if video.Policy != nil {
		body, err := json.Marshal(video.Policy)
		if err != nil {
			return nil, err
		}
		policy = body
	}
	tags := video.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	var publishedAt any
	if video.PublishedAt != nil {
		publishedAt = *video.PublishedAt
	}
	return []any{
		video.ID, video.PublicID, video.ChannelID, video.Title, video.Description,
		video.Status, video.Visibility, video.ContentHash, video.DurationSeconds,
		playback, video.License, policy, video.Format, video.Category, tagsJSON,
		video.Views, video.Likes, video.CommentsCount, video.CreatedAt, publishedAt,
		video.UpdatedAt,
	}, nil
}

func (s *Store) UpsertVideo(ctx context.Context, video Video) error {
	values, err := videoValues(video)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, videoInsert+`
		ON CONFLICT (id) DO UPDATE SET
			public_id = EXCLUDED.public_id,
			channel_id = EXCLUDED.channel_id,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			status = EXCLUDED.status,
			visibility = EXCLUDED.visibility,
			content_hash = EXCLUDED.content_hash,
			duration_seconds = EXCLUDED.duration_seconds,
			playback = EXCLUDED.playback,
			license = EXCLUDED.license,
			policy = EXCLUDED.policy,
			format = EXCLUDED.format,
			category = EXCLUDED.category,
			tags = EXCLUDED.tags,
			views = EXCLUDED.views,
			likes = EXCLUDED.likes,
			comments_count = EXCLUDED.comments_count,
			published_at = EXCLUDED.published_at,
			updated_at = EXCLUDED.updated_at
	`, values...)
	return err
}
