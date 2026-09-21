package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

// Session manages the current authenticated session state, persistence, and token lifecycle.
type Session struct {
	data       *model.AuthData
	path       string
	httpClient *resty.Client
	sessionID  string
	mu         sync.Mutex
	authMu     sync.Mutex
}

// LoadOrEmpty loads an AuthData session from disk, or returns an empty, unauthenticated Session.
func LoadOrEmpty(path string, httpClient *resty.Client) (*Session, error) {
	if httpClient == nil {
		httpClient = resty.New()
	}

	s := &Session{
		path:       path,
		httpClient: httpClient,
		sessionID:  NewSessionID(),
		data:       &model.AuthData{},
	}

	var data model.AuthData
	err := state.ReadJSON(path, &data)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}

	s.data = &data
	return s, nil
}

// Authenticated reports whether the session has an access token.
func (s *Session) Authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data != nil && s.data.AccessToken.Reveal() != ""
}

// ClientID returns the OAuth client that issued the session token.
// Auth files from older releases have no AuthClientID and were issued by the Android client.
func (s *Session) ClientID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil && s.data.AuthClientID != "" {
		return s.data.AuthClientID
	}
	return AndroidClientID
}

// DeviceID returns the persisted device ID (satisfies gql.Identity).
func (s *Session) DeviceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil {
		return s.data.DeviceID
	}
	return ""
}

// SessionID returns the ephemeral session ID generated once per process (satisfies gql.Identity).
func (s *Session) SessionID() string {
	return s.sessionID
}

// UserAgent returns the user agent associated with the session token's issuing client.
func (s *Session) UserAgent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil {
		if s.data.AuthUserAgent != "" {
			return s.data.AuthUserAgent
		}
		if s.data.AuthClientID == WebClientID {
			return WebUserAgent
		}
	}
	return AndroidUserAgents[0]
}

// AccessToken returns the revealed plaintext access token (satisfies gql.Identity).
func (s *Session) AccessToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil {
		return s.data.AccessToken.Reveal()
	}
	return ""
}

// Data returns a copy or pointer to the underlying AuthData.
func (s *Session) Data() *model.AuthData {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return &model.AuthData{}
	}
	cpy := *s.data
	return &cpy
}

// Login executes the OAuth Device Code Flow, validates the token, and persists the session.
// It synchronizes with other auth operations via authMu to avoid holding s.mu across network calls.
func (s *Session) Login(ctx context.Context, onCode func(verificationURI, userCode string)) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	deviceID := ""
	userAgent := ""
	if s.data != nil {
		deviceID = s.data.DeviceID
		userAgent = s.data.AuthUserAgent
	}
	s.mu.Unlock()

	if deviceID == "" {
		deviceID = NewDeviceID()
	}
	if userAgent == "" {
		userAgent = AndroidUserAgents[0]
	}

	accessToken, refreshToken, err := RunDeviceCodeFlow(ctx, s.httpClient, deviceID, userAgent, onCode)
	if err != nil {
		return fmt.Errorf("device code flow failed: %w", err)
	}

	userID, login, respClientID, err := Validate(ctx, s.httpClient, accessToken)
	if err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}

	if respClientID != AndroidClientID {
		return fmt.Errorf("client ID mismatch: expected %s, got %s", AndroidClientID, respClientID)
	}

	newData := &model.AuthData{
		AccessToken:   model.RedactedString(accessToken),
		RefreshToken:  model.RedactedString(refreshToken),
		AuthClientID:  AndroidClientID,
		UserID:        userID,
		Login:         login,
		DeviceID:      deviceID,
		AuthUserAgent: userAgent,
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

// SetToken validates an existing OAuth token, determines the client identity, and persists the session.
func (s *Session) SetToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, `"'`)
	token = strings.TrimPrefix(token, "oauth:")
	token = strings.TrimPrefix(token, "OAuth ")
	if idx := strings.Index(token, "auth-token="); idx != -1 {
		token = token[idx+len("auth-token="):]
		if endIdx := strings.IndexAny(token, "; 	\r\n"); endIdx != -1 {
			token = token[:endIdx]
		}
		token = strings.Trim(token, `"'`)
	}
	if token == "" {
		return errors.New("access token cannot be empty")
	}

	userID, login, clientID, err := Validate(ctx, s.httpClient, token)
	if err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	deviceID := ""
	if s.data != nil {
		deviceID = s.data.DeviceID
	}
	s.mu.Unlock()

	if deviceID == "" {
		deviceID = NewDeviceID()
	}

	authClientID := clientID
	if authClientID == "" {
		authClientID = WebClientID
	}

	authUserAgent := WebUserAgent
	if authClientID == AndroidClientID {
		authUserAgent = AndroidUserAgents[0]
	}

	newData := &model.AuthData{
		AccessToken:   model.RedactedString(token),
		RefreshToken:  "",
		AuthClientID:  authClientID,
		UserID:        userID,
		Login:         login,
		DeviceID:      deviceID,
		AuthUserAgent: authUserAgent,
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

// Logout removes the persisted credentials file and resets the in-memory session.
func (s *Session) Logout() error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = &model.AuthData{}
	if s.path != "" {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove auth file: %w", err)
		}
	}
	return nil
}
