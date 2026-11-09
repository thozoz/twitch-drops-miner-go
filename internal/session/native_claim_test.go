package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestNativeClaimAdvancesImmediatelyDespitePublisherMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"claimDropRewards":{"status":"ELIGIBLE_FOR_ALL","isUserAccountConnected":false}}}`)
	}))
	defer server.Close()
	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)
	client := gql.NewClient(reg, nil, nil, resty.New().SetBaseURL(server.URL))
	s := NewWatchSession(client, nil, nil, 1, nil, WithConfirmDelay(0), WithConfirmRetries(0))
	now := time.Now()
	game := model.NewGame("game", "Game", "")
	c := inventory.DropsCampaign{ID: "campaign", Linked: true, Valid: true, Game: game, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Drops: []inventory.TimedDrop{
		{ID: "first", Name: "Clover", RequiredMinutes: 30, CurrentMinutes: 30, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Benefits: []inventory.Benefit{{Type: inventory.BenefitBadge}}},
		{ID: "second", Name: "Pearl", RequiredMinutes: 60, PreconditionDropIDs: []string{"first"}, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Benefits: []inventory.Benefit{{Type: inventory.BenefitBadge}}},
	}}
	s.activeDrop = &c.Drops[0]
	require.NoError(t, s.claimAndAdvance(context.Background(), &c, model.Channel{Game: &game}))
	require.True(t, c.Drops[0].IsClaimed)
	require.Equal(t, "second", s.ActiveDrop().ID, "must advance immediately instead of waiting watchdog10min")
}
