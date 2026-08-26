package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestSelectCampaign(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	gameA := model.NewGame("1", "GameA", "")
	gameB := model.NewGame("2", "GameB", "")
	gameC := model.NewGame("3", "GameC", "")

	makeCampaign := func(id, name string, game model.Game, endsAt time.Time, linked bool, drops []TimedDrop) DropsCampaign {
		if drops == nil {
			drops = []TimedDrop{
				{
					ID:              id + "-drop1",
					Name:            "Drop",
					StartsAt:        now.Add(-1 * time.Hour),
					EndsAt:          endsAt,
					RequiredMinutes: 60,
					CurrentMinutes:  0,
					IsClaimed:       false,
					Benefits:        []Benefit{{ID: id + "-b1"}},
				},
			}
		}
		return DropsCampaign{
			ID:       id,
			Name:     name,
			Game:     game,
			Linked:   linked,
			Valid:    true,
			StartsAt: now.Add(-1 * time.Hour),
			EndsAt:   endsAt,
			Drops:    drops,
		}
	}

	t.Run("priority list picks listed game over unlisted game", func(t *testing.T) {
		campB := makeCampaign("c-b", "Campaign B", gameB, now.Add(5*time.Hour), true, nil)
		campC := makeCampaign("c-c", "Campaign C", gameC, now.Add(2*time.Hour), true, nil)

		priority := []string{"GameA", "GameB"}
		exclude := []string{}

		selected := SelectCampaign([]DropsCampaign{campC, campB}, priority, exclude, now, false)
		require.NotNil(t, selected)
		assert.Equal(t, "c-b", selected.ID)
	})

	t.Run("empty priority list picks sooner ending campaign", func(t *testing.T) {
		campSooner := makeCampaign("c-sooner", "Sooner", gameA, now.Add(2*time.Hour), true, nil)
		campLater := makeCampaign("c-later", "Later", gameB, now.Add(6*time.Hour), true, nil)

		selected := SelectCampaign([]DropsCampaign{campLater, campSooner}, nil, nil, now, false)
		require.NotNil(t, selected)
		assert.Equal(t, "c-sooner", selected.ID)
	})

	t.Run("exclude list filters campaign even if only eligible one", func(t *testing.T) {
		campA := makeCampaign("c-a", "Campaign A", gameA, now.Add(2*time.Hour), true, nil)

		selected := SelectCampaign([]DropsCampaign{campA}, nil, []string{"GameA"}, now, false)
		assert.Nil(t, selected, "Excluded campaign must not be selected")
	})

	t.Run("unlinked campaign is not eligible and never selected", func(t *testing.T) {
		campUnlinked := makeCampaign("c-unlinked", "Unlinked", gameA, now.Add(2*time.Hour), false, nil)

		selected := SelectCampaign([]DropsCampaign{campUnlinked}, nil, nil, now, false)
		assert.Nil(t, selected)
	})

	t.Run("completed campaign cannot be earned within timeframe", func(t *testing.T) {
		completedDrop := TimedDrop{
			ID:              "d-done",
			StartsAt:        now.Add(-1 * time.Hour),
			EndsAt:          now.Add(2 * time.Hour),
			RequiredMinutes: 60,
			CurrentMinutes:  60,
			IsClaimed:       true,
			Benefits:        []Benefit{{ID: "b1"}},
		}
		campDone := makeCampaign("c-done", "Done", gameA, now.Add(2*time.Hour), true, []TimedDrop{completedDrop})

		selected := SelectCampaign([]DropsCampaign{campDone}, nil, nil, now, false)
		assert.Nil(t, selected)
	})

	t.Run("badge/emote campaign requires enableBadgesEmotes", func(t *testing.T) {
		campBadge := makeCampaign("c-badge", "Badge Campaign", gameA, now.Add(2*time.Hour), true, []TimedDrop{
			{
				ID:              "c-badge-drop1",
				StartsAt:        now.Add(-1 * time.Hour),
				EndsAt:          now.Add(2 * time.Hour),
				RequiredMinutes: 60,
				CurrentMinutes:  0,
				IsClaimed:       false,
				Benefits:        []Benefit{{ID: "c-badge-b1", Type: BenefitBadge}},
			},
		})

		selected := SelectCampaign([]DropsCampaign{campBadge}, nil, nil, now, false)
		assert.Nil(t, selected)

		selected = SelectCampaign([]DropsCampaign{campBadge}, nil, nil, now, true)
		require.NotNil(t, selected)
		assert.Equal(t, "c-badge", selected.ID)
	})

	t.Run("dropExclude filters out a campaign whose only drop is excluded", func(t *testing.T) {
		campA := makeCampaign("c-a", "Campaign A", gameA, now.Add(2*time.Hour), true, []TimedDrop{
			{
				ID:              "c-a-drop1",
				Name:            "Rare Cosmetic Pack",
				StartsAt:        now.Add(-1 * time.Hour),
				EndsAt:          now.Add(2 * time.Hour),
				RequiredMinutes: 60,
				Benefits:        []Benefit{{ID: "c-a-b1"}},
			},
		})

		selected := SelectCampaign([]DropsCampaign{campA}, nil, nil, now, false, "cosmetic")
		assert.Nil(t, selected, "a campaign with no non-excluded earnable drop must not be selected")

		// Without the keyword, the same campaign is selectable.
		selected = SelectCampaign([]DropsCampaign{campA}, nil, nil, now, false)
		require.NotNil(t, selected)
		assert.Equal(t, "c-a", selected.ID)
	})

	t.Run("dropExclude does not affect a campaign with a non-excluded earnable drop", func(t *testing.T) {
		campA := makeCampaign("c-a", "Campaign A", gameA, now.Add(2*time.Hour), true, []TimedDrop{
			{
				ID:              "c-a-drop1",
				Name:            "Ordinary Drop",
				StartsAt:        now.Add(-1 * time.Hour),
				EndsAt:          now.Add(2 * time.Hour),
				RequiredMinutes: 60,
				Benefits:        []Benefit{{ID: "c-a-b1"}},
			},
		})

		selected := SelectCampaign([]DropsCampaign{campA}, nil, nil, now, false, "cosmetic")
		require.NotNil(t, selected)
		assert.Equal(t, "c-a", selected.ID)
	})
}

func TestSortByPriority(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	gameA := model.NewGame("1", "GameA", "")
	gameB := model.NewGame("2", "GameB", "")
	gameC := model.NewGame("3", "GameC", "")

	campA := DropsCampaign{ID: "c-a", Game: gameA, EndsAt: now.Add(5 * time.Hour)}
	campB := DropsCampaign{ID: "c-b", Game: gameB, EndsAt: now.Add(1 * time.Hour)}
	campC := DropsCampaign{ID: "c-c", Game: gameC, EndsAt: now.Add(2 * time.Hour)}

	t.Run("ranks by priority index first", func(t *testing.T) {
		out := SortByPriority([]DropsCampaign{campA, campB, campC}, []string{"GameB", "GameA"})
		require.Len(t, out, 3)
		assert.Equal(t, []string{"c-b", "c-a", "c-c"}, []string{out[0].ID, out[1].ID, out[2].ID})
	})

	t.Run("falls back to EndsAt ascending when priority is empty", func(t *testing.T) {
		out := SortByPriority([]DropsCampaign{campA, campB, campC}, nil)
		require.Len(t, out, 3)
		assert.Equal(t, []string{"c-b", "c-c", "c-a"}, []string{out[0].ID, out[1].ID, out[2].ID})
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		in := []DropsCampaign{campA, campB}
		out := SortByPriority(in, []string{"GameB"})
		out[0].ID = "mutated"
		assert.Equal(t, "c-a", in[0].ID)
	})
}
