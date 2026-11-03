package mailer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/mailer"
)

func testMessage() mailer.Message {
	return mailer.Message{
		To:      "recipient@example.com",
		Subject: "Verify your email",
		Text:    "Your code is 123456.",
		HTML:    "<p>Your code is 123456.</p>",
	}
}

func TestMailtrapSend(t *testing.T) {
	var path string
	var apiToken string
	var body map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		apiToken = r.Header.Get("Api-Token")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := mailer.NewMailtrap(mailer.Config{
		APIKey:  "test-api-key",
		From:    "KeelTube <no-reply@example.com>",
		BaseURL: server.URL,
		Timeout: time.Second,
	})
	if err := client.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}

	if path != "/api/send" {
		t.Fatalf("unexpected path: %s", path)
	}
	if apiToken != "test-api-key" {
		t.Fatalf("unexpected API token: %s", apiToken)
	}
	if body["subject"] != "Verify your email" {
		t.Fatalf("unexpected subject: %v", body["subject"])
	}
	from, _ := body["from"].(map[string]any)
	if from["email"] != "no-reply@example.com" {
		t.Fatalf("unexpected from: %v", from)
	}
	to, _ := body["to"].([]any)
	if len(to) != 1 {
		t.Fatalf("unexpected recipients: %v", to)
	}
}

func TestMailtrapReturnsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := mailer.NewMailtrap(mailer.Config{
		APIKey:  "test-api-key",
		From:    "KeelTube <no-reply@example.com>",
		BaseURL: server.URL,
		Timeout: time.Second,
	})
	err := client.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected a provider error, got %v", err)
	}
}

func TestMailgunSend(t *testing.T) {
	var path string
	var username, password string
	var form url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		username, password, _ = r.BasicAuth()
		_ = r.ParseForm()
		form = r.PostForm
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := mailer.NewMailgun(mailer.Config{
		APIKey:  "test-api-key",
		Domain:  "example.test",
		From:    "KeelTube <no-reply@example.com>",
		BaseURL: server.URL,
		Timeout: time.Second,
	})
	if err := client.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}

	if path != "/v3/example.test/messages" {
		t.Fatalf("unexpected path: %s", path)
	}
	if username != "api" || password != "test-api-key" {
		t.Fatalf("unexpected basic auth: %s/%s", username, password)
	}
	if form.Get("to") != "recipient@example.com" {
		t.Fatalf("unexpected recipient: %s", form.Get("to"))
	}
	if form.Get("subject") != "Verify your email" {
		t.Fatalf("unexpected subject: %s", form.Get("subject"))
	}
}

func TestMailgunRequiresDomain(t *testing.T) {
	client := mailer.NewMailgun(mailer.Config{
		APIKey:  "test-api-key",
		From:    "KeelTube <no-reply@example.com>",
		Timeout: time.Second,
	})
	if err := client.Send(context.Background(), testMessage()); err == nil {
		t.Fatal("expected a missing-domain error")
	}
}

func TestFromEnvFallsBackToLogWithoutKey(t *testing.T) {
	client, err := mailer.FromEnv(func(name string) string {
		if name == "KEELTUBE_MAIL_PROVIDER" {
			return "mailtrap"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if _, ok := client.(mailer.Log); !ok {
		t.Fatalf("expected a log mailer, got %T", client)
	}
}

func TestFromEnvRejectsUnknownProvider(t *testing.T) {
	client, err := mailer.FromEnv(func(name string) string {
		switch name {
		case "KEELTUBE_MAIL_PROVIDER":
			return "unknown"
		case "KEELTUBE_MAIL_API_KEY":
			return "test-api-key"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("expected an error, got %T", client)
	}
}
