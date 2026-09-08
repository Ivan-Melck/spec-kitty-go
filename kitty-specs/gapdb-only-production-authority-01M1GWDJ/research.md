# Research: GapDB-Only Production Authority

## Research question

How can Spec Kitty replace the qualified Phase 1–3 SQLite authority without
porting its table structure or losing behavior, while preserving the exact M1
GapDB admission tuple and proving resumable memory-primary operation?

## Sources

- `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md`
- `docs/PRODUCTION_RUNTIME_PROGRAM.md`
- `docs/PYTHON_BEHAVIOR_INVENTORY.md`
- `docs/programs/evidence/m1-adoption.json`
- `kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/`
- Consumer-owned service interfaces under `internal/{artifact,execution,mission,workspace,handoff,review,integration}`
- Current adapter/composition code under `internal/controlstore/sqlite` and `internal/helpercmd`
- Adopted GapDB public API at `/home/lynn/projects/gap/gapdb`

## Decision 1: Treat M1 admission as an immutable dependency

**Decision**: Bind M2 entry evidence to the exact accepted adoption record,
remote evidence archive, source/tree, sidecar image, public protocol, product
budgets, and evidence manifest. Reverify the remote archive before the first
implementation lane and final qualification.

**Rationale**: A mutable release label would let a different provider or
product contract inherit evidence it did not earn. M2 may add Spec Kitty event
families and application adapters, but provider-tuple drift is a new adoption
decision rather than an incidental dependency update.

**Alternatives considered**:

- Follow the latest GapDB branch: rejected because it breaks source binding.
- Embed the GapDB server: rejected because the adopted public lifecycle is a
  project-local sidecar over a Unix socket.
- Treat passing M1 tests on later source as equivalent: rejected because
  evidence is commit/tree and artifact bound.

## Decision 2: Separate global command binding from operation progression

**Decision**: Give every caller-visible `OperationID` one immutable
host-authority-wide `OperationBinding`, outside mission keyspaces. The binding
records command kind and schema, canonical request digest, typed scope,
actor/process and governance coordinates, originating runtime epoch, and
observed creation time. Exact reuse resumes or replays; a difference in any
bound field returns `IDEMPOTENCY_CONFLICT` before capability reads or effects.

Model mutable progress separately as an `OperationState` aggregate. It advances
monotonically through accepted, in-progress, resolved, refused, ambiguous, or
reconciliation-required states and carries the canonical typed result plus
event/effect membership. A storage `PublicationID`, derived from OperationID,
operation logical version, and sealed batch digest, identifies each durable
stage without inventing another caller operation.

The first semantically accepted stage atomically inserts the absent binding,
creates or advances OperationState, changes capability aggregates, and records
effect intents in one conditional batch. Later saga stages CAS OperationState
and their capability aggregates. No naked reservation is permitted: a
pre-effect reservation must itself record a meaningful requested/intent fact.

Runtime epoch is evaluated authority provenance, not part of the caller's
stable canonical intent digest. Resolution checks an existing binding/result
before current-epoch admission so a committed result remains replayable after
restart. If no binding exists, a request carrying predecessor authority is
refused for explicit re-evaluation under the new epoch; it is never silently
executed or assigned a replacement OperationID.

**Rationale**: SQLite currently enforces global uniqueness through primary keys
and cross-namespace triggers, while M1's proving prefix is mission-scoped.
Ignoring that mismatch would allow the same identity to mean different
commands after migration. A direct global operation key preserves the law
without a global journal-head hotspot or central god-store.

**Alternatives considered**:

- Make OperationID mission-scoped: rejected as silent behavioral weakening.
- Use one immutable terminal operation record: rejected because review,
  integration, workspace, decomposition, and effects have legitimate durable
  stages under one caller identity.
- Allocate a global sequence before every command: rejected because it creates
  a separate partial state and unnecessary contention.
- Keep a small SQLite reservation database: rejected as dual authority.

## Decision 3: Address suffix history by aggregate logical version

**Decision**: Replace M1's opaque EventID-only journal layout in the production
v2 keyspace with immutable, version-addressed aggregate event records. Every
accepted stage emits at most one transition event for each affected aggregate,
increments that aggregate's logical version once, and publishes the event and
head together. A checkpoint records the complete aggregate census, each
logical version, and the canonical projected state through a positive GapDB
watermark.

Restore closes command admission, captures one positive durable
`(DatabaseID, R)` coordinate, scans the stable current head census, and
compares it with the checkpoint census. For each new or advanced aggregate it
performs bounded parallel exact reads for versions
`checkpointVersion+1..currentVersion`. The event union is validated against
global OperationBinding and reconstructible historical OperationState
membership, grouped by commit revision, and ordered by
`(Record.Revision, EventIndex, EventID)` before pure reduction.
This gives suffix-bounded work without a per-command global sequence or a
provider revision-range API. A bounded full head/event reconstruction remains
the fallback when no compatible checkpoint exists; missing versions, excessive
suffixes, or an over-budget census fail readiness.

GapDB `Watch(afterRevision)` remains only a live projection invalidation hint.
Its bounded, compactable notification history is not the durable replay log.
Product checkpoint retention, suffix budgets, and immutable event records make
recovery independent of watch retention and provider WAL layout.

**Rationale**: The adopted public GapDB API has key-prefix scans and exact
reads, but no durable revision-range scan. Its watch history may compact and is
explicitly recoverable only by rescan. Per-aggregate logical versions already
exist for conflict detection; using them as immutable event addresses makes the
suffix directly enumerable, preserves unrelated-aggregate concurrency, and
still uses provider record revisions as the total cross-aggregate commit order.

The head namespace must be enumerable before any scope is known. Aggregate
heads therefore use
`spk/v2/heads/<scope>/<aggregate-kind>/<aggregate-id>` while immutable events
remain under the scope-first journal namespace. The rejected
`spk/v2/scopes/<scope>/heads/...` spelling cannot discover a scope created
after the checkpoint without scanning all scoped event history. Operation
bindings and current heads retain their host-global `spk/v2/operations/`
namespace and are scanned as a second bounded census.

GapDB does not expose an as-of `Get` or accept a requested revision on an
initial `ScanPrefix` call. Recovery therefore does not claim either facility.
It requires the first page to report captured `R`, relies on the provider-bound
cursor for continuation pages, requires every record revision to be positive
and no greater than `R`, and finally rechecks the identical DatabaseID and
exactly `R`. Monotonic non-reusable revisions make an equal capture/final pair
proof that no committed mutation occurred during the attempt. Any difference
discards the whole attempt.

**Alternatives considered**:

- Scan all EventID journal keys and filter revisions: retained only for the
  frozen M1 proof; it cannot guarantee suffix-bounded work after a nonzero
  checkpoint.
- Encode a global product sequence into keys: rejected because it adds a hot
  global CAS and duplicates the provider's already durable commit order.
- Use Watch as the durable suffix: rejected because bounded notification
  retention and compaction are not a product journal contract.
- Change GapDB to add an after-revision scan: rejected for M2 because it drifts
  the adopted tuple; reconsider only through a new explicit provider adoption.

## Decision 4: Build capability-owned repositories, not a replacement god-store

**Decision**: Each existing consumer service keeps or gains a narrow port:
artifact revision, mission lifecycle/action/decision, WorkRevision
finalization/reconciliation, decomposition reservation, run/lease/governance,
workspace, external effects, handoff, review, acceptance, integration, outbox,
and read projections. Implementations translate capability commands into
versioned domain events and one `ProductBatch`; no general `Store` exposes raw
keys or provider types to the kernel or service.

**Rationale**: `internal/controlstore/sqlite.Store` currently centralizes many
unrelated laws because a relational transaction is convenient. Recreating that
surface over key/value calls would preserve accidental structure and make
ownership unclear. Consumer-owned ports retain compile-time direction and make
atomicity/refusal requirements reviewable per aggregate.

**Alternatives considered**:

- One `GapDBStore` matching all SQLite methods: rejected as the original port
  with different nouns.
- Direct provider calls in domain services: rejected because provider semantics
  would enter the kernel.
- Promote M1's proving-ground workflow store: rejected because it is
  process-local, single-mission qualification machinery.

## Decision 5: Version event families around product capabilities

**Decision**: Extend the frozen event/reducer registry with bounded closed families
for mission, artifact, WorkRevision, decision/blocker, run, lease, governance,
workspace, effect, candidate/handoff, review, acceptance, integration, outbox,
and projection/checkpoint facts. Cross-aggregate commands list every expected
head and event in one validated batch. Unknown event/reducer/schema/order
coordinates fail restore before any command is admitted.

The operation family uses event schema 2 and reducer version 2. Its immutable
payload is the canonical OperationState transition preimage, excluding only
the PublicationID derived by the sealer; the completed envelope supplies that
identity during reconstruction. The incomplete operation `1/1` tuple is not a
compatibility alias. Registry identity includes registry ID/version,
publication rule, ordering tuple, and the sorted family/schema/reducer/owner
census. Reducer identity additionally includes the declared implementation
digest. Runtime `Apply` is stateless; repeated-call determinism qualification
is separate from replay and cannot impose a 256-event session ceiling.

**Rationale**: Event families are the stable semantic boundary that M3–M5 will
consume. They should describe what happened, not mirror tables or provider
mutations. Complete registries make restore and evidence closed-world and
mutation-testable.

**Alternatives considered**:

- One generic state-updated event: rejected because it hides authority laws.
- Serialize current SQL rows as payloads: rejected as table transliteration.
- Permit unregistered events for forward compatibility: rejected because it
  makes deterministic playback impossible.

## Decision 6: Compose one owner lifecycle and one projection authority

**Decision**: A local control host owns the exact admitted sidecar lifecycle,
one database identity, one runtime epoch, application services, in-memory
aggregate/progress indexes, checkpoint scheduling, and revision watches. Remote
or CLI callers use typed application capabilities. They never open GapDB or
construct repositories themselves.

All authority writes pass through a foundation-owned publication coordinator.
It registers the sealed plan before delivery, accepts only a durable exact
acknowledgement or same-identity reconciliation, and only then advances the
in-memory projection. It keeps a bounded revision-keyed acknowledgement ledger
capable of at least 10,000 publications between seals. At a quiescent
checkpoint barrier it orders acknowledgements by provider revision and derives
the rolling prefix from the prior prefix, revision, PublicationID, plan digest,
and sealed record-set digest. Those otherwise process-local proof coordinates
are persisted in one immutable, absent-conditioned publication witness in the
same atomic batch. The witness is constructed after PublicationID and the
non-witness record-set digest, avoiding recursive identity, and the live
tracker and restore share one pure prefix-advance helper. Unresolved ambiguity
blocks a seal. Capability packages may reach this coordinator but may not
reach the GapDB adapter.

**Rationale**: This is the machine/filesystem authority boundary ratified by
the architecture. It preserves fast reads while making restart and ownership
explicit. Package composition belongs outside the kernel.

**Alternatives considered**:

- Let every CLI process open/start a sidecar: rejected as split ownership.
- Shared GapDB files over network storage: rejected as outside first-release
  authority and GapDB's qualified boundary.
- Persist projections separately: rejected as a second truth; projections are
  rebuilt from checkpoint plus journal.

## Decision 7: Retire legacy eligibility and adapter-specific CLI contracts

**Decision**: Remove `legacy-v1` eligibility selection and every default that
falls back when complete eligibility is absent. Handoff, review, acceptance,
and integration require the current complete eligibility fact. Move
SQLite-specific WorkRevision request/evidence types behind consumer-owned
workgraph/mission contracts before removing the adapter. Replace raw `--db` and
`database` inputs with one control-host endpoint/configuration contract that
cannot select an authority substrate.

**Rationale**: A compatibility flag is a second semantic path even when the
storage provider is new. The target has one authority model and one complete
eligibility law.

**Alternatives considered**:

- Keep legacy mode hidden for tests: rejected because compiled reachability is
  still a product path.
- Auto-detect old/new state: rejected as implicit authority selection.

## Decision 8: Refuse historical roots before current mutation

**Decision**: Add a read-only bounded control-root classifier that detects the
known historical SQLite files, journals, sidecars, copied/renamed signatures,
mixed current/historical layouts, symlinked or shared roots, and malformed
current layouts. Any historical or ambiguous finding returns a typed
`OFFLINE_DISPOSITION_REQUIRED` before starting GapDB or creating current
state. M2 supplies diagnosis and documentation, not an importer.

**Rationale**: Treating an old root as empty loses missions; importing it inside
startup creates a hidden migration and dual-truth window. Refusal is the only
safe behavior within M2's scope.

**Alternatives considered**:

- Ignore old files: rejected as silent data loss.
- Rename and import automatically: rejected as unreviewed migration.
- Ship an offline converter in M2: rejected as unrelated falsification target;
  it can be specified independently if users require it.

## Decision 9: Prove structural absence across every production surface

**Decision**: Remove the SQLite package, all direct/indirect SQL imports,
modernc driver and SQLite-exclusive transitive modules, schema migrations,
adapter constructors, raw database flags/configuration, legacy modes, and
SQL-aware scenario assertions. Add closed architecture/reachability mutants,
module-graph checks, a deterministic CycloneDX SBOM generated from the complete
module/build graph, built-binary dependency/symbol/string inspection, clean
checkout/install qualification, runtime smoke tests, and historical-root
refusal probes.

Historical docs and committed evidence remain allowed under exact path-scoped
rules. Test code must no longer compile the driver; SQL-introspection tests are
ported to domain invariant queries or archived as non-Go historical evidence.

**Rationale**: Disabled code, test-only imports, build tags, and unused module
requirements can re-enter production later. The absence claim is meaningful
only when source, dependency, artifact, installation, configuration, and
runtime evidence agree.

**Alternatives considered**:

- Delete only production constructor calls: rejected because the dependency and
  escape hatch remain.
- Retain SQLite for test comparison: rejected because tests keep the driver and
  table semantics alive; frozen evidence already preserves the baseline.

## Decision 10: Qualify per capability before the final crown

**Decision**: Each capability slice carries contract, refusal, race,
crash/ambiguity, restart, and mutant tests into an exact M2 campaign census.
The final trial runs a complete two-item mission with owner restarts between
major phases, historical-root negatives, structural absence, clean install,
SBOM/binary checks, latency/recovery measurements, and independent review under
one source-bound evidence manifest.

**Rationale**: Package compilation cannot prove cross-capability atomicity or
recovery. Conversely, one large end-to-end trial cannot localize semantic drift.
Layered evidence provides both.

**Alternatives considered**:

- Port all stores and test at the end: rejected because architectural mistakes
  would be expensive to unwind.
- Accept current tests unchanged: rejected because many tests query SQL tables
  directly and therefore prove the old mechanism rather than product behavior.

## Resolved planning risks

| Risk | Resolution |
|---|---|
| Store-wide OperationID law differs from M1 mission prefix | Global application-owned operation record in every atomic command batch |
| Nonzero checkpoint suffix was previously refused | Version-addressed per-aggregate immutable events, stable head census, bounded exact suffix reads, and full-replay fallback |
| M1 direct authority CAS wrappers tempt bypass of the journal | Production capability adapters may persist authority only through validated event `ProductBatch`/epoch contracts |
| Legacy eligibility survives as a config fallback | Delete modes/defaults and require complete eligibility everywhere |
| SQLite god-store becomes a GapDB god-store | Consumer-owned ports and event families are separate compile-time boundaries |
| SQLite-only tests keep module dependency alive | Port assertions to domain/read-model invariants or archive outside Go build |
| SBOM proof does not exist | Add a pinned deterministic CycloneDX qualification step and validate built artifacts against it |
| Historical roots look empty after adapter deletion | Classify and refuse them before any current provider mutation |
