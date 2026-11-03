package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultDimensions = 384

type InputType string

const (
	InputTypePassage InputType = "passage"
	InputTypeQuery   InputType = "query"
)

type Embedder interface {
	Model() string
	Dimensions() int
	Embed(ctx context.Context, texts []string, inputType InputType) ([][]float32, error)
}

type HTTPEmbedder struct {
	baseURL    string
	token      string
	model      string
	dimensions int
	client     *http.Client
}

type embedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
	Normalize bool     `json:"normalize"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Dimensions int         `json:"dimensions"`
	Vectors    [][]float32 `json:"vectors"`
}

func NewHTTPEmbedder(baseURL, token, model string, dimensions int, timeout time.Duration) *HTTPEmbedder {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if dimensions <= 0 {
		dimensions = DefaultDimensions
	}
	return &HTTPEmbedder{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		model:      model,
		dimensions: dimensions,
		client:     &http.Client{Timeout: timeout},
	}
}

func (e *HTTPEmbedder) Model() string {
	return e.model
}

func (e *HTTPEmbedder) Dimensions() int {
	return e.dimensions
}

func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string, inputType InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	body, err := json.Marshal(embedRequest{
		Texts:     texts,
		InputType: string(inputType),
		Normalize: true,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, e.baseURL+"/v1/embed", bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		request.Header.Set("Authorization", "Bearer "+e.token)
	}

	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call embeddings service: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf(
			"embeddings service returned %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(detail)),
		)
	}

	var decoded embedResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if decoded.Model != "" && decoded.Model != e.model {
		return nil, fmt.Errorf(
			"embeddings service model %q does not match configured model %q",
			decoded.Model,
			e.model,
		)
	}
	if decoded.Dimensions != 0 && decoded.Dimensions != e.dimensions {
		return nil, fmt.Errorf(
			"embeddings service dimensions %d do not match configured dimensions %d",
			decoded.Dimensions,
			e.dimensions,
		)
	}
	if len(decoded.Vectors) != len(texts) {
		return nil, fmt.Errorf(
			"embeddings service returned %d vectors for %d texts",
			len(decoded.Vectors),
			len(texts),
		)
	}
	for index, vector := range decoded.Vectors {
		if len(vector) != e.dimensions {
			return nil, fmt.Errorf(
				"vector %d has %d dimensions; expected %d",
				index,
				len(vector),
				e.dimensions,
			)
		}
	}
	return decoded.Vectors, nil
}

type HashEmbedder struct {
	dimensions int
}

var hashTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func NewHashEmbedder(dimensions int) *HashEmbedder {
	if dimensions <= 0 {
		dimensions = DefaultDimensions
	}
	return &HashEmbedder{dimensions: dimensions}
}

func (e *HashEmbedder) Model() string {
	return "hash-embedder"
}

func (e *HashEmbedder) Dimensions() int {
	return e.dimensions
}

func (e *HashEmbedder) Embed(_ context.Context, texts []string, _ InputType) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		vectors[index] = e.embedText(text)
	}
	return vectors, nil
}

func (e *HashEmbedder) embedText(text string) []float32 {
	vector := make([]float32, e.dimensions)
	tokens := hashTokenPattern.FindAllString(strings.ToLower(text), -1)
	features := make([]string, 0, len(tokens)*2)
	features = append(features, tokens...)
	for index := 0; index+1 < len(tokens); index++ {
		features = append(features, tokens[index]+"_"+tokens[index+1])
	}
	if len(features) == 0 {
		features = append(features, strings.ToLower(strings.TrimSpace(text)))
	}
	for index, feature := range features {
		weight := float32(1)
		if index >= len(tokens) {
			weight = 0.5
		}
		position, sign := hashFeature(feature, e.dimensions)
		vector[position] += sign * weight
	}
	normalizeVector(vector)
	return vector
}

func hashFeature(feature string, dimensions int) (int, float32) {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(feature))
	value := hasher.Sum64()
	position := int(value % uint64(dimensions))
	sign := float32(1)
	if (value>>1)&1 == 1 {
		sign = -1
	}
	return position, sign
}

func normalizeVector(vector []float32) {
	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	if sum == 0 {
		if len(vector) > 0 {
			vector[0] = 1
		}
		return
	}
	norm := float32(1 / math.Sqrt(sum))
	for index := range vector {
		vector[index] *= norm
	}
}

func EmbedderFromEnv(getenv func(string) string) Embedder {
	baseURL := strings.TrimSpace(getenv("KEELTUBE_EMBEDDINGS_URL"))
	dimensions := envInt(getenv, "KEELTUBE_EMBEDDINGS_DIMENSIONS", DefaultDimensions)
	if baseURL == "" {
		return NewHashEmbedder(dimensions)
	}
	model := strings.TrimSpace(getenv("KEELTUBE_EMBEDDINGS_MODEL"))
	if model == "" {
		model = "BAAI/bge-small-en-v1.5"
	}
	timeout := time.Duration(envInt(getenv, "KEELTUBE_EMBEDDINGS_TIMEOUT_SECONDS", 30)) * time.Second
	return NewHTTPEmbedder(
		baseURL,
		strings.TrimSpace(getenv("KEELTUBE_EMBEDDINGS_TOKEN")),
		model,
		dimensions,
		timeout,
	)
}

func envInt(getenv func(string) string, name string, fallback int) int {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
