package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type inboxEnvelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Actor     json.RawMessage `json:"actor"`
	Object    json.RawMessage `json:"object"`
	Target    json.RawMessage `json:"target"`
	Published string          `json:"published"`
}

type remoteActor struct {
	ID                string
	URL               string
	Domain            string
	Type              string
	PreferredUsername string
	Name              string
	Summary           string
	InboxURL          string
	OutboxURL         string
	SharedInboxURL    string
	AvatarURL         string
	BannerURL         string
	PublicKeyPEM      string
}

func (s *Service) HandleInbox(ctx context.Context, localActorID string, request *http.Request, body []byte) (int, error) {
	if !s.Enabled() {
		return http.StatusServiceUnavailable, ErrDisabled
	}
	if localActorID != s.instance.ActorID && localActorID != "instance" {
		if _, err := s.store.GetChannel(ctx, localActorID); err != nil {
			return http.StatusNotFound, ErrActorNotFound
		}
	}
	keyID, err := VerifyRequest(ctx, request, body, s.now(), s.fetchPublicKey)
	if err != nil {
		return http.StatusUnauthorized, err
	}
	actorURL := strings.SplitN(keyID, "#", 2)[0]
	if actorURL == "" {
		return http.StatusUnauthorized, ErrSignatureInvalid
	}
	if peer, err := s.store.GetFederationPeer(ctx, actorURL); err == nil && peer.Status == "blocked" {
		return http.StatusForbidden, ErrBlockedPeer
	}
	var envelope inboxEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return http.StatusBadRequest, ErrInvalidActivity
	}
	if strings.TrimSpace(envelope.ID) == "" || strings.TrimSpace(envelope.Type) == "" {
		return http.StatusBadRequest, ErrInvalidActivity
	}
	activityActor := rawURL(envelope.Actor)
	if activityActor != "" && activityActor != actorURL {
		return http.StatusForbidden, ErrSignatureInvalid
	}
	first, err := s.store.RecordFederationInboxActivity(ctx, store.FederationInboxActivity{
		ActivityID: envelope.ID,
		ActorURL:   actorURL,
		Type:       envelope.Type,
		ReceivedAt: s.now(),
	})
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if !first {
		return http.StatusAccepted, nil
	}
	if err := s.handleActivity(ctx, localActorID, actorURL, envelope); err != nil {
		return http.StatusUnprocessableEntity, err
	}
	return http.StatusAccepted, nil
}

func (s *Service) handleActivity(
	ctx context.Context,
	localActorID, actorURL string,
	envelope inboxEnvelope,
) error {
	switch envelope.Type {
	case "Follow":
		return s.handleFollow(ctx, localActorID, actorURL, envelope)
	case "Undo":
		return s.handleUndo(ctx, actorURL, envelope)
	case "Accept":
		return s.handleFollowResult(ctx, actorURL, envelope, "accepted")
	case "Reject":
		return s.handleFollowResult(ctx, actorURL, envelope, "rejected")
	case "Create", "Update":
		if !s.acceptsContentFrom(ctx, actorURL) {
			return nil
		}
		object := rawMap(envelope.Object)
		if strings.EqualFold(rawString(object["type"]), "Video") {
			return s.upsertRemoteVideo(ctx, actorURL, object)
		}
	case "Delete":
		if !s.acceptsContentFrom(ctx, actorURL) {
			return nil
		}
		return s.handleRemoteDelete(ctx, envelope)
	case "Flag":
		// Flags are retained in the inbox log. A receiving instance may forward
		// them to its own moderation pipeline; KeelTube never auto-enforces from
		// a remote flag.
		return nil
	}
	return nil
}

func (s *Service) handleFollow(
	ctx context.Context,
	localActorID, actorURL string,
	envelope inboxEnvelope,
) error {
	targetActorID := localActorFromObject(envelope.Object)
	if targetActorID == "" {
		targetActorID = localActorID
	}
	if targetActorID != s.instance.ActorID && targetActorID != "instance" {
		if _, err := s.store.GetChannel(ctx, targetActorID); err != nil {
			return ErrActorNotFound
		}
	}
	remote, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return err
	}
	now := s.now()
	accepted := now
	if err := s.store.UpsertFederationFollower(ctx, store.FederationFollower{
		ActorURL:       remote.URL,
		LocalActorID:   targetActorID,
		ActorID:        remote.ID,
		InboxURL:       remote.InboxURL,
		SharedInboxURL: remote.SharedInboxURL,
		ActivityID:     envelope.ID,
		AcceptedAt:     &accepted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		return err
	}
	accept := map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        s.activityURL(),
		"type":      "Accept",
		"actor":     s.actorURL(targetActorID),
		"published": now,
		"object": map[string]any{
			"id":     envelope.ID,
			"type":   "Follow",
			"actor":  remote.URL,
			"object": s.actorURL(targetActorID),
		},
	}
	s.deliverActivity(ctx, accept, []string{remoteInbox(remote)})
	return nil
}

func (s *Service) handleUndo(ctx context.Context, actorURL string, envelope inboxEnvelope) error {
	object := rawMap(envelope.Object)
	objectType := rawString(object["type"])
	if objectType == "" {
		objectType = "Follow"
	}
	if strings.EqualFold(objectType, "Follow") {
		if err := s.store.DeleteFederationFollower(ctx, actorURL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleFollowResult(
	ctx context.Context,
	actorURL string,
	envelope inboxEnvelope,
	status string,
) error {
	object := rawMap(envelope.Object)
	if objectType := rawString(object["type"]); objectType != "" && !strings.EqualFold(objectType, "Follow") {
		return nil
	}
	if err := s.store.SetFederationFollowingStatus(ctx, actorURL, status, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) acceptsContentFrom(ctx context.Context, actorURL string) bool {
	following, err := s.store.GetFederationFollowing(ctx, actorURL)
	return err == nil && following.Status == "accepted"
}

func (s *Service) handleRemoteDelete(ctx context.Context, envelope inboxEnvelope) error {
	objectID := ""
	if text := rawString(envelope.Object); text != "" {
		objectID = text
	} else {
		object := rawMap(envelope.Object)
		objectID = rawString(object["id"])
	}
	if objectID == "" {
		return nil
	}
	videoID := remoteVideoID(objectID)
	video, err := s.store.GetVideo(ctx, videoID)
	if err != nil {
		return nil
	}
	return s.store.SetVideoStatus(ctx, video.ID, "removed", s.now())
}

func (s *Service) upsertRemoteVideo(ctx context.Context, activityActorURL string, object map[string]any) error {
	videoObjectURL := rawString(object["id"])
	if videoObjectURL == "" {
		return ErrInvalidActivity
	}
	actorURL := rawURL(object["attributedTo"])
	if actorURL == "" {
		actorURL = activityActorURL
	}
	remote, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return err
	}
	now := s.now()
	channelID := remoteID("ch", remote.URL)
	actorID := remoteID("usr", remote.URL)
	handle := "@remote_" + shortHash(remote.URL)
	name := firstNonEmpty(remote.Name, remote.PreferredUsername, "Remote creator")
	if err := s.store.UpsertActor(ctx, store.Actor{
		ID:          actorID,
		Type:        "channel",
		Handle:      handle,
		DisplayName: name,
		PublicKey:   remote.PublicKeyPEM,
		InboxURL:    remote.InboxURL,
		OutboxURL:   remote.OutboxURL,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return err
	}
	if err := s.store.UpsertChannel(ctx, store.Channel{
		ID:          channelID,
		ActorID:     actorID,
		Handle:      handle,
		Name:        name,
		Description: remote.Summary,
		AvatarURL:   remote.AvatarURL,
		BannerURL:   remote.BannerURL,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return err
	}

	title := firstNonEmpty(rawString(object["name"]), rawString(object["summary"]), "Federated video")
	description := firstNonEmpty(rawString(object["content"]), rawString(object["summary"]))
	duration := rawInt(object["duration"])
	publishedAt := rawTime(object["published"])
	updatedAt := rawTime(object["updated"])
	if updatedAt.IsZero() {
		updatedAt = now
	}
	if publishedAt.IsZero() {
		publishedAt = updatedAt
	}
	contentHash := rawString(object["keeltube:contentHash"])
	if contentHash == "" {
		contentHash = "sha256:" + digestHex([]byte(videoObjectURL))
	}
	videoID := remoteVideoID(videoObjectURL)
	video := store.Video{
		ID:              videoID,
		PublicID:        remotePublicID(videoObjectURL),
		ChannelID:       channelID,
		Title:           title,
		Description:     description,
		Status:          "published",
		Visibility:      "public",
		ContentHash:     contentHash,
		DurationSeconds: duration,
		Playback:        extractPlayback(object),
		License:         firstNonEmpty(rawString(object["license"]), "unknown"),
		Format:          "video",
		Category:        "Federation",
		Tags:            extractTags(object),
		CreatedAt:       publishedAt,
		PublishedAt:     &publishedAt,
		UpdatedAt:       updatedAt,
		Policy: &store.Policy{
			PolicyVersion: firstNonEmpty(
				rawString(object["keeltube:policyVersion"]), s.instance.PolicyVersion,
			),
		},
	}
	if err := s.store.UpsertVideo(ctx, video); err != nil {
		return err
	}
	return nil
}

func (s *Service) fetchPublicKey(ctx context.Context, keyID string) (string, error) {
	actorURL := strings.SplitN(keyID, "#", 2)[0]
	if peer, err := s.store.GetFederationPeer(ctx, actorURL); err == nil {
		if peer.Status == "blocked" {
			return "", ErrBlockedPeer
		}
		if peer.PublicKeyPEM != "" {
			return peer.PublicKeyPEM, nil
		}
	}
	remote, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return "", err
	}
	if remote.PublicKeyPEM == "" {
		return "", ErrRemoteUnavailable
	}
	return remote.PublicKeyPEM, nil
}

func (s *Service) resolveRemoteActor(ctx context.Context, actorURL string) (remoteActor, error) {
	parsed, err := url.Parse(actorURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return remoteActor{}, ErrRemoteUnavailable
	}
	if existing, err := s.store.GetFederationPeer(ctx, actorURL); err == nil {
		if existing.Status == "blocked" {
			return remoteActor{}, ErrBlockedPeer
		}
		if existing.PublicKeyPEM != "" && existing.InboxURL != "" {
			return remoteActor{
				ID:                existing.ActorURL,
				URL:               existing.ActorURL,
				Domain:            existing.Domain,
				Type:              existing.ActorType,
				PreferredUsername: existing.PreferredUsername,
				Name:              existing.Name,
				Summary:           existing.Summary,
				InboxURL:          existing.InboxURL,
				OutboxURL:         existing.OutboxURL,
				SharedInboxURL:    existing.SharedInboxURL,
				AvatarURL:         existing.AvatarURL,
				BannerURL:         existing.BannerURL,
				PublicKeyPEM:      existing.PublicKeyPEM,
			}, nil
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURL, nil)
	if err != nil {
		return remoteActor{}, ErrRemoteUnavailable
	}
	request.Header.Set("Accept", "application/activity+json, application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"")
	request.Header.Set("User-Agent", "KeelTube/0.1 (+"+s.instance.BaseURL+")")
	response, err := s.client.Do(request)
	if err != nil {
		return remoteActor{}, fmt.Errorf("%w: %v", ErrRemoteUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return remoteActor{}, ErrRemoteUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return remoteActor{}, fmt.Errorf("%w: actor returned %d", ErrRemoteUnavailable, response.StatusCode)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		return remoteActor{}, ErrRemoteUnavailable
	}
	remoteURL := firstNonEmpty(rawString(document["id"]), actorURL)
	publicKey := rawMap(document["publicKey"])
	remote := remoteActor{
		ID:                remoteURL,
		URL:               remoteURL,
		Domain:            parsed.Host,
		Type:              rawString(document["type"]),
		PreferredUsername: rawString(document["preferredUsername"]),
		Name:              rawString(document["name"]),
		Summary:           rawString(document["summary"]),
		InboxURL:          rawString(document["inbox"]),
		OutboxURL:         rawString(document["outbox"]),
		PublicKeyPEM:      rawString(publicKey["publicKeyPem"]),
	}
	if endpoints := rawMap(document["endpoints"]); endpoints != nil {
		remote.SharedInboxURL = rawString(endpoints["sharedInbox"])
	}
	if icon := rawMap(document["icon"]); icon != nil {
		remote.AvatarURL = firstNonEmpty(rawString(icon["url"]), rawString(icon["href"]))
	}
	if image := rawMap(document["image"]); image != nil {
		remote.BannerURL = firstNonEmpty(rawString(image["url"]), rawString(image["href"]))
	}
	if remote.InboxURL == "" {
		return remoteActor{}, ErrRemoteUnavailable
	}
	if err := s.store.UpsertFederationPeer(ctx, store.FederationPeer{
		Domain:            remote.Domain,
		ActorURL:          remote.URL,
		InboxURL:          remote.InboxURL,
		SharedInboxURL:    remote.SharedInboxURL,
		PublicKeyPEM:      remote.PublicKeyPEM,
		ActorType:         remote.Type,
		PreferredUsername: remote.PreferredUsername,
		Name:              remote.Name,
		Summary:           remote.Summary,
		AvatarURL:         remote.AvatarURL,
		BannerURL:         remote.BannerURL,
		OutboxURL:         remote.OutboxURL,
		Status:            "active",
		CreatedAt:         s.now(),
		UpdatedAt:         s.now(),
	}); err != nil {
		return remoteActor{}, err
	}
	return remote, nil
}

func (s *Service) FollowRemote(ctx context.Context, actorURL string) (store.FederationFollowing, error) {
	remote, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return store.FederationFollowing{}, err
	}
	now := s.now()
	localURL := s.instanceActorURL()
	follow := map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        s.activityURL(),
		"type":      "Follow",
		"actor":     localURL,
		"published": now,
		"object":    remote.URL,
	}
	followID, _ := follow["id"].(string)
	following := store.FederationFollowing{
		ActorURL:         remote.URL,
		LocalActorID:     s.instance.ActorID,
		ActorID:          remote.ID,
		InboxURL:         remote.InboxURL,
		SharedInboxURL:   remote.SharedInboxURL,
		FollowActivityID: followID,
		Status:           "pending",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.UpsertFederationFollowing(ctx, following); err != nil {
		return store.FederationFollowing{}, err
	}
	s.deliverActivity(ctx, follow, []string{remoteInbox(remote)})
	return following, nil
}

func (s *Service) UnfollowRemote(ctx context.Context, actorURL string) error {
	following, err := s.store.GetFederationFollowing(ctx, actorURL)
	if err != nil {
		return err
	}
	undo := map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        s.activityURL(),
		"type":      "Undo",
		"actor":     s.actorURL(following.LocalActorID),
		"published": s.now(),
		"object": map[string]any{
			"id":     following.FollowActivityID,
			"type":   "Follow",
			"actor":  s.actorURL(following.LocalActorID),
			"object": following.ActorURL,
		},
	}
	inbox := following.SharedInboxURL
	if inbox == "" {
		inbox = following.InboxURL
	}
	s.deliverActivity(ctx, undo, []string{inbox})
	return s.store.DeleteFederationFollowing(ctx, actorURL)
}

func privateHost(ctx context.Context, host string) bool {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return true
	}
	for _, address := range addresses {
		ip := address.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func remoteInbox(remote remoteActor) string {
	if remote.SharedInboxURL != "" {
		return remote.SharedInboxURL
	}
	return remote.InboxURL
}

func localActorFromObject(value json.RawMessage) string {
	if text := rawStringValue(value); text != "" {
		return lastPathID(text)
	}
	object := rawMap(value)
	if id := rawString(object["id"]); id != "" {
		return lastPathID(id)
	}
	return ""
}

func lastPathID(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	_, last := splitLast(path, "/")
	return last
}

func splitLast(value, separator string) (string, string) {
	index := strings.LastIndex(value, separator)
	if index < 0 {
		return "", value
	}
	return value[:index], value[index+len(separator):]
}

func remoteID(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "_remote_" + hex.EncodeToString(digest[:12])
}

func remoteVideoID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "vid_remote_" + hex.EncodeToString(digest[:12])
}

func remotePublicID(value string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	digest := sha256.Sum256([]byte(value))
	buf := make([]byte, 11)
	for index := range buf {
		buf[index] = alphabet[int(digest[index])%len(alphabet)]
	}
	return string(buf)
}

func shortHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:6])
}

func extractPlayback(object map[string]any) store.Playback {
	playback := store.Playback{}
	if value := rawString(object["thumbnailUrl"]); value != "" {
		playback.ThumbnailURL = value
	}
	if icon := rawMap(object["icon"]); icon != nil {
		playback.ThumbnailURL = firstNonEmpty(playback.ThumbnailURL, rawString(icon["url"]), rawString(icon["href"]))
	}
	addURL := func(value string, tags []string) {
		lower := strings.ToLower(value)
		tagSet := strings.Join(tags, " ")
		if strings.Contains(lower, ".m3u8") || strings.Contains(strings.ToLower(tagSet), "hls") {
			playback.HLSURL = firstNonEmpty(playback.HLSURL, value)
		} else if strings.Contains(lower, ".mp4") || strings.Contains(strings.ToLower(tagSet), "mp4") {
			playback.MP4URL = firstNonEmpty(playback.MP4URL, value)
		}
	}
	switch value := object["url"].(type) {
	case string:
		addURL(value, nil)
	case []any:
		for _, item := range value {
			switch typed := item.(type) {
			case string:
				addURL(typed, nil)
			case map[string]any:
				href := firstNonEmpty(rawString(typed["href"]), rawString(typed["url"]))
				addURL(href, rawStringSlice(typed["tag"]))
			}
		}
	}
	if subtitleLanguage, ok := object["subtitleLanguage"].([]any); ok {
		for _, item := range subtitleLanguage {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			href := firstNonEmpty(rawString(entry["href"]), rawString(entry["url"]))
			if href != "" {
				playback.CaptionsURL = href
				break
			}
		}
	}
	return playback
}

func extractTags(object map[string]any) []string {
	tags := make([]string, 0)
	switch value := object["tag"].(type) {
	case []any:
		for _, item := range value {
			switch typed := item.(type) {
			case string:
				tags = append(tags, typed)
			case map[string]any:
				if name := rawString(typed["name"]); name != "" {
					tags = append(tags, name)
				}
			}
		}
	case []string:
		tags = append(tags, value...)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags
}

func rawMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var result map[string]any
		if json.Unmarshal(typed, &result) == nil {
			return result
		}
	}
	return nil
}

func rawString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func rawStringValue(value json.RawMessage) string {
	var result string
	if len(value) > 0 && json.Unmarshal(value, &result) == nil {
		return result
	}
	return ""
}

func rawURL(value any) string {
	if raw, ok := value.(json.RawMessage); ok {
		if text := rawStringValue(raw); text != "" {
			return text
		}
		value = rawMap(raw)
	}
	if text := rawString(value); text != "" {
		return text
	}
	object := rawMap(value)
	return rawString(object["id"])
}

func rawInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	case int:
		return typed
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}

func rawTime(value any) time.Time {
	text := rawString(value)
	if text == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func rawStringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := rawString(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		return typed
	case string:
		return []string{typed}
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func envOr(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}
