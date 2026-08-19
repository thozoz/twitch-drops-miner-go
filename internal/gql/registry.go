package gql

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

//go:embed operations.json
var embeddedOperationsJSON []byte

// ErrUnknownOperation is returned when an operation is not in the registry.
var ErrUnknownOperation = errors.New("unknown GraphQL operation")

type registryFile struct {
	Endpoint   string               `json:"endpoint"`
	ClientID   string               `json:"client_id"`
	Operations map[string]Operation `json:"operations"`
}

// Registry stores the GraphQL endpoint, client ID, and persisted queries.
type Registry struct {
	endpoint   string
	clientID   string
	operations map[string]Operation
}

// Endpoint returns the GraphQL endpoint URL.
func (r *Registry) Endpoint() string {
	return r.endpoint
}

// ClientID returns the client ID associated with the registry operations.
func (r *Registry) ClientID() string {
	return r.clientID
}

// Operation looks up an operation by its Twitch operation name.
// Returns an error wrapping ErrUnknownOperation if the operation is not registered.
func (r *Registry) Operation(name string) (Operation, error) {
	op, ok := r.operations[name]
	if !ok {
		return Operation{}, fmt.Errorf("%w: %q", ErrUnknownOperation, name)
	}
	return op, nil
}

// Operations returns a copy of the operation map.
func (r *Registry) Operations() map[string]Operation {
	res := make(map[string]Operation, len(r.operations))
	for k, v := range r.operations {
		res[k] = v
	}
	return res
}

// parseEmbedded parses the embedded operations.json.
func parseEmbedded() (*Registry, error) {
	var rf registryFile
	if err := json.Unmarshal(embeddedOperationsJSON, &rf); err != nil {
		return nil, fmt.Errorf("failed to parse embedded operations.json: %w", err)
	}
	ops := make(map[string]Operation, len(rf.Operations))
	for name, op := range rf.Operations {
		op.Name = name
		ops[name] = op
	}
	return &Registry{
		endpoint:   rf.Endpoint,
		clientID:   rf.ClientID,
		operations: ops,
	}, nil
}

// LoadRegistry loads the embedded registry and merges an optional override file from overridePath.
// If overridePath is empty or the file does not exist on disk, it returns the embedded registry
// with an empty replaced list and nil error.
// If an override file exists, its operations replace embedded operations by name, and the replaced
// operation names are returned in the replaced slice.
func LoadRegistry(overridePath string) (*Registry, []string, error) {
	reg, err := parseEmbedded()
	if err != nil {
		return nil, nil, err
	}

	if overridePath == "" {
		return reg, nil, nil
	}

	data, err := os.ReadFile(overridePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return reg, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to read operations override file %q: %w", overridePath, err)
	}

	var override registryFile
	if err := json.Unmarshal(data, &override); err != nil {
		return nil, nil, fmt.Errorf("failed to parse operations override file %q: %w", overridePath, err)
	}

	if override.Endpoint != "" {
		reg.endpoint = override.Endpoint
	}
	if override.ClientID != "" {
		reg.clientID = override.ClientID
	}

	var replaced []string
	for name, op := range override.Operations {
		op.Name = name
		if _, exists := reg.operations[name]; exists {
			replaced = append(replaced, name)
		}
		reg.operations[name] = op
	}

	return reg, replaced, nil
}
