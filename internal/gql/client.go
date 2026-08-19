package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-resty/resty/v2"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBatchChunkSize = 20
	defaultOrigin         = "https://www.twitch.tv"
	defaultReferer        = "https://www.twitch.tv"
)

// Client executes GraphQL operations against Twitch with rate limiting,
// exponential backoff, header injection, and token refresh handling.
type Client struct {
	httpClient    *resty.Client
	limiter       *RateLimiter
	registry      *Registry
	identity      Identity
	refresher     TokenRefresher
	minRetryDelay time.Duration
}

// ClientOption allows customizing Client parameters.
type ClientOption func(*Client)

// WithLimiter overrides the client's RateLimiter.
func WithLimiter(limiter *RateLimiter) ClientOption {
	return func(c *Client) {
		if limiter != nil {
			c.limiter = limiter
		}
	}
}

// WithMinRetryDelay sets the minimum delay used for single-retry errors (e.g. PersistedQueryNotFound).
func WithMinRetryDelay(d time.Duration) ClientOption {
	return func(c *Client) {
		c.minRetryDelay = d
	}
}

// NewClient creates a new GQL Client.
func NewClient(
	registry *Registry,
	identity Identity,
	refresher TokenRefresher,
	httpClient *resty.Client,
	opts ...ClientOption,
) *Client {
	if httpClient == nil {
		httpClient = resty.New()
	}

	c := &Client{
		httpClient:    httpClient,
		limiter:       NewRateLimiter(5, time.Second),
		registry:      registry,
		identity:      identity,
		refresher:     refresher,
		minRetryDelay: 5 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Do executes a single persisted query operation by name.
func (c *Client) Do(ctx context.Context, operationName string, vars map[string]any) (json.RawMessage, error) {
	op, err := c.registry.Operation(operationName)
	if err != nil {
		return nil, err
	}

	payload := RequestPayload{
		OperationName: op.Name,
		Extensions: Extensions{
			PersistedQuery: PersistedQueryExtension{
				Version:    1,
				SHA256Hash: op.SHA256Hash,
			},
		},
		Variables: mergeVars(op.Variables, vars),
	}

	endpoint := c.resolveEndpoint()
	backoff := NewExponentialBackoff(WithBackoffMaximum(300))
	singleRetry := true
	unauthorizedRefreshed := false

	for {
		if err := c.limiter.Acquire(ctx); err != nil {
			return nil, err
		}

		req := c.newRestyRequest(ctx).SetBody(payload)
		resp, err := req.Post(endpoint)
		c.limiter.Release()

		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			delay := backoff.Next()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		statusCode := resp.StatusCode()

		// 401 Unauthorized handling
		if statusCode == 401 {
			if !unauthorizedRefreshed && c.refresher != nil {
				unauthorizedRefreshed = true
				if err := c.refresher.RefreshOnUnauthorized(ctx); err != nil {
					return nil, fmt.Errorf("unauthorized and token refresh failed: %w", err)
				}
				continue
			}
			return nil, fmt.Errorf("unauthorized: 401 received from server")
		}

		// 429 Too Many Requests or 5xx Server Error handling
		if statusCode == 429 || (statusCode >= 500 && statusCode < 600) {
			delay := backoff.Next()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		var env ResponseEnvelope
		if err := json.Unmarshal(resp.Body(), &env); err != nil {
			return nil, fmt.Errorf("failed to decode response json: %w", err)
		}

		if len(env.Errors) > 0 {
			forceRetry := false
			for _, gqlErr := range env.Errors {
				msg := gqlErr.Message
				if singleRetry && (msg == "PersistedQueryNotFound" || msg == "service error") {
					singleRetry = false
					slog.Error("Retrying GraphQL operation on error",
						slog.String("operation", operationName),
						slog.String("error", msg),
					)
					delay := backoff.Next()
					if delay < c.minRetryDelay {
						delay = c.minRetryDelay
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(delay):
					}
					forceRetry = true
					break
				}

				if msg == "service timeout" || msg == "service unavailable" || msg == "context deadline exceeded" {
					delay := backoff.Next()
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(delay):
					}
					forceRetry = true
					break
				}
			}

			if forceRetry {
				continue
			}

			return nil, fmt.Errorf("graphql error: %s", env.Errors[0].Message)
		}

		if env.Error != "" {
			return nil, fmt.Errorf("graphql error: %s: %s", env.Error, env.Message)
		}

		return env.Data, nil
	}
}

// DoBatch executes a batch of operations, chunking at 20 operations per HTTP request.
func (c *Client) DoBatch(ctx context.Context, ops []BatchOp) ([]json.RawMessage, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	payloads := make([]RequestPayload, len(ops))
	for i, op := range ops {
		regOp, err := c.registry.Operation(op.Name)
		if err != nil {
			return nil, fmt.Errorf("batch operation %d (%q) invalid: %w", i, op.Name, err)
		}
		payloads[i] = RequestPayload{
			OperationName: regOp.Name,
			Extensions: Extensions{
				PersistedQuery: PersistedQueryExtension{
					Version:    1,
					SHA256Hash: regOp.SHA256Hash,
				},
			},
			Variables: mergeVars(regOp.Variables, op.Variables),
		}
	}

	results := make([]json.RawMessage, len(ops))
	g, gCtx := errgroup.WithContext(ctx)

	for i := 0; i < len(payloads); i += defaultBatchChunkSize {
		start := i
		end := start + defaultBatchChunkSize
		if end > len(payloads) {
			end = len(payloads)
		}
		chunk := payloads[start:end]
		chunkIndex := start

		g.Go(func() error {
			chunkRes, err := c.doBatchChunk(gCtx, chunk)
			if err != nil {
				return err
			}
			if len(chunkRes) != len(chunk) {
				return fmt.Errorf("expected %d batch responses, got %d", len(chunk), len(chunkRes))
			}
			for j, r := range chunkRes {
				results[chunkIndex+j] = r
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (c *Client) doBatchChunk(ctx context.Context, chunk []RequestPayload) ([]json.RawMessage, error) {
	endpoint := c.resolveEndpoint()
	backoff := NewExponentialBackoff(WithBackoffMaximum(300))
	singleRetry := true
	unauthorizedRefreshed := false

	for {
		if err := c.limiter.Acquire(ctx); err != nil {
			return nil, err
		}

		req := c.newRestyRequest(ctx).SetBody(chunk)
		resp, err := req.Post(endpoint)
		c.limiter.Release()

		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			delay := backoff.Next()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		statusCode := resp.StatusCode()

		if statusCode == 401 {
			if !unauthorizedRefreshed && c.refresher != nil {
				unauthorizedRefreshed = true
				if err := c.refresher.RefreshOnUnauthorized(ctx); err != nil {
					return nil, fmt.Errorf("unauthorized and token refresh failed: %w", err)
				}
				continue
			}
			return nil, fmt.Errorf("unauthorized: 401 received from server")
		}

		if statusCode == 429 || (statusCode >= 500 && statusCode < 600) {
			delay := backoff.Next()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		var envs []ResponseEnvelope
		if err := json.Unmarshal(resp.Body(), &envs); err != nil {
			// Check if single envelope error was returned
			var singleEnv ResponseEnvelope
			if sErr := json.Unmarshal(resp.Body(), &singleEnv); sErr == nil && (len(singleEnv.Errors) > 0 || singleEnv.Error != "") {
				envs = []ResponseEnvelope{singleEnv}
			} else {
				return nil, fmt.Errorf("failed to decode batch response json: %w", err)
			}
		}

		forceRetry := false
		for _, env := range envs {
			if len(env.Errors) > 0 {
				for _, gqlErr := range env.Errors {
					msg := gqlErr.Message
					if singleRetry && (msg == "PersistedQueryNotFound" || msg == "service error") {
						singleRetry = false
						opName := ""
						if env.Extensions != nil {
							opName = env.Extensions.OperationName
						}
						slog.Error("Retrying GraphQL batch operation on error",
							slog.String("operation", opName),
							slog.String("error", msg),
						)
						delay := backoff.Next()
						if delay < c.minRetryDelay {
							delay = c.minRetryDelay
						}
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(delay):
						}
						forceRetry = true
						break
					}

					if msg == "service timeout" || msg == "service unavailable" || msg == "context deadline exceeded" {
						delay := backoff.Next()
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(delay):
						}
						forceRetry = true
						break
					}
				}
			}
			if forceRetry {
				break
			}
		}

		if forceRetry {
			continue
		}

		// Check for any unhandled GQL errors
		rawResults := make([]json.RawMessage, len(envs))
		for i, env := range envs {
			if len(env.Errors) > 0 {
				return nil, fmt.Errorf("batch operation %d failed: %s", i, env.Errors[0].Message)
			}
			if env.Error != "" {
				return nil, fmt.Errorf("batch operation %d failed: %s: %s", i, env.Error, env.Message)
			}
			rawResults[i] = env.Data
		}

		return rawResults, nil
	}
}

func (c *Client) resolveEndpoint() string {
	if c.httpClient != nil && c.httpClient.HostURL != "" {
		return c.httpClient.HostURL
	}
	if c.registry != nil && c.registry.Endpoint() != "" {
		return c.registry.Endpoint()
	}
	return "https://gql.twitch.tv/gql"
}

func (c *Client) newRestyRequest(ctx context.Context) *resty.Request {
	req := c.httpClient.R().SetContext(ctx)

	req.SetHeader("Accept", "*/*")
	req.SetHeader("Accept-Encoding", "gzip")
	req.SetHeader("Accept-Language", "en-US")
	req.SetHeader("Pragma", "no-cache")
	req.SetHeader("Cache-Control", "no-cache")
	req.SetHeader("Origin", defaultOrigin)
	req.SetHeader("Referer", defaultReferer)

	if c.identity != nil {
		if cid := c.identity.ClientID(); cid != "" {
			req.SetHeader("Client-Id", cid)
		}
		if did := c.identity.DeviceID(); did != "" {
			req.SetHeader("X-Device-Id", did)
		}
		if sid := c.identity.SessionID(); sid != "" {
			req.SetHeader("Client-Session-Id", sid)
		}
		if ua := c.identity.UserAgent(); ua != "" {
			req.SetHeader("User-Agent", ua)
		}
		if token := c.identity.AccessToken(); token != "" {
			req.SetHeader("Authorization", "OAuth "+token)
		}
	} else if c.registry != nil && c.registry.ClientID() != "" {
		req.SetHeader("Client-Id", c.registry.ClientID())
	}

	return req
}

func mergeVars(base, overrides map[string]any) map[string]any {
	if base == nil && overrides == nil {
		return nil
	}
	res := make(map[string]any)
	for k, v := range base {
		res[k] = v
	}
	for k, v := range overrides {
		if vMap, ok := v.(map[string]any); ok {
			if baseMap, ok := res[k].(map[string]any); ok {
				res[k] = mergeVars(baseMap, vMap)
				continue
			}
		}
		res[k] = v
	}
	return res
}

// UnmarshalResponse parses a raw JSON message into a target destination.
func UnmarshalResponse[T any](data json.RawMessage) (T, error) {
	var target T
	if err := json.Unmarshal(data, &target); err != nil {
		return target, fmt.Errorf("failed to unmarshal GQL response: %w", err)
	}
	return target, nil
}
