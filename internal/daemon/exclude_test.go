package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/ipc"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func newExcludeTestSupervisor(t *testing.T, opts ...SupervisorOption) *Supervisor {
	t.Helper()
	return NewSupervisor(
		func(ctx context.Context) ([]inventory.DropsCampaign, error) { return nil, nil },
		func(ctx context.Context, c inventory.DropsCampaign) (*model.Channel, error) { return nil, nil },
		nil,
		[]string{"Rust"},
		[]string{"Existing"},
		opts...,
	)
}

func TestSupervisor_UpdateExcludeAddPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit", "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeAdd,
		Games:  []string{"ROBLOX"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "ROBLOX"}, res.Exclude)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "ROBLOX"}, cfg.Exclude,
		"exclude must land in the config file the daemon was given")
}

func TestSupervisor_UpdateExcludePreservesOtherConfigKeys(t *testing.T) {
	// SaveExclude is a single-key read-modify-write. The regression this guards
	// is it marshaling the whole Config struct instead, which would overwrite a
	// hand-written config's other keys with this process's defaults.
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"log_level":"debug","priority":["Rust"],"exclude":["Existing"]}`), 0o600))

	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeAdd,
		Games:  []string{"ROBLOX"},
	})
	require.NoError(t, err)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing", "ROBLOX"}, cfg.Exclude)
	assert.Equal(t, []string{"Rust"}, cfg.Priority, "writing exclude must not clobber the priority list")
	assert.Equal(t, "debug", cfg.LogLevel, "an unrelated setting must survive the write")
}

func TestSupervisor_UpdateExcludeAddIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeAdd,
		Games:  []string{"Existing"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.Exclude, "an already-excluded game must not be duplicated")
}

func TestSupervisor_UpdateExcludeRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeSet,
		Games:  []string{"A", "B", "C"},
	})
	require.NoError(t, err)

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeRemove,
		Games:  []string{"B"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "C"}, res.Exclude, "removal must preserve the order of what remains")

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "C"}, cfg.Exclude)
}

func TestSupervisor_UpdateExcludeRemoveAbsentIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeRemove,
		Games:  []string{"Never Added"},
	})
	require.NoError(t, err, "removing an absent game is a no-op, not an error")
	assert.Equal(t, []string{"Existing"}, res.Exclude)
}

func TestSupervisor_UpdateExcludeIsCaseSensitive(t *testing.T) {
	// SelectCampaign compares Game.Name case-sensitively, so "roblox" must not
	// remove "ROBLOX" — that would report a removal the selector never honors.
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeSet,
		Games:  []string{"ROBLOX"},
	})
	require.NoError(t, err)

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeRemove,
		Games:  []string{"roblox"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ROBLOX"}, res.Exclude)
}

func TestSupervisor_UpdateExcludeListDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{Action: ipc.ExcludeList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.Exclude)

	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a read-only query must not create a config file")
}

func TestSupervisor_UpdateExcludeRollsBackOnWriteFailure(t *testing.T) {
	original := persistExclude
	t.Cleanup(func() { persistExclude = original })
	persistExclude = func(path string, exclude []string) error {
		return errors.New("disk on fire")
	}

	s := newExcludeTestSupervisor(t, WithConfigPath("/irrelevant/config.json"))

	_, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeAdd,
		Games:  []string{"ROBLOX"},
	})
	require.Error(t, err, "a failed disk write must not report success")

	// In-memory state must match what is on disk, or the operator is told a lie
	// that evaporates at restart.
	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{Action: ipc.ExcludeList})
	require.NoError(t, err)
	assert.Equal(t, []string{"Existing"}, res.Exclude, "failed write must roll the change back")
}

func TestSupervisor_UpdateExcludeWithoutConfigPathStaysInMemory(t *testing.T) {
	s := newExcludeTestSupervisor(t)

	res, err := s.UpdateExclude(context.Background(), ipc.ExcludeParams{
		Action: ipc.ExcludeAdd,
		Games:  []string{"ROBLOX"},
	})
	require.NoError(t, err, "no config path means in-memory only, not an error")
	assert.Equal(t, []string{"Existing", "ROBLOX"}, res.Exclude)
}

func TestSupervisor_UpdatePriorityRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newExcludeTestSupervisor(t, WithConfigPath(path))

	_, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PrioritySet,
		Games:  []string{"Rust", "Valorant", "Overwatch 2"},
	})
	require.NoError(t, err)

	res, err := s.UpdatePriority(context.Background(), ipc.PriorityParams{
		Action: ipc.PriorityRemove,
		Games:  []string{"Valorant"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust", "Overwatch 2"}, res.Priority)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"Rust", "Overwatch 2"}, cfg.Priority)
}
