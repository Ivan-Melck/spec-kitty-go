# Research: Software Development Mission Method

## Scope and method

This research resolves Phase 3 design questions using the ratified technical
plan, the Python behavior inventory, Phase 1/2 source and qualification
evidence, and the current Go implementation. No external dependency research
is required: the critical choices concern authority, persistence, and reuse of
already qualified local capabilities.

## Decision 1 — Extend the existing WorkRevision implementation

**Decision**: Version and extend `internal/workgraph.Revision` rather than create
a separate mission graph.

**Rationale**: Phase 1 already proves immutable canonical encoding, graph and
lane-cycle validation, durable finalization, consumer fencing, all four
disposition consequences, idempotency, and crash-safe persistence. The missing
Phase 3 fields are predecessor, planning source, richer criteria/work content,
write scopes, provider observations, dependency policy, and mixed per-work
reconciliation—not a different aggregate.

**Alternatives considered**:

- A second mission-only graph: rejected because two executable graph authorities
  would be irreconcilable.
- Store only tracker IDs and rebuild at runtime: rejected because a mutable
  provider cannot define a running contract.
- Replace v1 bytes in place: rejected because historical run/review evidence
  must remain decodable.

## Decision 2 — Use static mission definitions

**Decision**: Register the software-development definition at composition time
against closed mission contracts.

**Rationale**: This preserves explicit mission-type resolution and step laws
without building a workflow language before a second implementation earns it.
Tests can census the exact definition version and transition table.

**Alternatives considered**:

- YAML-driven executable workflow: rejected because it creates a second policy
  language for authority transitions.
- Hard-coded conditionals in the CLI: rejected because transports must not own
  mission law.
- Silent fallback to software-development: rejected by behavior contract M-02.

## Decision 3 — Make artifacts typed control-state records

**Decision**: Persist immutable artifact revisions and exact-revision approvals;
use repository files only as content adapters or presentations.

**Rationale**: Git records what bytes existed, but the mission engine also needs
typed artifact purpose, validation outcome, approval actor, evidence, and
freshness. A content digest makes approval staleness deterministic.

**Alternatives considered**:

- Infer approval from file existence or commit messages: rejected as ambient
  and non-portable authority.
- Keep all content only in the control database: rejected for this phase because
  user-versioned specifications and plans remain valuable Git artifacts.
- Parse status prose: rejected because machines branch on typed state.

## Decision 4 — Treat finalization as an observation saga

**Decision**: Observe the provider graph, normalize and validate it, recheck all
provider revisions, then publish the exact observation locally under an
expected-current-generation compare-and-swap.

**Rationale**: No transaction spans Beads/Dolt and the control store. A bounded
recheck closes most practical race windows without claiming impossible
atomicity. The publication identifies its observation; every subsequent change
is explicit drift.

**Alternatives considered**:

- Hold a provider lock across the control-store transaction: rejected because
  the tracker contract does not promise a portable lock and a human may use the
  provider directly.
- Accept any initial read: rejected because known mid-observation change should
  fail rather than freeze an internally inconsistent graph.
- Two-phase commit across stores: rejected because neither provider exposes the
  necessary protocol.

## Decision 5 — Compare semantic content separately from provider revisions

**Decision**: Drift reports distinguish executable semantic change from
observation-only revision change.

**Rationale**: Provider timestamps, ordering, or opaque revisions can change
without changing the execution contract. Conversely equal timestamps can hide
content change. Canonical field comparison decides affected work; provider
revisions remain audit evidence.

**Alternatives considered**:

- Any provider revision inequality is drift requiring disposition: rejected as
  noisy and operationally expensive.
- Compare timestamps only: rejected by Phase 2 equal-timestamp evidence.
- Compare rendered Markdown: rejected because presentation is not canonical.

## Decision 6 — Reconcile with one mixed per-work plan

**Decision**: A successor generation is published with exactly one disposition
for each affected active or accepted old work item. Different items may receive
different dispositions in the same atomic reconciliation.

**Rationale**: Real graph edits frequently have mixed consequences. One removed
item may be invalidated while another text-only-equivalent item continues and a
third forks. The existing per-disposition consequence matrix remains reusable.

**Alternatives considered**:

- One disposition for the entire graph: rejected because it cannot model mixed
  reality safely.
- Several independently committed reconciliations: rejected because partial
  publication can leave two unlabelled authorities.
- Automatically infer dispositions: rejected because reconciliation is a
  governed mission decision.

## Decision 7 — Keep next queries pure

**Decision**: `Next` reads a consistent mission snapshot and returns one closed
typed outcome. The action it names is a separate idempotent command.

**Rationale**: Repeated agent polling must be harmless. Pure queries also make
API/MCP projection straightforward later and prevent a transport correlation ID
from becoming accidental effect identity.

**Alternatives considered**:

- Query-and-claim: rejected because it creates surprising authority during
  inspection.
- Return free-form instructions: rejected because clients would parse prose.
- Let each transport decide the next action: rejected as control-plane
  fragmentation.

## Decision 8 — Reuse sealed governance contexts

**Decision**: Mission action records reference the existing schema-v12 sealed
governance context containing content commitments for configuration, doctrine,
profile, and mission definition.

**Rationale**: Phase 1 already guarantees immutable entry sets, aggregate digest
verification, and mandatory run binding. Extending that model to pre-execution
mission actions closes provenance early without migrating run rows later.

**Alternatives considered**:

- Store mutable paths: rejected because later edits would rewrite history.
- Copy the entire doctrine catalog into every action: rejected for privacy and
  context size.
- Wait until external dispatch: rejected because provenance is state and is
  expensive to retrofit.

## Decision 9 — Make tasks Markdown output-only

**Decision**: Render `tasks.md` from a canonical read model through a one-way
projection boundary. Include source generation/digest and an explicit
non-authoritative warning. Write atomically; never implement a production
Markdown reader.

**Rationale**: This preserves human familiarity and migration utility while
eliminating dual maintenance and checkbox authority.

**Alternatives considered**:

- Remove tasks rendering immediately: rejected because compatibility has user
  value and is cheap when one-way.
- Bidirectional synchronization: rejected because conflicts recreate the
  original problem.
- Parse only checkboxes: rejected because even a small authoritative subset is
  a second work state machine.

## Decision 10 — Keep SQLite for Phase 3

**Decision**: Add append-only migrations to the existing control-store adapter;
do not adopt Gapdb in this mission.

**Rationale**: SQLite is the qualified transactional authority and already owns
WorkRevision, governance, execution, handoff, review, and integration records.
Gapdb passed its own MVP gates, but adoption requires an equivalent external
adapter comparison and signed human record. Mission-method semantics remain
behind ports so a later adoption does not change domain behavior.

**Alternatives considered**:

- Bundle Gapdb migration now: rejected as two high-risk changes with different
  evidence needs.
- Write directly to Gapdb for new tables only: rejected because split
  transactional authority would be worse than either store alone.

## Decision 11 — Extend, never weaken, the architecture guard

**Decision**: Classify the new mission/artifact/mission-type packages as
authority capabilities and add a read-only projection tier. Retain Beads,
Markdown, API/MCP/Team Kitty, and provider-direction prohibitions with new
mutant controls.

**Rationale**: Phase 2's enforceable boundary is a project advantage. New
packages should add one policy axis, not trigger deletion of the guard.

**Alternatives considered**:

- Add every new package to a flat allowlist: rejected because direction matters.
- Exempt the mission composition root: rejected because authority must remain
  reachable only through typed application calls.

## Operational follow-ups inherited from Phase 2

The following are upstream Spec Kitty workflow defects, not Phase 3 Go product
scope. They should be tracked independently and used as adversarial workflow
evidence:

1. merge consolidation can omit an accepted late work-package lineage;
2. retrospective summary can report a committed, consumable record as missing;
3. automatic post-merge dead-code review is Python-only;
4. generic acceptance assumes Python-style `src/` and `tests/` directories.

None is silently repaired inside the redesigned mission engine. Phase 3 tests
should avoid depending on those accidental host behaviors.
