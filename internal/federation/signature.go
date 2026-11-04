package federation

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

var (
	ErrSignatureMissing = errors.New("http signature is missing")
	ErrSignatureInvalid = errors.New("http signature is invalid")
	ErrSignatureStale   = errors.New("http signature has expired")
	ErrDigestMismatch   = errors.New("request digest does not match")
)

const signatureMaxAge = 12 * time.Hour

type signatureParams struct {
	KeyID     string
	Algorithm string
	Headers   []string
	Signature []byte
}

func SignRequest(
	req *http.Request,
	body []byte,
	keyID string,
	privateKey *rsa.PrivateKey,
	now time.Time,
) error {
	if privateKey == nil {
		return ErrSignatureInvalid
	}
	req.Header.Set("Date", now.UTC().Format(http.TimeFormat))
	req.Header.Set("Host", req.URL.Host)
	headers := []string{"(request-target)", "host", "date"}
	if body != nil {
		digest := sha256.Sum256(body)
		req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(digest[:]))
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/activity+json")
		}
		headers = append(headers, "digest", "content-type")
	}
	signingString := buildSigningString(req, body, headers)
	digest := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(nil, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return fmt.Errorf("sign federation request: %w", err)
	}
	quoted := make([]string, 0, len(headers))
	for _, header := range headers {
		quoted = append(quoted, header)
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(quoted, " "), base64.StdEncoding.EncodeToString(signature),
	))
	return nil
}

func VerifyRequest(
	ctx context.Context,
	req *http.Request,
	body []byte,
	now time.Time,
	fetchKey func(context.Context, string) (string, error),
) (string, error) {
	params, err := parseSignature(req.Header.Get("Signature"))
	if err != nil {
		return "", err
	}
	if params.KeyID == "" || len(params.Signature) == 0 {
		return "", ErrSignatureMissing
	}
	if params.Algorithm != "" && !strings.EqualFold(params.Algorithm, "rsa-sha256") {
		return "", ErrSignatureInvalid
	}
	dateValue := req.Header.Get("Date")
	if dateValue == "" {
		return "", ErrSignatureMissing
	}
	date, err := http.ParseTime(dateValue)
	if err != nil {
		return "", ErrSignatureInvalid
	}
	if now.Sub(date) > signatureMaxAge || date.Sub(now) > signatureMaxAge {
		return "", ErrSignatureStale
	}
	if digestHeader := req.Header.Get("Digest"); digestHeader != "" {
		expected := sha256.Sum256(body)
		actual := strings.TrimPrefix(strings.TrimSpace(digestHeader), "SHA-256=")
		decoded, err := base64.StdEncoding.DecodeString(actual)
		if err != nil || !bytes.Equal(decoded, expected[:]) {
			return "", ErrDigestMismatch
		}
	} else if len(body) > 0 {
		return "", ErrDigestMismatch
	}
	headers := params.Headers
	if len(headers) == 0 {
		headers = []string{"(request-target)", "host", "date"}
	}
	signingString := buildSigningString(req, body, headers)
	digest := sha256.Sum256([]byte(signingString))
	publicKeyPEM, err := fetchKey(ctx, params.KeyID)
	if err != nil {
		return "", err
	}
	publicKey, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return "", ErrSignatureInvalid
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], params.Signature); err != nil {
		return "", ErrSignatureInvalid
	}
	return params.KeyID, nil
}

func buildSigningString(req *http.Request, _ []byte, headers []string) string {
	lines := make([]string, 0, len(headers))
	for _, header := range headers {
		header = strings.ToLower(strings.TrimSpace(header))
		var value string
		if header == "(request-target)" {
			target := req.URL.EscapedPath()
			if target == "" {
				target = "/"
			}
			if req.URL.RawQuery != "" {
				target += "?" + req.URL.RawQuery
			}
			value = strings.ToLower(req.Method) + " " + target
		} else if header == "host" {
			value = req.Host
			if value == "" {
				value = req.URL.Host
			}
		} else {
			values := req.Header.Values(http.CanonicalHeaderKey(header))
			if len(values) == 0 {
				values = req.Header.Values(header)
			}
			sort.Strings(values)
			value = strings.Join(values, ", ")
		}
		lines = append(lines, header+": "+strings.TrimSpace(value))
	}
	return strings.Join(lines, "\n")
}

func parseSignature(header string) (signatureParams, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return signatureParams{}, ErrSignatureMissing
	}
	params := signatureParams{}
	for _, part := range splitSignatureParts(header) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		switch key {
		case "keyId":
			params.KeyID = value
		case "algorithm":
			params.Algorithm = value
		case "headers":
			params.Headers = strings.Fields(value)
		case "signature":
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return signatureParams{}, ErrSignatureInvalid
			}
			params.Signature = decoded
		}
	}
	return params, nil
}

func splitSignatureParts(value string) []string {
	parts := make([]string, 0, 4)
	var builder strings.Builder
	inQuotes := false
	for _, character := range value {
		switch character {
		case '"':
			inQuotes = !inQuotes
			builder.WriteRune(character)
		case ',':
			if inQuotes {
				builder.WriteRune(character)
				continue
			}
			parts = append(parts, builder.String())
			builder.Reset()
		default:
			builder.WriteRune(character)
		}
	}
	if builder.Len() > 0 {
		parts = append(parts, builder.String())
	}
	return parts
}
