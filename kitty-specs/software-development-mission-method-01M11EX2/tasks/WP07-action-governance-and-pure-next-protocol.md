---
work_package_id: WP07
title: Action Governance and Pure Next Protocol
dependencies:
- WP02
- WP03
- WP05
requirement_refs:
- FR-021
- FR-022
- FR-023
- FR-024
- FR-028
- NFR-005
- NFR-009
- NFR-012
- NFR-014
- C-001
- C-005
- C-006
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T035
- T036
- T037
- T038
- T039
- T040
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/mission/
create_intent:
- internal/governancecontext/mission_action.go
- internal/governancecontext/mission_action_test.go
- internal/mission/decision.go
- internal/mission/decision_test.go
- internal/mission/next.go
- internal/mission/next_test.go
- internal/mission/action.go
- internal/mission/action_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/governancecontext/mission_action.go
- internal/governancecontext/mission_action_test.go
- internal/mission/decision.go
- internal/mission/decision_test.go
- internal/mission/next.go
- internal/mission/next_test.go
- internal/mission/action.go
- internal/mission/action_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP07 - Action Governance and Pure Next Protocol

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the governed application seam that tells an agent or human exactly what happens next without performing the action it describes.

The result must be one closed typed `NextResult` computed from one consistent mission snapshot. Repeating the query against unchanged state must return an equivalent value and write nothing. Decisions and blockers must be durable domain facts rather than transcript-only pauses. Any mutating action named by `next` must execute later through a separate idempotent command bound to the exact actor profile, doctrine, configuration, mission-definition revisions, granted authority, and expected mission state.

Implementation command:

```bash
spec-kitty agent action implement WP07 --agent codex
```

## Context

WP02 supplies the static software-development definition, mission instance, step contracts, legal transitions, and state-version authority. WP03 supplies typed artifact revisions and exact-revision approval freshness. WP05 supplies the immutable WorkRevision v2 identity and finalization semantics. Rebase onto their accepted lineages and use their actual types and store ports.

Phase 1 already provides sealed `governancecontext.Context` values. Each context contains ordered entries from the closed kinds `config`, `doctrine`, `profile`, and `mission_type`, with immutable refs, revisions, content commitments, and one verified aggregate digest. This WP must select and bind only the action-relevant entries. It must never record mutable file paths as authority, load an entire unrelated doctrine catalog, or persist credentials, raw environment values, private transcript text, or unrestricted tool output.

The `next-result.schema.json` contract is authoritative for the result vocabulary:

```text
query | action | work_ready | review_ready |
decision_required | blocked | terminal
```

Phase 3 production may return `work_ready` for finalized work. `review_ready` is reserved for forward-compatible synthetic contract tests because production review dispatch belongs to Phase 5. CLI, HTTP, MCP, Team Kitty, and Orchestrator API serving are Phase 6 concerns; this WP creates application types that those surfaces can project later.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated to WP07. This package depends on WP02, WP03, and WP05, so verify their accepted types are present in the dependency-resolved base before editing. Merge the reviewed lane only into `feat/software-development-mission-method`.

Do not edit the primary checkout, create or manipulate worktrees manually, merge another agent's lane, push to `main`, or modify files beyond the eight owned paths. WP08 will implement durable SQLite storage for these ports; do not add SQL here.

## Product Laws

1. `Next` is a pure query and has no operation ID because it has no effect.
2. A `NextResult` names a legal possibility; it does not confer authority to perform it.
3. Every mutating action requires its own operation ID, expected state version, actor, and sealed action governance.
4. Same operation ID plus the same canonical request digest replays; the same ID plus different content conflicts.
5. Tracker status, assignment, provider readiness, and Markdown projections never determine mission state or ready work.
6. `work_ready` references the exact current immutable WorkRevision and canonical `WorkID` values.
7. Decisions and blockers have stable identities and lifecycles independent of conversational text.
8. Human-facing messages explain typed state; callers never parse them to branch.
9. One consistent snapshot produces exactly one result kind with only fields legal for that kind.
10. Governance history remains interpretable after source profiles or doctrine change.

### Subtask T035: Define bounded mission-action governance context

**Purpose**: Bind each legal mutation to the smallest sealed governance evidence that authorized that specific action.

**Steps**:

1. Add a mission-action governance value that references a validated `governancecontext.Context` by stable ID, schema version, and aggregate commitment.
2. Include the mission ID, step key, action key, accountable actor identity, required role/profile class, and a closed granted-authority vocabulary.
3. Require exact selected entry identities and revisions for profile, doctrine, configuration, and mission type where the action contract requires them.
4. Validate every selected entry against the sealed source context; a ref/revision/commitment mismatch must fail rather than silently fall back.
5. Require the selected profile and mission-type entries for every mutating mission action; allow doctrine/config selections only according to explicit action requirements.
6. Reject duplicate entries, unknown kinds, unregistered authority grants, blank action keys, wrong mission scope, and aggregate mismatch with stable typed errors.
7. Keep immutable defensive copies and deterministic canonical ordering.
8. Expose only bounded references and commitments; no raw doctrine bodies, profile prompts, secret-bearing config, environment values, or transcript excerpts.
9. Prove later mutation of source slices or creation of a newer governance context does not alter a previously created action binding.
10. Add round-trip, tamper, missing-required-entry, excess-unrelated-entry, and changed-source tests.

**Files**:

- `internal/governancecontext/mission_action.go`
- `internal/governancecontext/mission_action_test.go`

**Validation**:

- One action binding explains exactly who, what action, what authority, and which immutable governance revisions apply.
- One hundred reconstructed bindings retain the original profile/doctrine commitments after new source contexts are created.
- No sensitive or unbounded content appears in the action value or its encoded form.

### Subtask T036: Model durable decisions and their lifecycle

**Purpose**: Replace transcript-only clarification with stable, bounded, auditable mission decisions.

**Steps**:

1. Define a decision identity, mission/step scope, bounded question, canonical unique options, status, and creation provenance.
2. Use the closed statuses `open`, `resolved`, `deferred`, and `canceled` from the data model.
3. Require resolution to record the selected answer, bounded rationale, actor, governance reference, and UTC resolution time.
4. Make state transitions explicit: open may resolve, defer, or cancel; terminal states cannot be rewritten.
5. Require an expected current status or state version for mutation so two resolvers cannot silently overwrite one another.
6. Make same-operation same-content transitions replay safely and conflicting reuses fail through the action command seam.
7. Reject answers outside the declared option set when options are closed; permit explicitly modeled free-form decisions only when their type says so.
8. Preserve decision history and stable identity across mission queries.
9. Provide a narrow repository port for snapshot reads and later transactional mutation; WP08 owns persistence.
10. Add creation, every legal transition, illegal rewrite, concurrent/stale status, option validation, and defensive-copy tests.

**Files**:

- `internal/mission/decision.go`
- `internal/mission/decision_test.go`

**Validation**:

- Open decisions block or shape `next` through identity, not transcript inspection.
- Resolved, deferred, and canceled records remain immutable audit evidence.
- Diagnostics expose stable kind, affected identity, retry safety, and bounded recovery category.

### Subtask T037: Model durable blockers and bounded recovery

**Purpose**: Represent conditions that prevent legal progress without collapsing them into errors, prose, or skipped gates.

**Steps**:

1. Define blocker identity, mission ID, optional WorkID, step key, closed kind, bounded summary, canonical recovery actions, status, and observation time.
2. Use an explicit `active` and `cleared` lifecycle; clearing records actor, governance, reason, and UTC time without deleting the original evidence.
3. Require each blocker kind to allow only registered recovery categories.
4. Keep provider transcripts, exception dumps, credentials, environment values, and arbitrary command output out of blocker records.
5. Reject empty recovery guidance, duplicate actions, wrong mission/work scope, invalid timestamps, and repeated conflicting clears.
6. Preserve active blockers in stable ordering when multiple conditions exist.
7. Define deterministic selection precedence for `next` when blockers and other possible outcomes coexist.
8. Ensure a cleared blocker no longer prevents progress but remains available to audit readers.
9. Add a narrow repository port compatible with WP08's aggregate transaction boundary.
10. Add active, clear, replay, conflict, privacy/bounds, stable-order, and precedence tests.

**Files**:

- `internal/mission/decision.go`
- `internal/mission/decision_test.go`

**Validation**:

- An active blocker yields exactly one `blocked` result with stable identity and recovery data.
- Clearing the blocker requires a separate governed command and never happens during `next`.
- Bounded validation prevents a raw external failure from becoming durable mission evidence.

### Subtask T038: Define the closed NextResult contract

**Purpose**: Provide one transport-neutral, machine-branchable result vocabulary for every valid mission state.

**Steps**:

1. Implement schema version 1 and the exact seven result kinds declared in `next-result.schema.json`.
2. Include mission ID, mission state version, and current step key in every result.
3. Add kind-specific constructors that make illegal field combinations difficult or impossible.
4. Model action descriptors with action key, mutating flag, and governance action name; the descriptor is not an authorization token.
5. Model decision and blocker payloads with stable IDs and bounded structured data.
6. Model `work_ready` with the exact WorkRevision ID and canonically sorted unique WorkIDs.
7. Keep `review_ready` constructible for contract tests but unreachable from the Phase 3 production state selector.
8. Model terminal outcome and typed diagnostic fields, including stable kind and retry safety.
9. Validate that fields forbidden for one kind are absent rather than silently ignored.
10. Add JSON/schema-shaped round trips, unknown-kind rejection, illegal-field matrix, duplicate-ready-work, and defensive-copy tests.

**Files**:

- `internal/mission/next.go`
- `internal/mission/next_test.go`

**Validation**:

- Encoded values conform structurally to the checked-in schema contract.
- Every legal kind round-trips with stable identities and no prose parsing.
- Every illegal cross-kind field combination is rejected with a typed diagnostic.

### Subtask T039: Implement a pure next query over one consistent snapshot

**Purpose**: Select exactly one legal outcome without mutating mission, artifact, work, decision, blocker, tracker, or projection state.

**Steps**:

1. Define a read-only snapshot port that returns mission state, definition revision, applicable artifact/approval facts, current WorkRevision, open decisions, active blockers, and terminal outcome at one mission state version.
2. Require one snapshot read per query; do not assemble a result from independently changing repositories or provider calls.
3. Establish explicit precedence consistent with the mission definition, including corrupt/unsupported state, active blocker, required decision, required query input, legal action, finalized ready work, and terminal completion.
4. Derive lifecycle actions only from WP02 step contracts and exact WP03 approval freshness.
5. Derive `work_ready` only from WP05's current finalized WorkRevision and mission authority.
6. Never read tracker status, assignment, external readiness projections, `tasks.md`, or other rendered files.
7. Return stable typed diagnostics for missing mission, unsupported definition, corrupt snapshot, stale artifact, and inconsistent current revision.
8. Canonically order equivalent results and exclude observation-only timestamps so unchanged snapshots compare equivalently.
9. Instrument test repositories with read/write counters and assert zero writes, zero provider calls, and exactly one snapshot acquisition.
10. Run every valid mission state and competing-condition precedence case through table-driven tests.

**Files**:

- `internal/mission/next.go`
- `internal/mission/next_test.go`

**Validation**:

- Repeating `Next` 100 times over one snapshot returns equivalent values and performs zero writes.
- Changing only tracker status or projection bytes cannot alter the result because neither is a dependency.
- A current finalized revision names exact ready work; it never guesses readiness from mutable provider state.

### Subtask T040: Implement separate idempotent action commands and provenance tests

**Purpose**: Ensure executing an action is explicit, governed, replay-safe, and never an accidental side effect of asking what is legal.

**Steps**:

1. Define a command envelope with schema version, correlation ID, operation ID, mission ID, expected state version, action key, actor/process identity where available, governance binding, and canonical payload digest.
2. Keep correlation identity observational; it must never substitute for operation identity.
3. Validate the requested action against the current WP02 step contract and the exact action descriptor obtainable from an equivalent snapshot.
4. Validate action governance through T035 before invoking any mutation port.
5. Dispatch to narrow domain command handlers rather than switch on human-readable instructions or transport route names.
6. Define replay records/results so same operation plus same digest returns the prior outcome and same operation plus different digest conflicts.
7. Include stable success/failure envelope fields, affected aggregate identities, retry safety, and bounded recovery categories.
8. Prove stale expected state, wrong actor authority, changed governance aggregate, unsupported action, open decision, and active blocker fail before mutation.
9. Record provenance sufficient for WP08 to persist the action and resulting transition atomically; do not add in-memory authority that persistence cannot reconstruct.
10. Add tests contrasting `Next` query purity with explicit action mutation, including concurrent duplicate commands and 100 governance-history reconstructions.

**Files**:

- `internal/mission/action.go`
- `internal/mission/action_test.go`
- `internal/governancecontext/mission_action.go`
- `internal/governancecontext/mission_action_test.go`

**Validation**:

- Querying an action performs no mutation; executing its separate command performs at most one authorized transition.
- Same request replays deterministically and conflicting operation reuse fails.
- Historical action evidence remains bound to the original governance entries after later profile/doctrine changes.

## Implementation Discipline

Reuse the accepted mission, artifact, WorkRevision, ID, and sealed governance types. Do not create shadow mission-state, artifact-approval, work-readiness, or governance models merely to simplify tests. Introduce narrow read and command ports that WP08 can implement transactionally.

Keep all application semantics independent of SQLite and transports. Do not add CLI parsing, JSON-RPC, HTTP handlers, MCP tools, Team Kitty calls, Beads reads, filesystem projection reads, or Gapdb code. JSON methods on domain application envelopes are acceptable when they enforce the checked-in contract rather than define a transport.

Use constructors and closed enums for invariants. Human-readable messages remain bounded explanations; every branchable outcome needs a stable kind. Avoid `map[string]any` for core result/action fields.

## Test Strategy

Run governance and decision/blocker tests first:

```bash
go test ./internal/governancecontext ./internal/mission -run 'MissionAction|Governance|Decision|Blocker' -count=10
```

Run the pure-query and action-command matrix:

```bash
go test ./internal/mission -run 'Next|Action' -count=10
go test -race ./internal/governancecontext ./internal/mission -count=1
```

Run compatibility and static checks:

```bash
go test ./internal/governancecontext ./internal/mission ./internal/artifact ./internal/workgraph -count=1
go test ./... -run '^$' -count=1
go vet ./internal/governancecontext ./internal/mission
go mod verify
gofmt -d internal/governancecontext/mission_action.go internal/governancecontext/mission_action_test.go internal/mission/decision.go internal/mission/decision_test.go internal/mission/next.go internal/mission/next_test.go internal/mission/action.go internal/mission/action_test.go
git diff --check
git status --short
```

Use deterministic clocks and fake repositories with observable method counters. Do not use sleeps, wall-clock timing floors, a tracker provider, projection fixtures, or external services in the unit suite.

## Definition of Done

- Mission actions bind exact sealed configuration, doctrine, profile, and mission-definition commitments required by the action.
- Governance values contain no credentials, raw environment, private transcript, unrestricted output, or unrelated doctrine content.
- Decisions and blockers are stable, bounded domain facts with explicit legal lifecycles and audit provenance.
- `NextResult` implements exactly the seven schema-version-1 kinds with strict kind-specific fields.
- `Next` reads one consistent snapshot, produces one deterministic result, performs zero writes, and makes zero provider/projection calls.
- `work_ready` names the exact current WorkRevision and canonical WorkIDs; tracker status is never consulted.
- `review_ready` remains contract-test-only in Phase 3 production selection.
- Action descriptors do not confer authority; mutations require a separate command envelope.
- Action commands enforce operation identity, expected state, current step law, actor authority, and sealed governance.
- Same operation/same digest replays; same operation/different digest conflicts without mutation.
- One hundred unchanged next queries remain equivalent and one hundred historical governance bindings survive later source changes.
- Failures expose stable kind, aggregate identities, retry safety, and bounded recovery categories.
- Focused, repeated, race, compilation, vet, formatting, module, and hygiene checks pass.
- Only the eight frontmatter-owned files are modified by implementation.
- T035 through T040 are recorded through the event-sourced status command:

  ```bash
  spec-kitty agent tasks mark-status T035 T036 T037 T038 T039 T040 --status done --mission software-development-mission-method-01M11EX2 --json
  ```

## Risks

- **Query side effects**: Convenience logging, blocker creation, provider refresh, or action reservation during `Next` violates purity. Keep observation separate from durable mutation.
- **Snapshot tearing**: Several repository reads can combine incompatible state versions. Require one consistent snapshot boundary.
- **Descriptor-as-token**: A returned action name is guidance, not authorization. Recheck state and governance in the command.
- **Tracker authority leakage**: Mutable ticket status or assignment cannot make work ready or advance steps.
- **Mutable governance**: Paths and current profile names rewrite history. Bind immutable revisions and commitments.
- **Context overcollection**: Persisting whole doctrine/profile bodies creates privacy and audit risk. Store only bounded commitments and grants.
- **Prose protocol**: Clients parsing summaries will diverge across transports. Branch only on closed kinds and typed fields.
- **Idempotency confusion**: Correlation IDs cannot prevent duplicate effects. Require a distinct operation ID and request digest.
- **Premature Phase 5/6 work**: Production review readiness, dispatch, CLI/API/MCP/Team Kitty, and external serving remain deferred.
- **Persistence mismatch**: Domain ports too weak for atomic WP08 implementation will force authority gaps. Make aggregate and expected-version boundaries explicit.

## Reviewer Guidance

Approve only if a reviewer can prove that querying and acting are two separate authority boundaries.

Verify explicitly:

1. `Next` performs exactly one snapshot read and no writes or external provider calls.
2. All seven result kinds exist, but Phase 3 production does not emit `review_ready`.
3. Kind-specific constructors reject illegal mixed payloads and unknown values.
4. `work_ready` comes only from a current immutable WorkRevision.
5. Tracker state and Markdown projections are absent from the query dependency graph.
6. Open decisions and active blockers are durable identities with deterministic precedence.
7. Clearing/resolving them requires separate governed mutation.
8. Action governance names exact entry revisions, commitments, aggregate, actor, and granted authority.
9. Stored evidence is bounded and free of secret or transcript content.
10. The action command rechecks expected state and governance instead of trusting the prior query.
11. Idempotent replay and conflicting operation reuse are directly tested.
12. One hundred repeated queries and provenance reconstructions provide non-vacuous invariance evidence.
13. No transport, database adapter, tracker provider, projection reader, execution allocator, or review dispatcher appears in the diff.
14. The implementation is confined to the exact eight owned files.

Reject a solution that mutates while querying, creates an operation ID for `Next`, infers readiness from tracker status, returns free-form next instructions without a closed kind, treats an action descriptor as authority, binds governance by mutable path, stores full prompts or tool output, uses correlation identity for idempotency, or folds Phase 5/6 dispatch into this package.
