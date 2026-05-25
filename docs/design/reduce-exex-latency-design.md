# Reduce ExEx Latency — Design

## Background

Baseline measurement: WS `eth_subscribe("newHeads")` arrives ~200 ms earlier than the ExEx gRPC subscription. Both originate from the same `notify_canon_state()` broadcast in `engine/tree/src/tree/mod.rs:2646`.

Root cause: the ExEx path routes through ExExManager, accumulating ~6 tokio async task yield points before the notification reaches `remote_exex`. The WS path subscribes `canonical_state_stream()` directly and has only ~2 yield points.

## Goal

Replace the ExExManager-forwarded notification source in `remote_exex` with a direct `canonical_state_stream()` subscription, matching the WS path. Target: avg delta < 50 ms.

---

## Module Layout

Only one file changes:

```
src/exex.rs   — remote_exex function: replace ctx.notifications with canonical_state_stream()
```

No changes to `src/convert.rs`, `src/lib.rs`, `proto/exex.proto`, or any Go examples.

---

## Interface Definition

The public gRPC interface is unchanged. `RemoteExEx.Subscribe` takes the same `SubscribeRequest` and returns the same `Notification` stream as before.

The internal `remote_exex` function signature is unchanged:

```rust
async fn remote_exex<Node: FullNodeComponents<Types: NodeTypes<Primitives = EthPrimitives>>>(
    mut ctx: ExExContext<Node>,
    notifications: Arc<broadcast::Sender<Arc<ExExNotification>>>,
) -> eyre::Result<()>
```

---

## Data Structures

No new data structures. The existing types used are:

| Type | Source | Role |
|------|--------|------|
| `CanonStateNotification` | `reth_chain_state` | Received from `canonical_state_stream()` |
| `ExExNotification` | `reth_exex` | Broadcast to gRPC subscribers; converted via `From` impl |
| `ExExEvent::FinishedHeight` | `reth_exex` | Sent to reth for WAL pruning |

---

## Implementation

### Current code (`src/exex.rs:82-88`)

```rust
use futures_util::TryStreamExt;
...
while let Some(notification) = ctx.notifications.try_next().await? {
    if let Some(committed_chain) = notification.committed_chain() {
        ctx.events.send(ExExEvent::FinishedHeight(committed_chain.tip().num_hash()))?;
    }
    let _ = notifications.send(Arc::new(notification));
}
```

### Replacement

```rust
use futures_util::StreamExt;
use reth_provider::CanonStateSubscriptions;
...
let mut stream = ctx.provider().canonical_state_stream();
while let Some(canon_notification) = stream.next().await {
    if let reth_chain_state::CanonStateNotification::Commit { new } = &canon_notification {
        ctx.events.send(ExExEvent::FinishedHeight(new.tip().num_hash()))?;
    }
    let notification = ExExNotification::from(canon_notification);
    let _ = notifications.send(Arc::new(notification));
}
```

**Import changes in `src/exex.rs`:**
- Remove: `use futures_util::TryStreamExt;`
- Add: `use futures_util::StreamExt;`
- Add: `use reth_provider::CanonStateSubscriptions;`

### Why this works

`ctx.provider()` returns `Node::Provider`, which implements `CanonStateSubscriptions` (verified in `reth/crates/exex/exex/src/context.rs:87-88` and `reth/crates/blockchain-tree/src/...`). `canonical_state_stream()` returns a `BroadcastStream` backed by the same `canon_state_notification_sender` that WS newHeads subscribes to (`reth/crates/chain-state/src/in_memory.rs:536-537`). The `From<CanonStateNotification> for ExExNotification` conversion is implemented in `reth/crates/exex/types/src/notification.rs:64-71`.

---

## Notification Path Comparison

| Step | Current path (via ExExManager) | New path (direct) |
|------|-------------------------------|-------------------|
| 1 | `notify_canon_state()` → broadcast send (sync) | same |
| 2 | ExEx launcher task: `canon_state_notifications.recv().await` ← **yield** | `BroadcastStream::poll_next()` ← **yield** |
| 3 | `ExExManagerHandle::send_async()` ← **yield** | `stream.next().await` ← **yield** |
| 4 | ExExManager loop: `handle_rx.recv()` ← **yield** | *(done)* |
| 5 | ExExManager: `self.sender.poll_reserve(cx)` ← **yield** | |
| 6 | `ExExNotifications::poll_recv(cx)` ← **yield** | |
| 7 | `ctx.notifications.try_next().await` ← **yield** | |
| **Total** | **~6 yield points** | **~2 yield points** |

---

## Risks and Accepted Trade-offs

**R1. Block gap on restart (known, accepted).**
After this change, ExExManager is bypassed and no WAL replay occurs. When reth restarts, gRPC clients disconnect and reconnect from the current chain head. Blocks produced during the restart window are not re-delivered; downstream clients must back-fill via RPC if needed.

*Accepted:* This behavior is identical to WS `eth_subscribe("newHeads")`. This server is stateless by design; clients already handle reconnection.

**R2. `FinishedHeight` must still be sent (requires code attention).**
reth uses `ExExEvent::FinishedHeight` to know when it is safe to prune its own WAL and old chain data. If this signal is not sent, reth's WAL grows unboundedly and reth's data pruning logic may stall.

After the change, `ctx.notifications` is no longer used, but `ctx.events` remains active. The replacement code must explicitly send `FinishedHeight` by extracting the tip block from `CanonStateNotification::Commit { new }`:

```rust
if let reth_chain_state::CanonStateNotification::Commit { new } = &canon_notification {
    ctx.events.send(ExExEvent::FinishedHeight(new.tip().num_hash()))?;
}
```

This is already included in the replacement code above. Omitting it would be a silent correctness bug.

**R3. `ChainReverted` never delivered (no behavioral impact during normal operation).**
`CanonStateNotification` has no `ChainReverted` variant; the `From` conversion maps only `Commit → ChainCommitted` and `Reorg → ChainReorged`. After bypassing ExExManager, `ChainReverted` has no remaining trigger.

*Accepted:* The reth engine/tree path never emits `ChainReverted` during normal block processing. This variant was only produced by ExExManager during WAL crash-recovery replay, which is removed by this change (see R1). There is no behavioral difference during normal operation.

**R4. Broadcast lag is not a real risk (negligible).**
`canonical_state_stream()` is backed by a broadcast channel with capacity 256. Processing one notification (type conversion + internal broadcast send) takes microseconds. Ethereum mainnet produces one block every ~12 seconds. The channel would need to accumulate 256 unprocessed blocks before any lag occurs — practically impossible under normal operation.

---

## Rollback Plan

This change touches only `src/exex.rs`. To roll back: revert the three import lines and the `remote_exex` function body to the previous version, rebuild, and redeploy. No data migration, no schema change, no config change required.

---

## Alternative Approaches

**Option A (chosen): Direct `canonical_state_stream()` subscription**
- Pro: minimal change (one function, three import lines), matches WS path exactly, ~4 fewer yield points
- Con: loses WAL crash-recovery, loses `ChainReverted` (both accepted per requirements)

**Option B: Keep ExExManager, tune channel capacities**
- Increase ExExManager's per-ExEx mpsc capacity from 1 to a larger value to reduce back-pressure stalls
- Pro: preserves WAL recovery
- Con: does not remove yield points; the fundamental scheduling delay of ~6 hops remains; unlikely to reach < 50 ms target

**Option C: Patch ExExManager to use unbounded channel / skip yield points**
- Modify reth fork to reduce ExExManager's async overhead
- Pro: preserves WAL recovery
- Con: requires changes to the reth fork (`bnmlsp/reth`), significantly higher scope and maintenance burden; this project's goal is to avoid fork changes

---

## Test Plan

### T1 — Compile (covers AC1)

| Scenario | Input | Expected |
|----------|-------|---------|
| Build release binary on remote server | `cargo build --release` | Exit 0, no errors, no warnings |

### T2 — Latency improvement (covers AC2, NFR1)

| Scenario | Input | Expected |
|----------|-------|---------|
| 10-block latency benchmark | `~/test/go-latency-bench --blocks 10` on remote server with new binary | `avg` delta (absolute value) < 50 ms |

### T3 — Correctness: call traces (covers AC3, NFR2)

| Scenario | Input | Expected |
|----------|-------|---------|
| 10-block trace verification | `~/test/go-trace-verifier 10` on remote server with new binary | All blocks pass, `0 mismatches` |

### T4 — ChainCommitted and ChainReorged delivery (covers AC4)

| Scenario | Input | Expected |
|----------|-------|---------|
| Normal block committed | gRPC client subscribed with `include_headers=true`, new block arrives | Client receives `ChainCommitted` notification with correct block header |
| Chain reorg | gRPC client subscribed, reth processes a reorg | Client receives `ChainReorged` notification with both old and new chain populated |

### T5 — Backward compatibility (covers NFR3)

| Scenario | Input | Expected |
|----------|-------|---------|
| Existing client, no code changes | An unmodified go-consumer client connects to the new binary with the same flags as before | Client connects successfully, receives notifications in the same format as before; no proto changes, no flag changes required |

### T6 — No regression on lag handling

| Scenario | Input | Expected |
|----------|-------|---------|
| Slow gRPC client | Client reads very slowly while blocks arrive | Server logs `gRPC client lagged, dropped N notifications`; other clients unaffected; server does not crash or block |

---

## Dependencies

No new crate dependencies. The following existing items are relied upon:

| Item | Location | Already used? |
|------|----------|---------------|
| `reth_provider::CanonStateSubscriptions` | reth crate | No — new import |
| `reth_chain_state::CanonStateNotification` | reth crate | No — new import |
| `futures_util::StreamExt` | `futures-util` crate | Already in `Cargo.toml` |
| `From<CanonStateNotification> for ExExNotification` | `reth/exex/types/src/notification.rs:64-71` | Implicit — now used explicitly |

---

## Future Optimizations

The following are not part of this change but are identified as the next candidates if further latency reduction is needed after this change is deployed and measured.

| # | Optimization | Estimated saving | Difficulty | Notes |
|---|-------------|-----------------|------------|-------|
| 1 | **Reduce broadcast lag retry in `CanonStateNotificationStream`** | ~2–5 ms | Medium | The `BroadcastStream` wrapper loops on `Lagged` errors before returning the next item. Replacing it with a raw `broadcast::Receiver` and handling `Lagged` explicitly eliminates the retry loop overhead. |
| 2 | **tonic backpressure tuning (`poll_send` instead of `send`)** | ~3–8 ms | Medium | The per-client mpsc `send` in the gRPC subscriber task yields when the channel is full. Switching to `poll_send` with a non-blocking path reduces queuing delay for fast clients. |
| 3 | **Batch / coalesce notifications before gRPC send** | ~5–10 ms | Low | If multiple notifications arrive in the same tokio scheduler tick, merging them into one gRPC message reduces round-trip overhead. Only beneficial during reorgs or rapid consecutive blocks. |

Combined potential: an additional ~10–23 ms reduction on top of this change, bringing avg delta toward ~20–30 ms.

### Latency floor analysis: can gRPC ever be faster than WS?

**Conclusion: No. The current avg 0 ms alignment is the architectural ceiling.**

Two fundamental constraints prevent gRPC from being stably earlier than WS:

**Constraint 1 — Extra hop in the gRPC path.**
WS subscribes the raw broadcast directly and sends to the socket in one task. gRPC has an additional intermediate layer:
```
WS:   canonical broadcast → WS task → JSON → socket
gRPC: canonical broadcast → ExEx task → internal broadcast → gRPC task → protobuf → mpsc → socket
```
This extra hop is structural and cannot be eliminated without a major architectural change.

**Constraint 2 — Non-deterministic tokio broadcast wakeup order.**
Both WS and gRPC subscribe to the same `broadcast::Sender`. Tokio does not guarantee which receiver wakes up first — the order is determined by the OS scheduler at the moment `Sender::send()` returns. Earlier registration does not imply earlier wakeup.

Even if all three future optimizations above were applied, gRPC could at best match WS (±0.5 ms). Stably beating WS would require modifying reth's core architecture or tokio itself — not worthwhile given the negligible gain.

---

## Out of Scope

- Future optimizations listed above
- gRPC proto schema changes
- `convert.rs` serialization changes
- TLS or authentication

---

## ADR-003: No abort handle for per-client `tokio::spawn` tasks

**Status:** Adopted

**Background:** Each gRPC subscriber connection spawns a `tokio::spawn` task with no abort handle, meaning it cannot be terminated from outside.

**Decision:** Keep the current pattern. Do not add abort handles or external cancellation.

**Rationale:** The task's exit conditions are complete and exhaustive:
1. Client disconnects → `tx.send()` fails → task breaks
2. Broadcast channel closes (node shutdown) → `RecvError::Closed` → task breaks

The task lifetime is fully driven by channel state, requiring no external control. This is idiomatic Rust async practice. Adding an abort handle would introduce complexity with no practical benefit.

**Consequences:** Tasks cannot be terminated externally. Acceptable because all natural exit paths are covered.

---

## ADR-002: Use `expect` in `spawn_critical_task` for gRPC server startup failure

**Status:** Adopted

**Background:** The gRPC server is launched via `spawn_critical_task`. If the server fails to start (e.g. port already in use), the process will panic.

**Decision:** Keep `expect("failed to start gRPC server")` as-is. Do not convert to a graceful error return.

**Rationale:** `spawn_critical_task` is reth's mechanism for tasks whose failure should terminate the node. If the gRPC server cannot start, continuing to run the node would create a silent failure state — the ExEx loop runs normally but no subscribers can connect. Panic-and-exit is the correct behavior here, and the error message is operationally actionable.

**Consequences:** gRPC server startup failure causes immediate node exit. This is intentional and consistent with `spawn_critical_task` semantics.

---

## ADR-001: Bypass ExExManager, accept WAL recovery loss

**Status:** Adopted

**Background:** The 200 ms latency gap is caused by ExExManager's ~6-hop async chain. Two alternatives preserve WAL recovery (Options B and C) but cannot reach the < 50 ms target without either being ineffective (B) or requiring reth fork changes (C).

**Decision:** Subscribe `canonical_state_stream()` directly, bypassing ExExManager entirely.

**Rationale:** This gRPC push server is stateless by design. Clients reconnect on restart; WAL replay was never relied upon. The < 50 ms target is only achievable by matching the WS path hop count. The change is confined to 10 lines in one file.

**Consequences:** WAL crash-recovery removed. `ChainReverted` not deliverable. Both are documented in requirements (L1, L2) and accepted by the team.
