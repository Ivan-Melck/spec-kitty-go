---
work_package_id: WP02
title: Permanent Beads Adapter Architecture Guard
dependencies: []
requirement_refs:
- C-002
- C-008
- FR-021
- NFR-008
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts were generated on feat/beads-tracker-integration. Implement only in the Spec Kitty lane worktree created for WP02, branch from the mission planning base because this package has no dependencies, and merge the accepted lane back into feat/beads-tracker-integration; do not work on or push directly to main.
subtasks:
- T006
- T007
- T008
- T009
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/architecture/
create_intent: []
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/policy.go
- internal/architecture/policy_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP02 - Permanent Beads Adapter Architecture Guard

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Turn Phase 1's deliberately hypothetical Beads-adapter allowance into a permanent, production policy without weakening the kernel boundary that made Phase 1 credible.

The finished policy must admit protocol implementation packages at the exact slash-bounded prefix `internal/tracker/beads`, keep the provider-neutral `internal/tracker` contract unambiguous, and mechanically reject every authority-kernel import of the adapter or a Beads Go package. It must also retain explicit deferral of Beads workflow-authority concepts and preserve the exact architecture-test census.

Implementation command:

```bash
spec-kitty agent action implement WP02 --agent codex
```

## Context

Phase 1 replaced a flat source allowlist with a closed, directional import policy in `internal/architecture`. The policy currently defines:

- `TierProviderContract` for the exact `internal/tracker` package;
- `TierAdapter` for `internal/tracker/fake`, SQLite, and Git adapters;
- `TierAuthority` for execution, handoff, integration, review, workgraph, and workspace packages;
- stable rule IDs for unknown packages, ambiguous classifications, forbidden directions, external imports, and deferred surfaces; and
- an exact top-level test census in the existing architecture campaign.

`TestPhase1SourceScopeMutantControl` already proves a temporary, hand-edited policy can admit an exact `internal/tracker/beads` package while rejecting an execution-to-adapter edge. That is a transition fixture, not the permanent rule. Phase 2 will create nested packages such as `internal/tracker/beads/protocol`; an exact-package entry is insufficient.

The production integration is a subprocess adapter for the documented Beads v1.2.2 JSON envelope. It is not a Go SDK integration. Beads exposes no supported public Go consumer package, and Go's `internal` rule prevents importing its implementation packages. Both the historic module path and the current repository path must remain forbidden Go dependencies:

```text
github.com/steveyegge/beads
github.com/gastownhall/beads
```

The architectural law is directional:

```text
composition root -> Beads adapter -> tracker contract
authority kernel  -> tracker contract
authority kernel  -X-> Beads adapter
any production package -X-> Beads Go module/internal implementation
```

The guard is structural. Do not reintroduce the old case-insensitive repository-wide grep for the word `beads`; the adapter must be allowed to contain protocol names such as `bd`, `BD_JSON_ENVELOPE`, and JSON schema fields. Likewise, do not claim that an import graph can infer arbitrary business meaning from comments or variable names. Enforce C-008 at the package/import boundary here; runtime behavior and capability honesty are proven by later conformance work packages.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated to WP02. This package has no dependencies, so its lane must be based on the mission planning base `feat/beads-tracker-integration`. The accepted result merges back into `feat/beads-tracker-integration`, which is also the mission merge target.

Do not edit the primary checkout, create a worktree manually, merge or rebase another lane, push directly to `main`, or broaden ownership beyond the two files listed in frontmatter. Other agents are working in the same repository; preserve every unrelated change.

## Existing Policy Contract to Preserve

Before editing, read all of `policy.go` and `policy_test.go`, then inspect the current graph and campaign tests that consume them. Preserve these behaviors:

1. Every production package has exactly one tier classification.
2. Exact-package patterns do not absorb descendants.
3. Prefix patterns match their root plus slash-bounded descendants only.
4. Unknown and multiply classified packages fail deterministically.
5. Test-only imports never grant production dependency permission.
6. Authority packages may depend on the provider-neutral tracker contract.
7. Authority packages may not depend on any provider adapter.
8. Provider dependency allowances remain confined to a provider-adapter tier.
9. Deferred Python, Markdown authority, HTTP/API, MCP, Team Kitty, and Orchestrator API surfaces remain rejected.
10. Violations retain exact rule, source package, source tier, target package, and target tier diagnostics.
11. `TestCurrentProductionGraphPassesClosedPolicy` continues to inspect the actual repository rather than a curated package list.
12. The architecture campaign's exact top-level test census remains valid.

Do not change `TierAuthority.AllowedLocalTargets` to solve an adapter-classification problem. Do not classify `internal/tracker` as a prefix. Do not add an unknown-package exemption.

### T006: Classify the permanent slash-bounded Beads adapter prefix

**Purpose**: Allow Phase 2 production packages to exist below `internal/tracker/beads` without allowing the provider-neutral tracker contract to swallow provider code.

**Implementation requirements**:

1. Extend `DefaultPolicy` so the provider-adapter tier contains a `PackagePrefix` rooted at:

   ```text
   <module>/internal/tracker/beads
   ```

2. Keep `<module>/internal/tracker` as an `ExactPackage` in `TierProviderContract`.
3. Do not use `strings.Contains`, an unbounded textual prefix, a filesystem substring, or a generic `internal/tracker` prefix.
4. Prove all of the following classifications explicitly:

   | Package | Expected result |
   |---|---|
   | `internal/tracker` | exactly `TierProviderContract` |
   | `internal/tracker/beads` | exactly `TierAdapter` |
   | `internal/tracker/beads/protocol` | exactly `TierAdapter` |
   | `internal/tracker/beads/protocol/jsonv1` | exactly `TierAdapter` |
   | `internal/tracker/beadsx` | `RuleUnknownPackage` |
   | `internal/tracker/beads-protocol` | `RuleUnknownPackage` |

5. Prove the new prefix does not overlap another tier and cannot produce `RuleAmbiguousPackage` for the root or nested adapter packages.
6. Preserve deterministic classification diagnostics across repeated calls.
7. Keep all existing fake, SQLite, Git, composition, quality, and authority classifications unchanged.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**:

- The current repository graph passes before any Beads production package exists.
- Synthetic root and nested Beads packages classify as adapters.
- Look-alike sibling paths remain unknown.

### T007: Add a positive adapter protocol-knowledge fixture

**Purpose**: Prove the architecture guard admits the implementation shape Phase 2 actually needs, not merely an empty package with the right name.

**Implementation requirements**:

1. Extend the existing top-level accepting policy test rather than adding a new top-level test name.
2. Add a realistic temporary Go fixture or equivalent loaded graph containing:

   ```text
   internal/tracker
   internal/tracker/beads
   internal/tracker/beads/protocol/jsonv1
   ```

3. Let the nested protocol package depend on the provider-neutral tracker contract and standard-library subprocess/JSON packages needed by the intended adapter shape.
4. Include harmless protocol knowledge such as the `BD_JSON_ENVELOPE` environment key, schema version `1`, and the `bd` executable name in fixture source. The test need not execute a subprocess.
5. Load the fixture through the structured Go graph loader when practical, so the accepting result proves real import classification instead of manually omitting inconvenient edges.
6. Use isolated temporary Go cache/module paths if the fixture invokes Go tooling.
7. Assert the whole policy check succeeds and that both adapter packages classify as `TierAdapter`.
8. Assert the exact `internal/tracker` package remains `TierProviderContract`.
9. Do not allow a Beads Go module import to make the positive fixture pass; command-protocol knowledge is allowed, provider implementation dependency is not.

**Non-vacuity requirement**:

The fixture must fail if the new Beads `PackagePrefix` is removed or changed back to `ExactPackage`. A test that calls only `Classify` on the adapter root does not prove nested protocol support.

**Files**: `internal/architecture/policy_test.go`.

**Validation**: The production-shaped fixture passes without adding Beads to `go.mod`, reaching the network, invoking `bd`, or parsing human-formatted output.

### T008: Kill kernel-to-adapter and forbidden-import semantic mutants

**Purpose**: Prove the new allowance cannot be used as a bridge from execution authority into provider protocol or implementation code.

**Implementation requirements**:

1. Extend the existing rejecting-fixture top-level test with table-driven subtests. Do not create uncensused top-level test functions.
2. Include at least these exact mutants:

   | Mutant edge | Required rule |
   |---|---|
   | `internal/execution -> internal/tracker/beads` | `RuleLocalDirection` |
   | `internal/execution -> internal/tracker/beads/protocol/jsonv1` | `RuleLocalDirection` |
   | `internal/review -> internal/tracker/beads` | `RuleLocalDirection` |
   | `internal/integration -> internal/tracker/beads/protocol` | `RuleLocalDirection` |
   | `internal/execution -> github.com/steveyegge/beads/internal/storage` | `RuleDeferredSurface` |
   | `internal/tracker/beads -> github.com/steveyegge/beads/internal/storage` | `RuleDeferredSurface` |
   | `internal/tracker/beads/protocol -> github.com/gastownhall/beads/internal/storage` | `RuleDeferredSurface` |

3. Add both Beads module roots to the globally forbidden import-prefix policy. Match the root and slash-bounded descendants; do not reject unrelated look-alike module names.
4. Reject direct provider imports even from the Beads adapter. This adapter owns the documented command protocol, not Beads implementation code or Dolt SQL.
5. For every mutant, assert exactly one intended violation where the fixture is constructed to contain one bad edge.
6. Assert the precise stable `RuleID`, source package, and target package. When applicable, assert source and target tier identities too.
7. Assert the rendered diagnostic includes the exact rule and edge; a generic nonzero result is insufficient.
8. Repeat evaluation and compare diagnostics to prove stable ordering.
9. Ensure a kernel-to-contract edge remains accepted in a sibling control fixture. The desired rejection is provider direction, not all tracker use.
10. Do not make the mutants fail through compilation errors, missing fixture packages, ambiguous classification, or unknown roots.

**Mutant intent**:

- Removing the Beads import prohibition must make the external-import mutants survive and red the test.
- Adding `TierAdapter` to authority's allowed local targets must make the kernel-to-adapter mutants survive and red the test.
- Reclassifying the Beads prefix as provider contract must either red the acceptance assertions or kill the wrong-rule expectation; it must never silently pass.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**: Every mutation is killed by the named architectural rule and exact edge.

### T009: Preserve deferred Beads workflow concepts and the architecture census

**Purpose**: Admit the ticket substrate without accidentally admitting Beads as a mission, identity, review, or Git authority.

**Implementation requirements**:

1. Retain every existing deferred package and import prohibition. This work package may add narrow Beads rules; it may not delete Phase 1's Python, Markdown, API/HTTP, MCP, Team Kitty, or Orchestrator API rules.
2. Express the C-008 boundary as explicit forbidden package/import shapes, not a repository-wide word ban.
3. Add table-driven rejecting fixtures for the following provider-workflow subtrees beneath the otherwise allowed adapter prefix:

   ```text
   internal/tracker/beads/molecules
   internal/tracker/beads/formulas
   internal/tracker/beads/agent-identity
   internal/tracker/beads/merge-slots
   internal/tracker/beads/custom-mission-status
   internal/tracker/beads/compaction
   internal/tracker/beads/git-hooks
   ```

4. Each forbidden subtree and its slash-bounded descendants must produce `RuleDeferredSurface`, with the package itself named as source and target.
5. Add a look-alike control where useful so narrow deferrals cannot become loose substring matching.
6. Do not prohibit ordinary provider-neutral words such as issue, dependency, comment, assignment, status observation, or readiness diagnostic; those are part of the approved ticket substrate.
7. Do not claim to detect a concept merely because its name appears in a comment, test description, or protocol payload. This guard owns production package/import structure.
8. Preserve all existing top-level architecture test names. Add new cases as subtests under the already-censused policy tests.
9. Run the `architecture` campaign and prove its expected, started, and terminal top-level test counts still agree.
10. Run the existing `scope` campaign so the Phase 1 transition mutant continues to pass against the now-permanent rule.
11. Confirm `TestCurrentProductionGraphPassesClosedPolicy` continues to load the live repository and is not replaced with a synthetic-only assertion.
12. Do not modify the campaign manifest in this WP. If implementation would require a new top-level test, fold the case under the corresponding existing top-level policy test instead.

**Files**:

- `internal/architecture/policy.go`
- `internal/architecture/policy_test.go`

**Validation**: Approved protocol packages pass, workflow-authority package/import mutants fail, and the exact architecture census remains green without an expected-list edit.

## Implementation Discipline

Apply the smallest policy-data change that satisfies the requirements. Prefer existing `PackagePrefix`, `ExactPackage`, `ForbiddenPackages`, `ForbiddenImportPrefixes`, `RuleLocalDirection`, and `RuleDeferredSurface` mechanisms over a new parallel policy engine.

If a requirement exposes a genuine inability in those mechanisms, extend the existing value model with typed, deterministic behavior in `policy.go`; do not add a source grep, shell script, or special case in scenario tests. Keep validation strict and error ordering deterministic.

No production Beads adapter file is owned by this package. Synthetic fixture source belongs inside `policy_test.go` and temporary directories only.

## Test Strategy

### Focused development loop

Run the policy package after each behavioral slice:

```bash
go test ./internal/architecture -run 'TestPolicyAcceptsProviderOnlyFromClassifiedAdapter|TestPolicyRejectingFixturesNameExactRuleAndEdge|TestPackagePatternsAreSlashBoundedAndAmbiguityIsDeterministic|TestCurrentProductionGraphPassesClosedPolicy' -count=1
```

### Repeated mutant qualification

```bash
go test ./internal/architecture -run 'TestPolicyAcceptsProviderOnlyFromClassifiedAdapter|TestPolicyRejectingFixturesNameExactRuleAndEdge|TestPackagePatternsAreSlashBoundedAndAmbiguityIsDeterministic' -count=10
```

The repeated run must fail on the exact assertion if any accepting or rejecting rule is removed. Do not add timing floors or sleeps.

### Census and integration gates

```bash
go test ./internal/architecture -count=1
go test -race ./internal/architecture -count=1
make scope
make fmt-check
```

Inspect the generated campaign evidence and verify the architecture profile reports equal expected, started, and terminal counts with a passing verdict. The number may remain the Phase 1 value because new cases are subtests; do not update reference evidence merely to hide a census mismatch.

### Repository hygiene

```bash
git diff --check
git status --short
```

Only the two owned files may appear as implementation changes in the WP02 lane. Mission task status events produced by Spec Kitty are governance metadata and should be handled by the runtime command rather than hand-edited.

## Definition of Done

- `internal/tracker` remains an exact provider-contract package.
- `internal/tracker/beads` and legitimate slash-bounded protocol descendants classify as provider adapters.
- Look-alike siblings remain unknown and do not inherit adapter permission.
- A production-shaped nested JSON/subprocess adapter fixture passes the closed policy without a Beads Go dependency.
- Execution, review, and integration kernel imports of the adapter fail with `RuleLocalDirection` and exact edges.
- Historic and current Beads Go module imports fail globally with `RuleDeferredSurface`, including when imported from the adapter.
- Forbidden Beads workflow-authority subtrees remain deferred while ticket catalog/graph/discussion terminology remains permitted.
- Existing deferred external surfaces and directional rules are unchanged.
- Accepting controls and rejecting mutants are deterministic across ten repetitions.
- The live production graph passes the closed policy.
- The exact architecture and scope campaign censuses pass without manifest changes.
- Focused race, formatting, and diff checks pass.
- No file outside WP02 ownership is modified manually.
- T006 through T009 are recorded done with:

  ```bash
  spec-kitty agent tasks mark-status T006 T007 T008 T009 --status done --mission beads-tracker-integration-01M0YG7E --json
  ```

## Risks

- **Contract/adapter ambiguity**: Classifying `internal/tracker` as a prefix would match the new adapter and create either ambiguous classification or silent provider leakage. Keep the contract exact.
- **Look-alike leakage**: Raw string prefixes accept `beadsx`. Use the existing slash-bounded `PackagePrefix` semantics and test sibling paths.
- **Tier-wide overpermission**: Adding adapter access to `TierAuthority` would also expose SQLite, fake tracker, and Git adapters. The correct change is adapter classification, never kernel permission.
- **Provider implementation coupling**: Allowing the Beads Go module because the adapter is provider-specific violates the chosen subprocess boundary and ties this module to unsupported internals.
- **Semantic theater**: Grepping for terms such as status or agent creates false confidence and false positives. This WP proves import/package structure; later conformance proves behavior.
- **Workflow subtree collision**: An overly broad forbidden path could reject legitimate protocol decoding. Keep each C-008 deferral explicit and slash-bounded.
- **Census drift**: New top-level tests would fail the exact architecture profile. Extend existing censused tests with subtests.
- **Vacuous mutants**: A bad fixture can fail as unknown or ambiguous before reaching the intended direction rule. Assert the exact rule and edge.
- **Cross-lane interference**: Other Phase 2 packages will create `internal/tracker/beads`. Do not touch their files, and do not relax the policy to accommodate transient unreviewed shapes.

## Reviewer Guidance

Approve only if the guard demonstrates both sides of the boundary: realistic nested adapter protocol code passes, while the same adapter edge from execution/review/integration fails for the intended rule.

Verify these points explicitly:

1. `internal/tracker` is still exact and `internal/tracker/beads` is slash-bounded.
2. Nested protocol packages pass; `beadsx` and `beads-protocol` do not.
3. Kernel-to-adapter mutants fail with `RuleLocalDirection`, not an accidental unknown-package error.
4. Direct imports of both Beads module roots fail from kernel and adapter code.
5. No Go module dependency on Beads was added.
6. C-008 workflow-authority subtrees are explicit deferred paths, while ordinary ticket concepts are not banned.
7. Existing forbidden surfaces were not removed to make the new fixture pass.
8. Existing top-level test names and the architecture campaign census remain intact.
9. Repeated tests kill the load-bearing rule mutations consistently.
10. The diff is confined to `policy.go` and `policy_test.go`.

Reject any solution based primarily on textual provider-name grep, exact classification of only the adapter root, a broad `internal/tracker` prefix, authority-to-adapter permission, unknown-package exemptions, direct Beads/Dolt implementation imports, weakened diagnostics, new uncensused top-level tests, or synthetic fixtures that never traverse the real graph-policy path.
