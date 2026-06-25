# Design: Base Chain Support

## Background

Add Base chain support to reth-remote-exex via Cargo feature flag. The primary type differences are: `BaseTxEnvelope` (adds Deposit variant), `BaseReceipt` (adds Deposit receipt with extra fields), and `BasePrimitives` (replaces `EthPrimitives`).

## Goal

Implement feature-flag-gated code paths so `cargo build --features base` produces a working Base ExEx binary, while `cargo build` (default `eth` feature) continues to work unchanged.

---

## Module Structure

```
src/
  lib.rs          — unchanged (re-exports proto + convert)
  convert.rs      — #[cfg(feature)] gated tx/receipt conversion functions
  exex.rs         — #[cfg(feature)] gated node type + primitives
proto/
  exex.proto      — new fields for Deposit tx + Deposit receipt (additive, backward compatible)
Cargo.toml        — feature flags + conditional dependencies
build.rs          — unchanged
```

---

## Interface Design

### Proto Changes (proto/exex.proto)

Additive fields only — no breaking changes:

```protobuf
message Transaction {
  // ... existing fields 1-16 ...

  // Deposit (type 0x7E) — L1-to-L2 deposit, no signature
  bytes  source_hash            = 17;  // B256 32 bytes
  bytes  mint                   = 18;  // U128 16 bytes big-endian; empty = zero
  bool   is_system_transaction  = 19;
}

message Receipt {
  // ... existing fields 1-4 ...

  // Deposit receipt extra fields
  bool   has_deposit_nonce           = 5;
  uint64 deposit_nonce               = 6;
  bool   has_deposit_receipt_version = 7;
  uint64 deposit_receipt_version     = 8;
}
```

### Cargo.toml Feature Flags

```toml
[features]
default = ["eth"]
eth = ["reth-node-ethereum", "reth-ethereum-primitives"]
base = ["base-common-consensus"]

[dependencies]
# ETH-only
reth-node-ethereum = { git = "https://github.com/bnmlsp/reth.git", branch = "dev-exex-internal-txs-v2.3.0", optional = true }
reth-ethereum-primitives = { git = "https://github.com/bnmlsp/reth.git", branch = "dev-exex-internal-txs-v2.3.0", optional = true }

# Base-only
base-common-consensus = { git = "https://github.com/bnmlsp/base.git", branch = "dev-exex-internal-txs-v2.3.0", optional = true, default-features = false, features = ["reth"] }
```

**Note**: `base-common-consensus` requires `features = ["reth"]` to export `BasePrimitives`, `BaseBlock`, and all reth trait implementations.

### convert.rs — Conditional Compilation

Two approaches considered:

**Option A: `#[cfg]` blocks within the same file**
- Pros: single file, easy to see all conversion logic together
- Cons: many `#[cfg]` annotations, harder to read

**Option B: Separate modules `convert_eth.rs` / `convert_base.rs` with shared helpers**
- Pros: cleaner separation, each file is self-contained
- Cons: code duplication for shared logic (header, state_diff, call_traces are identical)

**Decision: Option A** — most conversion logic is shared (header, withdrawal, state_diff, call_traces, access_list, signature). Only `tx_to_proto` and `receipt_to_proto` differ. The `#[cfg]` surface is small.

Implementation:

```rust
// Shared: notification_to_proto, chain_to_proto, block_with_receipts_to_proto,
//         header_to_proto, withdrawal_to_proto, state_diff_to_proto, call_traces_to_proto,
//         all helper functions (u256_to_bytes, b256_to_bytes, etc.)

// ETH-specific:
#[cfg(feature = "eth")]
fn tx_to_proto(tx: &TransactionSigned) -> proto::Transaction { ... }

#[cfg(feature = "eth")]
fn receipt_to_proto(r: &Receipt) -> proto::Receipt { ... }

// Base-specific:
#[cfg(feature = "base")]
fn tx_to_proto(tx: &BaseTxEnvelope) -> proto::Transaction { ... }

#[cfg(feature = "base")]
fn receipt_to_proto(r: &BaseReceipt) -> proto::Receipt { ... }
```

The `block_with_receipts_to_proto` function signature uses a type alias:

```rust
#[cfg(feature = "eth")]
type SignedTx = reth_ethereum_primitives::TransactionSigned;
#[cfg(feature = "eth")]
type BlockType = reth_ethereum_primitives::Block;
#[cfg(feature = "eth")]
type ReceiptType = reth_ethereum_primitives::Receipt;

#[cfg(feature = "base")]
type SignedTx = base_common_consensus::BaseTxEnvelope;
#[cfg(feature = "base")]
type BlockType = base_common_consensus::BaseBlock;
#[cfg(feature = "base")]
type ReceiptType = base_common_consensus::BaseReceipt;
```

### exex.rs — Conditional Node Type

```rust
#[cfg(feature = "eth")]
use reth_node_ethereum::EthereumNode;
#[cfg(feature = "eth")]
type Primitives = reth_ethereum_primitives::EthPrimitives;

#[cfg(feature = "base")]
type Primitives = base_common_consensus::BasePrimitives;
```

The ExEx loop function signature needs to accept the correct primitives:

```rust
async fn remote_exex<Node>(
    ctx: ExExContext<Node>,
    notifications: Arc<broadcast::Sender<Arc<ExExNotification>>>,
) -> eyre::Result<()>
where
    Node: FullNodeComponents<Types: NodeTypes<Primitives = Primitives>>,
```

**Key insight**: For the `base` feature, the ExEx binary will be compiled into `bnmlsp/base` as part of that workspace (step 2). So `exex.rs` in `base` mode does NOT need to import BaseNode or launch reth — it only needs to export the ExEx function that `bnmlsp/base` calls via `install_exex`. The `main()` function in `exex.rs` is only used for `eth` feature.

Revised approach:
- `src/exex.rs` contains the standalone `main()` for ETH mode only (`#[cfg(feature = "eth")]`)
- The ExEx core logic (gRPC server + notification fan-out) is in `src/lib.rs` or a dedicated module, generic over primitives
- For `base` mode, `bnmlsp/base` imports the library and calls the ExEx install function

### block_with_receipts_to_proto — Block Type

ETH uses `RecoveredBlock<reth_ethereum_primitives::Block>`, Base uses `RecoveredBlock<BaseBlock>`.

Both share `alloy_consensus::Header` for the header. The difference is in `body().transactions` type and receipt type. The function signature becomes:

```rust
pub(crate) fn block_with_receipts_to_proto(
    block: &reth_primitives_traits::RecoveredBlock<BlockType>,
    receipts: &[ReceiptType],
    flags: &SubscribeFlags,
) -> proto::BlockWithReceipts
```

Where `BlockType` and `ReceiptType` are feature-gated type aliases.

---

## Data Structures

### Deposit Transaction Fields

| Field | Type | Proto encoding |
|-------|------|---------------|
| source_hash | B256 | bytes (32) |
| from | Address | existing `senders` field (recovered from block) |
| to | TxKind | existing `to` field (empty = Create, 20 bytes = Call) |
| mint | u128 | bytes (16 big-endian), empty if zero |
| value | U256 | existing `value` field |
| gas_limit | u64 | existing `gas_limit` field |
| is_system_transaction | bool | bool field |
| input | Bytes | existing `input` field |

Deposit tx has NO: nonce, gas_price, max_fee, max_priority_fee, access_list, signature, chain_id, blob fields.

For `tx_to_proto` on a Deposit:
```rust
proto::Transaction {
    hash: sealed.hash().as_slice().to_vec(),
    tx_type: 126,  // 0x7E
    signature: None,
    nonce: 0,
    gas_limit: t.gas_limit,
    value: u256_to_bytes(t.value),
    input: t.input.to_vec(),
    to: txkind_to_bytes(&t.to),
    source_hash: t.source_hash.as_slice().to_vec(),
    mint: u128_to_bytes(t.mint),
    is_system_transaction: t.is_system_transaction,
    ..Default::default()
}
```

### Deposit Receipt Fields

| Field | Type | Proto encoding |
|-------|------|---------------|
| inner.success | bool | existing field |
| inner.cumulative_gas_used | u64 | existing field |
| inner.logs | Vec<Log> | existing field |
| deposit_nonce | Option<u64> | has_deposit_nonce + deposit_nonce |
| deposit_receipt_version | Option<u64> | has_deposit_receipt_version + deposit_receipt_version |

---

## Error/Edge Cases

| Case | Handling |
|------|----------|
| Both `eth` and `base` features enabled | Explicit `compile_error!` in `src/lib.rs`: "features 'eth' and 'base' are mutually exclusive" |
| Neither feature enabled | Compile error (missing types) — `default = ["eth"]` prevents this |
| Deposit tx with mint = 0 | mint field is empty bytes (proto3 default), valid |
| Deposit receipt without deposit_nonce | has_deposit_nonce = false, deposit_nonce = 0 |
| EIP-8130 tx encountered (base feature) | Panic with `unimplemented!()` — out of scope, should not appear on mainnet yet |

---

## Concurrency

No new concurrency concerns. The broadcast channel + per-client mpsc architecture remains unchanged. Feature flags only affect serialization logic which runs synchronously within each notification handler.

---

## Test Plan

### Unit Tests (convert.rs, `#[cfg(feature = "base")]`)

| Test | Scenario | Input | Expected |
|------|----------|-------|----------|
| `test_deposit_tx_to_proto` | Deposit tx serialization | TxDeposit with all fields set | proto::Transaction with tx_type=126, source_hash non-empty, mint correct, signature=None, nonce=0 |
| `test_deposit_tx_zero_mint` | Deposit tx with mint=0 | TxDeposit{mint: 0, ...} | mint field is empty bytes |
| `test_deposit_tx_create` | Deposit contract creation | TxDeposit{to: TxKind::Create, ...} | to field is empty |
| `test_deposit_receipt_with_nonce` | Deposit receipt with nonce | DepositReceipt{deposit_nonce: Some(42), deposit_receipt_version: Some(1)} | has_deposit_nonce=true, deposit_nonce=42, has_deposit_receipt_version=true, deposit_receipt_version=1 |
| `test_deposit_receipt_without_nonce` | Deposit receipt pre-Canyon | DepositReceipt{deposit_nonce: None, deposit_receipt_version: None} | has_deposit_nonce=false, deposit_nonce=0 |
| `test_base_receipt_legacy` | Legacy receipt via BaseReceipt | BaseReceipt::Legacy(Receipt{...}) | Same output as ETH receipt_to_proto |
| `test_base_receipt_eip1559` | EIP-1559 receipt via BaseReceipt | BaseReceipt::Eip1559(Receipt{...}) | Same output as ETH receipt_to_proto |
| `test_base_receipt_eip2930` | EIP-2930 receipt via BaseReceipt | BaseReceipt::Eip2930(Receipt{...}) | Same output as ETH receipt_to_proto |
| `test_base_receipt_eip7702` | EIP-7702 receipt via BaseReceipt | BaseReceipt::Eip7702(Receipt{...}) | Same output as ETH receipt_to_proto |
| `test_base_tx_legacy` | Legacy tx via BaseTxEnvelope | BaseTxEnvelope::Legacy(Signed<TxLegacy>) | Same output as ETH tx_to_proto for Legacy |
| `test_base_tx_eip1559` | EIP-1559 tx via BaseTxEnvelope | BaseTxEnvelope::Eip1559(Signed<TxEip1559>) | Same output as ETH tx_to_proto for EIP-1559 |
| `test_flags_all_false_base` | All flags false (base) | SubscribeFlags all false | Empty chain (same behavior as ETH) |
| `test_flags_all_true_base` | All flags true (base) | SubscribeFlags all true | Full chain with all fields populated |

### Unit Tests (existing ETH tests, regression)

| Test | Scenario | Expected |
|------|----------|----------|
| All 9 existing tests | Run with default feature (eth) | Pass unchanged |

### Integration / E2E (AC-4)

| Test | Scenario | Verification |
|------|----------|-------------|
| Base Mainnet subscribe | gRPC client subscribes with include_transactions=true, include_call_traces=true | Receives blocks containing Deposit txs; source_hash matches L1 origin; call traces present for non-system txs |
| Proto backward compat | Old Go client (pre-change proto) connects | Stream works, no decode errors |

---

## Out of Scope

- EIP-8130 transaction handling (panics if encountered)
- Modifications to bnmlsp/base (step 2)
- Go client regeneration
- Performance benchmarking

---

## ADR-1: Feature Flag vs Separate Crate for Chain Support

**Status**: Accepted

**Background**: Need to support both ETH and Base chains in the same repository. Two options: feature flags in one crate, or separate adapter crates in a workspace.

**Decision**: Use Cargo feature flags (`eth` / `base`) with `#[cfg]` gated code paths.

**Rationale**:
- Only ~2 functions differ between chains (tx_to_proto, receipt_to_proto)
- 90%+ code is shared (proto, gRPC server, state_diff, call_traces, headers)
- Workspace split adds overhead for minimal benefit at 2 chains
- Feature flags are simpler to build and test

**Consequences**:
- Features are mutually exclusive; cannot compile a single binary supporting both chains
- If a third chain is added, may need to revisit and refactor into workspace
- `#[cfg]` blocks make the code slightly harder to read in the 2 affected functions

## ADR-2: ExEx Binary Strategy for Base

**Status**: Accepted

**Background**: In ETH mode, `exex.rs` contains `main()` that launches reth + gRPC server. In Base mode, the ExEx will be installed into `bnmlsp/base` via `install_exex`, so a standalone binary is not needed.

**Decision**: Gate `main()` with `#[cfg(feature = "eth")]`. Export the ExEx install function from the library for Base mode consumption.

**Rationale**:
- Base node startup is handled by `bnmlsp/base` binary, not by reth-remote-exex
- The library needs to expose `remote_exex` function + `ExExService` for external integration
- Keeps the library usable as both a standalone binary (ETH) and a library dependency (Base)

**Consequences**:
- `cargo run` only works with ETH feature
- Base integration requires step 2 (modifications to bnmlsp/base)
- Library public API surface increases slightly (ExExService, remote_exex become pub) — implemented in step 2 when bnmlsp/base actually imports this crate
