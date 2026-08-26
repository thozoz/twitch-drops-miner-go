package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestBuildQueue(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	gameA := model.NewGame("1", "GameA", "")
	gameB := model.NewGame("2", "GameB", "")

	makeCampaign := func(id, name string, game model.Game, drops []inventory.TimedDrop) inventory.DropsCampaign {
		return inventory.DropsCampaign{
			ID:       id,
			Name:     name,
			Game:     game,
			Linked:   true,
			Valid:    true,
			StartsAt: now.Add(-1 * time.Hour),
			EndsAt:   now.Add(2 * time.Hour),
			Drops:    drops,
		}
	}

	t.Run("ranks campaigns by priority and flattens drops in order", func(t *testing.T) {
		campA := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Drop 1", RequiredMinutes: 30, CurrentMinutes: 10, Benefits: []inventory.Benefit{{ID: "b1"}}},
		})
		campB := makeCampaign("c-b", "Campaign B", gameB, []inventory.TimedDrop{
			{ID: "d2", Name: "Drop 2", RequiredMinutes: 60, CurrentMinutes: 0, Benefits: []inventory.Benefit{{ID: "b2"}}},
		})

		entries := BuildQueue([]inventory.DropsCampaign{campA, campB}, []string{"GameB", "GameA"}, nil, nil, false, now)
		require.Len(t, entries, 2)
		assert.Equal(t, "GameB", entries[0].Game)
		assert.Equal(t, "Campaign B", entries[0].Campaign)
		assert.Equal(t, "Drop 2", entries[0].Drop)
		assert.Equal(t, "Ready", entries[0].Status)
		assert.Equal(t, 60, entries[0].RemainingMinutes)

		assert.Equal(t, "GameA", entries[1].Game)
		assert.Equal(t, "Drop 1", entries[1].Drop)
		assert.Equal(t, 20, entries[1].RemainingMinutes)
	})

	t.Run("game exclude removes the whole campaign", func(t *testing.T) {
		campA := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Drop 1", RequiredMinutes: 30, Benefits: []inventory.Benefit{{ID: "b1"}}},
		})

		entries := BuildQueue([]inventory.DropsCampaign{campA}, nil, []string{"GameA"}, nil, false, now)
		assert.Empty(t, entries)
	})

	t.Run("unlinked campaign is not eligible and is dropped", func(t *testing.T) {
		camp := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Drop 1", RequiredMinutes: 30, Benefits: []inventory.Benefit{{ID: "b1"}}},
		})
		camp.Linked = false

		entries := BuildQueue([]inventory.DropsCampaign{camp}, nil, nil, nil, false, now)
		assert.Empty(t, entries)
	})

	t.Run("expired campaign is dropped", func(t *testing.T) {
		camp := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Drop 1", RequiredMinutes: 30, Benefits: []inventory.Benefit{{ID: "b1"}}},
		})
		camp.EndsAt = now.Add(-1 * time.Minute)

		entries := BuildQueue([]inventory.DropsCampaign{camp}, nil, nil, nil, false, now)
		assert.Empty(t, entries)
	})

	t.Run("dropExclude marks a matching drop Excluded and its dependent Blocked, but both still appear", func(t *testing.T) {
		camp := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Rare Cosmetic Pack", RequiredMinutes: 30, Benefits: []inventory.Benefit{{ID: "b1"}}},
			{ID: "d2", Name: "Tier 2 Drop", RequiredMinutes: 60, Benefits: []inventory.Benefit{{ID: "b2"}}, PreconditionDropIDs: []string{"d1"}},
		})

		entries := BuildQueue([]inventory.DropsCampaign{camp}, nil, nil, []string{"cosmetic"}, false, now)
		require.Len(t, entries, 2)
		assert.Equal(t, "Excluded", entries[0].Status)
		assert.Equal(t, "Blocked by Rare Cosmetic Pack", entries[1].Status)
	})

	t.Run("claimed drop reports Claimed", func(t *testing.T) {
		camp := makeCampaign("c-a", "Campaign A", gameA, []inventory.TimedDrop{
			{ID: "d1", Name: "Drop 1", RequiredMinutes: 30, CurrentMinutes: 30, IsClaimed: true, Benefits: []inventory.Benefit{{ID: "b1"}}},
		})

		entries := BuildQueue([]inventory.DropsCampaign{camp}, nil, nil, nil, false, now)
		require.Len(t, entries, 1)
		assert.Equal(t, "Claimed", entries[0].Status)
	})

	t.Run("no campaigns yields an empty, non-nil queue", func(t *testing.T) {
		entries := BuildQueue(nil, nil, nil, nil, false, now)
		assert.Empty(t, entries)
	})
}

func TestFormatQueue(t *testing.T) {
	t.Run("empty queue", func(t *testing.T) {
		assert.Equal(t, "(queue is empty)\n", FormatQueue(nil))
	})

	t.Run("renders game, campaign, drop, progress, status, and deadline", func(t *testing.T) {
		endsAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		entries := []QueueEntry{
			{
				Game:             "GameA",
				Campaign:         "Campaign A",
				Drop:             "Drop 1",
				CurrentMinutes:   10,
				RequiredMinutes:  30,
				RemainingMinutes: 20,
				Status:           "Ready",
				CampaignEndsAt:   endsAt,
			},
		}

		out := FormatQueue(entries)
		assert.Contains(t, out, "1. [GameA] Campaign A — Drop 1")
		assert.Contains(t, out, "Progress: 10/30 min (20 min remaining)")
		assert.Contains(t, out, "Status: Ready")
		assert.Contains(t, out, "Campaign ends: "+endsAt.Format(time.RFC3339))
	})

	t.Run("numbers multiple entries in order", func(t *testing.T) {
		entries := []QueueEntry{
			{Game: "GameA", Campaign: "A", Drop: "D1"},
			{Game: "GameB", Campaign: "B", Drop: "D2"},
		}
		out := FormatQueue(entries)
		assert.Contains(t, out, "1. [GameA] A — D1")
		assert.Contains(t, out, "2. [GameB] B — D2")
	})
}
