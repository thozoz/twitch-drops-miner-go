package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

// ErrUnsupportedTokenClient reports a valid token that cannot be used by the
// headless miner. In particular, Twitch Web tokens require browser integrity.
var ErrUnsupportedTokenClient = errors.New("only existing Android-issued Twitch tokens are supported")

// NormalizeTokenInput removes common wrappers copied with an OAuth token.
func NormalizeTokenInput(input string) string {
	token := strings.Trim(strings.TrimSpace(input), `"'`)
	if idx := strings.Index(token, "auth-token="); idx != -1 {
		token = token[idx+len("auth-token="):]
		if end := strings.IndexAny(token, "; \t\r\n"); end != -1 {
			token = token[:end]
		}
		token = strings.Trim(token, `"'`)
	}
	token = strings.TrimPrefix(token, "OAuth ")
	token = strings.TrimPrefix(token, "oauth:")
	return strings.Trim(strings.TrimSpace(token), `"'`)
}

// SetToken imports an existing Android-issued OAuth token. Web tokens are
// deliberately rejected because they fail Twitch's headless GQL integrity check.
func (s *Session) SetToken(ctx context.Context, input string) error {
	token := NormalizeTokenInput(input)
	if token == "" {
		return errors.New("access token cannot be empty")
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()
	// authMu serializes every session writer (login, refresh, import, and
	// logout). mu only guards short in-memory snapshots and is never held
	// while acquiring authMu, so the lock order remains consistent.

	userID, login, clientID, err := Validate(ctx, s.httpClient, token)
	if err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}
	if clientID != AndroidClientID {
		return fmt.Errorf("%w (token client ID %s)", ErrUnsupportedTokenClient, clientID)
	}

	s.mu.Lock()
	deviceID := ""
	if s.data != nil {
		deviceID = s.data.DeviceID
	}
	s.mu.Unlock()
	if deviceID == "" {
		deviceID = NewDeviceID()
	}

	newData := &model.AuthData{
		AccessToken:   model.RedactedString(token),
		AuthClientID:  AndroidClientID,
		UserID:        userID,
		Login:         login,
		DeviceID:      deviceID,
		AuthUserAgent: DefaultAndroidUserAgent,
		ObtainedAt:    time.Now().UTC(),
	}
	if err := state.AtomicWriteJSON(s.path, newData, 0600); err != nil {
		return fmt.Errorf("failed to persist credentials: %w", err)
	}

	s.mu.Lock()
	s.data = newData
	s.mu.Unlock()
	return nil
}
