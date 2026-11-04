package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type remoteInstance struct {
	server   *httptest.Server
	actorURL string
	inboxURL string
	private  *rsa.PrivateKey
	mu       sync.Mutex
	received []map[string]any
}

func newRemoteInstance(t *testing.T) *remoteInstance {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	publicPEM, err := encodePublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	remote := &remoteInstance{private: privateKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/actor", func(w http.ResponseWriter, r *http.Request) {
		writeFederationTestJSON(w, map[string]any{
			"@context":          "https://www.w3.org/ns/activitystreams",
			"id":                remote.actorURL,
			"type":              "Group",
			"preferredUsername": "peer",
			"name":              "Peer Channel",
			"summary":           "A remote channel.",
			"inbox":             remote.inboxURL,
			"outbox":            remote.server.URL + "/outbox",
			"url":               remote.server.URL + "/c/peer",
			"publicKey": map[string]any{
				"id":           remote.actorURL + "#main-key",
				"owner":        remote.actorURL,
				"publicKeyPem": publicPEM,
			},
		})
	})
	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var activity map[string]any
		_ = json.Unmarshal(body, &activity)
		remote.mu.Lock()
		remote.received = append(remote.received, activity)
		remote.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	remote.server = httptest.NewServer(mux)
	remote.actorURL = remote.server.URL + "/actor"
	remote.inboxURL = remote.server.URL + "/inbox"
	t.Cleanup(remote.server.Close)
	return remote
}

func writeFederationTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/activity+json")
	_ = json.NewEncoder(w).Encode(value)
}

func (r *remoteInstance) lastActivity(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.received) == 0 {
		t.Fatal("expected a delivered activity")
	}
	return r.received[len(r.received)-1]
}

func federationTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service, err := New(context.Background(), st, Config{
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
		t.Fatalf("new federation service: %v", err)
	}
	return service, st
}

func signedInboxRequest(t *testing.T, remote *remoteInstance, activity map[string]any) *http.Request {
	t.Helper()
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost, "http://local.test/federation/actors/ch_demo/inbox", strings.NewReader(string(body)),
	)
	request.Header.Set("Content-Type", "application/activity+json")
	if err := SignRequest(request, body, remote.actorURL+"#main-key", remote.private, time.Now().UTC()); err != nil {
		t.Fatalf("sign request: %v", err)
	}
	return request
}

func TestFederationFollowAcceptAndRemoteVideoImport(t *testing.T) {
	service, st := federationTestService(t)
	remote := newRemoteInstance(t)
	ctx := context.Background()

	following, err := service.FollowRemote(ctx, remote.actorURL)
	if err != nil {
		t.Fatalf("follow remote: %v", err)
	}
	if following.Status != "pending" {
		t.Fatalf("expected pending follow, got %s", following.Status)
	}
	service.ProcessDeliveries(ctx, 10)
	if activity := remote.lastActivity(t); activity["type"] != "Follow" {
		t.Fatalf("expected Follow delivery, got %v", activity["type"])
	}

	accept := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       remote.actorURL + "/accept/1",
		"type":     "Accept",
		"actor":    remote.actorURL,
		"object": map[string]any{
			"id":     following.FollowActivityID,
			"type":   "Follow",
			"actor":  "http://local.test/federation/actors/instance",
			"object": remote.actorURL,
		},
	}
	status, err := service.HandleInbox(ctx, "instance", signedInboxRequest(t, remote, accept), mustJSON(t, accept))
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("accept inbox: status=%d err=%v", status, err)
	}
	stored, err := st.GetFederationFollowing(ctx, remote.actorURL)
	if err != nil || stored.Status != "accepted" {
		t.Fatalf("expected accepted follow, got %+v err=%v", stored, err)
	}

	videoURL := remote.server.URL + "/videos/remote-1"
	create := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       remote.actorURL + "/create/1",
		"type":     "Create",
		"actor":    remote.actorURL,
		"object": map[string]any{
			"id":           videoURL,
			"type":         "Video",
			"name":         "Remote video",
			"content":      "Imported over ActivityPub.",
			"attributedTo": remote.actorURL,
			"duration":     42,
			"license":      "standard",
			"published":    time.Now().UTC().Format(time.RFC3339),
			"url": []any{map[string]any{
				"type": "Link", "mediaType": "video/mp4",
				"href": remote.server.URL + "/media/video.mp4", "tag": []any{"mp4"},
			}},
			"keeltube:contentHash": "sha256:" + strings.Repeat("a", 64),
		},
	}
	status, err = service.HandleInbox(ctx, "ch_demo", signedInboxRequest(t, remote, create), mustJSON(t, create))
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("create inbox: status=%d err=%v", status, err)
	}
	video, err := st.GetVideo(ctx, remoteVideoID(videoURL))
	if err != nil {
		t.Fatalf("remote video was not imported: %v", err)
	}
	if video.Title != "Remote video" || video.Playback.MP4URL == "" || video.ChannelID == "" {
		t.Fatalf("unexpected remote video: %+v", video)
	}
	channel, err := st.GetChannel(ctx, video.ChannelID)
	if err != nil || channel.Name != "Peer Channel" {
		t.Fatalf("unexpected remote channel: %+v err=%v", channel, err)
	}

	remove := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       remote.actorURL + "/delete/1",
		"type":     "Delete",
		"actor":    remote.actorURL,
		"object":   map[string]any{"id": videoURL, "type": "Video"},
	}
	status, err = service.HandleInbox(ctx, "ch_demo", signedInboxRequest(t, remote, remove), mustJSON(t, remove))
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("delete inbox: status=%d err=%v", status, err)
	}
	video, err = st.GetVideo(ctx, remoteVideoID(videoURL))
	if err != nil || video.Status != "removed" {
		t.Fatalf("expected removed remote video, got %+v err=%v", video, err)
	}
}

func TestIncomingFollowCreatesFollowerAndAccepts(t *testing.T) {
	service, st := federationTestService(t)
	remote := newRemoteInstance(t)
	ctx := context.Background()
	follow := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       remote.actorURL + "/follow/1",
		"type":     "Follow",
		"actor":    remote.actorURL,
		"object":   "http://local.test/federation/actors/ch_demo",
	}
	status, err := service.HandleInbox(ctx, "ch_demo", signedInboxRequest(t, remote, follow), mustJSON(t, follow))
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("follow inbox: status=%d err=%v", status, err)
	}
	followers, err := st.ListFederationFollowers(ctx)
	if err != nil || len(followers) != 1 || followers[0].ActorURL != remote.actorURL {
		t.Fatalf("expected follower, got %+v err=%v", followers, err)
	}
	service.ProcessDeliveries(ctx, 10)
	if activity := remote.lastActivity(t); activity["type"] != "Accept" {
		t.Fatalf("expected Accept delivery, got %v", activity["type"])
	}
}

func TestFederationSignaturesRejectTamperingAndStaleDates(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	publicPEM, err := encodePublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	fetch := func(context.Context, string) (string, error) { return publicPEM, nil }
	body := []byte(`{"type":"Follow"}`)
	now := time.Now().UTC()
	request := httptest.NewRequest(http.MethodPost, "http://local.test/inbox", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/activity+json")
	if err := SignRequest(request, body, "http://remote.test/actor#main-key", privateKey, now); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifyRequest(context.Background(), request, body, now, fetch); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := VerifyRequest(context.Background(), request, []byte(`{"type":"Delete"}`), now, fetch); err == nil {
		t.Fatal("expected digest mismatch")
	}
	if _, err := VerifyRequest(context.Background(), request, body, now.Add(24*time.Hour), fetch); err == nil {
		t.Fatal("expected stale signature rejection")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}
