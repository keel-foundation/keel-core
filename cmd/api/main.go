package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/config"
	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/federation"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/media"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/notifications"
	"github.com/keel-foundation/keel-core/internal/store"
)

func allowedOrigins() []string {
	raw := config.EnvOrDefault("KEELTUBE_CORS_ORIGINS", "http://localhost:3000")
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

const (
	authRateLimit  = 10
	authRateWindow = time.Minute
	trustProxy     = false
)

func identityConfig() identity.Config {
	cfg := identity.DefaultConfig()
	cfg.SessionCookieName = config.EnvOrDefault("KEELTUBE_SESSION_COOKIE", cfg.SessionCookieName)
	cfg.CSRFCookieName = config.EnvOrDefault("KEELTUBE_CSRF_COOKIE", cfg.CSRFCookieName)
	cfg.CookieSecure = config.BoolEnv("KEELTUBE_COOKIE_SECURE", cfg.CookieSecure)
	cfg.CookieDomain = config.EnvOrDefault("KEELTUBE_COOKIE_DOMAIN", cfg.CookieDomain)
	cfg.AppURL = config.EnvOrDefault("KEELTUBE_APP_URL", cfg.AppURL)
	cfg.EmailLogoURL = config.EnvOrDefault("KEELTUBE_EMAIL_LOGO_URL", cfg.EmailLogoURL)
	cfg.EmailLogoDarkURL = config.EnvOrDefault("KEELTUBE_EMAIL_LOGO_DARK_URL", cfg.EmailLogoDarkURL)
	return cfg
}

func startTelemetryRetention(ctx context.Context, st *store.Store, days int) {
	if days <= 0 {
		return
	}
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := st.PruneTelemetry(pruneCtx, time.Now().UTC().AddDate(0, 0, -days))
		if err != nil {
			log.Printf("analytics retention: %v", err)
			return
		}
		if result.Sessions > 0 || result.ViewEvents > 0 || result.Discovery > 0 {
			log.Printf(
				"analytics retention: sessions=%d view_events=%d discovery_events=%d",
				result.Sessions, result.ViewEvents, result.Discovery,
			)
		}
	}
	prune()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}
	addr := config.EnvOrDefault("KEELTUBE_CORE_ADDR", "127.0.0.1:8080")
	dsn, err := config.RequireEnv("KEELTUBE_DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	analyticsRetentionDays := 90
	if raw := strings.TrimSpace(os.Getenv("KEELTUBE_ANALYTICS_RETENTION_DAYS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			log.Fatalf("KEELTUBE_ANALYTICS_RETENTION_DAYS must be a positive integer")
		}
		analyticsRetentionDays = value
	}
	moderationTimeout := moderation.DefaultTimeout
	if raw := strings.TrimSpace(os.Getenv("KEELTUBE_MODERATION_TIMEOUT_SECONDS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			log.Fatalf("KEELTUBE_MODERATION_TIMEOUT_SECONDS must be a positive integer")
		}
		moderationTimeout = time.Duration(value) * time.Second
	}
	moderationClaimsTTL := time.Minute
	if raw := strings.TrimSpace(os.Getenv("KEELTUBE_MODERATION_CLAIMS_TTL_SECONDS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			log.Fatalf("KEELTUBE_MODERATION_CLAIMS_TTL_SECONDS must be a positive integer")
		}
		moderationClaimsTTL = time.Duration(value) * time.Second
	}
	moderationClaimsSecret := strings.TrimSpace(os.Getenv("KEELTUBE_MODERATION_CLAIMS_SECRET"))
	if moderationClaimsSecret != "" && len(moderationClaimsSecret) < 32 {
		log.Fatalf("KEELTUBE_MODERATION_CLAIMS_SECRET must be at least 32 characters")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer conn.Close()

	if err := db.Migrate(ctx, conn); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	mailerClient, err := mailer.FromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("configure mailer: %v", err)
	}

	st := store.New(conn)
	identityService := identity.New(st, mailerClient, identityConfig())
	socialConfig, err := identity.SocialConfigFromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("configure social sign-in: %v", err)
	}
	var socialService *identity.SocialService
	if len(socialConfig.Providers) > 0 {
		socialService = identity.NewSocialService(st, identityService, socialConfig)
	}
	federationService, err := federation.New(ctx, st, federation.ConfigFromEnv(os.Getenv))
	if err != nil {
		log.Fatalf("configure federation: %v", err)
	}
	notificationService := notifications.New(st)
	discoveryEngine := discovery.NewEngine(
		st,
		discovery.EmbedderFromEnv(os.Getenv),
		discovery.DefaultConfig(),
	)
	moderationClient := moderation.NewClient(
		config.EnvOrDefault("KEELTUBE_MODERATION_URL", ""),
		os.Getenv("KEELTUBE_MODERATION_TOKEN"),
		moderationTimeout,
	)
	moderationClient.WithClaims(
		moderationClaimsSecret,
		config.EnvOrDefault("KEELTUBE_MODERATION_CLAIMS_ISSUER", "keel-core"),
		config.EnvOrDefault("KEELTUBE_MODERATION_CLAIMS_AUDIENCE", "keel-moderation"),
		moderationClaimsTTL,
	)
	moderationClient.WithSignalToken(os.Getenv("KEELTUBE_MODERATION_SIGNAL_TOKEN"))

	mediaConfig, err := media.FromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("configure media: %v", err)
	}
	var mediaEngine *media.Engine
	if mediaConfig.Enabled() {
		mediaEngine, err = media.New(ctx, st, mediaConfig)
		if err != nil {
			log.Fatalf("configure media engine: %v", err)
		}
		mediaEngine.OnFailure = func(ctx context.Context, videoID, message string) error {
			return notificationService.UploadFailed(ctx, videoID, message)
		}
	}

	devAuthenticator := auth.NewDev(
		config.EnvOrDefault("KEELTUBE_DEV_ACTOR_ID", "usr_demo"),
		os.Getenv("KEELTUBE_DEV_ACTOR_TOKEN"),
	)
	allowDev := config.BoolEnv("KEELTUBE_AUTH_ALLOW_DEV", os.Getenv("KEELTUBE_DEV_ACTOR_TOKEN") != "")
	authenticator := auth.Fallback{
		Primary:        identityService,
		Secondary:      devAuthenticator,
		AllowSecondary: allowDev,
	}

	var studioMedia api.StudioMedia
	if mediaEngine != nil {
		studioMedia = mediaEngine
	}
	server := &http.Server{
		Addr: addr,
		Handler: api.New(api.Options{
			Store:         st,
			Auth:          authenticator,
			Identity:      identityService,
			Social:        socialService,
			Discovery:     discoveryEngine,
			Reranker:      discovery.RerankerFromEnv(os.Getenv),
			Notifications: notificationService,
			Moderation:    moderationClient,
			Media:         studioMedia,
			Federation:    federationService,
			InternalToken: strings.TrimSpace(os.Getenv("KEELTUBE_INTERNAL_TOKEN")),
			Origins:       allowedOrigins(),
			RateLimit:     authRateLimit,
			RateWindow:    authRateWindow,
			TrustProxy:    trustProxy,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Media workers run in cmd/worker. The API process only signs uploads and
	// serves playback metadata, so scaling HTTP replicas never multiplies FFmpeg.
	go startTelemetryRetention(runCtx, st, analyticsRetentionDays)
	go startScheduledPublisher(runCtx, st, discoveryEngine, notificationService, federationService)
	go startFederationDeliveries(runCtx, federationService)

	go func() {
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("keeltube core listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func startScheduledPublisher(
	ctx context.Context,
	st *store.Store,
	discoveryEngine *discovery.Engine,
	notificationService *notifications.Service,
	federationService *federation.Service,
) {
	publish := func() {
		publishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		videos, err := st.PublishScheduledVideos(publishCtx, time.Now().UTC())
		if err != nil {
			log.Printf("scheduled publish: %v", err)
			return
		}
		for _, video := range videos {
			if discoveryEngine != nil {
				if err := discoveryEngine.RefreshVideo(publishCtx, video.ID); err != nil {
					log.Printf("scheduled publish: refresh %s: %v", video.ID, err)
				}
			}
			if notificationService != nil {
				if err := notificationService.VideoPublished(publishCtx, video.ID); err != nil {
					log.Printf("scheduled publish: notify %s: %v", video.ID, err)
				}
			}
			if federationService != nil {
				federationService.PublishVideo(publishCtx, video)
			}
		}
	}
	publish()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

func startFederationDeliveries(ctx context.Context, service *federation.Service) {
	if service == nil {
		return
	}
	process := func() {
		deliveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		service.ProcessDeliveries(deliveryCtx, 20)
	}
	process()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}
