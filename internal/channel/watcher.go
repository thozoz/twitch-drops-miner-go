package channel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

const defaultWatchInterval = 59 * time.Second

// Watcher manages a single-channel-at-a-time Spade beacon loop.
type Watcher struct {
	gqlClient  *gql.Client
	httpClient *resty.Client
	identity   gql.Identity
	userID     int
	interval   time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// OnBeacon is an optional callback invoked after every beacon attempt.
	OnBeacon func(seq int, success bool, err error)

	// tickerChanProvider allows unit tests to inject custom tick channels.
	tickerChanProvider func(d time.Duration) (<-chan time.Time, func())
}

// NewWatcher constructs a new single-channel Watcher.
func NewWatcher(gqlClient *gql.Client, httpClient *resty.Client, identity gql.Identity, userID int) *Watcher {
	return &Watcher{
		gqlClient:  gqlClient,
		httpClient: httpClient,
		identity:   identity,
		userID:     userID,
		interval:   defaultWatchInterval,
	}
}

// Start begins watching the specified channel.
// Structural single-channel guarantee (WATCH-03): if a previous watch loop is running,
// Start cancels it and blocks until it fully terminates before initiating the new session.
// Precondition (WATCH-02): a PlaybackAccessToken is fetched before starting the beacon loop.
func (w *Watcher) Start(ctx context.Context, ch model.Channel) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Stop prior watch loop if running and wait for full termination
	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
		w.done = nil
	}

	// Validate stream and obtain playback token before beaconing
	if _, _, err := FetchPlaybackAccessToken(ctx, w.gqlClient, ch.Login); err != nil {
		return fmt.Errorf("cannot watch channel %s without playback access token: %w", ch.Login, err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.cancel = cancel
	w.done = done

	go w.loop(childCtx, ch, done)
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
	}
}

func (w *Watcher) loop(ctx context.Context, ch model.Channel, done chan struct{}) {
	defer close(done)

	spadeURL, err := DiscoverSpadeURL(ctx, w.httpClient, w.identity, ch.Login)
	if err != nil {
		if w.OnBeacon != nil {
			w.OnBeacon(0, false, err)
		}
		return
	}

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

	// Send initial beacon immediately
	success, err := w.sendBeacon(ctx, spadeURL, ch)
	if w.OnBeacon != nil {
		w.OnBeacon(seq, success, err)
	}
	seq++

	for {
		select {
		case <-ctx.Done():
			return
		case <-tickCh:
			// Drain queued ticks to mitigate suspend/resume bunching (Pitfall 9)
			drainTicker(tickCh)
			if ctx.Err() != nil {
				return
			}
			success, err := w.sendBeacon(ctx, spadeURL, ch)
			if w.OnBeacon != nil {
				w.OnBeacon(seq, success, err)
			}
			seq++
		}
	}
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

func (w *Watcher) sendBeacon(ctx context.Context, spadeURL string, ch model.Channel) (bool, error) {
	if w.httpClient == nil {
		return false, errors.New("httpClient cannot be nil")
	}

	var gameName, gameID string
	if ch.Game != nil {
		gameName = ch.Game.Name
		gameID = ch.Game.ID
	}

	payload := BuildWatchPayload(ch.BroadcastID, ch.ID, ch.Login, gameName, gameID, w.userID, time.Now())
	encoded, err := EncodeSpadeData(payload)
	if err != nil {
		return false, fmt.Errorf("failed to encode spade payload: %w", err)
	}

	req := w.httpClient.R().SetContext(ctx)
	applyTwitchHeaders(req, w.identity)
	req.SetHeader("Content-Type", "application/x-www-form-urlencoded")
	req.SetFormData(map[string]string{"data": encoded})

	resp, err := req.Post(spadeURL)
	if err != nil {
		// Matches reference channel.py:493-495 swallowing RequestException and returning false
		return false, nil
	}

	return resp.StatusCode() == 204, nil
}
