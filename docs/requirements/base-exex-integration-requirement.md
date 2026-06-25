# Requirement: Base ExEx Integration (Step 2)

## Background

Step 1 (feature flag `base` in reth-remote-exex) is complete. The library can now serialize Base-specific types (BaseTxEnvelope, BaseReceipt) to protobuf. However, the ExEx core logic (gRPC server + notification fan-out) is currently private to `exex.rs` and only callable from the ETH `main()`.

To run on Base Mainnet, we need:
1. Expose the ExEx core logic from reth-remote-exex as a public library API
2. Create a `BaseNodeExtension` in `bnmlsp/base` that mounts this ExEx into the Base node startup pipeline
3. Replace all `paradigmxyz/reth` dependencies in `bnmlsp/base` with `bnmlsp/reth` fork to enable `Chain.call_traces()`

## Goal

When `base-reth-node` binary starts, the gRPC ExEx is unconditionally mounted. Connected gRPC clients receive real-time Base chain data (headers/transactions/receipts/state_diff/call_traces) including Deposit transactions.

## Functional Requirements

### FR-1: Public ExEx Library API (reth-remote-exex) [Must]

- `ExExService` struct becomes `pub` — exposed from `src/exex.rs` (or a new module) via `lib.rs`
- `remote_exex` function becomes `pub` — the ExEx loop that receives chain notifications and broadcasts to gRPC subscribers
- Both use the existing feature-gated type alias `Primitives` (compile-time concrete type per feature, same as Step 1)
- gRPC server startup logic is also `pub` (or bundled into a single public entry point)
- gRPC server listens on hardcoded `0.0.0.0:10000`

### FR-2: Replace reth Dependencies in bnmlsp/base [Must]

- All `paradigmxyz/reth` git dependencies in `bnmlsp/base` workspace `Cargo.toml` are replaced with `bnmlsp/reth` branch `dev-exex-internal-txs-v2.3.0`
- Version alignment: bnmlsp/reth fork is based on v2.3.0, same version base currently uses
- After replacement, existing workspace crates continue to compile

### FR-3: gRPC ExEx Extension (bnmlsp/base) [Must]

- New crate `crates/execution/grpc-exex` (or similar) implementing `BaseNodeExtension`
- Extension unconditionally installs the ExEx via `NodeHooks::install_exex()`
- Extension starts the gRPC server via `NodeHooks::add_node_started_hook()`
- gRPC server listens on `0.0.0.0:10000` (hardcoded)
- Follows existing extension pattern (e.g. `ProofsHistoryExtension`)

### FR-4: Standard Node Wiring [Must]

- `StandardBaseRethNode::runner()` in `crates/execution/cli/src/standard_node.rs` installs the new extension
- No CLI flag needed — extension is always installed

### FR-5: reth-remote-exex Dependency [Must]

- `bnmlsp/base` workspace adds `reth-remote-exex` as a git dependency pointing to `bnmlsp/reth-remote-exex` branch `feat-base-support` with `features = ["base"]`

## Prerequisites

- Step 1 complete (reth-remote-exex `feat-base-support` branch with `base` feature flag)
- `bnmlsp/base` repo on branch `dev-exex-internal-txs-v2.3.0` (from tag v1.1.1)

## Out of Scope

- CLI flag for enabling/disabling gRPC ExEx
- Configurable gRPC listen address (hardcoded 0.0.0.0:10000)
- Go client changes
- Performance optimization
- TLS/auth for gRPC endpoint
- Modifications to `bnmlsp/base-node` Docker config (step 3)

## Non-Functional Requirements

- Not applicable for this step; same as ETH version runtime characteristics

## Acceptance Criteria

### AC-1: Compilation Verification
- Given: `bnmlsp/base` workspace with reth deps replaced and gRPC ExEx extension added
- When: `cargo build --bin base-reth-node` is executed
- Then: build succeeds without errors

### AC-2: Existing Tests Regression
- Given: `bnmlsp/base` workspace after modifications
- When: `cargo test -p base-execution-exex` and other related crate tests are run
- Then: existing tests pass (or fail only for known unrelated reasons)

### AC-3: Library API Available
- Given: `reth-remote-exex` with `features = ["base"]`
- When: external crate imports `reth_remote_exex::exex::{ExExService, remote_exex}`
- Then: import succeeds, types are public and usable

### AC-4: Extension Installed
- Given: `StandardBaseRethNode::runner()` is called
- When: examining the runner's extensions list
- Then: gRPC ExEx extension is present

## Dependencies

| Repository | Branch | Role |
|------------|--------|------|
| `bnmlsp/reth` | `dev-exex-internal-txs-v2.3.0` | Provides Chain.call_traces() |
| `bnmlsp/reth-remote-exex` | `feat-base-support` | gRPC ExEx library (base feature) |
| `bnmlsp/base` | `dev-exex-internal-txs-v2.3.0` | Target: Base node binary |

## Change History

- [2026-06-25] Initial requirement created / Base ExEx integration step 2
