package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/keel-foundation/keel-core/internal/config"
	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/discovery"
	"github.com/keel-foundation/keel-core/internal/media"
	"github.com/keel-foundation/keel-core/internal/moderation"
	"github.com/keel-foundation/keel-core/internal/notifications"
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
	mediaConfig, err := media.FromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("configure media: %v", err)
	}
	if !mediaConfig.Enabled() {
		log.Fatal("media storage is required for the worker; set KEELTUBE_MEDIA_S3_BUCKET")
	}
	retentionDays := 7
	if raw := strings.TrimSpace(os.Getenv("KEELTUBE_MEDIA_JOB_RETENTION_DAYS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			log.Fatal("KEELTUBE_MEDIA_JOB_RETENTION_DAYS must be a positive integer")
		}
		retentionDays = value
	}
	workerAddr := config.EnvOrDefault("KEELTUBE_WORKER_ADDR", "127.0.0.1:8083")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	cancel()

	st := store.New(conn)
	notificationService := notifications.New(st)
	discoveryEngine := discovery.NewEngine(
		st,
		discovery.EmbedderFromEnv(os.Getenv),
		discovery.DefaultConfig(),
	)
	mediaEngine, err := media.New(context.Background(), st, mediaConfig)
	if err != nil {
		log.Fatalf("configure media engine: %v", err)
	}
	mediaEngine.OnPublish = func(ctx context.Context, videoID string) error {
		var errs []error
		if err := discoveryEngine.RefreshVideo(ctx, videoID); err != nil {
			errs = append(errs, err)
		}
		if err := notificationService.VideoPublished(ctx, videoID); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	mediaEngine.OnFailure = func(ctx context.Context, videoID, message string) error {
		return notificationService.UploadFailed(ctx, videoID, message)
	}
	moderationTimeout := moderation.DefaultTimeout
	moderationClient := moderation.NewClient(
		config.EnvOrDefault("KEELTUBE_MODERATION_URL", ""),
		"",
		moderationTimeout,
	).WithSignalToken(os.Getenv("KEELTUBE_MODERATION_SIGNAL_TOKEN"))
	mediaEngine.OnScan = func(
		ctx context.Context,
		video store.Video,
		verdict, scanner string,
		findings []media.Finding,
		reason string,
	) error {
		if moderationClient == nil || !moderationClient.Enabled() {
			return nil
		}
		signalType := "other"
		text := strings.ToLower(scanner + " " + reason)
		if strings.Contains(text, "copyright") || strings.Contains(text, "blocklist") ||
			strings.Contains(text, "duplicate") {
			signalType = "copyright"
		}
		details := fmt.Sprintf(
			"%s scan returned %s: %s (findings: %d)", scanner, verdict, reason, len(findings),
		)
		_, err := moderationClient.Report(
			ctx, video.ChannelID, "video", video.ID, signalType, details, "",
		)
		return err
	}

	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mediaEngine.Start(runCtx)
	go startJobRetention(runCtx, st, retentionDays)

	status := &statusServer{store: st, workers: mediaConfig.Workers}
	server := &http.Server{
		Addr:              workerAddr,
		Handler:           status.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("keeltube media worker listening on %s with %d workers", workerAddr, mediaConfig.Workers)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func startJobRetention(ctx context.Context, st *store.Store, days int) {
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		before := time.Now().UTC().AddDate(0, 0, -days)
		total := int64(0)
		for {
			deleted, err := st.PruneMediaJobs(pruneCtx, before, 1000)
			if err != nil {
				log.Printf("media job retention: %v", err)
				return
			}
			total += deleted
			if deleted < 1000 {
				break
			}
		}
		if total > 0 {
			log.Printf("media job retention: removed %d completed jobs", total)
		}
	}
	prune()
	ticker := time.NewTicker(time.Hour)
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

type statusServer struct {
	store   *store.Store
	workers int
}

func (s *statusServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /status", s.status)
	return mux
}

func (s *statusServer) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeStatus(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "unavailable",
			"service": "media-worker",
		})
		return
	}
	writeStatus(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "media-worker",
	})
}

func (s *statusServer) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stats, err := s.store.MediaQueueStats(ctx)
	if err != nil {
		writeStatus(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable",
			"error":  "queue statistics unavailable",
		})
		return
	}
	writeStatus(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "media-worker",
		"workers": s.workers,
		"queue":   stats,
	})
}

func writeStatus(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
