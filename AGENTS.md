# AGENTS.md

Guidance for AI coding agents working in this repo. Humans: see [CONTRIBUTING.md](CONTRIBUTING.md).

## Project

`tdm` is a headless Go daemon that mines Twitch drops: tracks campaigns, emits watch-presence beacons (no video/audio download), and claims rewards. Single static binary, `CGO_ENABLED=0`, Linux first, also Windows/macOS.

## Commands

```sh
go build ./...                 # compile
go test ./...                  # all tests (CI: Ubuntu + Windows)
go test ./internal/ipc/ -run TestBind -count=50   # flake-check a test
golangci-lint run              # errcheck, govet, staticcheck, gosec
```

## Map

- `cmd/tdm/` CLI (Cobra). `internal/` holds all logic; `pkg/version/` build info.
- Twitch I/O: `internal/gql` (GQL + rate limit), `internal/auth`, `internal/pubsub`, `internal/channel` (HLS/Spade watch).
- Mining: `internal/inventory` (campaigns, selection, claims), `internal/session`, `internal/daemon`.
- Local: `internal/ipc` (JSON-RPC over Unix socket / `go-winio` named pipe), `internal/config`, `internal/state`.
- Fixtures: `testdata/fixtures/*.json`.

## Rules

- Reuse existing helpers and options before adding code; no new dependency for a few lines of stdlib.
- Stack is fixed: resty v2, coder/websocket, cobra, koanf, sourcegraph/jsonrpc2, `log/slog`, testify. Do not add viper, gorilla/websocket, utls, sqlite, or `net/rpc`.
- Tests: static fixtures + `httptest`; inject time via options (`WithNow`, `WithSleep`, interval options) instead of sleeping. Bound every channel wait with a timeout so a hang fails fast.
- Windows named pipes: keep a client open until the server's `Accept` returns (go-winio drops clients that disconnect early).
- Never log, commit, or put in fixtures real tokens, cookies, `auth.json`, or `settings.json`.
- Never download stream video/audio; presence beacons only.
- Conventional Commit messages (`fix(scope): ...`). Keep changes scoped to the task.
- Ported logic from DevilXD/TwitchDropsMiner keeps MIT attribution (`NOTICE`).
