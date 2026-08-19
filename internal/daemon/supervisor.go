package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"tdm/internal/gql"
	"tdm/internal/inventory"
	"tdm/internal/ipc"
	"tdm/internal/model"
	"tdm/internal/session"
)

// SupervisorOption configures a Supervisor instance.
type SupervisorOption func(*Supervisor)

// WithReselectBackoff overrides the duration to wait between selection attempts or watch cycles.
func WithReselectBackoff(d time.Duration) SupervisorOption {
	return func(s *Supervisor) {
		s.reselectBackoff = d
	}
}

// WithWatchRunner injects the runner function executed for each active watch session.
func WithWatchRunner(fn func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error)) SupervisorOption {
	return func(s *Supervisor) {
		s.runWatch = fn
	}
}

// Supervisor manages the long-lived campaign/channel selection and watch loop.
type Supervisor struct {
	fetchInventory  func(ctx context.Context) ([]inventory.DropsCampaign, error)
	resolveChannel  func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error)
	runWatch        func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error)
	logger          *slog.Logger
	reselectBackoff time.Duration
	startedAt       time.Time

	priorityMu sync.RWMutex
	priority   []string
	exclude    []string

	statusMu   sync.RWMutex
	status     ipc.StatusResult
	errorCount int
}

// NewSupervisor creates a Supervisor with injected fetch and resolve closures.
func NewSupervisor(
	fetchInventory func(ctx context.Context) ([]inventory.DropsCampaign, error),
	resolveChannel func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error),
	logger *slog.Logger,
	priority, exclude []string,
	opts ...SupervisorOption,
) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Supervisor{
		fetchInventory:  fetchInventory,
		resolveChannel:  resolveChannel,
		logger:          logger,
		reselectBackoff: 5 * time.Second,
		startedAt:       time.Now(),
		priority:        append([]string(nil), priority...),
		exclude:         append([]string(nil), exclude...),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// NewProductionSupervisor creates a Supervisor with standard production closures for Twitch GQL inventory.
func NewProductionSupervisor(
	gqlClient *gql.Client,
	userID int,
	logger *slog.Logger,
	priority, exclude []string,
	opts ...SupervisorOption,
) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	fetcher := inventory.NewFetcher(gqlClient)

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		campaigns, err := fetcher.FetchInventory(ctx, userID)
		if err != nil {
			return nil, err
		}

		sweptCount, sweepErrs := inventory.SweepUnclaimed(ctx, gqlClient, userID, campaigns, time.Now(), logger)
		if len(sweepErrs) > 0 {
			logger.Warn("encountered errors during startup sweep of unclaimed drops", "errors_count", len(sweepErrs))
		}
		if sweptCount > 0 {
			logger.Info("swept and claimed unclaimed drops from prior runs", "count", sweptCount)
		}

		eligible, unlinked := inventory.SplitEligible(campaigns)
		for _, u := range unlinked {
			logger.Warn("campaign unlinked, skipped: run the link URL to enable it",
				"game", u.Game.Name,
				"link_url", u.LinkURL,
			)
		}
		return eligible, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		return inventory.ResolveChannel(ctx, gqlClient, c)
	}

	return NewSupervisor(fetchInventory, resolveChannel, logger, priority, exclude, opts...)
}

// Run runs the supervisor loop: select campaign -> resolve channel -> watch -> repeat.
func (s *Supervisor) Run(ctx context.Context) error {
	if s.runWatch == nil {
		return errors.New("supervisor runWatch runner not configured")
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		eligible, err := s.fetchInventory(ctx)
		if err != nil {
			s.logger.Warn("failed to fetch inventory", "error", err)
			s.statusMu.Lock()
			s.errorCount++
			s.statusMu.Unlock()

			if !s.sleep(ctx, s.reselectBackoff) {
				return nil
			}
			continue
		}

		// Read fresh priority and exclude at every selection boundary (DMN-06)
		s.priorityMu.RLock()
		priority := append([]string(nil), s.priority...)
		exclude := append([]string(nil), s.exclude...)
		s.priorityMu.RUnlock()

		selected := inventory.SelectCampaign(eligible, priority, exclude, time.Now())
		if selected == nil {
			s.statusMu.Lock()
			s.status = ipc.StatusResult{
				Status:       "idle",
				LastSyncTime: time.Now(),
			}
			s.statusMu.Unlock()

			if !s.sleep(ctx, s.reselectBackoff) {
				return nil
			}
			continue
		}

		ch, err := s.resolveChannel(ctx, *selected)
		if err != nil || ch == nil {
			if err != nil {
				s.logger.Warn("failed to resolve channel for selected campaign", "campaign", selected.Name, "error", err)
				s.statusMu.Lock()
				s.errorCount++
				s.statusMu.Unlock()
			} else {
				s.logger.Info("no live channel found for campaign", "campaign", selected.Name, "game", selected.Game.Name)
			}
			s.statusMu.Lock()
			s.status = ipc.StatusResult{
				Status:         "idle",
				ActiveCampaign: selected.Name,
				ActiveGame:     selected.Game.Name,
				LastSyncTime:   time.Now(),
			}
			s.statusMu.Unlock()

			if !s.sleep(ctx, s.reselectBackoff) {
				return nil
			}
			continue
		}

		s.statusMu.Lock()
		s.status = ipc.StatusResult{
			Status:          "watching",
			ActiveCampaign:  selected.Name,
			ActiveGame:      selected.Game.Name,
			WatchingChannel: ch.Name(),
			LastSyncTime:    time.Now(),
		}
		s.statusMu.Unlock()

		drop, runErr := s.runWatch(ctx, *selected, *ch)
		if ctx.Err() != nil {
			return nil
		}

		if runErr != nil && !errors.Is(runErr, session.ErrChannelOffline) && !errors.Is(runErr, session.ErrNoEarnableDrop) {
			s.logger.Error("watch session error", "error", runErr)
			s.statusMu.Lock()
			s.errorCount++
			s.statusMu.Unlock()
		}

		if drop != nil {
			s.statusMu.Lock()
			s.status.ActiveDrop = drop.Name
			s.status.CurrentMinutes = drop.CurrentMinutes
			s.status.RequiredMinutes = drop.RequiredMinutes
			s.status.ProgressPercent = drop.Progress() * 100
			s.status.ETASeconds = int64(drop.RemainingMinutes()) * 60
			s.status.LastSyncTime = time.Now()
			s.statusMu.Unlock()
		}

		if !s.sleep(ctx, s.reselectBackoff) {
			return nil
		}
	}
}

func (s *Supervisor) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// Status returns a point-in-time snapshot of the daemon's operational state.
func (s *Supervisor) Status(ctx context.Context) (ipc.StatusResult, error) {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	st := s.status
	if st.Status == "" {
		st.Status = "starting"
	}
	st.PID = os.Getpid()
	st.UptimeSeconds = int64(time.Since(s.startedAt).Seconds())
	st.ErrorCount = s.errorCount
	return st, nil
}

// UpdatePriority updates or queries the priority game list.
func (s *Supervisor) UpdatePriority(ctx context.Context, p ipc.PriorityParams) (ipc.PriorityResult, error) {
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()

	switch p.Action {
	case ipc.PriorityList:
		// read-only no-op
	case ipc.PriorityAdd:
		for _, g := range p.Games {
			found := false
			for _, existing := range s.priority {
				if existing == g {
					found = true
					break
				}
			}
			if !found {
				s.priority = append(s.priority, g)
			}
		}
	case ipc.PrioritySet:
		s.priority = append([]string(nil), p.Games...)
	}

	return ipc.PriorityResult{
		Priority: append([]string(nil), s.priority...),
	}, nil
}
