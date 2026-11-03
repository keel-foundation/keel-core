package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestWorkerStatusEndpoints(t *testing.T) {
	conn := testutil.Open(t)
	defer conn.Close()
	st := store.New(conn)
	handler := (&statusServer{store: st, workers: 2}).handler()

	for _, path := range []string{"/health", "/status"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode body: %v", path, err)
		}
		if body["status"] != "ok" || body["service"] != "media-worker" {
			t.Fatalf("%s: unexpected body: %+v", path, body)
		}
		if path == "/status" {
			if _, ok := body["queue"]; !ok {
				t.Fatalf("status response missing queue: %+v", body)
			}
		}
	}
}
