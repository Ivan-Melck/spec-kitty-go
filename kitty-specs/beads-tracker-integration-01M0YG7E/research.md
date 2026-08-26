# Research: Beads Tracker Integration

**Mission**: `beads-tracker-integration-01M0YG7E`  
**Date**: 2026-08-26  
**Qualification baseline**: Beads v1.2.2, source tag commit `6c124203e771433a3550c348771a5b5e27fd3c21`

## Sources inspected

- Beads v1.2.2 `README.md`, `docs/JSON_SCHEMA.md`, `docs/CLI_REFERENCE.md`, `docs/ARCHITECTURE.md`, `docs/DOLT.md`, and command/protocol tests.
- The Beads module/package layout at the v1.2.2 tag.
- Existing Spec Kitty tracker, execution-saga, architecture-policy, scenario, and test-campaign code.
- Phase 1 architecture closeout artifacts and the Python behavior inventory.
- The alternate Go port at `/home/lynn/projects/spec-kitty-projects/spec-kitty-go`, used for concepts only.
- Gapdb adoption evidence, used only to preserve the separate control-store decision boundary.

## Decision 1: Integrate through the documented JSON CLI

**Decision**: Invoke a pinned `bd` executable and consume `--json` output with `BD_JSON_ENVELOPE=1`.

**Rationale**: Beads v1.2.2 has no supported public Go consumer package. Its domain and storage implementations live below Go `internal/`, so another module cannot legally import them. Beads documents the JSON envelope as its stable automation contract and supplies protocol contract tests.

**Alternatives considered**:

- Import Beads internal packages: impossible across the Go internal boundary and architecturally unstable.
- Direct Dolt SQL: couples Spec Kitty to provider storage/schema and bypasses product validation.
- Copy Beads structs/code: forks semantics and recreates Python-port baggage in another form.
- Parse human CLI text: explicitly unstable and prohibited.

## Decision 2: Pin v1.2.2 and schema 1

**Decision**: The initial compatibility window is exactly Beads v1.2.2 and JSON schema 1. Compatibility expands only after the same qualification passes.

**Rationale**: v1.2.2 is a recovery release. The v1.2.0/v1.2.1 artifacts accidentally included untested schema/features that were removed from the tested v1.2.2 release. Version ordering alone is therefore not sufficient evidence of capability.

**Alternatives considered**:

- Accept all 1.x releases: unsafe because observed behavior is non-monotonic across the recovery releases.
- Accept any schema `<= 1`: unnecessary before a second supported release exists.
- Follow `latest`: makes CI and recovery non-reproducible.

## Decision 3: Use capability interfaces, not one universal tracker

**Decision**: Define small provider-neutral ports for catalog, graph, discussion, assignment projection, inspection, and export.

**Rationale**: Providers vary precisely where Beads is strongest. A fat interface would either force fake support for Beads-only concepts or turn optional Beads behavior into kernel assumptions. Small interfaces let application services depend only on the capability they use and allow unsupported safety guarantees to be explicit.

**Alternatives considered**:

- Expand the current `Provider` with every operation: increases coupling and makes substitution dishonest.
- Adopt the donor port's universal connector ledger: solves broader synchronization problems that are outside this mission.
- Build a general plugin system first: delays the one production-shaped scenario and violates locality.

## Decision 4: Treat provider revisions as observations

**Decision**: Calculate a versioned canonical-content digest and fold it into the existing opaque `tracker.Revision` value.

**Rationale**: Beads exposes `updated_at`, but its own import documentation notes timestamp ties and second-granularity cases. Hashing all relevant canonical known fields detects equal-timestamp content drift. The digest is an observation token, not an update precondition.

**Alternatives considered**:

- Use `updated_at` alone: misses tied changes.
- Store only a local sequence: cannot detect direct human/provider changes.
- Expose a Beads timestamp type in the kernel: provider leakage without stronger safety.
- Upgrade `Revision` immediately to a string digest: possible future cleanup, but unnecessarily broad for the initial adapter if collision-resistant canonical hashing and full observations are retained in diagnostics.

## Decision 5: Refuse unsafe compensation

**Decision**: Beads v1.2.2 advertises arbitrary guarded update and safe arbitrary compensation as unsupported. Recovery re-observes state, but does not issue a read-then-write cleanup and call it safe.

**Rationale**: `bd update --claim` is atomic for claim. The documented CLI does not expose an expected-revision conditional update for arbitrary assignee/metadata changes or a conditional clear operation. A local lock cannot fence a human invoking `bd` directly. The Mini review's saga law therefore requires safe refusal and reconciliation debt.

**Alternatives considered**:

- Read, compare, then `bd update`: contains a TOCTOU race and can overwrite a human.
- Lock all Spec Kitty subprocesses: useful for embedded-mode serialization but cannot fence external writers.
- Direct SQL transaction: violates the provider boundary and still competes with provider invariants.
- Remove visible assignment entirely: safer but discards a desired provider capability; honest projection plus safe refusal retains value without transferring authority.

## Decision 6: Bind one explicit workspace

**Decision**: Store an absolute canonical Beads workspace identity in the project tracker binding and pass it explicitly on every invocation. Linked Git worktrees must resolve the same identity before mutation.

**Rationale**: Beads can discover databases through repository/worktree context and redirects. Implicit discovery could create or select one database per lane, destroying shared visibility. An explicit binding turns a subtle environmental assumption into a preflight invariant.

**Alternatives considered**:

- Let each process use its current working directory: fails under linked worktrees/nested repositories.
- Copy Beads state into each worktree: creates divergent mutable authorities.
- Use the Git common directory as the database automatically: convenient but still implicit and prevents non-default operator placement.

## Decision 7: Discovery never initializes production state

**Decision**: Inspection resolves and validates an existing binding only. Initialization is an explicit operator action. Test qualification may run `bd init --stealth --skip-agents --skip-hooks --non-interactive` inside disposable repositories.

**Rationale**: Default Beads initialization can update agent instructions and install integrations. Even stealth mode changes repository-local exclude configuration. Discovery and preflight must remain read-only and predictable.

**Alternatives considered**:

- Auto-init when missing: surprising mutation and difficult rollback.
- Run default `bd init`: directly violates the no-agent-file/no-hook boundary.
- Manage user-global Beads configuration: expands authority and host side effects.

## Decision 8: Model embedded and server concurrency honestly

**Decision**: Embedded writes are serialized across Spec Kitty processes by an adapter lock rooted at the binding; server-mode writes require health qualification. Capability output explains that local serialization does not guard direct external writers.

**Rationale**: Beads documents embedded Dolt as single-writer with file locking and server mode as multi-writer. Spec Kitty must not infer provider concurrency from its own execution concurrency.

**Alternatives considered**:

- Require server mode universally: operationally heavy for the local-first default.
- Trust retries around embedded lock failures: creates noisy ambiguity and weakens deterministic start behavior.
- Claim adapter locking creates CAS: false against direct writers.

## Decision 9: Keep structured criteria in namespaced metadata

**Decision**: Render criteria for humans in Beads' acceptance field and store stable structured criterion data under a versioned `spec_kitty` metadata object.

**Rationale**: The acceptance field is human-friendly but cannot preserve criterion identity, verifier type, or evidence semantics. Metadata supports round-trip structure without asking agents to toggle checkboxes. Updates merge only the namespaced object and preserve unrelated metadata.

**Alternatives considered**:

- Markdown checkbox criteria only: recreates the labor this redesign is removing.
- One Beads child issue per criterion: turns evidence into ticket choreography and pollutes the work graph.
- Store criteria only locally: makes the primary work catalog incomplete for other tools/humans.

## Decision 10: Idempotency is operation-marker observation

**Decision**: Create, comment, and projection requests include a namespaced operation marker. Retries first search/read the mapped issue and classify exact marker/value matches as replay.

**Rationale**: The CLI lacks a general idempotency-key header. Provider-visible operation markers make ambiguous results inspectable across process restarts. Where a command cannot attach/query a marker atomically, the capability reports weaker ambiguity guarantees and recovery refuses duplication until reconciled.

**Alternatives considered**:

- Trust process exit status: timeout-after-apply makes this incorrect.
- Store markers only in SQLite: cannot distinguish external apply from no-apply after a lost response.
- Parse Dolt history: direct storage coupling.

## Decision 11: Separate interchange export from restorable backup

**Decision**: Expose two operational facts: JSONL export for portability and Dolt-native `bd backup init/sync/restore` for a full restorable backup. Qualification verifies both where the platform supports a filesystem backup destination.

**Rationale**: Beads explicitly states that `bd export` is not a full database backup and does not preserve Dolt history. Documentation must not collapse these concepts.

**Alternatives considered**:

- Call JSONL export a backup: materially false.
- Copy the live database directory while open: provider-unsupported and crash-sensitive.
- Require DoltHub: violates offline/local-first operation.

## Decision 12: Three-layer conformance

**Decision**: Test the contract against (1) the durable fake provider, (2) a deliberately dishonest subprocess harness, and (3) real Beads v1.2.2.

**Rationale**: The fake proves portability and authority invariants. The dishonest harness deterministically injects slow, unavailable, malformed, stale, wrong, and apply-then-timeout behavior. Real Beads proves actual command shapes, workspace behavior, and provider integration. No one layer can substitute for the others.

**Alternatives considered**:

- Real Beads only: slower and poor at deterministic rare-fault injection.
- Fake only: cannot prove command/version/storage reality.
- Mock `exec.Command` calls only: overfits implementation and misses process/environment behavior.

## Decision 13: Keep ordinary PR CI provider-free

**Decision**: Portable contracts and the dishonest harness join the bounded PR quality campaign. Full real-provider contention/backup/fault qualification runs scheduled and on demand, with a small PR smoke added only if timing evidence fits the existing ten-minute budget.

**Rationale**: The Phase 1 CI loop is already intentionally bounded. Requiring a heavy provider installation for every unit change would encourage skipped gates. Mission acceptance still requires the real-provider evidence.

**Alternatives considered**:

- Run full Beads qualification on every PR: high feedback cost and supply-chain/network fragility.
- Keep qualification manual only: evidence will decay.
- Vendor the Beads binary: bloats the repository and obscures provenance.

## Decision 14: Borrow donor concepts, not donor architecture

**Decision**: Retain the donor port's useful concepts—explicit source-of-truth ownership, connector capabilities, and conflict records—but implement them in the current capability-oriented kernel.

**Rationale**: The alternate Go port provides context, but its universal ledger/protobuf and local worktree lifecycle solve a different product shape. Transplanting them would repeat structural porting instead of preserving Spec Kitty's stronger concurrency model.

**Alternatives considered**:

- Copy the donor connector substrate: expands scope and authority surfaces.
- Ignore the donor entirely: loses useful field-ownership and conflict vocabulary.

## Resolved research questions

| Question | Resolution |
|---|---|
| Is there a public Go API? | No supported consumer package exists in v1.2.2; use documented JSON CLI. |
| Can the CLI provide arbitrary provider CAS? | No documented primitive; report unsupported and refuse unsafe compensation. |
| Can worktrees share one tracker? | Yes, with one explicit canonical `BEADS_DIR`/workspace binding. |
| Can Beads be used without its Git/agent setup? | Yes for explicit test/operator initialization with stealth, skip-agent, skip-hook, and non-interactive controls; discovery itself does not initialize. |
| Is JSONL a backup? | No. It is interchange; Dolt-native backup is the restorable path. |
| Must Beads be installed for normal unit tests? | No. Fake and dishonest harness are portable; real-provider qualification is a separate campaign. |
| Does Beads assignment authorize work? | No. Spec Kitty's lease/fence/workspace rules remain authoritative. |

No unresolved research marker remains.
