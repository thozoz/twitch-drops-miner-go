package channel

import (
	"context"
	"encoding/json"
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

type mockIdentity struct {
	clientID    string
	deviceID    string
	sessionID   string
	userAgent   string
	accessToken string
}

func (m *mockIdentity) ClientID() string    { return m.clientID }
func (m *mockIdentity) DeviceID() string    { return m.deviceID }
func (m *mockIdentity) SessionID() string   { return m.sessionID }
func (m *mockIdentity) UserAgent() string   { return m.userAgent }
func (m *mockIdentity) AccessToken() string { return m.accessToken }

func TestDiscoverSpadeURL_DirectHTML(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/fixtures/spade_channel_page.html")
	require.NoError(t, err)

	var recordedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	ident := &mockIdentity{
		clientID:    "test-client-id",
		deviceID:    "test-device-id",
		sessionID:   "test-session-id",
		userAgent:   "test-user-agent",
		accessToken: "test-token",
	}

	httpClient := resty.New().SetHostURL(server.URL)
	spadeURL, err := DiscoverSpadeURL(context.Background(), httpClient, ident, "streamer_online")
	require.NoError(t, err)
	assert.Equal(t, "https://video-edge-example.spade.twitch.tv/spade", spadeURL)

	assert.Equal(t, "test-client-id", recordedHeaders.Get("Client-Id"))
	assert.Equal(t, "test-device-id", recordedHeaders.Get("X-Device-Id"))
	assert.Equal(t, "test-session-id", recordedHeaders.Get("Client-Session-Id"))
	assert.Equal(t, "test-user-agent", recordedHeaders.Get("User-Agent"))
	assert.Equal(t, "OAuth test-token", recordedHeaders.Get("Authorization"))
	assert.Equal(t, "https://www.twitch.tv", recordedHeaders.Get("Origin"))
	assert.Equal(t, "https://www.twitch.tv", recordedHeaders.Get("Referer"))
}

func TestDiscoverSpadeURL_SettingsJSFallback(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/streamer_fallback" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			html := `<html><head><script src="` + server.URL + `/config/settings.0123456789abcdef0123456789abcdef.js"></script></head></html>`
			_, _ = w.Write([]byte(html))
			return
		}
		if r.URL.Path == "/config/settings.0123456789abcdef0123456789abcdef.js" {
			w.Header().Set("Content-Type", "application/javascript")
			w.WriteHeader(http.StatusOK)
			js := `var spadeConfig = {"spade_url":"https://video-edge-fallback.spade.twitch.tv/spade"};`
			_, _ = w.Write([]byte(js))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	httpClient := resty.New().SetHostURL(server.URL)
	spadeURL, err := DiscoverSpadeURL(context.Background(), httpClient, nil, "streamer_fallback")
	require.NoError(t, err)
	assert.Equal(t, "https://video-edge-fallback.spade.twitch.tv/spade", spadeURL)
}

func TestDiscoverSpadeURL_ExtractionFailures(t *testing.T) {
	t.Run("step 1 failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body>No spade url or settings here</body></html>`))
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		_, err := DiscoverSpadeURL(context.Background(), httpClient, nil, "bad_channel")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "step #1")
	})

	t.Run("step 2 failure", func(t *testing.T) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bad_settings" {
				w.WriteHeader(http.StatusOK)
				html := `<html><head><script src="` + server.URL + `/config/settings.0123456789abcdef0123456789abcdef.js"></script></head></html>`
				_, _ = w.Write([]byte(html))
				return
			}
			if r.URL.Path == "/config/settings.0123456789abcdef0123456789abcdef.js" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`var somethingElse = 123;`))
				return
			}
			http.NotFound(w, r)
		}))
		defer server.Close()

		httpClient := resty.New().SetHostURL(server.URL)
		_, err := DiscoverSpadeURL(context.Background(), httpClient, nil, "bad_settings")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "step #2")
	})
}

func TestFetchPlaybackAccessToken_Success(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/fixtures/gql_playback_access_token.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var singleOp struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&singleOp)
		assert.Equal(t, "PlaybackAccessToken", singleOp.OperationName)
		assert.Equal(t, "streamer_online", singleOp.Variables["login"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

	val, sig, err := FetchPlaybackAccessToken(context.Background(), gqlClient, "streamer_online")
	require.NoError(t, err)
	assert.Contains(t, val, `"channel":"streamer_online"`)
	assert.Equal(t, "abcdef0123456789abcdef0123456789abcdef01", sig)
}

func TestFetchPlaybackAccessToken_OfflineOrNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":null}}`))
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

	_, _, err = FetchPlaybackAccessToken(context.Background(), gqlClient, "offline_channel")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "offline or does not exist")
}

func TestPlayback_ZeroUsherInSource(t *testing.T) {
	content, err := os.ReadFile("playback.go")
	require.NoError(t, err)
	assert.NotContains(t, string(content), "usher", "playback.go must not contain usher references")
}
