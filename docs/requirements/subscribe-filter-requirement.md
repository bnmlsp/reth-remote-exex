# Requirements: SubscribeRequest Field Filtering

## Background

The current `SubscribeRequest` message is empty — the server pushes all available data for every committed block, including block headers, transactions, receipts, withdrawals, state diffs, and call traces. Consumers that only need a subset of these fields (e.g. only call traces) are forced to receive and deserialize all data, wasting bandwidth and CPU. This is particularly costly for `state_diff` and `call_traces`, which can be several megabytes per block.

## Goal

Allow clients to declare which data types they need at subscribe time. The server serializes and pushes only the requested fields. Clients that do not set any flag receive no data — explicit subscription is the intended semantic.

## Functional Requirements

| ID | Priority | Requirement |
|----|----------|-------------|
| F1 | Must | `SubscribeRequest` is extended with 6 boolean fields: `include_headers`, `include_transactions`, `include_receipts`, `include_withdrawals`, `include_state_diff`, `include_call_traces`. |
| F2 | Must | The server serializes only the fields whose corresponding flag is `true`. Fields with flag `false` are left empty (zero value / empty list / nil). |
| F3 | Must | All flags default to `false` (proto3 zero value). A client that sends no flags receives no data. This is an intentional explicit-subscription semantic. |
| F4 | Must | `senders` (recovered sender addresses) is always bundled with `txs`: when `include_transactions = true`, both `txs` and `senders` are populated; when `false`, both are empty. `senders` has no independent flag. |
| F5 | Must | `go-consumer` is updated to accept 6 CLI flags (`--headers`, `--transactions`, `--receipts`, `--withdrawals`, `--state-diff`, `--call-traces`). Each flag maps directly to the corresponding `SubscribeRequest` field. With no flags, nothing is subscribed. |
| F6 | Must | `go-trace-verifier` is updated to set only `include_call_traces = true` in its `SubscribeRequest`. |
| F7 | Must | `src/convert.rs` is extended with Rust unit tests covering: all flags = false, all flags = true, each flag individually = true with others false. |

## Non-Functional Requirements

| Category | Decision |
|----------|----------|
| Performance | Out of scope — flag evaluation is O(1) per notification and has no measurable overhead |
| Availability | Out of scope |
| Security | Out of scope |
| Scalability | Out of scope |
| Observability | Out of scope |
| Data retention | Out of scope |
| Compatibility | **Breaking change**: existing clients that send no flags will receive no data after this change. This is intentional. Clients must be updated to set the desired flags explicitly. No transition flag (e.g. `include_all`) will be provided. |

## Out of Scope

- Server-side computation control (e.g. skipping `TracingInspector` when `include_call_traces = false`) — this change controls serialization only, not computation.
- Dynamic flag updates after a subscription is established.
- Validation of flag combinations (e.g. `include_receipts = true` with `include_headers = false` is allowed).
- Reconnect logic in any client.

## Dependencies

- Proto-generated Go types must be regenerated after `proto/exex.proto` is updated.
- `go-trace-verifier` proto files are copied from `go-consumer` (existing pattern).
- Rust proto types are regenerated automatically by `tonic-build` in `build.rs` during `cargo build`.

## Acceptance Criteria

| ID | Given | When | Then |
|----|-------|------|------|
| AC1 | Updated source files | `cargo build` run in repo root | Exits 0, no errors |
| AC2 | Unit tests added to `src/convert.rs` | `cargo test` run in repo root | All tests pass; all-false, all-true, and per-flag scenarios covered |
| AC3 | Updated go-consumer | `go build ./...` run in `examples/go-consumer/` | Exits 0, no errors |
| AC4 | Updated go-trace-verifier | `go build ./...` run in `examples/go-trace-verifier/` | Exits 0, no errors |
| AC5 | go-trace-verifier unit tests unchanged | `go test ./...` run in `examples/go-trace-verifier/` | All tests pass |
| AC6 | Remote node running, gRPC streaming | go-consumer run with `--call-traces` only | Received `ChainCommitted` messages have `header = nil`, `txs = []`, `state_diff = nil`, `call_traces` non-empty |
| AC7 | Remote node running, gRPC streaming | go-consumer run with all 6 flags | All fields non-empty; behavior identical to pre-change full subscription |
| AC8 | Remote node running, gRPC streaming | go-trace-verifier run for 50 blocks | All 50 blocks PASS, exit 0 |
