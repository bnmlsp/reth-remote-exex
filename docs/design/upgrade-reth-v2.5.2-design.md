# Design: Upgrade reth Dependency to v2.5.2

## Background

The project pins `bnmlsp/reth` branch `dev-exex-internal-txs-v2.5.0` (reth v2.5.0). Upstream branch `dev-exex-internal-txs-v2.5.2` (reth v2.5.2, head `fefe80c`) is available. See `docs/requirements/upgrade-reth-v2.5.2-requirement.md`.

## Goal

Switch the seven `bnmlsp/reth` git dependencies to the v2.5.2 branch and fix all resulting compilation errors, keeping existing behaviour, interfaces and tests unchanged.

## Module Impact Analysis

| File | Expected Change | Reason |
|------|----------------|--------|
| `Cargo.toml` | Must change | Switch git branch on 7 deps; possibly re-pin `reth-primitives-traits` / `alloy-*` / `revm-*` exact versions to match reth v2.5.2's dependency tree |
| `Cargo.lock` | Must change | New git revision + any transitive version moves |
| `src/convert.rs` (631 lines) | May change | If alloy/revm/reth types have breaking changes (field renames, method signatures, new enum variants needing match arms) |
| `src/exex.rs` (121 lines) | May change | If `ExExContext`, `ExExNotification`, `CanonStateNotification` or the node-builder API changed |
| `src/lib.rs` (9 lines) | No change | Re-exports only |
| `build.rs` | No change | tonic-build is independent of reth |
| `proto/exex.proto` | No change | Proto is owned by this project; data semantics unchanged by a patch-level reth bump |
| `examples/**` (Go) | No change expected | Only affected if proto changes (R4, conditional) |

## Interface Definition

No changes. The gRPC service (`proto/exex.proto`), `SubscribeFlags`, and all public re-exports in `src/lib.rs` stay byte-identical. This is a pure dependency bump: callers (go-consumer, go-trace-verifier, the four bench tools) require no recompilation.

If the compiler forces a public-signature change in `convert.rs`, that is a design deviation — work stops and the design doc is updated first (per requirement AC7).

## Data Structures

No changes. Protobuf messages and `SubscribeFlags` field set, types and defaults are unchanged.

## Dependency Graph

```
reth-remote-exex
├── reth                      (git, branch dev-exex-internal-txs-v2.5.2)   ← changed
├── reth-exex                 (same branch)                                 ← changed
├── reth-node-ethereum        (same branch)                                 ← changed
├── reth-node-api             (same branch)                                 ← changed
├── reth-tracing              (same branch)                                 ← changed
├── reth-execution-types      (same branch, feature "traces")               ← changed
├── reth-ethereum-primitives  (same branch)                                 ← changed
├── reth-primitives-traits    "=0.6.0"        (crates.io)                   ← may need re-pin
├── alloy-consensus / -eips / -rpc-types-trace "=2.3.0"                     ← may need re-pin
├── alloy-primitives "=1.6.1", alloy-eip2930 "=0.2.3", alloy-eip7702 "=0.6.3" ← may need re-pin
├── revm-database / revm-state "=42.0.0"                                    ← may need re-pin
└── tonic 0.11 / prost 0.12 / tokio 1 / eyre 0.6                            unchanged
```

Dependency direction is unchanged and acyclic: `exex.rs` → `convert.rs` → `proto` (generated).

## Implementation Strategy

Compiler-driven fix, in order:

1. Replace `dev-exex-internal-txs-v2.5.0` → `dev-exex-internal-txs-v2.5.2` on all 7 git deps in `Cargo.toml`.
2. `cargo build` (debug, faster feedback) and fix errors one at a time. Expected error classes:
   - **Version mismatch** on the exact-pinned crates.io deps (`=` prefixes above): read the version reth v2.5.2 actually requires and re-pin exactly. Exact pins are kept — never relaxed to caret ranges — so Cargo cannot silently resolve to an incompatible newer release.
   - **API change**: adjust type paths / method calls in `convert.rs` or `exex.rs`.
   - **New enum variants** (`ExExNotification`, `AccountStatus`, tx envelope types): add match arms preserving current semantics.
3. `cargo test` — all existing unit tests must pass unmodified.
4. `cargo build --release` for the deployable binary.
5. Only if `proto/exex.proto` had to change (not expected): regenerate Go proto files per the CLAUDE.md command block, copy to every `examples/*/proto/gen/`, and `go build ./...` in each Go example directory.

Constraint from the minimal-intrusion rule: every line in the final diff must be traceable to a compiler error or to step 1. No refactoring, reformatting, or opportunistic cleanup.

## Exception, Boundary and Concurrency Analysis

| Scenario | Handling |
|----------|----------|
| Compilation error fixable by re-pin / local type adaptation | Fix in place, continue (normal path) |
| API removed or semantically changed such that a design change is needed | Stop, report to user, update design doc before coding (AC7) |
| Upstream branch introduces new mandatory system libraries (as `jit`/`gmp` did in v2.4.0) | Report the required toolchain/system package to the user; do not silently alter `features` |
| `cargo test` failure after successful compilation | Treat as behaviour regression: report the failing case and its output, do not modify the test to make it pass |
| Concurrency | No new concurrency. The existing model (tokio broadcast/mpsc fan-out from the ExEx loop to gRPC subscribers) is untouched; no shared mutable state is added |

## Security Analysis

Not applicable per design-standards §4: this change adds no authentication/authorization, no new user-input handling, no new sensitive-data storage or transport, no new cross-service calls, and no permission changes. The gRPC server's exposure surface is identical to v2.5.0.

## Rollback Plan

- **Trigger**: compilation cannot be completed, `cargo test` regressions that cannot be resolved, or AC4–AC6 failing in deployment.
- **Steps**: `git checkout -- Cargo.toml Cargo.lock src/` for uncommitted work; if already committed on `main`, `git revert <commit>`; redeploy the binary built from tag `for_reth_v2.5.0`.
- **Data consistency impact**: none. The ExEx holds no persistent state of its own; it is a stateless projection of reth's canonical chain notifications. Clients simply reconnect and resume from the current tip.
- No tag is created until the user confirms AC4–AC6, so the last known-good tag stays unambiguous.

## Test Plan

No new test cases. The 9 existing `convert.rs` unit tests are the regression net.

| Case ID | Scenario | Input / Command | Expected Result |
|---------|----------|-----------------|-----------------|
| T1 | Release build succeeds after branch switch (AC1) | `cargo build --release` | Exit code 0, no errors; `target/release/exex` produced |
| T2 | Full unit test suite passes (AC2) | `cargo test` | Exit code 0; 9 tests pass, 0 failed, 0 ignored |
| T3 | Flag filtering unchanged — no flags set (AC2, boundary) | Existing test: `SubscribeFlags` all false | Serialized block contains header-less/empty optional fields exactly as before |
| T4 | Flag filtering unchanged — all flags set (AC2, boundary) | Existing test: all flags true | headers, txs+senders, receipts, withdrawals, state diff, call traces all present |
| T5 | `senders` still bundled with `txs` (AC2, invariant) | Existing test: `include_transactions = true` | Sender addresses present without an independent flag |
| T6 | Go client compilation (AC3, conditional) | Triggered only if `proto/exex.proto` changed: regenerate + `go build ./...` in `examples/go-consumer`, `go-trace-verifier`, `go-latency-bench`, `go-receipts-bench`, `go-internal-txs-bench`, `go-state-diff-bench` | Exit code 0 in every directory |
| T7 | Version-mismatch error path | A crates.io exact pin conflicts with reth v2.5.2's requirement | Cargo reports the conflict; fix is an updated exact pin, never a relaxed range — re-run T1 |
| T8 | Upstream API-break error path (AC7) | Compiler reports a removed/changed reth API needing a design change | Coding stops; incompatibility reported to user before any workaround |

AC4 (deployment stream), AC5 (call-trace correctness vs `debug_traceBlockByNumber`), AC6 (four bench tools run clean) are verified manually by the user post-deployment and require no design-time action.

## Known Risks and Limitations

- A patch bump (v2.5.0 → v2.5.2) usually means low churn, but the fork branch may carry unrelated upstream merges — churn is not bounded by the version number alone.
- Exact-pinned alloy/revm/reth-primitives-traits versions are the most likely failure point; a major alloy bump would cascade into `convert.rs` type paths.
- New default features or system-library requirements in the upstream branch may change the build environment (precedent: `jit`/`gmp` in v2.4.0).
- Branch refs are mutable: a later force-push to `dev-exex-internal-txs-v2.5.2` changes what the branch means. `Cargo.lock` records the effective revision and is committed, so the lock file is the audit anchor for what was actually built.
- Performance is not measured in this task; AC6 only asserts bench tools run, so a latency regression introduced upstream would not be caught here.

## Out of Scope

- No use of reth v2.5.2 new APIs
- No proto modifications
- No public behaviour or interface changes
- No latency/throughput targets or measurement
- No refactoring of unrelated code
- No tag creation before user confirmation of AC4–AC6

## ADR

### ADR-1: Adopt compiler-driven fixing instead of pre-analyzing the upstream diff

- **Status**: Accepted
- **Background**: The upgrade needs all v2.5.2 API breakages found and fixed. Two ways to find them: read the reth v2.5.0→v2.5.2 diff up front, or let `cargo build` report them.
- **Decision**: Switch the branch first and fix errors as the compiler reports them.
- **Rationale**: This project's Rust surface is ~761 lines across three files, while reth is a very large codebase — the upstream diff's signal-to-noise ratio for our small API surface is poor. The compiler is an exhaustive, precise oracle for exactly the APIs we use. Pre-analysis would cost more and still need compilation to confirm.
- **Consequences**: Error discovery is sequential, so the total number of fixes is unknown until the build is clean; a deeply cascading alloy/revm bump could surface late. Mitigated by the small code size and by AC7's stop-and-report rule for anything needing a design change. Consistent with the v2.3.0/v2.4.0/v2.5.0 upgrades, so no precedent break.

### ADR-2: Keep exact (`=`) version pins on crates.io dependencies

- **Status**: Accepted
- **Background**: reth's published crates (`alloy-*`, `revm-*`, `reth-primitives-traits`) must match the versions the forked reth branch compiles against. Alternative: relax to caret ranges and let Cargo resolve.
- **Decision**: Keep `=` exact pins and update them to whatever v2.5.2 requires.
- **Rationale**: Type identity across the reth boundary is version-sensitive — a caret range lets a later patch release of `alloy-primitives` produce "expected X, found X" mismatch errors at an arbitrary future build. Exact pins make the build reproducible and failures deterministic.
- **Consequences**: Every future reth upgrade must manually re-pin these versions — accepted cost, already the established pattern in this repo.
