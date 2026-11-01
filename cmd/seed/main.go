package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/config"
	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
)

func main() {
	dsn := config.EnvOrDefault("KEELTUBE_DATABASE_URL", config.DefaultDatabaseURL)
	pipelineURL := strings.TrimRight(
		config.EnvOrDefault("KEELTUBE_PIPELINE_PUBLIC_URL", "http://localhost:8081"), "/",
	)
	webURL := strings.TrimRight(
		config.EnvOrDefault("KEELTUBE_WEB_PUBLIC_URL", "http://localhost:3000"), "/",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer conn.Close()

	if err := db.Migrate(ctx, conn); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	if err := seed.Run(ctx, store.New(conn), pipelineURL, webURL); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	log.Printf("seeded demo actor, channel, and videos")
}
