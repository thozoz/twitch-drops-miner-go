package auth

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAndroidUserAgents(t *testing.T) {
	require.Len(t, AndroidUserAgents, 7, "AndroidUserAgents must contain exactly 7 user agents")
	for i, ua := range AndroidUserAgents {
		assert.True(t, strings.HasPrefix(ua, "Dalvik/2.1.0"), "UA[%d] must start with Dalvik/2.1.0: %s", i, ua)
		assert.Contains(t, ua, "tv.twitch.android.app/25.3.0/2503006")
	}
}

func TestPickUserAgent(t *testing.T) {
	picked := PickUserAgent()
	assert.NotEmpty(t, picked)
	assert.Contains(t, AndroidUserAgents, picked)
}

func TestNewDeviceID(t *testing.T) {
	hexRegex := regexp.MustCompile(`^[0-9a-f]{32}$`)
	id1 := NewDeviceID()
	id2 := NewDeviceID()

	assert.True(t, hexRegex.MatchString(id1), "id1 must be 32 hex chars: %s", id1)
	assert.True(t, hexRegex.MatchString(id2), "id2 must be 32 hex chars: %s", id2)
	assert.NotEqual(t, id1, id2, "two consecutive calls to NewDeviceID must return different values")
}

func TestNewSessionID(t *testing.T) {
	hexRegex := regexp.MustCompile(`^[0-9a-f]{16}$`)
	id1 := NewSessionID()
	id2 := NewSessionID()

	assert.True(t, hexRegex.MatchString(id1), "id1 must be 16 hex chars: %s", id1)
	assert.True(t, hexRegex.MatchString(id2), "id2 must be 16 hex chars: %s", id2)
	assert.NotEqual(t, id1, id2, "two consecutive calls to NewSessionID must return different values")
}
