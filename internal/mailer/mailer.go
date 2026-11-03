package mailer

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFrom     = "KeelTube <no-reply@keeltube.local>"
	defaultTimeout  = 10 * time.Second
	defaultProvider = "mailtrap"
)

// Message is a single transactional email. HTML is optional.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Mailer interface {
	Send(ctx context.Context, message Message) error
}

type Config struct {
	Provider     string
	APIKey       string
	Domain       string
	From         string
	BaseURL      string
	Timeout      time.Duration
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPSecurity string
}

// FromEnv builds a provider-backed mailer. SMTP requires an explicit host and
// credentials. Mailtrap and Mailgun fall back to the log mailer when no API key
// is configured so local development works without sending mail.
func FromEnv(getenv func(string) string) (Mailer, error) {
	config := Config{
		Provider:     strings.ToLower(strings.TrimSpace(getenv("KEELTUBE_MAIL_PROVIDER"))),
		APIKey:       strings.TrimSpace(getenv("KEELTUBE_MAIL_API_KEY")),
		Domain:       strings.TrimSpace(getenv("KEELTUBE_MAIL_DOMAIN")),
		From:         strings.TrimSpace(getenv("KEELTUBE_MAIL_FROM")),
		BaseURL:      strings.TrimSpace(getenv("KEELTUBE_MAIL_BASE_URL")),
		Timeout:      defaultTimeout,
		SMTPHost:     strings.TrimSpace(getenv("KEELTUBE_SMTP_HOST")),
		SMTPUsername: strings.TrimSpace(getenv("KEELTUBE_SMTP_USERNAME")),
		SMTPPassword: getenv("KEELTUBE_SMTP_PASSWORD"),
		SMTPSecurity: strings.TrimSpace(getenv("KEELTUBE_SMTP_SECURITY")),
	}
	if config.Provider == "" {
		config.Provider = defaultProvider
	}
	if config.From == "" {
		config.From = defaultFrom
	}
	if raw := strings.TrimSpace(getenv("KEELTUBE_MAIL_TIMEOUT_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return nil, fmt.Errorf("KEELTUBE_MAIL_TIMEOUT_SECONDS must be a positive integer")
		}
		config.Timeout = time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(getenv("KEELTUBE_SMTP_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("KEELTUBE_SMTP_PORT must be a number between 1 and 65535")
		}
		config.SMTPPort = port
	}

	switch config.Provider {
	case "log":
		return NewLog(os.Stderr), nil
	case "smtp":
		client, err := NewSMTP(config)
		if err != nil {
			return nil, err
		}
		return client, nil
	case "mailtrap":
		if config.APIKey == "" {
			log.Printf("mailer: KEELTUBE_MAIL_API_KEY is unset; using the log mailer")
			return NewLog(os.Stderr), nil
		}
		return NewMailtrap(config), nil
	case "mailgun":
		if config.APIKey == "" {
			log.Printf("mailer: KEELTUBE_MAIL_API_KEY is unset; using the log mailer")
			return NewLog(os.Stderr), nil
		}
		return NewMailgun(config), nil
	default:
		return nil, fmt.Errorf("unsupported KEELTUBE_MAIL_PROVIDER %q", config.Provider)
	}
}

type Log struct {
	logger *log.Logger
}

func NewLog(writer io.Writer) Log {
	return Log{logger: log.New(writer, "mailer: ", 0)}
}

func (l Log) Send(_ context.Context, message Message) error {
	// Do not log message bodies: they contain verification codes and reset tokens.
	l.logger.Printf("to=%s subject=%q", message.To, message.Subject)
	return nil
}

func splitFrom(value string) (name, address string) {
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return "", value
	}
	return parsed.Name, parsed.Address
}

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

func trimBaseURL(value, fallback string) string {
	if value == "" {
		value = fallback
	}
	return strings.TrimRight(value, "/")
}
