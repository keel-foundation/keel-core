package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/store"
)

const maxSuggestionLimit = 10

func (s *Server) semanticSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeCatalogError(w, &validationError{Field: "q", Message: "q is required"})
		return
	}
	if len(query) > 200 {
		writeCatalogError(w, &validationError{Field: "q", Message: "q must be 200 characters or fewer"})
		return
	}
	if s.discovery == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Semantic search is not configured.",
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
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format != "" && format != "video" && format != "short" {
		writeCatalogError(w, &validationError{Field: "format", Message: "format must be video or short"})
		return
	}
	if err := s.discovery.EnsureVideoEmbeddings(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Semantic search is temporarily unavailable.",
		})
		return
	}
	vector, err := s.discovery.EmbedQuery(r.Context(), query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "Semantic search is temporarily unavailable.",
		})
		return
	}
	results, total, err := s.store.SemanticSearchVideos(
		r.Context(), vector, s.discovery.EmbeddingModel(), channelID, format, limit, offset,
	)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	videos := make([]discovery.RecommendedVideo, len(results))
	for index, result := range results {
		videos[index] = discovery.RecommendedVideo{Video: result.Video, Score: result.Similarity}
	}
	if s.reranker != nil && len(videos) > 0 {
		candidates := make([]store.Video, len(videos))
		for index := range videos {
			candidates[index] = videos[index].Video
		}
		scores, rankErr := s.reranker.Rank(r.Context(), query, candidates)
		if rankErr == nil && len(scores) == len(videos) {
			for index := range videos {
				videos[index].Score = videos[index].Score + scores[index]*0.05
			}
			sort.SliceStable(videos, func(i, j int) bool {
				return videos[i].Score > videos[j].Score
			})
		}
	}
	setPublicCache(w)
	writeJSON(w, http.StatusOK, newPage(videos, limit, offset, total))
}

func (s *Server) searchSuggestions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"videos": []any{}, "shorts": []any{}, "channels": []any{},
		})
		return
	}
	limit, err := queryLimit(r, "limit", 5, maxSuggestionLimit)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	videos, _, err := s.store.SearchVideos(r.Context(), query, "video", "", limit, 0)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	shorts, _, err := s.store.SearchVideos(r.Context(), query, "short", "", limit, 0)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	channels, _, err := s.store.SearchChannels(r.Context(), query, limit, 0)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	setPublicCache(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"videos":   videos,
		"shorts":   shorts,
		"channels": channels,
	})
}
