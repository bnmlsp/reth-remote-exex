# Requirements: reth ExEx gRPC Internal Transactions Stream

## Background

The existing gRPC push service (v1.0) streams block headers, transactions, receipts, and state diffs to external consumers in real time via ExEx. However, internal transactions (EVM call traces — the full tree of contract-to-contract calls within a single transaction) are not included in the pushed data.

The only alternative today is to call `debug_traceBlockByNumber` with `callTracer` on the JSON-RPC endpoint after the fact, which adds latency and requires a separate RPC roundtrip per block.

The `bnmlsp/reth` fork branch `dev-exex-internal-txs` extends reth's Engine execution path to inject a `TracingInspector` for each block and expose call traces via `Chain::call_traces()`. This requirement builds on top of that fork to push the call trace data through the existing gRPC stream.

## Goal

Extend the gRPC push service to include internal transaction data (EVM call traces) in each `ChainCommitted` and `ChainReorged` (new chain) notification, so that external consumers receive the full call tree for every transaction without any additional RPC calls.

## Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| F1 | When a `ChainCommitted` notification is pushed, it shall include call traces for every block in the committed chain, with one root `CallFrame` per transaction | Must |
| F2 | When a `ChainReorged` notification is pushed, the new chain portion shall include call traces; the old chain portion shall not include call traces (old blocks are not re-executed) | Must |
| F3 | When a `ChainReverted` notification is pushed, the old chain shall not include call traces | Must |
| F4 | Each `CallFrame` shall include the complete set of fields: call type (`typ`), `from`, `to`, `value`, `gas`, `gas_used`, `input`, `output`, `error`, `revert_reason`, nested child calls (`calls`, recursive), and per-call log entries (`logs`) | Must |
| F5 | The Go consumer example shall be updated to demonstrate how to receive and print call trace data | Should |

## Non-Functional Requirements

| Category | Decision |
|----------|----------|
| Performance | Inherits v1.0 end-to-end latency target (< 100 ms on same machine); tracing overhead is bounded by the `TracingInspector` minimal config (`with_record_calls(true)` only — no opcode, memory, or stack recording) provided by the upstream fork |
| Compatibility | The new `call_traces` field is a `repeated` message field; its default value is an empty list, so existing consumers that do not read the field require zero code changes |
| Security | No authentication or TLS; deployment must remain in a trusted network environment (inherits v1.0 limitation) |
| Scalability | Out of scope; single-node, single-process only |
| Observability | Inherits v1.0 logging (client connect / disconnect / notification sent) |
| Data retention | Not applicable; call traces are pushed and discarded, no persistence; traces are lost on node restart |

## Dependencies

| Dependency | Detail |
|------------|--------|
| reth fork | `bnmlsp/reth` branch `dev-exex-internal-txs` — provides `Chain::call_traces()` returning `Option<&BTreeMap<BlockNumber, Vec<CallFrame>>>` |
| `alloy-rpc-types-trace` | Version `2.0.4` — provides the `CallFrame` and `CallLogFrame` types used as source data |

## Out of Scope

- Call traces for old-chain blocks in `ChainReorged` (those blocks are not re-executed)
- Call traces for historical (pipeline) blocks
- Opcode-level, memory, or stack tracing
- Authentication and TLS
- Consumer-side reconnect logic
- Data persistence and replay

## Acceptance Criteria

| ID | Given / When / Then |
|----|---------------------|
| AC1 | Given a consumer is connected, when a `ChainCommitted` notification is received, then the number of `CallFrame` entries in `call_traces` for each block equals the number of transactions in that block |
| AC2 | Given a `ChainCommitted` notification is received, when iterating `call_traces` for a block, then each root `CallFrame.from` address equals the corresponding transaction sender address at the same index |
| AC3 | Given a `ChainReorged` notification is received, then the new chain's `call_traces` is non-empty (when the new chain has transactions), and the old chain's `call_traces` is empty |
| AC4 | Given the Go consumer binary is built from source, when connected to the gRPC server and a block is committed, then call trace data is printed to stdout without error |
| AC5 | Given a finalized block, when comparing the Go consumer's received `call_traces` for that block against `debug_traceBlockByNumber(blockNumber, {tracer: "callTracer"})`, then `typ`, `from`, `to`, `value`, `gas_used`, and the nested `calls` tree structure are identical for every transaction |
