package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/api"
	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/config"
	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/mailer"
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

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}
	addr := config.EnvOrDefault("KEELTUBE_CORE_ADDR", "127.0.0.1:8080")
	dsn, err := config.RequireEnv("KEELTUBE_DATABASE_URL")
	if err != nil {
		log.Fatal(err)
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

	server := &http.Server{
		Addr: addr,
		Handler: api.New(api.Options{
			Store:      st,
			Auth:       authenticator,
			Identity:   identityService,
			Origins:    allowedOrigins(),
			RateLimit:  authRateLimit,
			RateWindow: authRateWindow,
			TrustProxy: trustProxy,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("keeltube core listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
