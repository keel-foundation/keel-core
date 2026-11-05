package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/billing"
	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/federation"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/notifications"
	"github.com/keel-foundation/keel-core/internal/store"
)

type Options struct {
	Store         *store.Store
	Auth          auth.Authenticator
	Identity      *identity.Service
	Social        *identity.SocialService
	Discovery     *discovery.Engine
	Reranker      discovery.Reranker
	Notifications *notifications.Service
	Moderation    moderation.Service
	Media         StudioMedia
	Billing       *billing.Service
	Federation    *federation.Service
	InternalToken string
	Origins       []string
	RateLimit     int
	RateWindow    time.Duration
	TrustProxy    bool
}

type Server struct {
	store         *store.Store
	auth          auth.Authenticator
	identity      *identity.Service
	social        *identity.SocialService
	discovery     *discovery.Engine
	reranker      discovery.Reranker
	notifications *notifications.Service
	moderation    moderation.Service
	media         StudioMedia
	billing       *billing.Service
	federation    *federation.Service
	internalToken string
	limiter       *rateLimiter
	trustProxy    bool
}

func New(options Options) http.Handler {
	discoveryEngine := options.Discovery
	if discoveryEngine == nil && options.Store != nil {
		discoveryEngine = discovery.NewEngine(
			options.Store,
			discovery.NewHashEmbedder(discovery.DefaultDimensions),
			discovery.DefaultConfig(),
		)
	}
	notificationService := options.Notifications
	if notificationService == nil && options.Store != nil {
		notificationService = notifications.New(options.Store)
	}
	reranker := options.Reranker
	if reranker == nil {
		reranker = discovery.LexicalReranker{}
	}
	s := &Server{
		store:         options.Store,
		auth:          options.Auth,
		identity:      options.Identity,
		social:        options.Social,
		discovery:     discoveryEngine,
		reranker:      reranker,
		notifications: notificationService,
		moderation:    options.Moderation,
		media:         options.Media,
		billing:       options.Billing,
		federation:    options.Federation,
		internalToken: options.InternalToken,
		trustProxy:    options.TrustProxy,
	}
	if options.RateLimit > 0 {
		s.limiter = newRateLimiter(options.RateLimit, options.RateWindow)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /.well-known/webfinger", s.webfinger)
	mux.HandleFunc("GET /federation/actors/{id}", s.federationActor)
	mux.HandleFunc("GET /federation/actors/{id}/outbox", s.federationOutbox)
	mux.HandleFunc("GET /federation/actors/{id}/followers", s.federationFollowers)
	mux.HandleFunc("GET /federation/actors/{id}/following", s.federationFollowing)
	mux.HandleFunc("POST /federation/actors/{id}/inbox", s.federationInbox)
	mux.HandleFunc("GET /federation/videos/{id}", s.federationVideo)
	mux.HandleFunc("GET /federation/policy", s.federationPolicy)
	mux.HandleFunc("GET /api/v1/feed", s.feed)
	mux.HandleFunc("GET /api/v1/explore", s.explore)
	mux.HandleFunc("GET /api/v1/videos", s.listVideos)
	mux.HandleFunc("GET /api/v1/videos/demo", s.demoVideo)
	mux.HandleFunc("GET /api/v1/videos/{id}", s.getVideo)
	mux.HandleFunc("GET /api/v1/videos/{id}/related", s.relatedVideos)
	mux.HandleFunc("GET /api/v1/videos/{id}/trust", s.videoTrustRecord)
	mux.HandleFunc("GET /api/v1/shorts", s.shorts)
	mux.HandleFunc("GET /api/v1/search", s.search)
	mux.HandleFunc("GET /api/v1/search/semantic", s.semanticSearch)
	mux.HandleFunc("GET /api/v1/search/suggestions", s.searchSuggestions)
	mux.HandleFunc("GET /api/v1/categories", s.categories)
	mux.HandleFunc("GET /api/v1/channels", s.listChannels)
	mux.HandleFunc("GET /api/v1/channels/demo", s.demoChannel)
	mux.HandleFunc("GET /api/v1/channels/{id}", s.getChannel)
	mux.HandleFunc("GET /api/v1/channels/{id}/videos", s.channelVideos)
	mux.HandleFunc("GET /api/v1/demo", s.demo)
	mux.HandleFunc("GET /api/v1/me", s.me)

	mux.HandleFunc("GET /api/v1/channels/{id}/subscription", s.getSubscription)
	mux.HandleFunc("POST /api/v1/channels/{id}/subscription", s.subscribe)
	mux.HandleFunc("DELETE /api/v1/channels/{id}/subscription", s.unsubscribe)
	mux.HandleFunc("GET /api/v1/me/subscriptions", s.listSubscriptions)
	mux.HandleFunc("GET /api/v1/me/feed", s.subscriptionFeed)
	mux.HandleFunc("GET /api/v1/me/library", s.library)
	mux.HandleFunc("GET /api/v1/me/playlists", s.listMyPlaylists)
	mux.HandleFunc("POST /api/v1/me/playlists", s.createPlaylist)
	mux.HandleFunc("GET /api/v1/playlists/{id}", s.getPlaylist)
	mux.HandleFunc("PATCH /api/v1/playlists/{id}", s.updatePlaylist)
	mux.HandleFunc("DELETE /api/v1/playlists/{id}", s.deletePlaylist)
	mux.HandleFunc("GET /api/v1/playlists/{id}/videos", s.listPlaylistItems)
	mux.HandleFunc("POST /api/v1/playlists/{id}/videos", s.addPlaylistItem)
	mux.HandleFunc("DELETE /api/v1/playlists/{id}/videos/{video_id}", s.removePlaylistItem)
	mux.HandleFunc("DELETE /api/v1/me/history", s.clearHistory)
	mux.HandleFunc("GET /api/v1/me/notifications", s.listNotifications)
	mux.HandleFunc("GET /api/v1/me/notifications/unread-count", s.unreadNotificationCount)
	mux.HandleFunc("POST /api/v1/me/notifications/{id}/read", s.markNotificationRead)
	mux.HandleFunc("POST /api/v1/me/notifications/read-all", s.markAllNotificationsRead)

	mux.HandleFunc("GET /api/v1/videos/{id}/reaction", s.getReaction)
	mux.HandleFunc("PUT /api/v1/videos/{id}/reaction", s.setReaction)
	mux.HandleFunc("DELETE /api/v1/videos/{id}/reaction", s.clearReaction)

	mux.HandleFunc("GET /api/v1/videos/{id}/save", s.getSave)
	mux.HandleFunc("PUT /api/v1/videos/{id}/save", s.saveVideo)
	mux.HandleFunc("DELETE /api/v1/videos/{id}/save", s.unsaveVideo)

	mux.HandleFunc("POST /api/v1/videos/{id}/view", s.startView)
	mux.HandleFunc("POST /api/v1/videos/{id}/view/progress", s.progressView)
	mux.HandleFunc("POST /api/v1/telemetry/events", s.recordTelemetryEvents)

	mux.HandleFunc("GET /api/v1/videos/{id}/comments", s.listComments)
	mux.HandleFunc("POST /api/v1/videos/{id}/comments", s.createComment)
	mux.HandleFunc("POST /api/v1/comments/{id}/like", s.likeComment)
	mux.HandleFunc("DELETE /api/v1/comments/{id}/like", s.unlikeComment)
	mux.HandleFunc("POST /api/v1/reports", s.createReport)
	mux.HandleFunc("GET /api/v1/reports/{id}", s.getReport)
	mux.HandleFunc("GET /api/v1/me/reports", s.listMyReports)

	mux.HandleFunc("GET /api/v1/studio/channel", s.studioChannel)
	mux.HandleFunc("PATCH /api/v1/studio/channel", s.updateStudioChannel)
	mux.HandleFunc("GET /api/v1/studio/videos", s.listStudioVideos)
	mux.HandleFunc("POST /api/v1/studio/videos", s.createStudioVideo)
	mux.HandleFunc("GET /api/v1/studio/videos/{id}", s.getStudioVideo)
	mux.HandleFunc("PATCH /api/v1/studio/videos/{id}", s.updateStudioVideo)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/publish", s.publishStudioVideo)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/unpublish", s.unpublishStudioVideo)
	mux.HandleFunc("DELETE /api/v1/studio/videos/{id}", s.archiveStudioVideo)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/restore", s.restoreStudioVideo)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/purge", s.purgeStudioVideo)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/schedule", s.scheduleStudioVideo)
	mux.HandleFunc("DELETE /api/v1/studio/videos/{id}/schedule", s.clearStudioSchedule)
	mux.HandleFunc("GET /api/v1/studio/videos/{id}/versions", s.listStudioVersions)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/versions", s.createStudioVersion)
	mux.HandleFunc("GET /api/v1/studio/videos/{id}/versions/{version_id}", s.getStudioVersion)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/thumbnail", s.createStudioThumbnail)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/thumbnail/complete", s.completeStudioThumbnail)
	mux.HandleFunc("GET /api/v1/studio/videos/{id}/subtitles", s.listStudioSubtitles)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/subtitles", s.createStudioSubtitle)
	mux.HandleFunc("POST /api/v1/studio/videos/{id}/subtitles/{track_id}/complete", s.completeStudioSubtitle)
	mux.HandleFunc("DELETE /api/v1/studio/videos/{id}/subtitles/{track_id}", s.deleteStudioSubtitle)
	mux.HandleFunc("GET /api/v1/studio/stats", s.studioStats)
	mux.HandleFunc("GET /api/v1/studio/comments", s.studioComments)
	mux.HandleFunc("POST /api/v1/studio/comments/{id}/pin", s.pinStudioComment)
	mux.HandleFunc("DELETE /api/v1/studio/comments/{id}/pin", s.unpinStudioComment)
	mux.HandleFunc("DELETE /api/v1/studio/comments/{id}", s.removeStudioComment)
	mux.HandleFunc("GET /api/v1/studio/monetization", s.studioMonetization)
	mux.HandleFunc("GET /api/v1/studio/earnings", s.studioEarnings)
	mux.HandleFunc("GET /api/v1/studio/payouts", s.studioPayouts)
	mux.HandleFunc("GET /api/v1/studio/billing/account", s.billingAccount)
	mux.HandleFunc("POST /api/v1/studio/billing/account", s.createBillingAccount)
	mux.HandleFunc("POST /api/v1/studio/billing/account/link", s.refreshBillingAccountLink)
	mux.HandleFunc("POST /api/v1/studio/billing/account/sync", s.syncBillingAccount)
	mux.HandleFunc("POST /webhooks/stripe", s.stripeWebhook)
	mux.HandleFunc("GET /api/v1/studio/analytics/summary", s.studioAnalyticsSummary)
	mux.HandleFunc("GET /api/v1/studio/analytics/timeseries", s.studioAnalyticsTimeseries)
	mux.HandleFunc("GET /api/v1/studio/videos/{id}/analytics", s.studioVideoAnalytics)
	mux.HandleFunc("POST /api/v1/studio/uploads", s.createStudioUpload)
	mux.HandleFunc("GET /api/v1/studio/uploads", s.listStudioUploads)
	mux.HandleFunc("GET /api/v1/studio/uploads/{id}", s.getStudioUpload)
	mux.HandleFunc("GET /api/v1/studio/uploads/{id}/scans", s.listStudioUploadScans)
	mux.HandleFunc("POST /api/v1/studio/uploads/{id}/parts", s.refreshStudioUploadParts)
	mux.HandleFunc("POST /api/v1/studio/uploads/{id}/complete", s.completeStudioUpload)
	mux.HandleFunc("DELETE /api/v1/studio/uploads/{id}", s.abortStudioUpload)

	mux.HandleFunc("GET /api/v1/studio/moderation/summary", s.moderationSummary)
	mux.HandleFunc("GET /api/v1/studio/moderation/cases", s.moderationCases)
	mux.HandleFunc("GET /api/v1/studio/moderation/cases/{id}", s.moderationCase)
	mux.HandleFunc("GET /api/v1/studio/moderation/actions", s.moderationActions)
	mux.HandleFunc("GET /api/v1/studio/moderation/actions/{id}", s.moderationAction)
	mux.HandleFunc("GET /api/v1/studio/moderation/appeals", s.moderationAppeals)
	mux.HandleFunc("GET /api/v1/studio/moderation/appeals/{id}", s.moderationAppeal)
	mux.HandleFunc("GET /api/v1/studio/moderation/ledger", s.moderationLedger)
	mux.HandleFunc("GET /api/v1/studio/moderation/ledger/verify", s.moderationLedgerVerify)
	mux.HandleFunc("GET /api/v1/studio/moderation/ledger/export", s.moderationLedgerExport)
	mux.HandleFunc("POST /api/v1/studio/moderation/actions/{id}/appeal", s.moderationFileAppeal)

	mux.HandleFunc("GET /api/v1/media/{id}/video.mp4", s.mediaMP4)
	mux.HandleFunc("GET /api/v1/media/{id}/thumbnail.jpg", s.mediaThumbnail)
	mux.HandleFunc("GET /api/v1/media/{id}/captions.vtt", s.mediaCaptions)
	mux.HandleFunc("GET /api/v1/media/{id}/subtitles/{track_id}", s.mediaSubtitleHandler)
	mux.HandleFunc("GET /api/v1/media/{id}/hls/master.m3u8", s.mediaMaster)
	mux.HandleFunc("GET /api/v1/media/{id}/hls/{rendition}/index.m3u8", s.mediaVariant)
	mux.HandleFunc("GET /api/v1/media/{id}/hls/{rendition}/{segment}", s.mediaSegmentHandler)

	if s.internalToken != "" {
		mux.HandleFunc("POST /internal/v1/notifications", s.ingestNotification)
		mux.HandleFunc("POST /internal/v1/finance/payouts", s.ingestFinancePayout)
		mux.HandleFunc("POST /internal/v1/billing/settlements", s.settleCreatorPayout)
		mux.HandleFunc("GET /internal/v1/federation", s.federationSnapshot)
		mux.HandleFunc("POST /internal/v1/federation/follow", s.federationFollow)
		mux.HandleFunc("POST /internal/v1/federation/unfollow", s.federationUnfollow)
		mux.HandleFunc("POST /internal/v1/federation/peers/status", s.federationPeerStatus)
		mux.HandleFunc("POST /internal/v1/federation/deliveries/process", s.federationProcessDeliveries)
	}

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
		mux.HandleFunc("GET /api/v1/auth/social/providers", s.socialProviders)
		mux.HandleFunc("GET /api/v1/auth/social/{provider}/start", s.socialStart)
		mux.HandleFunc("GET /api/v1/auth/social/{provider}/callback", s.socialCallback)
		mux.HandleFunc("POST /api/v1/auth/social/{provider}/callback", s.socialCallback)
		mux.HandleFunc("POST /api/v1/me/password", s.changePassword)
		mux.HandleFunc("GET /api/v1/me/sessions", s.listSessions)
		mux.HandleFunc("DELETE /api/v1/me/sessions/{id}", s.revokeSession)
		mux.HandleFunc("POST /api/v1/me/sessions/revoke-others", s.revokeOtherSessions)
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
	setPublicCache(w)
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
	if isPublicVideo(video) {
		setPublicCache(w)
	} else {
		setPrivateCache(w)
	}
	writeJSON(w, http.StatusOK, video)
}

func (s *Server) writeVideo(w http.ResponseWriter, r *http.Request, id string) {
	video, err := s.store.GetVideo(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	setPublicCache(w)
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
	setPublicCache(w)
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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
