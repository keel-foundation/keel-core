package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type fakeStorage struct {
	size         int64
	contentType  string
	created      int
	aborted      int
	completed    int
	deleted      int
	getsByKey    map[string]int
	objects      map[string][]byte
	sizes        map[string]int64
	contentTypes map[string]string
}

func (f *fakeStorage) CreateMultipartUpload(_ context.Context, key, contentType string) (string, error) {
	f.created++
	f.contentType = contentType
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	f.objects[key] = nil
	return "s3-upload", nil
}

func (f *fakeStorage) PresignUploadPart(_ context.Context, _, _ string, partNumber int32, _ time.Duration) (string, error) {
	return "https://storage.example/part/" + time.Duration(partNumber).String(), nil
}

func (f *fakeStorage) CompleteMultipartUpload(_ context.Context, _ string, _ string, _ []CompletedPart) error {
	f.completed++
	return nil
}

func (f *fakeStorage) AbortMultipartUpload(_ context.Context, _ string, _ string) error {
	f.aborted++
	return nil
}

func (f *fakeStorage) HeadObject(_ context.Context, key string) (ObjectInfo, error) {
	size := f.size
	if f.sizes != nil && f.sizes[key] > 0 {
		size = f.sizes[key]
	}
	contentType := f.contentType
	if f.contentTypes != nil && f.contentTypes[key] != "" {
		contentType = f.contentTypes[key]
	}
	return ObjectInfo{Size: size, ContentType: contentType}, nil
}

func (f *fakeStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if f.getsByKey == nil {
		f.getsByKey = make(map[string]int)
	}
	f.getsByKey[key]++
	return io.NopCloser(bytes.NewReader(f.objects[key])), nil
}

func (f *fakeStorage) PutObject(_ context.Context, key string, body io.Reader, _ string) error {
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.objects[key] = data
	return nil
}

func (f *fakeStorage) PresignPutObject(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://storage.example/put/" + key, nil
}

func (f *fakeStorage) PresignGetObject(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://storage.example/get/" + key, nil
}

func (f *fakeStorage) DeleteObject(_ context.Context, _ string) error {
	f.deleted++
	return nil
}

func testMediaConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Bucket:              "bucket",
		KeyPrefix:           "media",
		PartSize:            5 * 1024 * 1024,
		MaxUploadBytes:      10 * 1024 * 1024,
		PresignTTL:          time.Hour,
		WorkDir:             t.TempDir(),
		AllowedExtensions:   map[string]bool{".mp4": true},
		AllowedContentTypes: map[string]bool{"video/mp4": true},
	}
}

func TestEngineCreateCompleteAndAbortUpload(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fake := &fakeStorage{size: 6 * 1024 * 1024, contentType: "video/mp4"}
	engine := NewWithStorage(st, fake, testMediaConfig(t))
	ctx := context.Background()

	session, err := engine.CreateUpload(ctx, "ch_demo", "usr_demo", CreateUploadInput{
		Title: "Upload test", Description: "Description", Visibility: "private",
		Category: "Documentaries", License: "standard", Format: "video",
		Tags: []string{"test"}, Filename: "clip.mp4", ContentType: "video/mp4",
		SizeBytes: 6 * 1024 * 1024, RightsDeclaration: "original", RightsHolder: "Creator",
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if len(session.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(session.Parts))
	}
	if session.Video.Status != "processing" || session.Video.ChannelID != "ch_demo" {
		t.Fatalf("unexpected video: %+v", session.Video)
	}
	if session.Video.Policy == nil || session.Video.Policy.RightsDeclaration != "original" {
		t.Fatalf("unexpected rights policy: %+v", session.Video.Policy)
	}
	if fake.created != 1 {
		t.Fatalf("expected one multipart upload, got %d", fake.created)
	}
	refreshed, expiresAt, err := engine.RefreshUploadParts(ctx, "ch_demo", session.UploadID, []int32{2})
	if err != nil {
		t.Fatalf("refresh parts: %v", err)
	}
	if len(refreshed) != 1 || refreshed[0].PartNumber != 2 || expiresAt.IsZero() {
		t.Fatalf("unexpected refreshed parts: %+v expires=%v", refreshed, expiresAt)
	}
	if _, _, err := engine.RefreshUploadParts(ctx, "ch_demo", session.UploadID, []int32{99}); err == nil {
		t.Fatal("expected invalid part number error")
	}

	upload, err := engine.CompleteUpload(ctx, "ch_demo", session.UploadID, []CompletedPart{
		{PartNumber: 1, ETag: "etag-1"},
		{PartNumber: 2, ETag: "etag-2"},
	}, "")
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if upload.Status != "scanning" || fake.completed != 1 {
		t.Fatalf("unexpected complete result: %+v completed=%d", upload, fake.completed)
	}

	job, ok, err := st.ClaimMediaJob(ctx, "worker", time.Now().UTC(), time.Hour)
	if err != nil || !ok || job.Kind != "process" {
		t.Fatalf("claim process job: ok=%v job=%+v err=%v", ok, job, err)
	}

	second, err := engine.CreateUpload(ctx, "ch_demo", "usr_demo", CreateUploadInput{
		Title: "Abort test", Visibility: "private", Category: "Documentaries",
		License: "standard", Format: "video", Filename: "clip.mp4",
		ContentType: "video/mp4", SizeBytes: 6 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("create second upload: %v", err)
	}
	if err := engine.AbortUpload(ctx, "ch_demo", second.UploadID); err != nil {
		t.Fatalf("abort upload: %v", err)
	}
	if fake.aborted != 1 {
		t.Fatalf("expected one abort, got %d", fake.aborted)
	}
	aborted, err := st.GetUpload(ctx, "ch_demo", second.UploadID)
	if err != nil {
		t.Fatalf("get aborted upload: %v", err)
	}
	if aborted.Status != "aborted" {
		t.Fatalf("unexpected aborted status: %q", aborted.Status)
	}
}

func TestEngineRejectsPolicyAndInvalidInput(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := testMediaConfig(t)
	cfg.PolicyTerms = []string{"blockedterm"}
	engine := NewWithStorage(st, &fakeStorage{}, cfg)
	ctx := context.Background()

	_, err := engine.CreateUpload(ctx, "ch_demo", "usr_demo", CreateUploadInput{
		Title: "A blockedterm title", Visibility: "private", Category: "Documentaries",
		License: "standard", Format: "video", Filename: "clip.mp4",
		ContentType: "video/mp4", SizeBytes: 1024,
	})
	var policy *PolicyError
	if err == nil || !errors.As(err, &policy) {
		t.Fatalf("expected policy error, got %v", err)
	}

	_, err = engine.CreateUpload(ctx, "ch_demo", "usr_demo", CreateUploadInput{
		Title: "Valid", Visibility: "private", Category: "Documentaries",
		License: "standard", Format: "video", Filename: "clip.exe",
		ContentType: "application/x-msdownload", SizeBytes: 1024,
	})
	if err == nil {
		t.Fatal("expected invalid extension error")
	}
}

func TestEngineProcessesUploadToPublishedHLS(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.mp4")
	generate := exec.Command(ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10",
		"-t", "1", "-pix_fmt", "yuv420p", "-c:v", "libx264", sourcePath,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v: %s", err, output)
	}
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	fake := &fakeStorage{size: int64(len(sourceBytes)), contentType: "video/mp4"}
	cfg := testMediaConfig(t)
	cfg.MinDurationSeconds = 1
	cfg.MaxDurationSeconds = 60
	cfg.HLSSegmentSeconds = 2
	cfg.MaxRenditionHeight = 270
	engine := NewWithStorage(st, fake, cfg)
	ctx := context.Background()

	session, err := engine.CreateUpload(ctx, "ch_demo", "usr_demo", CreateUploadInput{
		Title: "Pipeline test", Visibility: "public", Category: "Documentaries",
		License: "standard", Format: "video", Filename: "clip.mp4",
		ContentType: "video/mp4", SizeBytes: int64(len(sourceBytes)),
	})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	fake.objects[session.Key] = sourceBytes
	imageBytes := testPNG(t)
	thumbnail, err := engine.CreateThumbnailUpload(ctx, "ch_demo", session.VideoID, "thumb.png", "image/png", int64(len(imageBytes)))
	if err != nil {
		t.Fatalf("create custom thumbnail: %v", err)
	}
	fake.objects[thumbnail.ObjectKey] = imageBytes
	if fake.sizes == nil {
		fake.sizes = make(map[string]int64)
	}
	fake.sizes[thumbnail.ObjectKey] = int64(len(imageBytes))
	if err := engine.CompleteThumbnailUpload(ctx, "ch_demo", session.VideoID, thumbnail.ObjectKey); err != nil {
		t.Fatalf("complete custom thumbnail: %v", err)
	}
	subtitleBytes := []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n")
	subtitle, err := engine.CreateSubtitleUpload(ctx, "ch_demo", session.VideoID, "en", "English", "subtitles", "en.vtt", "text/vtt", int64(len(subtitleBytes)))
	if err != nil {
		t.Fatalf("create subtitle: %v", err)
	}
	fake.objects[subtitle.Track.ObjectKey] = subtitleBytes
	if fake.sizes == nil {
		fake.sizes = make(map[string]int64)
	}
	if fake.contentTypes == nil {
		fake.contentTypes = make(map[string]string)
	}
	fake.sizes[subtitle.Track.ObjectKey] = int64(len(subtitleBytes))
	fake.contentTypes[subtitle.Track.ObjectKey] = "text/vtt"
	if _, err := engine.CompleteSubtitleUpload(ctx, "ch_demo", session.VideoID, subtitle.Track.ID); err != nil {
		t.Fatalf("complete subtitle: %v", err)
	}
	if _, err := engine.CompleteUpload(ctx, "ch_demo", session.UploadID, []CompletedPart{
		{PartNumber: 1, ETag: "etag-1"},
	}, ""); err != nil {
		t.Fatalf("complete upload: %v", err)
	}

	job, ok, err := st.ClaimMediaJob(ctx, "worker", time.Now().UTC(), time.Hour)
	if err != nil || !ok || job.Kind != "process" {
		t.Fatalf("claim process: ok=%v job=%+v err=%v", ok, job, err)
	}
	engine.runJob(ctx, job)

	upload, err := st.GetUpload(ctx, "ch_demo", session.UploadID)
	if err != nil {
		t.Fatalf("get upload: %v", err)
	}
	if upload.Status != "ready" {
		t.Fatalf("unexpected upload status: %q error=%q", upload.Status, upload.Error)
	}
	video, err := st.GetStudioVideo(ctx, "ch_demo", session.VideoID)
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if video.Status != "published" || video.Playback.MP4URL == "" || video.Playback.HLSURL == "" || video.Playback.ThumbnailURL == "" {
		t.Fatalf("unexpected processed video: %+v", video)
	}
	if !strings.Contains(video.Playback.MP4URL, "/api/v1/media/") || !strings.Contains(video.Playback.HLSURL, "/hls/master.m3u8") {
		t.Fatalf("unexpected playback URLs: %+v", video.Playback)
	}
	if video.Policy == nil || video.Policy.ScanVerdict != VerdictAllow {
		t.Fatalf("unexpected policy: %+v", video.Policy)
	}
	if !strings.HasPrefix(video.ContentHash, "sha256:") {
		t.Fatalf("unexpected content hash: %q", video.ContentHash)
	}
	for _, kind := range []string{"mp4", "hls", "thumbnail"} {
		if _, found, err := st.GetMediaAsset(ctx, video.ID, kind); err != nil || !found {
			t.Fatalf("missing asset %s: found=%v err=%v", kind, found, err)
		}
	}
	if _, found, err := st.GetMediaAsset(ctx, video.ID, "source"); err != nil || found {
		t.Fatalf("source asset should be removed after processing: found=%v err=%v", found, err)
	}
	if fake.deleted != 1 {
		t.Fatalf("expected the source object to be deleted once, got %d", fake.deleted)
	}
	if fake.getsByKey[session.Key] != 1 {
		t.Fatalf("expected one source download, got %d", fake.getsByKey[session.Key])
	}
	thumbnailAsset, found, err := st.GetMediaAsset(ctx, video.ID, "thumbnail")
	if err != nil || !found {
		t.Fatalf("get thumbnail asset: found=%v err=%v", found, err)
	}
	if thumbnailAsset.ObjectKey != thumbnail.ObjectKey {
		t.Fatalf("custom thumbnail was overwritten: %q", thumbnailAsset.ObjectKey)
	}
	if !strings.Contains(video.Playback.CaptionsURL, "/subtitles/"+subtitle.Track.ID+".vtt") {
		t.Fatalf("custom subtitle was overwritten: %q", video.Playback.CaptionsURL)
	}
	if len(fake.objects) < 5 {
		t.Fatalf("expected uploaded HLS and media objects, got %d", len(fake.objects))
	}
}

func TestEngineSubtitleLifecycle(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fake := &fakeStorage{size: 128, contentType: "text/vtt"}
	engine := NewWithStorage(st, fake, testMediaConfig(t))
	ctx := context.Background()

	upload, err := engine.CreateSubtitleUpload(ctx, "ch_demo", "vid_draft_001", "en", "English", "subtitles", "english.vtt", "text/vtt", 128)
	if err != nil {
		t.Fatalf("create subtitle: %v", err)
	}
	if upload.Track.Status != "pending" || upload.UploadURL == "" {
		t.Fatalf("unexpected subtitle upload: %+v", upload)
	}
	track, err := engine.CompleteSubtitleUpload(ctx, "ch_demo", "vid_draft_001", upload.Track.ID)
	if err != nil {
		t.Fatalf("complete subtitle: %v", err)
	}
	if track.Status != "ready" {
		t.Fatalf("unexpected subtitle status: %q", track.Status)
	}
	video, err := st.GetStudioVideo(ctx, "ch_demo", "vid_draft_001")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if !strings.Contains(video.Playback.CaptionsURL, "/subtitles/"+track.ID+".vtt") {
		t.Fatalf("unexpected captions URL: %q", video.Playback.CaptionsURL)
	}
	if err := engine.DeleteSubtitle(ctx, "ch_demo", "vid_draft_001", track.ID); err != nil {
		t.Fatalf("delete subtitle: %v", err)
	}
	if fake.deleted != 1 {
		t.Fatalf("expected one deleted object, got %d", fake.deleted)
	}
	tracks, err := st.ListSubtitles(ctx, "ch_demo", "vid_draft_001")
	if err != nil {
		t.Fatalf("list subtitles: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("expected no subtitles, got %+v", tracks)
	}
	video, err = st.GetStudioVideo(ctx, "ch_demo", "vid_draft_001")
	if err != nil {
		t.Fatalf("get video after delete: %v", err)
	}
	if video.Playback.CaptionsURL != "" {
		t.Fatalf("expected cleared captions URL, got %q", video.Playback.CaptionsURL)
	}
}

func TestEngineCustomThumbnailLifecycle(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	if err := seed.Run(context.Background(), st, "http://localhost:3000/media/ksKCMmvOxwQ.mp4", "http://localhost:3000", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	imageBytes := testPNG(t)
	fake := &fakeStorage{size: int64(len(imageBytes)), contentType: "image/png", objects: make(map[string][]byte)}
	engine := NewWithStorage(st, fake, testMediaConfig(t))
	ctx := context.Background()

	upload, err := engine.CreateThumbnailUpload(ctx, "ch_demo", "vid_draft_001", "thumb.png", "image/png", int64(len(imageBytes)))
	if err != nil {
		t.Fatalf("create thumbnail: %v", err)
	}
	fake.objects[upload.ObjectKey] = imageBytes
	if err := engine.CompleteThumbnailUpload(ctx, "ch_demo", "vid_draft_001", upload.ObjectKey); err != nil {
		t.Fatalf("complete thumbnail: %v", err)
	}
	asset, found, err := st.GetMediaAsset(ctx, "vid_draft_001", "thumbnail")
	if err != nil || !found {
		t.Fatalf("get thumbnail asset: found=%v err=%v", found, err)
	}
	if asset.ObjectKey != upload.ObjectKey {
		t.Fatalf("unexpected thumbnail asset: %+v", asset)
	}
	video, err := st.GetStudioVideo(ctx, "ch_demo", "vid_draft_001")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if !strings.Contains(video.Playback.ThumbnailURL, "/thumbnail.jpg") {
		t.Fatalf("unexpected thumbnail URL: %q", video.Playback.ThumbnailURL)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
