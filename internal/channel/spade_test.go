package channel

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpade_BuildWatchPayload(t *testing.T) {
	fixedTime := time.Date(2026, 8, 19, 5, 15, 0, 123000000, time.UTC)
	events := BuildWatchPayload("123", "456", "shroud", "Rust", "263490", 999, fixedTime)

	require.Len(t, events, 1)
	ev := events[0]

	assert.Equal(t, "minute-watched", ev.Event)
	assert.Equal(t, "123", ev.Properties.BroadcastID)
	assert.Equal(t, "456", ev.Properties.ChannelID)
	assert.Equal(t, "shroud", ev.Properties.Channel)
	assert.Equal(t, "2026-08-19T05:15:00.123Z", ev.Properties.ClientTime)
	assert.Equal(t, "Rust", ev.Properties.Game)
	assert.Equal(t, "263490", ev.Properties.GameID)
	assert.False(t, ev.Properties.Hidden)
	assert.True(t, ev.Properties.IsLive)
	assert.True(t, ev.Properties.Live)
	assert.True(t, ev.Properties.LoggedIn)
	assert.Equal(t, 1, ev.Properties.MinutesLogged)
	assert.False(t, ev.Properties.Muted)
	assert.Equal(t, 999, ev.Properties.UserID)
}

func TestSpade_EncodeSpadeData_RoundTrip(t *testing.T) {
	fixedTime := time.Date(2026, 8, 19, 5, 15, 0, 0, time.UTC)
	events := BuildWatchPayload("broadcast_1", "chan_2", "streamer_login", "Game Name", "12345", 777, fixedTime)

	encoded, err := EncodeSpadeData(events)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decodedBytes, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err, "must be valid base64")

	var decodedEvents []WatchEvent
	err = json.Unmarshal(decodedBytes, &decodedEvents)
	require.NoError(t, err, "must be valid non-gzipped JSON")

	require.Len(t, decodedEvents, 1)
	assert.Equal(t, events[0], decodedEvents[0])
}

func TestSpade_ClientTimeFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "exact seconds zero milliseconds",
			input:    time.Date(2026, 8, 19, 5, 15, 0, 0, time.UTC),
			expected: "2026-08-19T05:15:00.000Z",
		},
		{
			name:     "fractional milliseconds with extra nanos truncated/formatted to 3 digits",
			input:    time.Date(2026, 8, 19, 12, 34, 56, 789000000, time.UTC),
			expected: "2026-08-19T12:34:56.789Z",
		},
		{
			name:     "non-UTC timezone converted to UTC",
			input:    time.Date(2026, 8, 19, 8, 15, 0, 50000000, time.FixedZone("EDT", -4*3600)),
			expected: "2026-08-19T12:15:00.050Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isonow(tc.input))
		})
	}
}

func TestSpade_ZeroGzipInSource(t *testing.T) {
	content, err := os.ReadFile("spade.go")
	require.NoError(t, err)
	assert.NotContains(t, string(content), "gzip", "spade.go must not contain gzip references")
}

