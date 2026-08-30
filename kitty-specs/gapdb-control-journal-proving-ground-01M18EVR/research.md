# GapDB control-journal proving ground: research record

Status: decision-ready research for M1 planning. This document is not an
implementation claim or an adoption approval.

## Executive decision

Proceed with the narrow M1 proving ground described by the production runtime
program. The admitted GapDB artifact exposes the storage primitives needed for
the experiment, but the prior GapDB and Spec Kitty campaigns do not prove the
combined production boundary. M1 must therefore use the pinned external
`gapdbd` process and the public client protocol, re-run every one of GapDB's 14
portable-adapter scenarios through that boundary, and then execute the two-agent
torture chamber with product-level recovery and playback.

The experiment is admitted by machine-verifiable evidence. Human approval is
not an entry prerequisite: the named human signs the exact tested tuple only at
the M1 exit. If the experiment cannot preserve one valid authority and one
canonical restored/playback digest through the stale-writer scenario, the
program stops before M2.

The key design decision is to keep the product transaction indivisible. One
durable `AckDurable` GapDB atomic batch must contain:

1. the complete bounded, versioned event set for the command;
2. the `OperationID` request digest and typed idempotent result;
3. affected aggregate heads and the authoritative projection head;
4. checkpoint metadata, and the checkpoint itself when due; and
5. any external-effect/outbox intent created by the command.

Nothing becomes visible to live projections or watchers until that batch is
durably acknowledged. External effects run later. GapDB supplies atomic bytes,
conditional revisions, durability, and recovery; Spec Kitty continues to own
event meaning, reducer compatibility, authority, workspace quarantine,
idempotency, effect attribution, and acceptance.

## Scope and non-decisions

M1 covers one project, one verified GapDB owner, one real Git repository, two
finalized independent work items, two actor/process identities, one bounded
tracker adapter, independent review, and serialized integration. It does not
select a distributed database, admit a network filesystem, replace the product
event model with GapDB records, prove multi-host operation, or approve M2.

The evidence applies only to Linux/amd64 on one host under one filesystem
authority with an owner-visible lock, durable file and parent-directory fsync,
and same-filesystem atomic rename. Network/shared and otherwise unsupported
filesystems remain excluded.

## Admitted, pinned tuple

| Element | Admitted value |
| --- | --- |
| Architecture repository | `22128ca5dbec59e81ff40ea2259b0f61c8979a84` |
| GapDB source/tree | `ff3dd478f87c6b4cbb1dcc168562f99879ba8c25` / `64ada3cd6328aca73ef902e4ab9fe0405823841c` |
| Go module | `v0.0.0-20260824033134-ff3dd478f87c` |
| Module sum | `h1:kmYoO7Q3PlsNM6M0dSYtuJJSQSlbflC2wz3d/Bek2L0=` |
| go.mod sum | `h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=` |
| Sidecar command | `gapdbd` |
| Sidecar SHA-256 | `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e` |
| Build recipe | `GOBIN=<project-bin-directory> CGO_ENABLED=0 GOTOOLCHAIN=go1.26.7 go install -trimpath github.com/spec-kitty/gapdb/cmd/gapdbd@v0.0.0-20260824033134-ff3dd478f87c` |
| Wire/storage schema | protocol v1 / storage v1 |
| Reproducibility evidence | two builds, identical digest |
| Qualified Phase 3 source/tree | `03afabd6d875dd133d18fa759669498d92e097be` / `6173b2dbce671d6243b14f99414170c8a5929f0f` |
| Original Phase 1 source | `adf15aac42f3f03dd27ad6252df973bcd8b42433` |
| Exit signer | `github:lynncoleart` |

There is no GapDB release tag for this tuple. General instructions such as
installing `@latest` or discovering `gapdbd` on `PATH` are not admissible for
M1. The runtime must execute only the configured path after hashing it.

## Evidence-census dictionary

The historical evidence uses several different units. They must remain
separate in plans, reports, and acceptance claims.

| Count | Exact meaning | What it is not |
| ---: | --- | --- |
| 73 | Historical Phase 3 top-level Go tests in the broad `historical-full-scenario` census; 73/73 passed at the qualified source. | Not 73 scenarios in the Phase 1 catalog. |
| 35 | Behavioral cases in the Phase 1 scenario catalog: P1-01 through P1-28 plus WR-01 through WR-07. The catalog records 31 complete and four partial cases. | Not 35 top-level Go tests. |
| 28 | Named Phase 1 crash cuts: SR-00..09, HR-00..07, and IG-00..09. | Not a top-level-test count and not inclusive of Phase 3 cuts. |
| 17 | Frozen Phase 1 top-level tests exercised in 65 disjoint shards, each with `-count=100`. | Not the 28 named crash-cut total. |
| 9 | Phase 3 supplemental transaction, saga, and post-commit-response boundaries. | Not additions to the 28 as though both counts shared a unit. |

M1 adds five GapDB-specific cuts of its own. Those cuts and the 14 GapDB
portable-adapter scenarios are again distinct evidence classes. Reports must
state the unit beside every count rather than adding them together.

The nine Phase 3 supplemental boundaries are:

- `MM-ARTIFACT-APPROVAL-TX-ABORT`
- `MM-ARTIFACT-POSTCOMMIT-RESPONSE`
- `MM-DECOMPOSITION-APPLY-LOST-RESPONSE`
- `MM-FINALIZATION-PREWRITE-PROVIDER`
- `MM-FINALIZATION-TX-ABORT`
- `MM-FINALIZATION-POSTCOMMIT-RESPONSE`
- `MM-RECONCILIATION-DRIFT-TX-ABORT`
- `MM-RECONCILIATION-LATE-TX-ABORT`
- `MM-RECONCILIATION-POSTCOMMIT-RESPONSE`

These bodies of evidence establish the behaviors to preserve: primary-repo
isolation, one valid lease, immutable workspace binding, stale-token refusal,
target-ref serialization, reachable immutable candidates, and
attribution-safe compensation. They are historical design inputs because their
storage/runtime composition used SQLite and helpers rather than the external
GapDB production boundary.

## Storage-mechanics findings

GapDB has the necessary public primitives: `Get`, `Put`, `PutIfAbsent`, CAS,
conditional delete, `AtomicBatch`, sorted prefix scan, ordered watch, status,
health, and memory or durable acknowledgements. A successful durable batch
receives one global revision and publishes all of its records atomically.

Default bounds materially constrain the product schema: keys are 4 KiB, values
8 MiB, frames and batches 16 MiB, a batch has 1,024 operations, scans return at
most 1,000 records/16 MiB, and retained watch history is 100,000 events/64 MiB.
The product must define smaller budgets and refuse an oversized command before
publication. It must not rely on the larger hard ceilings as normal capacity.

One important modeling constraint follows from server-assigned revisions. A
client does not know the batch revision while constructing batch values, so it
cannot embed that revision in an event value or use it in the event key without
a second write. A second write would break the one-batch law. Journal entries
must instead use immutable, preallocated event identities plus a zero-based
`event_index`; replay obtains each record's authoritative GapDB revision from
the returned `Record.Revision` and orders by `(Record.Revision, event_index,
EventID)`. The operation result similarly obtains its commit revision from its
record metadata after acknowledgement or read reconciliation.

All writes in a new command carry conditional mutations: operation and event
keys require absence, while existing aggregate/projection heads use their
observed revisions. A prior `OperationID` is read first. The same canonical
request digest returns the stored typed result; a different digest is an
idempotency conflict. If the response is lost after GapDB acceptance, the host
re-reads that operation using the same identity. It must not synthesize a new
operation or dispatch an effect until durable state identifies the outcome.

GapDB's expiry is useful for liveness but does not define Spec Kitty authority.
Lease expiration is a product event and reducer decision; a missing/expired
storage record alone must not transfer a mutable workspace.

## Versioned event and reducer decision

Each event is a bounded canonical envelope containing `event_type`,
`schema_version`, `reducer_version`, aggregate identity, operation identity,
runtime epoch, event index/count, occurred-at data when semantically required,
and a type-specific payload. Event and reducer registries are closed for the
admitted build. Unknown event types, unknown versions, invalid ordering, a
digest mismatch, or the absence of a compatible deterministic reducer closes
recovery; there is no best-effort skip or force-ready path.

Reducers are pure. Recorded observations, policy revisions, work revisions,
and time decisions are inputs in the journal rather than calls made during
reduction. A checkpoint names its reducer/schema set, its through-position, a
canonical journal-prefix digest, and the resulting projection digest. This
makes live restore and playback comparable without giving GapDB knowledge of
product semantics.

## Recovery and playback protocol

Startup is an admission barrier, not a period of partial service:

1. verify the configured sidecar executable digest and configured paths;
2. connect to the configured socket and prove the expected database/process
   provenance, or start the verified executable; never take over an unknown
   owner;
3. require protocol-v1 status and health bound to the expected socket,
   database, and process before accepting commands;
4. let GapDB recover its bytes and fail closed on interior corruption,
   checksum/identity/version errors, or an invalid storage chain;
5. validate the complete compatible product journal;
6. select the newest valid checkpoint within the revision-pinned replay budget
   and reduce the suffix; if no checkpoint fits, replay the full journal only
   when bounded; otherwise expose degraded/refused readiness;
7. durably create a new `RuntimeEpochID` before serving commands;
8. make every predecessor lease non-current and quarantine its workspace;
9. reconcile outstanding external-effect intents by provider observation,
   without repeating an already-attributable effect;
10. rebuild projections and progress, then admit commands.

The new epoch is mandatory on every restart, including a restart after an
apparently clean shutdown. It prevents an old run capsule from becoming valid
merely because the same database and process role returned.

Playback reads the same checkpoint and ordered journal but runs only the pure
version-pinned reducers. It must invoke zero tracker, Git, filesystem,
qualification, artifact, outbox, compensation, or other external adapters.
The terminal canonical projection digest from live restore and playback must be
byte-identical. A playback command cannot mutate the journal, mark effects
observed, or allocate authority.

## Workspace isolation under stale writers

Database fencing cannot fence direct POSIX writes. Therefore a mutable
workspace is permanently bound to the lease and runtime epoch that created it.
When that lease expires, the workspace is quarantined; it is never transferred
to the replacement lease. The replacement receives a fresh workspace identity
and worktree (W3 in the torture chamber). A zombie may continue modifying W1,
but its stale capsule and workspace identity fail handoff, review, and
integration preconditions.

The required experiment must demonstrate that property, not merely inspect a
lease row: stop the first process's heartbeats while leaving it alive, expire
its lease, grant a replacement, allow the zombie to resume writing W1, finish
valid work in the fresh workspace, and prove that only the current candidate
can reach serialized integration.

## Five new GapDB-specific fault cuts

M1 must add these cuts at the real external boundary:

| Cut | Required observation |
| --- | --- |
| Before request publication | No durable operation/event/effect and retry is an ordinary first attempt. |
| After GapDB acceptance but before client response | Outcome is ambiguous; same `OperationID` and digest read-reconcile to one result and one batch. |
| After workspace creation but before durable binding | The orphan workspace has no authority, is quarantined/collected, and retry creates or binds exactly one authorized workspace. |
| After durable binding but before run-capsule response | Retry reconstructs the existing binding/capsule; it does not allocate a second lease or workspace. |
| After external-effect intent but before observation | Recovery observes provider state and records one attributable outcome; it does not blindly repeat or compensate. |

These five do not replace the 28 named Phase 1 crash cuts or nine Phase 3
supplemental boundaries. Focused M1 assertions must carry forward the universal
invariants at each cut.

## External `gapdbd` adapter qualification

The earlier GapDB adoption record passed 14 scenarios, but its GapDB adapter
used the repository's internal server composition. M1 must re-run all 14 through
the installed sidecar and public Unix-socket client:

| Scenario | M1 external-boundary obligation |
| --- | --- |
| `data/get-put` | Preserve opaque bytes and returned revision. |
| `conditional/cas-delete` | Reject absent/revision precondition losers and admit one winner. |
| `batch/atomic` | Expose all records at the same durable revision or none. |
| `scan/sorted-prefix` | Return bounded, deterministic prefix results. |
| `watch/registered-order` | Observe registration boundary and ordered durable publication. |
| `expiry/release-authority` | Demonstrate expiry mechanics without treating them as workspace transfer. |
| `ack/memory-visible` | Make memory acknowledgement semantics explicit and keep it out of authority commits. |
| `ack/durable-restart` | Stop and restart the external owner; retain acknowledged data. |
| `inspect/authority` | Bind database ID and revision/durability inspection to the expected daemon. |
| `authority/lease` | Preserve one durable holder through external restart. |
| `authority/review` | Preserve one durable holder through external restart. |
| `authority/workflow` | Preserve one durable holder through external restart. |
| `authority/integration` | Preserve one durable holder through external restart. |
| `response-loss/reconcile` | Cut the real response, restart externally, and reconcile durable state without a duplicate. |

Passing these scenarios is necessary but not sufficient. The M1 product
scenario must additionally prove checkpoint/suffix restore, a new epoch,
side-effect-free playback, outbox ambiguity handling, lease/workspace
quarantine, independent review, and serialized Git integration.

## Daemon identity and lifecycle gaps

The admitted daemon can be safely pre-hashed, but its current readiness message
contains only schema version, readiness, and socket path. It does not identify
the executable digest, source/module version, build recipe, database ID, owner
PID, or owner start time. A later public status call supplies database ID,
owner PID/start time, revisions, lifecycle, and limits, while health reports
subsystem state. Neither response cryptographically binds the running process
to the pre-exec digest.

The host must therefore own an explicit lifecycle proof: hash the configured
absolute executable before spawn, record the returned process identity and
start time, wait only on the configured socket, then compare status/health and
database identity against its expected paths and stored binding. Connecting to
an orphan is allowed only after digest, socket, database, lock, and process
provenance all match. Any mismatch is an operator decision, not an invitation
to signal or replace an unknown process.

Further gaps remain unresolved for M1 planning:

- `gapdbd` has no self-description or `--version` surface that attests the
  admitted source/build/digest.
- readiness is emitted before it conveys database/process provenance, so
  readiness alone cannot admit product commands.
- the public API provides no daemon-native shutdown RPC; a host-started daemon
  is stopped by signal and must be signalled only while ownership is still
  provable, including protection against PID reuse.
- start-or-connect, orphan reconnection, and same-process graceful shutdown are
  host policies rather than GapDB protocol concepts.
- a database ID proves storage identity, not executable identity.

M1 should first implement the narrow host-owned proof and capture all compared
fields in evidence. If exact same-process ownership cannot be established
without relying on a PID alone, the experiment must fail closed and record a
GapDB upstream requirement rather than weakening the admission policy.

## Decision matrix

| Question | Decision for M1 | Exit evidence |
| --- | --- | --- |
| May active authority be memory-only? | No. Memory is the fast projection; the accepted batch is durable authority. | Every successful command maps to one durable operation/event/head batch. |
| May an event batch be split around a checkpoint or outbox intent? | No. | Fault cuts show all-or-none records at one GapDB revision. |
| May a replacement inherit an expired workspace? | No. | Zombie W1 remains quarantined; replacement has fresh W3. |
| Is process restart the same runtime epoch? | No. | New durable epoch and rejection of every old capsule. |
| Can playback call adapters? | No. | Instrumented adapter-call census is exactly zero. |
| Can the product parse human CLI output? | No. | Only public typed protocol responses are consumed. |
| Does prior 14/14 GapDB evidence approve adoption? | No. | New 14/14 external-sidecar evidence plus torture-chamber evidence is required. |
| Is a human signature required before experimentation? | No. | Signature is collected only over the exact successful exit tuple. |

## M1 exit and stop conditions

Technical exit requires all of the following in one reproducible evidence set:

- the exact admitted source/module/binary/protocol/storage tuple;
- 14/14 portable scenarios through external `gapdbd`;
- all five new GapDB-specific cuts with universal-invariant assertions;
- one batch per accepted command containing events, operation result, heads,
  due checkpoint metadata, and outbox intent;
- response-loss reconciliation with one operation and no duplicate effect;
- owner/host restart with a new durable runtime epoch;
- checkpoint-plus-suffix restore and equal live/playback canonical digests;
- zero adapter calls during playback;
- stale lease rejection, permanent W1 quarantine, and a fresh replacement
  workspace;
- independent review and serialized integration of both valid candidates;
- a complete daemon provenance/lifecycle record and explicit treatment of any
  unresolved GapDB support gap; and
- the named human's signature over that exact evidence only after all machine
  gates pass.

Failure to preserve one valid authority or the canonical digest through the
zombie scenario is a program stop. No result from an unsupported filesystem,
unpinned executable, in-process/internal server, or partial scenario set can
be promoted to M1 evidence.

## Open risks carried into planning

1. **Daemon attestation:** pre-exec hashing plus PID/status correlation may not
   prove that a reconnecting orphan is the admitted executable. Plan an
   explicit fail-closed decision point and possible GapDB protocol enhancement.
2. **Lifecycle ownership:** signal-based shutdown needs stronger protection
   than PID equality. Capture process start identity and revalidate immediately
   before signalling.
3. **Batch budget:** realistic product events, typed results, checkpoint data,
   and effect intent must fit the 1,024-operation/16-MiB defaults. Establish a
   smaller product budget and test rejection before publication.
4. **Replay scan budget:** the public cursor pins the observed revision and
   returns `SCAN_STALE` if the database changes between pages. M1 must exercise
   multi-page traversal behind the closed restore-admission barrier and prove
   its restart behavior rather than assume one 1,000-record page is enough.
5. **Watch retention:** watch history is bounded and cannot be the recovery
   journal. On overflow/disconnect, rebuild from durable records.
6. **Checkpoint corruption or incompatibility:** the runtime needs an explicit
   degraded/refused state, not a fallback that silently skips events.
7. **External ambiguity:** a tracker may be slow, unavailable, stale, or changed
   by a human. Observation and compare-before-compensate must be tested against
   a deliberately misbehaving fake.
8. **Clock semantics:** lease expiry and recorded time decisions must not make
   playback consult wall time. Record the authoritative decision as an event.
9. **Journal retention:** compaction is outside the proving slice. M1 should
   retain immutable event records and defer destructive retention policy.

## Planning recommendation

Plan the work as vertical evidence slices rather than package scaffolding:

1. verified sidecar lifecycle and all 14 external adapter scenarios;
2. canonical event/reducer/key codec and one-batch command/idempotency path;
3. checkpoint/suffix restore, new runtime epoch, and pure playback digest;
4. workspace allocation, lease expiry, quarantine, and stale-capsule refusal;
5. outbox observation/attribution under ambiguous responses;
6. the complete two-agent torture chamber with the five new cuts; and
7. frozen evidence assembly followed by the exit-only human signature.

Each slice should produce machine-readable evidence tied to the admitted tuple.
No scaffolding-only milestone counts as progress, and no later slice may relax
the atomic-batch, fresh-workspace, new-epoch, or side-effect-free-playback laws.
