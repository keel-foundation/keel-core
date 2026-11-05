package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/billing"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newBillingStripeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "sk_test_fake" || pass != "" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/accounts":
			_, _ = io.WriteString(w, `{"id":"acct_api_test","details_submitted":true,"payouts_enabled":true,"charges_enabled":true,"requirements":{"currently_due":[],"disabled_reason":""}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/accounts/"):
			_, _ = io.WriteString(w, `{"id":"acct_api_test","details_submitted":true,"payouts_enabled":true,"charges_enabled":true,"requirements":{"currently_due":[],"disabled_reason":""}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/account_links":
			_ = r.ParseForm()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url":        "https://connect.stripe.test/api-onboard",
				"expires_at": time.Now().Add(time.Hour).Unix(),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers":
			_ = r.ParseForm()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "tr_api_test",
				"amount":      1222050,
				"currency":    "usd",
				"destination": "acct_api_test",
				"metadata": map[string]string{
					"payout_id":  r.Form.Get("metadata[payout_id]"),
					"channel_id": r.Form.Get("metadata[channel_id]"),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newBillingAPI(
	t *testing.T,
	baseURL, webhookSecret, token, internalToken string,
) (http.Handler, *store.Store, *billing.Service) {
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
	identityService := identity.New(st, mailer.NewLog(io.Discard), testIdentityConfig())
	authenticator := auth.Fallback{
		Primary:        identityService,
		Secondary:      auth.NewDev("usr_demo", token),
		AllowSecondary: true,
	}
	handler := api.New(api.Options{
		Store:         st,
		Auth:          authenticator,
		Identity:      identityService,
		Billing:       service,
		InternalToken: internalToken,
		Origins:       []string{"http://localhost:3000"},
		RateWindow:    time.Minute,
	})
	return handler, st, service
}

func apiBillingRequest(
	t *testing.T,
	handler http.Handler,
	method, path, token, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestBillingRequiresConfigurationAndSession(t *testing.T) {
	token, _ := auth.NewToken()
	handler := newHandler(t, token)
	rec := apiBillingRequest(t, handler, http.MethodGet, "/api/v1/studio/billing/account", token, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without billing config, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBillingAccountAndSettlementAPI(t *testing.T) {
	server := newBillingStripeServer(t)
	defer server.Close()
	token, _ := auth.NewToken()
	handler, st, _ := newBillingAPI(t, server.URL, "whsec_api", token, "internal-token")

	account := apiBillingRequest(t, handler, http.MethodGet, "/api/v1/studio/billing/account", token, "")
	if account.Code != http.StatusOK {
		t.Fatalf("get account: %d %s", account.Code, account.Body.String())
	}
	if decode[store.BillingAccount](t, account).Status != "not_connected" {
		t.Fatalf("unexpected initial account: %s", account.Body.String())
	}

	created := apiBillingRequest(t, handler, http.MethodPost, "/api/v1/studio/billing/account", token, "")
	if created.Code != http.StatusOK {
		t.Fatalf("create account: %d %s", created.Code, created.Body.String())
	}
	var link struct {
		Account       store.BillingAccount `json:"account"`
		OnboardingURL string               `json:"onboarding_url"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if link.OnboardingURL == "" || link.Account.StripeAccountID != "acct_api_test" {
		t.Fatalf("unexpected link response: %+v", link)
	}

	synced := apiBillingRequest(t, handler, http.MethodPost, "/api/v1/studio/billing/account/sync", token, "")
	if synced.Code != http.StatusOK || !decode[store.BillingAccount](t, synced).PayoutsEnabled {
		t.Fatalf("sync account: %d %s", synced.Code, synced.Body.String())
	}

	settle := apiBillingRequest(t, handler, http.MethodPost, "/internal/v1/billing/settlements", "internal-token", `{"channel_id":"ch_demo","period":"2026-11"}`)
	if settle.Code != http.StatusOK {
		t.Fatalf("settle: %d %s", settle.Code, settle.Body.String())
	}
	payout := decode[store.Payout](t, settle)
	if payout.Status != "paid" || payout.StripeTransferID != "tr_api_test" {
		t.Fatalf("unexpected payout: %+v", payout)
	}
	stored, err := st.GetPayout(context.Background(), payout.ID)
	if err != nil || stored.Provider != "stripe" {
		t.Fatalf("stored payout: %+v err=%v", stored, err)
	}

	unauthorized := apiBillingRequest(t, handler, http.MethodPost, "/internal/v1/billing/settlements", "", `{"channel_id":"ch_demo","period":"2026-11"}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorized.Code)
	}
}

func TestStripeWebhookAPIRoute(t *testing.T) {
	server := newBillingStripeServer(t)
	defer server.Close()
	token, _ := auth.NewToken()
	handler, _, _ := newBillingAPI(t, server.URL, "whsec_api", token, "")

	invalid := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(`{"id":"evt_bad","type":"account.updated","data":{"object":{"id":"acct_api_test"}}}`))
	invalid.Header.Set("Stripe-Signature", "t=1,v1=bad")
	invalidRec := httptest.NewRecorder()
	handler.ServeHTTP(invalidRec, invalid)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", invalidRec.Code)
	}

	payload := []byte(`{"id":"evt_account_api","type":"account.updated","data":{"object":{"id":"acct_api_test","details_submitted":true,"payouts_enabled":true,"charges_enabled":true,"requirements":{"currently_due":[],"disabled_reason":""}}}}`)
	timestamp := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte("whsec_api"))
	_, _ = fmt.Fprintf(mac, "%d.%s", timestamp, payload)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%x", timestamp, mac.Sum(nil)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
