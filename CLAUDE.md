# reth-remote-exex — Project Guide for Claude

## What This Project Does

A reth [ExEx (Execution Extension)](https://github.com/paradigmxyz/reth) that exposes a gRPC push server. When reth commits, reorgs, or reverts a chain segment, the ExEx serializes the data to protobuf and streams it to all connected gRPC subscribers in real time.

Clients subscribe with boolean flags to declare which data they need. The server serializes only the requested fields.

---

## Repository Layout

```
proto/exex.proto                   # Protobuf schema (source of truth)
src/lib.rs                         # Library: re-exports proto + convert modules
src/convert.rs                     # reth types → protobuf conversion; SubscribeFlags
src/exex.rs                        # Binary entry point: gRPC server + ExEx loop
build.rs                           # tonic-build: regenerates Rust proto types on cargo build

examples/go-consumer/              # Demo Go client with CLI flag support
  main.go                          # --headers/--transactions/--receipts/--withdrawals/--state-diff/--call-traces
  proto/gen/                       # Generated Go proto types (copy from here to go-trace-verifier)

examples/go-trace-verifier/        # Correctness verification tool
  main.go                          # Streams N blocks via gRPC, compares call traces against RPC
  compare.go                       # Frame-level comparison logic
  rpc.go                           # debug_traceBlockByNumber RPC client

docs/requirements/                 # Requirements documents per feature
docs/design/                       # Design documents per feature (include ADR)
```

---

## Key Design Decisions

- **SubscribeRequest boolean flags**: clients explicitly declare which fields they need; all flags default to `false` (explicit-subscription semantics — intentional breaking change from the original empty request).
- **`senders` always bundled with `txs`**: sender addresses are recovered from tx signatures; they share `include_transactions` flag with no independent control.
- **`SubscribeFlags` struct** in `convert.rs` decouples the proto-generated `SubscribeRequest` type from the conversion layer.
- **Custom reth fork**: depends on `bnmlsp/reth` branch `dev-exex-internal-txs` which exposes call traces via `ExExNotification`.
- **Proto-generated Go files** are committed to the repo (no protoc required at build time for Go consumers).

---

## Common Commands

### Rust

```bash
# Build the exex binary
cargo build --release

# Run unit tests (9 tests in convert.rs covering flag filtering)
cargo test
```

### Regenerate Go proto files (after proto/exex.proto changes)

```bash
protoc \
  --go_out=examples/go-consumer/proto/gen \
  --go-grpc_out=examples/go-consumer/proto/gen \
  --go_opt=paths=source_relative \
  --go-grpc_opt=paths=source_relative \
  -I proto proto/exex.proto

cp examples/go-consumer/proto/gen/exex.pb.go      examples/go-trace-verifier/proto/gen/
cp examples/go-consumer/proto/gen/exex_grpc.pb.go  examples/go-trace-verifier/proto/gen/
```

### go-consumer

```bash
cd examples/go-consumer
go build ./...

# Subscribe to specific fields (all flags default false)
go run . [addr] [--headers] [--transactions] [--receipts] [--withdrawals] [--state-diff] [--call-traces]

# Example: call traces only
go run . 127.0.0.1:10000 --call-traces
```

### go-trace-verifier

```bash
cd examples/go-trace-verifier
go build ./...
go test ./...

# Verify N blocks: compares gRPC call traces against debug_traceBlockByNumber
go run . <n> [--grpc ADDR] [--rpc URL] [--workers W]

# Example (defaults: grpc=127.0.0.1:10000, rpc=http://127.0.0.1:8545, workers=4)
go run . 50
```

---

## Development Workflow

This project follows a strict linear process enforced by the global CLAUDE.md:

```
Requirements → Design → Coding → Testing → Compliance Review → Quality Review → Commit
```

- Requirements docs: `docs/requirements/<feature>-requirement.md`
- Design docs: `docs/design/<feature>-design.md` (must include alternative approaches + ADR)
- Each phase requires explicit user approval before proceeding to the next.

---

## Current Project State

| Feature | Status |
|---------|--------|
| ExEx gRPC push server (v1.0) | ✅ Complete (commit 683fed6) |
| go-trace-verifier | ✅ Complete (commit c7eb605) |
| SubscribeRequest field filtering | ✅ Complete (commit 9fa3ee7) |
| Upgrade reth to v2.4.0 | ✅ Complete (commit 649664f), deployment verified (AC4–AC6 passed) |
