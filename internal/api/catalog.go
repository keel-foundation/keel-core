package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	defaultLimit           = 20
	maxLimit               = 50
	defaultExploreLimit    = 8
	maxExploreLimit        = 24
	defaultCollectionLimit = 4
	maxCollectionLimit     = 12
)

var (
	searchTypes           = []string{"all", "videos", "shorts", "channels"}
	exploreCollectionDefs = []struct {
		ID          string
		Title       string
		Description string
		Categories  []string
	}{
		{
			ID:          "even-keel",
			Title:       "The even keel",
			Description: "Protocol, moderation, and the record behind the platform.",
			Categories:  []string{"Protocol", "Moderation"},
		},
		{
			ID:          "field-notes",
			Title:       "Field notes",
			Description: "Original documentaries and observations from the field.",
			Categories:  []string{"Documentaries"},
		},
		{
			ID:          "build-in-public",
			Title:       "Build in public",
			Description: "Studio, federation, and the work behind the platform.",
			Categories:  []string{"Studio", "Federation"},
		},
	}
)

type page[T any] struct {
	Items   []T  `json:"items"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
}

func newPage[T any](items []T, limit, offset, total int) page[T] {
	if items == nil {
		items = []T{}
	}
	return page[T]{
		Items:   items,
		Limit:   limit,
		Offset:  offset,
		Total:   total,
		HasMore: offset+len(items) < total,
	}
}

type searchResponse struct {
	Videos   page[store.Video]   `json:"videos"`
	Channels page[store.Channel] `json:"channels"`
}

type exploreCollection struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Videos      page[store.Video] `json:"videos"`
}

type exploreResponse struct {
	Featured    *store.Video        `json:"featured"`
	New         page[store.Video]   `json:"new"`
	Trending    page[store.Video]   `json:"trending"`
	Collections []exploreCollection `json:"collections"`
}

type categoryResponse struct {
	Name       string `json:"name"`
	VideoCount int    `json:"video_count"`
}

type categoriesResponse struct {
	Categories []categoryResponse `json:"categories"`
}

type validationError struct {
	Field   string
	Message string
}

func (e *validationError) Error() string { return e.Message }

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	s.writeVideoList(w, r, "latest", "video", "", "")
}

func (s *Server) listVideos(w http.ResponseWriter, r *http.Request) {
	s.writeVideoList(w, r, "latest", "", "", "")
}

func (s *Server) shorts(w http.ResponseWriter, r *http.Request) {
	s.writeVideoList(w, r, "latest", "", "short", "")
}

func (s *Server) channelVideos(w http.ResponseWriter, r *http.Request) {
	channel, err := s.store.GetChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeVideoList(w, r, "latest", "video", "", channel.ID)
}

func (s *Server) writeVideoList(
	w http.ResponseWriter,
	r *http.Request,
	defaultSort, defaultFormat, forcedFormat, channelID string,
) {
	filter, err := parseVideoFilter(r, defaultSort, defaultFormat, forcedFormat, channelID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	videos, total, err := s.store.ListVideos(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(videos, filter.Limit, filter.Offset, total))
}

func parseVideoFilter(
	r *http.Request,
	defaultSort, defaultFormat, forcedFormat, channelID string,
) (store.VideoFilter, error) {
	limit, err := queryLimit(r, "limit", defaultLimit, maxLimit)
	if err != nil {
		return store.VideoFilter{}, err
	}
	offset, err := queryOffset(r)
	if err != nil {
		return store.VideoFilter{}, err
	}

	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = defaultSort
	}
	if !slices.Contains(store.VideoSorts, sort) {
		return store.VideoFilter{}, &validationError{
			Field:   "sort",
			Message: "sort must be one of " + strings.Join(store.VideoSorts, ", "),
		}
	}

	format := forcedFormat
	if format == "" {
		format = r.URL.Query().Get("format")
		if format == "" {
			format = defaultFormat
		}
	}
	if format != "" && !slices.Contains(store.Formats, format) {
		return store.VideoFilter{}, &validationError{
			Field:   "format",
			Message: "format must be one of " + strings.Join(store.Formats, ", "),
		}
	}

	var categories []string
	if category := r.URL.Query().Get("category"); category != "" {
		if !slices.Contains(store.Categories, category) {
			return store.VideoFilter{}, &validationError{
				Field:   "category",
				Message: "category must be one of " + strings.Join(store.Categories, ", "),
			}
		}
		categories = []string{category}
	}

	if channelID == "" {
		channelID = r.URL.Query().Get("channel_id")
	}

	return store.VideoFilter{
		ChannelID:  channelID,
		Categories: categories,
		Format:     format,
		Sort:       sort,
		Limit:      limit,
		Offset:     offset,
		PublicOnly: true,
	}, nil
}

func (s *Server) explore(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r, "limit", defaultExploreLimit, maxExploreLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	collectionLimit, err := queryLimit(r, "collection_limit", defaultCollectionLimit, maxCollectionLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	newVideos, newTotal, err := s.store.ListVideos(r.Context(), store.VideoFilter{
		Sort:       "latest",
		Limit:      limit,
		PublicOnly: true,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	trending, trendingTotal, err := s.store.ListVideos(r.Context(), store.VideoFilter{
		Sort:       "trending",
		Limit:      limit,
		PublicOnly: true,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	var featured *store.Video
	if len(trending) > 0 {
		featured = &trending[0]
	} else if len(newVideos) > 0 {
		featured = &newVideos[0]
	}

	collections := make([]exploreCollection, 0, len(exploreCollectionDefs))
	for _, def := range exploreCollectionDefs {
		videos, total, err := s.store.ListVideos(r.Context(), store.VideoFilter{
			Categories: def.Categories,
			Sort:       "latest",
			Limit:      collectionLimit,
			PublicOnly: true,
		})
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if len(videos) == 0 {
			continue
		}
		collections = append(collections, exploreCollection{
			ID:          def.ID,
			Title:       def.Title,
			Description: def.Description,
			Videos:      newPage(videos, collectionLimit, 0, total),
		})
	}

	writeJSON(w, http.StatusOK, exploreResponse{
		Featured:    featured,
		New:         newPage(newVideos, limit, 0, newTotal),
		Trending:    newPage(trending, limit, 0, trendingTotal),
		Collections: collections,
	})
}

func (s *Server) relatedVideos(w http.ResponseWriter, r *http.Request) {
	video, err := s.store.GetVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.ensureVideoAccess(w, r, video) {
		return
	}

	limit, err := queryLimit(r, "limit", 10, maxLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	videos, total, err := s.store.ListRelatedVideos(r.Context(), video.ID, video.Category, limit, offset)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newPage(videos, limit, offset, total))
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeCatalogError(w, &validationError{Field: "q", Message: "q is required"})
		return
	}
	if len(query) > 200 {
		writeCatalogError(w, &validationError{Field: "q", Message: "q must be 200 characters or fewer"})
		return
	}

	searchType := r.URL.Query().Get("type")
	if searchType == "" {
		searchType = "all"
	}
	if !slices.Contains(searchTypes, searchType) {
		writeCatalogError(w, &validationError{
			Field:   "type",
			Message: "type must be one of " + strings.Join(searchTypes, ", "),
		})
		return
	}

	limit, err := queryLimit(r, "limit", defaultLimit, maxLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	offset, err := queryOffset(r)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	videos := []store.Video{}
	totalVideos := 0
	if searchType != "channels" {
		format := ""
		switch searchType {
		case "videos":
			format = "video"
		case "shorts":
			format = "short"
		}
		videos, totalVideos, err = s.store.SearchVideos(r.Context(), query, format, limit, offset)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
	}

	channels := []store.Channel{}
	totalChannels := 0
	if searchType == "all" || searchType == "channels" {
		channels, totalChannels, err = s.store.SearchChannels(r.Context(), query, limit, offset)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, searchResponse{
		Videos:   newPage(videos, limit, offset, totalVideos),
		Channels: newPage(channels, limit, offset, totalChannels),
	})
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.ListCategoryCounts(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	categories := make([]categoryResponse, 0, len(counts))
	for _, count := range counts {
		categories = append(categories, categoryResponse{
			Name:       count.Name,
			VideoCount: count.VideoCount,
		})
	}
	writeJSON(w, http.StatusOK, categoriesResponse{Categories: categories})
}

func (s *Server) ensureVideoAccess(w http.ResponseWriter, r *http.Request, video store.Video) bool {
	if video.Status == "removed" {
		writeJSON(w, http.StatusGone, map[string]string{"detail": "This video was removed."})
		return false
	}
	if (video.Status == "published" || video.Status == "demonetized") &&
		(video.Visibility == "public" || video.Visibility == "unlisted") {
		return true
	}

	principal, err := s.auth.Authenticate(r)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthorized) {
			log.Printf("catalog auth failed: %v", err)
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return false
	}
	actorID, err := s.store.GetChannelActorID(r.Context(), video.ChannelID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if actorID != principal.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return false
	}
	return true
}

func writeCatalogError(w http.ResponseWriter, err error) {
	var validation *validationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": validation.Message,
			"field":  validation.Field,
		})
		return
	}
	log.Printf("catalog request failed: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"detail": "Internal server error",
	})
}

func queryLimit(r *http.Request, name string, fallback, max int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > max {
		return 0, &validationError{
			Field:   name,
			Message: fmt.Sprintf("%s must be between 1 and %d", name, max),
		}
	}
	return value, nil
}

func queryOffset(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, &validationError{
			Field:   "offset",
			Message: "offset must be zero or greater",
		}
	}
	return value, nil
}
