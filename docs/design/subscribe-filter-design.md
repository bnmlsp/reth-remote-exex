# Design: SubscribeRequest Field Filtering

## Background

The current `SubscribeRequest` message is empty. The server pushes all available data for every committed block. Clients that only need a subset of fields (e.g. only call traces) are forced to receive and deserialize the full payload. This design implements the requirements in `docs/requirements/subscribe-filter-requirement.md`.

## Alternative Approaches Considered

Three approaches were evaluated before selecting boolean flags. See ADR-001 at the end of this document for the full decision record.

| Approach | Filtering Granularity | Client Complexity | Server Complexity | Breaking Change |
|----------|-----------------------|-------------------|-------------------|-----------------|
| FieldMask (`repeated string fields`) | Any nested field | Medium (string paths, error-prone) | Medium (mask parsing + conditional fill) | No |
| Split RPCs (separate `Subscribe*` methods per data type) | Per data type | High (multiple streams, manual join by block number) | High (multiple handlers, shared state) | Yes |
| **Boolean flags (selected)** | Per top-level field group | Low (one stream, simple bool) | Low (O(1) if per flag) | Yes (intentional) |

## Interface Definition

### proto/exex.proto

```protobuf
message SubscribeRequest {
  bool include_headers      = 1;  // BlockWithReceipts.header
  bool include_transactions = 2;  // BlockWithReceipts.txs + senders (always bundled)
  bool include_receipts     = 3;  // BlockWithReceipts.receipts
  bool include_withdrawals  = 4;  // BlockWithReceipts.withdrawals
  bool include_state_diff   = 5;  // Chain.state_diff
  bool include_call_traces  = 6;  // Chain.call_traces
}
```

**Bundling rule**: `senders` (recovered sender addresses) are derived from `txs` signatures and have no independent meaning. They are always populated together with `txs` and share the `include_transactions` flag.

**Default semantic**: all flags default to `false` (proto3 zero value). A client sending an empty `SubscribeRequest` receives no data. This is an intentional explicit-subscription design.

### Rust: SubscribeFlags

```rust
pub struct SubscribeFlags {
    pub include_headers: bool,
    pub include_transactions: bool,
    pub include_receipts: bool,
    pub include_withdrawals: bool,
    pub include_state_diff: bool,
    pub include_call_traces: bool,
}
```

`SubscribeFlags` decouples the proto-generated `SubscribeRequest` type from the conversion layer — `convert.rs` should not depend on proto types directly. It is created once per client connection in `exex.rs`, wrapped in `Arc`, and cloned into the per-client spawn task. It is read-only after creation.

### go-consumer CLI

```
go run . [addr] [--headers] [--transactions] [--receipts] [--withdrawals] [--state-diff] [--call-traces]

Arguments:
  addr          gRPC server address (default: [::1]:10000)

Flags (all default false):
  --headers         subscribe to block headers
  --transactions    subscribe to transactions and senders
  --receipts        subscribe to receipts
  --withdrawals     subscribe to withdrawals
  --state-diff      subscribe to state diffs
  --call-traces     subscribe to call traces
```

## Data Structures

### Chain message (unchanged)

```protobuf
message Chain {
  repeated BlockWithReceipts blocks      = 1;
  StateDiff                  state_diff  = 2;
  repeated BlockCallTraces   call_traces = 3;
}
```

### BlockWithReceipts message (unchanged)

```protobuf
message BlockWithReceipts {
  BlockHeader          header      = 1;
  repeated Transaction txs         = 2;
  repeated bytes       senders     = 3;
  repeated Receipt     receipts    = 4;
  repeated Withdrawal  withdrawals = 5;
}
```

When a flag is `false`, the corresponding field is left at its proto3 zero value: `header = None`, repeated fields = empty list.

## Module Changes

### src/convert.rs

- Add `pub struct SubscribeFlags` at the top of the file.
- Change `pub fn notification_to_proto` signature to accept `flags: &SubscribeFlags`; propagate to `chain_to_proto` and `block_with_receipts_to_proto`.
- `chain_to_proto`: conditionally call `state_diff_to_proto` and `call_traces_to_proto` based on flags.
- `block_with_receipts_to_proto`: conditionally populate each field based on flags.
- Add `#[cfg(test)] mod tests` at the bottom covering flag filtering behavior.

No other functions in `convert.rs` are changed.

### src/exex.rs

- Add `use crate::convert::SubscribeFlags;`.
- In `subscribe()`: call `request.into_inner()` to extract flags, construct `Arc<SubscribeFlags>`, clone into spawn task.
- The spawn task calls `notification_to_proto(&notification, &flags)`.

### examples/go-consumer/main.go

- Replace the hardcoded empty `&pb.SubscribeRequest{}` with flag-parsed values.
- Use Go's `flag` standard library to parse `--headers`, `--transactions`, `--receipts`, `--withdrawals`, `--state-diff`, `--call-traces` boolean flags.
- No changes to `printNotification` or any other function.

### examples/go-trace-verifier/main.go

- Update `SubscribeRequest` to set `IncludeCallTraces: true`.
- No other changes.

### Proto-generated Go files

Both `examples/go-consumer/proto/gen/` and `examples/go-trace-verifier/proto/gen/` must be updated after `proto/exex.proto` changes:

```bash
protoc \
  --go_out=examples/go-consumer/proto/gen \
  --go-grpc_out=examples/go-consumer/proto/gen \
  --go_opt=paths=source_relative \
  --go-grpc_opt=paths=source_relative \
  -I proto proto/exex.proto

cp examples/go-consumer/proto/gen/exex.pb.go      examples/go-trace-verifier/proto/gen/
cp examples/go-consumer/proto/gen/exex_grpc.pb.go  examples/go-trace-verifier/proto/gen/
```

Rust proto types are regenerated automatically by `tonic-build` in `build.rs` during `cargo build`.

## Concurrency

`SubscribeFlags` is created once per client connection, wrapped in `Arc<SubscribeFlags>`, and shared read-only inside the per-client spawn task. No mutable shared state is introduced. Multiple concurrent clients each hold their own `Arc`, with independent flag sets.

## Known Limitations and Risks

- **Breaking change**: existing clients sending empty `SubscribeRequest` will receive no data. This is intentional and documented in the requirements.
- **Serialization only**: server-side computation (e.g. `TracingInspector`) runs regardless of flags. Skipping computation is out of scope.
- **No flag validation**: combinations such as `include_receipts = true` with `include_headers = false` are accepted without error. This is intentional (out of scope per requirements).

## Out of Scope

- Server-side computation control based on flags.
- Dynamic flag updates after subscription is established.
- Flag combination validation.

## Test Plan

### Compilation

| Case | Command | Expected |
|------|---------|----------|
| T0: Rust build | `cargo build` in repo root | Exit 0, no errors |

### Rust Unit Tests (`src/convert.rs`)

| Case | Input | Expected Output |
|------|-------|-----------------|
| T1: all flags false | `SubscribeFlags { all false }` + a block with all fields populated | `Chain { blocks: [BlockWithReceipts { header: None, txs: [], senders: [], receipts: [], withdrawals: [] }], state_diff: None, call_traces: [] }` |
| T2: all flags true | `SubscribeFlags { all true }` + same block | All fields populated, values match input |
| T3: include_headers only | `include_headers: true`, rest false | `header` populated, `txs/senders/receipts/withdrawals` empty, `state_diff: None`, `call_traces: []` |
| T4: include_transactions only | `include_transactions: true`, rest false | `txs` and `senders` populated, `header: None`, others empty |
| T5: include_receipts only | `include_receipts: true`, rest false | `receipts` populated, others empty |
| T6: include_withdrawals only | `include_withdrawals: true`, rest false | `withdrawals` populated, others empty |
| T7: include_state_diff only | `include_state_diff: true`, rest false | `state_diff` populated, `blocks` fields all empty, `call_traces: []` |
| T8: include_call_traces only | `include_call_traces: true`, rest false | `call_traces` populated, `blocks` fields all empty, `state_diff: None` |
| T9: senders bundled with txs | `include_transactions: true` | `senders` non-empty when `txs` non-empty |
| T10: senders absent without txs | `include_transactions: false` | `senders` is empty |

### Go Build and Unit Tests (regression)

| Case | Command | Expected |
|------|---------|----------|
| T11: go-consumer compiles | `go build ./...` in `examples/go-consumer/` | Exit 0 |
| T12: go-trace-verifier compiles | `go build ./...` in `examples/go-trace-verifier/` | Exit 0 |
| T13: go-trace-verifier unit tests | `go test ./...` in `examples/go-trace-verifier/` | All pass |

### Integration Tests (remote node, go-consumer)

| Case | Flags | Expected Observation |
|------|-------|----------------------|
| T14: call-traces only | `--call-traces` | `header = nil`, `txs = []`, `state_diff = nil`, `call_traces` non-empty |
| T15: full subscription | all 6 flags | All fields non-empty, behavior identical to pre-change |
| T17: headers only | `--headers` | `header` non-nil with valid block number, `txs = []`, `receipts = []`, `state_diff = nil`, `call_traces = 0 blocks` |
| T18: headers + transactions | `--headers --transactions` | `header` non-nil, `txs` and `senders` non-empty, `receipts = []`, `state_diff = nil`, `call_traces = 0 blocks` |
| T19: headers + call-traces | `--headers --call-traces` | `header` non-nil with valid block number, `txs = []`, `state_diff = nil`, `call_traces` non-empty |
| T20: state-diff only | `--state-diff` | `header = nil`, `txs = []`, `state_diff` non-nil with accounts/contracts, `call_traces = 0 blocks` |
| T21: headers + receipts (no txs) | `--headers --receipts` | `header` non-nil, `txs = []`, `receipts` non-empty, `state_diff = nil` |

### Integration Tests (remote node, go-trace-verifier)

| Case | Command | Expected |
|------|---------|----------|
| T16: call traces correctness | `go run . <grpc> <rpc> 50` | 50 blocks PASS, exit 0 |

---

## ADR-001: Boolean Flags over FieldMask and Split RPCs

**Status**: Accepted

**Background**: The server currently pushes all data to all clients. We need a filtering mechanism. Three approaches were considered: FieldMask, Split RPCs, and Boolean Flags.

**Decision**: Boolean flags added directly to `SubscribeRequest`.

**Reasoning**:

| Criterion | FieldMask | Split RPCs | Boolean Flags |
|-----------|-----------|------------|---------------|
| Client simplicity | Medium — string field paths, easy to mistype | Low — multiple streams, must join by block number | High — one stream, set a bool |
| Server simplicity | Medium — mask parsing, recursive fill logic | Low — multiple handlers, shared notification state | High — one `if` per flag |
| Proto change size | Small | Large (new service methods + messages) | Small |
| Backward compatibility | Yes (empty mask = full data) | No | No (intentional — explicit subscription) |
| Filtering granularity | Arbitrary nested fields | Per data type | Per top-level field group |
| Future extensibility | Highest | Medium | Medium — can migrate to FieldMask if per-field granularity needed |

The main trade-off accepted: FieldMask would allow finer granularity (e.g. header-only without txs) and would be non-breaking. It was rejected because the added client and server complexity is not justified by current requirements, and the explicit-subscription semantic is a deliberate product decision. If sub-field granularity is needed in the future, the flags can be replaced with FieldMask without breaking the wire format (proto field numbers are preserved).

**Consequences**:
- Existing clients break (intentional).
- Server implementation remains simple.
- Sub-field granularity (e.g. header without txs) requires a future change.
