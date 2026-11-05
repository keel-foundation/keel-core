package api

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/billing"
	"github.com/keel-foundation/keel-core/internal/store"
)

type billingAccountLinkResponse struct {
	Account       store.BillingAccount `json:"account"`
	OnboardingURL string               `json:"onboarding_url"`
	ExpiresAt     time.Time            `json:"expires_at"`
}

type settlementRequest struct {
	ChannelID string `json:"channel_id"`
	Period    string `json:"period"`
}

func (s *Server) requireBilling(w http.ResponseWriter) (*billing.Service, bool) {
	if s.billing == nil || !s.billing.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Stripe billing is not configured.",
		})
		return nil, false
	}
	return s.billing, true
}

func (s *Server) billingAccount(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireBilling(w)
	if !ok {
		return
	}
	account, err := service.Account(r.Context(), channel.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, store.BillingAccount{
				ChannelID:       channel.ID,
				Provider:        "stripe",
				Status:          "not_connected",
				RequirementsDue: []string{},
			})
			return
		}
		s.writeBillingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) createBillingAccount(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireBilling(w)
	if !ok {
		return
	}
	account, link, err := service.EnsureAccount(r.Context(), channel.ID, principal.ID)
	if err != nil {
		s.writeBillingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, billingAccountLinkResponse{
		Account:       account,
		OnboardingURL: link.URL,
		ExpiresAt:     link.ExpiresAt,
	})
}

func (s *Server) refreshBillingAccountLink(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireBilling(w)
	if !ok {
		return
	}
	account, link, err := service.RefreshLink(r.Context(), channel.ID)
	if err != nil {
		s.writeBillingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, billingAccountLinkResponse{
		Account:       account,
		OnboardingURL: link.URL,
		ExpiresAt:     link.ExpiresAt,
	})
}

func (s *Server) syncBillingAccount(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	service, ok := s.requireBilling(w)
	if !ok {
		return
	}
	account, err := service.SyncAccount(r.Context(), channel.ID)
	if err != nil {
		s.writeBillingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) settleCreatorPayout(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "A valid internal service token is required.",
		})
		return
	}
	service, ok := s.requireBilling(w)
	if !ok {
		return
	}
	var request settlementRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	channelID := strings.TrimSpace(request.ChannelID)
	if channelID == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"detail": "channel_id is required.",
		})
		return
	}
	payout, err := service.Settle(r.Context(), channelID, request.Period)
	if err != nil {
		s.writeBillingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payout)
}

func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.billing == nil || !s.billing.WebhooksEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Stripe webhooks are not configured.",
		})
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"detail": "Invalid webhook body.",
		})
		return
	}
	if err := s.billing.HandleWebhook(r.Context(), payload, r.Header.Get("Stripe-Signature")); err != nil {
		switch {
		case errors.Is(err, billing.ErrInvalidSignature):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"detail": "Invalid Stripe signature.",
			})
		case errors.Is(err, billing.ErrDisabled), errors.Is(err, billing.ErrStripeNotConfigured):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"detail": "Stripe webhooks are not configured.",
			})
		default:
			log.Printf("stripe webhook failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"detail": "Webhook processing failed.",
			})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

func (s *Server) writeBillingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrDisabled), errors.Is(err, billing.ErrStripeNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Stripe billing is not configured.",
		})
	case errors.Is(err, billing.ErrInvalidSignature):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"detail": "Invalid Stripe signature.",
		})
	case errors.Is(err, billing.ErrInvalidPeriod):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"detail": "period must use YYYY-MM.",
		})
	case errors.Is(err, billing.ErrAccountMissing):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "Connect a Stripe account before settling.",
		})
	case errors.Is(err, billing.ErrAccountNotReady):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "Stripe onboarding is not complete.",
		})
	case errors.Is(err, billing.ErrBelowMinimum):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "Settlement balance is below the minimum.",
		})
	case errors.Is(err, billing.ErrSettlementFailed):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "This settlement has already failed.",
		})
	case errors.Is(err, store.ErrNotEligible):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "Creator monetization is not eligible.",
		})
	case errors.Is(err, store.ErrInsufficientBalance):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "Settlement balance is insufficient.",
		})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	default:
		var stripeErr *billing.StripeError
		if errors.As(err, &stripeErr) {
			log.Printf("stripe request failed: %v", stripeErr)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"detail": "Stripe request failed.",
			})
			return
		}
		log.Printf("billing failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
	}
}
