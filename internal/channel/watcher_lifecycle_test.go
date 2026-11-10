package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func lifecycleWatcher(t *testing.T, handler http.HandlerFunc) *Watcher {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)
	client := resty.New().SetBaseURL(server.URL)
	watcher := NewWatcher(gql.NewClient(reg, nil, nil, client), client, nil, 1)
	t.Cleanup(watcher.Stop)
	return watcher
}

func TestWatcher_RepeatedForbiddenTerminates(t *testing.T) {
	var tokens atomic.Int32
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			tokens.Add(1)
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		} else {
			rw.WriteHeader(http.StatusForbidden)
		}
	})
	w.interval = 10 * time.Millisecond
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("repeated HTTP403 must terminate instead of silently remaining watching")
	}
	var se *HTTPStatusError
	require.ErrorAs(t, w.Err(), &se)
	require.Equal(t, http.StatusForbidden, se.Code)
	require.Equal(t, int32(1+maxConsecutiveFailures), tokens.Load(), "one playback refresh per failed cycle")
}

func TestWatcher_TransientFailureRecovers(t *testing.T) {
	var heads atomic.Int32
	results := make(chan bool, 16)
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/media.m3u8\n")
		case r.URL.Path == "/media.m3u8":
			io.WriteString(rw, "#EXTM3U\n#EXTINF:2,\n/segment.ts\n")
		case r.Method == http.MethodHead:
			if heads.Add(1) <= maxConsecutiveFailures-1 {
				rw.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}
	})
	w.interval = 10 * time.Millisecond
	w.OnBeacon = func(_ int, success bool, _ error) { results <- success }
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	for i := 1; i < maxConsecutiveFailures; i++ {
		select {
		case ok := <-results:
			require.False(t, ok)
		case <-time.After(time.Second):
			t.Fatal("missing failed cycle")
		}
	}
	select {
	case ok := <-results:
		require.True(t, ok, "watcher must keep going after transient failures")
	case <-w.Done():
		t.Fatalf("watcher terminated on transient failures: %v", w.Err())
	case <-time.After(time.Second):
		t.Fatal("no recovery cycle")
	}
	require.NoError(t, w.Err())
}

func TestWatcher_OfflineTerminatesImmediately(t *testing.T) {
	var tokens atomic.Int32
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			tokens.Add(1)
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	})
	w.interval = 10 * time.Millisecond
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("offline stream must end the watcher")
	}
	require.ErrorIs(t, w.Err(), ErrStreamOffline)
	require.Equal(t, int32(2), tokens.Load(), "offline is terminal after the single refresh attempt")
}

func TestWatcher_StopLeavesNoError(t *testing.T) {
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/media.m3u8\n")
		case r.URL.Path == "/media.m3u8":
			io.WriteString(rw, "#EXTM3U\n#EXTINF:2,\n/segment.ts\n")
		case r.Method == http.MethodHead:
			rw.WriteHeader(http.StatusOK)
		}
	})
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	w.Stop()
	require.NoError(t, w.Err(), "cancellation is not a watcher failure")
}

func TestWatcher_RefreshesPlaybackAfterMediaExpiry(t *testing.T) {
	var tokens, masters atomic.Int32
	completed := make(chan bool, 1)
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			tokens.Add(1)
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			if masters.Add(1) == 1 {
				io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/expired.m3u8\n")
			} else {
				io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/current.m3u8\n")
			}
		case r.URL.Path == "/expired.m3u8":
			rw.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/current.m3u8":
			io.WriteString(rw, "#EXTM3U\n#EXTINF:2,\n/segment.ts\n")
		case r.Method == http.MethodHead:
			rw.WriteHeader(http.StatusOK)
		}
	})
	w.OnBeacon = func(_ int, success bool, _ error) { completed <- success }
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	select {
	case success := <-completed:
		require.True(t, success)
	case <-time.After(time.Second):
		t.Fatal("refresh failed")
	}
	w.Stop()
	require.Equal(t, int32(2), tokens.Load())
	require.Equal(t, int32(2), masters.Load())
}

func TestWatcher_StopCancelsInflightSegment(t *testing.T) {
	entered := make(chan struct{})
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/media.m3u8\n")
		case r.URL.Path == "/media.m3u8":
			io.WriteString(rw, "#EXTM3U\n#EXTINF:2,\n/segment.ts\n")
		case r.Method == http.MethodHead:
			close(entered)
			<-r.Context().Done()
		}
	})
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no HEAD")
	}
	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop deadlocked waiting for canceled HEAD")
	}
}

func TestWatcher_PlaylistLimitAndSensitiveDefaults(t *testing.T) {
	var leaked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			leaked.Store(true)
		}
		io.WriteString(rw, strings.Repeat("a", maxPlaylistBodyBytes+1))
	}))
	defer server.Close()
	client := resty.New().SetHeader("Authorization", "secret").SetHeader("Cookie", "secret")
	w := NewWatcher(nil, client, nil, 0)
	_, _, err := w.fetchPlaylistBody(context.Background(), server.URL)
	require.Error(t, err)
	require.False(t, leaked.Load())
}

func TestWatcher_SanitizeContextAndSignedURL(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://cdn.invalid/private-signature?signature=secret", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	_, err := http.DefaultClient.Do(req.WithContext(ctx))
	err = sanitizeErr(err)
	require.ErrorIs(t, err, context.Canceled)
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "private-signature")
	require.True(t, errors.Is(err, context.Canceled))
}

func TestWatcher_TokenRefreshFailureIsTolerated(t *testing.T) {
	var tokens, heads atomic.Int32
	results := make(chan bool, 16)
	w := lifecycleWatcher(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			// Start's token succeeds; the first refresh fails; later refreshes succeed.
			if tokens.Add(1) == 2 {
				rw.Header().Set("Content-Type", "application/json")
				io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":null}}`)
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			io.WriteString(rw, `{"data":{"streamPlaybackAccessToken":{"value":"fake","signature":"fake"}}}`)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			io.WriteString(rw, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/media.m3u8\n")
		case r.URL.Path == "/media.m3u8":
			io.WriteString(rw, "#EXTM3U\n#EXTINF:2,\n/segment.ts\n")
		case r.Method == http.MethodHead:
			if heads.Add(1) == 1 {
				rw.WriteHeader(http.StatusForbidden)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}
	})
	w.interval = 10 * time.Millisecond
	w.OnBeacon = func(_ int, success bool, _ error) { results <- success }
	require.NoError(t, w.Start(context.Background(), model.Channel{Login: "test"}))
	select {
	case ok := <-results:
		require.False(t, ok, "first cycle fails: 403 then refresh error")
	case <-time.After(2 * time.Second):
		t.Fatal("missing failed cycle")
	}
	select {
	case ok := <-results:
		require.True(t, ok, "single refresh failure must not end the watcher")
	case <-w.Done():
		t.Fatalf("watcher terminated on one refresh failure: %v", w.Err())
	case <-time.After(2 * time.Second):
		t.Fatal("no recovery cycle")
	}
}
