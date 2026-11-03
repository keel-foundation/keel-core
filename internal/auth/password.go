package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

var ErrInvalidPasswordHash = errors.New("invalid password hash")

// HashPassword derives an Argon2id hash using a random salt. The encoded form is
// self-describing so parameters can be raised later without breaking old hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		argonIterations,
		argonMemory,
		argonParallelism,
		argonKeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against an encoded Argon2id hash in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		params.iterations,
		params.memory,
		params.parallelism,
		uint32(len(expected)),
	)
	if subtle.ConstantTimeCompare(key, expected) != 1 {
		return false, nil
	}
	return true, nil
}

type argonParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parsePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	var params argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params, nil, nil, ErrInvalidPasswordHash
	}

	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return params, nil, nil, ErrInvalidPasswordHash
	}

	for _, pair := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return params, nil, nil, ErrInvalidPasswordHash
		}
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return params, nil, nil, ErrInvalidPasswordHash
		}
		switch key {
		case "m":
			params.memory = uint32(number)
		case "t":
			params.iterations = uint32(number)
		case "p":
			params.parallelism = uint8(number)
		}
	}
	if params.memory == 0 || params.iterations == 0 || params.parallelism == 0 {
		return params, nil, nil, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return params, nil, nil, ErrInvalidPasswordHash
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return params, nil, nil, ErrInvalidPasswordHash
	}
	return params, salt, hash, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// DummyHash returns a valid hash used to equalize login timing when an account
// does not exist. It is never a valid credential for a real account.
func DummyHash() string {
	dummyOnce.Do(func() {
		secret, err := NewToken()
		if err != nil {
			return
		}
		dummyHash, _ = HashPassword(secret)
	})
	return dummyHash
}
