# Contributing to tdm

Thanks for helping out. Bug reports, fixes and small focused features are all welcome.

## Setup

- Go version from `go.mod` (currently 1.26.4). No CGO: release builds use `CGO_ENABLED=0`.
- Optional: [`golangci-lint`](https://golangci-lint.run/) v2 for `make lint`.

```sh
git clone https://github.com/thozoz/twitch-drops-miner-go
cd twitch-drops-miner-go
make build        # dist/tdm
make test         # go test -v ./...
make lint         # golangci-lint run (errcheck, govet, staticcheck, gosec)
```

## Layout

| Path | What lives there |
|------|------------------|
| `cmd/tdm/` | Cobra CLI (`start`, `run`, `status`, `auth`, `config`, `exclude`, ...) |
| `internal/auth/` | Device code flow, token import/refresh/validation |
| `internal/gql/` | Twitch GQL client, rate limiting, retries |
| `internal/inventory/` | Campaigns, drops, progress, selection, claiming |
| `internal/channel/` | Stream discovery and watch presence (HLS HEAD / Spade) |
| `internal/pubsub/` | PubSub WebSocket client |
| `internal/session/`, `internal/daemon/` | Mining loop and daemon supervisor |
| `internal/ipc/` | JSON-RPC over Unix socket / Windows named pipe |
| `internal/config/`, `internal/state/` | Config loading and atomic JSON state |
| `testdata/fixtures/` | Static Twitch API responses used by tests |

## Tests

- Tests use static JSON fixtures with `httptest`. **Never** record live traffic or commit real tokens/cookies.
- CI runs `go test -v ./...` on Ubuntu and Windows. Platform-specific code (`*_windows.go`, `*_unix.go`) needs tests on both; if you can't run Windows locally, CI will.
- Avoid sleeps and scheduler assumptions; drive time via the injected clocks/options (`WithNow`, `WithSleep`, interval options) so tests stay deterministic.

## Pull requests

- One topic per PR, branched from `main`. PRs are squash-merged.
- Commit/PR titles follow Conventional Commits: `fix(auth): ...`, `feat: ...`, `test(ipc): ...`, `docs: ...`, `chore: ...`.
- Say what changed and why; link the issue if there is one. Include how you tested it.
- Keep the binary lean: no new dependency for something a few lines of stdlib can do.
- Dependabot PRs are grouped weekly; if one is stale after a fix lands on `main`, comment `@dependabot rebase`.

## Ground rules

- Headless only: no GUI dependencies, and no video/audio downloads (watch presence only).
- Secrets (`auth.json`, `cookies.jar`, tokens) never go into commits, logs or fixtures.
- Logic ported from [DevilXD/TwitchDropsMiner](https://github.com/DevilXD/TwitchDropsMiner) keeps its MIT attribution; see [NOTICE](NOTICE).

By contributing you agree your work is released under the [MIT License](LICENSE).
