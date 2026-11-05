package media

import "testing"

func TestFromEnvUsesPublicEndpointForPresigning(t *testing.T) {
	env := map[string]string{
		"KEELTUBE_MEDIA_S3_BUCKET":          "keeltube",
		"KEELTUBE_MEDIA_S3_ENDPOINT":        "http://minio:9000",
		"KEELTUBE_MEDIA_S3_PUBLIC_ENDPOINT": "http://localhost:9000",
	}
	cfg, err := FromEnv(func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Endpoint != "http://minio:9000" {
		t.Fatalf("internal endpoint = %q", cfg.Endpoint)
	}
	if cfg.PublicEndpoint != "http://localhost:9000" {
		t.Fatalf("public endpoint = %q", cfg.PublicEndpoint)
	}
}

func TestFromEnvDefaultsPublicEndpointToInternal(t *testing.T) {
	env := map[string]string{
		"KEELTUBE_MEDIA_S3_BUCKET":   "keeltube",
		"KEELTUBE_MEDIA_S3_ENDPOINT": "https://account.r2.cloudflarestorage.com",
	}
	cfg, err := FromEnv(func(name string) string { return env[name] })
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.PublicEndpoint != cfg.Endpoint {
		t.Fatalf("public endpoint = %q, want %q", cfg.PublicEndpoint, cfg.Endpoint)
	}
	if !cfg.UsePathStyle {
		t.Fatalf("expected endpoint mode to enable path-style addressing")
	}
}
