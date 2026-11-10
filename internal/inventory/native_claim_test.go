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

func TestClaim_NativeRewardDoesNotRequirePublisherConnection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		types  []BenefitType
		status string
		want   bool
	}{
		{"badge", []BenefitType{BenefitBadge}, "ELIGIBLE_FOR_ALL", true},
		{"emote", []BenefitType{BenefitEmote}, "ELIGIBLE_FOR_ALL", true},
		{"both native", []BenefitType{BenefitBadge, BenefitEmote}, "ELIGIBLE_FOR_ALL", true},
		{"game reward", []BenefitType{BenefitDirectEntitlement}, "ELIGIBLE_FOR_ALL", false},
		{"mixed reward", []BenefitType{BenefitBadge, BenefitDirectEntitlement}, "ELIGIBLE_FOR_ALL", false},
		{"unknown", []BenefitType{BenefitUnknown}, "ELIGIBLE_FOR_ALL", false},
		{"empty", nil, "ELIGIBLE_FOR_ALL", false},
		{"already claimed", []BenefitType{BenefitDirectEntitlement}, "DROP_INSTANCE_ALREADY_CLAIMED", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"data":{"claimDropRewards":{"status":"`+tc.status+`","isUserAccountConnected":false}}}`)
			}))
			defer srv.Close()
			reg, _, err := gql.LoadRegistry("")
			require.NoError(t, err)
			client := gql.NewClient(reg, nil, nil, resty.New().SetBaseURL(srv.URL))
			d := TimedDrop{ID: "native", ClaimID: "fake", CurrentMinutes: 30, RequiredMinutes: 30}
			for _, typ := range tc.types {
				d.Benefits = append(d.Benefits, Benefit{Type: typ})
			}
			// Deliberately mixed campaign: linkage is decided for the actual claimed drop.
			c := DropsCampaign{ID: "campaign", Drops: []TimedDrop{{Benefits: []Benefit{{Type: BenefitDirectEntitlement}}}}}
			claimed, err := ClaimDrop(context.Background(), client, c, &d, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if tc.want {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrAccountNotLinked)
			}
			require.Equal(t, tc.want, claimed)
			require.Equal(t, tc.want, d.IsClaimed)
		})
	}
}
