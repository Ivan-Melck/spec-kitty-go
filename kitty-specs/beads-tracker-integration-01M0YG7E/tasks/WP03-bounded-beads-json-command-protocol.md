---
work_package_id: WP03
title: Bounded Beads JSON Command Protocol
dependencies:
- WP01
- WP02
requirement_refs:
- C-002
- C-003
- FR-002
- FR-003
- FR-016
- FR-017
- NFR-003
- NFR-007
- NFR-012
planning_base_branch: feat/beads-tracker-integration
merge_target_branch: feat/beads-tracker-integration
branch_strategy: Implement in the Spec Kitty lane allocated from the finalized dependency base after WP01 and WP02; merge the accepted candidate into feat/beads-tracker-integration. Do not choose or recreate a base branch manually.
subtasks:
- T010
- T011
- T012
- T013
- T014
- T015
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/tracker/beads/
create_intent:
- internal/tracker/beads/adapter.go
- internal/tracker/beads/command.go
- internal/tracker/beads/command_test.go
- internal/tracker/beads/protocol.go
- internal/tracker/beads/protocol_test.go
- internal/tracker/beads/errors.go
- internal/tracker/beads/errors_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/tracker/beads/adapter.go
- internal/tracker/beads/command.go
- internal/tracker/beads/command_test.go
- internal/tracker/beads/protocol.go
- internal/tracker/beads/protocol_test.go
- internal/tracker/beads/errors.go
- internal/tracker/beads/errors_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP03 - Bounded Beads JSON Command Protocol

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Build the narrow, bounded process and JSON protocol seam through which later
Beads capabilities will operate. The result must accept only typed commands,
render deterministic argv without a shell, enforce cancellation and output
limits, decode only the documented schema-1 automation protocol, and translate
every failure into stable provider-neutral diagnostics without parsing human
prose or leaking payloads and secrets.

## Context

Beads v1.2.2 does not expose a supported public Go consumer package. Its stable
automation boundary is the `bd` executable with documented `--json` output and
`BD_JSON_ENVELOPE=1`. This work package establishes that external-process seam;
later packages will supply canonical binding, workspace identity, catalog,
graph, discussion, assignment, and qualification behavior.

The central distinction is between transport evidence and application meaning.
This layer knows command kinds, typed argument values, process lifecycle,
wire shapes, schema versions, exit classifications, and whether a mutation may
have become ambiguous. It must not decide whether a mission can run, mint a
lease, infer readiness, interpret a review verdict, or compensate tracker
state. Those decisions remain above the adapter and authority kernel.

Honor the following boundaries throughout:

- Depend on provider-neutral types introduced by WP01; do not duplicate them.
- Remain entirely within the adapter tier admitted by WP02.
- Do not import any Beads Go package, especially a Beads `internal` package.
- Do not query Dolt directly or model its storage schema.
- Do not invoke a shell or accept caller-provided raw argv fragments.
- Do not infer success or error kinds from warning or human-formatted prose.
- Do not persist or log stdout, stderr, descriptions, comment bodies,
  credentials, or the full inherited environment.
- Do not implement discovery or initialization. WP04 owns executable and
  workspace binding, health negotiation, and writer serialization.
- Treat all mutating commands that start but lack a conclusive valid response
  as ambiguous until a later independent observation resolves the outcome.

Implement from the finalized dependency base using:

```bash
spec-kitty agent action implement WP03 --agent codex
```

Read `spec.md`, `plan.md`, `research.md`, `data-model.md`,
`contracts/beads-json-protocol.md`, and `contracts/tracker-capabilities.md`
before coding. Keep all changes within the exact `owned_files` list.

### Subtask T010: Define the closed typed command vocabulary and argv renderer

**Purpose**: Make unsupported or unsafe command construction unrepresentable
outside the adapter. Callers provide validated command-specific values; only
this package renders the final argv vector.

**Steps**:

1. In `internal/tracker/beads/command.go`, define a closed `CommandKind`
   vocabulary covering only the protocol operations reserved by the plan:
   executable identity, workspace identity, schema/database information,
   non-fixing health inspection, show, list, ready, create, update, atomic
   claim, dependency add/list, comment append/list, portable export, and the
   explicitly characterized native-backup probes.
2. Represent command inputs as typed request values. Prefer a closed tagged
   representation or private renderer interface over a public `[]string`.
   Reject invalid combinations before process creation: empty required IDs,
   relative executable paths, absent workspace, non-positive timeouts or
   limits, unsupported output shape, and mutation flags inconsistent with the
   selected command.
3. Render arguments deterministically. Include `--json` only where the v1.2.2
   command contract documents it; never invent JSON support for a human-only
   command. Preserve the dependency direction `blocked` then `blocker` for
   future graph callers, but do not translate graph domain values here.
4. Keep the executable separate from argv and pass argv directly to
   `exec.CommandContext` or the lower-level process primitive selected in
   T011. Never compose a shell command string and never invoke `sh -c`.
5. Associate each command kind with its expected wire shape (`object`, `array`,
   `JSONL`, or `empty`), whether it is mutating, and its default timeout class.
   Ordinary read defaults are 10 seconds; create/update/dependency/comment
   mutations default to 30 seconds; doctor/export/backup require an explicit
   bounded operation deadline.
6. Keep constructor visibility narrow. Later capability files should receive
   specific constructors such as a show or create request, not a generic
   escape hatch capable of injecting arbitrary flags.

**Files**:
- `internal/tracker/beads/command.go` — typed command model, validation,
  deterministic renderer, wire-shape and mutation metadata.
- `internal/tracker/beads/adapter.go` — minimal adapter composition needed to
  hold the runner/protocol dependencies; no business capabilities yet.

**Validation**:
- Table tests prove every supported command renders the exact expected argv.
- Invalid fields and flag combinations fail before a subprocess starts.
- Source tests or architecture guards prove there is no shell execution path,
  public raw-argv constructor, Beads module import, or Dolt SQL.
- Re-rendering the same typed request produces byte-for-byte identical argv.

### Subtask T011: Implement a bounded, cancelable process-group runner

**Purpose**: Ensure every provider invocation terminates within policy and
cannot exhaust memory, leak child processes, or inherit unrelated secrets.

**Steps**:

1. Define a narrow runner interface in `command.go` so protocol tests can use
   deterministic results while command tests exercise a real helper process.
   Return structured evidence: command kind, exit code when available,
   duration, start state, bounded stream metadata, and cancellation cause.
2. Require an absolute executable path and explicit working directory. Accept
   the canonical workspace only as a validated request field; WP04 will decide
   how it is resolved. Set `BEADS_DIR`, `BD_JSON_ENVELOPE=1`, and
   `BD_NON_INTERACTIVE=1` explicitly for every invocation.
3. Build a sanitized child environment from a small named allowlist rather
   than forwarding the whole parent environment. Include only OS necessities
   and binding-selected Beads/Dolt credentials. Sort keys for deterministic
   tests, reject embedded NULs, and ensure diagnostics expose variable names at
   most—never values.
4. Capture stdout and stderr independently with strict defaults: 4 MiB for
   ordinary stdout and 1 MiB for stderr. Continue draining safely after the
   logical limit so a noisy child cannot deadlock; mark overflow and discard
   excess bytes without allocating in proportion to provider output.
5. On POSIX hosts, start the command in its own process group. On deadline or
   caller cancellation, request termination, wait only a bounded grace period,
   then force-kill the entire group and reap the process. Structure platform
   details so unsupported hosts fail clearly or can gain a dedicated file in a
   later ownership-approved package; do not silently kill only the parent.
6. Preserve whether execution failed before start or after the process began.
   Spawn/validation failure is `not_started`. Once a mutating command starts,
   timeout, cancellation, signal, truncation, malformed response, or transport
   loss is `ambiguous`, regardless of what the local exit path guesses.
7. Ensure duration and classification are safe evidence, but command argument
   payloads and captured streams are not included in routine error strings.

**Files**:
- `internal/tracker/beads/command.go` — runner, bounded capture, environment,
  process lifecycle, and structured command result.
- `internal/tracker/beads/command_test.go` — real subprocess/process-group,
  timeout, overflow, environment, and mutation-classification tests.

**Validation**:
- A helper child that forks a descendant is completely terminated on timeout.
- Infinite stdout and stderr producers remain memory-bounded and are reaped.
- Cancellation before spawn classifies a mutation as `not_started`; timeout
  after observed process start classifies it as `ambiguous`.
- Tests inspect the child environment and prove unrelated secret-shaped parent
  values are absent while required fixed protocol variables are present.
- Run command tests under `go test -race` and repeat the timeout/process-group
  cases to expose goroutine, pipe, and reap races.

### Subtask T012: Decode schema-1 envelopes and documented JSON errors

**Purpose**: Turn the supported Beads automation wire format into precise typed
results while tolerating harmless evolution and ignoring non-semantic warnings.

**Steps**:

1. In `internal/tracker/beads/protocol.go`, implement a single-value envelope
   decoder for `{\"schema_version\":1,\"data\":...}`. Decode the top-level
   object strictly enough to require schema and data, but allow unknown optional
   fields inside provider payloads for compatible schema evolution.
2. Enforce the command's declared payload shape. Object commands reject arrays;
   array commands reject objects. Missing required top-level fields, duplicate
   JSON values, non-whitespace trailing stdout, invalid UTF-8 where relevant,
   and truncated or oversized capture are protocol failures.
3. Reject every schema other than exactly `1`, including zero, missing, and
   future values. Preserve the observed value in a safe typed diagnostic so
   operators can distinguish schema skew from malformed JSON.
4. Decode the documented JSON error object from stderr:
   `{\"schema_version\":1,\"error\":\"...\",\"code\":\"...\"}`. Stderr may
   contain warnings before or after it; locate at most one complete documented
   error value structurally rather than branching on warning text.
5. Treat zero exit plus a valid success envelope as success even when stderr
   contains warnings. Store only warning presence/count/truncation metadata in
   ordinary evidence, not warning bodies.
6. When exit is nonzero, prefer a valid documented error object for stable
   translation. Multiple competing error objects, invalid schema, truncated
   stderr, or prose-only stderr do not become guessed provider codes; return a
   malformed or ambiguous transport result according to command start/mutation
   state.

**Files**:
- `internal/tracker/beads/protocol.go` — envelope, error, and shape decoders.
- `internal/tracker/beads/protocol_test.go` — schema, warning, shape, trailing
  data, unknown-field, and malformed error tests.

**Validation**:
- Known object and array payloads decode from schema-1 envelopes.
- Unknown optional fields do not break a known payload.
- Warning text never selects an error kind and is never returned as evidence.
- Missing/future schema, shape mismatch, duplicate values, and trailing prose
  produce stable typed failure without panic or partial success.

### Subtask T013: Add a separate bounded JSONL streaming decoder

**Purpose**: Support portable export's record stream without weakening the
single-envelope contract used by ordinary commands.

**Steps**:

1. Add a JSONL decoder in `protocol.go` that is selected only for a command
   declared with `ExpectedShapeJSONL`. Do not make the ordinary envelope parser
   accept multiple top-level values or line-oriented fallback.
2. Stream records to a callback or writer-oriented consumer so the decoder does
   not retain an entire export in memory. The API must honor context
   cancellation between reads and before delivering each record.
3. Enforce the contract defaults independently: 2 MiB maximum per record and
   256 MiB aggregate. Avoid `bufio.Scanner`'s implicit token behavior unless
   its maximum is set explicitly and boundary cases are proven.
4. Define blank-line policy explicitly and test it. Reject a partial final
   record, malformed JSON, an oversized record, aggregate overflow, or consumer
   cancellation with a stable export/protocol diagnostic.
5. Preserve record order exactly. Decode only provider JSON values needed by
   later export work; never label JSONL interchange as a restorable backup.
6. Make error offsets diagnostic-safe (record number and byte count are safe;
   record bodies are not). Do not echo malformed content into errors.

**Files**:

- `internal/tracker/beads/protocol.go` — dedicated streaming JSONL path and
  limit accounting.
- `internal/tracker/beads/protocol_test.go` — record, boundary, cancellation,
  malformed, partial, and aggregate-limit coverage.

**Validation**:

- A multi-record stream arrives in order without aggregate buffering.
- Exactly-at-limit records succeed; one-byte-over records fail deterministically.
- An envelope parser rejects JSONL and a JSONL parser never treats an envelope
  as the ordinary single-command success contract.
- Cancellation stops delivery promptly and produces no leaked goroutine.

### Subtask T014: Translate failures, ambiguity, version, and schema stably

**Purpose**: Expose provider-neutral error semantics that application services
can branch on without inspecting Beads text, Go error strings, or exit codes.

**Steps**:

1. In `internal/tracker/beads/errors.go`, map validation, spawn, timeout,
   cancellation, process exit, output overflow, malformed protocol, documented
   provider code, and schema/version incompatibility into the portable error
   vocabulary established by WP01.
2. Use stable kinds and recovery codes for at least `unavailable`, `timeout`,
   `ambiguous`, `version_skew`, `schema_skew`, `malformed_protocol`,
   `not_found`, and `invalid_request`. Preserve safe causal wrapping for
   debugging without making message strings contractual.
3. Keep version negotiation representable even though WP04 performs the actual
   inspect flow. Parse the executable identity payload needed to identify
   exactly Beads `1.2.2`; reject invalid versions and make every other version
   an explicit `version_skew`, not a semver-range guess.
4. Carry command kind, exit classification, mutation outcome, schema/version
   observations, duration, truncation flags, and stable recovery guidance.
   Never attach stdout, stderr, environment values, descriptions, comments, or
   full argv to the exported diagnostic.
5. Define ambiguity monotonically: once a mutating process started and the
   response became inconclusive, lower layers cannot downgrade the outcome to
   `not_applied`. Only later independent provider observation may resolve it.
6. Make `errors.Is`/`errors.As` or equivalent typed inspection reliable so
   callers never parse human messages. Ensure schema and version errors remain
   distinguishable even when nested beneath a command result.

**Files**:

- `internal/tracker/beads/errors.go` — adapter-specific causes and portable
  diagnostic translation.
- `internal/tracker/beads/errors_test.go` — exhaustive classification,
  wrapping, redaction, and ambiguity monotonicity tests.
- `internal/tracker/beads/adapter.go` — expose protocol compatibility constants
  and wire runner/translator without adding discovery logic.

**Validation**:

- Every transport/protocol failure maps to exactly one stable kind and recovery
  code without inspecting message prose.
- Supported version/schema constants are exactly `1.2.2` and `1`.
- Mutation ambiguity cannot be accidentally converted to retry-safe failure.
- Secret-shaped sentinel values placed in env, stdout, stderr, and payloads do
  not appear in error strings or diagnostic serialization.

### Subtask T015: Build an exhaustive adversarial protocol test matrix

**Purpose**: Prove the command boundary against truthful, malformed, slow, and
ambiguous subprocess behavior before production capability code depends on it.

**Steps**:

1. Build table-driven tests using only the owned `_test.go` files. Prefer a
   test helper process launched from the test binary over committed shell
   scripts or fixtures outside ownership. Configure it through non-secret test
   variables or typed flags to emit precise protocol cases.
2. Cover valid schema-1 object and array envelopes; documented JSON errors;
   successful responses with stderr warnings; ignored unknown optional fields;
   missing fields; future schema; malformed JSON; truncated object/error;
   duplicate JSON values; trailing prose; wrong payload shape; and empty output.
3. Cover ordinary stdout and stderr overflow, JSONL per-record and aggregate
   overflow, boundary-equal sizes, cancellation during streaming, and a child
   that never closes output.
4. Cover missing executable, permission/spawn failure where portable, nonzero
   exit with and without valid JSON error, timeout before process start,
   timeout after a read starts, and timeout after a mutating helper records that
   it applied the operation but before it writes its response.
5. For timeout-after-mutation, prove the result is `ambiguous` and contains no
   assertion that the operation was unapplied. For timeout-before-start, prove
   `not_started` and no helper-side mutation marker exists.
6. Add negative tests that human-formatted success/error prose never decodes,
   warning wording changes do not change classification, arbitrary argv cannot
   be injected, and secret sentinels never enter errors or test evidence.
7. Drive protocol decoder branch coverage to 100% as required by NFR-007. Use
   coverage tooling to find genuine missing cases; do not add unreachable code
   or exclude meaningful branches merely to satisfy the number.
8. Run focused tests repeatedly and under the race detector. Keep individual
   deadlines short and deterministic so the package remains suitable for the
   bounded PR campaign.

**Files**:

- `internal/tracker/beads/command_test.go` — typed rendering, process lifecycle,
  environment, bounding, timeout-before/after-mutation, and process-group tests.
- `internal/tracker/beads/protocol_test.go` — exhaustive envelope/error/JSONL
  decoding and branch-coverage cases.
- `internal/tracker/beads/errors_test.go` — stable translation, ambiguity, typed
  inspection, and redaction cases.

**Validation**:

Run at minimum:

```bash
go test ./internal/tracker/beads
go test -race ./internal/tracker/beads
go test -count=20 ./internal/tracker/beads
go test -coverprofile=/tmp/wp03-cover.out ./internal/tracker/beads
go tool cover -func=/tmp/wp03-cover.out
go vet ./internal/tracker/beads
```

The protocol decoder's reported branch coverage must satisfy NFR-007. The
package tests must remain bounded, leave no child process alive, create no
repository files, and require neither an installed `bd` binary nor network
access.

## Test Strategy

Use three test levels within the owned files:

1. Pure table tests for command validation/rendering, envelope/error/JSONL
   decoding, classification, version/schema checks, and redaction.
2. Helper-process tests for real stdout/stderr pipes, exit states, cancellation,
   timeout, process groups, and timeout-after-mutation ambiguity.
3. Adapter composition tests proving the runner and decoder can be replaced by
   narrow fakes without exposing raw argv or transferring authority.

Tests must be deterministic under `-race`, repeated execution, and parallel
package scheduling. Use caller-controlled contexts and bounded eventually-style
polling rather than sleeps as correctness evidence. Temporary paths must come
from the test framework and must not depend on the repository working tree.

The exhaustive matrix must explicitly include: object, array, documented error,
warning-plus-success, unknown optional fields, malformed JSON, truncated
streams, oversized streams, future schema, timeout before mutation, and timeout
after mutation. No test may make human-output parsing an accepted fallback.

WP03 does not require a real Beads installation. Real-provider command-shape
qualification belongs to a later package; this package proves the generic
protocol boundary deterministically.

## Definition of Done

- The seven exact owned files implement a closed typed command seam, bounded
  process-group runner, schema-1 envelope/error decoder, independent JSONL
  decoder, stable error translator, and exhaustive protocol tests.
- Only Beads v1.2.2 and schema 1 are represented as supported compatibility.
- All subprocesses use explicit executable/workspace/cwd, sanitized environment,
  direct argv, deadlines, bounded streams, and whole-process-group termination.
- No production API accepts arbitrary flags or shell text.
- No production or test code imports Beads packages or reads Dolt directly.
- Successful warning-bearing responses remain successful without warning-prose
  branching; human-formatted output is always rejected as protocol data.
- Mutating timeout/cancellation/truncation after start is always ambiguous;
  pre-start failure remains distinguishable.
- Diagnostics are typed, carry stable recovery codes, and contain no payload
  bodies, environment secrets, raw stderr/stdout, or unredacted transcripts.
- Ordinary and JSONL output limits are enforced at and beyond boundaries.
- The protocol test matrix covers every required NFR-007 class and reports 100%
  branch coverage for the protocol decoder.
- `go test`, repeated tests, race tests, vet, and the repository's applicable
  bounded architecture/static gates pass.
- No file outside `owned_files` changes. If an unavoidable dependency mismatch
  is discovered, stop and report it instead of editing another WP's surface.
- Record each completed subtask through the event log; do not edit checkbox
  state in this prompt:

```bash
spec-kitty agent tasks mark-status T010 --status done
spec-kitty agent tasks mark-status T011 --status done
spec-kitty agent tasks mark-status T012 --status done
spec-kitty agent tasks mark-status T013 --status done
spec-kitty agent tasks mark-status T014 --status done
spec-kitty agent tasks mark-status T015 --status done
```

## Risks

- **Zombie descendants**: `exec.CommandContext` alone may kill only the direct
  child. Mitigate with explicit process-group lifecycle and a descendant test.
- **Pipe deadlock or unbounded allocation**: stopping reads at the logical limit
  can block child exit. Continue draining into bounded discard while recording
  overflow and test infinite/noisy producers.
- **False retry safety**: an applied mutation can time out before response.
  Make ambiguity monotonic from process start and require later observation.
- **Warning-text coupling**: stderr may mix warnings with a JSON error. Locate a
  structurally valid error object; never branch on prose or persist its body.
- **Decoder permissiveness**: treating JSONL, trailing prose, or multiple values
  as ordinary success weakens the stable envelope contract. Keep decoders
  separate and reject trailing data.
- **Secret leakage**: argv and streams may contain user content or credentials.
  Whitelist environment, expose metadata only, and use sentinel redaction tests.
- **Platform behavior**: process groups differ across operating systems. Meet
  the first-release POSIX target explicitly and fail honestly elsewhere rather
  than claiming descendant fencing that is not implemented.
- **Scope creep**: discovery, provider health, locking, catalog semantics, and
  saga recovery are adjacent but separately owned. Provide only the protocol
  primitives they require and leave their policies to WP04 and later.

## Reviewer Guidance

Review this package as a hostile process boundary, not merely a JSON utility.
Begin with the timeout-after-apply case: if any path calls a started mutation
`not_applied` without independent observation, reject the package. Then verify
that cancellation reaps descendants, bounded capture cannot deadlock, the
ordinary decoder accepts exactly one envelope, and JSONL is isolated.

Inspect every exported or package-visible construction path for raw argv,
shell interpolation, unconstrained environment inheritance, and accidental
payload logging. Search production code for Beads imports, Dolt/SQL access,
`sh -c`, human-output matching, stderr substring tests, and fallback parsing.
Any such path violates the mission even if happy-path tests pass.

Confirm that unknown optional payload fields are tolerated while missing
top-level contract fields and every non-1 schema are rejected. Check that
warning-bearing zero-exit success is not converted into failure and that
prose-only nonzero output does not acquire a guessed semantic code.

Finally, run the focused suite under the race detector and repeated execution,
inspect branch coverage for `protocol.go`, and confirm no child processes or
temporary repository artifacts survive. Reviewer approval should cite evidence
for object, array, error, unknown, malformed, truncated, oversized, future
schema, timeout-before-mutation, and timeout-after-mutation cases, plus the
negative proof that no human-formatted output is parsed.
