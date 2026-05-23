# Design: go-trace-verifier

## Background

The ExEx gRPC push server streams call traces for each committed block. The requirements document (`docs/requirements/go-trace-verifier-requirement.md`) calls for a standalone Go tool that subscribes to this stream, fetches ground-truth data from `debug_traceBlockByNumber` for each block, and performs a full recursive field-by-field comparison. This document covers the interface, data structures, module layout, concurrency model, and test plan.

## Module Layout

New directory `examples/go-trace-verifier/` — independent Go module, no changes to any existing file.

```
examples/go-trace-verifier/
├── go.mod               module exex_trace_verifier
├── go.sum
├── proto/gen/
│   ├── exex.pb.go       copied from examples/go-consumer/proto/gen/
│   └── exex_grpc.pb.go  copied from examples/go-consumer/proto/gen/
├── main.go              CLI, pipeline wiring, summary output
├── rpc.go               HTTP JSON-RPC client, RPC types, retry logic
├── compare.go           recursive comparison, normalization functions
└── compare_test.go      table-driven unit tests
```

**Why copied instead of shared:** Each example is a self-contained module. Sharing via relative path import across modules requires `replace` directives and couples release cycles. Copying keeps each module independently buildable.

## Interface Definition

### CLI

```
go run . <n> [--grpc ADDR] [--rpc URL] [--workers W]

  n             required  number of blocks to verify before exiting
  --grpc ADDR   optional  gRPC address       default: 127.0.0.1:10000
  --rpc  URL    optional  JSON-RPC URL       default: http://127.0.0.1:8545
  --workers W   optional  parallel workers   default: 4
```

Parsed with `flag` standard library. `n` is the first positional argument; invalid or missing `n` prints usage and exits 2.

### stdout output

Per-block line printed as each result arrives (not buffered until end):

```
block#25151792 PASS (280 txs, 2279 frames, 512 logs)
block#25151793 MISMATCH (2 errors):
  tx[2].calls[0].gasUsed: grpc=0x5208 rpc=0x5209
  tx[5].logs[0].topics[0]: grpc=0xddf252ad... rpc=0xddf253ad...
block#25151794 RPC_ERROR
```

Mismatch detail lines do **not** repeat the block number — it is already present in the block header line above them. F4's example path (`block#xxx tx[2]...`) is an illustration of the full logical path; the block context is conveyed by the header line, not repeated per detail.

Final summary printed after all `n` blocks are processed:

```
=== Summary ===
Blocks verified:   50
Total tx traces:   14237   ← extension beyond F5's listed fields; included for operator convenience
Total frames:      98341
Total logs:        31209
Total mismatches:  2
```

Exit code 0 = zero mismatches and zero RPC errors. Exit code 1 otherwise.

## Data Structures

### main.go

```go
// blockWork is sent from streamLoop to workers.
type blockWork struct {
    blockNumber uint64
    grpcFrames  []*pb.CallFrame // bct.Txs — one root frame per tx
}

// blockResult is sent from workers to the collector.
type blockResult struct {
    blockNumber uint64
    txCount     int
    frameCount  int    // total frames across all levels (recursive)
    logCount    int
    mismatches  []string
    rpcError    bool
}
```

### rpc.go

```go
// rpcResult is one element of the debug_traceBlockByNumber response array.
type rpcResult struct {
    Result rpcCallFrame `json:"result"`
}

type rpcCallFrame struct {
    Type         string         `json:"type"`
    From         string         `json:"from"`
    To           string         `json:"to,omitempty"`
    Value        string         `json:"value,omitempty"`
    Gas          string         `json:"gas"`
    GasUsed      string         `json:"gasUsed"`
    Input        string         `json:"input"`
    Output       string         `json:"output,omitempty"`
    Error        string         `json:"error,omitempty"`
    RevertReason string         `json:"revertReason,omitempty"`
    Calls        []rpcCallFrame `json:"calls,omitempty"`
    Logs         []rpcLogFrame  `json:"logs,omitempty"`
}

// Position and Index use *uint64 to distinguish "field absent" from "field = 0".
type rpcLogFrame struct {
    Address  string   `json:"address"`
    Topics   []string `json:"topics,omitempty"`
    Data     string   `json:"data"`
    Position *uint64  `json:"position,omitempty"`
    Index    *uint64  `json:"index,omitempty"`
}
```

## Module Design

### main.go

Responsibilities: CLI parsing, gRPC client setup, launching goroutines, collecting results, printing summary.

```go
func main()
// Parses flags, validates n > 0, dials gRPC, starts streamLoop and workers,
// collects n blockResults, cancels context, waits for shutdown, prints summary.

func streamLoop(ctx context.Context, grpcAddr string, workCh chan<- blockWork, n int)
// Connects to gRPC, reads Notification stream.
// On ChainCommitted: iterates chain.CallTraces; for each bct with len(bct.Txs) > 0
// sends blockWork to workCh. After sending n items, closes workCh via defer and returns.
// On stream error or ctx cancellation: logs and returns (defer close(workCh) still runs).

func workerLoop(ctx context.Context, rpcURL string, workCh <-chan blockWork, resultCh chan<- blockResult, wg *sync.WaitGroup)
// Reads blockWork from workCh until closed, calls fetchRPCTraces + compareBlock,
// sends blockResult to resultCh. Calls wg.Done() on return.
```

Constants:
```go
const maxRecvMsgSize = 64 * 1024 * 1024  // matches go-consumer
const workChBuffer   = 32
const rpcRetryDelay  = 500 * time.Millisecond
```

### rpc.go

Responsibilities: HTTP JSON-RPC call, retry, response decoding.

```go
func fetchRPCTraces(ctx context.Context, rpcURL string, blockNumber uint64) ([]rpcCallFrame, error)
// Calls doRPCCall once. On error, waits rpcRetryDelay and calls once more.
// Returns error only if both attempts fail.

func doRPCCall(ctx context.Context, rpcURL string, blockNumber uint64) ([]rpcCallFrame, error)
// Encodes request body, POSTs to rpcURL, decodes JSON response array.
// blockNumber formatted as "0x" + strconv.FormatUint(n, 16).
// Returns error with context on any HTTP or JSON decode failure.
```

### compare.go

Responsibilities: normalization and comparison. All functions are pure (no I/O, no global state).

```go
func compareBlock(blockNum uint64, grpcFrames []*pb.CallFrame, rpcFrames []rpcCallFrame) (mismatches []string, frameCount int, logCount int)
// Checks len equality first. On mismatch: returns one mismatch line, frameCount=0, logCount=0.
// Otherwise recurses into each tx pair via compareFrame.

func compareFrame(path string, grpc *pb.CallFrame, rpc rpcCallFrame) (mismatches []string, frameCount int, logCount int)
// Compares all scalar fields, then calls count. On calls count mismatch: one mismatch, no recursion.
// On logs count mismatch: one mismatch for logs, continues with calls.
// Recurses into calls and logs.

func compareLog(path string, grpc *pb.CallLogFrame, rpc rpcLogFrame) []string
// Compares address, each topic, data, position (has_position semantics), index.

// Normalization functions — all pure, no side effects:
func normalizeAddress(b []byte) string         // 20 bytes → "0x<40 hex>"; nil/empty → ""
func normalizeU256grpc(b []byte) string        // 32-byte big-endian → strip leading zeros → "0x<hex>"; nil/empty → ""; all-zero → "0x0"
func normalizeU256rpc(s string) string         // "0x<hex>" → strip leading zeros; "" → ""
func normalizeBytes(b []byte) string           // arbitrary bytes → "0x<hex>"; nil/empty → ""
func normalizeHexStr(s string) string          // lowercase "0x<hex>"; "" → ""
func countFrames(f *pb.CallFrame) int          // recursive count including root
func countLogs(f *pb.CallFrame) int            // recursive count of all logs
```

## Field Comparison Rules

| Field | gRPC encoding | RPC encoding | Normalization | Match condition |
|-------|--------------|--------------|---------------|-----------------|
| `typ` | string | string | none | exact string equality |
| `from` | 20 bytes | `"0x..."` lowercase | `normalizeAddress` | string equality |
| `to` | 20 bytes or nil (CREATE) | `"0x..."` or `""` (omitempty) | `normalizeAddress` | string equality; both `""` = match |
| `value` | 32 bytes big-endian or nil | `"0x..."` or `""` (omitempty) | `normalizeU256grpc` / `normalizeU256rpc` | string equality after strip |
| `gas` | 32 bytes big-endian | `"0x..."` | same as value | string equality |
| `gasUsed` | 32 bytes big-endian | `"0x..."` | same as value | string equality |
| `input` | []byte | `"0x..."` | `normalizeBytes` / `normalizeHexStr` | string equality |
| `output` | []byte or nil | `"0x..."` or `""` | `normalizeBytes` / `normalizeHexStr` | string equality; both `""` = match |
| `error` | string or `""` | string or `""` (omitempty) | none | direct equality |
| `revertReason` | string or `""` | string or `""` (omitempty) | none | direct equality |
| `calls` | slice | slice | — | count match first, then recurse |
| `logs` | slice | slice | — | count match first, then compare each |
| log `address` | 20 bytes or nil | `"0x..."` | `normalizeAddress` | string equality |
| log `topics[i]` | 32 bytes | `"0x..."` | `normalizeBytes` / `normalizeHexStr` | string equality |
| log `data` | []byte or nil | `"0x..."` | `normalizeBytes` / `normalizeHexStr` | string equality |
| log `position` | `has_position` + `position` | `*uint64` | — | rpc nil ↔ `has_position=false`; rpc non-nil ↔ `has_position=true` + value match |
| log `index` | `has_index` + `index` | `*uint64` | — | same pattern as position |

**Special rule — `value` zero vs absent:**
gRPC encodes `Option<U256>::None` as nil/empty bytes; `Some(U256::ZERO)` as 32 zero bytes.
- nil → `normalizeU256grpc` → `""`
- 32×`0x00` → `normalizeU256grpc` → `"0x0"`

RPC omits `value` for non-value calls (STATICCALL etc.) and for zero value in some implementations.
- absent → `""` → `normalizeU256rpc` → `""`
- `"0x0"` → `normalizeU256rpc` → `"0x0"`

These two cases are distinct and treated as a real discrepancy if they disagree.

## Concurrency Model

```
main goroutine
  │
  ├─ launches streamLoop goroutine
  ├─ launches W workerLoop goroutines (tracked by sync.WaitGroup)
  └─ reads resultCh until n results received, then cancels ctx

workCh   chan blockWork    buffer = workChBuffer (32)
resultCh chan blockResult  buffer = W * 2

streamLoop:
  - reads gRPC stream; on ChainCommitted with non-empty Txs → workCh <- blockWork
  - after sending n items: closes workCh via defer close(workCh) and returns
  - on ctx cancellation or stream error: defer close(workCh) still runs, workers drain and exit

workerLoop (W goroutines):
  - for work := range workCh { fetchRPCTraces → compareBlock → resultCh <- result }
  - wg.Done() on return

main collector:
  - reads resultCh; counts results; when count == n: cancel ctx
  - wg.Wait() to drain workers
  - print per-block results and summary
```

**Shutdown ordering:** streamLoop sends n items → `defer close(workCh)` → workers drain remaining items → workers exit → wg.Wait() returns → main prints summary. If stream errors early, same defer fires and workers exit cleanly.

**Concurrency safety:** workCh and resultCh are the only shared state; both are Go channels. No shared mutable structs. Race-detector clean by construction.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| RPC first attempt fails | Log attempt failure, wait `rpcRetryDelay` (500ms), retry once |
| RPC second attempt fails | Send `blockResult{rpcError: true}`, log `RPC_ERROR block#N`, **counts toward n** (exit code 1) |
| RPC returns different tx count than gRPC | One mismatch line `"tx count: grpc=X rpc=Y"`, no frame recursion |
| calls or logs count mismatch | One mismatch line at parent path, skip recursion into children |
| gRPC stream recv error | Log error and return from streamLoop; in-flight workers finish naturally |
| CLI n ≤ 0 | Print usage, exit 2 |

## Dependencies

| Dependency | Version | Reason |
|-----------|---------|--------|
| `google.golang.org/grpc` | v1.81.0 | gRPC client (matches go-consumer) |
| `google.golang.org/protobuf` | v1.36.11 | proto types (matches go-consumer) |

No new external dependencies beyond what go-consumer already uses.

## Alternative Approaches

**Sequential processing (no workers):** Simpler but risks stream backpressure on heavy blocks where `debug_traceBlockByNumber` takes > 12s. Rejected in favor of buffered channel + workers.

**Sharing proto/gen via replace directive:** Avoids file duplication but couples the two modules and requires go workspace or replace hacks. Rejected; copying is simpler and the files rarely change.

## Known Limitations

1. `ChainReorged` new-chain blocks are skipped — they would be valid to verify but are out of scope per requirements.
2. RPC errors count toward `n` and trigger exit code 1. In a slow-RPC environment, the program may spend extra time on retries before counting the block.
3. Traces are compared against RPC at the time of the gRPC notification. In the unlikely event of a chain reorg between notification and RPC call, the RPC may return a different block. This is not handled.

## Test Plan

### Unit tests (compare_test.go) — no network, no gRPC

All tests construct `*pb.CallFrame` / `rpcCallFrame` by hand and call comparison or normalization functions directly.

| Test name | Scenario | Input | Expected |
|-----------|----------|-------|----------|
| `TestNormalizeU256grpc_nil` | nil bytes | `nil` | `""` |
| `TestNormalizeU256grpc_zero` | 32 zero bytes | `make([]byte, 32)` | `"0x0"` |
| `TestNormalizeU256grpc_strip` | value with leading zeros | `[0,0,...,0,1]` (32 bytes) | `"0x1"` |
| `TestNormalizeU256rpc_absent` | absent RPC field | `""` | `""` |
| `TestNormalizeU256rpc_zero` | RPC zero | `"0x0"` | `"0x0"` |
| `TestNormalizeU256rpc_strip` | leading zeros | `"0x000001"` | `"0x1"` |
| `TestNormalizeAddress_nil` | nil | `nil` | `""` |
| `TestNormalizeAddress_valid` | 20-byte address | `[0xab, 0xcd, ...(20 bytes)]` | `"0xabcd..."` lowercase, no strip |
| `TestCompareFrame_pass` | matching CALL frame, no subcalls/logs | identical fields both sides | 0 mismatches |
| `TestCompareFrame_typ_mismatch` | type differs | grpc `"CALL"`, rpc `"STATICCALL"` | 1 mismatch containing `"typ"` |
| `TestCompareFrame_gasUsed_mismatch` | gasUsed differs | grpc 32-byte encoding of 0x5208, rpc `"0x5209"` | 1 mismatch containing `"gasUsed"` |
| `TestCompareFrame_to_absent_both` | CREATE: both absent | grpc `To=nil`, rpc `To=""` | 0 mismatches |
| `TestCompareFrame_value_zero_vs_nil` | gRPC zero value, RPC absent | grpc `Value=32×0x00`, rpc `Value=""` | 1 mismatch (real discrepancy) |
| `TestCompareFrame_value_nil_nil` | both absent | grpc `Value=nil`, rpc `Value=""` | 0 mismatches |
| `TestCompareFrame_calls_count_mismatch` | different subcall counts | grpc 2 calls, rpc 1 call | 1 mismatch, no recursion |
| `TestCompareFrame_recursive_pass` | nested call matches | grpc+rpc each have 1 matching subcall | 0 mismatches |
| `TestCompareFrame_recursive_mismatch` | nested call typ differs | grpc subcall `"CALL"`, rpc `"DELEGATECALL"` | 1 mismatch path contains `.calls[0].typ` |
| `TestCompareLog_pass` | all log fields match | same address/topics/data/position/index | 0 mismatches |
| `TestCompareLog_topic_mismatch` | topic differs | topic[0] grpc `0xaaa...` rpc `0xbbb...` | 1 mismatch path contains `.topics[0]` |
| `TestCompareLog_position_rpc_nil_grpc_set` | gRPC has position, RPC doesn't | grpc `HasPosition=true,Position=3`, rpc `Position=nil` | 1 mismatch |
| `TestCompareLog_position_match` | both have same position | grpc `HasPosition=true,Position=3`, rpc `Position=&3` | 0 mismatches |
| `TestCompareLog_index_rpc_present_grpc_absent` | RPC has index, gRPC doesn't | grpc `HasIndex=false`, rpc `Index=&0` | 1 mismatch |
| `TestNormalizeBytes_nil` | nil input | `nil` | `""` |
| `TestNormalizeBytes_nonempty` | non-empty bytes | `[]byte{0xde, 0xad}` | `"0xdead"` |
| `TestNormalizeHexStr_empty` | empty string | `""` | `""` |
| `TestNormalizeHexStr_uppercase` | mixed-case hex | `"0xDEAD"` | `"0xdead"` |
| `TestCompareBlock_tx_count_mismatch` | tx counts differ | grpc 3 frames, rpc 2 results | 1 mismatch, no frame recursion |
| `TestCompareBlock_all_pass` | 2 matching CALL frames, no subcalls, no logs | identical scalar fields both sides | 0 mismatches, frameCount=2, logCount=0 |

### Integration verification (remote node)

| AC | Command | Expected |
|----|---------|----------|
| AC1 | `go build ./...` in `examples/go-trace-verifier/` | exit 0 |
| AC2 | `go test ./...` in `examples/go-trace-verifier/` | all pass |
| AC3 | `go run . 10` (remote) | 10 blocks PASS, exit 0 |
| AC4 | `go run . 50` (remote) | 50 blocks PASS, exit 0 |
