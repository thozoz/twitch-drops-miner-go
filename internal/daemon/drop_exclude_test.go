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

func newDropExcludeTestSupervisor(t *testing.T, opts ...SupervisorOption) *Supervisor {
	t.Helper()
	allOpts := append([]SupervisorOption{WithDropExclude([]string{"Existing"})}, opts...)
	return NewSupervisor(
		func(ctx context.Context) ([]inventory.DropsCampaign, error) { return nil, nil },
		func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) { return nil, nil },
		nil,
		nil,
		nil,
		allOpts...,
	)
}

func TestSupervisor_UpdateDropExcludeAddPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit", "config.json")
	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeAdd,
		Keywords: []string{"cosmetic"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "cosmetic"}, res.DropExclude)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "cosmetic"}, cfg.DropExclude,
		"drop_exclude must land in the config file the daemon was given")
}

func TestSupervisor_UpdateDropExcludePreservesOtherConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"log_level":"debug","priority":["Rust"],"drop_exclude":["Existing"]}`), 0o600))

	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeAdd,
		Keywords: []string{"cosmetic"},
	})
	require.NoError(t, err)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "cosmetic"}, cfg.DropExclude)
	assert.Equal(t, []string{"Rust"}, cfg.Priority, "writing drop_exclude must not clobber the priority list")
	assert.Equal(t, "debug", cfg.LogLevel, "an unrelated setting must survive the write")
}

func TestSupervisor_UpdateDropExcludeAddIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeAdd,
		Keywords: []string{"Existing"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.DropExclude, "an already-excluded keyword must not be duplicated")
}

func TestSupervisor_UpdateDropExcludeRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeSet,
		Keywords: []string{"a", "b", "c"},
	})
	require.NoError(t, err)

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeRemove,
		Keywords: []string{"b"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, res.DropExclude, "removal must preserve the order of what remains")

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, cfg.DropExclude)
}

func TestSupervisor_UpdateDropExcludeRemoveAbsentIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeRemove,
		Keywords: []string{"Never Added"},
	})
	require.NoError(t, err, "removing an absent keyword is a no-op, not an error")
	assert.Equal(t, []string{"Existing"}, res.DropExclude)
}

func TestSupervisor_UpdateDropExcludeListDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newDropExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{Action: ipc.DropExcludeList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.DropExclude)

	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a read-only query must not create a config file")
}

func TestSupervisor_UpdateDropExcludeRollsBackOnWriteFailure(t *testing.T) {
	original := persistDropExclude
	t.Cleanup(func() { persistDropExclude = original })
	persistDropExclude = func(path string, dropExclude []string) error {
		return errors.New("disk on fire")
	}

	s := newDropExcludeTestSupervisor(t, WithConfigPath("/irrelevant/config.json"))

	_, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeAdd,
		Keywords: []string{"cosmetic"},
	})
	require.Error(t, err, "a failed disk write must not report success")

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{Action: ipc.DropExcludeList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.DropExclude, "failed write must roll the change back")
}

func TestSupervisor_UpdateDropExcludeWithoutConfigPathStaysInMemory(t *testing.T) {
	s := newDropExcludeTestSupervisor(t)

	res, err := s.UpdateDropExclude(context.Background(), ipc.DropExcludeParams{
		Action:   ipc.DropExcludeAdd,
		Keywords: []string{"cosmetic"},
	})
	require.NoError(t, err, "no config path means in-memory only, not an error")
	assert.Equal(t, []string{"Existing", "cosmetic"}, res.DropExclude)
}

func TestSupervisor_DropExcludeReturnsLiveCopy(t *testing.T) {
	s := newDropExcludeTestSupervisor(t)

	got := s.DropExclude()
	assert.Equal(t, []string{"Existing"}, got)

	got[0] = "mutated"
	assert.Equal(t, []string{"Existing"}, s.DropExclude(), "callers must not be able to mutate supervisor state")
}

func TestSupervisor_RunForwardsDropExcludeToResolveChannel(t *testing.T) {
	camp := makeTestCampaign("c1", "Campaign 1", "Game1")

	fetchInventory := func(ctx context.Context) ([]inventory.DropsCampaign, error) {
		return []inventory.DropsCampaign{camp}, nil
	}

	var mu sync.Mutex
	var gotDropExclude []string
	resolveChannel := func(ctx context.Context, c inventory.DropsCampaign, dropExclude ...string) (*model.Channel, error) {
		mu.Lock()
		gotDropExclude = append([]string(nil), dropExclude...)
		mu.Unlock()
		ch := makeTestChannel("ch1", "streamer1", "Streamer 1", c.Game.Name)
		return &ch, nil
	}

	sup := NewSupervisor(
		fetchInventory,
		resolveChannel,
		nil,
		nil,
		nil,
		WithDropExclude([]string{"cosmetic"}),
		WithReselectBackoff(5*time.Millisecond),
		WithWatchRunner(func(ctx context.Context, campaign inventory.DropsCampaign, ch model.Channel) (*inventory.TimedDrop, error) {
			return nil, nil
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- sup.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(gotDropExclude) > 0
	}, 2*time.Second, 10*time.Millisecond, "resolveChannel must have been called")

	mu.Lock()
	assert.Equal(t, []string{"cosmetic"}, gotDropExclude, "Run must forward the live dropExclude list to resolveChannel")
	mu.Unlock()

	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor Run did not terminate promptly")
	}
}
