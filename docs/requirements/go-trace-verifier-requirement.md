# Requirements: go-trace-verifier

## Background

The ExEx gRPC push server (v1.1, commit bb9d0c7) delivers call traces for each committed block via the `call_traces` field on the `Chain` message. The existing correctness check only verified root-frame fields and total frame count for a single block. Nested frame field content, logs, `error`, and `revert_reason` were not compared. A systematic, automated tool is needed to verify correctness across many blocks.

## Goal

Build a standalone Go program that subscribes to the ExEx gRPC stream, and for each block with call traces immediately fetches the ground-truth data from `debug_traceBlockByNumber` RPC, performs a full recursive field-by-field comparison, and reports any discrepancies.

## Functional Requirements

| ID | Priority | Requirement |
|----|----------|-------------|
| F1 | Must | The program subscribes to the gRPC stream and processes `ChainCommitted` notifications that contain non-empty call traces. `ChainReorged` and `ChainReverted` are skipped. |
| F2 | Must | For each qualifying block, the program immediately calls `debug_traceBlockByNumber` with `callTracer` on the configured RPC endpoint to obtain ground-truth trace data. |
| F3 | Must | The program compares all fields of all frames recursively: `typ`, `from`, `to`, `value`, `gas`, `gasUsed`, `input`, `output`, `error`, `revertReason`, nested `calls` (to any depth), and all `logs` fields (`address`, `topics` per entry, `data`, `position`, `index`). |
| F4 | Must | Any field mismatch is reported with its full path (e.g. `block#25151792 tx[2].calls[0].logs[1].topics[0]`) and both values. |
| F5 | Must | The program exits after verifying exactly `n` blocks (where `n` is a required CLI argument). It prints a summary of blocks verified, total frames, total logs, and total mismatches. |
| F6 | Must | Exit code 0 if all verified blocks pass with zero mismatches and zero RPC errors; exit code 1 otherwise. |
| F7 | Should | The program includes unit tests covering all normalization functions and comparison paths for boundary cases. |

## Non-Functional Requirements

| Category | Requirement |
|----------|-------------|
| Performance | Not required — development/verification tool only; no SLA |
| Concurrency | Stream reading and RPC comparison run in separate goroutines via a buffered channel to prevent stream backpressure |
| Security | No auth, no TLS — local development tool only |
| Compatibility | Independent Go module; no changes to existing files |
| Observability | Per-block PASS/MISMATCH/RPC_ERROR printed to stdout as results arrive |

## CLI Interface

```
go run . <n> [--grpc ADDR] [--rpc URL] [--workers W]

Arguments:
  n             Required. Number of blocks to verify before exiting.

Flags:
  --grpc ADDR   gRPC server address (default: 127.0.0.1:10000)
  --rpc  URL    Ethereum JSON-RPC URL (default: http://127.0.0.1:8545)
  --workers W   Number of parallel comparison workers (default: 4)
```

## Out of Scope

- `ChainReorged` / `ChainReverted` verification
- Long-running monitoring mode (no reconnect logic)
- `state_diff`, `receipts`, block header comparison
- opcode / memory / stack tracing fields

## Dependencies

- Existing proto-generated Go types from `examples/go-consumer/proto/gen/` (copied, not shared)
- Ethereum node with `debug_traceBlockByNumber` enabled on the RPC endpoint
- ExEx gRPC server running and streaming notifications

## Acceptance Criteria

| ID | Given | When | Then |
|----|-------|------|------|
| AC1 | Source files present | `go build ./...` run in `examples/go-trace-verifier/` | Exits 0, no errors |
| AC2 | Unit tests present | `go test ./...` run in `examples/go-trace-verifier/` | All tests pass |
| AC3 | Remote node running, gRPC streaming | `go run . 10` executed | 10 blocks verified, all PASS, exit 0 |
| AC4 | Remote node running, gRPC streaming | `go run . 50` executed | 50 blocks verified, all PASS, exit 0 |
