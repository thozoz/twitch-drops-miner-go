package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	maxPlaylistBodyBytes = 1024 * 1024 // 1MB
)

var (
	ErrStreamOffline   = errors.New("stream is offline or playlist not found")
	ErrTerminalWatcher = errors.New("watcher encountered a terminal error")
)

// Watcher manages a single-channel HLS HEAD beacon loop.
type Watcher struct {
	gqlClient  *gql.Client
	httpClient *resty.Client
	hlsClient  *http.Client
	identity   gql.Identity
	userID     int
	interval   time.Duration

	// lifecycleMu protects start/stop/cancellation
	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}

	// stateMu protects err and OnBeacon callback
	stateMu  sync.RWMutex
	err      error
	OnBeacon func(seq int, success bool, err error)

	tickerChanProvider func(d time.Duration) (<-chan time.Time, func())
}

func NewWatcher(gqlClient *gql.Client, httpClient *resty.Client, identity gql.Identity, userID int) *Watcher {
	hlsClient := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
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

func (w *Watcher) SetOnBeacon(cb func(seq int, success bool, err error)) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.OnBeacon = cb
}

func (w *Watcher) getOnBeacon() func(seq int, success bool, err error) {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return w.OnBeacon
}

func (w *Watcher) Start(ctx context.Context, ch model.Channel) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
	}

	w.stateMu.Lock()
	w.err = nil
	w.stateMu.Unlock()

	tokenVal, tokenSig, err := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login)
	if err != nil {
		return fmt.Errorf("cannot watch channel %s without playback access token: %w", ch.Login, err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done

	go w.loop(childCtx, ch, tokenVal, tokenSig, done)
	return nil
}

func (w *Watcher) Stop() {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()

	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
	}
}

func (w *Watcher) Err() error {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return w.err
}

func (w *Watcher) Done() <-chan struct{} {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	return w.done
}

func (w *Watcher) loop(ctx context.Context, ch model.Channel, tokenVal, tokenSig string, done chan struct{}) {
	defer close(done)

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
	for {
		if ctx.Err() != nil {
			return
		}

		var cycleErr error
		mediaURL, cycleErr = w.watchCycle(ctx, ch, tokenVal, tokenSig, mediaURL)
		if ctx.Err() != nil {
			return
		}

		if cycleErr != nil {
			if isRefreshableMediaError(cycleErr) {
				newVal, newSig, rerr := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login)
				if rerr == nil {
					tokenVal = newVal
					tokenSig = newSig
					mediaURL, cycleErr = w.watchCycle(ctx, ch, tokenVal, tokenSig, "")
				} else {
					cycleErr = fmt.Errorf("%w: playback token refresh failed: %v", ErrTerminalWatcher, rerr)
				}
			}
		}

		if ctx.Err() != nil {
			return
		}

		success := cycleErr == nil
		w.notifyBeacon(seq, success, cycleErr)

		if cycleErr != nil {
			w.setErr(cycleErr)
			return
		}

		seq++

		select {
		case <-ctx.Done():
			return
		case <-tickCh:
			drainTicker(tickCh)
		}
	}
}

func (w *Watcher) setErr(err error) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.err = err
}

func (w *Watcher) notifyBeacon(seq int, success bool, err error) {
	cb := w.getOnBeacon()
	if cb != nil {
		cb(seq, success, err)
	}
}

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
		return "", err
	}

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

	q := url.Values{
		"sig":   {tokenSig},
		"token": {tokenVal},
	}

	body, finalURL, err := w.fetchPlaylistBody(ctx, masterBase+"?"+q.Encode())
	if err != nil {
		return "", err
	}
	return ParseMasterPlaylist(finalURL, body)
}

func (w *Watcher) fetchMediaSegments(ctx context.Context, mediaURL string) ([]string, error) {
	body, finalURL, err := w.fetchPlaylistBody(ctx, mediaURL)
	if err != nil {
		return nil, err
	}
	return ParseMediaPlaylist(finalURL, body)
}

func (w *Watcher) fetchPlaylistBody(ctx context.Context, targetURL string) (string, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", nil, sanitizeErr(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Connection", "close")

	resp, err := w.hlsClient.Do(req)
	if err != nil {
		return "", nil, sanitizeErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil, ErrStreamOffline
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", nil, fmt.Errorf("playlist forbidden (%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("playlist HTTP %d", resp.StatusCode)
	}

	lr := io.LimitReader(resp.Body, maxPlaylistBodyBytes+1)
	bodyBytes, err := io.ReadAll(lr)
	if err != nil {
		return "", nil, fmt.Errorf("reading playlist failed: %w", sanitizeErr(err))
	}
	if int64(len(bodyBytes)) > maxPlaylistBodyBytes {
		return "", nil, fmt.Errorf("%w: playlist exceeded 1MB limit", ErrTerminalWatcher)
	}

	return string(bodyBytes), resp.Request.URL, nil
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("segment HEAD returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &url.Error{Op: urlErr.Op, URL: "[REDACTED]", Err: sanitizeErr(urlErr.Err)}
	}
	return err
}

func isRefreshableMediaError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrStreamOffline) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "403") || strings.Contains(msg, "401")
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
