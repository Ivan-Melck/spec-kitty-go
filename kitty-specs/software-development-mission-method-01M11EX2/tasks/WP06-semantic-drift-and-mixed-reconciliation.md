---
work_package_id: WP06
title: Semantic Drift and Mixed Reconciliation
dependencies:
- WP05
requirement_refs:
- FR-016
- FR-017
- FR-018
- FR-019
- FR-020
- FR-028
- NFR-001
- NFR-003
- NFR-004
- NFR-014
- C-001
- C-008
- C-011
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T029
- T030
- T031
- T032
- T033
- T034
history: []
agent_profile: debugger-debbie
authoritative_surface: internal/workgraph/
create_intent:
- internal/workgraph/drift.go
- internal/workgraph/drift_test.go
- internal/mission/reconciliation.go
- internal/mission/reconciliation_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/workgraph/drift.go
- internal/workgraph/drift_test.go
- internal/workgraph/reconcile.go
- internal/workgraph/reconcile_test.go
- internal/mission/reconciliation.go
- internal/mission/reconciliation_test.go
- internal/handoff/model.go
- internal/review/model.go
- internal/integration/model.go
role: investigator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 - Semantic Drift and Mixed Reconciliation

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `debugger-debbie`
- **Role**: `investigator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Turn post-finalization change into explicit, auditable mission information.
Compare a current immutable WorkRevision with a normalized provider observation,
distinguish executable semantic drift from provider revision noise, derive the
complete affected work set, and validate one mixed per-work reconciliation plan
that can atomically publish a successor generation.

Extend—not replace—the Phase 1 reconciliation laws. The existing four
dispositions and status-sensitive consequences remain the policy base, but the
current one-disposition-per-graph request must evolve into exactly one decision
for every affected active or accepted old work item. Integrated history remains
immutable. Consumers must reject ineligible old-generation handoff, review, and
integration attempts through typed model contracts, without implementing the
Phase 4/5 execution machinery those consumers will eventually drive.

## Context

WP05 supplies WorkRevision v2: immutable generation, predecessor, planning
source, mission-definition commitment, normalized work content, structured
criteria, dependencies, write scopes, lane graph, provider observations, and
digests. WP06 compares that contract with a candidate observation and creates
the governed bridge to a successor generation. A provider timestamp, opaque
revision, or item ordering change is evidence, not by itself executable drift.

The authority split remains:

```text
Tracker observation: reports mutable current planning facts
Drift report:        compares those facts to immutable generation N
Reconciliation:      records human-authorized per-work consequences
Control store:       later atomically publishes generation N+1 and the plan
Git history:         preserves already integrated candidates unchanged
```

### Required boundaries

1. Drift compares canonical semantic fields, never rendered Markdown or raw
   provider ordering, timestamps, status labels, or revision arithmetic.
2. Provider observations remain audit evidence. Revision-only change yields
   `observation_only`, not a required disposition or automatic generation.
3. The current WorkRevision is never edited, revoked, or silently advanced by a
   drift check. Reconciliation creates one immutable successor.
4. Every affected active, handed-off, or approved old item has exactly one
   legal `continue`, `supersede`, `invalidate`, or `fork` decision.
5. Consequences are derived from disposition and known work status; callers may
   not author permissive consequence booleans.
6. Integrated work always preserves history. New requirements become new
   repair, replacement, or fork work rather than retroactive mutation.
7. Publication semantics are compare-and-set against the expected current
   revision/generation. WP08 implements the durable transaction.
8. No tracker mutation, execution lease, workspace quarantine operation,
   review dispatch, Git integration, transport, or Gapdb migration is added.

## Branch Strategy

Work only in the Spec Kitty lane worktree assigned for WP06. The planning base
and merge target are `feat/software-development-mission-method`. Begin from the
accepted WP05 lane because WorkRevision v2 is the required comparison contract.

Do not edit the primary checkout, create another worktree, merge to `main`, or
push to a protected branch. Modify only the nine owned files. If implementation
requires a store adapter, service file, or schema outside this list, report the
ownership boundary for WP08 instead of expanding this WP.

## Implementation Command

```bash
spec-kitty agent action implement WP06 --agent codex
```

### Subtask T029: Compute canonical semantic drift separately from provider noise

**Purpose**: Produce a deterministic field-level comparison that identifies
real executable contract change without turning opaque provider churn into
workflow authority.

**Steps**:

1. Add closed `SemanticStatus`, `ChangeKind`, and semantic field-path values in
   `internal/workgraph/drift.go`. Support `clean`, `observation_only`, and
   `changed`, plus added, removed, and modified changes.
2. Model `DriftReport` with stable report identity, mission and base revision,
   candidate graph digest, observation time, sorted changes, affected old/new
   WorkIDs, semantic status, and complete provider-revision evidence.
3. Compare every executable WorkRevision v2 field: identity/mapping, title,
   description, design context, structured criteria and metadata, dependencies,
   write scopes, dependency policy, and derived lane membership/edges where
   their semantics are not already implied by source fields.
4. Represent old/new values with canonical commitments or bounded summaries,
   not unrestricted descriptions, criterion bodies, provider stderr, or private
   metadata. Diagnostics must identify the field and WorkID safely.
5. Canonicalize before comparison. Equivalent ordering of work, criteria,
   dependencies, scopes, lanes, metadata keys, and provider observations must
   not produce semantic change or a different report commitment.
6. Classify changed opaque provider revisions with equal semantic content as
   `observation_only`; identical semantics and observations are `clean`.
   Neither creates a successor or changes current authority.
7. Validate mission/revision scope and candidate normalization before diffing.
   Malformed, incomplete, or wrong-mission observations return stable typed
   errors rather than a misleading drift report.

**Files**:

- `internal/workgraph/drift.go`
- `internal/workgraph/drift_test.go`

**Validation**:

- One table case exists for every semantic field and add/remove behavior.
- Provider order/revision-only mutations never report semantic change.
- Equal-timestamp content changes are detected.
- Reports and change lists are byte-stable under randomized input ordering.
- Bounded findings do not disclose seeded private field content.

### Subtask T030: Derive the complete affected old and new work sets

**Purpose**: Prevent reconciliation from overlooking work whose executable
contract or dependency meaning changed indirectly.

**Steps**:

1. Derive affected sets from the canonical diff, not caller-supplied IDs.
   Include directly added, removed, remapped, or modified work on its applicable
   old/new side.
2. For dependency changes, include the changed endpoint and every old/new work
   item whose prerequisite ancestry, readiness meaning, or lane ordering changes.
   Use graph traversal over normalized IDs; never provider status or issue order.
3. Treat criterion, description, design-context, mapping, metadata-schema, and
   write-scope/lane changes as contract changes for the owning item. Propagate
   only where dependency or collapsed-lane semantics actually change.
4. Handle one-to-one replacement, deletion, addition before existing work,
   split/fork candidates, and merged scopes. Preserve separate old and new sets
   so identity disappearance cannot be hidden by a title match.
5. Sort/deduplicate all affected identities and reject unknown, duplicate, or
   cross-mission IDs. The same semantic comparison must always return the same
   set regardless of provider ordering.
6. Expose enough reason linkage for each affected ID to be traced to one or more
   `DriftChange` records without duplicating unbounded work content.

**Files**:

- `internal/workgraph/drift.go`
- `internal/workgraph/drift_test.go`

**Validation**:

- Direct field changes affect the correct old/new identities.
- Dependency insertion/removal and lane-collapse changes cover all impacted work.
- Add/delete/rebind/split fixtures cannot lose an old or new identity.
- Unrelated graph components remain unaffected.
- Random ordering yields identical affected sets and reason links.

### Subtask T031: Replace global reconciliation with mixed per-work decisions

**Purpose**: Express real graph evolution by assigning a legal disposition to
each affected prior item while preserving the proven consequence matrix.

**Steps**:

1. Evolve `internal/workgraph/reconcile.go` from one global disposition to a
   canonical `WorkDispositionDecision` collection. Each decision names one old
   WorkID, one disposition, related successor IDs, optional fork edges, bounded
   rationale, and derived consequences.
2. Preserve the closed dispositions `continue`, `supersede`, `invalidate`, and
   `fork` and the existing status-specific consequence semantics. Do not let a
   request supply `RunMayContinue`, eligibility, expiry, quarantine, or history
   preservation booleans.
3. Require exactly one decision for every affected old work item whose status is
   active, handed off, or approved. Reject omissions, duplicates, unaffected
   IDs, unknown IDs, and decisions inconsistent with the drift report.
4. Validate disposition shape: `continue` needs semantic compatibility;
   `supersede`/`invalidate` identify valid successor relationships as applicable;
   `fork` uses distinct new WorkIDs and complete explicit old-to-new edges.
5. Integrated items retain `preserves_integrated_history=true` for all
   dispositions. Any changed need must point to explicit new repair/replacement
   work; no decision may rewrite old candidate, review, or integration evidence.
6. Canonicalize decisions, successor IDs, and edges before computing the request
   digest. Equal semantics replay; changed rationale, disposition, mapping, or
   evidence under the same ID conflicts.
7. Preserve compatible Phase 1 decoding/query behavior where historical single-
   disposition records exist; never rewrite prior reconciliation evidence.

**Files**:

- `internal/workgraph/reconcile.go`
- `internal/workgraph/reconcile_test.go`

**Validation**:

- One plan mixes all four dispositions across different affected work items.
- Every missing, duplicate, extra, malformed, or incompatible decision fails.
- Consequence matrices remain exhaustive for every known work status.
- Canonical permutations produce one digest; semantic changes produce conflicts.
- Integrated-history tests prove old candidate/evidence identities stay fixed.

### Subtask T032: Define atomic successor-publication request semantics

**Purpose**: Give WP08 one honest aggregate transaction contract: either the
complete successor and reconciliation become current together, or nothing does.

**Steps**:

1. In `internal/mission/reconciliation.go`, define the mission-level command and
   store port for publishing a successor. Bind reconciliation ID, mission,
   expected old revision/generation, candidate successor, drift report, complete
   decision set, actor, rationale, sealed governance context, UTC time, and
   canonical request digest.
2. Validate the old revision is current, the successor names it as predecessor,
   generation increments exactly once, mission/project/definition scope matches,
   old/new graph digests match the drift evidence, and every required decision
   is complete before invoking persistence.
3. Express one atomic store operation that publishes successor WorkRevision,
   reconciliation plan, derived consequences/authority actions, audit event,
   outbox entry, and current-generation pointer. WP08 owns its SQLite details.
4. Require expected-current compare-and-set. Concurrent attempts from one base
   yield one winner; losers return exact replay or stable reconciliation conflict
   with no partially visible successor.
5. Exact reconciliation ID plus equal request digest replays the stored result.
   Reuse with changed semantics returns idempotency conflict even if another
   generation is now current.
6. A clean or observation-only report cannot publish a successor. A changed
   report with incomplete dispositions must fail before the store is called.
7. Model required downstream authority actions as durable typed facts. Do not
   directly expire a lease or quarantine a workspace in Phase 3.

**Files**:

- `internal/mission/reconciliation.go`
- `internal/mission/reconciliation_test.go`

**Validation**:

- In-memory port tests prove one complete winner for a two-caller CAS race.
- Replay and altered replay are distinct and deterministic.
- Faults before commit retain the old state; simulated after-commit faults replay
  the complete successor rather than duplicating it.
- Clean/noise-only/incomplete plans never call the store.
- Publication evidence binds both digests, both revision IDs, actor, governance,
  operation, and time.

### Subtask T033: Carry reconciliation consequences into existing consumers

**Purpose**: Make old-generation eligibility an explicit consumer contract so
handoff, review, and integration cannot accept work forbidden by reconciliation.

**Steps**:

1. Extend the existing model seams in handoff, review, and integration with a
   common-shaped immutable eligibility fact: reconciliation ID, old/new revision,
   WorkID, status, disposition, permitted operation, and bounded reason.
2. Reuse each package's existing `KindRevisionIneligible` refusal. Validate that
   eligibility matches the request's exact WorkRevision and WorkID before any
   consumer authority is accepted.
3. Map derived consequences precisely: superseded/invalidated work cannot create
   an acceptable new handoff or integration; review cannot approve ineligible
   old-contract evidence; invalidated active work emits later expiry/quarantine
   action requirements; continue/fork retain only their declared permissions.
4. Preserve durable replay: an operation already committed under previously
   valid authority may return its immutable result, but a new or changed attempt
   must re-evaluate current eligibility and fail closed.
5. Preserve integrated records for every disposition. A successor generation
   may create follow-up work, but these model changes never amend a Git candidate,
   approval verdict, result commit, or historical evidence digest.
6. Keep this change at the typed model/validation seam and prove it through
   mission reconciliation tests. Do not add lease expiry, workspace mutation,
   review dispatch, Git commands, or Phase 4/5 orchestration.

**Files**:

- `internal/handoff/model.go`
- `internal/review/model.go`
- `internal/integration/model.go`
- `internal/mission/reconciliation_test.go`

**Validation**:

- Each consumer accepts eligible matching facts and rejects mismatch/ineligibility
  with `revision_ineligible` before downstream mutation.
- Supersede and invalidate block new acceptance/integration as defined.
- Invalidate produces typed future authority actions but no direct side effect.
- Integrated evidence remains byte-identical after every disposition.
- Existing Phase 1 consumer model tests continue to pass unchanged.

### Subtask T034: Attack drift, reconciliation, crash, and replay semantics

**Purpose**: Prove zero silent drift and expose incomplete-set, stale-CAS,
integrated-history, and canonicalization bugs before adapter qualification.

**Steps**:

1. Add table, property-style, and deterministic generated tests covering every
   WorkRevision v2 field, add/remove/rebind, dependency/lane propagation,
   provider revision noise, equal timestamps, and randomized ordering.
2. Generate at least 1,000 deterministic semantic mutations across all fields;
   require 1,000 `changed` reports and zero mutation of the base revision.
   Separately generate provider-noise cases that remain `observation_only`.
3. Exercise mixed decision completeness across active, handed-off, approved, and
   integrated work, including incompatible `continue`, identity reuse in forks,
   missing ancestry, duplicate decisions, and repair work for integrated history.
4. Re-run every Phase 1 consequence case against the mixed model. Add a mutant
   that removes one affected-work decision check and prove the test fails for
   that exact omission.
5. Exercise exact replay, altered replay, stale expected generation, two-way
   publication race, and injected failures on both sides of the atomic port.
   Assert either one complete prior state or one complete successor state.
6. Assert deterministic report/request encodings and defensive copying. Mutating
   caller slices, maps, revisions, decisions, or edges after construction cannot
   alter stored commitments.
7. Keep the suite hermetic and fast: no Beads process, SQLite file, Git mutation,
   network, sleeps, wall-clock ordering, or external transport.

**Files**:

- `internal/workgraph/drift_test.go`
- `internal/workgraph/reconcile_test.go`
- `internal/mission/reconciliation_test.go`

**Validation**:

```bash
go test ./internal/workgraph ./internal/mission -run 'Drift|Reconciliation' -count=10
go test -race ./internal/workgraph ./internal/mission ./internal/handoff ./internal/review ./internal/integration
go test ./internal/workgraph -run 'Mutation|ProviderNoise' -count=1
go vet ./internal/workgraph ./internal/mission ./internal/handoff ./internal/review ./internal/integration
```

Also run the accepted WP01 architecture gate and the repository's bounded
mission-contract profile. Do not weaken a campaign census or architecture rule
to accommodate these packages.

## Definition of Done

- Drift reports distinguish `clean`, `observation_only`, and `changed` using
  canonical executable fields rather than provider revision inequality.
- Every WorkRevision v2 semantic field has positive and noise-control coverage.
- Affected old/new sets are derived, complete, deterministic, reason-linked,
  and include dependency/lane impacts without unrelated overreach.
- Reconciliation supports mixed per-work dispositions and requires exactly one
  legal decision for every affected active, handed-off, or approved old item.
- Consequences remain derived and exhaustive; callers cannot author permission
  booleans or rewrite integrated history.
- One atomic successor request binds predecessor, generation, drift evidence,
  decisions, actor, governance, and digest under current-generation CAS.
- Exact replay, altered replay, races, and before/after-commit faults have one
  deterministic complete result and no partial successor.
- Handoff, review, and integration model seams refuse ineligible old-generation
  attempts while preserving immutable completed-operation replay.
- Invalidated active work emits typed future expiry/quarantine action facts but
  performs no Phase 4 workspace or lease mutation.
- The 1,000-mutation campaign reports zero silent semantic drift; provider-only
  noise never forces reconciliation.
- Integrated candidates, reviews, result commits, and evidence remain unchanged;
  changed needs are represented by explicit new work.
- Focused, repeated, race, vet, architecture, and bounded contract gates pass,
  with all edits confined to the nine owned files.
- Mark all six subtasks complete only after evidence is green:

```bash
spec-kitty agent tasks mark-status T029 T030 T031 T032 T033 T034 --status done --mission software-development-mission-method-01M11EX2 --json
```

## Risks

- **Noise mistaken for law**: Opaque provider revision churn must remain audit
  evidence when canonical executable content is unchanged.
- **Incomplete affected set**: Direct-only comparison can miss dependents and
  lane-order changes; derive impact from both normalized graphs.
- **Global-disposition regression**: Preserve Phase 1 consequences while moving
  authority to a complete per-work decision collection.
- **Stale publication**: Validate fully, then CAS the exact current generation;
  never publish decisions and successor in separate transactions.
- **Consumer overreach**: Model future expiry/quarantine requirements without
  performing Phase 4 side effects in this WP.
- **Historical mutation**: Integrated facts are immutable; use new repair or
  replacement work for changed requirements.
- **Evidence disclosure**: Diff commitments and bounded paths, not raw private
  work content, transcripts, or provider output.

## Reviewer Guidance

Begin with adversarial pairs: same semantics/different provider revision must be
`observation_only`; same timestamp/different content must be `changed`. Verify
every v2 field participates in canonical comparison and every affected identity
is derived rather than accepted from the caller.

Inspect the mixed decision validator for omissions, duplicates, unaffected IDs,
incompatible `continue`, fork identity reuse, and integrated-history rewrites.
The existing consequence matrix must remain load-bearing, but no graph-wide
disposition may survive as the new authority model.

Finally, verify atomicity is expressed as one store-port operation under exact
predecessor/generation CAS, with replay decided by request digest. Consumer
changes must use typed `revision_ineligible` failures and emit only future action
facts—no leases, workspaces, Git history, or dispatch behavior. Reject raw
provider comparison, permissive missing decisions, caller-authored consequences,
partial publication, or changes beyond the nine owned files.
