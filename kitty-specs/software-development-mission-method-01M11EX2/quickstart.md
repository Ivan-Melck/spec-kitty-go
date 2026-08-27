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

Expected: typed success with `state=specifying`; missing or unknown definition
fails before mutation.

### 2. Publish and approve exact specification and plan revisions

```bash
go run ./cmd/spk-phase1-helper artifact publish --kind specification --source spec.md --source-revision "$SOURCE_COMMIT" ...
go run ./cmd/spk-phase1-helper artifact approve --revision artifact-spec-1 --verdict approved ...
go run ./cmd/spk-phase1-helper mission transition --to planning --expected-state-version 1 ...

go run ./cmd/spk-phase1-helper artifact publish --kind plan --source plan.md --source-revision "$SOURCE_COMMIT" ...
go run ./cmd/spk-phase1-helper artifact approve --revision artifact-plan-1 --verdict approved ...
go run ./cmd/spk-phase1-helper mission transition --to decomposing --expected-state-version 2 ...
```

Expected: each approval names the exact artifact revision. Publishing a changed
plan makes the earlier approval stale.

### 3. Decompose two items

Create:

```text
work-a: define the contract
work-b: exercise the contract
work-b depends on work-a
```

Each item has standalone context, one or more structured criteria, a bounded
write scope, stable WorkID, and traceability to the approved specification and
plan. Run the same application contract against the fake and Beads providers.

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
provider observations, and an acyclic lane graph. Repeating the same operation
replays; a competing different finalization conflicts. One hundred two-process
races publish exactly one current generation.

### 5. Query next and render compatibility output

```bash
go run ./cmd/spk-phase1-helper mission next --database "$CONTROL_DB" --mission mission-demo
go run ./cmd/spk-phase1-helper projection tasks --database "$CONTROL_DB" --mission mission-demo --output "$TASKS_MD"
```

Expected: `next` returns `work_ready` with `revision-demo-1`. The Markdown header
states that it is derived and identifies the revision/digest. Editing or deleting
the file has no effect on a later `next` result.

### 6. Introduce tracker drift

Change work-a's acceptance criterion and make work-b unnecessary. Do not change
mission state directly.

```bash
go run ./cmd/spk-phase1-helper work drift --database "$CONTROL_DB" --mission mission-demo --provider fake
```

Expected: generation 1 remains current and immutable. The drift report names the
criterion change and removed/affected work. Provider-only revision noise is
reported separately from semantic change.

### 7. Reconcile generation 2

```bash
go run ./cmd/spk-phase1-helper work reconcile \
  --database "$CONTROL_DB" \
  --mission mission-demo \
  --old-revision revision-demo-1 \
  --new-revision revision-demo-2 \
  --drift-report drift-demo-1 \
  --decision work-a:continue \
  --decision work-b:invalidate \
  --operation operation-reconcile-1 \
  --governance-context "$GOVERNANCE_CONTEXT"
```

Expected: one atomic successor generation, complete per-work decisions, old/new
digests, and an invalidation consequence that later Phase 4 consumers must use
to expire authority and quarantine a workspace. An omitted or duplicate decision
leaves generation 1 current.

### 8. Re-render and verify

```bash
go run ./cmd/spk-phase1-helper mission next --database "$CONTROL_DB" --mission mission-demo
go run ./cmd/spk-phase1-helper projection tasks --database "$CONTROL_DB" --mission mission-demo --output "$TASKS_MD"
```

Expected: both reference generation 2. The old revision, reconciliation evidence,
and any historical approval remain inspectable.

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
