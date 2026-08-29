---
work_package_id: WP11
title: Beads Mission-Method Provider Parity
dependencies:
- WP10
requirement_refs:
- FR-008
- FR-009
- FR-015
- FR-016
- FR-027
- FR-028
- NFR-001
- NFR-002
- NFR-006
- NFR-011
- NFR-013
- NFR-014
- C-001
- C-007
- C-009
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T057
- T058
- T059
- T060
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/mission_method_integration_test.go
- internal/scenario/mission_method_beads_test.go
- internal/controlstore/sqlite/decomposition.go
- internal/controlstore/sqlite/decomposition_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/catalog.go
- internal/tracker/beads/catalog_test.go
- internal/tracker/beads/metadata.go
- internal/tracker/beads/metadata_test.go
- internal/tracker/beads/graph.go
- internal/tracker/beads/graph_test.go
- internal/tracker/beads/mission_method_integration_test.go
- internal/scenario/mission_method_beads_test.go
- internal/tracker/work_definition.go
- internal/tracker/work_definition_test.go
- internal/mission/decomposition.go
- internal/mission/decomposition_test.go
- internal/controlstore/sqlite/decomposition.go
- internal/controlstore/sqlite/decomposition_test.go
- internal/helpercmd/mission_method.go
- internal/helpercmd/mission_method_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP11 - Beads Mission-Method Provider Parity

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Compose the Phase 3 software-development mission method with the already
qualified Beads v1.2.2 JSON adapter, proving that Beads can hold and return the
same mutable work definitions as the durable fake provider without acquiring
mission or execution authority. The two providers must produce equivalent
semantic WorkRevision content while retaining honest, provider-specific
observation evidence and failure behavior.

## Context

Phase 2 established a bounded Beads adapter around the `bd` executable and
schema-1 JSON envelope. It already owns canonical binding, health/preflight,
catalog mapping, structured criteria metadata, content-derived observation
revisions, blocking dependencies, writer policy, and stable diagnostics. WP11
extends those existing seams only where WP04's provider-neutral
`WorkDefinitionObservation` contract requires richer planning content.

WP10 provides the complete two-item mission scenario against the durable fake
provider. Reuse that scenario as the semantic reference. Do not fork mission
logic into `internal/tracker/beads`, add Beads branches to mission/workgraph
packages, or make the fake emulate Beads. Differences in provider name,
external ID, opaque revision, timestamp, and protocol evidence are expected;
differences in normalized WorkID, description, design context, criteria,
dependencies, write scopes, planning provenance, or executable digest are not.

### Ratified WP11 authority correction

Pre-implementation review exposed two inherited seams that cannot represent
Beads honestly. Correct them as part of this work package; do not disguise them
inside the adapter:

- Work-definition capabilities are operation-scoped. Derive whether this
  validated proposal set requires create, guarded update, and blocking-graph
  support. Create-only decomposition may use an honestly unguarded provider;
  any update proposal requires real guarded-update capability, and any proposal
  containing dependencies requires blocking-graph capability. Keep the strict
  full-port conformance check, but do not require unused capabilities.
- The root decomposition `OperationID` and complete canonical request digest
  are consumer authority, not tracker state. Reserve them durably in the local
  control store after all local mission/plan/work validation and before any
  provider call. Reuse of the same root operation with a different mission,
  plan, scope, work set, actor, governance context, or occurrence time conflicts
  without touching the provider. Exact replay may resume a partial saga.
- Use the existing immutable `phase3_operations` namespace with operation kind
  `decomposition_saga`; do not add a migration or an adapter-owned root ledger.
  A fresh root reservation paired with a replayed child mutation is impossible
  and must fail closed. The result is replayed only when the root reservation
  and every child mutation are replays.
- The request digest must bind the complete expected mission snapshot, including
  lifecycle, current WorkRevision, timestamps, terminal outcome, blockers, and
  decisions, plus the complete command time and all existing request fields.

This correction expands WP11 to exactly sixteen owned files: the original eight
provider/scenario files plus the provider-neutral capability contract,
decomposition service, SQLite operation reservation, and helper composition
files and their paired tests. The fake provider's historical preparation method
may remain for compatibility, but it is not decomposition authority.

Preserve every Phase 2 boundary:

- Execute only typed commands through the bounded process runner. Consume only
  documented JSON envelope/error shapes; never parse warning or human-formatted
  `bd` output.
- Do not import Beads Go packages, Beads `internal` code, or query Dolt/Beads
  tables directly. The adapter is the only package with provider protocol
  knowledge.
- Beads owns mutable visible work before finalization. Spec Kitty alone owns
  mission steps, approvals, WorkRevisions, drift, reconciliation, runs,
  workspaces, review, and integration.
- Provider status, readiness, assignee, labels, comments, Molecules, formulas,
  agent identity, hooks, and merge slots do not decide the mission lifecycle.
- Provider timestamps are diagnostic only. Complete content-derived observation
  identity must detect changes even when timestamps are equal.
- Beads updates remain honestly unguarded where v1.2.2 lacks provider CAS.
  Local serialization is not a fence against direct human/provider writes.
- Real-provider qualification requires the explicitly pinned binary/version,
  reviewed SHA-256 digest, isolated temporary workspace, non-interactive mode,
  and no network after installation. Ordinary tests must remain runnable
  without Beads or provider-specific environment variables.
- Slow, unavailable, stale, malformed, or wrong provider results must never
  publish a local WorkRevision or advance mission state.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated for WP11 after WP10 is accepted.
The dependency base contains the complete fake-provider mission slice, and the
merge target is `feat/software-development-mission-method`. Do not branch from
`main`, create a worktree manually, or edit files outside the ratified WP11
scope. Keep all changes within the exact sixteen owned files and extend existing adapter
capabilities rather than creating a parallel mission-method adapter.

Start implementation with:

```bash
spec-kitty agent action implement WP11 --agent codex
```

Read this mission's `spec.md`, `plan.md`, `data-model.md`, `research.md`, and
WorkRevision/command contracts, plus the Phase 2 tracker capability contract
and the existing catalog, metadata, graph, revision, real-provider, and fault
tests before coding.

### Subtask T057: Project Beads catalog metadata into neutral work observations

**Purpose**: Make the existing adapter satisfy WP04's complete mutable work
definition contract without introducing Beads-shaped domain types.

**Steps**:

1. Adapt `catalog.go`, `metadata.go`, and their tests to project each uniquely
   mapped Beads issue into WP04's provider-neutral work observation: stable
   ProjectID/WorkID, external reference, title, description, design context,
   structured criteria, write scopes, plan/requirement provenance, metadata
   schema, opaque provider revision, and observation boundary.
2. Reuse the existing `spec_kitty` namespace and mapping identity. Implement
   exactly the WP04 metadata schema/version; do not create a Beads-only work
   model. Retain read compatibility with qualified Phase 2 schema-1 records,
   while refusing mission finalization with a typed incomplete/schema-skew
   result when required Phase 3 fields cannot be reconstructed honestly.
3. Preserve unknown top-level and namespace fields during updates. Update only
   the adapter-owned documented keys, canonicalize structured metadata, enforce
   existing size/count bounds, and never store credentials, transcripts, tool
   output, or unbounded evidence.
4. Treat readable acceptance prose as presentation. Structured criterion IDs,
   verifier kinds, required flags, bounded metadata/evidence hints, and write
   scopes come from namespaced JSON; never parse human prose back into authority.
5. Expose complete catalog enumeration or mapped-work observation through the
   provider-neutral interface introduced by WP04. Enforce unique
   `(ProjectID, WorkID)` and `(ProjectID, provider, externalID)` mappings;
   missing, duplicate, rebound, cross-project, or unsupported-schema entries
   fail rather than being silently skipped or rewritten.
6. Preserve defensive copying and stable diagnostics. Observation values must
   not expose the adapter's JSON buffers or maps to caller mutation.

**Files**:

- `internal/tracker/beads/catalog.go` — complete mapped-work projection and
  provider-neutral enumeration/observation composition.
- `internal/tracker/beads/metadata.go` — compatible structured mission-method
  metadata decode/merge/encode.
- `internal/tracker/beads/catalog_test.go` — catalog projection, mapping,
  completeness, and mutation-alias tests.
- `internal/tracker/beads/metadata_test.go` — schema compatibility, unknown-field
  preservation, bounds, provenance, scopes, and strict failure cases.

**Validation**:

- Two mapped issues round-trip all neutral semantic fields without reading
  acceptance prose or provider status.
- Unknown metadata survives an owned-field update; conflicting/missing mapping
  and incomplete Phase 3 data fail with stable recovery kinds.
- Phase 2 schema-1 records remain readable for their guaranteed fields and are
  never silently promoted into a complete finalizable observation.
- Searches find no Beads Go import, Dolt SQL, human-output parser, or provider
  status branch in the new path.

### Subtask T058: Compose blocking graph and honest observation revisions

**Purpose**: Return a complete, deterministic work graph whose dependency
direction and revision evidence remain correct under ordering and timestamp
adversaries.

**Steps**:

1. In `graph.go`, join mapped catalog observations with the existing blocking
   dependency projection. Preserve the contract: `BlockedWorkID` depends on
   `BlockingWorkID`. Ignore non-blocking provider relationships for executable
   graph purposes without hiding malformed or cross-project mapped edges.
2. Canonically sort work by WorkID and dependencies by blocked/blocking WorkID;
   deduplicate only semantically identical edges. Provider issue/list order,
   dependency response order, external ID order, and JSON object-key order must
   not affect normalized observation content.
3. Retain the complete content-derived `ProviderObservation` identity over all
   recovery-relevant known fields. Equal `updated_at` values with changed title,
   description, design, criteria, metadata, scope, mapping, or dependency must
   produce a different opaque revision/digest.
4. Distinguish provider observation from semantic executable identity. Reordered
   presentation and equivalent metadata yield the same normalized work graph;
   provider revision/token/timestamp remain pinned audit evidence and are never
   interpreted as ordering, CAS, or readiness.
5. Add first-observation/second-recheck fixtures used by WP05 finalization:
   unchanged observations match exactly; stale, omitted, added, rebound, or
   content-changed observations refuse publication even if timestamps match.
6. Keep `ReadyProjection` diagnostic-only. Tests must prove changing Beads
   status/readiness alone cannot make unready work executable or advance the
   mission.

**Files**:

- `internal/tracker/beads/graph.go` — complete neutral graph observation and
  canonical dependency composition.
- `internal/tracker/beads/graph_test.go` — ordering, direction, duplicate,
  equal-timestamp, recheck, and provider-readiness tests.
- `internal/tracker/beads/catalog_test.go` — content-derived revision coverage
  for newly projected semantic fields.

**Validation**:

- One hundred work/dependency/order permutations produce one normalized
  semantic graph and deterministic observation evidence.
- Every relevant content mutation with an unchanged provider timestamp changes
  the provider observation; pure ordering changes do not change semantics.
- Reversed, dangling, cross-project, duplicate-mapping, and rebound dependency
  cases fail with exact typed diagnostics.
- Provider status/readiness changes produce zero mission, WorkRevision, or
  acceptance authority.

### Subtask T059: Run the full two-item mission against isolated real Beads

**Purpose**: Prove that the qualified provider can support the same mission
method as the fake through finalization, drift, reconciliation, and projection.

**Steps**:

1. Add `internal/tracker/beads/mission_method_integration_test.go` for focused
   adapter composition and `internal/scenario/mission_method_beads_test.go` for
   the full WP10 scenario. Reuse the existing real-Beads fixture, pinned binary
   digest/version check, environment sanitizer, bounded runner, and evidence
   conventions rather than creating a second harness.
2. Gate real-provider execution on the established explicit qualification
   environment. Create a unique temporary canonical Beads workspace outside the
   source checkout, initialize non-interactively, disable hooks/auto-sync/event
   flush/metrics as already qualified, and clean only exact test-owned paths.
3. Execute the complete two-item flow: initialized mission, approved spec/plan,
   create two stable mapped work definitions, add one blocking dependency,
   observe/validate, finalize generation 1, mutate one semantic field directly
   through supported provider tooling, detect drift, explicitly reconcile to
   generation 2, and regenerate the tasks projection.
   Drive finalization and reconciliation through the accepted mission services
   and a real SQLite control store, then query the persisted generation pointer
   and render from the read model. Calling pure candidate constructors or
   manually seeding database rows does not qualify this end-to-end path.
4. Assert every local transition uses Spec Kitty state and exact revisions.
   Changing Beads status, readiness, assignee, label, or comment must not
   advance a step, approve work, choose a disposition, or create a generation.
5. Capture reproducible bounded evidence: source commit/tree, exact test
   selection, Beads version and binary SHA-256, protocol/schema, workspace mode,
   final graph digests, drift kind, reconciliation identity, timing summary,
   and redacted result. WP12 owns the frozen aggregate evidence files.
6. Keep the scenario offline after the pinned binary is present and ensure the
   normal fake-provider test suite still runs when Beads is absent.
7. Preserve the accepted Phase 2 qualification runner and its exact 11-scenario
   evidence schema. WP11 exposes one exact, explicitly enabled real-provider
   mission-method test selector and qualifies that selector inside the same
   pinned offline containment boundary. WP12 owns registering it in the new
   Phase 3 campaign and publishing aggregate Phase 3 evidence; do not silently
   widen or relabel the historical Phase 2 campaign.

**Files**:

- `internal/tracker/beads/mission_method_integration_test.go` — isolated real
  adapter mission-method qualification and evidence helpers.
- `internal/scenario/mission_method_beads_test.go` — full two-item vertical
  scenario using WP10's provider-neutral harness.

**Validation**:

- The explicitly enabled real Beads scenario completes both generations and
  projection with no source-checkout mutation or network dependency.
- Disabling/removing Beads skips only the explicitly real-provider profile; all
  portable mission/fake tests continue to run.
- No test parses human output, uses provider state as mission authority, or
  leaves a workspace/branch/process behind.
- Evidence identifies exact source and provider inputs without credentials,
  raw environment, full command streams, or private work prose.
- The direct WP11 real-provider selector covers both generations, drift,
  reconciliation, and projection. A passing historical Phase 2 campaign alone
  is not evidence for T059.

### Subtask T060: Prove fake parity and adversarial provider honesty

**Purpose**: Demonstrate that Beads is one bounded implementation of the work
contract, including when it is slow, unavailable, stale, or wrong.

**Steps**:

1. Feed equivalent two-item work definitions through the durable fake and Beads
   adapters. Compare normalized WorkIDs, descriptions/design context, structured
   criteria, dependencies, scopes, lane graph, coverage, and semantic
   WorkRevision digest. Do not require provider/external IDs, opaque revisions,
   timestamps, tokens, or canonical provider evidence bytes to match.
2. Assert provider-specific observations remain present and attributable in
   each WorkRevision. Semantic parity must not erase the evidence needed for
   double-observation finalization or later observation-only drift.
3. Use the existing dishonest JSON command harness to inject slow response,
   unavailable executable/provider, timeout, malformed envelope, stale revision,
   equal-timestamp changed content, wrong WorkID/project/external mapping,
   omitted/additional work, reversed dependency, and unsupported metadata
   schema. Do not weaken the decoder or add prose fallbacks to make tests pass.
4. For every fault, assert stable error kind/recovery guidance and zero local
   finalization, generation-pointer movement, mission advancement, or projection
   authority. After a recoverable outage, a clean retry must observe afresh
   rather than trust cached provider content.
5. Exercise the bounded revision recheck window: mutate between the first and
   second observations and require refusal; mutate after successful publication
   and require explicit drift rather than retroactive failure or silent change.
6. Carry universal local authority assertions into any fixture that includes
   execution-era state, while recognizing that Phase 3 itself allocates no real
   leases/workspaces. Provider faults must never weaken Phase 1 laws.
7. Run package/scenario race tests, the JSON-only source guard, and both fake and
   real provider selections. Record T057–T060 only after all applicable gates
   pass or a real-provider skip is explicitly reported as non-qualification.
8. Prove operation-scoped capability selection and consumer-owned root replay:
   create-only unguarded support succeeds; guarded updates or required blocking
   graphs fail before reservation/provider effects when unsupported; exact
   same-digest reservations converge across reopen and two store handles;
   different-digest and cross-namespace collisions fail before provider calls;
   store unavailability and fresh-root/child-replay contradictions fail closed.

**Files**:

- `internal/tracker/beads/mission_method_integration_test.go` — fake/Beads
  semantic comparison plus dishonest-protocol matrix.
- `internal/scenario/mission_method_beads_test.go` — mission-level authority,
  recheck, drift, outage/recovery, and evidence assertions.
- Existing owned catalog/metadata/graph tests — focused regression cases where
  the behavior is local to those files.
- `internal/tracker/work_definition.go` and `_test.go` — operation-scoped
  capability requirements with strict full-port conformance retained.
- `internal/mission/decomposition.go` and `_test.go` — complete request identity,
  consumer-owned reservation ordering, resume semantics, and impossible-state
  refusal.
- `internal/controlstore/sqlite/decomposition.go` and `_test.go` — immutable
  global OperationID reservation and concurrent/restart collision proof using
  the existing schema.
- `internal/helpercmd/mission_method.go` and `_test.go` — compose SQLite as the
  root operation authority while keeping tracker effects outside its transaction.

**Validation**:

```bash
go test ./internal/tracker/beads
go test -race ./internal/tracker/beads
go test ./internal/scenario -run 'MissionMethod(Beads|ProviderParity)'
go test ./internal/tracker ./internal/mission ./internal/controlstore/sqlite ./internal/helpercmd
go test ./internal/tracker/fake ./internal/tracker/conformance ./internal/...
go test -race ./internal/mission ./internal/controlstore/sqlite ./internal/helpercmd
go vet ./internal/tracker/beads ./internal/scenario ./internal/mission ./internal/controlstore/sqlite ./internal/helpercmd
gofmt -d internal/tracker/beads internal/scenario internal/tracker/work_definition.go internal/tracker/work_definition_test.go internal/mission/decomposition.go internal/mission/decomposition_test.go internal/controlstore/sqlite/decomposition.go internal/controlstore/sqlite/decomposition_test.go internal/helpercmd/mission_method.go internal/helpercmd/mission_method_test.go
```

- Equivalent fake and Beads graphs yield one semantic WorkRevision digest while
  retaining distinct complete provider observations.
- Slow, unavailable, malformed, stale, equal-timestamp-wrong, misbound, and
  incomplete providers publish zero generations and advance zero mission state.
- Mutation during recheck refuses; mutation after publication yields drift and
  requires reconciliation.
- Source guards find only typed JSON command/protocol use and no provider
  workflow authority outside the adapter.

## Definition of Done

- Existing Beads catalog, metadata, and graph capabilities project complete
  WP04 provider-neutral work observations without a parallel domain model.
- Structured criteria, design context, provenance, write scopes, mappings, and
  blocking dependencies survive round-trip; human acceptance prose is never
  parsed into authority.
- Provider ordering is normalized, and complete content-derived observation
  revisions detect equal-timestamp changes to every relevant field.
- The isolated pinned Beads qualification completes the same two-item mission
  path as the fake: generation 1, semantic drift, explicit generation 2, and
  regenerated non-authoritative projection.
- Fake and Beads produce equivalent normalized executable content and semantic
  digest while retaining honest distinct provider evidence.
- Slow, unavailable, malformed, stale, wrong, rebound, and incomplete provider
  cases fail closed with stable diagnostics and zero local authority mutation.
- The JSON-only protocol, bounded process, canonical workspace, version/digest,
  offline, and secret-hygiene laws remain intact.
- No Beads status, readiness, assignment, comment, agent identity, Molecule,
  formula, hook, or merge slot gains mission/execution/review authority.
- Portable fake tests do not require Beads; a skipped real profile is reported
  honestly and is not recorded as qualification evidence.
- Create-only decomposition does not demand imaginary CAS. Update/dependency
  capability requirements are derived from the validated proposal set and
  refuse before effects when absent.
- Root operation identity is reserved by the local SQLite authority before any
  provider call. Exact replay can resume; changed semantics, cross-kind reuse,
  store failure, and impossible fresh-root/child-replay states fail closed.
- Record event-sourced completion after applicable validation succeeds:

```bash
spec-kitty agent tasks mark-status T057 --status done
spec-kitty agent tasks mark-status T058 --status done
spec-kitty agent tasks mark-status T059 --status done
spec-kitty agent tasks mark-status T060 --status done
```

## Risks

- **Semantic gravity**: Convenient Beads fields can leak into mission policy.
  Compare against the deliberately neutral fake and reject provider-name branches
  outside the adapter.
- **Incomplete legacy metadata**: Old schema-1 records may lack Phase 3 fields.
  Preserve readable evidence but refuse complete finalization rather than
  fabricate scopes or provenance.
- **Timestamp trust**: Equal timestamps can conceal changed content. Keep the
  complete content-derived observation identity and adversarial tests.
- **False parity**: Matching only happy-path titles can hide criteria,
  dependency, scope, or provenance loss. Compare the whole semantic graph and
  deliberately allow only provider evidence to differ.
- **Protocol weakening**: Real fixtures can tempt prose parsing or shell escape
  hatches. Reuse typed commands and schema-1 envelopes exclusively.
- **Qualification pollution**: Real Beads can mutate source state or leave
  processes/workspaces. Use exact temporary ownership, offline execution,
  bounded cleanup, and before/after source checks.
- **Wrong-provider acceptance**: A provider that returns a valid envelope with
  stale or misbound data is more dangerous than an outage. Test semantic
  identity and mapping, not merely protocol validity.

## Reviewer Guidance

Review the dependency direction first. Mission, workgraph, control-store, and
scenario law must remain provider-neutral; only `internal/tracker/beads` may
know `bd` protocol and namespaced Beads metadata. Reject copied Beads domain
structs outside the decoder, direct Dolt access, shell commands, human-output
parsing, or any provider-status mission branch.

Compare fake and Beads evidence field-by-field. Semantic graph identity must
match for equivalent work, while provider mapping/revision/timestamp evidence
must remain distinct and pinned. Ensure tests cover shuffled order and changed
content under the exact same timestamp; timestamp equality is never freshness.

Trace every adversarial result to the local publication boundary. Slow,
unavailable, malformed, stale, misbound, missing, additional, or wrong provider
state must be refused before WorkRevision publication and mission advancement.
A detected change during double observation refuses; a later change creates
drift and cannot rewrite generation 1.

Finally, inspect the real-provider harness for reproducibility and containment:
explicit enablement, pinned version/digest, isolated canonical workspace,
sanitized environment, bounded commands, no network, exact cleanup, redacted
evidence, and honest skips. Provider parity qualifies Beads as a replaceable
work substrate; it does not promote Beads into Spec Kitty's authority kernel.
