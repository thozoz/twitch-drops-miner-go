package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"github.com/thozoz/twitch-drops-miner-go/internal/channel"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/pubsub"
	"github.com/thozoz/twitch-drops-miner-go/internal/state"
)

var (
	// ErrChannelOffline indicates the watched channel went offline and stayed offline beyond the grace window.
	ErrChannelOffline = errors.New("channel went offline")

	// ErrNoEarnableDrop indicates that no earnable drop is available for the selected campaign and channel.
	ErrNoEarnableDrop = errors.New("no earnable drop on selected campaign/channel")

	// ErrNoProgress indicates the channel stayed online but the active drop stopped accruing
	// minutes for longer than noProgressTimeout. This is distinct from ErrNoEarnableDrop, which
	// is checked once at session start before watching begins: ErrNoProgress fires mid-session
	// when an earnable drop exists but the stream stops advancing it (e.g. a stale dropID or a
	// server that no longer reports progress for the tracked drop).
	ErrNoProgress = errors.New("no drop progress within timeout window")

	errSessionComplete = errors.New("session complete")
)

type dropProgressPayload struct {
	DropID             string `json:"drop_id"`
	CurrentProgressMin int    `json:"current_progress_min"`
	DropInstanceID     string `json:"drop_instance_id"`
}

type dropClaimPayload struct {
	DropID         string `json:"drop_id"`
	DropInstanceID string `json:"drop_instance_id"`
}

// SessionOption configures a WatchSession.
type SessionOption func(*WatchSession)

// WithReconcileInterval overrides the interval between GQL progress reconciliation queries.
func WithReconcileInterval(d time.Duration) SessionOption {
	return func(s *WatchSession) {
		s.reconcileInterval = d
	}
}

// WithOfflineGrace overrides the duration to wait before treating a stream-down event as fatal.
func WithOfflineGrace(d time.Duration) SessionOption {
	return func(s *WatchSession) {
		s.offlineGrace = d
	}
}

// WithNoProgressTimeout overrides the duration of zero drop-minute movement tolerated before the
// session gives up on the current channel and returns ErrNoProgress. 0 disables the watchdog.
func WithNoProgressTimeout(d time.Duration) SessionOption {
	return func(s *WatchSession) {
		s.noProgressTimeout = d
	}
}

// WithConfirmDelay overrides the sleep before post-claim GQL verification polling.
func WithConfirmDelay(d time.Duration) SessionOption {
	return func(s *WatchSession) {
		s.confirmDelay = d
	}
}

// WithConfirmPollInterval overrides the interval between post-claim GQL verification polls.
func WithConfirmPollInterval(d time.Duration) SessionOption {
	return func(s *WatchSession) {
		s.confirmPollInterval = d
	}
}

// WithConfirmRetries overrides the maximum number of post-claim GQL verification poll attempts.
func WithConfirmRetries(n int) SessionOption {
	return func(s *WatchSession) {
		s.confirmRetries = n
	}
}

// WithStatePath overrides the path to state.json for runtime state persistence.
func WithStatePath(path string) SessionOption {
	return func(s *WatchSession) {
		s.statePath = path
	}
}

// ProgressCallback is called whenever drop progress minutes or active drop changes.
type ProgressCallback func(drop *inventory.TimedDrop)

// WithOnProgress configures a callback invoked on drop progress changes.
func WithOnProgress(cb ProgressCallback) SessionOption {
	return func(s *WatchSession) {
		s.onProgress = cb
	}
}

// WatchSession orchestrates channel watching, PubSub event handling, and GQL drop reconciliation/claiming.
type WatchSession struct {
	gqlClient           *gql.Client
	watcher             *channel.Watcher
	pubsubClient        *pubsub.Client
	userID              int
	logger              *slog.Logger
	statePath           string
	reconcileInterval   time.Duration
	offlineGrace        time.Duration
	noProgressTimeout   time.Duration
	confirmDelay        time.Duration
	confirmPollInterval time.Duration
	confirmRetries      int
	onProgress          ProgressCallback

	mu             sync.Mutex
	claimMu        sync.Mutex
	activeDrop     *inventory.TimedDrop
	offlineSince   time.Time
	lastProgressAt time.Time
}

// NewWatchSession creates a new WatchSession instance.
func NewWatchSession(
	gqlClient *gql.Client,
	watcher *channel.Watcher,
	pubsubClient *pubsub.Client,
	userID int,
	logger *slog.Logger,
	opts ...SessionOption,
) *WatchSession {
	if logger == nil {
		logger = slog.Default()
	}
	s := &WatchSession{
		gqlClient:           gqlClient,
		watcher:             watcher,
		pubsubClient:        pubsubClient,
		userID:              userID,
		logger:              logger,
		reconcileInterval:   59 * time.Second,
		offlineGrace:        inventory.NewOfflineGrace().Window,
		noProgressTimeout:   10 * time.Minute,
		confirmDelay:        4 * time.Second,
		confirmPollInterval: 2 * time.Second,
		confirmRetries:      8,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ActiveDrop returns a copy of the currently active drop, or nil if none.
func (s *WatchSession) ActiveDrop() *inventory.TimedDrop {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeDrop == nil {
		return nil
	}
	d := *s.activeDrop
	return &d
}

// Run executes the watch session until all earnable drops in the campaign are claimed or the channel goes offline.
func (s *WatchSession) Run(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) error {
	if err := s.watcher.Start(ctx, ch); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}
	defer s.watcher.Stop()

	if s.statePath != "" {
		priorState, found, err := state.LoadRuntimeState(s.statePath)
		if err != nil {
			s.logger.Warn("failed to load prior runtime state", "error", err)
		} else if found {
			s.logger.Info("found prior runtime state",
				slog.String("active_campaign_id", priorState.ActiveCampaignID),
				slog.String("active_drop_id", priorState.ActiveDropID),
				slog.Int("current_minutes", priorState.CurrentMinutes),
			)
		} else {
			s.logger.Info("no prior runtime state found")
		}
	}

	var ok bool
	s.mu.Lock()
	s.activeDrop, ok = campaign.FirstEarnableDrop(time.Now(), &ch)
	s.lastProgressAt = time.Now()
	activeDropCopy := s.activeDrop
	s.mu.Unlock()
	if !ok {
		s.watcher.Stop()
		return ErrNoEarnableDrop
	}

	if s.onProgress != nil && activeDropCopy != nil {
		s.onProgress(activeDropCopy)
	}

	if s.statePath != "" && activeDropCopy != nil {
		if err := state.SaveRuntimeState(s.statePath, state.RuntimeState{
			ActiveCampaignID:     campaign.ID,
			ActiveDropID:         activeDropCopy.ID,
			WatchingChannelID:    ch.ID,
			WatchingChannelLogin: ch.Login,
			CurrentMinutes:       activeDropCopy.CurrentMinutes,
			LastSyncAt:           time.Now().UTC(),
		}); err != nil {
			s.logger.Warn("failed to save runtime state", "error", err)
		}
	}

	if s.pubsubClient != nil {
		s.pubsubClient.AddTopics(
			pubsub.UserDropsTopic(s.userID),
			pubsub.UserNotificationsTopic(s.userID),
			pubsub.ChannelStreamStateTopic(ch.ID),
			pubsub.ChannelStreamUpdateTopic(ch.ID),
		)
	}

	g, gctx := errgroup.WithContext(ctx)

	if s.pubsubClient != nil {
		g.Go(func() error {
			err := s.pubsubClient.Run(gctx)
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		})
	}

	// GQL Reconciliation ticker loop
	g.Go(func() error {
		ticker := time.NewTicker(s.reconcileInterval)
		defer ticker.Stop()

		for {
			select {
			case <-gctx.Done():
				return nil
			case <-ticker.C:
				drainTicker(ticker.C)
				if gctx.Err() != nil {
					return nil
				}

				s.mu.Lock()
				offlineSince := s.offlineSince
				s.mu.Unlock()
				if !offlineSince.IsZero() && time.Since(offlineSince) >= s.offlineGrace {
					return ErrChannelOffline
				}

				s.mu.Lock()
				lastProgressAt := s.lastProgressAt
				s.mu.Unlock()
				if s.noProgressTimeout > 0 && !lastProgressAt.IsZero() && time.Since(lastProgressAt) >= s.noProgressTimeout {
					s.logger.Warn("no drop progress within timeout window, abandoning channel",
						slog.Duration("timeout", s.noProgressTimeout),
						slog.Duration("elapsed", time.Since(lastProgressAt)),
					)
					return ErrNoProgress
				}

				dropID, currentMinutes, ok, err := inventory.FetchCurrentDropProgress(gctx, s.gqlClient, ch.ID)
				if err != nil {
					s.logger.Warn("failed to fetch current drop progress via GQL", "error", err)
				} else if ok {
					s.mu.Lock()
					var shouldClaim bool
					var changed bool
					if s.activeDrop != nil && s.activeDrop.ID == dropID {
						changed = inventory.ReconcileMinutes(s.activeDrop, currentMinutes, s.logger)
						if changed {
							s.lastProgressAt = time.Now()
						}
						if s.activeDrop.CurrentMinutes >= s.activeDrop.RequiredMinutes {
							shouldClaim = true
						}
					}
					activeDropCopy := s.activeDrop
					s.mu.Unlock()

					if changed && activeDropCopy != nil {
						if s.onProgress != nil {
							s.onProgress(activeDropCopy)
						}
						if s.statePath != "" {
							if err := state.SaveRuntimeState(s.statePath, state.RuntimeState{
								ActiveCampaignID:     campaign.ID,
								ActiveDropID:         activeDropCopy.ID,
								WatchingChannelID:    ch.ID,
								WatchingChannelLogin: ch.Login,
								CurrentMinutes:       activeDropCopy.CurrentMinutes,
								LastSyncAt:           time.Now().UTC(),
							}); err != nil {
								s.logger.Warn("failed to save runtime state", "error", err)
							}
						}
					}

					if shouldClaim {
						if err := s.claimAndAdvance(gctx, &campaign, ch); err != nil {
							return err
						}
					}
				}
			}
		}
	})

	// PubSub event reading loop
	if s.pubsubClient != nil {
		g.Go(func() error {
			for {
				select {
				case <-gctx.Done():
					return nil
				case ev, ok := <-s.pubsubClient.Events():
					if !ok {
						return nil
					}
					switch ev.Type {
					case "stream-down":
						s.mu.Lock()
						if s.offlineSince.IsZero() {
							s.offlineSince = time.Now()
						}
						s.mu.Unlock()

					case "stream-up":
						s.mu.Lock()
						s.offlineSince = time.Time{}
						s.mu.Unlock()

					case "drop-progress":
						var payload dropProgressPayload
						if err := json.Unmarshal(ev.Payload, &payload); err != nil {
							s.logger.Warn("failed to unmarshal drop-progress payload", "error", err)
							continue
						}
						s.mu.Lock()
						var shouldClaim bool
						var changed bool
						if s.activeDrop != nil && s.activeDrop.ID == payload.DropID {
							if payload.DropInstanceID != "" {
								s.activeDrop.ClaimID = payload.DropInstanceID
							}
							changed = inventory.ReconcileMinutes(s.activeDrop, payload.CurrentProgressMin, s.logger)
							if changed {
								s.lastProgressAt = time.Now()
							}
							if s.activeDrop.CurrentMinutes >= s.activeDrop.RequiredMinutes {
								shouldClaim = true
							}
						}
						activeDropCopy := s.activeDrop
						s.mu.Unlock()

						if changed && activeDropCopy != nil {
							if s.onProgress != nil {
								s.onProgress(activeDropCopy)
							}
							if s.statePath != "" {
								if err := state.SaveRuntimeState(s.statePath, state.RuntimeState{
									ActiveCampaignID:     campaign.ID,
									ActiveDropID:         activeDropCopy.ID,
									WatchingChannelID:    ch.ID,
									WatchingChannelLogin: ch.Login,
									CurrentMinutes:       activeDropCopy.CurrentMinutes,
									LastSyncAt:           time.Now().UTC(),
								}); err != nil {
									s.logger.Warn("failed to save runtime state", "error", err)
								}
							}
						}

						if shouldClaim {
							if err := s.claimAndAdvance(gctx, &campaign, ch); err != nil {
								return err
							}
						}

					case "drop-claim":
						var payload dropClaimPayload
						if err := json.Unmarshal(ev.Payload, &payload); err != nil {
							s.logger.Warn("failed to unmarshal drop-claim payload", "error", err)
							continue
						}
						s.mu.Lock()
						var shouldClaim bool
						if s.activeDrop != nil && (payload.DropID == "" || s.activeDrop.ID == payload.DropID) {
							if payload.DropInstanceID != "" {
								s.activeDrop.ClaimID = payload.DropInstanceID
							}
							shouldClaim = true
						}
						s.mu.Unlock()

						if shouldClaim {
							if err := s.claimAndAdvance(gctx, &campaign, ch); err != nil {
								return err
							}
						}
					}
				}
			}
		})
	}

	// Offline watchdog loop
	g.Go(func() error {
		checkInterval := s.offlineGrace / 4
		if checkInterval < 100*time.Millisecond {
			checkInterval = 100 * time.Millisecond
		} else if checkInterval > 5*time.Second {
			checkInterval = 5 * time.Second
		}
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-gctx.Done():
				return nil
			case <-ticker.C:
				s.mu.Lock()
				offlineSince := s.offlineSince
				s.mu.Unlock()

				if !offlineSince.IsZero() && time.Since(offlineSince) >= s.offlineGrace {
					return ErrChannelOffline
				}
			}
		}
	})

	err := g.Wait()
	if err != nil {
		if errors.Is(err, errSessionComplete) {
			return nil
		}
		return err
	}

	return nil
}

func (s *WatchSession) claimAndAdvance(ctx context.Context, campaign *inventory.DropsCampaign, ch model.Channel) error {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()

	s.mu.Lock()
	activeDrop := s.activeDrop
	if activeDrop == nil || activeDrop.IsClaimed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	claimed, err := inventory.ClaimDrop(ctx, s.gqlClient, *campaign, activeDrop, s.userID, s.logger)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	if s.statePath != "" {
		if err := state.SaveRuntimeState(s.statePath, state.RuntimeState{
			ActiveCampaignID:     campaign.ID,
			ActiveDropID:         activeDrop.ID,
			WatchingChannelID:    ch.ID,
			WatchingChannelLogin: ch.Login,
			CurrentMinutes:       activeDrop.CurrentMinutes,
			LastSyncAt:           time.Now().UTC(),
		}); err != nil {
			s.logger.Warn("failed to save runtime state", "error", err)
		}
	}

	if s.confirmDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.confirmDelay):
		}
	}

	for i := 0; i < s.confirmRetries; i++ {
		dropID, _, ok, err := inventory.FetchCurrentDropProgress(ctx, s.gqlClient, ch.ID)
		if err == nil && (!ok || dropID != activeDrop.ID) {
			break
		}
		if s.confirmPollInterval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.confirmPollInterval):
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	nextDrop, ok := campaign.FirstEarnableDrop(time.Now(), &ch)
	if ok {
		s.activeDrop = nextDrop
		s.lastProgressAt = time.Now()
		if s.onProgress != nil {
			s.onProgress(nextDrop)
		}
		s.logger.Info("advanced to next earnable drop",
			slog.String("drop", nextDrop.Name),
			slog.Int("required_minutes", nextDrop.RequiredMinutes),
		)
		return nil
	}

	s.activeDrop = nil
	return errSessionComplete
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
