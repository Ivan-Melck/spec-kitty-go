# Spec Kitty Redesign

> A Go-native authority kernel and qualified software-development mission
> method for safe, concurrent, provider-neutral work.

Spec Kitty Redesign preserves the original product's mission discipline,
isolated parallel work, independent review, and controlled integration without
copying the Python package structure. The repository contains the Go-native
GapDB authority host and an executable M2 crown qualifier. Public API, neutral
acceptance, real-agent composition, and optional MCP delivery remain explicit
successor missions and are not inferred from an M2 result.

## Status at a glance

| Surface | Delivered implementation and evidence boundary | Ratified successor target |
|---|---|---|
| Authority and concurrency | The GapDB production host implements leases, lease-scoped workspaces, quarantine, stale-authority refusal, ActorID-independent review, and serialized Git integration; Phase 1 remains the historical source-bound proof | The M2 crown requalifies the complete laws on the sole production store before M3/M4 admission |
| Work substrate | Provider-neutral ports and the exact Beads 1.2.2 JSON adapter tuple are delivered | M3 composes the admitted provider with the production host; Beads remains mutable work/discussion, never mission or execution authority |
| Mission method | Specification, plan, provider-neutral decomposition, immutable WorkRevision generations, drift reconciliation, typed `next`, execution, review, acceptance, integration, progress, and recovery are implemented as native capabilities | M3 adds real-agent/provider composition without changing these authority laws |
| Control state | GapDB is the sole compiled production authority; M2 has no SQLite package, module, fallback, importer, selector, or test driver | Release readiness requires the exact M2 crown evidence and independent review |
| Application surface | `cmd/spec-kitty` is the native authority command surface; `cmd/spk-m2-qualification` is release tooling and grants no authority | Orchestrator API 2.0 and the complete normal CLI remain M5 |
| Acceptance | Repository-specific Go campaigns and candidate-bound evidence | Stack-neutral claims, approved provider bindings, typed evidence envelopes, and a policy gate |
| MCP | Not shipped and not required | Optional, separately packaged MCP `2026-07-28` adapter with Tasks as a projection of native operations |
| Host boundary | The delivered boundary is one machine and one filesystem authority | Same first-release boundary; remote callers use the application API |

“Delivered” above is bounded to the cited source and evidence. “Ratified target”
means an accepted design and governed delivery sequence, not an implementation
claim.

## Five-minute review path

Read these in order to answer what works, what is approved, what remains, and
what happens next:

1. This status table and [Delivered native runtime](#delivered-native-runtime).
2. [Production Orchestration Architecture](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md)
   and [ADR 0002](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/adr/0002-production-orchestration-architecture.md)
   for the sole normative target.
3. [Orchestrator API 2.0](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/ORCHESTRATOR_API_2_0.md),
   [stack-neutral acceptance](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/STACK_NEUTRAL_ACCEPTANCE.md),
   and the [optional MCP adapter](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/MCP_2026_07_28_ADAPTER.md)
   for specialized proposed contracts.
4. The [production runtime program](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/PRODUCTION_RUNTIME_PROGRAM.md)
   for the M1–M7 dependency graph and stop conditions.
5. [Historical dispositions](https://github.com/spec-kitty/spec-kitty-redesign/blob/main/docs/HISTORICAL_ARCHITECTURE_DISPOSITIONS.md),
   [API 1.3 compatibility](docs/ORCHESTRATOR_API_1_3_PROJECTION.md), and the
   [Python behavior inventory](docs/PYTHON_BEHAVIOR_INVENTORY.md) for provenance.
6. [Build and run the current repository](#build-and-run-the-current-repository)
   and [Evidence](#evidence) for reproducible present-tense claims.
7. [What happens next](#what-happens-next) for the M2 review boundary and M3/M4 stop rule.

## What makes this port distinct

This is a behavior-preserving re-architecture, not a source-level translation.
The original implementation is behavioral evidence, not a Go package template.

| Concern | Go-native decision | Reason |
|---|---|---|
| Work representation | Provider-neutral tracker ports; Beads is the first qualified adapter | Agents work on the product instead of maintaining Markdown checkboxes |
| Mission semantics | Spec Kitty owns lifecycle and gates | Ticket edits cannot authorize execution or acceptance |
| Identity | Durable `ActorID`; per-execution `ProcessID` | Recovery and independent review need different answers |
| Executable work | Immutable generation-numbered `WorkRevision` | Tracker drift becomes `continue`, `supersede`, `invalidate`, or `fork`, never an in-place contract edit |
| Concurrency | Each lease owns one mutable workspace forever | A replacement cannot inherit an unfenceable stale writer's directory |
| Provider integration | Narrow application-owned ports | Beads, GapDB, Git, qualification providers, and transports cannot leak vocabulary into the kernel |
| Evidence | Candidate-, revision-, and policy-bound records | A successful process or tracker status is not a verdict |

## Authority ownership

The architectural shorthand remains:

```text
Beads       -> what mutable work exists and what humans discuss
Spec Kitty  -> whether work may execute and how it is reviewed and accepted
Git         -> code, commits, trees, and ref outcomes
GapDB       -> target ordered control-state substrate, not product semantics
```

```mermaid
flowchart LR
    Beads[Beads mutable work and discussion]
    Host[Spec Kitty mission and execution authority]
    Git[Git commits trees and refs]
    GapDB[GapDB target journal substrate]

    Beads -->|revisioned observation| Host
    Host -->|attributed projection| Beads
    Host -->|authorized operations| Git
    Git -->|observed facts| Host
    Host -->|durable target events| GapDB
    GapDB -->|ordered bytes and revisions| Host
```

Consequences:

- A Beads assignment is not an execution lease.
- Beads readiness is not permission to run.
- Beads comments are not review verdicts.
- A tracker closure or status is not mission acceptance.
- A database fencing token cannot stop arbitrary POSIX writes.
- A completed implementation is not an approved candidate.
- An approved candidate is not an integrated Git fact.
- GapDB ordering cannot decide whether a transition is legal.

## Delivered native runtime

The repository is a native authority application, not a collection of planned
packages. Its earlier phases established the laws now composed by the GapDB
production host:

- **Phase 1 authority proving ground:** real Git repositories and worktrees,
  concurrent claims, lease generations, quarantine and fresh replacement
  workspaces, immutable handoff, actor-level independent review, serialized
  integration, crash recovery, conflicts, and zombie-writer scenarios.
- **Phase 2 Beads integration:** bounded argv-only JSON protocol, exact provider
  identity, canonical project binding, portable and dishonest-provider
  conformance, contention, backup/restore, and attribution-safe refusal.
- **Phase 3 mission method:** governed specify/plan/decompose/finalize flow,
  immutable WorkRevision generations, semantic drift, mixed reconciliation,
  action-scoped governance, pure typed `next`, and non-authoritative task
  projection. Portable and real-Beads two-item scenarios pass at the recorded
  evidence boundary.

`cmd/spec-kitty` is the production authority command surface. It composes the
GapDB lifecycle, memory projection, mission/workgraph, execution/workspace,
handoff/review, acceptance/integration, external-effect, and progress
capabilities. `cmd/spk-m2-qualification` independently qualifies that runtime;
`cmd/spk-phase1-helper` and historical campaigns remain evidence tools. The
process-complete API 2.0 transport, stack-neutral acceptance providers, and
optional MCP adapter are distinct M4/M5 successors.

The delivered source keeps its helper/campaign surface outside the authority
kernel and keeps concrete tracker providers behind provider-neutral contracts:

```mermaid
flowchart TB
    subgraph Surface[Native application and qualification surfaces]
        App[cmd spec-kitty]
        Qualifier[cmd spk-m2-qualification]
        Historical[historical helpers and campaigns]
    end

    subgraph Kernel[Delivered provider-neutral authority capabilities]
        Mission[mission and workgraph]
        Execution[execution workspace handoff review integration]
        Store[GapDB authority host]
    end

    subgraph Providers[Tracker adapters and conformance]
        Ports[provider-neutral tracker ports]
        Fake[fake and dishonest providers]
        Beads[qualified Beads adapter]
    end

    App --> Mission
    App --> Execution
    App --> Store
    Qualifier --> Store
    Historical --> Execution
    Mission --> Ports
    Execution --> Ports
    Fake --> Ports
    Beads --> Ports
```

### Lease-scoped workspaces

A fence protects Spec Kitty operations; it cannot fence a shell or editor.
When lease 17 expires, W1 is quarantined and lease 18 receives W2.

```mermaid
sequenceDiagram
    participant A as Process A
    participant H as Authority host
    participant W1 as Workspace W1
    participant B as Replacement process
    participant W2 as Workspace W2

    A->>H: claim work under lease 17
    H->>W1: bind fresh workspace to lease 17
    A->>W1: implement
    H->>H: expire lease 17
    H->>W1: quarantine workspace
    B->>H: claim replacement lease 18
    H->>W2: bind a different fresh workspace
    A-->>W1: zombie may continue writing
    A->>H: attempt stale handoff
    H-->>A: reject stale authority
    B->>W2: produce current candidate
```

Review evaluates an immutable candidate, not a live directory. Phase 1 has no
waiver for actor-level reviewer independence; the historical wording correction
is recorded in [the Phase 1 erratum](docs/PHASE1_ACCEPTANCE_ERRATA.md).

## Approved production direction

The production architecture keeps active projections in memory for speed and
acknowledges authority-changing success only after one atomic durable GapDB
mutation. Storage recovery, Spec Kitty journal/checkpoint restore, new runtime
epoch creation, external reconciliation, and side-effect-free playback are
separate contracts.

The target production flow is:

```text
typed command
  -> validate revision, epoch, generation, lease, candidate, and policy
  -> pure in-memory reduction
  -> durable atomic GapDB event/result/effect-intent batch
  -> publish committed projection
  -> perform and observe external effects through adapters
```

Orchestrator API 2.0 is the proposed process-complete application seam. It
provides closed named commands, revisioned mission progress, legal next actions,
durable native operations, input decisions, cancellation intent, and resumable
watches. API 1.3 is compatibility history and may translate exactly one safe
operation or return a typed upgrade refusal; it is not canonical vocabulary.

Acceptance is stack-neutral. Policy contributes claims; an explicit binding
selects a qualified provider; a typed evidence envelope reports one bounded
observation; the acceptance kernel alone decides the complete proof set.
Repository commands belong inside providers, never in the kernel.

MCP is optional. A future separate `spec-kitty-mcp` module may project API 2.0
native operations as MCP `2026-07-28` Tasks. Task identity, TTL, disconnection,
or cancellation cannot change mission, lease, evidence, or Git authority. The
normal CLI and API remain complete when MCP is absent.

## Build and run the current repository

The module declares Go 1.26. The current checkout builds the native
`spec-kitty` authority command, the M2 qualifier, and historical proving
helpers. You also need Git. Portable development and Beads contract proof
require neither a Beads binary nor a hosted service.

```bash
go mod download
make preflight
make beads-contract
```

Build the production and qualification commands into an explicit directory:

```bash
make build BUILD_DIR="$(pwd)/bin"
CGO_ENABLED=0 go build -trimpath -o bin/spec-kitty ./cmd/spec-kitty
CGO_ENABLED=0 go build -trimpath -o bin/spk-m2-qualification ./cmd/spk-m2-qualification
```

Or install the native commands into the selected Go binary directory:

```bash
go install ./cmd/spec-kitty ./cmd/spk-m2-qualification
```

The producer qualifier emits an immutable technical pass with
`review_eligible=true` and `release_ready=false`. A different `ActorID` must
review that exact producer archive; only the outer release finalizer may emit
`release_ready=true`. Neither stage creates human adoption. API 2.0 serving,
stack-neutral acceptance, Team Kitty, real-agent composition, and
`spec-kitty-mcp` remain later program outcomes.

### M2 qualification

The pull-request tier is bounded and does not substitute for the release crown:

```bash
GOTOOLCHAIN=go1.26.7 make m2-short
```

The full run requires the exact admitted assertion-extension sidecar at an
absolute path. It executes the real two-item mission, closed 15-family census,
fault/concurrency/restart campaigns, restore-deadline and absence mutants,
10,000 query and durable-mutation observations, 30 recoveries, deterministic
CycloneDX generation, and independent manifest verification.

```bash
GOTOOLCHAIN=go1.26.7 make m2-qualification \
  M2_GAPDBD=/absolute/path/to/gapdbd \
  M2_RUN_ID=m2-unique-run \
  M2_EVIDENCE_ROOT=/absolute/private/evidence \
  M2_ACTOR_ID=implementing-agent \
  M2_PROCESS_ID=exact-process-id \
  M2_IMPLEMENTATION_CONTRIBUTORS=codex \
  M2_PROFILE_DOCUMENT="$(pwd)/docs/programs/evidence/m2-producer-profile.json" \
  M2_PROFILE_REVISION=94c8fd94c5e237df5b4e256a595804c6bae685e5a4a57417275c74c0b604a378

GOTOOLCHAIN=go1.26.7 make m2-verify-evidence \
  M2_EVIDENCE=/absolute/private/evidence/m2-unique-run \
  M2_EXPECTED_COMMIT=<trusted-commit> \
  M2_EXPECTED_TREE=<trusted-tree> \
  M2_EXPECTED_MANIFEST_SHA256=<trusted-manifest-digest> \
  M2_EXPECTED_ARCHIVE_SHA256=<trusted-producer-archive-digest>
```

Evidence directories are immutable and source-bound. Failed, incomplete, and
non-admissible attempts are retained under their original RunIDs. A passing
machine run makes one source tree eligible for independent review; it does not
approve, merge, tag, release, or adopt that tree.

After review, `spk-m2-qualification finalize-review` consumes the untouched
producer bundle and a bounded structured review bundle. The reviewer records
their distinct actor/process/profile identity, machine command outputs, and
structured findings. `verify-release` recursively verifies the resulting
outer archive using externally supplied source, manifest, producer-archive,
and release-archive digests. A later documentation commit may project those
identities, but it is not retroactively the source tree that was qualified.

## Qualified Beads operating contract

The exact qualified tuple is:

```text
Beads executable: 1.2.2
source tag commit: 6c124203e771433a3550c348771a5b5e27fd3c21
JSON envelope:    BD_JSON_ENVELOPE=1
schema_version:   1
integration:      argv-only documented JSON CLI
binary SHA-256:   0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6
```

Every linked worktree uses one canonical Beads workspace. Provider inspection
is read-only and does not initialize Beads, repair provider state, install hooks,
or edit agent files. The adapter refuses unavailable guarded arbitrary update
and safe arbitrary compensation semantics before mutation.

### Install and inspect the exact provider

The reviewed installer supports Linux/amd64 with Go 1.26.4 and a C compiler.
The destination is absolute, outside the source repository, and narrower than a
top-level system directory.

```bash
tool_root="$(mktemp -d /tmp/spec-kitty-beads-tools.XXXXXX)"
identity_file="$(mktemp /tmp/spec-kitty-beads-identity.XXXXXX.json)"
scripts/install-qualified-beads.sh --dest "$tool_root" > "$identity_file"
jq . "$identity_file"
beads_bin="$(jq -r .executable_path "$identity_file")"
sha256sum -- "$beads_bin"
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" version --json
```

Use the read-only helper only with absolute paths established by provider
preflight:

```bash
go run ./cmd/spk-phase1-helper tracker inspect-beads \
  --project example-project \
  --executable "$beads_bin" \
  --workspace /absolute/canonical/beads-workspace \
  --database-identity /absolute/existing/database-directory \
  --protocol-schema 1 \
  --mode embedded_serialized \
  --read-only=true \
  --expected-digest 0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6
```

Initialization is an explicit operator action shown only for a disposable Git
repository:

```bash
disposable="$(mktemp -d /tmp/spec-kitty-beads-disposable.XXXXXX)"
git -C "$disposable" init
(
  cd "$disposable"
  BD_NON_INTERACTIVE=1 "$beads_bin" init \
    --stealth --skip-agents --skip-hooks --non-interactive --prefix skt
)
```

The typed read-only preflight vocabulary is:

```bash
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" version --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" where --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" dolt show --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" dolt status --json
```

### Export, backup, and real-provider qualification

JSONL interchange is portable interchange, not a complete backup:

```bash
export_file="$(mktemp /tmp/spec-kitty-beads-export.XXXXXX.jsonl)"
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 \
  "$beads_bin" export --json > "$export_file"
```

A Dolt-native restorable backup uses an absolute destination and is restored
only into a new disposable workspace after inspection:

```bash
backup_root="$(mktemp -d /tmp/spec-kitty-beads-backup.XXXXXX)"
BD_NON_INTERACTIVE=1 "$beads_bin" backup init "$backup_root"
BD_NON_INTERACTIVE=1 "$beads_bin" backup sync
BD_NON_INTERACTIVE=1 "$beads_bin" backup status
```

The real-provider target requires the exact already-running disposable Dolt
server tuple and an enforced offline namespace:

```bash
BEADS_BIN=/absolute/path/to/verified/bd \
BEADS_INSTALL_RESULT=/absolute/path/to/installer-identity.json \
BEADS_SERVER_ID=dolt-v0.40.5-1bf533220ab0 \
BEADS_SERVER_ENDPOINT=127.0.0.1:3307 \
DOLT_SERVER_HOST=127.0.0.1 \
DOLT_SERVER_PORT=3307 \
OFFLINE_ENFORCED=linux-network-namespace \
make beads-qualification
```

`BEADS_SERVER_ENDPOINT` must equal
`DOLT_SERVER_HOST:DOLT_SERVER_PORT`. Omitting any server variable selects the
unqualified-for-this-record embedded mode. The target does not start or repair
the server and never authorizes initialization in the project workspace.

## Verification

The bounded local ladder is:

```bash
make preflight
make contract
make beads-contract
make mission-contract
make mission-acceptance
make quality
```

Longer campaigns remain explicit:

```bash
make mission-adversarial
make full-campaign
go test -race ./...
go vet ./...
staticcheck ./...
go mod verify
govulncheck ./...
(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)
(cd docs/phase3-evidence && sha256sum --check SHA256SUMS)
git diff --check
```

## Adoption boundary

The architecture decision is settled: GapDB (also written “Gapdb” in older
records) is the target and SQLite is not a production fallback. M1 qualified a
pinned GapDB source, `gapdbd` binary, public client/protocol, lifecycle policy,
platform/filesystem boundary, and evidence manifest through the two-agent
zombie/recovery/playback experiment and all 14 external adoption scenarios. The
exact tuple is now accepted by the attributable human
[adoption record](docs/programs/evidence/m1-adoption.json); its complete evidence
tree is retained in the content-addressed
[`m1-gapdb-evidence-v1`](https://github.com/spec-kitty/spec-kitty-redesign/releases/tag/m1-gapdb-evidence-v1)
release.

That decision admitted M2. The repository now contains the GapDB authority
capabilities and closed qualifier for source, module graph, SBOM, binaries,
install, configuration, fallback, and migration absence. Source presence alone
does not make an exact tree release-ready. There is no dual-write interval.

This is a gated implementation sequence, not uncertainty about the target.
The former store remains only an immutable Phase 1–3 evidence boundary; it is
not compiled, selectable, importable, or available as production fallback.

## What happens next

M1 is complete and adopted. M2, **GapDB-Only Production Authority**, is in its
qualification/review boundary. Its falsification target remains deliberately
stronger than “the new adapter works”: every authority capability preserves its
behavioral and refusal contract on the M1 event/CAS substrate while the retired
store disappears from every production surface.

If the full crown or independent review fails, retain that immutable attempt and
repair a new source candidate. Do not admit M3/M4, introduce dual write, add
read fallback, import legacy state, or create a runtime backend-selection escape
hatch. The complete dependency graph, exit evidence, and rollback rules are in
the production runtime program.

## Evidence

Evidence is source-bound. A later commit containing these files does not inherit
the verdict automatically.

- [Phase 1 evidence index](docs/phase1-evidence/README.md) and
  [ADR 0001](docs/adr/0001-phase1-authority-proving-ground-verdict.md)
- [Phase 2 evidence index](docs/phase2-evidence/README.md),
  [results](docs/phase2-evidence/results.md), and
  [limitations](docs/phase2-evidence/limitations.md)
- [Phase 3 evidence index](docs/phase3-evidence/README.md),
  [results](docs/phase3-evidence/results.md), and
  [limitations](docs/phase3-evidence/limitations.md)
- [Mini review resolution](docs/MINI_REVIEW_RESOLUTION.md)
- [Concurrency scenario catalog](docs/CONCURRENCY_SCENARIOS.md)
- [Technical project plan](docs/TECHNICAL_PROJECT_PLAN.md)
- [M2 acceptance mapping](docs/programs/evidence/m2-acceptance-matrix.json)
  and [independent review input](docs/programs/evidence/m2-independent-review-input.md)

## License

This project is licensed under the [MIT License](LICENSE).
