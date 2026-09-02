---
work_package_id: WP14
title: Portable Scenario Harness and Behavior Parity
dependencies:
- WP13
requirement_refs:
- FR-007
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-013
- FR-014
- FR-015
- FR-016
- FR-017
- FR-018
- FR-023
- FR-024
- FR-025
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-015
- NFR-016
- C-009
- C-010
- C-011
- C-012
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T070
- T071
- T072
- T073
- T074
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/scenario/
create_intent: []
execution_mode: code_change
model: ''
owned_files:
- internal/scenario/**
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP14 - Portable Scenario Harness and Behavior Parity

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, run `spec-kitty agent profile list`, resolve the
closest implementation profile, and record that governed profile invocation
before editing production code.

## Objective

Replace the SQLite-aware scenario harness with a portable, production-faithful
harness that owns the control host through its supported contracts and verifies
authority only through typed domain queries and source-bound evidence. Map every
frozen Exact and Strengthen behavior to executable proof, run a complete
two-item mission with restarts, and qualify concurrency, crash, ambiguity,
dishonest-observer, and mutation controls across every authority family.

Implementation command:

```bash
spec-kitty agent action implement WP14 --agent codex
```

## Branch, dependency, and ownership rules

- Start from the accepted WP13 revision assigned to WP14 in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- The planning and merge target is `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty-created lane worktree; do not create one manually.
- Edit only `internal/scenario/**`.
- Preserve other agents' concurrent edits and never revert unrelated changes.
- Exercise the production control host and public typed application/query
  contracts delivered by WP05–WP13. Do not open GapDB directly from scenarios.
- Do not change production code to make a scenario convenient. Report a product
  contract defect to the owning WP rather than adding a harness-only backdoor.
- Preserve the fake tracker contract and its deterministic fault scripting. It
  remains the portable external observer/effect boundary for these tests.
- Never parse human-facing CLI prose. If a process boundary is necessary, use a
  stable typed or versioned machine envelope and assert its schema explicitly.

Before editing, read `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`quickstart.md`, all contracts, the frozen Phase 1–3 behavior inventory, WP13's
production-host contracts, and the existing scenario suite. Classify each old
assertion as product behavior, stronger M2 behavior, or SQLite/test plumbing;
only the first two survive.

## Scenario laws

1. The scenario harness observes authority through domain queries and immutable
   evidence, never SQL, SQLite tables, raw GapDB keys, SDK values, or database
   file inspection.
2. Every scenario starts and stops the same production owner lifecycle used by
   supported composition, with one database identity and one current epoch.
3. Restarts discard memory projection state and recover through the supported
   checkpoint-plus-journal path.
4. The fake tracker is an external system. Its observations are attributable
   evidence and never substitute for Spec Kitty authority.
5. Stable OperationID and PublicationID control replay after timeout, crash, or
   malformed acknowledgement; scenarios never auto-create a new identity.
6. Refusal precedence is observable product behavior: the earliest authoritative
   refusal occurs before later reads, repository fences, or external effects.
7. Missing complete eligibility refuses. No scenario may request, construct, or
   silently fall back to `legacy-v1` eligibility.
8. Reviewer independence is based on ActorID across the complete contributor
   set, not ProcessID, executable, session, or human-readable label.
9. A zombie may change its stale POSIX workspace but cannot heartbeat, expire,
   hand off, review, accept, integrate, or mutate current authority.
10. Exactly one current integration authority exists per repository/target ref,
    and Git observations remain evidence rather than authority.
11. Universal invariants run after every admitted or refused phase and after
    every restart/fault cut, not only at terminal success.
12. Harness evidence is deterministic, bounded, machine-readable, and names the
    source revision, test, scenario, family, repetition, and outcome.

## Required universal invariants

- One immutable meaning and one recorded result exist for each OperationID.
- Each committed semantic stage occupies one complete provider revision.
- Each affected aggregate advances exactly once; incompatible same-head writers
  yield one winner and typed stale/conflict losers.
- No refused command creates authority, projection authority, effect intent,
  external effect, repository fence, or duplicate transition.
- Current WorkRevision, eligibility, lease, candidate, policy, reviewer, and
  integration-fence coordinates agree at each guarded gate.
- Every external mutation has prior durable attributable intent; unresolved
  ambiguity permits neither blind retry nor unsafe compensation.
- Restored canonical projection bytes/digest equal uninterrupted execution at
  the same admitted journal prefix.
- Historical owners, epochs, capsules, leases, workspaces, candidates, and
  fences cannot mutate current authority.

### Subtask T070 - Replace SQLite harness setup and introspection

#### Purpose

Make scenario setup, lifecycle control, observation, and teardown use the
production host and public domain query surfaces instead of SQLite-specific
constructors, SQL statements, schema triggers, or database file knowledge.

#### Steps

1. Inventory every import of `internal/controlstore/sqlite`, `database/sql`, and
   every SQL statement, `--db` argument, SQLite schema assertion, raw database
   path, and table-shaped helper in `internal/scenario/**`.
2. Replace setup with a fixture that launches the WP13 production owner against
   an isolated supported control root and waits for typed readiness containing
   database identity, runtime epoch, and current journal revision.
3. Give test clients only typed command/query capabilities or the supported
   versioned machine protocol. Never hand them a GapDB client or storage path.
4. Replace table introspection in `inspectAuthority` and universal assertions
   with mission, work, operation, effect, review, acceptance, integration, and
   progress queries from the recovered projection.
5. Replace direct row corruption with explicit fault seams from the v2
   committer, restore path, sidecar lifecycle, or fake observer. Corruption
   fixtures must use supported test-only dependency injection, not SQL writes.
6. Replace direct process helpers that accept `--db` with production-host
   endpoint/configuration contracts and versioned request/response envelopes.
7. Keep CLI helper use only where process/zombie behavior itself is under test.
   Decode a stable machine schema; never match or scrape human-readable prose.
8. Make fixture cleanup bounded and ownership-aware. Stop only the owner started
   by that fixture and retain failure evidence without leaving live sidecars.
9. Add an architecture assertion that `internal/scenario/**` has no SQLite/SQL,
   raw GapDB, table-name, database-file, or production-internal-server shortcut.

#### Files

- `internal/scenario/**`

#### Validation

- Run all harness bootstrap, query, teardown, and restart tests with no SQLite
  module or executable available.
- Prove assertions produce the same semantic evidence before and after restart.
- Prove malformed typed envelopes fail by schema/kind rather than prose match.
- Prove fake tracker fixtures remain deterministic and contract-compatible.

### Subtask T071 - Map every frozen behavior and refusal precedence

#### Purpose

Create an executable traceability map from each frozen Exact or Strengthen
inventory entry to capability-level scenarios and observable M2 evidence.

#### Steps

1. Load the complete frozen Phase 1–3 behavior inventory; do not select only the
   cases already represented by current scenario filenames.
2. For every `Exact` entry, record capability, setup, typed command/query,
   expected result kind, authority mutation count, event/effect evidence,
   refusal position where applicable, and scenario test identifier.
3. For every `Strengthen` entry, record the original observable promise and the
   stricter M2 invariant without changing an accepted outcome into an
   undocumented behavior removal.
4. Map mission lifecycle, artifact, WorkRevision/reconciliation, execution,
   lease/workspace, handoff, review, acceptance, integration, tracker effect,
   outbox, operation progress, query, and restart families.
5. Encode refusal-precedence probes with sentinels at every later boundary:
   provider observation, external mutation, workspace write, repository fence,
   CommitPlan construction, and publication.
6. Require zero additional authority and external mutations for every refused
   case and exact replay for same-binding admitted operations.
7. Keep storage or Python/Markdown mechanics explicitly classified as non-product
   evidence rather than recreating them in the new harness.
8. Fail the census for duplicate identifiers, missing mappings, substitutions,
   uncovered refusal edges, or a test name that does not resolve.

#### Files

- `internal/scenario/**`

#### Validation

- The generated/in-memory census accounts for 100% of Exact and Strengthen
  entries and links every item to a passing test and evidence kind.
- Mutating or deleting one mapping causes a deterministic census failure.
- Moving one refusal after an external-effect or repository-fence sentinel makes
  its dedicated scenario fail.
- No mapping asserts SQLite rows, Python call structure, or CLI prose.

### Subtask T072 - Run a portable complete two-item mission

#### Purpose

Prove the full authority lifecycle reaches identical terminal facts for an
uninterrupted two-item mission and variants that restart after every major
phase.

#### Steps

1. Define one canonical fixture with two independent work items, explicit
   dependencies/scopes where relevant, complete artifact and WorkRevision
   evidence, distinct ActorIDs, and a deterministic fake tracker plan.
2. Drive mission creation, lifecycle advance, decomposition, artifact publish /
   validate / approve, WorkRevision finalization, work start, lease/workspace,
   concurrent execution, handoff, independent review, acceptance, integration,
   tracker effects, outbox resolution, and terminal query state.
3. Preserve the fake tracker request/observation contract exactly. Use stable
   EffectIDs and provider markers and compare observations by attribution.
4. Run the canonical mission uninterrupted and capture bounded canonical
   terminal authority, projection digest, operation results, external evidence,
   repository result, and event-family coverage.
5. Run a variant that fully stops and restarts the production owner after each
   major phase, discarding memory projection and reconnecting through readiness.
6. After every restart, assert a new RuntimeEpoch, refusal of predecessor
   capsules/authority, byte-identical recovered projection for the committed
   prefix, and no duplicate transition or effect.
7. Compare uninterrupted and restart variants by semantic terminal facts and
   canonical digests, excluding expected epoch/process/timing differences.
8. Keep repository and workspace filesystem effects isolated per fixture and
   inspect them only as attributed domain evidence permits.

#### Files

- `internal/scenario/**`

#### Validation

- Both items reach the qualified terminal state with exactly one current
  authority at every gate and no lost parallel progress.
- Restart after every phase produces the same terminal semantic snapshot and
  exactly one result per committed operation.
- A stale pre-restart capsule fails before external or repository mutation.
- The scenario makes no direct database call and parses no CLI prose.

### Subtask T073 - Add 100-run concurrent authority scenarios

#### Purpose

Exercise concurrent ownership and fencing laws repeatedly for independent work,
same-aggregate conflicts, zombie writers, reviewer identity, and integration
serialization.

#### Steps

1. Run at least 100 two-agent trials in which independent work items progress
   concurrently and both reach their legal next authority state.
2. Run at least 100 incompatible same-aggregate trials per mutable family in
   scope; assert exactly one winner and typed stale/conflict losers.
3. Run at least 100 clean and unclean owner replacement trials. Each ready owner
   gets a unique epoch and every predecessor capsule refuses at all gates.
4. Suspend an old worker, replace/expire its lease and workspace, resume it, and
   prove it may write only its stale POSIX directory while all current authority
   and current workspace effects are fenced.
5. Prove reviewer independence by ActorID: a contributor under a different
   ProcessID refuses, while a distinct ActorID succeeds when all other policy
   and evidence conditions are current.
6. Race integration attempts for the same repository/target ref and prove one
   current fence; race independent target refs and prove they can progress.
7. At every losing path, assert no effect intent, tracker/Git mutation,
   projection authority, or extra domain event was admitted.
8. Use deterministic barriers and bounded deadlines. Record seed, trial,
   participants, expected winner law, outcomes, and invariant evidence.

#### Files

- `internal/scenario/**`

#### Validation

- All declared 100-run trials pass under the bounded scenario race profile.
- Independent work demonstrates actual overlap rather than serialized starts.
- Same-aggregate and integration conflicts yield stable typed results.
- Zombie and same-ActorID reviewer mutants are detected on every repetition.

### Subtask T074 - Qualify per-family faults and universal invariants

#### Purpose

Exercise crash, lost/malformed acknowledgement, ambiguous and dishonest external
observations for every authority-changing family, backed by universal invariants
and mutation controls.

#### Steps

1. Enumerate every authority-changing event family registered for M2 and fail
   setup if the scenario campaign omits, renames, duplicates, or substitutes one.
2. For each applicable family, inject crash before publication, crash after
   durable commit before response/projection publication, lost response,
   malformed acknowledgement, restart before reconciliation, and restart after
   reconciliation.
3. Repeat each declared crash/ambiguity cut at least 100 times as required by
   NFR-001, NFR-002, FR-025, and the mission success criteria.
4. Script fake tracker observations for unavailable, stale, contradictory,
   unattributed, maliciously changed, and permanently ambiguous outcomes while
   preserving the existing fake tracker protocol.
5. Assert committed operations have exactly one result and complete batch
   membership; absent operations have no authority fragments; ambiguous ones
   remain explicit with no blind retry or compensation.
6. Run universal invariants after each cut and after recovery using only typed
   domain/projection queries plus attributable tracker/repository evidence.
7. Add mutants for omitted intent, partial batch, duplicate transition, changed
   OperationID retry, projection-as-authority, stale epoch acceptance, stale
   WorkRevision/lease/candidate/policy/reviewer/fence acceptance, unsafe
   compensation, and integration fence bypass.
8. Add harness-integrity mutants for SQLite/raw-key introspection, CLI prose
   parsing, missing family coverage, disabled repetition, swallowed typed
   refusal, and fake tracker contract drift.
9. Emit a bounded machine-readable campaign census keyed by source revision,
   authority family, fault cut, repetition count, test, mutants, and outcomes.

#### Files

- `internal/scenario/**`

#### Validation

Run fast focused tests during implementation. Run repetitions through explicit
bounded profiles so normal unit feedback remains usable:

```bash
go test ./internal/scenario -run 'TestHarness|TestBehaviorParity|TestTwoItemMission'
go test ./internal/scenario -run 'TestConcurrent|TestZombie|TestReviewer|TestIntegration' -count=1
go test ./internal/scenario -run 'TestCrash|TestAmbiguous|TestDishonest|TestUniversal' -count=1
go test -race ./internal/scenario -run 'TestConcurrent|TestZombie|TestReviewer|TestIntegration' -count=1
go test ./internal/architecturetests
go vet ./internal/scenario
staticcheck ./internal/scenario/...
git diff --check
```

Use the repository's explicit campaign flag or environment contract for the
full 100-run census and record that exact command in the handoff. Do not hide a
reduced repetition count behind the same test name, and do not run the known
long suite accidentally during ordinary focused iteration.

## Definition of Done

- T070–T074 are complete only within `internal/scenario/**`.
- The scenario package contains no SQLite/SQL import, query, schema/table name,
  direct GapDB inspection, raw database input, or production bypass.
- Fixtures start and observe the WP13 production owner through typed supported
  contracts and rebuild memory-primary projections through supported recovery.
- The fake tracker contract and deterministic dishonest-observer scripting are
  preserved; provider evidence never becomes Spec Kitty authority.
- No assertion parses human-facing CLI prose; process-boundary tests use stable
  versioned machine envelopes.
- Every frozen Exact and Strengthen behavior maps to a passing capability test,
  refusal/mutation expectation, and evidence reference.
- Uninterrupted and restart-after-each-phase two-item missions reach identical
  semantic terminal facts with no duplicate authority or effects.
- All required 100-run concurrency, owner replacement, crash, ambiguity, and
  lost-response campaigns pass with complete family coverage.
- Universal invariants execute after every relevant phase/cut and all declared
  mutant controls prove the assertions are capable of failing.
- Focused tests, bounded race runs, architecture checks, `go vet`,
  `staticcheck`, and `git diff --check` pass.
- The implementation handoff records commands, counts, source revision, seeds,
  durations, family census, failures, and evidence artifact digests.
- Completion is recorded through the event log, not by editing frontmatter:

```bash
spec-kitty agent tasks mark-status T070 T071 T072 T073 T074 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks and mitigations

- **Storage-coupled assertions:** SQL replacement with raw GapDB inspection is
  still coupling. Assert authority only through domain queries and evidence.
- **Harness-only success path:** Starting a test adapter can miss production
  ownership behavior. Launch the same host lifecycle and readiness contract.
- **CLI text brittleness:** Human prose can change without semantic drift. Use
  typed calls or versioned machine envelopes and test their schema.
- **Fake tracker drift:** Rewriting the fake can weaken fault evidence. Preserve
  its request, attribution, timing, and dishonest-observer contract.
- **Nominal parity:** Happy-path-only mapping misses refusal order. Require
  explicit mutation/effect sentinels for every frozen refusal.
- **Flaky concurrency:** Sleeps do not prove overlap or race law. Use barriers,
  bounded deadlines, fixed seeds, and recorded winners/outcomes.
- **Incomplete fault census:** A renamed family can silently lose coverage. Join
  the campaign to the frozen event registry and fail on any mismatch.
- **Mutants that never execute:** Require a positive control proving each named
  mutant is reached and killed by the intended invariant.

## Reviewer guidance

Review WP14 as black-box behavioral qualification of the production authority,
not as a test-file port. Require direct evidence that:

1. No SQLite/SQL or raw GapDB inspection remains anywhere in the scenario
   harness, helpers, universal assertions, subprocesses, or fixtures.
2. The production host owns the database/epoch and all assertions use typed
   domain/projection queries or attributable external evidence.
3. The fake tracker contract is preserved and dishonest observations cannot
   authorize retry, compensation, or product state.
4. No human-facing CLI prose is parsed; process tests validate a versioned
   machine schema and semantic result kinds.
5. The Exact/Strengthen census is complete and every refusal proves both its
   precedence and zero later authority/external mutations.
6. The complete two-item mission covers every major phase and restart variants
   produce identical semantic terminal state with predecessor fencing.
7. The 100-run campaigns demonstrate independent progress, same-head exclusion,
   zombie fencing, ActorID reviewer independence, and target-ref serialization.
8. Every authority family has applicable crash, ambiguity, restart, and
   dishonest-observer coverage with exactly-one-result/all-or-nothing evidence.
9. Universal invariants run throughout the campaign and every named mutant is
   killed by an assertion tied to the violated product law.
10. The diff is confined to `internal/scenario/**` and does not weaken product
    contracts, test counts, time bounds, or evidence attribution.

Reject if tests replace SQLite reads with raw GapDB reads, start a harness-only
authority owner, parse CLI prose, silently reduce repetitions, omit an event
family or frozen behavior, infer authority from tracker/Git/filesystem state, or
claim parity without executable source-bound evidence and killed controls.
