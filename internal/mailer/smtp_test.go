package mailer_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/mailer"
)

func TestSMTPConfigValidation(t *testing.T) {
	cases := []mailer.Config{
		{SMTPUsername: "user", SMTPPassword: "pass"},
		{SMTPHost: "smtp.example.com", SMTPPassword: "pass"},
		{SMTPHost: "smtp.example.com", SMTPUsername: "user"},
		{SMTPHost: "smtp.example.com", SMTPUsername: "user", SMTPPassword: "pass", SMTPSecurity: "bogus"},
	}
	for _, config := range cases {
		if _, err := mailer.NewSMTP(config); err == nil {
			t.Fatalf("expected a validation error for %+v", config)
		}
	}

	if _, err := mailer.NewSMTP(mailer.Config{
		SMTPHost:     "smtp.example.com",
		SMTPUsername: "user",
		SMTPPassword: "pass",
	}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestSMTPSend(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		reply := func(value string) {
			_, _ = writer.WriteString(value)
			_ = writer.Flush()
		}

		reply("220 localhost ESMTP\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"):
				reply("250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(command, "AUTH PLAIN"):
				reply("235 2.7.0 Authentication successful\r\n")
			case strings.HasPrefix(command, "MAIL FROM"):
				reply("250 OK\r\n")
			case strings.HasPrefix(command, "RCPT TO"):
				reply("250 OK\r\n")
			case command == "DATA":
				reply("354 End data with <CR><LF>.<CR><LF>\r\n")
				var data strings.Builder
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if dataLine == ".\r\n" {
						break
					}
					data.WriteString(dataLine)
				}
				received <- data.String()
				reply("250 OK\r\n")
			case command == "QUIT":
				reply("221 Bye\r\n")
				return
			default:
				reply("250 OK\r\n")
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	client, err := mailer.NewSMTP(mailer.Config{
		SMTPHost:     "127.0.0.1",
		SMTPPort:     port,
		SMTPUsername: "smtp-user",
		SMTPPassword: "smtp-pass",
		SMTPSecurity: "none",
		From:         "KeelTube <no-reply@example.com>",
		Timeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("new smtp: %v", err)
	}
	if err := client.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case body := <-received:
		for _, want := range []string{
			"From: KeelTube <no-reply@example.com>",
			"To: <recipient@example.com>",
			"Subject: Verify your email",
			"Your code is 123456.",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("message missing %q:\n%s", want, body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the SMTP message")
	}
}

func TestSMTPRejectsInvalidRecipient(t *testing.T) {
	client, err := mailer.NewSMTP(mailer.Config{
		SMTPHost:     "127.0.0.1",
		SMTPUsername: "smtp-user",
		SMTPPassword: "smtp-pass",
		SMTPSecurity: "none",
		From:         "KeelTube <no-reply@example.com>",
	})
	if err != nil {
		t.Fatalf("new smtp: %v", err)
	}
	if err := client.Send(context.Background(), mailer.Message{To: "not-an-email"}); err == nil {
		t.Fatal("expected an invalid-recipient error")
	}
}
