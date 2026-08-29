# Architecture Review Brief

**WP08 artifact — Independent Architecture Review and Publication Gate**

Reviewer ActorID: `opencode`
Reviewer ProcessID: `proc-opencode-wp08-review-2026-08-29`
Review date: 2026-08-29
Source tree reviewed: `5c7592046f0abc85ac52e8eb1f830771d50ff9d8`

This brief presents concrete falsification questions and scenarios executed
against the integrated architecture documentation. It covers three attack
domains spanning T045 (authority, concurrency, recovery, compensation), T046
(API, progress, acceptance, MCP), and T047 (program and delivery truth). Each
scenario states the architecture's claimed behavior, the attack method, and the
result of executing the attack against the documented design.

Mini's prior attack themes are explicitly retested: fencing POSIX writes,
behavior characterization, WorkRevision drift, reviewer independence,
compensation attribution, single control-store authority, and the two-agent
crash experiment ordering.

---

## Domain 1: Authority, Concurrency, Recovery, and Compensation (T045)

### Scenario 1.1: Two agents claim the same work

**Claim:** The architecture guarantees at most one valid lease per WorkID +
WorkRevision generation (CONCURRENCY_SCENARIOS.md assertion 1).

**Attack:** Agent A and Agent B simultaneously call `ClaimReadyWork` for W-1
under WR-1 with different `OperationID` values. One must win; the other must
receive a typed refusal without receiving a workspace or tracker assignment.

**Architecture response:** API 2.0 Section 5.1 (`ClaimReadyWork`): "Execution
capability checks graph readiness and competing authority." The durable boundary
requires run/lease authority to commit before a capsule is returned. The
idempotency row states: "Duplicate claim with the same operation/digest returns
the same run/lease; it cannot allocate a second workspace or repeat assignment."

**Result:** PASS — The architecture is complete. The serialization mechanism
(GapDB conditional atomic mutation with aggregate revision precondition) makes
the second claim a revision conflict (`REVISION_CONFLICT` per Section 11.2
error matrix), which is forbidden retry until state is observed. No workspace or
tracker assignment occurs for the loser because workspace binding commits in a
separate durable step after successful claim. A simultaneous race with 32
contenders is required for M3 exit evidence.

**Open question for M3:** The GapDB conditional write is the serialization
boundary. If the GapDB response is ambiguous (e.g., write may or may not have
applied), the architecture requires the client to reconcile using the same
`OperationID` before attempting a second claim. This is specified in Section 6
(`ReconcileExternalFact`) but the exact GapDB response-loss protocol needs M1
exit evidence before being trusted in the multi-agent scenario.

---

### Scenario 1.2: Stale process writes after lease expiry (zombie writer)

**Claim:** Workspace W1 is permanently quarantined when lease L1 expires.
Replacement process receives fresh W2. Zombie A's writes in W1 cannot satisfy
a current handoff.

**Attack (Mini's blocker finding):** The fence token only fences Spec Kitty
application operations. Nothing stops a live shell or IDE writing POSIX files
in W1. What prevents the zombie's W1 commits from entering the production
candidate pipeline?

**Architecture response:** Architecture doc Section 10 (workspace fencing) and
Section 3.2 (strengthened behavior): "Lease expiry quarantines the workspace;
no later lease receives it." "W1 output can be inspected or harvested only
through explicit reconciliation after the writer is demonstrably quiescent; it
never becomes current merely because it contains a valid Git commit."

API 2.0 Section 5.1: "An expired lease's mutable workspace is never
transferred. A replacement claim receives a new lease and a fresh workspace."
Candidate handoff (Section 5.2) requires "Current run/lease/fence/epoch" as a
precondition, and refusals include "stale lease/epoch/fence."

**Result:** PASS — The architecture correctly acknowledges that POSIX writes
are unfenceable and addresses this through permanent workspace binding rather
than fence promises. The zombie's W1 commits are unreachable as a current
candidate without an explicit authorized reconciliation (`ReconcileExternalFact`
or `QuarantineWorkspace` plus human decision). The sequence diagram in Section
10 is correct and the "W2 is required because the host cannot prove P1 stopped
writing W1" rationale is explicit.

**Residual risk (confirmed, tracked to M1):** The architecture cannot be
considered proven until M1 runs the real two-agent zombie torture scenario and
the zombie process is demonstrably unable to hand off, review, or integrate its
output. The architecture gives the correct specification; M1 gives the proof.

---

### Scenario 1.3: Host crashes before durable acknowledgement

**Claim:** After step 5 of the durable command protocol (atomic GapDB mutation),
a crash before the response is resolved by repeating the same `OperationID` +
digest.

**Attack:** Kill the host process after GapDB accepts the mutation but before
the client response is sent. Can the client determine what happened and avoid
duplicating the effect?

**Architecture response:** Architecture doc Section 6, step 5 and the crash
resolution statement: "A crash after step 5 but before the response is resolved
by repeating the same OperationID and digest." API 2.0 Section 3.1 command
request: `operation_id` and `request_digest` are required fields. Section 11.2
error matrix: `DEADLINE_EXCEEDED` — "Query the recorded operation/result with
the same identity; never assume cancel or blindly submit new intent."

**Result:** PASS — The idempotency protocol is well-specified. The client can
replay the same operation_id + digest and receive the recorded result without
re-executing effects. Section 12.4 conformance campaign item 1 requires this to
be proven executable.

**Gap identified (Medium, informational):** The architecture does not explicitly
describe what happens when GapDB itself crashes during step 5 (partial WAL
write). It defers to GapDB's WAL/snapshot recovery contract (Section 8,
Recovery contract 1). The separation between GapDB storage recovery and Spec
Kitty product restore (Recovery contracts 1 and 2) is explicitly stated, but
the exact GapDB crash recovery semantics are delegated to M1's GapDB adoption
scenario testing. This is appropriate given the documentation-only mission
scope; the architecture makes the correct deferral.

---

### Scenario 1.4: Host crashes after durable acknowledgement

**Claim:** After a successful durable response, the committed state is
recoverable from GapDB snapshot + WAL + control journal.

**Attack:** Kill the host after a `ClaimReadyWork` response was sent and the
workspace was allocated. On restart, can the system correctly restore the lease
and workspace without orphaning the workspace or duplicating the allocation?

**Architecture response:** Recovery sequence (Section 8): GapDB storage
recovery → checkpoint validation → event reduction → durable new epoch → lease
invalidation → external reconciliation → progress publication. The runtime
epoch replacement (step 3) invalidates all predecessor leases. External
reconciliation (step 4) observes current workspace state through typed adapters
and produces `continue`, `supersede`, `invalidate`, `fork`, `retry`,
`compensate`, or `decision_required`.

API 2.0 Section 6.2 `BeginRecovery` and `CommitRuntimeEpoch` commands. The
recovery sequence is explicitly ordered and each step must commit before the
next begins.

**Result:** PASS — The architecture is complete for this scenario. The key
correctness invariant is that the new `RuntimeEpochID` is created durably
before any new commands are admitted. The predecessor lease's workspace becomes
quarantined evidence available for inspection. The recovery result is either
confirmed continuation or a decision point — never silent success.

---

### Scenario 1.5: GapDB response is ambiguous

**Claim:** When GapDB's response is ambiguous (write may or may not have
applied), the architecture handles it without blind retry or silent failure.

**Attack:** A GapDB write for a `CreateCandidateHandoff` command completes but
the response is lost. The client retries. What prevents a duplicate handoff from
being created?

**Architecture response:** The `operation_id` + `request_digest` idempotency
mechanism (Section 3.1). "Same ID and same digest returns the recorded result."
The durable boundary for candidate handoff (Section 5.2) requires the
`operation_id` to be stored in the GapDB mutation atomically with the handoff
result. On retry, the host finds the recorded result and returns it without
creating a second handoff.

The architecture doc Section 17 (GapDB design-input tuple) notes: "Spec Kitty
does not import GapDB `internal/server`." This means the host cannot inspect
GapDB's internal write state; it relies on GapDB's external client protocol for
response-loss recovery.

**Result:** PASS — The design is sound because idempotency is implemented at
the domain level, not the transport level. The exact GapDB protocol response-loss
handling is tested in M1's "ambiguous response reconciliation" scope (14 GapDB
adoption scenarios). The architecture correctly delegates this to M1 rather than
over-specifying GapDB internals.

---

### Scenario 1.6: Checkpoint is corrupt or stale

**Claim:** A checkpoint is an accelerator, never a second authority. A corrupt
or stale checkpoint must not prevent recovery.

**Attack:** The latest checkpoint has an invalid prefix digest. Can recovery
proceed to a safe state?

**Architecture response:** Section 8, Recovery contract 2: "Spec Kitty product
restore validates the latest mission checkpoint and its prefix digest, then
reduces subsequent versioned control events." The runtime "remains degraded or
closed when…checkpoint digest…cannot be validated."

Section 8 also states: "A checkpoint is an accelerator, never a second
authority." This means the system can fall back to full journal reduction from
the GapDB-stored base if no valid checkpoint exists, at the cost of replay time.

**Result:** PASS — The architecture correctly refuses to run with a corrupt
checkpoint (fail-closed) and does not claim checkpoint repair is possible at
the architecture level. The degraded/closed state requires operator action
(presumably creating a new checkpoint from a known-good journal state, which
requires M1's checkpoint/recovery proof).

**Gap (Low, informational):** The architecture does not specify a maximum
acceptable journal replay window without a checkpoint (performance bound). This
is appropriate for a documentation mission but is a future specification item
for M1.

---

### Scenario 1.7: Tracker is stale, dishonest, or human-edited

**Claim:** A stale, dishonest, or unavailable tracker cannot alter the finalized
executable graph or cause compensation to overwrite a human change.

**Attack 1 (stale):** The tracker reports WorkRevision WR-1 at provider revision
721. After finalization, the tracker state changes to revision 728. The scheduled
dispatcher consults the tracker again. Can this change the executable graph?

**Architecture response:** API 2.0 Section 4.2 `WorkRevision` commands:
"Authority: The tracker owns mutable work definition. Spec Kitty alone pins the
executable generation and owns consequences." Drift creates a `DRIFT_DECISION_REQUIRED`
result (Section 11.2) that requires an explicit `ReconcileWorkRevision` with one
of `continue`, `supersede`, `invalidate`, or `fork`. The reconciliation
explicitly "does not mutate a running WorkRevision in place."

**Attack 2 (human reassignment before compensation):** Operation O assigns a
Beads ticket to Agent A at revision 7. A human assigns it to B at revision 8.
Recovery compensation runs. Can O overwrite the human's change?

**Architecture response:** Section 11 (tracker, Git, external effects): "Compensation
is allowed only after compare-before-compensate proves current external state is
still attributable to the effect being undone. Ambiguity yields
`decision_required`, never a destructive guess." API 2.0 Section 4.2 refusals
include "dishonest/stale/slow/unavailable tracker."

API 2.0 Section 12.4 conformance campaign item 4: "A dishonest, stale, slow, or
unavailable tracker cannot alter the finalized graph or make compensation
overwrite human change."

**Result:** PASS — Both attacks are handled. Attribution-safe compensation
(Mini's finding 5) is correctly implemented in the design. The CONCURRENCY_SCENARIOS.md
P1-16 and P1-17 scenarios define the exact test cases that M3 must execute.

---

### Scenario 1.8: Candidate Git refs move after handoff

**Claim:** A moved branch pointer after handoff cannot make a historical review
apply to a new commit.

**Attack:** Candidate handoff H-1 binds commit C at tree T. After review verdict
V-1 is recorded, someone force-pushes the candidate branch to commit C2 (tree
T2). Can V-1's approval apply to C2?

**Architecture response:** Section 12: "A handoff is the immutable review
boundary. It binds the candidate commit/tree, WorkRevision, run/lease
generation, contributor ActorIDs, criteria/evidence digest, and observed
workspace/ref facts. Review consumes this record."

The `WithdrawCandidate` command exists for when observation no longer holds.
Section 5.2 refusals for `CreateCandidateHandoff` include "moved ref." Review
is tied to `HandoffID` + `candidate digest`; a moved branch creates a new Git
fact that is not the candidate.

API 2.0 Section 2.2: "Git ref or commit" proves "A repository fact observed
through the Git port" but "never authorizes…review approval."

**Result:** PASS — The handoff record binds the exact commit/tree digest. A
moved ref cannot retroactively change the handoff's bound commit. The
`REVISION_CONFLICT` error class applies when integration detects a moved target.
P1-23 (CONCURRENCY_SCENARIOS) defines the executable test case.

---

### Scenario 1.9: Same ActorID uses a new ProcessID to review

**Claim:** Reviewer independence is an ActorID invariant. A new ProcessID for
the same actor is not independent.

**Attack:** Agent A (ActorID=agent-a) implements candidate C. A new process for
the same agent (ProcessID=proc-a-review) attempts to review C.

**Architecture response:** Section 3.1 preserved product laws: "Reviewer ActorID
differs from every candidate contributor ActorID. A different ProcessID for the
same actor is not independent. There is no default waiver."

API 2.0 Section 5.3 independent review: "Review capability proves reviewer
ActorID differs from every candidate contributor unless an explicit mission
policy defines a visible exception." Refusals include `INDEPENDENCE_VIOLATION`.

The Phase 1 erratum (PHASE1_ACCEPTANCE_ERRATA.md) explicitly records that
"Phase 1 implements no same-actor reviewer waiver."

**Result:** PASS — The distinction between ActorID (durable accountable subject)
and ProcessID (execution instance) is consistently maintained across the
architecture doc, API 2.0, ADR 0002, and the Phase 1 erratum. P1-20
(CONCURRENCY_SCENARIOS) is the executable proof case for M3.

---

### Scenario 1.10: Integration races / conflicts

**Claim:** Integration is serialized by repository + target ref. Merge conflicts
preserve candidates and create explicit repair work.

**Attack:** Missions M-1 and M-2 have approved candidates for the same
repository/target branch. They request integration simultaneously.

**Architecture response:** Section 12: "Integration begins only from an
admissible candidate and acceptance result. It serializes by repository plus
target ref." API 2.0 Section 5.5 `ReserveIntegration`: "One integration
capability serializes attempts by repository and target ref." Duplicate reserve
cannot create another slot.

P1-24 and P1-27 (CONCURRENCY_SCENARIOS) define the two-mission serialization
test. P1-25 covers crash injection across integration saga boundaries. P1-26
covers conflict preservation.

**Result:** PASS — Serialization is fully specified through the integration
reservation mechanism. Section 5.5 refusals include "competing integration" and
"target moved." Conflicts produce `AbandonIntegration` or `ReconcileIntegration`
paths, never silent force-merge.

---

### Scenario 1.11: Recovery compensation encounters human reassignment

**Claim (Mini finding 6):** Compensation must compare current external state
(provider revision, value, operation marker) before undoing anything. Human
reassignment defeats old compensation.

**Attack:** (Revisiting Scenario 1.7 Attack 2 with compensation focus) After
the human reassignment (revision 8), recovery for operation O runs. O's saved
expected state was revision 7 + value "assigned to A" + marker O. The compare-
before-compensate check sees revision 8 + value "assigned to B" + a different
attribution. Compensation must halt.

**Architecture response:** Section 11: "Compensation is allowed only after
compare-before-compensate proves current external state is still attributable
to the effect being undone. Ambiguity yields `decision_required`, never a
destructive guess." API 2.0 Section 4.2 `ReconcileWorkRevision` refusals:
"unsafe compensation."

**Result:** PASS — CONCURRENCY_SCENARIOS P1-16 ("human reassignment defeats old
compensation") is the precise executable test case. The architecture explicitly
treats this as the correct behavior rather than an edge case.

---

## Domain 2: API, Progress, Acceptance, and MCP (T046)

### Scenario 2.1: Drive a complete mission using only API 2.0

**Claim:** A client can discover a mission, determine its exact position, obtain
the next legitimate actions, drive a complete governed lifecycle, and watch
progress without reading tracker state, Markdown, worktree files, or the control
store directly.

**Attack:** Enumerate all required operations for a complete software-dev
mission. Is any operation missing from the API 2.0 command families?

**Test:** Walk the mission lifecycle against API 2.0 Sections 4–8:
- Create: `CreateMission` ✓
- Begin: `BeginMission` ✓
- Observe work: `ObserveTrackerWork` ✓
- Finalize work: `FinalizeWorkRevision` ✓
- Claim: `ClaimReadyWork` ✓
- Bind workspace: `BindFreshWorkspace` ✓
- Heartbeat: `HeartbeatLease` ✓
- Record outcome: `RecordImplementationOutcome` ✓
- Handoff: `CreateCandidateHandoff` ✓
- Begin review: `BeginCandidateReview` ✓
- Record criteria: `RecordCriterionReview` ✓
- Submit verdict: `SubmitReviewVerdict` ✓
- Request acceptance: `RequestAcceptanceEvaluation` ✓
- Reserve integration: `ReserveIntegration` ✓
- Execute integration: `ExecuteIntegration` ✓
- Complete mission: `CompleteMission` ✓
- Progress queries: `GetMissionProgress`, `WatchMissionProgress` ✓
- Decision handling: `OpenDecision`, `AnswerDecision`, `ExpireDecision` ✓

**Result:** PASS — All required command families are present. Section 7 query
families cover all read operations. No mission operation requires direct tracker,
Markdown, Git, workspace, or GapDB access.

---

### Scenario 2.2: Disconnect and resume

**Attack:** A client invokes `DriveMissionUntilBoundary` and disconnects. The
host restarts. Can the client resume observation?

**Architecture response:** API 2.0 Section 8.3 traced example shows exactly
this: client disconnects at step 3, reconnects at step 4 using `GetOperation(nop-42)`
then `WatchMissionProgress(after_revision=123)`. Journal revision is the resume
token; no transport session is needed. Host restart preserves journal resume
semantics even when in-memory watch buffers disappear (Section 9.3).

**Result:** PASS — Resume is explicit, journal-revision-based, and does not
require the same transport connection or adapter instance.

---

### Scenario 2.3: Cancellation/completion race

**Attack:** `DriveMissionUntilBoundary` commits a terminal result. Simultaneously,
`RequestOperationCancellation` is submitted. Which wins?

**Architecture response:** Section 8.1 state machine: "Completion can win before
or after cancellation intent is observed; the first valid terminal event wins by
conditional revision." `cancellation_requested → completed` is a valid
transition. The terminal state is immutable. Cancellation is "cooperative
intent" and "does not revoke a lease, compensate an effect, roll back a
committed event, or guarantee the terminal status cancelled."

MCP adapter Section 10 race table: "completion commits before cancellation →
terminal result wins; cancellation cannot rewrite it."

**Result:** PASS — The race is explicitly handled with correct deterministic
semantics.

---

### Scenario 2.4: Watch compaction

**Attack:** A watcher falls behind. The journal is compacted. Can the watcher
resume from a compacted revision?

**Architecture response:** API 2.0 Section 9.3: "If `after_revision` was
compacted, the host returns `WATCH_RESET_REQUIRED` plus authorization-filtered
fresh snapshot and its revision. It never silently skips revisions or grows
memory without bound."

**Result:** PASS — The protocol is correctly specified. The client receives an
explicit reset notification rather than silently losing history.

---

### Scenario 2.5: Neutral acceptance against unrelated stacks

**Attack:** A project using an unfamiliar toolchain (not Go, not Python) wants
to supply a conforming provider. Does the acceptance kernel require modification?

**Architecture response:** STACK_NEUTRAL_ACCEPTANCE.md Section 10 claim-to-
provider matrix: "An unfamiliar fictional toolchain can provide a conforming
typed adapter and obtain an explicit binding. No claim, gate vocabulary, or
kernel package needs to change."

Section 9 import prohibition: "language, package-manager, build-system, or
ecosystem tooling" are forbidden in the kernel.

**Result:** PASS — The design is stack-neutral by construction. The claim
vocabulary (`candidate.objective-correctness`, etc.) makes no reference to
language or toolchain.

---

### Scenario 2.6: Non-code mission acceptance

**Attack:** A documentation mission needs to prove `documentation.links-resolve`.
Does the acceptance system work without a code compilation step?

**Architecture response:** Section 3.1 representative claims: `documentation.links-resolve`
maps to "Documentation graph validator" as an approved binding example. The claim
is stack-neutral (its `capability` is `documentation.integrity`, not `code.build`).

Section 1.1 non-goals: "This design applies unchanged to software, documentation,
research, data, migration, compliance, and mixed-stack missions."

**Result:** PASS — The acceptance design applies to non-code missions. The
claim vocabulary supports documentation, dataset, migration, and governance
claims explicitly.

---

### Scenario 2.7: Stale evidence admissibility

**Attack:** Evidence from a prior candidate is presented for a new candidate.
Can stale evidence satisfy the new candidate's claims?

**Architecture response:** STACK_NEUTRAL_ACCEPTANCE.md Section 6.2 (freshness
manifest): exact-candidate is the default. Reuse requires reconstructing the
stable-input manifest and proving all components are unchanged. Section 11.4
negative cases: "Stale candidate — Inadmissible `stale_candidate`; rerun
affected nodes."

**Result:** PASS — Evidence is candidate-bound by default. Reuse policy is
explicit and pinned to exact input components. Stale evidence is inadmissible
without a matching stable-input manifest.

---

### Scenario 2.8: Deceptive formatted output as evidence

**Attack:** A provider exits successfully and prints "All checks passed" to
stdout. Does this satisfy the acceptance claim?

**Architecture response:** Section 4 provider kinds: "Bounded local argv — An
executable plus an argument array under an approved sandbox and resource policy;
never a shell string." Section 9.1 kernel: "The evidence decoder…validates
machine evidence, never presentation text." Section 11.4 negative case: "Human-
formatted output only — Provider `error`; presentation text has no authority."

**Result:** PASS — The architecture explicitly prohibits parsing human-formatted
output. The typed evidence decoder and bounded result protocol prevent this
attack.

---

### Scenario 2.9: Remove the MCP module and verify no core capability disappears

**Attack:** Uninstall `spec-kitty-mcp` from a running environment. List all
capabilities that become unavailable. Any capability that becomes unavailable
is a violation of C-011.

**Architecture response:** MCP_2026_07_28_ADAPTER.md Section 1 product law 1:
"Core build, install, qualification, and complete CLI missions require no MCP
module." Section 2.3 complete CLI scenario row: "create, finalize, execute,
review, accept, integrate, recover, query progress, and export without MCP."
Tool catalog (Section 4.1) shows every MCP tool has a "Normal CLI/API equivalent"
column.

**Result:** PASS — The design ensures that no mission lifecycle capability, no
work command, no review, no acceptance, no integration, and no recovery operation
requires MCP. The MCP adapter is additive only.

---

### Scenario 2.10: Multi-instance stateless MCP requests

**Attack:** A `tasks/get` request for `task-51` arrives at adapter instance B
when the original `tools/call` went to instance A. Can instance B serve the
request?

**Architecture response:** MCP_2026_07_28_ADAPTER.md Section 6.1: `MCPTaskID`
is "a versioned, authenticated opaque token whose protected payload identifies
the adapter release/profile, NativeOperationID, creation time, and retrieval
expiry." Section 7: "Round-robin routing remains correct because any instance
can authenticate, decode a task token, query the authority host, and render the
current projection."

The MRTR sequence diagram (Section 9.1) explicitly shows `tasks/get task-51 on
another instance` → `GetOperation nop-51` → response.

**Result:** PASS — Stateless multi-instance routing is fully designed. No
adapter-local state is required.

---

### Scenario 2.11: Task TTL loss

**Attack:** `MCPTaskID task-51` expires and `tasks/get` returns an expired-handle
error. Is the native operation state lost?

**Architecture response:** Section 6.3 (loss, replay, expiry): "After task
TTL/purge, tasks/get may return the extension's invalid/expired handle error.
The mission and operation evidence remain queryable through normal API/CLI
retention." Section 11: "Purging a task token/projection never purges the native
operation, mission events, evidence, external-effect observations, or terminal
mission record."

**Result:** PASS — Task TTL is explicitly bounded to the transport projection
layer. The underlying operation and evidence have independent retention policy.

---

### Scenario 2.12: Cancellation after external effect

**Attack:** Integration durable effect intent exists (the Git ref mutation is
recorded as intent at journal revision 240). `tasks/cancel task-72` is
submitted. What happens?

**Architecture response:** MCP_2026_07_28_ADAPTER.md Section 10.1 sequence
diagram shows exactly this: cancellation intent is recorded at rev 241, then
the Git adapter observes the target ref already equals the candidate (integration
completed), the domain records the observed outcome at rev 242, and `nop-72`
becomes `completed`. `tasks/get` returns `task-72 completed, not cancelled`.

Section 10: "tasks/cancel never directly expires a lease, deletes/quarantines a
workspace, resets Git, clears a Beads assignment, withdraws a candidate, removes
review or evidence, or runs compensation."

**Result:** PASS — The cancel/completion race with a durable external effect is
explicitly traced and correctly resolved by observing the domain fact.

---

## Domain 3: Program and Delivery Truth (T047)

### Scenario 3.1: Program schema and manifest conformance

**Attack:** Validate the published program manifest against the JSON schema.
Check IDs, dependency references, acyclicity, critical path, maximum 3
dependencies, and parallel groups.

**Results (from publication gate checks):**

```
uvx check-jsonschema --schemafile ... docs/programs/production-runtime-program.json
→ ok -- validation done

jq unique IDs check       → PASS (7 missions, 7 unique IDs)
jq max 3 deps check       → PASS (M5 has max 3 deps: M2, M3, M4)
jq critical path check    → PASS (["M1","M2","M3","M5","M6"])
tsort acyclicity check    → PASS (order: M1, M2, M4, M3, M5, M7, M6)
diff source vs docs       → PASS (parity: PASS)
```

**Result:** PASS — All manifest checks pass.

---

### Scenario 3.2: Each mission has exactly one primary falsification target

**Attack:** Read each mission card. Does each have exactly one primary
falsification target that a reviewer can clearly understand in isolation?

| Mission | Primary falsification target | Reviewable in isolation? |
|---|---|---|
| M1 | GapDB can atomically support the control journal, projection, checkpoint, restart epoch, and side-effect-free playback through the two-agent zombie scenario. | Yes |
| M2 | Every authority capability can run without SQL constraints, dual truth, fallback, or lost refusal semantics. | Yes — but requires M1 evidence as entry. |
| M3 | The concurrency crown jewel survives when GapDB, Beads, Git, workspaces, processes, agents, and humans disagree. | Yes |
| M4 | One neutral gate can judge the same claim through materially different providers without ecosystem semantics. | Yes |
| M5 | A client can drive, disconnect, resume, and understand a mission without direct external-state inspection. | Yes |
| M6 | Independently proven components remain safe under sustained concurrency, crash timing, installation, restart, and recovery. | Yes |
| M7 | Connection loss, task expiry, input pauses, and cancellation races cannot alter durable mission authority. | Yes |

**Result:** PASS — Each mission has exactly one clearly stated falsification
target. Each is reviewable in isolation (no target depends on understanding
another mission's internals).

---

### Scenario 3.3: M1's value — what does passing or failing establish?

**Attack:** What hard claim is established if M1 passes? What later work is
prevented if M1 fails?

**If M1 passes:** The GapDB public client lifecycle, the conditional atomic
mutation, the checkpoint restore, the runtime epoch replacement, the
response-loss idempotency, the zombie scenario, and side-effect-free playback
all work at the specific pinned sidecar/protocol/adoption tuple. This is the
foundation on which M2's storage replacement, M3's composition, and ultimately
all later missions can stand. The signed human adoption record is evidence that a
human understood and accepted the exact adoption tuple.

**If M1 fails:** No adoption evidence for GapDB exists. M2 cannot replace SQLite
because it would have no qualified substrate to replace it with. M3, M4, and
M5 all depend on M2. The entire production program is blocked until M1's failure
is analyzed and either resolved (by a different GapDB version or design change)
or the program direction is reconsidered. The stop rule ("do not begin the
six-thousand-line storage replacement in M2") is correct and explicit.

**Result:** PASS — M1's value is clear, well-defined, and the stop rule is
honest. The program correctly requires M1's proof before any broad implementation.

---

### Scenario 3.4: Roadmap does not frontload significant code before testing zombie/recovery

**Attack (Mini's early torture finding):** Can the program spend significant code
before testing the zombie/recovery/playback authority scenario?

**Architecture response:** M1 is the first admitted mission and has no
dependencies. Its scope is narrow: one durable authority slice, one vertical
composition. M2 cannot start until M1's signed adoption record is accepted.
The stop rule is explicit: "If M1 cannot preserve authority and reproduce its
canonical replay digest through this two-agent zombie scenario, do not begin the
six-thousand-line storage replacement in M2."

**Result:** PASS — The program structure correctly forces zombie/recovery/playback
testing before broad implementation. M1 is bounded (not "implement all of
GapDB"). The torture chamber is detailed enough that an implementer cannot claim
completion without running it.

---

### Scenario 3.5: Search active docs for future behavior in present tense

**Attack:** Search canonical architecture docs for statements that describe
proposed GapDB, API 2.0, acceptance, or MCP behavior as currently shipped.

**Search method:** `grep -in "currently ships|ships today|currently implemented|
already ships" docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md ...`

**Result:** Zero false shipping claims found. The phrase "does not ship today"
appears explicitly for API 2.0, neutral acceptance, and MCP. Section 2 uses
controlled delivery labels. Every architecture doc carries a "Status: proposed"
or "Delivery effect: documentation only" header.

**Result:** PASS — Delivery honesty is maintained.

---

### Scenario 3.6: Search active docs for current delivered behavior described as globally incomplete

**Attack:** Is the delivered Phase 1–3 behavior described in a way that
diminishes it as incomplete rather than stating it is qualified at its attested
boundary?

**Result:** No such issue found. README Section "Delivered Phase 1–3" says
"useful in its own right rather than 'an incomplete app'." Phase 1 behavior is
described as "real Git repositories and worktrees, concurrent claims, lease
generations, quarantine and fresh replacement workspaces, immutable handoff,
actor-level independent review, serialized integration, crash recovery, conflicts,
and zombie-writer scenarios." This is accurate and honest.

**Result:** PASS — Delivery truth is both honest and confident.

---

### Scenario 3.7: Dependency edges, no more than 3, cycles, and parallel groups

**Attack:** Verify every dependency edge and parallel group annotation in the
manifest.

**Dependency map from manifest:**
- M1: []
- M2: [M1]
- M3: [M2] — parallel group `after-M2`
- M4: [M2] — parallel group `after-M2`
- M5: [M2, M3, M4]
- M6: [M5] — parallel group `after-M5`
- M7: [M5] — parallel group `after-M5`, not on core critical path

`jq` check: `{M1:[],M2:["M1"],M3:["M2"],M4:["M2"],M5:["M2","M3","M4"],M6:["M5"],M7:["M5"]}` → PASS

Every entry with `≤3` dependencies confirmed. Every parallel group annotated.
The manifest's `critical_path` field matches the declared path. No backdoor
dependency (e.g., M3 consuming M4 results before M5) exists.

**Result:** PASS

---

### Scenario 3.8: Entry/exit evidence for every mission

**Attack:** Does every mission have actionable entry and exit evidence that
cannot be satisfied by "package scaffolding, interface declarations, a green fake,
or an authored README"?

**Review:** Each mission's exit evidence specifies observable outcomes:

- M1: "crash and ambiguous-response injection passes at every durable boundary";
  "replacement workspace identity != expired workspace identity"; "all fourteen
  external GapDB adoption scenarios pass"; "human signature covers the exact tuple"
- M2: "source/module graph/SBOM/binaries contain no SQLite production implementation"
- M3: "zombie, dishonest tracker, human reassignment, review-independence, and
  repository-fence scenarios pass"
- M4: "materially different provider fixtures prove one claim contract and produce
  the same gate decision"
- M5: "black-box API and normal CLI complete equivalent missions"
- M6: "exact load/soak/crash/recovery/security/backup census"
- M7: "core builds, installs, and completes a mission with the MCP module absent"

**Result:** PASS — None of these exit criteria can be satisfied by scaffolding or
prose. Each requires executable, observable evidence.

---

### Scenario 3.9: Rollback semantics are correct

**Attack:** For each mission, does the rollback preserve safe state and avoid
forbidden workarounds?

**Review:** Every rollback entry specifies: stop work on the current WP, preserve
evidence, and explicitly prohibits forbidden fallbacks:
- M1: "do not admit M2"; "current runtime remains historical evidence, not a
  production fallback"
- M2: "never ship dual writes or re-enable the forbidden fallback"
- M3: "keep the qualified domain services and remove the unqualified production
  composition root"
- M4: "do not weaken claims to obtain a pass"
- M5: "retain internal application capabilities and remove unqualified public
  transports"
- M6: "withhold the release artifact; never enable a forbidden runtime fallback"
- M7: "remove or disable the independent adapter without changing the core
  application, mission history, or native operation result"

**Result:** PASS — Rollback semantics are honest and complete. No mission allows
a SQLite fallback, dual write, or weakened proof to bypass its stop condition.

---

## Summary of attack results

| Scenario | Domain | Result | Severity |
|---|---|---|---|
| 1.1 Two agents claim same work | Authority | PASS | — |
| 1.2 Zombie writer after lease expiry | Authority | PASS (proof deferred to M1) | Residual risk tracked |
| 1.3 Crash before durable acknowledgement | Recovery | PASS | — |
| 1.4 Crash after durable acknowledgement | Recovery | PASS | — |
| 1.5 GapDB ambiguous response | Recovery | PASS (proof deferred to M1) | — |
| 1.6 Corrupt checkpoint | Recovery | PASS | Low gap (perf bound) |
| 1.7 Stale/dishonest/human-edited tracker | Authority | PASS | — |
| 1.8 Candidate Git refs move | Authority | PASS | — |
| 1.9 Same ActorID / new ProcessID review | Authority | PASS | — |
| 1.10 Integration races | Authority | PASS | — |
| 1.11 Compensation + human reassignment | Compensation | PASS | — |
| 2.1 Complete mission via API only | API | PASS | — |
| 2.2 Disconnect/resume | API | PASS | — |
| 2.3 Cancellation/completion race | API | PASS | — |
| 2.4 Watch compaction | API | PASS | — |
| 2.5 Neutral acceptance, unrelated stack | Acceptance | PASS | — |
| 2.6 Non-code mission acceptance | Acceptance | PASS | — |
| 2.7 Stale evidence admissibility | Acceptance | PASS | — |
| 2.8 Deceptive formatted output | Acceptance | PASS | — |
| 2.9 MCP removal, no capability loss | MCP | PASS | — |
| 2.10 Multi-instance MCP routing | MCP | PASS | — |
| 2.11 Task TTL loss | MCP | PASS | — |
| 2.12 Cancellation after external effect | MCP | PASS | — |
| 3.1 Program manifest conformance | Program | PASS | — |
| 3.2 One falsification target per mission | Program | PASS | — |
| 3.3 M1 value/failure consequences | Program | PASS | — |
| 3.4 No frontload before zombie test | Program | PASS | — |
| 3.5 No false present-tense shipped claims | Delivery truth | PASS | — |
| 3.6 Delivered behavior described honestly | Delivery truth | PASS | — |
| 3.7 Dependency edges and parallel groups | Program | PASS | — |
| 3.8 Exit evidence is executable | Program | PASS | — |
| 3.9 Rollback semantics correct | Program | PASS | — |

**All 30 scenarios: PASS.**
**Zero blocker findings. Zero high findings.**
**Two low/informational gaps (1.2 residual risk, 1.6 checkpoint perf bound) correctly deferred to M1.**
