package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type financePayoutEvent struct {
	ChannelID string  `json:"channel_id"`
	PayoutID  string  `json:"payout_id"`
	Period    string  `json:"period"`
	Amount    float64 `json:"amount"`
	Status    string  `json:"status"`
}

func (s *Server) ingestFinancePayout(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "A valid internal service token is required.",
		})
		return
	}
	if s.notifications == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Notifications are not configured.",
		})
		return
	}
	var body financePayoutEvent
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeNotificationError(w, err)
		return
	}
	channel, err := s.store.GetChannel(r.Context(), strings.TrimSpace(body.ChannelID))
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	if strings.TrimSpace(body.PayoutID) == "" || strings.TrimSpace(body.Status) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": "payout_id and status are required",
		})
		return
	}
	payload, err := json.Marshal(map[string]any{
		"payout_id": body.PayoutID,
		"period":    body.Period,
		"amount":    body.Amount,
		"status":    body.Status,
	})
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	title := "Payout " + body.Status
	if body.Period != "" {
		title = "Payout " + body.Status + " for " + body.Period
	}
	_, _, err = s.notifications.Create(r.Context(), store.NotificationInput{
		ActorID:   channel.ActorID,
		ChannelID: channel.ID,
		Kind:      "finance.payout",
		Title:     title,
		Body:      fmt.Sprintf("Your %s payout is %s.", body.Period, body.Status),
		Href:      "/studio/monetization",
		Payload:   payload,
		DedupeKey: "finance:payout:" + body.PayoutID + ":" + body.Status,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}
