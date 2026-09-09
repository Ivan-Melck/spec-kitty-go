---
work_package_id: WP06
title: Native tracker consumer
dependencies:
- WP03
- WP04
- WP05
requirement_refs:
- FR-001
- FR-010
- FR-011
- C-001
- C-005
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
subtasks:
- T016
- T017
- T018
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/inspect.go
- internal/tracker/hubnot/inspect_test.go
- internal/tracker/hubnot/capability.go
- internal/tracker/hubnot/capability_test.go
- internal/helpercmd/provider.go
- internal/helpercmd/provider_test.go
- internal/helpercmd/provider_request.go
- internal/helpercmd/provider_request_test.go
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/inspect.go
- internal/tracker/hubnot/inspect_test.go
- internal/tracker/hubnot/capability.go
- internal/tracker/hubnot/capability_test.go
- internal/helpercmd/runner.go
- internal/helpercmd/runner_test.go
- internal/helpercmd/model.go
- internal/helpercmd/provider.go
- internal/helpercmd/provider_test.go
- internal/helpercmd/provider_request.go
- internal/helpercmd/provider_request_test.go
- cmd/spec-kitty/main_test.go
- internal/architecture/reachability.go
- internal/architecture/reachability_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP06 — Native tracker consumer

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Connect the compiled first-party spec-kitty tracker command family to completed neutral
ports and the real Hubnot adapter. Aggregate Inspector/capability declarations now
describe implemented methods.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-06; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

internal/helpercmd/production.go remains untouched. Tracker-only dispatch happens before
control-root validation and any HostFactory call; old mission commands keep their exact
lifecycle. No API 1.3 restoration or new host admission.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP06 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T016: Strict provider request and dispatch grammar

**Purpose**: Make tracker operations discoverable without mixing mission authority DTOs.

**Steps**:

1. Write parser tests for spec-kitty tracker ACTION --binding FILE --input FILE|- and
   existing output conventions.

2. Implement separate provider_request DTOs with schema go-kitty.tracker-request/1 and
   portable action data.

3. Permit inspect without input and derive inventory ProjectID from binding; explicit
   conflicting project refuses.

4. Reject duplicate/unknown keys, malformed UTF-8, trailing JSON and oversized input
   before any provider mutation.

5. Dispatch tracker commands before control-root classification/startup while retaining
   mission command parsing.

6. Keep available actions inspect, inventory, read, create, update, define, definition,
   assign, comment, discussion, dependencies, link, unlink and ready.

7. Wire each action through its real neutral port, not raw Git refs or test-only
   wrappers.

8. Keep text/JSON exit/outcome semantics consistent and expose actionable bounded
   diagnostics.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T017: Inspector and final honest port declarations

**Purpose**: Report exactly the guarantees provided by completed adapter methods.

**Steps**:

1. Implement inspection with public identity/binary/workspace checks and no
   initialization or repair.

2. Add final interface assertions only for implemented
   catalog/definition/graph/assignment/discussion/inventory/inspection methods.

3. Declare catalog and assignment unguarded, discussion observably idempotent after
   signed evidence, revision content-derived.

4. Declare safe compensation unsupported, concurrent writers none, portable export and
   restorable backup unsupported.

5. Keep complete-definition GuardedUpdate=false and preserve per-proposal capability
   gate refusal.

6. Wrong tuple, legacy binding, absent executable and signer drift produce explicit
   refusal without fallback.

7. Read-only mode must refuse mutation before child write while allowing valid
   inspection.

8. Keep signer/native ref fields as provenance and never change GapDB
   adoption/readiness.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T018: Compiled consumer and reachability proof

**Purpose**: Exercise actual command-to-provider wiring and preserve old mission behavior.

**Steps**:

1. Build the product binary externally and drive useful creation, reading, linking,
   assignment and discussion through its public request contract.

2. Use a HostFactory spy that fails on any call for tracker actions; require zero calls
   even on malformed tracker input.

3. Run existing mission runner tests to prove their control-root lifecycle and output
   path remain intact.

4. Kill removal of the actual consumer-to-adapter call with production reachability
   fixtures.

5. Test retry output distinguishes replayed/applied/prior-mutation/ambiguous rather than
   treating every error as no write.

6. Exercise invalid guarantee no-write sensors through compiled request decoding, not
   only direct adapter tests.

7. Document exact field spelling in request tests for WP09 documentation and WP07 runner
   reuse.

8. Run helper/adapter race tests and all-package build; no production host seam change
   is authorized.

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
spec-kitty agent tasks mark-status T016 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T017 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T018 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
