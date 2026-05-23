# Design: reth ExEx gRPC Internal Transactions Stream

## Interface Definition

No new RPC methods. The existing `RemoteExEx.Subscribe` stream is unchanged. The `Chain` message gains one new field that carries call trace data.

### Proto changes (`proto/exex.proto`)

```protobuf
// Modified — new field appended:
message Chain {
  repeated BlockWithReceipts blocks      = 1;
  StateDiff                  state_diff  = 2;
  repeated BlockCallTraces   call_traces = 3;  // NEW: one entry per block
}

// New messages:
message BlockCallTraces {
  uint64             block_number = 1;
  repeated CallFrame txs          = 2;  // one root CallFrame per transaction, same order as Block.txs
}

message CallFrame {
  string typ           = 1;   // e.g. "CALL", "STATICCALL", "DELEGATECALL", "CREATE", "CREATE2"
  bytes  from          = 2;   // Address 20 bytes
  bytes  to            = 3;   // Address 20 bytes; empty = CREATE (no recipient)
  bytes  value         = 4;   // U256 32 bytes big-endian; empty = absent (non-value call)
  bytes  gas           = 5;   // U256 32 bytes big-endian
  bytes  gas_used      = 6;   // U256 32 bytes big-endian
  bytes  input         = 7;
  bytes  output        = 8;   // empty = absent (call failed with no return data)
  string error         = 9;   // empty = absent
  string revert_reason = 10;  // empty = absent
  repeated CallFrame    calls = 11;  // nested child frames (recursive)
  repeated CallLogFrame logs  = 12;
}

message CallLogFrame {
  bytes          address      = 1;  // Address 20 bytes; empty = absent
  repeated bytes topics       = 2;  // each B256 32 bytes; empty list = absent
  bytes          data         = 3;  // empty = absent
  bool           has_position = 4;
  uint64         position     = 5;
  bool           has_index    = 6;
  uint64         index        = 7;
}
```

**Backward compatibility:** `call_traces` defaults to an empty list. Existing consumers that ignore the field require no changes.

## Data Structures

### Encoding Conventions (additions)

| Rust type | Proto type | Encoding |
|-----------|-----------|----------|
| `CallFrame.typ: String` | `string` | UTF-8, e.g. `"CALL"` |
| `Option<Address>` | `bytes` | 20 bytes if present; empty bytes if absent |
| `Option<U256>` | `bytes` | 32 bytes big-endian if present; empty bytes if absent |
| `Option<Bytes>` | `bytes` | raw bytes if present; empty bytes if absent |
| `Option<String>` | `string` | string value if present; empty string if absent |
| `Option<Vec<B256>>` (topics) | `repeated bytes` | list of 32-byte entries; empty list if absent |
| `Option<u64>` (position/index) | `bool has_xxx` + `uint64 xxx` | same pattern as existing `has_base_fee_per_gas` etc. |

### Source → Proto Mapping

```
alloy_rpc_types_trace::geth::CallFrame     →  proto::CallFrame
  .typ: String                             →  .typ
  .from: Address                           →  .from (20 bytes)
  .to: Option<Address>                     →  .to (20 bytes or empty)
  .value: Option<U256>                     →  .value (32 bytes or empty)
  .gas: U256                               →  .gas (32 bytes)
  .gas_used: U256                          →  .gas_used (32 bytes)
  .input: Bytes                            →  .input
  .output: Option<Bytes>                   →  .output (bytes or empty)
  .error: Option<String>                   →  .error (string or "")
  .revert_reason: Option<String>           →  .revert_reason (string or "")
  .calls: Vec<CallFrame>                   →  .calls (recursive)
  .logs: Vec<CallLogFrame>                 →  .logs

alloy_rpc_types_trace::geth::CallLogFrame  →  proto::CallLogFrame
  .address: Option<Address>               →  .address (20 bytes or empty)
  .topics: Option<Vec<B256>>              →  .topics (repeated bytes or empty list)
  .data: Option<Bytes>                    →  .data (bytes or empty)
  .position: Option<u64>                  →  .has_position + .position
  .index: Option<u64>                     →  .has_index + .index
```

## Module Layout

Only `convert.rs` changes. `exex.rs`, `lib.rs`, and `exex.proto` (structure only, no logic) also change.

### `src/convert.rs` additions

Three new pure functions appended to the existing file:

```
call_traces_to_proto(chain: &Chain) -> Vec<proto::BlockCallTraces>
  Reads chain.call_traces() (Option<&BTreeMap<BlockNumber, Vec<CallFrame>>>).
  Returns empty vec if None (pipeline blocks or reorg old-chain).
  For reorg old-chain, None is guaranteed by the fork: blocks_to_chain() is called
  with include_traces: false for old-chain segments, so call_traces is never populated.
  Iterates the BTreeMap and calls call_frame_to_proto() for each CallFrame.
  The fork guarantees BTreeMap keys correspond 1:1 to blocks in the chain; no extra entries are expected.

call_frame_to_proto(f: &CallFrame) -> proto::CallFrame
  Maps all fields. Recurses into f.calls via call_frame_to_proto().
  Recurses into f.logs via call_log_frame_to_proto().

call_log_frame_to_proto(l: &CallLogFrame) -> proto::CallLogFrame
  Maps all Option fields. Uses has_xxx pattern for position and index.
```

`chain_to_proto()` is updated to call `call_traces_to_proto(chain)` and include the result in the returned `proto::Chain`.

### Go consumer changes

Two files change on the Go side:

**`examples/go-consumer/proto/gen/exex.pb.go`** — must be regenerated from the updated `proto/exex.proto` using `protoc`. Without regeneration the Go struct for `Chain` will not have the `CallTraces` field and consumers will silently receive no call trace data even if the server sends it. Regeneration command (run from `proto/`):

```bash
protoc --go_out=../examples/go-consumer/proto/gen --go_opt=paths=source_relative \
       --go-grpc_out=../examples/go-consumer/proto/gen --go-grpc_opt=paths=source_relative \
       exex.proto
```

**`examples/go-consumer/main.go`** — `printChain()` is extended to iterate `chain.CallTraces` and recursively print each `CallFrame` tree (typ, from, to, value, gas_used, nested calls, logs).

## Dependencies

### `Cargo.toml` changes

| Change | Detail |
|--------|--------|
| All `reth-*` git sources | `paradigmxyz/reth tag v2.2.0` → `bnmlsp/reth branch dev-exex-internal-txs` |
| `reth-execution-types` features | Add `features = ["traces"]` to enable `Chain::call_traces()` |
| New crate | `alloy-rpc-types-trace = "2.0.4"` — provides `CallFrame` and `CallLogFrame` types |

`Cargo.lock` will be fully regenerated due to the git source change.

## Concurrency and Ownership

No changes to concurrency model. `call_traces_to_proto()` is a pure function called inside `notification_to_proto()`, which is already called synchronously in the per-subscriber `tokio::spawn` task. No shared mutable state is introduced.

## Alternative Approaches

**Place `call_traces` inside `BlockWithReceipts` instead of `Chain`:**
The source data is `BTreeMap<BlockNumber, Vec<CallFrame>>` on the `Chain` object, keyed by block number. Placing it inside each `BlockWithReceipts` would require a lookup per block during serialization and restructure the ownership. Placing it on `Chain` (as `repeated BlockCallTraces`) mirrors the source structure directly with zero restructuring, and `block_number` on `BlockCallTraces` makes the association explicit. This approach is simpler.

## Design Decisions

### Recursion depth in `call_frame_to_proto` and `printCallFrame`

Both functions use unbounded recursion over the `CallFrame.calls` tree. No depth limit is enforced. This is intentional: EVM enforces a hard call depth limit of 1024, so the actual maximum recursion depth is bounded. Rust's default 8 MB stack and Go's dynamically growing goroutine stack both handle 1024 levels of simple struct recursion without issue. Adding a depth parameter would introduce complexity without addressing a real risk.

## Known Limitations

| # | Limitation |
|---|------------|
| 1 | Old-chain blocks in `ChainReorged` carry no traces (those blocks are not re-executed) |
| 2 | Historical (pipeline) blocks carry no traces |
| 3 | Traces are not persisted; lost on node restart |
| 4 | `call_traces` ordering within a block follows `BTreeMap` key order (block number); within a block, `txs` order follows the original transaction order as stored by the fork. This ordering is assumed from the fork's implementation (`blocks_to_chain` collects `ExecutedBlock.call_traces` in block execution order) but is not formally documented — it must be verified by the AC2 integration test case |

## Test Plan

### AC1 — tx count matches

| Case | Input | Expected |
|------|-------|----------|
| Normal block with N transactions | `ChainCommitted` notification for a block with N txs | `call_traces[block_number].txs` has exactly N entries |
| Empty block (0 transactions) | `ChainCommitted` for a block with 0 txs | `call_traces` entry for that block is present with `txs = []` (Engine path produces `Some(vec![])` for the block, so the BTreeMap entry exists) |

### AC2 — from address matches sender

| Case | Input | Expected |
|------|-------|----------|
| Block with multiple txs | `ChainCommitted` for a block with txs at indices 0..N-1 | For each i, `call_traces[block].txs[i].from == block.senders[i]` (both as 20-byte hex) |

### AC3 — reorg chain traces

| Case | Input | Expected |
|------|-------|----------|
| Reorg with new chain having txs | `ChainReorged` notification | New chain `call_traces` is non-empty |
| Reorg old chain | Same `ChainReorged` notification | Old chain `call_traces` is empty list |

### AC4 — Go consumer compiles and prints

| Case | Input | Expected |
|------|-------|----------|
| Build check | Run `go build ./...` in `examples/go-consumer/` | Exits 0, no errors |
| Runtime print | Consumer connected, block committed | `call_traces` section printed to stdout for the received notification |

### AC5 — correctness vs RPC

| Case | Input | Expected |
|------|-------|----------|
| Single tx block | Block with 1 tx; compare consumer output vs `debug_traceBlockByNumber` | `typ`, `from`, `to`, `value`, `gasUsed`, nested `calls` structure identical |
| Multi-tx block | Block with multiple txs | Each tx's trace fields and nested call tree match RPC output at same index |
| Failed tx | Block containing a reverted tx | `error` or `revert_reason` field matches RPC output; nested calls still present |
| CREATE tx | Block containing a contract deployment | `to` is empty in both consumer output and RPC output |
| STATICCALL / non-value call | Block containing a tx whose root or nested call has no ETH transfer (`value` absent) | Consumer renders `value` as empty/absent; RPC omits the `value` field; both treated as equivalent |

### Error / edge paths

| Case | Input | Expected |
|------|-------|----------|
| Pipeline (historical) block | ExEx receives block from sync pipeline | `call_traces` is empty list (not panic) |
| Deeply nested calls | Block with tx that has many levels of nested calls | All levels serialized; no stack overflow (recursive proto encoding) |

## ADRs

### ADR-1: Switch reth dependency to `bnmlsp/reth` fork branch

**Status:** Adopted

**Background:** `Chain::call_traces()` and the `TracingInspector` injection exist only in the `bnmlsp/reth dev-exex-internal-txs` branch. The upstream `paradigmxyz/reth v2.2.0` does not expose this data.

**Decision:** Change all `reth-*` git sources from `paradigmxyz/reth tag v2.2.0` to `bnmlsp/reth branch dev-exex-internal-txs`.

**Rationale:** There is no alternative: the data simply does not exist in the upstream release. The fork has been compiled and run successfully on the deployment machine. All alloy dependency versions (2.0.4) are identical between the fork and the current lockfile.

**Consequences:** The binary now tracks a git branch rather than an immutable tag. Future upstream reth upgrades require the fork branch to be rebased first before this repo can follow. `Cargo.lock` will change completely.

### ADR-2: Add `alloy-rpc-types-trace` as a direct dependency

**Status:** Adopted

**Background:** `CallFrame` and `CallLogFrame` types are in this crate. Currently `convert.rs` does not depend on it.

**Decision:** Add `alloy-rpc-types-trace = "2.0.4"` to `Cargo.toml`.

**Rationale:** The types must be named explicitly to write the conversion functions. Version 2.0.4 is already present in the lockfile (transitive dep) and matches the rest of the alloy 2.x crate family already used.

**Consequences:** One new explicit dependency. No version conflict risk since the version was already resolved.
