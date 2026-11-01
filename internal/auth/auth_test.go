package auth_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/keel-foundation/keel-core/internal/auth"
)

func TestDevAuthenticatorOpenMode(t *testing.T) {
	authenticator := auth.NewDev("usr_demo", "")
	request := httptest.NewRequest("GET", "/api/v1/me", nil)

	principal, err := authenticator.Authenticate(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if principal.ID != "usr_demo" || principal.Role != "creator" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}

func TestDevAuthenticatorRequiresBearerToken(t *testing.T) {
	authenticator := auth.NewDev("usr_demo", "secret")
	request := httptest.NewRequest("GET", "/api/v1/me", nil)

	if _, err := authenticator.Authenticate(request); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("expected unauthorized without a token, got %v", err)
	}

	request.Header.Set("Authorization", "Bearer wrong")
	if _, err := authenticator.Authenticate(request); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for a wrong token, got %v", err)
	}

	request.Header.Set("Authorization", "Bearer secret")
	principal, err := authenticator.Authenticate(request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if principal.ID != "usr_demo" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}
