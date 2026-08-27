---
work_package_id: WP02
title: Static Mission Definition and Lifecycle Authority
dependencies:
- WP01
requirement_refs:
- FR-001
- FR-002
- FR-003
- FR-006
- FR-007
- FR-024
- FR-028
- NFR-014
- C-001
- C-003
- C-004
- C-005
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T006
- T007
- T008
- T009
- T010
- T011
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/mission/
create_intent:
- internal/ids/mission.go
- internal/mission/model.go
- internal/mission/definition.go
- internal/mission/service.go
- internal/mission/lifecycle_test.go
- internal/missiontype/softwaredev/definition.go
- internal/missiontype/softwaredev/definition_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/ids/mission.go
- internal/mission/model.go
- internal/mission/definition.go
- internal/mission/service.go
- internal/mission/lifecycle_test.go
- internal/missiontype/softwaredev/definition.go
- internal/missiontype/softwaredev/definition_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 - Static Mission Definition and Lifecycle Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Establish the smallest closed mission authority needed for the Phase 3
software-development method. Introduce typed mission identities and lifecycle
values, an explicit registry of immutable versioned definitions, one statically
registered software-development definition, and a transition service whose
compare-and-set contract makes mission state—not tracker status, Markdown, or a
transport—the only source of legal advancement.

This WP does not allocate work, leases, lanes, branches, or worktrees. It does
not dispatch agents, perform review or integration, parse `tasks.md`, consult
tracker status, introduce a dynamic workflow language, or add a production CLI,
API, MCP, Team Kitty, or migration surface.

## Context

Phase 1 established that execution authority belongs to Spec Kitty and Phase 2
proved that Beads may own mutable work facts without becoming an authority
kernel. Phase 3 restores the mission method above those boundaries. This WP is
the first lifecycle slice: it defines which mission law was selected and which
state transition may occur, without yet implementing the artifact, workgraph,
projection, or persistence adapters that provide later evidence.

### Required design boundaries

1. `internal/mission` owns lifecycle law, closed values, and ports; it imports no
   tracker adapter, projection, helper command, SQLite, Git runner, or transport.
2. `internal/missiontype/softwaredev` is static data over mission contracts,
   with no storage, dispatch, provider behavior, or dynamic registration.
3. Selection is exact and explicit: no default, alias, fuzzy, or “latest” lookup.
4. Definition identity is immutable and registry contents close at construction.
5. Step gates consume typed evidence, never files, Markdown, tracker state, Git
   prose, transcript text, or mutable process-global configuration.
6. Stored state, selected definition, and expected version alone authorize a
   transition; refusals are typed, bounded, deterministic, and secret-safe.
7. WP02 defines the atomic persistence operation; WP08 implements SQLite.
8. Reserved Phase 4/5 vocabulary is non-actionable, and WP01 guards stay intact.

## Branch Strategy

Work only in the Spec Kitty lane worktree assigned for WP02. The planning base
and merge target are both `feat/software-development-mission-method`. This WP
depends on WP01, so the assigned lane must include the accepted WP01
architecture policy before implementation begins.

Do not edit the primary checkout, create an additional worktree, merge directly
to `main`, or push to a protected branch. Keep every change within the exact
owned-file list in this prompt. If a required change falls outside that list,
stop and report the ownership conflict instead of widening scope silently.

## Implementation Command

```bash
spec-kitty agent action implement WP02 --agent codex
```

### Subtask T006: Define mission identities and closed lifecycle values

**Purpose**: Give the mission authority stable, validated identities and value
types that cannot be confused with tracker, execution, artifact, or transport
identifiers.

**Steps**:

1. Add `internal/ids/mission.go` for mission-method identities that are not
   already represented by the existing typed ID package. Preserve the existing
   `MissionID` definition and parser; do not duplicate or rename it merely to
   create a new file.
2. Introduce only identities earned by the Phase 3 model, such as definition,
   definition-version/commitment reference, step, decision, blocker, or mission
   event identity. Keep each semantically distinct even if its wire form is a
   validated string.
3. Reuse the existing ID validation law unless a domain value has a stronger
   closed vocabulary. Do not accept whitespace, path syntax, empty values, or
   arbitrary unvalidated strings at authority boundaries.
4. In `internal/mission/model.go`, define closed values for lifecycle state,
   step key, terminal outcome, approval/evidence requirement, and stable error
   or recovery category. Prefer named constants plus `Validate`/parse methods
   over open string comparisons scattered through the service.
5. Model `MissionInstance` with immutable mission, project, and selected
   definition identity; authoritative current step; optional current
   WorkRevision identity; optimistic `state_version`; UTC creation/update
   timestamps; and optional terminal outcome.
6. Make validation reject zero identities, unsupported values, impossible
   version/timestamp combinations, a terminal outcome on a nonterminal state,
   and any current WorkRevision reference before the lifecycle permits one.
7. Keep collections immutable from the caller's perspective. Constructors and
   accessors must defensively copy maps and slices used in definitions,
   evidence, findings, or history.
8. Use existing `ids.ProjectID`, `ids.MissionID`, `ids.WorkRevisionID`, and
   related types rather than parallel mission-local string aliases for the same
   concepts.

**Files**:

- `internal/ids/mission.go`
- `internal/mission/model.go`

**Validation**:

- Valid IDs and lifecycle values parse and round-trip deterministically.
- Invalid, zero, whitespace-bearing, path-like, and unknown values fail.
- A valid pre-finalization mission has no current WorkRevision.
- Invalid terminal/current-step and state-version combinations fail.
- Caller mutation of constructor input cannot mutate validated mission state.

### Subtask T007: Implement immutable definitions and a closed registry

**Purpose**: Make mission type resolution explicit, versioned, deterministic,
and closed before any lifecycle command can use it.

**Steps**:

1. In `internal/mission/definition.go`, define `MissionDefinition` and
   `StepContract` according to the data model. Each step must declare its stable
   key, required typed inputs, produced outputs, validation policy, approval
   policy, legal predecessors, legal successors, governance action, and whether
   the action is mutating.
2. Validate definition key and positive version, unique step keys, an existing
   initial step, at least one existing terminal step, symmetric declared edges,
   reachability from the initial step, and absence of cycles in the Phase 3
   authoritative progression.
3. Reject duplicate edges, unknown predecessor/successor keys, disconnected
   steps, self-edges, contradictory policies, empty governance actions for
   mutating steps, and terminal steps that expose production successors.
4. Compute the definition commitment from a canonical, normalized
   representation. Input slice/map ordering must not change the digest; semantic
   changes must change it. Do not include pointer identity, time, map iteration
   order, or presentation-only prose.
5. Provide a constructor for a registry from an explicit set of definitions.
   Reject duplicate exact key/version pairs and conflicting commitments. Copy
   all data so post-construction caller mutation cannot alter registry law.
6. Resolve only an exact supported key and version. Missing key, missing
   version, malformed selection, and unsupported version must return distinct
   stable typed failures with bounded recovery guidance.
7. Do not provide mutable registration after construction. If composition needs
   several mission types later, it must construct a new closed registry before
   serving commands.
8. Do not add YAML/JSON workflow loading, reflection registration, init-time
   side effects, filesystem discovery, semantic-version “latest” selection, or
   a fallback definition.

**Files**:

- `internal/mission/definition.go`
- `internal/mission/model.go`
- `internal/mission/lifecycle_test.go`

**Validation**:

- Exact registered key/version resolution returns byte-equivalent law.
- Missing, duplicate, malformed, and unsupported definitions fail closed.
- Equivalent reordered definition inputs produce one commitment.
- Every semantic contract mutation changes the commitment.
- Registry construction copies inputs and exposes no mutable backing storage.
- Invalid graph fixtures name the exact step and violated definition rule.

### Subtask T008: Encode lifecycle transition laws and typed refusals

**Purpose**: Ensure mission advancement can occur only along the selected
definition's declared edges with complete typed gate evidence.

**Steps**:

1. Define a transition request carrying mission identity, expected state
   version, requested successor step, operation identity, bounded evidence
   references, governance-context reference when required, actor, and UTC
   occurrence time. Keep future artifact details behind typed evidence
   references so WP03 can extend the gate without an import cycle.
2. Implement a pure transition evaluator that loads no ambient state and checks
   the current instance against its exact resolved definition. Its output must
   be either one complete successor candidate or one stable typed refusal.
3. Check request identity/scope, expected version, definition key/version and
   commitment, current-step contract, declared successor edge, required
   evidence kinds, approval-policy satisfaction, active blockers, and terminal
   status before constructing a successor.
4. Treat specification and plan requirements as closed policy declarations.
   The specify gate must require intent, scope, requirements, acceptance
   framing, assumptions, and boundaries. The plan gate must require
   architecture, data flow, risk, compatibility, migration, and validation
   strategy. WP03 later proves exact artifact revision freshness.
5. Never infer evidence from a path, file existence, branch name, commit
   message, tracker status, checkbox, or transcript. Evidence is accepted only
   through the typed transition input/port contract and must be scoped to the
   exact mission and current state.
6. Return distinct typed failures for invalid request, definition mismatch,
   illegal transition, state-version conflict, missing evidence, stale or
   rejected approval placeholder, active blocker, terminal mission, and
   unsupported Phase 4+ action.
7. A refusal must not construct or persist a partial successor. The evaluator
   must be deterministic for equal current state, definition, and request.
8. Increment state version exactly once on a successful transition, preserve
   immutable identity fields, update UTC audit time monotonically, and set a
   terminal outcome only when the destination contract declares it.

**Files**:

- `internal/mission/model.go`
- `internal/mission/definition.go`
- `internal/mission/service.go`
- `internal/mission/lifecycle_test.go`

**Validation**:

- Every declared Phase 3 edge succeeds with exact required evidence.
- Every undeclared edge, skip, reverse move, and terminal escape is rejected.
- Removing each required specification or plan evidence kind rejects the gate.
- Tracker-looking status strings and Markdown-looking input have no effect.
- Equal requests and state yield equal successor/refusal semantics.
- No refusal changes the supplied instance or records a store mutation.

### Subtask T009: Define the static software-development mission method

**Purpose**: Supply one explicit production mission definition without creating
a general workflow programming language or copying Python command sequencing.

**Steps**:

1. In `internal/missiontype/softwaredev/definition.go`, expose a constructor for
   the exact `software-development` key and supported definition version. Build
   it only from public `internal/mission` contracts.
2. Encode the Phase 3 lifecycle progression:
   `initialized -> specifying -> specification_approved -> planning ->
   plan_approved -> decomposing -> work_draft_ready -> work_finalized`.
3. Represent `reconciliation_required -> work_finalized` as a governed
   successor-generation lifecycle path without implementing drift or
   reconciliation mechanics owned by WP06.
4. Declare specification and plan evidence/approval policies at the appropriate
   transition boundaries. Declare typed governance action names for each
   mutating step so WP07 can bind action-scoped doctrine and profile context.
5. Reserve later execution/review/integration vocabulary only if needed to make
   the definition version stable. Mark it non-actionable in Phase 3 and prove
   the service refuses attempts to enter it.
6. Keep software-development-specific step data out of generic mission service
   conditionals. The service follows contracts; it must not switch on the
   definition key to discover policy.
7. Do not use package `init`, a global mutable registry, filesystem config,
   templates, YAML, Python assets, tracker provider values, or CLI strings to
   define mission law.
8. Make the definition constructor validate itself and return an error if a
   future edit makes the static graph inconsistent. Tests must pin key, version,
   commitment behavior, complete step census, and exact legal edge census.

**Files**:

- `internal/missiontype/softwaredev/definition.go`
- `internal/missiontype/softwaredev/definition_test.go`

**Validation**:

- The static definition validates and resolves only by its exact key/version.
- Tests census every step, legal edge, required gate, governance action, initial
  step, and terminal/reserved status.
- No generic mission source names Beads, SQLite, Markdown, API, MCP, or Team
  Kitty workflow semantics.
- No static-definition code performs I/O or changes process-global state.

### Subtask T010: Add the mission store port and optimistic transition service

**Purpose**: Define the durable atomic boundary and version-conflict behavior
that WP08's control-store adapter must honor.

**Steps**:

1. Define a narrow mission-owned persistence port in
   `internal/mission/service.go`. It must support creating a mission under an
   exact definition, loading an authoritative snapshot, and atomically
   committing one evaluated transition under expected state version and
   operation identity.
2. Keep storage-shaped fields, SQL transactions, schema versions, serialized
   blobs, event-table details, and SQLite errors out of the port. Express the
   aggregate operation and its durable evidence, not CRUD tables.
3. Define create and transition services that resolve the exact definition,
   validate all inputs before mutation, and delegate one atomic commit to the
   port. Never allow a store adapter to choose the legal successor.
4. Require an explicit operation identity and deterministic request commitment
   for mutating calls. Exact replay may return the prior result; the same
   operation ID with changed semantic input must return an idempotency conflict.
5. Require expected state version. Two callers racing from the same snapshot
   may not both advance it: one complete transition wins and the other receives
   a stable version conflict or exact replay.
6. Distinguish not-found, already-exists, definition mismatch, version conflict,
   idempotency conflict, invalid stored state, and unavailable persistence with
   stable machine-readable kinds and recovery categories.
7. Ensure blockers and unresolved decision references in the authoritative
   snapshot can prevent advancement. Detailed decision workflows belong to
   WP07, but the mission transition port must not require a later breaking
   replacement to honor durable gate facts.
8. Use an in-test memory port in `lifecycle_test.go` to prove semantics. Do not
   create a production in-memory store, SQLite adapter, fake tracker call, event
   outbox schema, or helper command in this WP.
9. Emit/return bounded transition evidence sufficient for WP08 to persist and
   FR-028 auditors to bind mission, old/new step, old/new state version,
   operation, actor, definition commitment, governance reference, and time.
10. Preserve state on every refusal and injected store error. Do not report a
    successful in-memory successor if the atomic store commit fails.

**Files**:

- `internal/mission/service.go`
- `internal/mission/lifecycle_test.go`

**Validation**:

- Create binds one exact definition key/version/commitment permanently.
- Exact operation replay returns the same durable transition result.
- Changed input under a reused operation identity returns idempotency conflict.
- Two expected-version peers produce one winner and one typed conflict.
- A store failure leaves the prior instance current and exposes no success.
- An active blocker or unresolved required decision prevents the store call.
- Diagnostics remain bounded and contain no seeded secret/transcript fixture.

### Subtask T011: Prove the definition and authority laws exhaustively

**Purpose**: Demonstrate that the new lifecycle authority is complete,
non-vacuous, deterministic, race-safe at its port contract, and isolated from
all deferred Phase 4+ behavior.

**Steps**:

1. Build table-driven tests in `internal/mission/lifecycle_test.go` for every
   valid lifecycle state, every declared edge, every possible undeclared pair,
   required evidence omission, blocker/decision guard, terminal behavior, and
   state-version conflict.
2. Add definition-construction mutants for duplicate keys, duplicate versions,
   duplicate steps, missing initial/terminal steps, disconnected graphs,
   unknown/asymmetric edges, cycles, unsupported values, and altered
   commitments. Each mutant must fail for the intended stable rule.
3. Prove registry non-vacuity: a valid software-development definition resolves,
   while removing it, changing its version, requesting an unknown type, or
   registering a conflicting duplicate fails with the expected error kind.
4. Prove authority isolation with hostile evidence values resembling tracker
   statuses, checked Markdown tasks, branch names, Git commits, and CLI action
   prose. None may satisfy or redirect a transition unless represented by the
   exact typed requirement declared by the selected contract.
5. Exercise exact replay, altered replay, stale expected version, and a
   synchronized two-goroutine transition race against the test port. Assert one
   durable winner, no partial mutation, and deterministic losing diagnostics.
6. Add input-mutation tests for definitions, contracts, evidence references,
   and mission snapshots. Validated/registered values must remain unchanged.
7. Run architecture tests from WP01 and confirm the new packages have exactly
   the intended direction: softwaredev may depend on mission; mission may not
   depend on softwaredev, tracker adapters, projection, helpercmd, or transports.
8. Run the race detector for the owned packages and repeat the focused lifecycle
   suite enough times to expose accidental map-order or shared-state behavior.
9. Keep tests hermetic: no Beads binary, network, Git repository, SQLite file,
   shell process, wall-clock sleeps, or environment-dependent workflow assets.
10. Record all six subtask completions only after the complete WP evidence is
    green.

**Files**:

- `internal/mission/lifecycle_test.go`
- `internal/missiontype/softwaredev/definition_test.go`
- All WP02 production files as exercised by the tests

**Validation**:

```bash
go test ./internal/ids ./internal/mission ./internal/missiontype/softwaredev -count=10
go test -race ./internal/ids ./internal/mission ./internal/missiontype/softwaredev
go test ./internal/architecture -count=1
go test ./internal/scenario -run 'Scope|Architecture' -count=1
go vet ./internal/ids ./internal/mission ./internal/missiontype/softwaredev
```

The implementer must also run the repository's bounded contract/static gate
appropriate to the lane after the focused checks. Report any unrelated existing
failure explicitly; do not weaken a guard or broaden owned files to make it
green.

## Definition of Done

- Mission identities and lifecycle values are typed, validated, deterministic,
  defensively copied, and distinct from tracker or execution identities.
- `MissionDefinition` and `StepContract` encode complete, canonical, versioned
  lifecycle law and produce a deterministic commitment.
- The registry is immutable after construction, resolves exact key/version
  pairs only, and fails closed for missing, duplicate, or unsupported types.
- The statically registered `software-development` definition has a censused
  Phase 3 graph, exact specification/plan policies, and no dynamic workflow DSL.
- Mission creation permanently binds an exact definition and commitment.
- Transition evaluation accepts only declared successors with complete typed
  evidence and never infers authority from tracker status, Markdown, Git, or
  transcript prose.
- The mission store port expresses atomic create/transition semantics,
  idempotent replay, and optimistic version conflict without leaking SQLite.
- Refusals/store failures produce no partial successor; blocker/decision refs
  gate advancement; diagnostics are bounded and machine-readable.
- Production behavior stops at Phase 3 lifecycle authority; there is no lease,
  workspace, dispatch, review, integration, merge, external surface, or Gapdb
  implementation.
- Focused, repeated, race, vet, architecture, and bounded repository gates pass;
  all changes remain within the exact owned-file list.
- Mark the six subtasks complete through the canonical event-sourced command:

```bash
spec-kitty agent tasks mark-status T006 T007 T008 T009 T010 T011 --status done --mission software-development-mission-method-01M11EX2 --json
```

## Risks

- **Second workflow language or silent default**: Keep contracts closed Go
  values and require exact definition key/version selection.
- **Artifact authority leakage**: Declare typed requirements here; leave exact
  approval freshness to WP03 and never infer it from files or Git.
- **Persistence leakage**: Model an aggregate compare-and-set, not SQL rows.
- **Concurrency ambiguity**: Distinguish replay, altered-operation reuse, and
  stale version with stable recovery categories.
- **Mutable or over-broad law**: Copy and close registries; keep Phase 4/5
  vocabulary non-actionable.
- **Disclosure**: Record IDs/digests and bounded findings, never raw content.

## Reviewer Guidance

Review this as an authority boundary. The selected definition—not caller
conditionals, tracker status, or projections—must fully determine legal moves.
Require exact-resolution and complete transition-matrix tests, including every
omitted specification/plan requirement and hostile Markdown/tracker/Git value.

Verify static dependency direction is `softwaredev -> mission`, with no I/O,
mutable globals, `init` registration, “latest” selection, or workflow DSL.
The store port must express atomic mission semantics rather than SQLite rows;
replay, operation conflict, version conflict, blockers, store failure, and a
two-caller race must be hermetic tests. Reject success before durable commit,
mutable registered law, unsafe diagnostics, Phase 4+ behavior, or edits beyond
the seven owned files.
