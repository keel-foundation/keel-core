package media

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Bucket              string
	Region              string
	Endpoint            string
	AccessKeyID         string
	SecretAccessKey     string
	SessionToken        string
	UsePathStyle        bool
	KeyPrefix           string
	PublicURL           string
	PresignTTL          time.Duration
	UploadTTL           time.Duration
	PartSize            int64
	MaxUploadBytes      int64
	MaxDurationSeconds  int
	MinDurationSeconds  int
	Workers             int
	PollInterval        time.Duration
	JobTimeout          time.Duration
	JobLease            time.Duration
	FFmpegPath          string
	FFprobePath         string
	WorkDir             string
	MaxRenditionHeight  int
	HLSSegmentSeconds   int
	AllowedExtensions   map[string]bool
	AllowedContentTypes map[string]bool
	PolicyTerms         []string
	ScannerURL          string
	ScannerToken        string
	ScannerTimeout      time.Duration
}

func FromEnv(getenv func(string) string) (Config, error) {
	bucket := strings.TrimSpace(getenv("KEELTUBE_MEDIA_S3_BUCKET"))
	if bucket == "" {
		return Config{}, nil
	}
	endpoint := strings.TrimSpace(getenv("KEELTUBE_MEDIA_S3_ENDPOINT"))
	cfg := Config{
		Bucket:              bucket,
		Region:              envOrDefault(getenv, "KEELTUBE_MEDIA_S3_REGION", "us-east-1"),
		Endpoint:            endpoint,
		AccessKeyID:         strings.TrimSpace(getenv("KEELTUBE_MEDIA_S3_ACCESS_KEY_ID")),
		SecretAccessKey:     strings.TrimSpace(getenv("KEELTUBE_MEDIA_S3_SECRET_ACCESS_KEY")),
		SessionToken:        strings.TrimSpace(getenv("KEELTUBE_MEDIA_S3_SESSION_TOKEN")),
		UsePathStyle:        boolEnv(getenv, "KEELTUBE_MEDIA_S3_USE_PATH_STYLE", endpoint != ""),
		KeyPrefix:           strings.Trim(strings.TrimSpace(envOrDefault(getenv, "KEELTUBE_MEDIA_S3_PREFIX", "media")), "/"),
		PublicURL:           strings.TrimRight(envOrDefault(getenv, "KEELTUBE_MEDIA_PUBLIC_URL", "http://localhost:8080"), "/"),
		FFmpegPath:          envOrDefault(getenv, "KEELTUBE_MEDIA_FFMPEG", "ffmpeg"),
		FFprobePath:         envOrDefault(getenv, "KEELTUBE_MEDIA_FFPROBE", "ffprobe"),
		WorkDir:             strings.TrimSpace(getenv("KEELTUBE_MEDIA_WORK_DIR")),
		PolicyTerms:         splitCSV(getenv("KEELTUBE_MEDIA_POLICY_TERMS")),
		ScannerURL:          strings.TrimSpace(getenv("KEELTUBE_MEDIA_SCANNER_URL")),
		ScannerToken:        strings.TrimSpace(getenv("KEELTUBE_MEDIA_SCANNER_TOKEN")),
		AllowedExtensions:   stringSet(splitCSV(envOrDefault(getenv, "KEELTUBE_MEDIA_ALLOWED_EXTENSIONS", ".mp4,.mov,.m4v,.mkv,.webm,.avi"))),
		AllowedContentTypes: stringSet(splitCSV(envOrDefault(getenv, "KEELTUBE_MEDIA_ALLOWED_CONTENT_TYPES", "video/mp4,video/quicktime,video/x-m4v,video/x-matroska,video/webm,video/x-msvideo,application/octet-stream"))),
	}
	var err error
	if cfg.PresignTTL, err = durationEnv(getenv, "KEELTUBE_MEDIA_PRESIGN_SECONDS", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.UploadTTL, err = durationEnv(getenv, "KEELTUBE_MEDIA_UPLOAD_TTL_SECONDS", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.PartSize, err = megabytesEnv(getenv, "KEELTUBE_MEDIA_PART_SIZE_MB", 64); err != nil {
		return Config{}, err
	}
	if cfg.MaxUploadBytes, err = megabytesEnv(getenv, "KEELTUBE_MEDIA_MAX_UPLOAD_MB", 20480); err != nil {
		return Config{}, err
	}
	if cfg.MaxDurationSeconds, err = intEnv(getenv, "KEELTUBE_MEDIA_MAX_DURATION_SECONDS", 14400); err != nil {
		return Config{}, err
	}
	if cfg.MinDurationSeconds, err = intEnv(getenv, "KEELTUBE_MEDIA_MIN_DURATION_SECONDS", 1); err != nil {
		return Config{}, err
	}
	if cfg.Workers, err = intEnv(getenv, "KEELTUBE_MEDIA_WORKERS", 2); err != nil {
		return Config{}, err
	}
	if cfg.PollInterval, err = durationEnv(getenv, "KEELTUBE_MEDIA_POLL_SECONDS", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.JobTimeout, err = durationEnv(getenv, "KEELTUBE_MEDIA_JOB_TIMEOUT_SECONDS", time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.JobLease, err = durationEnv(getenv, "KEELTUBE_MEDIA_JOB_LEASE_SECONDS", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.MaxRenditionHeight, err = intEnv(getenv, "KEELTUBE_MEDIA_MAX_RENDITION_HEIGHT", 1080); err != nil {
		return Config{}, err
	}
	if cfg.HLSSegmentSeconds, err = intEnv(getenv, "KEELTUBE_MEDIA_HLS_SEGMENT_SECONDS", 6); err != nil {
		return Config{}, err
	}
	if cfg.ScannerTimeout, err = durationEnv(getenv, "KEELTUBE_MEDIA_SCANNER_TIMEOUT_SECONDS", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "media"
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = "http://localhost:8080"
	}
	if cfg.PartSize < 5*1024*1024 {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_PART_SIZE_MB must be at least 5")
	}
	if cfg.MaxUploadBytes < cfg.PartSize {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_MAX_UPLOAD_MB must be at least the part size")
	}
	if cfg.UploadTTL <= cfg.PresignTTL {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_UPLOAD_TTL_SECONDS must be greater than the presign lifetime")
	}
	if cfg.Workers < 1 || cfg.Workers > 64 {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_WORKERS must be between 1 and 64")
	}
	if cfg.HLSSegmentSeconds < 2 || cfg.HLSSegmentSeconds > 30 {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_HLS_SEGMENT_SECONDS must be between 2 and 30")
	}
	if cfg.MinDurationSeconds < 1 || cfg.MaxDurationSeconds <= cfg.MinDurationSeconds {
		return Config{}, fmt.Errorf("invalid media duration limits")
	}
	if cfg.MaxRenditionHeight < 270 || cfg.MaxRenditionHeight > 2160 {
		return Config{}, fmt.Errorf("KEELTUBE_MEDIA_MAX_RENDITION_HEIGHT must be between 270 and 2160")
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = os.TempDir()
	}
	return cfg, nil
}

func (c Config) Enabled() bool {
	return c.Bucket != ""
}

func envOrDefault(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}

func intEnv(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func durationEnv(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of seconds", name)
	}
	return time.Duration(seconds) * time.Second, nil
}

func megabytesEnv(getenv func(string) string, name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback * 1024 * 1024, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value * 1024 * 1024, nil
}

func boolEnv(getenv func(string) string, name string, fallback bool) bool {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[strings.ToLower(value)] = true
	}
	return set
}
