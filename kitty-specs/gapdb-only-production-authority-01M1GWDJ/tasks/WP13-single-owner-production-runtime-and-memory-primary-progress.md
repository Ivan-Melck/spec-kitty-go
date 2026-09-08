---
work_package_id: WP13
title: Single-Owner Production Runtime and Memory-Primary Progress
dependencies:
- WP11
- WP12
requirement_refs:
- FR-002
- FR-003
- FR-019
- FR-020
- FR-021
- FR-022
- FR-026
- FR-027
- FR-029
- FR-030
- FR-034
- FR-036
- NFR-005
- NFR-006
- NFR-007
- NFR-010
- C-002
- C-003
- C-013
- C-014
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T064
- T065
- T066
- T067
- T068
- T069
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/productionruntime/
create_intent:
- internal/productionruntime/host.go
- internal/productionruntime/composition.go
- internal/productionruntime/progress.go
- internal/productionruntime/recovery.go
- internal/productionruntime/host_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/productionruntime/host.go
- internal/productionruntime/composition.go
- internal/productionruntime/progress.go
- internal/productionruntime/recovery.go
- internal/productionruntime/host_test.go
- internal/sidecar/gapdb/**
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP13 - Single-Owner Production Runtime and Memory-Primary Progress

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

Compose the GapDB-only capabilities into one local production host that owns
one exact admitted sidecar/database authority, restores and validates the
memory-primary projection before admitting a fresh runtime epoch, publishes
memory only after durable acknowledgement or exact reconciliation, and exposes
query-only mission/work/operation progress snapshots and watches.

Implementation command:

```bash
spec-kitty agent action implement WP13 --agent codex
```

## Branch, dependency, and ownership rules

- Start from accepted WP11 and WP12 revisions assigned in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Planning and merge target: `feat/gapdb-only-production-authority`.
- Work only in the Spec Kitty-provided lane worktree.
- Edit only the five declared `internal/productionruntime` files and
  `internal/sidecar/gapdb/**`.
- Preserve concurrent work and never revert unrelated edits.
- Compose existing capability ports; do not pull domain behavior into the host.
- Do not implement normal CLI, API 2.0, Team Kitty, MCP, or remote transport.
  WP15 and later missions own those surfaces.

Read the mission `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`quickstart.md`, all mission contracts, and
`docs/programs/evidence/m1-adoption.json` before editing. The exact M1 record is
an immutable input; no mutable branch, release alias, PATH discovery, or
compatible-looking sidecar may inherit its authority.

## T064: Bind admission to the exact M1 tuple, sidecar, and database

**Purpose**: Ensure a healthy but wrong binary, process, socket, database, or
protocol can never serve as the production authority.

**Files**: `internal/sidecar/gapdb/**`, `composition.go`, and `host_test.go`.

**Implementation requirements**:

1. Verify the canonical adoption record at
   `docs/programs/evidence/m1-adoption.json`, including decision `adopt`, source
   commit/tree, evidence-manifest digest, admitted-tuple digest, sidecar digest,
   signer/reference, and schema version.
2. Require the exact admitted source commit
   `e05a5e46feb3e65ff42c4544c16678c440ef3818`, source tree
   `c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73`, tuple SHA-256
   `4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d`,
   and sidecar SHA-256
   `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
3. Verify and descriptor-execute one explicit absolute sidecar binary. Preserve
   no-symlink, file identity, immutable execution image, platform, ownership,
   mode, size, digest, and PID-reuse protections from M1.
4. Require explicit absolute private runtime and data roots outside Git
   worktrees and network/shared filesystems. Never search PATH or arbitrary
   service discovery.
5. Admit Status, Health, DescribeConfig, wire/storage protocol proofs, connected
   socket identity, process identity, and database identity as one composite
   provenance record. No one endpoint can fabricate another endpoint's facts.
6. Create or verify one immutable `AuthorityBindingV2` containing
   HostAuthorityID, DatabaseID, admitted tuple/sidecar/protocol coordinates, and
   creating OperationID.
7. On a nonempty current root, require exact existing authority/database
   binding. A new healthy database with a different DatabaseID refuses rather
   than appearing empty or initializing another authority.
8. Treat tuple, executable, protocol, socket, process, database, product-policy,
   or authority-binding mismatch as a typed bounded admission failure before
   command readiness.
9. Keep raw provider transcripts, environment values, executable bytes, and
   secrets out of diagnostics.

**Required tests**:

- Exact tuple and bound database admit.
- Change each admission coordinate independently and require refusal.
- Same bytes through a symlink/PATH/substituted inode refuse.
- Wrong DatabaseID, foreign live owner, PID reuse, socket substitution, or
  unprovable process identity refuses.
- Restart of the same bound database succeeds after ownership reconciliation.

**Exit evidence**:

```bash
go test ./internal/sidecar/gapdb ./internal/productionruntime -run 'Admission|Binding|Database|Sidecar' -count=10
```

## T065: Compose one host lifecycle and closed command barrier

**Purpose**: Establish one process-local owner and one route to every mutating
capability without making process memory the authority.

**Files**: `host.go`, `composition.go`, and `host_test.go`.

**Implementation requirements**:

1. Define explicit host states `recovering`, `reconciling`, `ready`,
   `degraded`, `closing`, and `closed`, with a monotonic lifecycle and typed
   transition failures.
2. Construct the sidecar client, v2 adapters, capability services, restore
   engine, epoch authority, projection, checkpoint scheduler, and query service
   exactly once inside the host composition root.
3. Prevent callers from obtaining a raw GapDB client, database directory,
   committer, projection mutator, sidecar lifecycle, or repository constructor.
4. Expose typed application command entry points whose first common guard is
   the host barrier and whose semantic authority remains capability-owned.
5. Permit command admission only in `ready`, with the current durable epoch and
   sealed recovered projection revision. Reject during every other state before
   operation/capability reads or external effects.
6. Serialize host lifecycle changes without serializing independent capability
   commands. Do not add a global per-command mutex or journal head.
7. On sidecar/process/health/database ownership loss, close command admission
   immediately, retain bounded diagnosis, and enter degraded/closed recovery.
8. Shutdown closes admission first, drains bounded in-flight work according to
   policy, performs attributable lifecycle cleanup, and never deletes durable
   authority as a convenience.
9. A second host for the same database must fail ownership admission even in
   the same OS process/test binary.

**Required tests**:

- Every non-ready state rejects mutating commands with zero adapter/effect call.
- Two host owners race; exactly one obtains database authority.
- Independent ready commands remain concurrent.
- Sidecar death or database identity drift closes the barrier.
- Shutdown/restart preserves all durable facts and does not leak ownership.

**Exit evidence**:

```bash
go test ./internal/productionruntime -run 'Host|Barrier|Owner|Lifecycle|Shutdown' -count=10
```

## T066: Order checkpoint restore, reconciliation, and fresh epoch startup

**Purpose**: Make restart deterministic and ensure predecessor authority cannot
become current merely because a process came back.

**Files**: `recovery.go`, `host.go`, and `host_test.go`.

**Required startup order**:

1. Resolve and classify the control root read-only through WP12.
2. Refuse historical, mixed, unsafe, malformed, foreign, or ambiguous roots
   with zero current mutation and without starting the sidecar.
3. Verify exact M1 admission and sidecar executable, then acquire one owner.
4. Require provider byte recovery, Health/Status/DescribeConfig agreement, and
   exact AuthorityBinding/DatabaseID.
5. Close command admission and run WP04 recovery: newest valid compatible
   checkpoint plus complete suffix, bounded full replay, then typed refusal.
6. Validate complete operation membership, aggregate heads, projection digest,
   known event/reducer versions, and stable provider revision.
7. Atomically admit a fresh RuntimeEpochID and invalidate predecessor runtime,
   lease, capsule, and active-authority coordinates required by WP07.
8. While the barrier remains closed, reconcile restored ambiguous operations or
   external intents under the new epoch using explicit observers. Playback
   itself never performs this I/O.
9. Publish the sealed projection/epoch pair and transition to `ready` only after
   mandatory reconciliation produces a policy-admissible state.

Do not admit a new epoch before successful product restore. Do not open command
admission between epoch creation and required predecessor invalidation. Existing
committed operation results remain replayable before new-epoch validation;
absent predecessor commands refuse.

**Required tests**:

- Fault after every numbered boundary leaves the host non-ready and restartable.
- Corrupt newest checkpoint falls back only to an earlier compatible complete
  checkpoint; missing/over-budget/unknown history refuses.
- Every clean/unclean restart creates a unique epoch after identical restore.
- All predecessor capsules refuse after readiness.
- Playback probes report zero external calls and zero durable writes.
- Reconciliation cannot expose commands before it reaches an explicit outcome.

**Exit evidence**:

```bash
go test ./internal/productionruntime -run 'Recovery|Restore|Epoch|Reconcile|StartupOrder' -count=10
```

## T067: Publish memory projections only after durable truth

**Purpose**: Provide blazing-fast current state without allowing RAM to become
a shadow authority or acknowledged results to disappear on restart.

**Files**: `progress.go`, `host.go`, `recovery.go`, and `host_test.go`.

**Implementation requirements**:

1. Store immutable snapshots or owner-serialized maps for mission, work,
   operation, effect, review, integration, and progress views.
2. Apply a capability event to a new projection snapshot only after its complete
   durable publication is acknowledged or exactly reconciled by PublicationID.
3. Atomically publish the projection pointer with its GapDB revision,
   RuntimeEpochID, whole-product digest, and completeness metadata.
4. Never apply an optimistic command result, partial operation membership,
   watch notification, provider response, or external observation not already
   admitted as a journal event.
5. Detect revision gaps, duplicate/out-of-order publication, unknown event, and
   reducer failure; close/degrade the host and rebuild rather than guessing.
6. Treat Watch as an invalidation/refresh hint only. Dropped/coalesced/late watch
   messages trigger bounded authoritative rescan/replay.
7. Make all snapshots immutable to readers. A caller must not mutate slices,
   maps, bytes, or nested values held by the projection.
8. Provide explicit projection discard/rebuild used by tests and recovery.
   Deleting all RAM state and replaying durable history must yield byte-identical
   canonical state and digest.
9. Do not persist a separate query database, cache WAL, shadow file, or rescue
   store. Checkpoints remain journal-derived recovery artifacts, not a second
   writable truth.

**Required tests**:

- Before-ack and ambiguous-unresolved publications never appear in memory.
- Acknowledged/reconciled publication becomes visible exactly once.
- Lost watch delivery and projection loss rebuild identically.
- Reader mutation cannot alter stored snapshots.
- Unknown/gapped/partial events close projection readiness.
- Concurrent queries observe a complete old or complete new revision, never a
  mixed projection.

**Exit evidence**:

```bash
go test ./internal/productionruntime -run 'Projection|Publish|Rebuild|WatchHint|Snapshot' -count=20
```

## T068: Expose query-only mission, work, operation, and progress views

**Purpose**: Let callers answer “where is this running mission?” cheaply and
precisely without minting authority or coupling to storage.

**Files**: `progress.go` and `host_test.go`.

**Implementation requirements**:

1. Define typed Get/List snapshot queries for host, mission, WorkRevision/work,
   operation/effect, review/acceptance, and integration progress.
2. A mission progress snapshot includes MissionID, coarse lifecycle and full
   authorized/unavailable action census,
   journal revision, runtime epoch, current WorkRevision/generation, total and
   per-state work counts, running RunID/LeaseID/ActorID coordinates, blockers,
   open decisions, pending reconciliation/effects, review/integration state,
   typed blocking predicates and responsible producers, and bounded diagnoses.
3. Distinguish authoritative recorded state from observed external status and
   projection freshness. Never collapse “requested,” “observed,” “accepted,”
   and “completed.”
4. Return immutable canonically ordered results with explicit schema version,
   projection revision/digest, completeness, and observation time from recorded
   facts where applicable.
5. Define an in-process watch/subscription contract over projection publication.
   It reports revisioned snapshots or change notices and never journal events as
   commands.
6. Bound subscriber count, queue depth, snapshot/list sizes, diagnostics, and
   cancellation. A slow/overflowed subscriber receives a typed resnapshot
   requirement; it cannot block command publication.
7. Resume uses a projection revision hint only. If history is unavailable or
   the hint is stale/foreign, return a current complete snapshot rather than
   pretending the watch is a durable replay log.
8. Queries/watches perform zero durable writes, OperationID allocation, epoch
   changes, effect calls, or authority CAS. Unsupported filters fail typed.
9. Keep the contract projectable to later CLI/API/MCP work without implementing
   any transport, HTTP, JSON-RPC, or MCP task lifecycle here.

**Required tests**:

- Empty, active, blocked, decision-required, reconciling, reviewing,
  integrating, failed, canceled, and completed mission snapshots are exact.
- Querying or watching changes zero durable/provider/external call counters.
- Slow subscriber cannot delay a mutation and receives resnapshot-required.
- Snapshot revisions are monotonic and concurrent readers see complete values.
- At least 10,000 bounded Get/List observations meet the p95 target below 2 ms
  on the declared reference workload, with raw measurements retained.

**Exit evidence**:

```bash
go test ./internal/productionruntime -run 'Progress|Query|Watch|SlowSubscriber|Latency' -count=10
```

## T069: Qualify ownership, restart, stale clients, roots, and performance

**Purpose**: Attack the complete host boundary under real sidecar lifecycle,
wrong authority, process death, state loss, concurrency, and load.

**Files**: `host_test.go` and `internal/sidecar/gapdb/**` tests.

**Required campaigns**:

1. Run at least 100 clean and unclean owner restarts; every ready lifetime has a
   unique epoch and every predecessor client/capsule refuses.
2. Run at least 100 restore/projection comparisons spanning every registered
   event family; canonical bytes and digests match exactly.
3. Delete the full in-memory projection at ready, active, blocked,
   reconciliation, review, integration, and terminal states; rebuild each from
   checkpoint plus suffix and from bounded origin history.
4. Race two hosts against the same database, stale process/socket artifacts,
   foreign live ownership, PID reuse, wrong executable, wrong database, and
   wrong AuthorityBinding. No case admits two writable owners.
5. Exercise empty/current roots and WP12 historical, mixed, symlinked,
   shared-filesystem, foreign-owner, malformed, and ambiguous fixtures. Every
   unsupported root refuses before sidecar start/current mutation.
6. Drop, reorder, duplicate, and overflow watch notifications while commands
   continue; queries recover by resnapshot/replay without treating watch as log.
7. Inject faults before/after sidecar acquisition, byte recovery, checkpoint
   selection, suffix restore, epoch admission, reconciliation, projection
   publication, readiness, command commit acknowledgement, and shutdown.
8. Measure checkpoint-plus-10,000-operation recovery over at least 30 runs for
   the final p95 below 2 seconds and 10,000 query observations below 2 ms p95.
9. Run bounded races across host lifecycle, command barrier, projection swap,
   query/watch, sidecar death, restart, and concurrent capability commands.

**Mandatory controlled mutants**:

- arbitrary executable or PATH discovery;
- compatible but unadopted sidecar/database accepted;
- root classifier runs after sidecar/current-state mutation;
- second host opens the same authority;
- ready barrier opens before restore/epoch/reconciliation completes;
- fresh epoch admitted before product restore;
- predecessor client passes after restart;
- projection updates before durable ack;
- watch message treated as durable event;
- lost memory loses current mission state;
- query allocates operation/lease or writes durability;
- raw GapDB/database handle escapes composition;
- SQLite fallback/comparison/backend selector exists;
- playback invokes any external or durable-write adapter.

Each mutant requires a passing control and semantic failure for the intended
typed reason; source-string checks alone do not prove runtime behavior.

**Validation commands**:

```bash
go test ./internal/productionruntime ./internal/sidecar/gapdb -count=10
go test -race ./internal/productionruntime ./internal/sidecar/gapdb -count=1
go vet ./internal/productionruntime ./internal/sidecar/gapdb
staticcheck ./internal/productionruntime ./internal/sidecar/gapdb
go test ./internal/architecture -count=1
go test ./... -run '^$' -count=1
gofmt -d internal/productionruntime internal/sidecar/gapdb
git diff --check
```

## Definition of Done

- Exact M1 adoption, executable, protocol, socket/process, DatabaseID, and
  AuthorityBinding coordinates are verified before product authority.
- One host owns one sidecar/database/epoch/composition/projection.
- Unsupported roots refuse before sidecar startup and current mutation.
- Command barrier opens only after restore, fresh epoch, invalidation, and
  mandatory reconciliation complete.
- Existing operation replay precedes new-epoch validation.
- Memory projection publishes only after durable ack/exact reconciliation and
  is completely disposable/rebuildable.
- Query/watch surfaces report precise mission progress with zero authority or
  durable mutation.
- Wrong-owner/database/root, restart, stale-client, projection-loss, watch,
  performance, fault, mutant, and race campaigns pass.
- No SQLite, backend selector, raw store escape, transport, or alternate owner
  exists in the production runtime surface.
- T064–T069 are recorded `done` in the canonical task event log.
- The committed WP is handed to an independent reviewer and not self-approved.

After all implementation and validation genuinely complete:

```bash
spec-kitty agent tasks mark-status T064 T065 T066 T067 T068 T069 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ \
  --json
```

## Risks and mitigations

| Risk | Required mitigation |
|---|---|
| Healthy wrong database appears empty | Verify immutable AuthorityBinding and DatabaseID before restore/init. |
| Multiple CLI processes each start a sidecar | Keep lifecycle/repositories private to one host; clients use capabilities. |
| Barrier opens during partial recovery | Encode closed host states and assert zero command calls at every startup cut. |
| Reconciliation is confused with playback | Keep playback capability-free; run observers only after sealed restore with barrier closed. |
| RAM becomes acknowledged truth too early | Swap projection only after durable ack or exact publication reconciliation. |
| Watch loss causes state loss | Treat watch as a hint and resnapshot/replay authoritative records. |
| Fast queries leak mutable maps or authority | Publish immutable snapshots and use query-only interfaces with zero-call probes. |
| Progress transport expands M2 scope | Define in-process projectable results only; leave CLI/API/MCP to later work. |

## Independent reviewer guidance

Reproduce startup from an empty root, a current bound root, a wrong healthy
database, and every WP12 refusal class. Inspect actual call order: classifier,
admission, sidecar ownership/byte recovery, product restore/validation, fresh
epoch plus predecessor invalidation, reconciliation, projection publication,
then ready. Any current mutation before classification or command before ready
is blocking.

Kill the host after every durable boundary and delete all memory state. Verify
that restart reconstructs byte-identical progress, rotates epoch once, rejects
stale clients, and replays committed results before epoch validation. Suppress
all watch notifications to prove they are not durability.

Confirm a second host and a compatible-but-wrong database both fail closed.
Verify queries answer where any running mission is—including action readiness,
work counts, runs/leases, blockers, decisions, reconciliation, reviews,
integration, and all legal/unavailable actions—without performing any mutation. Reject raw
store escape, optimistic projection, hidden fallback, unbounded subscriber
backpressure, dead APIs, test-only composition, or transport expansion.
