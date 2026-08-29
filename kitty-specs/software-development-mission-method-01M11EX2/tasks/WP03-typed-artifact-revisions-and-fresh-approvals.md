---
work_package_id: WP03
title: Typed Artifact Revisions and Fresh Approvals
dependencies:
- WP02
requirement_refs:
- FR-004
- FR-005
- FR-006
- FR-007
- FR-028
- NFR-004
- NFR-012
- NFR-014
- C-001
- C-012
planning_base_branch: feat/software-development-mission-method
merge_target_branch: feat/software-development-mission-method
branch_strategy: Planning artifacts for this mission were generated on feat/software-development-mission-method. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/software-development-mission-method unless the human explicitly redirects the landing branch.
subtasks:
- T012
- T013
- T014
- T015
- T016
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/artifact/
scope: codebase-wide
create_intent:
- internal/artifact/model.go
- internal/artifact/validation.go
- internal/artifact/service.go
- internal/artifact/model_test.go
- internal/artifact/service_test.go
- internal/mission/service.go
- internal/mission/lifecycle_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/artifact/model.go
- internal/artifact/validation.go
- internal/artifact/service.go
- internal/artifact/model_test.go
- internal/artifact/service_test.go
- internal/mission/service.go
- internal/mission/lifecycle_test.go
- internal/architecture/reachability.go
- internal/architecture/reachability_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 - Typed Artifact Revisions and Fresh Approvals

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Create the portable artifact capability that gives intent, specification, and
plan outputs immutable typed revisions, deterministic content commitments,
bounded validation findings, and approvals bound to one exact revision. The
result must make stale approval replay impossible without turning repository
files, Markdown prose, or mutable paths into mission authority.

## Context

Phase 3 restores the software-development mission method above the existing
authority kernel and tracker boundary. Mission transitions introduced by WP02
need trustworthy evidence that required artifacts exist, passed the applicable
validation policy, and hold a fresh approval. This package supplies that
evidence without deciding mission transitions itself.

The package owns portable artifact values, validation vocabulary, approval
freshness rules, and a narrow consumer-owned store/service contract. WP08 owns
the SQLite schema and transaction implementation. WP02 owns lifecycle and step
law. Later WPs consume only validated artifact revisions and exact-revision
approval results; they must never infer authority from file presence, Git
commit messages, tracker status, or edited Markdown.

Apply these design laws throughout:

- Artifact content may live in Git, but a file is only an input adapter. A path
  is not approval, validation, or authoritative revision identity.
- An `ArtifactRevision` is immutable after publication. New content always
  creates a new positive per-mission/per-kind revision number and identity.
- Approval and rejection records are immutable historical verdicts bound to an
  exact `ArtifactRevisionID`; publishing a successor does not rewrite them.
- Freshness means that an approved verdict names the exact revision currently
  required by the transition. Digest similarity, path equality, or kind
  equality is insufficient.
- Validation and service failures use stable machine-readable kinds, bounded
  fields, and recovery categories. Do not branch on human prose.
- Evidence must contain commitments and bounded findings only—never credentials,
  private transcripts, unrestricted file contents, or tool output.
- The domain package must not import SQL, SQLite, filesystem, tracker, Beads,
  CLI, helper-command, projection, or transport types.
- Crash-atomic durable publication is implemented by WP08, but this port must
  make the required all-or-nothing operations explicit and testable.

## Branch Strategy

Use only the Spec Kitty lane worktree allocated for WP03 after task
finalization. Its dependency base is the accepted WP02 candidate (and therefore
the accepted WP01 candidate transitively), and its merge
target is `feat/software-development-mission-method`. Do not create a worktree,
branch from `main`, reuse another package's lane, or merge directly. WP03 follows
WP02 because artifact freshness becomes mission authority only when the real
mission transition path consumes it. Stay strictly within the nine owned files.
The narrowly overlapping mission and architecture files make this a
linearized cross-cutting boundary WP; they do not authorize broader edits.

Start implementation with:

```bash
spec-kitty agent action implement WP03 --agent codex
```

Read `spec.md`, `plan.md`, `data-model.md`, `research.md`, and
`contracts/artifact-contract.schema.json` before coding. The JSON contract is
the external shape to preserve; the Go model may use private fields and
constructors to enforce stronger invariants.

### Subtask T012: Define immutable typed artifact revisions and commitments

**Purpose**: Establish one portable value model whose identity and digest bind
the exact artifact content and provenance needed by later mission gates.

**Steps**:

1. In `internal/artifact/model.go`, define the closed Phase 3 artifact kinds
   `intent`, `specification`, and `plan`. Unknown or blank kinds must fail with
   a stable typed diagnostic; do not silently accept arbitrary strings or
   default to specification.
2. Define `ArtifactRevision` with the contract fields: stable artifact revision
   identity, mission identity, kind, positive revision number, SHA-256 content
   digest, repository-relative `source_ref` or explicit structured source
   identity, non-empty immutable `source_revision`, creator identity, and
   non-zero UTC creation time.
3. Define `ArtifactApproval` with stable approval identity, exact artifact
   revision identity, closed `approved | rejected` verdict, accountable actor,
   SHA-256 evidence digest, sealed governance-context identity, and non-zero UTC
   submission time. An approval record must not contain artifact bytes,
   transcript text, mutable source paths as authority, or unrestricted evidence.
4. Use constructors/restoration functions and private state where appropriate
   so invalid or partly initialized values cannot cross the package boundary.
   Accessors must not return caller-mutable slices or maps.
5. Implement a domain-qualified SHA-256 content commitment over the exact input
   bytes. Carry or document the algorithm explicitly enough that a future
   digest migration cannot reinterpret existing records.
6. Normalize timestamps to UTC for storage and comparison, but do not include
   wall-clock timestamps in semantic content identity. Preserve revision and
   provenance timestamps as audit facts.
7. Define stable artifact error kinds and an error shape with bounded field
   references and recovery category. Keep human messages useful but ensure no
   caller needs to parse them to classify invalid identity, unsupported kind,
   malformed digest, invalid revision, or invalid provenance.

**Files**:

- `internal/artifact/model.go` — artifact and approval values, closed
  vocabularies, commitments, constructors, accessors, validation, and stable
  domain diagnostics (approximately 220–320 lines).
- `internal/artifact/model_test.go` — value, immutability, digest, and invalid
  boundary tables shared by later subtasks.

**Validation**:

- Identical bytes yield one lowercase 64-character SHA-256 digest across at
  least 100 repeated constructions; a one-byte change changes the commitment.
- Blank or malformed IDs, non-positive revisions, unknown kinds/verdicts,
  malformed digests, absolute or ambient source authority, and zero timestamps
  fail with exact stable error kinds.
- Constructed and restored values preserve all contract fields without exposing
  mutable internal collections.
- Model tests demonstrate that Git path equality does not imply artifact
  equality and that timestamps do not rewrite content identity.

### Subtask T013: Model deterministic validation verdicts and bounded findings

**Purpose**: Represent specification and plan quality as typed evidence bound
to one revision, so lifecycle code can fail closed without parsing reviewer
prose or validation output.

**Steps**:

1. In `internal/artifact/validation.go`, define a closed validation verdict
   vocabulary of `pass` and `fail`, plus a `Finding` containing stable `kind`,
   bounded logical `field`, bounded human `message`, and a typed recovery
   category suitable for callers.
2. Define a validation result bound to one exact artifact revision and validator
   policy/revision. Include a non-zero UTC validation time and deterministic
   ordering of findings. If the public JSON contract keeps validation nested,
   ensure the Go read model can project to that shape without losing its exact
   revision binding.
3. Require pass results to contain no error findings and fail results to contain
   at least one finding. Reject blank kinds/fields/messages, unsupported recovery
   categories, duplicate semantic findings, and values above documented bounds.
4. Canonically sort findings by stable semantic keys or reject non-canonical
   input at the constructor; choose one rule and prove it. Presentation order
   from a validator must never alter the validation commitment or service
   replay behavior.
5. Provide small validation-policy helpers for the structural conditions needed
   by FR-006 and FR-007: specification evidence must cover intent, scope,
   requirements, acceptance framing, assumptions, and boundaries; plan evidence
   must cover architecture, data flow, risk, compatibility, migration, and
   validation strategy. Keep these helpers typed and policy-driven rather than
   parsing Markdown headings.
6. Bound every diagnostic before it enters durable evidence. Reject or safely
   truncate according to one tested rule; do not retain artifact bodies,
   command output, secrets, or private transcript material in findings.

**Files**:

- `internal/artifact/validation.go` — verdicts, findings, policies,
  normalization, and bounded diagnostic rules (approximately 180–280 lines).
- `internal/artifact/model_test.go` — validation constructor, ordering, policy,
  privacy, and bounds tests.

**Validation**:

- The specification and plan policy tables emit stable finding kinds for each
  missing required concern and pass only when the full applicable set exists.
- Reordered equivalent findings produce one canonical result and commitment.
- Invalid pass/fail combinations and oversized or sensitive evidence fail
  before reaching a store collaborator.
- No production validation path reads Markdown, checks a file's existence, or
  uses free-form prose as a state discriminator.

### Subtask T014: Publish revisions through a narrow idempotent service port

**Purpose**: Define the all-or-nothing application boundary that WP08 can
persist, while proving publication replay and conflict behavior without SQL in
the portable package.

**Steps**:

1. In `internal/artifact/service.go`, define a minimal consumer-owned repository
   interface for publication, exact revision lookup, current-revision lookup by
   mission/kind, validation retrieval, approval retrieval, and approval write.
   Express aggregate operations rather than exposing tables, transactions, or
   database implementation details.
2. Define a `Publish` command with explicit operation/idempotency identity,
   mission/kind, source provenance, creator, timestamp, exact content bytes or
   prevalidated commitment input, and the complete validation result. Ensure
   raw bytes are used transiently for commitment and are not automatically
   placed in diagnostics or persistence contracts.
3. Make publication assign or validate the next positive per-mission/per-kind
   revision number through the repository's atomic operation. Concurrent
   callers must not both claim the same revision. Do not derive authority from
   filesystem timestamps, branch names, or mutable working-tree state.
4. Compute a canonical request digest over all semantic command fields. The
   same operation ID plus the same request must return the original result;
   the same operation ID plus different semantic input must return an explicit
   idempotency conflict with no new revision.
5. Require publication and validation evidence to appear atomically at the port
   boundary. A failure before commit exposes the complete prior state; replay
   after an ambiguous return can discover and return the complete committed
   result. Leave crash injection and SQLite transaction tests to WP08.
6. Add a deterministic in-memory/recording repository only inside
   `service_test.go`. It should prove call order, exact requests, replay,
   conflict, and failure behavior without becoming production storage.

**Files**:

- `internal/artifact/service.go` — repository port, publish/lookup commands and
  results, request commitments, and service orchestration (approximately
  220–340 lines).
- `internal/artifact/service_test.go` — owned recording repository and
  publication/idempotency tests.

**Validation**:

- Two semantic replays return the same artifact revision identity, number,
  validation evidence, and operation result without a second write.
- Operation reuse with changed bytes, kind, source revision, actor, validation,
  or provenance returns the documented conflict kind.
- Store failure produces no apparent published artifact; an ambiguous committed
  response is recoverable through replay.
- Package imports remain portable and contain no SQL, SQLite, filesystem, Git
  runner, tracker, CLI, or projection dependencies.

### Subtask T015: Bind approvals to exact revisions and enforce freshness

**Purpose**: Make approval useful as mission authority only for the exact bytes
reviewed, while retaining rejection and superseded approvals as immutable audit
history.

**Steps**:

1. Add an `Approve` service command with explicit operation identity, exact
   artifact revision identity, verdict, actor, bounded evidence commitment,
   sealed governance-context identity, and submission time. Require the target
   revision and its completed validation to exist.
2. Refuse `approved` when validation failed, is absent, names another revision,
   or does not satisfy the applicable artifact-kind policy. A `rejected`
   verdict remains valid historical evidence and must never satisfy a gate.
3. Apply the same replay law as publication: same operation plus same canonical
   request returns the prior approval; changed request under the same operation
   returns conflict. Never update an existing verdict in place.
4. Implement a pure freshness query that receives the exact required/current
   revision identity and returns a typed result such as `fresh`, `missing`,
   `rejected`, or `stale`. `fresh` requires an approved verdict for that exact
   revision; same kind, source path, revision number, or digest alone must not
   substitute for identity equality.
5. Prove that publishing revision 2 leaves revision 1 and its approval readable
   but makes that approval stale for a transition requiring revision 2. A later
   approval of revision 2 becomes fresh without modifying revision 1 evidence.
6. Keep mission advancement outside this package. Return typed evidence that
   the mission service consumes; do not call a mission transition, infer current
   step, or grant execution authority from inside the artifact service.
7. In `internal/mission/service.go`, add a narrow read-only artifact-approval
   source and make specification/plan approval transitions load the exact
   current revision and call `artifact.EvaluateFreshness`. Only a fresh exact
   approval may synthesize the corresponding accepted mission evidence.
8. Refuse missing, rejected, and stale freshness results with stable mission
   diagnostics. Reject caller-manufactured specification/plan approval evidence
   so a transition requester cannot attest its own approval.
9. Keep direction one-way: mission may consume artifact freshness, while
   artifact must not import mission or advance lifecycle state.

**Files**:

- `internal/artifact/service.go` — approval command, idempotent service path,
  exact-revision freshness query, and typed results.
- `internal/artifact/service_test.go` — approval, rejection, stale-successor,
  replay, conflict, and failure-path tests.
- `internal/mission/service.go` — legitimate artifact-freshness consumer in the
  lifecycle transition path.
- `internal/mission/lifecycle_test.go` — fresh acceptance; missing, rejected,
  stale, and forged-evidence refusal controls.

**Validation**:

- A valid exact-revision approval is fresh; missing, rejected, failed-validation,
  and successor-revision cases return distinct stable machine-readable results.
- Repeated approval commands are deterministic and never create duplicate or
  contradictory verdicts.
- Mutating source bytes or publishing a new revision cannot reuse the previous
  approval to pass a later gate.
- Freshness queries are pure: repeated calls issue reads only and do not create
  events, operation rows, approvals, or revisions.
- `go list` exposes a real production `mission -> artifact` edge, and removal of
  the mission freshness call makes the permanent reachability campaign fail.

### Subtask T016: Lock the artifact contract with unit and adversarial tests

**Purpose**: Turn FR-004/FR-005 and the artifact JSON contract into durable
regression proof before SQLite and mission-lifecycle composition are added.

**Steps**:

1. In `model_test.go`, add table-driven contract fixtures matching
   `contracts/artifact-contract.schema.json`: schema version 1; complete
   artifact revision; pass/fail validation; null, approved, and rejected
   approval shapes; strict required fields; closed kinds and verdicts.
2. Test stable JSON projection or round-trip behavior if production encoding is
   exposed. Unknown top-level authority fields and unsupported schema versions
   must fail closed; malformed digests, timestamps, and enum values must return
   stable diagnostic kinds rather than generic decoder prose.
3. In `service_test.go`, build a complete progression: publish specification
   revision 1, validate, approve, confirm freshness, publish changed revision 2,
   confirm revision-1 approval is historical/stale, reject an invalid revision,
   approve corrected revision 3, and confirm only revision 3 is fresh.
4. Add deletion-style tests for each load-bearing check: exact identity,
   validation pass, policy match, idempotency digest, immutable historical
   verdict, and current revision comparison. Each test should fail if its guard
   is removed or weakened.
5. Exercise concurrency at the portable boundary with deterministic repository
   collaborators where useful, but reserve durable two-process/SQLite race and
   crash campaigns for WP08/WP12. Run the package under the race detector.
6. Search the owned production files for forbidden authority shortcuts and
   sensitive data capture. Assertions should prove paths/content adapters are
   never treated as approval and diagnostics do not contain supplied secret or
   transcript-shaped values.
7. Record T012 through T016 complete only after all targeted tests and the
   repository-wide compile check pass, using the event-sourced status commands
   in the Definition of Done.
8. Extend the permanent reachability registry with the stable
   `internal/artifact.EvaluateFreshness` seam using package-present activation.
   Add source-loaded mutants for missing registration, missing declaration,
   test-only or shadowed callers, and removal of the sole mission caller.

**Files**:

- `internal/artifact/model_test.go` — model/schema, canonicalization, strict
  decoding, validator, and mutation-control coverage.
- `internal/artifact/service_test.go` — end-to-end service, replay, freshness,
  privacy, concurrency-contract, and repository-collaborator coverage.
- `internal/architecture/reachability.go` — stable artifact authority target.
- `internal/architecture/reachability_test.go` — registry census and real
  source-loaded reachability mutants.
- `internal/mission/lifecycle_test.go` — mission/artifact authority integration
  and forged-evidence mutants.

**Validation**:

```bash
go test ./internal/artifact
go test ./internal/mission
go test ./internal/architecture
go test -race ./internal/artifact
go test -race ./internal/mission
go test ./internal/...
go vet ./internal/artifact
gofmt -d internal/artifact
```

- The complete artifact lifecycle test passes without a filesystem, Git
  process, tracker, SQLite database, or mission transition implementation.
- All stable error/freshness kinds are asserted by value; no test parses error
  prose to decide behavior.
- `go test -race` reports no races, and callers cannot mutate published values
  through retained slices, maps, or byte buffers.
- The exact architecture campaign proves the registered artifact seam has a
  legitimate non-test mission caller and rejects missing/test-only callers.

## Definition of Done

- The artifact package exposes immutable, validated values for intent,
  specification, plan, validation evidence, and exact-revision approvals.
- Content commitments are deterministic, domain-qualified, algorithm-explicit,
  and sensitive-input safe.
- Validation findings and service errors are typed, bounded, stable, and carry
  actionable recovery categories without private payloads.
- Publication and approval ports state atomic, crash-recoverable, idempotent
  semantics that WP08 can implement without changing domain law.
- The same operation and semantic request replay exactly; operation reuse with
  changed content conflicts without mutation.
- Approval freshness requires the exact currently required revision identity;
  successor publication preserves old evidence while making it stale.
- Specification and plan transitions derive approval evidence only through the
  registered artifact freshness seam; forged caller evidence is refused.
- Files remain adapters, not authority. No production code parses Markdown,
  treats path existence as approval, or imports a persistence/transport/provider
  adapter into `internal/artifact`.
- All nine owned files are present, formatted, and covered by artifact,
  mission, architecture, and race tests; the repository-wide internal package
  compile check passes.
- Record completion evidence for each subtask only after its validation passes:

```bash
spec-kitty agent tasks mark-status T012 --status done
spec-kitty agent tasks mark-status T013 --status done
spec-kitty agent tasks mark-status T014 --status done
spec-kitty agent tasks mark-status T015 --status done
spec-kitty agent tasks mark-status T016 --status done
```

## Risks

- **Path authority leakage**: A repository path is mutable and ambient.
  Mitigate by binding approval to immutable revision identity plus content and
  source commitments; use files only to supply bytes.
- **Approval replay after change**: Reusing a verdict by kind, path, or digest
  can authorize unreviewed content. Mitigate with exact revision identity and a
  pure explicit freshness result.
- **Duplicate authority with lifecycle code**: Artifact services that advance a
  mission collapse package ownership. Mitigate by returning typed evidence and
  leaving all transitions to `internal/mission`.
- **Persistence leakage**: Designing around SQLite rows now would couple domain
  law to the current adapter. Mitigate with narrow aggregate store operations;
  WP08 owns schemas and transaction mechanics.
- **Ambiguous crash result**: A write may commit before an error reaches the
  caller. Mitigate through operation identity, canonical request digest, and
  replayable repository operations.
- **Sensitive evidence retention**: Content, transcripts, tool logs, or secrets
  can leak through findings and errors. Mitigate with commitments, hard bounds,
  closed fields, and adversarial privacy tests.
- **Canonicalization drift**: Finding order or timestamp normalization can make
  equivalent requests conflict. Mitigate with documented canonical ordering,
  UTC normalization, and repeated deterministic tests.

## Reviewer Guidance

Review this package as an authority boundary, not merely a data model. Confirm
that an approval can authorize only the exact artifact revision it names, that
publishing new content cannot mutate or inherit a prior verdict, and that all
freshness decisions are typed and pure.

Reject any implementation that treats a file, Markdown heading, Git commit
message, tracker state, or source path as sufficient authority. Reject SQL,
SQLite, filesystem traversal, CLI types, or mission transitions in the
portable artifact package. Verify that service operations expose enough
idempotency and aggregate atomicity for WP08 to implement crash recovery without
inventing new domain semantics.

Pay particular attention to canonical request digests, caller-owned byte/slice
mutation, pass/fail finding consistency, sensitive diagnostics, replay after an
ambiguous commit, and strict exact-revision comparison. Run the package race
tests and remove one guard at a time mentally—or with a local mutant—to ensure
the tests fail for the architectural reasons claimed.
