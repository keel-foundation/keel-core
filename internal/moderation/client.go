package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds a single moderation request.
	DefaultTimeout = 10 * time.Second
	// maxResponseBytes protects the gateway from unbounded upstream responses.
	maxResponseBytes = 16 << 20
)

// ErrDisabled reports that no moderation service is configured.
var ErrDisabled = errors.New("moderation service is not configured")

// ErrSignalTokenMissing reports that viewer-report ingestion has no service credential.
var ErrSignalTokenMissing = errors.New("moderation signal token is not configured")

// Service is the creator-scoped moderation surface used by the gateway. It is an
// interface so tests and alternate transports can substitute an implementation.
type Service interface {
	Enabled() bool
	Summary(ctx context.Context, identity Identity, channelID string) (*Response, error)
	ListCases(ctx context.Context, identity Identity, channelID, status, videoID string, limit, offset int) (*Response, error)
	GetCase(ctx context.Context, identity Identity, channelID, caseID string) (*Response, error)
	ListActions(ctx context.Context, identity Identity, channelID, status, videoID string, limit, offset int) (*Response, error)
	GetAction(ctx context.Context, identity Identity, channelID, actionID string) (*Response, error)
	ListAppeals(ctx context.Context, identity Identity, channelID, status string, limit, offset int) (*Response, error)
	GetAppeal(ctx context.Context, identity Identity, channelID, appealID string) (*Response, error)
	Ledger(ctx context.Context, identity Identity, channelID string, limit, offset int) (*Response, error)
	VerifyLedger(ctx context.Context, identity Identity, channelID string) (*Response, error)
	ExportLedger(ctx context.Context, identity Identity, channelID, format string) (*Response, error)
	FileAppeal(ctx context.Context, identity Identity, channelID, actionID, grounds, statement string) (*Response, error)
	Report(ctx context.Context, channelID, subjectType, subjectID, reason, details, reporterID string) (*Response, error)
	PublicVideoRecord(ctx context.Context, videoID string) (*Response, error)
	Transparency(ctx context.Context, since, until string) (*Response, error)
}

// Response is a successful moderation response. The gateway forwards only the
// content headers the browser needs and the body unchanged.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Error is a non-2xx moderation response. Detail is the service's creator-safe
// error detail.
type Error struct {
	StatusCode int
	Detail     string
}

func (e *Error) Error() string {
	return fmt.Sprintf("moderation service returned %d: %s", e.StatusCode, e.Detail)
}

// Client calls the creator-scoped moderation endpoints with a server-side
// service credential. The browser never receives this credential.
type Client struct {
	baseURL     string
	token       string
	signalToken string
	claims      claimsConfig
	client      *http.Client
}

// NewClient constructs a moderation client. An empty base URL leaves the client
// disabled; the gateway then returns 503 instead of failing at startup.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

// WithSignalToken enables server-to-server viewer-report ingestion. It is a
// separate credential from creator gateway claims and must match moderation's
// KEELTUBE_SIGNAL_INGEST_TOKEN.
func (c *Client) WithSignalToken(token string) *Client {
	if c == nil {
		return c
	}
	c.signalToken = strings.TrimSpace(token)
	return c
}

func (c *Client) Summary(ctx context.Context, identity Identity, channelID string) (*Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/creator/summary", creatorQuery(channelID), identity, channelID, nil)
}

func (c *Client) ListCases(ctx context.Context, identity Identity, channelID, status, videoID string, limit, offset int) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/cases",
		listQuery(channelID, status, videoID, limit, offset), identity, channelID, nil,
	)
}

func (c *Client) GetCase(ctx context.Context, identity Identity, channelID, caseID string) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/cases/"+url.PathEscape(caseID),
		creatorQuery(channelID), identity, channelID, nil,
	)
}

func (c *Client) ListActions(ctx context.Context, identity Identity, channelID, status, videoID string, limit, offset int) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/actions",
		listQuery(channelID, status, videoID, limit, offset), identity, channelID, nil,
	)
}

func (c *Client) GetAction(ctx context.Context, identity Identity, channelID, actionID string) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/actions/"+url.PathEscape(actionID),
		creatorQuery(channelID), identity, channelID, nil,
	)
}

func (c *Client) ListAppeals(ctx context.Context, identity Identity, channelID, status string, limit, offset int) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/appeals",
		listQuery(channelID, status, "", limit, offset), identity, channelID, nil,
	)
}

func (c *Client) GetAppeal(ctx context.Context, identity Identity, channelID, appealID string) (*Response, error) {
	return c.do(
		ctx, http.MethodGet, "/v1/creator/appeals/"+url.PathEscape(appealID),
		creatorQuery(channelID), identity, channelID, nil,
	)
}

func (c *Client) Ledger(ctx context.Context, identity Identity, channelID string, limit, offset int) (*Response, error) {
	query := creatorQuery(channelID)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	return c.do(ctx, http.MethodGet, "/v1/creator/ledger", query, identity, channelID, nil)
}

func (c *Client) VerifyLedger(ctx context.Context, identity Identity, channelID string) (*Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/creator/ledger/verify", creatorQuery(channelID), identity, channelID, nil)
}

func (c *Client) ExportLedger(ctx context.Context, identity Identity, channelID, format string) (*Response, error) {
	query := creatorQuery(channelID)
	query.Set("format", format)
	return c.do(ctx, http.MethodGet, "/v1/creator/ledger/export", query, identity, channelID, nil)
}

func (c *Client) FileAppeal(ctx context.Context, identity Identity, channelID, actionID, grounds, statement string) (*Response, error) {
	return c.do(
		ctx, http.MethodPost, "/v1/actions/"+url.PathEscape(actionID)+"/appeal",
		creatorQuery(channelID), identity, channelID,
		appealRequest{Grounds: grounds, Statement: statement},
	)
}

func (c *Client) PublicVideoRecord(ctx context.Context, videoID string) (*Response, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	return c.doWithToken(
		ctx,
		http.MethodGet,
		"/v1/public/videos/"+url.PathEscape(videoID)+"/record",
		nil,
		"",
		nil,
	)
}

// Transparency returns the public moderation transparency report. It is
// unauthenticated upstream and carries no creator or moderator credential.
func (c *Client) Transparency(ctx context.Context, since, until string) (*Response, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	query := url.Values{}
	if since != "" {
		query.Set("since", since)
	}
	if until != "" {
		query.Set("until", until)
	}
	return c.doWithToken(ctx, http.MethodGet, "/v1/transparency", query, "", nil)
}

func (c *Client) Report(
	ctx context.Context,
	channelID, subjectType, subjectID, reason, details, reporterID string,
) (*Response, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(c.signalToken) == "" {
		return nil, ErrSignalTokenMissing
	}
	return c.doWithToken(
		ctx,
		http.MethodPost,
		"/v1/signals",
		nil,
		c.signalToken,
		signalRequest{
			SubjectType: subjectType,
			SubjectID:   subjectID,
			ChannelID:   channelID,
			Reason:      reason,
			Details:     details,
			ReporterID:  reporterID,
		},
	)
}

type appealRequest struct {
	Grounds   string `json:"grounds"`
	Statement string `json:"statement"`
}

type signalRequest struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	ChannelID   string `json:"channel_id"`
	Reason      string `json:"reason"`
	Details     string `json:"details"`
	ReporterID  string `json:"reporter_id,omitempty"`
}

func creatorQuery(channelID string) url.Values {
	return url.Values{"channel_id": []string{channelID}}
}

func listQuery(channelID, status, videoID string, limit, offset int) url.Values {
	query := creatorQuery(channelID)
	if status != "" {
		query.Set("status", status)
	}
	if videoID != "" {
		query.Set("video_id", videoID)
	}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	return query
}

func (c *Client) do(
	ctx context.Context,
	method, path string,
	query url.Values,
	identity Identity,
	channelID string,
	body any,
) (*Response, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	token := c.token
	if c.claimsEnabled() {
		var err error
		token, err = c.signClaims(identity, channelID, time.Now().UTC())
		if err != nil {
			return nil, err
		}
	}
	return c.doWithToken(ctx, method, path, query, token, body)
}

func (c *Client) doWithToken(
	ctx context.Context,
	method, path string,
	query url.Values,
	token string,
	body any,
) (*Response, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}

	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode moderation request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, fmt.Errorf("create moderation request: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/csv, application/x-ndjson")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.client.Do(request)
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, fmt.Errorf("call moderation service: %w: %v", context.DeadlineExceeded, err)
		}
		return nil, fmt.Errorf("call moderation service: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read moderation response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("moderation response exceeds %d bytes", maxResponseBytes)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &Error{
			StatusCode: response.StatusCode,
			Detail:     moderationDetail(data),
		}
	}

	return &Response{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       data,
	}, nil
}

func moderationDetail(data []byte) string {
	var payload struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(data, &payload); err == nil {
		if detail := strings.TrimSpace(payload.Detail); detail != "" {
			return truncateDetail(detail)
		}
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "moderation service request failed"
	}
	return truncateDetail(text)
}

func truncateDetail(value string) string {
	if runes := []rune(value); len(runes) > 500 {
		return string(runes[:500])
	}
	return value
}
