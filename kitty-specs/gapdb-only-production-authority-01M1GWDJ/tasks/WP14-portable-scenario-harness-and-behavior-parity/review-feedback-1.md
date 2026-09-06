# WP14 Independent Review Feedback — Cycle 1

**Verdict:** REQUEST CHANGES

**Reviewed candidate:** `d7ce52f5ffc8057d0fc76438654c4adeb26feb01`

**Reviewed tree:** `72a5fdb38ad34dbb131df64ab4a658340fbbb117`

## Blocker 1 — the behavior-parity census is name-only, not executable parity

`TestBehaviorParityCensus` maps all 59 Exact/Strengthen entries to one generic
test name per prefix family. Each row receives the same placeholder setup,
command, result, mutation, evidence, and refusal descriptions, and the only
connection to behavior is a source-text check that a function with the selected
name exists. The subtests do not invoke the named production test or the
behavior-specific product path. Deleting or changing the relevant production
behavior while retaining the test declaration can therefore leave the census
green.

Replace the family-prefix template with an explicit immutable row per frozen
behavior. Each row must identify and execute its exact scenario/product path,
assert its documented result and mutation/refusal evidence, and have
deletion/substitution mutants that fail because the behavior changed rather
than because metadata is missing.

## Blocker 2 — the “complete” two-item mission is incomplete and has no two-variant comparison

`TestTwoItemMissionRestartAfterEachDurableBoundary` does not exercise readiness,
decomposition, tracker effect authority, or outbox resolution. It creates a
mission already carrying the WorkRevision and proceeds directly to publication
and execution. It runs only one variant, with restarts after mission creation,
workgraph publication, and terminal completion. `completeTwoItemMission`
performs the many workspace, handoff, review, artifact, policy, acceptance,
integration, execution-completion, and mission-completion publications without
restart-after-each-boundary coverage.

Implement both the uninterrupted reference run and a restart-after-every-
durable-boundary run over the complete capability chain required by T072.
Compare bounded semantic terminal facts, operation results, external evidence,
and canonical projection at equivalent committed prefixes, while explicitly
excluding epoch/process/timing differences.

## Blocker 3 — the concurrency qualification covers only mission creation

The independent and same-aggregate campaigns exercise only mission creation.
There are no 100-run conflicts for the other mutable families, no target-ref
integration serialization/independent-target campaign, and no executable
ActorID reviewer-independence campaign. The purported reviewer and integration
evidence in `TestReviewerIndependenceAndIntegrationSerializationEvidence` is a
source grep for three test declarations. The zombie scenario creates an
ordinary temporary directory, restarts the whole runtime, and probes a stale
epoch with an empty mission command; it never allocates, expires, replaces, or
resumes a real lease-scoped workspace.

Add deterministic barrier-driven production-host scenarios for every T073
axis: independent work overlap, each in-scope mutable-family conflict, real
lease/workspace zombie replacement, same-Actor/different-Process refusal,
distinct-Actor success, same-target serialization, and independent-target
progress. Assert losing-path zero effects after every trial.

## Blocker 4 — the family/fault campaign records a Cartesian claim it never executes

`buildCampaignCensus` copies all six fault-cut names into every one of the 15
family rows. `executeQualificationProofs` then deduplicates and runs one ordinary
family test per family plus one unrelated shared publication test per cut. It
never injects or observes the declared cut through the corresponding family.
The comment that one shared committer test is equivalent contradicts T074's
requirement for family-specific crash, ambiguity, restart, reconciliation, and
semantic membership evidence.

The campaign also contains no dishonest-tracker scripting. Its 14 required
mutants are only strings in the census; `TestUniversalCampaignCensusMutants`
mutates five metadata fields and never mutates production behavior. Thus the
reported `family_proofs` and `shared_fault_proofs` counts do not prove the
claimed two-axis campaign.

Execute a source-bound family-by-applicable-fault matrix. Each cell must run the
production host, inject the cut, reconcile/restart as applicable, and assert
exactly-one result/all-or-nothing membership plus the family-specific semantic
state. Make all named product mutants executable, type-correct overlays or
equivalent controlled production substitutions with positive controls and
intended semantic failure signatures.

## Blocker 5 — “universal invariants” do not implement the governed invariant set

`assertUniversalInvariants` checks host/progress coordinate agreement, nonempty
items, and duplicate aggregate keys. It does not verify immutable OperationID
meaning/result, complete same-revision publication membership, exactly-once
aggregate advancement, refusal zero mutation/effect, WorkRevision/eligibility/
lease/candidate/policy/reviewer/fence agreement, intent-before-effect,
ambiguity retry/compensation prohibition, recovered/uninterrupted canonical
equality, or historical authority fencing. It also is not invoked inside the
child tests used as family/fault qualification.

Implement the complete invariant set through typed query/evidence surfaces and
invoke it after every admitted/refused command and every cut/recovery boundary.
Add a controlled failure for each invariant so the campaign cannot self-certify.

## Evidence and anti-pattern checklist

- Focused portable host, two-item, concurrency, zombie, parity, and census tests:
  PASS with the exact admitted assertion sidecar.
- Full ordinary `internal/scenario` package: PASS.
- Focused scenario race suite: PASS.
- `go vet ./internal/scenario`, `staticcheck ./internal/scenario/...`, and
  `git diff --check`: PASS.
- The full 100-run wrapper was not rerun because the structural inspection above
  proves it executes a different, weaker experiment than its census claims.
- Storage blindness: PASS; no SQLite/SQL or raw GapDB adapter import was found in
  the surviving scenario package.

Anti-patterns:

1. Dead code: PASS.
2. Synthetic-fixture test: **FAIL** — parity, reviewer/integration, fault, and
   mutant claims are metadata/source-existence checks rather than the asserted
   production scenarios.
3. Silent empty return: PASS.
4. FR coverage: **FAIL** — multiple required capability, restart, concurrency,
   fault, and dishonest-observer behaviors have no executable scenario.
5. Frozen surface: PASS for WP14-owned product changes.
6. Locked decision: **FAIL** — claiming shared publication faults in place of
   the required per-family applicable fault evidence weakens an explicit T074
   requirement.
7. Shared-file ownership: PASS; WP14-authored changes are confined to
   `internal/scenario/**`, with dependency repairs merged from owning lanes.
8. Production fragility: N/A; this WP adds no production exception path.
