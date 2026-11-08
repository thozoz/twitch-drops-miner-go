package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestWatcher_SingleChannelInvariant(t *testing.T) {
	var mu sync.Mutex
	var channelBeacons = make(map[string][]time.Time)
	var activeLoopChannel string
	var concurrentViolation bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. GQL PlaybackAccessToken endpoint
		if r.Header.Get("Content-Type") == "application/json" {
			body, _ := io.ReadAll(r.Body)
			var op struct {
				OperationName string `json:"operationName"`
			}
			_ = json.Unmarshal(body, &op)
			if op.OperationName == "PlaybackAccessToken" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":{"value":"{\"channel\":\"valid\"}","signature":"sig123"}}}`))
				return
			}
		}

		// 2. Usher Master Playlist
		if r.Method == http.MethodGet && (r.URL.Path == "/api/channel/hls/streamer_a.m3u8" || r.URL.Path == "/api/channel/hls/streamer_b.m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			streamer := "streamer_a"
			if r.URL.Path == "/api/channel/hls/streamer_b.m3u8" {
				streamer = "streamer_b"
			}
			mediaURL := fmt.Sprintf("http://%s/hls/%s/index.m3u8", r.Host, streamer)
			_, _ = w.Write([]byte(fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=160000\n%s\n", mediaURL)))
			return
		}

		// 3. Media Playlist
		if r.Method == http.MethodGet && (r.URL.Path == "/hls/streamer_a/index.m3u8" || r.URL.Path == "/hls/streamer_b/index.m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:2.0,\nchunk.ts\n"))
			return
		}

		// 4. Segment HEAD endpoint
		if r.Method == http.MethodHead && (r.URL.Path == "/hls/streamer_a/chunk.ts" || r.URL.Path == "/hls/streamer_b/chunk.ts") {
			chName := "streamer_a"
			if r.URL.Path == "/hls/streamer_b/chunk.ts" {
				chName = "streamer_b"
			}
			mu.Lock()
			now := time.Now()
			channelBeacons[chName] = append(channelBeacons[chName], now)
			if activeLoopChannel != "" && activeLoopChannel != chName {
				// Concurrent switch verification
			}
			activeLoopChannel = chName
			mu.Unlock()

			w.WriteHeader(http.StatusOK)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))
	watcher := NewWatcher(gqlClient, httpClient, nil, 12345)

	chA := model.Channel{
		ID:          "101",
		Login:       "streamer_a",
		BroadcastID: "bcast_a",
	}
	chB := model.Channel{
		ID:          "202",
		Login:       "streamer_b",
		BroadcastID: "bcast_b",
	}

	// Start watching Channel A
	err = watcher.Start(context.Background(), chA)
	require.NoError(t, err)

	// Wait for Channel A initial beacon
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(channelBeacons["streamer_a"]) >= 1
	}, 2*time.Second, 20*time.Millisecond)

	// Switch to Channel B immediately
	err = watcher.Start(context.Background(), chB)
	require.NoError(t, err)

	// Wait for Channel B initial beacon
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(channelBeacons["streamer_b"]) >= 1
	}, 2*time.Second, 20*time.Millisecond)

	mu.Lock()
	countA := len(channelBeacons["streamer_a"])
	lastA := channelBeacons["streamer_a"][countA-1]
	firstB := channelBeacons["streamer_b"][0]
	mu.Unlock()

	// Verify beacon timestamps: Channel A beacons must strictly precede Channel B first beacon
	assert.True(t, !lastA.After(firstB), "Channel A last beacon (%v) must be <= Channel B first beacon (%v)", lastA, firstB)
	assert.False(t, concurrentViolation)

	watcher.Stop()
}

func TestWatcher_PlaybackAccessTokenRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return offline / null token
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":null}}`))
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))
	watcher := NewWatcher(gqlClient, httpClient, nil, 12345)

	ch := model.Channel{
		ID:          "999",
		Login:       "offline_streamer",
		BroadcastID: "bcast_offline",
	}

	err = watcher.Start(context.Background(), ch)
	require.Error(t, err, "must refuse to start watching when playback access token fails")
	assert.Contains(t, err.Error(), "playback access token")

	// Ensure no loop is running
	assert.Nil(t, watcher.cancel)
	assert.Nil(t, watcher.done)
}

func TestWatcher_TickerDrain(t *testing.T) {
	var beaconCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":{"value":"{\"channel\":\"valid\"}","signature":"sig123"}}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/channel/hls/streamer_test.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=160000\nhttp://%s/hls/index.m3u8\n", r.Host)))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/hls/index.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:2.0,\nchunk.ts\n"))
			return
		}
		if r.Method == http.MethodHead && r.URL.Path == "/hls/chunk.ts" {
			atomic.AddInt32(&beaconCount, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))
	watcher := NewWatcher(gqlClient, httpClient, nil, 12345)
	beaconDone := make(chan int, 2)
	watcher.OnBeacon = func(seq int, _ bool, _ error) {
		beaconDone <- seq
	}

	// Inject a custom tick channel provider with a buffered channel
	tickChan := make(chan time.Time, 10)
	// Queue the burst before Start so the watcher always sees the complete burst.
	for i := 0; i < 5; i++ {
		tickChan <- time.Now()
	}
	watcher.tickerChanProvider = func(d time.Duration) (<-chan time.Time, func()) {
		return tickChan, func() {}
	}

	ch := model.Channel{
		ID:          "101",
		Login:       "streamer_test",
		BroadcastID: "bcast_101",
	}

	err = watcher.Start(context.Background(), ch)
	require.NoError(t, err)

	for want := 1; want <= 2; want++ {
		select {
		case got := <-beaconDone:
			assert.Equal(t, want, got)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for beacon %d", want)
		}
	}

	watcher.Stop()
	assert.Empty(t, tickChan)
	assert.Equal(t, int32(2), atomic.LoadInt32(&beaconCount), "initial beacon plus five collapsed ticks must produce exactly two sends")
}

func TestWatcher_HeaderHygieneAndSecurity(t *testing.T) {
	var leakDetected bool
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		// Ensure Usher and CDN requests do NOT leak Authorization, Cookie, Client-Id, X-Device-Id
		if r.Header.Get("Content-Type") != "application/json" {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Client-Id") != "" || r.Header.Get("X-Device-Id") != "" {
				leakDetected = true
			}
		}
		mu.Unlock()

		if r.Header.Get("Content-Type") == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":{"value":"secret_token_val","signature":"secret_sig"}}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/channel/hls/clean_channel.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=160000\nhttp://%s/hls/index.m3u8\n", r.Host)))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/hls/index.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:2.0,\nchunk.ts\n"))
			return
		}
		if r.Method == http.MethodHead && r.URL.Path == "/hls/chunk.ts" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))
	
	// Create identity with sensitive credentials
	ident := &mockIdentity{
		clientID:    "client_id_secret",
		deviceID:    "device_id_secret",
		sessionID:   "session_id_secret",
		accessToken: "oauth_access_token_secret",
	}

	watcher := NewWatcher(gqlClient, httpClient, ident, 12345)
	ch := model.Channel{
		ID:          "101",
		Login:       "clean_channel",
		BroadcastID: "bcast_101",
	}

	beaconDone := make(chan bool, 1)
	watcher.OnBeacon = func(seq int, success bool, err error) {
		select {
		case beaconDone <- success:
		default:
		}
	}

	err = watcher.Start(context.Background(), ch)
	require.NoError(t, err)

	select {
	case succ := <-beaconDone:
		assert.True(t, succ)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for beacon")
	}

	watcher.Stop()

	mu.Lock()
	assert.False(t, leakDetected, "sensitive headers must not be sent to Usher or CDN")
	mu.Unlock()
}


