package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

const tokenBytes = 32

// NewToken returns a URL-safe, 256-bit opaque token.
func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewCode returns a zero-padded six-digit verification code.
func NewCode() (string, error) {
	limit := big.NewInt(1_000_000)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", fmt.Errorf("read code: %w", err)
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

// HashToken hashes a token or code for storage. Raw values never touch the database.
func HashToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

// HashCode binds a low-entropy verification code to its account before hashing.
func HashCode(code, accountID string) string {
	return HashToken(code + ":" + accountID)
}

// RandomHex returns n random bytes encoded as hex.
func RandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// NewID returns an opaque prefixed identifier, for example usr_4f1c...
func NewID(prefix string) (string, error) {
	suffix, err := RandomHex(12)
	if err != nil {
		return "", err
	}
	return prefix + "_" + suffix, nil
}

// ConstantTimeEqual compares two strings in constant time.
func ConstantTimeEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
