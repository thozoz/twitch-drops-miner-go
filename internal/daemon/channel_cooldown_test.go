package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/session"
)

func TestSupervisor_NoProgressCooldownAndExpiry(t *testing.T) {
	campaign := makeTestCampaign("campaign", "Campaign", "Game")
	ch := makeTestChannel("channel", "streamer", "Streamer", "Game")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var s *Supervisor
	resolves, watches := 0, 0
	s = NewSupervisor(func(context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campaign}, nil
	},
		func(context.Context, inventory.DropsCampaign, ...string) (*model.Channel, error) {
			resolves++
			if resolves == 2 {
				require.True(t, s.isChannelCooled(ch.Login))
				cancel()
				return nil, nil
			}
			return &ch, nil
		}, nil, nil, nil, WithReselectBackoff(time.Millisecond), WithWatchRunner(func(context.Context, inventory.DropsCampaign, model.Channel) (*inventory.TimedDrop, error) {
			watches++
			return nil, session.ErrNoProgress
		}))
	require.NoError(t, s.Run(ctx))
	require.Equal(t, 1, watches)
	require.True(t, s.isChannelCooled(ch.Login))
	st, _ := s.Status(ctx)
	require.Equal(t, 1, st.ErrorCount)
	s.channelCooldown[ch.Login] = time.Now().Add(-time.Second)
	require.False(t, s.isChannelCooled(ch.Login))
	require.Empty(t, s.channelCooldown)
}

func TestSupervisor_WatcherFailureCoolsChannel(t *testing.T) {
	campaign := makeTestCampaign("campaign", "Campaign", "Game")
	ch := makeTestChannel("channel", "streamer", "Streamer", "Game")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var s *Supervisor
	resolves, watches := 0, 0
	s = NewSupervisor(func(context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campaign}, nil
	},
		func(context.Context, inventory.DropsCampaign, ...string) (*model.Channel, error) {
			resolves++
			if resolves == 2 {
				require.True(t, s.isChannelCooled(ch.Login), "channel abandoned by the watcher must be cooled down")
				cancel()
				return nil, nil
			}
			return &ch, nil
		}, nil, nil, nil, WithReselectBackoff(time.Millisecond), WithWatchRunner(func(context.Context, inventory.DropsCampaign, model.Channel) (*inventory.TimedDrop, error) {
			watches++
			return nil, fmt.Errorf("%w: segment HEAD returned HTTP 403", session.ErrWatcherFailed)
		}))
	require.NoError(t, s.Run(ctx))
	require.Equal(t, 1, watches)
	st, _ := s.Status(ctx)
	require.Equal(t, 1, st.ErrorCount)
}

func TestSupervisor_IdleBackoffAfterEmptyPass(t *testing.T) {
	campaign := makeTestCampaign("campaign", "Campaign", "Game")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolves := 0
	s := NewSupervisor(func(context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{campaign}, nil
	},
		func(context.Context, inventory.DropsCampaign, ...string) (*model.Channel, error) {
			resolves++
			return nil, nil
		}, nil, nil, nil,
		WithReselectBackoff(time.Millisecond),
		WithIdleBackoff(time.Hour),
		WithWatchRunner(func(context.Context, inventory.DropsCampaign, model.Channel) (*inventory.TimedDrop, error) {
			return nil, nil
		}))
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "idle wait must be cancelable")
	case <-time.After(time.Second):
		t.Fatal("idle backoff ignored cancellation")
	}
	require.Equal(t, 1, resolves, "empty pass must wait the idle backoff, not the reselect backoff")
}
