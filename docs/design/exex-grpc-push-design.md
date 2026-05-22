# Design: reth ExEx gRPC Push Server

## Interface Definition

gRPC service (`proto/exex.proto`):

```protobuf
service RemoteExEx {
  rpc Subscribe(SubscribeRequest) returns (stream Notification)
}
```

`Notification` is a oneof carrying one of three events:

| Event | Payload |
|-------|---------|
| `ChainCommitted` | `new Chain` |
| `ChainReorged` | `old Chain` + `new Chain` |
| `ChainReverted` | `old Chain` |

`Chain` contains:
- `repeated BlockWithReceipts blocks`
- `StateDiff state_diff`

Default listen address: `0.0.0.0:10000` (change to `[::1]:10000` to restrict to localhost).

## Data Structures

### BlockWithReceipts

```
BlockHeader header         // full block header including optional EIP-4844/4895/7685 fields
repeated Transaction txs   // supports Legacy / EIP-2930 / EIP-1559 / EIP-4844 / EIP-7702
repeated bytes senders     // sender address per tx, 20 bytes each
repeated Receipt receipts  // tx_type + success + cumulative_gas_used + logs
repeated Withdrawal withdrawals
```

### StateDiff

Derived from reth's `BundleState`:

```
repeated AccountDiff accounts    // per-account: status + current info + original info + storage slots
repeated ContractDiff contracts  // newly deployed or changed contract bytecode
repeated BlockReverts reverts    // per-block revert records for reorg replay
```

### Encoding Conventions

| Rust type | Proto type | Encoding |
|-----------|-----------|----------|
| `B256` | `bytes` | 32 bytes, as-is |
| `Address` | `bytes` | 20 bytes, as-is |
| `U256` | `bytes` | 32 bytes, big-endian |
| `U128` | `bytes` | 16 bytes, big-endian |
| `Option<T>` scalar | `bool has_xxx` + `T xxx` | proto3 workaround for optional scalars |

## Module Layout

```
src/
├── lib.rs       // exposes the `proto` and `convert` modules
├── convert.rs   // reth native types → protobuf (pure functions, stateless)
└── exex.rs      // binary entry point: ExEx hook + gRPC server + main
```

**convert.rs** — pure data transformation, no shared state. Single public entry point: `pub fn notification_to_proto`.

**exex.rs** — three responsibilities:
- `main`: initializes reth CLI, registers the ExEx, spawns the gRPC server task
- `remote_exex`: ExEx loop that receives `ExExNotification` from reth and forwards it via broadcast channel
- `ExExService`: implements the gRPC `RemoteExEx` trait; each `Subscribe` call creates an independent mpsc channel

## Concurrency Model

```
reth ExEx loop
    │
    │  broadcast::Sender  (capacity: BROADCAST_CHANNEL_CAPACITY = 16)
    │
    ├── subscriber 1: tokio::spawn task
    │       broadcast::Receiver → mpsc::Sender (capacity: PER_CLIENT_CHANNEL_CAPACITY = 16) → gRPC stream
    ├── subscriber 2: tokio::spawn task
    │       broadcast::Receiver → mpsc::Sender (capacity: PER_CLIENT_CHANNEL_CAPACITY = 16) → gRPC stream
    └── ...
```

Each `Subscribe` call spawns an independent Tokio task responsible for receiving from the broadcast channel and forwarding into the per-client mpsc channel. When the client disconnects, `mpsc::Sender::send` returns `Err` and the task exits.

- **broadcast channel**: fans out from the ExEx loop to all active subscribers. When the channel is full, new messages are dropped for lagged receivers (known limitation).
- **mpsc channel**: one per gRPC client connection. When the client disconnects, `send` returns `Err` and the spawned forwarding task exits cleanly.

## Dependencies

| Crate | Purpose |
|-------|---------|
| `reth` / `reth-exex` / `reth-node-ethereum` / `reth-node-api` | Node framework and ExEx interface |
| `reth-execution-types` / `reth-ethereum-primitives` / `reth-primitives-traits` | `Chain` / `Block` / `Receipt` types |
| `revm-database` / `revm-state` | `BundleState` / `AccountRevert` |
| `alloy-consensus` / `alloy-primitives` / `alloy-eips` / `alloy-eip2930` / `alloy-eip7702` | Per-EIP data structures |
| `tonic` / `prost` / `tonic-build` | gRPC framework and protobuf code generation |
| `tokio` / `tokio-stream` / `futures-util` | Async runtime |
| `reth-tracing` | Structured logging via `tracing::info!` |
| `eyre` | Error handling in `main` and the ExEx loop |

All versions are pinned via `Cargo.lock`, aligned with reth `v2.2.0`.

## Known Limitations

| # | Limitation | Impact |
|---|------------|--------|
| 1 | No authentication or TLS | Must be deployed in a trusted network environment |
| 2 | Slow consumers silently drop messages when broadcast channel is full | Consumer may miss notifications without being aware |
| 3 | No consumer-side reconnect logic in the example | Demo only; production consumers must implement reconnect |
| 4 | Call traces / internal transactions not available | Requires supplementary `debug_traceBlockByNumber` calls |
