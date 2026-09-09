---
work_package_id: WP03
title: Original operations and complete work
dependencies:
- WP02
requirement_refs:
- FR-003
- FR-004
- FR-008
- NFR-004
- C-002
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-hubnot-tracker-replacement-01M22QEJ
base_commit: c10593a91e24d746e4d8920510c43f072bdfa01e
created_at: '2026-09-09T12:00:01.650167+00:00'
subtasks:
- T007
- T008
- T009
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/operation.go
- internal/tracker/hubnot/operation_test.go
- internal/tracker/hubnot/catalog.go
- internal/tracker/hubnot/catalog_test.go
- internal/tracker/hubnot/definition.go
- internal/tracker/hubnot/definition_test.go
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/operation.go
- internal/tracker/hubnot/operation_test.go
- internal/tracker/hubnot/catalog.go
- internal/tracker/hubnot/catalog_test.go
- internal/tracker/hubnot/definition.go
- internal/tracker/hubnot/definition_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP03 — Original operations and complete work

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement one signed original-operation engine for every mutation family, then catalog
and full-definition ports. Deterministic child operations make definition creation plus
actual dependency relations resumable.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-03; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Low-level relation replacement belongs here so complete definition creation compiles
without later Graph methods. Use WP02 read model and codec; do not add parallel codecs.
Only actual completed catalog/definition interface assertions belong here.

The shared operation engine also supplies a private read-only actor-parameter lookup
in operation.go for later attribution reads. It uses bound execute and the public
original-operation query, validates requested/returned actor and every existing strict
identity/member/semantic invariant, and returns the envelope snapshot. Mutation signer
selection remains pinned. Preserve approved adapter.go unchanged; narrowly duplicated
wire validation must have equivalence/adversarial coverage rather than introducing
a second semantic recovery engine. Per-issue history cannot prove global key uniqueness.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP03 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T007: Original signed-operation recovery

**Purpose**: Recognize the original request before looking at mutable retry-time state.

**Steps**:

1. Write response-loss tests covering restart, later foreign edit and newer expected
   portable observation before implementing retries.

2. Derive bounded stable keys from OperationID plus action identity; maintain distinct
   deterministic child keys for issue and required edge actions.

3. Perform pinned actor/key lookup before mutable-state and revision checks; compare
   stable portable semantics while excluding retry-time observation preconditions.

4. Retain the exact original signed request, full IDs and outcome; do not rebuild an old
   full-state mutation from the newest issue state.

5. Identical original request returns original effects with no append even after a human
   changes state; divergent key reuse refuses.

6. Duplicate keys, contradictory actor/attribution or incomplete lookup remain
   conflict/ambiguity; uncertainty is never repaired by a second key.

7. For new actions prepare foreign-preserving full requests under accepted input limits,
   then dispatch pinned actor and observed native expected head.

8. Expose shared primitives used by graph and effects without references to their future
   portable methods.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T008: Useful catalog with guard preservation

**Purpose**: Create/read and update simple catalog work while refusing complete-definition bypass.

**Steps**:

1. Add failing tests for rich simple creation, stable mapping replay, read after restart
   and lossless foreign field preservation.

2. Use signed opening/current mapping agreement and complete snapshot observation from
   WP02.

3. Implement simple unguarded update using full-state preparation; native head checks do
   not upgrade the portable capability.

4. Generic update against a complete-definition marker refuses before any write,
   including malformed attempts to remove the marker.

5. Validate ProjectID, WorkID, operation identity and native encoding bounds before
   dispatch.

6. After acknowledged or uncertain creation verify actual original signed effects and
   return accurate replay/applied/ambiguous flags.

7. Keep old mapping and provider references immutable; legacy Beads references never
   auto-rebind to a new opening issue.

8. Run catalog tests with concurrent human edits and typed mismatches; no success-shaped
   empty record on read failure.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T009: Complete definition saga and honest capability

**Purpose**: Support create/read with actual dependency evidence and explicit guarded-update refusal.

**Steps**:

1. Write full-definition tests covering every criterion property, scope, context,
   provenance and opaque value.

2. Create opening issue and each required relation via deterministic child operations
   using low-level mutation primitives owned in this package.

3. Read actual relation facts from WP02 model before declaring the definition complete;
   metadata declarations alone cannot satisfy dependencies.

4. Resume a crash between creation and edge publication with accurate PriorMutation and
   incomplete outcome until all facts exist.

5. Never erase surviving issue facts through fictional rollback; report reconciliation
   for irrecoverable partial histories.

6. Expose GuardedUpdate=false and preserve WorkDefinitionCapabilities.Validate/Require;
   create-only proposals remain usable.

7. UpdateDefinition refuses before child mutation and generic catalog update cannot
   escape that refusal.

8. Compile this package without WP04/05 source, run targeted race tests and hand off
   operation/prior-saga contracts.

9. Prove historically completed creation saga from all original child operations. A
   later human unlink must not be undone when replaying that completed saga.

10. Distinguish original replay result from a fresh current graph read, whose
   dependencies may legitimately differ after link/unlink.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

## Definition of Done

- All declared subtasks deliver useful behavior and explicit refusal semantics.
- All owned code compiles with its approved dependency base; no future success
  stubs or disabled required scenario hides an incomplete implementation.
- Applicable formatting, external all-package build, static/module and targeted
  tests pass with installed Go 1.26.7 and GOTOOLCHAIN=local.
- Run the applicable complete make quality gate and the package's race tests;
  preserve old active provider qualification until WP09 switches it atomically.
- Keep real provider qualification distinct from fake transport tests; use only
  disposable repositories/identities and the exact admitted executable tuple.
- Record actual gate commands, exits, measured outcomes and any remaining
  dependency limitation; do not infer passing checks from earlier source.
- Stage and commit only owned source/doc files with an explicit path allowlist.
- Record each Txxx completion with the canonical event-sourced command, not a
  checkbox in this prompt, then request independent review through runtime.

```bash
spec-kitty agent tasks mark-status T007 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T008 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T009 --status done --mission hubnot-tracker-replacement-01M22QEJ
```

Await every runtime command completion. If inherited metadata guards reject a
transition, report the exact guard to root; never delete or restore runtime
files to bypass it. Do not selfapprove or advance another package.

## Risks

Signed actor history is evidence of provider facts, not global issue compare-
and-swap, independent human identity, or host execution/review authority.
Keep partial-saga and ambiguous outcomes distinct from no-write refusals.
A local passing fixture never adopts M2 or completes production M3.

Coherent observation budgets cover every page and restart. Foreign fields and
opaque values consume real native limits; refusing an unrepresentable request
is correct, truncating it or dropping unrelated data is not.

## Reviewer Guidance

Inspect the production caller appropriate to this stage and its actual tests.
For foundation packages, later consumer composition is an explicit dependency;
for WP06 and later, a test-only caller cannot establish delivered usefulness.
Check precise no-write assertions, replay identity, invariant preservation and
truthful capability declarations. Confirm there are no cross-owner edits or
rewritten historical identities. Return defects to the owning implementation
package; independent review must not manufacture passing evidence.
