# Design: Base ExEx Integration (Step 2)

## Background

Step 1 added Base chain type support (`BaseTxEnvelope`, `BaseReceipt`) to `reth-remote-exex` behind the `base` feature flag. The library can serialize Base types to protobuf. However, the ExEx core logic (gRPC server + notification broadcast loop) lives inside the binary entry point (`src/exex.rs`) and is not accessible to external crates.

Step 2 must:
1. Pub-export the ExEx logic from `reth-remote-exex` library
2. Replace reth dependencies in `bnmlsp/base` with `bnmlsp/reth` fork
3. Wire the ExEx into the Base node startup pipeline

## Goal

After this step, running `cargo build --bin base-reth-node` in the `bnmlsp/base` workspace produces a binary that unconditionally starts a gRPC server on `0.0.0.0:10000`, streaming Base chain data (headers/transactions/receipts/state_diff/call_traces) to connected subscribers.

---

## Module Structure

### Changes to reth-remote-exex

```
src/
  lib.rs          — adds `pub mod exex;` re-export
  exex.rs         — ExExService, remote_exex, start_grpc_server become pub
                    ETH main() calls the same pub functions
  convert.rs      — unchanged
proto/            — unchanged
```

### Changes to bnmlsp/base

```
Cargo.toml (workspace root)    — replace paradigmxyz/reth → bnmlsp/reth; add reth-remote-exex
crates/execution/grpc-exex/    — NEW crate: BaseNodeExtension that mounts the ExEx
  Cargo.toml
  src/lib.rs
crates/execution/cli/
  Cargo.toml                   — add base-grpc-exex dependency
  src/standard_node.rs         — install GrpcExExExtension
```

---

## Interface Design

### reth-remote-exex Public API

```rust
// src/lib.rs — new export
pub mod exex;

// src/exex.rs — newly public items
pub struct ExExService {
    pub notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
}

pub async fn remote_exex<Node: FullNodeComponents<Types: NodeTypes<Primitives = Primitives>>>(
    ctx: ExExContext<Node>,
    notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
) -> eyre::Result<()>

pub async fn start_grpc_server(
    notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
) -> eyre::Result<()>
```

The `start_grpc_server` function encapsulates tonic Server creation + serve on `0.0.0.0:10000`. The ETH `main()` is refactored to call these same functions.

**Type alias `Primitives`** is feature-gated (EthPrimitives / BasePrimitives) so the public API is concrete per feature — external consumers always see a single concrete type, not a generic.

### base-grpc-exex Extension

```rust
// crates/execution/grpc-exex/src/lib.rs

use base_node_runner::{BaseNodeExtension, FromExtensionConfig, NodeHooks};
use reth_remote_exex::exex::{start_grpc_server, remote_exex, BROADCAST_CHANNEL_CAPACITY};

#[derive(Debug, Clone, Copy)]
pub struct GrpcExExExtension;

impl BaseNodeExtension for GrpcExExExtension {
    fn apply(self: Box<Self>, hooks: NodeHooks) -> NodeHooks {
        let notifications = Arc::new(
            broadcast::channel(BROADCAST_CHANNEL_CAPACITY).0
        );
        let notif_exex = notifications.clone();

        hooks
            .install_exex("grpc-exex", move |ctx| async move {
                Ok(remote_exex(ctx, notif_exex))
            })
            .add_node_started_hook(move |node| {
                node.task_executor.spawn_critical_task(
                    "gRPC server",
                    start_grpc_server(notifications),
                );
                Ok(())
            })
    }
}

impl FromExtensionConfig for GrpcExExExtension {
    type Config = ();
    fn from_config(_config: Self::Config) -> Self {
        Self
    }
}
```

### standard_node.rs Wiring

```rust
// In StandardBaseRethNode::runner():
runner.install_ext::<GrpcExExExtension>(());
```

---

## Data Structures

No new data structures. The existing `ExExService`, `ExExNotification<Primitives>`, and proto types are reused.

### Constants Made Public

```rust
pub const BROADCAST_CHANNEL_CAPACITY: usize = 16;
pub const PER_CLIENT_CHANNEL_CAPACITY: usize = 16;
```

---

## Dependency Changes

### reth-remote-exex Cargo.toml

Changes needed:
1. **tonic/prost upgrade**: `tonic` 0.11→0.14, `prost` 0.12→0.14, `tonic-build` 0.11→0.14
2. **Replace `reth` umbrella crate with fine-grained crates**: The `reth` umbrella crate is not declared in base workspace. Split into:
   - `reth-cli` (ETH only, for `Cli::parse_args`) — optional, gated behind `eth` feature
   - `reth-provider` (for `CanonStateNotification`, `CanonStateSubscriptions`)
   - Keep `reth-exex`, `reth-node-api`, `reth-tracing`, `reth-execution-types` as-is

The `[[bin]]` section remains for ETH mode. Library exports are controlled purely by `pub` visibility in source.

### bnmlsp/base Workspace Cargo.toml

1. Replace all `paradigmxyz/reth` entries:
   ```toml
   # Before:
   reth-exex = { git = "https://github.com/paradigmxyz/reth", tag = "v2.3.0" }
   # After:
   reth-exex = { git = "https://github.com/bnmlsp/reth.git", branch = "dev-exex-internal-txs-v2.3.0" }
   ```
   This applies to all ~60 reth-* crates listed in workspace dependencies.

2. Add new workspace dependency:
   ```toml
   reth-remote-exex = { git = "https://github.com/bnmlsp/reth-remote-exex.git", branch = "feat-base-support", default-features = false, features = ["base"] }
   ```

3. Add workspace member:
   ```toml
   [workspace]
   members = [
     # ... existing ...
     "crates/execution/grpc-exex",
   ]
   ```

### base-grpc-exex/Cargo.toml

```toml
[package]
name = "base-grpc-exex"
version.workspace = true
edition.workspace = true
rust-version.workspace = true
license.workspace = true
homepage.workspace = true
repository.workspace = true
description = "gRPC ExEx extension for Base node"

[lints]
workspace = true

[dependencies]
reth-remote-exex.workspace = true
base-node-runner.workspace = true
tokio.workspace = true
tracing.workspace = true

[features]
```

### base-execution-cli/Cargo.toml

Add:
```toml
base-grpc-exex.workspace = true
```

---

## Error/Edge Cases

| Case | Handling |
|------|----------|
| Port 10000 already in use | gRPC server panics (same as ETH mode) — detectable via logs |
| No gRPC subscribers connected | Broadcast channel drops notifications silently (intentional) |
| ExEx notification stream ends | ExEx loop exits, gRPC server remains alive but no new data |
| Dependency version mismatch (bnmlsp/reth vs base) | Compile error — caught immediately at build time |

---

## Concurrency

Same architecture as ETH mode:
- `broadcast::channel` from ExEx loop to gRPC service (multi-producer-multi-consumer)
- Per-client `mpsc::channel` for backpressure isolation
- `start_grpc_server` runs as a critical spawned task
- No new shared mutable state introduced

---

## Security Analysis

gRPC endpoint listens on `0.0.0.0:10000` without authentication:
- **Who can access**: anyone with network access to port 10000
- **What they access**: read-only chain data (headers, transactions, receipts, state diffs, call traces)
- **Boundary**: network firewall / security group must restrict access
- **Worst case**: unauthorized read of public blockchain data (low impact — same data is available via RPC)
- **Mitigation**: same as ETH mode — rely on network-level isolation (firewall rules, private subnet)

No change from ETH mode. No additional attack surface.

---

## Test Plan

### AC-1: Compilation Verification

| Test | Input | Expected |
|------|-------|----------|
| `cargo build --bin base-reth-node` in bnmlsp/base | Clean workspace with all deps replaced | Build succeeds |

### AC-2: Existing Tests Regression

| Test | Input | Expected |
|------|-------|----------|
| `cargo check --workspace` in bnmlsp/base | After reth dep replacement | All workspace crates compile |
| `cargo test -p base-execution-exex` | After reth dep replacement | All existing tests pass |
| `cargo test -p base-node-runner` | After reth dep replacement | All existing tests pass |

### AC-3: Library API Available

| Test | Scenario | Input | Expected |
|------|----------|-------|----------|
| `test_exex_service_public` | Import ExExService from library | `use reth_remote_exex::exex::ExExService` | Compiles |
| `test_remote_exex_public` | Import remote_exex from library | `use reth_remote_exex::exex::remote_exex` | Compiles |
| `test_start_grpc_server_public` | Import start_grpc_server | `use reth_remote_exex::exex::start_grpc_server` | Compiles |
| `test_constants_public` | Import channel capacity constants | `use reth_remote_exex::exex::BROADCAST_CHANNEL_CAPACITY` | Value is 16 |

### AC-4: Extension Installed

| Test | Scenario | Input | Expected |
|------|----------|-------|----------|
| `test_grpc_extension_applies_hooks` | GrpcExExExtension::apply() | Empty NodeHooks | Returns hooks with exex and node_started hook registered |
| standard_node.rs code inspection | GrpcExExExtension is installed | `runner.install_ext::<GrpcExExExtension>(())` present in `StandardBaseRethNode::runner()` | Compilation verifies wiring (AC-1) |

### Error Path Tests

| Test | Scenario | Input | Expected |
|------|----------|-------|----------|
| `test_eth_mode_unchanged` | ETH binary still works | `cargo build` with default feature in reth-remote-exex | Produces working binary, same behavior |
| `test_base_binary_exits` | Base binary still prints error | `cargo run --features base` in reth-remote-exex | Exits with error message (unchanged from step 1) |

---

## Known Limitations

1. **tonic version mismatch**: reth-remote-exex uses tonic 0.11, base workspace uses tonic 0.14. This will likely cause a compile error. Resolution: upgrade tonic/prost in reth-remote-exex to match base workspace (0.14/0.14).
2. **`reth` umbrella crate**: reth-remote-exex currently depends on `reth` (umbrella). base workspace does not declare it. Solution: replace with fine-grained crates (`reth-cli` for ETH binary, `reth-provider` for ExEx loop). See Cargo.toml changes section.

---

## Out of Scope

- Configurable gRPC listen address
- CLI flags for enable/disable
- Docker / base-node changes (step 3)
- End-to-end testing on real network (step 6)
- Go client changes

---

## ADR-1: Extension Crate vs Inline in Existing Crate

**Status**: Accepted

**Background**: The gRPC ExEx could be wired either as a new extension crate (`base-grpc-exex`) or directly in an existing crate (e.g. `base-node-core` or `base-execution-cli`).

**Decision**: New extension crate `crates/execution/grpc-exex`.

**Rationale**:
- Follows the established pattern (see `base-proofs-extension`, `base-bundle-extension`)
- Single responsibility — one crate, one extension
- Minimal coupling — only depends on `reth-remote-exex` and `base-node-runner`
- Easy to remove if no longer needed

**Consequences**:
- One more crate in the workspace (trivial overhead)
- Workspace Cargo.toml grows by one member entry

## ADR-2: tonic/prost Version Alignment

**Status**: Accepted

**Background**: reth-remote-exex uses tonic 0.11 / prost 0.12. base workspace uses tonic 0.14 / prost 0.14. Mixing versions causes type incompatibility (generated code depends on prost traits from a specific version).

**Decision**: Upgrade reth-remote-exex to tonic 0.14 / prost 0.14 (and tonic-build 0.14) to match base workspace.

**Rationale**:
- base workspace pins tonic 0.14 across many crates; changing it would be a massive diff
- reth-remote-exex is our own code, upgrading is straightforward
- tonic 0.11→0.14 has no breaking API changes for our usage (simple unary/streaming)

**Consequences**:
- reth-remote-exex ETH mode also moves to tonic 0.14 (acceptable — no behavioral change)
- Must regenerate proto code (automatic via build.rs)
