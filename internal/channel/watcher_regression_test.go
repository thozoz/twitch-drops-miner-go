package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

// TestWatcher_HLS_Regression verifies that the Watcher requests master playlist via GET,
// media playlist via GET, and ALL listed segments via HEAD, and NEVER sends Spade POST or segment GET.
// This test MUST FAIL against the existing Spade-only codebase before implementation.
func TestWatcher_HLS_Regression(t *testing.T) {
	var (
		spadePostCount   int32
		segmentGetCount  int32
		masterGetCount   int32
		mediaGetCount    int32
		segmentHeadCount int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. GQL PlaybackAccessToken
		if r.Header.Get("Content-Type") == "application/json" {
			body, _ := io.ReadAll(r.Body)
			var op struct {
				OperationName string `json:"operationName"`
			}
			_ = json.Unmarshal(body, &op)
			if op.OperationName == "PlaybackAccessToken" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":{"value":"{\"channel\":\"test_streamer\"}","signature":"sig123"}}}`))
				return
			}
		}

		// 2. Usher Master Playlist
		if r.Method == http.MethodGet && r.URL.Path == "/api/channel/hls/test_streamer.m3u8" {
			atomic.AddInt32(&masterGetCount, 1)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			// Return master playlist with variant pointing to media playlist
			mediaURL := fmt.Sprintf("http://%s/hls/test_streamer/index-dvr.m3u8", r.Host)
			_, _ = w.Write([]byte(fmt.Sprintf("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=160000,RESOLUTION=160x90\n%s\n", mediaURL)))
			return
		}

		// 3. Media Playlist
		if r.Method == http.MethodGet && r.URL.Path == "/hls/test_streamer/index-dvr.m3u8" {
			atomic.AddInt32(&mediaGetCount, 1)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			// List 2 segments
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:2.0,\nchunk-01.ts\n#EXTINF:2.0,\nchunk-02.ts\n"))
			return
		}

		// 4. Segment HEAD
		if r.Method == http.MethodHead && (r.URL.Path == "/hls/test_streamer/chunk-01.ts" || r.URL.Path == "/hls/test_streamer/chunk-02.ts") {
			atomic.AddInt32(&segmentHeadCount, 1)
			w.WriteHeader(http.StatusOK)
			return
		}

		// 5. Segment GET (FORBIDDEN)
		if r.Method == http.MethodGet && (r.URL.Path == "/hls/test_streamer/chunk-01.ts" || r.URL.Path == "/hls/test_streamer/chunk-02.ts") {
			atomic.AddInt32(&segmentGetCount, 1)
			w.WriteHeader(http.StatusOK)
			return
		}

		// 6. Spade endpoint / Channel page (existing Spade flow)
		if r.URL.Path == "/spade_beacon" && r.Method == http.MethodPost {
			atomic.AddInt32(&spadePostCount, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/test_streamer" {
			// Old spade discovery
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><head><script>var spade={"spade_url":"http://` + r.Host + `/spade_beacon"};</script></head></html>`))
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

	ch := model.Channel{
		ID:          "101",
		Login:       "test_streamer",
		BroadcastID: "bcast_101",
	}

	beaconCalled := make(chan bool, 1)
	watcher.OnBeacon = func(seq int, success bool, err error) {
		select {
		case beaconCalled <- success:
		default:
		}
	}

	err = watcher.Start(context.Background(), ch)
	require.NoError(t, err)

	select {
	case succ := <-beaconCalled:
		assert.True(t, succ, "beacon cycle should succeed")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for beacon callback")
	}

	watcher.Stop()

	// Assertions verifying behavioral transition:
	assert.Equal(t, int32(0), atomic.LoadInt32(&spadePostCount), "must NOT send any Spade POST")
	assert.Equal(t, int32(0), atomic.LoadInt32(&segmentGetCount), "must NOT send any segment GET")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&masterGetCount), int32(1), "must GET master playlist")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&mediaGetCount), int32(1), "must GET media playlist")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&segmentHeadCount), int32(2), "must HEAD all listed segments (both chunks)")
}
