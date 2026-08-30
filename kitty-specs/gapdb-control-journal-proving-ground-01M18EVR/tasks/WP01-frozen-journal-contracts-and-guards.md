---
work_package_id: WP01
title: Frozen Journal Contracts and Architecture Guards
dependencies: []
requirement_refs:
- C-007
- C-009
- FR-014
- FR-015
- FR-016
- FR-017
- FR-039
- NFR-011
- NFR-013
planning_base_branch: main
merge_target_branch: main
branch_strategy: Planning artifacts for this mission were generated on main. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into main unless the human explicitly redirects the landing branch.
subtasks:
- T001
- T002
- T003
- T004
- T005
history: []
agent_profile: architect-alphonso
authoritative_surface: internal/architecture/
create_intent:
- internal/architecture/m1_budget_contract_test.go
- internal/architecture/m1_dependency_guard_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/architecture/m1_contracts_test.go
- internal/architecture/m1_evidence_semantics_test.go
- internal/architecture/m1_budget_contract_test.go
- internal/architecture/m1_dependency_guard_test.go
role: architect
tags:
- m1
- architecture
- contracts
tracker_refs: []
---

# Work Package Prompt: WP01 - Frozen Journal Contracts and Architecture Guards

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `architect-alphonso`
- **Role**: `architect`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Turn the already approved M1 contracts, product budgets, admitted dependency
tuple, and evidence predicate into an executable architectural boundary. Later
WPs must receive a failing test before they can widen a value, import a forbidden
package, smuggle storage coordinates into product values, or claim success from
an incomplete evidence census.

This WP strengthens tests only. It does not redesign approved contracts, create
runtime packages, add GapDB to the module, or manufacture passing evidence.

## Context and product laws

The contract directory is normative input. Existing schema compilation and
semantic evidence tests are valuable but must be made complete enough to police
implementation pressure.

Preserve these laws:

1. Only the pinned public GapDB module may appear at the storage edge.
2. `internal/server` is forbidden everywhere in this repository.
3. SQLite code is neither changed nor used as an M1 fallback.
4. Product budgets are stricter than admitted runtime limits/defaults.
5. Events are ordered by committed revision, event index, and EventID.
6. Current-batch server revisions never appear in product keys or values.
7. Runtime epoch changes only at owner restart, not on ordinary commands.
8. Evidence passes only with the exact census and combined ordered trial.

## Branch and ownership

Work only in the assigned WP01 lane. Integrate into
`kitty/mission-gapdb-control-journal-proving-ground-01M18EVR`; do not merge or
push directly to `main`. Edit only the four files in `owned_files`. Treat the
mission contracts as read-only fixtures. Do not edit `go.mod` or `go.sum`.

## T001: Freeze executable product budgets and contract fixtures

**Purpose**: Detect accidental widening before runtime code can rely on it.

**Files**: Create `internal/architecture/m1_budget_contract_test.go`.

**Implementation requirements**:

1. Load `m1-product-budgets-v1.json` relative to repository source, not cwd.
2. Decode into a typed test fixture and reject unknown or missing fields.
3. Assert the exact ratified key, value, checkpoint, event, mutation, batch,
   wire, scan-page, client, checkpoint-due, and restore bounds.
4. Assert every product limit is strictly below its admitted configured runtime
   limit or default using revision-pinned comparison data from the plan.
5. Keep storage byte limits distinct from product-event and evidence counts.
6. Refuse publication if comparison provenance is absent or stale.

**Tests**:

- Positive contract fixture passes.
- A copied fixture with each bound widened fails with a field-specific reason.
- Unknown and omitted fields fail closed.
- Test diagnostics name a contract field, never a secret or environment value.

**Exit evidence**: `go test ./internal/architecture -run M1Budget`.

## T002: Strengthen schema and semantic contract tests

**Purpose**: Prevent structurally valid but substantively vacant evidence.

**Files**: Update both existing `m1_*` tests owned by this WP.

**Implementation requirements**:

1. Compile every mission JSON Schema and retain asserted format checking.
2. Verify the positive fixture contains one shared source-bound RunID.
3. Require exactly fourteen scenario results, five fault results, eight focused
   summaries, nineteen killed mutants, one ordered trial, invariant summary,
   independent review, artifacts, and adoption state.
4. Require Actor A/Process A/W1, Actor B/Process B/W2, and Actor A/Process C/W3.
5. Require reviewer ActorIDs to differ from every candidate contributor ActorID.
6. Bind workspace quarantine, restart epoch, old-capsule refusal, equal digest,
   zero playback calls/writes, and serialized integration to the ordered trial.
7. Preserve honest `fail` and `incomplete` records; never coerce them to pass.

**Tests**:

- Every removed census member rejects pass.
- Every false universal invariant rejects pass.
- A different child RunID or source digest rejects pass.
- Failure and incomplete fixtures remain schema-valid but non-admissible.

**Exit evidence**: `go test ./internal/architecture -run 'M1Schemas|M1Evidence'`.

## T003: Enforce GapDB import and forbidden-backend boundaries

**Purpose**: Keep the proof on the admitted external sidecar seam.

**Files**: Create `internal/architecture/m1_dependency_guard_test.go`.

**Implementation requirements**:

1. Parse Go imports structurally; do not grep comments or string literals.
2. Permit the GapDB public module only below `internal/controlstore/gapdb` and
   `internal/sidecar/gapdb`.
3. Reject any GapDB `internal/server` import from every package and test.
4. Reject new M1 imports of SQLite packages and any runtime fallback selector.
5. Reject current-batch revision, shared projection head, and global epoch CAS
   constructions through precise source/fixture rules documented in the test.
6. Allow existing historical SQLite implementation to remain untouched.
7. Produce stable, actionable path/import diagnostics.

**Tests**:

- The current repository passes before M1 runtime files exist.
- Allowed adapter paths can import only the public module.
- A neutral package importing GapDB fails.
- Any package importing `internal/server` fails.
- Historical SQLite code is not misclassified as new M1 fallback.

**Exit evidence**: `go test ./internal/architecture -run M1Dependency`.

## T004: Add architecture-guard mutant controls

**Purpose**: Prove each guard detects the defect it claims to prevent.

**Files**: Extend the two new guard test files.

**Implementation requirements**:

1. Test guards against temporary synthetic source trees or pure classifiers.
2. Include mutants for allowlist widening, `internal/server`, SQLite fallback,
   current-batch revision fill-in, global authority CAS, shared projection head,
   runtime-coordinate conflation, and incomplete evidence signature.
3. Require every mutant to fail for its intended architectural reason.
4. Include a clean control beside each mutant to avoid always-failing guards.
5. Keep mutants local and deterministic; do not modify the working tree.
6. Give mutant IDs compatible with the mission evidence vocabulary.

**Exit evidence**: `go test ./internal/architecture -run 'M1.*Mutant'`.

## T005: Run the frozen-contract qualification slice

**Purpose**: Hand downstream WPs a trustworthy red/green boundary.

**Validation**:

1. Run `go test ./internal/architecture`.
2. Run `go test ./... -short` to detect unintended repository coupling.
3. Run `go vet ./internal/architecture`.
4. Confirm `git diff --check` is clean.
5. Confirm only owned files changed.
6. Record exact commands and results in the WP handoff; do not create an M1
   evidence manifest and do not claim runtime qualification.

## Review guidance

The reviewer should deliberately add one forbidden-import mutant and one
vacuous-evidence mutant to a temporary fixture and see both fail. Reject the WP
if a guard relies on filenames alone, if an approved contract was rewritten to
fit a test, or if product budgets merely equal the GapDB runtime ceilings.

## Completion criteria

- Every named contract and boundary has a positive and negative control.
- Existing honest failure evidence remains representable.
- No production package, module dependency, or frozen contract changed.
- The result is a guardrail, not a claim that M1 has passed.

