package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func studioStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:8081", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return st, func() { _ = conn.Close() }
}

func TestStudioVideoLifecycleAndOwnership(t *testing.T) {
	st, cleanup := studioStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	created, err := st.CreateStudioVideo(ctx, store.Video{
		ID: "vid_studio_test", ChannelID: "ch_demo", Title: "Studio draft",
		Status: "draft", Visibility: "private", Playback: store.Playback{},
		License: "standard", Format: "video", Category: "Documentaries",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create studio video: %v", err)
	}
	if created.PublicID == "" || len(created.PublicID) != 11 {
		t.Fatalf("unexpected public id: %q", created.PublicID)
	}

	videos, total, err := st.ListStudioVideos(ctx, "ch_demo", store.StudioVideoFilter{Status: "draft", Limit: 20})
	if err != nil {
		t.Fatalf("list studio videos: %v", err)
	}
	if total < 2 {
		t.Fatalf("expected seeded draft and new draft, got total=%d", total)
	}
	found := false
	for _, video := range videos {
		if video.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("new draft was not listed")
	}

	if _, err := st.GetStudioVideo(ctx, "ch_keel_lab", created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ownership 404, got %v", err)
	}

	updated, err := st.UpdateStudioVideo(ctx, "ch_demo", created.ID, store.VideoMetadataInput{
		Title: "Updated title", Description: "Updated description", Visibility: "unlisted",
		Category: "Studio", License: "cc-by", Tags: []string{"studio"}, Format: "video",
	}, &created.UpdatedAt, false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("update studio video: %v", err)
	}
	if updated.Title != "Updated title" || updated.Category != "Studio" {
		t.Fatalf("unexpected update: %+v", updated)
	}

	if _, err := st.UpdateStudioVideo(ctx, "ch_demo", created.ID, store.VideoMetadataInput{
		Title: "Stale", Visibility: "private", Category: "Studio", License: "cc-by", Format: "video",
	}, &created.UpdatedAt, false, now.Add(2*time.Minute)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected stale update conflict, got %v", err)
	}

	if _, err := st.PublishStudioVideo(ctx, "ch_demo", created.ID, now.Add(3*time.Minute)); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("expected publish without playback to fail, got %v", err)
	}
	updated.Playback = store.Playback{MP4URL: "https://example.com/video.mp4"}
	updated.UpdatedAt = now.Add(3 * time.Minute)
	if err := st.UpsertVideo(ctx, updated); err != nil {
		t.Fatalf("add playback: %v", err)
	}
	published, err := st.PublishStudioVideo(ctx, "ch_demo", created.ID, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("publish studio video: %v", err)
	}
	if published.Status != "published" || published.PublishedAt == nil {
		t.Fatalf("unexpected published video: %+v", published)
	}
}

func TestStudioVersionsAndStats(t *testing.T) {
	st, cleanup := studioStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()

	version, err := st.CreateVideoVersion(ctx, store.VideoVersion{
		VideoID: "vid_draft_001", ChannelID: "ch_demo", Label: "First cut",
		Trim:   store.VideoTrim{Start: 0, End: 8},
		Cuts:   []store.VideoCut{{Start: 2, End: 3}},
		Volume: 1, CreatedBy: "usr_demo", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if version.ID == "" || version.Cuts[0].Start != 2 {
		t.Fatalf("unexpected version: %+v", version)
	}
	versions, err := st.ListVideoVersions(ctx, "ch_demo", "vid_draft_001")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != version.ID {
		t.Fatalf("unexpected versions: %+v", versions)
	}
	if _, err := st.GetVideoVersion(ctx, "ch_keel_lab", "vid_draft_001", version.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected version ownership 404, got %v", err)
	}

	stats, err := st.StudioStats(ctx, "ch_demo")
	if err != nil {
		t.Fatalf("studio stats: %v", err)
	}
	if stats.VideoCount < 6 || stats.Subscribers != 2_400_000 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(stats.Recent) == 0 {
		t.Fatal("expected recent videos")
	}
}

func TestMediaUploadAndJobQueue(t *testing.T) {
	st, cleanup := studioStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	upload, err := st.CreateUpload(ctx, store.Video{
		ID: "vid_upload_test", ChannelID: "ch_demo", Title: "Upload test",
		Status: "processing", Visibility: "private", Playback: store.Playback{},
		License: "standard", Format: "video", Category: "Documentaries",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}, store.UploadIntent{
		ChannelID: "ch_demo", ActorID: "usr_demo", S3UploadID: "s3-upload",
		Bucket: "bucket", ObjectKey: "media/ch_demo/vid_upload_test/source.mp4",
		Filename: "source.mp4", ContentType: "video/mp4", SizeBytes: 10,
		PartSize: 5, Status: "pending", ExpiresAt: now.Add(time.Hour),
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if upload.ID == "" || upload.VideoID != "vid_upload_test" {
		t.Fatalf("unexpected upload: %+v", upload)
	}

	completed, err := st.MarkUploadCompleted(ctx, upload.ID, "", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("mark upload completed: %v", err)
	}
	if completed.Status != "scanning" {
		t.Fatalf("unexpected upload status: %q", completed.Status)
	}

	job, ok, err := st.ClaimMediaJob(ctx, "worker-1", now.Add(2*time.Minute), time.Hour)
	if err != nil || !ok {
		t.Fatalf("claim media job: ok=%v err=%v", ok, err)
	}
	if job.Kind != "scan" || job.UploadID != upload.ID || job.Attempts != 1 {
		t.Fatalf("unexpected job: %+v", job)
	}
	if err := st.FinishMediaJob(ctx, job.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("finish job: %v", err)
	}
	if err := st.EnqueueMediaJob(ctx, "transcode", upload.ID, upload.VideoID, nil, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("enqueue transcode: %v", err)
	}
	if err := st.EnqueueMediaJob(ctx, "transcode", upload.ID, upload.VideoID, nil, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("duplicate enqueue should be ignored: %v", err)
	}
	job, ok, err = st.ClaimMediaJob(ctx, "worker-2", now.Add(5*time.Minute), time.Hour)
	if err != nil || !ok || job.Kind != "transcode" {
		t.Fatalf("claim transcode: ok=%v job=%+v err=%v", ok, job, err)
	}
}

func TestMediaBlocklist(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	ctx := context.Background()
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO media_blocklist (content_sha256, kind, reason, created_at)
		VALUES ($1, 'copyright', 'Known copyrighted work', NOW())
	`, digest); err != nil {
		t.Fatalf("insert blocklist: %v", err)
	}
	kind, reason, blocked, err := st.IsHashBlocked(ctx, digest)
	if err != nil {
		t.Fatalf("is hash blocked: %v", err)
	}
	if !blocked || kind != "copyright" || reason == "" {
		t.Fatalf("unexpected blocklist result: blocked=%v kind=%q reason=%q", blocked, kind, reason)
	}
}

func TestReviewVideoCannotPublish(t *testing.T) {
	st, cleanup := studioStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	video, err := st.CreateStudioVideo(ctx, store.Video{
		ID: "vid_review_test", ChannelID: "ch_demo", Title: "Review draft",
		Status: "draft", Visibility: "public",
		Playback: store.Playback{MP4URL: "https://example.com/video.mp4"},
		License:  "standard", Format: "video", Category: "Documentaries",
		Policy: &store.Policy{PolicyVersion: "studio-0.1", UploadStatus: "review", ScanVerdict: "review"},
		Tags:   []string{}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create review video: %v", err)
	}
	if _, err := st.PublishStudioVideo(ctx, "ch_demo", video.ID, now.Add(time.Minute)); !errors.Is(err, store.ErrInvalidState) {
		t.Fatalf("expected review publish to fail, got %v", err)
	}
}

func TestDuplicateContentByHash(t *testing.T) {
	st, cleanup := studioStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	published := now
	video := store.Video{
		ID: "vid_duplicate_source", PublicID: "duplicate01", ChannelID: "ch_keel_lab", Title: "Duplicate source",
		Status: "published", Visibility: "public", ContentHash: "sha256:abc123",
		Playback: store.Playback{MP4URL: "https://example.com/video.mp4"},
		License:  "standard", Format: "video", Category: "Documentaries",
		Tags: []string{}, CreatedAt: now, PublishedAt: &published, UpdatedAt: now,
	}
	if err := st.UpsertVideo(ctx, video); err != nil {
		t.Fatalf("upsert duplicate source: %v", err)
	}
	found, ok, err := st.FindDuplicateVideoByHash(ctx, "abc123", "vid_other")
	if err != nil {
		t.Fatalf("find duplicate: %v", err)
	}
	if !ok || found.ID != video.ID {
		t.Fatalf("expected duplicate %s, got found=%v video=%+v", video.ID, ok, found)
	}
	if _, ok, err := st.FindDuplicateVideoByHash(ctx, "abc123", video.ID); err != nil || ok {
		t.Fatalf("expected exclusion of same video, ok=%v err=%v", ok, err)
	}
}
