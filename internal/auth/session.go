package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"tdm/internal/model"
	"tdm/internal/state"
)

// Session manages the current authenticated session state, persistence, and token lifecycle.
type Session struct {
	data       *model.AuthData
	path       string
	httpClient *resty.Client
	sessionID  string
	mu         sync.Mutex
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

// ClientID returns the client ID used by the session (satisfies gql.Identity).
func (s *Session) ClientID() string {
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

// UserAgent returns the persisted user agent (satisfies gql.Identity).
func (s *Session) UserAgent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil {
		return s.data.UserAgent
	}
	return ""
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
func (s *Session) Login(ctx context.Context, onCode func(verificationURI, userCode string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	deviceID := ""
	userAgent := ""
	if s.data != nil {
		deviceID = s.data.DeviceID
		userAgent = s.data.UserAgent
	}

	if deviceID == "" {
		deviceID = NewDeviceID()
	}
	if userAgent == "" {
		userAgent = PickUserAgent()
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

	s.data = &model.AuthData{
		AccessToken:  model.RedactedString(accessToken),
		RefreshToken: model.RedactedString(refreshToken),
		UserID:       userID,
		Login:        login,
		DeviceID:     deviceID,
		UserAgent:    userAgent,
		ObtainedAt:   time.Now().UTC(),
	}

	if err := state.AtomicWriteJSON(s.path, s.data, 0600); err != nil {
		return fmt.Errorf("failed to persist credentials: %w", err)
	}

	return nil
}

// Logout removes the persisted credentials file and resets the in-memory session.
func (s *Session) Logout() error {
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
