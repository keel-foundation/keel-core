package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTP struct {
	host     string
	port     int
	username string
	password string
	security string
	from     string
	timeout  time.Duration
}

func NewSMTP(config Config) (SMTP, error) {
	host := strings.TrimSpace(config.SMTPHost)
	if host == "" {
		return SMTP{}, fmt.Errorf("smtp requires KEELTUBE_SMTP_HOST")
	}
	port := config.SMTPPort
	if port == 0 {
		port = 587
	}
	if port < 1 || port > 65535 {
		return SMTP{}, fmt.Errorf("KEELTUBE_SMTP_PORT must be between 1 and 65535")
	}
	username := strings.TrimSpace(config.SMTPUsername)
	password := config.SMTPPassword
	if username == "" || password == "" {
		return SMTP{}, fmt.Errorf("smtp requires KEELTUBE_SMTP_USERNAME and KEELTUBE_SMTP_PASSWORD")
	}
	security := strings.ToLower(strings.TrimSpace(config.SMTPSecurity))
	if security == "" {
		security = "starttls"
	}
	switch security {
	case "starttls", "ssl", "none":
	default:
		return SMTP{}, fmt.Errorf("KEELTUBE_SMTP_SECURITY must be starttls, ssl, or none")
	}
	return SMTP{
		host:     host,
		port:     port,
		username: username,
		password: password,
		security: security,
		from:     config.From,
		timeout:  config.Timeout,
	}, nil
}

func (s SMTP) Send(ctx context.Context, message Message) error {
	recipient, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("smtp recipient: %w", err)
	}
	body, err := buildSMTPMessage(s.from, recipient.Address, message)
	if err != nil {
		return err
	}

	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	dialer := &net.Dialer{Timeout: timeout}

	var conn net.Conn
	if s.security == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: s.host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if s.security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: s.host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	_, fromAddress := splitFrom(s.from)
	if err := client.Mail(fromAddress); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func buildSMTPMessage(from, to string, message Message) ([]byte, error) {
	fromName, fromAddress := splitFrom(from)
	fromAddress = sanitizeHeader(fromAddress)
	if fromAddress == "" {
		return nil, fmt.Errorf("smtp from address is empty")
	}
	fromName = sanitizeHeader(fromName)
	to = sanitizeHeader(to)
	subject := sanitizeHeader(message.Subject)

	var builder strings.Builder
	builder.WriteString("From: ")
	if fromName != "" {
		builder.WriteString(mime.QEncoding.Encode("utf-8", fromName))
		builder.WriteString(" ")
	}
	builder.WriteString("<")
	builder.WriteString(fromAddress)
	builder.WriteString(">\r\n")
	builder.WriteString("To: <")
	builder.WriteString(to)
	builder.WriteString(">\r\n")
	builder.WriteString("Subject: ")
	builder.WriteString(mime.QEncoding.Encode("utf-8", subject))
	builder.WriteString("\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")

	if message.HTML != "" {
		const boundary = "keeltube-boundary"
		builder.WriteString("Content-Type: multipart/alternative; boundary=\"")
		builder.WriteString(boundary)
		builder.WriteString("\"\r\n\r\n")
		builder.WriteString("--")
		builder.WriteString(boundary)
		builder.WriteString("\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
		builder.WriteString(crlf(message.Text))
		builder.WriteString("\r\n--")
		builder.WriteString(boundary)
		builder.WriteString("\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n")
		builder.WriteString(crlf(message.HTML))
		builder.WriteString("\r\n--")
		builder.WriteString(boundary)
		builder.WriteString("--\r\n")
	} else {
		builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		builder.WriteString(crlf(message.Text))
		builder.WriteString("\r\n")
	}
	return []byte(builder.String()), nil
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.TrimSpace(value)
}

func crlf(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}
