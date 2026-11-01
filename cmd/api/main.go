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
	"github.com/keel-foundation/keel-core/internal/store"
)

func allowedOrigins() []string {
	raw := config.EnvOrDefault("TUBEKEEL_CORS_ORIGINS", "http://localhost:3000")
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func main() {
	addr := config.EnvOrDefault("TUBEKEEL_CORE_ADDR", "127.0.0.1:8080")
	dsn := config.EnvOrDefault("TUBEKEEL_DATABASE_URL", config.DefaultDatabaseURL)

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

	authenticator := auth.NewDev(
		config.EnvOrDefault("TUBEKEEL_DEV_ACTOR_ID", "usr_demo"),
		os.Getenv("TUBEKEEL_DEV_ACTOR_TOKEN"),
	)

	server := &http.Server{
		Addr:              addr,
		Handler:           api.New(store.New(conn), authenticator, allowedOrigins()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("tubekeel core listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
