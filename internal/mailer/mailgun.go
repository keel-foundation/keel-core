package mailer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const defaultMailgunBaseURL = "https://api.mailgun.net"

type Mailgun struct {
	apiKey  string
	domain  string
	from    string
	baseURL string
	client  *http.Client
}

func NewMailgun(config Config) Mailgun {
	return Mailgun{
		apiKey:  config.APIKey,
		domain:  config.Domain,
		from:    config.From,
		baseURL: trimBaseURL(config.BaseURL, defaultMailgunBaseURL),
		client:  newHTTPClient(config.Timeout),
	}
}

func (m Mailgun) Send(ctx context.Context, message Message) error {
	if strings.TrimSpace(m.domain) == "" {
		return fmt.Errorf("mailgun requires KEELTUBE_MAIL_DOMAIN")
	}
	form := url.Values{}
	form.Set("from", m.from)
	form.Set("to", message.To)
	form.Set("subject", message.Subject)
	form.Set("text", message.Text)
	if message.HTML != "" {
		form.Set("html", message.HTML)
	}

	endpoint := fmt.Sprintf("%s/v3/%s/messages", m.baseURL, url.PathEscape(m.domain))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("mailgun request: %w", err)
	}
	request.SetBasicAuth("api", m.apiKey)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := m.client.Do(request)
	if err != nil {
		return fmt.Errorf("mailgun send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mailgun returned %d: %s", response.StatusCode, string(detail))
	}
	return nil
}
