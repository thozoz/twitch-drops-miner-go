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

### 1. Authentication (Device Code Flow)
Authenticate your Twitch account from any terminal:
```bash
./tdm auth login
```
Open the printed URL (e.g. `https://www.twitch.tv/activate?device-code=...`) in your browser and authorize. Your session is saved securely with `0600` file permissions.

### 2. Start the Daemon
```bash
# Start background mining daemon
./tdm start

# Check real-time mining progress
./tdm status

# Follow daemon logs live
./tdm logs -f
```

### 3. Dynamic Priority Control
Change what the running daemon mines without stopping it or interrupting an active watch session:
```bash
# List current priorities
./tdm priority list

# Add games to priority (case-sensitive)
./tdm priority add "Rust" "World of Warcraft"
```

### 4. Stop the Daemon
```bash
# Gracefully shutdown daemon and release socket
./tdm stop
```

---

## 📖 Complete Command Reference

| Command | Subcommand / Flags | Description |
|---|---|---|
| `tdm start` | `--config`, `--log-file`, `--log-level` | Starts the mining supervisor as a detached background daemon. |
| `tdm stop` | `--timeout <sec>` | Gracefully stops the background daemon over the IPC socket. |
| `tdm status` | `--json` | Displays live status: active game, campaign, channel, drop progress %, ETA, and errors. |
| `tdm logs` | `-f, --follow`, `-n <lines>` | Views recent in-memory log buffer or follows live log stream over IPC. |
| `tdm priority` | `list`, `add <games...>`, `set <games...>` | Queries or updates the daemon's active priority list on the fly. |
| `tdm run` | `--daemon-mode` | Runs the mining supervisor continuously in the foreground (interactive). |
| `tdm mine` | `--no-pubsub` | One-shot mining session: selects, watches, claims one campaign, then exits. |
| `tdm auth` | `login`, `status`, `logout` | Manages OAuth Device Code Flow authentication and credentials. |
| `tdm inventory` | `list`, `select`, `watch-decision` | Inspects campaigns, calculates selection ordering, and evaluates channel switches. |
| `tdm channel` | `watch <channel>` | Diagnostics command to test stream presence and Spade beacons for a single channel. |
| `tdm config` | `show`, `init` | Displays merged runtime configuration or generates a starter `config.json`. |
| `tdm gql` | `probe <operation>` | Executes authenticated Twitch GQL queries for diagnostics. |
| `tdm pubsub` | `listen` | Diagnostics listener for Twitch PubSub WebSocket drop events. |
| `tdm version` | — | Prints version, commit hash, and build date. |

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
