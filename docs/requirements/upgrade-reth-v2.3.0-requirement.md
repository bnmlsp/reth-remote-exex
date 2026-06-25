# Requirements: Upgrade reth Dependency to v2.3.0

## Background

The project currently depends on the custom fork `bnmlsp/reth`, branch `dev-exex-internal-txs` (reth v2.2.0). An upstream branch `dev-exex-internal-txs-v2.3.0` (based on reth v2.3.0) is now available. The project needs to upgrade to the new branch to track the upstream version.

## Goal

Upgrade reth-remote-exex's reth dependency from v2.2.0 (branch `dev-exex-internal-txs`) to v2.3.0 (branch `dev-exex-internal-txs-v2.3.0`), ensuring the project compiles and all tests pass.

## Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| R1 | Switch all `bnmlsp/reth` git dependencies in Cargo.toml from branch `dev-exex-internal-txs` to `dev-exex-internal-txs-v2.3.0` | Must |
| R2 | Fix all compilation errors caused by reth v2.3.0 API changes so that `cargo build --release` succeeds | Must |
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

- **Given** Cargo.toml reth dependencies have been switched to the `dev-exex-internal-txs-v2.3.0` branch
- **When** running `cargo build --release`
- **Then** compilation succeeds with no errors

### AC2: Tests pass (covers R3)

- **Given** the project has compiled successfully
- **When** running `cargo test`
- **Then** all existing tests pass

### AC3: Go client compatibility (covers R4, conditionally triggered)

- **Given** proto/exex.proto was modified during the upgrade
- **When** Go proto files are regenerated and `go build ./...` is run in go-consumer and go-trace-verifier directories
- **Then** compilation succeeds

## Out of Scope

- No functional changes leveraging reth v2.3.0 new APIs
- No new tag
- No modifications to code unrelated to the upgrade
- No upgrading alloy or other dependencies unless forced by reth v2.3.0

## Dependencies

- Upstream branch `dev-exex-internal-txs-v2.3.0` (`https://github.com/bnmlsp/reth.git`) is ready

## Assumptions and Constraints

- The upstream fork's `dev-exex-internal-txs-v2.3.0` branch includes the same ExEx internal txs feature extensions as `dev-exex-internal-txs`
- The proto definition is controlled by this project and is not directly affected by reth version changes (unless reth-side data structure changes necessitate proto adjustments)
