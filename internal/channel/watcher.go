package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

const (
	defaultWatchInterval = 20 * time.Second
	maxPlaylistBodyBytes = 1024 * 1024 // 1MB playlist body limit
)

var (
	// ErrStreamOffline is returned when usher or media playlist reports channel offline or 404
	ErrStreamOffline = errors.New("stream is offline or playlist not found")
	// ErrTerminalWatcher is returned when watching fails with a fatal/unrecoverable error
	ErrTerminalWatcher = errors.New("watcher encountered a terminal error")
)

// Watcher manages a single-channel-at-a-time HLS HEAD beacon loop.
type Watcher struct {
	gqlClient  *gql.Client
	httpClient *resty.Client
	hlsClient  *http.Client
	identity   gql.Identity
	userID     int
	interval   time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	err    error

	// OnBeacon is an optional callback invoked after every beacon attempt.
	// Must be concurrency-safe.
	OnBeacon func(seq int, success bool, err error)

	// tickerChanProvider allows unit tests to inject custom tick channels.
	tickerChanProvider func(d time.Duration) (<-chan time.Time, func())
}

// NewWatcher constructs a new single-channel Watcher.
func NewWatcher(gqlClient *gql.Client, httpClient *resty.Client, identity gql.Identity, userID int) *Watcher {
	// Create an isolated http.Client without default headers, auth or cookies for HLS Usher/CDN
	hlsClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Strip sensitive headers on redirect
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Client-Id")
			req.Header.Del("X-Device-Id")
			req.Header.Del("Client-Session-Id")
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	if httpClient != nil && httpClient.GetClient() != nil && httpClient.GetClient().Transport != nil {
		hlsClient.Transport = httpClient.GetClient().Transport
	}

	return &Watcher{
		gqlClient:  gqlClient,
		httpClient: httpClient,
		hlsClient:  hlsClient,
		identity:   identity,
		userID:     userID,
		interval:   defaultWatchInterval,
	}
}

// Start begins watching the specified channel using HLS segment HEAD requests.
// Structural single-channel guarantee (WATCH-03): if a previous watch loop is running,
// Start cancels it and blocks until it fully terminates before initiating the new session.
func (w *Watcher) Start(ctx context.Context, ch model.Channel) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Stop prior watch loop if running and wait for full termination
	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
		w.err = nil
	}

	// Validate stream and obtain playback token before beaconing
	tokenVal, tokenSig, err := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login)
	if err != nil {
		return fmt.Errorf("cannot watch channel %s without playback access token: %w", ch.Login, err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done
	w.err = nil

	go w.loop(childCtx, ch, tokenVal, tokenSig, done)
	return nil
}

// Stop terminates any active watch loop and blocks until it has cleanly exited.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
		w.err = nil
	}
}

// Err returns the terminal error if the watcher loop exited with failure.
func (w *Watcher) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Done returns a channel that is closed when the current watch loop terminates.
func (w *Watcher) Done() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

func (w *Watcher) loop(ctx context.Context, ch model.Channel, initialToken, initialSig string, done chan struct{}) {
	defer close(done)

	tokenVal := initialToken
	tokenSig := initialSig
	mediaURL := ""

	var tickCh <-chan time.Time
	var stopTicker func()

	if w.tickerChanProvider != nil {
		tickCh, stopTicker = w.tickerChanProvider(w.interval)
	} else {
		ticker := time.NewTicker(w.interval)
		tickCh = ticker.C
		stopTicker = ticker.Stop
	}
	defer stopTicker()

	seq := 1

	// Initial cycle immediately
	mediaURL, err := w.watchCycle(ctx, ch, tokenVal, tokenSig, mediaURL)
	if err != nil {
		if errors.Is(err, ErrStreamOffline) || errors.Is(err, ErrPlaylistJSON) {
			w.setErr(err)
			w.notifyBeacon(0, false, err)
			return
		}
		// Refresh token once if unauthorized or expired
		if isAuthError(err) {
			newVal, newSig, rerr := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login)
			if rerr == nil {
				tokenVal = newVal
				tokenSig = newSig
				mediaURL, err = w.watchCycle(ctx, ch, tokenVal, tokenSig, "")
			}
		}
	}
	w.notifyBeacon(seq, err == nil, err)
	if err != nil && isFatal(err) {
		w.setErr(err)
		return
	}
	seq++

	for {
		select {
		case <-ctx.Done():
			return
		case <-tickCh:
			drainTicker(tickCh)
			if ctx.Err() != nil {
				return
			}
			var cycleErr error
			mediaURL, cycleErr = w.watchCycle(ctx, ch, tokenVal, tokenSig, mediaURL)
			if cycleErr != nil {
				if errors.Is(cycleErr, ErrStreamOffline) || errors.Is(cycleErr, ErrPlaylistJSON) {
					w.setErr(cycleErr)
					w.notifyBeacon(seq, false, cycleErr)
					return
				}
				if isAuthError(cycleErr) {
					newVal, newSig, rerr := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login)
					if rerr == nil {
						tokenVal = newVal
						tokenSig = newSig
						mediaURL, cycleErr = w.watchCycle(ctx, ch, tokenVal, tokenSig, "")
					}
				}
			}
			w.notifyBeacon(seq, cycleErr == nil, cycleErr)
			if cycleErr != nil && isFatal(cycleErr) {
				w.setErr(cycleErr)
				return
			}
			seq++
		}
	}
}

func (w *Watcher) setErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

func (w *Watcher) notifyBeacon(seq int, success bool, err error) {
	w.mu.Lock()
	cb := w.OnBeacon
	w.mu.Unlock()
	if cb != nil {
		cb(seq, success, err)
	}
}

// watchCycle executes one HLS check: master playlist -> media playlist -> HEAD segments
func (w *Watcher) watchCycle(ctx context.Context, ch model.Channel, tokenVal, tokenSig, cachedMediaURL string) (string, error) {
	mediaURL := cachedMediaURL
	var err error

	if mediaURL == "" {
		mediaURL, err = w.fetchMasterPlaylist(ctx, ch.Login, tokenVal, tokenSig)
		if err != nil {
			return "", err
		}
	}

	segments, err := w.fetchMediaSegments(ctx, mediaURL)
	if err != nil {
		// Reacquire master playlist once if media playlist returned 404/403/401
		if isRefreshableMediaError(err) {
			var mErr error
			mediaURL, mErr = w.fetchMasterPlaylist(ctx, ch.Login, tokenVal, tokenSig)
			if mErr != nil {
				return "", mErr
			}
			segments, err = w.fetchMediaSegments(ctx, mediaURL)
		}
		if err != nil {
			return "", err
		}
	}

	// HEAD all listed segments
	for _, segURL := range segments {
		if ctx.Err() != nil {
			return mediaURL, ctx.Err()
		}
		if err := w.headSegment(ctx, segURL); err != nil {
			return mediaURL, err
		}
	}

	return mediaURL, nil
}

func (w *Watcher) fetchMasterPlaylist(ctx context.Context, channelLogin, tokenVal, tokenSig string) (string, error) {
	masterBase := "https://usher.ttvnw.net/api/channel/hls/" + channelLogin + ".m3u8"
	if w.httpClient != nil && w.httpClient.HostURL != "" {
		masterBase = strings.TrimSuffix(w.httpClient.HostURL, "/") + "/api/channel/hls/" + channelLogin + ".m3u8"
	}

	q := url.Values{}
	q.Set("sig", tokenSig)
	q.Set("token", tokenVal)
	q.Set("allow_source", "true")
	q.Set("allow_audio_only", "true")
	q.Set("fast_bread", "true")
	q.Set("player_backend", "mediaplayer")

	masterURL := masterBase + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL, nil)
	if err != nil {
		return "", sanitizeErr(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/x-mpegURL, application/vnd.apple.mpegurl, application/json, text/plain")

	resp, err := w.hlsClient.Do(req)
	if err != nil {
		return "", sanitizeErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrStreamOffline
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("master playlist forbidden (%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("master playlist HTTP %d", resp.StatusCode)
	}

	lr := http.MaxBytesReader(nil, resp.Body, maxPlaylistBodyBytes)
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := lr.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	baseParsed := resp.Request.URL
	return ParseMasterPlaylist(baseParsed, sb.String())
}

func (w *Watcher) fetchMediaSegments(ctx context.Context, mediaURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Connection", "close")

	resp, err := w.hlsClient.Do(req)
	if err != nil {
		return nil, sanitizeErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrStreamOffline
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("media playlist forbidden (%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("media playlist HTTP %d", resp.StatusCode)
	}

	lr := http.MaxBytesReader(nil, resp.Body, maxPlaylistBodyBytes)
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := lr.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	baseParsed := resp.Request.URL
	return ParseMediaPlaylist(baseParsed, sb.String())
}

func (w *Watcher) headSegment(ctx context.Context, segURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, segURL, nil)
	if err != nil {
		return sanitizeErr(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Connection", "close")

	resp, err := w.hlsClient.Do(req)
	if err != nil {
		return sanitizeErr(err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("segment HEAD returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	// Strip raw URL and query parameters to prevent secret leakage in error messages
	msg := err.Error()
	if idx := strings.Index(msg, "token="); idx != -1 {
		msg = msg[:idx] + "token=[REDACTED]..."
	}
	if idx := strings.Index(msg, "sig="); idx != -1 {
		msg = msg[:idx] + "sig=[REDACTED]..."
	}
	return errors.New(msg)
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(msg, "forbidden")
}

func isRefreshableMediaError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "403") || strings.Contains(msg, "401")
}

func isFatal(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrStreamOffline) || errors.Is(err, ErrPlaylistJSON) || errors.Is(err, ErrTerminalWatcher)
}

func drainTicker(c <-chan time.Time) {
	for len(c) > 0 {
		select {
		case <-c:
		default:
			return
		}
	}
}
