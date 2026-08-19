package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockIdentity struct {
	token string
}

func (m *mockIdentity) ClientID() string    { return "test_client_id" }
func (m *mockIdentity) DeviceID() string    { return "test_device_id" }
func (m *mockIdentity) SessionID() string   { return "test_session_id" }
func (m *mockIdentity) UserAgent() string   { return "test_user_agent" }
func (m *mockIdentity) AccessToken() string { return m.token }

func toWSURL(httpURL string) string {
	return strings.Replace(httpURL, "http://", "ws://", 1)
}

func TestClient_ListenChunking(t *testing.T) {
	var listenFrames []outboundFrame
	var mu sync.Mutex
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var frame outboundFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			if frame.Type == "LISTEN" {
				mu.Lock()
				listenFrames = append(listenFrames, frame)
				count := len(listenFrames)
				mu.Unlock()
				if count == 3 {
					close(done)
				}
			}
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "mock_oauth_token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(10*time.Second),
		WithPingTimeout(1*time.Second),
		WithBackoffMax(1*time.Second),
	)

	var topics []Topic
	for i := 1; i <= 45; i++ {
		topics = append(topics, Topic(fmt.Sprintf("topic.%d", i)))
	}
	client.AddTopics(topics...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for 3 LISTEN chunks")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, listenFrames, 3)

	var allReceivedTopics []string
	for _, frame := range listenFrames {
		assert.Equal(t, "LISTEN", frame.Type)
		assert.Len(t, frame.Nonce, 30)

		payloadMap, ok := frame.Data.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "mock_oauth_token", payloadMap["auth_token"])

		rawTopics, ok := payloadMap["topics"].([]interface{})
		require.True(t, ok)
		assert.LessOrEqual(t, len(rawTopics), 20)

		for _, rt := range rawTopics {
			allReceivedTopics = append(allReceivedTopics, rt.(string))
		}
	}

	require.Len(t, allReceivedTopics, 45)
	var expectedTopics []string
	for _, t := range topics {
		expectedTopics = append(expectedTopics, string(t))
	}
	assert.ElementsMatch(t, expectedTopics, allReceivedTopics)
}

func TestClient_PingPong(t *testing.T) {
	var connCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&connCount, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, _, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			// Intentionally do NOT reply with PONG
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(40*time.Millisecond),
		WithPingTimeout(20*time.Millisecond),
		WithBackoffMax(50*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	// Wait for reconnect to happen due to missed PONG
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&connCount) >= 2
	}, 400*time.Millisecond, 20*time.Millisecond, "expected watchdog to trigger reconnect after missing PONG")
}

func TestClient_ReconnectResubscribes(t *testing.T) {
	var connCount int32
	var mu sync.Mutex
	connListens := make(map[int32][]string)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&connCount, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var frame outboundFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			if frame.Type == "LISTEN" {
				payloadMap, ok := frame.Data.(map[string]interface{})
				if ok {
					rawTopics, ok := payloadMap["topics"].([]interface{})
					if ok {
						mu.Lock()
						for _, rt := range rawTopics {
							connListens[idx] = append(connListens[idx], rt.(string))
						}
						mu.Unlock()
					}
				}
				// Force-close connection 1 to trigger reconnect
				if idx == 1 {
					conn.Close(websocket.StatusInternalError, "forced test drop")
					return
				}
			}
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(5*time.Second),
		WithPingTimeout(1*time.Second),
		WithBackoffMax(20*time.Millisecond),
	)
	client.AddTopics(Topic("user-drop-events.123"), Topic("onsite-notifications.123"))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(connListens[1]) == 2 && len(connListens[2]) == 2
	}, 800*time.Millisecond, 20*time.Millisecond, "expected connection 2 to resubscribe to the same 2 topics")

	mu.Lock()
	defer mu.Unlock()
	expected := []string{"user-drop-events.123", "onsite-notifications.123"}
	assert.ElementsMatch(t, expected, connListens[1])
	assert.ElementsMatch(t, expected, connListens[2])
}

func TestClient_ReconnectMessage(t *testing.T) {
	var connCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&connCount, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if idx == 1 {
			// Send RECONNECT message to force client to redial
			reconnFrame, _ := json.Marshal(map[string]string{"type": "RECONNECT"})
			_ = conn.Write(r.Context(), websocket.MessageText, reconnFrame)
		}

		for {
			_, _, err := conn.Read(r.Context())
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(5*time.Second),
		WithPingTimeout(1*time.Second),
		WithBackoffMax(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&connCount) >= 2
	}, 800*time.Millisecond, 20*time.Millisecond, "expected RECONNECT message to trigger redial")
}

func TestClient_MessageDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var frame outboundFrame
			_ = json.Unmarshal(data, &frame)
			if frame.Type == "LISTEN" {
				// Send sample drop-progress event
				msg := map[string]interface{}{
					"type": "MESSAGE",
					"data": map[string]string{
						"topic":   "user-drop-events.12345",
						"message": `{"type":"drop-progress","data":{"current_progress_min":3,"required_progress_min":10,"drop_id":"drop-abc"}}`,
					},
				}
				msgBytes, _ := json.Marshal(msg)
				_ = conn.Write(r.Context(), websocket.MessageText, msgBytes)
			}
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(5*time.Second),
		WithPingTimeout(1*time.Second),
	)
	client.AddTopics(UserDropsTopic(12345))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	select {
	case event := <-client.Events():
		assert.Equal(t, Topic("user-drop-events.12345"), event.Topic)
		assert.Equal(t, "drop-progress", event.Type)
		var payload struct {
			CurrentProgressMin  int    `json:"current_progress_min"`
			RequiredProgressMin int    `json:"required_progress_min"`
			DropID              string `json:"drop_id"`
		}
		err := json.Unmarshal(event.Payload, &payload)
		require.NoError(t, err)
		assert.Equal(t, 3, payload.CurrentProgressMin)
		assert.Equal(t, 10, payload.RequiredProgressMin)
		assert.Equal(t, "drop-abc", payload.DropID)

	case <-ctx.Done():
		t.Fatal("timed out waiting for event dispatch")
	}
}

func TestClient_DynamicAddRemoveTopics(t *testing.T) {
	var frames []outboundFrame
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var frame outboundFrame
			if err := json.Unmarshal(data, &frame); err == nil {
				mu.Lock()
				frames = append(frames, frame)
				mu.Unlock()
			}
		}
	}))
	defer server.Close()

	client := NewClient(
		&mockIdentity{token: "token"},
		WithEndpointURL(toWSURL(server.URL)),
		WithPingInterval(5*time.Second),
		WithPingTimeout(1*time.Second),
	)
	client.AddTopics(Topic("topic.1"))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_ = client.Run(ctx)
	}()

	// Wait for initial LISTEN
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) >= 1
	}, 300*time.Millisecond, 20*time.Millisecond)

	// Dynamically add topic.2
	client.AddTopics(Topic("topic.2"))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) >= 2
	}, 300*time.Millisecond, 20*time.Millisecond)

	// Dynamically remove topic.1
	client.RemoveTopics(Topic("topic.1"))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, f := range frames {
			if f.Type == "UNLISTEN" {
				return true
			}
		}
		return false
	}, 300*time.Millisecond, 20*time.Millisecond)
}
