---
work_package_id: WP18
title: Autonomous Readiness and Mission Ergonomics Authority
dependencies:
- WP10
requirement_refs:
- FR-007
- FR-008
- FR-009
- FR-010
- FR-015
- FR-017
- FR-018
- FR-019
- FR-023
- FR-024
- FR-025
- FR-036
- NFR-001
- NFR-004
- NFR-005
- NFR-010
- NFR-015
- NFR-016
- C-004
- C-005
- C-009
- C-010
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T093
- T094
- T095
- T096
- T097
- T098
- T099
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/mission/
create_intent:
- internal/controlstore/gapdb/mission_authority.go
- internal/controlstore/gapdb/mission_authority_test.go
- internal/architecture/m2_autonomy_contract_test.go
- internal/architecture/m2_autonomy_mutant_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/mission/**
- internal/missiontype/**
- internal/governancecontext/mission_action.go
- internal/governancecontext/mission_action_test.go
- internal/controlstore/gapdb/mission_authority.go
- internal/controlstore/gapdb/mission_authority_test.go
- internal/architecture/m2_autonomy_contract_test.go
- internal/architecture/m2_autonomy_mutant_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP18 — Autonomous Readiness and Mission Ergonomics Authority

## Objective

Replace the Python-shaped exclusive mission-step protocol with a Go-native,
pure capability-readiness model. Preserve durable activity history, blockers,
decisions, exact evidence, replay, and coarse lifecycle outcomes while removing
phase ceremony as a source of authority.

The result must tell an autonomous driver every action that is currently safe,
every action that is unavailable, and the exact typed predicates responsible.
It must permit independent authorized actions to coexist. No manual phase
advance, prompt completion claim, Markdown artifact, or edited status projection
may grant or withhold authority.

Implementation command:

```bash
spec-kitty agent action implement WP18 --agent codex
```

## Governing correction

Read and implement `post-approval-defect-10.md`. This WP is the kernel
correction routed by that ratified defect. It follows accepted WP10 so that
acceptance appears in the same capability census as creation, artifact,
workgraph, execution, handoff, review, and integration readiness. WP11 depends
on this WP and projects the resulting contract.

## Ownership and dependency rules

- Start from accepted WP10 and its complete dependencies.
- Work only in the Spec Kitty-assigned lane.
- Edit only the files in this WP's frontmatter.
- Preserve the authority, replay, idempotency, and refusal-order laws already
  proven by WP02–WP10.
- Do not change Git, tracker, verifier, CLI, API, MCP, or runtime-host adapters.
- Keep GapDB calls inside the existing control-store adapter.
- Compatibility code may decode historical phase events only when needed for a
  deliberate refusal/migration diagnosis. It may not influence new readiness.

## Product laws

1. Mission lifecycle is coarse: open, paused, failed, canceled, or completed.
   Activity history is not an exclusive position in a workflow.
2. Authorization is capability-owned and derived from exact current facts.
   There is no authoritative `CurrentStep`, successor edge, or phase advance.
3. One observation yields a complete, deterministic action census containing
   all authorized actions and all unavailable actions with typed predicates.
4. Independent actions may be authorized simultaneously. Ordering exists only
   where one action's safety predicate depends on another's evidence.
5. Decisions and blockers bind exact action/evidence scope, not whatever phase
   happened to be current when they were recorded.
6. Every blocking predicate names its authority source, evidence identity when
   applicable, responsible producing capability, and recovery classification.
7. Every required criterion has an explicit applicability result and a typed
   producer. Prompt prose, template presence, or path conventions are not
   producers.
8. Inapplicable, unavailable, failed, stale, satisfied, waived, and ambiguous
   are distinct closed states. A waiver is immutable scoped evidence, not a
   force flag.
9. Human renderers and projections cannot be parsed or edited into authority.
10. Exact OperationID replay precedes current readiness checks. A changed
    binding conflicts before domain reads or effects.
11. Reducers and readiness evaluation are pure and deterministic. Playback
    reaches no filesystem, Git, tracker, verifier, network, clock, or store.
12. The command/result contract is typed, bounded, versioned, and projectable
    unchanged to local CLI, Orchestrator API, and optional stateless MCP.

## T093 — Replace the authoritative phase cursor with coarse lifecycle

Refactor mission state so creation, pause/resume, failure, cancellation, and
completion remain authoritative, but phase/step is not. Replace lifecycle
advance events with capability activity events that record what occurred,
under which OperationID, actor/process/governance context, evidence binding,
and result. They must never imply the sole next action.

Remove readiness checks that compare `CurrentStep` or a successor edge.
Historical phase-shaped records, if retained temporarily for decoding, are
non-authoritative and cannot appear in new writes. Add a controlled mutant that
restores one phase comparison and prove it fails.

## T094 — Define the complete capability and predicate vocabulary

Define closed stable ActionID values for the admitted M2 capabilities and
closed predicate kinds for lifecycle, decision, artifact, WorkRevision,
eligibility, lease/epoch, workspace, candidate, independent review,
acceptance, repository target, external effect, conflict, and policy facts.

Each unavailable action carries canonically ordered predicate records with
status, authority coordinate, evidence digest/identity, responsible capability,
recovery, and bounded diagnosis. Define explicit action applicability separate
from authorization. Reject unknown action, predicate, producer, recovery, or
applicability values.

## T095 — Build the pure action-census evaluator

Implement a pure evaluator over one exact immutable authority snapshot. Return
all authorized actions, all unavailable applicable actions, decisions required,
conflicts, terminal status, and the snapshot revision/digest. Canonical ordering
must make equivalent inputs byte-identical.

Prove at least two independent work actions coexist, dependency-bound actions
remain unavailable for exact reasons, and changing activity order without
changing authority facts does not change the census. A definition with multiple
valid actions is ordinary, not an ambiguous successor error.

## T096 — Scope decisions, blockers, evidence, and WorkRevision finalization

Replace step-scoped decision/blocker authority with exact capability, work,
artifact, criterion, policy, or mission scope. A decision blocks only actions
whose declared predicate consumes it. Unrelated work remains schedulable.

Remove the `StepWorkDraftReady` prerequisite from WorkRevision finalization.
Finalization depends on exact complete workgraph/artifact/provider evidence and
policy. Preserve generation, drift, reconciliation, and
`continue/supersede/invalidate/fork` semantics.

## T097 — Add producer-backed applicability and instruction authority

Define provider-neutral contracts for criterion applicability and evidence
production. An applicable criterion names a typed producer contract; an
inapplicable criterion cites exact policy basis. Missing producer is a
configuration refusal, not a request for an agent to fabricate an artifact.

Define the closed instruction-authority labels `invariant`, `default`, and
`example`. Only a failed invariant predicate can withhold authority. Preserve
these labels in governance context and typed action descriptions without
parsing prose.

## T098 — Publish readiness through GapDB mission authority

Update mission authority encoding, events, reducers, operation binding, and
GapDB adapter translation for the new state and census inputs. Each semantic
mutation remains one sealed AtomicBatch publication with exact heads and
global OperationID membership. Census output itself is a derived projection,
not a second durable truth.

Prove checkpoint-plus-suffix and origin replay produce byte-identical mission
facts and census. Projection loss is harmless. No runtime `status.json`,
`status.events.jsonl`, task Markdown, or alternate state database is read or
written by the Go authority path.

## T099 — Qualify autonomous scheduling semantics and kill ceremony mutants

Run model and property tests that repeatedly select authorized actions in
different legal orders until terminal facts converge. Include parallel work,
decision-required, pause/resume, drift/reconciliation, rejection/rework,
verification unavailable/failure, conflict, cancellation, and restart.

Mandatory mutants:

- restore one authoritative current step;
- return only the first authorized action;
- treat multiple valid actions as ambiguity;
- scope a blocker to the whole mission instead of its consumer actions;
- let prompt prose or a status projection satisfy a predicate;
- require a producerless or inapplicable artifact;
- collapse unavailable verification into failure or confirmed absence;
- hide a skipped member from a successful batch result;
- make activity ordering change readiness with identical facts; and
- perform external I/O during readiness evaluation or replay.

## Validation

```bash
go test ./internal/mission ./internal/missiontype/... ./internal/governancecontext -count=20
go test ./internal/controlstore/gapdb -run 'Mission|Readiness|Census|Replay' -count=10
go test ./internal/architecture -run 'Autonomy|Ceremony|ProjectionAuthority' -count=1
go test -race ./internal/mission ./internal/missiontype/... ./internal/governancecontext ./internal/controlstore/gapdb -count=1
go vet ./internal/mission/... ./internal/missiontype/... ./internal/governancecontext ./internal/controlstore/gapdb
staticcheck ./internal/mission/... ./internal/missiontype/... ./internal/governancecontext ./internal/controlstore/gapdb
go test ./... -run '^$' -count=1
gofmt -d internal/mission internal/missiontype internal/governancecontext internal/controlstore/gapdb internal/architecture
git diff --check
```

## Definition of done

- No authoritative phase/current-step/successor protocol remains in the new
  mission command path.
- Complete action census exposes every authorized and unavailable action with
  exact typed reasons and deterministic ordering.
- Independent actions coexist and may be scheduled concurrently.
- Decisions, blockers, applicability, and evidence producers have exact scope.
- WorkRevision finalization depends on safety evidence, not ceremony.
- Activity history remains durable, attributable, replayable, and non-
  authoritative for scheduling.
- Derived human status and Git-tracked lane metadata cannot mint authority.
- Ceremony, prose, fake-artifact, missing-producer, and single-action mutants
  fail their intended semantic assertions.
- The completed WP is handed to an independent reviewer and not self-approved.
