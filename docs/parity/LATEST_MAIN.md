# Newest-main behavioral parity

Status: **baseline captured; native-boundary, fresh-observer and operation
membership and startup-construction corrections implemented; upstream refreshed;
full workflow/CLI/API parity not delivered**.

## Confirmed scope and immutable reference

The requester confirmed parity with the main implementation's user-visible
workflows, CLI/API behavior, and compatibility, while preserving the Go-native
architecture rather than copying Python internals. They additionally required
**newest upstream main**, not the existing local Python checkout.

Official repository: `https://github.com/spec-kitty/spec-kitty`.

Initially fetched `origin/main` on 2026-10-13 at
`7d7cb16dc65fa3a8fd3900aadbec76c5e2cd2b3f`; its exact inventory is preserved in
[the initial snapshot](upstream-main-7d7cb16.json). A later official-main check
on the same day detected an advance to `4e7c538d3d1f21044b127eccd49061a4cb9f451c`;
[that capture](upstream-main-4e7c538.json) is retained. A further official-main
check during continuation advanced the current reconciled source baseline to:

```text
d0c6ca61d87f1d92512bb45386351aae3be1eba5
docs(landing): accept the event-ledger ADR and fold landing review
```

The most recent official-main check advanced again to **current baseline**
`8c89aa90fe3d51f8f406158de0a30b98ac4a7ab0`, tree
`8f65a91977261236544947002546aa00d83a2920`:
`fix(ownership): infer valid surfaces for mixed paths and literal files`.
Its detached reference is `/tmp/spec-kitty-parity-reference-8c89aa9`.
The d0c6ca61 inventory is now separately preserved in
[its immutable capture](upstream-main-d0c6ca6.json); the current inventory is
regenerated and checked at 8c89aa90. See the
[current reconciliation](UPSTREAM_RECONCILIATION_8C89AA9.md) for the 72-file
(+1395/-301) interval, landed task-status/grammar/ownership/stage/warning changes,
and named inspected—not executed—upstream oracles. API 1.15.0 / 27 verbs / 62
non-synthetic root registrations remain unchanged. Installed Spec Kitty reports
4.0.0rc6; that version label is not executable conformance to the captured tree.

The initial reference was materialized in a detached checkout at
`/tmp/spec-kitty-parity-reference`; the user's `../spec-kitty` working tree and
local `main` were not advanced. The redesign work is on
`parity/latest-upstream-main`. These local paths are conveniences, not identity.
The later baselines are separately detached at
`/tmp/spec-kitty-parity-reference-4e7c538` and
`/tmp/spec-kitty-parity-reference-d0c6ca6`; all reference checkouts remain.
The user's checkout/local main still resolve to
`bcb80f5230bd001ac8bebce25bb70788f9a32438`. A later upstream advance requires
explicit recapture and reconciliation; this record does not claim to track a
moving branch indefinitely. See the [4e7c538 reconciliation](UPSTREAM_RECONCILIATION_4E7C538.md)
and [current d0c6ca61 reconciliation](UPSTREAM_RECONCILIATION_D0C6CA6.md).

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

The further d0c6ca61 baseline adds non-destructive malformed/non-UTF-8 TOML
refusals, clean moments on/off errors, checkout-bound isolated environments,
runtime-classified command-skill regeneration/reporting/pruning and CI consumer
routing. Its accepted default-on event-ledger/local-retention ADR is **not yet
implemented** and does not opt Go into hosted behavior or pruning committed
history. These remain public setup/tooling/projection parity obligations, not
native product delivery. The API/command inventory is unchanged apart from
source/tree identity; see the current reconciliation above for exact oracles.

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
At this slice, the complete architecture suite retained the failure below;
[the later namespace portability correction](USERNS_PORTABILITY.md) resolves
that local prerequisite, not complete candidate qualification.

These are bounded source/test results; admitted-sidecar tests that skip are not
counted as real-runtime proof. Existing frozen M2 release evidence remains
bound to its original tuple, not these changes.

### Baseline failures, not hidden by the bounded pass

The initial complete `go test ./...` run with installed Go
`go1.27.2-X:nodwarf5` failed before any implementation changes:

- `internal/architecture`: `TestCIUsernsWrapperPreservesOrdinaryExec` returned
  `Invalid argument`. The initial namespace-creation diagnosis was incorrect:
  the process-label read failed on a non-label-LSM kernel. The later
  [correction](USERNS_PORTABILITY.md) passes real namespaces and `strace` on this
  host; no skip or weakened isolation/profile assertion was added.
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
upstream planning/execution/review parity. At this slice, individual inspection
was fixed but mission-wide operation enumeration remained missing; the next
slice below closes that specific projection gap, not every progress family.

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
argument`. This was historical label-read failure, subsequently corrected;
complete program qualification remains unresolved. These results do not transfer
frozen M2 evidence or admit M3/M4/M5.

## Third implemented slice: mission operation membership

The existing fresh-process public CLI test was strengthened to compare the
mission's operation census with individual inspection. It failed through the
stable `progress` entry point: `operations=[]` despite a committed and separately
inspectable readiness operation. Host-scoped aggregate identity cannot be used
as mission membership, and copying nearby event scope or parsing operation IDs
would invent attribution.

The correction derives membership from the single canonical operation binding:

- Restore returns opaque, copy-only `OperationMembershipV2` values from its
  already-validated bindings, in deterministic operation-ID order. Identity,
  scope, count and ordering are length-framed into the in-memory restore seal.
- Projection restores those scopes from the sealed result, or installs the
  creating binding's scope from a closed acknowledged publication. Continuations
  require the existing immutable scope; conflicting scopes refuse before any
  state/index publication. State and membership become visible under one lock.
- Project, mission and work indexes, list filters and pagination share one
  membership resolver. Same-revision restoration compares membership too.
  Operation aggregate coordinates remain **host-scoped**, retaining global
  idempotency. No durable codec, event schema, flat projection digest, CLI/API
  envelope, dependency, mission admission or authority law is changed.

Native mission progress now includes its bound operations, including after
fresh observation and checkpoint publication, without including another
mission's operations or host-only checkpoint operations. The projection remains
memory-primary. Individual typed inspection deliberately also supports
standalone operations without a mission head; both consume the same immutable
binding facts, not two membership authorities.

Verification on Go 1.26.7 with the exact admitted executable/filesystem above:

- Projection and restore suites passed. Added scoped pagination, same mission ID
  across projects, work filtering, host-operation exclusion, and defensive-copy/
  seal-tampering tests. Reverting list filtering to host coordinates and omitting
  membership from the restore seal each failed at behavioral assertions under
  temporary Go overlays; no production source was replaced for those probes.
- Serialized `go test -p 1 ./internal/controljournal/v2/... ./internal/projection
  ./internal/productionruntime ./internal/helpercmd ./cmd/spec-kitty -count=1`
  passed with both real-sidecar environment bindings, including live/restarted
  mission isolation and fresh CLI census-to-inspection agreement.
- Race suites for projection, productionruntime, helpercmd and CLI passed;
  focused restore membership/prefix race tests passed.
- Eight targeted architecture guards (including foundation ownership and absence
  of generic raw-key authority), changed-package `go vet`, and
  `git diff --check` passed. Independent review found no defects in the membership
  delta.

**Verification is not universally green.** A concurrent normal package run
produced an intermittent native CLI `COMMAND_UNAVAILABLE`; a separate
30-iteration direct-runtime experiment failed an executable-identity startup
check **before journal restoration or mission mutation**. The serialized/race
passes do not erase either failure or establish a common cause. The open
[finding](SIDECAR_STARTUP_FINDING.md) records evidence, reproduction and the next
investigation boundary. Lifecycle identity protection was not weakened, and no
retry, sleep, skip, binary substitution or admission override was introduced.
Full-suite/program qualification and upstream workflow parity remain incomplete.

M3/M4 still have no recorded mission admission. The existing
[Hubnot qualification tuple](../HUBNOT_QUALIFICATION.md) is pinned; its existence
is not an M3 entry decision. At that slice, the conditional M3 permission document still awaited confirmation.
The requester has since [confirmed delegation and entry preparation](../programs/decisions/2026-10-13-m3-m4-entry-authorization.json);
[M3](../programs/m3/CANDIDATE_SPEC.md) and [M4](../programs/m4/CANDIDATE_SPEC.md)
candidate specifications are now prepared, not automatically admitted. Future execution/acceptance capability
work must resolve the actual immutable entry decisions, not claim these tuples
are absent or silently start the M5 join.

## Fourth implemented slice: startup construction and archived admission

A deterministic stable-`Start` regression failed before correction: a pre-endpoint
image observation returned the inherited supervisor image and was misclassified
as an executed-image substitution. Linux v7.2.9 wakes the vfork parent before
installing the new process mm; Go 1.26.7's exec-error-pipe handshake is not a
`/proc/PID/exe` ordering barrier. This supported mechanism is not captured proof
of the earlier intermittent iteration's exact cause.

Startup now waits for the configured endpoint as an ordering trigger, then
establishes exact image/start-ticks/pidfd identity once, before any response.
Image failures refuse immediately without retry. Socket presence grants no
ownership: peer/connected endpoint/socket-before-and-after/protocol checks remain.
Process/image/pidfd and the sole Wait goroutine are published together; before
that transition rollback terminates/reaps only the Go-owned child. Existing
post-attribution rollback limitations remain. No new sleep or identity allowance
was introduced. Cancellation, metadata-failure pidfd closure/reap, and persistent
wrong-image refusal have real-child regressions.

Enabling the historical M1 executable tests exposed a separate pre-existing
parser defect: its exact pinned admission was rejected because the strict DTO
omitted the archive's metadata fields. The DTO now explicitly names that archived
shape, keeping hash verification before decoding, strict unknown-field rejection
at authority-bearing levels, and unchanged exact tuple validation. Opaque signed
metadata is not interpreted as authority. An unconditional pinned-archive test
fails against the old decoder via Go overlay and rejects unknown authority keys.
No admission/evidence bytes or expected hashes changed.

The original M1 executable was independently rebuilt from its recorded recipe:

```text
Module: v0.0.0-20260824033134-ff3dd478f87c
Toolchain: go1.26.7; CGO_ENABLED=0; go install -trimpath
SHA-256: 62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e
```

Lifecycle fixture roots now honor `TMPDIR`; two raw-exec socket fixtures use
short private roots to preserve the Unix socket path bound on the longer btrfs
path. These are fixture corrections, not topology exceptions.

Verification with both exact binaries and btrfs roots:

- Complete sidecar lifecycle/provenance suite passed, exercising existing socket
  replacement, foreign ownership, PID reuse, descriptor swap/source mutation,
  restart, cleanup and response-drop cases rather than skipping them.
- New deterministic transition/wrong-image/cancellation/metadata regressions and
  32 plain instrumented starts passed. A separate 30-iteration fresh-observer
  experiment passed. Earlier failed attempts remain retained.
- Race-enabled sidecar, productionruntime, helpercmd and public CLI packages
  passed. Independent startup and archive-decoder reviewers found no actionable
  regressions in their respective deltas.
- These results are not current-branch program qualification. The historical
  [startup finding](SIDECAR_STARTUP_FINDING.md) remains open at the causation and
  complete-qualification boundary.

A later complete Go 1.26.7 run with exact original/extension binaries finished
with exit 1 (45 packages passing, seven failing, two without tests; 24 top-level
failures). It was not stopped at a timeout. Namespace refusal, further fixture
/tmpfs problems, the capture module's unused shebang being parsed as shell,
source-bound fault-runner constraints and missing mandatory executable proofs
were recorded. Six fresh fixture roots were individually corrected; the unused
shebang was removed while explicit `python3` invocation stayed unchanged. The
unmodified SQLite-absence guard then passed with no findings, and both publication
restart-cut cases passed. Focused/scenario qualifications still fail.

Activating the old M1 response-drop witness also exposed current connection
reuse versus a frozen single-exchange proxy: it refuses before the selected
atomic drop, not a proven journal durability defect. Independent investigation
supports that causal source chain; it remains a separately tracked compatibility/
qualification issue, not a reason to alter frozen evidence or disable reuse.
The plain M2 source install did not reproduce its qualification artifact digest
and was never substituted. The [exact artifact has since been recovered](M2_ARTIFACT_RECOVERY.md)
from the adoption-bound original release using existing authorized access;
artifact availability/identity is resolved without proof transfer. See [typed findings](QUALIFICATION_FINDINGS.md),
[admission readiness](ADMISSION_READINESS.md) and the
[prompt-to-artifact audit](COMPLETION_AUDIT.md) for precise stop/go inputs.
The complete-suite failure is not erased by later targeted passes.

## Fifth implemented slice: namespace evidence portability

The requester installed `strace` and authorized continuing. Real direct probes
proved user/network/PID namespaces already worked; the wrapper/prerequisite
failed on `/proc/self/attr/current` EINVAL. Both now share one bounded CI-only
label interpreter: only EINVAL with a closed-known non-label LSM stack produces
explicit null/not-applicable evidence. Unknown/unreadable stacks and
AppArmor/SELinux label failures refuse; selected dispatch still requires and
rechecks its exact profile label. No privilege administration or production
boundary changed.

Nine namespace tests passed, including real child PID 1, distinct namespaces,
ordinary-user mappings, denied setgroups, loopback-only interfaces and actual
`strace`; four additional architecture/absence guards passed unchanged.
Independent review found no actionable regressions. [Details and retained red
witnesses](USERNS_PORTABILITY.md) distinguish disposable hosted-state fixtures
from actual hosted-runner attestation. The subsequent complete development run
with exact distinct artifact bindings finished exit 1 (47 packages passing,
five failing, two without tests; 22 top-level failures). Native progress encoding
refusals are newly reached alongside proxy/candidate/source-bound failures.
Final cache-free source loads passed separate focused guards. The dirty/evolving
tree is not an immutable qualification candidate; no universal pass or adoption
transfer is claimed.

## Sixth implemented slice: complete query snapshot boundary

[Progress snapshots now own a bounded query contract](PROGRESS_SNAPSHOT_BOUNDARY.md),
not the 256 KiB durable event/reducer payload limit. The 16 MiB returned limit
includes the digest and charges complete classified membership before copies.
The consumer-owned typed DTO preserves the legacy canonical SHA-256 preimage,
exact uint64 values and schema; journal authority limits remain unchanged.

Unit/race, runtime/helper/CLI packages, six unchanged architecture guards and
independent review pass. Real publication-cut and outbox-performance scenarios
pass. Restart/real-Git scenarios reach completed/restored state, then fail a
separate terminal-item equivalence assertion. Diagnostic two-work histories have
46 identical shared items and ten extra acknowledged execution-rebind operations
in the restarted trace. No operations were hidden to declare that comparison
successful. The [typed finding 006](QUALIFICATION_FINDINGS.md) remains tracked;
all earlier full-suite failures and qualification/admission boundaries remain.

## Seventh implemented slice: unary qualification proxy compatibility

[The qualification proxy now supports sequential unary exchanges](PROXY_UNARY_COMPATIBILITY.md)
on a reused product connection, while forwarding every request once through a
fresh exactly attributed daemon connection. The selected global cut still
requires complete exact durable acceptance and sends zero bytes before EOF.
Shutdown now interrupts both ends rather than leaving a daemon read until its
long deadline. Production client reuse, tuple checks and frozen receipts remain
unchanged.

Unconditional serial-exchange/blocked-shutdown tests and the real journal GJ-02
witness failed before repair, now pass. Response-drop race controls, complete
sidecar/application packages, four unchanged architecture guards and vet pass;
independent review found no blocking defects. The source-contract correction is
new development work, not retroactive qualification or proof transfer. The fresh
complete development run finished exit 1: **48 passing packages, four failing,
two without tests; 19 top-level failures**. Failed: scenario, M1 faults/focused,
and M2 evidence. Complete critical sidecar/application/runtime/helper/CLI race
packages pass. All failed runs remain recorded. The requester subsequently
approved trace-aware restart equivalence and reviewed-only clean candidate
commits; implementation and source-bound qualification remain unfinished.

## Approved entry and clean candidate verification

[The numbered approval](../programs/decisions/2026-10-13-parity-qualification-entry-approval.json)
authorizes trace-aware outcome equivalence with complete independently proved
histories, separate reviewed-only commits, and conditional M3/M4 admission.
It is conversation-local authorization, not a verified GitHub signer identity.
The initial clean candidate is commit
`50b1e082ca314908b24195f66f32d3e730a9fc07`, tree
`c2d93a2757d0ef3b819e08f18df2c72b69f929cb`, on
`parity/qualification-approved-01M4V7BW`. The original dirty checkout is preserved.

The once-run five-package critical race check passed on that exact clean source.
The once-run M1 check failed: focused campaigns reached a 15-minute timeout in
quadratic full-journal observation; two fault fixtures refused overlong Unix
socket paths before source validation. Candidate status/source and all three
exact external executable hashes remained unchanged. Logs:
`/tmp/spec-kitty-clean-candidate-initial-critical-race.log` and
`/tmp/spec-kitty-clean-candidate-initial-m1.log`. These are fresh failed/limited
results, not immutable-candidate acceptance. The [journal scan and fixture corrections](JOURNAL_SNAPSHOT_BOUNDARY.md) were
independently reviewed and committed, with the ACK proof and approved entry
artifacts, as clean candidate `5e9da5fc94ffb756c2d889521754d9ce1eebdbee`
(tree `b2af1782dd70ace891dcfc7c34c67aca4ff73b79`). Its once-only critical race
run passed; M1 completed without timeout but failed two source observations:
atomic attempt 0 (99/100 passed) and fault GJ-05 both report `git write-tree`
exit 128. Seven other focused campaigns passed; exactly one opt-in ordered crown
skip is visible in complete verbose output. Logs:
`/tmp/spec-kitty-clean-candidate-journal-critical-race.log` and
`/tmp/spec-kitty-clean-candidate-journal-m1.log`. Original stderr was discarded,
so lock contention is not historically established. Read-only index-equality
source acquisition now has controlled red/green evidence but requires independent
review and another fresh candidate; no receipt is retargeted.

[The closed acknowledgement proof](ACKNOWLEDGED_PUBLICATION_PROOF.md) now verifies
individual persisted initial/continuation receipts, including host-only roots,
without expanding mission membership or adding lookup/mutation authority.
Independent review's impossible head-version jump was reproduced and corrected.
Complete trace collection, strict terminal outcome projection and real
restart/crown verification remain under development; no old result is retargeted.

M4 entry schema/topology/old-new bindings and the sole proposed-to-admitted status
transition pass consolidated checks in
`/tmp/spec-kitty-m4-admission-validation.log`. The canonical source design is
unchanged; its existing five-field Beads-to-Hubnot projection is accounted for.
The [exact Hubnot executable](HUBNOT_ARTIFACT_RECOVERY.md) was reproduced in one
recorded build, but M3 still needs full conformance producer evidence, deployment
policy and accountable attestation. No binary availability, namespace/tooling or
numbered approval request remains outstanding.

## Dependency order and stop rules

The existing [production program](../PRODUCTION_RUNTIME_PROGRAM.md) remains
canonical: M2 is adopted; M3 remains proposed with incomplete external bindings.
M4 is now **admitted**, following verified frozen inputs, independent ENTRY READY
review and the requester's conditional authorization. See the
[recorded admission](../programs/decisions/2026-10-13-m4-admission.json).
Admission permits governed initialization, not activation/completion or exit
acceptance. M5 still requires accepted M3 **and** M4. No historical qualification
is transferred to the parity candidate.

Recommended next slices:

1. Retain the reproduced admitted sidecar and fresh-observer witnesses;
   use verified local namespace/tooling support and recovered exact M2 bytes to
   establish fresh source-bound qualification before claiming program completion.
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

## Current reviewed-only candidate and canonical M4 specify

Candidate `41c20b14a31a9241833c39b0e3c4b6d652d7e136` (tree
`f81c9201998c7a96fdac4dbd5809fc2a3de523ee`) contains the reviewed
[single-pass canonical progress view](PROGRESS_CANONICAL_VIEW.md). Fresh
critical race and M1 checks both passed, each once (34.887/699.697 seconds),
including the unchanged 2 ms progress-query p95 assertion and eight × 100 M1
attempts. Completed verbose output establishes three critical Hubnot/tracker
skips and one ordered-crown opt-in skip. Source/tree and all three image hashes
remained unchanged. Logs/results:
`/tmp/spec-kitty-clean-candidate-canonical-{critical-race,m1}.{log,result.json}`.
These are scoped passes; older receipts do not transfer. Its parent `a0dbe69961cc5f00a17db4f2b93c1e9bf27f18c1` passed M1
focused/faults in 707.244 seconds (one ordered-crown opt-in skip), but failed
the unchanged 2 ms progress-query p95 bound at 2.214526 ms in its critical race
run. Candidate identities, clean trees and all three image hashes were preserved.

Canonical M4 specify created `stack-neutral-acceptance-runtime-01M4VFQB`
(`01M4VFQBZWYDKMZP34EF6ZB4NQ`) and committed all unchanged approved requirement
rows at `1f460f6c8fcdfa4b7f8fca2d244d92fcc5c25538` on
`parity/latest-upstream-main`. See [planning preflight](../programs/m4/PLANNING_PREFLIGHT.md).
This does not activate providers, allocate implementation lanes or accept M4;
the qualification clone excludes these mission artifacts. Full parity,
restart outcome equivalence and release qualification remain incomplete.

## Reproduce or reconcile the inventory

This developer-only tool reads Git objects, not the reference working tree,
imports no upstream Python code, and makes no network calls. Python is not a
production runtime dependency.

```bash
python3 scripts/parity/capture_upstream.py \
  --upstream /path/to/spec-kitty \
  --revision 8c89aa90fe3d51f8f406158de0a30b98ac4a7ab0 \
  --output docs/parity/upstream-main.json --check

python3 -m unittest discover -s scripts/parity -p 'test_*.py' -v
```

For a newer baseline, fetch official `origin/main`, select its full immutable
SHA, run capture without `--check`, and reconcile this gap map and compatibility
choices before extending implementation claims. The tool refuses mutable or
abbreviated revision selectors, missing source, silent snapshot drift and
outputs inside the reference repository. Git replacements cannot change the
object being characterized.
