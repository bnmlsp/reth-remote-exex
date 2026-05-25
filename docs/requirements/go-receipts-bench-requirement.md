# go-receipts-bench — Requirements

## Background

After the `reduce-exex-latency` optimization, ExEx gRPC latency is now on par with WS newHeads (~0 ms avg delta). The next question is: when a client only needs receipts data, which path delivers the complete receipts faster — ExEx gRPC (with `IncludeReceipts=true`) or WS newHeads followed by `eth_getBlockReceipts`?

This tool measures and compares the two paths head-to-head, per block, and reports the latency delta.

---

## Goal

Build a benchmark tool `go-receipts-bench` that simultaneously subscribes both data sources, matches arrivals by block number, and reports per-block and summary latency statistics. The tool must use the same patterns and conventions as the existing `go-latency-bench`.

---

## Functional Requirements

**FR1.** The tool must subscribe to the ExEx gRPC server with `SubscribeRequest{IncludeHeaders: true, IncludeReceipts: true}` (all other flags false). `IncludeHeaders` is required to obtain the block number for matching; `IncludeReceipts` is the data being benchmarked. For each `ChainCommitted` notification received, it must record the arrival time of the tip block's receipts as `grpcMs = time.Now().UnixMilli()` immediately after `stream.Recv()` returns.

**FR2.** The tool must subscribe to `eth_subscribe("newHeads")` via WebSocket. When a newHeads notification arrives for block N, it must immediately issue one `eth_getBlockReceipts` HTTP JSON-RPC call to the configured RPC endpoint. The arrival time is `rpcMs = time.Now().UnixMilli()` recorded immediately after the HTTP call returns, regardless of whether the returned receipts list is empty or non-empty. No retry is performed.

**FR3.** The tool must match gRPC and RPC arrivals by block number. When both sides have recorded an arrival for the same block, it must compute `delta = grpcMs - rpcMs` (negative = gRPC faster) and emit one output line per matched block.

**FR4.** The tool must accept a `--blocks N` flag (default 10). After N blocks have been matched, it must print a summary and exit.

**FR5.** Per-block output format must be:
```
block=<number> grpc=<ms>ms rpc=<ms>ms delta=<sign><ms>ms
```

**FR6.** Summary output format must be:
```
--- results ---
matched=<count> unmatched=<count>
delta min=<sign><ms>ms max=<sign><ms>ms avg=<sign><ms>ms
```

**FR7.** Blocks that do not achieve a match within 10 seconds on either side must be counted as unmatched and excluded from delta statistics.

**FR8.** The tool must accept the following CLI flags:
- `--blocks int` — number of blocks to match before exiting (default: 10)
- `--grpc string` — gRPC server address (default: `127.0.0.1:10000`)
- `--ws string` — WebSocket endpoint (default: `ws://127.0.0.1:8546`)
- `--rpc string` — HTTP JSON-RPC endpoint for `eth_getBlockReceipts` (default: `http://127.0.0.1:8545`)

---

## Non-Functional Requirements

**NFR1. Measurement accuracy** — Arrival times must be recorded with millisecond precision using `time.Now().UnixMilli()`, immediately after the relevant call returns, before any further processing.

**NFR2. No interference between paths** — The gRPC subscription and the WS+RPC path must run in separate goroutines. Neither path may block the other.

**NFR3. Backward compatibility** — This tool is a new binary; no existing tools or proto files are modified.

---

## Out of Scope

- Retry logic for `eth_getBlockReceipts` returning empty results
- Measuring latency of individual transactions or logs within a block
- Comparing data content (correctness verification is handled by `go-trace-verifier`)
- TLS or authentication for gRPC or RPC endpoints
- Reorg handling (reorg blocks are counted as unmatched if the opposing side never arrives within 10s)
- Performance under high load or stress testing

---

## Acceptance Criteria

| AC | Given | When | Then |
|----|-------|------|------|
| AC1 | Source code in `examples/go-receipts-bench/` | `go build ./...` | Exits 0, no errors |
| AC2 | reth running, gRPC server on `:10000`, RPC on `:8545`, WS on `:8546` | `go run . --blocks 5` | Prints 5 matched block lines + summary, then exits |
| AC3 | Both sources deliver block N | Arrival recorded | `delta = grpcMs - rpcMs` is correct; per-block line format matches FR5 |
| AC4 | `--blocks N` flag provided | N blocks matched | Tool exits after exactly N matched blocks |
| AC5 | One side does not arrive within 10s for block N | Timeout elapsed | Block counted as unmatched, excluded from delta stats |
| AC6 | Summary printed | After N matches + any timeouts | Format matches FR6; `matched + unmatched = total blocks seen` is consistent |

---

## Dependencies

- Proto generated Go files: copied to `examples/go-receipts-bench/proto/gen/` (same pattern as `go-trace-verifier`)
- `google.golang.org/grpc` — already used in all existing Go examples
- `github.com/gorilla/websocket` — already used in `go-latency-bench`
- No new external dependencies

---

## Change History

| Date | Summary | Reason | Impact |
|------|---------|--------|--------|
| 2026-05-25 | Initial version | Measure receipts latency: ExEx gRPC vs newHeads+getBlockReceipts | New tool |
| 2026-05-25 | FR1: add IncludeHeaders=true to SubscribeRequest | Block number is only present in the header field; required for block matching | FR1 scope expanded; no other impact |
