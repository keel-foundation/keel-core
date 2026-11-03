package auth

import (
	"errors"
	"net/http"
)

// Fallback tries the primary authenticator first and optionally falls back to a
// development authenticator. It exists so local development can keep the fixed
// dev principal while production requires a real session.
type Fallback struct {
	Primary        Authenticator
	Secondary      Authenticator
	AllowSecondary bool
}

func (f Fallback) Authenticate(r *http.Request) (Principal, error) {
	if f.Primary != nil {
		principal, err := f.Primary.Authenticate(r)
		if err == nil {
			return principal, nil
		}
		if !errors.Is(err, ErrUnauthorized) {
			return Principal{}, err
		}
	}
	if f.AllowSecondary && f.Secondary != nil {
		return f.Secondary.Authenticate(r)
	}
	return Principal{}, ErrUnauthorized
}
