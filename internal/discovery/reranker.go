package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type Reranker interface {
	Name() string
	Rank(ctx context.Context, query string, videos []store.Video) ([]float64, error)
}

type LexicalReranker struct{}

func (LexicalReranker) Name() string { return "lexical" }

var rerankTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func (LexicalReranker) Rank(_ context.Context, query string, videos []store.Video) ([]float64, error) {
	queryTokens := tokenSet(query)
	scores := make([]float64, len(videos))
	if len(queryTokens) == 0 {
		return scores, nil
	}
	for index, video := range videos {
		title := tokenSet(video.Title)
		tags := tokenSet(strings.Join(video.Tags, " "))
		category := tokenSet(video.Category)
		description := tokenSet(video.Description)
		score := 0.0
		for token := range queryTokens {
			if title[token] {
				score += 3
			}
			if tags[token] {
				score += 2
			}
			if category[token] {
				score += 1.5
			}
			if description[token] {
				score += 0.5
			}
		}
		scores[index] = score / float64(len(queryTokens))
	}
	return scores, nil
}

func tokenSet(value string) map[string]bool {
	tokens := make(map[string]bool)
	for _, token := range rerankTokenPattern.FindAllString(strings.ToLower(value), -1) {
		if len(token) > 1 {
			tokens[token] = true
		}
	}
	return tokens
}

type HTTPReranker struct {
	url    string
	token  string
	model  string
	client *http.Client
}

func NewHTTPReranker(url, token, model string, timeout time.Duration) *HTTPReranker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if model == "" {
		model = "cross-encoder"
	}
	return &HTTPReranker{
		url:    strings.TrimRight(strings.TrimSpace(url), "/"),
		token:  token,
		model:  model,
		client: &http.Client{Timeout: timeout},
	}
}

func (r *HTTPReranker) Name() string { return r.model }

type rerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResponse struct {
	Scores []float64 `json:"scores"`
}

func (r *HTTPReranker) Rank(ctx context.Context, query string, videos []store.Video) ([]float64, error) {
	documents := make([]string, len(videos))
	for index, video := range videos {
		documents[index] = strings.Join([]string{
			video.Title, video.Description, video.Category, strings.Join(video.Tags, " "),
		}, "\n")
	}
	body, err := json.Marshal(rerankRequest{Query: query, Documents: documents})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call reranker: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("reranker returned %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	var decoded rerankResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode reranker response: %w", err)
	}
	if len(decoded.Scores) != len(videos) {
		return nil, fmt.Errorf("reranker returned %d scores for %d documents", len(decoded.Scores), len(videos))
	}
	return decoded.Scores, nil
}

func RerankerFromEnv(getenv func(string) string) Reranker {
	url := strings.TrimSpace(getenv("KEELTUBE_RERANKER_URL"))
	if url == "" {
		return LexicalReranker{}
	}
	timeout := 5 * time.Second
	if raw := strings.TrimSpace(getenv("KEELTUBE_RERANKER_TIMEOUT_SECONDS")); raw != "" {
		if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
			timeout = seconds
		}
	}
	return NewHTTPReranker(
		url,
		getenv("KEELTUBE_RERANKER_TOKEN"),
		getenv("KEELTUBE_RERANKER_MODEL"),
		timeout,
	)
}
