---
work_package_id: WP04
title: Blocking graph and recovery
dependencies:
- WP03
requirement_refs:
- FR-005
- FR-008
- FR-009
- C-001
- C-002
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-hubnot-tracker-replacement-01M22QEJ
base_commit: c10593a91e24d746e4d8920510c43f072bdfa01e
created_at: '2026-09-09T12:55:44.723850+00:00'
subtasks:
- T010
- T011
- T012
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/graph.go
- internal/tracker/hubnot/graph_test.go
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/graph.go
- internal/tracker/hubnot/graph_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP04 — Blocking graph and recovery

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the neutral graph API using the completed original-operation engine and
coherent current-state model. Preserve blocker direction and every unrelated native
relation.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-04; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

No new global graph command, parallel operation engine or authoritative graph cache.
Ready remains a diagnostic projection. Definition creation already owns its low-level
relation primitive; this package adds portable graph semantics.

Readiness selects mapped open work with no incoming blocks from open suppliers.
Closed work is excluded; relations from closed suppliers remain current definition
dependencies. This status projection does not grant execution authority. ExternalEdgeID
is stable for the endpoint/kind tuple; signed source actor and revision stay in proof.
Existing cycles or incomplete suppliers refuse even unlink and require native
reconciliation. A new valid relation intent already in the desired state records a
signed operation stamp with Noop=true and Replayed=false; only verified original
operation lookup can return Replayed=true.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP04 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T010: Observed graph and validation

**Purpose**: Translate native blocker edges into stable internal WorkID relations.

**Steps**:

1. Write fixtures for source-blocks-target direction, reversed expectation, unrelated
   parent/related edges and native unmapped suppliers.

2. Use coherent detailed-state graph facts from WP02, preserving signed source
   revision/actor provenance.

3. Require valid stable source and target mappings under the bound project;
   cross-project edges refuse.

4. Conflicting source heads or incomplete suppliers cannot produce an apparently
   complete graph.

5. Detect cycles with deterministic traversal and typed reconciliation rather than
   silently deleting or ordering edges.

6. List relations in canonical order independent of page traversal.

7. Ready may show blocked/conflicted/unavailable diagnostics but supplies no execution
   authorization.

8. Assert zero provider writes for all invalid graph reads and unsupported observations.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T011: Add and remove through shared recovery

**Purpose**: Mutate exactly the requested relation while preserving collaborative fields.

**Steps**:

1. Write successful add/remove and replay tests before implementing the methods.

2. Validate requests and required graph consistency before a new action dispatches.

3. Use WP03 action keys and exact original lookup prior to mutable-state checks.

4. Prepare a complete native replacement from coherent state, preserving all non-target
   relations and metadata.

5. Use expected native head only as local stale detection; report portable behavior
   conservatively.

6. Handle a lost response by signed operation reconciliation, never a blind duplicate
   add/remove.

7. Return stable internal IDs plus truthful observed provider proof and prior mutation
   status.

8. Exercise a human changing an unrelated edge before retry and preserve that edit.

9. After link and unlink, complete definitions remain readable with current graph
   dependencies and unchanged title, criteria, context, scopes and opaque payload. This
   is not guarded full-definition replacement.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T012: Partial saga and hostile graph verification

**Purpose**: Prove graph interruption never creates imaginary complete work.

**Steps**:

1. Inject interruption before issue append, between issue and edge append, and after
   final signed edge with lost response.

2. Reuse original child keys so retry neither duplicates facts nor mints a different
   signed request.

3. Verify missing supplier admission remains typed incomplete until explicitly accepted
   facts become observable.

4. Combine cycle/conflict/foreign-relation fixtures with partial saga outcomes and
   compare exact refs.

5. Use rejecting mutants for reversed direction, treating historical creation metadata
   as current graph evidence and turning ready into permission.

6. Run graph unit/race tests against completed dependencies and all-package build.

7. Report downstream API signatures and typed outcomes to WP06; do not edit its consumer
   implementation.

8. Keep any public resolution beyond advertised graph operations unsupported rather than
   inventing policy.

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
spec-kitty agent tasks mark-status T010 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T011 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T012 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
