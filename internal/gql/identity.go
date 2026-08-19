package gql

import (
	"context"
	"errors"
)

// ErrUnauthenticated is returned when an operation requires an access token
// but none is available.
var ErrUnauthenticated = errors.New("unauthenticated: no access token available")

// Identity provides client identity and auth credentials for Twitch requests.
type Identity interface {
	ClientID() string
	DeviceID() string
	SessionID() string
	UserAgent() string
	AccessToken() string // must return "" if not yet authenticated
}

// TokenRefresher lets the client trigger a single-flighted re-auth on 401 and retry once.
// Implemented for real in Plan 04 (internal/auth); Plan 02's client works against a stub in tests.
type TokenRefresher interface {
	RefreshOnUnauthorized(ctx context.Context) error
}
