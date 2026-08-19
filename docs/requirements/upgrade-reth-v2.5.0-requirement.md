# Requirements: Upgrade reth Dependency to v2.5.0

## Background

The project currently depends on the custom fork `bnmlsp/reth`, branch `dev-exex-internal-txs-v2.4.0` (reth v2.4.0). An upstream branch `dev-exex-internal-txs-v2.5.0` (based on reth v2.5.0) is now available. The project needs to upgrade to the new branch to track the upstream version.

## Goal

Upgrade reth-remote-exex's reth dependency from v2.4.0 (branch `dev-exex-internal-txs-v2.4.0`) to v2.5.0 (branch `dev-exex-internal-txs-v2.5.0`), ensuring the project compiles, all tests pass, and deployed functionality is not degraded.

## Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| R1 | Switch all `bnmlsp/reth` git dependencies in Cargo.toml from branch `dev-exex-internal-txs-v2.4.0` to `dev-exex-internal-txs-v2.5.0` | Must |
| R2 | Fix all compilation errors caused by reth v2.5.0 API changes so that `cargo build --release` succeeds | Must |
| R3 | Ensure all existing unit tests pass (`cargo test`) | Must |
| R4 | If proto/exex.proto changes due to the upgrade, regenerate Go client proto files and ensure `go build ./...` succeeds | Should |

## Non-Functional Requirements

| Category | Conclusion |
|----------|------------|
| Performance | Not applicable, maintain current behavior |
| Security | Not applicable |
| Availability | Not applicable |
| Scalability | Not applicable |
| Observability | Not applicable |
| Data retention | Not applicable |
| Compatibility | If proto interface changes, Go client generated files must be updated accordingly |

## Acceptance Criteria

### AC1: Successful compilation (covers R1, R2)

- **Given** Cargo.toml reth dependencies have been switched to the `dev-exex-internal-txs-v2.5.0` branch
- **When** running `cargo build --release`
- **Then** compilation succeeds with no errors

### AC2: Tests pass (covers R3)

- **Given** the project has compiled successfully
- **When** running `cargo test`
- **Then** all existing tests pass

### AC3: Go client compatibility (covers R4, conditionally triggered)

- **Given** proto/exex.proto was modified during the upgrade
- **When** Go proto files are regenerated and `go build ./...` is run in all Go example directories
- **Then** compilation succeeds

### AC4: Deployment verification

- **Given** the upgraded binary is deployed with a running reth node
- **When** go-consumer connects with all subscription flags enabled
- **Then** data is received via gRPC stream without errors

### AC5: Call trace correctness verification

- **Given** the upgraded ExEx is running and streaming call traces
- **When** go-trace-verifier runs against N blocks
- **Then** all call traces match the results from `debug_traceBlockByNumber` RPC

### AC6: Bench tools operational

- **Given** the upgraded ExEx is running
- **When** each bench tool (go-latency-bench, go-receipts-bench, go-internal-txs-bench, go-state-diff-bench) is executed
- **Then** each completes without errors and produces measurement output

## Out of Scope

- No functional changes leveraging reth v2.5.0 new APIs
- No new tag (until deployment is verified)
- No modifications to code unrelated to the upgrade
- No upgrading alloy or other dependencies unless forced by reth v2.5.0

## Dependencies

- Upstream branch `dev-exex-internal-txs-v2.5.0` (`https://github.com/bnmlsp/reth.git`) is ready

## Assumptions and Constraints

- The upstream fork's `dev-exex-internal-txs-v2.5.0` branch includes the same ExEx internal txs feature extensions as `dev-exex-internal-txs-v2.4.0`
- The proto definition is controlled by this project and is not directly affected by reth version changes (unless reth-side data structure changes necessitate proto adjustments)
- AC4-AC6 are verified manually by the user in a real deployment environment; AI assists up to compilation and test passing
