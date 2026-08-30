# Mission Specification: GapDB Control Journal Proving Ground

**Mission Branch**: `kitty/mission-gapdb-control-journal-proving-ground-01M18EVR`
**Created**: 2026-08-30
**Status**: Draft
**Input**: Prove a narrow, production-shaped GapDB external-sidecar journal and recovery slice through the exact two-agent zombie torture chamber before authorizing any broad authority-store migration.

## Confirmed Intent

This mission is the smallest experiment likely to disprove the proposed
memory-primary, durably resumable runtime before broad migration begins. It
uses the admitted external `gapdbd` tuple, one real Git repository, two
independent work items, exactly two implementation ActorIDs across three process
lifetimes, and one bounded tracker. Each accepted authority command publishes one
indivisible durable product batch. Restart creates a new runtime epoch,
restores a product checkpoint plus journal suffix, and reconciles recorded
external observations. Adapter-free playback must reproduce the restored
canonical state digest.

This proves or falsifies one production-shaped slice. It does not migrate the
existing authority store, authorize production adoption, create a fallback, or
admit M2. GapDB byte recovery is not Spec Kitty product recovery. If exclusive
authority or canonical replay fails through the stale-writer scenario, the
program preserves the failure evidence and stops before M2.

The decisive proof is one source-bound end-to-end trial that executes the
entire ordered torture chamber and records all load-bearing outcomes together.
Focused scenarios remain necessary diagnostics, but cannot substitute for this
combined trial or independently authorize the exit signature or M2.

## Actors

- **Runtime operator**: Configures and admits only the exact external owner;
  ambiguous ownership requires a typed operator decision.
- **Authority runtime**: Owns command admission, durable product history,
  in-memory projections, epochs, leases, workspace binding, and recovery.
- **Implementation ActorID A**: Begins in ProcessID A with W1. After ProcessID A
  expires, the same ActorID A may resume only through replacement ProcessID C,
  a new lease, and fresh W3.
- **Implementation ActorID B**: Runs only as ProcessID B for the other work item
  in isolated W2.
- **Independent reviewers**: Review immutable candidates under ActorID-level
  independence. Each reviewer ActorID differs from every ActorID that
  contributed to its candidate; a new ProcessID for the same ActorID is not independent.
- **Bounded tracker**: Supplies fallible observations and effects but never
  grants execution, review, or integration authority.
- **Evidence auditor**: Verifies source binding, distinct scenario units,
  invariant observations, digests, and non-overclaim.
- **Accountable signer (`github:lynncoleart`)**: Accepts or refuses only the
  exact successful exit tuple after machine evidence is frozen.

## Domain Language

- **Admitted tuple**: Immutable source, module, sidecar bytes, public protocol,
  storage version, platform, lifecycle, harness, and signer identities.
- **Control journal**: Append-only, versioned Spec Kitty product history; it is
  not GapDB WAL, snapshots, watch history, tracker history, or Git history.
- **Durable product batch**: One all-or-none publication containing the
  command's events, idempotent result, affected heads, due checkpoint material,
  and new external-effect intents.
- **Memory-primary projection**: Fast disposable state rebuilt from durable
  product history; it is not independently authoritative.
- **Operation identity**: One stable `OperationID`, canonical request digest,
  and recorded typed result.
- **Domain event and reducer**: A bounded versioned fact and its compatible,
  deterministic, adapter-free state transition.
- **Runtime epoch**: One admitted host lifetime; every restart creates a new
  durable epoch before commands are served.
- **Checkpoint / restore / playback**: A validated prefix snapshot; live
  reconstruction from it plus later events; and read-only reduction with no
  live adapters or product writes.
- **External-effect intent / observation**: Durable attributable intent and
  later recorded provider facts. The runtime does not claim providers offer
  exactly-once effects; it prevents blind repeat by observe-before-retry.
- **Run capsule**: Epoch, generation, lease, fence, and workspace evidence
  presented by a process.
- **Lease-scoped workspace**: Mutable worktree permanently bound to its
  creating lease and epoch. Storage TTL or lease expiry can trigger a product
  decision but can never transfer the workspace.
- **Quarantine**: Evidence-preserving state for a workspace whose authority is
  no longer current.
- **Candidate / review / integration**: Immutable handoff evidence;
  ActorID-independent judgment; and serialized target-ref publication.
- **Universal invariants**: Primary isolation, one valid lease, immutable
  workspace binding, stale refusal, target serialization, candidate
  reachability, attribution-safe compensation, and provider topology.

## Scope

### In Scope

- One Linux/amd64 host and one local-filesystem authority.
- One verified external owner reached through the public typed protocol.
- One project, one real Git repository, two finalized independent work items,
  exactly two implementation ActorIDs (A and B), initial ProcessIDs A and B,
  replacement ProcessID C for ActorID A, and one bounded tracker.
- One durable product batch per accepted command; bounded versioned events and
  reducers; checkpoint-plus-suffix restore; new epoch; pure playback.
- Five new GapDB fault cuts, lease quarantine and fresh replacement workspace,
  ambiguous external effects, independent review, and serialized integration.
- All 14 external-sidecar qualification scenarios and the exact exit signature.

### Non-Goals

- Full authority-store migration, production cutover, adoption approval, M2
  admission, dual write, read fallback, SQLite fallback, or hidden import.
- Distributed/multi-host authority, shared/network filesystem operation, or
  direct database access by remote agents.
- Treating tracker status, Git state, a directory, a PID, storage TTL, or watch
  history as authority.
- Journal compaction, destructive retention, general artifact storage, API 2.0,
  neutral acceptance, release packaging, or optional MCP.
- Pretending fencing tokens can fence direct POSIX writes. Isolation and stale
  control-plane refusal are the enforceable laws.
- A force-ready waiver, best-effort event skip, current-policy lookup during
  replay, or silent import of zombie output.

## Assumptions

- The admission record and its digests remain available and authoritative.
- The host supplies admitted lock, sync, and atomic-rename filesystem semantics.
- The configured project-local sidecar can be reproduced and hashed before use.
- A deliberately dishonest bounded tracker can model timeout, stale state,
  ambiguity, and human reassignment.
- Historical SQLite-backed harness evidence is reusable design input, not proof
  of this external owner, product journal, restart, or playback boundary.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Admit the verified external owner (Priority: P1)

As an operator, I can start or reconnect only to the admitted owner and keep
commands closed until executable, process, database, protocol, health, and
filesystem evidence agree.

**Why this priority**: Every later result is invalid if the tested owner or
database is not the admitted one.

**Independent Test**: Exercise exact start/reconnect and wrong binary,
database, socket, process identity, health, filesystem, and ambiguous owner;
then run all 14 external scenarios through that same owner boundary.

**Acceptance Scenarios**:

1. **Given** an exact healthy owner, **When** admission completes, **Then**
   product restore and epoch creation finish before command readiness.
2. **Given** unverifiable provenance, **When** start, reconnect, shutdown, or
   orphan recovery is requested, **Then** no unknown process is used or
   signalled and a typed operator decision is returned.
3. **Given** the real external boundary, **When** qualification runs, **Then**
   all 14 declared scenarios reach their expected terminal result.

### User Story 2 - Publish one durable command result (Priority: P1)

As the authority runtime, I can accept a command as one indivisible product fact
and answer retries safely when the response is lost.

**Why this priority**: Split or ambiguous authority publication can duplicate
leases, history, or provider work.

**Independent Test**: Inject every conditional, size, and response-loss fault;
retry with equal and changed digests; verify one recorded result and no partial
batch or blind effect repeat.

**Acceptance Scenarios**:

1. **Given** a valid command, **When** it is durably accepted, **Then** events,
   result, heads, due checkpoint material, and intents appear together or not at all.
2. **Given** a lost durable response, **When** the same operation and digest is
   retried, **Then** read reconciliation returns the prior result without a new effect.
3. **Given** the same operation with a changed digest, **When** retried, **Then**
   it fails closed without mutation.

### User Story 3 - Restart, restore, and replay without effects (Priority: P1)

As an operator and auditor, I can restart the owner, obtain a new epoch, resume
from validated product history, and reproduce state through adapter-free playback.

**Why this priority**: Mission resumability requires deterministic product
recovery, not merely surviving storage bytes.

**Independent Test**: Restore a multi-page journal from a checkpoint and suffix;
inject stale scans, incompatible history, corrupt checkpoints, and old capsules;
compare live state with playback using fail-on-call adapters.

**Acceptance Scenarios**:

1. **Given** any prior ready lifetime, **When** the host restarts cleanly or
   uncleanly, **Then** a different durable epoch exists before readiness.
2. **Given** a pre-restart capsule, **When** it attempts any authority operation,
   **Then** its predecessor epoch is refused.
3. **Given** a valid checkpoint and suffix, **When** restore runs, **Then** every
   event is validated and reduced in authoritative order to the stored head digest.
4. **Given** an invalid checkpoint or stale scan, **When** restore runs, **Then**
   it chooses a fully valid bounded path or remains degraded/closed; it never
   skips history or uses a force-ready waiver.
5. **Given** the same history, **When** playback runs, **Then** canonical bytes
   and digest equal live restore and all adapter call counters remain zero.

### User Story 4 - Replace expired work without workspace transfer (Priority: P1)

As two implementation ActorIDs, we can work independently while ActorID A's
failed process is replaced without giving its mutable filesystem to the replacement.

**Why this priority**: Workspace isolation and stale refusal are the enforceable
defense against a zombie that can still write directly to files.

**Independent Test**: Claim two items as ActorID A/ProcessID A and ActorID
B/ProcessID B, suspend ProcessID A, expire its lease, grant replacement
ProcessID C to the same ActorID A with fresh W3, resume ProcessID A and let it
commit in W1, then attempt every control-plane crossing from the stale process.

**Acceptance Scenarios**:

1. **Given** two independent items, **When** ActorID A/ProcessID A and ActorID
   B/ProcessID B claim concurrently, **Then** they receive distinct current
   runs, leases, workspaces, branches, directories, and Git indices while the
   primary checkout stays isolated.
2. **Given** ProcessID A stops heartbeating but remains alive, **When** its lease expires,
   **Then** W1 is quarantined and is never transferred by lease expiry or TTL.
3. **Given** replacement ProcessID C for the same implementation ActorID A,
   **When** it reclaims A's work, **Then** it receives a new lease and fresh W3
   distinct from W1 without creating a third implementation ActorID.
4. **Given** zombie ProcessID A writes and commits in W1, **When** it presents stale epoch,
   lease, fence, or workspace evidence, **Then** handoff, review, integration,
   heartbeat, and effect publication all refuse it while W3 remains unchanged.
5. **Given** useful W1 output, **When** inspected, **Then** it survives only as
   quarantined evidence and is not silently imported or revived.

### User Story 5 - Reconcile provider ambiguity safely (Priority: P1)

As an operator, I can recover an external-effect intent without assuming that
the provider implements exactly-once behavior or overwriting later human state.

**Why this priority**: Local durability is unsafe if recovery blindly repeats
external work or erases an unattributable human change.

**Independent Test**: Cut after intent and after provider application; return
unavailable, stale, wrongly attributed, and human-reassigned state; then complete
W2 from ActorID B/ProcessID B and W3 from ActorID A/ProcessID C through
immutable handoff, contributor-independent review, and competing integration.

**Acceptance Scenarios**:

1. **Given** intent without observation, **When** recovery begins, **Then** it
   observes provider facts before dispatch, compensation, or decision.
2. **Given** an applied effect with a lost response, **When** revision, value,
   and marker still attribute it, **Then** one outcome is recorded without blind repeat.
3. **Given** later human state, **When** old compensation runs, **Then** the human
   state survives and an attribution conflict is recorded.
4. **Given** valid W2 and W3 candidates, **When** review occurs, **Then** each
   reviewer ActorID differs from every implementation ActorID that contributed
   to that candidate; ProcessID separation alone is insufficient.
5. **Given** two approvals, **When** integration requests compete, **Then** one
   target-ref authority acts at a time and the second revalidates after movement.
6. **Given** branch movement or conflict, **When** safe publication is impossible,
   **Then** no false completion occurs and candidate/conflict evidence stays reachable.

### User Story 6 - Produce an attributable go/stop decision (Priority: P1)

As a program owner, I can inspect one immutable evidence set and know whether
the exact experiment passed strongly enough to permit M2 consideration.

**Why this priority**: Technical completion must not silently become production
adoption or migration-risk acceptance.

**Independent Test**: Freeze source and tuple, run every required campaign,
verify artifact digests and distinct count units, then apply or withhold the
exit signature and inspect M2 admission state.

**Acceptance Scenarios**:

1. **Given** successful evidence, **When** assembled, **Then** it binds exact
   source/tree, tuple, lifecycle, platform, harness, cuts, digests, and call census.
2. **Given** different evidence families, **When** reported, **Then** 73 top-level
   historical tests, 35 catalog cases, 28 Phase 1 cuts, 17 frozen Phase 1 tests,
   nine supplemental cuts, five new cuts, repetitions, and 14 external scenarios
   retain their distinct units and are never conflated.
3. **Given** the exact frozen source and tuple, **When** the decisive combined
   trial runs, **Then** one ordered evidence record covers exact owner admission,
   14/14 external qualification, concurrent claims, the applicable GapDB cuts,
   response reconciliation,
   ProcessID A expiry and W1 quarantine, fresh W3 for ActorID A/ProcessID C,
   zombie stale refusal, host-and-owner restart, a new epoch, old-capsule
   refusal, checkpoint-plus-suffix restore, equal adapter-free playback digest,
   zero playback calls/writes, immutable handoff, contributor-independent
   ActorID review, and serialized integration while observing one valid
   authority per WorkRevision throughout.
4. **Given** every technical gate including the combined trial passes, **When**
   the signer acts, **Then** the signature covers only the exact tuple and
   evidence digests and may support M2 consideration.
5. **Given** any missing proof, unsupported environment, owner ambiguity,
   invariant failure, duplicate effect, digest mismatch, or missing signature,
   **When** adjudicated, **Then** no production/adoption claim is made and M2
   remains unadmitted with the stop reason preserved.

## Exact Fault-Cut Acceptance Matrix

| Cut ID | Boundary | Required terminal evidence |
|---|---|---|
| GJ-01 | Before request publication | No operation-attributable record is new or changed; every preexisting record revision and canonical digest is preserved exactly; retry is an ordinary first attempt. |
| GJ-02 | After durable acceptance but before client response | Same operation and digest reconcile to exactly one product batch and one typed result. |
| GJ-03 | After workspace creation but before durable binding | The orphan has no authority, is quarantined/collected safely, and retry yields exactly one authorized workspace. |
| GJ-04 | After durable binding but before run-capsule response | Retry reconstructs the existing lease, binding, and capsule without a second authority or workspace. |
| GJ-05 | After effect intent but before observation | Recovery observes provider state and records an attributable outcome without blind repeat or unsafe compensation. |

Each cut asserts every applicable universal invariant and an anti-vacuity
control. These cuts supplement rather than rename historical boundaries.

## Edge Cases

- Executable bytes change between admission, verification, and spawn.
- Socket, database, lock, process-start identity, or health does not match; PID
  reuse makes shutdown ownership ambiguous.
- Memory acknowledgement arrives without durable acceptance, or a durable
  response is lost before the client sees it.
- Equal and conflicting digests race under one operation identity.
- A batch has duplicate keys, too many records, an oversized event, or a due
  checkpoint that cannot fit its declared product budget.
- Event indices are missing, duplicated, nonconsecutive, or inconsistent.
- Request, event, journal-prefix, checkpoint, reducer-set, state, or projection
  digests fail canonical verification.
- No compatible checkpoint exists and bounded full replay is impossible.
- A multi-page scan becomes stale, duplicates a record, or changes its observed revision.
- Watch history overflows; restore must use the journal rather than watch history.
- A clean shutdown is followed by restart; a new epoch is still mandatory.
- A workspace exists physically before durable binding, or a zombie creates a
  valid Git commit after its epoch and lease expire.
- Replacement ProcessID C is incorrectly assigned a third implementation
  ActorID instead of continuing as ActorID A under new authority.
- Provider response is missing, stale, unavailable, wrong, or superseded by a human.
- Target moves after review, two integrations race, or merge conflicts after approval.
- An embedded store passes tests while the required external owner was not used.
- Evidence is incomplete, count units are conflated, or the signer is asked to
  approve a tuple different from the one tested.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|---|---|---|---|---|
| FR-001 | Immutable entry binding | As an auditor, I can identify the exact admitted architecture, source/tree, module, sidecar bytes, protocol, storage version, platform, lifecycle, harness, and signer. | High | Open |
| FR-002 | Configured discovery | As an operator, only the configured external owner path and endpoint are eligible; ambient executable discovery is never admission evidence. | High | Open |
| FR-003 | Owner provenance | As an operator, start, reconnect, shutdown, and orphan recovery proceed only while executable, process-start, socket, database, and lock observations prove the intended owner. | High | Open |
| FR-004 | Closed readiness | As an operator, commands remain unavailable until byte recovery, protocol health, database binding, product restore, epoch creation, and effect reconciliation complete. | High | Open |
| FR-005 | Fail-closed lifecycle | As an operator, an unknown, unhealthy, unsupported, or ambiguous owner yields a typed refusal/decision without takeover, unsafe signal, or force-ready waiver. | High | Open |
| FR-006 | External qualification | As an auditor, all 14 declared qualification scenarios run through the real external sidecar and public client. | High | Open |
| FR-007 | Durable authority | As the runtime, only durable acknowledgement or read proof of the same accepted operation can make an authority command successful. | High | Open |
| FR-008 | Canonical operation | As a caller, each command binds permanently to one operation ID, request digest, command type, preconditions, epoch, and typed result. | High | Open |
| FR-009 | Idempotent replay | As a caller, retrying the same operation and digest returns the recorded result without repeating local mutation or blindly repeating a provider effect. | High | Open |
| FR-010 | Digest conflict | As an auditor, reusing an operation identity with changed content fails closed without state change. | High | Open |
| FR-011 | One durable batch | As the runtime, one accepted command atomically publishes its complete events, result, heads, due checkpoint material, and new intents. | High | Open |
| FR-012 | Conditional transition | As a concurrent caller, record absence and observed-head preconditions ensure incompatible races admit at most one winner. | High | Open |
| FR-013 | Post-acceptance visibility | As a progress consumer, no projection, watcher result, capsule, or effect dispatch reports success before durable acceptance or exact read reconciliation. | High | Open |
| FR-014 | Oversize refusal | As an operator, commands exceeding any declared product budget fail before publication with no partial state. | High | Open |
| FR-015 | Bounded versioned events | As an auditor, every event has immutable identity, aggregate/operation/epoch, consecutive group position, explicit event/reducer versions, reduction inputs, and canonical digest. | High | Open |
| FR-016 | Closed event registry | As an operator, unknown types/versions, incompatible reducers, malformed groups, or digest mismatches block recovery and are never skipped. | High | Open |
| FR-017 | Pure reducers | As an auditor, reduction consults only version-pinned recorded inputs and never current time, policy, Git, filesystem, tracker, agent, artifact, or other adapters. | High | Open |
| FR-018 | Disposable projection | As an operator, active in-memory state can be discarded and rebuilt completely from accepted product history. | High | Open |
| FR-019 | Checkpoint in batch | As an operator, when due, checkpoint bytes and prefix/state/version evidence participate in the command's one durable batch. | High | Open |
| FR-020 | Restore precedence | As an operator, startup chooses the newest valid bounded checkpoint and suffix, then bounded full replay, otherwise degraded/closed with no skipped history. | High | Open |
| FR-021 | Complete paging | As an auditor, restore traverses every page at one observed revision and fully restarts after a stale cursor without omission or duplication. | High | Open |
| FR-022 | New restart epoch | As an operator, every clean or unclean host restart durably creates a different epoch before commands are admitted. | High | Open |
| FR-023 | Predecessor invalidation | As an operator, epoch replacement makes predecessor leases non-current, quarantines their workspaces, and rejects old capsules. | High | Open |
| FR-024 | Side-effect-free playback | As an auditor, playback mutates no product state, allocates no authority, and invokes no external adapter. | High | Open |
| FR-025 | Digest equality | As an auditor, live restore and independent playback over the same history produce byte-identical canonical state and digest. | High | Open |
| FR-026 | Durable effect intent | As an operator, every external action starts from a bounded attributable intent in an accepted command batch; no observation exists without intent. | High | Open |
| FR-027 | Observe before retry | As an operator, recovery of unfinished intent inspects current provider facts before dispatch, compensation, or decision and makes no exactly-once provider assumption. | High | Open |
| FR-028 | Safe compensation | As a human collaborator, compensation changes provider state only when revision, value, and marker still prove attribution. | High | Open |
| FR-029 | Five new cuts | As an auditor, GJ-01 through GJ-05 execute at the actual external boundary and prove their required terminal evidence. | High | Open |
| FR-030 | Concurrent isolated claims | As ActorID A/ProcessID A and ActorID B/ProcessID B, distinct claims yield distinct current runs, leases, workspaces, branches, directories, and indices without primary-checkout mutation. | High | Open |
| FR-031 | Immutable workspace binding | As an operator, a workspace's lease and epoch binding never changes after creation; expiry and TTL never transfer it. | High | Open |
| FR-032 | Fresh replacement process | As ActorID A in replacement ProcessID C, I receive a new lease and W3 after ProcessID A expires; no path transfers W1 or creates a third implementation ActorID. | High | Open |
| FR-033 | Zombie refusal | As an operator, zombie A may write W1 directly but cannot heartbeat, publish effects, hand off, review, or integrate with stale authority. | High | Open |
| FR-034 | Quarantine evidence | As an auditor, stale output remains inspectable by exact identity but cannot be silently imported or made authoritative. | Medium | Open |
| FR-035 | Immutable handoff | As an implementer, handoff binds exact reachable candidate evidence to current work, actor/process, epoch, lease, and workspace. | High | Open |
| FR-036 | Actor-independent review | As a reviewer, my ActorID must differ from every implementation ActorID that contributed to the candidate; a distinct process alone cannot satisfy this gate. | High | Open |
| FR-037 | Serialized integration | As an integrator, exactly one authority owns a repository target ref at a time and later candidates revalidate after movement. | High | Open |
| FR-038 | Conflict-safe outcome | As an auditor, target movement or conflict produces no false completion and preserves target, candidate, conflict, and repair evidence. | High | Open |
| FR-039 | Universal checkpoints | As an auditor, each named fault asserts applicable isolation, authority, reachability, attribution, and topology invariants with anti-vacuity controls. | High | Open |
| FR-040 | Source-bound evidence | As an auditor, one immutable manifest binds tested source/tree, tuple, environment, scenario units, repetitions, artifacts, results, digests, and adapter-call census. | High | Open |
| FR-041 | Honest census | As an auditor, top-level tests, catalog cases, crash cuts, repetitions, and external scenarios are reported separately without count conflation. | Medium | Open |
| FR-042 | Exact exit signature | As the signer, I accept or refuse only the complete frozen evidence for the exact tested tuple after all machine gates and FR-044's combined trial pass. | High | Open |
| FR-043 | Program stop | As program owner, any critical proof failure, failed/incomplete combined trial, incomplete campaign, unsupported boundary, adoption overclaim, or missing signature keeps M2 unadmitted. | High | Open |
| FR-044 | Combined torture-chamber proof | As an auditor, one source-bound end-to-end trial must execute the complete ordered M1 torture chamber in one evidence record, including exact owner admission and 14/14 external qualification, and simultaneously prove one valid authority per WorkRevision, W1 quarantine and fresh W3, owner/host restart and new epoch, old-capsule refusal, equal restored/playback digest, zero playback calls/writes, contributor-independent ActorID review, and serialized integration; focused tests cannot substitute for it. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|---|---|---|---|---|---|
| NFR-001 | Atomic fault reliability | Across at least 100 repetitions of each applicable publication fault, no accepted command exposes a partial batch and every accepted operation has one recorded result. | Reliability | High | Open |
| NFR-002 | Idempotency reliability | Across at least 100 lost-response retries, equal IDs/digests yield zero duplicate authority transitions and zero blind effect repeats; changed digests refuse in 100% of trials. | Reliability | High | Open |
| NFR-003 | Concurrency reliability | Across at least 100 two-item claim trials, both independent items progress while every observation has at most one valid authority per work revision. | Concurrency | High | Open |
| NFR-004 | Integration exclusivity | Across at least 100 competing integrations, every observation has at most one target authority and each admitted candidate integrates at most once. | Concurrency | High | Open |
| NFR-005 | Deterministic recovery | Across at least 100 restore/playback comparisons, canonical bytes and digest match in 100% of trials. | Determinism | High | Open |
| NFR-006 | Zero-call playback | Every playback reports exactly zero external adapter calls and performs zero durable writes. | Safety | High | Open |
| NFR-007 | Epoch freshness | Across 100 clean/unclean restarts, every ready lifetime has a unique epoch and 100% of predecessor capsules refuse. | Recovery | High | Open |
| NFR-008 | Zombie isolation | Across at least 100 stale-writer trials, replacement identity differs, contents stay unaffected, and zero stale authority operations succeed. | Isolation | High | Open |
| NFR-009 | Qualification completeness | Exactly 14/14 external scenarios start, terminate once, and pass without embedded/internal substitution. | Compatibility | High | Open |
| NFR-010 | Paged replay completeness | At least 2,001 journal records restore over multiple pages with zero missing/duplicates; every stale cursor restarts complete traversal. | Recovery | High | Open |
| NFR-011 | Revision-pinned product budgets | Every product key, value, operation-count, event-count, checkpoint, frame, and batch-byte budget is strictly below the admitted configured runtime limit or applicable default; planning pins the exact lower budgets to a revision, and 100% of over-budget commands fail before publication. | Capacity | High | Open |
| NFR-012 | Bounded admission | Every ready restore stays within its declared event/byte budget; excess fixtures become degraded/closed rather than unbounded. | Performance | High | Open |
| NFR-013 | Version refusal | 100% of unknown/incompatible event, reducer, ordering, and digest fixtures refuse readiness; none are skipped or force-admitted. | Compatibility | High | Open |
| NFR-014 | Provenance safety | 100% of wrong-binary/database/socket/process, PID-reuse, ambiguity, health, and filesystem fixtures admit no command and signal no unproven process. | Security | High | Open |
| NFR-015 | Attribution preservation | Across at least 100 ambiguity/reassignment trials, recovery makes zero blind repeats, zero unattributable overwrites, and records one typed attempt result. | Data integrity | High | Open |
| NFR-016 | Evidence completeness | Final evidence covers 100% of required artifacts with verified digests, exact identities, started/terminal counts, and no unexplained rerun. | Auditability | High | Open |
| NFR-017 | Primary isolation | Across every campaign, the primary head/index/tree remains unchanged except the explicitly serialized target-ref publication. | Isolation | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|---|---|---|---|---|---|
| C-001 | Narrow proving slice | M1 proves one production-shaped vertical slice only; it does not migrate the full authority store or authorize production cutover. | Scope | High | Open |
| C-002 | Exact tuple | Valid evidence is limited to the source, module, sidecar digest, protocol, storage schema, platform, lifecycle, harness, and signer in admission. | Governance | High | Open |
| C-003 | External public boundary | Scenarios use the configured external sidecar and typed public client; embedded/internal owners, CLI prose parsing, and PATH discovery are inadmissible. | Technical | High | Open |
| C-004 | One-host boundary | Evidence covers one Linux/amd64 host and one local filesystem authority with admitted locking, sync, and atomic rename. | Platform | High | Open |
| C-005 | Shared storage excluded | Network/shared control directories and filesystems lacking required durability semantics fail closed. | Platform | High | Open |
| C-006 | No dual truth | M1 introduces no dual writes, SQLite/read/config fallback, hidden import, or other production authority path. | Architecture | High | Open |
| C-007 | Product ownership | GapDB owns atomic durable bytes; Spec Kitty owns event meaning, reducers, authority, idempotency, checkpoints, recovery, attribution, and acceptance. | Architecture | High | Open |
| C-008 | One-batch law | No accepted command splits required product facts across multiple durable authority batches, including when checkpoint or intent is due. | Data integrity | High | Open |
| C-009 | Durable authority only | Memory state, watch, storage TTL, directory, branch, tracker assignment, or process liveness alone never grants authority. | Safety | High | Open |
| C-010 | Immutable M1 history | Events and checkpoints required for audit remain retained; destructive compaction and retention are deferred. | Retention | High | Open |
| C-011 | Closed compatibility | Recovery has no best-effort skip, unknown-version fallback, force-ready waiver, or current external lookup during reduction. | Compatibility | High | Open |
| C-012 | New epoch always | Every host restart creates a new durable epoch, including after clean shutdown. | Authority | High | Open |
| C-013 | Permanent binding | A mutable workspace remains bound to its creating lease/epoch and can never be transferred; fencing does not pretend to stop POSIX writes. | Concurrency | High | Open |
| C-014 | Actor independence | Each approving reviewer ActorID differs from every implementation ActorID that contributed to its candidate; process separation alone is insufficient. | Governance | High | Open |
| C-015 | Exit-only signature | Human adoption is not an entry prerequisite or automatic; the signer acts only on complete exact exit evidence. | Governance | High | Open |
| C-016 | Non-transitive decision | No later source, rebuild, binary, protocol, lifecycle, environment, or evidence inherits M1 acceptance. | Governance | High | Open |
| C-017 | Stop before M2 | Failure of the combined trial—or failure to preserve authority, fresh replacement, or restored/playback digest within it—blocks signature eligibility and M2 without fallback. | Program | High | Open |
| C-018 | Successor scope deferred | Broad execution, neutral acceptance, API 2.0, release packaging, and optional MCP remain later missions. | Scope | Medium | Open |

### Key Entities

- **Admission Record**: Immutable entry decision binding architecture, tuple,
  historical harness, signer, and stop rule.
- **Database Binding**: Project/database identity preventing a healthy but wrong
  database from serving the project.
- **Runtime Epoch**: Identity and provenance observations for one host lifetime,
  its predecessor, and readiness state.
- **Operation Record**: Operation identity, request digest, epoch, preconditions,
  typed result, and emitted event/effect identities.
- **Event Envelope**: Append-only bounded fact with aggregate/operation/epoch,
  deterministic group order, versions, payload, and digest.
- **Aggregate and Projection Heads**: Conditional authority summary and
  canonical state attestation through an exact journal position.
- **Checkpoint**: Canonical state and version/digest evidence bound to a journal prefix.
- **Effect Intent and Observation**: Attributable request and later provider
  facts sufficient for recovery and playback without assuming provider exactly-once.
- **Actor and Process Identities**: Exactly two implementation ActorIDs, A and
  B; initial ProcessIDs A and B; replacement ProcessID C still acting as
  ActorID A; and reviewer ActorIDs separate from candidate contributors.
- **Run, Lease, and Workspace**: Attempt, renewable authority, and mutable
  worktree permanently bound to one lease/epoch.
- **Candidate, Review, and Integration**: Immutable Git result,
  ActorID-independent verdict, and serialized target publication.
- **Playback Result**: Terminal position, event count, canonical state digest,
  and zero adapter-call/write census.
- **Evidence Manifest**: Source-bound campaign inventory, distinct count units,
  artifact digests, results, environment, and final human decision.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: One hundred consecutive two-item concurrency repetitions finish
  with at most one valid authority per work revision at every observation and
  no primary-working-copy mutation outside serialized publication.
- **SC-002**: In 100 lost durable-response trials, retry returns the original
  typed result with one accepted operation, one authority transition, and no
  blind duplicate provider effect.
- **SC-003**: All five new fault boundaries produce their specified terminal
  evidence with zero partial accepted commands and zero invariant violations.
- **SC-004**: Across 100 clean and unclean restarts, every admitted lifetime has
  a new epoch and every predecessor capsule is refused.
- **SC-005**: In 100 comparisons, live restore and historical playback produce
  identical canonical bytes/digest while playback records zero adapter calls
  and zero durable writes.
- **SC-006**: In 100 stale-writer trials, replacement workspace identity differs,
  remains unaffected by zombie writes, and accepts zero stale operations.
- **SC-007**: Both admissible torture-chamber candidates receive verdicts from
  reviewer ActorIDs distinct from every candidate-contributing implementation
  ActorID and reach the target history once under serialized integration.
- **SC-008**: All 14 external-store scenarios start and pass through the same
  independent owner boundary used by the product scenario; none are substituted.
- **SC-009**: Every corruption, unknown-version, incompatible-reducer,
  wrong-owner, and unsupported-environment fixture refuses readiness; none skip history.
- **SC-010**: At least 2,001 records restore without omission/duplication across
  pages, and every stale traversal causes a complete bounded retry.
- **SC-011**: Final evidence verifies 100% of required artifact digests and keeps
  every test, catalog, cut, repetition, and external-scenario count in its correct unit.
- **SC-012**: The human record covers exactly the successful source, owner,
  protocol, platform, lifecycle, and evidence identities; any mismatch or
  missing proof leaves M2 unadmitted and makes no production/adoption claim.
- **SC-013**: One source-bound evidence record completes the entire ordered M1
  torture chamber, including exact owner admission and 14/14 external
  qualification, in a single trial and simultaneously records one valid
  authority per WorkRevision, W1 quarantine and fresh W3, owner/host restart
  with new epoch and old-capsule refusal, identical restored/playback digest,
  zero playback calls/writes, contributor-independent ActorID reviews, and
  serialized integration; the exit signature and M2 consideration remain
  unavailable unless this combined trial passes.
