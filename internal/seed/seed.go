package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

var demoTime = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

type seedVideo struct {
	ID          string
	PublicID    string
	ChannelID   string
	Title       string
	Description string
	Status      string
	Visibility  string
	Format      string
	Category    string
	Tags        []string
	Views       int64
	Likes       int64
	Comments    int64
	Duration    int
	PublishedAt *time.Time
	Thumbnail   string
}

func Run(ctx context.Context, st *store.Store, demoMediaURL, webURL, demoEmail, demoPassword string) error {
	actors := []store.Actor{
		{
			ID:          "usr_demo",
			Type:        "person",
			Handle:      "@maravale",
			DisplayName: "Mara Vale",
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "usr_keel_lab",
			Type:        "person",
			Handle:      "@keellab",
			DisplayName: "Keel Lab",
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "usr_northstar",
			Type:        "person",
			Handle:      "@northstar",
			DisplayName: "Northstar Creators",
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "usr_open_archive",
			Type:        "person",
			Handle:      "@openarchive",
			DisplayName: "The Open Archive",
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
	}
	for _, actor := range actors {
		if err := st.UpsertActor(ctx, actor); err != nil {
			return err
		}
	}

	channels := []store.Channel{
		{
			ID:          "ch_demo",
			ActorID:     "usr_demo",
			Handle:      "@maravale",
			Name:        "Mara Vale",
			Description: "Original short documentaries and field notes about people holding a line.",
			AvatarURL:   webURL + "/avatars/mara-vale.jpg",
			Subscribers: 2_400_000,
			Verified:    true,
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "ch_keel_lab",
			ActorID:     "usr_keel_lab",
			Handle:      "@keellab",
			Name:        "Keel Lab",
			Description: "Protocol, moderation, and trust engineering.",
			AvatarURL:   webURL + "/avatars/keel-lab.jpg",
			Subscribers: 840_000,
			Verified:    true,
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "ch_northstar",
			ActorID:     "usr_northstar",
			Handle:      "@northstar",
			Name:        "Northstar Creators",
			Description: "A collective for independent video and creator rights.",
			AvatarURL:   webURL + "/avatars/northstar.jpg",
			Subscribers: 320_000,
			Verified:    true,
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
		{
			ID:          "ch_open_archive",
			ActorID:     "usr_open_archive",
			Handle:      "@openarchive",
			Name:        "The Open Archive",
			Description: "Preservation, portability, and the public record.",
			AvatarURL:   webURL + "/avatars/open-archive.jpg",
			Subscribers: 156_000,
			Verified:    false,
			CreatedAt:   demoTime,
			UpdatedAt:   demoTime,
		},
	}
	for _, channel := range channels {
		if err := st.UpsertChannel(ctx, channel); err != nil {
			return err
		}
	}

	if demoEmail != "" && demoPassword != "" {
		passwordHash, err := auth.HashPassword(demoPassword)
		if err != nil {
			return err
		}
		verifiedAt := demoTime
		account := store.Account{
			ID:              "acct_demo",
			ActorID:         "usr_demo",
			Email:           strings.ToLower(strings.TrimSpace(demoEmail)),
			PasswordHash:    passwordHash,
			EmailVerifiedAt: &verifiedAt,
			CreatedAt:       demoTime,
			UpdatedAt:       demoTime,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			return err
		}
	}

	policy := &store.Policy{PolicyVersion: "demo-0.1", MonetizationStatus: "eligible"}
	toStore := func(video seedVideo) store.Video {
		createdAt := demoTime
		if video.PublishedAt != nil {
			createdAt = *video.PublishedAt
		}
		return store.Video{
			ID:              video.ID,
			PublicID:        video.PublicID,
			ChannelID:       video.ChannelID,
			Title:           video.Title,
			Description:     video.Description,
			Status:          video.Status,
			Visibility:      video.Visibility,
			ContentHash:     contentHash(video.ID),
			DurationSeconds: video.Duration,
			Playback: store.Playback{
				MP4URL:       demoMediaURL,
				ThumbnailURL: webURL + video.Thumbnail,
				CaptionsURL:  webURL + "/media/mock-captions.vtt",
			},
			License:       "cc-by",
			Policy:        policy,
			Format:        video.Format,
			Category:      video.Category,
			Tags:          video.Tags,
			Views:         video.Views,
			Likes:         video.Likes,
			CommentsCount: video.Comments,
			CreatedAt:     createdAt,
			PublishedAt:   video.PublishedAt,
			UpdatedAt:     createdAt,
		}
	}

	videos := []seedVideo{
		{
			ID:          "vid_demo_001",
			PublicID:    "ksKCMmvOxwQ",
			ChannelID:   "ch_demo",
			Title:       "The Last Lighthouse",
			Description: "A short original documentary about a keeper on his final night. Shot on the outer breakwater over one long exposure of weather, salt, and routine.",
			Status:      "published",
			Visibility:  "public",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{"documentary", "lighthouse", "short-film"},
			Views:       1_842_000,
			Likes:       182_400,
			Comments:    1_284,
			Duration:    8,
			PublishedAt: at(demoTime),
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
		{
			ID:          "vid_even_keel",
			PublicID:    "aB3dEf9hJkL",
			ChannelID:   "ch_keel_lab",
			Title:       "What an even keel actually means",
			Description: "Why stability, due process, and a portable exit belong in the video protocol.",
			Status:      "published",
			Visibility:  "public",
			Format:      "video",
			Category:    "Protocol",
			Tags:        []string{"protocol", "due-process", "stability"},
			Views:       684_000,
			Likes:       42_100,
			Comments:    312,
			Duration:    14,
			PublishedAt: at(demoTime.Add(time.Hour)),
			Thumbnail:   "/mock/aB3dEf9hJkL.jpg",
		},
		{
			ID:          "vid_ledger",
			PublicID:    "mN4pQr7sTuV",
			ChannelID:   "ch_keel_lab",
			Title:       "Inside the audit ledger",
			Description: "How an append-only, hash-chained record makes moderation reviewable.",
			Status:      "published",
			Visibility:  "public",
			Format:      "video",
			Category:    "Moderation",
			Tags:        []string{"moderation", "audit", "ledger"},
			Views:       92_000,
			Likes:       12_300,
			Comments:    98,
			Duration:    21,
			PublishedAt: at(demoTime.Add(2 * time.Hour)),
			Thumbnail:   "/mock/mN4pQr7sTuV.jpg",
		},
		{
			ID:          "vid_studio",
			PublicID:    "wX2yZa5bCdE",
			ChannelID:   "ch_northstar",
			Title:       "Build a creator studio",
			Description: "The workflow behind a channel that can move with its creator.",
			Status:      "published",
			Visibility:  "public",
			Format:      "video",
			Category:    "Studio",
			Tags:        []string{"studio", "creators", "workflow"},
			Views:       48_000,
			Likes:       5_600,
			Comments:    64,
			Duration:    12,
			PublishedAt: at(demoTime.Add(3 * time.Hour)),
			Thumbnail:   "/mock/aB9cDe2fGhI.jpg",
		},
		{
			ID:          "vid_federation",
			PublicID:    "fG6hIj8kLmN",
			ChannelID:   "ch_open_archive",
			Title:       "Federation without a gatekeeper",
			Description: "Portability, PeerTube compatibility, and the exit that stays open.",
			Status:      "published",
			Visibility:  "public",
			Format:      "video",
			Category:    "Federation",
			Tags:        []string{"federation", "portability", "protocol"},
			Views:       36_000,
			Likes:       4_200,
			Comments:    41,
			Duration:    18,
			PublishedAt: at(demoTime.Add(4 * time.Hour)),
			Thumbnail:   "/mock/bC7dEf9gHiJ.jpg",
		},
		{
			ID:          "vid_short_keel",
			PublicID:    "hI3jKl5mNoP",
			ChannelID:   "ch_keel_lab",
			Title:       "The even keel in 30 seconds",
			Description: "A short on stability, due process, and the portable exit.",
			Status:      "published",
			Visibility:  "public",
			Format:      "short",
			Category:    "Protocol",
			Tags:        []string{"short", "protocol", "even-keel"},
			Views:       210_000,
			Likes:       18_000,
			Comments:    120,
			Duration:    30,
			PublishedAt: at(demoTime.Add(5 * time.Hour)),
			Thumbnail:   "/mock/cD5eFg7hIjK.jpg",
		},
		{
			ID:          "vid_short_appeal",
			PublicID:    "qR7sTu9vWxY",
			ChannelID:   "ch_keel_lab",
			Title:       "How an appeal works",
			Description: "Grounds, evidence, independent review, and a recorded outcome.",
			Status:      "published",
			Visibility:  "public",
			Format:      "short",
			Category:    "Moderation",
			Tags:        []string{"short", "appeals", "moderation"},
			Views:       165_000,
			Likes:       14_200,
			Comments:    88,
			Duration:    28,
			PublishedAt: at(demoTime.Add(6 * time.Hour)),
			Thumbnail:   "/mock/dE3fGh5iJkL.jpg",
		},
		{
			ID:          "vid_short_export",
			PublicID:    "zA4bCd6eFgH",
			ChannelID:   "ch_northstar",
			Title:       "Export your archive",
			Description: "Your videos, metadata, and record should leave with you.",
			Status:      "published",
			Visibility:  "public",
			Format:      "short",
			Category:    "Studio",
			Tags:        []string{"short", "export", "portability"},
			Views:       88_000,
			Likes:       9_100,
			Comments:    37,
			Duration:    24,
			PublishedAt: at(demoTime.Add(7 * time.Hour)),
			Thumbnail:   "/mock/fG6hIj8kLmN.jpg",
		},
		{
			ID:          "vid_removed_001",
			PublicID:    "eF4gHi6jKlM",
			ChannelID:   "ch_demo",
			Title:       "Removed demo video",
			Description: "A fixture used to verify the removed-video notice.",
			Status:      "removed",
			Visibility:  "public",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{"documentary"},
			Duration:    8,
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
		{
			ID:          "vid_private_001",
			PublicID:    "nO8pQr1sTuV",
			ChannelID:   "ch_demo",
			Title:       "Private demo video",
			Description: "A fixture used to verify the restricted-video notice.",
			Status:      "published",
			Visibility:  "private",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{"documentary"},
			Duration:    8,
			PublishedAt: at(demoTime),
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
		{
			ID:          "vid_unlisted_001",
			PublicID:    "uK3nLx9pQ2r",
			ChannelID:   "ch_demo",
			Title:       "Unlisted demo video",
			Description: "A fixture used to verify unlisted access by id.",
			Status:      "published",
			Visibility:  "unlisted",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{"documentary"},
			Duration:    8,
			PublishedAt: at(demoTime),
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
		{
			ID:          "vid_draft_001",
			PublicID:    "fG5hIj7kLmN",
			ChannelID:   "ch_demo",
			Title:       "Draft demo video",
			Description: "A fixture used to verify draft visibility.",
			Status:      "draft",
			Visibility:  "private",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{},
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
		{
			ID:          "vid_processing_001",
			PublicID:    "oP9qRs2tUvW",
			ChannelID:   "ch_demo",
			Title:       "Processing demo video",
			Description: "A fixture used to verify processing visibility.",
			Status:      "processing",
			Visibility:  "private",
			Format:      "video",
			Category:    "Documentaries",
			Tags:        []string{},
			Thumbnail:   "/mock/ksKCMmvOxwQ.jpg",
		},
	}
	for _, video := range videos {
		if err := st.UpsertVideo(ctx, toStore(video)); err != nil {
			return err
		}
	}
	if err := seedFinance(ctx, st); err != nil {
		return err
	}
	return nil
}

func at(value time.Time) *time.Time {
	return &value
}

func contentHash(id string) string {
	sum := sha256.Sum256([]byte("keeltube:" + id))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func seedFinance(ctx context.Context, st *store.Store) error {
	channelID := "ch_demo"
	nextPayout := time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertMonetization(ctx, store.Monetization{
		ChannelID:    channelID,
		Eligible:     true,
		RPM:          4.82,
		Holdback:     620,
		Balance:      12840.5,
		Currency:     "USD",
		NextPayoutAt: &nextPayout,
		UpdatedAt:    demoTime,
	}); err != nil {
		return err
	}

	earnings := []store.Earning{
		{ID: "ern_001", ChannelID: channelID, Date: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Description: "October ad revenue", Type: "ad_revenue", Amount: 4280.5, Balance: 12840.5},
		{ID: "ern_002", ChannelID: channelID, Date: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Description: "October payout", Type: "payout", Amount: -4280.5, Balance: 8560},
		{ID: "ern_003", ChannelID: channelID, Date: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), Description: "Membership revenue", Type: "membership", Amount: 1240.25, Balance: 12840.5},
		{ID: "ern_004", ChannelID: channelID, Date: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Description: "September payout", Type: "payout", Amount: -3940.25, Balance: 11600.25},
		{ID: "ern_005", ChannelID: channelID, Date: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), Description: "September ad revenue", Type: "ad_revenue", Amount: 3940.25, Balance: 15540.5},
		{ID: "ern_006", ChannelID: channelID, Date: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), Description: "Dispute holdback", Type: "holdback", Amount: -620, Balance: 11600.25},
	}
	for _, earning := range earnings {
		if err := st.UpsertEarning(ctx, earning); err != nil {
			return err
		}
	}

	payouts := []store.Payout{
		{ID: "pay_2026_10", ChannelID: channelID, Period: "October 2026", Amount: 4280.5, Status: "paid", Date: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)},
		{ID: "pay_2026_09", ChannelID: channelID, Period: "September 2026", Amount: 3940.25, Status: "paid", Date: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)},
		{ID: "pay_2026_11", ChannelID: channelID, Period: "November 2026", Amount: 4619.75, Status: "pending", Date: nextPayout},
	}
	for _, payout := range payouts {
		if err := st.UpsertPayout(ctx, payout); err != nil {
			return err
		}
	}
	return nil
}
