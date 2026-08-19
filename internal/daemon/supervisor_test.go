package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
					{ID: id + "-b1", Name: "Benefit 1", Type: inventory.BenefitBadge},
				},
			},
		},
	}
}

func TestSupervisor_PriorityAppliesAtNextSwitchNotMidWatch(t *testing.T) {
	campA := makeTestCampaign("c-a", "Campaign A", "GameA")
	campB := makeTestCampaign("c-b", "Campaign B", "GameB")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campA, campB}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		return &model.Channel{
			ID:          "ch-" + c.Game.Name,
			Login:       "streamer_" + c.Game.Name,
			DisplayName: "Streamer " + c.Game.Name,
			Online:      true,
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

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		return &model.Channel{
			ID:          "ch1",
			Login:       "streamer1",
			DisplayName: "Streamer 1",
			Online:      true,
			Viewers:     50,
		}, nil
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

func TestSupervisor_NoEligibleCampaign_IdlesAndRetries(t *testing.T) {
	camp := makeTestCampaign("c-unlinked", "Unlinked Campaign", "ExcludedGame")
	camp.Linked = false // unlinked => not eligible

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
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
