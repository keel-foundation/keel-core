package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type FederationInstance struct {
	ActorID       string
	Domain        string
	BaseURL       string
	PublicKeyPEM  string
	PrivateKeyPEM string
	PolicyVersion string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type FederationPeer struct {
	ID                string
	Domain            string
	ActorURL          string
	InboxURL          string
	SharedInboxURL    string
	PublicKeyPEM      string
	ActorType         string
	PreferredUsername string
	Name              string
	Summary           string
	AvatarURL         string
	BannerURL         string
	OutboxURL         string
	Status            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type FederationFollower struct {
	ActorURL       string
	LocalActorID   string
	ActorID        string
	InboxURL       string
	SharedInboxURL string
	ActivityID     string
	AcceptedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type FederationFollowing struct {
	ActorURL         string
	LocalActorID     string
	ActorID          string
	InboxURL         string
	SharedInboxURL   string
	FollowActivityID string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type FederationDelivery struct {
	ID            int64
	ActivityID    string
	Activity      json.RawMessage
	InboxURL      string
	Status        string
	Attempts      int
	MaxAttempts   int
	NextAttemptAt time.Time
	LockedAt      *time.Time
	LockedBy      string
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type FederationInboxActivity struct {
	ActivityID string
	ActorURL   string
	Type       string
	ReceivedAt time.Time
}

func (s *Store) GetFederationInstance(ctx context.Context) (FederationInstance, error) {
	var instance FederationInstance
	err := s.db.QueryRowContext(ctx, `
		SELECT actor_id, domain, base_url, public_key_pem, private_key_pem,
		       policy_version, created_at, updated_at
		FROM federation_instance WHERE id = 'instance'
	`).Scan(
		&instance.ActorID, &instance.Domain, &instance.BaseURL, &instance.PublicKeyPEM,
		&instance.PrivateKeyPEM, &instance.PolicyVersion, &instance.CreatedAt,
		&instance.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FederationInstance{}, ErrNotFound
	}
	return instance, err
}

func (s *Store) SaveFederationInstance(ctx context.Context, instance FederationInstance) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_instance (
			id, actor_id, domain, base_url, public_key_pem, private_key_pem,
			policy_version, created_at, updated_at
		)
		VALUES ('instance', $1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			actor_id = EXCLUDED.actor_id,
			domain = EXCLUDED.domain,
			base_url = EXCLUDED.base_url,
			public_key_pem = EXCLUDED.public_key_pem,
			private_key_pem = EXCLUDED.private_key_pem,
			policy_version = EXCLUDED.policy_version,
			updated_at = EXCLUDED.updated_at
	`, instance.ActorID, instance.Domain, instance.BaseURL, instance.PublicKeyPEM,
		instance.PrivateKeyPEM, instance.PolicyVersion, instance.CreatedAt,
		instance.UpdatedAt)
	return err
}

func (s *Store) UpsertFederationPeer(ctx context.Context, peer FederationPeer) error {
	if peer.ID == "" {
		id, err := NewID("peer_")
		if err != nil {
			return err
		}
		peer.ID = id
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_peers (
			id, domain, actor_url, inbox_url, shared_inbox_url, public_key_pem,
			actor_type, preferred_username, name, summary, avatar_url, banner_url,
			outbox_url, status, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (actor_url) DO UPDATE SET
			domain = EXCLUDED.domain,
			inbox_url = EXCLUDED.inbox_url,
			shared_inbox_url = EXCLUDED.shared_inbox_url,
			public_key_pem = CASE
				WHEN EXCLUDED.public_key_pem <> '' THEN EXCLUDED.public_key_pem
				ELSE federation_peers.public_key_pem
			END,
			actor_type = EXCLUDED.actor_type,
			preferred_username = EXCLUDED.preferred_username,
			name = EXCLUDED.name,
			summary = EXCLUDED.summary,
			avatar_url = EXCLUDED.avatar_url,
			banner_url = EXCLUDED.banner_url,
			outbox_url = EXCLUDED.outbox_url,
			updated_at = EXCLUDED.updated_at
	`, peer.ID, peer.Domain, peer.ActorURL, peer.InboxURL, peer.SharedInboxURL,
		peer.PublicKeyPEM, peer.ActorType, peer.PreferredUsername, peer.Name,
		peer.Summary, peer.AvatarURL, peer.BannerURL, peer.OutboxURL, peer.Status,
		peer.CreatedAt, peer.UpdatedAt)
	return err
}

func scanFederationPeer(scanner rowScanner) (FederationPeer, error) {
	var peer FederationPeer
	err := scanner.Scan(
		&peer.ID, &peer.Domain, &peer.ActorURL, &peer.InboxURL, &peer.SharedInboxURL,
		&peer.PublicKeyPEM, &peer.ActorType, &peer.PreferredUsername, &peer.Name,
		&peer.Summary, &peer.AvatarURL, &peer.BannerURL, &peer.OutboxURL,
		&peer.Status, &peer.CreatedAt, &peer.UpdatedAt,
	)
	return peer, err
}

func (s *Store) GetFederationPeer(ctx context.Context, actorURL string) (FederationPeer, error) {
	peer, err := scanFederationPeer(s.db.QueryRowContext(ctx, `
		SELECT id, domain, actor_url, inbox_url, shared_inbox_url, public_key_pem,
		       actor_type, preferred_username, name, summary, avatar_url, banner_url,
		       outbox_url, status, created_at, updated_at
		FROM federation_peers WHERE actor_url = $1
	`, actorURL))
	if errors.Is(err, sql.ErrNoRows) {
		return FederationPeer{}, ErrNotFound
	}
	return peer, err
}

func (s *Store) GetFederationPeerByDomain(ctx context.Context, domain string) (FederationPeer, error) {
	peer, err := scanFederationPeer(s.db.QueryRowContext(ctx, `
		SELECT id, domain, actor_url, inbox_url, shared_inbox_url, public_key_pem,
		       actor_type, preferred_username, name, summary, avatar_url, banner_url,
		       outbox_url, status, created_at, updated_at
		FROM federation_peers WHERE domain = $1 ORDER BY updated_at DESC LIMIT 1
	`, domain))
	if errors.Is(err, sql.ErrNoRows) {
		return FederationPeer{}, ErrNotFound
	}
	return peer, err
}

func (s *Store) ListFederationPeers(ctx context.Context) ([]FederationPeer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain, actor_url, inbox_url, shared_inbox_url, public_key_pem,
		       actor_type, preferred_username, name, summary, avatar_url, banner_url,
		       outbox_url, status, created_at, updated_at
		FROM federation_peers
		ORDER BY status ASC, domain ASC, actor_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := make([]FederationPeer, 0)
	for rows.Next() {
		peer, err := scanFederationPeer(rows)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, rows.Err()
}

func (s *Store) SetFederationPeerStatus(ctx context.Context, actorURL, status string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE federation_peers SET status = $2, updated_at = $3 WHERE actor_url = $1
	`, actorURL, status, at)
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

func (s *Store) UpsertFederationFollower(ctx context.Context, follower FederationFollower) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_followers (
			actor_url, local_actor_id, actor_id, inbox_url, shared_inbox_url, activity_id,
			accepted_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (actor_url) DO UPDATE SET
			local_actor_id = EXCLUDED.local_actor_id,
			actor_id = EXCLUDED.actor_id,
			inbox_url = EXCLUDED.inbox_url,
			shared_inbox_url = EXCLUDED.shared_inbox_url,
			activity_id = EXCLUDED.activity_id,
			accepted_at = EXCLUDED.accepted_at,
			updated_at = EXCLUDED.updated_at
	`, follower.ActorURL, follower.LocalActorID, follower.ActorID, follower.InboxURL,
		follower.SharedInboxURL, follower.ActivityID, follower.AcceptedAt,
		follower.CreatedAt, follower.UpdatedAt)
	return err
}

func (s *Store) DeleteFederationFollower(ctx context.Context, actorURL string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM federation_followers WHERE actor_url = $1`, actorURL)
	return err
}

func scanFederationFollower(scanner rowScanner) (FederationFollower, error) {
	var follower FederationFollower
	var accepted sql.NullTime
	err := scanner.Scan(
		&follower.ActorURL, &follower.LocalActorID, &follower.ActorID, &follower.InboxURL,
		&follower.SharedInboxURL, &follower.ActivityID, &accepted,
		&follower.CreatedAt, &follower.UpdatedAt,
	)
	if accepted.Valid {
		follower.AcceptedAt = &accepted.Time
	}
	return follower, err
}

func (s *Store) ListFederationFollowers(ctx context.Context) ([]FederationFollower, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT actor_url, local_actor_id, actor_id, inbox_url, shared_inbox_url, activity_id,
		       accepted_at, created_at, updated_at
		FROM federation_followers ORDER BY created_at ASC, actor_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	followers := make([]FederationFollower, 0)
	for rows.Next() {
		follower, err := scanFederationFollower(rows)
		if err != nil {
			return nil, err
		}
		followers = append(followers, follower)
	}
	return followers, rows.Err()
}

func (s *Store) UpsertFederationFollowing(ctx context.Context, following FederationFollowing) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_following (
			actor_url, local_actor_id, actor_id, inbox_url, shared_inbox_url,
			follow_activity_id, status, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (actor_url) DO UPDATE SET
			local_actor_id = EXCLUDED.local_actor_id,
			actor_id = EXCLUDED.actor_id,
			inbox_url = EXCLUDED.inbox_url,
			shared_inbox_url = EXCLUDED.shared_inbox_url,
			follow_activity_id = EXCLUDED.follow_activity_id,
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at
	`, following.ActorURL, following.LocalActorID, following.ActorID, following.InboxURL,
		following.SharedInboxURL, following.FollowActivityID, following.Status,
		following.CreatedAt, following.UpdatedAt)
	return err
}

func scanFederationFollowing(scanner rowScanner) (FederationFollowing, error) {
	var following FederationFollowing
	err := scanner.Scan(
		&following.ActorURL, &following.LocalActorID, &following.ActorID,
		&following.InboxURL, &following.SharedInboxURL, &following.FollowActivityID,
		&following.Status, &following.CreatedAt, &following.UpdatedAt,
	)
	return following, err
}

func (s *Store) GetFederationFollowing(ctx context.Context, actorURL string) (FederationFollowing, error) {
	following, err := scanFederationFollowing(s.db.QueryRowContext(ctx, `
		SELECT actor_url, local_actor_id, actor_id, inbox_url, shared_inbox_url,
		       follow_activity_id, status, created_at, updated_at
		FROM federation_following WHERE actor_url = $1
	`, actorURL))
	if errors.Is(err, sql.ErrNoRows) {
		return FederationFollowing{}, ErrNotFound
	}
	return following, err
}

func (s *Store) SetFederationFollowingStatus(ctx context.Context, actorURL, status string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE federation_following SET status = $2, updated_at = $3 WHERE actor_url = $1
	`, actorURL, status, at)
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

func (s *Store) DeleteFederationFollowing(ctx context.Context, actorURL string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM federation_following WHERE actor_url = $1`, actorURL)
	return err
}

func (s *Store) ListFederationFollowing(ctx context.Context) ([]FederationFollowing, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT actor_url, local_actor_id, actor_id, inbox_url, shared_inbox_url,
		       follow_activity_id, status, created_at, updated_at
		FROM federation_following ORDER BY created_at ASC, actor_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	following := make([]FederationFollowing, 0)
	for rows.Next() {
		item, err := scanFederationFollowing(rows)
		if err != nil {
			return nil, err
		}
		following = append(following, item)
	}
	return following, rows.Err()
}

func (s *Store) EnqueueFederationDelivery(ctx context.Context, activityID string, activity json.RawMessage, inboxURL string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_deliveries (
			activity_id, activity, inbox_url, status, attempts, max_attempts,
			next_attempt_at, created_at, updated_at
		)
		VALUES ($1, $2::jsonb, $3, 'queued', 0, 5, $4, $4, $4)
		ON CONFLICT (activity_id, inbox_url) DO NOTHING
	`, activityID, string(activity), inboxURL, at)
	return err
}

func (s *Store) ClaimFederationDelivery(ctx context.Context, workerID string, now time.Time, lease time.Duration) (FederationDelivery, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM federation_deliveries
			WHERE (status = 'queued' AND next_attempt_at <= $1)
			   OR (status = 'running' AND locked_at < $2)
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE federation_deliveries d
		SET status = 'running', attempts = d.attempts + 1,
		    locked_at = $1, locked_by = $3, updated_at = $1
		FROM candidate c
		WHERE d.id = c.id
		RETURNING d.id, d.activity_id, d.activity, d.inbox_url, d.status,
		          d.attempts, d.max_attempts, d.next_attempt_at, d.locked_at,
		          d.locked_by, d.last_error, d.created_at, d.updated_at
	`, now, now.Add(-lease), workerID)
	delivery, err := scanFederationDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FederationDelivery{}, false, nil
	}
	if err != nil {
		return FederationDelivery{}, false, err
	}
	return delivery, true, nil
}

func scanFederationDelivery(scanner rowScanner) (FederationDelivery, error) {
	var delivery FederationDelivery
	var activity []byte
	var lockedAt sql.NullTime
	err := scanner.Scan(
		&delivery.ID, &delivery.ActivityID, &activity, &delivery.InboxURL, &delivery.Status,
		&delivery.Attempts, &delivery.MaxAttempts, &delivery.NextAttemptAt, &lockedAt,
		&delivery.LockedBy, &delivery.LastError, &delivery.CreatedAt, &delivery.UpdatedAt,
	)
	if lockedAt.Valid {
		delivery.LockedAt = &lockedAt.Time
	}
	if len(activity) > 0 {
		delivery.Activity = json.RawMessage(activity)
	}
	return delivery, err
}

func (s *Store) FinishFederationDelivery(ctx context.Context, id int64, workerID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE federation_deliveries
		SET status = 'succeeded', locked_at = NULL, locked_by = '', updated_at = $3
		WHERE id = $1 AND locked_by = $2 AND status = 'running'
	`, id, workerID, at)
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

func (s *Store) RetryFederationDelivery(ctx context.Context, id int64, workerID, message string, retryAt, at time.Time) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `
		UPDATE federation_deliveries
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
		    next_attempt_at = $3, locked_at = NULL, locked_by = '',
		    last_error = $4, updated_at = $5
		WHERE id = $1 AND locked_by = $2 AND status = 'running'
		RETURNING status
	`, id, workerID, retryAt, message, at).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

func (s *Store) ListFederationDeliveries(ctx context.Context, limit int) ([]FederationDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, activity_id, activity, inbox_url, status, attempts, max_attempts,
		       next_attempt_at, locked_at, locked_by, last_error, created_at, updated_at
		FROM federation_deliveries
		ORDER BY id DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]FederationDelivery, 0, limit)
	for rows.Next() {
		delivery, err := scanFederationDelivery(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) RecordFederationInboxActivity(ctx context.Context, activity FederationInboxActivity) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_inbox_activities (activity_id, actor_url, type, received_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (activity_id) DO NOTHING
	`, activity.ActivityID, activity.ActorURL, activity.Type, activity.ReceivedAt)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted > 0, err
}
