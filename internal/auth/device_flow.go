package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// DefaultAuthBaseURL is Twitch's default OAuth identity server URL.
const DefaultAuthBaseURL = "https://id.twitch.tv"

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	Interval        int    `json:"interval"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
}

type tokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	Scope        []string `json:"scope"`
	TokenType    string   `json:"token_type"`
}

type deviceFlowConfig struct {
	baseURL string
	sleepFn func(ctx context.Context, d time.Duration) error
	nowFn   func() time.Time
}

// DeviceFlowOption allows customizing Device Code Flow behavior (e.g. in tests).
type DeviceFlowOption func(*deviceFlowConfig)

// WithBaseURL overrides the default OAuth identity base URL (https://id.twitch.tv).
func WithBaseURL(url string) DeviceFlowOption {
	return func(c *deviceFlowConfig) {
		c.baseURL = url
	}
}

// WithSleep overrides the sleep function between token poll attempts.
func WithSleep(fn func(ctx context.Context, d time.Duration) error) DeviceFlowOption {
	return func(c *deviceFlowConfig) {
		c.sleepFn = fn
	}
}

// WithNow overrides the current time provider for token expiry tracking.
func WithNow(fn func() time.Time) DeviceFlowOption {
	return func(c *deviceFlowConfig) {
		c.nowFn = fn
	}
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// RunDeviceCodeFlow executes the OAuth 2.0 Device Authorization Grant flow for the ANDROID_APP client.
// It requests a device code, calls onCode with the verification URI and user code, sleeps for the
// server-specified interval BEFORE the first poll (per D-01/twitch.py:171), and polls until authorization
// succeeds or context is cancelled. If the code expires, it requests a fresh code and continues.
func RunDeviceCodeFlow(
	ctx context.Context,
	httpClient *resty.Client,
	deviceID string,
	userAgent string,
	onCode func(verificationURI, userCode string),
	opts ...DeviceFlowOption,
) (accessToken, refreshToken string, err error) {
	cfg := deviceFlowConfig{
		baseURL: DefaultAuthBaseURL,
		sleepFn: defaultSleep,
		nowFn:   time.Now,
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

	deviceURL := cfg.baseURL + "/oauth2/device"
	tokenURL := cfg.baseURL + "/oauth2/token"

	headers := map[string]string{
		"Accept":          "application/json",
		"Accept-Encoding": "gzip",
		"Accept-Language": "en-US",
		"Cache-Control":   "no-cache",
		"Client-Id":       AndroidClientID,
		"Host":            "id.twitch.tv",
		"Origin":          AndroidClientURL,
		"Pragma":          "no-cache",
		"Referer":         AndroidClientURL,
		"User-Agent":      userAgent,
		"X-Device-Id":     deviceID,
	}

	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}

		now := cfg.nowFn()
		resp, err := httpClient.R().
			SetContext(ctx).
			SetHeaders(headers).
			SetFormData(map[string]string{
				"client_id": AndroidClientID,
				"scopes":    "",
			}).
			Post(deviceURL)
		if err != nil {
			return "", "", fmt.Errorf("request device code: %w", err)
		}
		if resp.StatusCode() != 200 {
			return "", "", fmt.Errorf("request device code failed with status %d: %s", resp.StatusCode(), resp.String())
		}

		var devResp deviceCodeResponse
		if err := json.Unmarshal(resp.Body(), &devResp); err != nil {
			return "", "", fmt.Errorf("parse device code response: %w", err)
		}

		interval := time.Duration(devResp.Interval) * time.Second
		if interval <= 0 {
			interval = 5 * time.Second
		}
		expiresAt := now.Add(time.Duration(devResp.ExpiresIn) * time.Second)

		if onCode != nil {
			onCode(devResp.VerificationURI, devResp.UserCode)
		}

		tokenFormData := map[string]string{
			"client_id":   AndroidClientID,
			"device_code": devResp.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		}

		// Polling loop for current device code
		for {
			// Per D-01 / twitch.py:171: Sleep BEFORE the first poll and every subsequent poll
			if err := cfg.sleepFn(ctx, interval); err != nil {
				return "", "", err
			}

			if !cfg.nowFn().Before(expiresAt) {
				// Device code expired, request a fresh code in outer loop
				break
			}

			tResp, err := httpClient.R().
				SetContext(ctx).
				SetHeaders(headers).
				SetFormData(tokenFormData).
				Post(tokenURL)
			if err != nil {
				// Network error during poll; check expiry or retry
				if !cfg.nowFn().Before(expiresAt) {
					break
				}
				continue
			}

			if tResp.StatusCode() == 200 {
				var tokResp tokenResponse
				if err := json.Unmarshal(tResp.Body(), &tokResp); err != nil {
					return "", "", fmt.Errorf("parse token response: %w", err)
				}
				return tokResp.AccessToken, tokResp.RefreshToken, nil
			}

			// Non-200 (typically 400 "authorization_pending") means user hasn't authorized yet
			if !cfg.nowFn().Before(expiresAt) {
				break
			}
		}
	}
}
