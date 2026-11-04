package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keel-foundation/keel-core/internal/store"
)

const (
	maxChannelName        = 200
	maxChannelDescription = 10000
	maxChannelURLLength   = 500
	maxChannelLinks       = 20
	maxChannelLinkLabel   = 80
)

type channelProfileRequest struct {
	Name        *string              `json:"name"`
	Description *string              `json:"description"`
	AvatarURL   *string              `json:"avatar_url"`
	BannerURL   *string              `json:"banner_url"`
	Links       *[]store.ChannelLink `json:"links"`
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
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
	var ids []string
	if raw := strings.TrimSpace(r.URL.Query().Get("ids")); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			if id := strings.TrimSpace(value); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > maxLimit {
			writeCatalogError(w, &validationError{
				Field:   "ids",
				Message: "ids accepts at most 50 channels",
			})
			return
		}
	}
	channels, total, err := s.store.ListChannels(
		r.Context(), ids, strings.TrimSpace(r.URL.Query().Get("q")), limit, offset,
	)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	setPublicCache(w)
	writeJSON(w, http.StatusOK, newPage(channels, limit, offset, total))
}

func (s *Server) updateStudioChannel(w http.ResponseWriter, r *http.Request) {
	_, channel, ok := s.requireStudioChannel(w, r)
	if !ok {
		return
	}
	var body channelProfileRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeStudioError(w, err)
		return
	}
	input := store.ChannelProfileInput{
		Name:        channel.Name,
		Description: channel.Description,
		AvatarURL:   channel.AvatarURL,
		BannerURL:   channel.BannerURL,
		Links:       channel.Links,
	}
	if body.Name != nil {
		input.Name = strings.TrimSpace(*body.Name)
	}
	if body.Description != nil {
		input.Description = strings.TrimSpace(*body.Description)
	}
	if body.AvatarURL != nil {
		input.AvatarURL = strings.TrimSpace(*body.AvatarURL)
	}
	if body.BannerURL != nil {
		input.BannerURL = strings.TrimSpace(*body.BannerURL)
	}
	if body.Links != nil {
		input.Links = *body.Links
	}
	if err := validateChannelProfile(input); err != nil {
		writeStudioError(w, err)
		return
	}
	updated, err := s.store.UpdateChannelProfile(r.Context(), channel.ID, input, time.Now().UTC())
	if err != nil {
		writeStudioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func validateChannelProfile(input store.ChannelProfileInput) error {
	if name := strings.TrimSpace(input.Name); utf8.RuneCountInString(name) == 0 ||
		utf8.RuneCountInString(name) > maxChannelName {
		return &validationError{Field: "name", Message: "name must be between 1 and 200 characters"}
	}
	if utf8.RuneCountInString(input.Description) > maxChannelDescription {
		return &validationError{Field: "description", Message: "description must be at most 10000 characters"}
	}
	if len(input.AvatarURL) > maxChannelURLLength || len(input.BannerURL) > maxChannelURLLength {
		return &validationError{Field: "avatar_url", Message: "image URLs must be at most 500 characters"}
	}
	if store.HasControlCharacters(input.Name) || store.HasControlCharacters(input.Description) {
		return &validationError{Field: "name", Message: "profile contains unsupported control characters"}
	}
	if len(input.Links) > maxChannelLinks {
		return &validationError{Field: "links", Message: "use no more than 20 links"}
	}
	for _, link := range input.Links {
		label := strings.TrimSpace(link.Label)
		url := strings.TrimSpace(link.URL)
		if utf8.RuneCountInString(label) == 0 || utf8.RuneCountInString(label) > maxChannelLinkLabel {
			return &validationError{Field: "links", Message: "each link needs a label of at most 80 characters"}
		}
		if url == "" || len(url) > maxChannelURLLength ||
			(!strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://")) {
			return &validationError{Field: "links", Message: "each link needs an http or https URL"}
		}
		if store.HasControlCharacters(label) || store.HasControlCharacters(url) {
			return &validationError{Field: "links", Message: "links contain unsupported control characters"}
		}
	}
	return nil
}
