package gql

import "encoding/json"

// Operation represents a GraphQL persisted query definition.
type Operation struct {
	Name       string         `json:"-"`
	SHA256Hash string         `json:"sha256Hash"`
	Variables  map[string]any `json:"variables,omitempty"`
}

// BatchOp represents a single operation in a batch request.
type BatchOp struct {
	Name      string         `json:"name"`
	Variables map[string]any `json:"variables,omitempty"`
}

// PersistedQueryExtension models the GQL persistedQuery extension object.
type PersistedQueryExtension struct {
	Version    int    `json:"version"`
	SHA256Hash string `json:"sha256Hash"`
}

// Extensions models the GQL extensions wrapper.
type Extensions struct {
	PersistedQuery PersistedQueryExtension `json:"persistedQuery"`
}

// RequestPayload represents the wire format of a single GQL persisted query request.
type RequestPayload struct {
	OperationName string         `json:"operationName"`
	Extensions    Extensions     `json:"extensions"`
	Variables     map[string]any `json:"variables,omitempty"`
}

// GQLError represents a single GraphQL error in a response.
type GQLError struct {
	Message string `json:"message"`
	Path    []any  `json:"path,omitempty"`
}

// ResponseExtensions represents optional extensions in a GQL response.
type ResponseExtensions struct {
	OperationName string `json:"operationName,omitempty"`
}

// ResponseEnvelope represents the wire format of a GQL response.
type ResponseEnvelope struct {
	Data       json.RawMessage     `json:"data,omitempty"`
	Errors     []GQLError          `json:"errors,omitempty"`
	Extensions *ResponseExtensions `json:"extensions,omitempty"`
	Error      string              `json:"error,omitempty"`
	Message    string              `json:"message,omitempty"`
}
