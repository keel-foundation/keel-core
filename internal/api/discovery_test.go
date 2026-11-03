package api_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type discoveryVideo struct {
	store.Video
	Reason *discovery.Reason `json:"reason"`
	Score  float64           `json:"score"`
}

type discoveryPage struct {
	Items   []discoveryVideo `json:"items"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
	Total   int              `json:"total"`
	HasMore bool             `json:"has_more"`
}

type discoveryCollection struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Reason      *discovery.Reason `json:"reason"`
	Videos      discoveryPage     `json:"videos"`
}

type discoveryExplore struct {
	Featured    *discoveryVideo       `json:"featured"`
	New         discoveryPage         `json:"new"`
	Trending    discoveryPage         `json:"trending"`
	Collections []discoveryCollection `json:"collections"`
}

func newDiscoveryHandler(t *testing.T, token string) (http.Handler, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(
		context.Background(), st, "http://localhost:8081", "http://localhost:3000", "", "",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	identityService := identity.New(st, mailer.NewLog(io.Discard), testIdentityConfig())
	authenticator := auth.Fallback{
		Primary:        identityService,
		Secondary:      auth.NewDev("usr_demo", token),
		AllowSecondary: true,
	}
	engine := discovery.NewEngine(
		st,
		discovery.NewHashEmbedder(discovery.DefaultDimensions),
		discovery.DefaultConfig(),
	)
	handler := api.New(api.Options{
		Store:      st,
		Auth:       authenticator,
		Identity:   identityService,
		Discovery:  engine,
		Origins:    []string{"http://localhost:3000"},
		RateWindow: time.Minute,
	})
	return handler, st
}

func TestDiscoveryFeedAndExplorePersonalized(t *testing.T) {
	handler, st := newDiscoveryHandler(t, "")
	registered, cookies := registerAndVerify(t, handler, "discovery@example.com", testPassword(t))
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected a CSRF cookie")
	}
	csrf := csrfCookie.Value

	rec := doJSON(t, handler, http.MethodPost, "/api/v1/channels/ch_keel_lab/subscription", nil, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{"kind": "like"}},
		{http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/save", nil},
		{http.MethodPost, "/api/v1/videos/aB3dEf9hJkL/view", nil},
		{http.MethodPut, "/api/v1/videos/ksKCMmvOxwQ/reaction", map[string]string{"kind": "dislike"}},
	} {
		rec = doJSON(t, handler, request.method, request.path, request.body, cookies, csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: expected 200, got %d: %s", request.method, request.path, rec.Code, rec.Body.String())
		}
	}

	rec = getWithCookies(t, handler, "/api/v1/feed", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed: expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("unexpected personalized feed cache header: %q", got)
	}
	feed := decode[discoveryPage](t, rec)
	if feed.Total == 0 || len(feed.Items) == 0 {
		t.Fatalf("expected personalized feed items: %+v", feed)
	}
	reasons := 0
	for _, item := range feed.Items {
		if item.ID == "vid_demo_001" {
			t.Fatal("disliked video must be excluded")
		}
		if item.Reason != nil {
			reasons++
		}
	}
	if reasons == 0 {
		t.Fatal("expected recommendation reasons")
	}

	rec = getWithCookies(t, handler, "/api/v1/explore", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("explore: expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("unexpected personalized explore cache header: %q", got)
	}
	explore := decode[discoveryExplore](t, rec)
	if len(explore.Collections) == 0 {
		t.Fatalf("expected personalized collections: %+v", explore)
	}
	codes := make(map[string]bool)
	for _, collection := range explore.Collections {
		if collection.Reason != nil {
			codes[collection.Reason.Code] = true
		}
	}
	if !codes["recently_watched"] && !codes["because_you_watched"] && !codes["because_you_liked"] {
		t.Fatalf("expected a history-based reason: %+v", codes)
	}

	_, _, found, err := st.GetActorEmbedding(
		context.Background(), registered.User.ID, "hash-embedder",
	)
	if err != nil {
		t.Fatalf("get actor embedding: %v", err)
	}
	if !found {
		t.Fatal("expected the discovery engine to cache the actor embedding")
	}
	rec = doJSON(t, handler, http.MethodPut, "/api/v1/videos/aB3dEf9hJkL/reaction", map[string]string{
		"kind": "dislike",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("reaction invalidation: expected 200, got %d", rec.Code)
	}
	_, _, found, err = st.GetActorEmbedding(
		context.Background(), registered.User.ID, "hash-embedder",
	)
	if err != nil {
		t.Fatalf("get actor embedding after invalidation: %v", err)
	}
	if found {
		t.Fatal("expected the actor embedding to be invalidated")
	}
}

func TestDiscoveryColdStart(t *testing.T) {
	handler, _ := newDiscoveryHandler(t, "dev-token")

	rec := getWithCookies(t, handler, "/api/v1/feed", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cold feed: expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("unexpected cold feed cache header: %q", got)
	}
	feed := decode[discoveryPage](t, rec)
	if feed.Total != 5 || len(feed.Items) != 5 {
		t.Fatalf("unexpected cold feed: %+v", feed)
	}
	for _, item := range feed.Items {
		if item.Reason == nil {
			t.Fatalf("expected a cold-start reason: %+v", item)
		}
	}

	rec = getWithCookies(t, handler, "/api/v1/explore", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cold explore: expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("unexpected cold explore cache header: %q", got)
	}
	explore := decode[discoveryExplore](t, rec)
	if len(explore.Collections) != 3 {
		t.Fatalf("expected the editorial collections, got %+v", explore.Collections)
	}
	for _, collection := range explore.Collections {
		if collection.Reason != nil {
			t.Fatalf("editorial collections should not have personalized reasons: %+v", collection)
		}
	}
}
