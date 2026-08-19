package channel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-resty/resty/v2"
	"tdm/internal/gql"
)

var (
	spadePattern    = regexp.MustCompile(`(?i)"spade_?url"\s*:\s*"(https?://[.\w\-/:?&=]+)"`)
	settingsPattern = regexp.MustCompile(`(?i)src="((?:https?://[\w.:]+)?/config/settings\.[0-9a-f]{32}\.js|https://[\w.]+/config/settings\.[0-9a-f]{32}\.js)"`)
)

// PlaybackAccessToken holds the value and signature for stream playback.
type PlaybackAccessToken struct {
	Value     string `json:"value"`
	Signature string `json:"signature"`
}

type playbackAccessTokenResponse struct {
	StreamPlaybackAccessToken *PlaybackAccessToken `json:"streamPlaybackAccessToken"`
}

// FetchPlaybackAccessToken queries Twitch GQL for stream playback credentials.
// Returns an error if the channel is offline, does not exist, or playback token is unavailable.
func FetchPlaybackAccessToken(ctx context.Context, client *gql.Client, channelLogin string) (value, signature string, err error) {
	if client == nil {
		return "", "", errors.New("gql client cannot be nil")
	}

	raw, err := client.Do(ctx, "PlaybackAccessToken", map[string]any{
		"login": channelLogin,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch playback access token: %w", err)
	}

	resp, err := gql.UnmarshalResponse[playbackAccessTokenResponse](raw)
	if err != nil {
		return "", "", fmt.Errorf("failed to unmarshal playback access token response: %w", err)
	}

	if resp.StreamPlaybackAccessToken == nil || resp.StreamPlaybackAccessToken.Value == "" || resp.StreamPlaybackAccessToken.Signature == "" {
		return "", "", fmt.Errorf("stream playback access token unavailable for channel %q (channel is offline or does not exist)", channelLogin)
	}

	return resp.StreamPlaybackAccessToken.Value, resp.StreamPlaybackAccessToken.Signature, nil
}

// applyTwitchHeaders applies standard Twitch headers to a Resty request.
func applyTwitchHeaders(req *resty.Request, identity gql.Identity) {
	req.SetHeader("Accept", "*/*")
	req.SetHeader("Accept-Encoding", "gzip")
	req.SetHeader("Accept-Language", "en-US")
	req.SetHeader("Pragma", "no-cache")
	req.SetHeader("Cache-Control", "no-cache")
	req.SetHeader("Origin", "https://www.twitch.tv")
	req.SetHeader("Referer", "https://www.twitch.tv")

	if identity != nil {
		if cid := identity.ClientID(); cid != "" {
			req.SetHeader("Client-Id", cid)
		}
		if did := identity.DeviceID(); did != "" {
			req.SetHeader("X-Device-Id", did)
		}
		if sid := identity.SessionID(); sid != "" {
			req.SetHeader("Client-Session-Id", sid)
		}
		if ua := identity.UserAgent(); ua != "" {
			req.SetHeader("User-Agent", ua)
		}
		if token := identity.AccessToken(); token != "" {
			req.SetHeader("Authorization", "OAuth "+token)
		}
	}
}

// DiscoverSpadeURL scrapes the streamer page (and if necessary, its settings JS) to extract the Spade beacon URL.
func DiscoverSpadeURL(ctx context.Context, httpClient *resty.Client, identity gql.Identity, channelLogin string) (string, error) {
	if httpClient == nil {
		return "", errors.New("httpClient cannot be nil")
	}

	targetURL := "https://www.twitch.tv/" + channelLogin
	if httpClient.HostURL != "" {
		targetURL = "/" + strings.TrimPrefix(channelLogin, "/")
	}

	req := httpClient.R().SetContext(ctx)
	applyTwitchHeaders(req, identity)

	resp, err := req.Get(targetURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch channel page for %s: %w", channelLogin, err)
	}
	if resp.IsError() {
		return "", fmt.Errorf("channel page request for %s returned status %d", channelLogin, resp.StatusCode())
	}

	body := resp.String()
	if match := spadePattern.FindStringSubmatch(body); len(match) > 1 {
		return match[1], nil
	}

	// Step 2 fallback: search for settings JS
	settingsMatch := settingsPattern.FindStringSubmatch(body)
	if len(settingsMatch) <= 1 {
		return "", errors.New("spade_url extraction failed: step #1")
	}

	settingsURL := settingsMatch[1]
	settingsReq := httpClient.R().SetContext(ctx)
	applyTwitchHeaders(settingsReq, identity)

	settingsResp, err := settingsReq.Get(settingsURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch streamer settings JS (%s): %w", settingsURL, err)
	}
	if settingsResp.IsError() {
		return "", fmt.Errorf("streamer settings JS (%s) returned status %d", settingsURL, settingsResp.StatusCode())
	}

	settingsBody := settingsResp.String()
	if match := spadePattern.FindStringSubmatch(settingsBody); len(match) > 1 {
		return match[1], nil
	}

	return "", errors.New("spade_url extraction failed: step #2")
}
