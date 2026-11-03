package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

var (
	ErrDisabled      = errors.New("media engine is not configured")
	ErrPolicyBlocked = errors.New("media blocked by policy")
	ErrInvalidUpload = errors.New("invalid upload")
)

const (
	VerdictAllow  = "allow"
	VerdictReview = "review"
	VerdictBlock  = "block"
)

type Finding struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Kind     string `json:"kind,omitempty"`
}

type PolicyError struct {
	Findings []Finding `json:"findings"`
}

func (e *PolicyError) Error() string {
	return ErrPolicyBlocked.Error()
}

func (e *PolicyError) Unwrap() error {
	return ErrPolicyBlocked
}

type ScanInput struct {
	VideoID         string   `json:"video_id"`
	ChannelID       string   `json:"channel_id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
	Filename        string   `json:"filename"`
	ContentType     string   `json:"content_type"`
	SizeBytes       int64    `json:"size_bytes"`
	ContentSHA256   string   `json:"content_sha256"`
	DurationSeconds float64  `json:"duration_seconds"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	VideoCodec      string   `json:"video_codec"`
	AudioCodec      string   `json:"audio_codec"`
}

type ScanResult struct {
	Scanner  string    `json:"scanner"`
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
}

type Scanner interface {
	Scan(ctx context.Context, input ScanInput) (ScanResult, error)
}

type MetadataScanner struct {
	Terms []string
}

func (s MetadataScanner) Scan(_ context.Context, input ScanInput) (ScanResult, error) {
	findings := make([]Finding, 0)
	title := strings.TrimSpace(input.Title)
	description := strings.TrimSpace(input.Description)
	if title == "" {
		findings = append(findings, Finding{Code: "title_required", Message: "A title is required.", Severity: "error", Kind: "policy"})
	}
	if len([]rune(title)) > 200 {
		findings = append(findings, Finding{Code: "title_too_long", Message: "The title is longer than 200 characters.", Severity: "error", Kind: "policy"})
	}
	if len([]rune(description)) > 5000 {
		findings = append(findings, Finding{Code: "description_too_long", Message: "The description is longer than 5000 characters.", Severity: "error", Kind: "policy"})
	}
	if len(input.Tags) > 20 {
		findings = append(findings, Finding{Code: "too_many_tags", Message: "Use no more than 20 tags.", Severity: "error", Kind: "policy"})
	}
	for _, tag := range input.Tags {
		if len([]rune(tag)) > 50 {
			findings = append(findings, Finding{Code: "tag_too_long", Message: "A tag is longer than 50 characters.", Severity: "error", Kind: "policy"})
			break
		}
		if store.HasControlCharacters(tag) {
			findings = append(findings, Finding{Code: "control_characters", Message: "Metadata contains unsupported control characters.", Severity: "error", Kind: "policy"})
			break
		}
	}
	if store.HasControlCharacters(title) || store.HasControlCharacters(description) {
		findings = append(findings, Finding{Code: "control_characters", Message: "Metadata contains unsupported control characters.", Severity: "error", Kind: "policy"})
	}
	if countLinks(title)+countLinks(description) > 3 {
		findings = append(findings, Finding{Code: "excessive_links", Message: "Metadata contains an excessive number of links.", Severity: "warning", Kind: "policy"})
	}
	haystack := strings.ToLower(title + "\n" + description + "\n" + strings.Join(input.Tags, " "))
	for _, term := range s.Terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(haystack, term) {
			findings = append(findings, Finding{
				Code:     "policy_term",
				Message:  fmt.Sprintf("Metadata matched a configured policy term: %s", term),
				Severity: "error",
				Kind:     "policy",
			})
		}
	}
	return ScanResult{Scanner: "metadata", Verdict: verdictFor(findings), Findings: findings}, nil
}

func verdictFor(findings []Finding) string {
	verdict := VerdictAllow
	for _, finding := range findings {
		if finding.Severity == "error" || finding.Severity == "critical" {
			return VerdictBlock
		}
		if finding.Severity == "warning" {
			verdict = VerdictReview
		}
	}
	return verdict
}

func countLinks(value string) int {
	lower := strings.ToLower(value)
	return strings.Count(lower, "http://") + strings.Count(lower, "https://") + strings.Count(lower, "www.")
}

type HashBlocklistScanner struct {
	Store *store.Store
}

func (s HashBlocklistScanner) Scan(ctx context.Context, input ScanInput) (ScanResult, error) {
	if input.ContentSHA256 == "" {
		return ScanResult{Scanner: "hash_blocklist", Verdict: VerdictAllow}, nil
	}
	kind, reason, blocked, err := s.Store.IsHashBlocked(ctx, input.ContentSHA256)
	if err != nil {
		return ScanResult{}, err
	}
	if !blocked {
		return ScanResult{Scanner: "hash_blocklist", Verdict: VerdictAllow}, nil
	}
	return ScanResult{
		Scanner: "hash_blocklist",
		Verdict: VerdictBlock,
		Findings: []Finding{{
			Code:     "blocked_hash",
			Message:  reason,
			Severity: "critical",
			Kind:     kind,
		}},
	}, nil
}

type DuplicateContentScanner struct {
	Store *store.Store
}

func (s DuplicateContentScanner) Scan(ctx context.Context, input ScanInput) (ScanResult, error) {
	if input.ContentSHA256 == "" {
		return ScanResult{Scanner: "duplicate_content", Verdict: VerdictAllow}, nil
	}
	video, found, err := s.Store.FindDuplicateVideoByHash(ctx, input.ContentSHA256, input.VideoID)
	if err != nil {
		return ScanResult{}, err
	}
	if !found || video.ChannelID == input.ChannelID {
		return ScanResult{Scanner: "duplicate_content", Verdict: VerdictAllow}, nil
	}
	return ScanResult{
		Scanner: "duplicate_content",
		Verdict: VerdictReview,
		Findings: []Finding{{
			Code:     "duplicate_content",
			Message:  "This file matches an existing published video on another channel and needs review.",
			Severity: "warning",
			Kind:     "copyright",
		}},
	}, nil
}

type HTTPPolicyScanner struct {
	URL     string
	Token   string
	Client  *http.Client
	Timeout time.Duration
}

func (s HTTPPolicyScanner) Scan(ctx context.Context, input ScanInput) (ScanResult, error) {
	if s.URL == "" {
		return ScanResult{Scanner: "external_policy", Verdict: VerdictAllow}, nil
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ScanResult{}, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return ScanResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		request.Header.Set("Authorization", "Bearer "+s.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return ScanResult{}, fmt.Errorf("external policy scanner unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ScanResult{}, fmt.Errorf("external policy scanner returned %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	var result ScanResult
	if err := decoder.Decode(&result); err != nil {
		return ScanResult{}, fmt.Errorf("external policy scanner response: %w", err)
	}
	switch result.Verdict {
	case VerdictAllow, VerdictReview, VerdictBlock:
	default:
		return ScanResult{}, fmt.Errorf("external policy scanner returned invalid verdict %q", result.Verdict)
	}
	result.Scanner = "external_policy"
	return result, nil
}

type CompositeScanner struct {
	Scanners []Scanner
}

func (s CompositeScanner) Scan(ctx context.Context, input ScanInput) (ScanResult, error) {
	verdict := VerdictAllow
	findings := make([]Finding, 0)
	names := make([]string, 0, len(s.Scanners))
	for _, scanner := range s.Scanners {
		result, err := scanner.Scan(ctx, input)
		if err != nil {
			return ScanResult{}, err
		}
		names = append(names, result.Scanner)
		findings = append(findings, result.Findings...)
		if result.Verdict == VerdictBlock {
			verdict = VerdictBlock
		} else if result.Verdict == VerdictReview && verdict == VerdictAllow {
			verdict = VerdictReview
		}
	}
	return ScanResult{
		Scanner:  strings.Join(names, "+"),
		Verdict:  verdict,
		Findings: findings,
	}, nil
}
