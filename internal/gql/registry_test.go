package gql

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Embedded(t *testing.T) {
	reg, replaced, err := LoadRegistry("")
	require.NoError(t, err)
	require.Empty(t, replaced)
	require.NotNil(t, reg)

	assert.Equal(t, "https://gql.twitch.tv/gql", reg.Endpoint())
	assert.Equal(t, "kd1unb4b3q4t58fwlpcbzcbnm76a8fp", reg.ClientID())

	// Verify ViewerDropsDashboard
	op, err := reg.Operation("ViewerDropsDashboard")
	require.NoError(t, err)
	assert.Equal(t, "ViewerDropsDashboard", op.Name)
	assert.Equal(t, "d9cae7761dafab85908c85e6683cb4201b449e66ac3bb5e894f15ff12aeafaa7", op.SHA256Hash)
	assert.Equal(t, false, op.Variables["fetchRewardCampaigns"])

	// Verify all 12 active queries are present
	expectedOps := []string{
		"VideoPlayerStreamInfoOverlayChannel",
		"ClaimCommunityPoints",
		"DropsPage_ClaimDropRewards",
		"ChannelPointsContext",
		"Inventory",
		"DropCurrentSessionContext",
		"ViewerDropsDashboard",
		"DropCampaignDetails",
		"DropsHighlightService_AvailableDrops",
		"PlaybackAccessToken",
		"DirectoryPage_Game",
		"DirectoryGameRedirect",
	}

	for _, name := range expectedOps {
		op, err := reg.Operation(name)
		assert.NoError(t, err, "expected operation %s to be present", name)
		assert.NotEmpty(t, op.SHA256Hash, "operation %s should have non-empty hash", name)
		assert.Equal(t, name, op.Name)
	}
}

func TestRegistry_Operation_Unknown(t *testing.T) {
	reg, _, err := LoadRegistry("")
	require.NoError(t, err)

	testCases := []string{
		"DoesNotExist",
		"UnknownQuery",
		"",
	}

	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			op, err := reg.Operation(name)
			assert.Error(t, err)
			assert.ErrorIs(t, err, ErrUnknownOperation)
			assert.Empty(t, op.SHA256Hash)
		})
	}
}

func TestRegistry_LoadRegistry_Override(t *testing.T) {
	t.Run("nonexistent file returns embedded defaults without error", func(t *testing.T) {
		reg, replaced, err := LoadRegistry(filepath.Join(t.TempDir(), "nonexistent.json"))
		require.NoError(t, err)
		assert.Empty(t, replaced)
		assert.NotNil(t, reg)
	})

	t.Run("valid override replaces operation and returns replaced list", func(t *testing.T) {
		dir := t.TempDir()
		overrideFile := filepath.Join(dir, "operations.json")
		customHash := "customhash1234567890abcdef1234567890abcdef1234567890abcdef12345678"
		content := `{
			"operations": {
				"ViewerDropsDashboard": {
					"sha256Hash": "` + customHash + `",
					"variables": {}
				},
				"BrandNewOperation": {
					"sha256Hash": "newhash123",
					"variables": {}
				}
			}
		}`
		err := os.WriteFile(overrideFile, []byte(content), 0600)
		require.NoError(t, err)

		reg, replaced, err := LoadRegistry(overrideFile)
		require.NoError(t, err)
		require.NotNil(t, reg)

		// Replaced should contain ViewerDropsDashboard (which was in embedded set)
		assert.Contains(t, replaced, "ViewerDropsDashboard")
		assert.NotContains(t, replaced, "BrandNewOperation")

		// Check updated operation
		op, err := reg.Operation("ViewerDropsDashboard")
		require.NoError(t, err)
		assert.Equal(t, customHash, op.SHA256Hash)

		// Check new operation
		newOp, err := reg.Operation("BrandNewOperation")
		require.NoError(t, err)
		assert.Equal(t, "newhash123", newOp.SHA256Hash)
	})

	t.Run("invalid json in override returns error", func(t *testing.T) {
		dir := t.TempDir()
		overrideFile := filepath.Join(dir, "operations.json")
		err := os.WriteFile(overrideFile, []byte("{invalid json"), 0600)
		require.NoError(t, err)

		reg, replaced, err := LoadRegistry(overrideFile)
		assert.Error(t, err)
		assert.Nil(t, reg)
		assert.Nil(t, replaced)
	})
}
