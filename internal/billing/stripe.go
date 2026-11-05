package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrStripeNotConfigured = errors.New("stripe is not configured")
	ErrInvalidSignature    = errors.New("invalid stripe webhook signature")
)

type StripeConfig struct {
	SecretKey          string
	WebhookSecret      string
	APIBaseURL         string
	APIVersion         string
	Timeout            time.Duration
	ConnectCountry     string
	RefreshURL         string
	ReturnURL          string
	MinSettlementCents int64
	Currency           string
	WebhookTolerance   time.Duration
}

type StripeAccount struct {
	ID               string `json:"id"`
	DetailsSubmitted bool   `json:"details_submitted"`
	PayoutsEnabled   bool   `json:"payouts_enabled"`
	ChargesEnabled   bool   `json:"charges_enabled"`
	Requirements     struct {
		CurrentlyDue   []string `json:"currently_due"`
		DisabledReason string   `json:"disabled_reason"`
	} `json:"requirements"`
}

type AccountLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Transfer struct {
	ID          string            `json:"id"`
	Amount      int64             `json:"amount"`
	Currency    string            `json:"currency"`
	Destination string            `json:"destination"`
	Reversed    bool              `json:"reversed"`
	Metadata    map[string]string `json:"metadata"`
}

type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Account string `json:"account"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

type StripeClient struct {
	secretKey  string
	baseURL    string
	apiVersion string
	http       *http.Client
}

func NewStripeClient(config StripeConfig) *StripeClient {
	baseURL := strings.TrimRight(config.APIBaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.stripe.com"
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &StripeClient{
		secretKey:  config.SecretKey,
		baseURL:    baseURL,
		apiVersion: config.APIVersion,
		http:       &http.Client{Timeout: timeout},
	}
}

type stripeErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	} `json:"error"`
}

type StripeError struct {
	Status  int
	Type    string
	Code    string
	Param   string
	Message string
}

func (e *StripeError) Error() string {
	if e.Param != "" {
		return fmt.Sprintf("stripe %s (%s): %s", e.Code, e.Param, e.Message)
	}
	return fmt.Sprintf("stripe %s: %s", e.Code, e.Message)
}

func (c *StripeClient) do(
	ctx context.Context,
	method, path string,
	form url.Values,
	idempotencyKey string,
	result any,
) error {
	if strings.TrimSpace(c.secretKey) == "" {
		return ErrStripeNotConfigured
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.secretKey, "")
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.apiVersion != "" {
		req.Header.Set("Stripe-Version", c.apiVersion)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope stripeErrorEnvelope
		_ = json.Unmarshal(payload, &envelope)
		return &StripeError{
			Status:  resp.StatusCode,
			Type:    envelope.Error.Type,
			Code:    envelope.Error.Code,
			Param:   envelope.Error.Param,
			Message: envelope.Error.Message,
		}
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(payload, result)
}

func (c *StripeClient) CreateExpressAccount(
	ctx context.Context,
	channelID, email, country string,
	idempotencyKey string,
) (StripeAccount, error) {
	form := url.Values{}
	form.Set("type", "express")
	form.Set("country", country)
	form.Set("capabilities[card_payments][requested]", "true")
	form.Set("capabilities[transfers][requested]", "true")
	form.Set("metadata[channel_id]", channelID)
	if email != "" {
		form.Set("email", email)
	}
	var account StripeAccount
	if err := c.do(ctx, http.MethodPost, "/v1/accounts", form, idempotencyKey, &account); err != nil {
		return StripeAccount{}, err
	}
	return account, nil
}

func (c *StripeClient) GetAccount(ctx context.Context, accountID string) (StripeAccount, error) {
	var account StripeAccount
	if err := c.do(ctx, http.MethodGet, "/v1/accounts/"+url.PathEscape(accountID), nil, "", &account); err != nil {
		return StripeAccount{}, err
	}
	return account, nil
}

func (c *StripeClient) CreateAccountLink(
	ctx context.Context,
	accountID, refreshURL, returnURL string,
) (AccountLink, error) {
	form := url.Values{}
	form.Set("account", accountID)
	form.Set("refresh_url", refreshURL)
	form.Set("return_url", returnURL)
	form.Set("type", "account_onboarding")
	var response struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/account_links", form, "", &response); err != nil {
		return AccountLink{}, err
	}
	return AccountLink{
		URL:       response.URL,
		ExpiresAt: time.Unix(response.ExpiresAt, 0).UTC(),
	}, nil
}

type TransferInput struct {
	Amount         int64
	Currency       string
	Destination    string
	PayoutID       string
	ChannelID      string
	Period         string
	IdempotencyKey string
}

func (c *StripeClient) CreateTransfer(ctx context.Context, input TransferInput) (Transfer, error) {
	form := url.Values{}
	form.Set("amount", strconv.FormatInt(input.Amount, 10))
	form.Set("currency", input.Currency)
	form.Set("destination", input.Destination)
	form.Set("transfer_group", "settlement:"+input.Period)
	form.Set("metadata[payout_id]", input.PayoutID)
	form.Set("metadata[channel_id]", input.ChannelID)
	form.Set("metadata[period]", input.Period)
	var transfer Transfer
	if err := c.do(
		ctx,
		http.MethodPost,
		"/v1/transfers",
		form,
		input.IdempotencyKey,
		&transfer,
	); err != nil {
		return Transfer{}, err
	}
	return transfer, nil
}

func VerifyWebhook(payload []byte, signatureHeader, secret string, tolerance time.Duration) (Event, error) {
	var event Event
	if strings.TrimSpace(secret) == "" {
		return event, ErrStripeNotConfigured
	}
	var timestamp int64
	signatures := make([]string, 0, 2)
	for _, part := range strings.Split(signatureHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return event, ErrInvalidSignature
			}
			timestamp = parsed
		case "v1":
			if value != "" {
				signatures = append(signatures, value)
			}
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return event, ErrInvalidSignature
	}
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	now := time.Now().UTC()
	signedAt := time.Unix(timestamp, 0).UTC()
	if signedAt.Before(now.Add(-tolerance)) || signedAt.After(now.Add(tolerance)) {
		return event, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", timestamp, payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	valid := false
	for _, signature := range signatures {
		if hmac.Equal([]byte(expected), []byte(signature)) {
			valid = true
			break
		}
	}
	if !valid {
		return event, ErrInvalidSignature
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return Event{}, err
	}
	if event.ID == "" || event.Type == "" {
		return Event{}, ErrInvalidSignature
	}
	return event, nil
}
