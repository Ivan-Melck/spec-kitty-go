---
work_package_id: WP08
title: Inert ignore scanner classification
dependencies: []
requirement_refs:
- NFR-005
- C-004
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T022
- T023
- T024
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent: []
execution_mode: code_change
owned_files:
- internal/architecture/m2_absence_test.go
- internal/architecture/m2_absence_mutants_test.go
- scripts/m2-absence/expected-absence-tests.txt
- internal/testcampaign/m2/absence.go
- internal/testcampaign/m2/evidence_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP08 — Inert ignore scanner classification

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Fix the actual false positive for inert SQLite filename patterns in plain .gitignore
without creating a hidden executable exception. Preserve all real absence and dependency
guards.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-08; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Own only absence scanner/tests/exact census. Do not alter the nineteen original Beads
data files, delete recovery material, skip hidden paths or rewrite historical receipt
identities.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP08 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T022: Failing inert-pattern and hidden-command examples

**Purpose**: Capture the exact false positive and its security-sensitive boundary.

**Steps**:

1. Read the current absence scanner and existing expected mutant list before changing
   classification.

2. Add a fixture reproducing the observed plain .gitignore SQLite filename entry from
   the local inventory.

3. Add hidden executable content using SQLite in an ignored file so the positive
   classifier cannot become a directory exemption.

4. Include shell command and forbidden dependency/import controls, including executable
   use without conventional extension.

5. Assert inert patterns pass and actual executable/import cases fail with the correct
   finding category.

6. Keep the real provider data read-only; use copied fixture strings inside tests.

7. Run targeted tests to demonstrate the existing false positive before implementation.

8. Record no scanner initialization, repair or user-data migration requirement.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T023: Context-sensitive inert pattern handling

**Purpose**: Distinguish patterns from executable use at the narrow syntax boundary.

**Steps**:

1. Implement a small explicit plain-ignore classification rule instead of excluding
   ignored directories wholesale.

2. Keep all actual source/dependency and subprocess scans active across hidden files.

3. Do not suppress every sqlite string by file extension when content can execute or
   declare a dependency.

4. Preserve exact finding paths and typed reason codes for genuine failures.

5. Ensure comments and filename glob syntax cannot be confused with recognized execution
   constructs.

6. Keep prior immutable history exemptions unchanged and independent of this rule.

7. Use existing scanner helpers where feasible; avoid a general-purpose parser or new
   dependency.

8. Run all targeted positive/negative cases after the change.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T024: Complete absence mutant census

**Purpose**: Demonstrate the correction closes the false positive without removing detection.

**Steps**:

1. Run complete existing absence mutants and expected source/dependency guards.

2. Update expected-absence-tests.txt only for actual newly named sensors; no removed
   failure is relabeled a pass.

3. Kill a mutant that exempts all .gitignore content and one that excludes ignored
   paths.

4. Check scanner reporting remains deterministic and bounded.

5. Compare original data manifest if any live repository read was used; do not change
   contents to satisfy tests.

6. Run appropriate architecture package checks and all-package compilation externally.

7. Report exact test names/evidence to WP09 and root without changing their selectors.

8. Leave source/evidence reconstruction and provider runtime outside this package.

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
spec-kitty agent tasks mark-status T022 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T023 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T024 --status done --mission hubnot-tracker-replacement-01M22QEJ
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

## Scope extension: exact historical and current receipt admission

The sixteen new observed cases increase the census from 489 to 505. Full quality exposed hardcoded 489 in the M2 production nested-absence verifier and its synthetic fixture. This necessary follow-through belongs to WP08; both files are explicitly owned.

Admit exactly the original expected-contract SHA256/count pair 245af6f271f12f1905613c4140cb4b842cc117ab40465f559a545069e79af09d / 489 and current pair 838f3a6c78e60f2d2153ab90df675117c03a4afc8bc3c5cfd4ef756b013e61d3 / 505. Validate manifest count against the admitted contract, retaining exact expected/run/pass/raw-event equality and no-skip/no-duplicate checks. Never accept arbitrary input-derived counts. Historical verification retains original bytes; current generation uses the actual 505 script contract. Mirror the current digest against that file in a test.

Derive the synthetic fixture count from its loaded admitted contract. Test both exact historical/current contracts and rejection of unknown/reduced/duplicate sets or forged counts. Preserve source/toolchain binding and other qualification requirements; no schema/adoption change or frozen receipt rewrite. Source scope is now five files. Run focused nested evidence regressions and final complete quality on corrected stable source.
