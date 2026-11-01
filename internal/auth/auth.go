package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

var ErrUnauthorized = errors.New("unauthorized")

type Principal struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

// DevAuthenticator is a local-development stand-in for OIDC. It returns a fixed actor and
// optionally requires a bearer token. Replace it with a real provider behind the same interface.
type DevAuthenticator struct {
	ActorID string
	Token   string
}

func NewDev(actorID, token string) DevAuthenticator {
	if actorID == "" {
		actorID = "usr_demo"
	}
	return DevAuthenticator{ActorID: actorID, Token: token}
}

func (d DevAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	if d.Token == "" {
		return Principal{ID: d.ActorID, Role: "creator"}, nil
	}

	header := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return Principal{}, ErrUnauthorized
	}
	token = strings.TrimSpace(token)
	if subtle.ConstantTimeCompare([]byte(token), []byte(d.Token)) != 1 {
		return Principal{}, ErrUnauthorized
	}
	return Principal{ID: d.ActorID, Role: "creator"}, nil
}
