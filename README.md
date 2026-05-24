# reth-remote-exex

A [reth](https://github.com/paradigmxyz/reth) ExEx (Execution Extension) that exposes a gRPC push server. On every committed, reorged, or reverted chain segment, the ExEx serializes block data to protobuf and streams it to all connected subscribers in real time.

## Architecture

```
reth node process
├── ExEx hook (in-process, low latency)
│     receives ExExNotification on every block execution
│     ChainCommitted / ChainReorged / ChainReverted
└── gRPC server (0.0.0.0:10000)
      └── RemoteExEx.Subscribe → stream Notification
            consumers in any language connect and receive data
```

## Subscribe Filtering

Clients declare which fields they need at subscribe time via boolean flags. The server serializes only the requested fields. All flags default to `false` — a client that sends no flags receives no data (explicit-subscription semantics).

| Flag | Data |
|------|------|
| `include_headers` | `BlockWithReceipts.header` |
| `include_transactions` | `BlockWithReceipts.txs` + `senders` (always bundled) |
| `include_receipts` | `BlockWithReceipts.receipts` |
| `include_withdrawals` | `BlockWithReceipts.withdrawals` |
| `include_state_diff` | `Chain.state_diff` |
| `include_call_traces` | `Chain.call_traces` (internal transactions) |

## Build

```bash
# Rust stable required
cargo build --release
# Output: target/release/exex (~70 MB, stripped)
# First build takes 15-30 minutes (fat LTO)
```

## go-consumer

A demo Go client with CLI flag support. Pre-generated proto files are committed — no protoc needed.

```bash
cd examples/go-consumer
go build ./...

# Subscribe to specific fields (flags before addr)
go run . --call-traces 127.0.0.1:10000
go run . --headers --transactions --receipts 127.0.0.1:10000

# All flags default to false; no flags = no data received
```

## go-trace-verifier

A correctness verification tool that streams N blocks via gRPC and compares call traces field-by-field against `debug_traceBlockByNumber` JSON-RPC responses.

```bash
cd examples/go-trace-verifier
go build ./...
go test ./...

# Verify N blocks (defaults: --grpc 127.0.0.1:10000, --rpc http://127.0.0.1:8545)
go run . 50
go run . 50 --grpc <addr> --rpc <url> --workers 8
```

## Key Parameters

| Parameter | Location | Default | Notes |
|-----------|----------|---------|-------|
| gRPC listen address | `src/exex.rs` | `0.0.0.0:10000` | Change to `[::1]:10000` to restrict to localhost |
| Broadcast channel capacity | `src/exex.rs` | `16` | Lagged subscribers receive a warning and skip missed messages |
| Per-client channel capacity | `src/exex.rs` | `16` | Increase if consumer processing is slow |
| Max receive message size | `examples/go-consumer/main.go` | `64 MB` | Mainnet blocks rarely exceed 20 MB |

## Known Limitations

- **No authentication**: plaintext, no TLS or auth. Deploy behind a firewall or restrict access at the network level.
- **Lagged subscribers skip messages**: if the broadcast channel fills up, lagged clients receive a warning log and resume from the oldest available message.
- **No reconnect logic**: consumers must implement their own reconnect logic.
- **Serialization only**: server-side computation (e.g. `TracingInspector`) runs regardless of subscribe flags. Flag filtering controls serialization, not execution.
