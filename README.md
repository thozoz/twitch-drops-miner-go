# tdm — Twitch Drops Miner (Go, Headless-First)

`tdm` is a lightweight, headless-first reimplementation of [DevilXD/TwitchDropsMiner](https://github.com/DevilXD/TwitchDropsMiner) written in pure Go.

Designed specifically for headless Linux servers, home labs, Docker containers, and NAS devices. It monitors Twitch Drop campaigns, selects the highest-priority live channel, simulates stream presence without downloading video or audio bandwidth, tracks real-time progress via PubSub WebSockets, and automatically claims drops to your Twitch inventory.

---

## 🌟 Key Features

- **🚀 Headless-First Daemon:** Runs in the background as a true detached daemon (`tdm start` / `tdm stop`) with zero GUI, browser, or X11 dependencies.
- **⚡ Zero Video Bandwidth:** Simulates legitimate viewer watch presence using Twitch Spade minute beacons (~59s cadence). **0 KB of video/HLS stream downloaded** — runs happily on minimal bandwidth.
- **🔄 Dual Progress Engine:** Subscribes to real-time Twitch PubSub WebSocket events (`user-drop-events.<user_id>`) for instant drop updates, backed by periodic GQL reconciliation.
- **🔌 Local IPC Control Plane:** Fast JSON-RPC 2.0 control plane over a `0600` Unix domain socket (or Windows named pipe). Monitor status, change game priorities on the fly, and stream logs without restarting.
- **🔑 Headless OAuth Device Code Flow:** Authenticate seamlessly from any remote SSH session via `https://www.twitch.tv/activate`.
- **🛡️ Atomic State & Stale-Socket Recovery:** Persists runtime progress to `state.json` atomically. Detects stale sockets from killed processes and rejects duplicate concurrent daemons to prevent double-beacon account risks.
- **📦 Single Static Binary:** Pure Go with `CGO_ENABLED=0` (<15 MB static ELF/PE binary). Native cross-compilation for Linux (`amd64`, `arm64`), macOS, and Windows.

---

## 🏗️ Architecture

```
                    ┌──────────────────────────────────────────────────┐
                    │                   tdm CLI                        │
                    │   (status / logs -f / priority / stop / start)   │
                    └────────────────────────┬─────────────────────────┘
                                             │ JSON-RPC 2.0 (Unix Socket / Named Pipe)
                                             ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                 tdm Daemon (Supervisor)                                │
│                                                                                        │
│   ┌──────────────────────┐      ┌─────────────────────┐      ┌─────────────────────┐   │
│   │   Campaign Selector  │ ───► │   Channel Resolver  │ ───► │    WatchSession     │   │
│   │  (Priority/Exclude)  │      │     (Live / ACL)    │      │ (Progress & Claim)  │   │
│   └──────────────────────┘      └─────────────────────┘      └──────────┬──────────┘   │
│                                                                         │              │
│       ┌─────────────────────────────────────────────────────────────────┼──────────┐   │
│       ▼                                                                 ▼          ▼   │
│ ┌───────────┐                                                     ┌──────────┐ ┌─────┐ │
│ │   Spade   │ (~59s Beacon)                                       │  PubSub  │ │ GQL │ │
│ │  Watcher  │                                                     │  Client  │ │ Eng │ │
│ └─────┬─────┘                                                     └────┬─────┘ └──┬──┘ │
└───────┼────────────────────────────────────────────────────────────────┼──────────┼────┘
        ▼                                                                ▼          ▼
 ┌──────────────┐                                                ┌───────────────┐ ┌────────┐
 │ Spade Beacon │                                                │ PubSub WS v1  │ │ GQL    │
 │ (Video-Free) │                                                │ (Drop Events) │ │ (Auth) │
 └──────────────┘                                                └───────────────┘ └────────┘
                                         Twitch Edge
```

---

## 📦 Installation & Build

Requires **Go 1.26+**:

```bash
# Clone the repository
git clone https://github.com/thozoz/twitch-drops-miner-go.git
cd twitch-drops-miner-go

# Build host binary
go build -o tdm ./cmd/tdm

# Or cross-compile static binaries using Makefile
make build-linux-amd64
make build-linux-arm64
```

---

## 🚀 Quick Start

```bash
# 1. Authenticate via Device Code Flow
./tdm auth login

# 2. Start background mining daemon
./tdm start

# 3. Check live status & progress
./tdm status

# 4. Stream daemon logs in real-time
./tdm logs -f

# 5. Add games to priority list dynamically
./tdm priority add "Rust" "World of Warcraft"

# 6. Stop daemon when finished
./tdm stop
```

---

## 📖 Complete CLI Command Reference

### 1. Daemon & Mining Management

#### `tdm start`
Starts the mining supervisor as a detached background daemon. Performs a fast socket health check and prints the assigned PID. If a daemon is already running, it rejects execution immediately (DMN-08).
```bash
./tdm start
# Output: tdm daemon started (PID 12345)

# Flags:
#   --config <path>      Path to custom configuration file
#   --log-file <path>    Path to rotating log file (default: $XDG_STATE_HOME/tdm/miner.log)
#   --log-level <level>  Log level (debug, info, warn, error)
#   --log-format <fmt>   Log format (text, json)
```

#### `tdm stop`
Gracefully halts the running mining daemon over the IPC socket. Flushes pending state, closes listeners, and unbinds the socket/named pipe.
```bash
./tdm stop
# Output: tdm daemon stopped

# Flags:
#   --timeout <sec>      Timeout in seconds to wait for graceful shutdown (default: 15)
```

#### `tdm status`
Queries the running daemon over the local socket and displays live operational status, active campaign, channel, drop progress percentage, remaining ETA, and error count.
```bash
./tdm status
# Output:
# Status: watching
# Campaign: Rust Charity '26 (Rust)
# Channel: streamer_name
# Drop: Rust Hoodie (45/60 min, 75.0%)
# ETA: 15m0s
# Errors: 0
# Uptime: 42m10s

# Output structured JSON (useful for monitoring scripts / jq):
./tdm status --json
```

#### `tdm logs`
Retrieves recent log entries from the daemon's in-memory 1000-line ring buffer or opens a real-time log stream over the IPC socket.
```bash
# View last 50 log lines:
./tdm logs

# View last 100 log lines:
./tdm logs -n 100

# Stream logs live (equivalent to tail -f, immune to log rotation line drops):
./tdm logs -f
```

#### `tdm priority`
Inspects and modifies the daemon's priority game list dynamically. Priority mutations apply immediately at the next campaign selection boundary without interrupting an active stream watch session (DMN-06).
```bash
# List active priority games in order:
./tdm priority list

# Add games to the priority list (preserves existing list):
./tdm priority add "Rust" "World of Warcraft"

# Overwrite priority list with a new order:
./tdm priority set "Fortnite" "Rust" "Overwatch"
```

#### `tdm run`
Runs the mining supervisor continuously in the foreground (interactive mode). Useful for systemd services, Docker containers, or direct terminal debugging.
```bash
./tdm run
# (Press Ctrl+C to initiate bounded graceful shutdown)

# Flags:
#   --daemon-mode        Suppresses interactive startup banner
```

#### `tdm mine`
Runs a single, one-shot mining session: selects the best available campaign, resolves a live channel, watches and claims drops to completion, and exits.
```bash
./tdm mine

# Flags:
#   --no-pubsub          Disables PubSub WebSocket and relies solely on GQL reconciliation
```

---

### 2. Authentication (`tdm auth`)

#### `tdm auth login`
Initiates Twitch OAuth Device Code Flow authorization. Prints the verification URI and user code. Polls until authorization completes and stores credentials securely in `auth.json` with `0600` permissions.
```bash
./tdm auth login
# Output: Go to https://www.twitch.tv/activate?device-code=... and enter code: ...
# Logged in as username (user id 12345678)
```

#### `tdm auth status`
Validates the current OAuth token against Twitch API and displays the logged-in username and user ID.
```bash
./tdm auth status
# Output: Authenticated as username (user id 12345678)
```

#### `tdm auth logout`
Securely removes stored credentials and resets local authentication state.
```bash
./tdm auth logout
# Output: Logged out successfully
```

---

### 3. Inventory & Channel Inspection (`tdm inventory`, `tdm channel`)

#### `tdm inventory list`
Fetches all active, upcoming, and completed Twitch drop campaigns for the authenticated account, displaying required minutes, earned minutes, claim status, and account link status.
```bash
./tdm inventory list
```

#### `tdm inventory select`
Runs the campaign selector against live Twitch data based on active priority and exclusion rules, showing which campaign would be mined next.
```bash
./tdm inventory select
```

#### `tdm inventory watch-decision`
Simulates a channel selection and channel switch evaluation, showing the selected streamer and decision reasons.
```bash
./tdm inventory watch-decision
```

#### `tdm channel watch <channel>`
Diagnostics command to test stream presence, channel metadata, broadcast ID extraction, and Spade minute beacon emission against a single specific channel.
```bash
./tdm channel watch streamer_login
```

---

### 4. Configuration & Diagnostics (`tdm config`, `tdm gql`, `tdm pubsub`)

#### `tdm config show`
Prints the active, merged configuration (from file and environment variables) in JSON format.
```bash
./tdm config show
```

#### `tdm config init`
Creates a starter `config.json` file in the default configuration directory.
```bash
./tdm config init
```

#### `tdm gql probe <operation>`
Executes an authenticated Twitch GraphQL persisted query from the registry (`internal/gql/operations.json`) and prints the raw JSON response.
```bash
./tdm gql probe ViewerDropsDashboard
./tdm gql probe ChannelVideoProperties
```

#### `tdm pubsub listen`
Opens an interactive diagnostics session connecting to Twitch PubSub WebSocket (`wss://pubsub-edge.twitch.tv/v1`), subscribing to `user-drop-events.<user_id>`, and printing raw drop events as they arrive.
```bash
./tdm pubsub listen
```

#### `tdm version`
Prints binary version, Git commit hash, build date, and Go compiler version.
```bash
./tdm version
```

---

### 5. Global Persistent Flags

The following flags can be passed to any `tdm` command:

| Flag | Shorthand | Description | Default |
|---|---|---|---|
| `--config <path>` | — | Path to custom configuration file | `""` (Auto-resolved via XDG) |
| `--log-level <lvl>` | — | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `--log-format <fmt>` | — | Log output format (`text`, `json`) | `text` |
| `--log-file <path>` | — | Path to rotating log file | `""` (stderr) |
| `--help` | `-h` | Help documentation for the command | — |

---

## ⚙️ Configuration (`config.json`)

Configuration is resolved automatically via standard **XDG Base Directory** paths:
- **Linux / POSIX:** `$XDG_CONFIG_HOME/tdm/config.json` (defaults to `~/.config/tdm/config.json`)
- **Windows:** `%LOCALAPPDATA%\tdm\config.json`

Example `config.json`:
```json
{
  "log_level": "info",
  "log_format": "text",
  "log_file": "",
  "priority": [
    "Rust",
    "World of Warcraft",
    "Fortnite"
  ],
  "exclude": [
    "Closed Beta Game"
  ]
}
```

### Environment Variable Overrides
All options can be overridden via environment variables:
- `TDM_CONFIG`: Path to custom configuration file.
- `TDM_LOG_LEVEL`: `debug`, `info`, `warn`, `error`.
- `TDM_LOG_FORMAT`: `text` or `json`.
- `TDM_LOG_FILE`: Path to rotating log file (empty = standard output/stderr).

---

## 🔍 Reliability & Multi-Day Soak Qualification

`tdm` is engineered for uninterrupted multi-day execution:
- **Goroutine Leak Proof:** Automated proxy soak tests (`TestSoak_GoroutineCeiling`, `TestSoak_ErrorPathDoesNotLeak`) cycle 800+ rapid mining loops with 0 goroutine growth.
- **Zero Timer Drift:** Reselect and beacon timers maintain strictly bounded cadences (drift < 100µs).
- **Race Condition Free:** All concurrent state mutations (PubSub vs GQL vs IPC) are protected by strict read/write locks.

For profiling commands and multi-day manual verification checklists, see [docs/SOAK.md](docs/SOAK.md).

---

## ⚖️ Acknowledgements & Attribution

Portions of this project's Twitch protocol logic (OAuth Device Code Flow parameters, GQL persisted query operations, rate limiting models, Spade beacon payloads, and header structures) are ported from [DevilXD/TwitchDropsMiner](https://github.com/DevilXD/TwitchDropsMiner) (MIT License).

Special thanks to **DevilXD** and all contributors to the original Python implementation. See the [NOTICE](NOTICE) and [LICENSE](LICENSE) files for full licensing details.

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).
