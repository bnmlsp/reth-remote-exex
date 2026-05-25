# Latency Bench — Requirements

## Background

ExEx is an in-process reth hook that fires immediately after the execution engine commits a chain segment, theoretically notifying consumers before the JSON-RPC/WS layer does. This tool quantifies that latency difference in practice.

## Goal

A command-line tool that concurrently subscribes to both `eth_subscribe("newHeads")` over WebSocket and the ExEx gRPC server, matches notifications by block number, prints the per-block arrival time delta at millisecond precision, and exits after N matched blocks.

## Functional Requirements

**FR1.** Establish two concurrent connections:
- WebSocket `eth_subscribe("newHeads")`
- ExEx gRPC `Subscribe` with `include_headers=true`

**FR2.** Match by block number: output one line only when both sides have arrived:
```
block=<N> ws=<T1>ms grpc=<T2>ms delta=<T1-T2>ms
```
Positive delta = gRPC arrived earlier; negative delta = WS arrived earlier.

**FR3.** CLI flags:
- `--ws`     WebSocket endpoint (default `ws://127.0.0.1:8546`)
- `--grpc`   gRPC endpoint (default `127.0.0.1:10000`)
- `--blocks` exit after N matched blocks (default `100`)

**FR4.** After N matched blocks, print summary and exit:
```
--- results ---
matched=N unmatched=M
delta min=Xms max=Xms avg=Xms
```

**FR5.** Single-side timeout: if only one side arrives within 10 s, mark the block as unmatched and skip it without blocking other blocks. Report `unmatched=M` in the summary.

## Non-Functional Requirements

- Timestamp precision: milliseconds (ms)
- Language: Go (consistent with other examples)
- No TLS, no authentication

## Out of Scope

- Latency histograms or distribution charts
- Persistent output (CSV/JSON)
- Multi-node comparison
- TLS / authentication

## Acceptance Criteria

| AC | Steps | Expected |
|----|-------|---------|
| AC1 | `cd examples/go-latency-bench && go build ./...` | Compiles without errors |
| AC2 | `go run . --blocks 3` (node online) | 3 lines of `block=N ws=...ms grpc=...ms delta=...ms`, then summary, then exit |
| AC3 | `go run . --ws ws://X --grpc Y --blocks 5` | Uses specified endpoints, exits after 5 blocks |
| AC4 | Observe delta sign | Positive when gRPC arrives first; negative when WS arrives first |
| AC5 | Disconnect one side, wait 10 s | That block marked unmatched, no deadlock, other blocks continue |
| AC6 | `go run . --ws ws://invalid` | Prints error, exits non-zero |
| AC7 | `go run . --grpc invalid:99999` | Prints error, exits non-zero |
| AC8 | `go run . --blocks 0` | Prints empty summary, exits 0 immediately |
