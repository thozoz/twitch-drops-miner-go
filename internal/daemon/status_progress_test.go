package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
)

func TestStatus_IdenticalNotificationsDoNotHideStall(t *testing.T) {
	s := NewSupervisor(nil, nil, nil, nil, nil)
	last := time.Now().Add(-6 * time.Minute)
	s.status = ipc.StatusResult{Status: "watching", ActiveDrop: "Icon", CurrentMinutes: 2, RequiredMinutes: 30, LastProgressTime: last}
	for n := 0; n < 20; n++ {
		s.UpdateDropProgress(&inventory.TimedDrop{ID: "icon", Name: "Icon", CurrentMinutes: 2, RequiredMinutes: 30})
	}
	st, err := s.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, "stalled", st.Status)
	require.Equal(t, 2, st.CurrentMinutes)
	require.True(t, st.LastProgressTime.Equal(last), "notifications are not earned minutes")
	s.UpdateDropProgress(&inventory.TimedDrop{ID: "icon", Name: "Icon", CurrentMinutes: 3, RequiredMinutes: 30})
	st, _ = s.Status(context.Background())
	require.Equal(t, "watching", st.Status)
	require.WithinDuration(t, time.Now(), st.LastProgressTime, time.Second)
}

func TestStatus_DecreasingMinutesAndIdle(t *testing.T) {
	s := NewSupervisor(nil, nil, nil, nil, nil)
	last := time.Now().Add(-6 * time.Minute)
	s.status = ipc.StatusResult{Status: "watching", ActiveDrop: "Icon", CurrentMinutes: 2, RequiredMinutes: 30, LastProgressTime: last}
	s.UpdateDropProgress(&inventory.TimedDrop{ID: "icon", Name: "Icon", CurrentMinutes: 1, RequiredMinutes: 30})
	st, _ := s.Status(context.Background())
	require.Equal(t, "stalled", st.Status)
	require.True(t, st.LastProgressTime.Equal(last))
	s.UpdateDropProgress(&inventory.TimedDrop{ID: "icon", Name: "Icon", CurrentMinutes: 2, RequiredMinutes: 30})
	st, _ = s.Status(context.Background())
	require.Equal(t, "stalled", st.Status, "oscillation back to an old high-water mark is not progress")
	s.UpdateDropProgress(&inventory.TimedDrop{ID: "next", Name: "Icon", CurrentMinutes: 2, RequiredMinutes: 30})
	st, _ = s.Status(context.Background())
	require.Equal(t, "watching", st.Status, "a genuinely new drop gets its own grace window even with the same name")
	s.status.Status = "idle"
	st, _ = s.Status(context.Background())
	require.Equal(t, "idle", st.Status)
}
