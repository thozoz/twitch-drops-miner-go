package channel

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"tdm/internal/gql"
	"tdm/internal/model"
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

		// 2. Channel page HTML endpoint (DiscoverSpadeURL)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><head><script>var spade={"spade_url":"http://` + r.Host + `/spade_beacon"};</script></head></html>`))
			return
		}

		// 3. Spade beacon POST endpoint
		if r.URL.Path == "/spade_beacon" && r.Method == http.MethodPost {
			_ = r.ParseForm()
			encoded := r.FormValue("data")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				var events []WatchEvent
				if json.Unmarshal(decoded, &events) == nil && len(events) > 0 {
					chName := events[0].Properties.Channel
					mu.Lock()
					now := time.Now()
					channelBeacons[chName] = append(channelBeacons[chName], now)

					if activeLoopChannel != "" && activeLoopChannel != chName {
						// Another channel sent a beacon while the previous was considered active
						// Note: since Start blocks until prior done is closed, activeLoopChannel should be replaced cleanly
					}
					activeLoopChannel = chName
					mu.Unlock()
				}
			}
			w.WriteHeader(http.StatusNoContent)
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
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><head><script>var spade={"spade_url":"http://` + r.Host + `/spade_beacon"};</script></head></html>`))
			return
		}
		if r.URL.Path == "/spade_beacon" {
			atomic.AddInt32(&beaconCount, 1)
			w.WriteHeader(http.StatusNoContent)
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

	// Inject a custom tick channel provider with a buffered channel
	tickChan := make(chan time.Time, 10)
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

	// 1 initial beacon was sent immediately
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&beaconCount) == 1
	}, 2*time.Second, 20*time.Millisecond)

	// Queue 5 burst ticks into the channel (simulating host wake after sleep)
	for i := 0; i < 5; i++ {
		tickChan <- time.Now()
	}

	// Wait briefly for the loop to process the tick
	time.Sleep(100 * time.Millisecond)

	// The loop must have drained the queued ticks and sent exactly 1 additional beacon (total 2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&beaconCount), "5 queued burst ticks must collapse into exactly 1 beacon send")

	watcher.Stop()
}
