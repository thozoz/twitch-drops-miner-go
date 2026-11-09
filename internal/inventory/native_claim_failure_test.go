package inventory

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
)

func TestClaim_NativeBenefitsDoNotOverrideFailureStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"claimDropRewards":{"status":"DROP_INSTANCE_NOT_ELIGIBLE","isUserAccountConnected":false}}}`)
	}))
	defer server.Close()
	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)
	client := gql.NewClient(reg, nil, nil, resty.New().SetBaseURL(server.URL))
	drop := TimedDrop{ID: "badge", ClaimID: "fake", Benefits: []Benefit{{Type: BenefitBadge}}}
	claimed, err := ClaimDrop(context.Background(), client, DropsCampaign{}, &drop, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	require.False(t, claimed)
	require.False(t, drop.IsClaimed)
}
