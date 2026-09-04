---
work_package_id: WP17
title: M2 Crown Campaign Evidence and Successor Contract
dependencies:
- WP16
requirement_refs:
- FR-001
- FR-003
- FR-019
- FR-020
- FR-021
- FR-022
- FR-023
- FR-024
- FR-025
- FR-026
- FR-031
- FR-032
- FR-035
- FR-036
- NFR-001
- NFR-002
- NFR-003
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-009
- NFR-010
- NFR-011
- NFR-012
- NFR-013
- NFR-014
- NFR-015
- NFR-016
- NFR-017
- NFR-018
- C-001
- C-018
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T086
- T087
- T088
- T089
- T090
- T091
- T092
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/testcampaign/m2/
create_intent:
- internal/testcampaign/m2/manifest.go
- internal/testcampaign/m2/runner.go
- internal/testcampaign/m2/runner_test.go
- cmd/spk-m2-qualification/main.go
- cmd/spk-m2-qualification/main_test.go
- .github/workflows/m2-qualification.yml
- docs/programs/evidence/m2-qualification.json
- docs/programs/evidence/m2-acceptance-matrix.json
- docs/programs/evidence/m2-independent-review-input.md
execution_mode: code_change
model: ''
owned_files:
- internal/testcampaign/m2/manifest.go
- internal/testcampaign/m2/runner.go
- internal/testcampaign/m2/runner_test.go
- cmd/spk-m2-qualification/main.go
- cmd/spk-m2-qualification/main_test.go
- Makefile
- .github/workflows/quality.yml
- .github/workflows/m2-qualification.yml
- README.md
- docs/PRODUCTION_RUNTIME_PROGRAM.md
- docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md
- docs/programs/evidence/m2-qualification.json
- docs/programs/evidence/m2-acceptance-matrix.json
- docs/programs/evidence/m2-independent-review-input.md
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP17 - M2 Crown Campaign Evidence and Successor Contract

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, use `spec-kitty agent profile list`, select the
closest implementation profile, and record the governed invocation before any
production or evidence edit.

## Objective

Build and execute the M2 crown qualification: one exact source-bound census of
capability behavior, faults, ambiguity, concurrency, restart, recovery,
performance, SQLite absence, installation, and mutants. Produce independently
verifiable evidence and stable successor contracts for M3/M4 without claiming
that machine qualification is human adoption.

Implementation command:

```bash
spec-kitty agent action implement WP17 --agent codex
```

## Post-approval correction: source-bound restore deadline evidence

Read `post-approval-defect-8.md`. The final frozen census must include WP01's
structural restore-time confinement tests, WP04's named deadline contract tests,
and every exact-anchored restore overlay mutant. Bind the production source
commit/tree, each substitution and anchor count, and each killed-by test to the
same RunID. Refuse a campaign that substitutes generated fake-package behavior,
omits a mutant, accepts a zero/multiple source anchor, or assembles structural
and behavioral evidence from different source identities.

## Branch, dependency, and evidence rules

- Start from accepted WP16 as recorded for WP17 in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Planning and merge target: `feat/gapdb-only-production-authority`.
- Work only in the Spec Kitty-provided lane worktree.
- Edit only the `owned_files` paths declared in frontmatter.
- Preserve all other agents' work and never rewrite unrelated evidence.
- Qualification helpers are test/release tooling, never production authority.
- One admissible run uses one clean source commit/tree, module graph, Go
  toolchain, sidecar, binaries, SBOM, install output, and RunID.
- Never assemble a pass from different runs, commits, machines, reruns, or
  substituted artifacts.
- Failed, incomplete, canceled, and non-admissible attempts remain immutable
  evidence; do not overwrite or delete them to obtain green status.
- Machine success yields `release_ready`/review eligibility only. It does not
  create a human adoption decision or authorize self-approval.

Read every M2 mission document and contract before implementation, especially
`contracts/m2-qualification-v1.schema.json`, `quickstart.md`, the v2 event
registry, the M1 adoption record, WP16 absence outputs, and every accepted WP
review. Freeze an exact census before executing the first final run.

## T086: Implement the source-bound M2 manifest, census, and verifier

**Purpose**: Make every final claim reproducible from immutable identities and
complete machine-checkable evidence rather than filenames or narrative.

**Files**: `internal/testcampaign/m2/**`, `cmd/spk-m2-qualification/**`, and
`docs/programs/evidence/m2-*` fixtures.

**Implementation requirements**:

1. Implement `preflight`, `run`, `verify`, and bounded diagnostic commands for
   `spk-m2-qualification`; human-formatted output is never parsed internally.
2. Emit the exact schema-v1 manifest required by
   `contracts/m2-qualification-v1.schema.json`, including program/mission/RunID,
   source, M1 admission, census, performance, artifacts, and verdict.
3. Bind source commit/tree, Go version, `go.mod`/`go.sum` digests, M1 adoption
   digest, evidence-manifest/tuple/sidecar digests, qualifier and product binary
   digests, SBOM, install artifacts, contracts, acceptance matrix, and raw
   measurement/campaign artifacts.
4. Freeze a closed census before running: every registered authority/event
   family, frozen Exact/Strengthen behavior, refusal, fault cut, ambiguity cut,
   concurrency/restart case, mutant, absence surface, platform/build target, and
   expected artifact.
5. Give each item a stable ID, expected repetitions, owner WP, command/test
   selector, evidence path/digest, observed count, pass/fail, and substitution
   status. Unknown, duplicate, missing, extra, or zero-count required items fail.
6. Require one exact source identity for all subprocesses and artifacts. Detect
   dirty trees, untracked production inputs, source changes during a run,
   mismatched binaries, reused output directories, and mixed RunIDs.
7. Recompute every artifact digest and census count during `verify`; do not
   trust manifest totals, symlinks, absolute paths outside the evidence root, or
   caller-supplied verdicts.
8. Require zero SQLite edges from WP16 across source, module, SBOM, binary,
   install, configuration, help, runtime, and mutants.
9. Use canonical ordering, bounded file/count/byte budgets, atomic evidence-file
   publication, and typed errors that reveal no secrets or raw provider output.
10. Record runner/reference-machine identity and timing methodology without
    treating machine metadata as source authority.

**Required tests**:

- Valid complete manifest round-trips and verifies byte-for-byte.
- Mutate every required identity/count/digest/verdict and require refusal.
- Mixed-commit, mixed-RunID, missing artifact, symlink, duplicate census,
  unexplained substitution, and assembled-pass mutants fail.
- A failed campaign stays verifiable as failed rather than disappearing.

**Exit evidence**:

```bash
go test ./internal/testcampaign/m2 ./cmd/spk-m2-qualification -run 'Manifest|Census|Verify|SourceBound' -count=10
```

## T087: Run the complete two-item restart-every-phase crown

**Purpose**: Prove the entire authority model works as one product, not merely
as independently green packages.

**Files**: `internal/testcampaign/m2/**` and qualifier command wiring.

**Scenario requirements**:

1. Use one real local Git repository, one real admitted GapDB sidecar/database,
   one fake but adversarial tracker, two independent work items, two distinct
   ActorIDs/processes, and separate lease-scoped workspaces.
2. Execute mission create/definition, artifact approval, WorkRevision
   finalization, concurrent claim, workspace preparation, implementation
   candidate, handoff, independent review, acceptance, and serialized
   integration to terminal completion.
3. Restart the production owner after every major phase and every durable saga
   stage. Each restart restores from checkpoint plus suffix, admits a fresh
   epoch, rejects predecessor capsules, and preserves exact results.
4. Kill one agent after lease/workspace acquisition, expire its lease,
   quarantine W1, start a replacement in fresh W2, then let the zombie modify
   W1. Every later gate rejects W1 while W2 completes.
5. Require reviewer ActorID independence, complete eligibility, exact current
   candidate/artifact/policy evidence, and repository refusal before fencing.
6. Run two integration requests concurrently and prove one target authority;
   the second receives the exact typed busy/replay outcome without duplicate
   publication.
7. Compare terminal projection, Git refs/commits, operation/effect membership,
   tracker observations, and audit evidence with an uninterrupted reference
   execution. Authority facts and digests must be identical where specified.
8. Assert playback performs zero external calls/writes and tracker/Git state
   never grants mission authority.

The scenario is invalid if it uses an in-memory fake store, embedded GapDB,
SQLite comparator, mocked Git ref update, shared workspace, or omitted restart.

**Exit evidence**:

```bash
go test ./internal/testcampaign/m2 -run 'TwoItemCrown|RestartEveryPhase' -count=1
```

## T088: Run per-family faults, ambiguity, concurrency, and restarts

**Purpose**: Prove every authority-changing family survives the exact places
where process lifetime, durable state, and external reality can disagree.

**Files**: `internal/testcampaign/m2/**`.

**Campaign requirements**:

1. Enumerate every family from the frozen registry; an unclassified family is
   an incomplete campaign, not a skip.
2. For each applicable family, run at least 100 faults before publication,
   after durable publication/before acknowledgement, and after acknowledgement/
   before memory projection. Add external-effect cuts before call, after call/
   before observation, and after observation/before result publication.
3. Run at least 100 lost/malformed acknowledgement retries per family. Same
   OperationID/binding yields one result and zero duplicates; changed binding
   refuses every time.
4. Run at least 100 same-aggregate contention trials: exactly one incompatible
   update wins and all losers are typed stale/conflict.
5. Run at least 100 two-agent independent-work trials with real separate
   workspaces and no global authority serialization.
6. Run at least 100 clean/unclean owner restarts: unique epoch, identical
   restored projection, and universal predecessor-capsule refusal.
7. Include slow/unavailable/dishonest tracker, manual Git movement, zombie
   workspace, stale WorkRevision, policy/artifact drift, review ActorID conflict,
   and compare-before-compensate cases.
8. Assert universal invariants after every successful and failed iteration,
   including complete operation/event/head membership, no partial authority,
   no blind external retry, and no playback I/O.
9. Retain per-iteration seed, fault boundary, typed outcome, duration, artifact
   digest, and invariant result. A rerun cannot replace a failed iteration.

**Exit evidence**:

```bash
GOTOOLCHAIN=go1.26.7 make m2-fault m2-race m2-mutants
```

## T089: Measure recovery, query, and durable mutation performance

**Purpose**: Demonstrate that memory-primary operation is fast and resumable on
the declared reference machine without hiding failed or warm-up samples.

**Files**: `internal/testcampaign/m2/**` and qualifier evidence output.

**Implementation requirements**:

1. Define and record CPU, memory, kernel, filesystem, Go version, sidecar/binary
   digests, power/load controls, dataset generator, warm-up, sample inclusion,
   percentile method, timeout, and raw observation format.
2. Generate at least 10,000 accepted operations spanning all authority families
   with a compatible checkpoint followed by a 10,000-operation suffix fixture
   required by NFR-008/NFR-009.
3. Run recovery at least 30 independent times from clean process memory. Require
   zero missing/duplicate operations, exact projection digest, and p95 below
   2,000 ms.
4. Measure at least 10,000 bounded current-state Get/List observations under the
   declared local workload; require p95 below 2 ms.
5. Measure at least 10,000 accepted durable authority mutations, including
   acknowledgement and required memory publication; require p95 below 8 ms.
6. Preserve every raw sample and classify warm-up, environmental invalidation,
   timeout, and exclusion visibly. Do not drop slow samples post hoc.
7. Use monotonic elapsed timing and compute percentiles deterministically in the
   verifier. Report counts, min/median/p95/p99/max, failures, and thresholds.
8. Keep performance results non-admissible when the reference environment,
   dataset, source, sidecar, or sample census differs.

**Exit evidence**:

```bash
GOTOOLCHAIN=go1.26.7 make m2-recovery m2-performance
```

## T090: Wire bounded CI tiers and deterministic CycloneDX SBOM

**Purpose**: Keep PR feedback usable while ensuring the full crown and absence
campaign cannot be skipped before release evidence.

**Files**: `Makefile`, `.github/workflows/quality.yml`,
`.github/workflows/m2-qualification.yml`, and qualification helpers.

**Implementation requirements**:

1. Provide stable Make targets: `m2-contract`, `m2-capabilities`, `m2-recovery`,
   `m2-race`, `m2-fault`, `m2-mutants`, `m2-absence`, `m2-sbom`,
   `m2-performance`, `m2-short`, and `m2-qualification`.
2. `m2-short` gates PRs with contracts, focused capability/refusal tests,
   architecture/absence guards, compile/vet/static analysis, bounded race smoke,
   module verification, and deterministic selectors under a declared timeout.
3. Nightly/manual full workflow runs the complete 100-repetition campaigns,
   crown scenario, clean-install/absence proof, recovery/performance, SBOM, and
   verifier. Release qualification cannot substitute the short tier.
4. Pin GitHub Actions by full commit SHA, use least privilege, bounded timeouts,
   explicit Go 1.26.7, no mutable downloaded executable, and immutable artifact
   names containing source identity and RunID.
5. Generate deterministic CycloneDX JSON from the complete module and built
   production graph with stable ordering/timestamps policy. Validate schema and
   inspect it for SQLite/SQL driver/provider residue.
6. Run SBOM generation twice from identical inputs and require byte-identical
   output/digest. Include generator identity/version in source-bound evidence.
7. Failed/nightly results remain artifacts but are `non_admissible` unless the
   exact clean release preflight and full census succeed.
8. A missing target, skipped step, timeout, canceled job, artifact-upload
   failure, or partial matrix makes qualification incomplete/fail, never green.

**Exit evidence**:

```bash
GOTOOLCHAIN=go1.26.7 make m2-short
GOTOOLCHAIN=go1.26.7 make m2-sbom
```

## T091: Independently verify evidence and publish successor contracts

**Purpose**: Connect requirements to inspectable evidence and tell M3/M4 what
they may safely consume without leaking storage internals.

**Files**: `docs/programs/evidence/m2-acceptance-matrix.json`,
`docs/programs/evidence/m2-independent-review-input.md`, `README.md`,
`docs/PRODUCTION_RUNTIME_PROGRAM.md`,
`docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md`, and `docs/programs/evidence/m2-*`.

**Implementation requirements**:

1. Build an acceptance matrix mapping every FR, NFR, constraint, success
   criterion, authority/event family, frozen Exact/Strengthen behavior, fault,
   race, mutant, performance threshold, absence surface, and artifact to stable
   evidence IDs and digests.
2. Require 100% accounted coverage with no placeholder, unexplained exclusion,
   substituted artifact, or implementation assertion counted as evidence.
3. Run the verifier from a fresh extracted copy of the archive using independently
   built verifier coordinates where defined; recompute source/artifact digests,
   census totals, SBOM absence, performance statistics, and verdict.
4. Record independent reviewer ActorID/ProcessID/profile revision, reviewed
   source/tree/RunID/manifest digest, commands, findings, and verdict in the
   mission review report. The implementing actor cannot approve its own work.
5. Update active README/program architecture honestly: M2 is qualified only if
   the exact crown passed and independent review has no unresolved critical/high
   finding. Preserve immutable earlier evidence and failed attempts.
6. Publish a successor contract for M3/M4 naming stable consumer-owned ports,
   event-family/schema/reducer tuples, operation/replay semantics, progress
   projection schema, single-owner boundary, sidecar/database admission,
   checkpoint/restore behavior, structural-absence evidence, and unsupported
   historical-root disposition.
7. Explicitly forbid successors from opening GapDB directly, importing adapters
   into kernels, reviving SQLite, trusting tracker/Git state as authority, or
   depending on qualification-only packages.
8. Mark CLI/API/MCP/Team Kitty/real-agent/stack-neutral acceptance work as later
   missions, not silently shipped by M2 evidence.

**Exit evidence**:

```bash
GOTOOLCHAIN=go1.26.7 make m2-contract m2-verify-evidence
```

## T092: Run final clean qualification and seal a release-ready archive

**Purpose**: Produce one complete immutable candidate evidence set without
self-adoption, evidence laundering, or fallback after failure.

**Files**: all WP17-owned qualification/evidence/document paths.

**Required procedure**:

1. Choose an exact clean candidate commit/tree after all code/document changes;
   record module/toolchain/sidecar/contract inputs before execution.
2. Create a new unique RunID and empty private runtime/data/evidence/install
   roots. Reverify the published immutable M1 archive and adoption tuple.
3. Build all production and qualification binaries with Go 1.26.7, `CGO_ENABLED=0`,
   `-trimpath`, then record hashes before running any crown step.
4. Run `m2-qualification` once for that RunID: contracts, capability parity,
   crown, all fault/concurrency/restart/mutant campaigns, historical-root matrix,
   absence, clean install/runtime smoke, SBOM, and performance.
5. If anything fails or source changes, close that attempt as failed/incomplete/
   non-admissible, retain it immutably, correct the product on a new commit, and
   start a new RunID. Never patch an evidence directory in place.
6. Run independent verification and review against a fresh archive extraction.
   Resolve every critical/high finding through a new candidate/run.
7. Create a deterministic archive and checksum containing the manifest, census,
   raw results, logs/diagnostics, SBOM, binary/install hashes, acceptance matrix,
   review, and successor contract. Verify extract-and-recompute succeeds.
8. Store the release-ready archive metadata under `docs/programs/evidence/m2-*`
   with exact source/tree/RunID/archive/manifest digests and prior-attempt index.
9. Report `release_ready`, not `adopted`. Do not create/sign a human adoption
   record, tag/release as adopted, merge outside Spec Kitty, or delete rollback
   evidence. A human/program gate owns the later adoption decision.

**Final validation**:

```bash
GOTOOLCHAIN=go1.26.7 make m2-short
GOTOOLCHAIN=go1.26.7 make m2-qualification
git diff --check
```

## Definition of Done

- One exact manifest and frozen census account for every required claim.
- The real two-item restart-every-phase crown passes end to end.
- Every authority family meets 100-run fault/ambiguity/idempotency coverage.
- Two-agent, same-aggregate, zombie, manual-Git, and restart campaigns pass.
- Recovery/query/mutation thresholds pass with complete raw samples.
- PR-short and nightly/manual-full CI tiers are separate and fail closed.
- Deterministic CycloneDX SBOM and WP16 proof report zero SQLite edges.
- Acceptance matrix has complete requirement/behavior/evidence traceability.
- Independent verification/review has no unresolved critical/high finding.
- README/program docs and M3/M4 successor contracts match shipped behavior.
- Every failed/incomplete/non-admissible attempt remains preserved.
- One extractable checksum-verified archive is marked release-ready only.
- T086–T092 are recorded `done` in the canonical task event log.
- The implementation neither self-approves nor self-adopts M2.

After all seven subtasks and evidence are genuinely complete:

```bash
spec-kitty agent tasks mark-status T086 T087 T088 T089 T090 T091 T092 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and mitigations

| Risk | Required mitigation |
|---|---|
| Pass assembled from several commits/runs | Freeze census and bind every artifact to one commit/tree/RunID. |
| Long suite causes developers to skip gates | Keep bounded PR-short and mandatory nightly/manual-full tiers. |
| Flaky timing is hidden by reruns | Preserve raw samples and failed attempts; use declared reference controls. |
| Coverage totals conceal missing families | Closed registry-derived census with stable IDs and zero unexplained omissions. |
| Qualification helper becomes production path | Architecture/reachability guard test-only package and command boundaries. |
| SBOM is nondeterministic or incomplete | Generate twice, validate CycloneDX, bind generator and built graph. |
| Reviewer trusts producer manifest | Fresh extraction independently recomputes hashes, counts, thresholds, and absence. |
| Machine pass is mislabeled adoption | Emit release-ready only; require a separate human/program decision afterward. |

## Independent reviewer guidance

Start from the archive, not the producer's summary. Recompute its checksum,
extract into a fresh directory, run the verifier, and independently trace every
manifest/census/review-input entry to immutable raw evidence. Confirm all
artifacts share one source/tree/RunID/toolchain/sidecar identity and all prior
failed attempts remain visible.

Reproduce at least the crown scenario, one fault per family, simultaneous
integration, zombie workspace rejection, wrong-root/database refusal, lost
acknowledgement, memory-loss restore, SQLite absence, SBOM determinism, and
performance calculation. Verify every action/workflow is pinned and no skipped,
canceled, timed-out, or upload-failed job can produce a pass.

Reject mixed evidence, mutable aliases, unexplained substitutions, missing raw
samples, unregistered families, grep-only mutants, fake-store crown runs,
provider/Git authority leakage, or qualification code reachable by production.
The final verdict may say `release_ready`; adoption remains an explicit later
human decision.
