---
work_package_id: WP10
title: Portable Two-Item Mission Vertical Slice
dependencies:
- WP04
- WP08
- WP09
requirement_refs:
- FR-001
- FR-003
- FR-006
- FR-007
- FR-008
- FR-009
- FR-013
- FR-016
- FR-017
- FR-018
- FR-021
- FR-022
- FR-023
- FR-025
- FR-026
- FR-027
- FR-028
- NFR-006
- NFR-013
- NFR-014
- C-001
- C-002
- C-005
- C-006
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T052
- T053
- T054
- T055
- T056
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/helpercmd/
create_intent:
- internal/helpercmd/mission_method.go
- internal/helpercmd/mission_method_test.go
- internal/helpercmd/artifact.go
- internal/helpercmd/artifact_test.go
- internal/helpercmd/projection.go
- internal/helpercmd/projection_test.go
- internal/scenario/mission_method_fake_test.go
- internal/scenario/mission_method_helpers_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/helpercmd/root.go
- internal/helpercmd/mission_method.go
- internal/helpercmd/mission_method_test.go
- internal/helpercmd/artifact.go
- internal/helpercmd/artifact_test.go
- internal/helpercmd/projection.go
- internal/helpercmd/projection_test.go
- cmd/spk-phase1-helper/main.go
- internal/scenario/mission_method_fake_test.go
- internal/scenario/mission_method_helpers_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP10 - Portable Two-Item Mission Vertical Slice

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Make the complete Phase 3 mission method executable through the existing internal qualification helper and one durable fake-provider scenario.

The vertical slice must initialize an explicit software-development mission; publish, validate, and approve exact specification and plan revisions; decompose two dependent tracker work definitions; finalize immutable generation 1; return pure typed `next`; render a non-authoritative tasks projection; observe semantic tracker drift; persist an explicit mixed reconciliation; atomically publish generation 2; and regenerate outputs that reference the successor while preserving generation 1 evidence.

Implementation command:

```bash
spec-kitty agent action implement WP10 --agent codex
```

## Context

WP04 provides provider-neutral decomposition, quality/coverage/graph validation, and durable fake work definitions. WP08 provides the transactional SQLite implementation for mission, artifact, WorkRevision v2, drift/reconciliation, and action records. WP09 provides the one-way projection read model, renderer, and atomic file replacement. Rebase onto their accepted lineage and compose their actual public contracts rather than recreating domain behavior inside the helper.

This binary is an internal proving surface retained from Phase 1. Its Phase 3 commands demonstrate that the application envelopes in `contracts/mission-method-commands.md` can be projected cleanly and that the future Orchestrator/API layer will not need to parse terminal prose. It is not a polished public CLI. Do not add a server, authentication flow, MCP, Team Kitty, general configuration system, plugin system, or compatibility promise for human-formatted output.

The proving graph is intentionally small and load-bearing:

```text
work-a: define the contract
work-b: exercise the contract
work-b depends on work-a
```

Each item needs stable `WorkID`, standalone objective/context, structured criteria, write scope, approved plan/spec traceability, and a complete provider observation. After generation 1, change work-a's criterion and make work-b unnecessary. Reconciliation must explicitly continue or replace the changed contract as permitted by the domain law and invalidate work-b, with every affected item covered exactly once. Use the exact legal dispositions enforced by WP06; do not hard-code an outcome that its validator rejects.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated to WP10. Verify accepted WP04, WP08, and WP09 lineages exist on the dependency-resolved base. The accepted result merges back into `feat/software-development-mission-method`.

Do not edit the primary checkout, create worktrees manually, merge another lane, push to `main`, or modify files outside the ten owned paths. WP11 owns Beads parity. WP12 owns CI campaigns and evidence documents.

## Qualification Surface Laws

1. Helper commands validate and translate inputs, compose domain services, and encode typed results; they do not own mission law.
2. Every mutating command has a distinct operation ID; pure queries do not.
3. Correlation IDs provide observability and never substitute for idempotency identity.
4. Success and refusal envelopes carry schema version, typed kind, aggregate identities, retry safety, and bounded recovery data.
5. Human-readable messages are explanatory only and are never parsed by scenarios or other code.
6. The helper opens real durable stores and closes them on every path.
7. The fake tracker is the portable reference provider; no network or Beads installation is required.
8. Tracker status, assignment, and projected Markdown never advance mission state.
9. Querying `next` or drift never performs an unadvertised local mutation.
10. Phase 4 execution/worktrees and Phase 5 review/integration are not composed here.

### Subtask T052: Add typed helper composition and command envelopes

**Purpose**: Extend the existing helper binary with a qualification-only Phase 3 command tree that faithfully projects application types.

**Steps**:

1. Extend `Run` with the nested roots `mission`, `artifact`, `work`, and `projection` while preserving all Phase 1 and Phase 2 commands.
2. Support the application command/query names from the contract: `mission initialize`, `mission transition`, `mission decompose`, `mission next`, `artifact publish`, `artifact approve`, `work finalize`, `work drift`, `work reconcile`, and `projection tasks`.
3. Define one closed schema-versioned result envelope or a compatible set of typed envelopes carrying correlation ID, optional operation ID, affected aggregate IDs, typed result/diagnostic, retry safety, and recovery categories.
4. Keep operation ID absent from pure `mission next`, external-observation `work drift`, and in-memory render queries unless a separate durable mutation is explicitly requested.
5. Validate flags into typed IDs and closed values before opening a store or calling a provider.
6. Encode all machine results as JSON on stdout. Reserve stderr for flag/usage diagnostics that callers never parse.
7. Preserve exit-code consistency: valid typed domain refusals remain machine-readable results; malformed invocation remains a usage failure.
8. Centralize only transport concerns such as flag parsing and envelope encoding; call existing mission/artifact/workgraph/projection services for behavior.
9. Wire SQLite as the qualified control-store adapter and the durable fake as the default portable tracker without exposing either in domain envelopes.
10. Use dependency injection seams in tests so command parsing and service composition can be verified without global state.

**Files**:

- `internal/helpercmd/root.go`
- `internal/helpercmd/mission_method.go`
- `internal/helpercmd/mission_method_test.go`
- `cmd/spk-phase1-helper/main.go`

**Validation**:

- Every declared command dispatches to the intended typed application service.
- Unknown roots/subcommands and malformed IDs fail before state mutation.
- Existing helper commands retain their behavior.
- Tests decode JSON types; no assertion scrapes human-formatted output.

### Subtask T053: Compose mission and artifact lifecycle commands

**Purpose**: Drive intent through exact approved specification and plan artifacts into eligible decomposition using real durable authority.

**Steps**:

1. Implement `mission initialize` with explicit project, mission, definition key/version, actor, process, operation, time, and sealed governance context.
2. Reject a missing or unsupported definition without silently defaulting to software-development.
3. Implement `artifact publish` for intent/specification/plan using exact content digest, repository-relative source ref, source revision, creator, timestamp, and typed validation result.
4. Implement `artifact approve` against one exact revision with verdict, approver, evidence digest, governance binding, and operation identity.
5. Implement `mission transition` with expected state version, requested successor, exact artifact revision references, actor/governance, and operation identity.
6. Implement `mission decompose` using WP04's command, approved plan gate, fake provider binding, proposed stable work definitions, and operation identity.
7. Do not load spec or plan truth by interpreting Markdown. Files may supply bytes whose digests are committed; typed command input supplies the artifact meaning.
8. Ensure publishing changed plan content creates a new revision and makes the older approval inapplicable rather than editing it.
9. Preserve exact idempotency: same operation plus same request replays; conflicting reuse fails without extra rows or provider mutations.
10. Add command tests for every happy transition and for absent, stale, rejected, wrong-kind, wrong-mission, and invalid-governance refusals.

**Files**:

- `internal/helpercmd/mission_method.go`
- `internal/helpercmd/mission_method_test.go`
- `internal/helpercmd/artifact.go`
- `internal/helpercmd/artifact_test.go`

**Validation**:

- A fresh database reaches decomposed/validated work only through the legal approved sequence.
- Changed artifact bytes invalidate applicability of the previous approval.
- Refusals name stable kinds, observed/expected state where safe, and bounded recovery.
- Tracker work alone cannot cause a mission transition.

### Subtask T054: Compose finalization, drift, reconciliation, next, and projection

**Purpose**: Expose the remaining Phase 3 vertical application contracts without duplicating their authority in command handlers.

**Steps**:

1. Implement `work finalize` with mission, expected current generation, exact plan source commit, provider observations, proposed WorkRevision ID, operation ID, actor, and governance.
2. Re-observe/recheck provider revisions through the domain finalization service and publish generation 1 only when expected generation is zero.
3. Implement pure `mission next` over WP08's consistent snapshot and encode the exact closed `NextResult`.
4. Implement `projection tasks` using WP09's read model and renderer, supporting JSON-contained UTF-8 bytes or an explicitly requested safe atomic output path.
5. Implement `work drift` as a provider-observation query that leaves the current generation and local authoritative state unchanged.
6. If reconciliation requires a persisted drift report, expose that as an explicit idempotent mutation rather than hiding persistence inside the drift query.
7. Implement `work reconcile` with expected old revision, candidate successor, exact persisted drift report, one decision per affected work, rationale, actor, governance, time, and operation ID.
8. Return old/new graph digests, successor generation, decisions, fork ancestry where present, and derived consequences without allocating workspaces or expiring real Phase 4 leases.
9. After reconciliation, query and projection must both reference generation 2 while historical generation 1 and approvals remain inspectable.
10. Add focused tests for replay/conflict, incomplete/duplicate dispositions, stale generation, provider-revision-only noise, unsafe output path, and projection regeneration.

**Files**:

- `internal/helpercmd/mission_method.go`
- `internal/helpercmd/mission_method_test.go`
- `internal/helpercmd/projection.go`
- `internal/helpercmd/projection_test.go`

**Validation**:

- Generation 1 finalization, pure next, drift observation, explicit reconciliation, generation 2, and re-render all use real application services.
- Deleting or editing the output Markdown cannot change subsequent `next` or canonical queries.
- A drift query makes no local authoritative write unless the caller invokes the separate persistence command.

### Subtask T055: Prove the full durable fake two-item scenario

**Purpose**: Demonstrate one working mission rather than a collection of disconnected packages and handler tests.

**Steps**:

1. Create isolated temporary paths for one real SQLite control database, one durable fake tracker store, and one derived tasks output.
2. Drive the helper `Run` surface or the compiled helper process from mission initialization through approved specification and plan.
3. Decompose `work-a` and `work-b`, with `work-b` blocked by `work-a`, distinct bounded write scopes, standalone context, structured criteria, and full approved-scope traceability.
4. Finalize generation 1 and assert no predecessor, exact planning source, complete provider observations, deterministic graph/criteria digests, and an acyclic lane DAG.
5. Query `next` repeatedly and require `work_ready` with generation 1 and the exact canonical WorkIDs, with no durable state delta.
6. Render `tasks.md`; assert the non-authoritative warning, mission state version, revision ID/digest, dependency, criteria, drift state, and render time disclosure.
7. Mutate the fake tracker after finalization by changing work-a's criterion and making work-b unnecessary while leaving generation 1 immutable.
8. Observe semantic drift, persist the report explicitly, and verify affected old/new WorkIDs and field-level changes.
9. Submit a legal mixed reconciliation covering each affected work exactly once, publish generation 2 atomically, and assert the invalidation consequence required by later authority/workspace consumers.
10. Restart by closing and reopening both durable stores between major phases; verify generation, artifact, approval, mapping, drift, decision, and reconciliation evidence survives.
11. Re-query and regenerate projection; require generation 2 while generation 1 remains decodable and auditable.
12. Run the same scenario at least twice with different temporary roots to prove it has no ambient repository or process state dependency.

**Files**:

- `internal/scenario/mission_method_fake_test.go`
- `internal/scenario/mission_method_helpers_test.go`

**Validation**:

- The scenario follows the checked-in `quickstart.md` behavior end to end.
- The durable fake is the only tracker; no network, `bd`, or provider-specific field is required.
- Restart never loses or partially publishes authoritative state.
- Generation 1 is unchanged after drift and remains historical after generation 2.

### Subtask T056: Prove typed refusals, purity, and standalone comprehension

**Purpose**: Attack the vertical slice's authority boundaries and show a fresh consumer can act without hidden transcript context.

**Steps**:

1. Build a table of invalid lifecycle transitions, stale approvals, missing coverage, bad dependency edges, provider ambiguity, generation conflicts, incomplete reconciliation, unsafe projection paths, unknown definitions, and corrupt snapshots.
2. Require every refusal to expose stable kind, affected aggregate identities, observed/expected state where safe, retry safety, and bounded recovery categories.
3. Assert no diagnostic contains credentials, environment values, full provider output, private transcript content, or unrestricted artifact bytes.
4. Snapshot the SQLite database, fake tracker bytes/state, and projection destination before repeated `mission next`; require zero mutation across at least 100 queries.
5. Edit, corrupt, and delete generated `tasks.md`, then prove canonical mission, tracker, approval, WorkRevision, decision, and next results are unchanged.
6. Mutate tracker status/assignment only and prove mission advancement and `next` do not infer authority from them.
7. Decode only public typed helper results and provider-neutral work observations into a fresh-reader summary for each work item.
8. Require that summary to identify objective, dependencies, acceptance criteria, current generation, source plan/revision, and next legal action for 100% of fixtures.
9. Prohibit test access to private package fields, setup-only variables, fixture prose not emitted by the application, or conversational context during comprehension checks.
10. Add a mutant for each omitted public field so the comprehension test becomes red when required context is not exposed.
11. Confirm no human-formatted CLI output is parsed anywhere under `internal/scenario`.
12. Repeat deterministic refusal and purity cases to expose ordering or ambient-clock instability.

**Files**:

- `internal/scenario/mission_method_fake_test.go`
- `internal/scenario/mission_method_helpers_test.go`
- `internal/helpercmd/mission_method_test.go`
- `internal/helpercmd/artifact_test.go`
- `internal/helpercmd/projection_test.go`

**Validation**:

- Every negative case fails for its intended typed reason and leaves no partial state.
- One hundred pure queries perform zero writes.
- Projection damage and tracker status changes have zero authority effect.
- Every work package remains independently understandable through public typed data alone.

## Implementation Discipline

The helper layer may translate flags, open adapters, compose services, and encode envelopes. It must not replicate lifecycle transition tables, artifact freshness, work validation, revision canonicalization, drift classification, reconciliation law, next precedence, or projection formatting. Those belong to the dependent packages.

Prefer small command-specific parsing functions and shared typed envelope helpers over one monolithic switch. Keep output deterministic except for explicitly injected timestamps. Pass clocks and identity sources into services in tests rather than relying on ambient time or random IDs.

Do not add new production files outside this WP, new dependencies, a public CLI package, HTTP server, MCP tool, Team Kitty connector, Beads integration, Gapdb adapter, worktree allocator, reviewer dispatch, or integration loop. The Phase 3 surface is a qualification composition root only.

## Test Strategy

Run helper contract tests first:

```bash
go test ./internal/helpercmd -run 'Mission|Artifact|Work|Projection' -count=10
go test -race ./internal/helpercmd -count=1
```

Run the portable scenario repeatedly and after process/store restart:

```bash
go test ./internal/scenario -run 'MissionMethodFake|MissionMethodHelper' -count=3
go test -race ./internal/scenario -run 'MissionMethodFake|MissionMethodHelper' -count=1
```

Run dependent contracts and repository compilation:

```bash
go test ./internal/mission ./internal/artifact ./internal/workgraph ./internal/projection ./internal/controlstore/sqlite ./internal/tracker/fake -count=1
go test ./... -run '^$' -count=1
go vet ./internal/helpercmd ./internal/scenario ./cmd/spk-phase1-helper
go build ./cmd/spk-phase1-helper
go mod verify
gofmt -d internal/helpercmd/root.go internal/helpercmd/mission_method.go internal/helpercmd/mission_method_test.go internal/helpercmd/artifact.go internal/helpercmd/artifact_test.go internal/helpercmd/projection.go internal/helpercmd/projection_test.go cmd/spk-phase1-helper/main.go internal/scenario/mission_method_fake_test.go internal/scenario/mission_method_helpers_test.go
git diff --check
git status --short
```

All portable tests must run offline with isolated temporary state. Do not add sleeps, timing floors, shared fixed paths, or an ambient user Beads database.

## Definition of Done

- The existing helper exposes all Phase 3 application contracts as typed qualification commands without breaking earlier commands.
- Missing/unsupported mission definitions fail before mutation; no implicit software-development fallback exists.
- Exact specification and plan revisions are published, validated, approved, and checked for freshness.
- The approved plan decomposes into two provider-neutral fake work definitions with one dependency and complete context.
- Generation 1 finalizes with deterministic identity, source, observations, digests, and lane graph.
- Repeated `next` returns exact `work_ready` state and performs no writes.
- The generated tasks document is visibly derived, source-bound, deletable, and never parsed as authority.
- Semantic tracker mutation yields drift without mutating generation 1.
- Complete mixed reconciliation atomically publishes generation 2 and retains all generation 1 evidence.
- Reopened stores reproduce artifacts, approvals, work mappings, revisions, drift, decisions, and reconciliation.
- Typed refusal coverage includes lifecycle, artifact, provider, graph, generation, reconciliation, projection, and corrupt-state cases.
- Standalone comprehension succeeds using only public typed results for every fixture.
- No test parses human-formatted CLI output or relies on private transcript/setup context.
- Focused, repeated, race, compilation, vet, build, formatting, module, and hygiene checks pass.
- Only the ten frontmatter-owned files are modified by implementation.
- T052 through T056 are recorded through the event-sourced status command:

  ```bash
  spec-kitty agent tasks mark-status T052 T053 T054 T055 T056 --status done --mission software-development-mission-method-01M11EX2 --json
  ```

## Risks

- **Helper becomes product law**: Duplicating domain decisions in flag handlers creates a second workflow engine. Compose accepted services only.
- **Envelope drift**: Ad hoc JSON per command will make future transport projection expensive. Preserve common typed laws now.
- **Prose parsing**: Tests that scrape console messages can pass while contracts break. Decode typed JSON exclusively.
- **Hidden mutation**: `next`, drift observation, or projection rendering must not silently persist authority.
- **Fake-only optimism**: The fake must preserve real conflict, ambiguity, restart, and provider-revision behavior even though Beads parity comes later.
- **Scenario theater**: A test that seeds generation rows directly does not prove the lifecycle. Drive public helper/application commands.
- **Projection dual authority**: Editing or deleting `tasks.md` must never affect state; there is no import/apply command.
- **Non-durable evidence**: Keeping drift, decisions, or reconciliation only in fixture variables defeats restart proof.
- **Ambient contamination**: Fixed database paths, current repository state, environment-specific IDs, and shared caches make results irreproducible.
- **Phase expansion**: API polish, remote serving, agent dispatch, worktrees, review, and integration remain later phases.

## Reviewer Guidance

Approve only if the helper is thin and the scenario proves the real domain/persistence stack end to end.

Verify explicitly:

1. Existing Phase 1/2 helper commands still dispatch correctly.
2. All Phase 3 outputs are typed JSON; scenario code parses no human prose.
3. Mutation commands carry operation identity and queries do not manufacture one.
4. Mission type is explicit and artifact approvals bind exact revisions.
5. Decomposition calls the real approved-plan gate and durable fake provider.
6. Finalization rechecks provider observations and publishes through WP08 transactions.
7. `next` uses one durable snapshot and no tracker/projection inference.
8. Drift observation leaves generation 1 current and immutable.
9. Reconciliation covers every affected work exactly once and publishes one successor.
10. Projection identifies its generation/digest and remains disposable.
11. Store restart occurs during the scenario and all historical evidence survives.
12. Refusal assertions name typed reasons and verify no partial mutation.
13. Standalone-comprehension checks use only public outputs and have omission mutants.
14. No Beads, HTTP/API, MCP, Team Kitty, Gapdb, execution, review, or integration surface enters the implementation.
15. The diff is confined to the exact ten owned paths.

Reject a solution that directly seeds final database rows instead of driving commands, parses output prose, defaults the mission type, treats tracker status as workflow state, mutates during `next`, persists drift implicitly during a query, imports generated tasks, recreates domain rules in helper handlers, or builds Phase 6 transport polish into the qualification binary.
