package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "core",
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})

	addr := os.Getenv("TUBEKEEL_CORE_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("tubekeel core listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
