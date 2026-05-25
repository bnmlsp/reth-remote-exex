# go-internal-txs-bench — Design

## Background

`go-receipts-bench` confirmed that ExEx gRPC delivers receipts ~12 ms faster than newHeads+getBlockReceipts on a local server. This tool measures the same question for call traces (internal transactions): ExEx gRPC (`IncludeCallTraces=true`) vs WS newHeads + `debug_traceBlockByNumber`.

## Goal

New binary `examples/go-internal-txs-bench` that runs both paths concurrently, matches arrivals by block number, and prints per-block delta and summary statistics. No changes to any existing file.

---

## Module Layout

```
examples/go-internal-txs-bench/
  main.go          — flag parsing, goroutine launch, block matching, output
  grpc.go          — gRPC subscriber goroutine
  ws.go            — WS + HTTP RPC subscriber goroutine
  main_test.go     — unit tests for signPrefix, emit, getOrCreate
  proto/gen/
    exex.pb.go     — copied from go-consumer/proto/gen/
    exex_grpc.pb.go
  go.mod           — module internal_txs_bench, go 1.25.0
  go.sum           — copied from go-receipts-bench (identical dependencies)
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

### grpc.go — `SubscribeGRPC(ctx, addr, ch, errCh)`

1. Dial gRPC: `grpc.NewClient(addr, insecure, MaxCallRecvMsgSize=64MB)`
2. `client.Subscribe(ctx, &pb.SubscribeRequest{IncludeHeaders: true, IncludeCallTraces: true})`
3. Loop: `stream.Recv()`
   - On `io.EOF`: print "grpc stream closed by server", return
   - On ctx cancel: return
   - On other error: send to `errCh`, return
   - On `ChainCommitted`: record `arrivedAtMs = time.Now().UnixMilli()` **immediately**; extract tip block number from `New.Blocks[len-1].Header.Number`; send `arrival{blockNumber, arrivedAtMs}` to `ch`
4. `ChainReorged` events are ignored

### ws.go — `SubscribeWS(ctx, wsURL, rpcURL, ch, errCh)`

1. Dial WS: `gorilla/websocket.DefaultDialer.DialContext`
2. Send `eth_subscribe("newHeads")`, read and validate ACK (if `ack.Error != nil` → send to `errCh`, return)
3. Loop: `conn.ReadMessage()`
   - On error or ctx cancel: return
   - On newHead: extract block number (hex → uint64)
4. For each newHead block number: issue **synchronous** HTTP `debug_traceBlockByNumber` call (POST to `rpcURL`)
5. Record `arrivedAtMs = time.Now().UnixMilli()` immediately after HTTP response returns
6. Send `arrival{blockNumber, arrivedAtMs}` to `ch`
7. Context cancellation: a separate goroutine closes the WS connection when `ctx.Done()` fires

**`debug_traceBlockByNumber` call format:**
```json
{"jsonrpc":"2.0","id":1,"method":"debug_traceBlockByNumber","params":["0x<hex blockNumber>",{"tracer":"callTracer"}]}
```
The response body is read and discarded after recording the time. No content parsing is needed.

### main.go

1. Parse flags; if `--blocks < 0` → print error to stderr, exit 1; if `--blocks == 0` → print empty summary, exit 0
2. Create `grpcCh`, `rpcCh` (buffered, cap 32 each), `errCh` (buffered, cap 2)
3. Launch `SubscribeGRPC` and `SubscribeWS` in goroutines with a shared signal-notified context
4. Ticker every 1 second for timeout scanning
5. Select loop:
   - `grpcCh`: update `pending[blockNumber].grpcMs`; if both sides arrived → emit, delete, increment matched
   - `rpcCh`: update `pending[blockNumber].rpcMs`; if both sides arrived → emit, delete, increment matched
   - `ticker`: scan pending, evict entries older than 10s as unmatched
   - `errCh`: log error to stderr, exit 1
   - `ctx.Done()`: print summary, exit 0
   - `matched == targetBlocks`: print summary, exit 0

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
Sign prefix: `"+"` for `delta >= 0`, `""` for negative (fmt prints `-` automatically).

---

## Concurrency

| Goroutine | Writes to | Reads from |
|-----------|-----------|------------|
| SubscribeGRPC | grpcCh | gRPC stream |
| SubscribeWS | rpcCh | WS conn, HTTP response |
| main | pending map, deltas slice | grpcCh, rpcCh, errCh, ticker |

`pending` and `deltas` are only ever accessed by the main goroutine. No mutex needed.

---

## Error Handling

| Scenario | Behavior |
|----------|----------|
| gRPC server not reachable | `SubscribeGRPC` sends error to `errCh`; main logs and exits 1 |
| WS server not reachable | `SubscribeWS` sends error to `errCh`; main logs and exits 1 |
| `debug_traceBlockByNumber` HTTP error (non-2xx or connection refused) | `SubscribeWS` sends error to `errCh`; main logs and exits 1 |
| WS subscribe rejected by server | `SubscribeWS` sends error to `errCh`; main logs and exits 1 |
| One side never arrives for a block | Evicted after 10s; counted as unmatched |
| Context cancelled (SIGINT / after N matches) | Both goroutines detect ctx cancellation and return cleanly |

---

## Alternative Approaches

**Option A (chosen): Inline `debug_traceBlockByNumber` in WS goroutine**
- Simple: one goroutine, sequential per block
- Works for mainnet (~12s blocks)
- Con: if `debug_traceBlockByNumber` is slow for a heavy block, the next newHead is delayed — acceptable since it accurately reflects real-world latency

**Option B: Per-block goroutine for `debug_traceBlockByNumber`**
- Spawns a goroutine per newHead to call RPC in parallel
- Needed for L2 chains with sub-second blocks
- Con: more complex (requires WaitGroup or result collection channel); not needed for this use case

Option A chosen per simplicity principle (FR2 says "immediately" — inline is the most direct implementation). Identical rationale to go-receipts-bench ADR-001.

---

## Known Limitations

- `debug_traceBlockByNumber` with `callTracer` can be significantly slower than `eth_getBlockReceipts` for blocks with many complex transactions (full call tree traversal). This means `rpcMs` may be substantially larger than in receipts-bench, making the gRPC advantage appear larger. This is the intended measurement.
- Response payload for call traces can reach tens of MB for busy blocks; gRPC side is capped at 64 MB. If a block exceeds this, the gRPC stream will error. This is the same cap used in all other Go examples.

---

## Test Plan

### T1 — Build (covers AC1)

| Scenario | Input | Expected |
|----------|-------|---------|
| Build binary | `go build ./...` in `examples/go-internal-txs-bench/` | Exit 0, no errors |

### T2 — Unit: signPrefix (covers helper correctness)

| Scenario | Input | Expected |
|----------|-------|---------|
| Negative value | `signPrefix(-1)` | Returns `""` |
| Zero | `signPrefix(0)` | Returns `"+"` |
| Positive value | `signPrefix(1)` | Returns `"+"` |

### T3 — Unit: emit (covers AC3 delta logic)

| Scenario | Input | Expected |
|----------|-------|---------|
| gRPC faster | `emit(100, 1000, 1020, 0, 0, nil)` | `deltas[0] == -20` |
| RPC faster | `emit(100, 1020, 1000, 0, 0, nil)` | `deltas[0] == +20` |
| Equal | `emit(100, 1000, 1000, 0, 0, nil)` | `deltas[0] == 0` |
| Matched counter | `emit(100, 1000, 1000, 5, 2, nil)` | `matched == 6`, `unmatched == 2` |
| Delta accumulation | two consecutive emit calls | `len(deltas) == 2`, correct values |

### T4 — Unit: getOrCreate (covers pending map logic)

| Scenario | Input | Expected |
|----------|-------|---------|
| New entry | `getOrCreate(pending, 42)` | Non-nil, grpcMs/rpcMs == 0, stored in map |
| Existing entry | second `getOrCreate(pending, 99)` after first | Same pointer returned, fields preserved |
| Independent keys | `getOrCreate(pending, 1)`, `getOrCreate(pending, 2)` | Different pointers |

### T5 — End-to-end 5 blocks (covers AC2, AC4)

| Scenario | Input | Expected |
|----------|-------|---------|
| Both servers running | `go run . --blocks 5` | Prints 5 matched block lines, then summary, then exits |

### T6 — Unmatched / timeout (covers AC5, AC6)

| Scenario | Input | Expected |
|----------|-------|---------|
| One side never arrives within 10s | Block N present on one side only | Counted as unmatched, excluded from delta stats |

### T7 — `--blocks` validation (covers AC7, AC8)

| Scenario | Input | Expected |
|----------|-------|---------|
| Negative blocks | `go run . --blocks -1` | Error to stderr, exits 1 |
| Zero blocks | `go run . --blocks 0` | Prints empty summary, exits 0 |

### T8 — gRPC unreachable (covers error handling)

| Scenario | Input | Expected |
|----------|-------|---------|
| gRPC server not running | `go run . --grpc 127.0.0.1:19999` | Error logged, exits non-zero |

### T9 — WS unreachable (covers error handling)

| Scenario | Input | Expected |
|----------|-------|---------|
| WS server not running | `go run . --ws ws://127.0.0.1:19998` | Error logged, exits non-zero |

### T10 — `debug_traceBlockByNumber` HTTP error (covers error handling)

| Scenario | Input | Expected |
|----------|-------|---------|
| RPC endpoint returns non-2xx or connection refused | `--rpc http://127.0.0.1:19997` | Error logged, exits non-zero |

### T11 — `debug_traceBlockByNumber` returns empty (covers AC3)

| Scenario | Input | Expected |
|----------|-------|---------|
| Block has no transactions | HTTP returns `[]` | Time is still recorded; block is matched normally; no retry |

---

## Out of Scope

- Content comparison of call traces between the two paths (handled by `go-trace-verifier`)
- Retry on empty trace response
- L2 / high-frequency block support
- TLS or authentication

---

## ADR-001: Inline `debug_traceBlockByNumber` in WS goroutine (no per-block goroutine)

**Status:** Adopted

**Background:** Same decision as go-receipts-bench. Two options: (A) inline in WS read loop, (B) spawn a goroutine per newHead.

**Decision:** Option A — inline.

**Rationale:** This tool targets Ethereum mainnet (~12s blocks). Even if `debug_traceBlockByNumber` takes 100–500 ms for a heavy block, the 12s block interval provides ample headroom. Option B adds synchronization complexity for no practical benefit on mainnet.

**Consequences:** If used on a chain with block times shorter than the `debug_traceBlockByNumber` RTT, newHeads may be delayed. Acceptable since the tool is scoped to mainnet only.
