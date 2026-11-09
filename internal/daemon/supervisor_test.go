package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func makeTestCampaign(id, name, gameName string) inventory.DropsCampaign {
	now := time.Now()
	game := model.NewGame(gameName, gameName, gameName)
	return inventory.DropsCampaign{
		ID:       id,
		Name:     name,
		Game:     game,
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(5 * time.Hour),
		Drops: []inventory.TimedDrop{
			{
				ID:              id + "-d1",
				Name:            name + " Drop 1",
				StartsAt:        now.Add(-1 * time.Hour),
				EndsAt:          now.Add(5 * time.Hour),
				RequiredMinutes: 60,
				CurrentMinutes:  0,
				Benefits: []inventory.Benefit{
					{ID: id + "-b1", Name: "Benefit 1", Type: inventory.BenefitDirectEntitlement},
				},
			},
		},
	}
}

func makeTestChannel(id, login, displayName, gameName string) model.Channel {
	g := model.NewGame(gameName, gameName, gameName)
	return model.Channel{
		ID:          id,
		Login:       login,
		DisplayName: displayName,
		Online:      true,
		Game:        &g,
		Viewers:     100,
	}
}

func TestSupervisor_PriorityAppliesAtNextSwitchNotMidWatch(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		g := c.Game
		return &model.Channel{
			ID:          "ch-" + c.Game.Name,
			Login:       "streamer_" + c.Game.Name,
			DisplayName: "Streamer " + c.Game.Name,
			Online:      true,
			Game:        &g,
			Viewers:     100,
		}, nil
	}

	var mu sync.Mutex
	var capturedCalls []string
	watch1Started := make(chan struct{})
	watch1Release := make(chan struct{})
	watch2Started := make(chan struct{})

	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		mu.Lock()
		callIdx := len(capturedCalls)
		capturedCalls = append(capturedCalls, campaign.Game.Name)
		mu.Unlock()

		if callIdx == 0 {
			close(watch1Started)
			select {
			case <-watch1Release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else if callIdx == 1 {
			close(watch2Started)
		}
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA"},
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	// Wait until watch session 1 is actively running on GameA
	select {
	case <-watch1Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch session 1 to start")
	}

	mu.Lock()
	require.Len(t, capturedCalls, 1)
	assert.Equal(t, "GameA", capturedCalls[0])
	mu.Unlock()

	// While blocked in watch session 1, mutate priority to prioritize GameB
	res, err := sup.UpdatePriority(ctx, ipc.PriorityParams{
		Action: ipc.PrioritySet,
		Games:  []string{"GameB"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"GameB"}, res.Priority)

	// Mid-watch argument is unaffected
	mu.Lock()
	require.Len(t, capturedCalls, 1)
	assert.Equal(t, "GameA", capturedCalls[0])
	mu.Unlock()

	// Release watch session 1 to complete
	close(watch1Release)

	// Wait for iteration 2 to select and run
	select {
	case <-watch2Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch session 2 to start")
	}

	mu.Lock()
	require.GreaterOrEqual(t, len(capturedCalls), 2)
	assert.Equal(t, "GameB", capturedCalls[1])
	mu.Unlock()

	// Cancel and wait for clean shutdown
	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func TestSupervisor_StatusReflectsProgress(t *testing.T) {
	camp := makeTestCampaign("c1", "Campaign 1", "Game1")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		return &inventory.TimedDrop{
			Name:            "Epic Reward",
			RequiredMinutes: 100,
			CurrentMinutes:  45,
		}, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	// Wait briefly for status update
	var st ipc.StatusResult
	var err error
	require.Eventually(t, func() bool {
		st, err = sup.Status(ctx)
		return err == nil && st.ActiveDrop == "Epic Reward"
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, 45.0, st.ProgressPercent)
	assert.Equal(t, int64(55*60), st.ETASeconds) // (100-45)*60
	assert.Equal(t, 45, st.CurrentMinutes)
	assert.Equal(t, 100, st.RequiredMinutes)
	assert.Equal(t, "watching", st.Status)
	assert.Equal(t, "Campaign 1", st.ActiveCampaign)
	assert.Equal(t, "Game1", st.ActiveGame)
	assert.Equal(t, "Streamer 1", st.WatchingChannel)
	assert.Greater(t, st.PID, 0)
	assert.GreaterOrEqual(t, st.UptimeSeconds, int64(0))

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func TestSupervisor_ResolvedChannelNotEarnable_IdlesAndDoesNotWatch(t *testing.T) {
	// Campaign has a drop starting in the future (CanEarnWithin is true, but FirstEarnableDrop is false now)
	now := time.Now()
	camp := makeTestCampaign("c-future", "Future Campaign", "Game1")
	camp.Drops[0].StartsAt = now.Add(30 * time.Minute)
	camp.Drops[0].EndsAt = now.Add(5 * time.Hour)

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	// Channel resolution succeeds (returns a live channel)
	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	runWatchCalled := false
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		runWatchCalled = true
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	// Wait briefly and verify status transitions to idle with campaign info
	// because FirstEarnableDrop returns false on the resolved channel
	var st ipc.StatusResult
	var err error
	require.Eventually(t, func() bool {
		st, err = sup.Status(ctx)
		return err == nil && st.Status == "idle" && st.ActiveCampaign == "Future Campaign"
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, "idle", st.Status)
	assert.Equal(t, "Future Campaign", st.ActiveCampaign)
	assert.Equal(t, "Game1", st.ActiveGame)
	assert.Empty(t, st.ActiveDrop)
	assert.False(t, runWatchCalled, "runWatch must not be called when FirstEarnableDrop returns false")

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func TestSupervisor_NoEligibleCampaign_IdlesAndRetries(t *testing.T) {
	camp := makeTestCampaign("c-unlinked", "Unlinked Campaign", "ExcludedGame")
	camp.Linked = false // unlinked => not eligible

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		return nil, nil
	}

	runWatchCalled := false
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		runWatchCalled = true
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	var st ipc.StatusResult
	var err error
	require.Eventually(t, func() bool {
		st, err = sup.Status(ctx)
		return err == nil && st.Status == "idle"
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, "idle", st.Status)
	assert.False(t, runWatchCalled)

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func newPriorityTestSupervisor(t *testing.T, opts ...SupervisorOption) *Supervisor {
	t.Helper()
	return NewSupervisor(
		func(ctx context.Context) ([]inventory.DropsCampaign, error) { return nil, nil },
		func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) { return nil, nil },
		nil,
		[]string{"Existing"},
		nil,
		opts...,
	)
}

func TestSupervisor_UpdatePriorityPersistsToConfiguredPath(t *testing.T) {
	// The regression this exists for: a daemon started with --config must write
	// back to THAT file, not to the default XDG location.
	explicitPath := filepath.Join(t.TempDir(), "explicit", "config.json")
	s := newPriorityTestSupervisor(t, WithConfigPath(explicitPath))

	res, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PriorityAdd,
		Games:  []string{"Rust"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "Rust"}, res.Priority)

	cfg, err := config.Load(explicitPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "Rust"}, cfg.Priority,
		"priority must land in the config file the daemon was given")
}

func TestSupervisor_UpdatePrioritySetPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newPriorityTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PrioritySet,
		Games:  []string{"Fortnite", "Rust"},
	})
	require.NoError(t, err)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Fortnite", "Rust"}, cfg.Priority)
}

func TestSupervisor_UpdatePriorityListDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newPriorityTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{Action: ipc.PriorityList})
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a read-only query must not create a config file")
}

func TestSupervisor_UpdatePriorityRollsBackOnWriteFailure(t *testing.T) {
	original := persistPriority
	t.Cleanup(func() { persistPriority = original })
	persistPriority = func(path string, priority []string) error {
		return errors.New("disk on fire")
	}

	s := newPriorityTestSupervisor(t, WithConfigPath("/irrelevant/config.json"))

	_, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PriorityAdd,
		Games:  []string{"Rust"},
	})
	require.Error(t, err, "a failed disk write must not report success")

	// In-memory state must match what is on disk, or the operator is told a lie
	// that evaporates at restart.
	res, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{Action: ipc.PriorityList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.Priority, "failed write must roll the change back")
}

func TestSupervisor_UpdatePriorityWithoutConfigPathStaysInMemory(t *testing.T) {
	s := newPriorityTestSupervisor(t)

	res, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PriorityAdd,
		Games:  []string{"Rust"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "Rust"}, res.Priority)
}

func TestSupervisor_CampaignFallback_NoLiveChannelTriesNextCandidate(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		if c.Game.Name == "GameA" {
			// GameA has no live channel
			return nil, nil
		}
		ch := makeTestChannel("ch-b", "streamer_b", "Streamer B", c.Game.Name)
		return &ch, nil
	}

	watched := make(chan string, 10)
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		watched <- campaign.Game.Name
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA", "GameB"},
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	// Expect GameB to be watched in the first pass because GameA has no live channel.
	// On buggy code, GameA causes supervisor to idle and sleep reselectBackoff without trying GameB.
	select {
	case game := <-watched:
		assert.Equal(t, "GameB", game)
	case <-time.After(150 * time.Millisecond):
		t.Fatal("timed out waiting for fallback to GameB: supervisor starved GameB due to GameA having no live channel")
	}

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func TestSupervisor_CampaignFallback_AllCandidatesExhaustedIdlesCleanly(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	var mu sync.Mutex
	passCount := 0
	resolutionsPerPass := make(map[int][]string)

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		mu.Lock()
		passCount++
		mu.Unlock()
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		mu.Lock()
		currentPass := passCount
		resolutionsPerPass[currentPass] = append(resolutionsPerPass[currentPass], c.Game.Name)
		mu.Unlock()
		return nil, nil // both offline
	}

	runWatchCalled := false
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		mu.Lock()
		runWatchCalled = true
		mu.Unlock()
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA", "GameB"},
		nil,
		WithReselectBackoff(10*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	// Wait until at least 2 passes have occurred
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return passCount >= 2
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.False(t, runWatchCalled)
	// In pass 1, both GameA and GameB should have been attempted exactly once
	require.Contains(t, resolutionsPerPass, 1)
	assert.Equal(t, []string{"GameA", "GameB"}, resolutionsPerPass[1], "pass 1 must attempt both GameA and GameB once before idling")
}

func TestSupervisor_CampaignFallback_NextPassReconsidersPriorCampaign(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	var mu sync.Mutex
	campAOnline := false
	var watchedGames []string
	watch1Done := make(chan struct{})
	watch2Done := make(chan struct{})

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		mu.Lock()
		online := campAOnline
		mu.Unlock()

		if c.Game.Name == "GameA" && !online {
			return nil, nil
		}
		ch := makeTestChannel("ch-"+c.Game.Name, "streamer_"+c.Game.Name, "Streamer "+c.Game.Name, c.Game.Name)
		return &ch, nil
	}

	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		mu.Lock()
		watchedGames = append(watchedGames, campaign.Game.Name)
		count := len(watchedGames)
		mu.Unlock()

		if count == 1 {
			// Pass 1 finished watching GameB. Now GameA comes online for pass 2.
			mu.Lock()
			campAOnline = true
			mu.Unlock()
			close(watch1Done)
		} else if count == 2 {
			close(watch2Done)
		}
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA", "GameB"},
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	select {
	case <-watch1Done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for watch 1 (GameB fallback)")
	}

	select {
	case <-watch2Done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for watch 2 (GameA reconsideration)")
	}

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, watchedGames, 2)
	assert.Equal(t, "GameB", watchedGames[0], "pass 1 should have watched GameB via fallback")
	assert.Equal(t, "GameA", watchedGames[1], "pass 2 should have watched GameA once online")
}

func TestSupervisor_CampaignFallback_ContextCancelAndError(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		if c.Game.Name == "GameA" {
			return nil, errors.New("resolution failure for GameA")
		}
		ch := makeTestChannel("ch-b", "streamer_b", "Streamer B", c.Game.Name)
		return &ch, nil
	}

	watched := make(chan string, 1)
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		watched <- campaign.Game.Name
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA", "GameB"},
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	select {
	case game := <-watched:
		assert.Equal(t, "GameB", game)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for fallback to GameB on GameA resolution error")
	}

	// Verify error was tracked
	st, err := sup.Status(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, st.ErrorCount, 1, "errorCount must be incremented on resolution error")

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}

func TestSupervisor_CampaignFallback_ContextCancelDuringFallback(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	inResolve := make(chan struct{})

	resolveChannel := func(cctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		close(inResolve)
		<-cctx.Done()
		return nil, cctx.Err()
	}

	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		[]string{"GameA", "GameB"},
		nil,
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	<-inResolve
	cancel()

	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly on context cancellation")
	}
}

