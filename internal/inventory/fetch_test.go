package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tdm/internal/gql"
)

func TestFetchInventory_OfflineFixtures(t *testing.T) {
	invFixture, err := os.ReadFile("../../testdata/fixtures/gql_inventory.json")
	require.NoError(t, err)
	dashFixture, err := os.ReadFile("../../testdata/fixtures/gql_dashboard_campaigns.json")
	require.NoError(t, err)
	detailFixture, err := os.ReadFile("../../testdata/fixtures/gql_campaign_details.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)

		// Check if batch array or single object
		var singleOp struct {
			OperationName string `json:"operationName"`
		}
		if err := json.Unmarshal(raw, &singleOp); err == nil && singleOp.OperationName != "" {
			switch singleOp.OperationName {
			case "Inventory":
				_, _ = w.Write(invFixture)
				return
			case "ViewerDropsDashboard":
				_, _ = w.Write(dashFixture)
				return
			case "DropCampaignDetails":
				_, _ = w.Write(detailFixture)
				return
			}
		}

		// Check if batch array
		var batchOps []struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &batchOps); err == nil && len(batchOps) > 0 {
			var batchResps []json.RawMessage
			for _, op := range batchOps {
				if op.Variables != nil && op.Variables["dropID"] == "camp-dash-2" {
					// Return unlinked campaign detail
					unlinkedDetail := []byte(`{
						"data": {
							"user": {
								"dropCampaign": {
									"id": "camp-dash-2",
									"name": "Dashboard Campaign 2 (Unlinked)",
									"status": "ACTIVE",
									"startAt": "2026-08-19T00:00:00Z",
									"endAt": "2026-08-20T00:00:00Z",
									"accountLinkURL": "https://example.com/link-2",
									"game": {
										"id": "102",
										"displayName": "Game Two",
										"slug": "game-two"
									},
									"self": {
										"isAccountConnected": false
									},
									"allow": {
										"isEnabled": true,
										"channels": []
									},
									"timeBasedDrops": []
								}
							}
						}
					}`)
					batchResps = append(batchResps, unlinkedDetail)
				} else {
					batchResps = append(batchResps, detailFixture)
				}
			}
			respBytes, _ := json.Marshal(batchResps)
			_, _ = w.Write(respBytes)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))
	fetcher := NewFetcher(gqlClient)

	campaigns, err := fetcher.FetchInventory(context.Background(), 123456)
	require.NoError(t, err)
	require.NotEmpty(t, campaigns)

	// Verify that expired campaign was excluded
	for _, c := range campaigns {
		assert.NotEqual(t, "camp-expired", c.ID, "EXPIRED campaign should be filtered out")
	}

	eligible, unlinked := SplitEligible(campaigns)
	assert.NotEmpty(t, eligible)
	require.NotEmpty(t, unlinked, "Unlinked campaign should be placed into unlinked slice")
	assert.Equal(t, "camp-dash-2", unlinked[0].ID)
	assert.False(t, unlinked[0].Linked)
	assert.False(t, unlinked[0].Eligible())
	assert.Equal(t, "https://example.com/link-2", unlinked[0].LinkURL)

	// Verify in-progress campaign details
	var inProg *DropsCampaign
	for i := range eligible {
		if eligible[i].ID == "camp-prog-1" {
			inProg = &eligible[i]
			break
		}
	}
	require.NotNil(t, inProg, "InProgress Campaign should be found in eligible list")
	assert.Equal(t, "InProgress Campaign", inProg.Name)
	assert.Equal(t, "101", inProg.Game.ID)
	assert.Equal(t, "game-one", inProg.Game.Slug())
	assert.True(t, inProg.Linked)
	require.Len(t, inProg.Drops, 2)
	assert.True(t, inProg.Drops[0].IsClaimed)
	assert.Equal(t, 1.0, inProg.Drops[0].Progress())
	assert.False(t, inProg.Drops[1].IsClaimed)
	assert.Equal(t, 45, inProg.Drops[1].CurrentMinutes)
	assert.Equal(t, 120, inProg.Drops[1].RequiredMinutes)

	// Verify ACL campaign from dashboard details
	var aclCamp *DropsCampaign
	for i := range eligible {
		if eligible[i].ID == "camp-dash-3" {
			aclCamp = &eligible[i]
			break
		}
	}
	if assert.NotNil(t, aclCamp, "ACL campaign should be found in eligible list") {
		assert.Len(t, aclCamp.AllowedChannels, 1)
		assert.Equal(t, "proplayer", aclCamp.AllowedChannels[0].Login)
		assert.True(t, aclCamp.AllowedChannels[0].ACLBased)
	}
}

func TestFetch_ZeroSha256InSource(t *testing.T) {
	content, err := os.ReadFile("fetch.go")
	require.NoError(t, err)
	assert.NotContains(t, string(content), "sha256Hash", "fetch.go must not contain raw sha256Hash literals")
}
