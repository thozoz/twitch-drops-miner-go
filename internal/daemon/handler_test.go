package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/inventory"
	"tdm/internal/ipc"
	"tdm/internal/model"
)

func TestHandler_ShutdownCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ring := NewRingBuffer(100)

	sup := NewSupervisor(
		func(ctx context.Context) ([]inventory.DropsCampaign, error) { return nil, nil },
		func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) { return nil, nil },
		nil,
		[]string{"Game1"},
		nil,
	)

	handler := NewHandler(sup, cancel, ring)

	// Test Status delegation
	st, err := handler.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "starting", st.Status)

	// Test Priority delegation
	pRes, err := handler.Priority(ctx, ipc.PriorityParams{Action: ipc.PriorityList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Game1"}, pRes.Priority)

	// Test GetLogs delegation
	_, err = ring.Write([]byte("test log line\n"))
	require.NoError(t, err)
	logsRes, err := handler.GetLogs(ctx, ipc.GetLogsParams{})
	require.NoError(t, err)
	assert.Equal(t, []string{"test log line"}, logsRes.Lines)

	// Test Shutdown cancels context
	res, err := handler.Shutdown(ctx, ipc.ShutdownParams{})
	require.NoError(t, err)
	assert.Equal(t, "shutting_down", res.Status)

	select {
	case <-ctx.Done():
		// Context successfully cancelled
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler.Shutdown did not cancel context")
	}
}
