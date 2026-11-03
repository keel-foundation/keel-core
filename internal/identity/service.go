package identity

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/email"
	"github.com/keel-foundation/keel-core/internal/mailer"
	"github.com/keel-foundation/keel-core/internal/store"
)

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailNotVerified   = errors.New("email not verified")
	ErrInvalidCode        = errors.New("invalid or expired verification code")
	ErrTooManyAttempts    = errors.New("too many attempts")
	ErrInvalidResetToken  = errors.New("invalid or expired reset token")
)

var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

type Config struct {
	SessionCookieName       string
	CSRFCookieName          string
	CookieSecure            bool
	CookieDomain            string
	SessionTTL              time.Duration
	VerificationTTL         time.Duration
	ResetTTL                time.Duration
	ResendCooldown          time.Duration
	MaxLoginAttempts        int
	LockDuration            time.Duration
	MaxVerificationAttempts int
	AppURL                  string
	EmailLogoURL            string
	EmailLogoDarkURL        string
	ExposeCodes             bool
}

const (
	defaultSessionTTL              = 30 * 24 * time.Hour
	defaultVerificationTTL         = 15 * time.Minute
	defaultResetTTL                = 30 * time.Minute
	defaultResendCooldown          = 60 * time.Second
	defaultMaxLoginAttempts        = 5
	defaultLockDuration            = 15 * time.Minute
	defaultMaxVerificationAttempts = 5
	exposeCodes                    = false
)

func DefaultConfig() Config {
	return Config{
		SessionCookieName:       "keeltube_session",
		CSRFCookieName:          "keeltube_csrf",
		CookieSecure:            false,
		SessionTTL:              defaultSessionTTL,
		VerificationTTL:         defaultVerificationTTL,
		ResetTTL:                defaultResetTTL,
		ResendCooldown:          defaultResendCooldown,
		MaxLoginAttempts:        defaultMaxLoginAttempts,
		LockDuration:            defaultLockDuration,
		MaxVerificationAttempts: defaultMaxVerificationAttempts,
		AppURL:                  "http://localhost:3000",
		ExposeCodes:             exposeCodes,
	}
}

type RequestMeta struct {
	UserAgent string
	IPAddress string
}

type User struct {
	ID            string `json:"id"`
	AccountID     string `json:"account_id"`
	Name          string `json:"name"`
	Handle        string `json:"handle"`
	Role          string `json:"role"`
	ChannelID     string `json:"channel_id"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

type SessionResult struct {
	Token      string    `json:"-"`
	CSRFToken  string    `json:"-"`
	User       User      `json:"user"`
	Provider   string    `json:"provider"`
	SignedInAt time.Time `json:"signed_in_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type RegisterInput struct {
	Name     string
	Email    string
	Password string
}

type RegisterResult struct {
	User                  User      `json:"user"`
	Email                 string    `json:"email"`
	VerificationRequired  bool      `json:"verification_required"`
	VerificationExpiresAt time.Time `json:"verification_expires_at"`
	DevCode               string    `json:"dev_code,omitempty"`
}

type VerifyEmailInput struct {
	Email string
	Code  string
}

type LoginInput struct {
	Email    string
	Password string
}

type ResetPasswordInput struct {
	Token    string
	Password string
}

type ForgotPasswordResult struct {
	DevToken  string    `json:"dev_token,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type ResendVerificationResult struct {
	DevCode   string    `json:"dev_code,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type Service struct {
	store  *store.Store
	mailer mailer.Mailer
	cfg    Config
	now    func() time.Time
}

func New(st *store.Store, mailerClient mailer.Mailer, cfg Config) *Service {
	return &Service{
		store:  st,
		mailer: mailerClient,
		cfg:    cfg,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Config() Config { return s.cfg }

func (s *Service) Register(ctx context.Context, input RegisterInput, meta RequestMeta) (RegisterResult, error) {
	name := strings.TrimSpace(input.Name)
	email := normalizeEmail(input.Email)
	if err := validateName(name); err != nil {
		return RegisterResult{}, err
	}
	if err := validateEmail(email); err != nil {
		return RegisterResult{}, err
	}
	if err := validatePassword(input.Password); err != nil {
		return RegisterResult{}, err
	}

	if _, err := s.store.GetAccountByEmail(ctx, email); err == nil {
		return RegisterResult{}, ErrEmailTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return RegisterResult{}, err
	}

	now := s.now()
	passwordHash, err := auth.HashPassword(input.Password)
	if err != nil {
		return RegisterResult{}, err
	}
	actorID, err := auth.NewID("usr")
	if err != nil {
		return RegisterResult{}, err
	}
	channelID, err := auth.NewID("ch")
	if err != nil {
		return RegisterResult{}, err
	}
	accountID, err := auth.NewID("acct")
	if err != nil {
		return RegisterResult{}, err
	}
	handle, err := s.uniqueHandle(ctx, name)
	if err != nil {
		return RegisterResult{}, err
	}

	actor := store.Actor{
		ID:          actorID,
		Type:        "person",
		Handle:      handle,
		DisplayName: name,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	channel := store.Channel{
		ID:        channelID,
		ActorID:   actorID,
		Handle:    handle,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	account := store.Account{
		ID:           accountID,
		ActorID:      actorID,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateIdentity(ctx, actor, channel, account); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return RegisterResult{}, ErrEmailTaken
		}
		return RegisterResult{}, err
	}

	code, expires, err := s.issueVerification(ctx, account, now)
	if err != nil {
		return RegisterResult{}, err
	}
	s.sendVerification(ctx, email, name, code, expires)
	s.record(ctx, account.ID, "auth.registered", meta, now, nil)

	result := RegisterResult{
		User:                  userFrom(account, actor, channel),
		Email:                 email,
		VerificationRequired:  true,
		VerificationExpiresAt: expires,
	}
	if s.cfg.ExposeCodes {
		result.DevCode = code
	}
	return result, nil
}

func (s *Service) VerifyEmail(ctx context.Context, input VerifyEmailInput, meta RequestMeta) (SessionResult, error) {
	email := normalizeEmail(input.Email)
	if err := validateEmail(email); err != nil {
		return SessionResult{}, err
	}
	code := strings.TrimSpace(input.Code)
	if !isSixDigits(code) {
		return SessionResult{}, &ValidationError{Field: "code", Message: "Enter the 6-digit code."}
	}

	account, err := s.store.GetAccountByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return SessionResult{}, ErrInvalidCode
	}
	if err != nil {
		return SessionResult{}, err
	}
	if account.EmailVerifiedAt != nil {
		return SessionResult{}, ErrInvalidCode
	}

	now := s.now()
	verification, err := s.store.GetActiveEmailVerification(ctx, account.ID, now)
	if errors.Is(err, store.ErrNotFound) {
		return SessionResult{}, ErrInvalidCode
	}
	if err != nil {
		return SessionResult{}, err
	}

	attempts, err := s.store.IncrementEmailVerificationAttempts(ctx, verification.ID)
	if err != nil {
		return SessionResult{}, err
	}
	if attempts > s.cfg.MaxVerificationAttempts {
		_ = s.store.ConsumeEmailVerification(ctx, verification.ID, now)
		return SessionResult{}, ErrTooManyAttempts
	}
	if !auth.ConstantTimeEqual(auth.HashCode(code, account.ID), verification.CodeHash) {
		s.record(ctx, account.ID, "auth.email_verification_failed", meta, now, nil)
		return SessionResult{}, ErrInvalidCode
	}

	if err := s.store.SetEmailVerified(ctx, account.ID, now); err != nil {
		return SessionResult{}, err
	}
	_ = s.store.ConsumeEmailVerification(ctx, verification.ID, now)
	account.EmailVerifiedAt = &now

	actor, channel, err := s.identityFor(ctx, account)
	if err != nil {
		return SessionResult{}, err
	}
	session, err := s.createSession(ctx, account, actor, channel, "password", meta, now)
	if err != nil {
		return SessionResult{}, err
	}
	s.record(ctx, account.ID, "auth.email_verified", meta, now, nil)
	s.sendWelcome(ctx, email, actor.DisplayName)
	return session, nil
}

func (s *Service) ResendVerification(ctx context.Context, email string, meta RequestMeta) (ResendVerificationResult, error) {
	email = normalizeEmail(email)
	if err := validateEmail(email); err != nil {
		return ResendVerificationResult{}, err
	}
	account, err := s.store.GetAccountByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return ResendVerificationResult{}, nil
	}
	if err != nil {
		return ResendVerificationResult{}, err
	}
	if account.EmailVerifiedAt != nil {
		return ResendVerificationResult{}, nil
	}

	now := s.now()
	if existing, err := s.store.GetActiveEmailVerification(ctx, account.ID, now); err == nil {
		if now.Sub(existing.CreatedAt) < s.cfg.ResendCooldown {
			return ResendVerificationResult{}, nil
		}
	}

	code, expires, err := s.issueVerification(ctx, account, now)
	if err != nil {
		return ResendVerificationResult{}, err
	}
	name := ""
	if actor, err := s.store.GetActor(ctx, account.ActorID); err == nil {
		name = actor.DisplayName
	}
	s.sendVerification(ctx, email, name, code, expires)
	s.record(ctx, account.ID, "auth.verification_resent", meta, now, nil)

	result := ResendVerificationResult{ExpiresAt: expires}
	if s.cfg.ExposeCodes {
		result.DevCode = code
	}
	return result, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput, meta RequestMeta) (SessionResult, error) {
	email := normalizeEmail(input.Email)
	if err := validateEmail(email); err != nil {
		return SessionResult{}, err
	}
	if input.Password == "" {
		return SessionResult{}, &ValidationError{Field: "password", Message: "Password is required."}
	}

	account, err := s.store.GetAccountByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = auth.VerifyPassword(input.Password, auth.DummyHash())
		return SessionResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionResult{}, err
	}

	now := s.now()
	if account.LockedUntil != nil && now.Before(*account.LockedUntil) {
		return SessionResult{}, ErrTooManyAttempts
	}

	ok, err := auth.VerifyPassword(input.Password, account.PasswordHash)
	if err != nil {
		return SessionResult{}, err
	}
	if !ok {
		failed := account.FailedLoginCount + 1
		var lockedUntil *time.Time
		if failed >= s.cfg.MaxLoginAttempts {
			until := now.Add(s.cfg.LockDuration)
			lockedUntil = &until
		}
		_ = s.store.RecordLoginFailure(ctx, account.ID, failed, lockedUntil, now)
		s.record(ctx, account.ID, "auth.login_failed", meta, now, nil)
		return SessionResult{}, ErrInvalidCredentials
	}

	_ = s.store.ResetLoginFailure(ctx, account.ID, now)
	if account.EmailVerifiedAt == nil {
		return SessionResult{}, ErrEmailNotVerified
	}

	actor, channel, err := s.identityFor(ctx, account)
	if err != nil {
		return SessionResult{}, err
	}
	session, err := s.createSession(ctx, account, actor, channel, "password", meta, now)
	if err != nil {
		return SessionResult{}, err
	}
	s.record(ctx, account.ID, "auth.login", meta, now, nil)
	return session, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	identity, err := s.store.GetSessionByTokenHash(ctx, auth.HashToken(rawToken))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.store.RevokeSession(ctx, identity.Session.ID, s.now())
}

func (s *Service) Session(ctx context.Context, rawToken string) (SessionResult, error) {
	if rawToken == "" {
		return SessionResult{}, auth.ErrUnauthorized
	}
	identity, err := s.store.GetSessionByTokenHash(ctx, auth.HashToken(rawToken))
	if errors.Is(err, store.ErrNotFound) {
		return SessionResult{}, auth.ErrUnauthorized
	}
	if err != nil {
		return SessionResult{}, err
	}
	now := s.now()
	if identity.Session.RevokedAt != nil || !now.Before(identity.Session.ExpiresAt) {
		return SessionResult{}, auth.ErrUnauthorized
	}
	_ = s.store.TouchSession(ctx, identity.Session.ID, now)
	return SessionResult{
		User:       userFrom(identity.Account, identity.Actor, identity.Channel),
		Provider:   identity.Session.Provider,
		SignedInAt: identity.Session.CreatedAt,
		ExpiresAt:  identity.Session.ExpiresAt,
	}, nil
}

func (s *Service) ForgotPassword(ctx context.Context, email string, meta RequestMeta) (ForgotPasswordResult, error) {
	email = normalizeEmail(email)
	if err := validateEmail(email); err != nil {
		return ForgotPasswordResult{}, err
	}

	account, err := s.store.GetAccountByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return ForgotPasswordResult{}, nil
	}
	if err != nil {
		return ForgotPasswordResult{}, err
	}
	if account.EmailVerifiedAt == nil {
		return ForgotPasswordResult{}, nil
	}

	now := s.now()
	if err := s.store.InvalidatePasswordResets(ctx, account.ID, now); err != nil {
		return ForgotPasswordResult{}, err
	}
	token, err := auth.NewToken()
	if err != nil {
		return ForgotPasswordResult{}, err
	}
	resetID, err := auth.NewID("pr")
	if err != nil {
		return ForgotPasswordResult{}, err
	}
	expires := now.Add(s.cfg.ResetTTL)
	reset := store.PasswordReset{
		ID:        resetID,
		AccountID: account.ID,
		TokenHash: auth.HashToken(token),
		CreatedAt: now,
		ExpiresAt: expires,
	}
	if err := s.store.CreatePasswordReset(ctx, reset); err != nil {
		return ForgotPasswordResult{}, err
	}
	name := ""
	if actor, err := s.store.GetActor(ctx, account.ActorID); err == nil {
		name = actor.DisplayName
	}
	s.sendPasswordReset(ctx, email, name, token, expires)
	s.record(ctx, account.ID, "auth.password_reset_requested", meta, now, nil)

	result := ForgotPasswordResult{ExpiresAt: expires}
	if s.cfg.ExposeCodes {
		result.DevToken = token
	}
	return result, nil
}

func (s *Service) ResetPassword(ctx context.Context, input ResetPasswordInput, meta RequestMeta) error {
	token := strings.TrimSpace(input.Token)
	if token == "" {
		return &ValidationError{Field: "token", Message: "Reset token is required."}
	}
	if err := validatePassword(input.Password); err != nil {
		return err
	}

	reset, err := s.store.GetPasswordResetByTokenHash(ctx, auth.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidResetToken
	}
	if err != nil {
		return err
	}

	now := s.now()
	if reset.ConsumedAt != nil || !now.Before(reset.ExpiresAt) {
		return ErrInvalidResetToken
	}
	account, err := s.store.GetAccountByID(ctx, reset.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidResetToken
	}
	if err != nil {
		return err
	}

	passwordHash, err := auth.HashPassword(input.Password)
	if err != nil {
		return err
	}
	if err := s.store.UpdateAccountPassword(ctx, account.ID, passwordHash, now); err != nil {
		return err
	}
	_ = s.store.ConsumePasswordReset(ctx, reset.ID, now)
	_ = s.store.InvalidatePasswordResets(ctx, account.ID, now)
	_ = s.store.RevokeAccountSessions(ctx, account.ID, now)
	s.record(ctx, account.ID, "auth.password_reset", meta, now, nil)
	return nil
}

func (s *Service) Authenticate(r *http.Request) (auth.Principal, error) {
	result, err := s.Session(r.Context(), s.SessionToken(r))
	if err != nil {
		return auth.Principal{}, err
	}
	return auth.Principal{ID: result.User.ID, Role: "creator"}, nil
}

func (s *Service) SessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(s.cfg.SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(value)
	}
	return ""
}

func (s *Service) WriteSessionCookies(w http.ResponseWriter, result SessionResult) {
	s.writeCookie(w, s.cfg.SessionCookieName, result.Token, result.ExpiresAt, true)
	if result.CSRFToken != "" {
		s.writeCookie(w, s.cfg.CSRFCookieName, result.CSRFToken, result.ExpiresAt, false)
	}
}

func (s *Service) ClearSessionCookies(w http.ResponseWriter) {
	s.clearCookie(w, s.cfg.SessionCookieName, true)
	s.clearCookie(w, s.cfg.CSRFCookieName, false)
}

// NewCSRFToken rotates the double-submit CSRF cookie for an existing session.
func (s *Service) NewCSRFToken(w http.ResponseWriter, expires time.Time) (string, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	s.writeCookie(w, s.cfg.CSRFCookieName, token, expires, false)
	return token, nil
}

func (s *Service) writeCookie(w http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	maxAge := int(expires.Sub(s.now()).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   s.cfg.CookieDomain,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   s.cfg.CookieDomain,
	})
}

func (s *Service) createSession(
	ctx context.Context,
	account store.Account,
	actor store.Actor,
	channel store.Channel,
	provider string,
	meta RequestMeta,
	now time.Time,
) (SessionResult, error) {
	token, err := auth.NewToken()
	if err != nil {
		return SessionResult{}, err
	}
	csrfToken, err := auth.NewToken()
	if err != nil {
		return SessionResult{}, err
	}
	sessionID, err := auth.NewID("ses")
	if err != nil {
		return SessionResult{}, err
	}
	expires := now.Add(s.cfg.SessionTTL)
	session := store.Session{
		ID:         sessionID,
		AccountID:  account.ID,
		TokenHash:  auth.HashToken(token),
		Provider:   provider,
		UserAgent:  meta.UserAgent,
		IPAddress:  meta.IPAddress,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expires,
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Token:      token,
		CSRFToken:  csrfToken,
		User:       userFrom(account, actor, channel),
		Provider:   provider,
		SignedInAt: now,
		ExpiresAt:  expires,
	}, nil
}

func (s *Service) identityFor(ctx context.Context, account store.Account) (store.Actor, store.Channel, error) {
	actor, err := s.store.GetActor(ctx, account.ActorID)
	if err != nil {
		return store.Actor{}, store.Channel{}, err
	}
	channel, err := s.store.GetChannelByActorID(ctx, account.ActorID)
	if err != nil {
		return store.Actor{}, store.Channel{}, err
	}
	return actor, channel, nil
}

func (s *Service) issueVerification(ctx context.Context, account store.Account, now time.Time) (string, time.Time, error) {
	if err := s.store.InvalidateEmailVerifications(ctx, account.ID, now); err != nil {
		return "", time.Time{}, err
	}
	code, err := auth.NewCode()
	if err != nil {
		return "", time.Time{}, err
	}
	verificationID, err := auth.NewID("ev")
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(s.cfg.VerificationTTL)
	verification := store.EmailVerification{
		ID:        verificationID,
		AccountID: account.ID,
		CodeHash:  auth.HashCode(code, account.ID),
		CreatedAt: now,
		ExpiresAt: expires,
	}
	if err := s.store.CreateEmailVerification(ctx, verification); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

func (s *Service) uniqueHandle(ctx context.Context, name string) (string, error) {
	base := handleFromName(name)
	candidate := base
	for attempt := 0; attempt < 10; attempt++ {
		exists, err := s.store.HandleExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		suffix, err := auth.RandomHex(3)
		if err != nil {
			return "", err
		}
		candidate = base + suffix
	}
	return "", fmt.Errorf("could not allocate a unique handle")
}

func (s *Service) sendVerification(ctx context.Context, to, name, code string, expires time.Time) {
	rendered, err := email.Verification(email.Data{
		Name:        name,
		Email:       to,
		Code:        code,
		ExpiresAt:   expires,
		ExpiresIn:   s.cfg.VerificationTTL,
		AppURL:      s.cfg.AppURL,
		LogoURL:     s.cfg.EmailLogoURL,
		LogoDarkURL: s.cfg.EmailLogoDarkURL,
	})
	if err != nil {
		log.Printf("identity: render verification for %s: %v", to, err)
		return
	}
	if err := s.mailer.Send(ctx, mailer.Message{
		To:      to,
		Subject: rendered.Subject,
		Text:    rendered.Text,
		HTML:    rendered.HTML,
	}); err != nil {
		log.Printf("identity: send verification to %s: %v", to, err)
	}
}

func (s *Service) sendWelcome(ctx context.Context, to, name string) {
	rendered, err := email.Welcome(email.Data{
		Name:        name,
		Email:       to,
		ActionURL:   strings.TrimRight(s.cfg.AppURL, "/") + "/studio",
		AppURL:      s.cfg.AppURL,
		LogoURL:     s.cfg.EmailLogoURL,
		LogoDarkURL: s.cfg.EmailLogoDarkURL,
	})
	if err != nil {
		log.Printf("identity: render welcome for %s: %v", to, err)
		return
	}
	if err := s.mailer.Send(ctx, mailer.Message{
		To:      to,
		Subject: rendered.Subject,
		Text:    rendered.Text,
		HTML:    rendered.HTML,
	}); err != nil {
		log.Printf("identity: send welcome to %s: %v", to, err)
	}
}

func (s *Service) sendPasswordReset(ctx context.Context, to, name, token string, expires time.Time) {
	link := fmt.Sprintf(
		"%s/reset-password?token=%s&email=%s",
		strings.TrimRight(s.cfg.AppURL, "/"),
		url.QueryEscape(token),
		url.QueryEscape(to),
	)
	rendered, err := email.PasswordReset(email.Data{
		Name:        name,
		Email:       to,
		ActionURL:   link,
		ExpiresAt:   expires,
		ExpiresIn:   s.cfg.ResetTTL,
		AppURL:      s.cfg.AppURL,
		LogoURL:     s.cfg.EmailLogoURL,
		LogoDarkURL: s.cfg.EmailLogoDarkURL,
	})
	if err != nil {
		log.Printf("identity: render password reset for %s: %v", to, err)
		return
	}
	if err := s.mailer.Send(ctx, mailer.Message{
		To:      to,
		Subject: rendered.Subject,
		Text:    rendered.Text,
		HTML:    rendered.HTML,
	}); err != nil {
		log.Printf("identity: send password reset to %s: %v", to, err)
	}
}

func (s *Service) record(ctx context.Context, accountID, eventType string, meta RequestMeta, now time.Time, metadata map[string]any) {
	event := store.AuthEvent{
		AccountID: accountID,
		Type:      eventType,
		IPAddress: meta.IPAddress,
		UserAgent: meta.UserAgent,
		Metadata:  metadata,
		CreatedAt: now,
	}
	if err := s.store.RecordAuthEvent(ctx, event); err != nil {
		log.Printf("identity: record auth event %s: %v", eventType, err)
	}
}

func userFrom(account store.Account, actor store.Actor, channel store.Channel) User {
	return User{
		ID:            actor.ID,
		AccountID:     account.ID,
		Name:          actor.DisplayName,
		Handle:        actor.Handle,
		Role:          "creator",
		ChannelID:     channel.ID,
		AvatarURL:     channel.AvatarURL,
		Email:         account.Email,
		EmailVerified: account.EmailVerifiedAt != nil,
	}
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateEmail(value string) error {
	if value == "" {
		return &ValidationError{Field: "email", Message: "Email is required."}
	}
	if len(value) > 320 || !emailPattern.MatchString(value) {
		return &ValidationError{Field: "email", Message: "Enter a valid email address."}
	}
	return nil
}

func validatePassword(value string) error {
	if len(value) < 8 {
		return &ValidationError{Field: "password", Message: "Use at least 8 characters."}
	}
	if len(value) > 128 {
		return &ValidationError{Field: "password", Message: "Password is too long."}
	}
	return nil
}

func validateName(value string) error {
	if value == "" {
		return &ValidationError{Field: "name", Message: "Name is required."}
	}
	if len(value) > 200 {
		return &ValidationError{Field: "name", Message: "Name is too long."}
	}
	return nil
}

func isSixDigits(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func handleFromName(name string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(name) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	handle := builder.String()
	if handle == "" {
		handle = "creator"
	}
	if len(handle) > 24 {
		handle = handle[:24]
	}
	return "@" + handle
}
