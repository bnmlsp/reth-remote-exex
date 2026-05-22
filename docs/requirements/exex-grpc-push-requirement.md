# Requirements: reth ExEx gRPC Push Server

## Background

reth is a high-performance Ethereum execution layer client that exposes an ExEx (Execution Extension) mechanism: custom logic compiled directly into the reth binary receives complete chain state change data in-process after each block is executed.

Existing data ingestion approaches based on JSON-RPC polling suffer from high latency, the need to re-execute transactions, and incomplete state data. ExEx can push complete block data with minimal latency immediately after block execution, but its interface is limited to in-process use — external systems cannot consume it directly.

## Goal

Build a gRPC push service on top of reth ExEx that streams full per-block data (block header, transactions, receipts, state diff) to external consumers in real time, using protobuf serialization, accessible from any language.

## Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| F1 | After each block is executed, the system shall push a `ChainCommitted` notification containing complete block data to all connected consumers via gRPC stream | Must |
| F2 | When a chain reorg occurs, the system shall push a `ChainReorged` notification containing both the old chain and new chain data | Must |
| F3 | When a chain revert occurs, the system shall push a `ChainReverted` notification containing the old chain data | Must |
| F4 | Each block payload shall include: block header, transaction list (all tx types: Legacy / EIP-2930 / EIP-1559 / EIP-4844 / EIP-7702), sender addresses, receipt list, and withdrawal list | Must |
| F5 | Each block payload shall include a complete state diff: account changes (balance / nonce / code_hash), storage slot changes, newly deployed contract bytecode, and per-block revert records | Must |
| F6 | The system shall support multiple concurrent consumers, each receiving notifications independently | Should |
| F7 | A Go consumer example shall be provided to demonstrate how to connect, subscribe, and decode notification data | Should |

## Non-Functional Requirements

| Category | Decision |
|----------|----------|
| Performance | End-to-end push latency (block executed → consumer receives) < 100 ms on the same machine; single-block serialized size ≤ 64 MB |
| Availability | Tied to the reth node process; no independent SLA |
| Security | No authentication or TLS in this version; deployment must be restricted to an internal network or behind a firewall |
| Scalability | Out of scope; single-node, single-process only |
| Observability | Key events (client connect / disconnect, notification sent) must be logged |
| Data retention | Not applicable; notifications are pushed and discarded, no persistence |
| Compatibility | All dependency versions locked via `Cargo.lock` for reproducible builds; the proto schema must not introduce breaking changes once published |

## Out of Scope

- Call traces / internal transactions (not available from ExEx; use `debug_traceBlockByNumber`)
- gRPC authentication and TLS
- Consumer-side reconnect logic (responsibility of the consumer)
- Backpressure for slow consumers (current design: messages are dropped when the channel is full; consumers are not notified)
- Data persistence and replay
- Docker image

## Acceptance Criteria

| ID | Given / When / Then |
|----|---------------------|
| AC1 | Given the node has started, when ExEx initialization completes, then the gRPC server is listening on `0.0.0.0:10000` |
| AC2 | Given a consumer is connected, when a new block is committed, then the consumer receives a `ChainCommitted` notification whose block number, tx count, and receipt count match `eth_getBlockByNumber` |
| AC3 | Given a consumer is connected, when a `ChainCommitted` notification is received, then the state diff account balance and nonce before/after match `eth_getBalance` and `eth_getTransactionCount` at the corresponding block heights |
| AC4 | Given multiple consumers are connected simultaneously, when a new block is committed, then each consumer independently receives the complete notification |
| AC5 | Given a consumer disconnects, when a new block is committed, then the server logs the disconnection and remaining consumers are unaffected |
