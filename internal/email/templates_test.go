package email_test

import (
	"strings"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/email"
)

func TestVerificationRendersBrandedHTMLAndText(t *testing.T) {
	message, err := email.Verification(email.Data{
		Name:      "Mara Vale",
		Email:     "mara@keeltube.com",
		Code:      "123456",
		ExpiresAt: time.Date(2026, 11, 3, 17, 38, 0, 0, time.UTC),
		ExpiresIn: 15 * time.Minute,
		AppURL:    "https://keeltube.com",
	})
	if err != nil {
		t.Fatalf("render verification: %v", err)
	}
	if message.Subject != "Your KeelTube verification code" {
		t.Fatalf("unexpected subject: %s", message.Subject)
	}

	for _, want := range []string{
		`lang="en"`,
		`alt="KeelTube"`,
		"data:image/png;base64,",
		"#9C599C",
		"#0F0F0F",
		"#1D1A1D",
		"#C494C4",
		"#262226",
		"@media (prefers-color-scheme: dark)",
		"email-logo-light",
		"email-logo-dark",
		"IBM Plex Mono",
		"Mara Vale",
		"123456",
		"3 November 2026 at 17:38 UTC",
		"15 minutes",
	} {
		if !strings.Contains(message.HTML, want) {
			t.Fatalf("html missing %q", want)
		}
	}
	if strings.Contains(message.HTML, "<script") {
		t.Fatal("html must not contain script tags")
	}
	if !strings.Contains(message.Text, "123456") || !strings.Contains(message.Text, "15 minutes") {
		t.Fatalf("unexpected text body: %s", message.Text)
	}
	if strings.Contains(message.Text, "<html") {
		t.Fatal("text body must not contain html")
	}
}

func TestPasswordResetRendersActionLink(t *testing.T) {
	link := "https://keeltube.com/reset-password?token=abc123&email=mara%40keeltube.com"
	message, err := email.PasswordReset(email.Data{
		Name:      "Mara Vale",
		Email:     "mara@keeltube.com",
		ActionURL: link,
		ExpiresAt: time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC),
		ExpiresIn: 30 * time.Minute,
		AppURL:    "https://keeltube.com",
	})
	if err != nil {
		t.Fatalf("render password reset: %v", err)
	}
	if message.Subject != "Reset your KeelTube password" {
		t.Fatalf("unexpected subject: %s", message.Subject)
	}
	escapedLink := "https://keeltube.com/reset-password?token=abc123&amp;email=mara%40keeltube.com"
	for _, want := range []string{
		"Choose a new password",
		escapedLink,
		"30 minutes",
		"3 November 2026 at 18:00 UTC",
	} {
		if !strings.Contains(message.HTML, want) {
			t.Fatalf("html missing %q", want)
		}
	}
	if !strings.Contains(message.Text, link) {
		t.Fatalf("text missing action link: %s", message.Text)
	}
}

func TestWelcomeRenders(t *testing.T) {
	message, err := email.Welcome(email.Data{
		Name:      "Mara Vale",
		Email:     "mara@keeltube.com",
		ActionURL: "https://keeltube.com/",
		AppURL:    "https://keeltube.com",
	})
	if err != nil {
		t.Fatalf("render welcome: %v", err)
	}
	if message.Subject != "Welcome to KeelTube" {
		t.Fatalf("unexpected subject: %s", message.Subject)
	}
	for _, want := range []string{
		"Your account is ready",
		"Open KeelTube",
		"https://keeltube.com/",
		"@media (prefers-color-scheme: dark)",
		"email-logo-light",
		"email-logo-dark",
	} {
		if !strings.Contains(message.HTML, want) {
			t.Fatalf("html missing %q", want)
		}
	}
	if !strings.Contains(message.Text, "Your email is verified") || !strings.Contains(message.Text, "https://keeltube.com/") {
		t.Fatalf("unexpected text body: %s", message.Text)
	}
}

func TestTemplatesEscapeUserControlledValues(t *testing.T) {
	message, err := email.Verification(email.Data{
		Name:      `<script>alert(1)</script>`,
		Email:     "mara@keeltube.com",
		Code:      "123456",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		ExpiresIn: 15 * time.Minute,
		AppURL:    "https://keeltube.com",
	})
	if err != nil {
		t.Fatalf("render verification: %v", err)
	}
	if strings.Contains(message.HTML, "<script>alert(1)</script>") {
		t.Fatal("user-controlled values must be escaped")
	}
	if !strings.Contains(message.HTML, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("expected escaped name in html")
	}
}

func TestVerificationUsesEmbeddedLogo(t *testing.T) {
	message, err := email.Verification(email.Data{
		Name:      "Mara Vale",
		Email:     "mara@keeltube.com",
		Code:      "123456",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		ExpiresIn: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("render verification: %v", err)
	}
	if strings.Count(message.HTML, "data:image/png;base64,") < 2 {
		t.Fatal("expected embedded light and dark logos")
	}
	if !strings.Contains(message.HTML, `alt="KeelTube"`) {
		t.Fatal("expected the logo alt text")
	}
}

func TestVerificationAllowsLogoOverride(t *testing.T) {
	message, err := email.Verification(email.Data{
		Name:        "Mara Vale",
		Email:       "mara@keeltube.com",
		Code:        "123456",
		ExpiresAt:   time.Now().Add(15 * time.Minute),
		ExpiresIn:   15 * time.Minute,
		LogoURL:     "https://example.com/logo-light.png",
		LogoDarkURL: "https://example.com/logo-dark.png",
	})
	if err != nil {
		t.Fatalf("render verification: %v", err)
	}
	if !strings.Contains(message.HTML, "https://example.com/logo-light.png") {
		t.Fatal("expected the light logo override")
	}
	if !strings.Contains(message.HTML, "https://example.com/logo-dark.png") {
		t.Fatal("expected the dark logo override")
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                   "a short time",
		30 * time.Second:    "a short time",
		time.Minute:         "1 minute",
		15 * time.Minute:    "15 minutes",
		time.Hour:           "1 hour",
		6 * time.Hour:       "6 hours",
		24 * time.Hour:      "1 day",
		48 * time.Hour:      "2 days",
		30 * 24 * time.Hour: "30 days",
	}
	for input, want := range cases {
		if got := email.HumanDuration(input); got != want {
			t.Fatalf("%s: expected %q, got %q", input, want, got)
		}
	}
}
