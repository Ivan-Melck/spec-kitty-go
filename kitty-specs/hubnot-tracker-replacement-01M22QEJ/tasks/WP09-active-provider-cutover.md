---
work_package_id: WP09
title: Active provider cutover
dependencies:
- WP01
- WP02
- WP03
- WP04
- WP05
- WP06
- WP07
- WP08
requirement_refs:
- FR-011
- FR-014
- FR-015
- NFR-003
- C-004
- C-005
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T025
- T026
- T027
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- .github/workflows/hubnot-qualification.yml
- internal/architecture/hubnot_documentation_test.go
- internal/architecture/hubnot_cutover_test.go
- docs/HUBNOT_TRACKER.md
- internal/testcampaign/historical_beads_evidence.go
- internal/testcampaign/historical_beads_evidence_test.go
execution_mode: code_change
owned_files:
- internal/tracker/beads/**
- internal/testcampaign/cmd/beads-qualification/**
- scripts/install-qualified-beads.sh
- .github/workflows/beads-qualification.yml
- internal/testcampaign/beads_evidence.go
- internal/testcampaign/beads_evidence_test.go
- .github/workflows/hubnot-qualification.yml
- .github/workflows/quality.yml
- .github/workflows/full-campaign.yml
- .github/workflows/m2-qualification.yml
- internal/testcampaign/manifest.go
- internal/testcampaign/mission_method.go
- internal/testcampaign/mission_method_test.go
- internal/testcampaign/census_test.go
- internal/testcampaign/cmd/campaign/main.go
- internal/testcampaign/cmd/campaign/main_test.go
- internal/testcampaign/testdata/qualification-reference.json
- internal/architecture/beads_documentation_test.go
- internal/architecture/closeout_documentation_test.go
- internal/architecture/m2_dependency_guard_test.go
- internal/architecture/m2_mutant_control_test.go
- internal/architecture/hubnot_documentation_test.go
- internal/architecture/hubnot_cutover_test.go
- docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md
- docs/PRODUCTION_RUNTIME_PROGRAM.md
- docs/programs/production-runtime-program.json
- docs/TECHNICAL_PROJECT_PLAN.md
- docs/ORCHESTRATOR_API_2_0.md
- docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md
- docs/PYTHON_BEHAVIOR_INVENTORY.md
- docs/adr/0002-production-orchestration-architecture.md
- docs/MCP_2026_07_28_ADAPTER.md
- docs/HUBNOT_TRACKER.md
- internal/execution/effect_authority_v2_test.go
- internal/scenario/behavior_parity_test.go
- Makefile
- README.md
- internal/testcampaign/historical_beads_evidence.go
- internal/testcampaign/historical_beads_evidence_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP09 — Active provider cutover

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Switch the live default only after the successor and real qualifier exist. Retire old
provider code while preserving frozen evidence validators and exact historical
identities.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-09; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Historical beads_evidence.go and its testdata may remain under explicit historical
naming; rename/isolate only when needed, never rewrite digests to resemble Hubnot.
Existing execution/scenario references are Beads-named sensor/census text, not current
direct imports. Preserve scenario/architecture_test.go deletion census unchanged.

Wire hosted provider checks to WP07's fixed hosted-provider selection and strict
selected wrapper, with explicit non-full scope in artifacts and current guidance.
Retain a distinct full-local qualification target requiring actual original-file/ref
preservation. Do not run the full wrapper and suppress its local-recovery failure;
frozen-source preservation and every repeatable scenario remain required in hosted
checks. Final mission acceptance still requires the full local census.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP09 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T025: Reference classification and runtime retirement

**Purpose**: Remove active Beads paths without deleting historical validation.

**Steps**:

1. Read occurrence_map.yaml and classify every owned occurrence as active, historical,
   guard or evidence before editing.

2. Remove internal/tracker/beads and old qualification/installer/workflow only after no
   live successor dependency is missing.

3. Inspect historical beads_evidence validators and qualification-reference fixture
   consumers; preserve their old tuple and expected digests.

4. If isolation is necessary, keep validation code under explicit historical names with
   identical assertions rather than mutating receipt data.

5. Update internal/execution/effect_authority_v2_test.go compensation sensor wording and
   internal/scenario/behavior_parity_test.go exact proof reference semantically.

6. Current scoped search finds no direct execution/scenario retired-adapter imports; if
   later source introduces one, classify and replace it within explicitly owned paths.

7. Keep internal/scenario/architecture_test.go retired-file deletion census unowned and
   unchanged.

8. Remove obsolete test-only dependency exceptions narrowly; no authority import rule
   may be weakened.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T026: Atomic installer, campaign and CI switch

**Purpose**: Select complete successor qualification with exact census and no fallback.

**Steps**:

1. Update Makefile provider targets, variables and package exclusions to the working
   Hubnot runner and tuple manifest.

   Build the qualifier externally with the pinned recipe and invoke its canonical
   `install --go-bin ABS --source CLEAN_ACCEPTED_CHECKOUT --destination ABS`
   subcommand. The redundant shell bootstrap is retired by WP07; do not recreate
   it or change the executable scanner to support it.

2. Update active provider-adversarial, provider-real and mission-real-identity test
   selections with exact expected test names.

3. Preserve zero-match/omitted-test rejection; do not hide transition failures by
   relaxing census validation.

4. Replace provider workflow and update generic quality/full/M2 wiring only where live
   command or artifact contracts require it.

5. Keep missing/offline prerequisite evidence non-admissible and upload bounded failure
   artifacts.

6. Run the active public consumer/qualification without bd or Dolt on PATH; ensure no
   dual-write/fallback remains.

7. Preserve frozen M1/M2/Phase1–3 validators and source references unchanged.

8. Check current architecture graph and rejecting mutants with the old implementation
   removed.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T027: Living docs and transition gates

**Purpose**: Make current instructions executable while retaining historical truth.

**Steps**:

1. Update README/current architecture/program/API/runbook guidance from actual consumer
   help and strict request fixtures.

2. Create docs/HUBNOT_TRACKER.md with actor binding, action examples, limits, recovery
   and unsupported capability semantics.

3. Distinguish tracker-only commands from unchanged M2 unavailable mission composition
   and unfinished M3.

4. Remove obsolete active inspect-beads guidance while retaining historical Phase2
   qualification claims as history.

5. Keep old receipt identities, adoption fields, release bytes and local nineteen-file
   provider manifest unchanged.

6. Split active documentation assertions from frozen evidence-integrity checks rather
   than changing the old fixture tuple.

7. Run complete applicable make quality, architecture/scope/census, build/static/module
   checks and current docs smoke commands.

8. Report required hosted workflow name transition separately from local proof; no local
   result implies hosted adoption.

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
spec-kitty agent tasks mark-status T025 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T026 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T027 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
