# Design: go-state-diff-bench — ExEx gRPC StateDiff vs newHeads+trace_replayBlockTransactions Latency Benchmark

## Module Layout

```
examples/go-state-diff-bench/
  main.go        — flag parsing, goroutine coordination, block matching, stats output
  grpc.go        — gRPC subscription (IncludeHeaders+IncludeStateDiff), records arrival time
  ws.go          — WS newHeads subscription + inline trace_replayBlockTransactions call
  go.mod         — module: state_diff_bench, go 1.25.0
  go.sum
  proto/gen/     — copied from go-consumer/proto/gen/
  main_test.go   — unit tests for signPrefix, emit, getOrCreate
```

## CLI Interface

```
--blocks int    number of matched blocks before exit (default 10)
--grpc  string  gRPC endpoint (default 127.0.0.1:10000)
--ws    string  WebSocket endpoint (default ws://127.0.0.1:8546)
--rpc   string  HTTP JSON-RPC endpoint (default http://127.0.0.1:8545)
```

## Data Structures

```go
type arrival struct {
    blockNumber uint64
    arrivedAtMs int64
    accounts    uint64  // len(chain.StateDiff.Accounts); gRPC side only, 0 on WS side
}

type partialArrival struct {
    grpcMs   int64
    rpcMs    int64
    since    time.Time
    accounts uint64
}
```

## Concurrency

| Goroutine | Writes to | Reads from |
|-----------|-----------|------------|
| SubscribeGRPC | grpcCh | — |
| SubscribeWS | rpcCh | — |
| main loop | pending map | grpcCh, rpcCh, ticker, errCh |

`pending` is accessed only in the main goroutine — no mutex needed.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| gRPC connect/subscribe fails | send to errCh → print to stderr + exit 1 |
| gRPC stream EOF | print message, goroutine exits cleanly |
| gRPC recv error (non-ctx) | send to errCh → print to stderr + exit 1 |
| WS connect/subscribe fails | send to errCh → print to stderr + exit 1 |
| HTTP POST fails | send to errCh → print to stderr + exit 1 |
| ctx cancelled | both goroutines exit cleanly |
| block timeout (10s) | count as unmatched, delete from pending |

## Output Formats

Per-block line (FR5):
```
block=<N> accounts=<N> grpc=<N>ms rpc=<N>ms delta=<+/->Nms
```

Summary (FR6):
```
--- results ---
matched=<N> unmatched=<N>
delta min=<+/->Nms max=<+/->Nms avg=<+/->Nms
```

`signPrefix`: returns `"+"` for zero and positive values, `""` for negative. Zero delta is displayed as `+0ms`.

`--blocks 0`: print the empty summary immediately and exit 0 without starting any goroutines.

`--blocks < 0`: print error to stderr and exit 1 immediately.

Latency formula (FR3): `delta = grpcMs - rpcMs`. Negative value means gRPC delivered state diff faster than RPC.

`trace_replayBlockTransactions` JSON-RPC request body:
```json
{"jsonrpc":"2.0","method":"trace_replayBlockTransactions","params":["0x<hex>",["stateDiff"]],"id":1}
```

## Key Implementation Notes

- `IncludeHeaders: true` is required alongside `IncludeStateDiff: true` — empty blocks have no StateDiff entries, so block number must come from the header.
- `accounts = len(chain.StateDiff.Accounts)` is taken on the gRPC side only; the RPC response is a per-tx array and parsing it adds overhead inconsistent with the latency measurement goal.
- `rpcMs` is recorded immediately after `http.DefaultClient.Do()` returns (before body drain). The body is then drained and discarded so the TCP connection can be reused; drain errors are ignored since `rpcMs` is already captured.
- gRPC client sets `MaxCallRecvMsgSize = 64 MiB` to accommodate large state diff payloads from high-activity blocks.
- `SubscribeWS` spawns an internal goroutine (`go func() { <-ctx.Done(); conn.Close() }()`) to unblock `ReadMessage` when the context is cancelled, ensuring the goroutine exits cleanly.
- HTTP responses with non-2xx status codes are treated as errors (sent to errCh → exit 1), consistent with the "HTTP POST fails" error handling entry.

## Test Plan

| ID | Type | Scenario | Input | Expected |
|----|------|----------|-------|----------|
| T1 | unit | signPrefix negative | -1 | "" |
| T2 | unit | signPrefix zero | 0 | "+" |
| T3 | unit | signPrefix positive | 1 | "+" |
| T4 | unit | emit: gRPC faster | grpcMs=1000, rpcMs=1020 | delta=-20, matched increments by 1 |
| T5 | unit | emit: RPC faster | grpcMs=1020, rpcMs=1000 | delta=+20, matched increments by 1 |
| T6 | unit | emit: equal | grpcMs=1000, rpcMs=1000 | delta=0, matched increments by 1 |
| T7 | unit | emit: matched counter | initial matched=5, unmatched=2 | returns matched=6, unmatched=2 |
| T8 | unit | emit: deltas accumulate | two sequential calls | len(deltas)=2, values correct |
| T9 | unit | getOrCreate: new entry | empty map, key=42 | non-nil, grpcMs=0, rpcMs=0, since≈now, stored in map |
| T10 | unit | getOrCreate: existing | key already present with grpcMs=12345 | same pointer returned, grpcMs preserved |
| T11 | unit | getOrCreate: independent keys | keys 1 and 2 | different pointers |
| T12 | manual | --blocks 0 early exit | blocks=0 | prints empty summary, exits 0, no goroutines started |
| T13 | unit | emit: accounts=0 (empty block) | accounts=0, grpcMs=1000, rpcMs=1010 | line printed with accounts=0, delta=-10 |
| T14 | manual | timeout: unmatched path | one side missing after 10s | block counted as unmatched, removed from pending |
| T15 | manual | AC6: gRPC connection failure | invalid gRPC address | error sent to errCh, printed to stderr, exit 1 |
| T16 | manual | AC6: WS connection failure | invalid WS URL | error sent to errCh, printed to stderr, exit 1 |
| T17 | integration | AC3: end-to-end 5 blocks | --blocks 5, live node | prints 5 per-block lines, then summary, exits 0 |

## Alternative Approaches

**RPC method selection**: `debug_traceBlockByNumber` with `prestateTracer+diffMode` was considered but rejected — it fetches additional contract bytecode not present in ExEx StateDiff, which would add overhead and overstate RPC latency relative to the gRPC path.

## ADR-001: Inline trace_replayBlockTransactions in WS goroutine

- **Status**: Adopted
- **Background**: `rpcMs` must capture the moment the full state diff data is available to the client, not just when the block header arrives.
- **Decision**: Call `trace_replayBlockTransactions` synchronously inside the WS goroutine immediately after receiving each newHead.
- **Rationale**: Same pattern as go-internal-txs-bench (inline `debug_traceBlockByNumber`). A separate worker pool would add queuing jitter to `rpcMs`, making the delta less meaningful as a latency measurement.
- **Consequences**: If the node is slow, the WS goroutine blocks on the HTTP call; this is intentional — it reflects real-world end-to-end latency from the client's perspective.
