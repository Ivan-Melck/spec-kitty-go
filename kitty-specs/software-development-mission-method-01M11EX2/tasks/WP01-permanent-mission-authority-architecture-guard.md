---
work_package_id: WP01
title: Permanent Mission Authority Architecture Guard
dependencies: []
requirement_refs:
- FR-003
- FR-009
- FR-019
- FR-025
- NFR-008
- C-001
- C-002
- C-003
- C-005
- C-006
- C-007
- C-008
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T001
- T002
- T003
- T004
- T005
history: []
agent_profile: paula-patterns
authoritative_surface: internal/architecture/
create_intent: []
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/graph.go
- internal/architecture/graph_test.go
- internal/architecture/policy.go
- internal/architecture/policy_test.go
- internal/architecture/reachability.go
- internal/architecture/reachability_test.go
role: architecture-scout
tags: []
tracker_refs: []
---

# Work Package Prompt: WP01 - Permanent Mission Authority Architecture Guard

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `paula-patterns`
- **Role**: `architecture-scout`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Extend the permanent Go import-graph and production-reachability guard so Phase 3's mission method can be implemented without weakening the authority boundaries qualified in Phases 1 and 2.

The completed guard must classify the new mission, artifact, mission-type, and projection package roots deliberately; enforce directional dependency rules that keep authority in the kernel; continue rejecting Markdown authority and every deferred Phase 4–6 surface; and register the Phase 3 authority entry points that must remain reachable from real production callers. Every new allowance or prohibition must have a corresponding accepting control and a mutant that proves the rule is load-bearing.

Implementation command:

```bash
spec-kitty agent action implement WP01 --agent codex
```

## Context

Phase 1 replaced a brittle flat source allowlist with the closed policy in `internal/architecture`. Phase 2 evolved that policy into a layered tracker boundary: the provider-neutral tracker contract is visible to authority packages, while fake and Beads implementations remain adapter details. This package must preserve that enforcement model as Phase 3 introduces four earned capability roots:

- `internal/mission` owns lifecycle authority, decisions, decomposition, finalization, reconciliation, and pure next-result selection;
- `internal/artifact` owns typed immutable artifact revisions, validation, and exact-revision approval;
- `internal/missiontype/softwaredev` provides a statically registered mission definition;
- `internal/projection` renders read-only compatibility outputs such as `tasks.md`.

The tracker remains the mutable catalog before finalization. It must not advance mission steps, approve artifacts, publish a `WorkRevision`, choose a reconciliation disposition, or mint downstream execution authority. The projection package is even narrower: it may consume typed read models and render output, but no authority package may consume it and no Markdown file may become an input to mission state.

The current policy already provides typed tier and rule identities, slash-bounded package matching, structured `go list` graph loading, deterministic violations, forbidden deferred surfaces, production-call reachability, and source analysis for Python and Markdown authority. Extend these facilities in place. Do not replace them with source-name grep, a second policy engine, or a new flat allowlist.

This WP is deliberately first and dependency-free. Later WPs will create the packages named above and rely on this guard to reject architectural shortcuts while they work concurrently. The policy may therefore classify package roots that are not present yet, but the live graph must remain closed: an unclassified production root must still fail as unknown.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated to WP01. Because this package has no dependencies, base the lane on the planning branch `feat/software-development-mission-method`. The accepted result merges back into the same branch.

Do not edit the primary checkout, create another worktree manually, merge or rebase another lane, push directly to `main`, or broaden ownership beyond the six files listed in frontmatter. Other agents are working in the same repository. Preserve their edits and coordinate through the mission rather than changing their files.

## Architectural Laws to Preserve

1. Every production package loaded from the main module has exactly one tier classification.
2. Unknown or ambiguously classified production packages fail with stable diagnostics.
3. Exact patterns remain exact; prefix patterns include only their root and slash-bounded descendants.
4. Authority code may depend on typed foundation, other authority capabilities, and provider-neutral ports.
5. Authority code may not depend on concrete tracker, database, Git, projection, quality, or transport adapters.
6. Provider-specific semantics remain behind the tracker adapter tier.
7. Composition roots may wire capabilities but may not redefine mission law.
8. Test-only imports never establish a permitted production dependency.
9. Markdown, Python runtime, API/HTTP, MCP, Team Kitty, and Orchestrator API authority surfaces remain deferred.
10. Existing Phase 1 lease, workspace, reviewer-independence, handoff, and integration boundaries remain unchanged.
11. Registered authority guards and constructors require resolved, real production callers; spelling a matching symbol in tests or shadowed code is insufficient.
12. Policy evaluation and diagnostics are deterministic across repeated runs.

## Required Direction Model

The implementation may reuse existing tier names or introduce a narrowly justified read-only tier, but it must encode this meaning:

```text
composition root -> mission authority -> artifact/workgraph/provider contracts
composition root -> projection
projection       -> typed read models only
mission authority -> provider-neutral tracker contract
mission authority -X-> tracker adapters
mission authority -X-> projection
projection         -X-> mission mutations or authority constructors
tracker adapters   -X-> mission authority
deferred transports -X-> production graph in Phase 3
```

If `internal/missiontype/softwaredev` is classified separately from `internal/mission`, its permitted direction must reflect static definition registration without granting adapter or transport access. Do not introduce a general workflow-language tier.

### Subtask T001: Classify Phase 3 packages in the permanent tier model

**Purpose**: Give every planned Phase 3 production root an explicit, reviewable architectural home before dependent WPs create code beneath it.

**Steps**:

1. Read the entire existing `DefaultPolicy`, validation logic, and classification tests before changing policy data.
2. Add exact or slash-bounded classifications for `internal/mission`, `internal/artifact`, `internal/missiontype/softwaredev`, and `internal/projection`.
3. Classify mission and artifact as authority capabilities unless a more restrictive existing tier is demonstrably appropriate.
4. Classify the static software-development definition so it may depend on closed mission contracts but cannot acquire adapter, projection, or transport authority.
5. Model projection as read-only presentation. Prefer a distinct tier if reusing `TierAdapter` would grant provider-specific or authority-facing permissions that projection must not have.
6. If a new tier is introduced, extend `TierName`, `RuleID` validation only if actually necessary, `Policy.Validate`, and allowed-target validation coherently.
7. Preserve exact classification of `internal/tracker`; never turn it into a prefix that absorbs provider adapters.
8. Add table-driven acceptance assertions for each new root and representative slash-bounded descendants.
9. Add look-alike controls such as `internal/missionary`, `internal/artifacts`, and `internal/projections` so raw textual prefixes cannot pass.
10. Prove every existing package classification remains unchanged.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**:

- Each planned package resolves to exactly one intended tier.
- Each look-alike remains unknown.
- The live repository graph continues to pass before downstream packages are added.
- Repeated classification returns identical typed diagnostics.

### Subtask T002: Enforce forbidden dependency directions around mission authority

**Purpose**: Prevent the new packages from collapsing the separation between mission law, mutable work providers, compatibility presentation, and deferred execution surfaces.

**Steps**:

1. Add focused synthetic graph cases for permitted mission-to-foundation, mission-to-artifact, mission-to-workgraph, and mission-to-provider-contract edges.
2. Add rejecting mutants for mission-to-Beads, mission-to-fake tracker, mission-to-SQLite, mission-to-Git adapter, and mission-to-projection edges.
3. Add rejecting mutants for artifact-to-adapter and artifact-to-projection edges.
4. Add rejecting mutants for tracker adapter-to-mission and projection-to-mission authority edges.
5. Prove the software-development definition can depend only on its intended mission contracts and typed foundation.
6. Ensure authority packages cannot import `internal/missiontype/softwaredev` merely to recover concrete workflow policy when they should consume registered contracts.
7. Assert the exact `RuleID`, source package, source tier, target package, and target tier for every mutant.
8. Keep fixtures non-vacuous: they must fail for direction, never because a package is accidentally unknown or ambiguous.
9. Include an accepting sibling fixture beside each rejecting class so the guard does not prohibit legitimate provider-neutral collaboration.
10. Evaluate mutant tables repeatedly and compare diagnostic order.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**:

- The allowed direction matrix passes exactly.
- Every forbidden direction produces one intended violation in one-edge fixtures.
- Removing or widening any new target allowance makes at least one focused control red.
- No provider-specific package becomes visible to mission or artifact authority.

### Subtask T003: Preserve Markdown and deferred-surface prohibition

**Purpose**: Ensure the compatibility `tasks.md` renderer cannot recreate the Markdown task ledger as a second source of authority, while retaining all previously deferred product boundaries.

**Steps**:

1. Preserve every existing forbidden production package and import prefix for Python, Markdown libraries, HTTP/API, MCP, Team Kitty, Orchestrator API, and deferred Beads workflow concepts.
2. Extend policy data only where the planned `internal/projection` package requires a narrow allowance; do not admit `internal/markdown` or `cmd/markdown` as alternative package roots.
3. Preserve `CheckDeferredRuntimeSurfaces` behavior that resolves production file reads of Markdown authority through Go types and constant propagation.
4. Add Phase 3-shaped Markdown mutants that attempt to read `tasks.md`, derive mission state from it, or route it into an authority constructor.
5. Test direct literals, constant aliases, local aliases, dot imports, helper-returned paths, and supported path joins where the existing scanner claims coverage.
6. Keep ordinary Markdown prose or renderer output writes legal when they do not feed authority.
7. Prove test-only Markdown readers do not count as production authority.
8. Retain bounded, typed source-load failures and caller cancellation in the Go-tooling loader.
9. Document any genuinely dynamic path expression outside static proof in code comments; do not silently treat a statically knowable alias as safe.
10. Require `RuleDeferredSurface` diagnostics to name the production source and a stable `markdown:<path>` or deferred target.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`
- `internal/architecture/reachability.go`
- `internal/architecture/reachability_test.go`

**Validation**:

- A projection that writes `tasks.md` from typed state is structurally admissible.
- A production path that reads `tasks.md` as authority is rejected.
- Existing Python and transport mutants remain red.
- Test-only and prose-only controls remain accepted.

### Subtask T004: Extend production reachability policy for Phase 3 authority seams

**Purpose**: Prevent authority checks from existing only in unit tests or unused packages while downstream code appears complete.

**Steps**:

1. Identify the smallest Phase 3 authority entry-point registry that can be named before downstream implementations land.
2. Prefer stable constructors or guards owned by mission/artifact contracts; do not register incidental helpers, concrete adapter methods, or future Phase 4–6 surfaces.
3. Design registry entries so missing declarations are distinguishable from declarations with no eligible production caller.
4. Preserve object-resolved call detection: shadowed identifiers, unrelated same-named methods, self-recursion, and unrooted mutual recursion cannot satisfy reachability.
5. Preserve active build-tag and platform filtering through Go tooling.
6. Keep test callers excluded from production reachability.
7. If downstream symbols do not yet exist, represent their required registrations as policy data or tests that become active when the package appears; do not make this dependency-free WP fail merely because later WP files are absent.
8. Add a control proving each existing registered Phase 1 target remains present and production-reachable.
9. Add mutants for missing Phase 3 registration, missing target, test-only caller, shadowed call, and sole-production-caller removal.
10. Preserve bounded command execution, typed load errors, deterministic ordering, and cancellation propagation.

**Files**:

- `internal/architecture/graph.go`
- `internal/architecture/graph_test.go`
- `internal/architecture/reachability.go`
- `internal/architecture/reachability_test.go`

**Validation**:

- Existing live targets pass their production-caller census.
- Phase 3 registrations are explicit without creating false dependencies on absent packages.
- Every reachability mutant fails with `RuleReachability` or `RuleReachabilityTarget` as intended.
- Parse, type-check, command, timeout, output-limit, and empty-graph failures remain typed and bounded.

### Subtask T005: Add load-bearing mutant controls for every policy axis

**Purpose**: Prove the permanent guard itself cannot be casually weakened during later work-package delivery.

**Steps**:

1. Build one consolidated table or a small set of existing top-level tests covering tier classification, local direction, external direction, deferred packages, Markdown authority, and reachability.
2. Include at least one accepting real-graph or production-shaped control for every rejecting class.
3. Mutate one policy fact at a time: remove a package classification, widen an allowed target, remove a forbidden prefix, omit a reachability registration, or remove the sole production caller.
4. Assert each mutant is killed by the intended stable rule rather than any incidental compile or fixture error.
5. Repeat mutation evaluation at least ten times without sleeps or timing floors.
6. Preserve top-level architecture-test census assumptions where campaign tooling depends on exact names; add cases as subtests when possible.
7. Keep temporary source fixtures isolated from the network with dedicated Go build and module caches.
8. Exercise the actual `LoadGraph`, `Policy.Check`, `CheckReachability`, and `CheckDeferredRuntimeSurfaces` paths; literal-only assertion fixtures are insufficient.
9. Run the current production graph through the complete policy after all changes.
10. Verify the owned-file diff contains no broad exemption, provider-name grep, transport implementation, Markdown parser, or future execution package.

**Files**:

- `internal/architecture/graph_test.go`
- `internal/architecture/policy_test.go`
- `internal/architecture/reachability_test.go`

**Validation**:

- Each new architectural permission has a positive fixture.
- Each new boundary has a mutant that survives if the boundary is removed.
- Repeated results and diagnostic ordering are stable.
- The current repository remains green under the closed policy.

## Implementation Discipline

Treat this as an evolution of a qualified policy, not a rewrite. Prefer existing `Tier`, `PackagePattern`, `Policy`, `Violation`, `ReachabilityTarget`, and typed loader mechanisms. A new tier is justified only when existing permissions would overgrant capability.

Do not classify packages by comments, filenames, arbitrary substrings, or provider terminology. Do not infer semantic authority from words such as mission, status, or task. The enforceable boundary here is production packages, imports, resolved calls, and statically knowable authority-file reads.

Do not add a dependency, production package, composition command, generated artifact, or campaign manifest. All six owned files already exist, and `create_intent` is intentionally empty. Test fixtures must live inside owned `_test.go` files and temporary directories.

## Test Strategy

Use a focused loop while evolving policy data:

```bash
go test ./internal/architecture -run 'TestCurrentProductionGraphPassesClosedPolicy|TestPolicy|TestPackagePatterns' -count=1
```

Exercise reachability and deferred-surface analysis separately:

```bash
go test ./internal/architecture -run 'TestReachability|TestDefaultReachability|TestRemovingEachRegistered|TestDeferredRuntime' -count=1
```

Run mutant-sensitive cases repeatedly:

```bash
go test ./internal/architecture -count=10
```

Then run the owned package with race detection and repository compilation:

```bash
go test -race ./internal/architecture -count=1
go test ./... -run '^$' -count=1
go vet ./internal/architecture
go mod verify
gofmt -d internal/architecture/graph.go internal/architecture/graph_test.go internal/architecture/policy.go internal/architecture/policy_test.go internal/architecture/reachability.go internal/architecture/reachability_test.go
git diff --check
git status --short
```

Do not add sleeps, minimum timing assertions, or network-dependent fixtures. A mutation test proves semantic rejection, not wall-clock duration.

## Definition of Done

- The four Phase 3 package roots have exact, non-overlapping classifications.
- Mission and artifact authority can reach only typed foundation, authority, and provider-neutral contracts required by the plan.
- Concrete tracker, control-store, Git, projection, quality, and transport packages cannot leak into mission authority.
- Projection is structurally read-only and cannot be imported by mission authority as a state source.
- A typed-state-to-`tasks.md` output path is allowed, while production Markdown-as-authority reads fail.
- Existing Python runtime, HTTP/API, MCP, Team Kitty, Orchestrator API, and deferred Beads prohibitions remain intact.
- Existing Phase 1 authority and adapter boundaries remain unchanged.
- Phase 3 reachability policy is explicit and does not rely on nonexistent downstream implementations to pass prematurely.
- Missing, test-only, shadowed, recursively isolated, and caller-removed authority targets fail with stable typed rules.
- All new allowances and prohibitions have load-bearing accepting and mutant controls.
- The live production graph passes the closed policy.
- Focused tests, ten repeated architecture runs, race detection, repository compilation, vet, module verification, formatting, and diff hygiene pass.
- Only the six frontmatter-owned files are changed by implementation.
- T001 through T005 are recorded through the event-sourced status command:

  ```bash
  spec-kitty agent tasks mark-status T001 T002 T003 T004 T005 --status done --mission software-development-mission-method-01M11EX2 --json
  ```

## Risks

- **Tier overreach**: Reusing the adapter tier for projection may grant provider imports or authority-facing dependencies. Introduce a narrow tier if reuse cannot express read-only presentation.
- **Authority inversion**: Allowing tracker adapters or projection to import mission mutation services would let external observations become workflow authority.
- **Prefix collision**: A broad `internal/mission` or `internal/tracker` textual prefix may absorb unrelated or provider-specific packages. Use slash-bounded patterns and look-alike controls.
- **Premature reachability**: Registering symbols that downstream WPs have not created can block every lane. Encode forward policy without fabricating declarations or weakening missing-target checks.
- **Markdown dual authority**: Allowing renderer output does not permit reading `tasks.md` back into mission state. Keep output and authority-input paths distinguishable.
- **Deferred-surface erosion**: A convenient HTTP, MCP, Team Kitty, Orchestrator API, Python, or general Markdown dependency would silently pull later phases forward.
- **Vacuous mutants**: Unknown-package or compile failures can disguise a missing direction rule. Assert exact rules and edges.
- **Synthetic confidence**: Hand-assembled graphs alone can omit real imports. Retain live `go list` graph coverage and production-shaped source fixtures.
- **Census drift**: New top-level tests can invalidate exact campaign manifests. Prefer subtests unless a deliberate later campaign update owns the census.
- **Cross-lane edits**: Downstream packages are owned elsewhere. Do not create placeholders or repair their code from WP01.

## Reviewer Guidance

Approve only if the change strengthens the existing closed policy while admitting exactly the Phase 3 capabilities described by the plan.

Verify explicitly:

1. Mission, artifact, software-development mission type, and projection packages classify exactly once.
2. Look-alike paths do not inherit a classification accidentally.
3. The provider-neutral tracker contract remains exact, and Beads remains an adapter.
4. Permitted mission dependencies pass while mission-to-adapter and mission-to-projection mutants fail for direction.
5. Projection cannot call mission mutation or authority-construction seams.
6. Writing a derived `tasks.md` remains possible, but reading Markdown to derive authority is rejected.
7. No deferred Phase 4–6 package/import/runtime prohibition was removed.
8. Existing Phase 1 reachability targets remain present and production-reachable.
9. Any Phase 3 reachability registration is stable, minimal, and compatible with dependency ordering.
10. Shadowed calls, test-only calls, recursion-only calls, and removed callers cannot satisfy reachability.
11. Mutants traverse production policy functions and fail with exact typed diagnostics.
12. The live graph, repeated tests, race detector, compilation, vet, formatting, and hygiene checks pass.
13. The implementation diff is confined to the six owned architecture files.

Reject a solution that adds a flat allowlist, broad unknown-package exemption, repository-wide word grep, provider-specific import in authority code, projection-to-authority dependency, Markdown parser, deferred transport/runtime, fake reachability declaration, test-only production caller, non-deterministic diagnostic, or mutation fixture that fails for the wrong reason.
