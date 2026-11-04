package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/keel-foundation/keel-core/internal/identity"
)

func (s *Server) socialProviders(w http.ResponseWriter, r *http.Request) {
	if s.social == nil {
		writeJSON(w, http.StatusOK, map[string]any{"providers": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.social.Providers()})
}

func (s *Server) socialStart(w http.ResponseWriter, r *http.Request) {
	if s.social == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "No social providers are configured.",
		})
		return
	}
	if !s.allowRequest(w, r) {
		return
	}
	location, expires, err := s.social.Start(
		r.Context(),
		r.PathValue("provider"),
		r.URL.Query().Get("return_to"),
	)
	if err != nil {
		s.writeSocialError(w, r, err)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"authorization_url": location,
			"expires_at":        expires,
		})
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) socialCallback(w http.ResponseWriter, r *http.Request) {
	if s.social == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"detail": "No social providers are configured.",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeSocialError(w, r, identity.ErrSocialState)
		return
	}
	providerError := strings.TrimSpace(r.Form.Get("error"))
	if providerError != "" {
		s.writeSocialError(w, r, errors.New(providerError))
		return
	}
	result, returnTo, err := s.social.Complete(
		r.Context(),
		r.PathValue("provider"),
		r.Form.Get("state"),
		r.Form.Get("code"),
		r.Form,
		requestMeta(r, s.trustProxy),
	)
	if err != nil {
		s.writeSocialError(w, r, err)
		return
	}
	s.identity.WriteSessionCookies(w, result)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, result)
		return
	}
	http.Redirect(w, r, strings.TrimRight(s.identity.Config().AppURL, "/")+returnTo, http.StatusFound)
}

func wantsJSON(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("format"), "json") {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/json") &&
		!strings.Contains(accept, "text/html")
}

func (s *Server) writeSocialError(w http.ResponseWriter, r *http.Request, err error) {
	if wantsJSON(r) {
		switch {
		case errors.Is(err, identity.ErrSocialUnavailable):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"detail": "That social provider is not configured.",
			})
		case errors.Is(err, identity.ErrSocialState):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"detail": "The social authorization state is invalid or expired.",
			})
		case errors.Is(err, identity.ErrSocialExchange):
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"detail": "The social provider did not complete the sign-in.",
			})
		case errors.Is(err, identity.ErrSocialClaims):
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "The social provider returned invalid claims.",
			})
		default:
			s.writeIdentityError(w, err)
		}
		return
	}
	target := strings.TrimRight(s.identity.Config().AppURL, "/")
	reason := "social_unavailable"
	switch {
	case errors.Is(err, identity.ErrSocialState):
		reason = "invalid_state"
	case errors.Is(err, identity.ErrSocialExchange):
		reason = "exchange_failed"
	case errors.Is(err, identity.ErrSocialClaims):
		reason = "invalid_claims"
	}
	http.Redirect(
		w,
		r,
		target+"/signin?social_error="+url.QueryEscape(reason),
		http.StatusFound,
	)
}
