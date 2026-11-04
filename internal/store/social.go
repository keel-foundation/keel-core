package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type AccountIdentity struct {
	ID          string
	AccountID   string
	Provider    string
	Subject     string
	Email       string
	DisplayName string
	AvatarURL   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OAuthState struct {
	StateHash  string
	Provider   string
	Verifier   string
	Nonce      string
	ReturnTo   string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

func (s *Store) CreateOAuthState(ctx context.Context, state OAuthState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_states (
			state_hash, provider, verifier, nonce, return_to,
			expires_at, consumed_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, state.StateHash, state.Provider, state.Verifier, state.Nonce, state.ReturnTo,
		state.ExpiresAt, state.ConsumedAt, state.CreatedAt,
	)
	return mapWriteError(err)
}

// ConsumeOAuthState atomically marks a state value as used. Replays and expired
// values return ErrNotFound.
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash string, now time.Time) (OAuthState, error) {
	var state OAuthState
	var consumedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		UPDATE oauth_states
		SET consumed_at = $2
		WHERE state_hash = $1 AND consumed_at IS NULL AND expires_at > $2
		RETURNING state_hash, provider, verifier, nonce, return_to,
		          expires_at, consumed_at, created_at
	`, stateHash, now).Scan(
		&state.StateHash, &state.Provider, &state.Verifier, &state.Nonce, &state.ReturnTo,
		&state.ExpiresAt, &consumedAt, &state.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthState{}, ErrNotFound
	}
	if err != nil {
		return OAuthState{}, err
	}
	if consumedAt.Valid {
		state.ConsumedAt = &consumedAt.Time
	}
	return state, nil
}

func (s *Store) PruneOAuthStates(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM oauth_states
		WHERE expires_at < $1 OR (consumed_at IS NOT NULL AND consumed_at < $1)
	`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanAccountIdentity(row *sql.Row) (AccountIdentity, error) {
	var identity AccountIdentity
	err := row.Scan(
		&identity.ID, &identity.AccountID, &identity.Provider, &identity.Subject,
		&identity.Email, &identity.DisplayName, &identity.AvatarURL,
		&identity.CreatedAt, &identity.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountIdentity{}, ErrNotFound
	}
	if err != nil {
		return AccountIdentity{}, err
	}
	return identity, nil
}

func (s *Store) GetAccountIdentity(ctx context.Context, provider, subject string) (AccountIdentity, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, account_id, provider, subject, email, display_name, avatar_url,
		       created_at, updated_at
		FROM account_identities
		WHERE provider = $1 AND subject = $2
	`, provider, subject)
	return scanAccountIdentity(row)
}

func (s *Store) GetAccountByIdentity(ctx context.Context, provider, subject string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.actor_id, a.email, a.password_hash, a.email_verified_at,
		       a.failed_login_count, a.locked_until, a.created_at, a.updated_at
		FROM accounts a
		JOIN account_identities i ON i.account_id = a.id
		WHERE i.provider = $1 AND i.subject = $2
	`, provider, subject)
	return scanAccount(row)
}

// CreateSocialIdentity creates the actor, channel, account, and provider
// identity in one transaction. The account has no password.
func (s *Store) CreateSocialIdentity(
	ctx context.Context,
	actor Actor,
	channel Channel,
	account Account,
	identity AccountIdentity,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO actors (id, type, handle, display_name, public_key, inbox_url, outbox_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, actor.ID, actor.Type, actor.Handle, actor.DisplayName, actor.PublicKey,
		actor.InboxURL, actor.OutboxURL, actor.CreatedAt, actor.UpdatedAt,
	); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO channels (id, actor_id, handle, name, description, avatar_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, channel.ID, channel.ActorID, channel.Handle, channel.Name, channel.Description,
		channel.AvatarURL, channel.CreatedAt, channel.UpdatedAt,
	); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (
			id, actor_id, email, password_hash, email_verified_at,
			failed_login_count, locked_until, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, account.ID, account.ActorID, account.Email, account.PasswordHash, account.EmailVerifiedAt,
		account.FailedLoginCount, account.LockedUntil, account.CreatedAt, account.UpdatedAt,
	); err != nil {
		return mapWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_identities (
			id, account_id, provider, subject, email, display_name, avatar_url,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, identity.ID, account.ID, identity.Provider, identity.Subject, identity.Email,
		identity.DisplayName, identity.AvatarURL, identity.CreatedAt, identity.UpdatedAt,
	); err != nil {
		return mapWriteError(err)
	}
	return tx.Commit()
}

// LinkSocialIdentity attaches a provider identity to an existing account. When
// verifiedAt is set, the account email is marked verified because the provider
// has proven control of the same address.
func (s *Store) LinkSocialIdentity(
	ctx context.Context,
	identity AccountIdentity,
	verifiedAt *time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_identities (
			id, account_id, provider, subject, email, display_name, avatar_url,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (provider, subject) DO UPDATE SET
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url,
			updated_at = EXCLUDED.updated_at
	`, identity.ID, identity.AccountID, identity.Provider, identity.Subject, identity.Email,
		identity.DisplayName, identity.AvatarURL, identity.CreatedAt, identity.UpdatedAt,
	); err != nil {
		return mapWriteError(err)
	}
	if verifiedAt != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE accounts
			SET email_verified_at = COALESCE(email_verified_at, $2), updated_at = $2
			WHERE id = $1
		`, identity.AccountID, *verifiedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
