---
work_package_id: WP05
title: WorkRevision v2 and Deterministic Finalization
dependencies:
- WP04
requirement_refs:
- FR-012
- FR-013
- FR-014
- FR-015
- FR-028
- NFR-002
- NFR-003
- NFR-004
- NFR-014
- C-001
- C-007
- C-009
- C-010
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T023
- T024
- T025
- T026
- T027
- T028
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/workgraph/
create_intent:
- internal/workgraph/revision_v2.go
- internal/workgraph/revision_v2_test.go
- internal/workgraph/finalization.go
- internal/workgraph/finalization_test.go
- internal/workgraph/lanes.go
- internal/workgraph/lanes_test.go
- internal/mission/finalization.go
- internal/mission/finalization_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/workgraph/revision.go
- internal/workgraph/revision_test.go
- internal/workgraph/revision_v2.go
- internal/workgraph/revision_v2_test.go
- internal/workgraph/finalization.go
- internal/workgraph/finalization_test.go
- internal/workgraph/lanes.go
- internal/workgraph/lanes_test.go
- internal/mission/finalization.go
- internal/mission/finalization_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 - WorkRevision v2 and Deterministic Finalization

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Extend the proven Phase 1 `WorkRevision` into a strictly versioned, immutable v2 executable
graph with complete mission, planning, provider, governance, criteria, write-scope, and lane
identity. Add an honest double-observation saga that permits exactly one current generation.

## Context

Phase 1 already provides valuable behavior in `internal/workgraph/revision.go`:
draft immutability after finalization, graph and lane-cycle refusal, canonical
ordering, mutation-sensitive digests, defensive copying, and persisted revision
verification. Preserve those laws and all historical v1 evidence. This package
adds the fields and application behavior required by Phase 3; it must not create
a second graph aggregate or silently reinterpret v1 bytes as v2.

WP04 supplies provider-neutral, validated work observations. WP05 consumes
those values, builds the executable lane graph, rechecks provider revisions,
and publishes locally through a narrow mission-owned port. WP08 will implement
the port transactionally in SQLite. WP06 owns semantic drift and successor
reconciliation; therefore ordinary finalization in this package may publish
generation 1 only when no current revision exists. It must not overwrite a
current generation or invent automatic reconciliation.

These rules are load-bearing:

- Schema v1 remains readable, byte-compatible, and immutable. Existing tests
  and call sites keep their behavior unless an explicit compatibility adapter
  is needed.
- Schema v2 is written for new Phase 3 finalizations and decoded strictly.
  Unknown, missing, zero, or future schema versions fail with stable kinds.
- Generation 1 has no predecessor. A later generation requires an exact
  predecessor and is published only by WP06 reconciliation.
- The executable graph digest reflects semantic execution content, not input
  order or opaque provider revision noise. Provider observations remain pinned
  and canonically encoded as separate audit evidence.
- All work, criteria, dependency, write-scope, observation, and lane collections
  are canonical, unique, complete, and defensively copied.
- Write-scope overlap collapses work into deterministic lanes before publication;
  the resulting lane dependency graph must be acyclic.
- The provider and control store do not share a transaction. Finalization is a
  bounded observation saga with an explicit revision recheck, not two-phase
  commit or an impossible cross-store atomicity claim.
- Provider status is never readiness or mission authority. Only normalized
  work content and the local expected-current-generation publication decide
  whether a revision becomes current.
- SQLite remains the qualified adapter; do not add Gapdb, SQL, schema migration,
  helper CLI, projection, execution, workspace, review, or integration code.

## Branch Strategy

Use only the lane worktree assigned for WP05 after WP04 is accepted. Its
dependency base contains the provider-neutral observation and work-quality
contracts, and its merge target is `feat/software-development-mission-method`.
Do not branch from `main`, manually create a worktree, reuse a stale lane, or
edit WP04/WP06/WP08-owned files. Changes to existing `revision.go` and
`revision_test.go` must be the minimum compatible extension needed for version
dispatch and shared behavior; the new v2 implementation belongs in the planned
new files.

Start implementation with:

```bash
spec-kitty agent action implement WP05 --agent codex
```

Before coding, read `spec.md`, `plan.md`, `data-model.md`, `research.md`,
`contracts/work-revision-v2.schema.json`, and the `work finalize` section of
`contracts/mission-method-commands.md`.

### Subtask T023: Preserve v1 compatibility and introduce strict version dispatch

**Purpose**: Add v2 without invalidating the evidence and consumers already
bound to the Phase 1 revision encoding.

**Steps**:

1. Characterize the current unversioned v1 envelope in `revision_test.go` with
   fixed golden bytes or stable fixtures before changing decoder behavior.
   Cover canonical graph, `sha256:`-qualified legacy digests, revision number,
   finalizer, timestamp, lanes, criteria, dependencies, and source revisions.
2. Keep `Revision.Encode`, `DecodeRevision`, v1 constructors/accessors, digest
   verification, defensive copies, and finalized-draft refusal compatible.
   Do not rewrite stored v1 records into v2 during decode or normal reads.
3. Add an explicit version-dispatch seam. Legacy envelopes that match the exact
   characterized v1 shape route to the v1 decoder; v2 envelopes require
   `schema_version: 2`. Do not treat malformed or unknown JSON as legacy merely
   because its version is absent.
4. Decode both versions strictly at their authority boundary: reject unknown
   top-level fields, duplicate keys where the chosen decoder can detect them,
   trailing values, malformed identities, and digest mismatch. Return stable
   `workgraph.ErrorKind` values with bounded fields.
5. Define a read-only common view only if consumers require one. Do not flatten
   away v2-only provenance or fabricate absent v1 fields. Callers must be able
   to determine which schema and guarantees they received.
6. Add fixtures proving v1 decode/encode remains deterministic after v2 is
   introduced and a future `schema_version` is refused rather than downgraded.

**Files**:

- `internal/workgraph/revision.go` — minimal legacy/version dispatch support.
- `internal/workgraph/revision_test.go` — frozen v1 compatibility and strict
  dispatch regression tests.
- `internal/workgraph/revision_v2.go` — v2 schema constants and decoder entry.

**Validation**:

- All pre-WP05 workgraph tests pass unchanged or with explicit compatibility
  assertions that preserve their original semantic law.
- Golden v1 evidence decodes to the same values and re-encodes byte-for-byte.
- Missing-version malformed input, schema 0, schema 3, unknown fields, trailing
  JSON, and mismatched legacy digests fail with exact diagnostic kinds.
- No migration, SQL, or write-on-read behavior appears in the domain package.

### Subtask T024: Model the complete immutable WorkRevision v2 identity

**Purpose**: Capture every fact a future run needs to identify its exact
executable contract without consulting mutable tracker state or private context.

**Steps**:

1. In `revision_v2.go`, define the schema-2 aggregate from the contract:
   WorkRevision, project, and mission IDs; generation and optional predecessor;
   planning source commit; mission-definition key/version/digest; dependency
   policy; work items; lanes; provider observations; graph and criteria digests;
   finalizer/time; and sealed governance-context identity.
2. Model the closed dependency policy `approved_or_done`. Validate the mission
   definition as explicit `software-development`, positive supported version,
   and 64-character lowercase digest. Validate the planning source as a 40–64
   character lowercase Git object ID without executing Git.
3. Extend work content with non-empty title and description, bounded optional
   design context, stable structured criteria (`criterion_id`, text,
   `verifier_kind`, required flag, bounded provider-neutral metadata), unique
   dependencies, and unique normalized write scopes.
4. Model provider observations with exactly one complete mapping per WorkID:
   provider, external ID, and opaque non-empty revision. Reject missing, extra,
   duplicate, cross-mission, or unknown-work observation entries.
5. Enforce generation law: generation 1 requires an empty predecessor; every
   later v2 generation requires an exact predecessor. Expose a construction
   policy that ordinary finalization can limit to generation 1 while WP06 can
   later construct a validated successor.
6. Keep values immutable through private fields, constructors/restorers, and
   defensive copying of slices, maps, metadata, and canonical bytes. Restored
   records must recompute and verify every claimed digest before use.
7. Emit JSON conforming exactly to `work-revision-v2.schema.json`, including
   bare lowercase 64-character v2 digests where the contract requires them.
   Do not leak legacy `sha256:` formatting into the v2 wire shape.

**Files**:

- `internal/workgraph/revision_v2.go` — complete v2 model, validation,
  immutable accessors, strict encoding/decoding, and version-aware view.
- `internal/workgraph/revision_v2_test.go` — field, generation, identity,
  observation coverage, strict schema, and immutability tests.

**Validation**:

- A full two-item generation-1 fixture validates and round-trips exactly.
- Removing or changing each required identity field produces a stable refusal.
- Generation/predecessor invalid combinations fail; a valid successor value is
  constructible for WP06 but cannot be published by ordinary finalization.
- Caller mutation of criteria metadata, dependencies, scopes, observations,
  lanes, or returned bytes cannot alter a finalized revision or its digests.

### Subtask T025: Canonicalize semantic graph and digest material deterministically

**Purpose**: Ensure equivalent provider observations produce identical
executable identity regardless of provider ordering or presentation details.

**Steps**:

1. Define explicit domain-separated canonical forms for the v2 executable graph
   and criteria set. Include all semantic execution fields: IDs, title,
   description, design context, structured criteria, dependencies, normalized
   write scopes, dependency policy, mission/planning identity as required by
   the contract, and the post-collapse lane DAG.
2. Sort work by WorkID; criteria by criterion ID; dependencies and write scopes
   lexically; lanes by lane ID; lane membership and dependency lanes by ID; and
   provider observations by stable provider/mapping keys. Normalize nil and
   empty collections to one encoding.
3. Canonicalize bounded criterion metadata recursively with deterministic key
   order and a closed JSON-compatible scalar/container vocabulary. Reject
   unsupported numeric ambiguity, non-finite values, duplicate semantic keys,
   or unbounded data rather than relying on incidental `encoding/json` output.
4. Compute a graph digest over semantic executable content and a criteria digest
   over the complete canonical criteria view. Provider opaque revision changes
   alone must not alter the semantic graph digest, but observations remain in
   the canonical v2 envelope for audit and later observation-only drift.
5. Ensure changes to any semantic field—including description, required flag,
   verifier kind, metadata, dependency, scope, mapping semantics, dependency
   policy, or lane edge—change the appropriate digest. Finalization actor and
   time are provenance, not semantic graph content.
6. Recompute canonical forms and both digests during restoration. Never trust
   caller- or store-supplied commitments without verification.

**Files**:

- `internal/workgraph/revision_v2.go` — canonical material and domain-separated
  digest implementation.
- `internal/workgraph/revision_v2_test.go` — permutation, nil/empty, mutation,
  metadata, observation-only, and repeated determinism tests.

**Validation**:

- One hundred random permutations of the same 100-item semantic graph produce
  byte-equivalent canonical content and exactly one graph/criteria digest.
- Every semantic field has a mutation-sensitivity case; opaque provider revision
  noise preserves semantic digests while changing pinned observation evidence.
- Encode/decode/re-encode is byte-equivalent and detects tampered digests.
- Digests conform to the schema and never depend on map iteration, local time,
  filesystem order, provider presentation order, or process identity.

### Subtask T026: Collapse overlapping write scopes into a validated lane DAG

**Purpose**: Convert work-level concurrency constraints into deterministic
execution lanes and reject graphs that become cyclic only after scope collapse.

**Steps**:

1. In `internal/workgraph/lanes.go`, consume the normalized write scopes and
   dependency graph produced by WP04. Define one conservative, provider-neutral
   overlap relation for repository-relative scopes; invalid, absolute, or
   semantically ambiguous patterns must already be rejected or fail closed.
2. Build connected components of overlapping work scopes. Each component is one
   lane; singleton non-overlapping work remains independently schedulable.
   Component construction must be independent of input order and transitive
   (`A` overlaps `B`, `B` overlaps `C` means one lane).
3. Derive stable lane IDs from canonical sorted WorkIDs or another documented
   content-stable rule. Do not use timestamps, map iteration, provider IDs,
   tracker ordering, random UUIDs, or process-local counters.
4. Project every inter-work dependency onto lane edges, remove self-edges caused
   by collapse, deduplicate edges, and ensure every WorkID occurs in exactly one
   lane. Preserve dependency direction from blocked work to prerequisite lane.
5. Run work-graph cycle validation before collapse and lane-DAG cycle validation
   after collapse. Return a distinct stable `lane_projection_cycle` finding
   identifying bounded implicated lane/work IDs when an acyclic work graph
   becomes cyclic after collapse.
6. Keep v1 lane lineage behavior intact. Use separate v2 lane values or explicit
   conversion so the new `work_ids`/`dependency_lane_ids` contract does not
   silently change the meaning of legacy `Lane.ParentID`.

**Files**:

- `internal/workgraph/lanes.go` — deterministic overlap components, lane IDs,
  dependency projection, coverage, and cycle validation.
- `internal/workgraph/lanes_test.go` — transitive overlap, permutation,
  singleton, deduplication, missing coverage, and collapse-cycle cases.

**Validation**:

- The same scopes and dependencies yield byte-equivalent lanes across at least
  100 input permutations.
- Every item is covered exactly once; no lane depends on itself; edges are
  unique; and lane IDs remain stable for identical component membership.
- A deliberately acyclic work graph whose collapsed lane graph cycles fails
  before any publish call, while valid independent work retains concurrency.
- Tests distinguish invalid work cycles, invalid scopes, and post-collapse lane
  cycles by stable error kind.

### Subtask T027: Implement the honest double-observation finalization saga

**Purpose**: Publish one authoritative generation from a coherent tracker
observation without claiming atomicity across the tracker and control store.

**Steps**:

1. In `internal/mission/finalization.go`, define a narrow provider observation
   dependency using WP04's provider-neutral contracts and a narrow local
   publication port using v2 domain values. Do not depend on Beads, fake-provider,
   SQLite, helper-command, or filesystem types.
2. Define the finalize command with mission/project, proposed WorkRevision ID,
   expected absence/current generation, exact approved planning source commit,
   mission-definition commitment, actor, sealed governance context, operation
   identity, and deterministic clock. Compute a canonical request digest for
   idempotent replay.
3. First observe the complete mapped work set. Normalize it, run WP04 quality,
   coverage, and graph validation, build/collapse lanes, construct v2 canonical
   content, and verify all exact observation mappings are present.
4. Re-observe or use the provider's bounded revision-recheck operation for every
   mapped item immediately before local publication. Compare exact provider,
   external ID, and revision tuples. Any added, removed, rebound, missing, or
   changed observation refuses with `provider_changed_during_observation` and a
   retry-safe diagnostic; do not publish a partial or best-effort graph.
5. After a successful recheck, call one local compare-and-swap publication
   operation that verifies no current revision exists, records the operation
   digest, publishes generation 1, and advances mission state atomically. WP08
   supplies the durable transaction; tests use recording collaborators.
6. Define replay/conflict results precisely. Same operation and same request
   returns the committed generation even after an ambiguous response; operation
   reuse with different input conflicts. Concurrent different operations race
   on expected current generation so exactly one wins and all others return a
   bounded current-generation conflict without partial publication.
7. A provider change after the last recheck is later drift, not proof that the
   finalized observation was transactional. Preserve observation evidence and
   document this boundary in types/comments without weakening refusal for a
   change detected during the bounded saga.
8. Reject any request for generation greater than 1 or non-empty predecessor in
   this ordinary service. WP06 alone publishes successor generations through
   explicit reconciliation.

**Files**:

- `internal/workgraph/finalization.go` — pure finalization input assembly,
  observation comparison, request digest, and typed refusal values.
- `internal/workgraph/finalization_test.go` — observation recheck and canonical
  candidate tests.
- `internal/mission/finalization.go` — application saga, ports, generation-1
  guard, replay, and local CAS publication.
- `internal/mission/finalization_test.go` — recording provider/store scenario,
  boundary fault, replay, and conflict tests.

**Validation**:

- Unchanged first/second observations publish exactly the first normalized,
  fully validated generation once.
- Changes to content, external mapping, revision, membership, or metadata schema
  between observations refuse before local publication.
- Provider failure at either observation leaves local state unchanged and
  returns stable retry safety without embedding raw provider output.
- Failures before local commit expose the prior state; replay after a committed
  ambiguous result returns the original complete revision.

### Subtask T028: Prove determinism, replay, and single-winner concurrency contracts

**Purpose**: Lock the v2 compatibility and publication laws before WP08 supplies
durable persistence and WP12 runs full two-process campaigns.

**Steps**:

1. Build a shared two-item fixture with one dependency, non-overlapping scopes,
   structured required criteria, complete provider mappings, approved planning
   source, mission-definition commitment, and sealed governance context.
2. Run finalization 100 times with shuffled work, criteria, dependency, scope,
   metadata-key, lane, and observation ordering. Assert byte-equivalent
   canonical v2 content and exactly one graph/criteria digest pair.
3. Exercise same-operation replay before and after each service boundary using
   deterministic recording collaborators. Assert no duplicate generation,
   event intent, or current-pointer change is requested by replay.
4. Race at least 100 concurrent finalize calls against a concurrency-safe fake
   CAS repository. Exactly one current generation is published; same-request
   replays converge on it and distinct losing operations return conflict. Run
   these contract tests under the race detector.
5. Add deletion-style controls for provider recheck, complete observation
   coverage, semantic validation, lane-cycle validation, generation-1 guard,
   request-digest conflict, and expected-current CAS. Each removed guard must
   make a named test fail.
6. Test crash-equivalent fault points around candidate construction, second
   observation, pre-publication, committed publication with lost response, and
   replay. Durable SQLite restart injection remains WP08/WP12 scope.
7. Run legacy and v2 suites together to prove no v1 regression, then record
   T023–T028 done only after every command below passes.

**Files**:

- `internal/workgraph/revision_test.go` — legacy compatibility assertions.
- `internal/workgraph/revision_v2_test.go` — v2 schema and 100-run determinism.
- `internal/workgraph/finalization_test.go` — recheck and guard mutants.
- `internal/workgraph/lanes_test.go` — deterministic/cyclic lane contracts.
- `internal/mission/finalization_test.go` — replay, fault, and concurrent CAS
  contract campaign.

**Validation**:

```bash
go test ./internal/workgraph ./internal/mission
go test -race ./internal/workgraph ./internal/mission
go test ./internal/...
go vet ./internal/workgraph ./internal/mission
gofmt -d internal/workgraph internal/mission
```

- The 100-run normalization campaign yields one byte sequence and digest pair.
- The 100-race service contract yields one current generation and no partial or
  duplicate publication.
- Every provider-change fixture refuses before the local CAS collaborator is
  called, and every mutation control catches its removed guard.
- All legacy Phase 1 workgraph tests remain green with historical bytes intact.

## Definition of Done

- Legacy WorkRevision v1 records remain strictly readable, digest-verified,
  immutable, and byte-compatible; unsupported versions fail closed.
- WorkRevision v2 conforms to the published schema and pins complete mission,
  planning, executable graph, provider observation, lane, finalizer, and
  governance identity.
- Generation/predecessor law is enforced, and ordinary finalization can publish
  only generation 1 into an empty current-generation slot.
- Semantic graph and criteria digests are deterministic across provider/input
  ordering and sensitive to every executable field, while opaque observation
  revision noise remains distinct audit evidence.
- Write-scope collapse is deterministic, covers every work item exactly once,
  preserves safe concurrency, and rejects post-collapse lane cycles before
  publication.
- Finalization performs complete observation, validation, exact revision
  recheck, and one local CAS publication without claiming cross-store atomicity.
- Replay, conflicting operation reuse, provider failure, ambiguous committed
  response, and concurrent finalizers have stable typed outcomes.
- No provider-specific, SQL/SQLite, Gapdb, Markdown, filesystem, transport,
  worktree, review, or integration semantics enter the owned production files.
- Targeted unit, race, repository compile, vet, and formatting gates pass.
- Record event-sourced completion evidence after each subtask passes:

```bash
spec-kitty agent tasks mark-status T023 --status done
spec-kitty agent tasks mark-status T024 --status done
spec-kitty agent tasks mark-status T025 --status done
spec-kitty agent tasks mark-status T026 --status done
spec-kitty agent tasks mark-status T027 --status done
spec-kitty agent tasks mark-status T028 --status done
```

## Risks

- **v1 evidence corruption**: Adding a schema field or changing legacy digest
  framing can invalidate accepted history. Freeze v1 fixtures first and keep
  version-specific encoders and digest formats explicit.
- **Two graph authorities**: A parallel mission graph would diverge from Phase
  1 execution evidence. Extend the existing aggregate and expose one clear
  version-aware boundary.
- **False cross-store safety**: The provider can change after any read. Use a
  bounded exact recheck, pin what was observed, and classify later change as
  drift; never claim a provider/control-store transaction.
- **Observation noise in semantic identity**: Opaque provider revisions may
  change without executable meaning. Keep semantic digests distinct from pinned
  observation evidence and test both behaviors.
- **Nondeterministic metadata or lanes**: Go map iteration and random lane IDs
  can make equivalent graphs disagree. Canonicalize recursively and derive lane
  identity from sorted stable work identity.
- **Lost concurrency after collapse**: Over-conservative global lanes serialize
  all work. Collapse only connected overlaps and test independent singleton
  lanes alongside safety cases.
- **Cycle check ordered too late**: Publishing before lane validation creates an
  unusable current generation. Candidate construction must complete all work and
  lane checks before the local publication port is invoked.
- **Replay confused with retry**: A lost successful response can prompt another
  write. Bind operation identity to canonical request digest and require the
  publication port to return the original result on exact replay.

## Reviewer Guidance

Begin review with the v1 golden fixtures. Reject any implementation that
rewrites legacy bytes, silently upgrades on read, accepts a future schema as
legacy, or changes existing Phase 1 digest semantics. Then compare the v2 wire
shape field-by-field with `work-revision-v2.schema.json` and verify every stored
digest is recomputed during restoration.

Trace the finalization sequence explicitly: first provider observation,
provider-neutral validation, canonical lane construction, candidate creation,
exact second observation/recheck, then one local expected-current CAS. No local
publish call may occur before all checks pass. Confirm the code and comments are
honest that a mutation after recheck is later drift rather than an impossibly
fenced provider write.

Pay special attention to collection aliasing, metadata canonicalization,
nil/empty equivalence, dependency direction, transitive scope overlap, stable
lane identity, post-collapse cycles, observation coverage, and generation-1
publication limits. Run race tests and inspect the single-winner collaborator:
the contract must require atomic CAS from WP08 rather than accidentally passing
because the service serializes calls in-process.

Finally, verify authority separation. Tracker status must not influence
readiness, Beads must not leak through WP04's provider-neutral values, and this
package must not allocate runs/workspaces or perform reconciliation. The output
is one immutable executable generation and auditable saga evidence—nothing
more.

## Activity Log

- 2026-08-28T04:11:17Z – codex – Human Arbiter decision: blanket end-to-end authority confirmed after three review cycles. Perform one surgical remediation for complete ExpectedMission request binding and consistent blocker/decision canonical-set identity; require independent verification before approval.
