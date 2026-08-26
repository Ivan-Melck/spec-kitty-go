# Two-Agent Authority and Recovery Scenario Catalog

Status: Phase 1 executable test specification

Companion: `docs/PYTHON_BEHAVIOR_INVENTORY.md`

Date: 2026-08-23

## 1. Purpose

This catalog is designed to disprove the hardest claim in the Go architecture:

> Spec Kitty can preserve safe parallel execution when process lifetime,
> control authority, tracker state, filesystem state, and Git history disagree.

Phase 1 implements only enough product to execute these scenarios. It does not
implement Beads, a complete mission language, Team Kitty, MCP, migration,
Markdown projections, or multiple mission types. A deliberately stupid fake
tracker is mandatory so tracker convenience cannot hide an authority leak.

## 2. Test harness

Every Phase 1 scenario uses real concurrency and real Git operations:

```text
1 temporary Git repository
1 SQLite control store in WAL mode
1 in-process or local fake tracker with durable revisions
2 independent OS processes by default
2 work items in one immutable WorkRevision
2 logical lanes unless a scenario states otherwise
1 target integration branch
real git worktrees and branches
deterministic clock with explicit advance
named crash/fault hooks after each durable boundary
```

The harness must be runnable under:

```text
go test -race ./...
go test ./... -run Phase1 -count=100
```

Contention tests must use independent processes where SQLite, filesystem locks,
or Git indexes are part of the claim. Goroutines alone are insufficient.

### 2.1 Stable actors and processes

The standard cast is:

| Name | Identity |
|---|---|
| Lynn | `ActorID=lynn`, human authority for explicit decisions |
| Agent A | `ActorID=agent-a`, `ProcessID=proc-a1` |
| Agent A replacement | `ActorID=agent-a`, `ProcessID=proc-a2` |
| Agent B | `ActorID=agent-b`, `ProcessID=proc-b1` |
| Reviewer C | `ActorID=reviewer-c`, `ProcessID=proc-c1` |
| Same-actor reviewer | `ActorID=agent-a`, `ProcessID=proc-a-review` |

The same durable actor may own multiple processes. That never collapses their
`ProcessID`, `RunID`, workspace, lease generation, or audit trail.

### 2.2 Baseline work graph

Unless overridden:

```text
WorkRevision WR-1, generation 1
  W-1: change a.txt, lane L-1, no dependency
  W-2: change b.txt, lane L-2, no dependency

planning_source_commit = P0
target branch            = main at P0
```

Each work item has one machine-evaluable criterion and one human-readable
criterion so the handoff/review record must support both.

### 2.3 Fake tracker contract

The fake tracker intentionally exposes only:

- create/read/update work;
- dependency and criterion fields;
- visible assignment;
- comments;
- monotonically increasing provider revision;
- compare-and-set assignment/update;
- idempotency marker lookup;
- injected unavailable, timeout, stale-revision, and ambiguous-result failures.

It has no mission phase, run lease, worktree, reviewer, acceptance, merge slot,
or Git hook. If Phase 1 needs one of those concepts in the fake, the authority
boundary has leaked.

### 2.4 Control-store observation

Tests inspect application queries and a read-only diagnostic projection. Direct
SQL may additionally assert schema-level uniqueness, but production behavior
must not depend on tests mutating tables.

Required observable records are:

- immutable WorkRevision and graph digest;
- operation/idempotency record and current saga stage;
- run, role, actor, process, lease generation, token, and expiry;
- lane lineage and pinned accepted head;
- workspace path, branch, base, state, and lease binding;
- tracker expected revision/value/operation marker;
- handoff candidate and evidence digest;
- review actor/process/candidate/verdict;
- integration queue position, target ref, phases, and result;
- typed blocker, decision, conflict, or compensation outcome;
- outbox event created in the same transaction as its source state.

## 3. Universal safety assertions

Every scenario, including failure scenarios, asserts all applicable properties:

1. At most one valid implementation lease exists for a WorkID + WorkRevision.
2. Every mutable workspace belongs to exactly one lease generation forever.
3. A quarantined workspace is never read as a valid candidate without explicit
   quiescence-checked reconciliation.
4. Every accepted handoff names a still-valid lease and immutable commit.
5. Review verdicts name their exact candidate and WorkRevision.
6. An ActorID cannot independently approve its own contributed candidate.
7. Only one integration attempt mutates a repository target ref at a time.
8. A failed operation either remains resumable or records a typed terminal
   conflict; it never reports success with an incomplete durable effect.
9. Retry with the same idempotency key never duplicates a lease, workspace,
   external assignment, review, merge, or outbox event.
10. Compensation never overwrites a newer human or operation-owned external
    state.
11. No cleanup deletes an unknown, dirty, active, or quarantined workspace.
12. The primary checkout remains unchanged by implementation and review runs.
13. Tracker state cannot grant execution, review, acceptance, or integration.
14. A stale fencing token cannot mutate the control plane even when its process
    and filesystem access remain alive.
15. Every refusal is typed and includes the stable IDs needed to recover.

## 4. Reference saga boundaries

The exact internal implementation may evolve, but fault injection requires
named externally meaningful boundaries.

### 4.1 Start-run saga

```text
SR-00 request_validated
SR-01 control_lease_committed
SR-02 tracker_assignment_attempted
SR-03 tracker_assignment_confirmed_or_deferred
SR-04 workspace_reserved
SR-05 worktree_created
SR-06 base_verified
SR-07 dependency_commits_applied
SR-08 branch_ownership_verified
SR-09 workspace_ready_committed
```

No workspace path is returned as usable before `SR-09`.

### 4.2 Handoff/review saga

```text
HR-00 handoff_requested
HR-01 lease_and_revision_revalidated
HR-02 candidate_commit_verified
HR-03 evidence_recorded
HR-04 handoff_ready_committed
HR-05 review_claimed
HR-06 review_verdict_committed
HR-07 tracker_projection_attempted
```

`HR-06` is locally authoritative. `HR-07` may fail without losing the verdict.

### 4.3 Integration saga

```text
IG-00 integration_requested
IG-01 target_ref_lease_acquired
IG-02 preflight_snapshot_recorded
IG-03 merge_workspace_ready
IG-04 candidate_applied
IG-05 target_ref_advanced
IG-06 integration_result_committed
IG-07 work_done_committed
IG-08 projections_queued
IG-09 cleanup_eligible
```

The critical crash window is between `IG-05` and `IG-07`. Recovery must probe
Git and the candidate/result relationship before replaying bookkeeping.

## 5. Phase 1 proving scenarios

### P1-01: two independent claims execute concurrently

**Setup:** W-1 and W-2 are ready in WR-1. Processes A and B synchronize on a
barrier, then claim one item each.

**Expected:** both claims succeed; they receive distinct `RunID`, token,
WorkspaceID, branch, directory, and Git index. Their wall-clock active periods
overlap. Each can commit without observing the other's uncommitted files.

**Assertions:** the control store and fake tracker are shared; mutable
workspaces are not. Primary checkout HEAD/index/tree remain unchanged.

### P1-02: two processes race for one work item

**Setup:** A and B simultaneously request W-1 under WR-1 with different
idempotency keys.

**Expected:** exactly one `SR-01` transaction succeeds. The loser receives
`lease_conflict` naming the winning RunID/ActorID as policy permits. No loser
workspace or tracker assignment is created.

**Repeat:** run with 2, 8, and 32 contenders and with the same ActorID but
different ProcessIDs.

### P1-03: same start request is replayed

**Setup:** A sends the identical operation ID concurrently from two client
connections and then retries after the response is lost.

**Expected:** every successful response identifies the same run, lease token,
workspace operation, and final WorkspaceID. At most one external assignment and
one outbox event exist.

### P1-04: crash after lease commit

**Fault:** kill A after `SR-01`, before tracker or filesystem effects.

**Expected:** recovery sees an authoritative lease with an incomplete start
operation. Before expiry, retry continues the same operation. After expiry,
the run becomes expired and a replacement gets a new generation. No phantom
workspace is reported.

### P1-05: tracker outage after lease commit

**Fault:** fake tracker returns unavailable at `SR-02`.

**Expected:** policy decides one explicit result:

- `blocked_external`: keep the lease for a bounded interval and retry; or
- `local_authority_ready_projection_pending`: continue locally with visible
  tracker projection debt.

The result may not be an untyped half-success. Retry uses one idempotency marker.
The architecture's initial policy should prefer blocking before workspace
creation when the selected primary tracker cannot confirm the work/revision.

### P1-06: ambiguous tracker assignment result

**Fault:** tracker applies A's assignment and drops the response.

**Expected:** retry first queries operation marker/current revision. It
recognizes its own effect and continues; it does not create a second comment or
oscillate the assignee.

### P1-07: crash after workspace reservation

**Fault:** kill A after `SR-04`, before `git worktree add`.

**Expected:** reserved identity/path cannot be allocated by an unrelated
operation. Same-operation retry creates it and continues. Expiry quarantines
the reservation record and replacement receives a different WorkspaceID/path.

### P1-08: crash after worktree creation

**Fault:** kill A after `SR-05`, before base/dependency verification.

**Expected:** directory and Git registration exist but queries report
`workspace_preparing`, never ready. Retry verifies ownership, base, branch, and
operation ID before continuing. A mismatching artifact yields
`workspace_reconciliation_required` rather than reuse.

**Regression covered:** Python issue #3281.

### P1-09: crash during dependency preparation

**Setup:** W-3 depends on two accepted candidates D-1 and D-2.

**Fault:** stop after D-1 is applied but before D-2.

**Expected:** retry either deterministically continues from a recorded
intermediate state or rolls back to the recorded pre-dependency head before
replaying both. It never publishes the partial head as `workspace_ready`.

### P1-10: dependency merge conflict is atomic

**Setup:** D-1 and D-2 conflict when combined for W-3.

**Expected:** W-3 receives a typed dependency-preparation conflict with both
candidate SHAs and paths. Its branch returns to the pre-attempt head (or the
failed workspace is quarantined). Neither partial dependency is advertised as
the run base.

**Regression covered:** Python issue #1915.

### P1-11: target branch moves after finalization

**Setup:** WR-1 pins P0. A human advances `main` to P1 before A claims W-1.

**Expected:** W-1 starts from the recorded planning source/lane policy, not the
ambient live target tip. The movement is reported diagnostically. If policy
requires rebasing, that is a new explicit revision/base reconciliation, not an
allocator side effect.

**Regression covered:** Python issue #2993.

### P1-12: manual branch movement during a run

**Fault:** after `SR-09`, a human force-moves A's owned branch away from the
recorded head.

**Expected:** next control mutation detects head mismatch and returns
`branch_moved`. No handoff is accepted. Recovery preserves both the recorded
candidate ancestry and current branch state, then asks for explicit repair.

### P1-13: lease expires and replacement gets a new workspace

**Setup:** A holds generation 17 and W1, then heartbeats stop. Advance the clock
beyond expiry. B replaces it.

**Expected:** A's run is expired; W1 and its branch are quarantined. B receives
generation 18, W2, and a distinct branch/path. No code path rebinds W1 to B.

### P1-14: zombie writer resumes after replacement

**Fault:** suspend A before expiry rather than killing it. After B receives W2,
resume A and have it write and commit additional files in W1.

**Expected:** POSIX writes and even local Git commits in W1 may succeed. W2's
tree/index/branch do not change. A's heartbeat, status, handoff, review,
tracker-projection, and integration commands all fail `stale_fence` or
`expired_run`. No watcher is required to pretend W1 is immutable.

This is the crown-jewel scenario. The test passes because authority and
workspace isolation hold, not because the zombie is stopped.

### P1-15: quarantined output reconciliation

**Setup:** A zombie produced a potentially useful commit Q in W1. A is now
proven terminated. B's replacement work is still active.

**Expected:** a read-only inspect command can report Q. Importing it requires a
human-authorized reconciliation that proves quiescence, snapshots the exact Q,
and chooses discard, preserve-as-evidence, cherry-pick-to-new-run, or new-work.
It never revives A's expired lease or mutates B's workspace silently.

### P1-16: human reassignment defeats old compensation

**Setup:** operation O assigns fake ticket W-1 to A at revision 7, then crashes.
A human assigns it to B at revision 8. Recovery for O runs.

**Expected:** compare-before-compensate sees revision/value mismatch and records
`compensation_conflict`. B remains assigned. The recovery result names O's
expected and observed values without overwriting the human action.

### P1-17: compensation succeeds only on attributable state

**Setup:** O assigns W-1 to A with marker O at revision 7 and no later writer
touches it. The local lease fails before a workspace is usable.

**Expected:** recovery compares revision/value/marker, clears only O's
assignment, records the new provider revision, and is idempotent on replay.

### P1-18: immutable handoff ignores later workspace writes

**Setup:** A commits candidate C, submits a valid handoff, then modifies the
workspace without producing a new handoff.

**Expected:** review sees C exactly. Dirty later files and moving workspace HEAD
cannot change the candidate. Submitting a new candidate creates a new handoff
and stales any verdict for C according to explicit policy.

### P1-19: stale lease cannot hand off an otherwise good commit

**Setup:** A creates commit C, then its lease expires before `HR-01`.

**Expected:** handoff is rejected even if C is clean and tests pass. C can only
enter the system through explicit quarantined-output reconciliation and a valid
new run.

### P1-20: reviewer ActorID independence

**Setup:** candidate C was implemented by `ActorID=agent-a`. Review is claimed
by `ActorID=agent-a`, `ProcessID=proc-a-review`.

**Expected:** the independent-review gate rejects the claim/verdict. Phase 1
defines no waiver or bypass; a distinct reviewing actor is required.

### P1-21: distinct reviewer succeeds

**Setup:** Reviewer C reviews A's candidate C.

**Expected:** the verdict binds Reviewer C's ActorID/ProcessID, C, WR-1,
criteria, evidence, and review policy. Approval makes work `approved`, not
`done`. Rejection retains the record and creates/reopens an explicit rework run.

### P1-22: concurrent verdict persistence

**Setup:** two authorized reviewer processes race to write verdicts for the same
candidate/review slot, and two other reviewers write verdicts for distinct work.

**Expected:** same-slot policy either admits one and explicitly conflicts the
other, or records a defined multi-review set. Distinct reviews both persist. No
verdict is merely an uncommitted file or lost Git index entry.

**Regression covered:** Python issue #3235.

### P1-23: candidate movement stales review

**Setup:** C is approved. The owned branch moves to C2 before acceptance.

**Expected:** C's approval remains historical but applies only to C. C2 is not
accepted, queued, or integrated without a new handoff/review. Moving a branch
pointer never changes a ReviewID's meaning.

### P1-24: two integrations serialize

**Setup:** W-1 and W-2 are independently approved. Two processes request
integration against `main` simultaneously.

**Expected:** exactly one holds the repository+target-ref integration lease.
The other waits or receives a typed busy result. After the first commits, the
second re-runs preflight against the new target before applying its candidate.

### P1-25: crash matrix across integration

Inject process death after each `IG-*` boundary.

**Expected by region:**

- before `IG-05`, target ref remains unchanged and retry may rebuild the merge
  workspace;
- after `IG-05` but before `IG-06`, recovery proves whether the target contains
  the exact candidate/result before recording success;
- after `IG-06` but before `IG-07`, retry emits `done` exactly once;
- after `IG-07`, projection or cleanup failure never rolls back Git or done;
- no retry creates a second target commit for the same integration operation.

**Regressions covered:** Python issues #416, #2711, and #2786.

### P1-26: integration conflict preserves both lines

**Setup:** target advances incompatibly after review; applying C conflicts.

**Expected:** target ref remains at its pre-attempt SHA. Candidate branch and C
remain reachable. Conflict paths and SHAs are recorded. Work enters explicit
conflict-repair state/work; it is neither done nor silently rejected.

### P1-27: two missions target the same branch

**Setup:** mission M-1 and M-2 each have approved work targeting `main`.

**Expected:** repository+target-ref serialization applies across missions. A
mission-scoped lock cannot admit both. Queue order uses the declared stable
policy, and the second mission revalidates after the first advances `main`.

### P1-28: restart with corrupt or partial control artifacts

**Fault:** simulate a process crash during every SQLite transaction and during
non-transactional Git/filesystem steps.

**Expected:** SQLite exposes either pre-transaction or committed records, never
partial rows. Startup diagnoses incomplete external stages. It does not infer
authority from a directory, branch, tracker assignment, or outbox item alone.

## 6. WorkRevision reconciliation scenarios

These can initially be domain/application tests if Phase 1 does not yet include
the full mission UI, but the run/review/integration gates must consume their
results.

### WR-01: equivalent edit continues

Tracker wording changes without changing effective description, dependencies,
criteria, lane, or required candidate. Reconciliation creates WR-2 and records
`continue` for active W-1 with rationale and old/new digests. The existing run
remains eligible; its handoff names the allowed generation relationship.

### WR-02: replacement supersedes active work

W-1 is replaced by W-1b. WR-2 records `supersede`. W-1's process may finish
locally, but its handoff cannot be accepted or integrated. Its workspace is
preserved as recoverable evidence until policy permits archive.

### WR-03: criterion change invalidates active work

W-1's acceptance contract materially changes. WR-2 records `invalidate`; its
lease expires at the authority boundary and workspace is quarantined. A new run
and workspace execute the new contract. The old process may still write only in
its quarantined workspace.

### WR-04: intentional graph fork

One work line becomes two legitimate alternatives. WR-2 records `fork` and
allocates distinct stable WorkIDs and graph edges. Runs, reviews, and
integrations cannot ambiguously refer to “the old W-1.”

### WR-05: new dependency affects approved work

W-1 is approved but not integrated. WR-2 inserts a dependency that changes its
validity. Acceptance and integration reject the old approval until explicit
reconciliation says continue or new work/review satisfies the dependency.

### WR-06: graph edit after integration

W-1 is already integrated. WR-2 does not rewrite Git or mark history invalid.
Any unmet criterion/dependency becomes new repair/follow-up work with explicit
lineage to the integrated result.

### WR-07: post-collapse lane cycle

Individual work dependencies are valid, but write-scope collapse produces a
cycle between lanes. Finalization rejects WR-2 before it becomes executable and
reports the work/lane edges causing the cycle.

**Regression covered:** Python issue #3431.

## 7. Later semantic-parity scenarios

These are required before replacement release, but do not expand Phase 1.

| ID | Scenario | Required outcome |
|---|---|---|
| SP-01 | Missing mission type | Fail loud; never silently select software-dev. |
| SP-02 | Finalized genesis/uninitialized mismatch | Refuse claim with finalize/migration diagnosis. |
| SP-03 | Approved versus done | Acceptance admits approved; integration alone produces done. |
| SP-04 | Review rejection cycle | Preserve old evidence; new run/review attempt is explicit. |
| SP-05 | Block and resume | Durable blocker, actor/reason, legal resume transition. |
| SP-06 | Cancel and exceptional reopen | Terminal by default; explicit attributed recovery only. |
| SP-07 | Cannot evaluate evidence | Diagnose without mutation; accept fails closed. |
| SP-08 | Derived `tasks.md` edited by user | Detect/overwrite projection or warn; never mutate canonical work. |
| SP-09 | Tracker changes after finalization | Drift result; no silent executable-contract mutation. |
| SP-10 | Beads readiness disagrees with pinned graph | Diagnostic conflict; Spec Kitty scheduler remains authoritative. |
| SP-11 | Team Kitty inbound status asks for run | Request/reconciliation only; no lease or Git effect. |
| SP-12 | Orchestrator and CLI issue same operation | Same application result and idempotency record. |
| SP-13 | Local merge without remote | No fetch, network, or push required. |
| SP-14 | Publish requested while offline | Local integration remains true; publish debt is explicit. |
| SP-15 | Python import has graph/event disagreement | Dry-run refuses with exact conflicting authorities. |
| SP-16 | Import active merge state | Refuse or import as recoverable operation; never declare merged. |
| SP-17 | Unsupported old mission format | Explicit unsupported result and upgrade/export path. |
| SP-18 | Research/plan/documentation mission | Preserve each typed workflow when its implementation phase arrives. |
| SP-19 | Remote API races a local claim | Both use the same application CAS; API exposes stable IDs, never DB authority. |
| SP-20 | Team Kitty is unavailable | Local authoritative work continues; delivery debt remains bounded and diagnosable. |

## 8. Failure diagnostics contract

Every scenario failure response should contain:

```text
schema_version
correlation_id
operation_id
error_code
human_message
project_id
mission_id (when known)
work_id / work_revision_id (when known)
run_id / workspace_id / integration_id (when known)
observed_state
expected_state
retry_safe
decision_required
recovery_actions[]
```

Secret values, tracker credentials, transcript contents, and arbitrary command
output do not appear in the envelope.

Initial error-code families:

- `conflict.lease_held`
- `conflict.branch_moved`
- `conflict.tracker_revision_changed`
- `stale.fencing_token`
- `stale.work_revision`
- `blocked.external_unavailable`
- `blocked.dependency_conflict`
- `blocked.review_independence`
- `cannot_evaluate.evidence`
- `recovery.workspace_incomplete`
- `recovery.compensation_conflict`
- `decision.workspace_reconciliation_required`
- `decision.work_revision_disposition_required`

## 9. Phase 1 exit decision

Phase 1 passes only when all P1 scenarios are executable and green under the
race detector, repeated contention, and crash injection. A scenario marked
“not implemented” is a failed exit criterion, not a documentation success.

If a scenario demonstrates that an invariant cannot be enforced, stop before
Beads and revise the relevant ADR. The experiment is allowed to disprove the
architecture. That is its job.
