package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thozoz/twitch-drops-miner-go/internal/auth"
	"github.com/thozoz/twitch-drops-miner-go/internal/config"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/logging"
)

var queueJSON bool

// QueueEntry describes one drop's place in the execution queue: which
// campaign it belongs to, how far along it is, and whether anything is
// currently stopping it from being earned.
type QueueEntry struct {
	Game             string    `json:"game"`
	Campaign         string    `json:"campaign"`
	Drop             string    `json:"drop"`
	CurrentMinutes   int       `json:"current_minutes"`
	RequiredMinutes  int       `json:"required_minutes"`
	RemainingMinutes int       `json:"remaining_minutes"`
	Status           string    `json:"status"`
	CampaignEndsAt   time.Time `json:"campaign_ends_at"`
}

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Show the wanted-drops execution queue",
	Long: "Fetch inventory and show every drop tdm would work through, in the order it\n" +
		"would mine them: ranked by game priority, with excluded games and\n" +
		"excluded drop-name/benefit keywords already applied.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := logging.FromContext(ctx)

		authPath, err := config.AuthFilePath()
		if err != nil {
			logger.Error("failed to resolve auth file path", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		httpClient := newHTTPClient()
		authSession, err := auth.LoadOrEmpty(authPath, httpClient)
		if err != nil {
			logger.Error("failed to load session", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		if !authSession.Authenticated() {
			fmt.Println("not authenticated, run 'tdm auth login'")
			return &CommandError{Code: ExitError, Err: errors.New("not authenticated")}
		}

		overridePath, _ := config.OperationsOverridePath()
		registry, replaced, err := gql.LoadRegistry(overridePath)
		if err != nil {
			logger.Error("failed to load GQL operations registry", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}
		if len(replaced) > 0 {
			logger.Warn("overriding GQL operations from config", "replaced", replaced)
		}

		client := gql.NewClient(registry, authSession, authSession, httpClient)
		fetcher := inventory.NewFetcher(client)

		campaigns, err := fetcher.FetchInventory(ctx, authSession.Data().UserID)
		if err != nil {
			if errors.Is(err, auth.ErrReauthRequired) {
				logger.Error("credentials invalid or expired: run 'tdm auth login'")
				return &CommandError{Code: ExitAuthRequired, Err: err}
			}
			logger.Error("failed to fetch inventory", "error", err)
			return &CommandError{Code: ExitError, Err: err}
		}

		cfg := config.FromContext(ctx)
		var priority, exclude, dropExclude []string
		enableBadgesEmotes := false
		if cfg != nil {
			priority = cfg.Priority
			exclude = cfg.Exclude
			dropExclude = cfg.DropExclude
			enableBadgesEmotes = cfg.EnableBadgesEmotes
		}

		entries := BuildQueue(campaigns, priority, exclude, dropExclude, enableBadgesEmotes, time.Now())

		if queueJSON {
			data, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				logger.Error("failed to marshal queue to JSON", "error", err)
				return &CommandError{Code: ExitError, Err: err}
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Fprint(cmd.OutOrStdout(), FormatQueue(entries))
		return nil
	},
}

// BuildQueue derives the ranked execution queue from a raw campaign list: it
// drops unlinked/badge-emote-gated campaigns per enableBadgesEmotes, drops
// campaigns for games in exclude, keeps only campaigns currently Active, ranks
// the rest with the same priority/EndsAt ordering SelectCampaign uses, and
// flattens each campaign's drops in slice order. dropExclude does not remove
// entries from the queue — an excluded drop, and anything it blocks, still
// appears so the operator can see why it is stuck (see PreconditionStatus).
func BuildQueue(campaigns []inventory.DropsCampaign, priority, exclude, dropExclude []string, enableBadgesEmotes bool, now time.Time) []QueueEntry {
	eligible, _ := inventory.SplitEligible(campaigns, enableBadgesEmotes)

	excludeMap := make(map[string]struct{}, len(exclude))
	for _, ex := range exclude {
		excludeMap[ex] = struct{}{}
	}

	var active []inventory.DropsCampaign
	for _, c := range eligible {
		if _, excluded := excludeMap[c.Game.Name]; excluded {
			continue
		}
		if !c.Active(now) {
			continue
		}
		active = append(active, c)
	}

	ranked := inventory.SortByPriority(active, priority)

	var entries []QueueEntry
	for _, c := range ranked {
		for _, d := range c.Drops {
			entries = append(entries, QueueEntry{
				Game:             c.Game.Name,
				Campaign:         c.Name,
				Drop:             d.Name,
				CurrentMinutes:   d.CurrentMinutes,
				RequiredMinutes:  d.RequiredMinutes,
				RemainingMinutes: d.RemainingMinutes(),
				Status:           c.PreconditionStatus(d, dropExclude),
				CampaignEndsAt:   c.EndsAt,
			})
		}
	}
	return entries
}

// FormatQueue renders the queue for terminal output, one numbered block per drop.
func FormatQueue(entries []QueueEntry) string {
	if len(entries) == 0 {
		return "(queue is empty)\n"
	}

	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. [%s] %s — %s\n", i+1, e.Game, e.Campaign, e.Drop)
		fmt.Fprintf(&b, "   Progress: %d/%d min (%d min remaining)\n", e.CurrentMinutes, e.RequiredMinutes, e.RemainingMinutes)
		fmt.Fprintf(&b, "   Status: %s\n", e.Status)
		fmt.Fprintf(&b, "   Campaign ends: %s\n", e.CampaignEndsAt.Format(time.RFC3339))
	}
	return b.String()
}

func init() {
	queueCmd.Flags().BoolVar(&queueJSON, "json", false, "Output the queue in JSON format")
	rootCmd.AddCommand(queueCmd)
}
