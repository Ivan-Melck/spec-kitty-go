---
work_package_id: WP09
title: Candidate Handoff and Independent Review Authority
dependencies:
- WP05
- WP06
- WP07
- WP08
requirement_refs:
- FR-013
- FR-014
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
- C-012
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T044
- T045
- T046
- T047
- T048
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/handoff/
create_intent:
- internal/controlstore/gapdb/handoff_authority.go
- internal/controlstore/gapdb/handoff_authority_test.go
- internal/controlstore/gapdb/review_authority.go
- internal/controlstore/gapdb/review_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/handoff/**
- internal/review/**
- internal/controlstore/gapdb/handoff_authority.go
- internal/controlstore/gapdb/handoff_authority_test.go
- internal/controlstore/gapdb/review_authority.go
- internal/controlstore/gapdb/review_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP09 — Candidate Handoff and Independent Review Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement immutable candidate handoff and independently accountable review as
capability-owned Authority Journal v2 aggregates backed only by GapDB. Every
accepted handoff and verdict must cite current graph, lease, workspace,
repository, criterion, contributor, and policy evidence; stale or incomplete
evidence must refuse before authority mutation.

## Context

WP05 supplies mission lifecycle authority. WP06 supplies immutable
WorkRevision generations and complete eligibility facts. WP07 supplies current
run/lease/epoch evidence, and WP08 supplies the permanently lease-bound
workspace. This WP turns those facts into a candidate and then a review record
that WP10 can use for acceptance and integration.

Read `spec.md`, `plan.md`, `research.md`, `data-model.md`, and
`contracts/authority-journal-v2.md`, then inspect the landed dependency APIs.
Preserve the existing product behavior in `internal/handoff` and
`internal/review`, including exact Git evidence and replayable refusals, while
removing legacy eligibility, overwrite-oriented stores, and SQL-shaped seams.

Authority laws for this WP:

- Candidate identity is immutable and content-addressed; a display branch or
  mutable ref is never the candidate.
- Handoff binds exact repository observations, WorkRevision, WorkID, run,
  lease, workspace, and the complete canonical contributor ActorID set.
- Review binds one candidate and the exact immutable criterion set.
- Reviewer independence compares durable ActorID to every contributor ActorID.
  A different ProcessID for the same actor is not independent.
- Same-actor review is forbidden unless a current, explicit mission policy
  records the exception; no default, CLI flag, or adapter choice may waive it.
- A committed OperationID result replays before mutable eligibility inspection.
- A new operation must pass all current authority gates before any durable or
  external mutation.

Stay within the declared ownership globs. Git observation remains behind the
existing verifier boundary; do not edit `internal/gitrepo`. Workgraph,
execution, workspace, journal, and integration packages belong to other lanes.

## Branch Strategy

- Planning branch: `feat/gapdb-only-production-authority`.
- Final mission merge target: `feat/gapdb-only-production-authority`.
- Spec Kitty assigns this WP to the execution worktree computed in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Use the runtime capsule's branch and worktree rather than guessing either.
- Do not revert concurrent work or broaden ownership to make a dependency
  convenient; report a missing dependency contract explicitly.

Start or resume implementation with:

```bash
spec-kitty agent action implement WP09 --agent <name> --mission gapdb-only-production-authority-01M1GWDJ
```

## T044 — Candidate/handoff capability ports, events, and immutable evidence

### Purpose

Replace the handoff store with a capability-owned aggregate whose events prove
exactly which immutable candidate and authority evidence were submitted and
observed.

### Implementation guidance

1. Define a handoff-slot aggregate coordinated by WorkRevisionID and WorkID,
   with immutable CandidateID/HandoffID generations rather than a mutable
   “current branch” record.
2. Preserve exact portable identities: ProjectID, MissionID, WorkRevisionID,
   WorkID, RunID, RuntimeEpochID, LeaseID/generation/fence, WorkspaceID, LaneID,
   RepositoryID, OperationID, PublicationID, ActorID, and ProcessID.
3. Bind candidate evidence to the exact commit/object ID, pinned base, owned
   branch, observed branch head, repository identity, workspace identity,
   criterion digest, evidence digest, observation time, and canonical
   contributor set/digest.
4. Canonicalize contributor ActorIDs by validated identity and stable order.
   Reject empty sets, duplicates, malformed identities, or a submitting actor
   absent from the contributor set. Never infer contributors from commits,
   process IDs, agent labels, or current branch ownership.
5. Define versioned events for submission requested, repository evidence
   observed, candidate recorded, candidate replaced, and immutable refusal as
   needed by the closed M2 registry. Use product vocabulary, not keys/rows.
6. Keep candidate verification read-only and explicit. Preserve both expected
   and observed Git facts when a ref moved; do not rewrite the branch, merge,
   fetch, or mutate the worktree during handoff validation.
7. Define a narrow consumer-owned port that returns the handoff snapshot plus
   logical/provider versions and publishes a sealed CommitPlan. Do not expose
   GapDB records, raw keys, generic Get/Put, or transaction callbacks.
8. Bind a refusal to the original OperationID and immutable observed facts so
   it replays after later branch, lease, or graph changes.
9. Ensure candidate replacement is an explicit versioned transition with
   lineage/reason; never mutate the bytes of a prior candidate or its evidence.
10. Add command validation, event/reducer, contributor canonicalization,
    candidate immutability, moved-ref, replacement-lineage, and refusal-replay
    tests.

### Validation

```bash
go test ./internal/handoff -count=1
```

## T045 — Current eligibility, lease, generation, and refusal ordering

### Purpose

Ensure a new handoff can cross the boundary only under the current executable
graph and execution authority, with all mutable preconditions CASed in the same
publication and all earlier refusals occurring before mutation.

### Implementation guidance

1. Preserve this high-level order: syntactic/authentication validation; global
   operation binding/result replay; changed-binding conflict; complete
   WorkRevision eligibility; current runtime epoch; execution lease/run/actor/
   process authority; workspace binding/readiness; repository evidence;
   candidate decision; sealed one-batch publication.
2. Exact committed results and durable refusals replay before current
   eligibility or repository observation. A replay performs no Git call or
   durable mutation.
3. Remove `legacy-v1`, fallback `WorkEligibility`, zero expected eligibility,
   mode/default switches, and any downgrade when the complete eligibility
   reader is missing. Missing complete eligibility is a typed refusal.
4. Require eligibility to match reconciliation ID, old/current generation,
   successor identity, disposition, WorkID, expected work status, and the
   handoff operation. Stale generation or substituted disposition refuses.
5. Verify RuntimeEpochID, RunID, ActorID, ProcessID, LeaseID, generation, fence,
   and unexpired authority exactly. Process separation cannot compensate for a
   mismatched ActorID or predecessor lease.
6. Require WorkspaceID to be current, ready, non-quarantined, and permanently
   bound to that lease/epoch/repository/lane. Never accept candidate evidence
   from a replacement's predecessor workspace.
7. Include every mutable eligibility, execution, workspace, and handoff head in
   the sealed commit's provider conditions. No read-then-unconditional-write.
8. On CAS loss, reread the global operation first; if it did not commit, return
   the earliest moved-authority refusal rather than silently recomputing a new
   candidate or OperationID.
9. Test replay-before-inspection, invalid command before reads, stale graph,
   stale epoch, stale lease/fence, wrong actor/process, quarantined workspace,
   moved branch, simultaneous handoffs, and unrelated-work progress.

### Validation

```bash
go test ./internal/handoff -run 'Test.*Eligibility|Test.*Authority|Test.*Replay|Test.*Contention' -count=3
```

## T046 — Staged review attempt, criterion evidence, and terminal verdict

### Purpose

Represent review as a durable staged aggregate under one caller OperationID so
crash recovery can distinguish requested work, recorded criterion evidence,
and the terminal verdict without duplicating or overwriting review history.

### Implementation guidance

1. Define a review-slot aggregate bound to ReviewID, HandoffID, CandidateID,
   exact candidate commit, WorkRevisionID, WorkID, reviewer ActorID/ProcessID,
   policy revision, and immutable criterion-set digest.
2. Model stages such as attempt requested/started, criterion evidence recorded,
   and terminal verdict recorded. Use one stable OperationID with successive
   OperationState/PublicatonID transitions; do not allocate one top-level
   operation per criterion or retry.
3. Require every immutable WorkRevision criterion exactly once before terminal
   verdict. Reject omissions, duplicates, unknown criteria, changed criterion
   digest, empty evidence, unknown status, and evidence above product bounds.
4. Preserve verdict laws: approval requires all criteria to pass; changes
   requested requires at least one failure; only one terminal verdict exists.
   A different terminal verdict under the same binding is an idempotency or
   monotonicity conflict, not an update.
5. Bind criterion evidence to exact artifact/candidate/policy coordinates and
   canonical evidence digest. Later artifact or policy changes do not rewrite
   the review; downstream gates decide freshness.
6. Publish each legitimate stage as one complete journal batch with exact
   review and OperationState event membership. A crash may leave an explicit
   in-progress review, never an invisible partial row.
7. Replay committed stage/result before current eligibility inspection. Resume
   only the recorded next stage and preserve the original reviewer/provenance.
8. Emit bounded typed results for in-progress, approved, changes-requested,
   refused, ambiguous, and reconciliation-required states.
9. Test crash/restart between every stage, dropped acknowledgement, duplicate
   criterion retry, terminal replay, changed verdict, stale candidate, and
   projection equality after restore.

### Validation

```bash
go test ./internal/review -run 'Test.*Stage|Test.*Criterion|Test.*Verdict|Test.*Replay' -count=3
```

## T047 — ActorID reviewer independence and explicit-policy-only exception

### Purpose

Make real reviewer independence a product law. Separate processes or sessions
belonging to the same durable actor do not provide independent review.

### Implementation guidance

1. Compare reviewer ActorID against the complete canonical contributor ActorID
   set stored in the immutable handoff evidence. Reject equality regardless of
   ProcessID, model, harness, session, hostname, or display name.
2. Require the current candidate/handoff contributor digest to match exactly;
   never accept a caller-supplied partial contributor list.
3. Default policy forbids same-actor review. The only exception is an explicit,
   current mission policy fact that identifies exception ID, policy revision,
   mission/work/candidate scope, reviewer ActorID, reason, authorizer, and
   effective bounds required by the landed policy contract.
4. Bind the exact exception fact and policy revision into the review event,
   result, and atomic provider conditions. A stale, expired, differently scoped,
   absent, malformed, or broad wildcard exception refuses.
5. An exception permits only the named independence predicate; it cannot waive
   current eligibility, candidate, lease, criterion, evidence, or verdict laws.
6. Preserve refusal precedence: operation replay/conflict, graph/candidate
   freshness, and required authority checks occur in their contract order;
   reviewer independence is evaluated before any review stage publication.
7. Return a stable `reviewer_not_independent` diagnostic with reviewer ActorID,
   contributor-set digest, policy coordinate, and safe recovery actions. Avoid
   exposing unbounded contributor/evidence payloads.
8. Test same ActorID/same ProcessID, same ActorID/different ProcessID, different
   ActorID/same label, contributor-set reorder, omitted contributor, valid
   narrow exception, and every stale/widened/forged exception mutation.
9. Add mutants that compare ProcessID, compare only submitting contributor,
   allow by default, trust a request boolean, or omit policy CAS; kill each via
   the same production decision path.

### Validation

```bash
go test ./internal/review -run 'Test.*Independent|Test.*Policy|Test.*Contributor' -count=3
```

## T048 — GapDB adapters and adversarial recovery/concurrency proof

### Purpose

Connect handoff/review ports to the common GapDB adapter and prove the complete
boundary under restart, contention, stale evidence, ambiguous acknowledgement,
and semantic mutation.

### Implementation guidance

1. Implement files matching `internal/controlstore/gapdb/handoff_*.go` and
   `review_*.go` using only WP03's canonical codec, revision-aware reader, and
   sealed-plan committer. Do not import provider types into handoff/review.
2. Load typed aggregate snapshots and exact logical/provider revisions. The
   adapter translates facts and sealed plans only; it must not decide
   eligibility, reviewer independence, candidate validity, or verdict policy.
3. Publish operation, handoff/review events, heads, and all mutable guard
   conditions in one AtomicBatch. Update in-memory projection only after durable
   acknowledgement or same-PublicationID reconciliation.
4. On EOF, timeout, cancellation, or malformed acknowledgement, inspect exact
   operation/event/head membership: replay complete matching truth, retry only
   proven absence with the identical sealed plan, and close on partial or
   contradictory evidence.
5. Restart after handoff intent, repository observation, candidate publication,
   review start, each criterion stage, and terminal verdict. Compare recovered
   projections/results with uninterrupted execution and assert zero playback
   Git/provider calls.
6. Race simultaneous handoffs for one work item, simultaneous terminal verdicts
   for one ReviewID, stale versus current candidate, and unrelated work items.
   Same-slot winners must be singular while unrelated work progresses.
7. Add mutants for split batch, optimistic result, stale eligibility, stale
   lease, predecessor workspace, mutable candidate, partial contributor set,
   ProcessID independence, implicit exception, partial criterion set, changed
   verdict, dropped-ack duplication, and adapter policy interpretation.
8. Carry the universal stale-authority and one-result-per-operation invariants
   into every fault case. Require exact stable mutant/fault census.
9. Run package, repeated, and race tests plus v2 journal regression tests.

### Validation

```bash
go test ./internal/handoff ./internal/review ./internal/controlstore/gapdb -count=1
go test -race ./internal/handoff ./internal/review ./internal/controlstore/gapdb -run 'Test.*Handoff|Test.*Review|Test.*Contention|Test.*Dropped' -count=1
go test ./internal/controljournal/v2/... -count=1
git diff --check
```

## Definition of Done

- Handoff is a capability-owned versioned aggregate with immutable candidate,
  repository, graph, run, lease, workspace, criterion, and contributor evidence.
- Legacy/incomplete eligibility paths are absent; committed operation results
  replay before mutable inspections, while new operations require current facts.
- Every mutable eligibility/lease/workspace/handoff/review fact is conditioned
  in the same sealed publication as the accepted transition.
- Review stages attempt, criterion evidence, and one terminal verdict under one
  stable OperationID with exact event/OperationState membership.
- Approval and changes-requested verdicts enforce complete immutable criteria
  and cannot be rewritten after terminal state.
- Reviewer ActorID differs from every contributor ActorID unless one current,
  explicit, narrowly scoped policy exception is durably cited and CASed.
- Concrete GapDB adapters translate only typed ports and sealed plans, with no
  policy logic, dual write, fallback, raw key exposure, or optimistic result.
- Restart, contention, stale candidate/lease/workspace, dropped acknowledgements,
  complete replay, unrelated progress, mutants, and race tests pass.
- Only declared ownership paths changed and `git diff --check` is clean.
- Completion is recorded through the event log:

```bash
spec-kitty agent tasks mark-status T044 T045 T046 T047 T048 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks

- **Mutable Git refs**: bind the candidate to an exact object plus observed ref
  facts; preserve divergence rather than “fixing” it during validation.
- **Eligibility downgrade**: remove legacy/default modes instead of retaining a
  hidden fallback for tests.
- **Read/write race**: every mutable fact used by a decision must appear in the
  same publication's provider conditions.
- **Contributor undercount**: authority uses the stored canonical contributor
  set, never a request-provided subset or commit-author guess.
- **Process independence**: different ProcessIDs for one ActorID remain the same
  accountable reviewer and implementer.
- **Policy escape hatch**: exceptions are explicit authority facts, not booleans,
  flags, adapter defaults, or unversioned prose.
- **Staged operation duplication**: all review stages retain one OperationID and
  deterministic PublicationIDs; retry cannot allocate a replacement identity.
- **Adapter semantic gravity**: domain packages own decisions; GapDB adapters
  translate only validated records and sealed batches.

## Reviewer Guidance

Trace one candidate from current WorkRevision and lease-scoped workspace through
Git observation, handoff publication, staged criterion evidence, and terminal
review. Verify exact identities and digests survive restart and later mutable
ref/policy changes do not rewrite history.

Pay special attention to refusal ordering and side-effect boundaries: replay
must avoid eligibility/Git reads, a new stale request must refuse before durable
mutation, and CAS loss must resolve the same OperationID before describing
moved authority. Confirm all guard revisions participate in the atomic batch.

Independence review is decisive: same ActorID under another ProcessID must fail;
a different actor with a similar label must pass; only a current explicit
policy fact may permit the same actor, and it must waive nothing else. Reject
legacy eligibility, mutable candidates, overwrite-only review records, partial
criteria, implicit waivers, split batches, policy-aware adapters, or mutants
that die only because of unrelated fixture errors.
