package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
)

// FetchCurrentDropProgress queries Twitch GQL for the currently watched drop progress on a given channel.
// If there is no active drop session (DropCurrentSession is null), it returns ("", 0, false, nil).
// If a drop session is active, it returns the drop ID, current minutes watched, ok=true, and nil error.
func FetchCurrentDropProgress(ctx context.Context, client *gql.Client, channelID string) (dropID string, currentMinutes int, ok bool, err error) {
	data, err := client.Do(ctx, "DropCurrentSessionContext", map[string]any{"channelID": channelID})
	if err != nil {
		return "", 0, false, err
	}

	var resp struct {
		CurrentUser struct {
			DropCurrentSession *struct {
				DropID                string `json:"dropID"`
				CurrentMinutesWatched int    `json:"currentMinutesWatched"`
			} `json:"dropCurrentSession"`
		} `json:"currentUser"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return "", 0, false, fmt.Errorf("failed to parse DropCurrentSessionContext response: %w", err)
	}

	if resp.CurrentUser.DropCurrentSession == nil {
		return "", 0, false, nil
	}

	return resp.CurrentUser.DropCurrentSession.DropID, resp.CurrentUser.DropCurrentSession.CurrentMinutesWatched, true, nil
}

// ReconcileMinutes reconciles local drop progress with the server-reported minutes.
// Server state always wins. The server minutes are defensively clamped to [0, drop.RequiredMinutes].
// If the clamped value differs from drop.CurrentMinutes, a log line is emitted at Info level,
// drop.CurrentMinutes is updated, and changed=true is returned.
func ReconcileMinutes(drop *TimedDrop, serverMinutes int, logger *slog.Logger) (changed bool) {
	if drop == nil {
		return false
	}

	clamped := serverMinutes
	if clamped < 0 {
		clamped = 0
	}
	if clamped > drop.RequiredMinutes {
		clamped = drop.RequiredMinutes
	}

	if drop.CurrentMinutes == clamped {
		return false
	}

	if logger == nil {
		logger = slog.Default()
	}

	oldMinutes := drop.CurrentMinutes
	logger.Info("drop progress reconciled from server, server state wins",
		slog.String("drop", drop.Name),
		slog.Int("local_minutes", oldMinutes),
		slog.Int("server_minutes", clamped),
	)

	drop.CurrentMinutes = clamped
	return true
}
