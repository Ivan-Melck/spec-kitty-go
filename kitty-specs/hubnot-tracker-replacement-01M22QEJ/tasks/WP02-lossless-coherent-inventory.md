---
work_package_id: WP02
title: Lossless codec and coherent inventory
dependencies:
- WP01
requirement_refs:
- FR-002
- FR-004
- FR-005
- FR-006
- FR-007
- FR-009
- NFR-001
- NFR-002
- NFR-004
- C-003
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T004
- T005
- T006
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/codec.go
- internal/tracker/hubnot/codec_test.go
- internal/tracker/hubnot/observation.go
- internal/tracker/hubnot/observation_test.go
- internal/tracker/hubnot/revision.go
- internal/tracker/hubnot/revision_test.go
- internal/tracker/hubnot/mapping.go
- internal/tracker/hubnot/mapping_test.go
- internal/tracker/hubnot/testdata/codec-fixtures.json
- internal/tracker/inventory.go
- internal/tracker/inventory_test.go
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/codec.go
- internal/tracker/hubnot/codec_test.go
- internal/tracker/hubnot/observation.go
- internal/tracker/hubnot/observation_test.go
- internal/tracker/hubnot/revision.go
- internal/tracker/hubnot/revision_test.go
- internal/tracker/hubnot/mapping.go
- internal/tracker/hubnot/mapping_test.go
- internal/tracker/hubnot/testdata/codec-fixtures.json
- internal/tracker/inventory.go
- internal/tracker/inventory_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP02 — Lossless codec and coherent inventory

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Own the common immutable read model so catalog, graph and effects never reconstruct
incompatible partial views. Add the small neutral inventory port requested by plan,
preserving existing authority boundaries.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-02; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

All dependency facts derive from actual current native relations across detailed rows,
including unmapped suppliers. There is no global graph CLI. Use signed opening_metadata
to validate immutable mapping without one history scan per issue. Namespace is
go-kitty/<name>; default details page size is 8.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP02 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T004: Versioned lossless work codec

**Purpose**: Encode complete portable meaning without erasing native collaboration fields.

**Steps**:

1. Write table fixtures for rich criteria, design context, cross-domain reason, scopes,
   provenance, original creation-saga dependencies and nested opaque JSON before
   implementation.

2. Reserve go-kitty/<name> metadata keys; use schema, immutable IDs, simple/complete
   marker, byte/chunk counts and SHA-256 over canonical payload.

3. Chunk at UTF-8 boundaries into values no larger than 4096 bytes; account for all
   fixed, payload and foreign keys within the 64-key native limit.

4. Preserve arbitrary JSON semantic values with numeric precision and nested nulls;
   avoid float64 conversion and lossy string coercion.

5. Validate native title/body/ordered criterion ID/text against the owned payload;
   external contradictory edits must produce reconciliation, not a stale returned
   definition.

6. Preserve independent decoded values, unknown foreign metadata, labels, status,
   assignees and unrelated relations during prepared full-state updates.

7. Reject unknown owned schema, duplicate/missing chunks, wrong count/digest,
   cross-project mapping and unsupported representation before a mutation can begin.

8. Add encoded request budget tests where foreign fields exhaust capacity; never discard
   them to make an update fit.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T005: Coherent snapshot and observation proof

**Purpose**: Build one bounded read model that later portable methods can consume.

**Steps**:

1. Write mismatched snapshot, stale-page, multiple-head, repeated mapping and
   missing-supplier refusal tests first.

2. Collect detailed rows using reviewed page size and cursor semantics, preserving one
   shared budget across page retries and stale snapshot restarts.

3. Compare signed opening_metadata mapping against current state and supplied external
   reference; duplicates and rewritten identities fail closed.

4. Derive directed blocker graph from all relevant detailed native state rows, retaining
   unmapped suppliers as necessary context; never call a nonexistent global graph
   endpoint.

5. For known-reference observations collect required heads/discussion pages and compare
   every snapshot; incomplete histories cannot masquerade as empty data.

6. Canonicalize state, full IDs, relation/comment provenance and mapping without losing
   semantic distinctions; retain full proof alongside folded equality-only Revision.

7. No process-local sequence counter, authoritative adapter cache or comparison that
   orders numeric revisions is permitted.

8. Enforce 64 MiB/10,000 returned-item/120-second budget across all attempts, not anew
   after each snapshot change.

9. Current definition dependencies come only from verified current native graph
   relations. Original create dependencies are historical saga intent, never a competing
   current list.

10. Inventory common snapshot/current-definition proof is weaker than full
   known-reference head/edge proof. Do not reuse opening creator as current-edge signer
   or invent a current head ID.

11. Known-reference reads use accepted paginated heads/graph to obtain full current-edge
   provenance; inventory remains free of per-issue history scans.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T006: Neutral inventory and scaling fixture

**Purpose**: Expose complete current definitions without per-issue history fan-out.

**Steps**:

1. Create internal/tracker/inventory.go and its tests with a small ProjectID-scoped
   request and immutable results containing complete current definitions, references and
   common proof.

2. Keep provider-native structures out of the neutral port and preserve existing
   definition validation and guarded-update meanings.

3. Implement mapped inventory using one pass through detailed current states plus
   in-memory graph derivation, not one full-history scan per issue.

4. Unmapped rows may be absent from results but must remain available to reject
   missing/conflicted suppliers and duplicate mapping claims.

5. Compare every field of a 1,000-definition fixture, including relations and opaque
   payload; summary/title equality is insufficient.

6. Count fake transport requests and returned items to catch N-history-query growth;
   keep actual compiled timing for final acceptance.

7. Run codec/observation/inventory race tests and all-package build; publish immutable
   read model and canonical digest rules to downstream owners.

8. Report upstream opening_metadata/page-size contract hold explicitly rather than
   silently choosing a different proof.

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
spec-kitty agent tasks mark-status T004 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T005 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T006 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
