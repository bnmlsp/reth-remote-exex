# Requirements: Upgrade reth Dependency to v2.5.2

## Background

The project currently depends on the custom fork `bnmlsp/reth`, branch `dev-exex-internal-txs-v2.5.0` (reth v2.5.0), pinned on `main` at tag `for_reth_v2.5.0`. An upstream branch `dev-exex-internal-txs-v2.5.2` (based on reth v2.5.2, head `fefe80c`) is now available. The project needs to upgrade to the new branch to track the upstream version.

## Goal

Upgrade reth-remote-exex's reth dependency from v2.5.0 (branch `dev-exex-internal-txs-v2.5.0`) to v2.5.2 (branch `dev-exex-internal-txs-v2.5.2`), ensuring the project compiles, all tests pass, and deployed functionality is not degraded.

## Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| R1 | Switch all `bnmlsp/reth` git dependencies in Cargo.toml from branch `dev-exex-internal-txs-v2.5.0` to `dev-exex-internal-txs-v2.5.2` | Must |
| R2 | Fix all compilation errors caused by reth v2.5.2 API changes so that `cargo build --release` succeeds | Must |
| R3 | Ensure all existing unit tests pass (`cargo test`) | Must |
| R4 | If proto/exex.proto changes due to the upgrade, regenerate Go client proto files and ensure `go build ./...` succeeds in all Go example directories | Should |

Priority note: 3 of 4 requirements are Must (75%, above the 60% guideline). This is inherent to a dependency-upgrade task — compilation and tests are non-negotiable, and R4 is the only conditional item. Confirmed with the user to keep this split.

## Non-Functional Requirements

| Category | Conclusion |
|----------|------------|
| Performance | Not in scope; existing bench tools are only required to run without error (AC6), no latency threshold asserted |
| Availability | Not in scope |
| Security | Not in scope |
| Scalability | Not in scope |
| Observability | Not in scope; logging/tracing behaviour unchanged |
| Data retention | Not in scope |
| Compatibility | gRPC/proto interface must stay backward compatible for existing Go clients; if proto changes, generated Go files must be updated (R4) |

## Acceptance Criteria

### AC1: Successful compilation (covers R1, R2)

- **Given** Cargo.toml reth dependencies have been switched to the `dev-exex-internal-txs-v2.5.2` branch
- **When** running `cargo build --release`
- **Then** compilation succeeds with no errors

### AC2: Tests pass (covers R3)

- **Given** the project has compiled successfully
- **When** running `cargo test`
- **Then** all existing tests pass

### AC3: Go client compatibility (covers R4, conditionally triggered)

- **Given** proto/exex.proto was modified during the upgrade
- **When** Go proto files are regenerated and `go build ./...` is run in every Go example directory
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

### AC7: Abnormal path — upstream API incompatibility

- **Given** reth v2.5.2 removed or changed an API the ExEx relies on in a way that cannot be fixed without a design change
- **When** the incompatibility is found during compilation
- **Then** work stops, the incompatibility is reported to the user, and no workaround is implemented before the user approves a design update

## Out of Scope

- No functional changes leveraging reth v2.5.2 new APIs
- No performance tuning or latency-target assertions
- No modifications to code unrelated to the upgrade
- No upgrading of alloy or other dependencies unless forced by reth v2.5.2
- No tag creation until the user confirms AC4–AC6 passed in a real deployment

## Dependencies

- Upstream branch `dev-exex-internal-txs-v2.5.2` (`https://github.com/bnmlsp/reth.git`, head `fefe80c`) is ready
- R2 depends on R1; R3 depends on R2; R4 is conditional on proto changes observed while satisfying R2
- AC4–AC6 depend on a real reth node deployment provided by the user

## Assumptions and Constraints

- The upstream fork's `dev-exex-internal-txs-v2.5.2` branch includes the same ExEx internal-txs feature extensions as `dev-exex-internal-txs-v2.5.0`
- The proto definition is controlled by this project and is not directly affected by reth version changes (unless reth-side data structure changes necessitate proto adjustments)
- Work is committed directly on `main` (user decision); tag `for_reth_v2.5.2` is created only after the user confirms deployment verification
- AC4–AC6 are verified manually by the user in a real deployment environment; AI assists up to compilation and test passing

## Change History

- [2026-09-23] Initial version / New upstream branch `dev-exex-internal-txs-v2.5.2` available / Cargo.toml dependency branch, possible API-adaptation code changes
