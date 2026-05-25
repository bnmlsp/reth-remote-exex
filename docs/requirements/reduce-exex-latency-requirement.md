# Reduce ExEx Latency — Requirements

## Background

Measurement shows WS `eth_subscribe("newHeads")` notifications arrive approximately 200 ms earlier than the ExEx gRPC subscription, even though both originate from the same `notify_canon_state()` broadcast in `engine/tree/src/tree/mod.rs`. The gap comes from the ExEx path routing through ExExManager, which introduces approximately 6 tokio async task yield points, versus the WS path which has approximately 2.

### Baseline Measurement (2026-05-25)

Measured on production server using `go-latency-bench --blocks 5`. Delta = `wsMs - grpcMs`; negative means WS arrived earlier.

```
block=25169797 ws=1779681684852ms grpc=1779681685118ms delta=-266ms
block=25169798 ws=1779681695954ms grpc=1779681696089ms delta=-135ms
block=25169799 ws=1779681709448ms grpc=1779681709768ms delta=-320ms
block=25169800 ws=1779681720814ms grpc=1779681721026ms delta=-212ms
block=25169801 ws=1779681732706ms grpc=1779681732764ms delta=-58ms
matched=5 unmatched=0
delta min=-320ms max=-58ms avg=-198ms
```

**Baseline: avg -198ms (WS ~200ms earlier than gRPC)**

## Goal

Reduce the per-block arrival time delta between WS newHeads and ExEx gRPC (i.e. `grpcMs - wsMs`) from ~200 ms to < 50 ms on average, by bypassing ExExManager and subscribing `canonical_state_stream()` directly inside the ExEx function — the same broadcast receiver the WS path already uses.

---

## Functional Requirements

**FR1.** The ExEx `remote_exex` function must subscribe to `canonical_state_stream()` directly via `ctx.provider().canonical_state_stream()`, instead of receiving notifications through `ctx.notifications` (the ExExManager-forwarded channel).

**FR2.** For each `CanonStateNotification::Commit` received from the stream, the ExEx must send `ExExEvent::FinishedHeight(tip.num_hash())` via `ctx.events`, so that reth can prune its WAL.

**FR3.** Each notification must be converted to `ExExNotification` via the existing `From<CanonStateNotification>` impl and broadcast to all connected gRPC subscribers, preserving the same semantics as before for `ChainCommitted` and `ChainReorged`.

**FR4.** The gRPC interface (`RemoteExEx.Subscribe`) must remain unchanged. No proto changes, no changes to `SubscribeRequest` flags, no changes to serialization logic in `convert.rs`.

**FR5.** Only `src/exex.rs` may be modified. No other source files are changed.

---

## Non-Functional Requirements

**NFR1. Latency** — After the change, the avg per-block delta (`grpcMs - wsMs`, measured by `go-latency-bench --blocks 10`) must be < 50 ms in absolute value on the production server.

**NFR2. Correctness** — Call traces and block data delivered to gRPC subscribers must remain identical to the current implementation, verified by `go-trace-verifier`.

**NFR3. Backward compatibility** — The gRPC API is unchanged. Existing clients require no modification.

---

## Known Limitations and Accepted Risks

**L1. Block gap on restart (known, accepted).**
After this change, ExExManager WAL replay is bypassed. When reth restarts, blocks produced during the downtime are not re-delivered to gRPC subscribers. Downstream clients must back-fill via RPC if needed.

*Accepted because:* This behavior is identical to WS `eth_subscribe("newHeads")`. This server is stateless; clients already handle reconnection.

**L2. `FinishedHeight` must still be sent (requires code attention).**
reth uses `ExExEvent::FinishedHeight` to prune its own WAL and old chain data. The replacement code must continue to send this signal from `CanonStateNotification::Commit { new }`. Omitting it would cause reth's WAL to grow unboundedly. This is explicitly handled in FR2 and in the design's replacement code.

**L3. `ChainReverted` not deliverable (no behavioral impact).**
`CanonStateNotification` has no `ChainReverted` variant. After this change, gRPC subscribers will never receive `ExExNotification::ChainReverted`.

*Accepted because:* The reth engine/tree path never emits `ChainReverted` during normal operation. This variant was only produced by ExExManager during WAL crash-recovery replay, which is removed by this change. There is no behavioral difference during normal block processing.

---

## Out of Scope

- Further latency optimizations beyond path change (e.g. tonic backpressure tuning, broadcast lag retry reduction, batched sends)
- Changes to gRPC proto schema
- Changes to `convert.rs` serialization logic
- TLS or authentication

---

## Acceptance Criteria

| AC | Given | When | Then |
|----|-------|------|------|
| AC1 | Modified code on remote server | `cargo build --release` | Compiles without errors or warnings |
| AC2 | reth running with new binary | `~/test/go-latency-bench --blocks 10` | `avg` delta (absolute value) < 50 ms |
| AC3 | reth running with new binary | `~/test/go-trace-verifier 10` | All 10 blocks pass, no mismatches reported |
| AC4 | New binary running | gRPC client subscribes and receives blocks | `ChainCommitted` and `ChainReorged` notifications arrive correctly; no regression in data content |

---

## Dependencies

- `reth_provider::CanonStateSubscriptions` trait — already available via `ctx.provider()` in `ExExContext`
- `From<CanonStateNotification> for ExExNotification` — already implemented in reth (`exex/types/src/notification.rs:64-71`)
- No new crate dependencies required

---

## Change History

| Date | Summary | Reason | Impact |
|------|---------|--------|--------|
| 2026-05-25 | Initial version | Baseline latency measured at ~200 ms avg; optimization planned | New feature |
