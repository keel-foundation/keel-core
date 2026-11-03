package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/media"
	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	defaultStudioLimit = 20
	maxStudioLimit     = 100
	maxStudioBody      = 1 << 20
	maxTitleLength     = 200
	maxDescription     = 5000
	maxTags            = 20
	maxTagLength       = 50
	maxCuts            = 100
	maxVersionLabel    = 100
)

var renditionPattern = regexp.MustCompile(`^[0-9]{3,4}p$`)
var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ts$`)

type StudioMedia interface {
	CreateUpload(context.Context, string, string, media.CreateUploadInput) (media.UploadSession, error)
	CompleteUpload(context.Context, string, string, []media.CompletedPart, string) (store.UploadIntent, error)
	AbortUpload(context.Context, string, string) error
	RefreshUploadParts(context.Context, string, string, []int32) ([]media.PresignedPart, time.Time, error)
	CreateSubtitleUpload(context.Context, string, string, string, string, string, string, string, int64) (media.SubtitleUpload, error)
	CompleteSubtitleUpload(context.Context, string, string, string) (store.SubtitleTrack, error)
	DeleteSubtitle(context.Context, string, string, string) error
	CreateThumbnailUpload(context.Context, string, string, string, string, int64) (media.ThumbnailUpload, error)
	CompleteThumbnailUpload(context.Context, string, string, string) error
	PresignGet(context.Context, string) (string, error)
	GetObject(context.Context, string) (io.ReadCloser, error)
}

type studioVideoPage struct {
	Items   []store.Video `json:"items"`
	Limit   int           `json:"limit"`
	Offset  int           `json:"offset"`
	Total   int           `json:"total"`
	HasMore bool          `json:"has_more"`
}

type studioChannelResponse struct {
	Channel store.Channel `json:"channel"`
}

// studioVideoDetail is the creator edit payload. The embedded Video fields stay at the
// top level so existing clients keep working; rights, subtitles, and versions are additive.
type studioVideoDetail struct {
	store.Video
	Rights    *store.VideoRights    `json:"rights,omitempty"`
	Subtitles []store.SubtitleTrack `json:"subtitles"`
	Versions  []store.VideoVersion  `json:"versions"`
}

type studioVideoRequest struct {
	Title             *string   `json:"title"`
	Description       *string   `json:"description"`
	Visibility        *string   `json:"visibility"`
	Category          *string   `json:"category"`
	License           *string   `json:"license"`
	Format            *string   `json:"format"`
	Tags              *[]string `json:"tags"`
	ExpectedUpdatedAt *string   `json:"expected_updated_at"`
}

type studioVersionRequest struct {
	Label  string                 `json:"label"`
	Trim   store.VideoTrim        `json:"trim"`
	Cuts   []store.VideoCut       `json:"cuts"`
	Audio  *store.VideoAudioTrack `json:"audio"`
	Volume *float64               `json:"volume"`
}

type studioUploadRequest struct {
	Title         string               `json:"title"`
	Description   string               `json:"description"`
	Visibility    string               `json:"visibility"`
	Category      string               `json:"category"`
	License       string               `json:"license"`
	Format        string               `json:"format"`
	Tags          []string             `json:"tags"`
	Filename      string               `json:"filename"`
	ContentType   string               `json:"content_type"`
	SizeBytes     int64                `json:"size_bytes"`
	ContentSHA256 string               `json:"content_sha256"`
	Rights        *studioRightsRequest `json:"rights"`
}

type studioRightsRequest struct {
	Declaration string `json:"declaration"`
	Holder      string `json:"holder"`
	Notes       string `json:"notes"`
}

type studioUploadResponse struct {
	Upload store.UploadIntent `json:"upload"`
	Video  store.Video        `json:"video"`
}

type completeUploadRequest struct {
	Parts         []completedUploadPart `json:"parts"`
	ContentSHA256 string                `json:"content_sha256"`
}

type refreshUploadPartsRequest struct {
	PartNumbers []int32 `json:"part_numbers"`
}

type uploadPartsResponse struct {
	Parts     []media.PresignedPart `json:"parts"`
	ExpiresAt time.Time             `json:"expires_at"`
}

type completedUploadPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

type studioSubtitleRequest struct {
	Language    string `json:"language"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type studioThumbnailRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type completeThumbnailRequest struct {
	ObjectKey string `json:"object_key"`
}

func (s *Server) studioChannel(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, studioChannelResponse{Channel: channel})
}

func (s *Server) listStudioVideos(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	limit, err := queryLimit(r, "limit", defaultStudioLimit, maxStudioLimit)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	filter := store.StudioVideoFilter{
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
		Visibility: strings.TrimSpace(r.URL.Query().Get("visibility")),
		Format:     strings.TrimSpace(r.URL.Query().Get("format")),
		Category:   strings.TrimSpace(r.URL.Query().Get("category")),
		Sort:       strings.TrimSpace(r.URL.Query().Get("sort")),
		Limit:      limit,
		Offset:     offset,
	}
	if filter.Status != "" && !store.ValidVideoStatus(filter.Status) {
		writeStudioError(w, &validationError{Field: "status", Message: "status is invalid"})
		return
	}
	if filter.Visibility != "" && !store.ValidVisibility(filter.Visibility) {
		writeStudioError(w, &validationError{Field: "visibility", Message: "visibility is invalid"})
		return
	}
	if filter.Format != "" && !store.ValidFormat(filter.Format) {
		writeStudioError(w, &validationError{Field: "format", Message: "format is invalid"})
		return
	}
	if filter.Category != "" && !store.ValidCategory(filter.Category) {
		writeStudioError(w, &validationError{Field: "category", Message: "category is invalid"})
		return
	}
	if filter.Sort != "" && filter.Sort != "latest" && filter.Sort != "oldest" && filter.Sort != "published" && filter.Sort != "views" {
		writeStudioError(w, &validationError{Field: "sort", Message: "sort is invalid"})
		return
	}
	videos, total, err := s.store.ListStudioVideos(r.Context(), channel.ID, filter)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, studioVideoPage{
		Items:   videos,
		Limit:   limit,
		Offset:  offset,
		Total:   total,
		HasMore: offset+len(videos) < total,
	})
}

func (s *Server) createStudioVideo(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	var request studioVideoRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	videoID, err := store.NewID("vid_")
	if err != nil {
		writeStudioError(w, err)
		return
	}
	now := time.Now().UTC()
	video := store.Video{
		ID:          videoID,
		ChannelID:   channel.ID,
		Title:       stringValue(request.Title),
		Description: stringValue(request.Description),
		Status:      "draft",
		Visibility:  defaultString(stringValue(request.Visibility), "private"),
		Playback:    store.Playback{},
		License:     defaultString(stringValue(request.License), "standard"),
		Format:      defaultString(stringValue(request.Format), "video"),
		Category:    defaultString(stringValue(request.Category), "Documentaries"),
		Tags:        tagsValue(request.Tags),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := validateStudioMetadata(video.Title, video.Description, video.Visibility, video.Category, video.License, video.Format, video.Tags); err != nil {
		writeStudioError(w, err)
		return
	}
	created, err := s.store.CreateStudioVideo(r.Context(), video)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getStudioVideo(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	subtitles, err := s.store.ListSubtitles(r.Context(), channel.ID, video.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	versions, err := s.store.ListVideoVersions(r.Context(), channel.ID, video.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if subtitles == nil {
		subtitles = []store.SubtitleTrack{}
	}
	if versions == nil {
		versions = []store.VideoVersion{}
	}
	writeJSON(w, http.StatusOK, studioVideoDetail{
		Video:     video,
		Rights:    videoRights(video),
		Subtitles: subtitles,
		Versions:  versions,
	})
}

func videoRights(video store.Video) *store.VideoRights {
	if video.Policy == nil {
		return nil
	}
	rights := store.VideoRights{
		Declaration: video.Policy.RightsDeclaration,
		Holder:      video.Policy.RightsHolder,
		Notes:       video.Policy.RightsNotes,
	}
	if rights.Declaration == "" && rights.Holder == "" && rights.Notes == "" {
		return nil
	}
	return &rights
}

func (s *Server) updateStudioVideo(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	var request studioVideoRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	updated := store.VideoMetadataInput{
		Title:       pointerOr(request.Title, video.Title),
		Description: pointerOr(request.Description, video.Description),
		Visibility:  pointerOr(request.Visibility, video.Visibility),
		Category:    pointerOr(request.Category, video.Category),
		License:     pointerOr(request.License, video.License),
		Format:      pointerOr(request.Format, video.Format),
		Tags:        video.Tags,
	}
	if request.Tags != nil {
		updated.Tags = *request.Tags
	}
	if err := validateStudioMetadata(updated.Title, updated.Description, updated.Visibility, updated.Category, updated.License, updated.Format, updated.Tags); err != nil {
		writeStudioError(w, err)
		return
	}
	expected, expectedRange, err := expectedUpdatedAt(r, request.ExpectedUpdatedAt)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	result, err := s.store.UpdateStudioVideo(r.Context(), channel.ID, video.ID, updated, expected, expectedRange, time.Now().UTC())
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) publishStudioVideo(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.PublishStudioVideo(r.Context(), channel.ID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if s.notifications != nil {
		notifyCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		if err := s.notifications.VideoPublished(notifyCtx, video.ID); err != nil {
			log.Printf("notifications: publish %s: %v", video.ID, err)
		}
		cancel()
	}
	if s.discovery != nil {
		go func(videoID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.discovery.RefreshVideo(ctx, videoID); err != nil {
				log.Printf("discovery: refresh published video %s: %v", videoID, err)
			}
		}(video.ID)
	}
	writeJSON(w, http.StatusOK, video)
}

func (s *Server) unpublishStudioVideo(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.UnpublishStudioVideo(r.Context(), channel.ID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, video)
}

func (s *Server) listStudioVersions(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	versions, err := s.store.ListVideoVersions(r.Context(), channel.ID, video.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": versions})
}

func (s *Server) createStudioVersion(w http.ResponseWriter, r *http.Request) {
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	var request studioVersionRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	if err := validateVideoVersion(request); err != nil {
		writeStudioError(w, err)
		return
	}
	volume := 1.0
	if request.Volume != nil {
		volume = *request.Volume
	}
	version, err := s.store.CreateVideoVersion(r.Context(), store.VideoVersion{
		VideoID:   video.ID,
		ChannelID: channel.ID,
		Label:     strings.TrimSpace(request.Label),
		Trim:      request.Trim,
		Cuts:      request.Cuts,
		Audio:     request.Audio,
		Volume:    volume,
		CreatedBy: principal.ID,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (s *Server) getStudioVersion(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	version, err := s.store.GetVideoVersion(r.Context(), channel.ID, video.ID, r.PathValue("version_id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *Server) studioStats(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	stats, err := s.store.StudioStats(r.Context(), channel.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) createStudioUpload(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	principal, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	var request studioUploadRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	input := media.CreateUploadInput{
		Title:         request.Title,
		Description:   request.Description,
		Visibility:    request.Visibility,
		Category:      request.Category,
		License:       request.License,
		Format:        request.Format,
		Tags:          request.Tags,
		Filename:      request.Filename,
		ContentType:   request.ContentType,
		SizeBytes:     request.SizeBytes,
		ContentSHA256: request.ContentSHA256,
	}
	if request.Rights != nil {
		input.RightsDeclaration = request.Rights.Declaration
		input.RightsHolder = request.Rights.Holder
		input.RightsNotes = request.Rights.Notes
	}
	session, err := s.media.CreateUpload(r.Context(), channel.ID, principal.ID, input)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getStudioUpload(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	upload, err := s.store.GetUpload(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, upload.VideoID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, studioUploadResponse{Upload: upload, Video: video})
}

func (s *Server) listStudioUploadScans(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	upload, err := s.store.GetUpload(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	scans, err := s.store.ListMediaScans(r.Context(), upload.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": scans})
}

func (s *Server) refreshStudioUploadParts(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	var request refreshUploadPartsRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	parts, expiresAt, err := s.media.RefreshUploadParts(r.Context(), channel.ID, r.PathValue("id"), request.PartNumbers)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, uploadPartsResponse{Parts: parts, ExpiresAt: expiresAt})
}

func (s *Server) completeStudioUpload(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	var request completeUploadRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	if len(request.Parts) == 0 {
		writeStudioError(w, &validationError{Field: "parts", Message: "parts are required"})
		return
	}
	parts := make([]media.CompletedPart, 0, len(request.Parts))
	for _, part := range request.Parts {
		parts = append(parts, media.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag})
	}
	upload, err := s.media.CompleteUpload(r.Context(), channel.ID, r.PathValue("id"), parts, request.ContentSHA256)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, upload.VideoID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, studioUploadResponse{Upload: upload, Video: video})
}

func (s *Server) abortStudioUpload(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	if err := s.media.AbortUpload(r.Context(), channel.ID, r.PathValue("id")); err != nil {
		writeStudioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createStudioThumbnail(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	var request studioThumbnailRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	upload, err := s.media.CreateThumbnailUpload(
		r.Context(), channel.ID, video.ID, request.Filename, request.ContentType, request.SizeBytes,
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, upload)
}

func (s *Server) completeStudioThumbnail(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	var request completeThumbnailRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	if err := s.media.CompleteThumbnailUpload(r.Context(), channel.ID, video.ID, request.ObjectKey); err != nil {
		writeStudioError(w, err)
		return
	}
	updated, err := s.store.GetStudioVideo(r.Context(), channel.ID, video.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listStudioSubtitles(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	tracks, err := s.store.ListSubtitles(r.Context(), channel.ID, video.ID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tracks})
}

func (s *Server) createStudioSubtitle(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	var request studioSubtitleRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeStudioError(w, err)
		return
	}
	upload, err := s.media.CreateSubtitleUpload(
		r.Context(), channel.ID, video.ID, request.Language, request.Label,
		request.Kind, request.Filename, request.ContentType, request.SizeBytes,
	)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, upload)
}

func (s *Server) completeStudioSubtitle(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	track, err := s.media.CompleteSubtitleUpload(r.Context(), channel.ID, video.ID, r.PathValue("track_id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, track)
}

func (s *Server) deleteStudioSubtitle(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	video, err := s.store.GetStudioVideo(r.Context(), channel.ID, r.PathValue("id"))
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if err := s.media.DeleteSubtitle(r.Context(), channel.ID, video.ID, r.PathValue("track_id")); err != nil {
		writeStudioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mediaSubtitle(w http.ResponseWriter, r *http.Request) {
	video, ok := s.mediaVideo(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	trackID := strings.TrimSuffix(r.PathValue("track_id"), ".vtt")
	if trackID == "" || strings.ContainsAny(trackID, "/\\") {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	track, err := s.store.GetSubtitleTrack(r.Context(), video.ID, trackID)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if track.Status != "ready" {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	s.redirectMedia(w, r, video, track.ObjectKey)
}

func (s *Server) requireStudioChannel(w http.ResponseWriter, r *http.Request) (auth.Principal, store.Channel, bool) {
	principal, ok := s.requirePrincipal(w, r)
	if !ok {
		return auth.Principal{}, store.Channel{}, false
	}
	channel, err := s.store.GetChannelByActorID(r.Context(), principal.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"detail": "A creator channel is required for studio access.",
			})
			return auth.Principal{}, store.Channel{}, false
		}
		writeStudioError(w, err)
		return auth.Principal{}, store.Channel{}, false
	}
	return principal, channel, true
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxStudioBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &validationError{Field: "body", Message: "invalid JSON body"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &validationError{Field: "body", Message: "request body must contain one JSON object"}
	}
	return nil
}

func validateStudioMetadata(title, description, visibility, category, license, format string, tags []string) error {
	if len([]rune(strings.TrimSpace(title))) == 0 || len([]rune(strings.TrimSpace(title))) > maxTitleLength {
		return &validationError{Field: "title", Message: "title must be between 1 and 200 characters"}
	}
	if len([]rune(description)) > maxDescription {
		return &validationError{Field: "description", Message: "description must be at most 5000 characters"}
	}
	if store.HasControlCharacters(title) || store.HasControlCharacters(description) {
		return &validationError{Field: "title", Message: "metadata contains unsupported control characters"}
	}
	if !store.ValidVisibility(visibility) {
		return &validationError{Field: "visibility", Message: "visibility must be public, unlisted, or private"}
	}
	if !store.ValidCategory(category) {
		return &validationError{Field: "category", Message: "category is invalid"}
	}
	if !store.ValidLicense(license) {
		return &validationError{Field: "license", Message: "license is invalid"}
	}
	if !store.ValidFormat(format) {
		return &validationError{Field: "format", Message: "format must be video or short"}
	}
	if len(tags) > maxTags {
		return &validationError{Field: "tags", Message: "use no more than 20 tags"}
	}
	for _, tag := range tags {
		if len([]rune(strings.TrimSpace(tag))) == 0 || len([]rune(tag)) > maxTagLength || store.HasControlCharacters(tag) {
			return &validationError{Field: "tags", Message: "each tag must be between 1 and 50 characters"}
		}
	}
	return nil
}

func validateVideoVersion(request studioVersionRequest) error {
	if len([]rune(request.Label)) > maxVersionLabel {
		return &validationError{Field: "label", Message: "label must be at most 100 characters"}
	}
	if request.Trim.Start < 0 || request.Trim.End <= request.Trim.Start {
		return &validationError{Field: "trim", Message: "trim end must be greater than trim start"}
	}
	if request.Volume != nil && (*request.Volume < 0 || *request.Volume > 2) {
		return &validationError{Field: "volume", Message: "volume must be between 0 and 2"}
	}
	if len(request.Cuts) > maxCuts {
		return &validationError{Field: "cuts", Message: "too many cuts"}
	}
	for _, cut := range request.Cuts {
		if cut.Start < 0 || cut.End <= cut.Start {
			return &validationError{Field: "cuts", Message: "each cut must have a positive duration"}
		}
	}
	if request.Audio != nil {
		if len([]rune(request.Audio.Name)) > 100 {
			return &validationError{Field: "audio.name", Message: "audio name is too long"}
		}
		if request.Audio.URL != "" {
			parsed, err := url.Parse(request.Audio.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return &validationError{Field: "audio.url", Message: "audio URL must use http or https"}
			}
		}
		if request.Audio.Volume < 0 || request.Audio.Volume > 2 {
			return &validationError{Field: "audio.volume", Message: "audio volume must be between 0 and 2"}
		}
	}
	return nil
}

func expectedUpdatedAt(r *http.Request, body *string) (*time.Time, bool, error) {
	if value := strings.TrimSpace(r.Header.Get("If-Unmodified-Since")); value != "" {
		parsed, err := http.ParseTime(value)
		if err != nil {
			return nil, false, &validationError{Field: "If-Unmodified-Since", Message: "invalid If-Unmodified-Since header"}
		}
		parsed = parsed.UTC()
		return &parsed, true, nil
	}
	if body != nil && strings.TrimSpace(*body) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*body))
		if err != nil {
			return nil, false, &validationError{Field: "expected_updated_at", Message: "expected_updated_at must be RFC3339"}
		}
		parsed = parsed.UTC()
		return &parsed, false, nil
	}
	return nil, false, nil
}

func writeStudioError(w http.ResponseWriter, err error) {
	var validation *validationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": validation.Message,
			"field":  validation.Field,
		})
		return
	}
	var policy *media.PolicyError
	if errors.As(err, &policy) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail":   "The upload was blocked by policy.",
			"findings": policy.Findings,
		})
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "The video changed since it was loaded."})
	case errors.Is(err, store.ErrInvalidState):
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "The video is not in a state that allows this action."})
	case errors.Is(err, store.ErrInvalidInput):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": err.Error()})
	case errors.Is(err, media.ErrDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Media storage is not configured."})
	case errors.Is(err, media.ErrInvalidUpload):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": err.Error()})
	case errors.Is(err, media.ErrStorage):
		log.Printf("studio media storage failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": "Media storage is unavailable."})
	default:
		log.Printf("studio request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "Internal server error"})
	}
}

func (s *Server) mediaPlayback(w http.ResponseWriter, r *http.Request, kind string) {
	video, ok := s.mediaVideo(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	asset, found, err := s.store.GetMediaAsset(r.Context(), video.ID, kind)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if !found || asset.ObjectKey == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	s.redirectMedia(w, r, video, asset.ObjectKey)
}

func (s *Server) mediaManifest(w http.ResponseWriter, r *http.Request, variant string) {
	video, ok := s.mediaVideo(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	asset, found, err := s.store.GetMediaAsset(r.Context(), video.ID, "hls")
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if !found || asset.ObjectKey == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	key := asset.ObjectKey + "master.m3u8"
	if variant != "" {
		if !renditionPattern.MatchString(variant) {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
			return
		}
		key = asset.ObjectKey + variant + "/index.m3u8"
	}
	body, err := s.media.GetObject(r.Context(), key)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	if isPublicVideo(video) {
		setPublicCache(w)
	} else {
		setPrivateCache(w)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(body, 1<<20))
}

func (s *Server) mediaSegment(w http.ResponseWriter, r *http.Request) {
	video, ok := s.mediaVideo(w, r)
	if !ok {
		return
	}
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	rendition := r.PathValue("rendition")
	segment := r.PathValue("segment")
	if !renditionPattern.MatchString(rendition) || !segmentPattern.MatchString(segment) || path.Base(segment) != segment {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	asset, found, err := s.store.GetMediaAsset(r.Context(), video.ID, "hls")
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if !found || asset.ObjectKey == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	s.redirectMedia(w, r, video, asset.ObjectKey+rendition+"/"+segment)
}

func (s *Server) redirectMedia(w http.ResponseWriter, r *http.Request, video store.Video, key string) {
	if s.media == nil {
		writeStudioError(w, media.ErrDisabled)
		return
	}
	location, err := s.media.PresignGet(r.Context(), key)
	if err != nil {
		writeStudioError(w, err)
		return
	}
	if isPublicVideo(video) {
		setPublicCache(w)
	} else {
		setPrivateCache(w)
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) mediaVideo(w http.ResponseWriter, r *http.Request) (store.Video, bool) {
	video, err := s.store.GetVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return store.Video{}, false
	}
	if !s.ensureVideoAccess(w, r, video) {
		return store.Video{}, false
	}
	return video, true
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func pointerOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func tagsValue(value *[]string) []string {
	if value == nil {
		return []string{}
	}
	return *value
}

func (s *Server) mediaMP4(w http.ResponseWriter, r *http.Request) {
	s.mediaPlayback(w, r, "mp4")
}

func (s *Server) mediaThumbnail(w http.ResponseWriter, r *http.Request) {
	s.mediaPlayback(w, r, "thumbnail")
}

func (s *Server) mediaCaptions(w http.ResponseWriter, r *http.Request) {
	s.mediaPlayback(w, r, "caption")
}

func (s *Server) mediaMaster(w http.ResponseWriter, r *http.Request) {
	s.mediaManifest(w, r, "")
}

func (s *Server) mediaVariant(w http.ResponseWriter, r *http.Request) {
	s.mediaManifest(w, r, r.PathValue("rendition"))
}

func (s *Server) mediaSegmentHandler(w http.ResponseWriter, r *http.Request) {
	s.mediaSegment(w, r)
}

func (s *Server) mediaSubtitleHandler(w http.ResponseWriter, r *http.Request) {
	s.mediaSubtitle(w, r)
}
