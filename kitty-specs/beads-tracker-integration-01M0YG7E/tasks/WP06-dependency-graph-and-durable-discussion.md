---
work_package_id: WP06
title: Dependency Graph and Durable Discussion
dependencies:
- WP05
requirement_refs:
- C-001
- C-008
- FR-010
- FR-011
- FR-016
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Planning artifacts for this mission were generated on feat/beads-tracker-integration. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/beads-tracker-integration unless the human explicitly redirects the landing branch.
subtasks:
- T027
- T028
- T029
- T030
- T031
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/graph.go
- internal/tracker/beads/graph_test.go
- internal/tracker/beads/discussion.go
- internal/tracker/beads/discussion_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/graph.go
- internal/tracker/beads/graph_test.go
- internal/tracker/beads/discussion.go
- internal/tracker/beads/discussion_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP06 - Dependency Graph and Durable Discussion

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Implement the Beads adapter's provider-neutral dependency graph and durable
discussion capabilities. Preserve exact blocking-edge direction, expose Beads
readiness only as a diagnostic projection, and make comment replay safe by
observing operation markers without ever guessing whether an ambiguous append
was applied.

## Context

WP05 supplies stable `WorkID` to Beads issue mappings, metadata, catalog reads,
and deterministic observation revisions. WP06 builds graph and discussion
behavior on those mappings and on the bounded command protocol from WP03. WP08
and WP09 will reuse these capabilities for adversarial and real-provider
conformance, so this package must provide observable, provider-neutral behavior
rather than tests coupled to a particular subprocess call sequence.

The authority boundary is normative:

- Beads may store mutable dependency facts, readiness projections, and visible
  comments.
- A finalized Spec Kitty `WorkRevision` remains the executable graph for a run.
- Beads readiness, issue status, custom states, Molecules, formulas, agent
  identity, and merge slots cannot authorize scheduling or execution.
- A Beads comment may mirror review or integration information, but it is never
  itself a review verdict, acceptance record, or integration result.
- Human-authored comments are provider-owned history. The adapter may observe
  and return them but must never rewrite, hide, or delete them.

Use the capability interfaces and domain values established by WP01. Use WP03's
typed commands, protocol decoder, and ambiguity vocabulary; use WP04's binding
and writer policy; use WP05's mappings, metadata, and revision canonicalization.
Do not duplicate those mechanisms in these four owned files. Do not parse human
CLI output, import Beads Go internals, issue Dolt SQL, or add provider workflow
semantics to the tracker port.

## Branch Strategy

Spec Kitty owns lane allocation and dependency selection. Begin only after WP05
is accepted, work in the generated WP06 lane, and return the reviewed candidate
to `feat/beads-tracker-integration`. Do not branch from `main`, manually copy
WP05 changes, or edit another work package's files to resolve an interface
mismatch; report such a mismatch through the mission workflow.

Start implementation with:

```bash
spec-kitty agent action implement WP06 --agent codex
```

Before coding, read `spec.md`, `plan.md`, `research.md`, `data-model.md`, and
both protocol contracts. Inspect the accepted WP01–WP05 APIs as implemented;
the contracts govern semantics while the accepted code governs exact names.

### Subtask T027: Implement dependency direction, translation, and validation

**Purpose**: Translate the provider-neutral graph port to Beads blocking edges
without allowing inverted edges, unsupported edge kinds, or provider-only
relationships to enter the executable work graph.

**Steps**:

1. In `internal/tracker/beads/graph.go`, implement the graph capability methods
   defined by WP01 for adding, removing, and listing blocking dependencies.
   Resolve both endpoints through WP05's stable mappings before mutation.
2. Preserve the canonical direction everywhere: `BlockedWorkID` is the work
   that cannot proceed, and `BlockingWorkID` is its prerequisite. Render the
   provider command as `bd dep add <blocked> <blocker> --type blocks --json`
   through WP03's typed constructor; never swap arguments to match prose.
3. Validate both work references, project identity, mapping uniqueness, edge
   kind, operation identity where required, and self-dependency before spawning
   a command. Reject an unmapped endpoint or mismatched project as a stable
   provider-neutral diagnostic without creating a partial edge.
4. Translate only Beads `blocks` relationships into scheduling-capable
   `DependencyRef` values. Preserve other provider edge kinds, when exposed by
   the accepted domain API, only as non-authoritative provenance/diagnostics;
   never silently reinterpret them as blocking.
5. Normalize list results deterministically. Return edges in the ordering
   required by the port or sort by stable internal identities so command output
   order cannot create observation drift. Preserve external edge identity only
   as diagnostic data.
6. Translate duplicate-edge no-op, missing-edge removal, provider cycle
   rejection, missing issue, malformed response, and revision drift according
   to the portable error vocabulary. Do not invent local graph success when
   provider mutation is ambiguous.
7. For a mutating command that times out or loses its response after start,
   re-observation may classify the requested exact edge as applied, absent, or
   conflicting. If observation is incomplete or contradictory, return
   ambiguity; do not issue a blind second mutation.

**Files**:

- `internal/tracker/beads/graph.go` — graph adapter, endpoint validation,
  direction-safe translation, deterministic listing, and result classification.
- `internal/tracker/beads/graph_test.go` — translation, validation, cycle,
  unknown-edge, and ambiguity tests.

**Validation**:

- A test with intentionally distinct blocked/blocker IDs proves exact argv and
  exact round-trip direction.
- Self-edges, unmapped endpoints, cross-project references, unsupported kinds,
  and cycles produce stable diagnostics without a second mutation.
- Unknown non-blocking relationships never appear as executable blockers.
- Add/remove replay and timeout results remain honest about provider evidence.

### Subtask T028: Expose provider readiness as a diagnostic projection

**Purpose**: Make Beads' view of ready work visible for reconciliation without
letting mutable provider state replace the pinned Spec Kitty graph.

**Steps**:

1. Implement `ReadyProjection` using the accepted `Graph` interface and the
   WP03 typed `ready` command. Resolve returned external issues through WP05's
   mappings and report unknown or conflicting mappings explicitly.
2. Return provider-ready IDs and their observation evidence as a projection.
   Do not return a boolean or state transition that can directly authorize a
   run, lease, workspace allocation, handoff, review, or integration.
3. Compare provider readiness with the caller-supplied pinned `WorkRevision`
   facts only through the provider-neutral diagnostic shape established by
   WP01. Identify agreement, provider-only-ready, pinned-only-ready, unknown,
   and unavailable states without mutating either graph.
4. Ensure post-finalization tracker changes become drift input. They must not
   rewrite the finalized generation or silently change a currently running
   agent's acceptance or dependency contract.
5. Treat provider status and readiness causes as opaque provider facts. Do not
   adopt custom Beads statuses, Molecules, formulas, or issue assignment as
   Spec Kitty scheduler conditions.
6. Make list ordering deterministic and exclude unmapped/foreign-project work
   from positive agreement. Retain safe identifiers and revision evidence in
   diagnostics; do not include descriptions or comments.

**Files**:

- `internal/tracker/beads/graph.go` — readiness projection and pinned-graph
  comparison adaptation.
- `internal/tracker/beads/graph_test.go` — agreement, disagreement, unknown,
  post-finalization drift, and non-authority tests.

**Validation**:

- Provider-ready disagreement is observable but cannot alter the pinned graph.
- A Beads-ready item that is blocked in the finalized revision remains blocked
  for Spec Kitty and produces a diagnostic disagreement.
- Unknown and foreign mappings cannot become ready work.
- No graph method imports or calls execution, lease, workspace, review, or
  integration authority APIs.

### Subtask T029: Define a safe operation-marker discussion convention

**Purpose**: Attach durable, queryable Spec Kitty operation identity to comments
while preserving the exact human body and preventing comment content from
leaking into diagnostics or logs.

**Steps**:

1. In `internal/tracker/beads/discussion.go`, define one versioned namespaced
   marker convention for adapter-authored comments. It must encode at least the
   `OperationID` and schema version in a deterministic form that can be found on
   a later list, while avoiding collision with normal human prose.
2. Keep the marker separate from the semantic comment body in the adapter API.
   Rendering may place a compact marker into the provider-visible comment only
   if Beads v1.2.2 cannot atomically associate metadata with a comment; document
   that provider representation in code comments and parse only the exact
   namespaced format.
3. Escape or encode operation identity so an untrusted body cannot forge a
   second marker, terminate the marker envelope, or cause body normalization.
   Reject invalid IDs before mutation.
4. Preserve user comment bodies byte-for-byte according to the provider's JSON
   string contract. Do not trim, case-fold, interpolate through a shell, or
   treat body lines resembling the marker as authoritative.
5. Translate listed provider comments into `DiscussionEntry` values including
   external comment identity, provider author, timestamp, operation marker when
   valid, and observation revision. Preserve provider-significant comment
   ordering.
6. Ensure normal diagnostics and errors contain IDs, marker presence, counts,
   and safe hashes at most. They must never contain body text, provider warning
   prose, or arbitrary author-supplied content.

**Files**:

- `internal/tracker/beads/discussion.go` — marker codec, provider comment
  translation, and redaction-safe diagnostic helpers.
- `internal/tracker/beads/discussion_test.go` — marker round-trip, forgery,
  arbitrary body, human comment, order, and secret-sentinel tests.

**Validation**:

- Exact adapter markers round-trip to the correct operation; malformed and
  human lookalikes remain ordinary comment content.
- Multiline, Unicode, empty-policy boundary, and marker-like bodies are
  preserved without command injection or normalization.
- Human comments without markers remain visible and never become replay proof.
- Secret/body sentinels do not appear in errors, diagnostics, or test logs.

### Subtask T030: Implement append/list replay and ambiguity refusal

**Purpose**: Provide useful durable discussion while admitting that Beads
v1.2.2 does not expose a general conditional comment append primitive.

**Steps**:

1. Implement `Discussion.List` through the WP03 JSON command and WP05 mapping.
   Return all visible comments, including human entries, in provider-significant
   order with deterministic observation revision handling.
2. Implement `Discussion.Append` with required `OperationID`, mapped work,
   author mapping, body, and expected observation inputs. Validate all inputs
   and binding state before invoking the provider.
3. Before append/retry, observe comments. An exact operation-marker and exact
   semantic-body match is replay evidence and returns the original mutation as
   a no-op. The same marker with a different body or work mapping is a conflict,
   never a successful replay.
4. Append the body and marker in the one provider command characterized by the
   protocol. If the provider cannot attach and query the marker atomically,
   declare only append/ambiguous capability—not unconditional idempotency.
5. After a valid success response, re-observe when the accepted capability
   contract requires proof and return the actual provider observation. Do not
   fabricate an external comment ID or revision from local intent.
6. If append times out, is canceled, emits malformed/truncated success, or loses
   transport after start, observe before deciding. Exact marker/body means
   applied replay; proven absence permits only the retry disposition allowed by
   the capability contract; marker conflict or unavailable observation returns
   reconciliation-worthy ambiguity.
7. Never blindly repeat an ambiguous append. A provider that cannot prove the
   marker/body association must leave the operation unresolved rather than
   create duplicate comments.

**Files**:

- `internal/tracker/beads/discussion.go` — list, append, replay observation,
  conflict detection, and ambiguity refusal.
- `internal/tracker/beads/discussion_test.go` — success, exact replay, marker
  collision, unavailable observation, and timeout-after-append tests.

**Validation**:

- Replaying the same operation/body returns one provider comment.
- Reusing an operation with a different body is a typed conflict.
- Timeout-after-append plus successful observation resolves as applied without
  a second append; unavailable/indeterminate observation performs no retry.
- Listing retains human comments and adapter comments in provider order.

### Subtask T031: Complete graph and discussion contract tests

**Purpose**: Prove the two capabilities against adversarial provider responses
and the mission's authority laws before broader conformance packages consume
them.

**Steps**:

1. Build table-driven graph tests covering add, remove, list, exact direction,
   duplicate edge, missing edge, self-edge, provider cycle rejection, unknown
   relationship kinds, unknown mappings, foreign project mappings, malformed
   responses, and timeout-after-add/remove.
2. Add readiness tests for exact agreement, both disagreement directions,
   stale provider revision, unmapped ready issues, provider unavailability, and
   tracker changes after a finalized pinned revision.
3. Build discussion tests covering append/list, exact replay, marker/body
   conflict, human comments before and after adapter comments, unknown provider
   fields, malformed markers, duplicate markers, unavailable observation, and
   timeout-after-append.
4. Use command seams or helper processes already accepted from WP03. Do not
   depend on a real `bd` binary, network, repository Git state, or human output.
5. Add negative authority assertions at the adapter boundary: readiness cannot
   create execution authority; comment contents/status-like words cannot become
   a verdict; unknown/non-blocking edges cannot block or unblock pinned work.
6. Run tests under the race detector and repeated execution. Avoid wall-clock
   sleeps; use controlled contexts and deterministic observations. Ensure test
   failures redact comment bodies and do not write repository artifacts.

**Files**:

- `internal/tracker/beads/graph_test.go` — full graph/readiness matrix.
- `internal/tracker/beads/discussion_test.go` — full marker/replay/ambiguity
  matrix and authority-negative assertions.

**Validation**:

Run at minimum:

```bash
go test ./internal/tracker/beads
go test -race ./internal/tracker/beads
go test -count=20 ./internal/tracker/beads
go vet ./internal/tracker/beads
```

The focused suite must demonstrate cycles, unknown relationships/mappings,
human comments, exact replay, and timeout-after-append without skipped cases.

## Test Strategy

Tests should target observable capability contracts, not private call counts.
Use deterministic protocol doubles to inject valid, stale, malformed,
unavailable, and timeout-after-apply responses. Assert exact command direction
where it is itself contractual, then assert returned provider-neutral values,
stable diagnostics, and whether a follow-up mutation occurred.

For graph behavior, use distinct IDs that make reversal obvious and compare the
provider projection to an immutable pinned revision. For discussion behavior,
seed both human and adapter-authored comments, vary order and unknown fields,
and make every ambiguity test assert the absence of a blind replay.

WP06 must remain runnable without Beads or network access. WP08 owns universal
portable conformance and WP09 owns real Beads qualification; these tests should
provide the narrow deterministic evidence those later packages compose.

## Definition of Done

- The four exact owned files implement the accepted provider-neutral `Graph`
  and `Discussion` capabilities through existing adapter primitives.
- Blocking dependencies preserve `blocked depends on blocker` in requests,
  provider commands, observations, and returned domain values.
- Self, cycle, unknown, unmapped, cross-project, duplicate, missing, malformed,
  and ambiguous dependency outcomes have explicit tested semantics.
- Non-blocking provider relationships never enter executable readiness.
- Provider readiness is a diagnostic comparison with pinned `WorkRevision`
  facts and cannot authorize or mutate scheduling.
- Operation markers are versioned, deterministic, forgery-resistant within the
  representation, and separate from semantic body handling.
- Human comments remain visible, ordered, and untouched.
- Exact operation-marker/body replay produces one comment; marker conflict and
  indeterminate timeout never issue a blind append.
- Routine errors, diagnostics, and logs contain no comment bodies or arbitrary
  provider text.
- All focused tests pass normally, repeatedly, and under `-race`; vet passes.
- No production code parses human CLI output, imports Beads internals, queries
  Dolt, or adopts Beads workflow states as Spec Kitty authority.
- No file outside `owned_files` changes. Stop and report an interface mismatch
  instead of editing another WP's accepted surface.
- Record completion through event-sourced task status, not Markdown checkboxes:

```bash
spec-kitty agent tasks mark-status T027 --status done
spec-kitty agent tasks mark-status T028 --status done
spec-kitty agent tasks mark-status T029 --status done
spec-kitty agent tasks mark-status T030 --status done
spec-kitty agent tasks mark-status T031 --status done
```

## Risks

- **Reversed dependency direction**: provider verbs and natural language are
  easy to misread. Use asymmetric IDs and assert argv plus round-trip values.
- **Readiness authority leakage**: a convenient provider-ready boolean can
  bypass immutable mission generations. Return comparison diagnostics only and
  include explicit negative authority tests.
- **Cycle misclassification**: provider cycle rejection may arrive as a generic
  error. Translate only structurally supported codes; never parse prose.
- **False comment idempotency**: pre-read then append is not atomic against
  external writers. Declare the real capability and refuse uncertain replay.
- **Marker forgery/collision**: body text may resemble adapter metadata. Use an
  exact versioned encoding, validate identity, and treat lookalikes as content.
- **Privacy leakage**: comments routinely contain sensitive material. Keep body
  values out of diagnostics, errors, evidence, and arbitrary test output.
- **Human-history damage**: filtering or rewriting unknown comments would make
  Beads unsafe as the shared catalog. Preserve unknown human entries and fields.
- **Scope creep**: assignment reconciliation, authority scenarios, and real
  provider setup belong to WP07–WP09. Expose narrow primitives, not policy.

## Reviewer Guidance

Start review with dependency direction. Use one blocked item and one distinctly
named blocker, inspect the rendered command, then verify the listed edge returns
the same internal meaning. Reject any implementation whose naming, test data,
or helper hides a possible reversal.

Next trace provider readiness end to end. It must terminate in a diagnostic
projection compared against a pinned revision; it must not call execution,
lease, workspace, review, acceptance, or integration APIs. Search for Beads
status/Molecule/formula concepts and reject any scheduling dependency on them.

For discussion, force timeout after the provider records a comment but before
the response. The adapter must observe the exact marker/body, resolve replay if
proven, and never append blindly. Then make observation unavailable and verify
that the adapter returns ambiguity without a second write. Confirm a reused
marker with different content is a conflict and that human comments survive.

Finally inspect error and test output with secret-shaped comment bodies. Verify
no body, raw stderr, or provider prose escapes. Run the focused suite under the
race detector and repeated execution, and cite evidence for cycle rejection,
unknown edges/mappings, readiness disagreement, human comments, exact replay,
and timeout-after-append before approval.
