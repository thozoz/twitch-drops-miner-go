package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tdm/internal/model"
	"tdm/internal/state"
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.data == nil || s.data.RefreshToken.Reveal() == "" {
		return fmt.Errorf("no refresh token available: %w", ErrReauthRequired)
	}

	// Redundant call check: if another goroutine recently completed refresh, return success immediately.
	if time.Since(s.data.ObtainedAt) < 5*time.Second {
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

	req := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("Client-Id", AndroidClientID).
		SetFormData(map[string]string{
			"client_id":     AndroidClientID,
			"grant_type":    "refresh_token",
			"refresh_token": s.data.RefreshToken.Reveal(),
		})

	if s.data.UserAgent != "" {
		req.SetHeader("User-Agent", s.data.UserAgent)
	}
	if s.data.DeviceID != "" {
		req.SetHeader("X-Device-Id", s.data.DeviceID)
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

	s.data.AccessToken = model.RedactedString(refreshResult.AccessToken)
	if refreshResult.RefreshToken != "" {
		s.data.RefreshToken = model.RedactedString(refreshResult.RefreshToken)
	}
	s.data.ObtainedAt = time.Now().UTC()

	if s.path != "" {
		if err := state.AtomicWriteJSON(s.path, s.data, 0600); err != nil {
			return fmt.Errorf("failed to persist refreshed credentials: %w", err)
		}
	}

	return nil
}
