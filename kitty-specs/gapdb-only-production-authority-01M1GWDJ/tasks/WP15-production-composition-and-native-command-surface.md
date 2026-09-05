---
work_package_id: WP15
title: Production Composition and Native Command Surface
dependencies:
- WP13
requirement_refs:
- FR-002
- FR-019
- FR-020
- FR-026
- FR-027
- FR-029
- FR-030
- FR-034
- FR-036
- NFR-010
- NFR-014
- C-002
- C-003
- C-007
- C-014
- C-015
- C-016
planning_base_branch: feat/gapdb-only-production-authority
merge_target_branch: feat/gapdb-only-production-authority
branch_strategy: Planning artifacts for this mission were generated on feat/gapdb-only-production-authority. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/gapdb-only-production-authority unless the human explicitly redirects the landing branch.
subtasks:
- T075
- T076
- T077
- T078
- T079
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/helpercmd/
create_intent:
- cmd/spec-kitty/main.go
- cmd/spec-kitty/main_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/helpercmd/**
- cmd/spk-phase1-helper/**
- cmd/spec-kitty/main.go
- cmd/spec-kitty/main_test.go
role: implementer
tags: []
tracker_refs: []
---

# Work Package Prompt: WP15 — Production Composition and Native Command Surface

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty agent profile list` and select the best match for this work package's `task_type` and `authoritative_surface`.

---

## Objective

Replace the remaining SQLite-shaped helper composition with a normal native
`spec-kitty` command surface backed exclusively by the production runtime host
completed in WP13.

The result must give humans and automation a useful local command interface for
typed commands, progress queries, status queries, and operation resumption
without exposing storage internals. The command surface must neither construct a
store directly nor accept a raw database path. It must enter through one
production host capability boundary, which owns root classification, GapDB
sidecar admission, recovery, runtime epoch establishment, and orderly shutdown.

This work package is an application-composition change, not a transport or
product-scope expansion. It does not implement HTTP, RPC, MCP, Team Kitty,
remote orchestration, real Beads orchestration, or the later stack-neutral
acceptance provider system.

## Context and Architectural Position

M2 establishes one production authority model:

1. the production runtime host owns the admitted GapDB sidecar;
2. authoritative history is restored into memory-primary projections;
3. domain capabilities operate through typed ports;
4. queries observe typed projections without minting authority; and
5. mutations are identified and recovered by stable `OperationID`.

The current `internal/helpercmd` surface predates that model. It imports
`internal/controlstore/sqlite`, exposes SQLite-specific response types, accepts
`--db`, and opens a database within individual commands. Those choices make the
command layer a second composition root and expose an obsolete backend decision
to every caller.

WP13 is the dependency because it provides the only supported production host
composition. Consume that host as a capability boundary. Do not duplicate its
sidecar admission, root classification, journal recovery, epoch, or shutdown
logic in this package.

The helper binary may remain as a bounded qualification compatibility entry
point, but it must delegate to the same typed runner and production host. It may
not retain a hidden SQLite path, a second set of repositories, or a permissive
fallback mode.

## Branch Strategy

- Planning base branch: `feat/gapdb-only-production-authority`.
- Merge target branch: `feat/gapdb-only-production-authority`.
- Obtain the implementation lane and worktree from
  `kitty-specs/gapdb-only-production-authority-01M1GWDJ/lanes.json`.
- Do not infer a lane from the work-package number.
- Do not create an ad hoc branch or worktree.
- Keep edits within the `owned_files` globs in frontmatter.
- Preserve concurrent work from other agents; do not reset, rewrite, or revert
  files outside this package's authority.

## Required Outcome

At completion, these statements must all be true:

- `internal/helpercmd` has no import or public type tied to SQLite.
- neither command binary accepts `--db`, a backend selector, or an eligibility
  selector;
- `cmd/spec-kitty` provides the bounded native command surface for M2;
- commands and queries enter through production host capabilities;
- status and progress are derived from typed projections;
- the native surface exposes autonomous `drive` over the full readiness census,
  not a fixed phase-command ladder;
- operation resume uses the original stable operation identity and typed input;
- historical or unsafe roots are refused before a sidecar or runtime is opened;
- black-box tests prove the help, configuration, restart, and refusal contracts;
  and
- no new transport, provider, or acceptance-execution subsystem is introduced.

## T075 — Replace SQLite Store and Raw `--db` Composition

Refactor `internal/helpercmd` and `cmd/spk-phase1-helper` so that the command
layer consumes a production host capability instead of constructing
`sqlite.Store` or repositories itself.

### Required implementation

1. Inventory every helper command, response, option, and setup path that names:
   - `sqlite.Store`;
   - any SQLite snapshot or repository type;
   - `--db` or an equivalent raw database pathname;
   - backend-selection or legacy-eligibility state; and
   - direct repository or sidecar construction.
2. Replace those dependencies with narrow production-host capabilities.
3. Keep command inputs and results provider-neutral and domain-typed.
4. Use one host lifecycle for an invocation. The command layer must not open
   multiple authorities to answer different subcommands.
5. Pass a bounded control-root identity through the supported production host
   configuration. A control root is not a database filename.
6. Ensure graceful completion performs the host's defined flush and close
   sequence. Error and signal paths must also release ownership predictably.
7. Convert `spk-phase1-helper` into a thin delegate to the shared typed runner,
   or retire obsolete subcommands where the mission contracts permit it.
8. Keep qualification-only fault injection visibly separate from the normal
   native surface. It must not become a production flag family.

### Prohibited implementation

- no direct `gapdb.Open` or repository construction in a leaf command;
- no replacement of `--db` with a differently named sidecar-file flag;
- no implicit SQLite fallback when host creation fails;
- no storage-provider interfaces on public command request types;
- no command-owned recovery or runtime-epoch algorithm; and
- no second runtime owner hidden behind compatibility code.

### Verification focus

Add focused composition tests using a host/capability test seam. Prove that one
command invocation acquires one host, calls only its declared capability, and
closes it exactly once. Prove that setup failures emit a typed diagnostic and do
not execute the requested mutation.

## T076 — Add the Normal Native `spec-kitty` Command Surface

Create the normal Go executable under `cmd/spec-kitty`. It must be a small
application adapter over `internal/helpercmd`, not a new domain layer.

### Command design

- Define a closed registry of commands supported by the completed M2
  capabilities.
- Use signal-aware contexts and injected stdin, stdout, and stderr so behavior
  is deterministic under tests.
- Provide a stable control-root option and a stable machine-output option.
- Prefer explicit product verbs over storage verbs.
- Keep mutation commands typed and operation-aware.
- Keep query commands observational and authority-free.
- Return stable process exit categories mapped from typed diagnostic kinds.
- Make `go build ./cmd/spec-kitty` work with `CGO_ENABLED=0`.

The surface may expose the mission, work, operation, and progress capabilities
already supported by the M2 host. It must not invent a generic transition or
arbitrary mutation command. Every mutation must map to a declared capability
and its closed request/result contract.

### Scope boundary

Do not add:

- HTTP listeners, RPC protocols, sockets, or a daemon protocol;
- MCP resources, tools, or task endpoints;
- Team Kitty synchronization;
- real Beads execution or tracker ownership;
- stack-specific acceptance command execution;
- a remote-agent protocol;
- a general event append command; or
- a `--force`, waiver, fallback, backend, or importer switch.

Those surfaces belong to later missions. This command is deliberately local and
native, while preserving typed results that can be projected into those future
surfaces.

## T077 — Typed Query, Progress, Status, and Operation Resume

Expose the smallest useful native observation and recovery surface over the
production host.

### Typed observation contract

Support typed queries sufficient to answer where a running mission or operation
is now. Results should include, where applicable:

- stable mission, work, run, and `OperationID` identities;
- current journal revision and runtime epoch;
- pinned work revision and execution generation;
- current operation stage and terminal result, if committed;
- active blockers or degraded-mode reason;
- all authorized and unavailable actions with typed reasons, derived by the
  kernel projection; and
- a typed diagnostic when the requested identity does not exist.

The command layer must not synthesize progress by scraping logs, Markdown task
lists, Git output, provider prose, or a human-oriented renderer. It serializes a
closed typed query result. An optional human presentation may be derived from
that result, but it must never be read back as input to automation.

### Autonomous drive

Expose a bounded local `drive` operation that repeatedly queries one exact
readiness census, schedules safe independent actions, and re-evaluates after
durable publication. It stops only at terminal state, a typed decision,
conflict, unavailable external dependency, cancellation, or explicit policy-
mandated human authority. It never advances a generic phase and never treats a
prompt completion claim as evidence.

### Machine output

Define a versioned, bounded JSON envelope for automation. Preserve stable field
meaning and use explicit discriminators for result and diagnostic variants.
Unknown or unsupported variants must fail closed. Bound collection sizes and
avoid exposing secret configuration, provider internals, raw event payloads, or
filesystem implementation details.

### Operation resume

Provide a resume path that:

1. receives the original `OperationID`;
2. receives or resolves the same canonical typed request identity/digest;
3. asks the production host to resolve committed result, ambiguous attempt, or
   safe re-entry under the operation protocol;
4. returns an already committed result exactly, even across restart;
5. rejects mismatched reuse of an `OperationID` as an idempotency conflict; and
6. refuses to invent predecessor mutation or mint a replacement operation when
   the necessary operation history is absent.

Resume is not “rerun the words the user typed.” It is recovery of a typed,
durably identified operation.

### Query purity

Tests must prove status, progress, inspect, and operation-query commands append
zero authoritative events, acquire no mutation lease, advance no fence, and
perform no external effect. Repeated queries against the same revision must be
semantically identical.

## T078 — Gate Host Startup on Historical Root Classification

The command surface must rely on the WP12 classifier before the production host
creates, opens, locks, repairs, or writes any sidecar state.

### Required startup order

1. Parse and validate the bounded control-root argument.
2. Classify the root through the production composition contract.
3. Continue only for the canonical `empty_supported` or `current_supported`
   dispositions.
4. For historical, mixed, malformed, ambiguous, or unsafe roots, return the
   typed `OFFLINE_DISPOSITION_REQUIRED` refusal.
5. Only after an admitted classification may the host acquire ownership and
   open the current GapDB sidecar.

Refusal must precede lock files, temporary files, journals, checkpoints,
directories, telemetry sidecars, or any other mutation. Preserve the
classifier's canonical evidence without attempting to interpret or repair it in
the CLI.

### Remove obsolete selection mechanisms

Delete raw database and backend selection from:

- command flags and positional arguments;
- environment variables;
- config decoding;
- help and examples;
- compatibility aliases; and
- test-only production paths.

Do the same for legacy eligibility selectors that allow users to opt into an
otherwise inadmissible backend or root. There is exactly one production
authority path. Historical data requires offline disposition; this WP adds no
waiver, importer, conversion, “prefer current,” or best-effort mode.

Add an ordering spy or equivalent test double that fails if any host/sidecar
operation occurs before classification acceptance.

## T079 — Black-Box Command and Restart Evidence

Build a black-box test suite that exercises the compiled commands, not merely
their internal functions.

### Required matrix

Cover at least:

1. `spec-kitty --help` and subcommand help expose only the supported closed
   option set.
2. `--db`, backend, eligibility, fallback, waiver, and import flags are rejected
   as unknown input.
3. Empty supported roots initialize through the production host.
4. Current supported roots recover and answer typed status after restart.
5. Historical, copied, renamed, mixed, malformed, ambiguous, and unsafe roots
   refuse before mutation with `OFFLINE_DISPOSITION_REQUIRED`.
6. Valid query output strictly decodes into the documented typed envelope.
7. Invalid input, missing identity, stale authority, degraded operation, and
   host-start failure map to stable typed diagnostics and exit categories.
8. A dropped acknowledgement followed by resume with the same operation
   identity returns the committed result rather than repeating the effect.
9. Reusing an operation identity with a different request is rejected.
10. Human-renderer wording changes cannot affect machine-mode behavior.
11. The compatibility helper, if retained, reaches the same composition and has
    no independent persistence path.
12. Both binaries build with `CGO_ENABLED=0` and no runtime SQLite requirement.

### Structural checks in this WP

Add focused source and help/config checks proving the owned command surfaces do
not contain direct SQL execution, SQLite imports, raw database flags, backend
fallbacks, or import modes. WP16 owns the repository-wide eradication and final
module/binary absence proof; do not duplicate its full responsibility here.

Run black-box tests in isolated temporary control roots. Do not inspect or alter
a developer's real control root. Use deterministic fixtures and bounded waits.
Exercise concurrent read-only status requests under the race detector to prove
the CLI adapter does not introduce mutable presentation state.

## Implementation Guidance

Keep `internal/helpercmd` organized around three boundaries:

- argument decoding into closed typed requests;
- invocation of a narrow production host capability; and
- rendering of a typed result or typed diagnostic.

Do not let parsing, composition, and domain behavior collapse into a single
large switch. A small command registry may bind names to request decoders and
capability calls, but it must not become a generic reflection-driven execution
framework.

Prefer dependency injection for process streams, clock-independent contexts,
and the production-host factory seam. Production wiring must still select only
the real WP13 host. Test seams must not be selectable from normal command-line
or configuration input.

Preserve operation identities from input to output. Preserve typed refusal
kinds rather than flattening them into strings. Never expose a success exit code
for a refused or ambiguous mutation.

## Implementation Command

Start the governed implementation action with:

```bash
spec-kitty agent action implement WP15 --agent codex --mission gapdb-only-production-authority-01M1GWDJ
```

Follow the lane and worktree instructions emitted by Spec Kitty. Do not perform
implementation on the planning branch unless the runtime explicitly assigns it.

## Verification Commands

Run focused checks first, then the relevant repository gates:

```bash
go test ./internal/helpercmd/... ./cmd/spk-phase1-helper/... ./cmd/spec-kitty/...
go test -race ./internal/helpercmd/...
CGO_ENABLED=0 go build ./cmd/spec-kitty ./cmd/spk-phase1-helper
go test ./...
go vet ./...
```

Also run the mission's applicable architecture, contract, and scope targets from
the repository `Makefile`. Record exact commands, exit status, and any bounded
qualification fixture used.

## Definition of Done

- [ ] T075 replaces SQLite-specific helper composition and raw `--db` input
      with production-host capabilities.
- [ ] T076 ships a bounded native `spec-kitty` executable with no transport or
      later-mission expansion.
- [ ] T077 exposes typed query/progress/status and exact operation resume without
      parsing prose.
- [ ] T078 enforces canonical root classification before all host mutation and
      removes backend/eligibility selectors.
- [ ] T079 supplies black-box help, config, restart, refusal, resume, build, and
      typed-diagnostic evidence.
- [ ] Both command binaries share one production composition and no SQLite
      authority path remains in the owned surfaces.
- [ ] Query purity and operation idempotency are covered by executable tests.
- [ ] Focused tests, race tests, static build checks, and repository gates pass.
- [ ] Changes stay within owned files and preserve concurrent work.
- [ ] Implementation evidence is attached to the governed WP action.

After all five subtasks and their evidence are complete, record their status
through the event-sourced task surface:

```bash
spec-kitty agent tasks mark-status T075 T076 T077 T078 T079 --status done --mission gapdb-only-production-authority-01M1GWDJ
```

Do not hand-edit task status fields in Markdown or YAML.

## Risks and Required Mitigations

### Split authority through convenience construction

A helper may appear simpler if it opens persistence directly. That would create
a second authority owner. Mitigate with composition spies and source guards that
permit production construction only through the WP13 host.

### Storage details leaking into the product interface

Renaming `--db` to `--journal` or `--sidecar` preserves the same architectural
leak. Accept a control root and product identities only; keep physical storage
private to the host.

### CLI becoming an accidental generic transition API

A flexible “set state” or “append event” command bypasses domain legality.
Expose closed capability verbs and reject unknown transitions.

### Resume accidentally repeats an external effect

Treat resume as operation-resolution, not command replay. Require the stable
operation identity and canonical request match; return the committed result
before considering re-entry.

### Root refusal occurring after mutation

Startup convenience can create locks or directories too early. Use an ordering
test seam and black-box filesystem snapshots to prove classification refusal is
zero-mutation.

### Human output becoming an automation contract

Keep machine JSON generated directly from typed results. Tests must vary human
wording and prove automation output and behavior are unchanged.

### Scope creep into M3, M4, M5, or M7

Do not add real tracker orchestration, acceptance execution, network API, MCP,
or Team Kitty behavior. Leave projection-friendly typed contracts, then stop at
the M2 boundary.

## Reviewer Guidance

Review this package as a production composition and contract boundary, not as a
cosmetic CLI refactor.

The reviewer should specifically verify:

1. no owned production file imports SQLite or constructs a persistence store;
2. the normal binary enters through the sole WP13 production host;
3. the compatibility helper cannot select a different authority path;
4. control-root classification completes before every filesystem or sidecar
   mutation;
5. no flag, environment value, config field, alias, or hidden default selects a
   backend, waiver, importer, or legacy eligibility path;
6. progress and status originate in typed projections and are query-pure;
7. operation resume preserves identity and cannot duplicate a committed effect;
8. machine output is a strict typed envelope independent of prose rendering;
9. exit categories distinguish success, refusal, invalid input, ambiguity, and
   unavailable/degraded operation;
10. black-box restart evidence proves the native surface recovers current state;
11. `CGO_ENABLED=0` builds succeed; and
12. no HTTP, RPC, MCP, Team Kitty, Beads-production, or acceptance-provider
    implementation slipped into this work package.

Reject the package if it merely hides `--db`, retains a direct store constructor
behind a factory, parses human output to recover typed state, or permits an
unsafe root to be mutated before refusal.
