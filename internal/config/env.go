package config

import "os"

const DefaultDatabaseURL = "postgres://tubekeel:tubekeel-dev@127.0.0.1:5432/tubekeel?sslmode=disable"

func EnvOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
