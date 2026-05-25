# Latency Bench — Design

## Background

ExEx is an in-process reth hook that theoretically notifies consumers before the WS newHeads layer. This tool subscribes to both sources concurrently, matches by block number, and measures the arrival time delta at millisecond precision.

## Goal

A Go CLI tool under `examples/go-latency-bench/` that exits after N matched blocks and prints a summary.

---

## Module Layout

```
examples/go-latency-bench/
  main.go      — CLI parsing, goroutine startup, matching loop, output
  ws.go        — WS eth_subscribe("newHeads") connection and reader
  grpc.go      — ExEx gRPC Subscribe connection and reader
  proto/gen/   — copied from go-consumer (no protoc needed)
```

---

## Interface Definitions

**ws.go**
```go
// SubscribeNewHeads connects to wsURL, subscribes to eth_subscribe("newHeads"),
// and writes each new block's arrival event to ch. Errors are sent to errCh.
// The goroutine exits when ctx is cancelled or a fatal error occurs.
func SubscribeNewHeads(ctx context.Context, wsURL string, ch chan<- arrival, errCh chan<- error)
```

**grpc.go**
```go
// SubscribeGRPC connects to grpcAddr and subscribes with include_headers=true.
// For each ChainCommitted notification it writes the tip block's arrival event
// to ch. Other notification types (reorged, reverted) are ignored.
// Errors are sent to errCh. The goroutine exits when ctx is cancelled or a
// fatal error occurs.
func SubscribeGRPC(ctx context.Context, grpcAddr string, ch chan<- arrival, errCh chan<- error)
```

---

## Data Structures

```go
// arrival records a single-side notification event.
type arrival struct {
    blockNumber uint64
    arrivedAtMs int64  // time.Now().UnixMilli(), Unix epoch millis; same basis on both sides so delta = wsMs - grpcMs
}

// partialArrival holds an in-progress match for one block number.
type partialArrival struct {
    wsMs   int64     // 0 means not yet arrived
    grpcMs int64     // 0 means not yet arrived
    since  time.Time // when the first side arrived, used for timeout
}
```

Note: `matchedBlock` is not defined as a standalone struct; the matching fields are handled inline in the `emit` function.

---

## Matching Logic (main.go)

```
pending: map[uint64]*partialArrival  // accessed only in the main goroutine select loop — no locking needed

loop:
  select {
    case a := <-wsCh:
      update pending[a.blockNumber].wsMs
      if grpcMs already set → emit matched block, matched++
    case a := <-grpcCh:
      update pending[a.blockNumber].grpcMs
      if wsMs already set → emit matched block, matched++
    case <-ticker.C (every 1s):
      scan pending; entries older than 10s → unmatched++, delete
  }
  if matched == N → print summary, exit 0
```

**Concurrency safety**: `pending` map is only accessed in the main goroutine. `ws.go` and `grpc.go` each run in their own goroutine and only write to channels — no shared mutable state.

---

## CLI

```
go run . [--ws WS_URL] [--grpc GRPC_ADDR] [--blocks N]

Defaults:
  --ws     ws://127.0.0.1:8546
  --grpc   127.0.0.1:10000
  --blocks 100
```

---

## Output Format

Per matched block:
```
block=21000001 ws=1748123456789ms grpc=1748123456785ms delta=+4ms
block=21000002 ws=1748123467890ms grpc=1748123467893ms delta=-3ms
```
Positive delta (gRPC earlier) is prefixed with `+`; negative delta (WS earlier) is prefixed with `-`.

On timeout, one diagnostic line is printed per unmatched block:
```
block=21000003 timeout (ws=true grpc=false)
```

Summary on exit:
```
--- results ---
matched=100 unmatched=2
delta min=-3ms max=+12ms avg=+5ms
```

When `matched=0` (all blocks timed out) or `--blocks 0`:
```
--- results ---
matched=0 unmatched=2
(no data)
```

---

## Error and Edge Cases

| Scenario | Behaviour |
|----------|-----------|
| WS connection fails | Print error to stderr, exit 1 |
| gRPC connection fails | Print error to stderr, exit 1 |
| One side missing after 10 s | Print timeout line, mark unmatched, continue |
| ChainReorged / ChainReverted | gRPC side processes ChainCommitted only; other types ignored (necessary boundary — not relevant for latency comparison) |
| N=0 | Print empty summary immediately, exit 0 |

---

## Alternatives Considered

**Option A (chosen): goroutine + channel + main select matching**
- Pro: logic centralised in main, pending map single-goroutine access, lock-free, easy to read
- Con: ticker-based timeout scan has up to 1 s delay — acceptable

**Option B: both sides write to a shared channel, consumer uses sync.Map**
- Pro: no select branches
- Con: sync.Map introduces concurrent read/write complexity with no meaningful benefit — rejected

---

## Test Plan

| Case | Scenario | Input | Expected |
|------|----------|-------|---------|
| T1 | Compiles | `go build ./...` | No errors |
| T2 | Normal 3-block match | `--blocks 3`, node online | 3 lines of `block=N ws=...ms grpc=...ms delta=...ms`, then summary, then exit |
| T3 | Custom endpoints | `--ws ws://X --grpc Y --blocks 5` | Uses specified endpoints, exits after 5 blocks |
| T4 | Delta sign correct | Observe output | Positive when gRPC first; negative when WS first |
| T5 | Timeout skip | Disconnect one side, wait 10 s | Block marked unmatched, no deadlock, other blocks continue |
| T6 | Invalid WS address | `--ws ws://invalid` | Prints error, exits non-zero |
| T7 | Invalid gRPC address | `--grpc invalid:99999` | Prints error, exits non-zero |
| T8 | N=0 | `--blocks 0` | Prints empty summary, exits 0 |

---

## Dependencies

- `github.com/gorilla/websocket` — WS client (new dependency; see ADR-001)
- `google.golang.org/grpc` — already used in go-consumer; gRPC client configured with 64 MB max receive message size (consistent with go-consumer; mainnet blocks rarely exceed 20 MB)
- `proto/gen/` — copied from `examples/go-consumer/proto/gen/`

---

## ADR-001: Use gorilla/websocket

- **Status**: Accepted
- **Context**: Go standard library has no WebSocket support; a third-party library is required
- **Decision**: `github.com/gorilla/websocket`
- **Rationale**: de-facto standard Go WS library; simple client API; no heavy dependencies needed
- **Consequences**: adds one external dependency; gorilla/websocket is archived but poses negligible risk for a pure client use case

---

## Out of Scope

- Latency histograms or distribution charts
- Persistent output (CSV/JSON)
- Multi-node comparison
- TLS / authentication
