package inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"tdm/internal/model"
)

func TestTimedDrop_ProgressAndRemaining(t *testing.T) {
	drop := TimedDrop{
		ID:              "drop1",
		RequiredMinutes: 60,
		CurrentMinutes:  30,
	}
	assert.Equal(t, 0.5, drop.Progress())
	assert.Equal(t, 30, drop.RemainingMinutes())

	drop.CurrentMinutes = 60
	assert.Equal(t, 1.0, drop.Progress())
	assert.Equal(t, 0, drop.RemainingMinutes())

	drop.CurrentMinutes = 75
	assert.Equal(t, 1.0, drop.Progress())
	assert.Equal(t, 0, drop.RemainingMinutes())

	drop.CurrentMinutes = 0
	assert.Equal(t, 0.0, drop.Progress())
	assert.Equal(t, 60, drop.RemainingMinutes())

	drop.RequiredMinutes = 0
	assert.Equal(t, 0.0, drop.Progress())
	assert.Equal(t, 0, drop.RemainingMinutes())
}

func TestCheckClaimedFromBenefits(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	end := now.Add(2 * time.Hour)

	benefits := []Benefit{
		{ID: "b1", Name: "Reward 1", Type: BenefitDirectEntitlement},
		{ID: "b2", Name: "Reward 2", Type: BenefitDirectEntitlement},
	}

	// (a) 2-benefit drop where only ONE benefit ID is in claimedBenefits with in-window timestamp -> true
	claimedA := map[string]time.Time{
		"b1": now.Add(-1 * time.Hour), // in window
	}
	assert.True(t, CheckClaimedFromBenefits(benefits, start, end, claimedA))

	// (b) 2-benefit drop where one ID is present but timestamp is outside the window -> false
	claimedB := map[string]time.Time{
		"b1": now.Add(-3 * time.Hour), // before start
	}
	assert.False(t, CheckClaimedFromBenefits(benefits, start, end, claimedB))

	claimedB2 := map[string]time.Time{
		"b1": now.Add(3 * time.Hour), // after end
	}
	assert.False(t, CheckClaimedFromBenefits(benefits, start, end, claimedB2))

	// (c) no benefit IDs present at all -> false
	claimedC := map[string]time.Time{
		"other_b": now,
	}
	assert.False(t, CheckClaimedFromBenefits(benefits, start, end, claimedC))
}

func TestDropsCampaign_EligibilityAndStatus(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	cUnlinked := DropsCampaign{
		ID:       "c1",
		Linked:   false,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(1 * time.Hour),
	}
	assert.False(t, cUnlinked.Eligible())
	assert.True(t, cUnlinked.Active(now))
	assert.False(t, cUnlinked.Upcoming(now))
	assert.False(t, cUnlinked.Expired(now))

	cLinked := DropsCampaign{
		ID:       "c2",
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(1 * time.Hour),
	}
	assert.True(t, cLinked.Eligible())
	assert.True(t, cLinked.Active(now))

	cUpcoming := DropsCampaign{
		ID:       "c3",
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(1 * time.Hour),
		EndsAt:   now.Add(2 * time.Hour),
	}
	assert.False(t, cUpcoming.Active(now))
	assert.True(t, cUpcoming.Upcoming(now))
	assert.False(t, cUpcoming.Expired(now))

	cExpired := DropsCampaign{
		ID:       "c4",
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-2 * time.Hour),
		EndsAt:   now.Add(-1 * time.Hour),
	}
	assert.False(t, cExpired.Active(now))
	assert.False(t, cUpcoming.Expired(now))
	assert.True(t, cExpired.Expired(now))
}

func TestDropsCampaign_CanEarn(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	game := model.NewGame("100", "Normal Game", "")
	specialGame := model.NewGame("509663", "Special Game", "")

	campaign := DropsCampaign{
		ID:       "c1",
		Name:     "Campaign 1",
		Game:     game,
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(1 * time.Hour),
		Drops: []TimedDrop{
			{
				ID:              "d1",
				Name:            "Drop 1",
				StartsAt:        now.Add(-1 * time.Hour),
				EndsAt:          now.Add(1 * time.Hour),
				RequiredMinutes: 30,
				CurrentMinutes:  0,
				IsClaimed:       false,
				Benefits:        []Benefit{{ID: "b1", Type: BenefitDirectEntitlement}},
			},
		},
	}

	// 1. Channel is nil -> can earn
	assert.True(t, campaign.CanEarn(now, nil))

	// 2. Channel with Game == nil -> returns false without panicking
	chanNilGame := &model.Channel{
		ID:     "c_nil",
		Login:  "nilgame",
		Online: true,
		Game:   nil,
	}
	assert.False(t, campaign.CanEarn(now, chanNilGame))

	// 3. Channel with matching Game
	chanMatchingGame := &model.Channel{
		ID:     "c_match",
		Login:  "match",
		Online: true,
		Game:   &game,
	}
	assert.True(t, campaign.CanEarn(now, chanMatchingGame))

	// 4. Channel with different Game
	diffGame := model.NewGame("200", "Other Game", "")
	chanDiffGame := &model.Channel{
		ID:     "c_diff",
		Login:  "diff",
		Online: true,
		Game:   &diffGame,
	}
	assert.False(t, campaign.CanEarn(now, chanDiffGame))

	// 5. Special game campaign -> can earn on any channel even if channel has nil or diff game
	specialCampaign := campaign
	specialCampaign.Game = specialGame
	assert.True(t, specialCampaign.CanEarn(now, chanNilGame))
	assert.True(t, specialCampaign.CanEarn(now, chanDiffGame))

	// 6. ACL restricted campaign
	aclCampaign := campaign
	aclCampaign.AllowedChannels = []model.Channel{
		{ID: "allowed_1", Login: "streamer1"},
	}
	assert.False(t, aclCampaign.CanEarn(now, chanMatchingGame))

	chanAllowed := &model.Channel{
		ID:     "allowed_1",
		Login:  "streamer1",
		Online: true,
		Game:   &game,
	}
	assert.True(t, aclCampaign.CanEarn(now, chanAllowed))
}

func TestDropsCampaign_Preconditions(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	game := model.NewGame("100", "Normal Game", "")

	campaign := DropsCampaign{
		ID:       "c1",
		Game:     game,
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(1 * time.Hour),
		Drops: []TimedDrop{
			{
				ID:                  "d1",
				Name:                "Drop 1",
				StartsAt:            now.Add(-1 * time.Hour),
				EndsAt:              now.Add(1 * time.Hour),
				RequiredMinutes:     30,
				IsClaimed:           false,
				Benefits:            []Benefit{{ID: "b1"}},
				PreconditionDropIDs: nil,
			},
			{
				ID:                  "d2",
				Name:                "Drop 2",
				StartsAt:            now.Add(-1 * time.Hour),
				EndsAt:              now.Add(1 * time.Hour),
				RequiredMinutes:     30,
				IsClaimed:           false,
				Benefits:            []Benefit{{ID: "b2"}},
				PreconditionDropIDs: []string{"d1"},
			},
		},
	}

	assert.True(t, campaign.CanEarn(now, nil))

	// When d1 is claimed, d2 becomes earnable
	campaign.Drops[0].IsClaimed = true
	assert.True(t, campaign.CanEarn(now, nil))

	// When both are claimed, nothing is earnable
	campaign.Drops[1].IsClaimed = true
	assert.False(t, campaign.CanEarn(now, nil))
}

func TestDropsCampaign_CanEarnWithin(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	game := model.NewGame("100", "Normal Game", "")

	campaign := DropsCampaign{
		ID:       "c1",
		Game:     game,
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(30 * time.Minute), // starts in 30m
		EndsAt:   now.Add(2 * time.Hour),
		Drops: []TimedDrop{
			{
				ID:              "d1",
				StartsAt:        now.Add(30 * time.Minute),
				EndsAt:          now.Add(2 * time.Hour),
				RequiredMinutes: 30,
				Benefits:        []Benefit{{ID: "b1"}},
			},
		},
	}

	// Active now? No
	assert.False(t, campaign.Active(now))
	// Can earn now? No
	assert.False(t, campaign.CanEarn(now, nil))
	// Can earn within 15 mins? No
	assert.False(t, campaign.CanEarnWithin(now, now.Add(15*time.Minute)))
	// Can earn within 1 hour? Yes (starts in 30m)
	assert.True(t, campaign.CanEarnWithin(now, now.Add(1*time.Hour)))
}
