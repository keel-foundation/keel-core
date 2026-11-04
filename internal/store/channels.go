package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ChannelProfileInput struct {
	Name        string
	Description string
	AvatarURL   string
	BannerURL   string
	Links       []ChannelLink
}

func (s *Store) UpdateChannelProfile(
	ctx context.Context,
	channelID string,
	input ChannelProfileInput,
	at time.Time,
) (Channel, error) {
	links := input.Links
	if links == nil {
		links = []ChannelLink{}
	}
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return Channel{}, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE channels
		SET name = $2, description = $3, avatar_url = $4, banner_url = $5,
		    links = $6, updated_at = $7
		WHERE id = $1
	`, channelID, input.Name, input.Description, input.AvatarURL,
		input.BannerURL, linksJSON, at)
	if err != nil {
		return Channel{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return Channel{}, err
	}
	if updated == 0 {
		return Channel{}, ErrNotFound
	}
	return s.GetChannel(ctx, channelID)
}

func (s *Store) ListChannels(
	ctx context.Context,
	ids []string,
	query string,
	limit, offset int,
) ([]Channel, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	clauses := []string{"1 = 1"}
	args := make([]any, 0, len(ids)+3)
	if len(ids) > 0 {
		placeholders := make([]string, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		clauses = append(clauses, "id IN ("+strings.Join(placeholders, ", ")+")")
	}
	if query != "" {
		args = append(args, query)
		clauses = append(clauses, fmt.Sprintf(
			"(strpos(lower(name), lower($%d)) > 0 OR strpos(lower(handle), lower($%d)) > 0 OR strpos(lower(coalesce(description, '')), lower($%d)) > 0)",
			len(args), len(args), len(args),
		))
	}
	where := strings.Join(clauses, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM channels WHERE "+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	dataArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+channelColumns+`, `+channelStatsColumns+`
		FROM channels
		WHERE `+where+`
		ORDER BY subscribers DESC, name ASC, id ASC
		LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2),
		dataArgs...,
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
