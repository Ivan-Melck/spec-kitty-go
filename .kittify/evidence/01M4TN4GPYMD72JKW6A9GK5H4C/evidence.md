# Newest-main behavioral parity

Status: **baseline captured; native-boundary and fresh-observer corrections
implemented; full workflow/CLI/API parity not delivered**.

## Confirmed scope and immutable reference

The requester confirmed parity with the main implementation's user-visible
workflows, CLI/API behavior, and compatibility, while preserving the Go-native
architecture rather than copying Python internals. They additionally required
**newest upstream main**, not the existing local Python checkout.

Official repository: `https://github.com/spec-kitty/spec-kitty`.

Fetched `origin/main` on 2026-10-13 and pinned:

```text
7d7cb16dc65fa3a8fd3900aadbec76c5e2cd2b3f
fix(landing): let the plan delegation test's fake accept owned_checkout
```

The reference was materialized in a detached checkout at
`/tmp/spec-kitty-parity-reference`; the user's `../spec-kitty` working tree and
local `main` were not advanced. The redesign work is on
`parity/latest-upstream-main`. These local paths are conveniences, not identity.
A later upstream advance requires explicit recapture and reconciliation; this
record does not claim to track a moving branch indefinitely.

[Machine inventory](upstream-main.json) binds the exact source/tree, source
SHA-256 values, API version, allowed/forbidden API verbs and errors, and root
completion metadata. It is **source inventory, not executable conformance**.
Completion metadata is not proof that a command works or appears in runtime
help; hidden and deprecated registration need separate behavioral checks.

The original [Python inventory](../PYTHON_BEHAVIOR_INVENTORY.md) remains
historical behavioral evidence at `d060cff9...`; its authority laws are retained.
The [API 1.3 projection](../ORCHESTRATOR_API_1_3_PROJECTION.md) remains a
source-bound compatibility design, not the current upstream surface. Neither
historical record is rewritten or retroactively requalified here.

## Significant changes since the old compatibility inventory

- Upstream Orchestrator API is **1.15.0**, minimum provider **0.1.0**, with
  **27 verbs**, not the ten-verb API 1.3 inventory.
- Planning is a governed public journey: context, interviews, artifact reads
  and CAS submissions, validation, finalization, and current analysis.
  Scaffolding is not completed specification/planning.
- Native `next` distinguishes read-only query from explicit advancement;
  action selection includes analysis currency and API-authored-stage guards.
- Root `merge` is a failing migration stub directing users to `consolidate`;
  it is not a working alias. `agent mission merge` is a separate surface.
- Consolidation checks immutable reviewed heads and defaults remote freshness
  to enforcement. This is a compatibility tension with the historical
  network-free integration law, not permission to silently change Go authority.
- Profiles, action-scoped doctrine loading, standalone Ops, lifecycle
  administration, and setup tooling are public behaviors beyond the native
  authority kernel.

## Capability-gap map

**Kernel-only** means some relevant Go capability exists, not that the upstream
command is implemented or behaviorally compatible. No API 1.15 facade currently
ships. Exact wire and CLI compatibility therefore remain missing for all 27
API verbs, including those with kernel-only semantic support.

| Upstream API verbs | Go evidence / missing public behavior | Delivery dependency |
|---|---|---|
| `contract-version` | No API negotiation or upstream envelope. Native envelope is a different schema and must remain explicitly distinguishable. | M5 |
| `mission-state`, `list-ready`, `resolve-workspace` | Kernel-only progress, graph and workspace records; no upstream aggregate projection/resolution interface. Production readiness dependencies are unavailable. | M3 then M5 |
| `start-implementation`, `start-review` | Kernel-only leases/workspaces/candidates/independent review. Real agent/provider execution and reviewer-start composition are missing. | M3 then M5 |
| `transition`, `append-history` | Typed capability events exist. Generic transitions cannot be authoritative; translate safe requests to named capabilities or refuse explicitly. No public history-note adapter. | M3/M5 |
| `accept-mission`, `consolidate-mission` | Candidate-bound authority exists, but stack-neutral acceptance, real production Git integration, joined gates and public compatibility are absent. | M3/M4 then M5 |
| `specify`, `plan`, `tasks`, `check-prerequisites`, `record-analysis` | Software-dev method/graph/artifact types exist; no complete public planning journey or authoritative analysis-currency composition. | Scoped planning capabilities, then M5 |
| `open-decision`, `resolve-decision`, `defer-decision`, `cancel-decision`, `answer-decision` | Some typed readiness facts exist; no complete public decision/interview lifecycle. | Scoped application capabilities, then M5 |
| `design-status`, `artifact-read`, `artifact-submit`, `design-validate`, `design-context`, `interview-record` | Artifact/governance commitment types are not authoring/context/interview services. Missing CAS batches, lineage, validation and progressive doctrine resolution at the public boundary. | Scoped planning capabilities, then M5 |
| `next` | Legacy tested step evaluator has no production snapshot source. Native `drive` executes a supplied bounded plan, not the upstream control loop. | M3/M4 then M5 |

Other public families requiring separately executable parity witnesses:

| Family | Principal gap |
|---|---|
| Initialization/config/upgrade/doctor | Project bootstrap, asset installation, migrations and read-only diagnostics are not the native control-root initializer. No silent Python/legacy-state importer is permitted. |
| Mission types and administration | Only software-dev is statically implemented; research, plan, documentation, close/reopen/follow-up/archive and post-merge review/retrospective need explicit capability decisions. |
| Charter/profiles/Ops | No Go doctrine resolver, action-scoped loader, active-profile routing, dispatch/open/close invocation service, or corresponding CLI. |
| Agent integration | Harness installation/configuration, prompt/context/decision/tracer/verdict and session surfaces are missing. |
| Tracker | Native Hubnot descriptive operations exist, but upstream discovery/binding/mapping/sync UX is not equivalent. Diagnostic tracker `ready` is never execution authority. |
| Hosted/team | Auth, consent, sync/outbox and hosted services remain later consumers. Hidden upstream registration is not automatic admission or a reason to make hosted connectivity mandatory locally. |
| Projections and tooling | Materialization, event queries, glossary, documentation query and validation need non-authoritative adapters with explicit compatibility. |

## Contracts the implementation must test

Use upstream executable tests as behavioral oracles, then bind Go tests to the
same observable outcomes. Source reading alone is insufficient acceptance.
Paths below are relative to the pinned official reference:

| Contract | Source / upstream oracle |
|---|---|
| JSON envelope, policy, capability handshake and parser failures | `src/specify_cli/orchestrator_api/{commands,envelope}.py`; `tests/contract/test_orchestrator_api.py` |
| No implicit success or query mutation | `src/specify_cli/cli/commands/next_cmd.py`; `tests/contract/test_next_no_implicit_success.py` |
| Dependency-ready implementation and review cannot wedge each other | `src/runtime/next/decision.py`; `tests/next/test_next_dependency_wedge_5669.py` |
| Public planning journey, no manual filesystem bridge | `src/specify_cli/orchestrator_api/{design_phase,design_authoring,design_context}.py`; `tests/specify_cli/orchestrator_api/test_planning_client_journey.py` |
| Native and API next enforce current authoring/analysis | `tests/specify_cli/orchestrator_api/test_runtime_next.py` and planning journey above |
| Reviewed-head and freshness refusals before consolidation mutation | `tests/orchestrator_api/test_consolidate_mission_post_approval.py`; `tests/orchestrator_api/test_consolidate_mission_premutation_timing.py`; `tests/orchestrator_api/test_origin_freshness_envelopes.py` |
| Dispatch opens but never spawns a model; profile activation states | `tests/specify_cli/invocation/cli/test_dispatch.py`; `tests/specify_cli/invocation/cli/test_profiles_activation.py` |

The envelope must preserve all seven upstream fields, stable errors, timestamps,
correlation, policy/secret validation and exit semantics. A Go-native envelope
cannot be relabeled as upstream-compatible. API next must return bounded prompt
content and digest, not expose an upstream-shaped authoritative file path.

## First implemented slice: existing native command boundary

This is safety/observability groundwork, **not delivery of an upstream facade**.
No new architecture tier, event schema, dependency, transport, compatibility
success claim, or mission admission is introduced.

`internal/helpercmd/native_boundary_test.go` first demonstrated failures through
the pre-existing entry point. The corrections in `request.go` and `runner.go`:

1. Native operation/drive input uses the existing tracker wire validator rather
   than another parser. Duplicate keys (including escaped duplicates),
   case-folded structured fields, invalid UTF-8 and unpaired surrogates cannot
   disappear into a canonical request identity before refusal.
2. Mutation envelopes report the host's observed **post-command** projection
   coordinate, for both successful commands and failures with durable intent.
   This coordinate is observation, not proof that a command succeeded.
3. Drive records **every completed concurrent-wave outcome** before stopping on
   a failed member. A sibling's committed result is no longer omitted; later
   waves remain stopped. Drive is not an atomic batch and must never pretend
   that sibling effects were rolled back.
4. Owner shutdown failure retains command identity, committed results, drive
   outcomes and the refreshed coordinate while returning its typed diagnostic.
   Cleanup failure does not undo a committed command.

An independent code review found the shutdown-evidence loss and two inventory
risks: Git replacement objects could change source under the same reported SHA,
and an output path could overwrite the reference repository. All three were
reproduced before correction. Capture now disables Git replacements, rejects
output under the reference worktree/Git directories (including symlink aliases),
and atomically replaces output inodes so an outside hard link cannot truncate
reference source. Regression tests cover these cases.

Verification performed:

```text
New native regression tests against pre-fix code: FAIL (all five defect families)
GOTOOLCHAIN=go1.26.7 go test ./internal/helpercmd ./cmd/spec-kitty -count=1: PASS
GOTOOLCHAIN=go1.26.7 go test -race ./internal/helpercmd -count=1: PASS
python3 -m unittest discover -s scripts/parity -p 'test_*.py' -v: PASS (9 tests)
Pinned-source capture --check: PASS
GOTOOLCHAIN=go1.26.7 go test ./internal/testcampaign/m2 \
  -run '^TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound$' -count=1: PASS
```

Six targeted architecture guards also passed: current production dependency
graph, complete M2 guard activation, exact durable-codec round-trip, API 1.3
historical projection contract, documentation links/commands and kernel autonomy.
The complete architecture suite still has the namespace blocker below.

These are bounded source/test results; admitted-sidecar tests that skip are not
counted as real-runtime proof. Existing frozen M2 release evidence remains
bound to its original tuple, not these changes.

### Baseline failures, not hidden by the bounded pass

The initial complete `go test ./...` run with installed Go
`go1.27.2-X:nodwarf5` failed before any implementation changes:

- `internal/architecture`: `TestCIUsernsWrapperPreservesOrdinaryExec` could not
  create its namespace (`Invalid argument`). Host/namespace qualification
  remains blocked; no skip or weakened assertion was added.
- `internal/scenario`: executable smoke rejects skipped required proofs when
  admitted sidecar/authority-executable environment bindings are absent.
  Source-only package success is not a substitute.
- `internal/testcampaign/m2`: source-bound binary verification required
  **Go 1.26.7**, not the newly installed toolchain. The named round-trip test
  passes with that exact toolchain; the full campaign is not thereby qualified.

## Second implemented slice: fresh observation after publication

The previously unverified observer question was reproduced with the exact
production-admitted assertion-extension executable. Initialization alone was
observable, but fresh observations after one/two mission publications or a
checkpoint publication refused with `STARTUP_REFUSED: observation_coordinate`.
The epoch admission marker describes the recovered prefix at admission, not
live progress. Comparing it to every later projection incorrectly rejects
legitimate publications.

The correction preserves that distinction without weakening integrity:

- `productionruntime/observation.go` validates the immutable epoch record at
  the same commit revision as `runtime/current`, restores the current sealed
  journal projection, then rechecks the epoch and provider revision. A query
  reports the captured latest projection without rotating epochs or writing.
- `controljournal/v2/restore` can optionally prove a historical projection
  prefix, while still returning the latest sealed snapshot. Checkpoints newer
  than the required prefix are ineligible; bounded origin replay is allowed
  when necessary. Existing recovery budgets, terminal checks, publication
  membership, and witness validation remain active. An unavailable historical
  proof refuses rather than silently falling back to a weaker check.
- Independent review caught a digest-integrity bypass in the initial fix:
  checking the admission digest only at its original revision allowed a later
  metadata-only publication to hide consistently corrupted owner-record
  digests. That regression failed before correction. The observer now requires
  restore to derive and match the admission-prefix digest at every revision.
- The fresh public-binary witness also reproduced `operation inspect` returning
  `MISSION_NOT_FOUND` for a committed operation. Operation aggregates are
  host-scoped for global idempotency, so a mission-filtered projection lookup
  cannot establish membership. Host and observer now share a read-only typed
  inspector: immutable binding scope proves project/mission membership;
  binding/head semantic addresses, record revisions and operation IDs are
  checked against the captured coordinate before returning state. Context
  cancellation propagates. No provider keys, new durable schema, or mutation
  authority are exposed.

Given a committed native readiness creation, fresh CLI processes now return
`status`, mission `progress`, and `operation inspect` at the same committed
coordinate. Repeated queries preserve database, epoch, revision and projection
digest. Wrong-project, wrong-mission and missing-operation inspection refuses
without disclosing state. This witnesses the existing native surface, **not**
upstream planning/execution/review parity. Mission-wide operation enumeration
still needs separate membership projection work; fixing individual inspection
is not a claim that every progress family is complete.

### Executable and filesystem evidence

The exact binary was reproduced, not admitted by override:

```bash
GOBIN=/tmp/spec-kitty-admitted-reproduction/bin CGO_ENABLED=0 \\
GOTOOLCHAIN=go1.26.7 go install -trimpath \\
  github.com/spec-kitty/gapdb/cmd/gapdbd@v0.0.0-20260904151017-4d88094749e0
```

```text
Source: 4d88094749e0f6fdb38a4786708d920fb51966a1
Tree: a109220e3dae287e321fb37e904d3bea0802b911
Binary SHA-256: 947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad
Size: 5327126 bytes
```

The tuple matches the existing M1 assertion-extension admission/adoption
records. The newer GapDB source in `go.mod` is a different M2 qualification
candidate, not automatic production sidecar admission.

`/tmp` is tmpfs on this host; production correctly refuses it for runtime/data
roots. Four existing positive real-sidecar fixtures hard-coded `/tmp` and failed
with `unsafe_path` even with `TMPDIR` configured. They now honor `TMPDIR` via
`os.MkdirTemp("", ...)`, preserving every topology assertion. Both new and old
real-sidecar tests ran on `/home/user/.cache/spktmp` (btrfs); no filesystem gate
was bypassed.

### Verification

With both real-sidecar environment bindings set to that exact executable and
`TMPDIR` pointing to the admitted filesystem:

```bash
export TMPDIR=/home/user/.cache/spktmp
export SPEC_KITTY_WP13_GAPDBD=/tmp/spec-kitty-admitted-reproduction/bin/gapdbd
export SPEC_KITTY_WP15_GAPDBD="$SPEC_KITTY_WP13_GAPDBD"
GOTOOLCHAIN=go1.26.7 go test ./internal/controljournal/v2/... \\
  ./internal/productionruntime ./internal/helpercmd ./cmd/spec-kitty -count=1
GOTOOLCHAIN=go1.26.7 go test -race ./internal/productionruntime \\
  ./internal/helpercmd ./cmd/spec-kitty -count=1
```

Both runs passed, including the existing production-host mutant campaign and
actual real-sidecar host restart/domain-validation/public-CLI tests. Additional
prefix regressions cover origin, checkpoint boundary, suffix, latest revision,
wrong digests and future revisions without replacing the latest restore seal.
Observer refusals cover missing/corrupted/wrong-revision epoch anchors,
concurrent epoch replacement/publication, and admission digest mismatch before
and after metadata publication. Unit inspection tests also refuse misattributed
record addresses and future record revisions.

Targeted current import-policy, complete M2 activation, exact durable-codec,
foundation ownership, historical API 1.3, documentation and kernel-autonomy
architecture guards passed, as did `go vet` for all changed Go packages and
`git diff --check`. Independent follow-up review found no additional defects in
the prefix/observer/inspection changes reviewed. Subsequent defensive
record-attribution checks are separately covered by red/green unit tests.

A complete `go test ./...` attempt with Go 1.26.7 and the real-sidecar bindings
was stopped at the 180-second command limit; it has **no complete pass result**.
The isolated `TestCIUsernsWrapperPreservesOrdinaryExec` was rerun with that exact
toolchain and still failed with `namespace dispatch refused: [Errno 22] Invalid
argument`. Namespace and complete program qualification remain blocked; these
results do not transfer frozen M2 evidence or admit M3/M4/M5.

## Dependency order and stop rules

The existing [production program](../PRODUCTION_RUNTIME_PROGRAM.md) remains
canonical: M2 is adopted; M3/M4 require their remaining immutable entry evidence;
M5 requires accepted M3 **and** M4. This inventory does not admit them, create
new missions, alter the program manifest, or transfer frozen qualification.

Recommended next slices:

1. Retain the reproduced admitted sidecar and fresh-observer witnesses;
   establish namespace support and remaining qualification executable bindings
   before claiming complete program qualification.
2. Reconcile newest-main planning/decision/context requirements into bounded
   admitted capability work, preserving consumer-owned ports.
3. Complete M3 real execution/provider/Git composition and M4 neutral acceptance
   under their independent proof obligations.
4. Admit the M5 join; implement the current API 1.15 compatibility projection
   outside the canonical API 2.0 application semantics, including typed
   incompatibility when an unsafe request cannot be represented.
5. Black-box complete planning -> next -> implementation -> independent review
   -> acceptance -> consolidation -> closeout through CLI and API. Add
   disconnect/restart/idempotency, refusal and no-mutation variants.
6. Close remaining governance/setup/type/hosted/tooling families with explicit
   witnesses or recorded architectural dispositions. Missing surfaces cannot be
   reported as parity simply because help lists them.

When a source behavior conflicts with a retained Go law (generic force,
caller-minted lease/workspace authority, physical workspace reuse, remote
freshness/network assumptions), stop that slice and record the compatibility
choice. Do not relax the kernel, use mutable Markdown as authority, import
qualification tooling into production, or introduce a Python/SQLite fallback.

## Reproduce or reconcile the inventory

This developer-only tool reads Git objects, not the reference working tree,
imports no upstream Python code, and makes no network calls. Python is not a
production runtime dependency.

```bash
python3 scripts/parity/capture_upstream.py \
  --upstream /path/to/spec-kitty \
  --revision 7d7cb16dc65fa3a8fd3900aadbec76c5e2cd2b3f \
  --output docs/parity/upstream-main.json --check

python3 -m unittest discover -s scripts/parity -p 'test_*.py' -v
```

For a newer baseline, fetch official `origin/main`, select its full immutable
SHA, run capture without `--check`, and reconcile this gap map and compatibility
choices before extending implementation claims. The tool refuses mutable or
abbreviated revision selectors, missing source, silent snapshot drift and
outputs inside the reference repository. Git replacements cannot change the
object being characterized.
