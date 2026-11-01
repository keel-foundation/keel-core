package config

import "os"

const DefaultDatabaseURL = "postgres://keeltube:keeltube-dev@127.0.0.1:5432/keeltube?sslmode=disable"

func EnvOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
