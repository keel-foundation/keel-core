package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/keel-foundation/keel-core/internal/auth"
)

func TestNewTokenIsUniqueAndOpaque(t *testing.T) {
	first, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	second, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if first == second {
		t.Fatal("expected unique tokens")
	}
	if len(first) < 40 {
		t.Fatalf("expected a long token, got %d characters", len(first))
	}
	if strings.Contains(first, " ") {
		t.Fatal("token must not contain whitespace")
	}
}

func TestNewCodeIsSixDigits(t *testing.T) {
	pattern := regexp.MustCompile(`^\d{6}$`)
	for i := 0; i < 20; i++ {
		code, err := auth.NewCode()
		if err != nil {
			t.Fatalf("new code: %v", err)
		}
		if !pattern.MatchString(code) {
			t.Fatalf("unexpected code: %q", code)
		}
	}
}

func TestHashingBindsValues(t *testing.T) {
	value, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	hashed := auth.HashToken(value)
	if hashed != auth.HashToken(value) {
		t.Fatal("expected deterministic token hashes")
	}
	if !strings.HasPrefix(hashed, "sha256:") {
		t.Fatalf("unexpected hash prefix: %s", hashed)
	}
	if auth.HashCode("123456", "acct_one") == auth.HashCode("123456", "acct_two") {
		t.Fatal("verification codes must be bound to their account")
	}
}

func TestNewIDIsPrefixed(t *testing.T) {
	id, err := auth.NewID("usr")
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	if !strings.HasPrefix(id, "usr_") {
		t.Fatalf("unexpected id: %s", id)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !auth.ConstantTimeEqual("same", "same") {
		t.Fatal("expected equal strings to match")
	}
	if auth.ConstantTimeEqual("same", "different") {
		t.Fatal("expected different strings not to match")
	}
}
