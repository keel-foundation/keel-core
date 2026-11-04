package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

var errSizeMismatch = errors.New("uploaded object size does not match the declared size")

type CreateUploadInput struct {
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Visibility        string   `json:"visibility"`
	Category          string   `json:"category"`
	License           string   `json:"license"`
	Format            string   `json:"format"`
	Tags              []string `json:"tags"`
	Filename          string   `json:"filename"`
	ContentType       string   `json:"content_type"`
	SizeBytes         int64    `json:"size_bytes"`
	ContentSHA256     string   `json:"content_sha256"`
	RightsDeclaration string   `json:"rights_declaration"`
	RightsHolder      string   `json:"rights_holder"`
	RightsNotes       string   `json:"rights_notes"`
}

type PresignedPart struct {
	PartNumber int32  `json:"part_number"`
	URL        string `json:"url"`
}

type UploadSession struct {
	UploadID       string          `json:"upload_id"`
	VideoID        string          `json:"video_id"`
	Video          store.Video     `json:"video"`
	Bucket         string          `json:"bucket"`
	Key            string          `json:"key"`
	S3UploadID     string          `json:"s3_upload_id"`
	PartSize       int64           `json:"part_size"`
	Parts          []PresignedPart `json:"parts"`
	PartsExpiresAt time.Time       `json:"parts_expires_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
}

type SubtitleUpload struct {
	Track     store.SubtitleTrack `json:"track"`
	UploadURL string              `json:"upload_url"`
	ExpiresAt time.Time           `json:"expires_at"`
}

type ThumbnailUpload struct {
	ObjectKey string    `json:"object_key"`
	UploadURL string    `json:"upload_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

const (
	maxSubtitleBytes  = 5 << 20
	maxThumbnailBytes = 10 << 20
)

type Engine struct {
	store      *store.Store
	storage    Storage
	cfg        Config
	scanner    Scanner
	processor  Processor
	OnPublish  func(context.Context, string) error
	OnFailure  func(context.Context, string, string) error
	OnScan     func(context.Context, store.Video, string, string, []Finding, string) error
	workerName string
}

func New(ctx context.Context, st *store.Store, cfg Config) (*Engine, error) {
	if !cfg.Enabled() {
		return nil, ErrDisabled
	}
	storage, err := NewS3Storage(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewWithStorage(st, storage, cfg), nil
}

func NewWithStorage(st *store.Store, storage Storage, cfg Config) *Engine {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = time.Hour
	}
	if cfg.JobLease <= 0 {
		cfg.JobLease = 30 * time.Minute
	}
	if cfg.PresignTTL <= 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.UploadTTL <= cfg.PresignTTL {
		cfg.UploadTTL = cfg.PresignTTL + time.Hour
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = os.TempDir()
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "media"
	}
	scanner := CompositeScanner{Scanners: []Scanner{
		MetadataScanner{Terms: cfg.PolicyTerms},
		HashBlocklistScanner{Store: st},
		DuplicateContentScanner{Store: st},
		HTTPPolicyScanner{
			URL:     cfg.ScannerURL,
			Token:   cfg.ScannerToken,
			Timeout: cfg.ScannerTimeout,
		},
	}}
	return &Engine{
		store:   st,
		storage: storage,
		cfg:     cfg,
		scanner: scanner,
		processor: Processor{
			FFmpeg:             cfg.FFmpegPath,
			FFprobe:            cfg.FFprobePath,
			WorkDir:            cfg.WorkDir,
			MaxRenditionHeight: cfg.MaxRenditionHeight,
			SegmentSeconds:     cfg.HLSSegmentSeconds,
		},
		workerName: "media-worker",
	}
}

func (e *Engine) CreateUpload(ctx context.Context, channelID, actorID string, input CreateUploadInput) (UploadSession, error) {
	if !e.cfg.Enabled() {
		return UploadSession{}, ErrDisabled
	}
	if err := e.validateCreateUpload(input); err != nil {
		return UploadSession{}, err
	}
	quickInput := ScanInput{
		ChannelID:     channelID,
		Title:         input.Title,
		Description:   input.Description,
		Tags:          input.Tags,
		Filename:      input.Filename,
		ContentType:   input.ContentType,
		SizeBytes:     input.SizeBytes,
		ContentSHA256: strings.ToLower(strings.TrimSpace(input.ContentSHA256)),
	}
	metadataResult, err := MetadataScanner{Terms: e.cfg.PolicyTerms}.Scan(ctx, quickInput)
	if err != nil {
		return UploadSession{}, err
	}
	if metadataResult.Verdict == VerdictBlock {
		return UploadSession{}, &PolicyError{Findings: metadataResult.Findings}
	}
	if quickInput.ContentSHA256 != "" {
		blockResult, err := HashBlocklistScanner{Store: e.store}.Scan(ctx, quickInput)
		if err != nil {
			return UploadSession{}, err
		}
		if blockResult.Verdict == VerdictBlock {
			return UploadSession{}, &PolicyError{Findings: blockResult.Findings}
		}
	}
	videoID, err := store.NewID("vid_")
	if err != nil {
		return UploadSession{}, err
	}
	uploadID, err := store.NewID("upl_")
	if err != nil {
		return UploadSession{}, err
	}
	now := time.Now().UTC()
	extension := strings.ToLower(filepath.Ext(input.Filename))
	objectKey := strings.Trim(e.cfg.KeyPrefix, "/") + "/" + channelID + "/" + videoID + "/source" + extension
	s3UploadID, err := e.storage.CreateMultipartUpload(ctx, objectKey, normalizeContentType(input.ContentType))
	if err != nil {
		return UploadSession{}, err
	}
	video := store.Video{
		ID:          videoID,
		ChannelID:   channelID,
		Title:       strings.TrimSpace(input.Title),
		Description: strings.TrimSpace(input.Description),
		Status:      "processing",
		Visibility:  input.Visibility,
		Playback:    store.Playback{},
		License:     input.License,
		Policy: &store.Policy{
			PolicyVersion:     "studio-0.1",
			UploadStatus:      "uploading",
			RightsDeclaration: input.RightsDeclaration,
			RightsHolder:      input.RightsHolder,
			RightsNotes:       input.RightsNotes,
		},
		Format:    input.Format,
		Category:  input.Category,
		Tags:      input.Tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	upload := store.UploadIntent{
		ID:            uploadID,
		ChannelID:     channelID,
		ActorID:       actorID,
		S3UploadID:    s3UploadID,
		Bucket:        e.cfg.Bucket,
		ObjectKey:     objectKey,
		Filename:      filepath.Base(input.Filename),
		ContentType:   normalizeContentType(input.ContentType),
		SizeBytes:     input.SizeBytes,
		PartSize:      e.cfg.PartSize,
		ContentSHA256: strings.ToLower(strings.TrimSpace(input.ContentSHA256)),
		Status:        "pending",
		ExpiresAt:     now.Add(e.cfg.UploadTTL),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	createdUpload, err := e.store.CreateUpload(ctx, video, upload)
	if err != nil {
		e.abortMultipart(objectKey, s3UploadID)
		return UploadSession{}, err
	}
	createdVideo, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		e.abortMultipart(objectKey, s3UploadID)
		e.failUpload(createdUpload.ID, videoID, "load created video")
		return UploadSession{}, err
	}
	parts, err := e.presignParts(ctx, createdUpload)
	if err != nil {
		e.abortMultipart(objectKey, s3UploadID)
		e.failUpload(createdUpload.ID, createdVideo.ID, "presign upload parts")
		return UploadSession{}, err
	}
	return UploadSession{
		UploadID:       createdUpload.ID,
		VideoID:        videoID,
		Video:          createdVideo,
		Bucket:         createdUpload.Bucket,
		Key:            createdUpload.ObjectKey,
		S3UploadID:     createdUpload.S3UploadID,
		PartSize:       createdUpload.PartSize,
		Parts:          parts,
		PartsExpiresAt: now.Add(e.cfg.PresignTTL),
		ExpiresAt:      createdUpload.ExpiresAt,
	}, nil
}

func (e *Engine) validateCreateUpload(input CreateUploadInput) error {
	if len([]rune(strings.TrimSpace(input.Title))) == 0 || len([]rune(strings.TrimSpace(input.Title))) > 200 {
		return fmt.Errorf("%w: title must be between 1 and 200 characters", ErrInvalidUpload)
	}
	if len([]rune(input.Description)) > 5000 {
		return fmt.Errorf("%w: description is too long", ErrInvalidUpload)
	}
	if !store.ValidVisibility(input.Visibility) {
		return fmt.Errorf("%w: invalid visibility", ErrInvalidUpload)
	}
	if !store.ValidCategory(input.Category) {
		return fmt.Errorf("%w: invalid category", ErrInvalidUpload)
	}
	if !store.ValidLicense(input.License) {
		return fmt.Errorf("%w: invalid license", ErrInvalidUpload)
	}
	if !store.ValidFormat(input.Format) {
		return fmt.Errorf("%w: invalid format", ErrInvalidUpload)
	}
	if len(input.Tags) > 20 {
		return fmt.Errorf("%w: too many tags", ErrInvalidUpload)
	}
	for _, tag := range input.Tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" || len([]rune(trimmed)) > 50 {
			return fmt.Errorf("%w: each tag must be between 1 and 50 characters", ErrInvalidUpload)
		}
	}
	if input.SizeBytes <= 0 || input.SizeBytes > e.cfg.MaxUploadBytes {
		return fmt.Errorf("%w: file size is outside the allowed range", ErrInvalidUpload)
	}
	if input.Filename == "" || len([]rune(input.Filename)) > 255 ||
		strings.ContainsAny(input.Filename, "\x00\r\n/\\") {
		return fmt.Errorf("%w: invalid filename", ErrInvalidUpload)
	}
	extension := strings.ToLower(filepath.Ext(input.Filename))
	if !e.cfg.AllowedExtensions[extension] {
		return fmt.Errorf("%w: unsupported file extension", ErrInvalidUpload)
	}
	contentType := normalizeContentType(input.ContentType)
	if contentType != "" && !e.cfg.AllowedContentTypes[contentType] {
		return fmt.Errorf("%w: unsupported content type", ErrInvalidUpload)
	}
	if input.RightsDeclaration != "" && !validRightsDeclaration(input.RightsDeclaration) {
		return fmt.Errorf("%w: invalid rights declaration", ErrInvalidUpload)
	}
	if len([]rune(input.RightsHolder)) > 200 || len([]rune(input.RightsNotes)) > 2000 {
		return fmt.Errorf("%w: rights metadata is too long", ErrInvalidUpload)
	}
	if input.ContentSHA256 != "" {
		decoded, err := hex.DecodeString(strings.ToLower(input.ContentSHA256))
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("%w: content_sha256 must be a hex encoded SHA-256 digest", ErrInvalidUpload)
		}
	}
	return nil
}

func (e *Engine) presignParts(ctx context.Context, upload store.UploadIntent) ([]PresignedPart, error) {
	count := int((upload.SizeBytes + upload.PartSize - 1) / upload.PartSize)
	if count < 1 || count > 10000 {
		return nil, fmt.Errorf("%w: invalid part count", ErrInvalidUpload)
	}
	parts := make([]PresignedPart, 0, count)
	for partNumber := 1; partNumber <= count; partNumber++ {
		url, err := e.storage.PresignUploadPart(ctx, upload.ObjectKey, upload.S3UploadID, int32(partNumber), e.cfg.PresignTTL)
		if err != nil {
			return nil, err
		}
		parts = append(parts, PresignedPart{PartNumber: int32(partNumber), URL: url})
	}
	return parts, nil
}

func (e *Engine) RefreshUploadParts(ctx context.Context, channelID, uploadID string, partNumbers []int32) ([]PresignedPart, time.Time, error) {
	if !e.cfg.Enabled() {
		return nil, time.Time{}, ErrDisabled
	}
	upload, err := e.store.GetUpload(ctx, channelID, uploadID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if upload.Status != "pending" && upload.Status != "uploading" {
		return nil, time.Time{}, fmt.Errorf("%w: upload is %s", ErrInvalidUpload, upload.Status)
	}
	now := time.Now().UTC()
	if now.After(upload.ExpiresAt) {
		return nil, time.Time{}, fmt.Errorf("%w: upload session expired", ErrInvalidUpload)
	}
	count := int((upload.SizeBytes + upload.PartSize - 1) / upload.PartSize)
	if len(partNumbers) == 0 {
		partNumbers = make([]int32, 0, count)
		for partNumber := 1; partNumber <= count; partNumber++ {
			partNumbers = append(partNumbers, int32(partNumber))
		}
	}
	seen := make(map[int32]bool, len(partNumbers))
	parts := make([]PresignedPart, 0, len(partNumbers))
	for _, partNumber := range partNumbers {
		if partNumber < 1 || partNumber > int32(count) {
			return nil, time.Time{}, fmt.Errorf("%w: invalid part number", ErrInvalidUpload)
		}
		if seen[partNumber] {
			continue
		}
		seen[partNumber] = true
		url, err := e.storage.PresignUploadPart(ctx, upload.ObjectKey, upload.S3UploadID, partNumber, e.cfg.PresignTTL)
		if err != nil {
			return nil, time.Time{}, err
		}
		parts = append(parts, PresignedPart{PartNumber: partNumber, URL: url})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	return parts, now.Add(e.cfg.PresignTTL), nil
}

func (e *Engine) CompleteUpload(ctx context.Context, channelID, uploadID string, parts []CompletedPart, contentSHA string) (store.UploadIntent, error) {
	if !e.cfg.Enabled() {
		return store.UploadIntent{}, ErrDisabled
	}
	upload, err := e.store.GetUpload(ctx, channelID, uploadID)
	if err != nil {
		return store.UploadIntent{}, err
	}
	switch upload.Status {
	case "pending", "uploading":
	case "scanning", "processing", "ready", "review", "blocked":
		return upload, nil
	default:
		return store.UploadIntent{}, fmt.Errorf("%w: upload is %s", ErrInvalidUpload, upload.Status)
	}
	now := time.Now().UTC()
	if now.After(upload.ExpiresAt) {
		_, _ = e.store.AbortUpload(ctx, channelID, uploadID, "upload session expired", now)
		e.abortMultipart(upload.ObjectKey, upload.S3UploadID)
		return store.UploadIntent{}, fmt.Errorf("%w: upload session expired", ErrInvalidUpload)
	}
	if err := validateCompletedParts(parts, upload.SizeBytes, upload.PartSize); err != nil {
		return store.UploadIntent{}, err
	}
	if contentSHA == "" {
		contentSHA = upload.ContentSHA256
	} else if upload.ContentSHA256 != "" && strings.ToLower(contentSHA) != upload.ContentSHA256 {
		return store.UploadIntent{}, fmt.Errorf("%w: content_sha256 does not match the upload intent", ErrInvalidUpload)
	}
	if contentSHA != "" {
		decoded, err := hex.DecodeString(strings.ToLower(contentSHA))
		if err != nil || len(decoded) != sha256.Size {
			return store.UploadIntent{}, fmt.Errorf("%w: invalid content_sha256", ErrInvalidUpload)
		}
		contentSHA = strings.ToLower(contentSHA)
	}
	if err := e.storage.CompleteMultipartUpload(ctx, upload.ObjectKey, upload.S3UploadID, parts); err != nil {
		info, headErr := e.storage.HeadObject(ctx, upload.ObjectKey)
		if headErr != nil || info.Size != upload.SizeBytes {
			return store.UploadIntent{}, err
		}
	}
	info, err := e.storage.HeadObject(ctx, upload.ObjectKey)
	if err != nil {
		return store.UploadIntent{}, err
	}
	if info.Size != upload.SizeBytes {
		e.failUpload(upload.ID, upload.VideoID, "uploaded object size mismatch")
		return store.UploadIntent{}, fmt.Errorf("%w: %v", ErrInvalidUpload, errSizeMismatch)
	}
	return e.store.MarkUploadCompleted(ctx, upload.ID, contentSHA, now)
}

func validateCompletedParts(parts []CompletedPart, sizeBytes, partSize int64) error {
	expected := int((sizeBytes + partSize - 1) / partSize)
	if len(parts) != expected {
		return fmt.Errorf("%w: expected %d parts, got %d", ErrInvalidUpload, expected, len(parts))
	}
	seen := make(map[int32]bool, len(parts))
	for _, part := range parts {
		if part.PartNumber < 1 || part.PartNumber > int32(expected) {
			return fmt.Errorf("%w: invalid part number", ErrInvalidUpload)
		}
		if part.ETag == "" || len(part.ETag) > 128 {
			return fmt.Errorf("%w: invalid part etag", ErrInvalidUpload)
		}
		if seen[part.PartNumber] {
			return fmt.Errorf("%w: duplicate part number", ErrInvalidUpload)
		}
		seen[part.PartNumber] = true
	}
	return nil
}

func (e *Engine) AbortUpload(ctx context.Context, channelID, uploadID string) error {
	upload, err := e.store.GetUpload(ctx, channelID, uploadID)
	if err != nil {
		return err
	}
	if upload.Status != "pending" && upload.Status != "uploading" {
		return fmt.Errorf("%w: upload is %s", ErrInvalidUpload, upload.Status)
	}
	if err := e.storage.AbortMultipartUpload(ctx, upload.ObjectKey, upload.S3UploadID); err != nil {
		return err
	}
	_, err = e.store.AbortUpload(ctx, channelID, uploadID, "upload aborted by creator", time.Now().UTC())
	return err
}

func (e *Engine) CreateSubtitleUpload(ctx context.Context, channelID, videoID, language, label, kind, filename, contentType string, sizeBytes int64) (SubtitleUpload, error) {
	if !e.cfg.Enabled() {
		return SubtitleUpload{}, ErrDisabled
	}
	video, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		return SubtitleUpload{}, err
	}
	if err := validateSubtitleUpload(language, label, kind, filename, contentType, sizeBytes); err != nil {
		return SubtitleUpload{}, err
	}
	trackID, err := store.NewID("sub_")
	if err != nil {
		return SubtitleUpload{}, err
	}
	now := time.Now().UTC()
	objectKey := e.videoBaseKey(video.ChannelID, video.ID) + "/subtitles/" + trackID + ".vtt"
	track, err := e.store.CreateSubtitleTrack(ctx, store.SubtitleTrack{
		ID:        trackID,
		VideoID:   video.ID,
		ChannelID: video.ChannelID,
		Language:  strings.TrimSpace(language),
		Label:     strings.TrimSpace(label),
		Kind:      kind,
		ObjectKey: objectKey,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return SubtitleUpload{}, err
	}
	uploadURL, err := e.storage.PresignPutObject(ctx, objectKey, "text/vtt", e.cfg.PresignTTL)
	if err != nil {
		_, _ = e.store.DeleteSubtitleTrack(ctx, channelID, video.ID, trackID)
		return SubtitleUpload{}, err
	}
	return SubtitleUpload{Track: track, UploadURL: uploadURL, ExpiresAt: now.Add(e.cfg.PresignTTL)}, nil
}

func (e *Engine) CompleteSubtitleUpload(ctx context.Context, channelID, videoID, trackID string) (store.SubtitleTrack, error) {
	if !e.cfg.Enabled() {
		return store.SubtitleTrack{}, ErrDisabled
	}
	video, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		return store.SubtitleTrack{}, err
	}
	track, err := e.store.GetSubtitleTrack(ctx, video.ID, trackID)
	if err != nil {
		return store.SubtitleTrack{}, err
	}
	if track.Status == "ready" {
		if err := e.setCaptionsURL(ctx, video, track, time.Now().UTC()); err != nil {
			return store.SubtitleTrack{}, err
		}
		return track, nil
	}
	info, err := e.storage.HeadObject(ctx, track.ObjectKey)
	if err != nil {
		return store.SubtitleTrack{}, err
	}
	if info.Size <= 0 || info.Size > maxSubtitleBytes {
		return store.SubtitleTrack{}, fmt.Errorf("%w: subtitle file size is outside the allowed range", ErrInvalidUpload)
	}
	if info.ContentType != "" && !strings.HasPrefix(strings.ToLower(info.ContentType), "text/") {
		return store.SubtitleTrack{}, fmt.Errorf("%w: uploaded subtitle is not a text file", ErrInvalidUpload)
	}
	now := time.Now().UTC()
	track, err = e.store.CompleteSubtitleTrack(ctx, channelID, video.ID, trackID, now)
	if err != nil {
		return store.SubtitleTrack{}, err
	}
	if err := e.setCaptionsURL(ctx, video, track, now); err != nil {
		return store.SubtitleTrack{}, err
	}
	return track, nil
}

func (e *Engine) DeleteSubtitle(ctx context.Context, channelID, videoID, trackID string) error {
	if !e.cfg.Enabled() {
		return ErrDisabled
	}
	video, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		return err
	}
	track, err := e.store.GetSubtitleTrack(ctx, video.ID, trackID)
	if err != nil {
		return err
	}
	if _, err := e.store.DeleteSubtitleTrack(ctx, channelID, video.ID, trackID); err != nil {
		return err
	}
	if err := e.storage.DeleteObject(ctx, track.ObjectKey); err != nil {
		log.Printf("media delete subtitle object %s: %v", track.ObjectKey, err)
	}
	tracks, err := e.store.ListSubtitles(ctx, channelID, video.ID)
	if err != nil {
		return err
	}
	captionsURL := video.Playback.CaptionsURL
	if captionsURL == "" || strings.Contains(captionsURL, "/subtitles/"+track.ID+".vtt") {
		captionsURL = ""
		for _, candidate := range tracks {
			if candidate.Status == "ready" {
				captionsURL = e.mediaURL(video, "subtitles/"+candidate.ID+".vtt")
				break
			}
		}
		if captionsURL == "" {
			if asset, found, err := e.store.GetMediaAsset(ctx, video.ID, "caption"); err == nil && found && asset.ObjectKey != "" {
				captionsURL = e.mediaURL(video, "captions.vtt")
			}
		}
	}
	return e.store.SetVideoCaptionsURL(ctx, video.ID, captionsURL, time.Now().UTC())
}

func (e *Engine) setCaptionsURL(ctx context.Context, video store.Video, track store.SubtitleTrack, at time.Time) error {
	return e.store.SetVideoCaptionsURL(ctx, video.ID, e.mediaURL(video, "subtitles/"+track.ID+".vtt"), at)
}

func (e *Engine) videoBaseKey(channelID, videoID string) string {
	return strings.Trim(e.cfg.KeyPrefix, "/") + "/" + channelID + "/" + videoID
}

func (e *Engine) mediaURL(video store.Video, suffix string) string {
	publicID := video.PublicID
	if publicID == "" {
		publicID = video.ID
	}
	return e.cfg.PublicURL + "/api/v1/media/" + publicID + "/" + strings.TrimPrefix(suffix, "/")
}

func validateSubtitleUpload(language, label, kind, filename, contentType string, sizeBytes int64) error {
	if !validLanguageTag(language) {
		return fmt.Errorf("%w: language must be a valid language tag", ErrInvalidUpload)
	}
	if trimmed := strings.TrimSpace(label); trimmed == "" || len([]rune(trimmed)) > 100 {
		return fmt.Errorf("%w: label must be between 1 and 100 characters", ErrInvalidUpload)
	}
	if kind != "subtitles" && kind != "captions" {
		return fmt.Errorf("%w: kind must be subtitles or captions", ErrInvalidUpload)
	}
	if filename == "" || len([]rune(filename)) > 255 || strings.ContainsAny(filename, "\x00\r\n/\\") || strings.ToLower(filepath.Ext(filename)) != ".vtt" {
		return fmt.Errorf("%w: subtitle filename must be a .vtt file", ErrInvalidUpload)
	}
	contentType = normalizeContentType(contentType)
	if contentType != "" && contentType != "text/vtt" && contentType != "text/plain" {
		return fmt.Errorf("%w: unsupported subtitle content type", ErrInvalidUpload)
	}
	if sizeBytes <= 0 || sizeBytes > maxSubtitleBytes {
		return fmt.Errorf("%w: subtitle file size is outside the allowed range", ErrInvalidUpload)
	}
	return nil
}

func validRightsDeclaration(value string) bool {
	switch value {
	case "original", "licensed", "public_domain", "fair_use":
		return true
	default:
		return false
	}
}

func validLanguageTag(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 35 {
		return false
	}
	parts := strings.Split(value, "-")
	if len(parts[0]) < 2 || len(parts[0]) > 3 {
		return false
	}
	for _, r := range parts[0] {
		if !isASCIILetter(r) {
			return false
		}
	}
	for _, part := range parts[1:] {
		if len(part) < 2 || len(part) > 8 {
			return false
		}
		for _, r := range part {
			if !isASCIILetter(r) && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func (e *Engine) CreateThumbnailUpload(ctx context.Context, channelID, videoID, filename, contentType string, sizeBytes int64) (ThumbnailUpload, error) {
	if !e.cfg.Enabled() {
		return ThumbnailUpload{}, ErrDisabled
	}
	video, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		return ThumbnailUpload{}, err
	}
	if err := validateThumbnailUpload(filename, contentType, sizeBytes); err != nil {
		return ThumbnailUpload{}, err
	}
	extension := strings.ToLower(filepath.Ext(filename))
	objectKey := e.videoBaseKey(video.ChannelID, video.ID) + "/thumbnail-custom" + extension
	uploadURL, err := e.storage.PresignPutObject(ctx, objectKey, normalizeContentType(contentType), e.cfg.PresignTTL)
	if err != nil {
		return ThumbnailUpload{}, err
	}
	return ThumbnailUpload{ObjectKey: objectKey, UploadURL: uploadURL, ExpiresAt: time.Now().UTC().Add(e.cfg.PresignTTL)}, nil
}

func (e *Engine) CompleteThumbnailUpload(ctx context.Context, channelID, videoID, objectKey string) error {
	if !e.cfg.Enabled() {
		return ErrDisabled
	}
	video, err := e.store.GetStudioVideo(ctx, channelID, videoID)
	if err != nil {
		return err
	}
	baseKey := e.videoBaseKey(video.ChannelID, video.ID)
	if !strings.HasPrefix(objectKey, baseKey+"/thumbnail-custom.") || strings.Contains(objectKey, "..") {
		return fmt.Errorf("%w: invalid thumbnail object key", ErrInvalidUpload)
	}
	info, err := e.storage.HeadObject(ctx, objectKey)
	if err != nil {
		return err
	}
	if info.Size <= 0 || info.Size > maxThumbnailBytes {
		return fmt.Errorf("%w: thumbnail file size is outside the allowed range", ErrInvalidUpload)
	}
	body, err := e.storage.GetObject(ctx, objectKey)
	if err != nil {
		return err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxThumbnailBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxThumbnailBytes {
		return fmt.Errorf("%w: thumbnail file size is outside the allowed range", ErrInvalidUpload)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: thumbnail is not a valid JPEG or PNG image", ErrInvalidUpload)
	}
	extension := strings.ToLower(filepath.Ext(objectKey))
	if (extension == ".png" && format != "png") || ((extension == ".jpg" || extension == ".jpeg") && format != "jpeg") {
		return fmt.Errorf("%w: thumbnail file type does not match its extension", ErrInvalidUpload)
	}
	if config.Width < 16 || config.Height < 16 || config.Width > 10000 || config.Height > 10000 {
		return fmt.Errorf("%w: thumbnail dimensions are outside the allowed range", ErrInvalidUpload)
	}
	now := time.Now().UTC()
	if err := e.store.UpsertMediaAsset(ctx, store.MediaAsset{
		VideoID:     video.ID,
		Kind:        "thumbnail",
		ObjectKey:   objectKey,
		ContentType: "image/" + format,
		SizeBytes:   info.Size,
		UpdatedAt:   now,
	}); err != nil {
		return err
	}
	return e.store.SetVideoThumbnailURL(ctx, video.ID, e.mediaURL(video, "thumbnail.jpg"), now)
}

func validateThumbnailUpload(filename, contentType string, sizeBytes int64) error {
	if filename == "" || len([]rune(filename)) > 255 || strings.ContainsAny(filename, "\x00\r\n/\\") {
		return fmt.Errorf("%w: invalid thumbnail filename", ErrInvalidUpload)
	}
	extension := strings.ToLower(filepath.Ext(filename))
	if extension != ".jpg" && extension != ".jpeg" && extension != ".png" {
		return fmt.Errorf("%w: thumbnail must be a JPEG or PNG file", ErrInvalidUpload)
	}
	contentType = normalizeContentType(contentType)
	if contentType != "" && contentType != "image/jpeg" && contentType != "image/png" {
		return fmt.Errorf("%w: unsupported thumbnail content type", ErrInvalidUpload)
	}
	if extension == ".png" && contentType != "" && contentType != "image/png" {
		return fmt.Errorf("%w: thumbnail content type does not match its extension", ErrInvalidUpload)
	}
	if (extension == ".jpg" || extension == ".jpeg") && contentType != "" && contentType != "image/jpeg" {
		return fmt.Errorf("%w: thumbnail content type does not match its extension", ErrInvalidUpload)
	}
	if sizeBytes <= 0 || sizeBytes > maxThumbnailBytes {
		return fmt.Errorf("%w: thumbnail file size is outside the allowed range", ErrInvalidUpload)
	}
	return nil
}

func (e *Engine) PresignGet(ctx context.Context, key string) (string, error) {
	if !e.cfg.Enabled() {
		return "", ErrDisabled
	}
	return e.storage.PresignGetObject(ctx, key, e.cfg.PresignTTL)
}

func (e *Engine) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if !e.cfg.Enabled() {
		return nil, ErrDisabled
	}
	return e.storage.GetObject(ctx, key)
}

func (e *Engine) Start(ctx context.Context) {
	if !e.cfg.Enabled() {
		return
	}
	for index := 0; index < e.cfg.Workers; index++ {
		go e.worker(ctx, fmt.Sprintf("%s-%d", e.workerName, index))
	}
	go e.sweeper(ctx)
}

func (e *Engine) sweeper(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.expireUploads(ctx)
		}
	}
}

func (e *Engine) worker(ctx context.Context, workerID string) {
	idle := 0
	for {
		if ctx.Err() != nil {
			return
		}
		job, ok, err := e.store.ClaimMediaJob(
			ctx, workerID, time.Now().UTC(), e.cfg.JobLease,
		)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("media worker %s: claim job: %v", workerID, err)
			}
			idle++
			if !sleepContext(ctx, e.workerBackoff(idle)) {
				return
			}
			continue
		}
		if !ok {
			idle++
			if !sleepContext(ctx, e.workerBackoff(idle)) {
				return
			}
			continue
		}
		idle = 0
		e.runJob(ctx, job)
	}
}

func (e *Engine) workerBackoff(idle int) time.Duration {
	base := e.cfg.PollInterval
	if base <= 0 {
		base = 2 * time.Second
	}
	maximum := 30 * time.Second
	if idle < 1 {
		idle = 1
	}
	shift := idle - 1
	if shift > 5 {
		shift = 5
	}
	delay := base * time.Duration(1<<uint(shift))
	if delay > maximum {
		delay = maximum
	}
	if delay <= 0 {
		return base
	}
	jitter := time.Duration(rand.Int63n(int64(delay/4) + 1))
	if delay+jitter > maximum {
		return maximum
	}
	return delay + jitter
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Engine) failUpload(uploadID, videoID, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	_ = e.store.UpdateUploadStatus(ctx, uploadID, "failed", message, "", "", now)
	_ = e.store.SetVideoStatus(ctx, videoID, "draft", now)
	if e.OnFailure != nil {
		go func(videoID, message string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := e.OnFailure(ctx, videoID, message); err != nil {
				log.Printf("media failure callback for %s: %v", videoID, err)
			}
		}(videoID, message)
	}
}

func (e *Engine) abortMultipart(key, uploadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = e.storage.AbortMultipartUpload(ctx, key, uploadID)
}

func (e *Engine) expireUploads(ctx context.Context) {
	now := time.Now().UTC()
	uploads, err := e.store.ListExpiredUploads(ctx, now, 25)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("media expire uploads: %v", err)
		}
		return
	}
	for _, upload := range uploads {
		e.abortMultipart(upload.ObjectKey, upload.S3UploadID)
		if _, err := e.store.AbortUpload(ctx, upload.ChannelID, upload.ID, "upload session expired", now); err != nil {
			log.Printf("media expire upload %s: %v", upload.ID, err)
		}
	}
}

func (e *Engine) runJob(parent context.Context, job store.MediaJob) {
	ctx, cancel := context.WithTimeout(parent, e.cfg.JobTimeout)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go e.heartbeatJob(job, cancel, heartbeatDone)
	defer close(heartbeatDone)
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprintf("media job panic: %v", recovered)
			e.retryJob(job, message, time.Now().UTC())
			log.Printf("media job %d (%s) panicked: %v", job.ID, job.Kind, recovered)
		}
	}()
	var err error
	switch job.Kind {
	case "scan", "transcode", "process":
		err = e.processJob(ctx, job)
	default:
		err = fmt.Errorf("unknown media job kind %q", job.Kind)
	}
	now := time.Now().UTC()
	if err != nil {
		e.retryJob(job, truncateError(err), now)
		log.Printf("media job %d (%s): %v", job.ID, job.Kind, err)
		return
	}
	bookkeeping, cancelBookkeeping := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelBookkeeping()
	if err := e.store.FinishMediaJob(bookkeeping, job.ID, job.LockedBy, job.Attempts, now); err != nil {
		log.Printf("media job %d: finish: %v", job.ID, err)
	}
}

func (e *Engine) heartbeatJob(
	job store.MediaJob,
	cancel context.CancelFunc,
	done <-chan struct{},
) {
	interval := e.cfg.JobLease / 3
	if interval <= 0 {
		interval = time.Minute
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancelHeartbeat := context.WithTimeout(context.Background(), 5*time.Second)
			ok, err := e.store.RenewMediaJobLease(
				ctx, job.ID, job.LockedBy, time.Now().UTC(),
			)
			cancelHeartbeat()
			if err != nil {
				log.Printf("media job %d: renew lease: %v", job.ID, err)
				continue
			}
			if !ok {
				log.Printf("media job %d: lease lost", job.ID)
				cancel()
				return
			}
		}
	}
}

func (e *Engine) retryJob(job store.MediaJob, message string, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := e.store.RetryMediaJob(
		ctx, job.ID, job.LockedBy, job.Attempts, message,
		now.Add(jobBackoff(job.Attempts)), now,
	)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("media job %d: retry: %v", job.ID, err)
		}
		return
	}
	if status == "failed" {
		e.failUpload(job.UploadID, job.VideoID, message)
	}
}

func jobBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 {
		attempts = 7
	}
	delay := time.Duration(1<<uint(attempts-1)) * 30 * time.Second
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	return delay
}

func (e *Engine) blockUpload(ctx context.Context, upload store.UploadIntent, video store.Video, scanner string, findings []Finding, digest string, size int64) error {
	findingsJSON, err := json.Marshal(findings)
	if err != nil {
		return err
	}
	reason := findingsSummary(findings)
	e.reportScan(ctx, video, VerdictBlock, scanner, findings, reason)
	now := time.Now().UTC()
	if err := e.store.RecordMediaScan(ctx, store.MediaScan{
		UploadID:      upload.ID,
		VideoID:       video.ID,
		Scanner:       scanner,
		Verdict:       VerdictBlock,
		Findings:      findingsJSON,
		ContentSHA256: digest,
		SizeBytes:     size,
		CreatedAt:     now,
	}); err != nil {
		return err
	}
	if err := e.store.UpdateUploadStatus(ctx, upload.ID, "blocked", reason, VerdictBlock, reason, now); err != nil {
		return err
	}
	if err := e.store.UpdateVideoPolicy(ctx, video.ID, policyFor(video, "blocked", VerdictBlock, reason), now); err != nil {
		return err
	}
	return e.store.SetVideoStatus(ctx, video.ID, "draft", now)
}

// processJob downloads each source exactly once, then scans, probes, and
// transcodes it. Legacy scan/transcode jobs run through the same path so an
// in-flight job upgraded during deployment still avoids a second download.
func (e *Engine) processJob(ctx context.Context, job store.MediaJob) error {
	upload, err := e.store.GetUploadByID(ctx, job.UploadID)
	if err != nil {
		return err
	}
	video, err := e.store.GetVideo(ctx, upload.VideoID)
	if err != nil {
		return err
	}
	tempDir, sourcePath, digest, size, err := e.downloadSource(ctx, upload)
	defer func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err != nil {
		if errors.Is(err, errSizeMismatch) {
			return e.blockUpload(ctx, upload, video, "size_check", []Finding{{
				Code:     "size_mismatch",
				Message:  "The uploaded object does not match the declared size.",
				Severity: "critical",
				Kind:     "policy",
			}}, digest, size)
		}
		return err
	}
	if upload.ContentSHA256 != "" && upload.ContentSHA256 != digest {
		return e.blockUpload(ctx, upload, video, "hash_check", []Finding{{
			Code:     "hash_mismatch",
			Message:  "The uploaded object does not match the declared content hash.",
			Severity: "critical",
			Kind:     "policy",
		}}, digest, size)
	}
	probe, err := e.processor.Probe(ctx, sourcePath)
	if err != nil {
		return err
	}
	findings := validateProbe(probe, e.cfg)
	if len(findings) > 0 {
		return e.blockUpload(ctx, upload, video, "media_probe", findings, digest, size)
	}
	input := ScanInput{
		VideoID:         video.ID,
		ChannelID:       video.ChannelID,
		Title:           video.Title,
		Description:     video.Description,
		Tags:            video.Tags,
		Filename:        upload.Filename,
		ContentType:     upload.ContentType,
		SizeBytes:       size,
		ContentSHA256:   digest,
		DurationSeconds: probe.DurationSeconds,
		Width:           probe.Width,
		Height:          probe.Height,
		VideoCodec:      probe.VideoCodec,
		AudioCodec:      probe.AudioCodec,
	}
	result, err := e.scanner.Scan(ctx, input)
	if err != nil {
		return err
	}
	if result.Verdict == VerdictBlock {
		return e.blockUpload(ctx, upload, video, result.Scanner, result.Findings, digest, size)
	}
	if result.Verdict == VerdictReview {
		e.reportScan(ctx, video, VerdictReview, result.Scanner, result.Findings, findingsSummary(result.Findings))
	}
	findingsJSON, err := json.Marshal(result.Findings)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := e.store.RecordMediaScan(ctx, store.MediaScan{
		UploadID:      upload.ID,
		VideoID:       video.ID,
		Scanner:       result.Scanner,
		Verdict:       result.Verdict,
		Findings:      findingsJSON,
		ContentSHA256: digest,
		SizeBytes:     size,
		CreatedAt:     now,
	}); err != nil {
		return err
	}
	reason := findingsSummary(result.Findings)
	if err := e.store.UpdateVideoPolicy(ctx, video.ID, policyFor(video, "processing", result.Verdict, reason), now); err != nil {
		return err
	}
	if err := e.store.UpdateUploadStatus(ctx, upload.ID, "processing", "", result.Verdict, reason, now); err != nil {
		return err
	}

	output, err := e.processor.Process(ctx, sourcePath, probe)
	if err != nil {
		return err
	}
	if output.MasterPath != "" {
		defer func() { _ = os.RemoveAll(filepath.Dir(output.MasterPath)) }()
	}
	now = time.Now().UTC()
	baseKey := e.videoBaseKey(video.ChannelID, video.ID)
	hlsPrefix := baseKey + "/hls/"
	if err := e.uploadProcessedOutput(ctx, baseKey, hlsPrefix, output); err != nil {
		return err
	}
	assets := []store.MediaAsset{
		{VideoID: video.ID, Kind: "source", ObjectKey: upload.ObjectKey, ContentType: upload.ContentType, SizeBytes: size, UpdatedAt: now},
		{VideoID: video.ID, Kind: "mp4", ObjectKey: baseKey + "/video.mp4", ContentType: "video/mp4", SizeBytes: fileSize(output.MP4Path), UpdatedAt: now},
		{VideoID: video.ID, Kind: "hls", ObjectKey: hlsPrefix, ContentType: "application/vnd.apple.mpegurl", UpdatedAt: now},
	}
	if existing, found, err := e.store.GetMediaAsset(ctx, video.ID, "thumbnail"); err != nil {
		return err
	} else if !found || !strings.Contains(existing.ObjectKey, "/thumbnail-custom.") {
		assets = append(assets, store.MediaAsset{
			VideoID: video.ID, Kind: "thumbnail", ObjectKey: baseKey + "/thumbnail.jpg",
			ContentType: "image/jpeg", SizeBytes: fileSize(output.ThumbnailPath), UpdatedAt: now,
		})
	}
	playback := store.Playback{
		MP4URL:       e.mediaURL(video, "video.mp4"),
		HLSURL:       e.mediaURL(video, "hls/master.m3u8"),
		ThumbnailURL: e.mediaURL(video, "thumbnail.jpg"),
	}
	if output.CaptionsPath != "" {
		assets = append(assets, store.MediaAsset{
			VideoID:     video.ID,
			Kind:        "caption",
			ObjectKey:   baseKey + "/captions.vtt",
			ContentType: "text/vtt",
			SizeBytes:   fileSize(output.CaptionsPath),
			UpdatedAt:   now,
		})
		playback.CaptionsURL = e.mediaURL(video, "captions.vtt")
	}
	for _, asset := range assets {
		if err := e.store.UpsertMediaAsset(ctx, asset); err != nil {
			return err
		}
	}
	publish := result.Verdict == VerdictAllow
	policy := policyFor(video, uploadStatus(publish, result.Verdict), result.Verdict, reason)
	if _, err := e.store.FinalizeVideoProcessing(
		ctx, video.ID, video.ChannelID, digest, output.DurationSeconds, playback, policy, publish, now,
	); err != nil {
		return err
	}
	status := "ready"
	if result.Verdict == VerdictReview {
		status = "review"
	}
	if err := e.store.UpdateUploadStatus(ctx, upload.ID, status, "", result.Verdict, reason, now); err != nil {
		return err
	}
	if !e.cfg.RetainSource && result.Verdict == VerdictAllow {
		if err := e.deleteSource(ctx, upload, video.ID); err != nil {
			log.Printf("media source cleanup for %s: %v", video.ID, err)
		}
	}
	if publish && e.OnPublish != nil {
		go func(videoID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := e.OnPublish(ctx, videoID); err != nil {
				log.Printf("media publish callback for %s: %v", videoID, err)
			}
		}(video.ID)
	}
	return nil
}

func (e *Engine) deleteSource(ctx context.Context, upload store.UploadIntent, videoID string) error {
	if err := e.storage.DeleteObject(ctx, upload.ObjectKey); err != nil {
		return err
	}
	return e.store.DeleteMediaAsset(ctx, videoID, "source")
}

func (e *Engine) uploadProcessedOutput(ctx context.Context, baseKey, hlsPrefix string, output ProcessedOutput) error {
	if err := uploadFile(ctx, e.storage, output.MasterPath, hlsPrefix+"master.m3u8", "application/vnd.apple.mpegurl"); err != nil {
		return err
	}
	names := make([]string, 0, len(output.VariantPaths))
	for name := range output.VariantPaths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variantPath := output.VariantPaths[name]
		if err := uploadFile(ctx, e.storage, variantPath, hlsPrefix+name+"/index.m3u8", "application/vnd.apple.mpegurl"); err != nil {
			return err
		}
		entries, err := os.ReadDir(filepath.Dir(variantPath))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ts") {
				continue
			}
			if err := uploadFile(ctx, e.storage, filepath.Join(filepath.Dir(variantPath), entry.Name()), hlsPrefix+name+"/"+entry.Name(), "video/mp2t"); err != nil {
				return err
			}
		}
	}
	if err := uploadFile(ctx, e.storage, output.MP4Path, baseKey+"/video.mp4", "video/mp4"); err != nil {
		return err
	}
	if err := uploadFile(ctx, e.storage, output.ThumbnailPath, baseKey+"/thumbnail.jpg", "image/jpeg"); err != nil {
		return err
	}
	if output.CaptionsPath != "" {
		if err := uploadFile(ctx, e.storage, output.CaptionsPath, baseKey+"/captions.vtt", "text/vtt"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) downloadSource(ctx context.Context, upload store.UploadIntent) (string, string, string, int64, error) {
	tempDir, err := os.MkdirTemp(e.cfg.WorkDir, "keeltube-source-*")
	if err != nil {
		return "", "", "", 0, err
	}
	path := filepath.Join(tempDir, "source"+strings.ToLower(filepath.Ext(upload.Filename)))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", 0, err
	}
	body, err := e.storage.GetObject(ctx, upload.ObjectKey)
	if err != nil {
		_ = file.Close()
		_ = os.RemoveAll(tempDir)
		return "", "", "", 0, err
	}
	hasher := sha256.New()
	limit := e.cfg.MaxUploadBytes + 1
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(body, limit))
	_ = body.Close()
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", 0, copyErr
	}
	if closeErr != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", 0, closeErr
	}
	if written > e.cfg.MaxUploadBytes {
		_ = os.RemoveAll(tempDir)
		return "", "", "", 0, fmt.Errorf("%w: source exceeds the maximum size", ErrInvalidUpload)
	}
	if upload.SizeBytes > 0 && written != upload.SizeBytes {
		return tempDir, path, hex.EncodeToString(hasher.Sum(nil)), written, errSizeMismatch
	}
	return tempDir, path, hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func validateProbe(probe ProbeResult, cfg Config) []Finding {
	findings := make([]Finding, 0)
	if probe.DurationSeconds < float64(cfg.MinDurationSeconds) {
		findings = append(findings, Finding{Code: "duration_too_short", Message: "The video is shorter than the configured minimum.", Severity: "error", Kind: "policy"})
	}
	if probe.DurationSeconds > float64(cfg.MaxDurationSeconds) {
		findings = append(findings, Finding{Code: "duration_too_long", Message: "The video is longer than the configured maximum.", Severity: "error", Kind: "policy"})
	}
	if probe.Width < 2 || probe.Height < 2 {
		findings = append(findings, Finding{Code: "invalid_dimensions", Message: "The video has invalid dimensions.", Severity: "error", Kind: "policy"})
	}
	if probe.Width > 7680 || probe.Height > 4320 {
		findings = append(findings, Finding{Code: "dimensions_too_large", Message: "The video resolution is too large.", Severity: "error", Kind: "policy"})
	}
	return findings
}

func policyFor(video store.Video, uploadStatus, verdict, reason string) store.Policy {
	policy := store.Policy{}
	if video.Policy != nil {
		policy = *video.Policy
	}
	if policy.PolicyVersion == "" {
		policy.PolicyVersion = "studio-0.1"
	}
	policy.UploadStatus = uploadStatus
	policy.ScanVerdict = verdict
	policy.ScanReason = reason
	return policy
}

func uploadStatus(publish bool, verdict string) string {
	if publish {
		return "ready"
	}
	if verdict == VerdictReview {
		return "review"
	}
	return "processing"
}

func findingsSummary(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.Message != "" {
			messages = append(messages, finding.Message)
		}
	}
	return truncateError(errors.New(strings.Join(messages, "; ")))
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func uploadFile(ctx context.Context, storage Storage, path, key, contentType string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return storage.PutObject(ctx, key, file, contentType)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func normalizeContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value
}

func (e *Engine) reportScan(
	ctx context.Context,
	video store.Video,
	verdict, scanner string,
	findings []Finding,
	reason string,
) {
	if e.OnScan == nil {
		return
	}
	if err := e.OnScan(ctx, video, verdict, scanner, findings, reason); err != nil {
		log.Printf("media scan callback for %s: %v", video.ID, err)
	}
}

var ErrPurgeUnsupported = errors.New("media storage does not support prefix deletion")

// PurgeVideo irreversibly destroys a creator-owned, archived video and all of
// its media objects. The database rows are removed only after object storage
// succeeds, so a transient storage failure leaves the archived record intact
// and the purge can be retried.
func (e *Engine) PurgeVideo(ctx context.Context, channelID, actorID, videoID string) error {
	plan, err := e.store.PrepareVideoPurge(ctx, channelID, videoID)
	if err != nil {
		return err
	}
	deleter, ok := e.storage.(PrefixDeleter)
	if !ok {
		return ErrPurgeUnsupported
	}
	baseKey := e.videoBaseKey(plan.Video.ChannelID, plan.Video.ID)
	keys := make(map[string]struct{})
	for _, asset := range plan.Assets {
		if asset.ObjectKey != "" {
			keys[asset.ObjectKey] = struct{}{}
		}
	}
	for _, track := range plan.Subtitles {
		if track.ObjectKey != "" {
			keys[track.ObjectKey] = struct{}{}
		}
	}
	for _, upload := range plan.Uploads {
		if upload.ObjectKey != "" {
			keys[upload.ObjectKey] = struct{}{}
		}
	}

	if err := deleter.DeletePrefix(ctx, baseKey+"/"); err != nil {
		return err
	}
	for key := range keys {
		if strings.HasPrefix(key, baseKey+"/") {
			continue
		}
		if err := e.storage.DeleteObject(ctx, key); err != nil {
			return err
		}
	}
	if err := e.store.DeletePurgedVideo(ctx, plan.Video.ChannelID, plan.Video.ID); err != nil {
		return err
	}
	if err := e.store.RecordMediaPurge(ctx, store.MediaPurge{
		VideoID:     plan.Video.ID,
		PublicID:    plan.Video.PublicID,
		ChannelID:   plan.Video.ChannelID,
		ActorID:     actorID,
		ObjectCount: len(keys),
		PurgedAt:    time.Now().UTC(),
	}); err != nil {
		log.Printf("media purge audit for %s: %v", plan.Video.ID, err)
	}
	return nil
}
