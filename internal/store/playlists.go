package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Playlist struct {
	ID          string    `json:"id"`
	ActorID     string    `json:"actor_id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Visibility  string    `json:"visibility"`
	ItemCount   int       `json:"item_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PlaylistItem struct {
	PlaylistID string    `json:"playlist_id"`
	VideoID    string    `json:"video_id"`
	Position   int       `json:"position"`
	AddedAt    time.Time `json:"added_at"`
	Video      Video     `json:"video"`
}

const playlistColumns = `p.id, p.actor_id, p.kind, p.title, p.description, p.visibility,
	(SELECT COUNT(*) FROM playlist_items i WHERE i.playlist_id = p.id) AS item_count,
	p.created_at, p.updated_at`

const playlistReturningColumns = `id, actor_id, kind, title, description, visibility,
	(SELECT COUNT(*) FROM playlist_items i WHERE i.playlist_id = playlists.id) AS item_count,
	created_at, updated_at`

func scanPlaylist(scanner rowScanner) (Playlist, error) {
	var playlist Playlist
	err := scanner.Scan(
		&playlist.ID, &playlist.ActorID, &playlist.Kind, &playlist.Title,
		&playlist.Description, &playlist.Visibility, &playlist.ItemCount,
		&playlist.CreatedAt, &playlist.UpdatedAt,
	)
	return playlist, err
}

func (s *Store) CreatePlaylist(
	ctx context.Context,
	actorID, kind, title, description, visibility string,
	at time.Time,
) (Playlist, error) {
	id, err := NewID("pl_")
	if err != nil {
		return Playlist{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO playlists (id, actor_id, kind, title, description, visibility, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING `+playlistReturningColumns,
		id, actorID, kind, title, description, visibility, at,
	)
	return scanPlaylist(row)
}

func (s *Store) ListPlaylists(ctx context.Context, actorID string) ([]Playlist, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+playlistColumns+`
		FROM playlists p
		WHERE p.actor_id = $1
		ORDER BY p.updated_at DESC, p.id DESC
	`, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	playlists := make([]Playlist, 0)
	for rows.Next() {
		playlist, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		playlists = append(playlists, playlist)
	}
	return playlists, rows.Err()
}

func (s *Store) GetPlaylist(ctx context.Context, id string) (Playlist, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+playlistColumns+`
		FROM playlists p
		WHERE p.id = $1
	`, id)
	playlist, err := scanPlaylist(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	return playlist, err
}

func (s *Store) UpdatePlaylist(
	ctx context.Context,
	id, actorID, title, description, visibility string,
	at time.Time,
) (Playlist, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE playlists p
		SET title = $3, description = $4, visibility = $5, updated_at = $6
		WHERE p.id = $1 AND p.actor_id = $2
		RETURNING `+playlistColumns,
		id, actorID, title, description, visibility, at,
	)
	playlist, err := scanPlaylist(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	return playlist, err
}

func (s *Store) DeletePlaylist(ctx context.Context, id, actorID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM playlists WHERE id = $1 AND actor_id = $2
	`, id, actorID)
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
	return nil
}

func (s *Store) AddPlaylistItem(
	ctx context.Context,
	playlistID, actorID, videoID string,
	at time.Time,
) (Playlist, error) {
	playlist, err := s.GetPlaylist(ctx, playlistID)
	if err != nil {
		return Playlist{}, err
	}
	if playlist.ActorID != actorID {
		return Playlist{}, ErrNotFound
	}
	video, err := s.GetVideo(ctx, videoID)
	if err != nil {
		return Playlist{}, err
	}
	if !((video.Status == "published" || video.Status == "demonetized") &&
		(video.Visibility == "public" || video.Visibility == "unlisted")) {
		ownerActorID, err := s.GetChannelActorID(ctx, video.ChannelID)
		if err != nil || ownerActorID != actorID {
			return Playlist{}, ErrNotFound
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO playlist_items (playlist_id, video_id, position, added_at)
		VALUES ($1, $2, COALESCE((SELECT MAX(position) + 1 FROM playlist_items WHERE playlist_id = $1), 0), $3)
		ON CONFLICT (playlist_id, video_id) DO NOTHING
	`, playlistID, video.ID, at); err != nil {
		return Playlist{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE playlists SET updated_at = $2 WHERE id = $1
	`, playlistID, at); err != nil {
		return Playlist{}, err
	}
	return s.GetPlaylist(ctx, playlistID)
}

func (s *Store) RemovePlaylistItem(ctx context.Context, playlistID, actorID, videoID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM playlist_items i
		USING playlists p
		WHERE i.playlist_id = $1 AND i.video_id = $2
		  AND p.id = i.playlist_id AND p.actor_id = $3
	`, playlistID, videoID, actorID)
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
	_, err = s.db.ExecContext(ctx, `UPDATE playlists SET updated_at = $2 WHERE id = $1`, playlistID, at)
	return err
}

func (s *Store) ListPlaylistItems(
	ctx context.Context,
	playlistID, viewerID string,
	limit, offset int,
) ([]PlaylistItem, int, error) {
	visibility := `(v.status IN ('published', 'demonetized') AND v.visibility IN ('public', 'unlisted'))`
	args := []any{playlistID}
	if viewerID != "" {
		args = append(args, viewerID)
		visibility = "(" + visibility + fmt.Sprintf(
			" OR v.channel_id IN (SELECT id FROM channels WHERE actor_id = $%d))",
			len(args),
		)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM playlist_items i
		JOIN videos v ON v.id = i.video_id
		WHERE i.playlist_id = $1 AND `+visibility,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	dataArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+videoColumnsV+`, i.playlist_id, i.video_id, i.position, i.added_at
		FROM playlist_items i
		JOIN videos v ON v.id = i.video_id
		WHERE i.playlist_id = $1 AND `+visibility+`
		ORDER BY i.position ASC, i.added_at ASC
		LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2),
		dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]PlaylistItem, 0, limit)
	for rows.Next() {
		var item PlaylistItem
		video, err := scanVideoWithExtras(
			rows, &item.PlaylistID, &item.VideoID, &item.Position, &item.AddedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		item.Video = video
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
