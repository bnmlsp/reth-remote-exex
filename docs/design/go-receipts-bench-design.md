# go-receipts-bench — Design

## Background

ExEx gRPC latency has been reduced to ~0 ms avg vs WS newHeads. This tool measures whether ExEx gRPC (receipts-only subscription) delivers receipts faster or slower than the WS newHeads + `eth_getBlockReceipts` path, and by how much.

## Goal

New binary `examples/go-receipts-bench` that runs both paths concurrently, matches arrivals by block number, and prints per-block delta and summary statistics. No changes to any existing file.

---

## Module Layout

```
examples/go-receipts-bench/
  main.go          — flag parsing, goroutine launch, block matching, output
  grpc.go          — gRPC subscriber goroutine
  ws.go            — WS + HTTP RPC subscriber goroutine
  proto/gen/
    exex.pb.go     — copied from go-consumer/proto/gen/
    exex_grpc.pb.go
  go.mod           — module receipts_bench, go 1.25.0
  go.sum
```

No existing files are modified.

---

## Interface Definition

### CLI

```
go run . [--blocks N] [--grpc ADDR] [--ws URL] [--rpc URL]

Flags:
  --blocks int    blocks to match before exiting  (default: 10)
  --grpc   string gRPC address                    (default: 127.0.0.1:10000)
  --ws     string WebSocket endpoint              (default: ws://127.0.0.1:8546)
  --rpc    string HTTP JSON-RPC endpoint          (default: http://127.0.0.1:8545)
```

### Internal channel types

```go
// arrival is sent from each subscriber goroutine to main.
type arrival struct {
    blockNumber uint64
    arrivedAtMs int64  // Unix epoch milliseconds, recorded immediately after data is received
}
```

---

## Data Structures

```go
// partialArrival tracks which sides have arrived for a given block.
// Owned exclusively by the main goroutine via the pending map.
type partialArrival struct {
    grpcMs int64      // 0 = not yet arrived
    rpcMs  int64      // 0 = not yet arrived
    since  time.Time  // time first side arrived, used for timeout
}

// pending is keyed by block number, written and read only by main goroutine.
pending map[uint64]*partialArrival
```

Matched deltas are appended to a `[]int64` slice in main, also owned by main only.

---

## Module Design

### grpc.go — `runGRPC(ctx, addr, ch, errCh)`

1. Dial gRPC: `grpc.NewClient(addr, insecure, MaxCallRecvMsgSize=64MB)`
2. `client.Subscribe(ctx, &pb.SubscribeRequest{IncludeHeaders: true, IncludeReceipts: true})`
3. Loop: `stream.Recv()`
   - On `io.EOF` or ctx cancel: return
   - On other error: send to `errCh`, return
   - On `ChainCommitted`: record `arrivedAtMs = time.Now().UnixMilli()` **immediately**; extract tip block number from `New.Blocks[len-1].Header.Number`; send `arrival{blockNumber, arrivedAtMs}` to `ch`
4. `ChainReorged` events are ignored (no newHeads equivalent to match against)

### ws.go — `runWS(ctx, wsURL, rpcURL, ch, errCh)`

1. Dial WS: `gorilla/websocket.DefaultDialer.DialContext`
2. Send `eth_subscribe("newHeads")`, discard confirmation response
3. Loop: `conn.ReadMessage()`
   - On error or ctx cancel: return
   - On newHead: extract block number (hex → uint64)
4. For each newHead block number: issue **synchronous** HTTP `eth_getBlockReceipts` call (POST to `rpcURL`)
5. Record `arrivedAtMs = time.Now().UnixMilli()` immediately after HTTP response returns
6. Send `arrival{blockNumber, arrivedAtMs}` to `ch`
7. Context cancellation: a separate goroutine closes the WS connection when `ctx.Done()` fires (same pattern as go-latency-bench)

**`eth_getBlockReceipts` call format:**
```json
{"jsonrpc":"2.0","id":1,"method":"eth_getBlockReceipts","params":["0x<hex blockNumber>"]}
```
The response body is read and discarded after recording the time. No content parsing is needed.

**Inline vs per-goroutine RPC call:** The RPC call is made inline (blocking the WS read loop). On Ethereum mainnet (~12s blocks) with a local RPC (<1ms RTT), this is safe. If the RPC call is still in flight when the next newHead arrives, that newHead is delayed — but this is acceptable because the measurement goal is wall-clock time to receive receipts, and both scenarios (fast local RPC, slow remote RPC) are reflected accurately.

### main.go

1. Parse flags, create `grpcCh`, `rpcCh` (buffered, cap 32 each), `errCh` (buffered, cap 2)
2. Launch `runGRPC` and `runWS` in goroutines with a shared context
3. Ticker every 1 second for timeout scanning
4. Select loop:
   - `grpcCh`: update `pending[blockNumber].grpcMs`; if both sides arrived → emit, delete, increment matched
   - `rpcCh`: update `pending[blockNumber].rpcMs`; if both sides arrived → emit, delete, increment matched
   - `ticker`: scan pending, evict entries older than 10s as unmatched
   - `errCh`: log error, cancel context, break
   - `matched == targetBlocks`: break
5. Print summary; cancel context

**emit() per-block output:**
```
block=<number> grpc=<ms>ms rpc=<ms>ms delta=<sign><ms>ms
```

**Summary output:**
```
--- results ---
matched=<count> unmatched=<count>
delta min=<sign><ms>ms max=<sign><ms>ms avg=<sign><ms>ms
```
Sign prefix: `"+"` for `delta >= 0`, `""` for negative (fmt prints `-` automatically), same as go-latency-bench.

---

## Concurrency

| Goroutine | Writes to | Reads from |
|-----------|-----------|------------|
| runGRPC | grpcCh | gRPC stream |
| runWS | rpcCh | WS conn, HTTP response |
| main | pending map, deltas slice | grpcCh, rpcCh, errCh, ticker |

`pending` and `deltas` are only ever accessed by the main goroutine. No mutex needed.

---

## Error Handling

| Scenario | Behavior |
|----------|----------|
| gRPC server not reachable | `runGRPC` sends error to `errCh`; main logs and exits |
| WS server not reachable | `runWS` sends error to `errCh`; main logs and exits |
| `eth_getBlockReceipts` HTTP error | `runWS` sends error to `errCh`; main logs and exits |
| One side never arrives for a block | Evicted after 10s; counted as unmatched |
| Context cancelled (e.g. after N matches) | Both goroutines detect ctx cancellation and return cleanly |

---

## Alternative Approaches

**Option A (chosen): Inline `eth_getBlockReceipts` in WS goroutine**
- Simple: one goroutine, sequential per block
- Works for mainnet (12s blocks, <1ms local RPC)
- Con: if RPC is slow or remote, delays next newHead read — acceptable since it reflects real-world latency

**Option B: Per-block goroutine for `eth_getBlockReceipts`**
- Spawns a goroutine per newHead to call RPC in parallel
- Needed for L2 chains with sub-second blocks
- Con: more complex (requires WaitGroup or result collection channel); not needed for this use case (mainnet only)

Option A chosen per simplicity principle (FR2 says "immediately" — inline is the most direct implementation).

---

## Test Plan

### T1 — Build (covers AC1)

| Scenario | Input | Expected |
|----------|-------|---------|
| Build binary | `go build ./...` in `examples/go-receipts-bench/` | Exit 0, no errors |

### T2 — End-to-end 5 blocks (covers AC2)

| Scenario | Input | Expected |
|----------|-------|---------|
| Both servers running | `go run . --blocks 5` | Prints 5 matched block lines, then summary, then exits |

### T3 — Delta calculation (covers AC3)

| Scenario | Input | Expected |
|----------|-------|---------|
| grpcMs=1000, rpcMs=1020 for block N | Both arrivals received | Output: `delta=-20ms` (gRPC faster) |
| grpcMs=1020, rpcMs=1000 for block N | Both arrivals received | Output: `delta=+20ms` (RPC faster) |
| grpcMs=rpcMs for block N | Both arrivals received | Output: `delta=+0ms` |

### T4 — `--blocks` flag (covers AC4)

| Scenario | Input | Expected |
|----------|-------|---------|
| N=3 matched blocks | `go run . --blocks 3` | Tool exits after exactly 3 matched blocks |

### T5 — Unmatched / timeout (covers AC5, AC6)

| Scenario | Input | Expected |
|----------|-------|---------|
| gRPC side never delivers block N | Block N present on RPC side only, 10s pass | Counted as `unmatched=1`, excluded from delta stats |
| Summary after N matches + 1 timeout | N=5, 1 unmatched | `matched=5 unmatched=1`; avg computed from 5 deltas only |

### T6 — gRPC unreachable

| Scenario | Input | Expected |
|----------|-------|---------|
| gRPC server not running | `go run . --grpc 127.0.0.1:19999` | Error logged, tool exits non-zero |

### T7 — WS unreachable

| Scenario | Input | Expected |
|----------|-------|---------|
| WS server not running | `go run . --ws ws://127.0.0.1:19998` | Error logged, tool exits non-zero |

### T8 — `eth_getBlockReceipts` returns empty

| Scenario | Input | Expected |
|----------|-------|---------|
| Node not yet indexed block N's receipts | HTTP returns `[]` | Time is still recorded; block is matched normally; no retry |

### T9 — `eth_getBlockReceipts` HTTP error

| Scenario | Input | Expected |
|----------|-------|---------|
| RPC endpoint returns non-2xx or connection refused | `--rpc http://127.0.0.1:19997` | Error logged, tool exits non-zero |

---

## Out of Scope

- Content comparison of receipts between the two paths
- Retry on empty receipts response
- L2 / high-frequency block support (no per-goroutine RPC dispatch)
- TLS or authentication

---

## ADR-001: Inline `eth_getBlockReceipts` in WS goroutine (no per-block goroutine)

**Status:** Adopted

**Background:** Two options exist for how to issue the `eth_getBlockReceipts` call: (A) inline in the WS read loop (blocks next newHead), (B) spawn a goroutine per newHead.

**Decision:** Option A — inline.

**Rationale:** This tool targets Ethereum mainnet (~12s blocks). A local `eth_getBlockReceipts` returns in <1ms, so the WS read loop is never meaningfully delayed. Option B adds synchronization complexity for no practical benefit on mainnet.

**Consequences:** If used on a chain with block times shorter than the RPC RTT, newHeads may be delayed. Acceptable since the tool is scoped to mainnet only.
