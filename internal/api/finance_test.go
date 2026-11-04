package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

func studioRequest(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestStudioFinanceEndpoints(t *testing.T) {
	token, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate dev token: %v", err)
	}
	handler := newHandler(t, token)

	monetization := studioRequest(t, handler, "/api/v1/studio/monetization", token)
	if monetization.Code != http.StatusOK {
		t.Fatalf("monetization: expected 200, got %d: %s", monetization.Code, monetization.Body.String())
	}
	payload := decode[store.Monetization](t, monetization)
	if !payload.Eligible || payload.RPM != 4.82 || payload.Balance != 12840.5 {
		t.Fatalf("unexpected monetization: %+v", payload)
	}

	earnings := studioRequest(t, handler, "/api/v1/studio/earnings", token)
	if earnings.Code != http.StatusOK {
		t.Fatalf("earnings: expected 200, got %d: %s", earnings.Code, earnings.Body.String())
	}
	earningPage := decode[struct {
		Items   []store.Earning `json:"items"`
		Total   int             `json:"total"`
		HasMore bool            `json:"has_more"`
	}](t, earnings)
	if earningPage.Total != 6 || len(earningPage.Items) != 6 {
		t.Fatalf("unexpected earnings page: %+v", earningPage)
	}

	payouts := studioRequest(t, handler, "/api/v1/studio/payouts", token)
	if payouts.Code != http.StatusOK {
		t.Fatalf("payouts: expected 200, got %d: %s", payouts.Code, payouts.Body.String())
	}
	payoutPage := decode[struct {
		Items []store.Payout `json:"items"`
		Total int            `json:"total"`
	}](t, payouts)
	if payoutPage.Total != 3 || len(payoutPage.Items) != 3 {
		t.Fatalf("unexpected payouts page: %+v", payoutPage)
	}

	stats := studioRequest(t, handler, "/api/v1/studio/stats", token)
	if stats.Code != http.StatusOK {
		t.Fatalf("stats: expected 200, got %d: %s", stats.Code, stats.Body.String())
	}
	statsPayload := decode[store.StudioStats](t, stats)
	if statsPayload.Revenue != 12840.5 {
		t.Fatalf("expected studio revenue, got %+v", statsPayload)
	}
}

func TestStudioUploadsAndCommentsListings(t *testing.T) {
	token, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate dev token: %v", err)
	}
	handler, st := newHandlerWithStore(t, token)

	uploads := studioRequest(t, handler, "/api/v1/studio/uploads", token)
	if uploads.Code != http.StatusOK {
		t.Fatalf("uploads: expected 200, got %d: %s", uploads.Code, uploads.Body.String())
	}

	commentID, err := store.NewID("cmt_")
	if err != nil {
		t.Fatalf("new comment id: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateComment(context.Background(), store.Comment{
		ID:        commentID,
		VideoID:   "vid_demo_001",
		ActorID:   "usr_demo",
		Body:      "A studio export fixture.",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	comments := studioRequest(t, handler, "/api/v1/studio/comments", token)
	if comments.Code != http.StatusOK {
		t.Fatalf("comments: expected 200, got %d: %s", comments.Code, comments.Body.String())
	}
	commentPage := decode[struct {
		Items []store.Comment `json:"items"`
		Total int             `json:"total"`
	}](t, comments)
	if commentPage.Total != 1 || len(commentPage.Items) != 1 {
		t.Fatalf("unexpected comment page: %+v", commentPage)
	}
	if commentPage.Items[0].VideoID != "vid_demo_001" {
		t.Fatalf("unexpected studio comment: %+v", commentPage.Items[0])
	}
}
