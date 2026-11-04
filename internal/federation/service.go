package federation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

var (
	ErrDisabled          = errors.New("federation is not enabled")
	ErrActorNotFound     = errors.New("federation actor not found")
	ErrBlockedPeer       = errors.New("federation peer is blocked")
	ErrInvalidActivity   = errors.New("invalid federation activity")
	ErrRemoteUnavailable = errors.New("the remote federation actor is unavailable")
)

const (
	publicAudience = "https://www.w3.org/ns/activitystreams#Public"
	workerID       = "federation-delivery"
)

type Config struct {
	Enabled       bool
	Domain        string
	BaseURL       string
	ActorID       string
	PolicyVersion string
	Timeout       time.Duration
	Lease         time.Duration
	AllowPrivate  bool
}

type Service struct {
	store      *store.Store
	cfg        Config
	instance   store.FederationInstance
	privateKey *rsa.PrivateKey
	client     *http.Client
	now        func() time.Time
}

func ConfigFromEnv(getenv func(string) string) Config {
	domain := strings.TrimSpace(getenv("KEELTUBE_FEDERATION_DOMAIN"))
	if domain == "" {
		return Config{}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(getenv("KEELTUBE_FEDERATION_BASE_URL")), "/")
	if baseURL == "" {
		if strings.Contains(domain, "localhost") || strings.HasPrefix(domain, "127.") {
			baseURL = "http://" + domain
		} else {
			baseURL = "https://" + domain
		}
	}
	timeout := 10 * time.Second
	if raw := strings.TrimSpace(getenv("KEELTUBE_FEDERATION_TIMEOUT_SECONDS")); raw != "" {
		if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
			timeout = seconds
		}
	}
	lease := 5 * time.Minute
	if raw := strings.TrimSpace(getenv("KEELTUBE_FEDERATION_DELIVERY_LEASE_SECONDS")); raw != "" {
		if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
			lease = seconds
		}
	}
	return Config{
		Enabled:       true,
		Domain:        domain,
		BaseURL:       baseURL,
		ActorID:       envOr(getenv, "KEELTUBE_FEDERATION_ACTOR_ID", "instance"),
		PolicyVersion: envOr(getenv, "KEELTUBE_FEDERATION_POLICY_VERSION", "demo-0.1"),
		Timeout:       timeout,
		Lease:         lease,
		AllowPrivate:  strings.EqualFold(strings.TrimSpace(getenv("KEELTUBE_FEDERATION_ALLOW_PRIVATE")), "true"),
	}
}

func New(ctx context.Context, st *store.Store, cfg Config) (*Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Domain == "" || cfg.BaseURL == "" {
		return nil, fmt.Errorf("federation requires a domain and base URL")
	}
	now := time.Now().UTC()
	instance, err := st.GetFederationInstance(ctx)
	if errors.Is(err, store.ErrNotFound) {
		privateKey, keyErr := rsa.GenerateKey(rand.Reader, 2048)
		if keyErr != nil {
			return nil, fmt.Errorf("generate federation key: %w", keyErr)
		}
		privatePEM, keyErr := encodePrivateKey(privateKey)
		if keyErr != nil {
			return nil, keyErr
		}
		publicPEM, keyErr := encodePublicKey(&privateKey.PublicKey)
		if keyErr != nil {
			return nil, keyErr
		}
		instance = store.FederationInstance{
			ActorID:       cfg.ActorID,
			Domain:        cfg.Domain,
			BaseURL:       cfg.BaseURL,
			PublicKeyPEM:  publicPEM,
			PrivateKeyPEM: privatePEM,
			PolicyVersion: cfg.PolicyVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := st.SaveFederationInstance(ctx, instance); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if instance.Domain != cfg.Domain || instance.BaseURL != cfg.BaseURL {
		instance.Domain = cfg.Domain
		instance.BaseURL = cfg.BaseURL
		instance.ActorID = cfg.ActorID
		instance.PolicyVersion = cfg.PolicyVersion
		instance.UpdatedAt = now
		if err := st.SaveFederationInstance(ctx, instance); err != nil {
			return nil, err
		}
	}
	privateKey, err := parseRSAPrivateKey(instance.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &Service{
		store:      st,
		cfg:        cfg,
		instance:   instance,
		privateKey: privateKey,
		client:     &http.Client{Timeout: cfg.Timeout},
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) Enabled() bool { return s != nil }

func (s *Service) BaseURL() string { return s.instance.BaseURL }

func (s *Service) instanceActorURL() string {
	return s.actorURL(s.instance.ActorID)
}

func (s *Service) actorURL(actorID string) string {
	return s.instance.BaseURL + "/federation/actors/" + url.PathEscape(actorID)
}

func (s *Service) videoURL(video store.Video) string {
	id := video.PublicID
	if id == "" {
		id = video.ID
	}
	return s.instance.BaseURL + "/federation/videos/" + url.PathEscape(id)
}

func (s *Service) activityURL() string {
	token := make([]byte, 12)
	_, _ = rand.Read(token)
	return s.instance.BaseURL + "/federation/activities/" + hex.EncodeToString(token)
}

func (s *Service) WebFinger(ctx context.Context, resource string) (map[string]any, error) {
	resource = strings.TrimSpace(resource)
	resource = strings.TrimPrefix(resource, "acct:")
	local, domain, ok := strings.Cut(resource, "@")
	if !ok || local == "" || !strings.EqualFold(domain, s.instance.Domain) {
		return nil, ErrActorNotFound
	}
	local = strings.TrimPrefix(local, "@")
	var actorID string
	if local == s.instance.ActorID || local == "instance" {
		actorID = s.instance.ActorID
	} else {
		channel, err := s.store.GetChannel(ctx, local)
		if err != nil {
			return nil, ErrActorNotFound
		}
		actorID = channel.ID
	}
	actor := s.actorURL(actorID)
	subject := "acct:" + local + "@" + s.instance.Domain
	return map[string]any{
		"subject": subject,
		"aliases": []string{actor},
		"links": []map[string]string{
			{"rel": "self", "type": "application/activity+json", "href": actor},
			{"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": s.profileURL(actorID)},
		},
	}, nil
}

func (s *Service) profileURL(actorID string) string {
	if channel, err := s.store.GetChannel(context.Background(), actorID); err == nil {
		return s.instance.BaseURL + "/channels/" + url.PathEscape(channel.ID)
	}
	return s.instance.BaseURL + "/"
}

func (s *Service) ActorDocument(ctx context.Context, id string) (map[string]any, error) {
	if id == s.instance.ActorID || id == "instance" {
		return s.instanceActorDocument(), nil
	}
	channel, err := s.store.GetChannel(ctx, id)
	if err != nil {
		return nil, ErrActorNotFound
	}
	actor, err := s.store.GetActor(ctx, channel.ActorID)
	if err != nil {
		return nil, ErrActorNotFound
	}
	actorURL := s.actorURL(channel.ID)
	username := strings.TrimPrefix(channel.Handle, "@")
	document := map[string]any{
		"@context": []any{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
			map[string]string{"keeltube": "https://keeltube.com/ns#"},
		},
		"id":                actorURL,
		"type":              "Group",
		"preferredUsername": username,
		"name":              channel.Name,
		"summary":           channel.Description,
		"url":               s.profileURL(channel.ID),
		"inbox":             actorURL + "/inbox",
		"outbox":            actorURL + "/outbox",
		"followers":         actorURL + "/followers",
		"following":         actorURL + "/following",
		"published":         channel.CreatedAt,
		"publicKey": map[string]any{
			"id":           actorURL + "#main-key",
			"owner":        actorURL,
			"publicKeyPem": s.instance.PublicKeyPEM,
		},
		"keeltube:policyVersion": s.instance.PolicyVersion,
	}
	if channel.AvatarURL != "" {
		document["icon"] = map[string]any{"type": "Image", "url": channel.AvatarURL}
	}
	if channel.BannerURL != "" {
		document["image"] = map[string]any{"type": "Image", "url": channel.BannerURL}
	}
	if len(channel.Links) > 0 {
		attachments := make([]map[string]any, 0, len(channel.Links))
		for _, link := range channel.Links {
			attachments = append(attachments, map[string]any{
				"type": "PropertyValue", "name": link.Label, "value": link.URL,
			})
		}
		document["attachment"] = attachments
	}
	if actor.PublicKey != "" && actor.PublicKey != s.instance.PublicKeyPEM {
		document["keeltube:actorKey"] = actor.PublicKey
	}
	return document, nil
}

func (s *Service) instanceActorDocument() map[string]any {
	actorURL := s.instanceActorURL()
	return map[string]any{
		"@context": []any{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
			map[string]string{"keeltube": "https://keeltube.com/ns#"},
		},
		"id":                actorURL,
		"type":              "Application",
		"preferredUsername": s.instance.ActorID,
		"name":              "KeelTube " + s.instance.Domain,
		"summary":           "This instance publishes signed KeelTube federation activities.",
		"url":               s.instance.BaseURL + "/",
		"inbox":             actorURL + "/inbox",
		"outbox":            actorURL + "/outbox",
		"followers":         actorURL + "/followers",
		"following":         actorURL + "/following",
		"publicKey": map[string]any{
			"id":           actorURL + "#main-key",
			"owner":        actorURL,
			"publicKeyPem": s.instance.PublicKeyPEM,
		},
		"keeltube:policyVersion": s.instance.PolicyVersion,
	}
}

func (s *Service) Outbox(ctx context.Context, id string) (map[string]any, error) {
	if id == s.instance.ActorID || id == "instance" {
		return map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       s.instanceActorURL() + "/outbox", "type": "OrderedCollection",
			"totalItems": 0, "orderedItems": []any{},
		}, nil
	}
	channel, err := s.store.GetChannel(ctx, id)
	if err != nil {
		return nil, ErrActorNotFound
	}
	videos, _, err := s.store.ListVideos(ctx, store.VideoFilter{
		ChannelID: channel.ID, PublicOnly: true, Limit: 50, Sort: "latest",
	})
	if err != nil {
		return nil, err
	}
	actorURL := s.actorURL(channel.ID)
	items := make([]map[string]any, 0, len(videos))
	for _, video := range videos {
		item, err := s.videoActivity(video, channel, "Create")
		if err == nil {
			items = append(items, item)
		}
	}
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           actorURL + "/outbox",
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}, nil
}

func (s *Service) FollowersCollection(ctx context.Context, id string) (map[string]any, error) {
	if id == s.instance.ActorID || id == "instance" {
		return s.collection(ctx, id, "followers")
	}
	channel, err := s.store.GetChannel(ctx, id)
	if err != nil {
		return nil, ErrActorNotFound
	}
	followers, err := s.store.ListFederationFollowers(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0)
	for _, follower := range followers {
		if follower.LocalActorID == channel.ID || follower.LocalActorID == s.instance.ActorID {
			items = append(items, follower.ActorURL)
		}
	}
	sort.Strings(items)
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           s.actorURL(channel.ID) + "/followers",
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}, nil
}

func (s *Service) FollowingCollection(ctx context.Context, id string) (map[string]any, error) {
	if id == s.instance.ActorID || id == "instance" {
		return s.collection(ctx, id, "following")
	}
	channel, err := s.store.GetChannel(ctx, id)
	if err != nil {
		return nil, ErrActorNotFound
	}
	following, err := s.store.ListFederationFollowing(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0)
	for _, item := range following {
		if item.LocalActorID == channel.ID || item.LocalActorID == s.instance.ActorID {
			items = append(items, item.ActorURL)
		}
	}
	sort.Strings(items)
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           s.actorURL(channel.ID) + "/following",
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}, nil
}

func (s *Service) collection(ctx context.Context, actorID, kind string) (map[string]any, error) {
	items := []string{}
	if kind == "followers" {
		followers, err := s.store.ListFederationFollowers(ctx)
		if err != nil {
			return nil, err
		}
		for _, follower := range followers {
			if follower.LocalActorID == actorID {
				items = append(items, follower.ActorURL)
			}
		}
	} else {
		following, err := s.store.ListFederationFollowing(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range following {
			if item.LocalActorID == actorID {
				items = append(items, item.ActorURL)
			}
		}
	}
	sort.Strings(items)
	return map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       s.actorURL(actorID) + "/" + kind, "type": "OrderedCollection",
		"totalItems": len(items), "orderedItems": items,
	}, nil
}

func (s *Service) VideoObject(ctx context.Context, id string) (map[string]any, error) {
	video, err := s.store.GetVideo(ctx, id)
	if err != nil || video.Visibility != "public" || (video.Status != "published" && video.Status != "demonetized") {
		return nil, ErrActorNotFound
	}
	channel, err := s.store.GetChannel(ctx, video.ChannelID)
	if err != nil {
		return nil, ErrActorNotFound
	}
	return s.videoObject(video, channel), nil
}

func (s *Service) PolicyDocument() map[string]any {
	return map[string]any{
		"@context": []any{
			"https://www.w3.org/ns/activitystreams",
			map[string]string{"keeltube": "https://keeltube.com/ns#"},
		},
		"id":                    s.instance.BaseURL + "/federation/policy",
		"type":                  "keeltube:InstancePolicy",
		"instance":              s.instance.Domain,
		"policy_version":        s.instance.PolicyVersion,
		"labels_are_signed":     true,
		"silent_downranking":    false,
		"appeal_required":       true,
		"moderation_records":    "not_federated_without_consent",
		"creator_portability":   true,
		"published_at":          s.instance.UpdatedAt,
		"public_key":            s.instance.PublicKeyPEM,
		"keeltube:policyDigest": "sha256:" + digestHex([]byte(s.instance.PolicyVersion+"\n"+s.instance.Domain)),
	}
}

func digestHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func (s *Service) videoObject(video store.Video, channel store.Channel) map[string]any {
	actorURL := s.actorURL(channel.ID)
	object := map[string]any{
		"id":                     s.videoURL(video),
		"type":                   "Video",
		"name":                   video.Title,
		"content":                video.Description,
		"attributedTo":           actorURL,
		"published":              video.CreatedAt,
		"updated":                video.UpdatedAt,
		"duration":               video.DurationSeconds,
		"license":                video.License,
		"category":               video.Category,
		"tag":                    video.Tags,
		"to":                     []string{publicAudience},
		"cc":                     []string{actorURL + "/followers"},
		"keeltube:contentHash":   video.ContentHash,
		"keeltube:policyVersion": s.instance.PolicyVersion,
		"keeltube:format":        video.Format,
	}
	if video.PublishedAt != nil {
		object["published"] = *video.PublishedAt
	}
	urls := make([]map[string]any, 0, 3)
	if video.Playback.MP4URL != "" {
		urls = append(urls, map[string]any{
			"type": "Link", "mediaType": "video/mp4", "href": video.Playback.MP4URL,
			"tag": []string{"mp4"},
		})
	}
	if video.Playback.HLSURL != "" {
		urls = append(urls, map[string]any{
			"type": "Link", "mediaType": "application/x-mpegURL", "href": video.Playback.HLSURL,
			"tag": []string{"hls"},
		})
	}
	if len(urls) > 0 {
		object["url"] = urls
	}
	if video.Playback.ThumbnailURL != "" {
		object["icon"] = map[string]any{"type": "Image", "url": video.Playback.ThumbnailURL}
	}
	if video.Playback.CaptionsURL != "" {
		object["subtitleLanguage"] = []map[string]any{{
			"type": "Link", "mediaType": "text/vtt", "href": video.Playback.CaptionsURL, "tag": []string{"captions"},
		}}
	}
	if video.Policy != nil && video.Policy.PolicyVersion != "" {
		object["keeltube:policyVersion"] = video.Policy.PolicyVersion
	}
	return object
}

func (s *Service) videoActivity(video store.Video, channel store.Channel, kind string) (map[string]any, error) {
	actorURL := s.actorURL(channel.ID)
	object := s.videoObject(video, channel)
	return map[string]any{
		"@context": []any{
			"https://www.w3.org/ns/activitystreams",
			map[string]string{"keeltube": "https://keeltube.com/ns#"},
		},
		"id":        s.activityURL(),
		"type":      kind,
		"actor":     actorURL,
		"published": s.now(),
		"to":        []string{publicAudience},
		"cc":        []string{actorURL + "/followers"},
		"object":    object,
	}, nil
}

func (s *Service) PublishVideo(ctx context.Context, video store.Video) {
	s.deliverVideo(ctx, video, "Create")
}

func (s *Service) UpdateVideo(ctx context.Context, video store.Video) {
	s.deliverVideo(ctx, video, "Update")
}

func (s *Service) DeleteVideo(ctx context.Context, video store.Video) {
	if !s.Enabled() || strings.HasPrefix(video.ID, "vid_remote_") {
		return
	}
	channel, err := s.store.GetChannel(ctx, video.ChannelID)
	if err != nil {
		return
	}
	actorURL := s.actorURL(channel.ID)
	activity := map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        s.activityURL(),
		"type":      "Delete",
		"actor":     actorURL,
		"published": s.now(),
		"to":        []string{publicAudience},
		"cc":        []string{actorURL + "/followers"},
		"object": map[string]any{
			"id":   s.videoURL(video),
			"type": "Tombstone",
		},
	}
	s.deliverActivity(ctx, activity, s.followerInboxes(ctx, channel.ID))
}

func (s *Service) deliverVideo(ctx context.Context, video store.Video, kind string) {
	if !s.Enabled() || strings.HasPrefix(video.ID, "vid_remote_") || video.Visibility != "public" {
		return
	}
	if video.Status != "published" && video.Status != "demonetized" {
		return
	}
	channel, err := s.store.GetChannel(ctx, video.ChannelID)
	if err != nil {
		return
	}
	activity, err := s.videoActivity(video, channel, kind)
	if err != nil {
		return
	}
	s.deliverActivity(ctx, activity, s.followerInboxes(ctx, channel.ID))
}

func (s *Service) followerInboxes(ctx context.Context, localActorID string) []string {
	followers, err := s.store.ListFederationFollowers(ctx)
	if err != nil {
		return nil
	}
	inboxes := make([]string, 0, len(followers))
	seen := map[string]bool{}
	for _, follower := range followers {
		if follower.LocalActorID != localActorID && follower.LocalActorID != s.instance.ActorID {
			continue
		}
		inbox := follower.SharedInboxURL
		if inbox == "" {
			inbox = follower.InboxURL
		}
		if inbox != "" && !seen[inbox] {
			seen[inbox] = true
			inboxes = append(inboxes, inbox)
		}
	}
	return inboxes
}

func (s *Service) deliverActivity(ctx context.Context, activity map[string]any, inboxes []string) {
	if len(inboxes) == 0 {
		return
	}
	body, err := json.Marshal(activity)
	if err != nil {
		return
	}
	activityID, _ := activity["id"].(string)
	if activityID == "" {
		return
	}
	for _, inbox := range inboxes {
		if err := s.store.EnqueueFederationDelivery(ctx, activityID, body, inbox, s.now()); err != nil {
			log.Printf("federation enqueue %s: %v", inbox, err)
		}
	}
}

func (s *Service) ProcessDeliveries(ctx context.Context, limit int) {
	if !s.Enabled() {
		return
	}
	if limit <= 0 {
		limit = 10
	}
	for index := 0; index < limit; index++ {
		delivery, ok, err := s.store.ClaimFederationDelivery(ctx, workerID, s.now(), s.cfg.Lease)
		if err != nil {
			log.Printf("federation claim delivery: %v", err)
			return
		}
		if !ok {
			return
		}
		if err := s.deliver(ctx, delivery); err != nil {
			retryAt := s.now().Add(deliveryBackoff(delivery.Attempts))
			status, retryErr := s.store.RetryFederationDelivery(
				ctx, delivery.ID, workerID, err.Error(), retryAt, s.now(),
			)
			if retryErr != nil {
				log.Printf("federation retry delivery %d: %v", delivery.ID, retryErr)
			} else if status == "failed" {
				log.Printf("federation delivery %d failed permanently: %v", delivery.ID, err)
			}
			continue
		}
		if err := s.store.FinishFederationDelivery(ctx, delivery.ID, workerID, s.now()); err != nil {
			log.Printf("federation finish delivery %d: %v", delivery.ID, err)
		}
	}
}

func deliveryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Duration(1<<uint(minInt(attempts, 8))) * time.Minute
	return delay
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *Service) deliver(ctx context.Context, delivery store.FederationDelivery) error {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, delivery.InboxURL, bytes.NewReader(delivery.Activity),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/activity+json")
	request.Header.Set("Accept", "application/activity+json")
	request.Header.Set("User-Agent", "KeelTube/0.1 (+"+s.instance.BaseURL+")")
	if err := SignRequest(request, delivery.Activity, s.instanceActorURL()+"#main-key", s.privateKey, s.now()); err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("federation inbox returned %d", response.StatusCode)
	}
	return nil
}
