---
work_package_id: WP01
title: Bound public transport
dependencies: []
requirement_refs:
- FR-001
- FR-008
- FR-009
- FR-013
- NFR-001
- NFR-004
- C-001
- C-003
planning_base_branch: feat/hubnot-tracker-replacement
merge_target_branch: feat/hubnot-tracker-replacement
branch_strategy: Planning artifacts for this mission were generated on feat/hubnot-tracker-replacement. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/hubnot-tracker-replacement unless the human explicitly redirects the landing branch.
base_branch: kitty/mission-hubnot-tracker-replacement-01M22QEJ
base_commit: c10593a91e24d746e4d8920510c43f072bdfa01e
created_at: '2026-09-09T10:26:08.863919+00:00'
subtasks:
- T001
- T002
- T003
history: []
agent_profile: implementer-ivan
authoritative_surface: internal
create_intent:
- internal/tracker/hubnot/adapter.go
- internal/tracker/hubnot/adapter_test.go
- internal/tracker/hubnot/binding.go
- internal/tracker/hubnot/binding_test.go
- internal/tracker/hubnot/transport.go
- internal/tracker/hubnot/transport_test.go
- internal/tracker/hubnot/protocol.go
- internal/tracker/hubnot/protocol_test.go
- internal/tracker/hubnot/errors.go
- internal/tracker/hubnot/errors_test.go
- internal/tracker/hubnot/testdata/protocol_child.go
- internal/tracker/hubnot/testdata/protocol_cases.json
execution_mode: code_change
owned_files:
- internal/tracker/hubnot/adapter.go
- internal/tracker/hubnot/adapter_test.go
- internal/tracker/hubnot/binding.go
- internal/tracker/hubnot/binding_test.go
- internal/tracker/hubnot/transport.go
- internal/tracker/hubnot/transport_test.go
- internal/tracker/hubnot/protocol.go
- internal/tracker/hubnot/protocol_test.go
- internal/tracker/hubnot/errors.go
- internal/tracker/hubnot/errors_test.go
- internal/tracker/hubnot/testdata/protocol_child.go
- internal/tracker/hubnot/testdata/protocol_cases.json
- internal/tracker/capabilities.go
- internal/tracker/capabilities_test.go
- internal/architecture/policy.go
- internal/architecture/policy_test.go
role: implementer
tags: []
tracker_refs: []
---

# WP01 — Bound public transport

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Bind one explicitly configured canonical provider workspace and public signing actor to
verified source/binary/schema identities. Supply reusable bounded public command
transport without exposing issue history as Go Kitty authority.

## Context

Read this mission's reviewed `plan.md`, `spec.md`, `data-model.md` and the
provider, consumer and qualification files under `contracts/`. The final
reviewed provider contract overrides provisional upstream research wording.
The work package implements concern IC-01; its declared requirement references
are acceptance obligations, not permission to broaden its write ownership.

Open returns a bound transport. No future Catalog, Graph, Discussion or Inspector method
assertion is permitted. Classify the new adapter and consumer/qualification boundaries
before dependent packages introduce their files. Retain permanent historical Beads
prohibitions; never allow adapter imports into inward authority.

The runtime chooses the worktree and dependency base; do not invent a base branch.
Start only after root dispatch and required dependency approvals:

```bash
spec-kitty agent action implement WP01 --agent codex --mission hubnot-tracker-replacement-01M22QEJ
```

Only frontmatter-owned files may be staged. New paths are explicitly marked
with create_intent; report required ownership corrections before editing another
package's file. Do not change frozen missions, receipts, real provider data,
old stashes/branches, or internal/helpercmd/production.go.

### Subtask T001: Binding and architecture refusal sensors

**Purpose**: Bind exact project, workspace, executable and public signer before any mutation.

**Steps**:

1. Read the independently accepted upstream contract and tuple; if either is pending,
   report the prerequisite instead of accepting feature presence on a mutable branch.

2. Write failing tests for relative paths, symlink identity changes, binary digest
   mismatch, malformed actor fingerprint, wrong project, legacy binding and read-only
   policy.

3. Decode binding go-kitty.hubnot-binding/1 with duplicate/unknown-key and
   invalid-Unicode rejection; require absolute existing paths without PATH or
   current-directory discovery.

4. Inspect only public hn identity show --json; validate actor, name and public key
   against reviewed schema. Never inspect a private key file or initialize a repository.

5. Add git_native only if the neutral enum needs it; preserve existing enum and
   capability meanings, with concurrent writers declared none.

6. Classify exact Hubnot adapter prefix in architecture policy and extend rejecting
   fixtures for adapter-to-authority and authority-to-provider edges.

7. Keep the old active provider policy valid until cutover; historical forbidden Beads
   modules remain useful negative coverage.

8. Validate targeted binding and architecture fixtures before introducing transport
   calls.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T002: Strict bounded subprocess transport

**Purpose**: Execute only the accepted public argv with monotonic outcome accounting.

**Steps**:

1. Write a fake-child test before implementation for an endless stdout producer,
   overflowing stderr, timeout, cancellation and nonzero exit with valid typed error
   JSON.

2. Use exec argv without a shell, one fixed workspace/executable and an allowlisted
   environment; do not inherit ambient alternate Git repository or provider credentials.

3. Enforce child stdout 8 MiB, stderr 64 KiB and 30 seconds; on cancellation terminate
   and reap the entire child work under the accepted process policy.

4. Reject encoded mutation/native-limit violations before starting a child; no
   truncation and no temporary input file in the user repository.

5. Decode exactly one complete strict envelope, retaining valid nonzero typed errors and
   original child diagnostics as bounded causes.

6. Classify unstarted, applied and ambiguous monotonically; a malformed response after
   dispatch is not evidence of no write.

7. Pass the expected actor on every mutation and revalidate executable identity at
   dispatch; signer mismatch must be refused at signing, not only preflight.

8. Exercise malformed UTF-8, unpaired escapes, duplicate nested fields, trailing JSON,
   wrong schema and contradictory ok/error payloads.

**Files**: Use only the frontmatter files matching this subtask’s boundary.
Keep production helpers cohesive and tests beside their owned implementation.
Estimate the change from the existing contracts before coding; if it cannot
fit this ownership without a new cross-package dependency, notify root.

**Validation**: Capture the failing behavioral regression or acceptance fixture
before implementation, then run the targeted tests after the actual change.
For refusals, check subprocess/ref/control-write counters or exact snapshots;
an error string alone does not prove no mutation. Record the test names and
exit results with the implemented behavior for independent review.

### Subtask T003: Protocol contracts and downstream handoff

**Purpose**: Provide stable wire types and public primitive calls for later packages.

**Steps**:

1. Implement only independently reviewed identity, detailed-list, issue-read,
   operation-lookup and mutation command shapes; keep signed-event/envelope/owned
   metadata schema versions separate.

2. Operation lookup must retain full event ID, original
   actor/key/request/parents/state/body and completeness semantics; absence with
   incomplete history is not definitive absence.

3. Detailed-list wire types must accommodate accepted opening_metadata proof; this is
   required by the reviewed provider contract.

4. Create shared logical budget plumbing for 120 seconds, cumulative 64 MiB and 10,000
   items across initial attempt plus at most two restarts.

5. Public command helpers return structured raw facts rather than calling future catalog
   or graph methods.

6. Run transport and binding race tests with no real private identity fixture; compile
   all packages externally.

7. Provide exact function/type handoff to WP02/WP03 and report any accepted-upstream
   spelling discrepancy to root.

8. Record targeted command exits and architecture result; do not claim complete provider
   inspection until WP06.

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
spec-kitty agent tasks mark-status T001 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T002 --status done --mission hubnot-tracker-replacement-01M22QEJ
spec-kitty agent tasks mark-status T003 --status done --mission hubnot-tracker-replacement-01M22QEJ
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
