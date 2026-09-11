---
work_package_id: WP10
title: Integrated public acceptance
dependencies:
- WP09
requirement_refs:
- FR-001
- FR-002
- FR-003
- FR-004
- FR-005
- FR-006
- FR-007
- FR-008
- FR-009
- FR-010
- FR-011
- FR-012
- FR-013
- FR-014
- FR-015
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-005
- C-001
- C-002
- C-003
- C-004
- C-005
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-hubnot-tracker-replacement-01M22QEJ
base_commit: c10593a91e24d746e4d8920510c43f072bdfa01e
created_at: '2026-09-09T20:26:45.434806+00:00'
subtasks:
- T028
- T029
- T030
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/helpercmd/hubnot_acceptance_test.go
- internal/helpercmd/hubnot_performance_test.go
- docs/HUBNOT_QUALIFICATION.md
execution_mode: code_change
owned_files:
- internal/helpercmd/hubnot_acceptance_test.go
- internal/helpercmd/hubnot_performance_test.go
- docs/HUBNOT_QUALIFICATION.md
role: implementer
tags: []
tracker_refs: []
---

# WP10 — Integrated public acceptance

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Prove every story and requirement through compiled first-party/provider interfaces and
independent evidence, including integrated scale. Deliver final operational
qualification guidance without editing dependency implementation.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-10; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Use WP07 runner by invocation; its directory is not owned here. Report dependency
defects to root/owner rather than patching outside scope. All writes use disposable
repos/identities and external logs; original bindings, .beads, stashes, branches and
frozen receipts remain unchanged.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP10 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T028: Public lifecycle, recovery and authority acceptance

**Purpose**: Exercise useful work and explicit refusals through the actual compiled consumer.

**Steps**:

1. Build real product and pinned provider binaries externally and record exact
   source/build identities.

2. Create two full definitions with all rich fields, link/unlink dependencies, project
   assignment and append attributed/native discussion.

3. Compare full semantic fields after restart and verify simple catalog update preserves
   foreign fields.

4. Inject response loss around actual public mutation and retry after restart/later
   human edits/new expected observation.

5. Assert identical retry appends nothing; divergent, duplicate, incomplete or
   signer-drift retries stay typed reconciliation.

6. Prove invalid Unicode/input/guarantee cases do not unexpectedly change provider or
   control refs.

7. Use hostile approval comment and same accountable actor with two signers without
   granting self-review or host permission.

8. Run true control-journal playback with the production reducer composition,
   including checkpoint-plus-suffix equivalence and typed projection assertions;
   verify the existing structural no-I/O boundary. Prove zero HostFactory calls on
   tracker dispatch with its actual connectable tripwire separately. Use a
   panic-on-provider-call sensor only where an existing execution seam can
   genuinely invoke it; do not introduce a provider dependency into pure playback
   or count an unused trap as evidence.

9. Keep exact production tuple admission during real lost-response trials. Discard a
   completed compiled-consumer response externally and retry the same input in a fresh
   process; do not bind an arbitrary proxy executable as qualified hn.

10. Use fake/injected child transport only in unit boundary adversity. If killing after
   signing, first prove the signed event and bounded cleanup; describe only the fault
   actually exercised.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T029: Replica reconstruction and integrated 1000-item measurement

**Purpose**: Measure complete compiled behavior and explicit accepted-history reconstruction.

**Steps**:

1. Use two real disposable actors/replicas and public selected sync; withheld suppliers
   must remain refused until explicitly admitted.

2. Reconstruct public rich work into a fresh path and compare complete
   IDs/definitions/graph/discussion; document signer recovery separately.

3. Exercise concurrent multihead histories and large head sets with bounded
   refusal/recovery as actually advertised.

4. Prepare 1,000 full mapped definitions with representative graph/discussion outside
   the timed interval.

5. Time compiled consumer inventory including all detailed pages, normalization and
   graph derivation; compare every field and report page/item/byte counts.

6. Measure known-reference observation and original-operation recovery separately with
   duration and child process counts.

7. Require complete observation within 120 seconds on the qualified host; no title-only
   listing or internal-helper benchmark substitutes.

8. Report host, Go toolchain, OS/architecture, race mode, source/binary tuple and actual
   measured durations.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T030: Requirement matrix, preservation and complete gates

**Purpose**: Supply reviewable source-bound evidence without overclaiming completion.

**Steps**:

1. Run WP07 real runner and verify every closed required scenario has a started and
   terminal passing record.

2. Run applicable complete quality/static/module/build gates and focused/full required
   race gates on final source.

3. Compare original nineteen provider file hashes and all frozen source/evidence
   manifests; backup history remains unclassified and preserved.

4. Create docs/HUBNOT_QUALIFICATION.md with exact commands, accepted tuple,
   reconstruction/signer boundaries and test-to-requirement matrix.

5. Include FR/NFR/C coverage and all four stories; classify actual limitations rather
   than suppressing useful features.

6. Keep M2 release_ready=true adopted=false and M3/M4 scope unchanged; distinguish local
   and hosted evidence.

7. Provide root final fixed commit, logs and concrete test names for canonical
   acceptance-verdict recording.

8. Do not selfapprove or mutate dependency/runtime metadata to pass a review gate.

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
spec-kitty agent tasks mark-status T028 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T029 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T030 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
