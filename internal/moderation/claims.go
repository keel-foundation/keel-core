package moderation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultClaimsIssuer   = "keel-core"
	defaultClaimsAudience = "keel-moderation"
	defaultClaimsTTL      = time.Minute
)

type claimsConfig struct {
	secret   string
	issuer   string
	audience string
	ttl      time.Duration
}

// Identity is the authenticated principal forwarded to moderation as OIDC-style
// claims. The gateway derives it from the core session, never from the client.
type Identity struct {
	ActorID string
	Role    string
}

// WithClaims enables short-lived HS256 claims for moderation requests. When a
// secret is configured, the static service token is no longer sent.
func (c *Client) WithClaims(secret, issuer, audience string, ttl time.Duration) *Client {
	if c == nil {
		return c
	}
	c.claims = claimsConfig{
		secret:   strings.TrimSpace(secret),
		issuer:   strings.TrimSpace(issuer),
		audience: strings.TrimSpace(audience),
		ttl:      ttl,
	}
	if c.claims.issuer == "" {
		c.claims.issuer = defaultClaimsIssuer
	}
	if c.claims.audience == "" {
		c.claims.audience = defaultClaimsAudience
	}
	if c.claims.ttl <= 0 {
		c.claims.ttl = defaultClaimsTTL
	}
	return c
}

func (c *Client) claimsEnabled() bool {
	return c != nil && c.claims.secret != ""
}

func (c *Client) signClaims(identity Identity, channelID string, now time.Time) (string, error) {
	subject := strings.TrimSpace(identity.ActorID)
	if subject == "" {
		return "", fmt.Errorf("moderation claims subject is required")
	}
	role := strings.TrimSpace(identity.Role)
	if role == "" {
		role = "creator"
	}
	if channelID == "" {
		return "", fmt.Errorf("moderation claims channel is required")
	}

	header, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	if err != nil {
		return "", fmt.Errorf("encode moderation claims header: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"iss":         c.claims.issuer,
		"aud":         c.claims.audience,
		"sub":         subject,
		"role":        role,
		"channel_ids": []string{channelID},
		"iat":         now.Unix(),
		"exp":         now.Add(c.claims.ttl).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode moderation claims: %w", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(header) +
		"." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(c.claims.secret))
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}
