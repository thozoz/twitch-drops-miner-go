package pubsub

import "encoding/json"

// Event represents a decoded inbound PubSub event.
type Event struct {
	Topic   Topic           `json:"topic"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// inboundFrame represents the top-level frame received from the Twitch PubSub Edge WebSocket.
type inboundFrame struct {
	Type  string     `json:"type"`
	Error string     `json:"error,omitempty"`
	Nonce string     `json:"nonce,omitempty"`
	Data  *frameData `json:"data,omitempty"`
}

// frameData contains the topic string and double-encoded message string.
type frameData struct {
	Topic   string `json:"topic"`
	Message string `json:"message"`
}

// innerMessage is the decoded structure of frameData.Message JSON string.
type innerMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// outboundFrame represents a frame sent to the Twitch PubSub Edge WebSocket.
type outboundFrame struct {
	Type  string      `json:"type"`
	Nonce string      `json:"nonce,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// topicsPayload is the data object for LISTEN / UNLISTEN commands.
type topicsPayload struct {
	Topics    []string `json:"topics"`
	AuthToken string   `json:"auth_token,omitempty"`
}
