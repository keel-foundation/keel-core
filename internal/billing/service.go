package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

var (
	ErrDisabled         = errors.New("stripe billing is not configured")
	ErrAccountMissing   = errors.New("creator billing account is not connected")
	ErrAccountNotReady  = errors.New("creator billing account is not ready for payouts")
	ErrInvalidPeriod    = errors.New("period must use YYYY-MM")
	ErrBelowMinimum     = errors.New("settlement amount is below the minimum")
	ErrSettlementFailed = errors.New("settlement has already failed")
)

var periodPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

type Service struct {
	store  *store.Store
	client *StripeClient
	config StripeConfig
}

func New(st *store.Store, config StripeConfig) *Service {
	if config.APIBaseURL == "" {
		config.APIBaseURL = "https://api.stripe.com"
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Second
	}
	if config.ConnectCountry == "" {
		config.ConnectCountry = "US"
	}
	if config.RefreshURL == "" {
		config.RefreshURL = "http://localhost:3000/studio/monetization?stripe=refresh"
	}
	if config.ReturnURL == "" {
		config.ReturnURL = "http://localhost:3000/studio/monetization?stripe=return"
	}
	if config.Currency == "" {
		config.Currency = "usd"
	}
	if config.WebhookTolerance <= 0 {
		config.WebhookTolerance = 5 * time.Minute
	}
	if config.MinSettlementCents <= 0 {
		config.MinSettlementCents = 100
	}
	return &Service{
		store:  st,
		client: NewStripeClient(config),
		config: config,
	}
}

func FromEnv(st *store.Store, getenv func(string) string) (*Service, error) {
	secretKey := strings.TrimSpace(getenv("KEELTUBE_STRIPE_SECRET_KEY"))
	if secretKey == "" {
		return New(st, StripeConfig{}), nil
	}
	timeout := 20 * time.Second
	if raw := strings.TrimSpace(getenv("KEELTUBE_STRIPE_TIMEOUT_SECONDS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("KEELTUBE_STRIPE_TIMEOUT_SECONDS must be a positive integer")
		}
		timeout = time.Duration(value) * time.Second
	}
	tolerance := 5 * time.Minute
	if raw := strings.TrimSpace(getenv("KEELTUBE_STRIPE_WEBHOOK_TOLERANCE_SECONDS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("KEELTUBE_STRIPE_WEBHOOK_TOLERANCE_SECONDS must be a positive integer")
		}
		tolerance = time.Duration(value) * time.Second
	}
	minCents := int64(100)
	if raw := strings.TrimSpace(getenv("KEELTUBE_STRIPE_MIN_SETTLEMENT_CENTS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("KEELTUBE_STRIPE_MIN_SETTLEMENT_CENTS must be a positive integer")
		}
		minCents = value
	}
	return New(st, StripeConfig{
		SecretKey:          secretKey,
		WebhookSecret:      strings.TrimSpace(getenv("KEELTUBE_STRIPE_WEBHOOK_SECRET")),
		APIBaseURL:         strings.TrimSpace(getenv("KEELTUBE_STRIPE_API_BASE_URL")),
		APIVersion:         strings.TrimSpace(getenv("KEELTUBE_STRIPE_API_VERSION")),
		Timeout:            timeout,
		ConnectCountry:     strings.TrimSpace(getenv("KEELTUBE_STRIPE_CONNECT_COUNTRY")),
		RefreshURL:         strings.TrimSpace(getenv("KEELTUBE_STRIPE_CONNECT_REFRESH_URL")),
		ReturnURL:          strings.TrimSpace(getenv("KEELTUBE_STRIPE_CONNECT_RETURN_URL")),
		MinSettlementCents: minCents,
		Currency:           strings.ToLower(strings.TrimSpace(getenv("KEELTUBE_STRIPE_CURRENCY"))),
		WebhookTolerance:   tolerance,
	}), nil
}

func (s *Service) Enabled() bool {
	return s != nil && strings.TrimSpace(s.config.SecretKey) != ""
}

func (s *Service) WebhooksEnabled() bool {
	return s.Enabled() && strings.TrimSpace(s.config.WebhookSecret) != ""
}

func (s *Service) Account(ctx context.Context, channelID string) (store.BillingAccount, error) {
	if !s.Enabled() {
		return store.BillingAccount{}, ErrDisabled
	}
	return s.store.GetBillingAccount(ctx, channelID)
}

func (s *Service) EnsureAccount(
	ctx context.Context,
	channelID, actorID string,
) (store.BillingAccount, AccountLink, error) {
	if !s.Enabled() {
		return store.BillingAccount{}, AccountLink{}, ErrDisabled
	}
	account, err := s.store.GetBillingAccount(ctx, channelID)
	if err == nil && account.StripeAccountID != "" {
		link, linkErr := s.client.CreateAccountLink(
			ctx,
			account.StripeAccountID,
			s.config.RefreshURL,
			s.config.ReturnURL,
		)
		return account, link, linkErr
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.BillingAccount{}, AccountLink{}, err
	}
	monetization, err := s.store.GetMonetization(ctx, channelID)
	if err != nil {
		return store.BillingAccount{}, AccountLink{}, err
	}
	if !monetization.Eligible {
		return store.BillingAccount{}, AccountLink{}, store.ErrNotEligible
	}
	email := ""
	if actorID != "" {
		if creator, accountErr := s.store.GetAccountByActorID(ctx, actorID); accountErr == nil {
			email = creator.Email
		}
	}
	stripeAccount, err := s.client.CreateExpressAccount(
		ctx,
		channelID,
		email,
		s.config.ConnectCountry,
		"connect-account:v2:"+channelID,
	)
	if err != nil {
		return store.BillingAccount{}, AccountLink{}, err
	}
	now := time.Now().UTC()
	account = store.BillingAccount{
		ChannelID:        channelID,
		Provider:         "stripe",
		StripeAccountID:  stripeAccount.ID,
		Status:           accountStatus(stripeAccount),
		DetailsSubmitted: stripeAccount.DetailsSubmitted,
		PayoutsEnabled:   stripeAccount.PayoutsEnabled,
		ChargesEnabled:   stripeAccount.ChargesEnabled,
		RequirementsDue:  requirementsDue(stripeAccount),
		DisabledReason:   stripeAccount.Requirements.DisabledReason,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.UpsertBillingAccount(ctx, account); err != nil {
		return store.BillingAccount{}, AccountLink{}, err
	}
	link, err := s.client.CreateAccountLink(
		ctx,
		account.StripeAccountID,
		s.config.RefreshURL,
		s.config.ReturnURL,
	)
	if err != nil {
		return account, AccountLink{}, err
	}
	return account, link, nil
}

func (s *Service) RefreshLink(
	ctx context.Context,
	channelID string,
) (store.BillingAccount, AccountLink, error) {
	if !s.Enabled() {
		return store.BillingAccount{}, AccountLink{}, ErrDisabled
	}
	account, err := s.store.GetBillingAccount(ctx, channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.BillingAccount{}, AccountLink{}, ErrAccountMissing
		}
		return store.BillingAccount{}, AccountLink{}, err
	}
	link, err := s.client.CreateAccountLink(
		ctx,
		account.StripeAccountID,
		s.config.RefreshURL,
		s.config.ReturnURL,
	)
	return account, link, err
}

func (s *Service) SyncAccount(ctx context.Context, channelID string) (store.BillingAccount, error) {
	if !s.Enabled() {
		return store.BillingAccount{}, ErrDisabled
	}
	account, err := s.store.GetBillingAccount(ctx, channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.BillingAccount{}, ErrAccountMissing
		}
		return store.BillingAccount{}, err
	}
	stripeAccount, err := s.client.GetAccount(ctx, account.StripeAccountID)
	if err != nil {
		return store.BillingAccount{}, err
	}
	account.Status = accountStatus(stripeAccount)
	account.DetailsSubmitted = stripeAccount.DetailsSubmitted
	account.PayoutsEnabled = stripeAccount.PayoutsEnabled
	account.ChargesEnabled = stripeAccount.ChargesEnabled
	account.RequirementsDue = requirementsDue(stripeAccount)
	account.DisabledReason = stripeAccount.Requirements.DisabledReason
	account.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertBillingAccount(ctx, account); err != nil {
		return store.BillingAccount{}, err
	}
	return account, nil
}

func (s *Service) Settle(ctx context.Context, channelID, period string) (store.Payout, error) {
	if !s.Enabled() {
		return store.Payout{}, ErrDisabled
	}
	period = strings.TrimSpace(period)
	if !periodPattern.MatchString(period) {
		return store.Payout{}, ErrInvalidPeriod
	}
	payoutID := settlementPayoutID(channelID, period)
	existing, existingErr := s.store.GetPayout(ctx, payoutID)
	if existingErr == nil {
		if existing.Status == "reversed" {
			return existing, ErrSettlementFailed
		}
		if existing.Status != "pending" && existing.Status != "failed" {
			return existing, nil
		}
	}
	if existingErr != nil && !errors.Is(existingErr, store.ErrNotFound) {
		return store.Payout{}, existingErr
	}

	account, err := s.store.GetBillingAccount(ctx, channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Payout{}, ErrAccountMissing
		}
		return store.Payout{}, err
	}
	if !account.DetailsSubmitted || !account.PayoutsEnabled || account.DisabledReason != "" {
		return store.Payout{}, ErrAccountNotReady
	}

	var payout store.Payout
	var amountCents int64
	if existingErr == nil && existing.Status == "pending" {
		payout = existing
		amountCents = int64(math.Round(existing.Amount * 100))
	} else {
		monetization, err := s.store.GetMonetization(ctx, channelID)
		if err != nil {
			return store.Payout{}, err
		}
		if !monetization.Eligible {
			return store.Payout{}, store.ErrNotEligible
		}
		available := monetization.Balance - monetization.Holdback
		amountCents = int64(math.Round(available * 100))
		if amountCents < s.config.MinSettlementCents {
			return store.Payout{}, ErrBelowMinimum
		}
		currency := strings.ToLower(s.config.Currency)
		if currency == "" {
			currency = strings.ToLower(monetization.Currency)
		}
		payout, err = s.store.ReserveSettlement(ctx, store.SettlementInput{
			PayoutID:  payoutID,
			ChannelID: channelID,
			Period:    period,
			Amount:    float64(amountCents) / 100,
			Currency:  strings.ToUpper(currency),
			At:        time.Now().UTC(),
		})
		if err != nil {
			return store.Payout{}, err
		}
		if payout.Status != "pending" {
			return payout, nil
		}
	}

	currency := strings.ToLower(payout.Currency)
	if currency == "" {
		currency = strings.ToLower(s.config.Currency)
	}
	transfer, err := s.client.CreateTransfer(ctx, TransferInput{
		Amount:         amountCents,
		Currency:       currency,
		Destination:    account.StripeAccountID,
		PayoutID:       payout.ID,
		ChannelID:      channelID,
		Period:         period,
		IdempotencyKey: fmt.Sprintf("settlement:%s:%d", payout.ID, payout.Attempt),
	})
	if err != nil {
		failed, failErr := s.store.FailSettlement(ctx, payout.ID, err.Error(), time.Now().UTC())
		if failErr == nil {
			return failed, err
		}
		return store.Payout{}, err
	}
	completed, err := s.store.CompleteSettlement(
		ctx,
		payout.ID,
		transfer.ID,
		account.StripeAccountID,
		time.Now().UTC(),
	)
	if err != nil {
		return store.Payout{}, err
	}
	return completed, nil
}

func (s *Service) HandleWebhook(ctx context.Context, payload []byte, signature string) error {
	if !s.WebhooksEnabled() {
		return ErrDisabled
	}
	event, err := VerifyWebhook(payload, signature, s.config.WebhookSecret, s.config.WebhookTolerance)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	switch event.Type {
	case "account.updated":
		if err := s.applyAccountEvent(ctx, event.Data.Object, now); err != nil {
			return err
		}
	case "transfer.created", "transfer.updated", "transfer.reversed":
		if err := s.applyTransferEvent(ctx, event.Type, event.Data.Object, now); err != nil {
			return err
		}
	}
	_, err = s.store.RecordStripeEvent(ctx, event.ID, event.Type, event.Account, payload, now)
	return err
}

func (s *Service) applyAccountEvent(ctx context.Context, raw json.RawMessage, now time.Time) error {
	var stripeAccount StripeAccount
	if err := json.Unmarshal(raw, &stripeAccount); err != nil {
		return err
	}
	if stripeAccount.ID == "" {
		return nil
	}
	account, err := s.store.GetBillingAccountByStripeID(ctx, stripeAccount.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	account.Status = accountStatus(stripeAccount)
	account.DetailsSubmitted = stripeAccount.DetailsSubmitted
	account.PayoutsEnabled = stripeAccount.PayoutsEnabled
	account.ChargesEnabled = stripeAccount.ChargesEnabled
	account.RequirementsDue = requirementsDue(stripeAccount)
	account.DisabledReason = stripeAccount.Requirements.DisabledReason
	account.UpdatedAt = now
	return s.store.UpsertBillingAccount(ctx, account)
}

func (s *Service) applyTransferEvent(
	ctx context.Context,
	eventType string,
	raw json.RawMessage,
	now time.Time,
) error {
	var transfer Transfer
	if err := json.Unmarshal(raw, &transfer); err != nil {
		return err
	}
	payoutID := transfer.Metadata["payout_id"]
	var payout store.Payout
	var err error
	if payoutID != "" {
		payout, err = s.store.GetPayout(ctx, payoutID)
	} else {
		payout, err = s.store.GetPayoutByTransferID(ctx, transfer.ID)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if eventType == "transfer.reversed" || transfer.Reversed {
		_, err = s.store.ReverseSettlement(ctx, payout.ID, "Stripe transfer reversed", now)
		return err
	}
	_, err = s.store.CompleteSettlement(ctx, payout.ID, transfer.ID, transfer.Destination, now)
	return err
}

func settlementPayoutID(channelID, period string) string {
	sum := sha256.Sum256([]byte(channelID + ":" + period))
	return "pay_stripe_" + hex.EncodeToString(sum[:10])
}

func requirementsDue(account StripeAccount) []string {
	if account.Requirements.CurrentlyDue == nil {
		return []string{}
	}
	return account.Requirements.CurrentlyDue
}

func accountStatus(account StripeAccount) string {
	if account.Requirements.DisabledReason != "" {
		return "restricted"
	}
	if account.DetailsSubmitted && account.PayoutsEnabled {
		return "active"
	}
	return "pending"
}
