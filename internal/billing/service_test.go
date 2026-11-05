package billing_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/billing"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type fakeStripe struct {
	mu           sync.Mutex
	secret       string
	accountID    string
	transfers    map[string]map[string]string
	transferCall int
	failNext     bool
	account      map[string]any
}

func newFakeStripe() *fakeStripe {
	return &fakeStripe{
		secret:    "sk_test_fake",
		accountID: "acct_test_123",
		transfers: map[string]map[string]string{},
		account: map[string]any{
			"id":                "acct_test_123",
			"details_submitted": true,
			"payouts_enabled":   true,
			"charges_enabled":   true,
			"requirements": map[string]any{
				"currently_due":   []string{},
				"disabled_reason": "",
			},
		},
	}
}

func (f *fakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || user != f.secret || pass != "" {
		http.Error(w, `{"error":{"message":"bad auth"}}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/accounts":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		account := make(map[string]any, len(f.account))
		for key, value := range f.account {
			account[key] = value
		}
		f.mu.Unlock()
		writeJSON(w, account)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/accounts/"):
		f.mu.Lock()
		account := make(map[string]any, len(f.account))
		for key, value := range f.account {
			account[key] = value
		}
		f.mu.Unlock()
		writeJSON(w, account)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/account_links":
		writeJSON(w, map[string]any{
			"url":        "https://connect.stripe.test/onboard",
			"expires_at": time.Now().Add(10 * time.Minute).Unix(),
		})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		f.mu.Lock()
		f.transferCall++
		if f.failNext {
			f.failNext = false
			f.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"balance_insufficient","message":"insufficient funds"}}`))
			return
		}
		if existing, ok := f.transfers[key]; ok {
			transfer := existing
			f.mu.Unlock()
			writeJSON(w, transfer)
			return
		}
		transfer := map[string]any{
			"id":          "tr_test_123",
			"amount":      mustInt(r.Form.Get("amount")),
			"currency":    r.Form.Get("currency"),
			"destination": r.Form.Get("destination"),
			"reversed":    false,
			"metadata": map[string]string{
				"payout_id":  r.Form.Get("metadata[payout_id]"),
				"channel_id": r.Form.Get("metadata[channel_id]"),
				"period":     r.Form.Get("metadata[period]"),
			},
		}
		f.transfers[key] = map[string]string{
			"id":          "tr_test_123",
			"amount":      r.Form.Get("amount"),
			"currency":    r.Form.Get("currency"),
			"destination": r.Form.Get("destination"),
			"payout_id":   r.Form.Get("metadata[payout_id]"),
		}
		f.mu.Unlock()
		writeJSON(w, transfer)
	default:
		http.NotFound(w, r)
	}
}

func mustInt(value string) int64 {
	var out int64
	_, _ = fmt.Sscan(value, &out)
	return out
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func newBillingService(t *testing.T, baseURL, webhookSecret string) (*billing.Service, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := billing.New(st, billing.StripeConfig{
		SecretKey:          "sk_test_fake",
		WebhookSecret:      webhookSecret,
		APIBaseURL:         baseURL,
		ConnectCountry:     "US",
		RefreshURL:         "http://localhost:3000/studio/monetization?stripe=refresh",
		ReturnURL:          "http://localhost:3000/studio/monetization?stripe=return",
		MinSettlementCents: 100,
		Currency:           "usd",
		WebhookTolerance:   5 * time.Minute,
	})
	return service, st
}

func signWebhook(payload []byte, secret string) string {
	timestamp := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", timestamp, payload)
	return fmt.Sprintf("t=%d,v1=%x", timestamp, mac.Sum(nil))
}

func TestStripeOnboardingAndSettlement(t *testing.T) {
	fake := newFakeStripe()
	server := httptest.NewServer(fake)
	defer server.Close()
	service, st := newBillingService(t, server.URL, "whsec_test")
	ctx := context.Background()

	account, link, err := service.EnsureAccount(ctx, "ch_demo", "usr_demo")
	if err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	if account.StripeAccountID != "acct_test_123" || account.Status != "active" {
		t.Fatalf("unexpected account: %+v", account)
	}
	if link.URL == "" || link.ExpiresAt.IsZero() {
		t.Fatalf("unexpected account link: %+v", link)
	}

	payout, err := service.Settle(ctx, "ch_demo", "2026-11")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if payout.Status != "paid" || payout.StripeTransferID != "tr_test_123" {
		t.Fatalf("unexpected payout: %+v", payout)
	}
	if payout.Amount != 12220.5 {
		t.Fatalf("unexpected payout amount: %v", payout.Amount)
	}

	monetization, err := st.GetMonetization(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("monetization: %v", err)
	}
	if monetization.Balance != 620 {
		t.Fatalf("unexpected balance: %v", monetization.Balance)
	}
	payouts, total, err := st.ListPayouts(ctx, "ch_demo", 10, 0)
	if err != nil {
		t.Fatalf("list payouts: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected 4 payouts, got %d", total)
	}
	foundStripe := false
	for _, item := range payouts {
		if item.Provider == "stripe" {
			foundStripe = true
		}
	}
	if !foundStripe {
		t.Fatalf("expected a stripe payout in %+v", payouts)
	}
	earnings, earningTotal, err := st.ListEarnings(ctx, "ch_demo", 20, 0)
	if err != nil {
		t.Fatalf("list earnings: %v", err)
	}
	if earningTotal != 7 || earnings[0].Type != "payout" {
		t.Fatalf("unexpected earnings: total=%d first=%+v", earningTotal, earnings[0])
	}

	again, err := service.Settle(ctx, "ch_demo", "2026-11")
	if err != nil {
		t.Fatalf("idempotent settle: %v", err)
	}
	if again.ID != payout.ID {
		t.Fatalf("expected same payout, got %s", again.ID)
	}
	if fake.transferCall != 1 {
		t.Fatalf("expected one transfer call, got %d", fake.transferCall)
	}
}

func TestStripeSettlementRejectsIncompleteOnboarding(t *testing.T) {
	fake := newFakeStripe()
	server := httptest.NewServer(fake)
	defer server.Close()
	service, _ := newBillingService(t, server.URL, "whsec_test")
	ctx := context.Background()

	if _, _, err := service.EnsureAccount(ctx, "ch_demo", "usr_demo"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	fake.mu.Lock()
	fake.account["payouts_enabled"] = false
	fake.account["details_submitted"] = false
	fake.mu.Unlock()
	if _, err := service.SyncAccount(ctx, "ch_demo"); err != nil {
		t.Fatalf("sync account: %v", err)
	}

	if _, err := service.Settle(ctx, "ch_demo", "2026-11"); err == nil {
		t.Fatal("expected settlement to be rejected")
	}
}

func TestStripeSettlementRetriesAfterFailure(t *testing.T) {
	fake := newFakeStripe()
	server := httptest.NewServer(fake)
	defer server.Close()
	service, st := newBillingService(t, server.URL, "whsec_test")
	ctx := context.Background()

	if _, _, err := service.EnsureAccount(ctx, "ch_demo", "usr_demo"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	fake.mu.Lock()
	fake.failNext = true
	fake.mu.Unlock()

	if _, err := service.Settle(ctx, "ch_demo", "2026-11"); err == nil {
		t.Fatal("expected first settlement to fail")
	}
	payouts, _, err := st.ListPayouts(ctx, "ch_demo", 10, 0)
	if err != nil {
		t.Fatalf("list payouts: %v", err)
	}
	var failed store.Payout
	for _, payout := range payouts {
		if payout.Provider == "stripe" {
			failed = payout
		}
	}
	if failed.Status != "failed" || failed.Attempt != 1 {
		t.Fatalf("unexpected failed payout: %+v", failed)
	}
	monetization, err := st.GetMonetization(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("monetization: %v", err)
	}
	if monetization.Balance != 12840.5 {
		t.Fatalf("balance was not restored: %v", monetization.Balance)
	}

	retried, err := service.Settle(ctx, "ch_demo", "2026-11")
	if err != nil {
		t.Fatalf("retry settlement: %v", err)
	}
	if retried.Status != "paid" || retried.Attempt != 2 {
		t.Fatalf("unexpected retried payout: %+v", retried)
	}
}

func TestStripeWebhookReversesSettlementAndUpdatesAccount(t *testing.T) {
	fake := newFakeStripe()
	server := httptest.NewServer(fake)
	defer server.Close()
	service, st := newBillingService(t, server.URL, "whsec_test")
	ctx := context.Background()

	if _, _, err := service.EnsureAccount(ctx, "ch_demo", "usr_demo"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	payout, err := service.Settle(ctx, "ch_demo", "2026-11")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	reversePayload, err := json.Marshal(map[string]any{
		"id":   "evt_reverse_1",
		"type": "transfer.reversed",
		"data": map[string]any{
			"object": map[string]any{
				"id":          payout.StripeTransferID,
				"amount":      1222050,
				"currency":    "usd",
				"destination": "acct_test_123",
				"reversed":    true,
				"metadata":    map[string]string{"payout_id": payout.ID},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal reverse event: %v", err)
	}
	if err := service.HandleWebhook(ctx, reversePayload, signWebhook(reversePayload, "whsec_test")); err != nil {
		t.Fatalf("reverse webhook: %v", err)
	}
	reversed, err := st.GetPayout(ctx, payout.ID)
	if err != nil {
		t.Fatalf("get payout: %v", err)
	}
	if reversed.Status != "reversed" {
		t.Fatalf("unexpected payout status: %s", reversed.Status)
	}
	monetization, err := st.GetMonetization(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("monetization: %v", err)
	}
	if monetization.Balance != 12840.5 {
		t.Fatalf("expected balance restored, got %v", monetization.Balance)
	}

	if err := service.HandleWebhook(ctx, reversePayload, signWebhook(reversePayload, "whsec_test")); err != nil {
		t.Fatalf("duplicate reverse webhook: %v", err)
	}
	monetization, err = st.GetMonetization(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("monetization: %v", err)
	}
	if monetization.Balance != 12840.5 {
		t.Fatalf("duplicate webhook altered balance: %v", monetization.Balance)
	}

	accountPayload, err := json.Marshal(map[string]any{
		"id":   "evt_account_1",
		"type": "account.updated",
		"data": map[string]any{
			"object": map[string]any{
				"id":                "acct_test_123",
				"details_submitted": true,
				"payouts_enabled":   false,
				"charges_enabled":   true,
				"requirements": map[string]any{
					"currently_due":   []string{"individual.verification.document"},
					"disabled_reason": "requirements.pending_verification",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal account event: %v", err)
	}
	if err := service.HandleWebhook(ctx, accountPayload, signWebhook(accountPayload, "whsec_test")); err != nil {
		t.Fatalf("account webhook: %v", err)
	}
	account, err := st.GetBillingAccount(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("billing account: %v", err)
	}
	if account.Status != "restricted" || account.PayoutsEnabled {
		t.Fatalf("unexpected account state: %+v", account)
	}
	if len(account.RequirementsDue) != 1 {
		t.Fatalf("unexpected requirements: %+v", account.RequirementsDue)
	}
}

func TestStripeWebhookRejectsInvalidSignature(t *testing.T) {
	fake := newFakeStripe()
	server := httptest.NewServer(fake)
	defer server.Close()
	service, _ := newBillingService(t, server.URL, "whsec_test")
	payload := []byte(`{"id":"evt_bad","type":"account.updated","data":{"object":{"id":"acct_test_123"}}}`)
	err := service.HandleWebhook(context.Background(), payload, "t=1,v1=bad")
	if err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestStripeDisabledWhenSecretMissing(t *testing.T) {
	conn := testutil.Open(t)
	service := billing.New(store.New(conn), billing.StripeConfig{})
	if service.Enabled() {
		t.Fatal("expected disabled service")
	}
}
