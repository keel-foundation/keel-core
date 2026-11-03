package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/store"
)

type Options struct {
	Store      *store.Store
	Auth       auth.Authenticator
	Identity   *identity.Service
	Origins    []string
	RateLimit  int
	RateWindow time.Duration
	TrustProxy bool
}

type Server struct {
	store      *store.Store
	auth       auth.Authenticator
	identity   *identity.Service
	limiter    *rateLimiter
	trustProxy bool
}

func New(options Options) http.Handler {
	s := &Server{
		store:      options.Store,
		auth:       options.Auth,
		identity:   options.Identity,
		trustProxy: options.TrustProxy,
	}
	if options.RateLimit > 0 {
		s.limiter = newRateLimiter(options.RateLimit, options.RateWindow)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /api/v1/feed", s.feed)
	mux.HandleFunc("GET /api/v1/explore", s.explore)
	mux.HandleFunc("GET /api/v1/videos", s.listVideos)
	mux.HandleFunc("GET /api/v1/videos/demo", s.demoVideo)
	mux.HandleFunc("GET /api/v1/videos/{id}", s.getVideo)
	mux.HandleFunc("GET /api/v1/videos/{id}/related", s.relatedVideos)
	mux.HandleFunc("GET /api/v1/shorts", s.shorts)
	mux.HandleFunc("GET /api/v1/search", s.search)
	mux.HandleFunc("GET /api/v1/categories", s.categories)
	mux.HandleFunc("GET /api/v1/channels/demo", s.demoChannel)
	mux.HandleFunc("GET /api/v1/channels/{id}", s.getChannel)
	mux.HandleFunc("GET /api/v1/channels/{id}/videos", s.channelVideos)
	mux.HandleFunc("GET /api/v1/demo", s.demo)
	mux.HandleFunc("GET /api/v1/me", s.me)

	if s.identity != nil {
		mux.HandleFunc("POST /api/v1/auth/register", s.register)
		mux.HandleFunc("POST /api/v1/auth/verify-email", s.verifyEmail)
		mux.HandleFunc("POST /api/v1/auth/resend-verification", s.resendVerification)
		mux.HandleFunc("POST /api/v1/auth/login", s.login)
		mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
		mux.HandleFunc("GET /api/v1/auth/session", s.session)
		mux.HandleFunc("GET /api/v1/auth/csrf", s.csrf)
		mux.HandleFunc("POST /api/v1/auth/forgot-password", s.forgotPassword)
		mux.HandleFunc("POST /api/v1/auth/reset-password", s.resetPassword)
	}

	return withCORS(withCSRF(mux, s.identity), options.Origins)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":  "unavailable",
			"service": "core",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "core",
	})
}

func (s *Server) demoChannel(w http.ResponseWriter, r *http.Request) {
	s.writeChannel(w, r, "ch_demo")
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	s.writeChannel(w, r, r.PathValue("id"))
}

func (s *Server) writeChannel(w http.ResponseWriter, r *http.Request, id string) {
	channel, err := s.store.GetChannel(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

func (s *Server) demoVideo(w http.ResponseWriter, r *http.Request) {
	s.writeVideo(w, r, "vid_demo_001")
}

func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	video, err := s.store.GetVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.ensureVideoAccess(w, r, video) {
		return
	}
	writeJSON(w, http.StatusOK, video)
}

func (s *Server) writeVideo(w http.ResponseWriter, r *http.Request, id string) {
	video, err := s.store.GetVideo(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, video)
}

func (s *Server) demo(w http.ResponseWriter, r *http.Request) {
	video, err := s.store.GetVideo(r.Context(), "vid_demo_001")
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	channel, err := s.store.GetChannel(r.Context(), "ch_demo")
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"video":   video,
		"channel": channel,
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "A valid session is required.",
			})
			return
		}
		log.Printf("core auth failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
		return
	}
	log.Printf("core store failed: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"detail": "Internal server error",
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func withCORS(next http.Handler, origins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, origins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if candidate == "*" || candidate == origin {
			return true
		}
	}
	return false
}
