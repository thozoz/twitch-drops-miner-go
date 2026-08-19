package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

// ErrTokenInvalid is returned when Twitch returns 401 Unauthorized for the token.
var ErrTokenInvalid = errors.New("oauth token is invalid or expired (401)")

type validateResponse struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	Scopes    []string `json:"scopes"`
	UserID    string   `json:"user_id"`
	ExpiresIn int      `json:"expires_in"`
}

type validateConfig struct {
	baseURL string
}

// ValidateOption allows customizing Validate behavior (e.g. in tests).
type ValidateOption func(*validateConfig)

// WithValidateBaseURL overrides the default OAuth identity base URL (https://id.twitch.tv).
func WithValidateBaseURL(url string) ValidateOption {
	return func(c *validateConfig) {
		c.baseURL = url
	}
}

// Validate calls GET https://id.twitch.tv/oauth2/validate with header `Authorization: OAuth <accessToken>`
// (matching twitch.py:399-401). It returns the verified userID, login, and clientID.
// Non-200 responses return typed errors (e.g. ErrTokenInvalid for 401).
// Per D-07, client ID mismatch detection is surfaced to the caller via clientID.
func Validate(
	ctx context.Context,
	httpClient *resty.Client,
	accessToken string,
	opts ...ValidateOption,
) (userID int, login string, clientID string, err error) {
	cfg := validateConfig{
		baseURL: DefaultAuthBaseURL,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if httpClient == nil {
		httpClient = resty.New()
	}
	if cfg.baseURL == DefaultAuthBaseURL && httpClient.HostURL != "" {
		cfg.baseURL = httpClient.HostURL
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")

	validateURL := cfg.baseURL + "/oauth2/validate"

	resp, err := httpClient.R().
		SetContext(ctx).
		SetHeader("Authorization", "OAuth "+accessToken).
		Get(validateURL)
	if err != nil {
		return 0, "", "", fmt.Errorf("oauth validate request: %w", err)
	}

	if resp.StatusCode() == 401 {
		return 0, "", "", ErrTokenInvalid
	}
	if resp.StatusCode() != 200 {
		return 0, "", "", fmt.Errorf("oauth validate failed with status %d: %s", resp.StatusCode(), resp.String())
	}

	var valResp validateResponse
	if err := json.Unmarshal(resp.Body(), &valResp); err != nil {
		return 0, "", "", fmt.Errorf("parse oauth validate response: %w", err)
	}

	uid, err := strconv.Atoi(valResp.UserID)
	if err != nil {
		return 0, "", "", fmt.Errorf("invalid user_id in oauth validate response %q: %w", valResp.UserID, err)
	}

	return uid, valResp.Login, valResp.ClientID, nil
}
