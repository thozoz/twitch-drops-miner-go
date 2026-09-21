package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

// ErrReauthRequired indicates that stored credentials are missing, invalid, or refresh failed.
var ErrReauthRequired = errors.New("credentials invalid or refresh failed: run 'tdm auth login'")

type refreshResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	Scope        []string `json:"scope"`
	TokenType    string   `json:"token_type"`
}

// RefreshOnUnauthorized attempts to refresh the access token using the stored refresh token.
// It is single-flighted across concurrent callers via Session's internal mutex.
func (s *Session) RefreshOnUnauthorized(ctx context.Context) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	s.mu.Lock()
	if s.data == nil {
		s.mu.Unlock()
		return fmt.Errorf("no stored credentials: %w", ErrReauthRequired)
	}
	data := *s.data
	s.mu.Unlock()

	if data.RefreshToken.Reveal() == "" && data.AuthClientID != "" && data.AuthClientID != AndroidClientID {
		captured, err := captureBrowserSession(ctx, s.browserProfileDir(), nil)
		if err != nil {
			return fmt.Errorf("browser session renewal failed: %v: %w", err, ErrReauthRequired)
		}
		if err := s.persistBrowserSession(ctx, captured); err != nil {
			return fmt.Errorf("browser session renewal failed: %w", err)
		}
		return nil
	}
	if data.RefreshToken.Reveal() == "" {
		return fmt.Errorf("no refresh token available: %w", ErrReauthRequired)
	}

	// Redundant call check: if another goroutine recently completed refresh, return success immediately.
	if time.Since(data.ObtainedAt) < 5*time.Second {
		return nil
	}

	client := s.httpClient
	if client == nil {
		return fmt.Errorf("http client is nil: %w", ErrReauthRequired)
	}

	baseURL := DefaultAuthBaseURL
	if client.HostURL != "" {
		baseURL = client.HostURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/oauth2/token"

	authClientID := data.AuthClientID
	if authClientID == "" {
		// Auth files written before auth_client_id was introduced used the Android client.
		authClientID = AndroidClientID
	}

	req := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("Client-Id", authClientID).
		SetFormData(map[string]string{
			"client_id":     authClientID,
			"grant_type":    "refresh_token",
			"refresh_token": data.RefreshToken.Reveal(),
		})

	if data.AuthUserAgent != "" {
		req.SetHeader("User-Agent", data.AuthUserAgent)
	}
	if data.DeviceID != "" {
		req.SetHeader("X-Device-Id", data.DeviceID)
	}

	resp, err := req.Post(endpoint)
	if err != nil {
		return fmt.Errorf("token refresh network request failed: %w", ErrReauthRequired)
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("token refresh endpoint returned %d (%s): %w", resp.StatusCode(), resp.String(), ErrReauthRequired)
	}

	var refreshResult refreshResponse
	if err := json.Unmarshal(resp.Body(), &refreshResult); err != nil {
		return fmt.Errorf("failed to parse refresh response: %w", ErrReauthRequired)
	}

	if refreshResult.AccessToken == "" {
		return fmt.Errorf("refresh response contained empty access_token: %w", ErrReauthRequired)
	}

	data.AccessToken = model.RedactedString(refreshResult.AccessToken)
	if refreshResult.RefreshToken != "" {
		data.RefreshToken = model.RedactedString(refreshResult.RefreshToken)
	}
	data.ObtainedAt = time.Now().UTC()

	if s.path != "" {
		if err := state.AtomicWriteJSON(s.path, &data, 0600); err != nil {
			return fmt.Errorf("failed to persist refreshed credentials: %w", err)
		}
	}
	s.mu.Lock()
	s.data = &data
	s.mu.Unlock()

	return nil
}
