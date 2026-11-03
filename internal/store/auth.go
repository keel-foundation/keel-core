package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrConflict = errors.New("conflict")

type Account struct {
	ID               string
	ActorID          string
	Email            string
	PasswordHash     string
	EmailVerifiedAt  *time.Time
	FailedLoginCount int
	LockedUntil      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Session struct {
	ID         string
	AccountID  string
	TokenHash  string
	Provider   string
	UserAgent  string
	IPAddress  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

type SessionIdentity struct {
	Session Session
	Account Account
	Actor   Actor
	Channel Channel
}

type EmailVerification struct {
	ID         string
	AccountID  string
	CodeHash   string
	Attempts   int
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type PasswordReset struct {
	ID         string
	AccountID  string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type AuthEvent struct {
	AccountID string
	Type      string
	IPAddress string
	UserAgent string
	Metadata  map[string]any
	CreatedAt time.Time
}

// CreateIdentity creates an actor, channel, and account in one transaction.
func (s *Store) CreateIdentity(ctx context.Context, actor Actor, channel Channel, account Account) error {
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

	return tx.Commit()
}

func (s *Store) GetAccountByEmail(ctx context.Context, email string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, actor_id, email, password_hash, email_verified_at,
		       failed_login_count, locked_until, created_at, updated_at
		FROM accounts
		WHERE lower(email) = lower($1)
	`, email)
	return scanAccount(row)
}

func (s *Store) GetAccountByID(ctx context.Context, id string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, actor_id, email, password_hash, email_verified_at,
		       failed_login_count, locked_until, created_at, updated_at
		FROM accounts
		WHERE id = $1
	`, id)
	return scanAccount(row)
}

func scanAccount(row *sql.Row) (Account, error) {
	var account Account
	var verifiedAt, lockedUntil sql.NullTime
	err := row.Scan(
		&account.ID, &account.ActorID, &account.Email, &account.PasswordHash, &verifiedAt,
		&account.FailedLoginCount, &lockedUntil, &account.CreatedAt, &account.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	if verifiedAt.Valid {
		account.EmailVerifiedAt = &verifiedAt.Time
	}
	if lockedUntil.Valid {
		account.LockedUntil = &lockedUntil.Time
	}
	return account, nil
}

func (s *Store) GetChannelByActorID(ctx context.Context, actorID string) (Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelColumns+`, `+channelStatsColumns+` FROM channels WHERE actor_id = $1 ORDER BY created_at ASC LIMIT 1`,
		actorID,
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

func (s *Store) HandleExists(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM actors WHERE lower(handle) = lower($1))`, handle,
	).Scan(&exists)
	return exists, err
}

func (s *Store) UpsertAccount(ctx context.Context, account Account) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (
			id, actor_id, email, password_hash, email_verified_at,
			failed_login_count, locked_until, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			actor_id = EXCLUDED.actor_id,
			email = EXCLUDED.email,
			password_hash = EXCLUDED.password_hash,
			email_verified_at = EXCLUDED.email_verified_at,
			updated_at = EXCLUDED.updated_at
	`, account.ID, account.ActorID, account.Email, account.PasswordHash, account.EmailVerifiedAt,
		account.FailedLoginCount, account.LockedUntil, account.CreatedAt, account.UpdatedAt,
	)
	return mapWriteError(err)
}

func (s *Store) SetEmailVerified(ctx context.Context, accountID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET email_verified_at = $2, updated_at = $2
		WHERE id = $1
	`, accountID, at)
	return err
}

func (s *Store) UpdateAccountPassword(ctx context.Context, accountID, passwordHash string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET password_hash = $2, failed_login_count = 0, locked_until = NULL, updated_at = $3
		WHERE id = $1
	`, accountID, passwordHash, at)
	return err
}

func (s *Store) RecordLoginFailure(ctx context.Context, accountID string, failedCount int, lockedUntil *time.Time, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET failed_login_count = $2, locked_until = $3, updated_at = $4
		WHERE id = $1
	`, accountID, failedCount, lockedUntil, at)
	return err
}

func (s *Store) ResetLoginFailure(ctx context.Context, accountID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET failed_login_count = 0, locked_until = NULL, updated_at = $2
		WHERE id = $1
	`, accountID, at)
	return err
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, account_id, token_hash, provider, user_agent, ip_address,
			created_at, last_seen_at, expires_at, revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, session.ID, session.AccountID, session.TokenHash, session.Provider, session.UserAgent,
		session.IPAddress, session.CreatedAt, session.LastSeenAt, session.ExpiresAt, session.RevokedAt,
	)
	return mapWriteError(err)
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (SessionIdentity, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			s.id, s.account_id, s.token_hash, s.provider, s.user_agent, s.ip_address,
			s.created_at, s.last_seen_at, s.expires_at, s.revoked_at,
			a.id, a.actor_id, a.email, a.password_hash, a.email_verified_at,
			a.failed_login_count, a.locked_until, a.created_at, a.updated_at,
			ac.id, ac.type, ac.handle, ac.display_name, ac.public_key,
			ac.inbox_url, ac.outbox_url, ac.created_at, ac.updated_at,
			ch.id, ch.actor_id, ch.handle, ch.name, ch.description,
			ch.avatar_url, ch.created_at, ch.updated_at
		FROM sessions s
		JOIN accounts a ON a.id = s.account_id
		JOIN actors ac ON ac.id = a.actor_id
		JOIN channels ch ON ch.actor_id = ac.id
		WHERE s.token_hash = $1
		ORDER BY ch.created_at ASC
		LIMIT 1
	`, tokenHash)

	var identity SessionIdentity
	var sessionRevoked, accountVerified, accountLocked sql.NullTime
	err := row.Scan(
		&identity.Session.ID, &identity.Session.AccountID, &identity.Session.TokenHash,
		&identity.Session.Provider, &identity.Session.UserAgent, &identity.Session.IPAddress,
		&identity.Session.CreatedAt, &identity.Session.LastSeenAt, &identity.Session.ExpiresAt,
		&sessionRevoked,
		&identity.Account.ID, &identity.Account.ActorID, &identity.Account.Email,
		&identity.Account.PasswordHash, &accountVerified, &identity.Account.FailedLoginCount,
		&accountLocked, &identity.Account.CreatedAt, &identity.Account.UpdatedAt,
		&identity.Actor.ID, &identity.Actor.Type, &identity.Actor.Handle, &identity.Actor.DisplayName,
		&identity.Actor.PublicKey, &identity.Actor.InboxURL, &identity.Actor.OutboxURL,
		&identity.Actor.CreatedAt, &identity.Actor.UpdatedAt,
		&identity.Channel.ID, &identity.Channel.ActorID, &identity.Channel.Handle,
		&identity.Channel.Name, &identity.Channel.Description, &identity.Channel.AvatarURL,
		&identity.Channel.CreatedAt, &identity.Channel.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionIdentity{}, ErrNotFound
	}
	if err != nil {
		return SessionIdentity{}, err
	}
	if sessionRevoked.Valid {
		identity.Session.RevokedAt = &sessionRevoked.Time
	}
	if accountVerified.Valid {
		identity.Account.EmailVerifiedAt = &accountVerified.Time
	}
	if accountLocked.Valid {
		identity.Account.LockedUntil = &accountLocked.Time
	}
	return identity, nil
}

func (s *Store) TouchSession(ctx context.Context, sessionID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET last_seen_at = $2 WHERE id = $1
	`, sessionID, at)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, sessionID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL
	`, sessionID, at)
	return err
}

func (s *Store) RevokeAccountSessions(ctx context.Context, accountID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = $2 WHERE account_id = $1 AND revoked_at IS NULL
	`, accountID, at)
	return err
}

func (s *Store) CreateEmailVerification(ctx context.Context, verification EmailVerification) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO email_verifications (id, account_id, code_hash, attempts, created_at, expires_at, consumed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, verification.ID, verification.AccountID, verification.CodeHash, verification.Attempts,
		verification.CreatedAt, verification.ExpiresAt, verification.ConsumedAt,
	)
	return mapWriteError(err)
}

func (s *Store) GetActiveEmailVerification(ctx context.Context, accountID string, now time.Time) (EmailVerification, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, account_id, code_hash, attempts, created_at, expires_at, consumed_at
		FROM email_verifications
		WHERE account_id = $1 AND consumed_at IS NULL AND expires_at > $2
		ORDER BY created_at DESC
		LIMIT 1
	`, accountID, now)
	var verification EmailVerification
	var consumedAt sql.NullTime
	err := row.Scan(
		&verification.ID, &verification.AccountID, &verification.CodeHash, &verification.Attempts,
		&verification.CreatedAt, &verification.ExpiresAt, &consumedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailVerification{}, ErrNotFound
	}
	if err != nil {
		return EmailVerification{}, err
	}
	if consumedAt.Valid {
		verification.ConsumedAt = &consumedAt.Time
	}
	return verification, nil
}

func (s *Store) IncrementEmailVerificationAttempts(ctx context.Context, verificationID string) (int, error) {
	var attempts int
	err := s.db.QueryRowContext(ctx, `
		UPDATE email_verifications
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts
	`, verificationID).Scan(&attempts)
	return attempts, err
}

func (s *Store) ConsumeEmailVerification(ctx context.Context, verificationID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE email_verifications
		SET consumed_at = $2
		WHERE id = $1 AND consumed_at IS NULL
	`, verificationID, at)
	return err
}

func (s *Store) InvalidateEmailVerifications(ctx context.Context, accountID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE email_verifications
		SET consumed_at = $2
		WHERE account_id = $1 AND consumed_at IS NULL
	`, accountID, at)
	return err
}

func (s *Store) CreatePasswordReset(ctx context.Context, reset PasswordReset) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO password_resets (id, account_id, token_hash, created_at, expires_at, consumed_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, reset.ID, reset.AccountID, reset.TokenHash, reset.CreatedAt, reset.ExpiresAt, reset.ConsumedAt,
	)
	return mapWriteError(err)
}

func (s *Store) GetPasswordResetByTokenHash(ctx context.Context, tokenHash string) (PasswordReset, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, account_id, token_hash, created_at, expires_at, consumed_at
		FROM password_resets
		WHERE token_hash = $1
	`, tokenHash)
	var reset PasswordReset
	var consumedAt sql.NullTime
	err := row.Scan(
		&reset.ID, &reset.AccountID, &reset.TokenHash, &reset.CreatedAt,
		&reset.ExpiresAt, &consumedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordReset{}, ErrNotFound
	}
	if err != nil {
		return PasswordReset{}, err
	}
	if consumedAt.Valid {
		reset.ConsumedAt = &consumedAt.Time
	}
	return reset, nil
}

func (s *Store) ConsumePasswordReset(ctx context.Context, resetID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE password_resets
		SET consumed_at = $2
		WHERE id = $1 AND consumed_at IS NULL
	`, resetID, at)
	return err
}

func (s *Store) InvalidatePasswordResets(ctx context.Context, accountID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE password_resets
		SET consumed_at = $2
		WHERE account_id = $1 AND consumed_at IS NULL
	`, accountID, at)
	return err
}

func (s *Store) RecordAuthEvent(ctx context.Context, event AuthEvent) error {
	var metadata any
	if event.Metadata != nil {
		body, err := json.Marshal(event.Metadata)
		if err != nil {
			return err
		}
		metadata = body
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_events (account_id, event_type, ip_address, user_agent, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, nullString(event.AccountID), event.Type, event.IPAddress, event.UserAgent, metadata, event.CreatedAt)
	return err
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
