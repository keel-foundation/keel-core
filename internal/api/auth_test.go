package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keel-foundation/keel-core/internal/auth"
	"github.com/keel-foundation/keel-core/internal/identity"
)

func postJSON(
	t *testing.T,
	handler http.Handler,
	path string,
	body any,
	cookies []*http.Cookie,
	csrf string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(http.MethodPost, path, reader)
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	return rec
}

func getWithCookies(t *testing.T, handler http.Handler, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	return rec
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func testPassword(t *testing.T) string {
	t.Helper()
	value, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate test password: %v", err)
	}
	return value
}

func registerAndVerify(t *testing.T, handler http.Handler, email, password string) (identity.RegisterResult, []*http.Cookie) {
	t.Helper()
	rec := postJSON(t, handler, "/api/v1/auth/register", map[string]string{
		"name":     "Ada Lovelace",
		"email":    email,
		"password": password,
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	registered := decode[identity.RegisterResult](t, rec)
	if registered.DevCode == "" {
		t.Fatal("expected a dev verification code")
	}

	rec = postJSON(t, handler, "/api/v1/auth/verify-email", map[string]string{
		"email": email,
		"code":  registered.DevCode,
	}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return registered, rec.Result().Cookies()
}

func TestAuthRegisterVerifySessionLogout(t *testing.T) {
	handler := newHandler(t, "")
	password := testPassword(t)

	rec := postJSON(t, handler, "/api/v1/auth/register", map[string]string{
		"name":     "Ada Lovelace",
		"email":    "ada@example.com",
		"password": password,
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	registered := decode[identity.RegisterResult](t, rec)
	if registered.User.Name != "Ada Lovelace" || registered.User.Handle != "@adalovelace" {
		t.Fatalf("unexpected registered user: %+v", registered.User)
	}
	if !registered.VerificationRequired || registered.DevCode == "" {
		t.Fatalf("unexpected register result: %+v", registered)
	}

	duplicate := postJSON(t, handler, "/api/v1/auth/register", map[string]string{
		"name":     "Ada Again",
		"email":    "ada@example.com",
		"password": testPassword(t),
	}, nil, "")
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d", duplicate.Code)
	}

	unverifiedLogin := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
		"email":    "ada@example.com",
		"password": password,
	}, nil, "")
	if unverifiedLogin.Code != http.StatusForbidden {
		t.Fatalf("unverified login: expected 403, got %d", unverifiedLogin.Code)
	}

	wrongCode := postJSON(t, handler, "/api/v1/auth/verify-email", map[string]string{
		"email": "ada@example.com",
		"code":  "000000",
	}, nil, "")
	if wrongCode.Code != http.StatusBadRequest {
		t.Fatalf("wrong code: expected 400, got %d", wrongCode.Code)
	}

	verified := postJSON(t, handler, "/api/v1/auth/verify-email", map[string]string{
		"email": "ada@example.com",
		"code":  registered.DevCode,
	}, nil, "")
	if verified.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d: %s", verified.Code, verified.Body.String())
	}
	cookies := verified.Result().Cookies()
	sessionCookie := findCookie(cookies, "keeltube_session")
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session and CSRF cookies, got %+v", cookies)
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if csrfCookie.HttpOnly {
		t.Fatal("CSRF cookie must be readable by the browser")
	}

	session := getWithCookies(t, handler, "/api/v1/auth/session", cookies)
	if session.Code != http.StatusOK {
		t.Fatalf("session: expected 200, got %d", session.Code)
	}
	body := decode[identity.SessionResult](t, session)
	if body.User.Email != "ada@example.com" || !body.User.EmailVerified {
		t.Fatalf("unexpected session user: %+v", body.User)
	}

	missingCSRF := postJSON(t, handler, "/api/v1/auth/logout", nil, cookies, "")
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF: expected 403, got %d", missingCSRF.Code)
	}

	logout := postJSON(t, handler, "/api/v1/auth/logout", nil, cookies, csrfCookie.Value)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout: expected 204, got %d: %s", logout.Code, logout.Body.String())
	}

	afterLogout := getWithCookies(t, handler, "/api/v1/auth/session", cookies)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout: expected 401, got %d", afterLogout.Code)
	}
}

func TestAuthLoginAndForgotReset(t *testing.T) {
	handler := newHandler(t, "")
	password := testPassword(t)
	_, cookies := registerAndVerify(t, handler, "ada@example.com", password)
	csrfCookie := findCookie(cookies, "keeltube_csrf")
	if csrfCookie == nil {
		t.Fatal("expected CSRF cookie")
	}

	// Reset revokes the current session.
	forgot := postJSON(t, handler, "/api/v1/auth/forgot-password", map[string]string{
		"email": "ada@example.com",
	}, nil, "")
	if forgot.Code != http.StatusAccepted {
		t.Fatalf("forgot: expected 202, got %d: %s", forgot.Code, forgot.Body.String())
	}
	var forgotBody map[string]any
	if err := json.Unmarshal(forgot.Body.Bytes(), &forgotBody); err != nil {
		t.Fatalf("decode forgot body: %v", err)
	}
	token, _ := forgotBody["dev_token"].(string)
	if token == "" {
		t.Fatalf("expected dev reset token, got %+v", forgotBody)
	}

	newPassword := testPassword(t)
	reset := postJSON(t, handler, "/api/v1/auth/reset-password", map[string]string{
		"token":    token,
		"password": newPassword,
	}, nil, "")
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset: expected 204, got %d: %s", reset.Code, reset.Body.String())
	}

	oldSession := getWithCookies(t, handler, "/api/v1/auth/session", cookies)
	if oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("session after reset: expected 401, got %d", oldSession.Code)
	}

	oldPassword := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
		"email":    "ada@example.com",
		"password": password,
	}, nil, "")
	if oldPassword.Code != http.StatusUnauthorized {
		t.Fatalf("old password: expected 401, got %d", oldPassword.Code)
	}

	newPasswordLogin := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
		"email":    "ada@example.com",
		"password": newPassword,
	}, nil, "")
	if newPasswordLogin.Code != http.StatusOK {
		t.Fatalf("new password: expected 200, got %d: %s", newPasswordLogin.Code, newPasswordLogin.Body.String())
	}

	// A second use of the same token must fail.
	reused := postJSON(t, handler, "/api/v1/auth/reset-password", map[string]string{
		"token":    token,
		"password": testPassword(t),
	}, nil, "")
	if reused.Code != http.StatusBadRequest {
		t.Fatalf("reused reset token: expected 400, got %d", reused.Code)
	}
}

func TestAuthLoginLockout(t *testing.T) {
	handler := newHandler(t, "")
	password := testPassword(t)
	registerAndVerify(t, handler, "ada@example.com", password)
	wrongPassword := testPassword(t)

	for attempt := 0; attempt < 3; attempt++ {
		rec := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
			"email":    "ada@example.com",
			"password": wrongPassword,
		}, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", attempt+1, rec.Code)
		}
	}

	locked := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
		"email":    "ada@example.com",
		"password": password,
	}, nil, "")
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("locked account: expected 429, got %d", locked.Code)
	}
}

func TestAuthSessionRequiresValidCookie(t *testing.T) {
	handler := newHandler(t, "")

	missing := getWithCookies(t, handler, "/api/v1/auth/session", nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing session: expected 401, got %d", missing.Code)
	}

	bogus := getWithCookies(t, handler, "/api/v1/auth/session", []*http.Cookie{
		{Name: "keeltube_session", Value: "not-a-real-token"},
	})
	if bogus.Code != http.StatusUnauthorized {
		t.Fatalf("bogus session: expected 401, got %d", bogus.Code)
	}
}

func TestPasswordChangeAndSessionManagement(t *testing.T) {
	handler := newHandler(t, "dev-token")
	password := testPassword(t)
	newPassword := testPassword(t)
	_, cookies := registerAndVerify(t, handler, "session@example.com", password)
	csrf := findCookie(cookies, "keeltube_csrf")
	if csrf == nil {
		t.Fatal("missing csrf cookie")
	}

	sessions := getWithCookies(t, handler, "/api/v1/me/sessions", cookies)
	if sessions.Code != http.StatusOK {
		t.Fatalf("sessions: expected 200, got %d: %s", sessions.Code, sessions.Body.String())
	}
	sessionPage := decode[struct {
		Items []identity.SessionInfo `json:"items"`
	}](t, sessions)
	if len(sessionPage.Items) != 1 || !sessionPage.Items[0].Current {
		t.Fatalf("unexpected sessions: %+v", sessionPage.Items)
	}

	changed := postJSON(t, handler, "/api/v1/me/password", map[string]string{
		"current_password": password,
		"new_password":     newPassword,
	}, cookies, csrf.Value)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("change password: expected 204, got %d: %s", changed.Code, changed.Body.String())
	}

	login := postJSON(t, handler, "/api/v1/auth/login", map[string]string{
		"email":    "session@example.com",
		"password": newPassword,
	}, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login with new password: expected 200, got %d: %s", login.Code, login.Body.String())
	}

	revokeOthers := postJSON(t, handler, "/api/v1/me/sessions/revoke-others", nil, cookies, csrf.Value)
	if revokeOthers.Code != http.StatusNoContent {
		t.Fatalf("revoke others: expected 204, got %d: %s", revokeOthers.Code, revokeOthers.Body.String())
	}

	sessionID := sessionPage.Items[0].ID
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/me/sessions/"+sessionID, nil)
	for _, cookie := range cookies {
		deleteReq.AddCookie(cookie)
	}
	deleteReq.Header.Set("X-CSRF-Token", csrf.Value)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("revoke session: expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	if rec := getWithCookies(t, handler, "/api/v1/me", cookies); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session should be unauthorized, got %d", rec.Code)
	}
}
