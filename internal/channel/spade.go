package channel

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// WatchProperties holds the exact telemetry fields sent in a minute-watched event.
// Field order and JSON tags mirror the Python reference implementation.
type WatchProperties struct {
	BroadcastID   string `json:"broadcast_id"`
	ChannelID     string `json:"channel_id"`
	Channel       string `json:"channel"`
	ClientTime    string `json:"client_time"`
	Game          string `json:"game"`
	GameID        string `json:"game_id"`
	Hidden        bool   `json:"hidden"`
	IsLive        bool   `json:"is_live"`
	Live          bool   `json:"live"`
	LoggedIn      bool   `json:"logged_in"`
	MinutesLogged int    `json:"minutes_logged"`
	Muted         bool   `json:"muted"`
	UserID        int    `json:"user_id"`
}

// WatchEvent represents a single Spade telemetry event.
type WatchEvent struct {
	Event      string          `json:"event"`
	Properties WatchProperties `json:"properties"`
}

// isonow formats a timestamp in UTC as RFC3339 with millisecond precision and a trailing 'Z'.
func isonow(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05.000Z")
}

// BuildWatchPayload constructs a single-element slice of WatchEvent for a minute-watched beacon.
func BuildWatchPayload(broadcastID, channelID, channelLogin, gameName, gameID string, userID int, now time.Time) []WatchEvent {
	return []WatchEvent{
		{
			Event: "minute-watched",
			Properties: WatchProperties{
				BroadcastID:   broadcastID,
				ChannelID:     channelID,
				Channel:       channelLogin,
				ClientTime:    isonow(now),
				Game:          gameName,
				GameID:        gameID,
				Hidden:        false,
				IsLive:        true,
				Live:          true,
				LoggedIn:      true,
				MinutesLogged: 1,
				Muted:         false,
				UserID:        userID,
			},
		},
	}
}

// EncodeSpadeData marshals events to minified JSON and returns standard base64 string.
// Note: Spade HTTP POST data uses raw base64 JSON without compression.
func EncodeSpadeData(events []WatchEvent) (string, error) {
	data, err := json.Marshal(events)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
