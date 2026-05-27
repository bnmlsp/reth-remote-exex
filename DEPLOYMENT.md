# Deployment Guide

This guide covers deploying reth-remote-exex on a server that is **already running a standard reth node**, replacing the reth binary with the ExEx-enabled build.

## How It Works

reth-remote-exex ships its own reth binary (`target/release/exex`) built on top of a forked reth (`bnmlsp/reth`, branch `dev-exex-internal-txs`). The ExEx and gRPC server are embedded in the process — no separate service is needed. On startup, the gRPC server automatically listens on `0.0.0.0:10000` and begins streaming block data to connected subscribers.

## Requirements

- The existing node runs via systemd
- Rust stable toolchain installed (`rustup install stable`)
- Go 1.21+ (only needed for client tools)

## Step 1 — Build the Binary

```bash
cd ~/reth-remote-exex
cargo build --release
# First build takes 15–30 minutes (downloads fork + compiles)
ls -lh target/release/exex
```

## Step 2 — Back Up the Existing Binary

```bash
cp /home/ubuntu/eth/bin/reth /home/ubuntu/eth/bin/reth.bak
```

## Step 3 — Replace the Binary

```bash
cp ~/reth-remote-exex/target/release/exex /home/ubuntu/eth/bin/reth
```

## Step 4 — No systemd Changes Required

The existing `ExecStart` flags (`--datadir`, `--http`, `--ws`, `--authrpc`, etc.) are fully compatible. The ExEx initializes automatically at startup — no additional flags needed. The gRPC server listens on `0.0.0.0:10000`.

## Step 5 — Restart

```bash
sudo systemctl restart reth.service
sudo journalctl -u reth.service -f
```

Confirm the ExEx is running by looking for these log lines:

```
ExEx started, subscribing to canonical state stream
gRPC server listening on 0.0.0.0:10000
```

## Step 6 — Verify

```bash
cd ~/reth-remote-exex/examples/go-consumer
go run . --headers 127.0.0.1:10000
```

Receiving `block=<N>` output confirms the deployment is working.

## Rollback

```bash
sudo systemctl stop reth.service
cp /home/ubuntu/eth/bin/reth.bak /home/ubuntu/eth/bin/reth
sudo systemctl start reth.service
```

## Notes

- **Data directory**: the forked reth uses the same on-disk format as upstream — no data migration needed.
- **Downtime**: `systemctl restart` takes 3–5 seconds under normal conditions (`TimeoutStopSec=180` is the upper bound for a graceful shutdown).
- **gRPC port**: `0.0.0.0:10000` is exposed by default. Restrict access at the firewall level if external clients should not connect directly.
- **No TLS / no auth**: the gRPC server is plaintext. Rely on network-level controls (firewall rules, VPN, SSH tunnel) to limit access.
- **No reconnect logic**: clients must implement their own reconnect on stream EOF or error.
