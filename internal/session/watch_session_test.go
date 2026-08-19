package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thozoz/twitch-drops-miner-go/internal/channel"
	"github.com/thozoz/twitch-drops-miner-go/internal/gql"
	"github.com/thozoz/twitch-drops-miner-go/internal/inventory"
	"github.com/thozoz/twitch-drops-miner-go/internal/model"
	"github.com/thozoz/twitch-drops-miner-go/internal/pubsub"
	statestore "github.com/thozoz/twitch-drops-miner-go/internal/state"
)

type mockIdentity struct {
	token string
}

func (m *mockIdentity) ClientID() string    { return "test_client_id" }
func (m *mockIdentity) DeviceID() string    { return "test_device_id" }
func (m *mockIdentity) SessionID() string   { return "test_session_id" }
func (m *mockIdentity) UserAgent() string   { return "test_user_agent" }
func (m *mockIdentity) AccessToken() string { return m.token }
func (m *mockIdentity) RefreshOnUnauthorized(ctx context.Context) error {
	return nil
}

func toWSURL(httpURL string) string {
	return strings.Replace(httpURL, "http://", "ws://", 1)
}

func setupMockServers(t *testing.T) (
	*httptest.Server, // GQL / Spade / HTML server
	*httptest.Server, // PubSub WS server
	*testServerState,
) {
	state := &testServerState{
		claimCalls:      make([]string, 0),
		beaconChannels:  make(map[string]int),
		currentDropID:   "drop-1",
		currentMinutes:  0,
		hasActiveDrop:   true,
		claimStatus:     "ELIGIBLE_FOR_ALL",
		wsConnections:   make([]*websocket.Conn, 0),
	}

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Spade beacon POST endpoint
		if r.URL.Path == "/spade_beacon" && r.Method == http.MethodPost {
			_ = r.ParseForm()
			encoded := r.FormValue("data")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				var events []struct {
					Properties struct {
						Channel string `json:"channel"`
					} `json:"properties"`
				}
				if json.Unmarshal(decoded, &events) == nil && len(events) > 0 {
					chName := events[0].Properties.Channel
					state.mu.Lock()
					state.beaconChannels[chName]++
					state.mu.Unlock()
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// 2. DiscoverSpadeURL endpoint (GET returning HTML)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><head><script>var spade={"spade_url":"http://` + r.Host + `/spade_beacon"};</script></head></html>`))
			return
		}

		// 3. GQL endpoint (all other POST requests)
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var op struct {
				OperationName string         `json:"operationName"`
				Variables     map[string]any `json:"variables"`
			}
			_ = json.Unmarshal(body, &op)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			switch op.OperationName {
			case "PlaybackAccessToken":
				_, _ = w.Write([]byte(`{"data":{"streamPlaybackAccessToken":{"value":"{\"channel_id\":\"ch_101\"}","signature":"sig123"}}}`))
				return

			case "DropCurrentSessionContext":
				state.mu.Lock()
				hasActive := state.hasActiveDrop
				dropID := state.currentDropID
				mins := state.currentMinutes
				state.mu.Unlock()

				if !hasActive {
					_, _ = w.Write([]byte(`{"data":{"currentUser":{"dropCurrentSession":null}}}`))
					return
				}
				resp := fmt.Sprintf(`{"data":{"currentUser":{"dropCurrentSession":{"dropID":%q,"currentMinutesWatched":%d}}}}`, dropID, mins)
				_, _ = w.Write([]byte(resp))
				return

			case "DropsPage_ClaimDropRewards":
				state.mu.Lock()
				var dropInstanceID string
				if input, ok := op.Variables["input"].(map[string]any); ok {
					if id, ok := input["dropInstanceID"].(string); ok {
						dropInstanceID = id
					}
				}
				state.claimCalls = append(state.claimCalls, dropInstanceID)
				status := state.claimStatus
				state.mu.Unlock()

				resp := fmt.Sprintf(`{"data":{"claimDropRewards":{"status":%q}}}`, status)
				_, _ = w.Write([]byte(resp))
				return

			default:
				_, _ = w.Write([]byte(`{"data":{}}`))
				return
			}
		}

		http.NotFound(w, r)
	}))

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		state.mu.Lock()
		state.wsConnections = append(state.wsConnections, conn)
		state.mu.Unlock()

		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var frame struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &frame) == nil {
				if frame.Type == "PING" {
					_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"PONG"}`))
				}
			}
		}
	}))

	return httpServer, wsServer, state
}

type testServerState struct {
	mu             sync.Mutex
	claimCalls     []string
	beaconChannels map[string]int
	currentDropID  string
	currentMinutes int
	hasActiveDrop  bool
	claimStatus    string
	wsConnections  []*websocket.Conn
}

func (s *testServerState) sendPubSubMessage(ctx context.Context, topic, msgType, payloadJSON string) error {
	s.mu.Lock()
	conns := make([]*websocket.Conn, len(s.wsConnections))
	copy(conns, s.wsConnections)
	s.mu.Unlock()

	msg := map[string]any{
		"type": "MESSAGE",
		"data": map[string]string{
			"topic":   topic,
			"message": fmt.Sprintf(`{"type":%q,"data":%s}`, msgType, payloadJSON),
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	for _, conn := range conns {
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return err
		}
	}
	return nil
}

func buildTestCampaign(game model.Game, drops ...inventory.TimedDrop) inventory.DropsCampaign {
	now := time.Now()
	return inventory.DropsCampaign{
		ID:       "camp-1",
		Name:     "Test Campaign",
		Game:     game,
		Linked:   true,
		Valid:    true,
		StartsAt: now.Add(-1 * time.Hour),
		EndsAt:   now.Add(24 * time.Hour),
		Drops:    drops,
	}
}

// Test 1: Happy path with PubSub enabled: receives drop-progress event -> claims drop -> Run completes cleanly.
func TestWatchSession_HappyPath_PubSubEnabled(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	pubsubClient := pubsub.NewClient(
		ident,
		pubsub.WithEndpointURL(toWSURL(wsServer.URL)),
		pubsub.WithPingInterval(10*time.Second),
		pubsub.WithPingTimeout(1*time.Second),
	)

	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "state.json")

	session := NewWatchSession(
		gqlClient,
		watcher,
		pubsubClient,
		12345,
		nil,
		WithStatePath(stateFile),
		WithReconcileInterval(50*time.Millisecond),
		WithConfirmDelay(10*time.Millisecond),
		WithConfirmPollInterval(10*time.Millisecond),
	)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "teststreamer",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	drop := inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Tier 1 Reward",
		RequiredMinutes: 10,
		CurrentMinutes:  0,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, drop)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- session.Run(ctx, camp, ch)
	}()

	// Wait for PubSub WS connection to establish, then emit drop-progress event at 100%
	require.Eventually(t, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return len(state.wsConnections) > 0
	}, 1*time.Second, 10*time.Millisecond)

	// In fake GQL, update active drop state so confirm loop will observe change
	state.mu.Lock()
	state.hasActiveDrop = false
	state.mu.Unlock()

	err = state.sendPubSubMessage(
		ctx,
		"user-drop-events.12345",
		"drop-progress",
		`{"drop_id":"drop-1","current_progress_min":10,"drop_instance_id":"12345#camp-1#drop-1"}`,
	)
	require.NoError(t, err)

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for session to complete")
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	require.Len(t, state.claimCalls, 1)
	assert.Equal(t, "12345#camp-1#drop-1", state.claimCalls[0])

	// Verify state file persisted the final claimed drop and 100% progress
	st, found, err := statestore.LoadRuntimeState(stateFile)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "camp-1", st.ActiveCampaignID)
	assert.Equal(t, "drop-1", st.ActiveDropID)
	assert.Equal(t, "ch_101", st.WatchingChannelID)
	assert.Equal(t, "teststreamer", st.WatchingChannelLogin)
	assert.Equal(t, 10, st.CurrentMinutes)
}

// Test 2: PubSub deliberately disabled (nil pubsubClient): GQL reconciliation alone drives progress to 100% and claims drop.
func TestWatchSession_PubSubDisabled_GQLReconcileAlone(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	// Construct session with pubsubClient = nil (PubSub disabled)
	session := NewWatchSession(
		gqlClient,
		watcher,
		nil, // Disabled
		12345,
		nil,
		WithReconcileInterval(20*time.Millisecond),
		WithConfirmDelay(10*time.Millisecond),
		WithConfirmPollInterval(10*time.Millisecond),
	)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "teststreamer",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	drop := inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Tier 1 Reward",
		RequiredMinutes: 10,
		CurrentMinutes:  0,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, drop)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Increment GQL server minutes asynchronously
	go func() {
		time.Sleep(30 * time.Millisecond)
		state.mu.Lock()
		state.currentMinutes = 5
		state.mu.Unlock()

		time.Sleep(30 * time.Millisecond)
		state.mu.Lock()
		state.currentMinutes = 10
		state.mu.Unlock()

		time.Sleep(30 * time.Millisecond)
		state.mu.Lock()
		state.hasActiveDrop = false // Drop finished, confirm loop completes
		state.mu.Unlock()
	}()

	err = session.Run(ctx, camp, ch)
	require.NoError(t, err)

	state.mu.Lock()
	defer state.mu.Unlock()
	require.Len(t, state.claimCalls, 1)
	assert.Equal(t, "12345#camp-1#drop-1", state.claimCalls[0])
}

// Test 3: Server-wins conflict: PubSub reports 3 min, GQL reports 5 min -> final minutes is 5 (server-authoritative).
func TestWatchSession_ServerWinsConflict(t *testing.T) {
	drop := &inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Reward",
		RequiredMinutes: 10,
		CurrentMinutes:  0,
	}

	// 1. Pubsub event arrives reporting 3 minutes
	pubsubChanged := inventory.ReconcileMinutes(drop, 3, nil)
	assert.True(t, pubsubChanged)
	assert.Equal(t, 3, drop.CurrentMinutes)

	// 2. GQL poll arrives reporting 5 minutes
	gqlChanged := inventory.ReconcileMinutes(drop, 5, nil)
	assert.True(t, gqlChanged)
	assert.Equal(t, 5, drop.CurrentMinutes)
}

// Test 4: Single-channel invariant: only beacons for the watched channel are ever emitted.
func TestWatchSession_SingleChannelInvariant(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	session := NewWatchSession(
		gqlClient,
		watcher,
		nil,
		12345,
		nil,
		WithReconcileInterval(50*time.Millisecond),
		WithConfirmDelay(10*time.Millisecond),
		WithConfirmPollInterval(10*time.Millisecond),
	)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "streamer_only",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	drop := inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Tier 1 Reward",
		RequiredMinutes: 1,
		CurrentMinutes:  0,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, drop)

	state.mu.Lock()
	state.currentMinutes = 1
	state.hasActiveDrop = false
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = session.Run(ctx, camp, ch)
	require.NoError(t, err)

	state.mu.Lock()
	defer state.mu.Unlock()
	assert.Len(t, state.beaconChannels, 1, "exactly one channel must be beaconed")
	assert.Contains(t, state.beaconChannels, "streamer_only")
}

// Test 5: No earnable drop: campaign with all drops already claimed returns ErrNoEarnableDrop.
func TestWatchSession_NoEarnableDrop(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	session := NewWatchSession(gqlClient, watcher, nil, 12345, nil)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "streamer_test",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	alreadyClaimedDrop := inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Claimed Reward",
		RequiredMinutes: 10,
		CurrentMinutes:  10,
		IsClaimed:       true,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, alreadyClaimedDrop)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err = session.Run(ctx, camp, ch)
	require.ErrorIs(t, err, ErrNoEarnableDrop)

	state.mu.Lock()
	defer state.mu.Unlock()
	assert.Empty(t, state.claimCalls)
}

// Test 6: Multi-tier campaign advancement: tier 1 is claimed, confirms, and advances to tier 2.
func TestWatchSession_MultiTierAdvancement(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	session := NewWatchSession(
		gqlClient,
		watcher,
		nil,
		12345,
		nil,
		WithReconcileInterval(20*time.Millisecond),
		WithConfirmDelay(10*time.Millisecond),
		WithConfirmPollInterval(10*time.Millisecond),
	)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "teststreamer",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	tier1 := inventory.TimedDrop{
		ID:              "drop-t1",
		Name:            "Tier 1 Drop",
		RequiredMinutes: 5,
		CurrentMinutes:  0,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	tier2 := inventory.TimedDrop{
		ID:                  "drop-t2",
		Name:                "Tier 2 Drop",
		RequiredMinutes:     10,
		CurrentMinutes:      0,
		PreconditionDropIDs: []string{"drop-t1"},
		StartsAt:            time.Now().Add(-1 * time.Hour),
		EndsAt:              time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-2", Name: "Reward 2", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, tier1, tier2)

	state.mu.Lock()
	state.currentDropID = "drop-t1"
	state.currentMinutes = 0
	state.hasActiveDrop = true
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		// Drive tier 1 to 5 min
		time.Sleep(30 * time.Millisecond)
		state.mu.Lock()
		state.currentMinutes = 5
		state.mu.Unlock()

		// Wait for tier 1 claim, switch to tier 2 in mock GQL
		time.Sleep(60 * time.Millisecond)
		state.mu.Lock()
		state.currentDropID = "drop-t2"
		state.currentMinutes = 0
		state.mu.Unlock()

		// Drive tier 2 to 10 min
		time.Sleep(30 * time.Millisecond)
		state.mu.Lock()
		state.currentMinutes = 10
		state.mu.Unlock()

		// Complete tier 2
		time.Sleep(60 * time.Millisecond)
		state.mu.Lock()
		state.hasActiveDrop = false
		state.mu.Unlock()
	}()

	err = session.Run(ctx, camp, ch)
	require.NoError(t, err)

	state.mu.Lock()
	defer state.mu.Unlock()
	require.Len(t, state.claimCalls, 2)
	assert.Equal(t, "12345#camp-1#drop-t1", state.claimCalls[0])
	assert.Equal(t, "12345#camp-1#drop-t2", state.claimCalls[1])
}

// Test 7: Offline stream timeout: PubSub stream-down event stays offline beyond offlineGrace -> ErrChannelOffline.
func TestWatchSession_OfflineGraceTimeout(t *testing.T) {
	httpServer, wsServer, state := setupMockServers(t)
	defer httpServer.Close()
	defer wsServer.Close()

	reg, _, err := gql.LoadRegistry("")
	require.NoError(t, err)

	ident := &mockIdentity{token: "mock_token"}
	httpClient := resty.NewWithClient(httpServer.Client()).SetHostURL(httpServer.URL)
	gqlClient := gql.NewClient(reg, ident, ident, httpClient, gql.WithMinRetryDelay(1*time.Millisecond), gql.WithLimiter(gql.NewRateLimiter(1000, time.Millisecond)))
	watcher := channel.NewWatcher(gqlClient, httpClient, ident, 12345)

	pubsubClient := pubsub.NewClient(
		ident,
		pubsub.WithEndpointURL(toWSURL(wsServer.URL)),
		pubsub.WithPingInterval(10*time.Second),
		pubsub.WithPingTimeout(1*time.Second),
	)

	session := NewWatchSession(
		gqlClient,
		watcher,
		pubsubClient,
		12345,
		nil,
		WithReconcileInterval(50*time.Millisecond),
		WithOfflineGrace(60*time.Millisecond),
	)

	game := model.NewGame("game-1", "Test Game", "test-game")
	ch := model.Channel{
		ID:           "ch_101",
		Login:        "teststreamer",
		BroadcastID:  "bcast_101",
		Online:       true,
		Game:         &game,
		DropsEnabled: true,
	}

	drop := inventory.TimedDrop{
		ID:              "drop-1",
		Name:            "Tier 1 Reward",
		RequiredMinutes: 10,
		CurrentMinutes:  0,
		StartsAt:        time.Now().Add(-1 * time.Hour),
		EndsAt:          time.Now().Add(24 * time.Hour),
		Benefits: []inventory.Benefit{
			{ID: "b-1", Name: "Reward 1", Type: inventory.BenefitBadge},
		},
	}
	camp := buildTestCampaign(game, drop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- session.Run(ctx, camp, ch)
	}()

	// Wait for PubSub WS connection
	require.Eventually(t, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return len(state.wsConnections) > 0
	}, 1*time.Second, 10*time.Millisecond)

	// Emit stream-down event
	err = state.sendPubSubMessage(
		ctx,
		"video-playback-by-id.ch_101",
		"stream-down",
		`{"server_time":12345}`,
	)
	require.NoError(t, err)

	select {
	case err := <-runErr:
		require.ErrorIs(t, err, ErrChannelOffline)
	case <-ctx.Done():
		t.Fatal("timed out waiting for offline grace timeout")
	}
}
