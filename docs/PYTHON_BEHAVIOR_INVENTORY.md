# Python Spec Kitty Behavioral Inventory

Status: ratified behavioral baseline, amended by Phase 1 closeout

Characterized: 2026-08-23

Python source: `Priivacy-ai/spec-kitty` at
`d060cff9a5c9f8cf369c8786e5bf9b4f89931d0a`

Scope: behavior and product semantics, not Python package parity

Closeout correction: Phase 1 implements no review-independence waiver or
general acceptance bypass. The completed proving-ground mission's contrary
acceptance wording is preserved as history and corrected by
[`PHASE1_ACCEPTANCE_ERRATA.md`](PHASE1_ACCEPTANCE_ERRATA.md). Any future
exception would be a separately specified and ratified capability, not a
shipped Phase 1 path.

## 1. Decision

The proposed Go architecture survives behavioral characterization, with the
amendments in section 11. It preserves the part of Spec Kitty that is unusually
valuable: governed missions, dependency-aware parallel execution, isolated Git
work, independent review, acceptance before integration, and recoverable
serialized merge.

The review also confirms that a source-level or package-level port would carry
substantial accidental complexity into Go. In particular, Markdown task
mutation, Git-committed runtime bookkeeping, PRIMARY/COORD artifact routing,
sparse-checkout exclusions, frontmatter state, and ambient-current-directory
dispatch are representations or historical repairs. They are not the product.

The architecture may proceed to foundational ADR ratification after the seven
amendments in section 11 are folded into the project plan. Phase 1 remains a
disproving experiment: failure of the two-agent crash-and-zombie scenario must
reopen the authority, persistence, or workspace ADRs before Beads is added.

## 2. Evidence method

Evidence was ranked as follows:

1. Current executable tests and current source behavior.
2. Accepted ADRs and current canonical operator/agent documentation.
3. GitHub incidents and merged repairs demonstrating behavior under failure.
4. Proposed ADRs and roadmap issues, used as intent rather than shipped fact.
5. ThickTicket and historical Beads plans, used as design references only.

The following labels are used throughout:

| Treatment | Meaning |
|---|---|
| **Exact** | Preserve the observable rule and its failure posture. |
| **Semantic** | Preserve the outcome/invariant using a Go-native representation. |
| **Strengthen** | Preserve product intent while deliberately closing a known gap. |
| **Projection** | Offer compatibility output, but never make it authoritative. |
| **Retire** | Deliberately omit the mechanism or behavior, with rationale. |
| **Defer** | Record the behavior now; implement it after the first software-development path. |

Focused verification performed against the pinned Python commit:

```text
100 passed, 1 skipped
  lane computation
  lane worktree allocation atomicity
  status emit durability
  merge-state persistence

34 passed, 1 skipped
  merge resume
  acceptance behavior
  orchestrator-api contract
```

No Python source or tracked artifact was modified by this review.

## 3. The product behavior to preserve

### 3.1 Mission method and governance

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| M-01 | A mission is a governed lifecycle, not an epic inferred from ticket statuses. | **Exact** | Mission state and gates remain Spec Kitty authority. Tracker events can request reconciliation but cannot advance a mission. | Built-in mission definitions; mission runner tests; current charter. |
| M-02 | Mission type resolution is explicit and fails loudly when missing or unsupported. It must not silently become `software-dev`. | **Exact** | One canonical mission-type reader; typed error on absent/invalid type. | Accepted ADRs `2026-07-14-2` and `2026-08-22-1`. |
| M-03 | The software-development lifecycle distinguishes discovery/specification, planning/decomposition, implementation, review, acceptance, merge, and closeout. | **Semantic** | Typed Go steps may regroup UI labels, but may not collapse authority boundaries. | `packs/built-in/missions/software-dev/mission.yaml`; mission integration tests. |
| M-04 | Implementation begins only after a finalized executable work graph exists. | **Exact** | A `WorkRevision` is mandatory before claims; no runtime reconstruction from mutable Markdown. | Finalize path, lane manifest requirements, genesis behavior. |
| M-05 | `next` is the simple agent-facing control loop and returns a legal next action or a typed blocked/decision/terminal outcome. | **Semantic** | Preserve the compact command/query experience over typed application use cases. | Current runtime `next` path and finalized-task routing tests. |
| M-06 | Governance and profile context are selected for the action being performed, with traceable resolved identity. | **Semantic** | Store governance/profile revision on runs; supply an action-scoped capsule rather than reproducing prompt-file assembly. | Charter, doctrine runtime, resolved-binding events. |
| M-07 | Research, documentation, plan, and custom mission behavior is real product surface, but need not block proving software development first. | **Defer** | Inventory and reserve the mission registry contract now; add implementations after the first vertical path. | Four built-in mission definitions and mission-type tests. |
| M-08 | Mission review and retrospective occur after merge; retrospective is learning, not retroactive implementation authority. | **Semantic** | Post-merge audit and non-blocking retrospective remain distinct use cases. | Current agent guidance and post-merge tests. |

The current built-in workflows provide these minimum semantic reference points:

- `software-dev`: discovery -> specify -> plan/decompose -> implement -> review
  -> done, with review rework returning to implementation;
- `research`: scoping -> methodology -> gathering -> synthesis -> output, with
  an evidence-gathering loop;
- `plan`: goals -> research -> structure -> draft -> review, with revision
  loops;
- `documentation`: a documentation-oriented artifact workflow whose current
  definition is older and should be recharacterized when that mission type is
  scheduled for implementation.

### 3.2 Work graph and finalization

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| W-01 | Work packages are planning, review, dependency, and acceptance units. They are not necessarily worktree units. | **Exact** | Keep `WorkID` separate from `LaneID`, `RunID`, and `WorkspaceID`. | Execution-lanes ADR and lane models. |
| W-02 | Finalization validates ownership, dependencies, and executable structure and fails closed when required ownership is absent. | **Exact** | Validate the graph before pinning a revision; return typed cycle/ownership errors. | `lanes/compute.py`, persistence tests. |
| W-03 | File ownership overlap can require serial execution; dependencies alone do not require collapsing work into one lane. | **Exact** | Lane planning groups overlapping write scopes, while dependency edges remain scheduler edges. | Current lane computation and disjoint fan-in tests. |
| W-04 | The executable graph must not mutate under a running agent. | **Strengthen** | Introduce explicit generation-numbered `WorkRevision`s and reconciliation dispositions. | Current pinning intent plus open issue #2644 exposing stale finalized event authority. |
| W-05 | Planning content has a recorded source revision. Runtime work must not silently rebase onto a later target tip. | **Exact** | Pin planning source commit and graph digest in `WorkRevision`; use them when producing run bases. | Planning-commit ADR, issue #2993, PR #3076. |
| W-06 | A dependency is execution-ready when required predecessors are review-approved or integrated, according to the mission gate. Requiring only `done` would deadlock pre-merge execution. | **Exact** | Model dependency satisfaction levels explicitly; initial software-dev default accepts `approved` or `done`. | WP FSM and acceptance/mission guard tests. |
| W-07 | Re-finalization must never create two unlabelled authorities for one mission graph. | **Strengthen** | Old revisions remain immutable; a new graph is a new generation with explicit affected-work disposition. | Issue #2644. |
| W-08 | The task checklist is not intrinsically required to execute work; terminal WP state is stronger evidence than checked Markdown boxes. | **Retire / Projection** | Tracker work plus structured criteria is canonical. Generate `tasks.md` only for compatibility. | Acceptance normalization deliberately ignores stale unchecked tasks after WPs are terminal; Robert's stated product requirement. |

### 3.3 Lanes, workspaces, and parallel execution

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| C-01 | Independent work can run concurrently in the same project without sharing a mutable checkout. | **Exact** | Separate implementation workspaces and branches; shared transactional control authority. | Execution-lanes ADR; worktree allocator; integration tests. |
| C-02 | A lane is a durable scheduling/integration lineage that may contain ordered WPs. | **Exact** | A lane survives across sequential runs and owns an ordered accepted-head lineage. | `ExecutionLane` model and lane computation. |
| C-03 | A mutable checkout must never be shared by concurrent agents. | **Exact** | One active lease generation per mutable `WorkspaceID`; no mutable workspace transfer. | Existing worktree isolation plus Mini's fencing correction. |
| C-04 | Same-lane sequential work can build on prior accepted same-lane work, but a replacement after expiry must not inherit the expired checkout. | **Strengthen** | Distinguish logical `LaneID` from disposable per-lease `WorkspaceID`. Seed a fresh checkout from a pinned lane head. | Current same-lane reuse behavior, reconciled with zombie-writer impossibility. |
| C-05 | Dependency lane tips are propagated before dependent work starts; retries are idempotent. | **Exact** | Pin the dependency candidate commits used to create a run base and verify them at handoff/review. | Allocator dependency merge behavior; issue #1684 tests. |
| C-06 | Multi-dependency base preparation either succeeds completely or rolls back to the pre-attempt head. | **Exact** | Workspace preparation is a staged saga with atomic publish of `workspace_ready`. | Issue #1915 and merged repair. |
| C-07 | A surviving branch with a missing checkout is recoverable without inventing a new lineage. | **Semantic** | Re-materialize only after branch ownership/base checks; recovery receives a new workspace identity. | Issue #415 and current branch-reattach code. |
| C-08 | Directory existence is not proof that workspace allocation completed. | **Strengthen** | Persist allocation phases and validate every phase on retry; `workspace_ready` is the only readiness authority. | Open issue #3281. |
| C-09 | The user's primary checkout is not an implementation workspace. | **Strengthen** | Always allocate a run workspace, including planning-only work when it mutates files. Read-only planning commands may operate on a scoped primary capability. | Python's special `lane-planning` shortcut is convenient but violates the cleaner isolation rule. |
| C-10 | Dirty, unknown, or manually moved worktrees/branches must not be silently cleaned, deleted, or reused. | **Exact** | Explicit ownership metadata, canonical paths, dirty checks, head/base compare, and decision-required recovery. | Allocator/stale checks and operator guidance. |
| C-11 | Database fencing can reject stale control-plane operations but cannot stop arbitrary writes by a zombie process. | **Strengthen** | Expire authority, quarantine W1, create W2, and consume only recorded commits from valid leases. | Mini review; POSIX semantics. |
| C-12 | A worktree operation must be rooted in an explicit scoped handle, not whichever checkout happens to be current. | **Strengthen** | Go APIs require `WorkspaceID`/repository capability; no ambient-CWD mutation below CLI resolution. | Issue #3129; ThickTicket shadow workspace pattern. |

The crucial model correction is:

```text
LaneID = durable scheduling and integration lineage

RunID + lease generation = one execution authority

WorkspaceID = one disposable mutable checkout, permanently bound to that lease
```

A new run in the same lane may start from the lane's last accepted commit. It
does not receive the old directory or Git index. This preserves Spec Kitty's
successful lane method without pretending a fencing token can fence POSIX.

### 3.4 Work-package lifecycle and durable state

The current Python lifecycle has these observable states:

```text
genesis -> planned -> claimed -> in_progress -> for_review -> in_review
                                              -> approved -> done

active states may enter blocked or canceled
review rejection returns to in_progress or planned with evidence
done and canceled are terminal except explicit recovery/force paths
```

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| S-01 | Finalization seeds a real `planned` transition; missing initialization is not silently treated as ready. | **Exact** | Revision finalization transaction creates work runtime eligibility. | Genesis ADR and lifecycle tests. |
| S-02 | Claim and start are atomic from the caller's perspective. A same-attempt retry resumes; a competing actor conflicts. | **Semantic / Strengthen** | One CAS creates a run lease and initial execution state. Idempotency is keyed by operation/run, not merely actor name. | `start_implementation_status`, feature lock. |
| S-03 | Runtime state has one canonical durable authority and projections can be rebuilt. | **Exact** | SQLite event/command records are authority; JSON/status views are projections. | Append-only event-log ADR, reducer recovery tests. |
| S-04 | State mutations and related typed annotations can be one durability unit. | **Exact** | One DB transaction writes transition, binding, evidence metadata, and outbox rows. | Atomic status emit path. |
| S-05 | Concurrent state writes do not corrupt or silently drop the log. | **Exact** | Database transaction/uniqueness tests with real processes. | Status durability and stress tests. |
| S-06 | Blockers, handoffs, reviewer results, runtime binding, and tracker references are durable and queryable outside transcripts. | **Semantic** | First-class typed records; human discussion may be mirrored to the tracker. | `InnerStateChanged` payload and reducer. |
| S-07 | `approved` means review passed; `done` means the accepted work was integrated. | **Exact** | Keep separate states and gate meanings. Acceptance may accept approved work; merge produces done. | Accepted ADR `2026-04-03-2`; acceptance tests. |
| S-08 | Recovery/force operations are exceptional, actor-attributed, and reasoned. | **Exact** | Typed admin/recovery commands, not a general bypass flag. | WP FSM force requirements and banned-orchestrator flags. |

The JSONL implementation is not itself sacred. Its important properties are
durability, append-like audit history, deterministic reduction, idempotency,
and reconstructible projections. SQLite can provide those properties without
merging runtime state through Git.

### 3.5 Handoff and review

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| R-01 | Review operates on a submitted implementation candidate, not a live mutable directory. | **Strengthen** | A handoff pins candidate commit, run, revision, criteria, and evidence. | Current review artifacts plus known shared-write races. |
| R-02 | Review is a separately recorded lifecycle with approve/reject evidence. | **Exact** | First-class `ReviewID`, actor, process, candidate, verdict, rationale, and criterion results. | WP FSM structured `ReviewResult`. |
| R-03 | Rejection returns work through an explicit rework attempt and retains prior evidence. | **Exact** | Append a new attempt/review cycle; never overwrite the rejected verdict. | Review transition guards and cycle tests. |
| R-04 | An implementation must not satisfy a gate through self-approval. | **Strengthen** | Reviewer `ActorID` differs from all candidate contributor actors; same actor/different process is not independent, and Phase 1 has no waiver. | Current intent, open issue #3445, Mini review. |
| R-05 | Review evidence must not vanish during concurrent writers or Git index races. | **Strengthen** | Store evidence transactionally in the control DB; if large artifacts use content-addressed blobs referenced by the transaction. | Open issue #3235. |
| R-06 | A verdict applies only to the candidate commit, criteria, and WorkRevision it reviewed. | **Strengthen** | Candidate movement or graph drift makes a verdict stale and ineligible for acceptance. | Current artifact consistency gates; WorkRevision requirement. |
| R-07 | Phase 1 review independence fails closed and has no waiver mechanism. | **Strengthen** | Reject every same-actor review regardless of ProcessID. A future exception would require a separately approved policy, typed authority, visible evidence, and new acceptance criteria; no bypass exists today. | Current gate behavior and `PHASE1_ACCEPTANCE_ERRATA.md`. |

Python currently performs role-aware, best-effort review collision checks. In
some incomplete historical states, a missing runtime role degrades to allowing
a claim, and same-identity self-review is still advisory. The Go rule is an
intentional strengthening, not a claim of byte-for-byte parity.

### 3.6 Acceptance and integration

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| A-01 | Acceptance checks mission-wide readiness and evidence; it is not ticket closure. | **Exact** | Acceptance is a mission command over the pinned graph, reviews, criteria, and Git candidates. | Acceptance package and mission guards. |
| A-02 | Work in `approved` or `done` is acceptable for the pre-merge mission gate. | **Exact** | Accepted work may still be integration-pending. | Acceptance constants and tests. |
| A-03 | Required evidence that cannot be evaluated fails closed or returns a typed cannot-evaluate result; diagnosis mode does not mutate. | **Exact** | Separate diagnostic query from mutating accept command. | Gate execution context and diagnose tests. |
| A-04 | Acceptance is attributable and tied to a source commit. | **Semantic** | Store actor, timestamp, WorkRevision, candidate set, evidence digest, and accepted source ref. | Python acceptance metadata and commit provenance. |
| A-05 | Acceptance matrices express requirement and negative-invariant coverage, but agents should not hand-maintain them. | **Semantic / Strengthen** | Generate the matrix from structured criteria/evidence; allow explicit human verdict commands for genuinely manual criteria. | Current JSON matrix writers and issue #3682. |
| I-01 | Integration is serialized and deterministic after implementation fans out. | **Exact** | One target-branch integration lease/queue, independent of tracker merge slots. | Global merge lock and execution-lane ADR. |
| I-02 | Merge is local and network-free unless publish is explicitly requested. | **Exact** | Git integration command has no remote dependency; push is a separate adapter/use case. | Accepted merge/publish ADR. |
| I-03 | Merge is resumable and retries consult durable outcome evidence rather than trusting a progress cache. | **Exact** | Persist integration phases; verify Git refs and authoritative events before skipping a step. | Issue #416; `MergeState`; issue #2711 repair. |
| I-04 | A conflict preserves all candidate lines and creates an explicit repair path. | **Exact** | No destructive reset of contributor branches; record conflict evidence and repair work. | Merge conflict tests and current resolver behavior. |
| I-05 | `done` is emitted only for integrated work and must be reconcilable after a crash between Git movement and bookkeeping. | **Exact** | Treat Git result and DB bookkeeping as a saga; recovery probes the Git result before replay. | Done-bookkeeping resume reconciliation; issues #2711/#2786. |
| I-06 | Concurrent integrations affecting one target branch cannot both mutate it. | **Exact / Strengthen** | Lock by repository + target ref, not merely mission. | Python global merge lock; product singular-integration law. |
| I-07 | Merge preflight validates clean/owned workspaces, target/base assumptions, review evidence, and artifact consistency. | **Semantic** | Preserve fail-closed checks as typed preconditions without porting file-placement machinery. | Merge preflight package and tests. |

The current Python merge implementation uses a dedicated detached worktree,
mission-scoped resume state, a global lock, and durable cross-checks for `done`
evidence. These are strong behavioral references. The exact JSON state layout,
coordination-ref reconciliation, generated-artifact snapshots, and two-surface
bookkeeping projection are consequences of the current storage design and
should not be recreated.

### 3.7 Trackers and the Beads boundary

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| T-01 | A project selects one primary tracker binding; local and hosted providers expose different capabilities. | **Semantic** | Capability discovery and one writable `WorkCatalog` authority. | Tracker facade/config/service. |
| T-02 | External IDs are mappings, not Spec Kitty's stable work identity. | **Exact** | `WorkID` plus `ExternalWorkRef`; provider rebinding cannot rewrite internal identity. | Tracker mapping store and ticket context. |
| T-03 | Pull, push, and full sync have explicit checkpoints/idempotency behavior. | **Semantic** | Connector-owned cursor and idempotency keys; contract tests against a stupid fake tracker. | Local tracker store/service and SaaS client. |
| T-04 | Provider failure is diagnosable and must not silently erase authority or consent state. | **Exact** | Typed unavailable/stale/schema-skew results; no mutation on failed preflight. | Tracker config preservation and stale-binding behavior. |
| T-05 | Tracker egress is explicit and fails closed when required consent/identity cannot be established. | **Exact** | Separate local-process and hosted-service destinations; never treat configuration as consent. | Tracker egress verdict and sync consent code. |
| T-06 | Ticket assignment/status is useful visible coordination but is not an execution lease, review verdict, mission transition, or Git result. | **Strengthen** | Tracker changes are projections or saga steps around Spec Kitty commands. | Existing gaps, Beads roadmap issue #1173, project authority decision. |
| T-07 | Compensation may undo only state still attributable to its operation. | **Strengthen** | Compare provider revision, assignee/value, and operation marker before compensating. | Mini review; required distributed-saga safety. |

Beads is the preferred initial substrate for `WorkCatalog`, dependency graph,
assignment, discussion, and structured criteria. It is deliberately not the
source of truth for mission lifecycle, run leases, workspaces, reviews,
acceptance, or integration.

The historical Beads 3.3 planning draft linked from issue #1173 proposed moving
nearly all runtime and review authority into Beads. It is valuable as an
operational-requirements source—clean Git, dry-run migration, doctor support,
version drift, backup, and multi-writer behavior—but its authority model is
rejected for this project. Adopting it would turn Spec Kitty into a tracker
extension and discard the separation this rewrite is intended to protect.

### 3.8 Team Kitty and external orchestration

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| X-01 | Local mission work remains possible while hosted sync is unavailable or disabled. | **Exact** | Transaction commits local authority plus an outbox item; delivery is asynchronous and non-authoritative. | Sync outbox/runtime, consent gates, offline tests. |
| X-02 | Remote synchronization is project-scoped, identity-scoped, consent-aware, retryable, and diagnosable. | **Semantic** | Project outbox, delivery cursor, bounded retry policy, dead-letter/blocked reason, explicit purge/repair. | Current project sync store and queue diagnostics. |
| X-03 | Remote/Team Kitty state cannot grant local execution or advance Git. | **Strengthen** | Inbound messages become typed requests, observations, or reconciliation candidates. | Product authority law and current sync boundary. |
| X-04 | The Orchestrator API is a versioned JSON contract over host-owned workflow operations. | **Exact** | Same application commands as CLI/MCP; stable envelope, correlation ID, typed errors, idempotency. | `orchestrator_api/envelope.py`, contract tests. |
| X-05 | External orchestrators supply attributable policy metadata for run-affecting operations and cannot smuggle secrets or banned unsafe flags through it. | **Semantic** | Validate actor/process/client policy at API admission; secrets remain separate capabilities. | Current policy parser and banned flag set. |
| X-06 | Remote orchestrators do not directly mutate host storage or infer file placement. | **Strengthen** | API returns opaque IDs and scoped workspace operations; only host services open SQLite or mutate Git. | Current API statement that CLI owns workflow state; single-machine law. |
| X-07 | API reads and commands fail loudly when the host cannot resolve authoritative mission/workspace state. | **Exact** | Typed `not_found`, `stale`, `conflict`, `cannot_evaluate`, and `decision_required` responses. | Current fail-closed orchestrator command tests. |

The Python API contract is currently version `1.3.0` and JSON-first, but the Go
API should version its own semantic schemas rather than perpetuate every field
that exposes Python artifact placement. Preserve outcomes and typed failures;
translate or retire path-shaped fields where a stable ID is safer.

### 3.9 Recovery, diagnosis, and migration

| ID | Observable contract | Treatment | Go consequence | Evidence |
|---|---|---|---|---|
| D-01 | Read-only diagnosis does not mutate by default. | **Exact** | `doctor` reports facts; each repair is separately named and authorized. | Current doctor/diagnose patterns. |
| D-02 | Recovery is idempotent and validates real durable state before declaring a step complete. | **Exact** | Operation log plus provider/Git probes; cached progress is advisory. | Merge and reducer recovery tests. |
| D-03 | Ambiguous recovery fails loud and asks for mission/operation identity. | **Exact** | No “pick the first active operation” mutation. | Multi-mission merge-state ambiguity behavior. |
| D-04 | A partial external saga remains inspectable and resumable. | **Semantic** | Durable stage, inputs, expected external revision, outcome, and compensation state. | Merge resume and tracker/sync patterns. |
| D-05 | Migration validates before mutation and never invents missing authority. | **Exact** | Dry-run importer, format floor, ambiguity report, receipt, and count/digest verification. | Existing upgrade posture and project plan. |
| D-06 | Unsupported old representations may be projected or explicitly rejected rather than contaminating the kernel. | **Semantic** | Compatibility adapters sit outside domain/application capabilities. | Current accumulation of dual-era readers demonstrates the cost of indefinite live compatibility. |

## 4. Python mechanisms not to port

These mechanisms may contain useful test cases, but they should not determine
the Go package structure or canonical data model.

| Python mechanism | Go disposition | Why |
|---|---|---|
| Markdown task checkboxes as mutable runtime state | **Retire** | Duplicates tracker and execution state; consumes agent time; structurally merge-prone. |
| Work-package frontmatter for assignee/runtime status | **Retire** | Runtime state already migrated toward events; content hashes should describe authored work. |
| `status.events.jsonl` committed and merged through Git | **Retire representation** | Preserve audit/reduction semantics in SQLite; Git is source history, not the concurrent control database. |
| PRIMARY versus COORD artifact partition routing | **Retire** | Repeated wrong-surface incidents show accidental complexity. Use explicit repository/workspace/store capabilities. |
| Sparse-checkout exclusions for shared status files | **Retire** | A workaround for Git-hosted runtime authority. |
| Ambient CWD/worktree inference below the command boundary | **Retire** | Causes wrong-workspace writes; explicit scoped handles are safer. |
| Same physical lane worktree reused across sequential WPs | **Retire physical reuse** | Preserve lane lineage, create a fresh workspace per lease/run. |
| Git index commits for review-cycle concurrency | **Retire** | Shared index races can silently lose evidence. Store review authority transactionally. |
| `tasks.md`, `status.json`, matrices as independent authored authorities | **Projection** | Generate from work, review, and evidence records. |
| Python module/package boundaries and facade proliferation | **Retire** | These reflect evolutionary decomposition, not product capability seams. |
| Backward-compatible silent defaults | **Retire** | Use explicit importer/projection support and fail-loud current commands. |
| Beads agent identity, molecule state, merge slots, or provider Git hooks | **Do not adopt** | They collapse tracker, execution, and integration authority. |

## 5. Known Python gaps that must become negative requirements

The inventory treats these as evidence about the contract, not behaviors to
reproduce.

| Incident | Demonstrated failure | Go negative requirement |
|---|---|---|
| #3281 | A failed planning-base merge can leave a registered worktree; retry may mistake existence for completed allocation. | No retry may bypass durable allocation phases; `workspace_ready` requires verified base/dependency state. |
| #3235 | Concurrent review writers can race in a shared Git worktree/index and silently lose a verdict commit. | Review evidence must either commit transactionally or one writer must receive an explicit conflict. |
| #2644 | Re-finalization can leave old event authority inconsistent with new task/lane files. | Every executable graph is an immutable generation; reconciliation is explicit. |
| #3445 | Same-identity review independence is advisory. | Default independence compares `ActorID`, not only process/role. |
| #3682 | Approval evidence can be prose-only or unbound; first-pass review/acceptance coverage can remain pending. | Verdicts bind actor, process, revision, candidate, criteria, and evidence; acceptance projection must close consistently. |
| #3129 | Ambient checkout location repeatedly directs writes to the wrong authority surface. | All mutation services require scoped repository/workspace capabilities. |
| #3431 | Lane collapse can produce a cyclic lane graph while finalization reports success. | Validate the post-collapse lane DAG before publishing a WorkRevision. |
| #1556 | Cross-machine preclaim cannot be a hard local lock while offline. | V1 has one machine control authority; remote agents claim through its API. |

Earlier repaired incidents remain valuable permanent scenarios:

- #2993: planning corrections disappeared from lane ancestry;
- #1915: a multi-dependency preparation conflict left partial merges;
- #415: crash left branches without recoverable worktrees/state;
- #416: interrupted merge and cleanup were not safely resumable;
- #2711/#2786: Git movement and `done` bookkeeping could become split-brain.

## 6. ThickTicket findings

ThickTicket is a useful prototype reference, not an execution model to adopt.

Worth borrowing:

- a single canonical definition for typed `ProjectID`, `TicketID`, `ActorID`,
  and `ProcessID`;
- a durable actor directory with idempotent external-identity links;
- the shadow-workspace pattern: a returned handle is rooted inside a detached
  worktree and cannot accidentally expose the source root;
- explicit diff, merge-check, and typed conflict operations.

Do not borrow:

- patching a shadow diff directly back into the source checkout as the primary
  integration method;
- its lack of durable leases, heartbeat/expiry, zombie quarantine, dependency
  lane propagation, mission gates, or serialized multi-lane integration;
- prototype assumptions about one ticket/process being the whole mission.

No substantial reusable checklist engine was found. ThickTicket's acceptance
criteria are structured strings in frontier materialization, not a production
replacement for Spec Kitty's mission-wide acceptance method.

## 7. Beads decision

Beads should be the default ticket substrate after Phase 1, behind a deliberately
small provider-neutral contract.

Required initial capabilities:

- create/read/update work descriptions and criteria;
- stable external issue identity and Spec Kitty mapping metadata;
- dependency graph and ready queries;
- assignment and human discussion;
- provider version/schema/capability reporting;
- optimistic revision or equivalent compare support;
- backup/export and diagnostic support.

Explicitly outside Beads authority:

- mission phase and step completion;
- `WorkRevision` acceptance and reconciliation disposition;
- execution leases, fencing, and workspace ownership;
- handoff validity and review independence;
- mission acceptance;
- Git integration ordering and results.

The adapter conformance suite must also run against a deliberately stupid fake
tracker. If a use case only works because Beads happens to supply extra mission
or execution semantics, the boundary has leaked.

## 8. External boundary decision

Team Kitty, the Orchestrator API, MCP, CLI, and future tracker mirrors are
adapters over the same application services.

```text
CLI -----------+
MCP -----------+--> application commands/queries --> capabilities --> stores/Git
Orchestrator --+
Team Kitty <--- durable outbox / projection consumers
```

Rules confirmed by characterization:

- local command success does not wait for Team Kitty delivery;
- every remote delivery is attributable, retryable, and project-scoped;
- inbound remote state does not become a lease or Git command;
- remote agents never open the host control DB;
- command envelopes carry stable IDs and typed results, not authority-bearing
  filesystem paths;
- policy/consent are checked at the actual egress or mutation boundary;
- the API and CLI cannot develop separate workflow rules.

## 9. Compatibility floor

The importer should initially target the current event-log/finalized-lanes era,
with a dry-run classifier for older missions. Exact version-floor selection is
still a focused migration decision, but the policy is now clear:

1. recognize the source representation;
2. validate its internal authority consistency;
3. refuse ambiguous dual-authority state with repair guidance;
4. import mission/work/review/integration facts into canonical Go records;
5. generate compatibility files only after canonical import succeeds;
6. write a receipt containing source commit, counts, mappings, digests, skipped
   history, and warnings.

The importer must explicitly detect at least:

- missing or corrupt `lanes.json`;
- uninitialized/genesis versus finalized status;
- task/lane/event graph disagreement such as #2644;
- approved versus integrated/done work;
- review artifacts not bound to a candidate;
- active or stranded merge state;
- coordination topology with missing/deleted branches;
- dirty or unowned worktrees;
- unsupported mission type/version;
- existing tracker mappings and consent state.

## 10. Parity test ownership

| Behavior family | Planned proving layer |
|---|---|
| Mission transitions and guards | Domain model + Python/Go differential fixtures |
| WorkRevision pinning and reconciliation | Domain model + application scenarios |
| Lane computation | Pure graph/property tests + Python golden fixtures |
| Claim and workspace allocation | Multi-process real-Git fault injection |
| Status durability and idempotency | SQLite transaction/contention tests |
| Review cycles and independence | Application scenarios + policy tests |
| Acceptance coverage | Structured criterion/evidence model tests |
| Merge/integration recovery | Real-Git crash matrix and model tests |
| Tracker behavior | Fake-provider contract, then real Beads integration |
| Team Kitty/offline delivery | Outbox retry, consent, and outage scenarios |
| Orchestrator API | Versioned contract/golden envelope tests |
| Python migration | Fixture corpus, dry-run, round-trip projection checks |

## 11. Required architecture amendments

These changes are required before the foundational ADRs are marked accepted.

### Amendment 1: separate lane lineage from workspace lifetime

Replace any implication that a lane lifecycle and a mutable worktree lifecycle
are the same object. A `LaneID` owns scheduling order and an accepted-head
lineage. Every implementation/review/repair run receives its own
lease-generation-bound `WorkspaceID`. A fresh same-lane run can be based on the
lane head but never inherits an expired directory.

### Amendment 2: make workspace allocation a durable staged saga

At minimum record:

```text
lease_acquired
workspace_reserved
worktree_created
base_verified
dependency_commits_applied
branch_ownership_verified
workspace_ready
```

Retries inspect and continue the operation. Directory or branch existence is
never a shortcut to `workspace_ready`.

### Amendment 3: enrich `WorkRevision`

A generation pins:

- work IDs, descriptions, dependencies, criteria, and provider revisions;
- post-collapse lane DAG and write-scope decisions;
- planning artifact/source commit and graph digest;
- dependency satisfaction level required by each edge;
- predecessor generation and reconciliation dispositions.

The scheduler also records the exact dependency candidate commits used to
prepare a run.

### Amendment 4: make handoff the immutable review boundary

A handoff binds the valid lease generation, candidate commit, work revision,
lane lineage, contributor actors, criteria/evidence digest, and workspace
quiescence check. Review and integration consume this record, never a live
checkout or moving branch tip.

### Amendment 5: store authoritative review evidence outside Git bookkeeping

Review verdicts and criterion results commit transactionally in the control
store. Optional large evidence is content-addressed. Generated matrices and
tracker comments are projections. This removes the shared-index race without
weakening auditable review.

### Amendment 6: serialize integration by target ref

The integration lock key is repository identity plus target ref. Mission-local
queue state is insufficient when two missions target the same branch. Resume
state is a cache; Git probes plus durable integration records decide whether a
step actually completed.

### Amendment 7: name outbound consent and compensation contracts

Every connector command declares:

- whether it can transmit data and which project identity/consent authorizes it;
- idempotency key and expected provider revision/value;
- attribution marker for compare-before-compensate;
- offline/retry policy and whether local workflow may continue;
- diagnostic and operator-repair behavior.

## 12. Ratification verdict

With the amendments above, the product laws and source-of-truth split are
supported by the current behavior:

```text
Tracker / Beads: what work exists and what humans discuss
Spec Kitty:      whether it may run and how it is judged
Git:             what source history actually happened
```

This is not a declaration that every proposed package or schema is correct.
It is permission to ratify the authority boundaries and run Phase 1. Phase 1 is
the empirical ratification of the concurrency mechanism; broader capabilities
remain intentionally unearned until it passes.

## Appendix A: source register

### Current Python source and tests

- `packs/built-in/missions/*/mission.yaml`
- `src/specify_cli/lanes/compute.py`
- `src/specify_cli/lanes/models.py`
- `src/specify_cli/lanes/persistence.py`
- `src/specify_cli/lanes/worktree_allocator.py`
- `src/specify_cli/status/models.py`
- `src/specify_cli/status/store.py`
- `src/specify_cli/status/locking.py`
- `src/specify_cli/coordination/status_transition.py`
- `src/specify_cli/merge/state.py`
- `src/specify_cli/merge/executor.py`
- `src/specify_cli/merge/done_bookkeeping.py`
- `src/specify_cli/merge/workspace.py`
- `src/specify_cli/acceptance/`
- `src/specify_cli/tracker/`
- `src/specify_cli/sync/`
- `src/specify_cli/orchestrator_api/`
- Focused tests named in section 2 and the incident-specific lane/merge/review
  tests adjacent to these packages.

### Accepted design records

- `2026-04-03-1-execution-lanes-own-worktrees-and-mission-branches.md`
- `2026-04-03-2-review-approval-and-integration-completion-are-distinct.md`
- `2026-06-05-1-merge-publish-layer-boundary.md`
- `2026-06-07-3-wp-lane-fsm-genesis-and-finalize-clobber.md`
- `2026-07-14-2-doctrine-to-core-mission-type-resolution-unification.md`
- `2026-07-19-1-wp-runtime-state-event-log-eviction-via-innerstatechanged.md`
- `2026-07-29-1-lane-base-recorded-planning-commit.md`
- `2026-08-03-1-review-cycle-artifacts-are-coord-partition.md`
- `2026-08-22-1-canonical-mission-type-reader-legacy-retirement.md`

### GitHub incidents and repairs

- [#1173: Beads state, lanes, and workspace lease cutover](https://github.com/Priivacy-ai/spec-kitty/issues/1173)
- [#1556: cross-machine preclaim](https://github.com/Priivacy-ai/spec-kitty/issues/1556)
- [#1915: atomic multi-dependency merge](https://github.com/Priivacy-ai/spec-kitty/issues/1915)
- [#2644: re-finalization leaves stale event authority](https://github.com/Priivacy-ai/spec-kitty/issues/2644)
- [#2993: lane planning ancestry loss](https://github.com/Priivacy-ai/spec-kitty/issues/2993)
- [#3129: ambient worktree write authority](https://github.com/Priivacy-ai/spec-kitty/issues/3129)
- [#3235: concurrent review verdict loss](https://github.com/Priivacy-ai/spec-kitty/issues/3235)
- [#3281: partial worktree allocation retry](https://github.com/Priivacy-ai/spec-kitty/issues/3281)
- [#3431: cyclic collapsed lane graph](https://github.com/Priivacy-ai/spec-kitty/issues/3431)
- [#3445: strict reviewer identity](https://github.com/Priivacy-ai/spec-kitty/issues/3445)
- [#3682: approval evidence gaps](https://github.com/Priivacy-ai/spec-kitty/issues/3682)
- [#415: worktree crash-recovery deadlock](https://github.com/Priivacy-ai/spec-kitty/issues/415)
- [#416: resumable merge recovery](https://github.com/Priivacy-ai/spec-kitty/issues/416)
- [PR #1908: topology and branch-identity authority seams](https://github.com/Priivacy-ai/spec-kitty/pull/1908)
- [PR #3076: recorded planning base and deterministic artifact writers](https://github.com/Priivacy-ai/spec-kitty/pull/3076)
- [PR #3209: remove dead sync queue authority](https://github.com/Priivacy-ai/spec-kitty/pull/3209)

### Prototype reference

- `/home/lynn/projects/thickticket/internal/runtimebase/identity.go`
- `/home/lynn/projects/thickticket/internal/actordirectory/identity_service.go`
- `/home/lynn/projects/thickticket/internal/workspace/shadow.go`
- `/home/lynn/projects/thickticket/internal/frontier/`
