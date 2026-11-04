package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/federation"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func newFederationHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service, err := federation.New(context.Background(), st, federation.Config{
		Enabled:       true,
		Domain:        "local.test",
		BaseURL:       "http://local.test",
		ActorID:       "instance",
		PolicyVersion: "test-0.1",
		Timeout:       5 * time.Second,
		Lease:         time.Minute,
		AllowPrivate:  true,
	})
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	handler := api.New(api.Options{
		Store:         st,
		Auth:          auth.NewDev("usr_demo", ""),
		Federation:    service,
		InternalToken: "internal-token",
		Origins:       []string{"http://localhost:3000"},
		RateWindow:    time.Minute,
	})
	return handler, st
}

func TestFederationDiscoveryDocuments(t *testing.T) {
	handler, _ := newFederationHandler(t)

	rec := request(t, handler, "/.well-known/webfinger?resource=acct:maravale@local.test", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/jrd+json; charset=utf-8" {
		t.Fatalf("webfinger: %d %s", rec.Code, rec.Body.String())
	}
	webfinger := decode[map[string]any](t, rec)
	if webfinger["subject"] != "acct:maravale@local.test" {
		t.Fatalf("unexpected webfinger: %+v", webfinger)
	}

	rec = request(t, handler, "/federation/actors/ch_demo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("actor: %d %s", rec.Code, rec.Body.String())
	}
	actor := decode[map[string]any](t, rec)
	if actor["type"] != "Group" || actor["inbox"] != "http://local.test/federation/actors/ch_demo/inbox" {
		t.Fatalf("unexpected actor: %+v", actor)
	}

	rec = request(t, handler, "/federation/actors/ch_demo/outbox", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("outbox: %d %s", rec.Code, rec.Body.String())
	}
	outbox := decode[map[string]any](t, rec)
	if outbox["type"] != "OrderedCollection" || len(outbox["orderedItems"].([]any)) == 0 {
		t.Fatalf("unexpected outbox: %+v", outbox)
	}

	rec = request(t, handler, "/federation/videos/ksKCMmvOxwQ", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("video: %d %s", rec.Code, rec.Body.String())
	}
	video := decode[map[string]any](t, rec)
	if video["type"] != "Video" || video["attributedTo"] != "http://local.test/federation/actors/ch_demo" {
		t.Fatalf("unexpected video: %+v", video)
	}

	rec = request(t, handler, "/federation/policy", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body.String())
	}
	policy := decode[map[string]any](t, rec)
	if policy["policy_version"] != "test-0.1" {
		t.Fatalf("unexpected policy: %+v", policy)
	}
}

func TestFederationInboxRequiresSignatureAndInternalSnapshotNeedsToken(t *testing.T) {
	handler, _ := newFederationHandler(t)
	rec := doJSON(t, handler, http.MethodPost, "/federation/actors/ch_demo/inbox", map[string]any{
		"id": "http://remote.test/follow/1", "type": "Follow", "actor": "http://remote.test/actor",
		"object": "http://local.test/federation/actors/ch_demo",
	}, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unsigned inbox rejection, got %d %s", rec.Code, rec.Body.String())
	}

	rec = request(t, handler, "/internal/v1/federation", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected internal auth failure, got %d", rec.Code)
	}
	req, _ := http.NewRequest(http.MethodGet, "/internal/v1/federation", nil)
	req.Header.Set("Authorization", "Bearer internal-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected federation snapshot, got %d %s", recorder.Code, recorder.Body.String())
	}
	snapshot := decode[map[string]any](t, recorder)
	if snapshot["domain"] != "local.test" || snapshot["actor_id"] != "instance" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}
