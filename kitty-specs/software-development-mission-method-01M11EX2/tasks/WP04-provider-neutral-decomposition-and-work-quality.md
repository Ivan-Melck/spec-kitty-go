---
work_package_id: WP04
title: Provider-Neutral Decomposition and Work Quality
dependencies:
- WP02
- WP03
requirement_refs:
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-028
- NFR-006
- NFR-013
- NFR-014
- C-001
- C-002
- C-007
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T017
- T018
- T019
- T020
- T021
- T022
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/
scope: codebase-wide
create_intent:
- internal/tracker/work_definition.go
- internal/tracker/work_definition_test.go
- internal/tracker/fake/work_definition_test.go
- internal/workgraph/observation.go
- internal/workgraph/validation.go
- internal/workgraph/validation_test.go
- internal/mission/decomposition.go
- internal/mission/decomposition_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/policy.go
- internal/architecture/policy_test.go
- internal/tracker/provider.go
- internal/tracker/work_definition.go
- internal/tracker/work_definition_test.go
- internal/tracker/fake/provider.go
- internal/tracker/fake/store.go
- internal/tracker/fake/work_definition_test.go
- internal/workgraph/observation.go
- internal/workgraph/validation.go
- internal/workgraph/validation_test.go
- internal/mission/decomposition.go
- internal/mission/decomposition_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP04 - Provider-Neutral Decomposition and Work Quality

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the provider-neutral decomposition boundary that turns one freshly approved plan into durable tracker work and one complete, validated observation of the proposed executable graph.

This WP must prove that tracker-backed work is understandable on its own, traces bidirectionally to approved specification and plan scope, has explicit ownership and dependency structure, and can be observed identically through the durable fake provider contract. Invalid work must produce stable typed findings before WorkRevision finalization. The tracker owns mutable work definitions at this stage; it does not own mission transitions, approvals, graph publication, or readiness.

Implementation command:

```bash
spec-kitty agent action implement WP04 --agent codex
```

## Context

WP02 supplies the static mission definition and mission lifecycle authority. WP03 supplies immutable artifact revisions, validation, exact-revision approvals, and freshness checks. Rebase this lane onto their accepted state before implementing; use their actual exported contracts instead of guessing parallel models.

Phase 2 already established a provider-neutral tracker surface in `internal/tracker`, including stable `ExternalWorkRef` mappings, structured criteria, complete `ProviderObservation` values, catalog capability discovery, guarded mutation semantics, and a durable fake provider. Extend those contracts compatibly. Do not add Beads fields to mission or workgraph packages and do not require the provider's human-formatted CLI output.

The primary proving graph contains two standalone work items with one dependency. Decomposition creates or updates the provider-backed definitions only after the exact approved plan is eligible. Observation then converts the provider records into stable Spec Kitty identities and validates quality, coverage, ownership, dependency integrity, and the graph that will be handed to WP05 for lane collapse and immutable finalization.

The decomposition boundary is a saga, not a cross-store transaction. Provider mutations may succeed before the local mission operation finishes, time out ambiguously, or encounter a concurrent human edit. Every mutation therefore needs an operation identity, an expected provider observation when updating, replay recognition, and bounded reconciliation guidance. Never claim the tracker and control store commit atomically.

## Branch Strategy

Work only in the Spec Kitty lane worktree allocated to WP04. This package depends on WP02 and WP03, so use the dependency-resolved base selected by Spec Kitty and verify both accepted lineages are present. Land only into `feat/software-development-mission-method` after review.

Do not edit the primary checkout, create another worktree manually, merge another lane yourself, push to `main`, or change files outside the thirteen owned paths. WP05 and later packages depend on the types produced here; keep their future files untouched. The two architecture files are a narrowly governed review-driven overlap with accepted WP01, serialized through WP04's dependency chain; they authorize only the workgraph-to-tracker denial and its mutant.

## Locked Boundaries

- Tracker status and assignment never decide mission state or executable readiness.
- `WorkID` is the stable internal identity; provider external IDs never substitute for it.
- Provider revisions are opaque equality tokens, not counters or freshness timestamps.
- Work definition remains mutable only before finalization; this WP does not publish a `WorkRevision`.
- Markdown task files are neither decomposition input nor authoritative output.
- Beads protocol, metadata keys, status names, and availability assumptions stay inside its adapter.
- Decomposition requires the exact approved and fresh plan artifact, not a path that merely exists.
- Validation findings are typed, bounded, sorted, and safe to persist; private transcripts and raw tool output are excluded.
- Production execution leases, worktrees, review, acceptance, and integration remain out of scope.
- SQLite/Gapdb persistence changes and Beads parity are owned by later WPs.

### Subtask T017: Define provider-neutral work-definition observations

**Purpose**: Establish the complete mutable-planning vocabulary shared by fake, Beads, mission decomposition, validation, and later WorkRevision finalization.

**Steps**:

1. Add a `WorkDefinitionObservation` value rooted in stable `ProjectID`, `WorkID`, and `ExternalWorkRef` identities.
2. Include title, description, design context, structured criteria, dependencies expressed only as `WorkID` edges, declared write scopes, provider observation/revision, metadata schema, and observation time.
3. Include bounded provenance linking the work to approved plan scope and requirement outcomes; use stable IDs or digests rather than raw transcript text.
4. Preserve defensive-copy behavior for slices, opaque metadata, and canonical provider fields.
5. Extend provider-neutral create/update requests only as required to express these fields without adding Beads-specific names or semantics.
6. Define a narrow catalog/observation port for decomposition rather than widening the legacy `Provider` compatibility interface with unrelated methods.
7. Validate identity agreement among mapping, observation, project, and work records.
8. Reject empty required context, duplicate criterion IDs, duplicate dependencies, invalid write-scope syntax, unknown metadata schema, and zero provider observations with stable errors.
9. Make ordering and equality semantics explicit: callers may normalize collections, while provider revisions remain opaque.
10. Add construction, defensive-copy, JSON/canonical-data where applicable, invalid-value, and look-alike-provider tests.

**Files**:

- `internal/tracker/provider.go`
- `internal/tracker/work_definition.go`
- `internal/tracker/work_definition_test.go`

**Validation**:

- A two-item provider observation round-trips without losing identity, criteria, dependency, write-scope, provenance, or provider revision.
- Mutating caller-owned input/output slices cannot change the constructed value.
- No exported provider-neutral type mentions Beads, Dolt, Markdown, lanes, mission status, or execution state.

### Subtask T018: Extend the durable fake provider for decomposition

**Purpose**: Supply a truthful, persistent reference provider for the complete work-definition contract and later conformance testing.

**Steps**:

1. Extend the fake store schema compatibly to persist design context, structured criteria, dependency identities, write scopes, provenance, metadata schema, and stable internal mapping.
2. Implement provider-neutral work creation with operation identity and deterministic replay behavior.
3. Implement guarded work-definition updates using the existing opaque observation/revision contract.
4. Implement dependency storage and complete reads required by decomposition; do not infer readiness from the dependency graph.
5. Report the catalog create/update and blocking-graph capability levels the fake actually enforces.
6. Preserve atomic file replacement, process locking, bounded context cancellation, injected failure behavior, and existing Phase 2 assignment/discussion data.
7. Preserve unknown JSON fields or version state according to the existing fake-store compatibility law; use an explicit version migration if the encoded format changes.
8. Add deterministic hooks or existing failure modes for unavailable, timeout-before-apply, timeout-after-apply, and revision-conflict cases.
9. Prove a restart retains every work-definition field and observation identity.
10. Prove same operation plus same request replays, while same operation plus different request returns a typed conflict without partial mutation.

**Files**:

- `internal/tracker/fake/provider.go`
- `internal/tracker/fake/store.go`
- `internal/tracker/fake/work_definition_test.go`

**Validation**:

- The two-item graph survives close/reopen with exact semantic content.
- Failure injection yields either the prior state or one complete successor state.
- Human/concurrent edits are never overwritten by an unguarded retry.
- Existing Phase 2 fake-provider tests continue to pass.

### Subtask T019: Implement the decomposition saga and approved-plan gate

**Purpose**: Ensure only an eligible, exact approved plan can create mutable tracker work and that partial provider outcomes remain observable and recoverable.

**Steps**:

1. Define a decomposition command containing mission identity, expected mission state/version, exact plan artifact revision, bounded work proposals, actor, operation identity, and action governance reference where the dependency contracts require it.
2. Consult the WP02 mission authority and WP03 artifact service to require the plan step, passed validation, approved exact revision, and fresh content digest before any provider mutation.
3. Refuse absent, rejected, stale, wrong-kind, wrong-mission, or superseded plan revisions before acquiring provider mutation authority.
4. Validate the complete proposed work set before the first provider write when validation can be performed locally.
5. Preflight provider capabilities for create/update/dependency operations before mutation.
6. Apply work mutations with per-item deterministic operation identities derived from the parent operation without treating provider order as authority.
7. Re-observe each created/updated item and its dependency edges; return the exact observations used by later validation.
8. Model partial, timeout, unavailable, conflict, and ambiguous results as typed decomposition outcomes with bounded recovery categories.
9. On replay, compare the request digest and provider attribution before accepting existing work; never adopt a look-alike ticket solely by title.
10. Do not advance the mission to finalized state or publish an executable graph in this WP. Return a validated draft/observation for the later finalization command.

**Files**:

- `internal/mission/decomposition.go`
- `internal/mission/decomposition_test.go`
- `internal/tracker/work_definition.go`

**Validation**:

- Every ineligible plan case makes zero provider mutations.
- A valid two-item request creates exactly two mapped tracker records and one dependency.
- Repeating the same operation is a deterministic replay; changing its payload conflicts.
- Ambiguous provider outcomes surface reconciliation guidance instead of claiming rollback.

### Subtask T020: Validate standalone context, criteria, boundaries, and overreach

**Purpose**: Ensure a fresh implementer can execute each work item without private conversation and without silently expanding beyond approved scope.

**Steps**:

1. Define stable `FindingKind`, severity, work/field identity, bounded message, and recovery category values in the workgraph validation surface.
2. Validate that each work item has a specific objective, adequate description, design context, and explicit owned/write scope.
3. Require at least one structured criterion with stable identity, non-empty outcome, valid verifier kind, and explicit required/optional meaning.
4. Reject duplicate criterion identities and criteria that merely restate implementation activity without an observable result.
5. Detect over-broad work using explicit structural limits and approved-scope mappings, not subjective title length or keyword scoring.
6. Reject work that combines unrelated ownership domains without an explicit, validated reason.
7. Keep human-readable details useful but bounded; findings must branch on stable kind and fields rather than prose.
8. Canonically sort findings by work identity, kind, and field so repeated validation is byte-stable.
9. Add a complete valid-item control and one focused mutant per finding kind.
10. Prove validation consumes structured work observations, never `tasks.md` or transcript context.

**Files**:

- `internal/workgraph/observation.go`
- `internal/workgraph/validation.go`
- `internal/workgraph/validation_test.go`

**Validation**:

- The valid two-item graph has zero quality findings.
- Missing context, untestable criteria, overreach, ambiguous boundaries, and duplicate structures each yield their exact typed finding.
- Reordering provider results does not reorder findings or change their meaning.

### Subtask T021: Validate requirement coverage and ownership

**Purpose**: Prove the decomposition covers every required approved outcome and contains no work outside the approved plan.

**Steps**:

1. Accept an explicit approved-scope model derived from the exact spec and plan artifact revisions supplied by WP03.
2. Require every mandatory requirement/outcome to map to at least one work item.
3. Require every work item to trace to one or more approved plan concerns or scope identities.
4. Reject references to unknown, stale, optional-as-mandatory, or wrong-mission scope identities.
5. Treat duplicate coverage references deterministically without inflating coverage counts.
6. Detect ambiguous ownership where two independent items claim the same exclusive file/write scope without a declared serialization or lane relationship.
7. Treat compatible read-only context separately from mutable ownership.
8. Produce typed missing-coverage, orphan-work, unknown-scope, and ownership-conflict findings with affected stable identities.
9. Add mutation controls removing one requirement mapping, adding one orphan work item, and creating one overlapping exclusive scope.
10. Keep the validator provider-neutral: identical semantic observations from fake and Beads must produce equal findings.

**Files**:

- `internal/workgraph/observation.go`
- `internal/workgraph/validation.go`
- `internal/workgraph/validation_test.go`
- `internal/mission/decomposition_test.go`

**Validation**:

- Every required approved outcome in the proving fixture has traceable work.
- Extra work and unknown references fail rather than being silently ignored.
- Equivalent provider ordering yields the same canonical coverage result.

### Subtask T022: Validate dependencies and the post-lane graph seam

**Purpose**: Reject invalid dependency graphs both before and after overlapping write scopes are collapsed into the lane DAG finalized by WP05.

**Steps**:

1. Validate all dependency endpoints against stable `WorkID` values in the observed set.
2. Reject self-dependencies, duplicate edges where forbidden, unknown endpoints, direct cycles, and multi-node cycles.
3. Define a narrow validation input or reusable function for WP05 to submit its post-write-scope lane projection.
4. Require the projected lane graph to cover every work item exactly once and retain all ordering constraints implied by work dependencies.
5. Reject unknown lanes, missing membership, duplicate membership, lane self-edges, and cycles introduced only by write-scope collapse.
6. Keep lane construction itself out of this WP; WP05 owns `lanes.go` and the deterministic collapse algorithm.
7. Return typed graph findings or existing compatible `workgraph.Error` kinds with exact node/edge fields.
8. Add table-driven two-node, long-cycle, disconnected-valid, unknown-node, and lane-collapse-cycle cases.
9. Add a load-bearing mutant where pre-collapse work dependencies are acyclic but a deliberately bad lane projection is cyclic.
10. Prove findings remain deterministic across map and provider-order permutations.
11. Extend the permanent architecture policy with a package-specific denial for
    `internal/workgraph -> internal/tracker`, evaluated before the broader
    authority-to-contract tier allowance. Preserve the legitimate
    `internal/mission -> internal/tracker` projection edge.
12. Add a production-shaped policy mutant that injects the forbidden
    workgraph-to-tracker edge and is killed by the exact stable architecture
    rule. Keep it under an existing censused top-level test.

**Files**:

- `internal/workgraph/observation.go`
- `internal/workgraph/validation.go`
- `internal/workgraph/validation_test.go`
- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**:

- The two-item dependency graph passes before and after a valid lane projection.
- Every invalid endpoint or cycle fails with the intended stable kind and exact identities.
- Removing post-lane validation makes the lane-collapse mutant survive and red the test.
- A workgraph-to-tracker import fails the permanent policy, while the mission
  projection control remains accepted.

## Implementation Discipline

Extend existing tracker contracts rather than creating a second issue abstraction. Reuse `ExternalWorkRef`, `ProviderObservation`, `Criterion`, `DependencyRef`, capability preflights, typed tracker errors, and durable fake-store mechanics. Preserve compatibility for Phase 2 callers and fixtures.

Keep domain laws in `internal/tracker`, `internal/workgraph`, and `internal/mission`; the fake package implements ports but must not define mission policy. Do not add a generic repository/service/util package, Markdown reader, Beads adapter edit, SQL table, CLI command, or execution concept.

Use constructors and unexported fields when defensive-copy or validation invariants matter. Avoid getters and types with no production consumer. If a dependency API differs from the planning model, adapt to its accepted surface and document the alignment in tests rather than duplicating it.

## Test Strategy

Run the provider-neutral value and fake-provider loops first:

```bash
go test ./internal/tracker ./internal/tracker/fake -count=1
go test -race ./internal/tracker ./internal/tracker/fake -count=1
```

Run quality and graph mutants repeatedly:

```bash
go test ./internal/workgraph -run 'Observation|Validation|Coverage|Ownership|Dependency|Lane' -count=10
go test ./internal/architecture -count=3
```

Run the approved-plan and saga cases:

```bash
go test ./internal/mission -run 'Decomposition' -count=10
go test -race ./internal/mission ./internal/workgraph -count=1
```

Then verify compatibility and repository health:

```bash
go test ./internal/tracker/... ./internal/workgraph ./internal/mission -count=1
go test ./... -run '^$' -count=1
go vet ./internal/tracker/... ./internal/workgraph ./internal/mission
go mod verify
gofmt -d internal/tracker/provider.go internal/tracker/work_definition.go internal/tracker/work_definition_test.go internal/tracker/fake/provider.go internal/tracker/fake/store.go internal/tracker/fake/work_definition_test.go internal/workgraph/observation.go internal/workgraph/validation.go internal/workgraph/validation_test.go internal/mission/decomposition.go internal/mission/decomposition_test.go
git diff --check
git status --short
```

Use deterministic clocks and explicit failure injection. Do not use sleeps, network access, a live Beads installation, or timing floors in this WP.

## Definition of Done

- Provider-neutral work observations contain complete identities, context, criteria, dependencies, write scopes, provenance, metadata schema, and opaque provider revision evidence.
- The durable fake provider creates, updates, persists, reopens, and re-observes the complete definition without losing Phase 2 data.
- Decomposition refuses every absent, rejected, stale, wrong-kind, or wrong-mission plan before provider mutation.
- The two-item approved plan creates exactly two stable tracker definitions and one dependency with deterministic replay.
- Partial and ambiguous provider outcomes return typed, bounded recovery guidance and never claim cross-store rollback.
- Standalone-context, criterion, overreach, boundary, coverage, orphan, and ownership violations produce exact sorted findings.
- Unknown, self, duplicate, direct-cycle, multi-node-cycle, and post-lane-cycle dependency mutants fail.
- Provider result ordering does not affect semantic observations or validation findings.
- No tracker status, Markdown file, provider external ID, or private transcript becomes authority.
- No Beads-specific semantic field enters tracker, workgraph, or mission contracts.
- All focused, repeated, race, compilation, vet, formatting, module, and hygiene checks pass.
- Only the thirteen frontmatter-owned files are modified by implementation.
- T017 through T022 are recorded through the event-sourced status command:

  ```bash
  spec-kitty agent tasks mark-status T017 T018 T019 T020 T021 T022 --status done --mission software-development-mission-method-01M11EX2 --json
  ```

## Risks

- **Parallel work model**: Creating another ticket/work aggregate would split provider truth. Extend the existing tracker vocabulary.
- **Provider gravity**: Beads labels, metadata keys, revision ordering, or status semantics in mission/workgraph code would violate substitutability.
- **False transaction**: Provider mutation plus local state cannot be committed atomically. Preserve saga evidence and ambiguous outcomes.
- **Stale plan authority**: Checking only an artifact path or old approval would allow changed content to decompose. Bind the exact fresh revision.
- **Weak fake**: A fake that cannot conflict, time out after apply, or preserve restart state will overstate the contract.
- **Subjective validation**: Keyword or length heuristics produce unstable quality judgments. Prefer explicit structured requirements and bounded rules.
- **Coverage inflation**: Duplicate or unknown mappings can make incomplete work look covered. Normalize identities before counting.
- **Lane ownership confusion**: WP04 validates a supplied projection; it must not preempt WP05's lane-collapse algorithm.
- **Error prose branching**: Clients must use stable kinds and fields, not parse diagnostic sentences.
- **Cross-lane edits**: WP02/WP03 contracts are dependencies and WP05 owns finalization/lanes. Modify only WP04-owned files.

## Reviewer Guidance

Approve only if the complete two-item decomposition can be understood and validated without provider-specific knowledge, Markdown, or private transcript context.

Verify explicitly:

1. `WorkID` remains authoritative and every provider mapping agrees with it.
2. Provider revisions remain opaque equality evidence; no ordering arithmetic appears.
3. Every executable field survives fake-provider persistence and restart.
4. The plan approval/freshness gate runs before any provider mutation.
5. Replay compares operation identity and request content; it does not adopt by title.
6. Timeout-after-apply and concurrent-edit cases remain ambiguous/conflicted until observation resolves them.
7. Quality findings are typed, bounded, sorted, and tied to stable work/field identities.
8. Coverage is bidirectional: all required outcomes have work and all work has approved scope.
9. Ownership overlap is explicit and does not rely on filenames parsed from Markdown.
10. Pre-lane and supplied post-lane graph validation both reject the intended mutants.
11. Tests exercise the production constructors, provider, saga, and validators rather than static fixture literals.
12. Existing Phase 2 tracker and fake-provider behavior remains green.
13. No Beads, SQLite, Gapdb, CLI, API, execution, review, or integration implementation leaked into the diff.
14. The diff is confined to the exact eleven owned paths.

Reject a solution that creates a second work identity model, infers readiness from tracker status, parses `tasks.md`, relies on provider revision ordering, skips the exact approved-plan gate, claims rollback after ambiguous provider success, validates only a hand-written happy-path graph, performs lane collapse here, or returns untyped prose-only findings.
