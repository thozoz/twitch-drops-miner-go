package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil && s.data.ClientSessionID != "" {
		return s.data.ClientSessionID
	}
	return s.sessionID
}

// UserAgent returns the user agent associated with the session token's issuing client.
func (s *Session) UserAgent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil && s.data.AuthUserAgent != "" {
		return s.data.AuthUserAgent
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

// IntegrityToken returns the browser-issued Client-Integrity token, if one is stored.
func (s *Session) IntegrityToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != nil {
		return s.data.ClientIntegrity.Reveal()
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

// LoginWithBrowser opens a normal Chromium window and persists the complete
// authenticated Web GQL identity captured from Twitch's own request.
func (s *Session) LoginWithBrowser(ctx context.Context, ready func()) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	captured, err := captureBrowserSession(ctx, s.browserProfileDir(), ready)
	if err != nil {
		return err
	}
	return s.persistBrowserSession(ctx, captured)
}

// RefreshIntegrity reopens the persistent Chromium profile and captures a fresh
// browser-authenticated GQL identity. It also accepts an OAuth token rotation.
func (s *Session) RefreshIntegrity(ctx context.Context) error {
	s.mu.Lock()
	observedToken := ""
	if s.data != nil {
		observedToken = s.data.ClientIntegrity.Reveal()
	}
	s.mu.Unlock()

	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	isWebSession := s.data != nil && s.data.AuthClientID != "" && s.data.AuthClientID != AndroidClientID
	currentToken := ""
	if s.data != nil {
		currentToken = s.data.ClientIntegrity.Reveal()
	}
	s.mu.Unlock()
	if !isWebSession {
		return errBrowserSessionRequired
	}
	// Another failed request may already have renewed the shared session while
	// this caller waited for authMu (notably concurrent GQL batch chunks).
	if currentToken != observedToken {
		return nil
	}

	captured, err := captureBrowserSession(ctx, s.browserProfileDir(), nil)
	if err != nil {
		return fmt.Errorf("refresh browser integrity session: %w", err)
	}
	return s.persistBrowserSession(ctx, captured)
}

func (s *Session) persistBrowserSession(ctx context.Context, captured BrowserSession) error {
	if captured.AccessToken == "" || captured.ClientID == "" || captured.ClientIntegrity == "" || captured.DeviceID == "" || captured.UserAgent == "" {
		return errors.New("captured browser session is incomplete")
	}

	userID, login, tokenClientID, err := Validate(ctx, s.httpClient, captured.AccessToken)
	if err != nil {
		return fmt.Errorf("browser token validation failed: %w", err)
	}
	if tokenClientID != "" && tokenClientID != captured.ClientID {
		return fmt.Errorf("browser client ID mismatch: request used %s, token belongs to %s", captured.ClientID, tokenClientID)
	}

	now := time.Now().UTC()
	newData := &model.AuthData{
		AccessToken:       model.RedactedString(captured.AccessToken),
		ClientIntegrity:   model.RedactedString(captured.ClientIntegrity),
		AuthClientID:      captured.ClientID,
		UserID:            userID,
		Login:             login,
		DeviceID:          captured.DeviceID,
		ClientSessionID:   captured.ClientSessionID,
		AuthUserAgent:     captured.UserAgent,
		ObtainedAt:        now,
		IntegrityCaptured: now,
	}
	if err := state.AtomicWriteJSON(s.path, newData, 0600); err != nil {
		return fmt.Errorf("failed to persist browser credentials: %w", err)
	}

	s.mu.Lock()
	s.data = newData
	s.mu.Unlock()
	return nil
}

func (s *Session) browserProfileDir() string {
	if s.path == "" {
		return filepath.Join(os.TempDir(), "tdm-browser-profile")
	}
	return filepath.Join(filepath.Dir(s.path), "browser-profile")
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
		if err := os.RemoveAll(s.browserProfileDir()); err != nil {
			return fmt.Errorf("failed to remove browser profile: %w", err)
		}
	}
	return nil
}
