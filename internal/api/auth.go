package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
	"github.com/keel-foundation/keel-core/internal/store"
)

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyEmailRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body registerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := s.identity.Register(r.Context(), identity.RegisterInput{
		Name:     body.Name,
		Email:    body.Email,
		Password: body.Password,
	}, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body verifyEmailRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := s.identity.VerifyEmail(r.Context(), identity.VerifyEmailInput{
		Email: body.Email,
		Code:  body.Code,
	}, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	s.writeSession(w, result)
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body forgotPasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := s.identity.ResendVerification(r.Context(), body.Email, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	response := map[string]any{
		"detail": "If that account exists and is unverified, a new code has been sent.",
	}
	if result.DevCode != "" {
		response["dev_code"] = result.DevCode
		response["expires_at"] = result.ExpiresAt
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body loginRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := s.identity.Login(r.Context(), identity.LoginInput{
		Email:    body.Email,
		Password: body.Password,
	}, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	s.writeSession(w, result)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := s.identity.SessionToken(r)
	if err := s.identity.Logout(r.Context(), token); err != nil {
		s.writeIdentityError(w, err)
		return
	}
	s.identity.ClearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	result, err := s.identity.Session(r.Context(), s.identity.SessionToken(r))
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "No active session.",
			})
			return
		}
		s.writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) csrf(w http.ResponseWriter, r *http.Request) {
	result, err := s.identity.Session(r.Context(), s.identity.SessionToken(r))
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"detail": "No active session.",
			})
			return
		}
		s.writeIdentityError(w, err)
		return
	}
	token, err := s.identity.NewCSRFToken(w, result.ExpiresAt)
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": token})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body forgotPasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := s.identity.ForgotPassword(r.Context(), body.Email, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	response := map[string]any{
		"detail": "If that account exists, a reset link has been sent.",
	}
	if result.DevToken != "" {
		response["dev_token"] = result.DevToken
		response["expires_at"] = result.ExpiresAt
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r) {
		return
	}
	var body resetPasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	err := s.identity.ResetPassword(r.Context(), identity.ResetPasswordInput{
		Token:    body.Token,
		Password: body.Password,
	}, requestMeta(r, s.trustProxy))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body changePasswordRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.identity.ChangePassword(
		r.Context(),
		s.identity.SessionToken(r),
		body.CurrentPassword,
		body.NewPassword,
		requestMeta(r, s.trustProxy),
	); err != nil {
		s.writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.identity.ListSessions(r.Context(), s.identity.SessionToken(r))
	if err != nil {
		s.writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sessions})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if err := s.identity.RevokeSession(
		r.Context(),
		s.identity.SessionToken(r),
		r.PathValue("id"),
		requestMeta(r, s.trustProxy),
	); err != nil {
		s.writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.identity.RevokeOtherSessions(
		r.Context(),
		s.identity.SessionToken(r),
		requestMeta(r, s.trustProxy),
	); err != nil {
		s.writeIdentityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeSession(w http.ResponseWriter, result identity.SessionResult) {
	s.identity.WriteSessionCookies(w, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeIdentityError(w http.ResponseWriter, err error) {
	var validation *identity.ValidationError
	switch {
	case errors.As(err, &validation):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"detail": validation.Message,
			"field":  validation.Field,
		})
	case errors.Is(err, identity.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, map[string]string{
			"detail": "An account with that email already exists.",
		})
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "Email or password is incorrect.",
		})
	case errors.Is(err, identity.ErrEmailNotVerified):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"detail": "Verify your email before signing in.",
		})
	case errors.Is(err, identity.ErrInvalidCode):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"detail": "That verification code is invalid or expired.",
		})
	case errors.Is(err, identity.ErrTooManyAttempts):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"detail": "Too many attempts. Try again later.",
		})
	case errors.Is(err, identity.ErrInvalidResetToken):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"detail": "That reset link is invalid or expired.",
		})
	case errors.Is(err, identity.ErrInvalidPassword):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"detail": "Current password is incorrect.",
		})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Not found"})
	case errors.Is(err, auth.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"detail": "No active session.",
		})
	default:
		log.Printf("identity request failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"detail": "Internal server error",
		})
	}
}

func (s *Server) allowRequest(w http.ResponseWriter, r *http.Request) bool {
	if s.limiter == nil {
		return true
	}
	if s.limiter.allow(clientIP(r, s.trustProxy)) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"detail": "Too many requests. Try again shortly.",
	})
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"detail": "Invalid JSON body.",
		})
		return false
	}
	return true
}

func requestMeta(r *http.Request, trustProxy bool) identity.RequestMeta {
	return identity.RequestMeta{
		UserAgent: r.UserAgent(),
		IPAddress: clientIP(r, trustProxy),
	}
}
