package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tdm/internal/gql"
	"tdm/internal/logging"
	"tdm/internal/model"
)

// Fetcher coordinates fetching and assembling Twitch drops inventory.
type Fetcher struct {
	Client *gql.Client
}

// NewFetcher constructs a new Fetcher using the provided GQL client.
func NewFetcher(client *gql.Client) *Fetcher {
	return &Fetcher{Client: client}
}

// SplitEligible divides a campaign slice into eligible and unlinked campaigns.
func SplitEligible(campaigns []DropsCampaign) (eligible, unlinked []DropsCampaign) {
	for _, c := range campaigns {
		if c.Eligible() {
			eligible = append(eligible, c)
		} else {
			unlinked = append(unlinked, c)
		}
	}
	return eligible, unlinked
}

// FetchInventory retrieves the operator's in-progress and available drops campaigns
// by merging Inventory, ViewerDropsDashboard, and DropCampaignDetails GQL operations.
func (f *Fetcher) FetchInventory(ctx context.Context, userID int) ([]DropsCampaign, error) {
	logger := logging.FromContext(ctx)

	// Step 1: Query Inventory for in-progress campaigns and claimed benefits
	invRaw, err := f.Client.Do(ctx, "Inventory", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Inventory: %w", err)
	}

	var invResp struct {
		CurrentUser struct {
			Inventory struct {
				DropCampaignsInProgress []json.RawMessage `json:"dropCampaignsInProgress"`
				GameEventDrops          []struct {
					ID            string `json:"id"`
					LastAwardedAt string `json:"lastAwardedAt"`
				} `json:"gameEventDrops"`
			} `json:"inventory"`
		} `json:"currentUser"`
	}

	if err := json.Unmarshal(invRaw, &invResp); err != nil {
		return nil, fmt.Errorf("failed to parse Inventory response: %w", err)
	}

	claimedBenefits := make(map[string]time.Time)
	for _, b := range invResp.CurrentUser.Inventory.GameEventDrops {
		if t, err := time.Parse(time.RFC3339, b.LastAwardedAt); err == nil {
			claimedBenefits[b.ID] = t
		}
	}

	// Map raw campaign JSON by campaign ID. In-progress detailed entries take priority.
	campaignRawMap := make(map[string]json.RawMessage)
	for _, raw := range invResp.CurrentUser.Inventory.DropCampaignsInProgress {
		var idHolder struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &idHolder); err == nil && idHolder.ID != "" {
			campaignRawMap[idHolder.ID] = raw
		}
	}

	// Step 2: Query ViewerDropsDashboard for general available campaigns
	dashRaw, err := f.Client.Do(ctx, "ViewerDropsDashboard", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ViewerDropsDashboard: %w", err)
	}

	var dashResp struct {
		CurrentUser struct {
			DropCampaigns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"dropCampaigns"`
		} `json:"currentUser"`
	}

	if err := json.Unmarshal(dashRaw, &dashResp); err != nil {
		return nil, fmt.Errorf("failed to parse ViewerDropsDashboard response: %w", err)
	}

	// Step 3: Find dashboard campaigns not in inventory that need details
	var detailOps []gql.BatchOp
	userIDStr := strconv.Itoa(userID)

	for _, c := range dashResp.CurrentUser.DropCampaigns {
		if c.Status != "ACTIVE" && c.Status != "UPCOMING" {
			continue
		}
		if _, exists := campaignRawMap[c.ID]; !exists {
			detailOps = append(detailOps, gql.BatchOp{
				Name: "DropCampaignDetails",
				Variables: map[string]any{
					"channelLogin": userIDStr,
					"dropID":       c.ID,
				},
			})
		}
	}

	if len(detailOps) > 0 {
		detailResults, err := f.Client.DoBatch(ctx, detailOps)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch DropCampaignDetails batch: %w", err)
		}

		for _, itemRaw := range detailResults {
			var itemResp struct {
				User struct {
					DropCampaign json.RawMessage `json:"dropCampaign"`
				} `json:"user"`
			}
			if err := json.Unmarshal(itemRaw, &itemResp); err != nil {
				logger.Warn("failed to parse campaign detail item", "err", err)
				continue
			}
			if len(itemResp.User.DropCampaign) > 0 && string(itemResp.User.DropCampaign) != "null" {
				var idHolder struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(itemResp.User.DropCampaign, &idHolder); err == nil && idHolder.ID != "" {
					campaignRawMap[idHolder.ID] = itemResp.User.DropCampaign
				}
			}
		}
	}

	// Step 4: Parse all assembled campaigns into domain DropsCampaign objects
	var campaigns []DropsCampaign
	for _, raw := range campaignRawMap {
		var rc rawCampaign
		if err := json.Unmarshal(raw, &rc); err != nil {
			logger.Warn("skipping campaign with malformed json", "err", err)
			continue
		}

		// Filter out campaigns missing game data
		if rc.Game == nil {
			continue
		}

		c, ok := parseRawCampaign(rc, claimedBenefits, logger)
		if ok {
			campaigns = append(campaigns, c)
		}
	}

	return campaigns, nil
}

type rawCampaign struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Status         string        `json:"status"`
	StartAt        string        `json:"startAt"`
	EndAt          string        `json:"endAt"`
	AccountLinkURL string        `json:"accountLinkURL"`
	Game           *rawGame      `json:"game"`
	Self           *rawSelf      `json:"self"`
	Allow          *rawAllow     `json:"allow"`
	TimeBasedDrops []rawTimeDrop `json:"timeBasedDrops"`
}

type rawGame struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
}

type rawSelf struct {
	IsAccountConnected bool `json:"isAccountConnected"`
}

type rawAllow struct {
	IsEnabled *bool        `json:"isEnabled"`
	Channels  []rawChannel `json:"channels"`
}

type rawChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type rawTimeDrop struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	StartAt                string            `json:"startAt"`
	EndAt                  string            `json:"endAt"`
	RequiredMinutesWatched int               `json:"requiredMinutesWatched"`
	BenefitEdges           []rawBenefitEdge  `json:"benefitEdges"`
	PreconditionDrops      []rawPrecondition `json:"preconditionDrops"`
	Self                   *rawDropSelf      `json:"self"`
}

type rawBenefitEdge struct {
	Benefit rawBenefit `json:"benefit"`
}

type rawBenefit struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	DistributionType string `json:"distributionType"`
	ImageAssetURL    string `json:"imageAssetURL"`
}

type rawPrecondition struct {
	ID string `json:"id"`
}

type rawDropSelf struct {
	DropInstanceID        string `json:"dropInstanceID"`
	IsClaimed             bool   `json:"isClaimed"`
	CurrentMinutesWatched int    `json:"currentMinutesWatched"`
}

func parseRawCampaign(rc rawCampaign, claimedBenefits map[string]time.Time, logger interface{ Warn(string, ...any) }) (DropsCampaign, bool) {
	cStart, err := time.Parse(time.RFC3339, rc.StartAt)
	if err != nil {
		logger.Warn("skipping campaign with invalid start timestamp", "campaign_id", rc.ID, "err", err)
		return DropsCampaign{}, false
	}
	cEnd, err := time.Parse(time.RFC3339, rc.EndAt)
	if err != nil {
		logger.Warn("skipping campaign with invalid end timestamp", "campaign_id", rc.ID, "err", err)
		return DropsCampaign{}, false
	}

	gameName := rc.Game.DisplayName
	if gameName == "" {
		gameName = rc.Game.Name
	}
	game := model.NewGame(rc.Game.ID, gameName, rc.Game.Slug)

	linked := false
	if rc.Self != nil {
		linked = rc.Self.IsAccountConnected
	}
	// Special Events and Twitch-native campaigns (badges, emotes, twitch.tv links) do not
	// require external 3rd-party game publisher account linking.
	if !linked && (game.IsSpecial() || game.Name == "Special Events" || isTwitchNativeLink(rc.AccountLinkURL)) {
		linked = true
	}

	var allowedChannels []model.Channel
	if rc.Allow != nil && (rc.Allow.IsEnabled == nil || *rc.Allow.IsEnabled) {
		for _, ch := range rc.Allow.Channels {
			allowedChannels = append(allowedChannels, model.Channel{
				ID:          ch.ID,
				Login:       ch.Name,
				DisplayName: ch.DisplayName,
				ACLBased:    true,
			})
		}
	}

	var drops []TimedDrop
	for _, rd := range rc.TimeBasedDrops {
		dStart, err := time.Parse(time.RFC3339, rd.StartAt)
		if err != nil {
			logger.Warn("skipping drop with invalid start timestamp", "drop_id", rd.ID, "err", err)
			continue
		}
		dEnd, err := time.Parse(time.RFC3339, rd.EndAt)
		if err != nil {
			logger.Warn("skipping drop with invalid end timestamp", "drop_id", rd.ID, "err", err)
			continue
		}

		var benefits []Benefit
		for _, be := range rd.BenefitEdges {
			bType := BenefitType(be.Benefit.DistributionType)
			switch bType {
			case BenefitBadge, BenefitEmote, BenefitDirectEntitlement:
			default:
				bType = BenefitUnknown
			}
			benefits = append(benefits, Benefit{
				ID:       be.Benefit.ID,
				Name:     be.Benefit.Name,
				ImageURL: be.Benefit.ImageAssetURL,
				Type:     bType,
			})
		}

		var preconditions []string
		for _, p := range rd.PreconditionDrops {
			if p.ID != "" {
				preconditions = append(preconditions, p.ID)
			}
		}

		var claimID string
		var isClaimed bool
		var currentMinutes int

		if rd.Self != nil {
			claimID = rd.Self.DropInstanceID
			isClaimed = rd.Self.IsClaimed
			currentMinutes = rd.Self.CurrentMinutesWatched
			if isClaimed {
				currentMinutes = rd.RequiredMinutesWatched
			}
		} else {
			isClaimed = CheckClaimedFromBenefits(benefits, dStart, dEnd, claimedBenefits)
		}

		drops = append(drops, TimedDrop{
			ID:                  rd.ID,
			Name:                rd.Name,
			Benefits:            benefits,
			StartsAt:            dStart,
			EndsAt:              dEnd,
			ClaimID:             claimID,
			IsClaimed:           isClaimed,
			PreconditionDropIDs: preconditions,
			RequiredMinutes:     rd.RequiredMinutesWatched,
			CurrentMinutes:      currentMinutes,
		})
	}

	return DropsCampaign{
		ID:              rc.ID,
		Name:            rc.Name,
		Game:            game,
		Linked:          linked,
		LinkURL:         rc.AccountLinkURL,
		StartsAt:        cStart,
		EndsAt:          cEnd,
		Valid:           rc.Status != "EXPIRED",
		AllowedChannels: allowedChannels,
		Drops:           drops,
	}, true
}

func isTwitchNativeLink(rawURL string) bool {
	if rawURL == "" {
		return true
	}
	return strings.Contains(rawURL, "twitch.tv")
}
