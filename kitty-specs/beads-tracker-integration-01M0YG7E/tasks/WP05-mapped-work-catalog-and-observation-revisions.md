---
work_package_id: WP05
title: Mapped Work Catalog and Observation Revisions
dependencies:
- WP04
requirement_refs:
- C-007
- FR-007
- FR-008
- FR-009
- FR-013
- FR-016
- NFR-002
- NFR-006
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
subtasks:
- T021
- T022
- T023
- T024
- T025
- T026
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/metadata.go
- internal/tracker/beads/metadata_test.go
- internal/tracker/beads/revision.go
- internal/tracker/beads/revision_test.go
- internal/tracker/beads/catalog.go
- internal/tracker/beads/catalog_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/metadata.go
- internal/tracker/beads/metadata_test.go
- internal/tracker/beads/revision.go
- internal/tracker/beads/revision_test.go
- internal/tracker/beads/catalog.go
- internal/tracker/beads/catalog_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP05 - Mapped Work Catalog and Observation Revisions

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the first production-shaped Beads work catalog: stable Spec Kitty-to-Beads identity mappings, human-readable work briefs, structured acceptance criteria, deterministic observation revisions, and honest create/read/update recovery.

The catalog must make Beads useful as the primary work inventory without making Beads IDs authoritative inside Spec Kitty. It must preserve provider and human metadata, detect equal-timestamp content drift, recover ambiguous creates without duplicating issues, and refuse guarded updates that Beads v1.2.2 cannot enforce.

Implementation command:

```bash
spec-kitty agent action implement WP05 --agent codex
```

## Context

The product boundary is:

```text
Beads:       mutable work facts and visible coordination
Spec Kitty: stable WorkID, mission/run authority, pinned WorkRevision, acceptance
Git:         committed implementation facts
```

WP01 defines the provider-neutral catalog vocabulary and capability levels. WP03 supplies the bounded `bd --json` command protocol. WP04 supplies a validated, explicit binding to one canonical Beads workspace and the concurrency mode used for mutation. Use those accepted types and seams directly; do not create a parallel provider model, command runner, binding, diagnostic vocabulary, or identity type in this package.

Beads v1.2.2 is reached only through its documented schema-1 JSON envelope. It exposes provider timestamps but no arbitrary compare-and-swap update. Therefore:

- a provider observation revision is a content-derived drift token, not a mutation precondition;
- matching an expected revision before `bd update` does not make the subsequent update guarded;
- an ambiguous create is observed through mapping and operation metadata before any retry; and
- provider-visible metadata is merged, never replaced wholesale.

The provider-neutral fake remains the portability reference. Do not make a Beads-only field mandatory in the tracker contract and do not leak `bd` payload structs outside `internal/tracker/beads`.

## Branch Strategy

Work only in the WP05 lane worktree created by Spec Kitty. WP05 depends on WP04, so verify the lane base contains the accepted WP04 binding/inspection and transitive command-protocol work before editing. If those APIs differ from planning names, adapt this implementation to the accepted contracts rather than duplicating them.

The planning and merge target is `feat/beads-tracker-integration`. Do not edit the primary checkout, create worktrees manually, merge another active lane, push directly to `main`, or modify files outside the six exact owned paths. Other agents are working in the repository; preserve all unrelated changes.

## Required Invariants

Preserve these laws across every subtask:

1. `tracker.WorkID` remains the stable internal identity; a Beads issue ID is an external mapping only.
2. `(ProjectID, WorkID, provider)` and `(ProjectID, provider, ExternalID)` are each unique.
3. Rebinding never silently changes a `WorkID` or adopts a conflicting external issue.
4. Metadata updates own only the `spec_kitty` namespace and preserve every unrelated top-level key.
5. Unknown newer `spec_kitty.schema_version` values are read-only conflicts, never rewritten.
6. Structured criteria round-trip without using Markdown checkboxes as state.
7. Provider status, readiness, assignment, or checkbox state does not satisfy Spec Kitty acceptance.
8. Revisions cover canonical known content, not timestamps alone.
9. Revision equality means equal observation input under one encoding version; it does not mean the next provider write is conditional.
10. Create replay requires the same operation and semantically identical input.
11. Ambiguous mutation results are observed before retry and never classified from stderr prose.
12. A request for guarded update fails before mutation when the declared provider capability is unguarded.
13. Unknown optional provider response fields do not break known schema-1 behavior.
14. Normal diagnostics do not include descriptions, criterion text, or other potentially sensitive bodies.

### T021: Implement the versioned `spec_kitty` metadata codec

**Purpose**: Preserve stable identity and criteria in Beads without overwriting metadata owned by humans, Beads, or other tools.

**Implementation requirements**:

1. Define the adapter-owned schema-1 namespace using the accepted provider-neutral IDs and criterion values. It must cover at least:

   ```json
   {
     "schema_version": 1,
     "project_id": "project-id",
     "work_id": "work-id",
     "created_by_operation": "operation-id",
     "last_operation": "operation-id",
     "assignment_marker": "operation-id",
     "criteria": []
   }
   ```

2. Treat `assignment_marker` as optional for catalog-only records.
3. Decode the provider's metadata as a JSON object. Reject invalid JSON, a non-object root, or a non-object `spec_kitty` value with a typed provider-neutral diagnostic.
4. Validate required schema-1 fields, stable IDs, duplicate criterion IDs, empty required values, and the bounds defined by WP01.
5. Render each structured criterion with stable ID, text, verifier kind, required flag, and optional evidence hint.
6. Preserve criterion order when the provider-neutral contract declares it meaningful.
7. When creating metadata, add the namespace without removing or altering unrelated top-level keys.
8. When updating metadata, replace or merge only known owned fields inside `spec_kitty`; preserve unrelated top-level keys and unknown schema-1 namespace keys so a round trip is non-destructive.
9. Refuse a merge when an existing namespace belongs to another project, another `WorkID`, another creation operation for the same mapping, or a future schema the adapter does not support.
10. Return a conflict rather than silently repairing missing/conflicting mapping fields on an existing mapped issue.
11. Keep encoding deterministic: the same semantic metadata must produce the same canonical bytes regardless of input map iteration order.
12. Do not use raw string concatenation to build JSON and do not parse acceptance text back into criteria.

**Tests**:

- empty provider metadata receives one schema-1 namespace;
- unrelated nested top-level metadata survives byte-equivalent semantic round trips;
- unknown schema-1 namespace fields survive updates;
- project, work, and creation-operation conflicts are typed and non-mutating;
- unknown newer schema is refused without producing replacement bytes;
- malformed roots and duplicate criterion IDs fail deterministically;
- criteria with every verifier/required/evidence-hint combination round-trip;
- map order does not change encoded output.

**Files**: `metadata.go`, `metadata_test.go`.

### T022: Implement canonical content digests and observation revisions

**Purpose**: Detect provider drift even when Beads timestamps tie, while retaining the existing compact `tracker.Revision` surface.

**Implementation requirements**:

1. Define a versioned canonical observation input over every known provider-owned field relevant to catalog recovery:

   - external issue ID;
   - title, description, design context, and human acceptance rendering;
   - structured `spec_kitty` metadata and operation markers;
   - labels or other decoded catalog fields;
   - blocking dependency tuples when present in the observation;
   - visible assignee and assignment marker;
   - comments/markers when the requested observation includes them; and
   - provider update timestamp with explicit presence.

2. Begin canonical bytes with an encoding-schema version independent of the Beads JSON envelope version.
3. Encode scalar values with type/presence tags and length prefixes. Never use delimiter joining that makes `a|bc` collide structurally with `ab|c`.
4. Distinguish absent from present-but-empty values.
5. Normalize timestamps to a documented UTC representation; do not use local timezone formatting.
6. Sort map keys, labels, dependency tuples, and semantically unordered arrays by complete stable keys.
7. Preserve provider-significant order, including ordered criteria or comments where the accepted contract says order is meaningful.
8. Hash canonical bytes with SHA-256.
9. Derive `tracker.Revision` from the first eight digest bytes using one documented fixed byte order.
10. Reserve revision zero consistently. If the folded prefix is zero, apply one deterministic nonzero mapping and retain the full digest to disambiguate diagnostics.
11. Return or retain the full 32-byte digest alongside the compact revision for conflict evidence and collision diagnosis.
12. Never log canonical plaintext or sensitive field bodies merely to explain a digest mismatch.

**Tests**:

- golden canonical bytes/digests for a complete observation;
- equal semantic maps/sets in different input order yield identical digests;
- dependency and label permutations do not change the digest;
- provider-significant comment/criterion order changes the digest when appropriate;
- absent and empty values differ;
- every included scalar changing alone changes the full digest;
- identical timestamps with different content produce different revisions/full digests;
- timestamp normalization is timezone-independent;
- zero-prefix folding behavior is tested through a narrow helper without brute-forcing SHA-256;
- revision/full-digest output is stable across repeated runs.

**Files**: `revision.go`, `revision_test.go`.

### T023: Implement create mapping, replay, and ambiguous recovery

**Purpose**: Create Beads issues concurrently without losing Spec Kitty identity or duplicating work after an uncertain command result.

**Implementation requirements**:

1. Implement the accepted provider-neutral `Catalog.Create` port using WP03 typed commands and WP04 validated binding/mutation preflight.
2. Require stable `ProjectID`, `WorkID`, `OperationID`, binding, non-empty title, structured criteria, and supported metadata schema before invoking `bd`.
3. Render title/description/design context into provider-owned fields and criteria into:

   - a deterministic, readable acceptance field for humans; and
   - the structured `spec_kitty.criteria` metadata for machines.

4. Include mapping identity, creation operation, last operation, and metadata schema atomically in the create command's metadata argument.
5. Preserve any caller-supplied provider metadata outside `spec_kitty` through the T021 merge codec.
6. Allow Beads to assign its native hash-style external issue ID. Do not configure sequential IDs or synthesize an external ID from `WorkID`.
7. Before replaying a known operation, observe provider state through documented JSON commands and the namespaced mapping. Classify:

   - one exact mapping with the same operation and semantic input: replayed no-op returning the original `ExternalWorkRef`;
   - same operation with different semantic input: `invalid_request`;
   - same `WorkID` mapped to another external issue: mapping conflict;
   - one external issue claiming another `WorkID`: mapping conflict;
   - multiple matches: reconciliation conflict; and
   - unavailable/incomplete observation: ambiguity remains unresolved.

8. If create returns a valid success envelope, validate the returned issue and metadata before reporting confirmed success.
9. If the mutating command times out, is canceled, is truncated, or returns malformed success after start, classify it as ambiguous and perform bounded independent observation before any retry.
10. Retry only when the accepted ambiguity policy proves no issue was created and explicitly permits retry. Never turn “not found in a partial/unavailable list” into proof of absence.
11. A recovered matching issue must return `Replayed`/observed-applied semantics, its actual provider ID, and a fresh observation revision.
12. Keep command payload bodies out of routine errors and evidence.

**Tests**:

- ordinary create returns a stable mapping and revision;
- exact same-operation replay creates no second issue;
- changed-input replay is refused before mutation;
- apply-then-timeout is recovered by marker/mapping observation;
- timeout-before-apply remains retryable only after complete absence proof;
- multiple/mismatched mappings produce deterministic conflicts;
- unknown optional provider fields are ignored;
- native hash IDs remain distinct under concurrent creation.

**Files**: `catalog.go`, `catalog_test.go`.

### T024: Implement complete read translation

**Purpose**: Return a provider-neutral, self-consistent work observation without importing Beads semantics into callers.

**Implementation requirements**:

1. Implement `Catalog.Read` using the validated binding and typed `show` command.
2. Resolve by the supplied `ExternalWorkRef`; never silently search for or adopt a different issue when the external ID is missing.
3. Validate that provider metadata matches the requested project, stable `WorkID`, provider, external ID, schema, and creation operation.
4. Translate known provider fields into the accepted `WorkRecord`: title, description, design context, criteria, visible assignment fields, provider timestamp, and any graph/discussion fields already present in the decoded observation.
5. Derive criteria from structured metadata. The human acceptance rendering is a consistency/diagnostic surface, not a checkbox authority source.
6. Calculate the T022 compact revision and retain full diagnostic digest from one immutable decoded snapshot.
7. Update the returned `ExternalWorkRef.LastObservation` from that same snapshot.
8. Ignore unknown optional schema-1 provider fields.
9. Return typed `not_found`, malformed protocol, mapping conflict, unsupported metadata schema, or revision diagnostics without mutation.
10. Ensure reads never initialize, repair, remap, install hooks, or rewrite metadata.

**Tests**:

- a complete issue translates exactly;
- metadata criteria survive a create/read round trip;
- unknown provider fields do not affect known translation;
- missing/malformed/conflicting mappings fail without update commands;
- equal timestamps with changed title, criteria, or metadata are detected;
- repeated identical reads return identical revisions and full digests.

**Files**: `catalog.go`, `catalog_test.go`.

### T025: Implement honest update semantics and metadata preservation

**Purpose**: Support provider-owned work edits without advertising a safety guarantee the Beads CLI does not provide.

**Implementation requirements**:

1. Implement the accepted `Catalog.Update` port and branch on the declared catalog-update capability level, not the provider name.
2. If the request requires guarded/conditional mutation and the binding declares Beads v1.2.2 catalog updates unguarded, return `guarded_update_unsupported` before acquiring a writer lock or invoking any mutating command.
3. Never name or expose a read-compare-`bd update` sequence as compare-and-swap.
4. For an explicitly permitted unguarded update:

   - read the current complete observation;
   - validate the stable mapping and caller's policy/preconditions;
   - surface a changed expected observation as `revision_conflict` before mutation;
   - merge only the requested provider-owned fields;
   - merge the `spec_kitty` namespace while preserving all unrelated metadata;
   - set `last_operation` without changing `created_by_operation`; and
   - invoke one typed update command through the WP04 concurrency boundary.

5. Document in the returned mutation guarantee that the pre-read did not fence a direct human or external writer between observation and mutation.
6. Validate the success response with a fresh canonical observation before returning applied.
7. Handle ambiguous update outcomes by re-observing mapping, semantic target values, operation marker, and digest. Classify observed-applied, observed-unchanged, external conflict, unavailable, or indeterminate; never retry blindly.
8. Preserve human/provider metadata added between prior reads and the update input whenever the command contract permits a fresh merge.
9. Refuse future metadata schemas and mapping conflicts without mutation.
10. Do not mutate dependencies, comments, assignment, mission status, or acceptance verdict through the catalog update path unless the provider-neutral update request explicitly owns the corresponding approved catalog field.

**Tests**:

- guarded request refuses before writer acquisition/command execution;
- explicitly unguarded update reports its actual guarantee;
- stale expected observation refuses before update;
- unrelated top-level and unknown schema-1 namespace keys survive;
- creation operation is immutable and last operation advances;
- apply-then-timeout re-observes and classifies without duplicate update;
- human-changed target values become external conflict rather than overwrite/retry;
- no test asserts false provider CAS.

**Files**: `catalog.go`, `catalog_test.go`.

### T026: Qualify catalog correctness, latency, and concurrent identity

**Purpose**: Prove the catalog is usable at the target scale while respecting provider capability and topology limits.

**Implementation requirements**:

1. Build table-driven unit/contract tests around the accepted fake or dishonest command seam; do not require a locally installed `bd` for ordinary package tests.
2. Exercise validation, metadata preservation, create/read/update round trips, operation replay, ambiguous recovery, conflict typing, unknown optional fields, and digest determinism.
3. Make every mutating test assert the exact number and order of provider commands, especially that refusal paths execute zero mutation commands.
4. Run the 100-item read-latency scenario against a warm catalog:

   - seed exactly 100 work records outside the timed interval;
   - perform at least 200 single-item reads using a deterministic selection schedule;
   - report sample count, p50, p95, maximum, host/runtime context, and provider mode;
   - require p95 at or below 500 ms only in the designated qualification environment; and
   - avoid fragile per-call sleep-based assertions in ordinary unit CI.

5. Run the 1,000-create concurrent identity scenario when the declared topology supports concurrent writes:

   - use 1,000 distinct `WorkID` and `OperationID` values;
   - start multiple callers/process-equivalent workers through a bounded barrier;
   - assert 1,000 unique provider IDs and 1,000 one-to-one mappings;
   - read all mappings back and verify project/work/external identity;
   - retry a sample of operation IDs and prove no duplicate issues; and
   - run under the race detector for the in-process harness.

6. Make topology handling capability-aware:

   - provider-native server mode runs the full real-provider concurrency target;
   - adapter-serialized embedded mode may run concurrent callers through serialization but must report that it is not provider-native CAS;
   - unsupported concurrent-write topology yields an explicit non-applicable qualification record, never a false pass; and
   - the portable dishonest/fake seam still runs a deterministic 1,000-identity test without external Beads.

7. Keep large real-provider/latency qualification selectable so it can join the later scheduled campaign without slowing every focused unit loop.
8. Ensure skipping a real-provider case because no qualified binding exists does not skip portable replay, mapping, or revision tests.
9. Record timing and identity evidence without titles, descriptions, criteria text, raw metadata, or command payload bodies.
10. Run all focused tests repeatedly and under `-race` before marking the package complete.

**Files**: `metadata_test.go`, `revision_test.go`, `catalog_test.go`.

## Test Strategy

### Focused correctness loop

```bash
go test ./internal/tracker/beads -run 'Metadata|Revision|Catalog|Create|Read|Update|Replay|Ambiguous' -count=1
```

### Repetition and race safety

```bash
go test ./internal/tracker/beads -run 'Metadata|Revision|Catalog|Create|Read|Update|Replay|Ambiguous' -count=10
go test -race ./internal/tracker/beads -count=1
```

### Scale selectors

Use the test selectors implemented in this WP, adapting exact names to the final test functions:

```bash
go test ./internal/tracker/beads -run 'CatalogReadLatency100Items' -count=1 -v
go test ./internal/tracker/beads -run 'ConcurrentCreateIdentity1000' -count=1 -v
```

The portable 1,000-identity case must run without Beads. A real-provider variant runs only with an explicitly qualified WP04 binding and records its capability/topology disposition.

### Integration and hygiene

```bash
go test ./internal/tracker/... -count=1
go vet ./internal/tracker/beads
gofmt -w internal/tracker/beads/metadata.go internal/tracker/beads/metadata_test.go internal/tracker/beads/revision.go internal/tracker/beads/revision_test.go internal/tracker/beads/catalog.go internal/tracker/beads/catalog_test.go
git diff --check
git status --short
```

Do not use a formatting command to alter files owned by another WP. Inspect the final diff and keep implementation changes within the six owned paths.

## Definition of Done

- Schema-1 `spec_kitty` metadata round-trips stable identity and structured criteria.
- Unrelated provider/human metadata and unknown schema-1 namespace fields survive catalog updates.
- Mapping conflicts, malformed metadata, duplicate criteria, and future schemas are typed and non-mutating.
- Canonical observation encoding is versioned, deterministic, length-prefixed, order-aware, and absence-aware.
- SHA-256 full digests and compact nonzero revisions detect equal-timestamp content changes.
- Create returns native Beads IDs mapped one-to-one to stable Spec Kitty `WorkID` values.
- Exact operation replay is a no-op; changed replay and conflicting mappings are refused.
- Apply-then-timeout create recovery observes the original issue instead of duplicating it.
- Read returns one complete, self-consistent known provider observation and never repairs state.
- Guarded update requests fail before mutation for Beads v1.2.2.
- Explicit unguarded updates preserve metadata and report their real guarantee.
- Ambiguous updates are observed and classified without blind replay.
- Portable 100-item/200-read latency evidence is produced, with the qualification p95 target at 500 ms.
- The portable 1,000-create test produces zero provider-ID collisions and zero duplicate mappings.
- Real-provider scale execution is capability/topology-aware and cannot report unsupported behavior as passed.
- Focused repeated, race, tracker-wide, vet, formatting, and diff checks pass.
- No Beads internal Go package, direct Dolt SQL, Markdown task-state parser, or new authority decision enters the catalog.
- T021 through T026 are recorded done with:

  ```bash
  spec-kitty agent tasks mark-status T021 T022 T023 T024 T025 T026 --status done --mission beads-tracker-integration-01M0YG7E --json
  ```

## Risks

- **Metadata erasure**: Sending only a reconstructed `spec_kitty` object can delete human/provider keys. Merge from the freshest complete provider metadata.
- **Namespace theft**: Silently accepting another project or work mapping corrupts identity. Return a conflict and require reconciliation.
- **Future-schema downgrade**: Rewriting an unknown newer namespace with schema 1 loses data. Refuse mutation.
- **Timestamp aliasing**: Equal `updated_at` values can accompany different content. Include canonical content, not merely time.
- **Canonicalization collision**: Delimiter joining or unstable map order makes revisions nondeterministic. Use typed length-prefix encoding and golden vectors.
- **Compact revision collision**: A uint64 token is not collision-proof. Retain the full SHA-256 digest for diagnostic disambiguation.
- **False CAS**: A successful pre-read does not guard the later CLI update against direct writers. Capability and mutation results must remain explicitly unguarded.
- **Duplicate ambiguous create**: Blind retry after apply-then-timeout can create a second issue. Resolve through mapping and operation metadata first.
- **Partial absence proof**: An unavailable or truncated list is not evidence that create did not apply.
- **Sensitive evidence**: Catalog bodies and criteria may contain private material. Evidence records IDs, digests, counts, and timings only.
- **Benchmark theater**: A tiny fake-only timing pass does not prove the real-provider NFR. Label provider, topology, host, and admissibility.
- **Embedded concurrency confusion**: Adapter serialization can make callers safe locally but is not provider-native multi-writer support or CAS against humans.
- **Cross-lane API drift**: WP01/WP03/WP04 own contracts used here. Adapt to accepted names and do not fork their types in this package.

## Reviewer Guidance

Require tests that make destructive shortcuts visible. In particular, introduce or inspect mutants that replace the entire metadata object, use `updated_at` as the revision, sort provider-significant comments, retry an ambiguous create blindly, or execute an update after guarded capability refusal; each must red the focused suite for the intended reason.

Verify:

1. identity is anchored by project plus stable `WorkID`, not by title or provider ID;
2. native external IDs remain opaque and are never configured sequentially;
3. metadata merge preserves unrelated keys and refuses future/conflicting namespaces;
4. criteria are structured metadata, while acceptance rendering remains human-facing;
5. canonical encoding includes every declared recovery-relevant field with deterministic normalization;
6. full digest and compact revision come from the same immutable observation;
7. routine diagnostics expose digests/IDs rather than work bodies;
8. create replay compares semantic input, not only operation ID;
9. ambiguous create/update paths observe before retry and cannot duplicate effects;
10. guarded updates refuse before writer lock and command execution when unsupported;
11. an explicitly unguarded update is never described as CAS;
12. the 100-item latency and 1,000-create identity cases disclose provider and topology capabilities;
13. portable tests remain runnable with no `bd`, Dolt, network, or provider environment; and
14. the diff contains only the six files owned by WP05.

Reject whole-object metadata replacement, timestamp-only revisions, nondeterministic JSON/map hashing, raw-body diagnostics, synthesized sequential provider IDs, title-based replay lookup, best-effort remapping, blind ambiguous retries, read-before-write labeled as guarded mutation, tests that silently skip unsupported capability, or new Beads-shaped requirements in the provider-neutral kernel.
