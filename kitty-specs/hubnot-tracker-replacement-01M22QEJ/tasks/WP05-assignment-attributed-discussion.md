---
work_package_id: WP05
title: Assignment and attributed discussion
dependencies:
- WP03
requirement_refs:
- FR-006
- FR-007
- FR-008
- NFR-004
- C-001
- C-002
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T013
- T014
- T015
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/assignment.go
- internal/tracker/hubnot/assignment_test.go
- internal/tracker/hubnot/discussion.go
- internal/tracker/hubnot/discussion_test.go
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/assignment.go
- internal/tracker/hubnot/assignment_test.go
- internal/tracker/hubnot/discussion.go
- internal/tracker/hubnot/discussion_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP05 — Assignment and attributed discussion

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Expose useful descriptive assignment and signed idempotent discussion without confusing
transport signer with accountable author. Every effect shares original-request recovery.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-05; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Use the shared actor-parameter read lookup for the actual comment signer, checking
its snapshot against collected comments under the same cumulative budget/restarts.
Pair a deterministic signed attribution revision with an exact-body comment; the
comment key must derive its attribution key without recovering the portable ID first.
Author is semantic payload, never action-key material. Validate global actor/key
uniqueness and all signed child relationships; preserve surviving attribution if
comment publication fails. Do not wrap or rewrite the native comment body.

Atomic claim and conditional compensation refuse before mutation. Multiple native
assignees are ambiguous. Neither an actor key nor comment body grants host identity,
review independence or permission.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP05 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T013: Descriptive assignment with refusal sensors

**Purpose**: Implement ordinary assignment while retaining conservative portable guarantees.

**Steps**:

1. Write single-assignee observation and unguarded projection tests with operation
   attribution.

2. Use coherent state from WP02 and the original-operation engine from WP03.

3. Preserve native labels, metadata, relations and unrelated state during full
   assignment replacement.

4. Return multiassignee ambiguity rather than selecting an assignee or collapsing a
   list.

5. Atomic claim request and conditional compensation request must refuse before a
   mutation starts.

6. Prior operation replay after later human reassignment returns original effects
   without changing the human assignment.

7. Stale new action or changed semantic reuse yields typed reconciliation with no
   destructive undo.

8. Use child-write/ref sensors to kill read-compare-update compensation emulation.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T014: Discussion identity and exact content

**Purpose**: Append and list original facts with explicit accountable-author attribution.

**Steps**:

1. Write native third-party and adapter-authored comment fixtures with equal body but
   different IDs/signers.

2. Preserve full comment/event ID, exact body bytes, timestamp and transport signer from
   signed facts.

3. Encode versioned accountable-author/operation/semantic-digest attribution as a claim
   by the pinned signer, not an independent verified host identity.

4. Native foreign comments retain native attribution and are not silently assigned an
   adapter author.

5. Call the shared lookup engine so lost-response append replay returns the original
   fact and timestamp.

6. Changed body or accountable author under the same action key refuses divergent reuse.

7. Paginate discussion under one matching snapshot and common logical budget.

8. Bound input and output without removing Unicode, changing body whitespace or dropping
   provenance.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T015: Cross-identity and recovery acceptance

**Purpose**: Demonstrate that descriptive facts never acquire execution or review authority.

**Steps**:

1. Test one accountable author using two signing keys; transport distinction cannot
   create independent self-review.

2. Add an approval-looking native comment and prove it remains discussion data only.

3. Inject lost response, restart and later unrelated human edit for both mutation
   families.

4. Test duplicate operations, signer drift and incomplete original lookup with zero
   duplicate appends.

5. Run focused race tests and compare observed IDs/refs rather than only friendly
   rendered messages.

6. Expose only actual completed AssignmentProjection and Discussion interfaces.

7. Publish attribution schema and replay outcomes to the consumer owner; request
   contract review for any mismatch.

8. Leave graph ownership and shared codec/recovery files unchanged.

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
spec-kitty agent tasks mark-status T013 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T014 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T015 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
