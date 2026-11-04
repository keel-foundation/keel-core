package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Monetization struct {
	ChannelID    string     `json:"channel_id"`
	Eligible     bool       `json:"eligible"`
	RPM          float64    `json:"rpm"`
	Holdback     float64    `json:"holdback"`
	Balance      float64    `json:"balance"`
	Currency     string     `json:"currency"`
	NextPayoutAt *time.Time `json:"next_payout,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Earning struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id,omitempty"`
	Date        time.Time `json:"date"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Amount      float64   `json:"amount"`
	Balance     float64   `json:"balance"`
}

type Payout struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id,omitempty"`
	Period    string    `json:"period"`
	Amount    float64   `json:"amount"`
	Status    string    `json:"status"`
	Date      time.Time `json:"date"`
}

func (s *Store) GetMonetization(ctx context.Context, channelID string) (Monetization, error) {
	var monetization Monetization
	var nextPayout sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT channel_id, eligible, rpm, holdback, balance, currency,
		       next_payout_at, updated_at
		FROM creator_monetization
		WHERE channel_id = $1
	`, channelID).Scan(
		&monetization.ChannelID,
		&monetization.Eligible,
		&monetization.RPM,
		&monetization.Holdback,
		&monetization.Balance,
		&monetization.Currency,
		&nextPayout,
		&monetization.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Monetization{ChannelID: channelID, Currency: "USD"}, nil
	}
	if err != nil {
		return Monetization{}, err
	}
	if nextPayout.Valid {
		value := nextPayout.Time
		monetization.NextPayoutAt = &value
	}
	return monetization, nil
}

func (s *Store) ListEarnings(ctx context.Context, channelID string, limit, offset int) ([]Earning, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM creator_earnings WHERE channel_id = $1
	`, channelID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, occurred_at, description, kind, amount, balance
		FROM creator_earnings
		WHERE channel_id = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, channelID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	earnings := make([]Earning, 0, limit)
	for rows.Next() {
		var earning Earning
		if err := rows.Scan(
			&earning.ID,
			&earning.ChannelID,
			&earning.Date,
			&earning.Description,
			&earning.Type,
			&earning.Amount,
			&earning.Balance,
		); err != nil {
			return nil, 0, err
		}
		earnings = append(earnings, earning)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return earnings, total, nil
}

func (s *Store) ListPayouts(ctx context.Context, channelID string, limit, offset int) ([]Payout, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM creator_payouts WHERE channel_id = $1
	`, channelID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, period, amount, status, paid_at
		FROM creator_payouts
		WHERE channel_id = $1
		ORDER BY paid_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, channelID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	payouts := make([]Payout, 0, limit)
	for rows.Next() {
		var payout Payout
		if err := rows.Scan(
			&payout.ID,
			&payout.ChannelID,
			&payout.Period,
			&payout.Amount,
			&payout.Status,
			&payout.Date,
		); err != nil {
			return nil, 0, err
		}
		payouts = append(payouts, payout)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return payouts, total, nil
}

func (s *Store) UpsertMonetization(ctx context.Context, monetization Monetization) error {
	if monetization.Currency == "" {
		monetization.Currency = "USD"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO creator_monetization (
			channel_id, eligible, rpm, holdback, balance, currency,
			next_payout_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (channel_id) DO UPDATE SET
			eligible = EXCLUDED.eligible,
			rpm = EXCLUDED.rpm,
			holdback = EXCLUDED.holdback,
			balance = EXCLUDED.balance,
			currency = EXCLUDED.currency,
			next_payout_at = EXCLUDED.next_payout_at,
			updated_at = EXCLUDED.updated_at
	`, monetization.ChannelID, monetization.Eligible, monetization.RPM,
		monetization.Holdback, monetization.Balance, monetization.Currency,
		monetization.NextPayoutAt, monetization.UpdatedAt)
	return err
}

func (s *Store) UpsertEarning(ctx context.Context, earning Earning) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO creator_earnings (
			id, channel_id, occurred_at, description, kind, amount, balance, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $3)
		ON CONFLICT (id) DO UPDATE SET
			channel_id = EXCLUDED.channel_id,
			occurred_at = EXCLUDED.occurred_at,
			description = EXCLUDED.description,
			kind = EXCLUDED.kind,
			amount = EXCLUDED.amount,
			balance = EXCLUDED.balance
	`, earning.ID, earning.ChannelID, earning.Date, earning.Description,
		earning.Type, earning.Amount, earning.Balance)
	return err
}

func (s *Store) UpsertPayout(ctx context.Context, payout Payout) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO creator_payouts (
			id, channel_id, period, amount, status, paid_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (id) DO UPDATE SET
			channel_id = EXCLUDED.channel_id,
			period = EXCLUDED.period,
			amount = EXCLUDED.amount,
			status = EXCLUDED.status,
			paid_at = EXCLUDED.paid_at
	`, payout.ID, payout.ChannelID, payout.Period, payout.Amount, payout.Status, payout.Date)
	return err
}
