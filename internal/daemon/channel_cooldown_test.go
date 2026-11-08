package daemon

import (
	"context"
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
