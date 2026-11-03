package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	maxTitleLength = 200
	maxBodyLength  = 1000
	maxHrefLength  = 500
	maxKindLength  = 64

	commentPreviewLength = 160
)

type Service struct {
	store *store.Store
}

func New(st *store.Store) *Service {
	return &Service{store: st}
}

func (s *Service) Create(ctx context.Context, input store.NotificationInput) (store.Notification, bool, error) {
	if s == nil || s.store == nil {
		return store.Notification{}, false, errors.New("notifications: store is not configured")
	}
	if err := validate(input); err != nil {
		return store.Notification{}, false, err
	}
	return s.store.CreateNotification(ctx, input)
}

// Comment creates creator notifications for a new comment or reply. It is best-effort:
// the caller decides whether a notification failure should affect the comment request.
func (s *Service) Comment(
	ctx context.Context,
	comment store.Comment,
	video store.Video,
	parent *store.Comment,
) error {
	if s == nil || s.store == nil {
		return nil
	}
	ownerActorID, err := s.store.GetChannelActorID(ctx, video.ChannelID)
	if err != nil {
		return err
	}
	href := videoHref(video)
	preview := fmt.Sprintf("%s: %s", comment.Author, truncate(comment.Body, commentPreviewLength))

	type recipient struct {
		actorID string
		kind    string
		title   string
	}
	recipients := make([]recipient, 0, 2)
	if parent != nil {
		if parent.ActorID != comment.ActorID {
			recipients = append(recipients, recipient{
				actorID: parent.ActorID,
				kind:    "comment.reply",
				title:   "New reply to your comment",
			})
		}
		if ownerActorID != comment.ActorID && ownerActorID != parent.ActorID {
			recipients = append(recipients, recipient{
				actorID: ownerActorID,
				kind:    "comment.video",
				title:   truncate("New reply on "+video.Title, maxTitleLength),
			})
		}
	} else if ownerActorID != comment.ActorID {
		recipients = append(recipients, recipient{
			actorID: ownerActorID,
			kind:    "comment.video",
			title:   truncate("New comment on "+video.Title, maxTitleLength),
		})
	}

	payload, err := json.Marshal(map[string]string{
		"comment_id": comment.ID,
		"video_id":   video.ID,
		"actor_id":   comment.ActorID,
	})
	if err != nil {
		return err
	}

	var errs []error
	for _, item := range recipients {
		_, _, err := s.Create(ctx, store.NotificationInput{
			ActorID:   item.actorID,
			ChannelID: video.ChannelID,
			Kind:      item.kind,
			Title:     item.title,
			Body:      preview,
			Href:      href,
			Payload:   payload,
			DedupeKey: "comment:" + comment.ID + ":" + item.actorID,
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) UploadFailed(ctx context.Context, videoID, reason string) error {
	if s == nil || s.store == nil {
		return nil
	}
	video, err := s.store.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	ownerActorID, err := s.store.GetChannelActorID(ctx, video.ChannelID)
	if err != nil {
		return err
	}
	body := video.Title + " could not be processed."
	if strings.TrimSpace(reason) != "" {
		body = video.Title + " could not be processed: " + reason
	}
	payload, err := json.Marshal(map[string]string{
		"video_id": video.ID,
		"reason":   reason,
	})
	if err != nil {
		return err
	}
	_, _, err = s.Create(ctx, store.NotificationInput{
		ActorID:   ownerActorID,
		ChannelID: video.ChannelID,
		Kind:      "upload.failed",
		Title:     "Your upload could not be processed",
		Body:      truncate(body, maxBodyLength),
		Href:      "/studio/videos/" + video.ID + "/edit",
		Payload:   payload,
		DedupeKey: "upload:failed:" + video.ID,
	})
	return err
}

func (s *Service) VideoPublished(ctx context.Context, videoID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	video, err := s.store.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	ownerActorID, err := s.store.GetChannelActorID(ctx, video.ChannelID)
	if err != nil {
		return err
	}
	body := video.Title + " finished processing."
	if video.Visibility == "public" {
		body = video.Title + " is now live."
	}
	payload, err := json.Marshal(map[string]string{"video_id": video.ID})
	if err != nil {
		return err
	}
	_, _, err = s.Create(ctx, store.NotificationInput{
		ActorID:   ownerActorID,
		ChannelID: video.ChannelID,
		Kind:      "upload.published",
		Title:     "Your video is published",
		Body:      truncate(body, maxBodyLength),
		Href:      "/studio/videos/" + video.ID + "/edit",
		Payload:   payload,
		DedupeKey: "upload:published:" + video.ID,
	})
	return err
}

func validate(input store.NotificationInput) error {
	if strings.TrimSpace(input.ActorID) == "" {
		return fmt.Errorf("%w: actor_id is required", store.ErrInvalidInput)
	}
	if strings.TrimSpace(input.Kind) == "" || len([]rune(input.Kind)) > maxKindLength {
		return fmt.Errorf("%w: kind must be between 1 and %d characters", store.ErrInvalidInput, maxKindLength)
	}
	if strings.TrimSpace(input.Title) == "" || len([]rune(input.Title)) > maxTitleLength {
		return fmt.Errorf("%w: title must be between 1 and %d characters", store.ErrInvalidInput, maxTitleLength)
	}
	if len([]rune(input.Body)) > maxBodyLength {
		return fmt.Errorf("%w: body must be at most %d characters", store.ErrInvalidInput, maxBodyLength)
	}
	if len([]rune(input.Href)) > maxHrefLength {
		return fmt.Errorf("%w: href must be at most %d characters", store.ErrInvalidInput, maxHrefLength)
	}
	if len([]rune(input.DedupeKey)) > 200 {
		return fmt.Errorf("%w: dedupe_key must be at most 200 characters", store.ErrInvalidInput)
	}
	return nil
}

func videoHref(video store.Video) string {
	if video.PublicID != "" {
		return "/watch/" + video.PublicID
	}
	return "/watch/" + video.ID
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
