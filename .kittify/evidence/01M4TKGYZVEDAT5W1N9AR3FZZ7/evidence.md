# Newest-main behavioral parity

Status: **baseline captured; first native-boundary corrections implemented;
full workflow/CLI/API parity not delivered**.

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

Potential fresh-observer behavior after mission writes also needs a real
admitted-sidecar regression. Source inspection raised a coordinate-consistency
question, but it is **unverified**, not a confirmed defect or a completed fix.

## Dependency order and stop rules

The existing [production program](../PRODUCTION_RUNTIME_PROGRAM.md) remains
canonical: M2 is adopted; M3/M4 require their remaining immutable entry evidence;
M5 requires accepted M3 **and** M4. This inventory does not admit them, create
new missions, alter the program manifest, or transfer frozen qualification.

Recommended next slices:

1. Establish the exact sidecar/authority executable and namespace test
   environment; verify fresh observation after actual mission mutation.
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
