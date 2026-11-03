package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/keel-foundation/keel-core/internal/auth"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate password: %v", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}

	ok, err := auth.VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("verify password: %v", err)
	}
	if !ok {
		t.Fatal("expected the password to verify")
	}

	wrong, err := auth.NewToken()
	if err != nil {
		t.Fatalf("generate wrong password: %v", err)
	}
	ok, err = auth.VerifyPassword(wrong, hash)
	if err != nil {
		t.Fatalf("verify wrong password: %v", err)
	}
	if ok {
		t.Fatal("expected the wrong password to fail")
	}

	second, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password again: %v", err)
	}
	if second == hash {
		t.Fatal("expected a fresh salt to produce a different hash")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"plain-text",
		"$argon2id$v=19$m=65536,t=3,p=2$onlyonepart",
		"$bcrypt$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
	}
	for _, encoded := range cases {
		if _, err := auth.VerifyPassword("password", encoded); !errors.Is(err, auth.ErrInvalidPasswordHash) {
			t.Fatalf("%q: expected ErrInvalidPasswordHash, got %v", encoded, err)
		}
	}
}

func TestDummyHashIsAValidButUnusableHash(t *testing.T) {
	hash := auth.DummyHash()
	if hash == "" {
		t.Fatal("expected a dummy hash")
	}
	ok, err := auth.VerifyPassword("anything", hash)
	if err != nil {
		t.Fatalf("verify against dummy hash: %v", err)
	}
	if ok {
		t.Fatal("dummy hash must not match an arbitrary password")
	}
}
