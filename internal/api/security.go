package api

import (
	"net/http"
	"strings"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
)

var csrfExempt = map[string]bool{
	"/api/v1/auth/register":            true,
	"/api/v1/auth/login":               true,
	"/api/v1/auth/verify-email":        true,
	"/api/v1/auth/resend-verification": true,
	"/api/v1/auth/forgot-password":     true,
	"/api/v1/auth/reset-password":      true,
}

// withCSRF applies double-submit CSRF validation to unsafe requests that carry a
// session cookie. Requests without a session cookie, and API clients using bearer
// tokens, are not affected.
func withCSRF(next http.Handler, identityService *identity.Service) http.Handler {
	if identityService == nil {
		return next
	}
	cfg := identityService.Config()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") || r.URL.Path == "/api/v1/me" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if isSafeMethod(r.Method) || csrfExempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie(cfg.SessionCookieName); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(cfg.CSRFCookieName)
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || header == "" || !auth.ConstantTimeEqual(cookie.Value, header) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"detail": "CSRF validation failed.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
