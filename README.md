# Spec Kitty Redesign

> A Go-native authority kernel with a qualified Beads ticket substrate for safe,
> concurrent, mission-shaped software work.

| Project property | Delivered value |
|---|---|
| Maturity | Qualified Phase 1 authority kernel plus qualified Phase 2 Beads tracker integration |
| Language | Go 1.26.4 |
| Default tracker | Beads 1.2.2 through a checksum-pinned JSON command protocol |
| Control store | SQLite; Gapdb adoption is a separate evidence and human-approval decision |
| Execution model | Lease-scoped Git worktrees, independent review, serialized target-ref integration |
| License | MIT |

## Abstract

Spec Kitty Redesign is not a source-level translation of the Python application.
It preserves the behaviors that made Spec Kitty valuable—mission governance,
concurrent isolated work, independent review, and safe integration—while choosing
Go-native boundaries for authority, failure, and recovery.

Phase 2 adds the first production tracker adapter. Beads supplies a revisioned
ticket catalog, dependencies, discussion, and visible assignment. It does not gain
mission authority. The governing separation remains:

```text
Beads:       what work exists, what blocks it, and what humans discuss
Spec Kitty: whether work may run, where it runs, and how it is accepted
Git:         what actually happened
```

This replaces agent labor spent constructing and reparsing Markdown task lists
with a provider-neutral ticket interface. Mission steps, executable graph
generations, leases, workspace ownership, review verdicts, acceptance, and
integration remain Spec Kitty facts.

## Contents

- [Architectural thesis](#architectural-thesis)
- [What changed relative to Python Spec Kitty](#what-changed-relative-to-python-spec-kitty)
- [Architecture](#architecture)
- [Authority and concurrency](#authority-and-concurrency)
- [Beads compatibility contract](#beads-compatibility-contract)
- [Build and installation](#build-and-installation)
- [Project binding and inspection](#project-binding-and-inspection)
- [Capabilities and authority limits](#capabilities-and-authority-limits)
- [Operations and recovery](#operations-and-recovery)
- [Export and backup](#export-and-backup)
- [Verification](#verification)
- [Delivered boundary](#delivered-boundary)
- [Evidence and design record](#evidence-and-design-record)

## Architectural thesis

Each kind of fact has one owner.

```mermaid
flowchart LR
    Beads["Beads tracker<br/><b>work, blockers, discussion</b>"]
    Kernel["Spec Kitty kernel<br/><b>authority, review, acceptance</b>"]
    Git["Git repository<br/><b>commits, refs, ancestry</b>"]

    Beads -->|"provider-neutral observations"| Kernel
    Kernel -->|"attribution-safe projections"| Beads
    Kernel -->|"argv-only Git operations"| Git
    Git -->|"observed repository facts"| Kernel
```

The consequences are deliberate:

- A Beads assignment is not an execution lease.
- Beads readiness is not permission to run.
- Beads comments are not review verdicts.
- A tracker closure or status is not mission acceptance.
- A database fencing token cannot stop an arbitrary POSIX write.
- A completed implementation is not an approved implementation.
- An approved implementation is not an integrated commit.

## What changed relative to Python Spec Kitty

The original implementation is behavioral evidence, not a package template.

| Concern | Inherited tendency | Go-native decision | Why |
|---|---|---|---|
| Port strategy | Reproduce modules and command choreography | Preserve product behavior behind explicit capabilities | Structural parity preserves accidental coupling |
| Work representation | Markdown task lists double as a mutable work database | Provider-neutral tracker ports with Beads as the first qualified adapter | Agents should work on the product, not maintain presentation syntax |
| Mission semantics | Work artifacts and mission state can blur together | Mission authority stays in the kernel | A ticket edit must not authorize execution or acceptance |
| Identity | Agent name can ambiguously mean actor or process | Durable `ActorID`; per-execution `ProcessID` | Recovery and independent review need both answers |
| Plan stability | Work text can drift under a running agent | `WorkRevision` freezes an executable graph generation | Reconciliation becomes explicit: continue, supersede, invalidate, or fork |
| Concurrency | Durable lanes use mutable worktrees | Lane lineage is durable; lease workspaces are disposable | Preserve concurrency while isolating stale writers |
| Provider integration | Copy provider concepts into mission logic | Kernel imports only provider-neutral tracker contracts | Beads stays replaceable and cannot acquire authority by convenience |
| Persistence | Implicit files coordinate processes | SQLite transactions coordinate the single-machine authority kernel | CAS, idempotency, refusal evidence, and crash recovery become explicit |

## Architecture

The kernel and the Beads adapter are intentionally separated by a failing-CI
dependency guard.

```mermaid
flowchart TB
    subgraph Surface["Executable and qualification surfaces"]
        Helper["cmd/spk-phase1-helper"]
        Campaign["internal/testcampaign"]
    end

    subgraph Kernel["Authority kernel — cannot import Beads"]
        Workgraph["workgraph"]
        Execution["execution"]
        Workspace["workspace"]
        Handoff["handoff"]
        Review["review"]
        Integration["integration"]
        Control["controlstore/sqlite"]
    end

    subgraph Ports["Provider-neutral tracker contracts"]
        Catalog["catalog and observation"]
        Graph["blocking graph and readiness"]
        Discussion["discussion"]
        Assignment["assignment projection"]
        Diagnostics["typed capabilities and diagnostics"]
    end

    subgraph Providers["Tracker implementations and proof"]
        Fake["tracker/fake<br/>portable conformance"]
        Dishonest["dishonest subprocess<br/>fault conformance"]
        BeadsAdapter["tracker/beads<br/>bounded argv + JSON"]
        Real["real Beads qualification<br/>contention + recovery"]
    end

    Surface --> Kernel
    Kernel --> Ports
    Fake --> Ports
    Dishonest --> Ports
    BeadsAdapter --> Ports
    Campaign --> Fake
    Campaign --> Dishonest
    Campaign --> BeadsAdapter
    BeadsAdapter --> Real
```

Repository guide:

```text
cmd/spk-phase1-helper/       Thin JSON helper composition surface
internal/tracker/            Provider-neutral capabilities and ports
internal/tracker/fake/       Durable portable reference provider
internal/tracker/conformance Shared provider-neutral contract suite
internal/tracker/beads/      Qualified Beads protocol and adapter
internal/testcampaign/       Exact censuses and evidence producers
internal/execution/          Claims, leases, runs, fencing, recovery
internal/workspace/          Lease-scoped workspace lifecycle
internal/handoff/            Immutable candidate evidence
internal/review/             Actor-independent judgments
internal/integration/        Repository/target-ref serialization
internal/controlstore/sqlite Single-machine transactional authority
docs/phase2-evidence/        Published Phase 2 evidence and limitations
```

## Authority and concurrency

### Lease-scoped workspaces

A fencing token can fence kernel operations; it cannot fence an editor, shell,
compiler, or zombie agent writing directly to a directory. A replacement lease
therefore never inherits a mutable workspace.

```mermaid
sequenceDiagram
    participant A as Process A
    participant K as Authority kernel
    participant W1 as Workspace W1
    participant B as Process B
    participant W2 as Workspace W2

    A->>K: claim and receive lease 17
    K->>W1: bind fresh W1 to lease 17
    A->>W1: implement
    Note over A,K: heartbeat stops; process remains alive
    K->>K: expire lease 17
    K->>W1: quarantine W1
    B->>K: replacement receives lease 18
    K->>W2: bind fresh W2 to lease 18
    A-->>W1: zombie resumes writing
    Note over W1,K: W1 cannot become lease 18 workspace
    B->>W2: produce candidate commit
    B->>K: hand off immutable candidate
```

The stale process may change W1, but those bytes have no route to handoff,
approval, or integration under lease 18. Review evaluates a pinned candidate
commit. Its actor must differ from every candidate contributor actor. Integration
then serializes the contested `(RepositoryID, TargetRef)` and re-observes Git.
Phase 1 has no waiver for actor-level review independence; the historical
contract correction is recorded in
[`PHASE1_ACCEPTANCE_ERRATA.md`](docs/PHASE1_ACCEPTANCE_ERRATA.md).

### One-machine control authority

One Spec Kitty control store belongs to one machine and one filesystem authority.
SQLite WAL is not opened across a shared filesystem by several hosts. Remote
clients will use the future Orchestrator API served by the owning host instead of
opening the control database or mutable worktrees directly.

## Beads compatibility contract

The supported tuple is exact:

```text
Beads executable: 1.2.2
source tag commit: 6c124203e771433a3550c348771a5b5e27fd3c21
JSON envelope:    BD_JSON_ENVELOPE=1
schema_version:   1
integration:      argv-only documented JSON CLI
binary SHA-256:   0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6
```

Beads 1.2.2 has no supported public Go consumer package for this module. Useful
implementation packages are beneath Go `internal/`; importing them is both
forbidden and semantically unsafe. Spec Kitty also does not couple to the Dolt
schema. `internal/tracker/beads` issues only allowlisted argument vectors, sets a
fixed environment, reads bounded structured output, and rejects unsupported
versions or envelope schemas before mutation.

Unknown optional fields in schema 1 are ignored. An unknown envelope schema,
metadata schema, executable version, or binary digest fails closed. Version
ordering is not compatibility: 1.2.2 is a tested recovery release, not a promise
about 1.2.0, 1.2.1, or a future release. Expanding support requires portable,
dishonest, real-provider, contention, export/backup, and authority-invariant
qualification plus a new allowlisted digest.

## Build and installation

### Portable development

Ordinary development needs Go, Git, and downloaded Go modules. It needs no `bd`,
Dolt server, hosted account, or network after dependencies are present.

```bash
go mod download
make preflight
make beads-contract
```

Build all packages and the repository helper into an explicit directory:

```bash
make build BUILD_DIR="$(pwd)/bin"
```

Or install the helper into the selected Go binary directory:

```bash
go install ./cmd/spk-phase1-helper
```

The helper remains a thin Phase 1/2 composition and diagnostic surface; it is
not the eventual full Spec Kitty CLI.

### Install the checksum-qualified provider

The reviewed installer supports Linux/amd64 with Go 1.26.4 and a C compiler. Its
destination must be absolute, outside the source repository, and narrower than a
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

After a verified target already exists, `--offline` checks and selects it without
downloading. The installer rejects an existing mismatched target rather than
overwriting it.

## Project binding and inspection

A project binding records:

- the absolute, canonical executable and its version/digest;
- protocol schema 1;
- one absolute canonical Beads workspace shared by every linked worktree;
- a derived `ws1:` workspace identity;
- explicit read-only state; and
- either `embedded_serialized` or `server_multiwriter` concurrency.

The canonical Beads workspace is project-scoped. It is not a lease workspace and
must not silently become `<linked-worktree>/.beads`. All linked worktrees pass the
same binding. Inspection rejects a different observed workspace or database as
`workspace_mismatch`; mutations do not repair or switch it.

Provider inspection is read-only. It never initializes Beads, installs hooks, edits
`AGENTS.md`, `CLAUDE.md`, or `.cursorrules`, enables Git automation, repairs
provider state, or chooses a per-worktree database. It observes the exact JSON
preflight only:

```bash
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" version --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" where --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" dolt show --json
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 "$beads_bin" dolt status --json
```

For an existing embedded binding, the helper can return the normalized binding,
capability report, health, and typed diagnostics. Substitute only absolute paths
obtained from the read-only preflight:

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

Initialization is an explicit operator act and is shown only for an obviously
disposable repository. Never run it in this checkout merely to make discovery
succeed.

```bash
disposable="$(mktemp -d /tmp/spec-kitty-beads-disposable.XXXXXX)"
git -C "$disposable" init
(
  cd "$disposable"
  BD_NON_INTERACTIVE=1 "$beads_bin" init \
    --stealth --skip-agents --skip-hooks --non-interactive --prefix skt
)
```

## Capabilities and authority limits

The capability report describes provider guarantees, not overall product
quality. A request stronger than the provider guarantee is refused before
mutation.

The qualified guarded arbitrary update and safe arbitrary compensation levels
are deliberately unsupported, as the matrix makes explicit.

| Capability | Qualified 1.2.2 level | Operational meaning |
|---|---|---|
| Catalog read | `supported` | Decode bounded schema-1 records |
| Catalog create | `supported` | Create with Spec Kitty metadata and operation marker |
| Catalog update | `unguarded` | Arbitrary fields can change, but not under provider-side CAS |
| Blocking graph | `supported` | Add/remove/list blockers; pinned graph remains execution authority |
| Readiness | diagnostic projection | Cross-check provider state; never permission to run |
| Discussion | `observably_idempotent` append | Append and re-observe an operation marker; not a review verdict |
| Assignment | `atomic_claim` where characterized | Visible ownership projection; not a lease |
| Observation revision | `content_derived` | Opaque content digest; neither ordered revision nor CAS token |
| Guarded arbitrary update | `unsupported` | Fails `guarded_update_unsupported` before mutation |
| Safe arbitrary compensation | `unsupported` | Fails `safe_compensation_unsupported`; human state is preserved |
| Embedded concurrency | `adapter_serialized` | Coordinates Spec Kitty processes sharing the binding |
| Server concurrency | `provider_native` | Qualified shared-server multiwriter mode |
| Portable export | `supported` | Bounded JSONL interchange |
| Restorable backup | `supported` | Dolt-native backup, restore, and disposable inspection |

Local serialization is not compare-and-swap against humans or external tools.
Direct `bd` users do not pass through the adapter lock. Before compensating,
Spec Kitty compares the current mapping, operation marker, value, and content
revision. If attribution has changed, it preserves the provider state and records
a reconciliation conflict.

## Operations and recovery

Inspection output includes executable version/digest, protocol schema, canonical
workspace identity, storage/concurrency mode, read-only state, capability levels,
health, stable diagnostic kind, and recovery code. Automation branches only on
those typed fields, never warning prose or human-formatted CLI output.

| Diagnostic | Safe operator response |
|---|---|
| `unavailable` / spawn failure | Repair the absolute binding or install the verified binary; do not substitute an arbitrary PATH executable |
| `version_skew` | Restore 1.2.2 or qualify a new exact version and digest |
| `schema_skew` | Stop before mutation; update protocol contracts and requalify |
| `workspace_mismatch` | Correct the project binding so every worktree resolves the same canonical workspace |
| `lock_contended` | Retry within bounded policy or select a qualified server-multiwriter binding |
| `malformed_protocol` / bounded-output failure | Stop automation, verify the binary tuple, and capture only redacted diagnostics |
| `ambiguous` | Observe mapping, operation marker, current value, and content revision before deciding; never repeat blindly |
| `revision_conflict` | Preserve provider state and reconcile the durable conflict explicitly |
| `attribution_lost` | A human or external writer changed the fact; preserve it and resolve explicitly |
| `guarded_update_unsupported` | Reconcile without mutation or choose a provider with an actual guarded primitive |
| `safe_compensation_unsupported` | Do not emulate safety with read-then-write; leave state intact for operator resolution |

If Beads is unavailable, portable fake conformance and existing Spec Kitty
authority/control data remain usable. Provider-dependent reads or mutations
return typed diagnostics; they do not reconstruct mission state from tickets.

Embedded serialization coordinates Spec Kitty processes only. It cannot fence a
human or external writer. Server mode is accepted only with a pinned logical
server identity, endpoint, allowlisted connection inputs, provider-native
multiwriter behavior, and successful qualification.

## Export and backup

Portable JSONL export is interchange, not full backup and not a restorable copy
of Dolt history:

```bash
export_file="$(mktemp /tmp/spec-kitty-beads-export.XXXXXX.jsonl)"
BD_JSON_ENVELOPE=1 BD_NON_INTERACTIVE=1 \
  "$beads_bin" export --json > "$export_file"
```

A Dolt-native restorable backup uses an absolute destination outside the active
workspace. Sync and inspect it before depending on it:

```bash
backup_root="$(mktemp -d /tmp/spec-kitty-beads-backup.XXXXXX)"
BD_NON_INTERACTIVE=1 "$beads_bin" backup init "$backup_root"
BD_NON_INTERACTIVE=1 "$beads_bin" backup sync
BD_NON_INTERACTIVE=1 "$beads_bin" backup status
```

Qualification verifies restoration in a fresh, empty, disposable workspace and
then inspects recovered catalog, dependencies, criteria metadata, and discussion.
Destructive restore never runs automatically against the project binding. An
operator must inspect the backup, select an empty disposable target, and explicitly
choose restoration. Do not copy a live Dolt database or issue direct Dolt SQL.

## Verification

The portable pull-request ladder is offline after modules are present:

```bash
make preflight
make contract
make beads-contract
make quality
```

The exact real-provider target requires the installer identity, verified binary,
a pinned external Dolt server tuple, and an enforced offline network namespace.
The CI workflow in [`.github/workflows/beads-qualification.yml`](.github/workflows/beads-qualification.yml)
provisions those prerequisites and invokes:

```bash
make beads-qualification \
  BEADS_BIN=/absolute/path/to/verified/bd \
  BEADS_INSTALL_RESULT=/absolute/path/to/installer-identity.json \
  OFFLINE_ENFORCED=linux-network-namespace
```

Additional closeout gates:

```bash
make full-campaign
go test -race ./...
go vet ./...
staticcheck ./...
go mod verify
govulncheck ./...
go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=10
(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)
git diff --check
```

## Delivered boundary

Phase 2 delivers a qualified default tracker substrate composed with the Phase 1
authority kernel. It does not deliver the eventual full mission method and CLI,
Team Kitty or hosted sync, Orchestrator API serving, MCP, Python-project
migration, alternate production trackers, multi-machine control authority, or a
complete replacement of mission artifacts by tickets. Those are future missions;
they are listed here once so the delivered Beads adapter does not read like a
hypothesis or an unfinished experiment.

SQLite remains the qualified control store in this repository. Gapdb has its own
successful MVP evidence, but replacing SQLite is a separate migration/adoption
decision requiring equivalent external-adapter evidence and an explicit signed
human record. Phase 2 neither performs nor implies that adoption.

## Evidence and design record

- [Phase 2 evidence index](docs/phase2-evidence/README.md)
- [Phase 2 results](docs/phase2-evidence/results.md)
- [Phase 2 limitations](docs/phase2-evidence/limitations.md)
- [Technical project plan](docs/TECHNICAL_PROJECT_PLAN.md)
- [Python behavior inventory](docs/PYTHON_BEHAVIOR_INVENTORY.md)
- [Concurrency scenarios](docs/CONCURRENCY_SCENARIOS.md)
- [Orchestrator API 1.3 projection](docs/ORCHESTRATOR_API_1_3_PROJECTION.md)
- [Phase 1 evidence index](docs/phase1-evidence/README.md)

The evidence uses two-step provenance. Machine artifacts bind the exact frozen
source commit/tree that ran. The evidence payload is committed afterward, and a
following docs-only attestation names that payload commit and checksums. A later
commit does not inherit qualification merely because this directory still exists.

## License

This project is licensed under the [MIT License](LICENSE).
