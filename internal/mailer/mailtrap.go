package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultMailtrapBaseURL = "https://send.api.mailtrap.io"

type Mailtrap struct {
	apiKey  string
	from    string
	baseURL string
	client  *http.Client
}

func NewMailtrap(config Config) Mailtrap {
	return Mailtrap{
		apiKey:  config.APIKey,
		from:    config.From,
		baseURL: trimBaseURL(config.BaseURL, defaultMailtrapBaseURL),
		client:  newHTTPClient(config.Timeout),
	}
}

type mailtrapAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type mailtrapRequest struct {
	From    mailtrapAddress   `json:"from"`
	To      []mailtrapAddress `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
}

func (m Mailtrap) Send(ctx context.Context, message Message) error {
	name, address := splitFrom(m.from)
	payload := mailtrapRequest{
		From:    mailtrapAddress{Email: address, Name: name},
		To:      []mailtrapAddress{{Email: message.To}},
		Subject: message.Subject,
		Text:    message.Text,
		HTML:    message.HTML,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mailtrap marshal: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/api/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mailtrap request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Api-Token", m.apiKey)

	response, err := m.client.Do(request)
	if err != nil {
		return fmt.Errorf("mailtrap send: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mailtrap returned %d: %s", response.StatusCode, string(detail))
	}
	return nil
}
