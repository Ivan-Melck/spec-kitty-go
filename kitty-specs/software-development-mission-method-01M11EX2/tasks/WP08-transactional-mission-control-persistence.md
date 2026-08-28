---
work_package_id: WP08
title: Transactional Mission Control Persistence
dependencies:
- WP02
- WP03
- WP05
- WP06
- WP07
requirement_refs:
- FR-003
- FR-004
- FR-005
- FR-013
- FR-017
- FR-018
- FR-021
- FR-023
- FR-024
- FR-028
- NFR-003
- NFR-004
- NFR-007
- NFR-009
- NFR-012
- C-009
- C-010
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T041
- T042
- T043
- T044
- T045
- T046
- T047
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/controlstore/
create_intent:
- internal/controlstore/sqlite/mission.go
- internal/controlstore/sqlite/mission_test.go
- internal/controlstore/sqlite/artifact.go
- internal/controlstore/sqlite/artifact_test.go
- internal/controlstore/sqlite/workrevision_v2.go
- internal/controlstore/sqlite/workrevision_v2_test.go
- internal/controlstore/sqlite/reconciliation_v2.go
- internal/controlstore/sqlite/reconciliation_v2_test.go
- internal/controlstore/sqlite/mission_action.go
- internal/controlstore/sqlite/mission_action_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/controlstore/store.go
- internal/controlstore/sqlite/migrations.go
- internal/controlstore/sqlite/governance_context_test.go
- internal/controlstore/sqlite/mission.go
- internal/controlstore/sqlite/mission_test.go
- internal/controlstore/sqlite/artifact.go
- internal/controlstore/sqlite/artifact_test.go
- internal/controlstore/sqlite/workrevision_v2.go
- internal/controlstore/sqlite/workrevision_v2_test.go
- internal/controlstore/sqlite/reconciliation_v2.go
- internal/controlstore/sqlite/reconciliation_v2_test.go
- internal/controlstore/sqlite/mission_action.go
- internal/controlstore/sqlite/mission_action_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP08 - Transactional Mission Control Persistence

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Persist the complete Phase 3 mission-method authority in the qualified local
control store. Extend the control-store surface and SQLite adapter so mission
transitions, exact artifact revisions and approvals, WorkRevision v2
finalization, mixed reconciliation, governance-bound actions, decisions, and
blockers survive concurrency, crashes, and restart without partial authority.

The implementation begins from schema v12 and uses append-only migrations.
Domain packages continue to own validation and aggregate semantics; SQLite owns
atomic publication, foreign-key integrity, optimistic compare-and-set,
idempotent replay, immutable historical records, events, and outbox effects.
Gapdb is not adopted or evaluated in this WP: SQLite remains the approved
single-machine control authority until a separate signed migration decision.

## Context

WP02, WP03, WP05, WP06, and WP07 define the ports and closed domain records this
adapter implements. The existing store already proves WAL concurrency,
transactional run/lease/workspace/review/integration behavior, WorkRevision v1,
immutable reconciliation evidence, operation replay, event recording, and
schema-v12 sealed governance contexts. WP08 extends those mechanisms rather than
creating parallel control authority or storage-shaped domain types.

Aggregate transactions are:

```text
mission transition = state CAS + operation result + event + outbox
artifact publish   = immutable revision + validation + operation result + event
artifact approval  = exact revision verdict + governance + event
finalization       = WorkRevision v2 + graph children + current pointer + event
reconciliation     = drift + successor + decisions + consequences + actions
mission action     = operation + sealed governance + result/decision/blocker
```

### Required boundaries

1. One control store is owned by one machine/filesystem authority. Remote
   callers later use an orchestrator surface; they never open this database.
2. SQLite rows persist domain facts but do not decide mission transitions,
   approval freshness, graph validity, drift, dispositions, or next outcomes.
3. Every aggregate write validates domain values before opening or mutating a
   transaction, then rechecks storage CAS/uniqueness inside the transaction.
4. Exact operation ID plus equal request digest replays. Reusing an operation ID
   with changed semantics returns a stable idempotency conflict.
5. Immutable revisions, approvals, governance contexts, reconciliations, and
   completed action evidence are never updated in place or deleted.
6. Events/outbox records commit with the authority fact they describe. No
   success may be returned for an uncommitted event or missing aggregate record.
7. Artifact content, private transcripts, provider stderr, credentials, raw
   tool output, and mutable doctrine paths do not enter the control database.
8. Keep compatibility with every supported historical schema and WorkRevision
   v1 record. Never rewrite historical authority to resemble v2.
9. Do not add tracker effects, production worktrees, external APIs, migration to
   Gapdb, or distributed database coordination.

## Branch Strategy

Work only in the Spec Kitty lane worktree assigned for WP08. Planning and merge
target are `feat/software-development-mission-method`. Begin from accepted WP02,
WP03, WP05, WP06, and WP07 so the adapter implements their final ports rather
than guessing ahead of them.

Do not edit the primary checkout, create another worktree, merge to `main`, or
push to a protected branch. Restrict changes to the twelve owned files. Report
an ownership conflict instead of editing a domain contract, helper command,
scenario, or unrelated adapter outside this package.

## Implementation Command

```bash
spec-kitty agent action implement WP08 --agent codex
```

### Subtask T041: Extend the control-store composition contract

**Purpose**: Expose the mission-method persistence capabilities through one
explicit control-store boundary without coupling domain packages to SQLite.

**Steps**:

1. Extend `internal/controlstore/store.go` with the smallest composition-facing
   contracts needed to assemble the mission, artifact, finalization,
   reconciliation, and action stores defined by their owning domain packages.
2. Prefer interface satisfaction and narrow capability accessors over a single
   giant CRUD interface. Preserve current `Settings` and existing callers.
3. Keep SQL types, row IDs, migration versions, JSON blobs, `*sql.Tx`, and
   SQLite error codes private to the adapter.
4. Make transaction ownership unambiguous: a domain service invokes one
   aggregate operation; the adapter owns begin/commit/rollback and never calls
   back into a provider or Git while holding the transaction.
5. Document the single-machine boundary and WAL/busy-timeout expectations
   without implying cross-host safety.
6. Add compile-time interface assertions in the relevant new SQLite files so a
   later port change fails at build time.

**Files**:

- `internal/controlstore/store.go`
- New SQLite implementation files as compile-time contract consumers

**Validation**:

- Existing control-store construction and packages compile unchanged.
- Every Phase 3 domain port has exactly one SQLite implementation assertion.
- No domain package imports `database/sql` or the SQLite adapter.
- No adapter interface exposes provider, Git, transport, or Markdown behavior.

### Subtask T042: Add append-only schema v13+ migrations

**Purpose**: Introduce durable mission-method tables and invariants while
preserving all schema-v1-through-v12 data and qualification evidence.

**Steps**:

1. Increment `currentSchemaVersion` and append one or more migrations after v12.
   Never reorder, edit, or renumber historical migration entries.
2. Add normalized tables for missions and state transitions; artifact revisions,
   validations, findings, and exact-revision approvals; WorkRevision v2 detail;
   drift reports; mixed reconciliation decisions/consequences; mission actions;
   durable decisions/blockers; events; and outbox delivery state.
3. Use foreign keys, unique constraints, checks, partial indexes, and immutable
   triggers to enforce identity, scope, one current generation, monotonic
   revisions, one exact operation result, and append-only evidence where SQLite
   can enforce facts without duplicating domain policy.
4. Reference schema-v12 governance contexts with `ON DELETE RESTRICT`. Preserve
   sealed-context triggers and require governance for every action/verdict that
   grants authority.
5. Store canonical encodings or decomposed fields sufficient to strictly restore
   and revalidate each aggregate. Do not store only a digest when replay needs
   the typed result; do not duplicate unrestricted source content.
6. Preserve WorkRevision v1 tables and records. Add v2-compatible structures or
   columns in a way that makes decoding version explicit and prevents ambiguous
   mixed encodings.
7. Update legacy migration handling so fresh databases and upgrades from every
   historically supported version converge on the same schema and invariants.
8. Keep each migration atomic. A failed statement must leave `user_version` and
   all prior authority data unchanged.

**Files**:

- `internal/controlstore/sqlite/migrations.go`
- `internal/controlstore/sqlite/mission_action_test.go` for migration matrix

**Validation**:

- Fresh database schema equals an upgrade from every supported prior version.
- v12 governance/run/lease/review/integration rows remain readable unchanged.
- Failed migration rolls back schema, data, and `PRAGMA user_version` together.
- New foreign keys, unique indexes, checks, and immutability triggers redden
  deliberate invalid SQL mutants.
- Opening a database newer than supported still fails loudly.

### Subtask T043: Persist mission creation and lifecycle transitions under CAS

**Purpose**: Make mission state and legal advancement durable, replayable, and
single-winner under concurrent callers.

**Steps**:

1. Implement the WP02 mission store port in
   `internal/controlstore/sqlite/mission.go`: create under an exact definition,
   load a consistent snapshot, and commit one prevalidated transition.
2. Persist immutable project/mission/definition key, version, and commitment;
   current step; optional current WorkRevision; state version; terminal outcome;
   UTC timestamps; and durable decision/blocker references.
3. Creation requires an explicit operation and request digest. Exact replay
   returns the stored mission; changed reuse conflicts; duplicate MissionID with
   different immutable scope is never treated as replay.
4. Transition updates current state only when mission, definition commitment,
   current step, and expected state version match. Increment state version once
   and persist old/new step evidence, actor, governance, event, and outbox in the
   same transaction.
5. Check active blockers and required unresolved decisions from the same
   transaction snapshot. Never accept a caller-provided claim that they are
   absent.
6. Translate busy, constraint, corrupt-row, not-found, stale-version, and
   idempotency conditions into stable domain error kinds and retry categories.
7. Strictly restore and validate every loaded domain value; corrupt stored state
   fails closed instead of producing a partially valid mission.

**Files**:

- `internal/controlstore/sqlite/mission.go`
- `internal/controlstore/sqlite/mission_test.go`

**Validation**:

- Create/load/transition/restart round-trip exact domain values.
- One hundred two-connection same-version races produce one transition winner.
- Replay and altered replay remain distinct after process restart.
- Active blockers/open decisions prevent the transition inside the transaction.
- Injected precommit failures leave state version, event, and outbox unchanged.

### Subtask T044: Persist immutable artifact revisions and exact approvals

**Purpose**: Bind validation and approval to exact content identity so a mutable
file path or newer revision cannot inherit authority.

**Steps**:

1. Implement WP03 artifact publication and query ports in
   `internal/controlstore/sqlite/artifact.go`. Atomically persist one immutable
   ArtifactRevision, its validation verdict/findings, operation result, and
   audit event.
2. Enforce positive per-mission/per-kind revision numbers, unique revision IDs,
   canonical digest, bounded repository-relative source reference, source
   revision, creator, UTC time, and exact validation evidence.
3. Publish under expected latest revision so concurrent authors cannot both
   become the same generation. Exact replay returns the original record;
   semantic reuse conflicts without partial findings.
4. Persist approval/rejection as a separate immutable record bound to the exact
   ArtifactRevision ID and content digest, actor, bounded evidence digest,
   sealed governance context, and submission time.
5. Approval must not update the artifact row. Publishing revision N+1 preserves
   revision N and its verdict but freshness queries must never apply N's approval
   to N+1 or to a different mission/kind.
6. Provide consistent queries for latest revision, exact revision, exact verdict,
   and fresh approval used by mission transition gates. Sort findings
   deterministically and validate on restore.
7. Store digests and bounded findings only—never artifact bodies, private
   transcripts, credentials, or unrestricted validator output.

**Files**:

- `internal/controlstore/sqlite/artifact.go`
- `internal/controlstore/sqlite/artifact_test.go`

**Validation**:

- Publication, validation, approval, rejection, query, and restart round-trip.
- New content makes old approval inapplicable without modifying old rows.
- Two-author and two-approver races have one valid result per expected version.
- Crash points yield prior state or one complete revision/verdict plus event.
- Direct SQL mutation/deletion of immutable evidence is rejected.

### Subtask T045: Publish WorkRevision v2 with one current-generation winner

**Purpose**: Persist the complete immutable executable contract while retaining
strict v1 compatibility and atomic finalization.

**Steps**:

1. Implement WP05 finalization/query ports in
   `internal/controlstore/sqlite/workrevision_v2.go`. Store schema version,
   predecessor, planning source, mission definition, dependency policy, canonical
   work/criteria/scopes, lane DAG, provider observations, digests, finalizer,
   governance context, and UTC time.
2. Validate and encode the domain revision before transaction entry, then
   restore and strictly revalidate on every query. Preserve deterministic order
   and byte-equivalent canonical content.
3. Generation 1 requires no predecessor and no current mission generation.
   Later finalization outside reconciliation is refused; successor publication
   belongs to T046.
4. Compare expected current generation/revision and mission state. Insert all
   revision children, current pointer, mission transition, operation result,
   event, and outbox atomically.
5. Use database uniqueness/CAS so 100 two-connection races publish exactly one
   current generation. Losers receive exact replay or typed conflict, never a
   partial duplicate graph.
6. Keep historical v1 decode/query behavior intact. Do not rewrite v1 rows,
   silently synthesize v2 provenance, or allow v1/v2 identity collision.
7. Verify provider observations are persisted as opaque strings/evidence; never
   infer ordering or freshness from their representation.

**Files**:

- `internal/controlstore/sqlite/workrevision_v2.go`
- `internal/controlstore/sqlite/workrevision_v2_test.go`

**Validation**:

- v2 round-trip preserves every field and canonical digest after restart.
- Reordered equivalent inputs restore identically.
- 100 finalization races yield one winner/current pointer and no orphan rows.
- Fault injection at each child/event/pointer boundary fully rolls back.
- Seeded v1 history remains readable and byte/meaning stable.

### Subtask T046: Atomically persist drift and mixed reconciliation

**Purpose**: Publish generation N+1, its complete decisions, consequences, and
future authority actions as one indivisible governed fact.

**Steps**:

1. Implement WP06 drift-report persistence and reconciliation ports in
   `internal/controlstore/sqlite/reconciliation_v2.go`. Persist canonical drift
   identity, base/candidate digests, semantic status, changes, affected sets,
   provider evidence, and observation time idempotently.
2. Clean and observation-only reports remain queryable evidence but cannot
   advance the current generation. A changed report must match the expected old
   and proposed successor digests exactly.
3. In one transaction, compare the mission's current revision/generation,
   revalidate the drift link and complete per-work decision set, insert the
   successor and all graph children, reconciliation header, decisions, fork
   edges, derived status consequences, required future authority actions,
   operation result, event/outbox, and new current pointer.
4. Never persist caller-authored permission booleans. Store only consequences
   produced by the validated domain plan and reject rows inconsistent with its
   disposition/status matrix.
5. Preserve integrated records and immutable Git evidence. Store an explicit
   history-preservation fact and new repair/replacement WorkIDs where required;
   never update prior handoff, review, or integration rows.
6. Exact reconciliation replay returns the complete stored result after restart.
   Changed reuse and stale-current attempts conflict before inserting anything.
7. Ensure consumer eligibility queries observe either all old-generation
   consequences or none. No handoff/review/integration check may see a new
   current pointer without its reconciliation decision.

**Files**:

- `internal/controlstore/sqlite/reconciliation_v2.go`
- `internal/controlstore/sqlite/reconciliation_v2_test.go`

**Validation**:

- Mixed continue/supersede/invalidate/fork round-trips with exact consequences.
- Omitted/duplicate/extra decisions and digest mismatch make zero writes.
- Two-connection successor races have one winner and no orphan decision/action.
- Every transaction-boundary fault recovers to complete N or complete N+1.
- Integrated handoff/review/integration rows are unchanged byte-for-byte.

### Subtask T047: Persist governed actions, decisions, blockers, and recovery evidence

**Purpose**: Complete the durable interruption/audit surface and prove all new
schema paths survive migration, fault injection, concurrency, and restart.

**Steps**:

1. Implement WP07 mission-action ports in
   `internal/controlstore/sqlite/mission_action.go`. Bind action operation,
   request digest, mission state version, step, actor/profile authority, sealed
   governance context, result/refusal, timestamps, and bounded audit evidence.
2. Persist decisions with stable identity, mission/step scope, bounded question
   and options, status, answer/rationale, actor, and resolution time. Resolve
   under expected status/version so two answers cannot both win.
3. Persist blockers with mission/optional work scope, closed kind, bounded
   summary, typed recovery actions, active/cleared status, and audit times.
   Clear under compare-and-set; deletion or transcript-only resolution is not
   permitted.
4. Store action capsules as exact immutable governance references. Reconstruct
   and validate schema-v12 contexts; later profile/doctrine changes must not alter
   100 historical action interpretations.
5. Persist mutating action result, mission change/decision/blocker, event, and
   outbox in the aggregate's transaction. Pure `next` queries create no row,
   event, timestamp, lock, or outbox entry.
6. Add migration tests for fresh schema and every supported historical version,
   including populated v1, workgraph, review/integration, and v12 governance
   fixtures. Verify all historical facts remain readable.
7. Add deterministic fault hooks around every new transaction boundary and
   reopen the database after each fault. Accept only complete prior or complete
   successor state; repeat critical tests under `-race` and two connections.
8. Inspect stored text/blob columns for seeded credentials, transcript bodies,
   environment values, raw tool output, and artifact content; require zero
   disclosure while retaining stable IDs/digests and bounded diagnostics.

**Files**:

- `internal/controlstore/sqlite/mission_action.go`
- `internal/controlstore/sqlite/mission_action_test.go`
- `internal/controlstore/sqlite/migrations.go`
- `internal/controlstore/sqlite/governance_context_test.go`

**Validation**:

```bash
go test ./internal/controlstore/... -run 'Mission|Artifact|WorkRevisionV2|ReconciliationV2|MissionAction|Migration' -count=10
go test -race ./internal/controlstore/...
go vet ./internal/controlstore/...
```

- One hundred sealed action capsules retain exact governance after source edits.
- Pure query loops make zero durable writes.
- Decision/blocker races have one legal winner and deterministic conflicts.
- Every migration/fault fixture reopens with foreign-key checks clean.
- The repository's bounded contract and architecture gates remain green.

## Definition of Done

- Control-store composition exposes every Phase 3 domain port without leaking
  SQL or provider semantics into domain packages.
- Append-only schema v13+ migrations preserve every v1-v12 authority fact and
  converge fresh/upgraded databases on one verified schema.
- Mission creation/transitions are exact-definition, version-CAS,
  operation-idempotent, and atomic with events/outbox.
- Artifact revisions/validations are immutable; approvals bind exact revision
  and governance; newer content never inherits older approval.
- WorkRevision v2 persists every canonical field and has exactly one current
  generation winner across 100 two-connection races.
- WorkRevision v1 evidence remains readable and is never rewritten as v2.
- Drift evidence persists without authority; mixed reconciliation atomically
  publishes the complete successor, decisions, consequences, actions, event,
  outbox, and current pointer.
- Integrated Git/review/handoff evidence is unchanged by reconciliation.
- Decisions and blockers are durable CAS-controlled facts, not transcript state.
- Action records retain exact sealed governance context; pure `next` queries
  perform no mutation.
- Fault injection at every new transaction boundary yields complete prior or
  complete successor state after reopen, with no orphan rows or missing events.
- Stored evidence contains zero secrets, artifact bodies, private transcripts,
  provider stderr, or unrestricted tool output.
- SQLite remains the sole approved Phase 3 backend; no Gapdb or distributed
  control-store implementation enters this WP.
- Focused, repeated, race, vet, migration, foreign-key, architecture, and
  bounded contract gates pass within the twelve owned files.
- Mark all seven subtasks done only after the evidence is green:

```bash
spec-kitty agent tasks mark-status T041 T042 T043 T044 T045 T046 T047 --status done --mission software-development-mission-method-01M11EX2 --json
```

## Risks

- **Policy in SQL**: Constraints enforce structural facts; domain packages still
  decide transitions, freshness, drift, dispositions, and next outcomes.
- **Partial aggregate publication**: Current pointers, events, and outbox rows
  must commit in the same transaction as their authority facts.
- **Historical corruption**: Never edit old migrations or backfill invented v2
  provenance into v1 evidence; test every supported upgrade path.
- **Replay ambiguity**: Inspect operation ID/request digest before mutation and
  distinguish exact replay from changed reuse and stale CAS.
- **SQLite contention**: Use bounded busy handling and short transactions with no
  provider, Git, or network calls under a database lock.
- **Governance disclosure**: Persist sealed commitments and bounded references,
  not source paths, doctrine bodies, credentials, or transcripts.
- **Backend scope drift**: Gapdb adoption requires separate comparison evidence
  and signed human approval; do not create split transactional authority here.

## Reviewer Guidance

Review transaction boundaries before table aesthetics. For each aggregate,
identify the one operation, its CAS predicate, replay lookup, immutable result,
event/outbox writes, and rollback tests. Reject any flow that updates the current
mission/generation pointer separately from its evidence.

Inspect migrations as historical contracts: v1-v12 text must be unchanged,
fresh and upgraded schemas must converge, failures must leave `user_version`
unchanged, and old authority records must decode after upgrade. Verify strict
foreign keys, uniqueness, immutable triggers, one-current-generation enforcement,
and exact governance references without duplicating domain policy in SQL.

Finally, run the two-connection races and every crash point with reopen. Confirm
exact replay after commit, no partial result before commit, immutable integrated
history, zero pure-query writes, and zero seeded secret/transcript leakage.
Reject Gapdb work, provider calls inside transactions, artifact-body storage,
mutable history, cross-host claims, or edits beyond the twelve owned files.
