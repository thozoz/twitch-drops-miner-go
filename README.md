# tdm — Twitch Drops Miner (Go, Headless-First)

`tdm` is a lightweight, headless-first reimplementation of [DevilXD/TwitchDropsMiner](https://github.com/DevilXD/TwitchDropsMiner) written in pure Go.

It runs as a standalone daemon or CLI utility designed for headless Linux servers, containers, and home labs. It monitors Twitch Drop campaigns, simulates stream watching without downloading video or audio data, and automatically claims drops when progress reaches 100%.

## Features

- **Headless-First Design:** Pure CLI / daemon with zero GUI or browser dependencies.
- **Zero Bandwidth Video:** Simulates stream viewing signals via Twitch GQL API without downloading video or audio streams.
- **OAuth Device Code Flow:** Safe authorization on headless hosts using `https://www.twitch.tv/activate`.
- **Single Static Binary:** Compiles with `CGO_ENABLED=0` to a static, lightweight binary (<15 MB) for Linux (`amd64`, `arm64`), macOS, and Windows.
- **Crash Resilience & Security:** Atomic JSON persistence (`0600` file permissions) with automatic token redaction in logs.
- **Rate-Limited GQL Client:** Built-in token-bucket rate limiting (5 req/s) with exponential backoff on HTTP 429/5xx.

## Getting Started

### Building from Source

Requires Go 1.26+:

```bash
# Build host binary
go build -o tdm ./cmd/tdm

# Or cross-compile for Linux ARM64 / AMD64 using Makefile
make build-linux-arm64
make build-linux-amd64
```

### Authentication

Authorize your Twitch account using the OAuth Device Code Flow:

```bash
# 1. Start login flow
./tdm auth login

# 2. Open the printed https://www.twitch.tv/activate URL in your browser and enter the user code.

# 3. Check status
./tdm auth status
```

### Configuration & Diagnostics

```bash
# View active merged configuration (JSON)
./tdm config show

# Initialize default configuration file
./tdm config init

# Run a diagnostic GraphQL persisted query
./tdm gql probe ViewerDropsDashboard
```

## Acknowledgements & Attribution

Portions of this project's Twitch protocol logic (OAuth Device Code Flow, GQL persisted query operations, rate limiting models, and header structures) are ported from [DevilXD/TwitchDropsMiner](https://github.com/DevilXD/TwitchDropsMiner) (MIT License).

Special thanks to **DevilXD** and all contributors to the original Python implementation. See the [NOTICE](NOTICE) and [LICENSE](LICENSE) files for details.

## License

This project is licensed under the [MIT License](LICENSE).
