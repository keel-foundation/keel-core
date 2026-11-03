package discovery_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/discovery"
)

func TestHTTPEmbedder(t *testing.T) {
	var received struct {
		Texts     []string `json:"texts"`
		InputType string   `json:"input_type"`
		Normalize bool     `json:"normalize"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      "test-model",
			"dimensions": 3,
			"vectors":    [][]float32{{1, 0, 0}, {0, 1, 0}},
		})
	}))
	defer server.Close()

	embedder := discovery.NewHTTPEmbedder(server.URL, "secret", "test-model", 3, time.Second)
	vectors, err := embedder.Embed(
		context.Background(),
		[]string{"first", "second"},
		discovery.InputTypeQuery,
	)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 3 {
		t.Fatalf("unexpected vectors: %+v", vectors)
	}
	if len(received.Texts) != 2 || received.InputType != "query" || !received.Normalize {
		t.Fatalf("unexpected request: %+v", received)
	}
}

func TestHTTPEmbedderRejectsDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      "test-model",
			"dimensions": 2,
			"vectors":    [][]float32{{1, 0}},
		})
	}))
	defer server.Close()

	embedder := discovery.NewHTTPEmbedder(server.URL, "", "test-model", 3, time.Second)
	if _, err := embedder.Embed(
		context.Background(), []string{"first"}, discovery.InputTypePassage,
	); err == nil {
		t.Fatal("expected a dimension mismatch error")
	}
}

func TestHTTPEmbedderRejectsServiceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	embedder := discovery.NewHTTPEmbedder(server.URL, "", "test-model", 3, time.Second)
	if _, err := embedder.Embed(
		context.Background(), []string{"first"}, discovery.InputTypePassage,
	); err == nil {
		t.Fatal("expected a service error")
	}
}

func TestHashEmbedder(t *testing.T) {
	embedder := discovery.NewHashEmbedder(256)
	first, err := embedder.Embed(
		context.Background(), []string{"lighthouse keeper documentary"}, discovery.InputTypePassage,
	)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	second, err := embedder.Embed(
		context.Background(), []string{"lighthouse keeper documentary"}, discovery.InputTypePassage,
	)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(first) != 1 || len(first[0]) != 256 {
		t.Fatalf("unexpected shape: %+v", first)
	}
	for index := range first[0] {
		if first[0][index] != second[0][index] {
			t.Fatalf("hash embeddings are not deterministic")
		}
	}
	var norm float64
	for _, value := range first[0] {
		norm += float64(value) * float64(value)
	}
	if norm < 0.99 || norm > 1.01 {
		t.Fatalf("expected a normalized vector, got norm %f", norm)
	}
}
