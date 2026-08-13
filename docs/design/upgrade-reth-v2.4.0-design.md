# Design: Upgrade reth Dependency to v2.4.0

## Background

Upgrade reth-remote-exex's reth dependency from `dev-exex-internal-txs-v2.3.0` (v2.3.0) to `dev-exex-internal-txs-v2.4.0` (v2.4.0).

## Goal

Switch dependency branches and fix all compilation errors, ensuring existing functionality and tests remain unaffected.

## Module Impact Analysis

| File | Expected Change | Reason |
|------|----------------|--------|
| `Cargo.toml` | Must change | Switch git branch; may need to upgrade alloy/revm versions to match reth v2.4.0 dependency tree |
| `src/convert.rs` | May change | If reth/alloy types have breaking changes (field renames, method signature changes) |
| `src/exex.rs` | May change | If ExExContext/CanonStateNotification APIs changed |
| `src/lib.rs` | No change | Only re-exports, no direct type dependencies |
| `build.rs` | No change | tonic-build unaffected by reth version |
| `proto/exex.proto` | No change | Proto definition is controlled by us, data semantics unchanged |

## Interface Definition

No changes. This upgrade does not modify any public interfaces (gRPC proto unchanged, SubscribeFlags unchanged).

## Data Structures

No changes. Proto message structures and SubscribeFlags remain the same.

## Dependency Graph

```
reth-remote-exex
├── reth (git, branch: dev-exex-internal-txs-v2.4.0)         ← changed
├── reth-exex (same)                                          ← changed
├── reth-node-ethereum (same)                                 ← changed
├── reth-node-api (same)                                      ← changed
├── reth-tracing (same)                                       ← changed
├── reth-execution-types (same)                               ← changed
├── reth-ethereum-primitives (same)                           ← changed
├── reth-primitives-traits (crates.io)                        ← may need version bump
├── alloy-* (crates.io)                                       ← may need version bump
├── revm-* (crates.io)                                        ← may need version bump
└── tonic/prost/tokio (unchanged)
```

## Implementation Strategy

Use a **compiler-driven fix** approach:

1. Modify all `bnmlsp/reth` dependency branch fields in Cargo.toml
2. Run `cargo build` and fix compilation errors one by one
3. Expected fix categories:
   - Version incompatibility: bump alloy/revm/reth-primitives-traits versions in Cargo.toml with exact pinning (`=` prefix) to prevent Cargo from resolving to newer incompatible versions
   - API changes: update type references and method calls in src/convert.rs or src/exex.rs
4. Once compilation passes, run `cargo test` to confirm all tests pass

## Alternative Approaches

| Approach | Description | Trade-off |
|----------|-------------|-----------|
| A: Compiler-driven fix (adopted) | Switch branch directly, fix based on compiler errors | Most direct and efficient for a pure upgrade task |
| B: Pre-analyze upstream diff | Compare reth v2.3.0 vs v2.4.0 API differences, plan changes in advance | Overkill for a small project; reth codebase is huge, diff signal-to-noise ratio is low |

Only approach A is reasonable — this project's Rust code is small (~750 lines across convert.rs + exex.rs), and the compiler will precisely report all locations that need modification.

## Known Risks and Limitations

- reth v2.4.0 may introduce new `ExExNotification` variants or `AccountStatus` enum values requiring match arm additions
- Major alloy/revm version bumps may cause significant type path changes
- If `call_traces()` method signature changes, return type compatibility must be verified

## Test Plan

No new test cases are added. Verification is that all existing tests pass.

| Case ID | Scenario | Input | Expected Result |
|---------|----------|-------|-----------------|
| T1 | Successful compilation | `cargo build --release` | exit code 0, no compilation errors |
| T2 | Unit tests pass | `cargo test` | All existing tests pass |
| T3 | Go client compilation (conditionally triggered) | Not triggered if proto unchanged; if changed, regenerate then `go build ./...` | Compilation succeeds |

Test case details:
- **T1**: Verifies all API adaptations are correct, types match, no missing imports
- **T2**: Verifies flag filtering logic in convert.rs behaves identically under new dependencies
- **T3**: Proto is not expected to change (requirements explicitly state no functional changes), but if unexpectedly needed, this case is triggered

## Out of Scope

- No use of v2.4.0 new APIs
- No proto modifications
- No changes to public behavior
- AC4 (deployment verification), AC5 (call trace correctness), AC6 (bench tools operational) are verified manually by the user post-deployment per the requirements document. No design-time action required.

## ADR: Exclude `jit` feature from reth dependency

- **Title**: Exclude reth's `jit` default feature to avoid LLVM 22 build dependency
- **Status**: Adopted
- **Background**: reth v2.4.0 adds `jit` to its default features. This feature enables experimental `revmc` JIT compilation of EVM bytecode via LLVM. It introduces `llvm-sys v221` as a transitive dependency, which requires LLVM 22 installed on the build machine (not available in Ubuntu 24.04 default repos — requires adding the LLVM apt repository and setting `LLVM_SYS_221_PREFIX`).
- **Decision**: Exclude `jit` from default features by using `default-features = false` with an explicit feature list that includes all other defaults.
- **Rationale**: JIT is experimental in v2.4.0 and disabled at runtime by default (requires `--jit.enabled` CLI flag). Excluding it at compile time produces identical runtime behavior to the official reth binary when run without `--jit.enabled`. This avoids adding a heavy system dependency (LLVM 22) to the build environment for a feature that is not yet production-ready.
- **Consequences**: If/when JIT becomes stable and enabled by default in a future reth release, this decision should be revisited — at that point, installing LLVM on the build machine would be justified.

## ADR: Exclude `gmp` feature from reth dependency

- **Title**: Exclude reth's `gmp` default feature to avoid `m4` build dependency
- **Status**: Adopted
- **Background**: reth v2.4.0 adds `gmp` to its default features (not present in v2.3.0). This feature enables the C library GMP (`gmp-mpfr-sys`) for the modexp precompile (EIP-198, address `0x05`), which performs modular exponentiation (`base^exp mod modulus`). Building `gmp-mpfr-sys` requires the system tool `m4`. Without `gmp`, revm falls back to a pure Rust implementation (`aurora_engine_modexp`) that produces identical results.
- **Decision**: Exclude `gmp` from the feature list, consistent with v2.3.0 behavior.
- **Rationale**: The modexp precompile is rarely called on Ethereum mainnet. The pure Rust fallback is functionally correct — only slower for very large integer operations. v2.3.0 ran without `gmp` with no issues. Excluding it keeps the build environment simple (no additional system dependency).
- **Consequences**: If future workloads involve heavy modexp usage, or if `gmp` becomes required (no fallback), this decision should be revisited. Re-enabling only requires `sudo apt-get install -y m4` on the build machine and adding `"gmp"` back to the features list.
