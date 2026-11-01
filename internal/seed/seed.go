package seed

import (
	"context"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

var demoTime = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

const demoContentHash = "sha256:4d1a52f8c6b6e1c0e6f0f3d3b4a0f6f0a8e0c2d4b6a8f0e2c4d6b8a0f2e4c6d8"

func Run(ctx context.Context, st *store.Store, pipelineURL, webURL string) error {
	actor := store.Actor{
		ID:          "usr_demo",
		Type:        "person",
		Handle:      "@maravale",
		DisplayName: "Mara Vale",
		CreatedAt:   demoTime,
		UpdatedAt:   demoTime,
	}
	if err := st.UpsertActor(ctx, actor); err != nil {
		return err
	}

	channel := store.Channel{
		ID:          "ch_demo",
		ActorID:     actor.ID,
		Handle:      "@maravale",
		Name:        "Mara Vale",
		Description: "Original short documentaries.",
		CreatedAt:   demoTime,
		UpdatedAt:   demoTime,
	}
	if err := st.UpsertChannel(ctx, channel); err != nil {
		return err
	}

	playback := store.Playback{
		MP4URL:      pipelineURL + "/demo/sample.mp4",
		CaptionsURL: webURL + "/demo/sample.vtt",
	}
	policy := &store.Policy{PolicyVersion: "demo-0.1", MonetizationStatus: "eligible"}
	published := demoTime

	videos := []store.Video{
		{
			ID:              "vid_demo_001",
			ChannelID:       channel.ID,
			Title:           "The Last Lighthouse",
			Description:     "A short original documentary about a keeper on his final night. Demo asset only.",
			Status:          "published",
			Visibility:      "public",
			ContentHash:     demoContentHash,
			DurationSeconds: 8,
			Playback:        playback,
			License:         "cc-by",
			Policy:          policy,
			CreatedAt:       demoTime,
			PublishedAt:     &published,
			UpdatedAt:       demoTime,
		},
		{
			ID:              "vid_removed_001",
			ChannelID:       channel.ID,
			Title:           "Removed demo video",
			Description:     "A fixture used to verify the removed-video notice.",
			Status:          "removed",
			Visibility:      "public",
			ContentHash:     demoContentHash,
			DurationSeconds: 8,
			Playback:        playback,
			License:         "cc-by",
			Policy:          policy,
			CreatedAt:       demoTime,
			UpdatedAt:       demoTime,
		},
		{
			ID:              "vid_private_001",
			ChannelID:       channel.ID,
			Title:           "Private demo video",
			Description:     "A fixture used to verify the restricted-video notice.",
			Status:          "published",
			Visibility:      "private",
			ContentHash:     demoContentHash,
			DurationSeconds: 8,
			Playback:        playback,
			License:         "cc-by",
			Policy:          policy,
			CreatedAt:       demoTime,
			PublishedAt:     &published,
			UpdatedAt:       demoTime,
		},
	}

	for _, video := range videos {
		if err := st.UpsertVideo(ctx, video); err != nil {
			return err
		}
	}
	return nil
}
