package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Channel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

type Playback struct {
	MP4URL       string `json:"mp4_url"`
	HLSURL       string `json:"hls_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

type Video struct {
	ID              string   `json:"id"`
	ChannelID       string   `json:"channel_id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	Visibility      string   `json:"visibility"`
	ContentHash     string   `json:"content_hash"`
	DurationSeconds int      `json:"duration_seconds"`
	Playback        Playback `json:"playback"`
	License         string   `json:"license"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

type Demo struct {
	Video   Video   `json:"video"`
	Channel Channel `json:"channel"`
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func pipelineBaseURL() string {
	return envOrDefault("TUBEKEEL_PIPELINE_PUBLIC_URL", "http://localhost:8081")
}

func allowedOrigins() []string {
	raw := envOrDefault("TUBEKEEL_CORS_ORIGINS", "http://localhost:3000")
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if candidate == "*" || candidate == origin {
			return true
		}
	}
	return false
}

func demoChannel() Channel {
	return Channel{ID: "ch_demo", Name: "Mara Vale", Handle: "@maravale"}
}

func demoVideo() Video {
	base := pipelineBaseURL()
	return Video{
		ID:              "vid_demo_001",
		ChannelID:       "ch_demo",
		Title:           "The Last Lighthouse",
		Description:     "A short original documentary about a keeper on his final night. Demo asset only.",
		Status:          "published",
		Visibility:      "public",
		ContentHash:     "sha256:4d1a52f8c6b6e1c0e6f0f3d3b4a0f6f0a8e0c2d4b6a8f0e2c4d6b8a0f2e4c6d8",
		DurationSeconds: 8,
		Playback: Playback{
			MP4URL: base + "/demo/sample.mp4",
		},
		License:   "cc-by",
		CreatedAt: "2026-10-31T00:00:00Z",
		UpdatedAt: "2026-10-31T00:00:00Z",
	}
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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

func newMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "core",
		})
	})

	mux.HandleFunc("GET /api/v1/channels/demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, demoChannel())
	})

	mux.HandleFunc("GET /api/v1/videos/demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, demoVideo())
	})

	mux.HandleFunc("GET /api/v1/demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Demo{Video: demoVideo(), Channel: demoChannel()})
	})

	return mux
}

func main() {
	addr := envOrDefault("TUBEKEEL_CORE_ADDR", "127.0.0.1:8080")
	server := &http.Server{
		Addr:              addr,
		Handler:           withCORS(newMux(), allowedOrigins()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("tubekeel core listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
