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
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
)

func TestResolve_ACLPath(t *testing.T) {
	onlineFixture, err := os.ReadFile("../../testdata/fixtures/gql_stream_info_online.json")
	require.NoError(t, err)
	offlineFixture, err := os.ReadFile("../../testdata/fixtures/gql_stream_info_offline.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)

		var batchOps []struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &batchOps); err == nil && len(batchOps) > 0 {
			var batchResps []json.RawMessage
			for _, op := range batchOps {
				if op.Variables != nil && op.Variables["channel"] == "streamer_online" {
					batchResps = append(batchResps, onlineFixture)
				} else {
					batchResps = append(batchResps, offlineFixture)
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

	campaign := DropsCampaign{
		ID:       "camp-acl",
		Name:     "ACL Campaign",
		Game:     model.NewGame("100", "Test Game", "test-game"),
		Linked:   true,
		Valid:    true,
		StartsAt: time.Now().Add(-1 * time.Hour),
		EndsAt:   time.Now().Add(5 * time.Hour),
		Drops: []TimedDrop{
			{
				ID:              "d-1",
				Name:            "Drop 1",
				StartsAt:        time.Now().Add(-1 * time.Hour),
				EndsAt:          time.Now().Add(5 * time.Hour),
				RequiredMinutes: 60,
				Benefits:        []Benefit{{ID: "b-1", Name: "Benefit", Type: BenefitDirectEntitlement}},
			},
		},
		AllowedChannels: []model.Channel{
			{ID: "chan-online-1", Login: "streamer_online"},
			{ID: "chan-offline-1", Login: "streamer_offline"},
		},
	}

	candidates, err := ResolveCandidates(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "Only the online streamer should be returned as candidate")
	assert.Equal(t, "streamer_online", candidates[0].Login)
	assert.Equal(t, 1250, candidates[0].Viewers)
	assert.True(t, candidates[0].ACLBased)
	assert.True(t, candidates[0].Online)

	primary, err := ResolveChannel(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	require.NotNil(t, primary)
	assert.Equal(t, "streamer_online", primary.Login)
}

func TestResolve_OpenDirectoryPath(t *testing.T) {
	dirFixture, err := os.ReadFile("../../testdata/fixtures/gql_directory_game.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var singleOp struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&singleOp)

		if singleOp.OperationName == "DirectoryPage_Game" {
			_, _ = w.Write(dirFixture)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	httpClient := resty.New().SetHostURL(server.URL)
	gqlClient := gql.NewClient(reg, nil, nil, httpClient, gql.WithMinRetryDelay(1*time.Millisecond))

	campaign := DropsCampaign{
		ID:              "camp-dir",
		Name:            "Directory Campaign",
		Game:            model.NewGame("100", "Test Game", "test-game"),
		Linked:          true,
		Valid:           true,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(5 * time.Hour),
		AllowedChannels: nil, // Open directory
		Drops: []TimedDrop{
			{
				ID:              "d-dir-1",
				Name:            "Dir Drop 1",
				StartsAt:        time.Now().Add(-1 * time.Hour),
				EndsAt:          time.Now().Add(5 * time.Hour),
				RequiredMinutes: 60,
				Benefits:        []Benefit{{ID: "b-dir-1", Name: "Benefit", Type: BenefitDirectEntitlement}},
			},
		},
	}

	candidates, err := ResolveCandidates(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "The null broadcaster edge must be safely ignored")
	assert.Equal(t, "dir_streamer", candidates[0].Login)
	assert.Equal(t, 500, candidates[0].Viewers)
	assert.False(t, candidates[0].ACLBased)
	assert.True(t, candidates[0].Online)

	primary, err := ResolveChannel(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	require.NotNil(t, primary)
	assert.Equal(t, "dir_streamer", primary.Login)
}

func TestResolve_GameMismatchFilteredOut(t *testing.T) {
	onlineFixture, err := os.ReadFile("../../testdata/fixtures/gql_stream_info_online.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)

		var batchOps []struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &batchOps); err == nil && len(batchOps) > 0 {
			var batchResps []json.RawMessage
			for range batchOps {
				batchResps = append(batchResps, onlineFixture)
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

	// Campaign expects game ID 999, but fixture channel is playing game 100 ("Test Game")
	campaign := DropsCampaign{
		ID:       "camp-mismatch",
		Name:     "Mismatch Campaign",
		Game:     model.NewGame("999", "Different Game", "different-game"),
		Linked:   true,
		Valid:    true,
		StartsAt: time.Now().Add(-1 * time.Hour),
		EndsAt:   time.Now().Add(5 * time.Hour),
		Drops: []TimedDrop{
			{
				ID:              "d-1",
				Name:            "Drop 1",
				StartsAt:        time.Now().Add(-1 * time.Hour),
				EndsAt:          time.Now().Add(5 * time.Hour),
				RequiredMinutes: 60,
				Benefits:        []Benefit{{ID: "b-1", Name: "Benefit", Type: BenefitDirectEntitlement}},
			},
		},
		AllowedChannels: []model.Channel{
			{ID: "chan-online-1", Login: "streamer_online"},
		},
	}

	// ResolveCandidates returns the online streamer
	candidates, err := ResolveCandidates(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	// But ResolveChannel filters them out because they are playing a different game
	primary, err := ResolveChannel(context.Background(), gqlClient, campaign)
	require.NoError(t, err)
	assert.Nil(t, primary, "channel playing different game must not be chosen by ResolveChannel")
}
