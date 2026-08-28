# Quickstart: Phase 3 Two-Item Proving Mission

This is the acceptance walkthrough the implementation must make executable. The
command spelling is the helper composition contract for Phase 3 planning; it is
not yet a claim that the current binary implements these commands.

## Prerequisites

- Go 1.26.4 or the repository-pinned supported toolchain;
- a clean checkout on the mission candidate;
- the durable fake tracker for the portable run;
- the Phase 2-qualified Beads executable and isolated provider root only for the
  real-provider variant;
- a temporary control database owned by the local machine/filesystem authority.

No network, Team Kitty, MCP, orchestrator server, Gapdb, or production agent
worktree is required.

## Scenario

### 1. Initialize an explicit software-development mission

```bash
go run ./cmd/spk-phase1-helper mission initialize \
  --database "$CONTROL_DB" \
  --project project-demo \
  --mission mission-demo \
  --definition software-development@1 \
  --actor actor-author \
  --process process-author \
  --operation operation-initialize \
  --governance-context "$GOVERNANCE_CONTEXT"
```

Expected: typed success with `state=initialized`, `state_version=1`, and the
exact actor, process, and sealed-governance commitment. Missing provenance or an
unknown definition fails before mutation; replay returns the original creation
authority rather than authority reconstructed from current inputs.

### 2. Publish intent, then approve exact specification and plan revisions

```bash
go run ./cmd/spk-phase1-helper artifact publish \
  --kind intent --source intent.md --source-revision "$SOURCE_COMMIT" \
  --expected-latest "" ...
go run ./cmd/spk-phase1-helper mission transition \
  --to specifying --expected-snapshot "$MISSION_V1_JSON" ...

go run ./cmd/spk-phase1-helper artifact publish \
  --kind specification --source spec.md --source-revision "$SOURCE_COMMIT" \
  --expected-latest "" ...
go run ./cmd/spk-phase1-helper artifact approve --revision artifact-spec-1 --verdict approved ...
go run ./cmd/spk-phase1-helper mission transition \
  --to specification_approved --expected-snapshot "$MISSION_V2_JSON" ...
go run ./cmd/spk-phase1-helper mission transition \
  --to planning --expected-snapshot "$MISSION_V3_JSON" ...

go run ./cmd/spk-phase1-helper artifact publish \
  --kind plan --source plan.md --source-revision "$SOURCE_COMMIT" \
  --expected-latest "" ...
go run ./cmd/spk-phase1-helper artifact approve --revision artifact-plan-1 --verdict approved ...
go run ./cmd/spk-phase1-helper mission transition \
  --to plan_approved --expected-snapshot "$MISSION_V4_JSON" ...
```

Expected: the mission reaches `plan_approved` at state version 5. Each mutation
carries the exact prior `MissionSnapshot`; a stale snapshot is refused even if
its numeric version was guessed correctly. Each approval names the exact
artifact revision. Publishing changed bytes with
`--expected-latest artifact-plan-1` creates a new revision and makes the earlier
approval inapplicable.

### 3. Decompose two items

Create:

```text
work-a: define the contract
work-b: exercise the contract
work-b depends on work-a
```

Each item has standalone context, one or more structured criteria, a bounded
write scope, stable WorkID, and traceability to the approved specification and
plan. Invoke `mission decompose` against the fake provider while the exact
expected snapshot is version 5. The command records the decomposition start as
version 6; validation of the returned provider-neutral draft advances to
`work_draft_ready` at version 7. WP10 proves the contract with the durable fake;
WP11 applies the same provider-neutral contract to Beads.

Expected: both yield equivalent provider-neutral observations. Unknown
dependencies, missing coverage, overreach, or a post-collapse lane cycle fails
before finalization.

### 4. Finalize generation 1

```bash
go run ./cmd/spk-phase1-helper work finalize \
  --database "$CONTROL_DB" \
  --mission mission-demo \
  --expected-generation 0 \
  --revision revision-demo-1 \
  --planning-source "$SOURCE_COMMIT" \
  --operation operation-finalize-1 \
  --governance-context "$GOVERNANCE_CONTEXT"
```

Expected: generation 1 with no predecessor, a deterministic graph digest,
provider observations, and an acyclic lane graph. Finalization advances the
mission from `work_draft_ready` version 7 to the definition's terminal
work-ready step at version 8 with generation 1 current. Repeating the same
operation replays; a competing different finalization conflicts. One hundred
two-process races publish exactly one current generation.

### 5. Query next and render compatibility output

```bash
go run ./cmd/spk-phase1-helper mission next --database "$CONTROL_DB" --mission mission-demo
go run ./cmd/spk-phase1-helper projection tasks \
  --database "$CONTROL_DB" --mission mission-demo \
  --root "$RUN_ROOT" --output tasks.md
```

Expected: `next` returns `work_ready` with `revision-demo-1`. The Markdown header
states that it is derived and identifies the revision/digest. Editing or deleting
the file has no effect on a later `next` result.

### 6. Introduce tracker drift

Create a distinct fake-provider item `work-a-v2` carrying the changed acceptance
criterion. Make `work-b` unnecessary in the explicitly proposed successor
scope, but do not delete or rewrite provider history for old `work-a` or
`work-b`, and do not change mission state directly.

```bash
go run ./cmd/spk-phase1-helper work drift \
  --database "$CONTROL_DB" --mission mission-demo --provider fake \
  >"$DRIFT_JSON"
```

Expected: generation 1 remains current and immutable. The drift report names the
old and replacement WorkIDs, the criterion change, and the old work omitted from
the proposed successor. Provider-only revision noise is reported separately
from semantic change. The report has no local authority identity and the query
writes nothing.

### 7. Reconcile generation 2

```bash
go run ./cmd/spk-phase1-helper work reconcile \
  --database "$CONTROL_DB" \
  --mission mission-demo \
  --old-revision revision-demo-1 \
  --new-revision revision-demo-2 \
  --drift-report "$DRIFT_JSON" \
  --decision work-a:supersede:work-a-v2 \
  --decision work-b:invalidate \
  --operation operation-reconcile-1 \
  --governance-context "$GOVERNANCE_CONTEXT"
```

Expected: one atomic successor generation containing `work-a-v2`, complete
per-work decisions, old/new digests, and an invalidation consequence that later
Phase 4 consumers must use to expire authority and quarantine a workspace. The
exact supplied drift report, decisions, reconciliation, successor, current
pointer, event, and outbox commit together. An omitted or duplicate decision
leaves generation 1 current and persists none of them.

### 8. Re-render and verify

```bash
go run ./cmd/spk-phase1-helper mission next --database "$CONTROL_DB" --mission mission-demo
go run ./cmd/spk-phase1-helper projection tasks \
  --database "$CONTROL_DB" --mission mission-demo \
  --root "$RUN_ROOT" --output tasks.md
```

Expected: both reference generation 2 and `work-a-v2`. The old revision,
superseded and invalidated WorkIDs, reconciliation evidence, provider history,
and every historical approval remain inspectable.

## Qualification commands

The delivered mission must make these repository gates real:

```bash
make mission-contract
make mission-acceptance
make mission-adversarial
make quality
```

Evidence must record the tested source commit/tree, clean-source state, exact
profile census, durations, and verdict. A failed or dirty attempt remains
non-admissible and is never relabeled.
