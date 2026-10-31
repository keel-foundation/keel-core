package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	withCORS(newMux(), []string{"http://localhost:3000"}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["service"] != "core" {
		t.Fatalf("unexpected health body: %v", body)
	}
}

func TestDemoVideoHasNoPlaybackDuration(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/videos/demo", nil)
	recorder := httptest.NewRecorder()
	withCORS(newMux(), []string{"http://localhost:3000"}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	var video map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &video); err != nil {
		t.Fatal(err)
	}
	playback, ok := video["playback"].(map[string]any)
	if !ok {
		t.Fatal("playback is not an object")
	}
	if _, exists := playback["duration_seconds"]; exists {
		t.Fatal("playback.duration_seconds is not allowed by the v0.1 schema")
	}
	if video["duration_seconds"] != float64(8) {
		t.Fatalf("unexpected duration: %v", video["duration_seconds"])
	}
}

func TestCORSOnlyReflectsConfiguredOrigins(t *testing.T) {
	allowed := []string{"http://localhost:3000"}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	withCORS(newMux(), allowed).ServeHTTP(recorder, request)

	if header := recorder.Header().Get("Access-Control-Allow-Origin"); header != "" {
		t.Fatalf("unexpected CORS header for disallowed origin: %q", header)
	}

	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	recorder = httptest.NewRecorder()
	withCORS(newMux(), allowed).ServeHTTP(recorder, request)

	if header := recorder.Header().Get("Access-Control-Allow-Origin"); header != "http://localhost:3000" {
		t.Fatalf("unexpected CORS header: %q", header)
	}
}

func TestDemoPayload(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/demo", nil)
	recorder := httptest.NewRecorder()
	newMux().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	var payload Demo
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Video.ID != "vid_demo_001" || payload.Channel.ID != "ch_demo" {
		t.Fatalf("unexpected demo payload: %+v", payload)
	}
}
