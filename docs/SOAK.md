# Daemon Soak & Reliability Guide (OPS-07)

This document describes the automated proxy verification, profiling tools, and manual soak procedures used to verify the long-term reliability of the `tdm` mining daemon over multi-day operations.

---

## 1. Automated Proxy Test

The automated soak test simulates hundreds of accelerated select-watch-reselect mining cycles within a few seconds to detect goroutine leaks, error-path resource leaks, and timer drift before deploying.

Run the automated proxy suite:
```bash
go test ./internal/daemon/... -run TestSoak -v
```

### What It Proves
- **`TestSoak_GoroutineCeiling`**: Cycles the supervisor through 200+ rapid mining iterations with active channel resolution and watch runner dispatch. Asserts that total goroutines return to baseline (delta ≤ 3) after execution.
- **`TestSoak_ErrorPathDoesNotLeak`**: Alternates transient errors on the watch runner path across 200+ iterations. Asserts that error handling, logging, and error count mutations do not leak goroutines or resources.
- **`TestSoak_TimerDriftBounded`**: Samples timestamp intervals across 50+ reselect cycles. Asserts every interval remains within ±10ms of the configured 20ms backoff, and the final cycle interval exhibits no cumulative drift compared to the first cycle interval (drift difference ≤ 10ms).

---

## 2. Race Detector

Data races that only manifest under concurrent asynchronous events (PubSub WebSocket messages, GQL polling, and IPC JSON-RPC requests) can cause silent memory corruption in multi-day runs.

Run the race detector across core concurrency packages:
```bash
go test -race ./internal/daemon/... ./internal/session/...
```

### What It Catches
- Concurrent access to campaign/priority state without proper lock acquisition.
- Unsynchronized status snapshot mutations during active watch loops.
- Subscriber channel races during live log streaming (`daemon.StreamLogs`).

---

## 3. Profiling a Real Run

### Memory & CPU Profiling via Test Runner
To capture CPU and memory allocation profiles without exposing a permanent remote attack surface, profile against the soak test suite:

```bash
# Capture memory and CPU profiles
go test -run TestSoak -memprofile=mem.prof -cpuprofile=cpu.prof ./internal/daemon/

# Inspect memory allocations
go tool pprof mem.prof

# Inspect CPU bottlenecks
go tool pprof cpu.prof
```

### Inspecting Live Goroutines (On-Demand Crash Dump)
In production, `tdm` avoids running an unauthenticated pprof HTTP server (deferred to v2 per OPS2-02). To capture an instantaneous stack trace of all running goroutines in a live daemon without restarting:

```bash
# On Linux / POSIX systems, send SIGQUIT to dump stack traces to the log file:
kill -QUIT <pid>
```
The Go runtime will immediately output full stack traces of every active goroutine to stderr, which is redirected to the daemon's rotating log file (`miner.log`).

---

## 4. Manual Multi-Day Soak Procedure [MANUAL]

> [!IMPORTANT]
> A multi-day soak test evaluates real Twitch network interaction, server-side drops claiming, and OS-level runtime stability over time. This procedure **cannot be automated** and **cannot be replaced by CI**.

### Setup
1. Authenticate with a valid Twitch account:
   ```bash
   tdm auth login
   ```
2. Start the daemon in detached background mode:
   ```bash
   tdm start
   ```
3. Record the initial PID and start time.

### Daily Verification Checklist
Perform the following checks once every 24 hours during a 3–7 day soak window:

1. **Daemon Status & Progress Check**:
   ```bash
   tdm status --json
   ```
   - Verify `status` is `"watching"` or `"idle"`.
   - Verify `active_game`, `active_campaign`, and `watching_channel` are populated.
   - Verify `progress_percent` and `current_minutes` are advancing over time when channels are online.

2. **Resident Memory (RSS) Sampling**:
   ```bash
   # Linux:
   ps -o rss= -p <pid>
   # or
   grep VmRSS /proc/<pid>/status
   ```
   - Record the RSS value in KB/MB.

3. **Thread and Scheduling Pressure**:
   ```bash
   ls /proc/<pid>/task | wc -l
   ```
   - Record OS thread count.

4. **Timer Drift Sampling**:
   - At two points at least 60 minutes apart during a continuous stream watch, record:
     - Host wall-clock elapsed minutes: `ΔT_wall = T2 - T1`
     - Drop earned minutes from `tdm status --json`: `ΔM_earned = M2 - M1`
   - Calculate drift deficit: `(ΔT_wall - ΔM_earned) / ΔT_wall * 100%`.

### Pass / Fail Thresholds

| Metric | Fail Threshold | Pass Criteria |
|---|---|---|
| **Resident Memory (RSS)** | RSS grows beyond **2x** the Day 1 baseline value. | RSS remains stable within standard GC fluctuations. |
| **OS Threads** | Thread count trends strictly upward day-over-day. | Thread count remains flat (typically 4–12 threads for Go runtime). |
| **Timer Drift** | Earned minutes fall more than **5% behind wall-clock elapsed time** during active streaming, or the gap percentage increases daily. | Earned minutes track wall-clock time within 5% tolerance (~1 earned minute per ~59s beacon). |
| **Drop Progression** | Progress or ETA visibly stuck for > 60 minutes while the streamer is actively online. | Drops advance through tiers and automatically claim to completion. |
| **Twitch Inventory** | Drops claimed by `tdm` do not appear on Twitch inventory page. | Claimed drops are confirmed present in real Twitch Drops Inventory (`https://www.twitch.tv/drops/inventory`). |

If all criteria are met across the observation window, the release passes the OPS-07 soak qualification.
