package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/config"
	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/seed"
	"github.com/keel-foundation/keel-core/internal/store"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}
	dsn, err := config.RequireEnv("KEELTUBE_DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	pipelineURL := strings.TrimRight(
		config.EnvOrDefault("KEELTUBE_PIPELINE_PUBLIC_URL", "http://localhost:8081"), "/",
	)
	webURL := strings.TrimRight(
		config.EnvOrDefault("KEELTUBE_WEB_PUBLIC_URL", "http://localhost:3000"), "/",
	)
	demoEmail := strings.TrimSpace(os.Getenv("KEELTUBE_SEED_DEMO_EMAIL"))
	demoPassword := os.Getenv("KEELTUBE_SEED_DEMO_PASSWORD")
	if (demoEmail == "") != (demoPassword == "") {
		log.Fatal("set both KEELTUBE_SEED_DEMO_EMAIL and KEELTUBE_SEED_DEMO_PASSWORD, or neither")
	}

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
	if err := seed.Run(ctx, store.New(conn), pipelineURL, webURL, demoEmail, demoPassword); err != nil {
		log.Fatalf("seed database: %v", err)
	}
	log.Printf("seeded catalog fixtures and demo actor")
}
