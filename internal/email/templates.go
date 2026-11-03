package email

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*.html
var htmlFiles embed.FS

//go:embed templates/*.txt
var textFiles embed.FS

//go:embed assets/keeltube-logo-light.png
var logoLightPNG []byte

//go:embed assets/keeltube-logo-dark.png
var logoDarkPNG []byte

type Message struct {
	Subject string
	Text    string
	HTML    string
}

type Data struct {
	Name        string
	Email       string
	Code        string
	ActionURL   string
	ExpiresAt   time.Time
	ExpiresIn   time.Duration
	AppURL      string
	LogoURL     string
	LogoDarkURL string
}

type view struct {
	Data
	Subject       string
	Preheader     string
	Eyebrow       string
	Heading       string
	FooterNote    string
	LogoURL       template.URL
	LogoDarkURL   template.URL
	ExpiresAtText string
	ExpiresInText string
}

func Verification(data Data) (Message, error) {
	return render("verification", view{
		Data:       data,
		Preheader:  "Your code expires in " + HumanDuration(data.ExpiresIn) + ".",
		Eyebrow:    "Verify your email",
		Heading:    "Confirm your email address",
		FooterNote: "If you did not create a KeelTube account, you can ignore this email.",
	})
}

func PasswordReset(data Data) (Message, error) {
	return render("password_reset", view{
		Data:       data,
		Preheader:  "This link expires in " + HumanDuration(data.ExpiresIn) + ".",
		Eyebrow:    "Reset your password",
		Heading:    "Choose a new password",
		FooterNote: "If you did not request a password reset, you can ignore this email. Your password has not changed.",
	})
}

func Welcome(data Data) (Message, error) {
	return render("welcome", view{
		Data:       data,
		Preheader:  "Your email is verified. Your account and channel are ready.",
		Eyebrow:    "Welcome",
		Heading:    "Welcome to KeelTube",
		FooterNote: "You are receiving this because you verified a KeelTube account.",
	})
}

func render(name string, value view) (Message, error) {
	value.Subject = subject(name)
	value.LogoURL = logoSrc(value.Data.LogoURL, logoLightPNG)
	value.LogoDarkURL = logoSrc(value.Data.LogoDarkURL, logoDarkPNG)
	value.ExpiresAtText = value.ExpiresAt.UTC().Format("2 January 2006 at 15:04 MST")
	value.ExpiresInText = HumanDuration(value.ExpiresIn)

	htmlTemplate, err := template.New("base.html").ParseFS(htmlFiles, "templates/base.html", "templates/"+name+".html")
	if err != nil {
		return Message{}, fmt.Errorf("parse %s html template: %w", name, err)
	}
	var htmlBody bytes.Buffer
	if err := htmlTemplate.ExecuteTemplate(&htmlBody, "base", value); err != nil {
		return Message{}, fmt.Errorf("render %s html template: %w", name, err)
	}

	textTemplate, err := texttemplate.New(name+".txt").ParseFS(textFiles, "templates/"+name+".txt")
	if err != nil {
		return Message{}, fmt.Errorf("parse %s text template: %w", name, err)
	}
	var textBody bytes.Buffer
	if err := textTemplate.ExecuteTemplate(&textBody, name+".txt", value); err != nil {
		return Message{}, fmt.Errorf("render %s text template: %w", name, err)
	}

	return Message{
		Subject: value.Subject,
		Text:    textBody.String(),
		HTML:    htmlBody.String(),
	}, nil
}

func logoSrc(override string, fallback []byte) template.URL {
	value := strings.TrimSpace(override)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "data:image/") {
		return template.URL(value)
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(fallback))
}

func subject(name string) string {
	switch name {
	case "verification":
		return "Your KeelTube verification code"
	case "password_reset":
		return "Reset your KeelTube password"
	case "welcome":
		return "Welcome to KeelTube"
	default:
		return "KeelTube"
	}
}

// HumanDuration formats a duration for transactional copy without exposing
// implementation-level precision.
func HumanDuration(value time.Duration) string {
	if value <= 0 {
		return "a short time"
	}
	if value >= 24*time.Hour {
		days := int(value.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	if value >= time.Hour {
		hours := int(value.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	minutes := int(value.Minutes())
	if minutes < 1 {
		return "a short time"
	}
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
