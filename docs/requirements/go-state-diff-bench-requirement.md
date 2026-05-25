# Requirements: go-state-diff-bench — ExEx gRPC StateDiff vs newHeads+trace_replayBlockTransactions Latency Benchmark

## Background

go-internal-txs-bench confirmed that ExEx gRPC delivers call traces ~97ms faster than
newHeads+debug_traceBlockByNumber (avg). This tool performs the same comparison for state diffs:
gRPC push (IncludeStateDiff=true) vs WS newHeads + trace_replayBlockTransactions.

## Goal

Measure the end-to-end latency difference between ExEx gRPC state diff push and
WS newHeads + trace_replayBlockTransactions.

## Functional Requirements

**FR1 — gRPC subscription**
The tool shall connect to the specified gRPC address and subscribe with
`SubscribeRequest{IncludeHeaders: true, IncludeStateDiff: true}`.
On each ChainCommitted notification, record `grpcMs = time.Now().UnixMilli()` immediately
after `stream.Recv()` returns, use `New.Blocks[len-1].Header.Number` as the block number,
and use `len(chain.StateDiff.Accounts)` as the accounts count.

**FR2 — WS + RPC subscription**
The tool shall subscribe to `eth_subscribe("newHeads")` via WebSocket.
On each new head, immediately issue an HTTP POST to
`trace_replayBlockTransactions("0x<hex>", ["stateDiff"])`.
Record `rpcMs = time.Now().UnixMilli()` immediately after the HTTP response returns.
No retries.

**FR3 — Latency calculation**
`delta = grpcMs - rpcMs` (negative means gRPC is faster).

**FR4 — Block matching and exit**
Match gRPC and RPC arrivals by block number. After `--blocks N` matched pairs,
print the summary and exit 0.

**FR5 — Per-block output**
Print one line per matched block:
```
block=<N> accounts=<N> grpc=<N>ms rpc=<N>ms delta=<+/->Nms
```

**FR6 — Summary output**
Before exiting, print `matched=<N> unmatched=<N>` and delta min/max/avg with sign prefix.

**FR7 — Timeout handling**
If one side does not arrive within 10 seconds of the other, count the block as unmatched
and remove it from pending. Do not block subsequent matching.

**FR8 — CLI flags**
```
--blocks int     number of matched blocks before exit (default 10)
--grpc  string   gRPC endpoint (default 127.0.0.1:10000)
--ws    string   WebSocket endpoint (default ws://127.0.0.1:8546)
--rpc   string   HTTP JSON-RPC endpoint (default http://127.0.0.1:8545)
```

## Non-functional Requirements

- Performance: out of scope (single-run measurement tool)
- Security: insecure gRPC credentials (consistent with other bench tools); TLS out of scope
- Observability: out of scope
- All other NFRs: out of scope

## Out of Scope

- Parsing or counting accounts from the RPC response (format mismatch, no latency value)
- Retry logic
- Multi-node comparison
- Correctness verification of trace_replayBlockTransactions results

## Dependencies

- ExEx gRPC server with `IncludeStateDiff` flag support
- reth node with `trace` namespace enabled (`--http.api trace`)
- proto/gen identical to go-internal-txs-bench (shared RemoteExEx client)

## Acceptance Criteria

- **AC1**: `go build ./...` succeeds with no errors
- **AC2**: `go test ./...` passes, covering signPrefix, emit, getOrCreate
- **AC3**: `go run . --blocks 5` connects to a node, prints 5 per-block lines, then prints summary and exits
- **AC4**: Each per-block line contains `block=`, `accounts=`, `grpc=`, `rpc=`, `delta=` fields
- **AC5**: `--blocks 0` prints an empty summary and exits 0 immediately
- **AC6**: gRPC or WS connection failure prints error to stderr and exits 1
