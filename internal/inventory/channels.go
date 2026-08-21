package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

// ResolveCandidates discovers live streaming channels eligible for a campaign.
// For ACL-restricted campaigns, it queries VideoPlayerStreamInfoOverlayChannel in batch for allowed channels.
// For open-directory campaigns, it queries DirectoryPage_Game with the DROPS_ENABLED filter.
func ResolveCandidates(ctx context.Context, client *gql.Client, campaign DropsCampaign) ([]model.Channel, error) {
	logger := logging.FromContext(ctx)

	// Path 1: ACL-restricted campaign
	if len(campaign.AllowedChannels) > 0 {
		ops := make([]gql.BatchOp, 0, len(campaign.AllowedChannels))
		for _, ch := range campaign.AllowedChannels {
			ops = append(ops, gql.BatchOp{
				Name: "VideoPlayerStreamInfoOverlayChannel",
				Variables: map[string]any{
					"channel": ch.Login,
				},
			})
		}

		results, err := client.DoBatch(ctx, ops)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch overlay stream info for ACL channels: %w", err)
		}

		candidates := make([]model.Channel, 0)
		for _, raw := range results {
			var resp struct {
				User *struct {
					ID          string `json:"id"`
					Login       string `json:"login"`
					DisplayName string `json:"displayName"`
					Stream      *struct {
						ID           string `json:"id"`
						ViewersCount int    `json:"viewersCount"`
					} `json:"stream"`
					BroadcastSettings *struct {
						Title string `json:"title"`
						Game  *struct {
							ID          string `json:"id"`
							DisplayName string `json:"displayName"`
							Slug        string `json:"slug"`
						} `json:"game"`
					} `json:"broadcastSettings"`
				} `json:"user"`
			}

			if err := json.Unmarshal(raw, &resp); err != nil {
				logger.Warn("failed to parse stream overlay response", "err", err)
				continue
			}

			if resp.User == nil || resp.User.Stream == nil {
				// Channel does not exist or is offline
				continue
			}

			var game *model.Game
			if resp.User.BroadcastSettings != nil && resp.User.BroadcastSettings.Game != nil {
				g := resp.User.BroadcastSettings.Game
				gEntity := model.NewGame(g.ID, g.DisplayName, g.Slug)
				game = &gEntity
			}

			candidates = append(candidates, model.Channel{
				ID:           resp.User.ID,
				Login:        resp.User.Login,
				DisplayName:  resp.User.DisplayName,
				ACLBased:     true,
				Online:       true,
				Game:         game,
				Viewers:      resp.User.Stream.ViewersCount,
				DropsEnabled: true,
				BroadcastID:  resp.User.Stream.ID,
			})
		}

		// Sort ACL channels by viewers descending (highest-viewer channel first)
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].Viewers > candidates[j].Viewers
		})

		return candidates, nil
	}

	// Path 2: Open directory campaign
	dataRaw, err := client.Do(ctx, "DirectoryPage_Game", map[string]any{
		"slug": campaign.Game.Slug(),
		"options": map[string]any{
			"systemFilters": []string{"DROPS_ENABLED"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch game directory: %w", err)
	}

	var dirResp struct {
		Game *struct {
			Streams *struct {
				Edges []struct {
					Node struct {
						ID           string `json:"id"`
						Title        string `json:"title"`
						ViewersCount int    `json:"viewersCount"`
						Game         *struct {
							ID          string `json:"id"`
							DisplayName string `json:"displayName"`
							Slug        string `json:"slug"`
						} `json:"game"`
						Broadcaster *struct {
							ID          string `json:"id"`
							Login       string `json:"login"`
							DisplayName string `json:"displayName"`
						} `json:"broadcaster"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"streams"`
		} `json:"game"`
	}

	if err := json.Unmarshal(dataRaw, &dirResp); err != nil {
		return nil, fmt.Errorf("failed to parse directory response: %w", err)
	}

	candidates := make([]model.Channel, 0)
	if dirResp.Game == nil || dirResp.Game.Streams == nil {
		return candidates, nil
	}

	for _, edge := range dirResp.Game.Streams.Edges {
		if edge.Node.Broadcaster == nil {
			// Skip hosted or removed streams
			continue
		}

		var game *model.Game
		if edge.Node.Game != nil {
			gEntity := model.NewGame(edge.Node.Game.ID, edge.Node.Game.DisplayName, edge.Node.Game.Slug)
			game = &gEntity
		}

		candidates = append(candidates, model.Channel{
			ID:           edge.Node.Broadcaster.ID,
			Login:        edge.Node.Broadcaster.Login,
			DisplayName:  edge.Node.Broadcaster.DisplayName,
			ACLBased:     false,
			Online:       true,
			Game:         game,
			Viewers:      edge.Node.ViewersCount,
			DropsEnabled: true,
			BroadcastID:  edge.Node.ID,
		})
	}

	return candidates, nil
}

// ResolveChannel returns the primary live channel candidate for a campaign that can earn drops right now, or nil if none is live/eligible.
func ResolveChannel(ctx context.Context, client *gql.Client, campaign DropsCampaign) (*model.Channel, error) {
	candidates, err := ResolveCandidates(ctx, client, campaign)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range candidates {
		if campaign.CanEarn(now, &candidates[i]) {
			res := candidates[i]
			return &res, nil
		}
	}
	return nil, nil
}
