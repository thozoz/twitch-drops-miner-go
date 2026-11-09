package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestSupervisor_FallbackRejectsWrongCategoryWithoutMutatingInventory(t *testing.T) {
	campaigns := []inventory.DropsCampaign{makeTestCampaign("a", "A", "GameA"), makeTestCampaign("b", "B", "GameB")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var resolved, watched []string
	s := NewSupervisor(func(context.Context) ([]inventory.DropsCampaign, error) { return campaigns, nil },
		func(_ context.Context, c inventory.DropsCampaign, _ ...string) (*model.Channel, error) {
			resolved = append(resolved, c.ID)
			game := c.Game.Name
			if c.ID == "a" {
				game = "WrongCategory"
			}
			ch := makeTestChannel("ch", "streamer", "Streamer", game)
			return &ch, nil
		}, nil, []string{"GameA", "GameB"}, nil, WithReselectBackoff(time.Hour), WithWatchRunner(func(_ context.Context, c inventory.DropsCampaign, _ model.Channel) (*inventory.TimedDrop, error) {
			watched = append(watched, c.ID)
			cancel()
			return nil, nil
		}))
	require.NoError(t, s.Run(ctx))
	require.Equal(t, []string{"a", "b"}, resolved)
	require.Equal(t, []string{"b"}, watched)
	require.Equal(t, "a", campaigns[0].ID)
	require.Equal(t, "b", campaigns[1].ID)
	st, _ := s.Status(ctx)
	require.Zero(t, st.ErrorCount)
}
