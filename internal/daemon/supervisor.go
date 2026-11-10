package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/session"
)

// SupervisorOption configures a Supervisor instance.
type SupervisorOption func(*Supervisor)

// WithReselectBackoff overrides the duration to wait between selection attempts or watch cycles.
func WithReselectBackoff(d time.Duration) SupervisorOption {
	return func(s *Supervisor) {
		s.reselectBackoff = d
	}
}

// defaultIdleBackoff is the production wait after a full selection pass found nothing to watch.
const defaultIdleBackoff = 60 * time.Second

// WithIdleBackoff overrides the wait after a selection pass yields no watchable campaign.
// Zero (the NewSupervisor default) falls back to the reselect backoff.
func WithIdleBackoff(d time.Duration) SupervisorOption {
	return func(s *Supervisor) {
		s.idleBackoff = d
	}
}

// WithWatchRunner injects the runner function executed for each active watch session.
func WithWatchRunner(fn func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error)) SupervisorOption {
	return func(s *Supervisor) {
		s.runWatch = fn
	}
}

// WithConfigPath tells the Supervisor which config file to persist priority
// changes into. It must be the same file the running daemon loaded — the caller
// resolves it, rather than the Supervisor re-deriving a path that could differ
// from the one actually in use.
//
// When unset, priority changes stay in memory only.
func WithConfigPath(path string) SupervisorOption {
	return func(s *Supervisor) {
		s.configPath = path
	}
}

// WithEnableBadgesEmotes sets whether badge/emote-reward campaigns are
// included in the eligible candidate pool, mirroring the enable_badges_emotes
// config setting read at startup. See Supervisor.enableBadgesEmotes's doc
// comment for why this is not live-updatable.
func WithEnableBadgesEmotes(enabled bool) SupervisorOption {
	return func(s *Supervisor) {
		s.enableBadgesEmotes = enabled
	}
}

// WithDropExclude seeds the initial drop-name/benefit keyword exclusion list.
// It is a SupervisorOption rather than a constructor positional argument (like
// priority/exclude) so existing NewSupervisor call sites do not need updating.
func WithDropExclude(list []string) SupervisorOption {
	return func(s *Supervisor) {
		s.dropExclude = append([]string(nil), list...)
	}
}

// persistPriority is the hook used to write the priority list to disk. It is a
// field so tests can substitute a failing writer without touching the filesystem.
var persistPriority = config.SavePriority

// persistExclude is the hook used to write the exclude list to disk. It is a
// variable for the same reason persistPriority is: tests swap it out.
var persistExclude = config.SaveExclude

// persistDropExclude is the hook used to write the drop-name/benefit keyword
// exclude list to disk. It is a variable for the same reason persistPriority is:
// tests swap it out.
var persistDropExclude = config.SaveDropExclude

// Supervisor manages the long-lived campaign/channel selection and watch loop.
type Supervisor struct {
	fetchInventory  func(ctx context.Context) ([]inventory.DropsCampaign, error)
	resolveChannel  func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error)
	runWatch        func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error)
	logger          *slog.Logger
	reselectBackoff time.Duration
	idleBackoff     time.Duration
	startedAt       time.Time
	configPath      string

	// channelCooldown tracks channels that yielded ErrNoProgress to temporarily avoid reselecting them
	channelCooldown map[string]time.Time

	// enableBadgesEmotes mirrors the enable_badges_emotes config setting at the
	// moment the daemon started. Unlike priority/exclude it is never mutated
	// live -- SplitEligible only runs once per selection cycle inside
	// fetchInventory, and threading a live update through would need a new IPC
	// method and a second mutable field with its own lock. A config change
	// takes effect on the next daemon restart.
	enableBadgesEmotes bool

	priorityMu  sync.RWMutex
	priority    []string
	exclude     []string
	dropExclude []string

	statusMu          sync.RWMutex
	status            ipc.StatusResult
	errorCount        int
	progressDropID    string
	progressHighWater int
}

// NewSupervisor creates a Supervisor with injected fetch and resolve closures.
func NewSupervisor(
	fetchInventory func(ctx context.Context) ([]inventory.DropsCampaign, error),
	resolveChannel func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error),
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
	enableBadgesEmotes bool,
	opts ...SupervisorOption,
) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	fetcher := inventory.NewFetcher(gqlClient)

	var s *Supervisor
	unclaimableDrops := make(map[string]time.Time)

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		campaigns, err := fetcher.FetchInventory(ctx, userID)
		if err != nil {
			return nil, err
		}

		var currentExclude, currentDropExclude []string
		if s != nil {
			s.priorityMu.RLock()
			currentExclude = append([]string(nil), s.exclude...)
			currentDropExclude = append([]string(nil), s.dropExclude...)
			s.priorityMu.RUnlock()
		} else {
			currentExclude = append([]string(nil), exclude...)
		}

		sweptCount, sweepErrs := inventory.SweepUnclaimed(
			ctx, gqlClient, userID, campaigns, time.Now(), logger,
			inventory.WithSweepExclude(currentExclude),
			inventory.WithSweepDropExclude(currentDropExclude),
			inventory.WithSweepUnclaimableCache(unclaimableDrops),
		)
		if len(sweepErrs) > 0 {
			logger.Warn("encountered errors during startup sweep of unclaimed drops", "errors_count", len(sweepErrs))
		}
		if sweptCount > 0 {
			logger.Info("swept and claimed unclaimed drops from prior runs", "count", sweptCount)
		}

		eligible, skipped := inventory.SplitEligible(campaigns, enableBadgesEmotes)
		for _, sk := range skipped {
			logger.Debug("campaign skipped",
				"game", sk.Game.Name,
				"campaign", sk.Name,
				"reason", string(sk.Reason),
				"detail", sk.Reason.Detail(),
				"link_url", sk.LinkURL,
			)
		}
		return eligible, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		return inventory.ResolveChannelWithCooldown(ctx, gqlClient, c, s.isChannelCooled, dropExclude...)
	}

	allOpts := append([]SupervisorOption{
		WithEnableBadgesEmotes(enableBadgesEmotes),
		WithIdleBackoff(defaultIdleBackoff),
	}, opts...)
	s = NewSupervisor(fetchInventory, resolveChannel, logger, priority, exclude, allOpts...)
	return s
}

func (s *Supervisor) isChannelCooled(login string) bool {
	if s.channelCooldown == nil {
		return false
	}
	until, ok := s.channelCooldown[login]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(s.channelCooldown, login)
		return false
	}
	return true
}

func (s *Supervisor) coolChannel(login string, d time.Duration) {
	if s.channelCooldown == nil {
		s.channelCooldown = make(map[string]time.Time)
	}
	s.channelCooldown[login] = time.Now().Add(d)
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

		// Read fresh priority, exclude, and dropExclude at every selection boundary (DMN-06)
		s.priorityMu.RLock()
		priority := append([]string(nil), s.priority...)
		exclude := append([]string(nil), s.exclude...)
		dropExclude := append([]string(nil), s.dropExclude...)
		s.priorityMu.RUnlock()

		candidates := append([]inventory.DropsCampaign(nil), eligible...)
		var (
			selectedCampaign *inventory.DropsCampaign
			selectedChannel  *model.Channel
			initialDrop      *inventory.TimedDrop
			firstAttempted   *inventory.DropsCampaign
		)

		for len(candidates) > 0 {
			if ctx.Err() != nil {
				return nil
			}

			pick := inventory.SelectCampaign(candidates, priority, exclude, time.Now(), s.enableBadgesEmotes, dropExclude...)
			if pick == nil {
				break
			}
			if firstAttempted == nil {
				firstAttempted = pick
			}

			// Remove attempted campaign from this pass's candidates
			n := 0
			for _, c := range candidates {
				if c.ID != pick.ID {
					candidates[n] = c
					n++
				}
			}
			candidates = candidates[:n]

			ch, err := s.resolveChannel(ctx, *pick, dropExclude...)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				s.logger.Warn("failed to resolve channel for selected campaign", "campaign", pick.Name, "error", err)
				s.statusMu.Lock()
				s.errorCount++
				s.statusMu.Unlock()
				continue
			}
			if ch == nil {
				s.logger.Info("no live channel found for campaign", "campaign", pick.Name, "game", pick.Game.Name)
				continue
			}

			drop, ok := pick.FirstEarnableDrop(time.Now(), ch, dropExclude...)
			if !ok || drop == nil {
				s.logger.Info("selected campaign has no earnable drop on resolved channel",
					"campaign", pick.Name,
					"game", pick.Game.Name,
					"channel", ch.Name())
				continue
			}

			selectedCampaign = pick
			selectedChannel = ch
			initialDrop = drop
			break
		}

		if selectedCampaign == nil {
			s.statusMu.Lock()
			st := ipc.StatusResult{
				Status:       "idle",
				LastSyncTime: time.Now(),
			}
			if firstAttempted != nil {
				st.ActiveCampaign = firstAttempted.Name
				st.ActiveGame = firstAttempted.Game.Name
			}
			s.status = st
			s.statusMu.Unlock()

			idle := s.idleBackoff
			if idle == 0 {
				idle = s.reselectBackoff
			}
			if !s.sleep(ctx, idle) {
				return nil
			}
			continue
		}

		s.statusMu.Lock()
		s.status = ipc.StatusResult{
			Status:           "watching",
			ActiveCampaign:   selectedCampaign.Name,
			ActiveGame:       selectedCampaign.Game.Name,
			WatchingChannel:  selectedChannel.Name(),
			ActiveDrop:       initialDrop.Name,
			CurrentMinutes:   initialDrop.CurrentMinutes,
			RequiredMinutes:  initialDrop.RequiredMinutes,
			ProgressPercent:  initialDrop.Progress() * 100,
			ETASeconds:       int64(initialDrop.RemainingMinutes()) * 60,
			LastSyncTime:     time.Now(),
			LastProgressTime: time.Now(),
		}
		s.progressDropID = initialDrop.ID
		s.progressHighWater = initialDrop.CurrentMinutes
		s.statusMu.Unlock()

		drop, runErr := s.runWatch(ctx, *selectedCampaign, *selectedChannel)
		if ctx.Err() != nil {
			return nil
		}

		if runErr != nil && !errors.Is(runErr, session.ErrChannelOffline) && !errors.Is(runErr, session.ErrNoEarnableDrop) {
			s.logger.Error("watch session error", "error", runErr)
			s.statusMu.Lock()
			s.errorCount++
			s.statusMu.Unlock()
		}

		if errors.Is(runErr, session.ErrNoProgress) || errors.Is(runErr, session.ErrWatcherFailed) {
			s.logger.Warn("abandoning channel, placing into temporary cooldown", "channel", selectedChannel.Name(), "reason", runErr)
			s.coolChannel(selectedChannel.Login, 10*time.Minute)
		}

		if runErr != nil {
			s.statusMu.Lock()
			s.status.Status = "retrying"
			s.statusMu.Unlock()
		}

		if drop != nil {
			s.statusMu.Lock()
			s.recordProgressLocked(drop)
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
	if st.Status == "watching" && !st.LastProgressTime.IsZero() && time.Since(st.LastProgressTime) >= 5*time.Minute {
		st.Status = "stalled"
	}
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

	previous := append([]string(nil), s.priority...)

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
	case ipc.PriorityRemove:
		s.priority = removeGames(s.priority, p.Games)
	case ipc.PrioritySet:
		s.priority = append([]string(nil), p.Games...)
	}

	// Persist mutations so they survive a restart. Without this the change lives
	// only in this process and is silently lost on `tdm stop` / reboot.
	if p.Action != ipc.PriorityList && s.configPath != "" {
		if err := persistPriority(s.configPath, s.priority); err != nil {
			// Roll back, so what the caller is told matches both what is on disk
			// and what this process will act on. Reporting success here would
			// leave the operator believing a change that vanishes at restart.
			s.priority = previous
			s.logger.Error("failed to persist priority to config",
				"path", s.configPath, "error", err)
			return ipc.PriorityResult{}, fmt.Errorf("failed to persist priority to %s: %w", s.configPath, err)
		}
	}

	return ipc.PriorityResult{
		Priority: append([]string(nil), s.priority...),
	}, nil
}

// UpdateExclude updates or queries the excluded game list.
//
// It mirrors UpdatePriority exactly — same lock, same persist-or-roll-back
// contract — because an exclude that survives in memory but not on disk is the
// same trap as a priority that does: the operator sees the game disappear from
// selection, restarts, and it silently comes back.
//
// Matching stays case-sensitive on Game.Name, the same comparison
// inventory.SelectCampaign performs; normalizing here would make the CLI accept
// entries that then never match a campaign.
func (s *Supervisor) UpdateExclude(ctx context.Context, p ipc.ExcludeParams) (ipc.ExcludeResult, error) {
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()

	previous := append([]string(nil), s.exclude...)

	switch p.Action {
	case ipc.ExcludeList:
		// read-only no-op
	case ipc.ExcludeAdd:
		for _, g := range p.Games {
			found := false
			for _, existing := range s.exclude {
				if existing == g {
					found = true
					break
				}
			}
			if !found {
				s.exclude = append(s.exclude, g)
			}
		}
	case ipc.ExcludeRemove:
		s.exclude = removeGames(s.exclude, p.Games)
	case ipc.ExcludeSet:
		s.exclude = append([]string(nil), p.Games...)
	}

	if p.Action != ipc.ExcludeList && s.configPath != "" {
		if err := persistExclude(s.configPath, s.exclude); err != nil {
			s.exclude = previous
			s.logger.Error("failed to persist exclude to config",
				"path", s.configPath, "error", err)
			return ipc.ExcludeResult{}, fmt.Errorf("failed to persist exclude to %s: %w", s.configPath, err)
		}
	}

	return ipc.ExcludeResult{
		Exclude: append([]string(nil), s.exclude...),
	}, nil
}

// DropExclude returns a copy of the current drop-name/benefit keyword exclude
// list. It exists for callers like the watch-session runner, constructed
// before the Supervisor selects a campaign, that need the live list rather
// than whatever was configured at daemon startup.
func (s *Supervisor) DropExclude() []string {
	s.priorityMu.RLock()
	defer s.priorityMu.RUnlock()
	return append([]string(nil), s.dropExclude...)
}

// UpdateDropExclude updates or queries the drop-name/benefit keyword exclude list.
//
// It mirrors UpdateExclude exactly — same lock, same persist-or-roll-back
// contract — for the same reason: a keyword that survives in memory but not on
// disk vanishes silently on restart. Matching against drop names/benefits is
// case-insensitive substring matching (see inventory.DropsCampaign.CanEarn),
// but the keyword list itself is deduplicated case-sensitively here, the same
// as priority/exclude.
func (s *Supervisor) UpdateDropExclude(ctx context.Context, p ipc.DropExcludeParams) (ipc.DropExcludeResult, error) {
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()

	previous := append([]string(nil), s.dropExclude...)

	switch p.Action {
	case ipc.DropExcludeList:
		// read-only no-op
	case ipc.DropExcludeAdd:
		for _, kw := range p.Keywords {
			found := false
			for _, existing := range s.dropExclude {
				if existing == kw {
					found = true
					break
				}
			}
			if !found {
				s.dropExclude = append(s.dropExclude, kw)
			}
		}
	case ipc.DropExcludeRemove:
		s.dropExclude = removeGames(s.dropExclude, p.Keywords)
	case ipc.DropExcludeSet:
		s.dropExclude = append([]string(nil), p.Keywords...)
	}

	if p.Action != ipc.DropExcludeList && s.configPath != "" {
		if err := persistDropExclude(s.configPath, s.dropExclude); err != nil {
			s.dropExclude = previous
			s.logger.Error("failed to persist drop exclude to config",
				"path", s.configPath, "error", err)
			return ipc.DropExcludeResult{}, fmt.Errorf("failed to persist drop exclude to %s: %w", s.configPath, err)
		}
	}

	return ipc.DropExcludeResult{
		DropExclude: append([]string(nil), s.dropExclude...),
	}, nil
}

// removeGames returns list with every entry in drop removed, preserving the
// order of what remains. Entries in drop that are not present are ignored, so
// removing an absent game is a no-op rather than an error — the caller asked
// for a state, not for a transaction.
//
// It always returns a fresh slice so callers never alias the input backing
// array, matching how the add/set branches build their results.
func removeGames(list, drop []string) []string {
	if len(drop) == 0 {
		return append([]string(nil), list...)
	}

	dropSet := make(map[string]struct{}, len(drop))
	for _, g := range drop {
		dropSet[g] = struct{}{}
	}

	out := make([]string, 0, len(list))
	for _, g := range list {
		if _, found := dropSet[g]; found {
			continue
		}
		out = append(out, g)
	}
	return out
}

// recordProgressLocked ignores repeated/decreasing samples; a new drop gets a grace window.
func (s *Supervisor) recordProgressLocked(drop *inventory.TimedDrop) {
	if s.progressDropID == "" {
		s.progressDropID = drop.ID
		s.progressHighWater = s.status.CurrentMinutes
	}
	if s.progressDropID != drop.ID {
		s.progressDropID = drop.ID
		s.progressHighWater = drop.CurrentMinutes
		s.status.LastProgressTime = time.Now()
	} else if drop.CurrentMinutes > s.progressHighWater {
		s.progressHighWater = drop.CurrentMinutes
		s.status.LastProgressTime = time.Now()
	}
}

// UpdateDropProgress updates the live active drop progress in the supervisor status.
func (s *Supervisor) UpdateDropProgress(drop *inventory.TimedDrop) {
	if drop == nil {
		return
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.recordProgressLocked(drop)
	s.status.ActiveDrop = drop.Name
	s.status.CurrentMinutes = drop.CurrentMinutes
	s.status.RequiredMinutes = drop.RequiredMinutes
	s.status.ProgressPercent = drop.Progress() * 100
	s.status.ETASeconds = int64(drop.RemainingMinutes()) * 60
	s.status.LastSyncTime = time.Now()
}
