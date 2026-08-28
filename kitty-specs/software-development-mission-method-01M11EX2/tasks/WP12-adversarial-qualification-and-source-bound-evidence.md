---
work_package_id: WP12
title: Adversarial Qualification and Source-Bound Evidence
dependencies:
- WP01
- WP10
- WP11
requirement_refs:
- FR-003
- FR-009
- FR-010
- FR-011
- FR-012
- FR-013
- FR-015
- FR-016
- FR-017
- FR-018
- FR-019
- FR-020
- FR-021
- FR-022
- FR-023
- FR-025
- FR-027
- FR-028
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
- NFR-011
- NFR-012
- NFR-013
- NFR-014
- C-001
- C-002
- C-005
- C-006
- C-007
- C-008
- C-009
- C-010
- C-011
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T061
- T062
- T063
- T064
- T065
- T066
- T067
history: []
agent_profile: debugger-debbie
authoritative_surface: internal/scenario/
create_intent:
- internal/scenario/mission_method_adversarial_test.go
- internal/scenario/mission_method_fault_test.go
- internal/scenario/mission_method_race_test.go
- internal/scenario/mission_method_projection_test.go
- internal/testcampaign/mission_method.go
- internal/testcampaign/mission_method_test.go
- docs/phase3-evidence/README.md
- docs/phase3-evidence/qualification-evidence.json
- docs/phase3-evidence/campaign-census.json
- docs/phase3-evidence/results.md
- docs/phase3-evidence/limitations.md
- docs/phase3-evidence/SHA256SUMS
execution_mode: code_change
model: ''
owned_files:
- internal/scenario/mission_method_adversarial_test.go
- internal/scenario/mission_method_fault_test.go
- internal/scenario/mission_method_race_test.go
- internal/scenario/mission_method_projection_test.go
- internal/scenario/review_test.go
- internal/scenario/integration_test.go
- internal/scenario/campaign_test.go
- internal/testcampaign/mission_method.go
- internal/testcampaign/mission_method_test.go
- internal/testcampaign/census.go
- internal/testcampaign/census_test.go
- internal/testcampaign/cmd/campaign/main.go
- internal/testcampaign/cmd/campaign/main_test.go
- internal/helpercmd/review.go
- internal/helpercmd/review_test.go
- internal/helpercmd/integration.go
- internal/helpercmd/integration_test.go
- internal/controlstore/sqlite/handoff_review.go
- internal/controlstore/sqlite/handoff_review_test.go
- Makefile
- .github/workflows/quality.yml
- .github/workflows/full-campaign.yml
- README.md
- docs/phase3-evidence/README.md
- docs/phase3-evidence/qualification-evidence.json
- docs/phase3-evidence/campaign-census.json
- docs/phase3-evidence/results.md
- docs/phase3-evidence/limitations.md
- docs/phase3-evidence/SHA256SUMS
role: investigator
tags: []
tracker_refs: []
---

# Work Package Prompt: WP12 - Adversarial Qualification and Source-Bound Evidence

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `debugger-debbie`
- **Role**: `investigator`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Qualify the Phase 3 software-development mission method against its hardest safety, concurrency, recovery, independence, privacy, architecture, and delivery claims, then publish an honest source-bound evidence package.

The completed work must prove 1,000 semantic drift mutations, 100 two-process finalization races, crash recovery at every declared persistence boundary, 100 projection independence trials, 100 governance-history trials, exact fast/slow campaign censuses, the full Go quality ladder, and load-bearing mutant controls. A failed, dirty, incomplete, prerequisite-missing, census-mismatched, source-drifted, or interrupted run remains recorded as failed or `non_admissible`; it can never be relabeled as qualification evidence.

Implementation command:

```bash
spec-kitty agent action implement WP12 --agent codex
```

## Context

WP01 supplies the permanent architecture, deferred-surface, and reachability guard. WP10 supplies the complete durable-fake vertical slice and typed qualification helper. WP11 supplies qualified Beads mission-method parity. Rebase this lane onto all three accepted lineages before adding campaigns.

The repository already has a mature campaign runner with exact top-level test censuses, universal invariant requirements, stale-authority probes, bounded artifact serialization, and separate quality versus full-campaign workflows. Extend those mechanisms. Do not add a second shell-only test registry or replace exact census checks with a broad `go test ./...` claim.

The WP12 preflight identified two ownership omissions. First, the existing shared census verifier treats a required skip as a successful terminal result and does not expose the distinct counts required by T065, so the governed scope includes `internal/testcampaign/census.go` and `internal/testcampaign/census_test.go`. Second, configured timeout and deadline-exhaustion state exist only in the campaign command, so `internal/testcampaign/cmd/campaign/main.go` and its test are included to serialize that distinction through the authoritative runner.

The first clean qualification attempt then exposed a shared helper-composition defect rather than a Beads-specific fixture defect. Handoff, review, and integration helpers auto-select Phase 3 complete eligibility when backed by SQLite, but their command formats did not bind the expected eligibility fact; frozen v1 scenarios therefore refused before reaching their intended stale-authority assertions. The governed scope includes the three helper command implementations and tests plus their two shared scenario argument builders. Complete mode remains the default. Explicit `legacy-v1` mode is accepted only after the helper proves the v2 revision is typed-absent and the exact frozen v1 revision exists; a present v2 revision can never downgrade. Historical evidence artifacts and transactional authority decisions remain unchanged.

The first exact full-campaign attempt then exposed a repository-wide census contract that still equated the frozen historical `full-scenario` profile with every top-level scenario test and hard-coded the superseded 12-minute workflow timeout. The governed scope therefore includes `internal/scenario/campaign_test.go`. Its census must compare the complete scenario inventory against the canonical union of the frozen historical profile, the provider-adversarial profile, and the Phase 3 mission profiles, with explicit missing/extra mutant controls; it must not inflate or semantically rewrite the historical profile. Its workflow assertion must match the ratified 15-minute bounded timeout.

The exact historical campaign then proved that an invalidated v2 reconciliation could mask a stale handoff/review lease with `revision_ineligible` before the mandatory stale-authority probe ran. Handoff has an existing read-only authority inspection, but review's existing inspection persists an attributed attempt and does not check lease authority. The governed scope therefore includes `internal/controlstore/sqlite/handoff_review.go` and its focused test. Add only the missing read-only review-authority probe: it must bind the immutable handoff, revision, work, candidate, lease, generation, token digest, lease status, and deadline; it must make no attempt write or eligibility decision. Helper replay remains first, the read-only stale probe precedes eligibility, and the service retains its authoritative transactional reinspection/CAS. Do not weaken stale-probe expectations, downgrade a present v2 revision, or move eligibility behind repository-fence/Git mutation. These thirteen supporting test/campaign/helper paths are the complete governed amendment.

Evidence attaches to the exact tested source commit and tree, not to a branch name, nearby documentation, or a later commit. The evidence payload and any following docs-only attestation must distinguish:

- the frozen implementation source that was tested;
- the generated machine artifacts and their checksums;
- the commit that publishes those artifacts;
- later documentation-only attestations; and
- every failed, superseded, or non-admissible attempt.

No later commit inherits a verdict automatically. Any product, authority, provider, protocol, campaign, or evidence-serializer change requires a new frozen candidate and new evidence.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated to WP12. Verify accepted WP01, WP10, and WP11 lineages are present. Work toward one frozen committed candidate; qualification runs against committed, clean source, after which evidence payload publication is a separately identified step. Merge the reviewed result only into `feat/software-development-mission-method`.

Do not edit the primary checkout, create worktrees manually, merge another lane, push to `main`, or modify files beyond the sixteen owned paths. Preserve unrelated user changes. Do not clean, reset, or stash a dirty tree to manufacture admissibility.

## Evidence Laws

1. Expected, started, terminal, passed, and failed counts are explicit for every profile.
2. A profile passes only when expected equals started equals terminal equals passed and failed is zero.
3. Universal authority invariants and stale-authority probes remain mandatory where execution state is present.
4. A source commit/tree mismatch makes a run non-admissible even if all tests pass.
5. A dirty source tree makes a run non-admissible and the dirty paths are bounded/redacted in evidence.
6. Missing prerequisites, skipped required scenarios, unavailable external provider, or partial census cannot become pass.
7. Failed and superseded artifacts are retained and described; never overwrite them with a later pass.
8. Evidence excludes credentials, environment dumps, absolute home paths, private transcript text, raw provider databases, unrestricted stderr, and raw test streams.
9. Machine records distinguish absent, unknown, not measured, failed, and zero; `null` is not rewritten as `0`.
10. Documentation claims must map to an executable test and machine record or be labeled a limitation/non-claim.

### Subtask T061: Run 1,000 semantic drift mutations with zero silent change

**Purpose**: Attack the claim that a finalized WorkRevision remains immutable while every later semantic provider edit is detected and explicitly reconciled.

**Steps**:

1. Build deterministic generated cases from the finalized two-item and larger provider-neutral graphs.
2. Cover addition, removal, replacement, and modification across title, description, design context, criteria identity/text/verifier/required flag, dependencies, write scopes, mapping identity, metadata schema, provider canonical content, and stable WorkID relationships where legal to observe.
3. Include equal provider timestamps with different content and changed provider revisions with identical semantic content.
4. Distinguish `changed` semantic drift from `observation_only` provider noise and `clean` equivalence.
5. For each semantic mutation, assert generation 1 bytes, graph digest, criteria digest, current pointer, and downstream authority evidence remain unchanged before reconciliation.
6. Require one structured drift report naming exact affected old/new WorkIDs and canonical field changes.
7. Require explicit reconciliation before any candidate successor becomes current.
8. Include removal mutants for each field comparator so omitting one comparison causes at least one generated case to fail.
9. Record the deterministic seed, generator/schema version, field-kind census, expected/started/terminal counts, and zero-silent-drift result.
10. Repeat normalization/finalization 100 times over equivalent shuffled input and require byte-equivalent canonical content and one digest.
11. Avoid probabilistic-only coverage: retain a mandatory fixed case for every semantic field before generated permutations.
12. Keep runtime bounded for scheduled use and emit progress through structured test events rather than unbounded logs.

**Files**:

- `internal/scenario/mission_method_adversarial_test.go`
- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`

**Validation**:

- Exactly 1,000 semantic mutations yield exactly 1,000 changed drift results and zero current-generation mutations.
- Provider-revision-only cases remain observation evidence without demanding dispositions.
- Every deleted comparator mutant is killed by its named fixed/generated case.

### Subtask T062: Run 100 two-process finalization races

**Purpose**: Prove the SQLite control authority publishes exactly one current generation under real process contention and never leaves partial generations.

**Steps**:

1. Use two independent processes or the existing helper subprocess seam with separate SQLite connections to the same temporary database.
2. Synchronize each pair at a deterministic barrier immediately before expected-generation publication; do not rely on scheduler luck or sleeps.
3. Run at least 100 races from expected generation zero using valid, fully observed candidates.
4. Include same-operation/same-request races and different-operation/different-candidate races.
5. Require exactly one current generation after every race.
6. Require losing outcomes to be typed replay or conflict according to operation/request identity.
7. Assert no duplicate current pointer, orphan successor, partial provider-observation set, partial lane graph, missing action provenance, or extra generation number.
8. Close all handles, restart the store, and repeat authority assertions after every iteration or a justified bounded batch.
9. Record contention attempts, winners, replays, conflicts, unexpected errors, and persistence audits without recording secrets or database bytes.
10. Add mutants that remove expected-generation CAS, weaken operation-digest comparison, or publish the pointer before complete revision state.
11. Run focused race detection over mission/workgraph/control-store packages in addition to process contention.
12. Make barrier failure a campaign failure, not a skipped race.

**Files**:

- `internal/scenario/mission_method_race_test.go`
- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`

**Validation**:

- All 100 races publish exactly one current generation.
- Every loser is a deterministic replay or typed conflict.
- Restart audits find zero partial, orphaned, duplicated, or mismatched records.

### Subtask T063: Inject crashes at every mission publication boundary

**Purpose**: Verify recovery produces either one complete prior state or one complete successor state across local transactions and provider sagas.

**Steps**:

1. Census named fault points around artifact approval, decomposition provider application/observation, WorkRevision finalization, drift-report persistence, and reconciliation successor publication.
2. Include pre-write, mid-transaction, post-commit/pre-response, and replay/recovery boundaries where each service exposes them.
3. Use subprocess termination or the existing file-controller fault seam for process-realistic crash boundaries.
4. After each injected crash, close/reopen SQLite and the durable fake provider before recovery.
5. For local aggregate transactions, require either the complete prior state or one complete successor state, never partial rows or mixed pointers.
6. For decomposition provider effects, preserve honest saga semantics: an externally applied mutation may survive; recovery must observe attribution and resume/reconcile without duplicate work or unsafe compensation.
7. For timeout/crash after provider apply, compare provider revision, value, and operation marker before compensating or retrying.
8. For reconciliation, require generation 1 or complete generation 2 plus every disposition/consequence; never a successor lacking decisions.
9. Record every fault ID in an exact manifest with expected, started, terminal, and verdict fields.
10. Add a mutant deleting each recovery probe or moving the current-pointer write before its dependent evidence.
11. Preserve Phase 1 universal authority invariants whenever synthetic downstream statuses are involved.
12. Treat an unreachable fault point as a failed census, not proof that the boundary is safe.

**Files**:

- `internal/scenario/mission_method_fault_test.go`
- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`

**Validation**:

- Every named boundary is reached and terminates.
- Restart returns only complete prior or complete successor aggregate state.
- Provider-side survival is reported as saga recovery, never mislabeled atomic rollback.

### Subtask T064: Run 100 projection and governance immutability trials

**Purpose**: Prove derived files and mutable governance sources cannot rewrite canonical mission history.

**Steps**:

1. For projections, generate a source-bound `tasks.md`, then alternate deletion, truncation, arbitrary edit, checkbox flip, stale-copy restore, invalid UTF-8 bytes, symlink/path attack where safe to test, and replacement with another mission's rendering.
2. Run at least 100 isolated projection trials across generation 1 and generation 2.
3. Before and after each trial, compare mission state/version, artifacts/approvals, tracker mappings, WorkRevision bytes/digests, decisions/blockers, drift/reconciliation records, and pure `next` results.
4. Require zero canonical mutations and prove there is no production import/apply path.
5. For governance, create an action binding/capsule, then change profile/doctrine/config/mission-type source revisions or construct newer contexts.
6. Run at least 100 governance-history trials and reconstruct persisted actions after restart.
7. Require every historical binding to retain the original refs, revisions, commitments, aggregate digest, actor, and authority grant.
8. Ensure unrelated doctrine/profile content was never captured and privacy scans find no source bodies, credentials, environment dumps, or transcript text.
9. Add mutants that parse projection checkbox state, resolve governance by current mutable path, or omit one commitment from the aggregate.
10. Include 100 repeated `next` queries with state-write counters to reinforce query purity.
11. Record trial-kind censuses and mutant results as machine facts.
12. Use independent temporary roots so a prior corrupted projection cannot contaminate the next case.

**Files**:

- `internal/scenario/mission_method_projection_test.go`
- `internal/scenario/mission_method_adversarial_test.go`
- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`

**Validation**:

- All 100 projection trials produce zero authoritative changes.
- All 100 governance trials preserve exact historical interpretation.
- Every projection/governance authority mutant is killed.

### Subtask T065: Add exact mission profiles, fast/slow separation, and bounded CI

**Purpose**: Keep pull-request feedback usable without allowing expensive safety campaigns to disappear from delivery evidence.

**Steps**:

1. Register exact top-level tests for `mission-contract`, `mission-acceptance`, and `mission-adversarial` profiles in the existing campaign model.
2. Keep the PR path limited to deterministic contract, architecture, portable fake, bounded acceptance, and focused race checks that demonstrably complete under 10 minutes.
3. Place 1,000-mutation, 100-process-race, full crash, real Beads, and long soak work in scheduled/on-demand slow profiles unless measurement proves they fit the bounded path.
4. Define a full Phase 3 campaign requiring every fast and slow profile, universal invariants, stale probes where applicable, and exact required-test identities.
5. Add census mutant tests that remove, rename, skip, duplicate, or add an uncatalogued top-level test and require validation failure.
6. Ensure expected, started, terminal, passed, failed, skipped, duration, timeout, and source identity are serialized distinctly.
7. Add Make targets `mission-contract`, `mission-acceptance`, and `mission-adversarial`, and include the bounded profiles in `quality` without weakening existing targets.
8. Extend the SHA-pinned quality workflow with least privileges, isolated mutable paths, explicit timeout headroom, always-upload evidence, and a measured campaign duration target below 10 minutes.
9. Extend the scheduled/manual full workflow for exhaustive campaigns; never add it to every PR merely to make a documentation claim simple.
10. Preserve offline portable operation and the stricter pinned/offline Beads qualification prerequisite boundary.
11. Record hosted/local durations as separate observations; missing hosted baselines remain pending/null.
12. Test workflow and Makefile contracts so future deletion of a gate fails the architecture/quality census.

**Files**:

- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`
- `internal/testcampaign/census.go`
- `internal/testcampaign/census_test.go`
- `internal/testcampaign/cmd/campaign/main.go`
- `internal/testcampaign/cmd/campaign/main_test.go`
- `internal/scenario/campaign_test.go`
- `Makefile`
- `.github/workflows/quality.yml`
- `.github/workflows/full-campaign.yml`

**Validation**:

- Exact censuses reject missing, extra, skipped, or unterminated tests.
- The measured bounded local PR-equivalent run completes within 10 minutes or is honestly marked failing/pending rather than waived.
- Slow campaigns remain executable, scheduled/manual, and required for full qualification.

### Subtask T066: Run the complete quality, race, security, and reproducibility ladder

**Purpose**: Establish that the qualified candidate is not only behaviorally correct but buildable, race-safe, statically clean, dependency-verified, and privacy-reviewed.

**Steps**:

1. Freeze a committed candidate and record full commit, tree, Go version, GOOS/GOARCH, campaign schema, and clean-source status before executing gates.
2. Run formatting checks and repository-wide compilation before expensive campaigns.
3. Run unit/contract, architecture, scope, portable provider, mission acceptance, mission adversarial, fault, race, and full scenario profiles according to their fast/slow placement.
4. Run `go test -race` over mission, artifact, workgraph, governance, next, tracker fake/Beads applicable code, SQLite control-store, helper, and campaign packages with no waived finding.
5. Run `go vet ./...`, pinned `staticcheck ./...`, `go mod verify`, a clean `go.mod`/`go.sum` diff check, pinned `govulncheck ./...`, formatting, and binary builds.
6. Exercise Linux/amd64 as the qualification host and retain portable compilation evidence for other supported targets where configured.
7. Scan serialized evidence and documentation for absolute home paths, credentials, environment secrets, transcript text, raw provider content, and unrestricted command output.
8. Verify every JSON evidence artifact against its closed schema and bounded-size policy.
9. Record duration and exit status for every gate; absent measurement is `null`, not zero.
10. If source changes after freeze, mark every in-progress result non-admissible and restart from a new committed candidate.
11. Preserve failing artifacts before fixes; issue a new attempt identity rather than overwriting history.
12. Require clean reruns of affected profiles plus the full required qualification set after any product, campaign, or serializer correction.

**Files**:

- `Makefile`
- `.github/workflows/quality.yml`
- `.github/workflows/full-campaign.yml`
- `internal/testcampaign/mission_method.go`
- `internal/testcampaign/mission_method_test.go`
- `internal/helpercmd/review.go`
- `internal/helpercmd/review_test.go`
- `internal/helpercmd/integration.go`
- `internal/helpercmd/integration_test.go`
- `internal/scenario/review_test.go`
- `internal/scenario/integration_test.go`

**Validation**:

- Every required quality gate passes against one explicitly named clean candidate or is honestly documented as not yet qualified.
- Race, vet, staticcheck, module verification, vulnerability, formatting, and build results are all present and non-waived.
- Evidence privacy checks report zero prohibited disclosures.

### Subtask T067: Publish source-bound evidence, limitations, and mutant closeout

**Purpose**: Turn raw campaign artifacts into a verifiable technical record without extending claims beyond what ran.

**Steps**:

1. Create `qualification-evidence.json` as the bounded machine summary of source identity, host/toolchain, profile censuses, scale counts, universal-invariant audits, durations, and verdict.
2. Create `campaign-census.json` mapping every fast/slow/provider/architecture/security gate to exact tests, artifacts, source identity, and admissibility.
3. Create `results.md` mapping every FR/NFR/constraint/success criterion in scope to executable proof and measured result.
4. Retain a dedicated section for failed, superseded, dirty, interrupted, source-drifted, prerequisite-missing, and non-admissible attempts with their own immutable identities/checksums.
5. Create `limitations.md` listing host boundary, single-machine SQLite authority, Beads tuple limitations, lack of Phase 4/5 execution, lack of Phase 6 surfaces, no Gapdb adoption, performance measurement scope, and every pending external baseline.
6. Create the evidence index explaining payload versus tested-source versus publication/attestation commits and non-transitive verdicts.
7. Hash the immutable payload files in `SHA256SUMS`; avoid impossible self-hashing and document which index/attestation bytes sit outside the manifest.
8. Update the repository README with Phase 3 behavior, commands, qualified boundaries, evidence links, and explicit non-claims without calling the helper a production CLI.
9. Add documentation mutant controls for broken links, malformed Mermaid, commands that do not exist, claim/test mismatch, missing limitations, changed checksums, missing failed attempts, and falsified pass/non-admissible status.
10. Run checksum verification and schema validation from a clean checkout of the payload/publication commit.
11. After documentation-only corrections, record a separate attestation rather than claiming it retroactively changed the tested source.
12. Complete a final strict mutant audit: each load-bearing authority, drift, race, crash, projection, governance, census, privacy, and source-binding rule must have a named mutant that fails.
13. Do not fabricate commit IDs before they exist; use the established two-step payload then docs-only attestation pattern.
14. Preserve all raw admissible and non-admissible external artifacts outside the repository when privacy/size rules prohibit committing them, and publish their digests plus bounded summaries.

**Files**:

- `README.md`
- `docs/phase3-evidence/README.md`
- `docs/phase3-evidence/qualification-evidence.json`
- `docs/phase3-evidence/campaign-census.json`
- `docs/phase3-evidence/results.md`
- `docs/phase3-evidence/limitations.md`
- `docs/phase3-evidence/SHA256SUMS`
- `internal/scenario/mission_method_adversarial_test.go`
- `internal/testcampaign/mission_method_test.go`

**Validation**:

- `sha256sum --check docs/phase3-evidence/SHA256SUMS` passes.
- Every delivered claim resolves to a machine record and executable proof or is explicitly a limitation.
- Failed/non-admissible attempts remain visible and cannot satisfy a pass assertion.
- Documentation mutants fail for the exact missing or falsified evidence property.

## Implementation Discipline

Extend the existing scenario and `internal/testcampaign` machinery. Campaign code must execute production services and adapters through public seams; it must not recreate algorithms inside tests or merely compare literal fixture strings. Reuse the universal invariant and stale-probe contracts wherever applicable.

Prefer deterministic generators, fixed seeds, explicit barriers, injectable clocks, isolated temporary roots, and process-realistic restart. Do not use sleeps as synchronization, timing floors as correctness, shared user databases, network-dependent portable tests, or unbounded raw logs.

Evidence generation may read Git identity and clean status, but tests must not mutate Git history, push, or erase dirty work. The documentation payload follows the established two-step source/payload/attestation model so no file predicts its own containing commit.

## Test and Qualification Strategy

Use focused development loops before expensive campaigns:

```bash
go test ./internal/scenario -run 'MissionMethod.*(Drift|Projection|Governance)' -count=1
go test ./internal/scenario -run 'MissionMethod.*(Race|Fault)' -count=1
go test ./internal/testcampaign -run 'MissionMethod|Census|Mutant' -count=10
```

Exercise the new explicit profiles:

```bash
make mission-contract
make mission-acceptance
make mission-adversarial
```

Run the bounded pull-request-equivalent gate and capture its measured duration:

```bash
artifact_dir="$(mktemp -d /tmp/go-kitty3-phase3-quality.XXXXXX)"
make quality ARTIFACT_DIR="$artifact_dir"
```

Run exhaustive and static qualification from a frozen committed candidate:

```bash
artifact_dir="$(mktemp -d /tmp/go-kitty3-phase3-full.XXXXXX)"
make full-campaign ARTIFACT_DIR="$artifact_dir"
make race ARTIFACT_DIR="$artifact_dir"
make static ARTIFACT_DIR="$artifact_dir"
make dependencies ARTIFACT_DIR="$artifact_dir"
make fmt-check
make build
```

Run the real Beads profile only with its pinned executable/install evidence and offline isolation. Missing prerequisites must emit `non_admissible` and leave all portable evidence intact.

Finally verify payload integrity and repository hygiene:

```bash
(cd docs/phase3-evidence && sha256sum --check SHA256SUMS)
go test ./internal/architecture ./internal/testcampaign -count=10
go test ./... -run '^$' -count=1
git diff --check
git status --short
```

## Definition of Done

- Exactly 1,000 semantic mutations produce 1,000 drift results and zero silent current-generation changes.
- Equivalent normalization/finalization repeated 100 times yields one digest and byte-equivalent canonical content.
- Exactly 100 two-process finalization races yield one current generation each and only replay/conflict losers.
- Every declared artifact, decomposition, finalization, drift-persistence, and reconciliation crash boundary is reached and restart-safe.
- Exactly 100 projection corruption/deletion/edit trials cause zero authoritative changes.
- Exactly 100 governance-history trials retain original profile/doctrine/config/mission-type commitments.
- At least 100 repeated `next` queries perform zero writes.
- Fake and applicable Beads mission-method profiles pass without provider semantics entering the kernel.
- Architecture, deferred-surface, and reachability mutants remain load-bearing.
- Exact mission contract, acceptance, adversarial, fault, race, provider, architecture, and full campaign censuses pass.
- The measured bounded PR-equivalent campaign is under 10 minutes or is explicitly not qualified without waiver.
- Race detector, vet, staticcheck, module verification, dependency diff, vulnerability scan, formatting, and builds pass with no unexplained waiver.
- Evidence privacy scans find zero credentials, environment secrets, absolute home paths, transcript content, or unrestricted provider/tool output.
- Machine evidence names exact source commit/tree, clean state, toolchain/host, counts, duration, verdict, and artifact checksums.
- Failed, superseded, dirty, interrupted, and non-admissible attempts remain visible and cannot satisfy pass.
- Results and limitations make no Phase 4/5/6, distributed control-store, Gapdb adoption, or unmeasured performance claim.
- Every source-bound evidence file passes schema/checksum/documentation mutant controls.
- Only the twenty-nine frontmatter-owned paths are modified by implementation.
- T061 through T067 are recorded through the event-sourced status command:

  ```bash
  spec-kitty agent tasks mark-status T061 T062 T063 T064 T065 T066 T067 --status done --mission software-development-mission-method-01M11EX2 --json
  ```

## Risks

- **Pass-only history**: Overwriting failed attempts destroys auditability. Give every attempt its own identity and retain its verdict.
- **Source drift**: Tests can finish green after source changes underneath them. Bind and recheck commit/tree/clean state before admitting results.
- **Census theater**: Broad test commands can omit or silently skip critical cases. Require exact started and terminal events.
- **Fake concurrency**: Goroutines sharing one handle do not prove process contention. Use independent processes/connections and explicit barriers.
- **Fault gaps**: An injected label that is never reached is not evidence. Census reached boundaries.
- **Generated randomness**: Seed-only fuzzing may miss a whole field. Pair fixed field cases with deterministic generation.
- **Projection contamination**: Trials must compare all canonical aggregates, not merely the rendered file or one mission field.
- **Governance leakage**: Capturing source bodies or current paths both leaks information and weakens historical identity.
- **PR slowdown**: Putting every long campaign on every PR invites skipped gates. Preserve fast/slow separation and require the slow set for full qualification.
- **Self-referential evidence**: A payload cannot honestly name the commit containing itself before that commit exists. Use payload/publication/attestation layers.
- **Inherited qualification**: A later docs or code commit does not inherit prior results. State source boundaries prominently.
- **Unmeasured claims**: Local p95, hosted duration, or real-provider results apply only to the recorded tuple and host.

## Reviewer Guidance

Approve only if the evidence makes it easier to discover failure than to conceal it.

Verify explicitly:

1. Mutation generation has fixed coverage for every semantic WorkRevision field plus deterministic permutations.
2. Observation-only provider revision changes are not mislabeled semantic drift.
3. Race tests use independent processes/connections and deterministic barriers.
4. Exactly one current generation and no partial successor exists after restart.
5. Fault manifests prove every boundary was reached, including post-commit/pre-response cases.
6. Provider saga recovery does not claim cross-store rollback.
7. Projection and governance trials compare the complete canonical authority set.
8. Fast PR and slow full profiles are both exact, load-bearing, and appropriately scheduled.
9. Census mutants catch missing, extra, skipped, duplicated, and unterminated tests.
10. Universal invariants and stale probes were not dropped from relevant campaigns.
11. The full quality ladder ran against explicitly identified clean source with no unexplained waiver.
12. Evidence JSON distinguishes null/not-measured from zero and fail/non-admissible from pass.
13. Privacy filters exclude sensitive, ambient, and unbounded content.
14. Failed and superseded attempts are preserved with checksums and cannot satisfy qualification logic.
15. Documentation explains source, payload, publication, and attestation commits without implying transitive qualification.
16. Every claim maps to executable proof or a candid limitation.
17. Gapdb, distributed authority, production CLI/API/MCP/Team Kitty, Phase 4 execution, and Phase 5 review/integration remain explicit non-claims.
18. The diff is confined to the exact twenty-nine owned paths.

Reject a solution that reports only the final pass, runs races through one in-memory handle, uses sleeps as synchronization, silently skips fault points, omits field comparators from drift generation, treats projection edits as harmless without comparing canonical state, resolves governance from mutable paths, broadens the PR gate beyond its measured budget, publishes secrets/raw logs, fabricates future commit IDs, or lets dirty/source-drifted results become admissible.
