---
work_package_id: WP06
title: Artifact and WorkRevision Authority
dependencies:
- WP04
requirement_refs:
- FR-008
- FR-009
- FR-010
- FR-023
- FR-024
- FR-025
- FR-033
- FR-036
- NFR-001
- NFR-004
- NFR-005
- NFR-016
- C-004
- C-005
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T028
- T029
- T030
- T031
- T032
- T033
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/artifact/
create_intent:
- internal/controlstore/gapdb/artifact_authority.go
- internal/controlstore/gapdb/artifact_authority_test.go
- internal/controlstore/gapdb/workgraph_authority.go
- internal/controlstore/gapdb/workgraph_authority_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/artifact/**
- internal/workgraph/**
- internal/controlstore/gapdb/artifact_authority.go
- internal/controlstore/gapdb/artifact_authority_test.go
- internal/controlstore/gapdb/workgraph_authority.go
- internal/controlstore/gapdb/workgraph_authority_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 - Artifact and WorkRevision Authority

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the
frontmatter, and behave according to its guidance before parsing the rest of
this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If profile resolution fails, run `spec-kitty agent profile list`, resolve the
closest implementation profile, and record that governed profile invocation
before editing production code.

## Objective

Make artifact revisions and immutable WorkRevision generations authoritative
through consumer-owned, provider-neutral ports backed only by Authority Journal
v2 and GapDB. Preserve exact-revision validation and approval, publish complete
pinned work graphs, make every lineage disposition explicit, and provide bounded
eligibility guards that refuse stale-generation work atomically.

Implementation command:

```bash
spec-kitty agent action implement WP06 --agent codex
```

## Branch, dependency, and ownership rules

- Start from the accepted WP04 revision assigned to WP06 in
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- The planning and merge target is `feat/gapdb-only-production-authority`.
- Use only the Spec Kitty lane worktree; do not create a worktree manually.
- Edit only `internal/artifact/**`, `internal/workgraph/**`,
  `internal/controlstore/gapdb/artifact_*.go`, and
  `internal/controlstore/gapdb/workgraph_*.go`.
- Preserve other agents' concurrent edits and never revert unrelated changes.
- Consume WP02 journal semantics, WP03 GapDB publication contracts, and WP04
  checkpoint/restore contracts without modifying or bypassing them.
- Do not add a generalized store, raw-key API, provider-shaped repository, or a
  second playback engine.
- Keep SQLite, SQL vocabulary, filesystem state, Git state, and server-local
  process memory outside the production authority path.

Before changing code, read `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`contracts/authority-journal-v2.md`,
`contracts/event-family-registry-v2.json`, and all WP04 public contracts. Treat
the frozen behavior inventory as required semantic evidence, not as a reason to
retain obsolete table-shaped APIs.

## Product laws carried by this WP

1. Artifact catalog identity and immutable artifact revision identity are
   distinct aggregates with distinct event history.
2. Artifact bytes are committed by a canonical content digest; source path,
   source revision, provider observation, or mutable file is provenance only.
3. Validation and approval always name one exact immutable artifact revision.
4. Publishing a later revision never rewrites, reinterprets, or transfers a
   verdict recorded against an earlier revision.
5. A finalized WorkRevision is immutable and contains the complete executable
   graph, not references to mutable planning or tracker state.
6. Continue, supersede, invalidate, and fork are explicit lineage transitions;
   none mutates a frozen generation in place.
7. Eligibility is complete, generation-specific authority. Missing or legacy
   eligibility refuses; neither is inferred from currentness alone.
8. Every stale-generation check that guards a mutation must be a provider
   condition in the same AtomicBatch as that mutation.
9. Eligibility sharding is fixed and deterministic, so unrelated work can
   progress without a mission-global hot key or an unbounded publication.
### Subtask T028 - Port artifact catalog and revision authority

#### Purpose

Define the artifact-owned v2 domain model, event contracts, reducers, and
consumer-owned persistence ports for catalogs, revisions, validation evidence,
content commitments, and provenance.

#### Steps

1. Preserve the public distinction between an artifact catalog coordinate and
   an immutable `ArtifactRevisionID`. A catalog identifies mission and artifact
   kind; a revision identifies exactly one published content commitment.
2. Define versioned artifact catalog and artifact revision events under
   `internal/artifact`. Register only the families assigned to that package by
   `event-family-registry-v2.json`.
3. Keep event envelopes provider-neutral. Event payloads may contain domain
   identities, content digest, source reference, source revision, actor,
   timestamp, validation evidence digest, and provenance, but never GapDB keys,
   provider revisions, SDK types, SQL rows, or mutable file handles.
4. Commit content with the existing domain-separated digest algorithm. Ensure
   equivalent bytes produce the same digest and byte changes cannot alias.
5. Carry enough provenance to explain who published which bytes from which
   attributable input without elevating the source locator to authority.
6. Make validation evidence immutable, digest-addressed, and bound to the exact
   revision it examined. Preserve detailed findings in bounded domain values.
7. Replace persistence-shaped repository calls with narrow ports expressed in
   artifact command and query language. Do not expose raw streams, key strings,
   generic `Get`/`Put`, or a cross-capability transaction object.
8. Implement pure, deterministic reducers and restore constructors that apply
   the same invariant checks as live commands without performing I/O.
9. Preserve stable exported artifact contracts where compatible; if a contract
   must change, supply an explicit successor and migrate all in-scope callers.

#### Files

- `internal/artifact/**`

#### Validation

- Round-trip every artifact event version through its canonical codec.
- Prove source paths or provider observations cannot replace the content digest.
- Prove unknown versions, corrupt commitments, invalid provenance, and event
  order violations fail closed with typed bounded errors.
- Prove live command reduction and restored reduction produce identical state.

### Subtask T029 - Enforce latest-revision CAS and exact validation/approval

#### Purpose

Publish revisions with catalog-head compare-and-swap and make validation and
approval exact-revision decisions whose meaning survives later publication.

#### Steps

1. Require publication commands to state the expected latest revision identity;
   an empty expectation means the catalog must not yet have a revision.
2. Validate artifact bytes, identity, provenance, and canonical operation
   binding before sealing the publication plan.
3. Atomically append the immutable revision event, advance the catalog head,
   advance affected aggregate heads, and complete OperationState in one batch.
4. Condition the batch on the expected catalog head and all relevant aggregate
   heads. Never implement publication as read-current followed by unrelated
   writes.
5. Make validation and approval commands name an exact `ArtifactRevisionID` and
   verify that revision's immutable digest and evidence, not merely artifact
   kind or whatever catalog head is current at response time.
6. Preserve the exact qualified approval rule: approval is admitted only with
   the required successful validation for that same revision and governance
   context. Contradictory verdicts must refuse deterministically.
7. Record actor, verdict, evidence digest, governance context, and decision time
   in immutable authority events.
8. Ensure current-revision views compose catalog state with exact-revision
   validation and approval without transferring old verdicts to new content.
9. Return the committed outcome on same-OperationID/same-binding retry; return a
   typed idempotency conflict on same OperationID with a changed binding.
10. Preserve refusal precedence for malformed requests, operation conflicts,
    missing revisions, stale catalog expectations, invalid evidence, and policy
    refusal as established by the frozen inventory.

#### Files

- `internal/artifact/**`

#### Validation

- Race two publishers from one catalog head and prove exactly one wins.
- Publish revision B after approving revision A and prove A's verdict remains
  attributable to A while B remains unapproved until independently decided.
- Retry admitted publication and approval across restart and prove no duplicate
  revision, verdict, domain event, or logical-version increment is created.
- Inject failure at each batch boundary and prove no partial catalog, revision,
  validation, approval, head, or operation state becomes visible.

### Subtask T030 - Finalize immutable complete WorkRevisions

#### Purpose

Make finalization publish a complete, content-addressed WorkRevision generation
whose execution meaning never depends on mutable tracker, artifact, or planning
state.

#### Steps

1. Finalize one immutable WorkRevision containing mission and project identity,
   generation identity, all WorkIDs, descriptions, dependencies, acceptance
   criteria, write scopes, lane assignments, and required observations.
2. Pin artifact observations by exact artifact revision identity and content
   digest. Pin provider observations by attributable external identity and
   observation metadata while keeping them non-authoritative.
3. Validate uniqueness, dependency closure, acyclicity, scope normalization,
   acceptance-criteria completeness, lane consistency, and observation
   completeness before publication.
4. Canonically order maps, sets, graph edges, scopes, and observations before
   computing content, graph, and acceptance-criteria digests.
5. Derive identities and digests from documented domain-separated encodings;
   never use map iteration order, JSON incidental order, or provider revision.
6. Publish finalization as a versioned workgraph event and immutable generation
   record using one sealed CommitPlan.
7. Advance only the explicitly modeled workgraph heads and OperationState in the
   same AtomicBatch. Do not silently make the generation executable through a
   projection write.
8. Make reducers reject incomplete graphs, dangling dependencies, cycles,
   digest mismatch, unknown event versions, and mutation of prior generations.
9. Preserve the full WorkRevision across checkpoint and suffix replay with
   deterministic byte-equivalent domain state.

#### Files

- `internal/workgraph/**`

#### Validation

- Finalize the same canonical graph from different input orders and prove equal
  identities and digests.
- Change one description, criterion, dependency, scope, or pinned observation
  and prove the relevant commitment changes.
- Prove a finalized generation remains unchanged when tracker data, artifact
  currentness, or later planning inputs change.
- Prove malformed and incomplete graphs refuse before provider publication.

### Subtask T031 - Implement explicit reconciliation lineage

#### Purpose

Represent changes after finalization as explicit immutable lineage transitions
with exact continue, supersede, invalidate, and fork consequences.

#### Steps

1. Record reconciliation identity, old and successor revision identities,
   actor, rationale, drift evidence, affected WorkIDs, disposition, and time in
   versioned workgraph events.
2. Implement `continue` so the named older generation remains executable only
   under the recorded rationale and the qualified per-work policy.
3. Implement `supersede` so the successor becomes current and older work loses
   later-gate eligibility except for explicitly permitted already-running
   consequences from the frozen behavior contract.
4. Implement `invalidate` so affected old-generation work immediately loses
   eligibility for subsequent guarded operations.
5. Implement `fork` as two explicit named generation branches with separate
   scope and lineage; never merge them into an ambiguous current pointer.
6. Preserve run, handoff, integration, expiry, quarantine, and history
   consequences exactly. Do not reduce reconciliation to a boolean current flag.
7. Never edit or replace a frozen WorkRevision. A reconciliation adds authority
   and changes eligible lineage through events.
8. Reject missing successor generations, cross-mission lineage, cycles,
   conflicting prior dispositions, incomplete affected-work coverage, and
   invalid disposition/state combinations.
9. Bind retries to the full lineage command. Same binding replays the result;
   changed disposition, rationale, successor, or affected set conflicts.

#### Files

- `internal/workgraph/**`

#### Validation

- Exercise all four dispositions at every relevant work status and operation.
- Assert the exact qualified consequences for running and not-yet-started work.
- Prove historical generations and lineage remain queryable and immutable.
- Prove cross-mission, cyclic, ambiguous, and incomplete lineage fails closed.

### Subtask T032 - Publish bounded eligibility guard shards

#### Purpose

Provide deterministic bounded generation-eligibility authority that downstream
commands can condition atomically without a global mission hot key.

#### Steps

1. Use the fixed shard-count contract established by the mission contracts; do
   not choose a dynamic shard count at runtime or encode deployment topology.
2. Derive a WorkID's shard deterministically from mission identity and WorkID
   using the canonical key/hash rule shared with the GapDB adapter.
3. Store complete eligibility facts naming mission, WorkID, WorkRevision,
   reconciliation lineage, status/operation scope, and semantic permission.
4. Derive permission from domain disposition law. Never accept caller-supplied
   `permitted`, infer eligibility from currentness, or admit `legacy-v1` facts.
5. Update only affected shard facts when publishing reconciliation. Group facts
   by fixed shard so the batch has a bounded number of mutations rather than one
   mutation per work item.
6. Publish reconciliation events, lineage heads, affected guard shards, and
   OperationState atomically. A budget refusal must occur before provider I/O;
   chunking must never expose partially reconciled eligibility.
7. Expose a provider-neutral guard token/condition through the workgraph-owned
   port so downstream capability commands can CAS the relevant shard in their
   own AtomicBatch.
8. Require every guarded mutation to condition both its own aggregate head and
   the eligibility shard observed for the exact WorkRevision generation.
9. Treat guard provider revision as concurrency metadata only. Do not emit a
   domain event or advance a domain logical version when semantics did not
   change.
10. Ensure unrelated shards can advance independently while same-shard races
    fail cleanly and retry only through a new read/plan cycle.

#### Files

- `internal/workgraph/**`

#### Validation

- Prove every WorkID maps to exactly one stable shard across restart.
- Prove missing, incomplete, stale-generation, mismatched-lineage, and legacy
  facts refuse with typed bounded errors.
- Race reconciliation against a guarded work mutation and prove the stale
  generation cannot commit.
- Race unrelated work on different shards and prove no mission-global key
  serializes them.
- Use a large affected-work fixture to prove the mutation count stays within the
  declared batch budget and no partial visibility occurs.

### Subtask T033 - Add GapDB adapters and adversarial qualification

#### Purpose

Translate artifact and workgraph consumer-owned ports onto the shared GapDB v2
committer and restore path, then prove parity and atomicity under contention,
restart, ambiguity, faults, and targeted mutants.

#### Steps

1. Implement artifact adapters only in
   `internal/controlstore/gapdb/artifact_*.go` and workgraph adapters only in
   `internal/controlstore/gapdb/workgraph_*.go`.
2. Reuse WP03 canonical keys, codecs, sealed CommitPlan committer, conditions,
   publication identity, and batch budgets. Do not call the provider SDK from
   artifact or workgraph packages.
3. Reuse WP04 checkpoint, suffix restore, cursor, and pure playback surfaces.
   Do not restore from process-local caches or add family-specific scan loops.
4. Translate provider missing-key, conflict, budget, corruption, and ambiguity
   outcomes into the domain port's stable typed result without leaking SDK
   types or raw keys.
5. Cover independent-aggregate concurrency, same-head contention, same-shard
   contention, and unrelated-shard progress.
6. Cover crash before publication, crash after commit before response, lost
   response, replay after restart, corrupt checkpoint, suffix gap, duplicate
   event, and unknown event version.
7. Compare v2 behavior with the frozen Phase 1–3 artifact and workgraph inventory
   for accepted outcomes, refusals, ordering, identities, and observability.
8. Add fault tests proving every semantic publication has exact expected batch
   membership: operation transition, domain events, heads, indexes, and guard
   shards are all present or all absent.
9. Add or retain mutants that must fail when catalog CAS is removed, approvals
   bind current kind instead of exact revision, WorkRevision content is omitted,
   a frozen generation mutates, lineage collapses to current/not-current,
   legacy eligibility passes, a global hot key is introduced, a guard is read
   then written separately, or partial shard updates become visible.
10. Add boundary mutants that must fail if generic store methods, raw keys,
    GapDB SDK types, SQLite/SQL symbols, provider revisions as domain versions,
    or generic state-updated events cross into capability packages.

#### Files

- `internal/artifact/**`
- `internal/workgraph/**`
- `internal/controlstore/gapdb/artifact_*.go`
- `internal/controlstore/gapdb/workgraph_*.go`

#### Validation

Run focused tests while iterating, then the bounded package and architecture
suite appropriate to this WP:

```bash
go test ./internal/artifact ./internal/workgraph ./internal/controlstore/gapdb
go test -race ./internal/artifact ./internal/workgraph ./internal/controlstore/gapdb
go test ./internal/architecturetests
go vet ./internal/artifact ./internal/workgraph ./internal/controlstore/gapdb
staticcheck ./internal/artifact/... ./internal/workgraph/... ./internal/controlstore/gapdb/...
git diff --check
```

If this repository's current staticcheck version does not accept `...` after a
package path, run the equivalent package list resolved by `go list`. Do not run
the known long scenario suite as a substitute for focused qualification.

## Definition of Done

- T028–T033 are implemented only within the declared owned files.
- Artifact catalog, immutable revision, validation, approval, WorkRevision,
  lineage, and eligibility facts restore from versioned admitted events.
- Artifact publication enforces latest-revision CAS and approval remains bound
  to exactly one immutable revision across later publication and restart.
- WorkRevision finalization commits a complete pinned graph with deterministic
  identities and digests; no mutable source is needed to execute its meaning.
- Continue, supersede, invalidate, and fork preserve the frozen consequences and
  produce explicit immutable lineage.
- Eligibility is complete, generation-specific, bounded by deterministic
  shards, and stale-generation mutations are refused atomically.
- Every semantic command seals one CommitPlan and produces one AtomicBatch with
  exact membership, operation binding, conditions, events, and heads.
- Exact replay, changed-binding conflict, contention, restart, ambiguity, fault,
  parity, budget, and mutant tests pass.
- Architecture checks prove consumer-owned ports and the absence of generic
  stores, provider leakage, SQL/SQLite authority, or reducer I/O.
- Focused tests, race tests, architecture tests, `go vet`, `staticcheck`, and
  `git diff --check` pass, or a tool-unavailability caveat is recorded with the
  exact command and output.
- The implementer records relevant commands and results in the WP handoff.
- Completion is recorded through the event log, not by editing frontmatter:

```bash
spec-kitty agent tasks mark-status T028 T029 T030 T031 T032 T033 \
  --status done \
  --mission gapdb-only-production-authority-01M1GWDJ
```

## Risks and mitigations

- **Verdict drift:** A current-catalog shortcut can accidentally approve new
  bytes. Bind every validation and approval event to exact revision identity and
  digest, and test publication after verdict.
- **Incomplete frozen graph:** References to live tracker or artifact state make
  a WorkRevision mutable in practice. Require and digest the full pinned graph.
- **Lineage collapse:** A single current flag loses continue and fork meaning.
  Persist explicit disposition, edges, affected works, and consequences.
- **Hot-key serialization:** One mission eligibility head blocks unrelated work.
  Use the fixed deterministic shard contract and test independent progress.
- **Partial invalidation:** Chunked eligibility writes can briefly authorize
  stale work. Keep one semantic reconciliation atomic and fail before I/O when
  its bounded shard plan exceeds provider limits.
- **Authority inversion:** Adapter conveniences can leak raw keys or provider
  revisions into domain APIs. Keep translations behind consumer-owned ports.
- **Replay divergence:** Live and restore paths can normalize differently. Share
  pure validation, codec, and reducer primitives and compare final states.
- **False idempotency:** Reusing an OperationID for changed content or lineage
  can hide conflict. Bind all semantic inputs, including digests and affected set.

## Reviewer guidance

Review this WP as an authority migration, not merely as repository replacement.
Require direct evidence for the following:

1. Artifact catalog and artifact revision are separate aggregates and events.
2. Content digest is authoritative while path, Git, tracker, and provider facts
   remain attributed observations.
3. Latest-revision publication CAS and exact-revision validation/approval are in
   the same atomic authority path as events, heads, and OperationState.
4. WorkRevision contains a complete deterministic graph and cannot observe
   mutable sources after finalization.
5. All four reconciliation dispositions retain their exact per-status and
   per-operation consequences without mutating frozen generations.
6. No missing or legacy eligibility can authorize work, and stale-generation
   refusal is a condition in the same batch as the guarded mutation.
7. Guard sharding is deterministic and bounded, updates affected shards only,
   and allows unrelated shards to advance independently.
8. GapDB adapters translate consumer-owned ports without exposing generic
   storage, raw keys, SDK types, or provider revisions as domain meaning.
9. Same-head races, same-shard races, restart, lost-response ambiguity, injected
   faults, budget edges, and all named mutants are exercised and fail correctly.
10. The final diff stays inside WP06 ownership and contains no SQLite/SQL or
    internal-server authority fallback.

Reject if any acceptance path relies on a file, tracker, process-local map,
projection, SQL/SQLite state, or provider metadata as production authority; if
approval floats to the latest artifact rather than an exact revision; if any
WorkRevision is incomplete or mutable; if lineage meaning is collapsed; or if a
stale-generation mutation can commit without atomically conditioning its exact
eligibility shard.
