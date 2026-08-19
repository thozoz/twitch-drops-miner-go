package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactedString_String(t *testing.T) {
	secret := RedactedString("supersecret123")
	assert.Equal(t, "[REDACTED]", secret.String())
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%s", secret))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%v", secret))
	assert.Equal(t, "\"[REDACTED]\"", fmt.Sprintf("%q", secret))
}

func TestRedactedString_Reveal(t *testing.T) {
	raw := "supersecret123"
	secret := RedactedString(raw)
	assert.Equal(t, raw, secret.Reveal())
}

func TestRedactedString_SlogLogging(t *testing.T) {
	raw := "my-very-secret-token"
	secret := RedactedString(raw)

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)

	logger.Info("authenticating", "token", secret)

	output := buf.String()
	assert.Contains(t, output, "token=[REDACTED]")
	assert.NotContains(t, output, raw)
}

func TestAuthData_JSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond).UTC()
	auth := AuthData{
		AccessToken:  RedactedString("oauth-access-token"),
		RefreshToken: RedactedString("oauth-refresh-token"),
		UserID:       12345678,
		Login:        "testuser",
		DeviceID:     "0123456789abcdef0123456789abcdef",
		UserAgent:    "Dalvik/2.1.0 (Linux; U; Android 16; SM-S911B) tv.twitch.android.app/25.3.0",
		ObtainedAt:   now,
	}

	data, err := json.Marshal(auth)
	require.NoError(t, err)

	// JSON payload must contain the actual tokens for disk persistence
	jsonStr := string(data)
	assert.Contains(t, jsonStr, "oauth-access-token")
	assert.Contains(t, jsonStr, "oauth-refresh-token")

	var decoded AuthData
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, auth.UserID, decoded.UserID)
	assert.Equal(t, auth.Login, decoded.Login)
	assert.Equal(t, auth.DeviceID, decoded.DeviceID)
	assert.Equal(t, auth.UserAgent, decoded.UserAgent)
	assert.Equal(t, "oauth-access-token", decoded.AccessToken.Reveal())
	assert.Equal(t, "oauth-refresh-token", decoded.RefreshToken.Reveal())
	assert.Equal(t, "[REDACTED]", decoded.AccessToken.String())
	assert.Equal(t, "[REDACTED]", decoded.RefreshToken.String())
}
