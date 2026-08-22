package daemon

import (
	"context"
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestSoak_GoroutineCeiling(t *testing.T) {
	camp := makeTestCampaign("c1", "Campaign 1", "Game1")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	var iterCount atomic.Int64
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		iterCount.Add(1)
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(2*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := sup.Run(ctx)
	require.NoError(t, err)

	require.GreaterOrEqual(t, iterCount.Load(), int64(200), "expected at least 200 iterations")

	runtime.GC()
	after := runtime.NumGoroutine()
	delta := after - baseline

	t.Logf("GoroutineCeiling completed %d iterations, baseline goroutines: %d, after: %d, delta: %d",
		iterCount.Load(), baseline, after, delta)

	assert.LessOrEqual(t, delta, 3, "goroutine leak detected: delta > 3")
}

func TestSoak_ErrorPathDoesNotLeak(t *testing.T) {
	camp := makeTestCampaign("c1", "Campaign 1", "Game1")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	var iterCount atomic.Int64
	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		n := iterCount.Add(1)
		if n%2 == 0 {
			return nil, errors.New("simulated transient network error")
		}
		return nil, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(2*time.Millisecond),
		WithWatchRunner(runWatch),
	)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := sup.Run(ctx)
	require.NoError(t, err)

	require.GreaterOrEqual(t, iterCount.Load(), int64(200), "expected at least 200 iterations on error path")

	runtime.GC()
	after := runtime.NumGoroutine()
	delta := after - baseline

	t.Logf("ErrorPathDoesNotLeak completed %d iterations, baseline goroutines: %d, after: %d, delta: %d",
		iterCount.Load(), baseline, after, delta)

	assert.LessOrEqual(t, delta, 3, "goroutine leak detected on error path: delta > 3")
}

func TestSoak_TimerDriftBounded(t *testing.T) {
	camp := makeTestCampaign("c1", "Campaign 1", "Game1")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) {
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	var mu sync.Mutex
	var timestamps []time.Time

	runWatch := func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		return nil, nil
	}

	backoff := 20 * time.Millisecond
	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithReselectBackoff(backoff),
		WithWatchRunner(runWatch),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := sup.Run(ctx)
	require.NoError(t, err)

	mu.Lock()
	recorded := append([]time.Time(nil), timestamps...)
	mu.Unlock()

	require.GreaterOrEqual(t, len(recorded), 50, "expected at least 50 recorded timestamps")

	// Discard the initial startup cycle interval (recorded[1] - recorded[0])
	var intervals []time.Duration
	for i := 2; i < len(recorded); i++ {
		intervals = append(intervals, recorded[i].Sub(recorded[i-1]))
	}

	require.NotEmpty(t, intervals)

	minInterval := intervals[0]
	maxInterval := intervals[0]
	var totalDuration time.Duration

	for _, d := range intervals {
		if d < minInterval {
			minInterval = d
		}
		if d > maxInterval {
			maxInterval = d
		}
		totalDuration += d
	}

	meanInterval := totalDuration / time.Duration(len(intervals))
	meanTolerance := 10 * time.Millisecond
	assert.True(t, meanInterval >= backoff-meanTolerance && meanInterval <= backoff+meanTolerance,
		"mean interval %v not within expected backoff %v ± %v", meanInterval, backoff, meanTolerance)

	// Ensure individual interval jitter remains reasonable without being overly brittle to OS timer granularity
	jitterCeiling := 100 * time.Millisecond
	assert.LessOrEqual(t, maxInterval, jitterCeiling, "maximum interval %v exceeded jitter ceiling %v", maxInterval, jitterCeiling)

	firstInterval := intervals[0]
	lastInterval := intervals[len(intervals)-1]
	driftDiff := time.Duration(math.Abs(float64(lastInterval - firstInterval)))
	driftTolerance := 15 * time.Millisecond

	t.Logf("TimerDriftBounded: %d intervals recorded. min=%v, max=%v, mean=%v, first=%v, last=%v, first-vs-last diff=%v",
		len(intervals), minInterval, maxInterval, meanInterval, firstInterval, lastInterval, driftDiff)

	assert.LessOrEqual(t, driftDiff, driftTolerance, "timer drift exceeded tolerance between first and last interval")
}
