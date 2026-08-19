package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
)

// GenerateClaimID constructs a deterministic drop claim ID in the format UserID#CampaignID#DropID.
func GenerateClaimID(userID int, campaignID, dropID string) string {
	return fmt.Sprintf("%d#%s#%s", userID, campaignID, dropID)
}

// CanClaim reports whether a drop is eligible to be claimed.
// A drop can be claimed if it has a non-empty ClaimID, is not already claimed,
// and the current time is before the campaign end time plus a 24-hour grace period.
func CanClaim(d TimedDrop, campaignEndsAt, now time.Time) bool {
	return d.ClaimID != "" && !d.IsClaimed && now.Before(campaignEndsAt.Add(24*time.Hour))
}

// ClaimDrop attempts to claim a drop reward via the DropsPage_ClaimDropRewards GQL mutation.
// If the drop is already claimed, it returns true immediately without issuing a GQL request.
// If the claim ID is missing, one is generated from the user ID, campaign ID, and drop ID.
// Both ELIGIBLE_FOR_ALL and DROP_INSTANCE_ALREADY_CLAIMED statuses from Twitch are treated as success.
func ClaimDrop(ctx context.Context, client *gql.Client, campaign DropsCampaign, drop *TimedDrop, userID int, logger *slog.Logger) (claimed bool, err error) {
	if drop == nil {
		return false, nil
	}

	if drop.IsClaimed {
		return true, nil
	}

	claimID := drop.ClaimID
	if claimID == "" {
		claimID = GenerateClaimID(userID, campaign.ID, drop.ID)
	}

	data, err := client.Do(ctx, "DropsPage_ClaimDropRewards", map[string]any{
		"input": map[string]any{
			"dropInstanceID": claimID,
		},
	})
	if err != nil {
		return false, err
	}

	var resp struct {
		ClaimDropRewards *struct {
			Status string `json:"status"`
		} `json:"claimDropRewards"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return false, fmt.Errorf("failed to parse DropsPage_ClaimDropRewards response: %w", err)
	}

	if resp.ClaimDropRewards == nil {
		return false, nil
	}

	status := resp.ClaimDropRewards.Status
	if status != "ELIGIBLE_FOR_ALL" && status != "DROP_INSTANCE_ALREADY_CLAIMED" {
		return false, nil
	}

	drop.IsClaimed = true
	drop.ClaimID = claimID
	drop.CurrentMinutes = drop.RequiredMinutes

	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("drop claimed successfully",
		slog.String("campaign", campaign.Name),
		slog.String("drop", drop.Name),
		slog.Time("claimed_at", time.Now().UTC()),
	)

	return true, nil
}

// SweepUnclaimed iterates through all non-upcoming campaigns and attempts to claim any drops
// satisfying CanClaim. Individual errors are collected into errs without aborting the sweep.
func SweepUnclaimed(ctx context.Context, client *gql.Client, userID int, campaigns []DropsCampaign, now time.Time, logger *slog.Logger) (claimedCount int, errs []error) {
	for i := range campaigns {
		if campaigns[i].Upcoming(now) {
			continue
		}

		for j := range campaigns[i].Drops {
			d := &campaigns[i].Drops[j]
			if CanClaim(*d, campaigns[i].EndsAt, now) {
				claimed, err := ClaimDrop(ctx, client, campaigns[i], d, userID, logger)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if claimed {
					claimedCount++
				}
			}
		}
	}

	return claimedCount, errs
}
