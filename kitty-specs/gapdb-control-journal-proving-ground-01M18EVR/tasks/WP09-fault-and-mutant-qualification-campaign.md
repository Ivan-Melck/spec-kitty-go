---
work_package_id: WP09
title: Fault and Mutant Qualification Campaign
dependencies:
- WP06
- WP07
- WP08
requirement_refs:
- C-003
- C-012
- C-013
- C-014
- FR-006
- FR-029
- FR-039
- FR-040
- FR-041
- NFR-001
- NFR-002
- NFR-003
- NFR-004
- NFR-005
- NFR-006
- NFR-007
- NFR-008
- NFR-009
- NFR-010
- NFR-015
- NFR-016
- NFR-017
planning_base_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
merge_target_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
branch_strategy: Planning artifacts for this mission were generated on kitty/mission-gapdb-control-journal-proving-ground-01M18EVR. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into the mission branch; only the accepted mission may merge to main.
subtasks:
- T047
- T048
- T049
- T050
- T051
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/testcampaign/m1/faults/
create_intent:
- internal/testcampaign/m1/faults/runner.go
- internal/testcampaign/m1/faults/runner_test.go
- internal/testcampaign/m1/focused/campaign.go
- internal/testcampaign/m1/focused/campaign_test.go
- internal/testcampaign/m1/mutants/catalog.go
- internal/testcampaign/m1/mutants/catalog_test.go
- internal/testcampaign/m1/invariants.go
- internal/testcampaign/m1/invariants_test.go
- internal/provingground/qualification.go
- internal/provingground/qualification_test.go
- internal/gitrepo/workspace.go
- internal/gitrepo/workspace_test.go
- internal/gitrepo/integration.go
- internal/gitrepo/integration_test.go
- internal/gitrepo/inspection.go
- internal/gitrepo/inspection_test.go
- internal/provingground/review.go
- internal/provingground/review_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/testcampaign/m1/faults/runner.go
- internal/testcampaign/m1/faults/runner_test.go
- internal/testcampaign/m1/focused/campaign.go
- internal/testcampaign/m1/focused/campaign_test.go
- internal/testcampaign/m1/mutants/catalog.go
- internal/testcampaign/m1/mutants/catalog_test.go
- internal/testcampaign/m1/invariants.go
- internal/testcampaign/m1/invariants_test.go
- internal/provingground/qualification.go
- internal/provingground/qualification_test.go
- internal/gitrepo/workspace.go
- internal/gitrepo/workspace_test.go
- internal/gitrepo/integration.go
- internal/gitrepo/integration_test.go
- internal/gitrepo/inspection.go
- internal/gitrepo/inspection_test.go
- internal/provingground/review.go
- internal/provingground/review_test.go
role: implementer
tags:
- m1
- fault
- campaign
- mutants
tracker_refs: []
---

# Work Package Prompt: WP09 - Fault and Mutant Qualification Campaign

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Build the deterministic qualification machinery that attacks the completed M1
slice at every ratified fault boundary, repeats the eight critical concurrency
and recovery campaigns, kills the exact nineteen architectural mutants, and
asserts universal invariants even when a scenario fails. This WP creates honest
observations; it does not decide production adoption.

## Counting and evidence laws

1. Five fault records, eight focused summaries, nineteen mutant results, and
   fourteen external scenarios are different censuses and never conflated.
2. Every started attempt reaches a terminal pass/fail/incomplete record.
3. Each focused campaign starts, terminates, and passes at least 100 repetitions.
4. Mutants are executable negative controls, not prose claims.
5. Universal invariants run after every terminal attempt, including failures.
6. Fault injection is boundary/ack driven, not sleep-duration driven.
7. A non-admissible environment produces honest incomplete evidence.
8. No isolated campaign substitutes for WP10's combined ordered trial.

## Branch and ownership

Begin from accepted WP06–WP08. Edit only the eight testcampaign files, two
additive proving-ground qualification seam files, six Git-repository files,
and two proving-ground review files listed. The seam may expose read-only
production facts and existing fault hooks; it must not add a testcampaign
dependency or a test-only mutation path. The Git files own the cross-process
worktree-metadata lock and its direct regressions discovered by the NFR-003
campaign. The review files preserve current-head validation through
`Runtime.Authorize` while allowing a heartbeat-refreshed capsule to retain its
immutable lease-scoped workspace binding. WP10 owns aggregate campaign/evidence/CLI
files; do not create them here.

## T047: Implement five deterministic fault-cut runners

**Files**: Create `faults/runner.go` and `runner_test.go`.

**Required cuts**:

1. `GJ-01` / `before_request_publication`
2. `GJ-02` / `after_durable_acceptance_before_client_response`
3. `GJ-03` / `after_workspace_creation_before_durable_binding`
4. `GJ-04` / `after_durable_binding_before_run_capsule_response`
5. `GJ-05` / `after_effect_intent_before_observation`

**Implementation requirements**:

- Arm and trigger cuts through explicit acknowledgements from the component
  crossing the boundary; reject untriggered or multiply triggered attempts.
- Capture RunID, fault ID, started/terminal/outcome, exact cut, product facts,
  source binding, and bounded artifacts.
- GJ-01 compares operation-attributable record absence and exact preservation of
  all preexisting revisions/digests, not global emptiness.
- GJ-02 must verify the real proxy consumed a complete daemon response.
- GJ-03/GJ-04 record physical/binding/capsule facts distinctly.
- GJ-05 records intent and provider observation separately.

**Tests**:

- Every cut can be triggered once and terminates.
- Wrong cut order and fake timeout fail.
- Fail/incomplete records remain honest and schema-shaped.

**Exit evidence**: `go test ./internal/testcampaign/m1/faults`.

## T048: Implement eight focused campaigns with at least 100 repetitions

**Files**: Create `focused/campaign.go` and `campaign_test.go`.

**Required stable IDs**:

1. `atomic-publication-nfr001`
2. `lost-response-idempotency-nfr002`
3. `concurrent-claims-nfr003`
4. `competing-integrations-nfr004`
5. `restore-playback-nfr005`
6. `restart-epoch-nfr007`
7. `zombie-isolation-nfr008`
8. `attribution-reassignment-nfr015`

**Implementation requirements**:

- Run every campaign with deterministic seed capture and at least 100 attempts.
- Require started == terminal == passed for an admissible summary.
- Run campaigns against the real sidecar/composed production slice where the
  requirement is external; do not replace it with a more capable fake.
- Vary scheduling through explicit barriers and randomized seeded order, not
  wall-clock sleeps.
- Assert local universal invariants on every repetition before aggregation.
- Preserve first failure artifacts and bounded diagnostic context.

**Exit evidence**: `go test ./internal/testcampaign/m1/focused`.

## T049: Kill the exact nineteen architectural mutants

**Files**: Create `mutants/catalog.go` and `catalog_test.go`.

**Exact mutant IDs**:

1. `split-batch`
2. `pre-ack-publish`
3. `retry-new-operation-id`
4. `revision-fill-in`
5. `server-revision-in-value-or-key`
6. `invalid-event-order`
7. `current-batch-checkpoint`
8. `global-authority-cas`
9. `shared-projection-head`
10. `runtime-coordinate-conflation`
11. `workspace-reuse`
12. `same-contributor-review`
13. `invalid-checkpoint`
14. `playback-adapter-or-write`
15. `watch-as-journal`
16. `misattributed-public-fields`
17. `fake-timeout`
18. `gapdb-import-allowlist-or-internal-server`
19. `incomplete-signature`

**Implementation requirements**:

- Give each mutant an executable defect fixture and the precise guard/test that
  kills it; do not mutate the repository working tree.
- Require a clean control through the same seam.
- A killed result records RunID, stable ID, guard identity, and bounded reason.
- Survived/not-run mutant makes aggregate evidence non-pass.

**Exit evidence**: `go test ./internal/testcampaign/m1/mutants`.

## T050: Assert universal invariants on every terminal attempt

**Files**: Create `invariants.go` and `invariants_test.go`.

**Required assertions**:

1. Journal record/event/head integrity and operation atomicity.
2. At most one current authority per WorkRevision.
3. Workspace binding uniqueness and no expired-workspace transfer.
4. Current RuntimeEpochID consistency and old-capsule refusal after restart.
5. Candidate contributor/reviewer ActorID independence.
6. Serialized integration authority and Git-fact consistency.
7. Attribution-safe effect reconciliation.
8. Stable restored/playback digest semantics and zero playback I/O.

**Implementation requirements**:

- Accept an observation port rather than test-private internal state.
- Run after pass, fail, and incomplete terminal paths where state is inspectable.
- Expose individual booleans compatible with evidence schema plus detailed local
  diagnostics; never turn inability to inspect into true.
- Include `RequireStaleProbe`-equivalent checks for stale lease/epoch capsules.

**Exit evidence**: `go test ./internal/testcampaign/m1 -run Invariant`.

## T051: Verify honest incomplete/failure evidence under faults

**Validation**:

1. Simulate daemon unavailable, wrong version, provenance mismatch, provider
   outage, killed runner, and invariant violation.
2. Require every case to terminate as fail/incomplete with no pass signature.
3. Run all campaign packages repeatedly and under race detector.
4. Run `go test ./... -short`, vet, WP01 guards, and `git diff --check`.
5. Record runtime duration/resource bounds for WP10 without weakening campaigns.

## Review guidance

The reviewer should disable one invariant call, make one mutant a no-op, and
shorten one campaign to 99 repetitions; all must prevent pass. Reject sleep-based
fault cuts, fakes that always tell the truth, or aggregate counts that combine
unlike evidence categories.

## Completion criteria

- Five cuts, eight campaigns, and nineteen mutants are exact and executable.
- Universal invariants accompany all terminal observations.
- Honest failure/incomplete evidence is retained.
- Nothing here claims the combined trial passed.
