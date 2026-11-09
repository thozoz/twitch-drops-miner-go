package inventory

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseCampaign_LinkEvidence(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, url, gameID string
		connected         bool
		missingSelf       bool
		types             []BenefitType
		want              bool
	}{
		{name: "FELLOWSHIP live shape", url: "https://www.twitch.tv/drops/campaigns", types: []BenefitType{BenefitDirectEntitlement}},
		{name: "missing link is not linkage", types: []BenefitType{BenefitDirectEntitlement}},
		{name: "missing self is not linkage", missingSelf: true, url: "https://www.twitch.tv/drops/campaigns", types: []BenefitType{BenefitDirectEntitlement}},
		{name: "external link unconnected", url: "https://publisher.example/link", types: []BenefitType{BenefitDirectEntitlement}},
		{name: "connected game", connected: true, types: []BenefitType{BenefitDirectEntitlement}, want: true},
		{name: "native badge", types: []BenefitType{BenefitBadge}, want: true},
		{name: "native emote", types: []BenefitType{BenefitEmote}, want: true},
		{name: "mixed native and game reward", url: "https://www.twitch.tv/drops/campaigns", types: []BenefitType{BenefitBadge, BenefitDirectEntitlement}},
		{name: "unknown reward", url: "https://www.twitch.tv/", types: []BenefitType{BenefitUnknown}},
		{name: "empty rewards", url: "https://www.twitch.tv/"},
		{name: "special event", gameID: "509663", types: []BenefitType{BenefitDirectEntitlement}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gameID := tc.gameID
			if gameID == "" {
				gameID = "177579460"
			}
			rc := rawCampaign{ID: "campaign", Name: "Campaign", Status: "ACTIVE", StartAt: now.Add(-time.Hour).Format(time.RFC3339), EndAt: now.Add(time.Hour).Format(time.RFC3339), AccountLinkURL: tc.url, Game: &rawGame{ID: gameID, DisplayName: "Game"}, Self: &rawSelf{IsAccountConnected: tc.connected}}
			if tc.missingSelf {
				rc.Self = nil
			}
			rd := rawTimeDrop{ID: "drop", Name: "Drop", StartAt: rc.StartAt, EndAt: rc.EndAt, RequiredMinutesWatched: 30}
			for _, typ := range tc.types {
				rd.BenefitEdges = append(rd.BenefitEdges, rawBenefitEdge{Benefit: rawBenefit{ID: string(typ), DistributionType: string(typ)}})
			}
			rc.TimeBasedDrops = []rawTimeDrop{rd}
			c, ok := parseRawCampaign(rc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.True(t, ok)
			require.Equal(t, tc.want, c.Linked)
			if !tc.want {
				require.False(t, c.Eligible(true))
				require.False(t, c.CanEarn(now, nil))
				eligible, skipped := SplitEligible([]DropsCampaign{c}, true)
				require.Empty(t, eligible)
				require.Len(t, skipped, 1)
				require.Equal(t, SkipReasonUnlinked, skipped[0].Reason)
			}
		})
	}
}
