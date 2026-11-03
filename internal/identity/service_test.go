package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/store"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

type captureMailer struct {
	mu       sync.Mutex
	messages []mailer.Message
}

func (m *captureMailer) Send(_ context.Context, message mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

func (m *captureMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

func (m *captureMailer) last(t *testing.T) mailer.Message {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		t.Fatal("expected a captured message")
	}
	return m.messages[len(m.messages)-1]
}

func newTestService(t *testing.T, cfg Config) (*Service, *store.Store, *captureMailer) {
	t.Helper()
	conn := testutil.Open(t)
	st := store.New(conn)
	messages := &captureMailer{}
	return New(st, messages, cfg), st, messages
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.ExposeCodes = true
	cfg.ResendCooldown = 0
	cfg.VerificationTTL = 15 * time.Minute
	cfg.ResetTTL = 30 * time.Minute
	cfg.SessionTTL = time.Hour
	cfg.MaxLoginAttempts = 3
	cfg.MaxVerificationAttempts = 3
	return cfg
}

func testPassword(t *testing.T) string {
	t.Helper()
	value, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate test password: %v", err)
	}
	return value
}

func registerUser(t *testing.T, service *Service) (RegisterResult, string) {
	t.Helper()
	password := testPassword(t)
	result, err := service.Register(context.Background(), RegisterInput{
		Name:     "Test User",
		Email:    "user@example.com",
		Password: password,
	}, RequestMeta{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return result, password
}

func verifyUser(t *testing.T, service *Service, result RegisterResult) SessionResult {
	t.Helper()
	session, err := service.VerifyEmail(context.Background(), VerifyEmailInput{
		Email: result.Email,
		Code:  result.DevCode,
	}, RequestMeta{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return session
}

var codePattern = regexp.MustCompile(`\b(\d{6})\b`)

func extractCode(t *testing.T, text string) string {
	t.Helper()
	match := codePattern.FindStringSubmatch(text)
	if len(match) != 2 {
		t.Fatalf("no verification code in %q", text)
	}
	return match[1]
}

func TestResendVerificationRotatesCode(t *testing.T) {
	service, _, messages := newTestService(t, testConfig())
	result, _ := registerUser(t, service)

	first := extractCode(t, messages.last(t).Text)
	if first != result.DevCode {
		t.Fatalf("first code mismatch: %s != %s", first, result.DevCode)
	}
	if _, err := service.ResendVerification(context.Background(), result.Email, RequestMeta{}); err != nil {
		t.Fatalf("resend: %v", err)
	}
	second := extractCode(t, messages.last(t).Text)
	if second == first {
		t.Fatal("expected a rotated verification code")
	}

	if _, err := service.VerifyEmail(context.Background(), VerifyEmailInput{
		Email: result.Email,
		Code:  first,
	}, RequestMeta{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("old code: expected ErrInvalidCode, got %v", err)
	}
	if _, err := service.VerifyEmail(context.Background(), VerifyEmailInput{
		Email: result.Email,
		Code:  second,
	}, RequestMeta{}); err != nil {
		t.Fatalf("new code: %v", err)
	}
}

func TestResendVerificationHonorsCooldown(t *testing.T) {
	cfg := testConfig()
	cfg.ResendCooldown = time.Hour
	service, _, messages := newTestService(t, cfg)
	result, _ := registerUser(t, service)
	before := messages.count()

	if _, err := service.ResendVerification(context.Background(), result.Email, RequestMeta{}); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if messages.count() != before {
		t.Fatal("expected no resend during the cooldown")
	}

	now := service.now()
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := service.ResendVerification(context.Background(), result.Email, RequestMeta{}); err != nil {
		t.Fatalf("resend after cooldown: %v", err)
	}
	if messages.count() != before+1 {
		t.Fatal("expected a resend after the cooldown")
	}
}

func TestVerifyEmailRejectsExpiredCode(t *testing.T) {
	cfg := testConfig()
	service, _, _ := newTestService(t, cfg)
	result, _ := registerUser(t, service)

	now := service.now()
	service.now = func() time.Time { return now.Add(cfg.VerificationTTL + time.Minute) }
	if _, err := service.VerifyEmail(context.Background(), VerifyEmailInput{
		Email: result.Email,
		Code:  result.DevCode,
	}, RequestMeta{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expected ErrInvalidCode, got %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	cfg := testConfig()
	service, _, _ := newTestService(t, cfg)
	result, _ := registerUser(t, service)
	session := verifyUser(t, service, result)

	if _, err := service.Session(context.Background(), session.Token); err != nil {
		t.Fatalf("valid session: %v", err)
	}

	now := service.now()
	service.now = func() time.Time { return now.Add(cfg.SessionTTL + time.Minute) }
	if _, err := service.Session(context.Background(), session.Token); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestForgotPasswordHidesUnknownAccounts(t *testing.T) {
	service, _, messages := newTestService(t, testConfig())
	result, err := service.ForgotPassword(context.Background(), "unknown@example.com", RequestMeta{})
	if err != nil {
		t.Fatalf("forgot password: %v", err)
	}
	if result.DevToken != "" {
		t.Fatal("must not reveal a token for an unknown account")
	}
	if messages.count() != 0 {
		t.Fatal("must not send mail for an unknown account")
	}
}

func TestResetPasswordRejectsExpiredToken(t *testing.T) {
	cfg := testConfig()
	service, _, _ := newTestService(t, cfg)
	result, _ := registerUser(t, service)
	verifyUser(t, service, result)

	forgot, err := service.ForgotPassword(context.Background(), result.Email, RequestMeta{})
	if err != nil {
		t.Fatalf("forgot password: %v", err)
	}
	if forgot.DevToken == "" {
		t.Fatal("expected a reset token")
	}

	now := service.now()
	service.now = func() time.Time { return now.Add(cfg.ResetTTL + time.Minute) }
	err = service.ResetPassword(context.Background(), ResetPasswordInput{
		Token:    forgot.DevToken,
		Password: testPassword(t),
	}, RequestMeta{})
	if !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("expected ErrInvalidResetToken, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	service, _, _ := newTestService(t, testConfig())
	cases := []RegisterInput{
		{Name: "", Email: "user@example.com", Password: testPassword(t)},
		{Name: "Test", Email: "not-an-email", Password: testPassword(t)},
		{Name: "Test", Email: "user@example.com", Password: strings.Repeat("a", 7)},
	}
	for _, input := range cases {
		if _, err := service.Register(context.Background(), input, RequestMeta{}); err == nil {
			t.Fatalf("expected a validation error for %+v", input)
		} else {
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected ValidationError, got %T", err)
			}
		}
	}
}

func TestRegistrationAllocatesUniqueHandles(t *testing.T) {
	service, _, _ := newTestService(t, testConfig())
	first, err := service.Register(context.Background(), RegisterInput{
		Name:     "Same Name",
		Email:    "one@example.com",
		Password: testPassword(t),
	}, RequestMeta{})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	second, err := service.Register(context.Background(), RegisterInput{
		Name:     "Same Name",
		Email:    "two@example.com",
		Password: testPassword(t),
	}, RequestMeta{})
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if first.User.Handle == second.User.Handle {
		t.Fatalf("expected unique handles, both were %s", first.User.Handle)
	}
	if !strings.HasPrefix(first.User.Handle, "@samename") {
		t.Fatalf("unexpected handle: %s", first.User.Handle)
	}
}

func TestLoginUnknownAccountIsGeneric(t *testing.T) {
	service, _, _ := newTestService(t, testConfig())
	_, err := service.Login(context.Background(), LoginInput{
		Email:    "unknown@example.com",
		Password: testPassword(t),
	}, RequestMeta{})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}
