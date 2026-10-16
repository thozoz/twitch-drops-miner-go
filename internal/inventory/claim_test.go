package inventory

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestClaim_GenerateClaimID(t *testing.T) {
	claimID := GenerateClaimID(999, "camp-1", "drop-1")
	assert.Equal(t, "999#camp-1#drop-1", claimID)
}

func TestClaim_CanClaim(t *testing.T) {
	campaignEndsAt := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)

	// Valid claimable drop
	validDrop := TimedDrop{
		ID:        "drop-1",
		ClaimID:   "claim-1",
		IsClaimed: false,
	}
	assert.True(t, CanClaim(validDrop, campaignEndsAt, now))

	// Already claimed
	claimedDrop := TimedDrop{
		ID:        "drop-2",
		ClaimID:   "claim-2",
		IsClaimed: true,
	}
	assert.False(t, CanClaim(claimedDrop, campaignEndsAt, now))

	// Empty ClaimID
	noClaimIDDrop := TimedDrop{
		ID:        "drop-3",
		ClaimID:   "",
		IsClaimed: false,
	}
	assert.False(t, CanClaim(noClaimIDDrop, campaignEndsAt, now))

	// After campaign ends + 24 hours
	pastGracePeriod := campaignEndsAt.Add(25 * time.Hour)
	assert.False(t, CanClaim(validDrop, campaignEndsAt, pastGracePeriod))

	// Exactly at 24 hours after campaign ends (now.Before is false when equal or after)
	atGracePeriod := campaignEndsAt.Add(24 * time.Hour)
	assert.False(t, CanClaim(validDrop, campaignEndsAt, atGracePeriod))

	// Just before 24 hours after campaign ends
	beforeGracePeriod := campaignEndsAt.Add(24*time.Hour - time.Second)
	assert.True(t, CanClaim(validDrop, campaignEndsAt, beforeGracePeriod))
}

func TestClaim_ClaimDrop(t *testing.T) {
	fixtureData, err := os.ReadFile("../../testdata/fixtures/gql_claim_drop_rewards.json")
	require.NoError(t, err)

	campaign := DropsCampaign{
		ID:   "camp-100",
		Name: "Test Campaign",
		Game: model.NewGame("1", "Game", "game"),
	}

	t.Run("success ELIGIBLE_FOR_ALL", func(t *testing.T) {
		var reqCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&reqCount, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fixtureData)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, nil))

		drop := &TimedDrop{
			ID:              "drop-1",
			Name:            "Reward Drop 1",
			RequiredMinutes: 60,
			CurrentMinutes:  30,
			IsClaimed:       false,
			ClaimID:         "custom-claim-id",
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 12345, logger)
		require.NoError(t, err)
		assert.True(t, claimed)
		assert.True(t, drop.IsClaimed)
		assert.Equal(t, 60, drop.CurrentMinutes)
		assert.Equal(t, "custom-claim-id", drop.ClaimID)
		assert.Equal(t, int32(1), atomic.LoadInt32(&reqCount))

		logOut := buf.String()
		assert.Contains(t, logOut, "drop claimed successfully")
		assert.Contains(t, logOut, `"campaign":"Test Campaign"`)
		assert.Contains(t, logOut, `"drop":"Reward Drop 1"`)
		assert.Contains(t, logOut, `"claimed_at":`)
	})

	t.Run("success DROP_INSTANCE_ALREADY_CLAIMED", func(t *testing.T) {
		alreadyClaimedResp := []byte(`{
			"data": {
				"claimDropRewards": {
					"status": "DROP_INSTANCE_ALREADY_CLAIMED"
				}
			}
		}`)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(alreadyClaimedResp)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		drop := &TimedDrop{
			ID:              "drop-2",
			Name:            "Reward Drop 2",
			RequiredMinutes: 30,
			CurrentMinutes:  10,
			IsClaimed:       false,
			ClaimID:         "", // will be generated
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 999, nil)
		require.NoError(t, err)
		assert.True(t, claimed)
		assert.True(t, drop.IsClaimed)
		assert.Equal(t, 30, drop.CurrentMinutes)
		assert.Equal(t, "999#camp-100#drop-2", drop.ClaimID)
	})

	t.Run("unsuccessful status returns claimed=false and nil error", func(t *testing.T) {
		unknownResp := []byte(`{
			"data": {
				"claimDropRewards": {
					"status": "INTERNAL_ERROR"
				}
			}
		}`)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(unknownResp)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		drop := &TimedDrop{
			ID:              "drop-3",
			Name:            "Reward Drop 3",
			RequiredMinutes: 30,
			CurrentMinutes:  30,
			IsClaimed:       false,
			ClaimID:         "claim-3",
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 999, nil)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.False(t, drop.IsClaimed)
	})

	t.Run("null claimDropRewards returns claimed=false and nil error", func(t *testing.T) {
		nullResp := []byte(`{
			"data": {
				"claimDropRewards": null
			}
		}`)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(nullResp)
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		drop := &TimedDrop{
			ID:              "drop-4",
			Name:            "Reward Drop 4",
			RequiredMinutes: 30,
			CurrentMinutes:  30,
			IsClaimed:       false,
			ClaimID:         "claim-4",
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 999, nil)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.False(t, drop.IsClaimed)
	})

	t.Run("already claimed drop short-circuits without GQL request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("GQL endpoint should not have been called for already claimed drop")
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		drop := &TimedDrop{
			ID:        "drop-5",
			IsClaimed: true,
			ClaimID:   "claim-5",
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 999, nil)
		require.NoError(t, err)
		assert.True(t, claimed)
	})

	t.Run("eligible status with isUserAccountConnected=false returns ErrAccountNotLinked", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"claimDropRewards":{"status":"ELIGIBLE_FOR_ALL","isUserAccountConnected":false}}}`))
		}))
		defer server.Close()

		reg, _, err := gql.LoadRegistry("")
		require.NoError(t, err)

		httpClient := resty.New().SetHostURL(server.URL)
		client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

		drop := &TimedDrop{
			ID:              "drop-unlinked",
			Name:            "Unlinked Drop",
			RequiredMinutes: 60,
			CurrentMinutes:  60,
			IsClaimed:       false,
			ClaimID:         "claim-unlinked",
		}

		claimed, err := ClaimDrop(context.Background(), client, campaign, drop, 999, nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrAccountNotLinked))
		assert.False(t, claimed)
		assert.False(t, drop.IsClaimed)
	})
}

func TestSweepUnclaimed(t *testing.T) {
	fixtureData, err := os.ReadFile("../../testdata/fixtures/gql_claim_drop_rewards.json")
	require.NoError(t, err)

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixtureData)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	client := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

	campaigns := []DropsCampaign{
		{
			ID:       "camp-active-1",
			Name:     "Active Campaign 1",
			StartsAt: now.Add(-2 * time.Hour),
			EndsAt:   now.Add(2 * time.Hour),
			Valid:    true,
			Linked:   true,
			Drops: []TimedDrop{
				{
					ID:              "d1-1",
					Name:            "Drop 1-1 (Claimable)",
					ClaimID:         "claim-d1-1",
					IsClaimed:       false,
					RequiredMinutes: 60,
					CurrentMinutes:  60,
				},
				{
					ID:              "d1-2",
					Name:            "Drop 1-2 (Already Claimed)",
					ClaimID:         "claim-d1-2",
					IsClaimed:       true,
					RequiredMinutes: 60,
					CurrentMinutes:  60,
				},
			},
		},
		{
			ID:       "camp-active-2",
			Name:     "Active Campaign 2",
			StartsAt: now.Add(-5 * time.Hour),
			EndsAt:   now.Add(1 * time.Hour),
			Valid:    true,
			Linked:   true,
			Drops: []TimedDrop{
				{
					ID:              "d2-1",
					Name:            "Drop 2-1 (Claimable)",
					ClaimID:         "claim-d2-1",
					IsClaimed:       false,
					RequiredMinutes: 30,
					CurrentMinutes:  30,
				},
				{
					ID:              "d2-2",
					Name:            "Drop 2-2 (Already Claimed)",
					ClaimID:         "claim-d2-2",
					IsClaimed:       true,
					RequiredMinutes: 30,
					CurrentMinutes:  30,
				},
			},
		},
		{
			ID:       "camp-upcoming",
			Name:     "Upcoming Campaign",
			StartsAt: now.Add(2 * time.Hour),
			EndsAt:   now.Add(6 * time.Hour),
			Valid:    true,
			Linked:   true,
			Drops: []TimedDrop{
				{
					ID:              "d-up-1",
					Name:            "Upcoming Drop (Should Not Claim)",
					ClaimID:         "claim-up-1",
					IsClaimed:       false,
					RequiredMinutes: 60,
				},
			},
		},
	}

	count, errs := SweepUnclaimed(context.Background(), client, 12345, campaigns, now, nil)
	require.Empty(t, errs)
	assert.Equal(t, 2, count)

	// Verify drops in campaigns slice were mutated in place
	assert.True(t, campaigns[0].Drops[0].IsClaimed)
	assert.True(t, campaigns[0].Drops[1].IsClaimed)
	assert.True(t, campaigns[1].Drops[0].IsClaimed)
	assert.True(t, campaigns[1].Drops[1].IsClaimed)
	assert.False(t, campaigns[2].Drops[0].IsClaimed, "Upcoming campaign drop must remain unclaimed")

	t.Run("options filter excluded games, drops and unclaimable cache", func(t *testing.T) {
		camps := []DropsCampaign{
			{
				ID:       "camp-ex-game",
				Name:     "Excluded Game Camp",
				Game:     model.NewGame("g-ex", "Excluded Game", "ex-game"),
				StartsAt: now.Add(-2 * time.Hour),
				EndsAt:   now.Add(2 * time.Hour),
				Valid:    true,
				Linked:   true,
				Drops: []TimedDrop{
					{ID: "d-ex-1", Name: "Drop 1", ClaimID: "c1", RequiredMinutes: 10, CurrentMinutes: 10},
				},
			},
			{
				ID:       "camp-ex-drop",
				Name:     "Allowed Game",
				Game:     model.NewGame("g-ok", "Allowed Game", "ok-game"),
				StartsAt: now.Add(-2 * time.Hour),
				EndsAt:   now.Add(2 * time.Hour),
				Valid:    true,
				Linked:   true,
				Drops: []TimedDrop{
					{ID: "d-ex-drop", Name: "Excluded Cosmetic Item", ClaimID: "c2", RequiredMinutes: 10, CurrentMinutes: 10},
					{ID: "d-cached", Name: "Cached Unclaimable", ClaimID: "c3", RequiredMinutes: 10, CurrentMinutes: 10},
					{ID: "d-ok", Name: "Eligible Reward", ClaimID: "c4", RequiredMinutes: 10, CurrentMinutes: 10},
				},
			},
		}

		cache := map[string]time.Time{
			"d-cached": now.Add(30 * time.Minute),
		}

		c, e := SweepUnclaimed(
			context.Background(), client, 12345, camps, now, nil,
			WithSweepExclude([]string{"Excluded Game"}),
			WithSweepDropExclude([]string{"Cosmetic"}),
			WithSweepUnclaimableCache(cache),
		)
		require.Empty(t, e)
		assert.Equal(t, 1, c)
		assert.False(t, camps[0].Drops[0].IsClaimed, "Excluded game drop must not be claimed")
		assert.False(t, camps[1].Drops[0].IsClaimed, "Excluded keyword drop must not be claimed")
		assert.False(t, camps[1].Drops[1].IsClaimed, "Cached unclaimable drop must not be claimed")
		assert.True(t, camps[1].Drops[2].IsClaimed, "Eligible drop must be claimed")
	})
}
