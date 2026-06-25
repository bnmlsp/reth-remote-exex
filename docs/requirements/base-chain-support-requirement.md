# Requirement: Base Chain Support

## Background

reth-remote-exex currently only supports Ethereum mainnet (EthPrimitives). Base is an OP-stack L2 chain using `base/base` v1.1.1 as its node implementation, which depends on reth v2.3.0 — fully aligned with this project's reth fork version.

Key differences between Base and Ethereum:
- Transaction types: adds Deposit tx (type 0x7E) — no signature, extra fields: source_hash/mint/is_system_transaction
- Receipt types: BaseReceipt is an enum; Deposit receipt adds deposit_nonce and deposit_receipt_version
- Primitives: BasePrimitives (not EthPrimitives), SignedTx is BaseTxEnvelope (not EthereumTxEnvelope)
- No EIP-4844 blob transactions (L2 does not produce blobs)
- No validator withdrawals (L2 has no beacon chain withdrawals)

## Goal

Support Base chain in the same repository via Cargo feature flag, producing a Base-specific ExEx binary that provides the same gRPC push functionality as the ETH version (headers/transactions/receipts/state_diff/call_traces).

## Functional Requirements

### FR-1: Feature Flag Mechanism [Must]

- Add Cargo feature `base`, mutually exclusive with existing `eth` (default)
- `cargo build --features base` produces a Base ExEx binary
- `cargo build` (default) behavior unchanged, still produces ETH version

### FR-2: Deposit Transaction Serialization [Must]

- Proto adds Deposit tx fields: source_hash (bytes 32), mint (bytes 16), is_system_transaction (bool)
- convert.rs handles BaseTxEnvelope::Deposit variant, serializing all fields correctly
- tx_type field value is 126 (0x7E) for Deposit transactions
- Deposit tx has no signature; signature field is None

### FR-3: Base Receipt Adaptation [Must]

- convert.rs handles BaseReceipt enum (Legacy/Eip2930/Eip1559/Eip7702/Deposit/Eip8130)
- Deposit receipt additionally serializes deposit_nonce and deposit_receipt_version (new proto fields)
- Non-Deposit receipt types behave identically to ETH version

### FR-4: BasePrimitives Generic Adaptation [Must]

- exex.rs ExEx loop supports BasePrimitives (Block/SignedTx/Receipt types aligned)
- chain_to_proto / block_with_receipts_to_proto and related functions handle Base block structure

### FR-5: All Existing Fields Continue Streaming [Must]

- headers: uses alloy_consensus::Header (same as ETH)
- transactions: all BaseTxEnvelope variants (Legacy/2930/1559/7702/Deposit)
- receipts: all BaseReceipt variants
- state_diff: Chain.execution_outcome() structure is the same as ETH, no extra adaptation needed
- call_traces: Chain.call_traces() interface provided by reth fork, same as ETH

### FR-6: Withdrawals Handling [Should]

- Base L2 block body withdrawals field is always empty (or Some([])); existing logic handles it correctly (empty list)
- Do not remove withdrawals flag or proto field; maintain protocol compatibility

## Prerequisites

- Fork `base/base` v1.1.1 to `bnmlsp/base` (done). reth-remote-exex's `[dependencies]` under the `base` feature points to `bnmlsp/base` to obtain BaseTxEnvelope/BaseReceipt types. Step 2 of the overall plan will modify the same fork to replace reth dependencies + mount ExEx, maintaining Cargo dependency consistency.

## Out of Scope

- EIP-8130 (type 0x7D) Account Abstraction transaction support — experimental, not implemented now
- Go client (go-consumer / go-trace-verifier) adaptation — proto3 is backward compatible, old clients work as-is
- Performance optimization — same implementation strategy as ETH version
- Workspace split — feature flag is sufficient, no crate split
- Subsequent operations on bnmlsp/base fork (replacing reth deps + install_exex) — belongs to step 2

## Non-Functional Requirements

No independent NFR constraints for this step; same as ETH version.

## Acceptance Criteria

### AC-1: Build Verification
- Given: feature `base` is configured
- When: `cargo build --features base` is executed
- Then: build succeeds, producing a Base ExEx binary

### AC-2: Unit Tests
- Given: feature `base` is configured
- When: `cargo test --features base` is executed
- Then: all convert.rs tests pass, including Deposit tx and BaseReceipt test cases

### AC-3: ETH Version Regression
- Given: default feature (eth)
- When: `cargo build` and `cargo test` are executed
- Then: behavior is identical to before the change, no regression

### AC-4: End-to-End Verification
- Given: Base Mainnet node running base/base (reth replaced with bnmlsp/reth fork) + ExEx mounted
- When: gRPC client connects and subscribes to call_traces + transactions
- Then: receives real-time Base block data; Deposit tx fields are correct (source_hash non-empty, mint correct, is_system_transaction correct)

### AC-5: Proto Compatibility
- Given: old Go client (proto not regenerated)
- When: connects to Base ExEx gRPC service
- Then: receives push stream without errors (new fields are ignored)

## Dependencies

### Current Version Baseline

| Repository | Branch/Tag | Version | Purpose |
|------------|------------|---------|---------|
| `bnmlsp/reth` | `dev-exex-internal-txs-v2.3.0` | Based on reth v2.3.0 | Provides Chain.call_traces() interface |
| `bnmlsp/base` | `dev-exex-internal-txs-v2.3.0` (from tag v1.1.1) | Based on base/base v1.1.1 | Provides BasePrimitives/BaseTxEnvelope/BaseReceipt types |
| `bnmlsp/base-node` | `dev-exex-internal-txs-v2.3.0` (from tag v1.1.1) | Based on base/node v1.1.1 | Deployment configuration |

### Version Dependency Chain

```
base/node v1.1.1 → base/base v1.1.1 → paradigmxyz/reth v2.3.0
                                         ↑
                              bnmlsp/reth fork (dev-exex-internal-txs-v2.3.0)
```

### Upgrade Strategy

This implementation follows reth version upgrades. When paradigmxyz/reth releases a new version:
1. `bnmlsp/reth` fork needs to rebase the call traces patch onto the new version (new branch e.g. `dev-exex-internal-txs-vX.Y.Z`)
2. `bnmlsp/base` needs to sync with the corresponding upstream base/base version
3. `reth-remote-exex` needs to update Cargo.toml dependency references

### Runtime Environment

| Dependency | Purpose |
|------------|---------|
| Base Mainnet node | End-to-end verification environment |

## Change History

- [2026-06-25] Initial requirement created / Base chain gRPC push support / new feature
