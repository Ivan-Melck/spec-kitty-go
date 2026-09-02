---
work_package_id: WP10
title: Acceptance and Serialized Integration Authority
dependencies:
- WP06
- WP07
- WP09
requirement_refs:
- FR-015
- FR-016
- FR-023
- FR-024
- FR-025
- NFR-001
- NFR-002
- NFR-004
- NFR-005
- NFR-016
- C-004
- C-005
- C-010
- C-012
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T049
- T050
- T051
- T052
- T053
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/integration/
create_intent:
- internal/controlstore/gapdb/acceptance_authority.go
- internal/controlstore/gapdb/acceptance_authority_test.go
- internal/controlstore/gapdb/integration_authority.go
- internal/controlstore/gapdb/integration_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/integration/**
- internal/controlstore/gapdb/acceptance_authority.go
- internal/controlstore/gapdb/acceptance_authority_test.go
- internal/controlstore/gapdb/integration_authority.go
- internal/controlstore/gapdb/integration_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP10 - Acceptance and Serialized Integration Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, use `spec-kitty agent profile list` to resolve the
closest implementation profile and record the governed invocation before any
production edit.

## Objective

Implement acceptance and serialized repository integration as two explicit,
versioned authority capabilities over the GapDB-only journal. Acceptance cites
exact current candidates, independent reviews, artifact/workgraph revisions,
policy, and visible waivers. Only a completely eligible accepted candidate may
approach the repository fence. Integration then owns one staged attempt and one
target authority while treating every Git value as an observed external fact,
never as mission authority.

Implementation command:

```bash
spec-kitty agent action implement WP10 --agent codex
```

## Branch, dependency, and ownership rules

- Start from accepted WP06, WP07, and WP09 revisions assigned in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Planning and merge target: `feat/gapdb-only-production-authority`.
- Work only in the Spec Kitty lane worktree.
- Edit only `internal/integration/**`,
  `internal/controlstore/gapdb/acceptance_*.go`, and
  `internal/controlstore/gapdb/integration_*.go`.
- Preserve other agents' concurrent edits; never revert unrelated work.
- Consume WP02–WP04 journal/restore, WP06 work/artifact, WP07 epoch/effect, and
  WP09 handoff/review contracts without modifying or bypassing them.
- Git implementations remain outbound adapters behind integration-owned ports.

Read the mission `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`contracts/authority-journal-v2.md`, and frozen acceptance/integration behavior
tests before editing. Preserve behavior and refusal order, not SQLite store
methods or the current staged table shape.

## Non-negotiable authority laws

1. Acceptance and integration are separate aggregates and separate decisions.
2. Acceptance cites exact immutable evidence; it does not infer approval from a
   branch, tracker state, Git ancestry, current directory, or caller claim.
3. Reviewer independence is evaluated by ActorID, including all candidate
   contributors, unless an exact recorded mission policy permits an exception.
4. Missing or stale complete WorkRevision eligibility is a typed refusal. No
   legacy mode, permissive default, zero-value fallback, or consequence adapter
   remains in production.
5. Every refusal that precedes repository authority is evaluated before any
   Git probe, integration workspace mutation, fence acquisition, or ref update.
6. One repository/target coordinate has at most one current integration-target
   authority, while unrelated repositories/targets may progress concurrently.
7. One OperationID owns all legitimate stages of an integration attempt;
   retries and repair observations never invent another identity implicitly.
8. Git owns commits, trees, refs, and observed outcomes. Spec Kitty owns whether
   those observations satisfy accepted mission/integration authority.
9. Conflict is preserved as evidence and explicit repair/candidate work; it is
   never auto-resolved or silently published.
10. At-most-once admission means one accepted candidate/result membership in
    mission authority, not an assumption that external Git calls execute once.

## T049: Define the acceptance aggregate and exact evidence decision

**Purpose**: Make acceptance a durable, replayable decision over current
authority evidence rather than an incidental check inside Git integration.

**Files**: `internal/integration/**` and acceptance-focused tests.

**Implementation requirements**:

1. Define an acceptance aggregate coordinate scoped by MissionID,
   WorkRevisionID, and WorkID, with immutable AcceptanceID and positive logical
   version.
2. Define a typed acceptance command binding exact CandidateID/HandoffID,
   candidate commit/tree/repository, complete contributor ActorIDs, ReviewIDs
   and terminal verdicts, reviewer ActorIDs, artifact revision/approval IDs and
   digests, current WorkRevision generation/reconciliation fact, policy revision,
   governance/profile revisions, visible waivers, and OperationID.
3. Require every cited candidate, review, artifact, workgraph, and policy value
   to be current at decision publication. Later evidence cannot retroactively
   authorize the command.
4. Re-evaluate reviewer independence from complete contributor ActorIDs; a new
   ProcessID for the same ActorID is not independent.
5. Model waivers as explicit immutable evidence with WaiverID, schema/version,
   exact criterion/scope, policy basis, accountable ActorID, rationale,
   creation/expiry or terminal bounds, and content digest.
6. Reject hidden, textual, global, expired, scope-mismatched, policy-ineligible,
   or mutable waiver data. A waiver cannot bypass identity, currentness,
   reviewer independence where policy forbids it, or repository safety.
7. Define versioned acceptance requested/granted/refused/invalidated events and
   a pure reducer. A semantic refusal may bind and resolve for exact replay.
8. Construct one sealed publication containing operation transition,
   acceptance event/head, and CAS conditions for every mutable evidence/guard
   coordinate used by the decision.
9. Make terminal acceptance evidence immutable. Evidence drift requires a new
   explicit operation/decision; never edit a prior acceptance in place.
10. Return bounded typed result evidence suitable for later projections without
    exposing GapDB keys, Git handles, or mutable source objects.

**Required tests**:

- Exact complete current evidence grants one acceptance.
- Mutate each candidate/review/artifact/workgraph/policy/waiver coordinate and
  require the intended typed refusal with zero publication.
- Same-Actor/different-Process review refuses unless exact policy allows it.
- Hidden or expired waiver never makes an ineligible candidate acceptable.
- Exact retry returns byte-identical acceptance; changed binding conflicts.
- Reducer playback is deterministic and performs zero external calls.

**Exit evidence**:

```bash
go test ./internal/integration -run 'Acceptance|Waiver|Independence|Evidence' -count=10
```

## T050: Require complete eligibility and refuse before repository fencing

**Purpose**: Remove the last compatibility path and mechanically preserve the
earliest-refusal boundary before any repository mutation or authority claim.

**Files**: `internal/integration/**`.

**Implementation requirements**:

1. Delete `eligibilityLegacyV1`, legacy fact construction, auto-detection,
   permissive consequence fallback, and configuration that can downgrade a
   complete eligibility reader.
2. Require the exact complete eligibility fact for the accepted WorkRevision,
   WorkID, current reconciliation generation/disposition, approved status, and
   integration operation.
3. Require exact accepted candidate, handoff, reviews, policy, artifacts,
   waivers, repository binding, runtime epoch, and complete eligibility before
   invoking any repository/Git adapter method.
4. Resolve exact completed OperationID replay before current eligibility and
   epoch checks. Binding mismatch refuses before evidence reads.
5. Preserve the frozen semantic refusal precedence. At minimum, syntactic and
   binding validity, exact replay, accepted evidence identity, eligibility,
   current epoch/authority, and repository binding must precede the first fence
   or Git effect in their behavior-inventory order.
6. Make the pre-fence boundary observable with an adapter call counter and
   fault hook placed immediately before repository authority acquisition.
7. Refusal must leave integration-target, integration-attempt, effect intent,
   workspace, fence ref, target ref, and cleanup state unchanged.
8. A store/read error is not ineligibility and cannot fall through to legacy
   evaluation; return a distinct typed unavailable/corrupt outcome.

**Mandatory tests**:

- Omitted, zero, legacy, stale, substituted, or forbidden eligibility refuses.
- Stale candidate/review/artifact/policy/work generation refuses before fence.
- Review not approved or ActorID-independence failure refuses before fence.
- Repository-fence acquisition counter remains zero on every early refusal.
- Remove-one-early-check mutants fail the exact call-count/refusal assertion.
- Completed exact replay bypasses later drift and returns recorded result
  without touching Git, while changed operation binding still conflicts.

**Exit evidence**:

```bash
go test ./internal/integration -run 'Eligibility|Refusal|BeforeFence|Replay' -count=20
```

## T051: Model integration-target fencing and staged attempt state

**Purpose**: Serialize publication per repository/target without creating one
global integration lock or fragmenting one caller operation into new IDs.

**Files**: `internal/integration/**`.

**Implementation requirements**:

1. Define separate `integration_target` and `integration_attempt` aggregates.
   The target coordinate is exact RepositoryID plus full target ref; the attempt
   binds AcceptanceID, candidate, actor/process, epoch, and OperationID.
2. Define explicit attempt stages such as requested, authority-intent,
   authority-active, workspace-observed, result-observed, publishing,
   completed, repair-required, and cleanup-eligible/completed.
3. Encode the allowed transition graph explicitly with one logical-version
   increment and one event per changed aggregate per publication.
4. Use one caller OperationID and revisioned OperationState for every stage.
   Derive stable EffectIDs/PublicationIDs for fence, workspace, merge, ref
   update, observation, and cleanup; never create a new caller operation.
5. Before installing an external Git fence, atomically publish meaningful
   integration intent plus attributable fence effect intent and CAS the current
   integration-target aggregate.
6. After observing the installed fence, publish active target authority and
   attempt progress atomically. Ambiguous installation requires observation and
   reconciliation, not blind replacement.
7. Fence evidence binds repository, target ref, expected predecessor, candidate,
   request digest, generation, OperationID, token digest, and deadline.
8. Exactly one incompatible contender may own a repository/target. Losers get
   typed busy/stale outcomes; different targets do not share a global CAS.
9. Expiry/supersession is versioned and cannot let an old fence authorize a new
   attempt. Exact committed replay remains readable after epoch replacement.
10. Cleanup is a later non-authoritative effect and cannot make a failed or
    unfinished integration complete.

**Required tests**:

- Two simultaneous attempts for one target produce one authority winner.
- Different repository/target coordinates proceed independently.
- Every stage resumes with the same identities after crash/restart.
- Stale fence generation/token/epoch/operation refuses.
- Split-stage, global-lock, new-operation-per-stage, and cleanup-as-completion
  mutants are killed.

**Exit evidence**:

```bash
go test ./internal/integration -run 'TargetAuthority|Fence|Stage|Contention' -count=10
```

## T052: Record Git outcomes, conflicts, repair, and at-most-once admission

**Purpose**: Integrate real candidates safely while keeping Git observation and
Spec Kitty authority rigorously separate.

**Files**: `internal/integration/**`.

**Implementation requirements**:

1. Put every Git mutation behind an intent-before-effect contract. Record the
   exact expected-old ref, candidate, base, repository/target identity, and
   stable effect marker before workspace creation, merge, or ref update.
2. Re-observe target ref, integration workspace, base/candidate reachability,
   clean/conflicted state, result commit/tree, and ancestry as typed immutable
   facts. Never trust caller-provided success or parse human-formatted output.
3. Revalidate acceptance, complete eligibility, current target authority, epoch,
   and expected target immediately before the ref update.
4. Perform target publication with compare-and-swap expected-old semantics.
   Manual branch movement is an observed conflict/stale fact, not permission to
   merge over the new target.
5. Preserve merge conflicts as bounded evidence and transition the attempt to
   repair-required. Do not auto-resolve, amend, force-push, or accept a working
   tree as a reviewed candidate.
6. Repair produces a new immutable candidate/review/acceptance chain or an
   explicit operation-stage outcome according to policy; it never edits the
   original approved candidate's meaning.
7. After an ambiguous ref-update response, observe the target. Mark completion
   only when the exact intended result is attributable to the recorded effect,
   is reachable as required, and complete operation/event membership matches.
8. Record each accepted candidate/result admission at most once. Existing Git
   ancestry without matching acceptance/attempt/operation evidence does not
   synthesize completion.
9. A target advanced by another actor after successful publication remains Git
   history; exact recorded completion replays without trying to rewind it.
10. Cleanup failure preserves terminal integration evidence and remains a
    separate retryable effect with attribution-safe behavior.

**Required tests**:

- Clean integration records exact base/candidate/result/target observations.
- Merge conflict preserves evidence and requires explicit repair.
- Manual target movement before, during, and after publication yields the
  defined stale/repair/completed result without overwriting another actor.
- Result already ancestral is accepted only with matching recorded operation
  and effect evidence, never from ancestry alone.
- Lost ref-update response reconciles to exactly one admission.
- Force-update, caller-success, Git-is-authority, auto-repair, and duplicate-
  admission mutants are killed.

**Exit evidence**:

```bash
go test ./internal/integration -run 'Git|Conflict|Repair|TargetMoved|Admission' -count=10
```

## T053: Implement GapDB adapters and adversarial qualification

**Purpose**: Persist both aggregates through sealed v2 batches and prove the
complete acceptance-to-integration flow under concurrency and failure.

**Files**: `internal/controlstore/gapdb/acceptance_*.go`,
`internal/controlstore/gapdb/integration_*.go`, and tests under owned surfaces.

**Adapter requirements**:

1. Implement acceptance/integration-owned ports using WP03's v2 committer and
   reader. Raw GapDB calls remain in the adapter package.
2. Translate each sealed plan into exactly one conditional batch containing
   global operation state, capability events/heads, eligibility guards, effect
   intents/observations, and all expected provider revisions.
3. Enforce absent creates and exact record/logical-version CAS updates. Reject
   duplicate coordinates, partial membership, key aliases, unknown versions,
   physical event/head deletion, and over-budget evidence.
4. Reconcile ambiguous acknowledgements by PublicationID and complete binding,
   event, head, effect, and result membership; never allocate a new identity.
5. Restore acceptance, target authority, attempts, repair evidence, cleanup,
   and operation results byte-identically through WP04 checkpoint/suffix and
   bounded origin playback.
6. Return typed bounded errors without leaking raw keys, values, sockets, Git
   output, or provider details.
7. Add no SQLite/SQL dependency, legacy eligibility, backend selector,
   fallback, dual write, importer, or general replacement store.

**Required campaigns**:

- At least 100 simultaneous same-target integrations: one active authority and
  no duplicate target/candidate admission.
- At least 100 lost-response retries for acceptance and every integration stage:
  identical results, zero duplicate events/effects.
- At least 100 checkpoint/replay comparisons covering acceptance, active fence,
  result, repair-required, completed, and cleanup states.
- Fault injection before/after every authority publication and Git effect,
  including ambiguous acknowledgements and owner restart.
- Manual branch movement at preflight, pre-merge, pre-publish, during CAS, and
  post-publish observation boundaries.
- Race detector coverage for services, reducers, adapters, contention, replay,
  and independent targets.

**Mandatory controlled mutants**:

- acceptance omits candidate, review, artifact, workgraph, policy, or waiver;
- reviewer independence compares ProcessID instead of ActorID;
- complete eligibility silently downgrades to legacy/zero/default;
- an early refusal occurs after repository-fence acquisition;
- target authority uses a global lock or omits repository/target coordinate;
- stage retry creates a new OperationID or EffectID;
- Git ref/ancestry alone grants mission completion;
- manual branch movement is overwritten or silently rebased;
- merge conflict is auto-resolved or discarded;
- ambiguous acknowledgement creates duplicate admission;
- cleanup status controls terminal integration truth;
- adapter bypasses journal sealing or reducer calls Git/storage.

Each mutant needs a passing control and must fail for its intended semantic
reason; grep-only source assertions are insufficient.

**Validation commands**:

```bash
go test ./internal/integration ./internal/controlstore/gapdb -count=10
go test -race ./internal/integration ./internal/controlstore/gapdb -count=1
go vet ./internal/integration ./internal/controlstore/gapdb
staticcheck ./internal/integration ./internal/controlstore/gapdb
go test ./internal/architecture -count=1
go test ./... -run '^$' -count=1
gofmt -d internal/integration internal/controlstore/gapdb
git diff --check
```

## Definition of Done

- Acceptance cites exact current candidate, reviews, artifacts, workgraph,
  policy, governance, and visible waiver evidence.
- Reviewer independence uses ActorID and complete contributor identity.
- Complete eligibility is the only path; all legacy/default modes are absent.
- Every pre-fence refusal occurs before Git/fence/workspace mutation.
- Integration-target and attempt aggregates have explicit staged authority.
- One OperationID and stable effect identities survive every restart/retry.
- Git observations never substitute for acceptance or mission authority.
- Conflict/repair and manual branch movement remain explicit and recoverable.
- Each candidate/result is admitted at most once under exact evidence.
- GapDB adapters use one sealed conditional batch per authority stage.
- Contention, replay, fault, ambiguity, branch-movement, mutant, and race tests
  satisfy their declared 100-run censuses.
- No SQLite, fallback, general store, or provider dependency enters the kernel.
- T049–T053 are recorded `done` in the canonical task event log.
- The committed WP is handed to an independent reviewer and not self-approved.

After all implementation and validation genuinely complete:

```bash
spec-kitty agent tasks mark-status T049 T050 T051 T052 T053 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and mitigations

| Risk | Required mitigation |
|---|---|
| Acceptance is collapsed into integration preflight | Persist a separate exact-evidence acceptance aggregate and decision. |
| Visible waiver becomes a generic bypass | Bind typed immutable scope/policy/actor/rationale and forbid safety-law waivers. |
| Legacy eligibility survives by auto-detection | Delete the mode and test missing/zero/legacy facts as refusals. |
| Repository fence is acquired before an earlier refusal | Assert zero Git/fence calls for every acceptance/eligibility refusal mutant. |
| Target serialization becomes repository-global | Key/CAS exact repository plus target ref and prove independent targets progress. |
| Git ancestry fabricates mission truth | Require matching operation, acceptance, effect, and event membership. |
| Response ambiguity duplicates ref publication/admission | Reconcile stable identity and exact expected-old/result observations. |
| Cleanup or repair rewrites terminal evidence | Model both as later explicit stages/effects without changing admitted result. |

## Independent reviewer guidance

Trace one clean integration and every refusal from request binding through exact
acceptance, complete eligibility, target authority, Git observations, target
CAS, result admission, and cleanup. Verify the first Git/fence call occurs only
after every earlier product gate passes. A passing happy path is insufficient.

Reproduce simultaneous same-target integration, independent-target progress,
manual branch movement at each publication boundary, merge conflict, restart
from every stage, and a lost ref-update acknowledgement. Confirm Git can change
without becoming Spec Kitty authority and that an external result is admitted
only once under the exact accepted candidate and operation evidence.

Reject hidden/permissive waivers, ProcessID reviewer independence, legacy
eligibility, process-local locking, blind retry, automatic rebase/repair,
force-update, ancestry-only success, optimistic projection writes, unsealed
adapter calls, dead production APIs, or tests that mock away the disputed Git
state. Ensure no GapDB/Git/SQL implementation is reachable from pure reducers.
