---
work_package_id: WP07
title: Pinned installation and real qualifier
dependencies:
- WP06
requirement_refs:
- FR-012
- FR-013
- FR-014
- NFR-001
- NFR-003
- C-003
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T019
- T020
- T021
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- scripts/install-qualified-hubnot.sh
- internal/testcampaign/hubnot_evidence.go
- internal/testcampaign/hubnot_evidence_test.go
- internal/testcampaign/testdata/hubnot-qualification-reference.json
- internal/testcampaign/cmd/hubnot-qualification/main.go
- internal/testcampaign/cmd/hubnot-qualification/main_test.go
- internal/testcampaign/cmd/hubnot-qualification/fixture.go
- internal/testcampaign/cmd/hubnot-qualification/fixture_test.go
- internal/testcampaign/cmd/hubnot-qualification/runner.go
- internal/testcampaign/cmd/hubnot-qualification/runner_test.go
- internal/testcampaign/hubnot_manifest.go
- internal/testcampaign/hubnot_manifest_test.go
execution_mode: code_change
owned_files:
- scripts/install-qualified-hubnot.sh
- internal/testcampaign/hubnot_evidence.go
- internal/testcampaign/hubnot_evidence_test.go
- internal/testcampaign/testdata/hubnot-qualification-reference.json
- internal/testcampaign/cmd/hubnot-qualification/main.go
- internal/testcampaign/cmd/hubnot-qualification/main_test.go
- internal/testcampaign/cmd/hubnot-qualification/fixture.go
- internal/testcampaign/cmd/hubnot-qualification/fixture_test.go
- internal/testcampaign/cmd/hubnot-qualification/runner.go
- internal/testcampaign/cmd/hubnot-qualification/runner_test.go
- internal/testcampaign/hubnot_manifest.go
- internal/testcampaign/hubnot_manifest_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP07 — Pinned installation and real qualifier

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Create the independently source-pinned Hubnot installer and a complete real
public-consumer qualification runner before switching active campaigns. One canonical
tuple/scenario manifest prevents drifting copies.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-07; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Accepted upstream prerequisite source is mandatory; old PR2 source or the dev version
string is insufficient. Do not edit Makefile/CI yet and do not disable scenarios waiting
for future adapter code. Original provider data remains untouched.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP07 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T019: Exact source and binary installer

**Purpose**: Build and admit only the independently accepted reproducible tuple.

**Steps**:

1. Write failing tests for missing source, mutable branch substitution, wrong commit,
   wrong toolchain/target and unexpected binary digest.

2. Use accepted source commit plus Go 1.26.7, GOOS/GOARCH and deterministic build recipe
   as canonical manifest data.

3. Build into an explicit external directory; verify checkout identity before build and
   resulting bytes before executing.

4. Do not fetch or execute arbitrary lifecycle scripts, discover hn from PATH or accept
   matching dev version as source proof.

5. Produce machine-readable installer identity distinguishing
   envelope/signed-event/adapter metadata schema versions.

6. Derive or verify shared tuple values in installer and runner from one manifest rather
   than handwritten parallel constants.

7. Handle already-installed verified bytes without overwriting an unrelated user
   executable.

8. Report exact source, command, digest and build environment for the reviewed
   candidate.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T020: Closed real scenario runner

**Purpose**: Exercise every required family with exact started and terminal evidence.

**Steps**:

1. Define concrete scenario IDs for all eleven families in contracts/qualification.md
   before implementing the census.

2. Invoke externally built Go Kitty and qualified hn binaries through public commands in
   disposable Git repositories.

3. Cover binding, rich work/guards, graph/saga, assignment/refusal, discussion/identity,
   operation replay, bounds/snapshots, replicas/reconstruction, reachability/playback,
   cutover/preservation and performance.

4. Isolate identities/config and never initialize, sync or change binding in the user
   repository.

5. Keep temporary network/remote selection explicit and record offline enforcement when
   required.

6. Capture source/tree/binary/environment/exits and individual scenario status under
   bounded output/deadlines.

7. Missing prerequisites, skipped or omitted scenarios and empty selection are
   non-admissible, never a pass.

8. Large integrated scenarios may invoke existing public acceptance harness logic, but
   cannot mutate signed refs directly to fabricate user success.

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

### Subtask T021: Evidence validation and runner smoke

**Purpose**: Prove evidence cannot manufacture qualification from incomplete execution.

**Steps**:

1. Create source-bound Hubnot evidence schema and reference fixture separate from
   historical Beads receipts.

2. Add tamper mutants for tuple/digest, expected count, started/terminal mismatch,
   omitted row, failure exit and overwritten verdict.

3. Test runner timeout and subprocess failures preserve explicit non-admissible
   evidence.

4. Run useful real public lifecycle, two-replica selection and restart/replay smoke with
   exact signed IDs.

5. Reconstruction evidence must distinguish public work/history from same-owner private
   signer recovery.

6. Keep export/restorable backup capability unsupported despite successful public
   reconstruction.

7. Provide canonical manifest/runner CLI handoff to WP09 active Makefile/workflow
   selector.

8. Run qualifier/evidence tests and all-package build; save logs outside repository.

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
spec-kitty agent tasks mark-status T019 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T020 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T021 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
