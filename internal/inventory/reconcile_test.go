package inventory

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/gql"
)

func TestReconcile_ReconcileMinutes(t *testing.T) {
	// Test 1: CurrentMinutes=5, RequiredMinutes=10, serverMinutes=7 -> 7, changed=true
	t.Run("server value updates progress", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))

		drop := &TimedDrop{
			ID:              "drop-1",
			Name:            "Test Drop 1",
			RequiredMinutes: 10,
			CurrentMinutes:  5,
		}

		changed := ReconcileMinutes(drop, 7, logger)
		assert.True(t, changed)
		assert.Equal(t, 7, drop.CurrentMinutes)
		logOutput := buf.String()
		assert.Contains(t, logOutput, "server state wins")
		assert.Contains(t, logOutput, `"local_minutes":5`)
		assert.Contains(t, logOutput, `"server_minutes":7`)
		assert.Contains(t, logOutput, `"drop":"Test Drop 1"`)
	})

	// Test 2: server value 15 > RequiredMinutes=10 -> clamps to 10
	t.Run("server value clamped to required minutes", func(t *testing.T) {
		drop := &TimedDrop{
			ID:              "drop-2",
			Name:            "Test Drop 2",
			RequiredMinutes: 10,
			CurrentMinutes:  5,
		}

		changed := ReconcileMinutes(drop, 15, nil)
		assert.True(t, changed)
		assert.Equal(t, 10, drop.CurrentMinutes)
	})

	// Test 3: server value -3 -> clamps to 0
	t.Run("negative server value clamped to zero", func(t *testing.T) {
		drop := &TimedDrop{
			ID:              "drop-3",
			Name:            "Test Drop 3",
			RequiredMinutes: 10,
			CurrentMinutes:  5,
		}

		changed := ReconcileMinutes(drop, -3, nil)
		assert.True(t, changed)
		assert.Equal(t, 0, drop.CurrentMinutes)
	})

	// Test 4: server value equal to current value -> changed=false, no log
	t.Run("equal server value returns false without logging", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))

		drop := &TimedDrop{
			ID:              "drop-4",
			Name:            "Test Drop 4",
			RequiredMinutes: 10,
			CurrentMinutes:  5,
		}

		changed := ReconcileMinutes(drop, 5, logger)
		assert.False(t, changed)
		assert.Equal(t, 5, drop.CurrentMinutes)
		assert.Empty(t, buf.String())
	})
}

func TestReconcile_FetchCurrentDropProgress(t *testing.T) {
	fixtureData, err := os.ReadFile("../../testdata/fixtures/gql_drop_current_session_context.json")
	require.NoError(t, err)

	t.Run("active drop session returns drop info", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtureData)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		dropID, minutes, ok, err := FetchCurrentDropProgress(context.Background(), client, "channel-123")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "drop-abc", dropID)
		assert.Equal(t, 7, minutes)
	})

	t.Run("null drop session returns ok=false and nil error", func(t *testing.T) {
		nullResp := []byte(`{
			"data": {
				"currentUser": {
					"dropCurrentSession": null
				}
			}
		}`)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(nullResp)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		dropID, minutes, ok, err := FetchCurrentDropProgress(context.Background(), client, "channel-123")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, dropID)
		assert.Equal(t, 0, minutes)
	})
}
