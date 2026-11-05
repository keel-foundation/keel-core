package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotEligible         = errors.New("creator is not eligible for settlement")
	ErrInsufficientBalance = errors.New("insufficient settlement balance")
)

type BillingAccount struct {
	ChannelID        string    `json:"channel_id"`
	Provider         string    `json:"provider"`
	StripeAccountID  string    `json:"stripe_account_id"`
	Status           string    `json:"status"`
	DetailsSubmitted bool      `json:"details_submitted"`
	PayoutsEnabled   bool      `json:"payouts_enabled"`
	ChargesEnabled   bool      `json:"charges_enabled"`
	RequirementsDue  []string  `json:"requirements_due"`
	DisabledReason   string    `json:"disabled_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type SettlementInput struct {
	PayoutID  string
	ChannelID string
	Period    string
	Amount    float64
	Currency  string
	At        time.Time
}

func (s *Store) GetBillingAccount(ctx context.Context, channelID string) (BillingAccount, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT channel_id, provider, stripe_account_id, status, details_submitted,
		       payouts_enabled, charges_enabled, requirements_due, disabled_reason,
		       created_at, updated_at
		FROM creator_billing_accounts
		WHERE channel_id = $1
	`, channelID)
	return scanBillingAccount(row)
}

func (s *Store) GetBillingAccountByStripeID(ctx context.Context, stripeAccountID string) (BillingAccount, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT channel_id, provider, stripe_account_id, status, details_submitted,
		       payouts_enabled, charges_enabled, requirements_due, disabled_reason,
		       created_at, updated_at
		FROM creator_billing_accounts
		WHERE stripe_account_id = $1
	`, stripeAccountID)
	return scanBillingAccount(row)
}

func scanBillingAccount(row rowScanner) (BillingAccount, error) {
	var account BillingAccount
	var requirements []byte
	err := row.Scan(
		&account.ChannelID,
		&account.Provider,
		&account.StripeAccountID,
		&account.Status,
		&account.DetailsSubmitted,
		&account.PayoutsEnabled,
		&account.ChargesEnabled,
		&requirements,
		&account.DisabledReason,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BillingAccount{}, ErrNotFound
	}
	if err != nil {
		return BillingAccount{}, err
	}
	if len(requirements) > 0 {
		if err := json.Unmarshal(requirements, &account.RequirementsDue); err != nil {
			return BillingAccount{}, err
		}
	}
	if account.RequirementsDue == nil {
		account.RequirementsDue = []string{}
	}
	return account, nil
}

func (s *Store) UpsertBillingAccount(ctx context.Context, account BillingAccount) error {
	if account.Provider == "" {
		account.Provider = "stripe"
	}
	if account.Status == "" {
		account.Status = "pending"
	}
	if account.RequirementsDue == nil {
		account.RequirementsDue = []string{}
	}
	requirements, err := json.Marshal(account.RequirementsDue)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO creator_billing_accounts (
			channel_id, provider, stripe_account_id, status, details_submitted,
			payouts_enabled, charges_enabled, requirements_due, disabled_reason,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (channel_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			stripe_account_id = EXCLUDED.stripe_account_id,
			status = EXCLUDED.status,
			details_submitted = EXCLUDED.details_submitted,
			payouts_enabled = EXCLUDED.payouts_enabled,
			charges_enabled = EXCLUDED.charges_enabled,
			requirements_due = EXCLUDED.requirements_due,
			disabled_reason = EXCLUDED.disabled_reason,
			updated_at = EXCLUDED.updated_at
	`, account.ChannelID, account.Provider, account.StripeAccountID, account.Status,
		account.DetailsSubmitted, account.PayoutsEnabled, account.ChargesEnabled,
		requirements, account.DisabledReason, account.CreatedAt, account.UpdatedAt)
	return err
}

func (s *Store) GetPayout(ctx context.Context, payoutID string) (Payout, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, channel_id, period, amount, status, paid_at,
		       provider, currency, stripe_transfer_id, stripe_account_id,
		       failure_reason, attempt, updated_at
		FROM creator_payouts
		WHERE id = $1
	`, payoutID)
	return scanPayout(row)
}

func (s *Store) GetPayoutByTransferID(ctx context.Context, transferID string) (Payout, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, channel_id, period, amount, status, paid_at,
		       provider, currency, stripe_transfer_id, stripe_account_id,
		       failure_reason, attempt, updated_at
		FROM creator_payouts
		WHERE stripe_transfer_id = $1
	`, transferID)
	return scanPayout(row)
}

func (s *Store) ReserveSettlement(ctx context.Context, input SettlementInput) (Payout, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Payout{}, err
	}
	defer tx.Rollback()

	existing, err := getPayoutTx(ctx, tx, input.PayoutID)
	retry := false
	if err == nil {
		if existing.Status != "failed" {
			return existing, nil
		}
		retry = true
	} else if !errors.Is(err, ErrNotFound) {
		return Payout{}, err
	}

	var eligible bool
	var balance, holdback float64
	var currency string
	err = tx.QueryRowContext(ctx, `
		SELECT eligible, balance, holdback, currency
		FROM creator_monetization
		WHERE channel_id = $1
		FOR UPDATE
	`, input.ChannelID).Scan(&eligible, &balance, &holdback, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return Payout{}, ErrNotEligible
	}
	if err != nil {
		return Payout{}, err
	}
	if !eligible {
		return Payout{}, ErrNotEligible
	}
	if input.Amount <= 0 || input.Amount > balance-holdback+0.005 {
		return Payout{}, ErrInsufficientBalance
	}
	if input.Currency == "" {
		input.Currency = currency
	}
	newBalance := balance - input.Amount
	if _, err := tx.ExecContext(ctx, `
		UPDATE creator_monetization
		SET balance = $2, updated_at = $3
		WHERE channel_id = $1
	`, input.ChannelID, newBalance, input.At); err != nil {
		return Payout{}, err
	}

	if retry {
		if _, err := tx.ExecContext(ctx, `
			UPDATE creator_payouts
			SET status = 'pending',
			    amount = $2,
			    currency = $3,
			    paid_at = $4,
			    stripe_transfer_id = NULL,
			    stripe_account_id = NULL,
			    failure_reason = '',
			    attempt = attempt + 1,
			    updated_at = $4
			WHERE id = $1
		`, input.PayoutID, input.Amount, input.Currency, input.At); err != nil {
			return Payout{}, err
		}
	} else if _, err := tx.ExecContext(ctx, `
		INSERT INTO creator_payouts (
			id, channel_id, period, amount, status, paid_at, created_at,
			provider, currency, failure_reason, attempt, updated_at
		)
		VALUES ($1, $2, $3, $4, 'pending', $5, $5, 'stripe', $6, '', 1, $5)
	`, input.PayoutID, input.ChannelID, input.Period, input.Amount, input.At, input.Currency); err != nil {
		return Payout{}, err
	}

	earningID, err := NewID("ern_")
	if err != nil {
		return Payout{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO creator_earnings (
			id, channel_id, occurred_at, description, kind, amount, balance, created_at
		)
		VALUES ($1, $2, $3, $4, 'payout', $5, $6, $3)
	`, earningID, input.ChannelID, input.At, "Stripe settlement "+input.Period,
		-input.Amount, newBalance); err != nil {
		return Payout{}, err
	}

	payout, err := getPayoutTx(ctx, tx, input.PayoutID)
	if err != nil {
		return Payout{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payout{}, err
	}
	return payout, nil
}

func getPayoutTx(ctx context.Context, tx *sql.Tx, payoutID string) (Payout, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, channel_id, period, amount, status, paid_at,
		       provider, currency, stripe_transfer_id, stripe_account_id,
		       failure_reason, attempt, updated_at
		FROM creator_payouts
		WHERE id = $1
		FOR UPDATE
	`, payoutID)
	return scanPayout(row)
}

func (s *Store) CompleteSettlement(
	ctx context.Context,
	payoutID, transferID, accountID string,
	at time.Time,
) (Payout, error) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE creator_payouts
		SET status = 'paid',
		    paid_at = $4,
		    stripe_transfer_id = $2,
		    stripe_account_id = $3,
		    failure_reason = '',
		    updated_at = $4
		WHERE id = $1 AND status NOT IN ('reversed', 'failed')
	`, payoutID, nullString(transferID), nullString(accountID), at); err != nil {
		return Payout{}, err
	}
	return s.GetPayout(ctx, payoutID)
}

func (s *Store) FailSettlement(
	ctx context.Context,
	payoutID, reason string,
	at time.Time,
) (Payout, error) {
	return s.restoreSettlement(ctx, payoutID, "failed", reason, "Stripe settlement failed", at)
}

func (s *Store) ReverseSettlement(
	ctx context.Context,
	payoutID, reason string,
	at time.Time,
) (Payout, error) {
	return s.restoreSettlement(ctx, payoutID, "reversed", reason, "Stripe settlement reversed", at)
}

func (s *Store) restoreSettlement(
	ctx context.Context,
	payoutID, nextStatus, reason, description string,
	at time.Time,
) (Payout, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Payout{}, err
	}
	defer tx.Rollback()

	payout, err := getPayoutTx(ctx, tx, payoutID)
	if err != nil {
		return Payout{}, err
	}
	if payout.Status == nextStatus || payout.Status == "failed" || payout.Status == "reversed" {
		return payout, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE creator_monetization
		SET balance = balance + $2, updated_at = $3
		WHERE channel_id = $1
	`, payout.ChannelID, payout.Amount, at); err != nil {
		return Payout{}, err
	}
	var balance float64
	if err := tx.QueryRowContext(ctx, `
		SELECT balance FROM creator_monetization WHERE channel_id = $1
	`, payout.ChannelID).Scan(&balance); err != nil {
		return Payout{}, err
	}
	earningID, err := NewID("ern_")
	if err != nil {
		return Payout{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO creator_earnings (
			id, channel_id, occurred_at, description, kind, amount, balance, created_at
		)
		VALUES ($1, $2, $3, $4, 'adjustment', $5, $6, $3)
	`, earningID, payout.ChannelID, at, description, payout.Amount, balance); err != nil {
		return Payout{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE creator_payouts
		SET status = $2, failure_reason = $3, updated_at = $4
		WHERE id = $1
	`, payoutID, nextStatus, reason, at); err != nil {
		return Payout{}, err
	}
	updated, err := getPayoutTx(ctx, tx, payoutID)
	if err != nil {
		return Payout{}, err
	}
	if err := tx.Commit(); err != nil {
		return Payout{}, err
	}
	return updated, nil
}

func (s *Store) RecordStripeEvent(
	ctx context.Context,
	eventID, eventType, accountID string,
	payload []byte,
	at time.Time,
) (bool, error) {
	var storedID string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO stripe_webhook_events (id, type, account_id, payload, received_at)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`, eventID, eventType, accountID, string(payload), at).Scan(&storedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
