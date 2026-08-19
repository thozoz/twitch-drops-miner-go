package pubsub

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"tdm/internal/gql"
)

const (
	defaultEndpointURL  = "wss://pubsub-edge.twitch.tv/v1"
	defaultPingInterval = 3 * time.Minute
	defaultPingTimeout  = 10 * time.Second
	defaultBackoffMax   = 180 * time.Second
	nonceChars          = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// ClientOption configures a PubSub Client.
type ClientOption func(*Client)

// WithEndpointURL overrides the PubSub Edge WebSocket URL (useful for testing).
func WithEndpointURL(url string) ClientOption {
	return func(c *Client) {
		c.endpointURL = url
	}
}

// WithPingInterval overrides the interval between PING frames.
func WithPingInterval(d time.Duration) ClientOption {
	return func(c *Client) {
		c.pingInterval = d
	}
}

// WithPingTimeout overrides the maximum duration to wait for a PONG response.
func WithPingTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.pingTimeout = d
	}
}

// WithBackoffMax overrides the maximum backoff delay between reconnect attempts.
func WithBackoffMax(d time.Duration) ClientOption {
	return func(c *Client) {
		c.backoffMax = d
	}
}

// Client manages a single WebSocket connection to Twitch PubSub Edge.
type Client struct {
	identity      gql.Identity
	events        chan Event
	topics        map[Topic]struct{}
	topicsChanged chan struct{}
	mu            sync.Mutex

	endpointURL  string
	pingInterval time.Duration
	pingTimeout  time.Duration
	backoffMax   time.Duration
}

// NewClient creates a new PubSub Client.
func NewClient(identity gql.Identity, opts ...ClientOption) *Client {
	c := &Client{
		identity:      identity,
		events:        make(chan Event, 128),
		topics:        make(map[Topic]struct{}),
		topicsChanged: make(chan struct{}, 1),
		endpointURL:   defaultEndpointURL,
		pingInterval:  defaultPingInterval,
		pingTimeout:   defaultPingTimeout,
		backoffMax:    defaultBackoffMax,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Events returns the receive-only channel for inbound PubSub events.
func (c *Client) Events() <-chan Event {
	return c.events
}

// AddTopics registers topics to be subscribed to.
func (c *Client) AddTopics(topics ...Topic) {
	c.mu.Lock()
	changed := false
	for _, t := range topics {
		if _, exists := c.topics[t]; !exists {
			c.topics[t] = struct{}{}
			changed = true
		}
	}
	c.mu.Unlock()

	if changed {
		select {
		case c.topicsChanged <- struct{}{}:
		default:
		}
	}
}

// RemoveTopics unregisters topics so they are unlistened.
func (c *Client) RemoveTopics(topics ...Topic) {
	c.mu.Lock()
	changed := false
	for _, t := range topics {
		if _, exists := c.topics[t]; exists {
			delete(c.topics, t)
			changed = true
		}
	}
	c.mu.Unlock()

	if changed {
		select {
		case c.topicsChanged <- struct{}{}:
		default:
		}
	}
}

// Topics returns a copy of all currently registered topics.
func (c *Client) Topics() []Topic {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := make([]Topic, 0, len(c.topics))
	for t := range c.topics {
		list = append(list, t)
	}
	return list
}

// Run executes the connection loop, reconnecting with exponential backoff on disconnects.
// It returns when ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	backoff := gql.NewExponentialBackoff(gql.WithBackoffMaximum(c.backoffMax.Seconds()))

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		conn, _, err := websocket.Dial(ctx, c.endpointURL, nil)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			delay := backoff.Next()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		backoff.Reset()

		if err := c.runConn(ctx, conn); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}

func (c *Client) runConn(ctx context.Context, conn *websocket.Conn) error {
	connCtx, cancelConn := context.WithCancel(ctx)
	defer func() {
		cancelConn()
		conn.Close(websocket.StatusNormalClosure, "disconnecting")
	}()

	var writeMu sync.Mutex
	sendFrame := func(frame outboundFrame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		data, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		return conn.Write(connCtx, websocket.MessageText, data)
	}

	submitted := make(map[Topic]struct{})

	syncTopics := func() error {
		c.mu.Lock()
		currentTopics := make([]Topic, 0, len(c.topics))
		for t := range c.topics {
			currentTopics = append(currentTopics, t)
		}
		var authToken string
		if c.identity != nil {
			authToken = c.identity.AccessToken()
		}
		c.mu.Unlock()

		currentSet := make(map[Topic]struct{}, len(currentTopics))
		for _, t := range currentTopics {
			currentSet[t] = struct{}{}
		}

		// Determine removed topics
		var removed []Topic
		for t := range submitted {
			if _, exists := currentSet[t]; !exists {
				removed = append(removed, t)
			}
		}

		if len(removed) > 0 {
			for _, chunk := range ChunkTopics(removed, 20) {
				strChunk := make([]string, len(chunk))
				for i, t := range chunk {
					strChunk[i] = string(t)
				}
				frame := outboundFrame{
					Type:  "UNLISTEN",
					Nonce: newNonce(),
					Data: topicsPayload{
						Topics:    strChunk,
						AuthToken: authToken,
					},
				}
				if err := sendFrame(frame); err != nil {
					return err
				}
			}
			for _, t := range removed {
				delete(submitted, t)
			}
		}

		// Determine added topics
		var added []Topic
		for _, t := range currentTopics {
			if _, exists := submitted[t]; !exists {
				added = append(added, t)
			}
		}

		if len(added) > 0 {
			for _, chunk := range ChunkTopics(added, 20) {
				strChunk := make([]string, len(chunk))
				for i, t := range chunk {
					strChunk[i] = string(t)
				}
				frame := outboundFrame{
					Type:  "LISTEN",
					Nonce: newNonce(),
					Data: topicsPayload{
						Topics:    strChunk,
						AuthToken: authToken,
					},
				}
				if err := sendFrame(frame); err != nil {
					return err
				}
			}
			for _, t := range added {
				submitted[t] = struct{}{}
			}
		}

		return nil
	}

	// Initial topic subscription
	if err := syncTopics(); err != nil {
		return err
	}

	var pongDeadlineMu sync.Mutex
	var pongDeadline time.Time

	var wg sync.WaitGroup

	// Goroutine (a): PING and dynamic topic subscription updates
	wg.Add(1)
	go func() {
		defer wg.Done()
		pingTicker := time.NewTicker(c.pingInterval)
		defer pingTicker.Stop()

		for {
			select {
			case <-connCtx.Done():
				return
			case <-c.topicsChanged:
				if err := syncTopics(); err != nil {
					cancelConn()
					return
				}
			case <-pingTicker.C:
				pongDeadlineMu.Lock()
				pongDeadline = time.Now().Add(c.pingTimeout)
				pongDeadlineMu.Unlock()

				if err := sendFrame(outboundFrame{Type: "PING"}); err != nil {
					cancelConn()
					return
				}
			}
		}
	}()

	// Goroutine (b): Watchdog for missed PONG
	wg.Add(1)
	go func() {
		defer wg.Done()
		checkInterval := c.pingTimeout / 2
		if checkInterval < 10*time.Millisecond {
			checkInterval = 10 * time.Millisecond
		} else if checkInterval > time.Second {
			checkInterval = time.Second
		}
		watchdogTicker := time.NewTicker(checkInterval)
		defer watchdogTicker.Stop()

		for {
			select {
			case <-connCtx.Done():
				return
			case <-watchdogTicker.C:
				pongDeadlineMu.Lock()
				deadline := pongDeadline
				pongDeadlineMu.Unlock()

				if !deadline.IsZero() && time.Now().After(deadline) {
					cancelConn()
					return
				}
			}
		}
	}()

	// Read loop
	for {
		msgType, data, err := conn.Read(connCtx)
		if err != nil {
			cancelConn()
			break
		}
		if msgType != websocket.MessageText {
			continue
		}

		var frame inboundFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}

		switch frame.Type {
		case "PONG":
			pongDeadlineMu.Lock()
			pongDeadline = time.Time{}
			pongDeadlineMu.Unlock()

		case "RECONNECT":
			cancelConn()
			goto waitAndExit

		case "RESPONSE":
			// No special handling needed

		case "MESSAGE":
			if frame.Data == nil || frame.Data.Message == "" {
				continue
			}
			var inner innerMessage
			if err := json.Unmarshal([]byte(frame.Data.Message), &inner); err != nil {
				continue
			}
			event := Event{
				Topic:   Topic(frame.Data.Topic),
				Type:    inner.Type,
				Payload: inner.Data,
			}
			select {
			case <-ctx.Done():
				cancelConn()
				goto waitAndExit
			case <-connCtx.Done():
				goto waitAndExit
			case c.events <- event:
			}
		}
	}

waitAndExit:
	cancelConn()
	wg.Wait()
	return connCtx.Err()
}

func newNonce() string {
	b := make([]byte, 30)
	if _, err := cryptorand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	for i := range b {
		b[i] = nonceChars[int(b[i])%len(nonceChars)]
	}
	return string(b)
}
