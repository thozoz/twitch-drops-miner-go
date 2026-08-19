package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeState_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "state.json")

	now := time.Now().Truncate(time.Millisecond).UTC()
	in := RuntimeState{
		ActiveCampaignID:     "camp-123",
		ActiveDropID:         "drop-456",
		WatchingChannelID:    "ch-789",
		WatchingChannelLogin: "streamer_alpha",
		CurrentMinutes:       42,
		LastSyncAt:           now,
	}

	err := SaveRuntimeState(statePath, in)
	require.NoError(t, err)

	out, found, err := LoadRuntimeState(statePath)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, in.ActiveCampaignID, out.ActiveCampaignID)
	assert.Equal(t, in.ActiveDropID, out.ActiveDropID)
	assert.Equal(t, in.WatchingChannelID, out.WatchingChannelID)
	assert.Equal(t, in.WatchingChannelLogin, out.WatchingChannelLogin)
	assert.Equal(t, in.CurrentMinutes, out.CurrentMinutes)
	assert.True(t, in.LastSyncAt.Equal(out.LastSyncAt))
}

func TestRuntimeState_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "missing_state.json")

	out, found, err := LoadRuntimeState(nonExistentPath)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, RuntimeState{}, out)
}

func TestRuntimeState_CorruptFile(t *testing.T) {
	tempDir := t.TempDir()
	corruptPath := filepath.Join(tempDir, "corrupt_state.json")

	err := os.WriteFile(corruptPath, []byte("invalid json data {"), 0600)
	require.NoError(t, err)

	out, found, err := LoadRuntimeState(corruptPath)
	require.Error(t, err)
	assert.False(t, found)
	assert.Equal(t, RuntimeState{}, out)
}

func TestRuntimeState_MultipleSaves_NoLeftoverTmpFiles(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "state.json")

	now1 := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond).UTC()
	firstState := RuntimeState{
		ActiveCampaignID:     "camp-1",
		ActiveDropID:         "drop-1",
		WatchingChannelID:    "ch-1",
		WatchingChannelLogin: "streamer1",
		CurrentMinutes:       10,
		LastSyncAt:           now1,
	}
	err := SaveRuntimeState(statePath, firstState)
	require.NoError(t, err)

	now2 := time.Now().Truncate(time.Millisecond).UTC()
	secondState := RuntimeState{
		ActiveCampaignID:     "camp-1",
		ActiveDropID:         "drop-1",
		WatchingChannelID:    "ch-1",
		WatchingChannelLogin: "streamer1",
		CurrentMinutes:       25,
		LastSyncAt:           now2,
	}
	err = SaveRuntimeState(statePath, secondState)
	require.NoError(t, err)

	out, found, err := LoadRuntimeState(statePath)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, secondState.CurrentMinutes, out.CurrentMinutes)
	assert.True(t, secondState.LastSyncAt.Equal(out.LastSyncAt))

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.False(t, strings.HasSuffix(entry.Name(), ".tmp"), "found leftover tmp file: %s", entry.Name())
	}
	assert.Len(t, entries, 1)
	assert.Equal(t, "state.json", entries[0].Name())
}
