# Current parity target

The target is upstream `main` at
`581b5c46da1700987d0352c3cc53eaec80372a02`, which reports the unreleased
`4.0.0rc8`. The newest published release is `4.0.0rc7`, which branch
`rc7-parity` already matches. This branch, `parity/upstream-main-581b5c4`,
advances the previous main-based port (pin `ae979820332e`, kept below) across
225 upstream commits that touch 88 source files.

## What changed upstream

The command tree still has 279 paths. Four help pages changed:

- `dispatch`, `invocations list` and `profile-invocation complete` gained
  `--owned-checkout <path>`: Op records kept in a linked checkout that the
  operator owns. The repository root checkout is refused.
- `agent tasks move-task` documents that `--note` is recorded on a move to
  `approved`/`done` only when the move writes a new approval review-cycle
  record, and that a stderr warning reports otherwise.

All 725 embedded assets were re-synced from the wheel built at the pin
(`internal/assets/data/UPSTREAM_REVISION`). The migration catalog has 136
entries.

| Area | Ported behavior |
|---|---|
| Locked event-log appends | `kernel.event_log_append` as `internal/eventlog`. A machine file lock lives under `<git-common-dir>/spec-kitty-locks/event-logs/`, or `<log>.event-log.lock` outside git. A torn tail is refused; a short write is cut back to the pre-write size. Refusal codes are `EVENT_LOG_TORN_TAIL`, `_LOCK_TIMEOUT`, `_LOCK_UNAVAILABLE`, `_SYMLINK` and `_SHORT_WRITE`. Invocation, lifecycle and the other interim logs use it. |
| Op custody | `--owned-checkout` for dispatch, listing and completion (#6564, #6682, #6691, #6694). Records stay in the owned checkout and never in the root checkout. |
| Migration and upgrade | Residue-safe successors replace migrations that wrote into residue folders, and superseded migrations are not re-run (#6627, #6669). Also ported: interview-selection promotion (#6629) and the fabricated-identity audit (#6668). The `migrate` walkers skip residue folders. |
| Review | Approval feedback is recorded on approve/done moves (#6652, #6654). The issue matrix is terminal at `done` (#6653, #6711, #6712), and a `NOT_AUTHORED` typed-ref read is supported. |
| Charter | `charter list` reports the organization-required floor (#6417). Reach-only edges fail closed on an unreadable synthesis manifest (#6454). `charter context` refuses coded errors, including charter encoding errors, with their code and body. |
| Missions and acceptance | Non-WP plan missions can be accepted (#6707). The decisions lock is treated as acceptance self-bookkeeping (#6628). Other changes: safe-commit of a named ignored file (#6649) and move-task binding hops (#6630). |

## Issues reproduced and fixed on this branch

Each issue was reproduced on the previous port before it was fixed.

1. **Lost status updates under concurrent writers.** Forty processes calling
   `status.AppendRows` at once kept only 2 of 40 rows. The append was a
   read-whole-file, append and rename sequence with no lock. Appends now hold
   the mission status lock. `TestAppendRowsConcurrentProcesses` covers the
   git and non-git variants.
2. **Unsafe changes to projects created by Python `main`.** The previous port
   showed these failures:
   - It missed Op records in owned checkouts and wrote Ops into the root
     checkout.
   - Its `migrate` walkers wrote into residue folders.
   - Upgrades damaged residue folders.
   - After a Python-main upgrade, it re-ran the superseded selection
     promotion, which added 38 directives and 121 tactics to `config.yaml`.

   All four cases now match the oracle.
3. **No Windows build.** Code using Unix-only syscalls (`Flock`, `Stat_t`,
   `Setsid`, `Kill`, `O_NOFOLLOW`) now lives in build-tagged files. On Windows:
   - Locks use `LockFileEx`, mirroring `msvcrt.locking`.
   - Process trees use `CREATE_NEW_PROCESS_GROUP` and `taskkill /T`.
   - Node identity uses the volume serial number and file index.
   - Lock stubs that were no-ops now take real locks.

   All six release targets pass `go vet` and build. Under Wine:
   - The `filelock` tests and the non-git status concurrency test pass.
   - `spec-kitty.exe --version` reports `4.0.0rc8`.
   - `init smoke --ai codex --non-interactive` exits 0.

   The Wine prefix has no git, so git-backed tests were not run there.
4. **No install channel.** This branch adds:
   - `scripts/build-release.sh`, which builds reproducible archives and
     `SHA256SUMS`;
   - `scripts/install.sh` and `scripts/install.ps1`, which verify checksums
     and install atomically;
   - `.github/workflows/release.yml`, triggered by a `v*` tag that must equal
     `app.Version`;
   - `.github/workflows/portability.yml`, which cross-builds all targets and
     runs a Windows smoke test.

   A local mirror install succeeded, and a tampered archive was refused. Two
   builds produced identical checksums.

## Known gaps

These upstream behaviors are not yet ported:

- Merge gates do not read the issue matrix from the coordination ref. This
  gap predates this pin.
- `next` has no terminal retrospective.
- `accept` does not handle a WP file without frontmatter.

`next` prompt construction falls back to the compact governance block when
charter context fails with `SYNTHESIS_MEMBERSHIP_UNREADABLE`. This is upstream's
behaviour too (`prompt_builder._governance_context` re-raises only scope and
config errors), and the prompts match the oracle.

An upstream defect is deliberately not replicated: Python fails to promote
selections when `.kittify/encoding-provenance` is missing.

## Reference and verification

The reference is `$HOME/.cache/spec-kitty-parity/main-581b5c46da`. It was
set up with the commands in [the porting guide](../PORTING.md). The full
help inventory has 279 paths; with
`SPEC_KITTY_HELP_INVENTORY=$REF/inventory/help`, all 279 match.

The full differential campaign ran on 2026-10-27, in about two hours. It used a
frozen executable built from commit `61ae0952`:

```sh
GOMAXPROCS=8 TMPDIR=/var/tmp/sk-campaign/tmp \
SPEC_KITTY_GO_BIN=/var/tmp/sk-campaign/spec-kitty \
SPEC_KITTY_ORACLE="$REF/oracle-bin/spec-kitty" \
SPEC_KITTY_HELP_INVENTORY="$REF/inventory/help" \
go test -p 8 -parallel 4 -json -count=1 -timeout 180m ./...
```

It recorded 1,091 passing test/subtest results, 13 skipped tests and 8
failing results. The failures came from three top-level tests: 71 packages
passed, 3 failed and 27 have no test files. The three tests were fixed
afterwards, and each now passes against the oracle in a focused rerun that
uses the campaign's `TMPDIR`:

| Test | Cause | Fix |
|---|---|---|
| `charterenc` `TestDiffCharterNonUTF8Answers` | A real difference. Upstream `charter context` now refuses a charter encoding error with its code and full diagnostic (#6454). Go printed only the code. | `b87ebfc3` |
| `closeout` `TestDiffConsolidateAttestApproved` | A test artifact. Rich wrapped the coordination worktree's mid8 across two lines, so normalization did not mask it. | `cb02833b` |
| `setup` `TestInitColourOutputMatchesOracle` | A test artifact. A panel wrapped the sandbox base path, leaving its per-sandbox last component (`001` or `002`) on the next line. | `b5c3b358` |

The final tree passes these checks:

```sh
go vet ./...                                   # and for windows, darwin and linux on amd64 and arm64
SPEC_KITTY_HELP_INVENTORY="$REF/inventory/help" go test -p 8 ./...
```

`gofmt -l` reports only `internal/charter/language.go`, which was already
unformatted before this branch.

## Previous pin: upstream main `ae979820332e`

The selected target is upstream `main` at
`ae979820332ee259eba467d03a00e8bb50196237`, reporting the unreleased
`4.0.0rc8`. The latest published/tagged release candidate at selection time
was `4.0.0rc7`; this port advances the existing main-based RC8 port from
`dccf199f7a42fd91556610163c989794d210eac9`.

The port is updated to this pin, with CLI parity verified by the complete
differential campaign and the focused follow-up comparisons below. Final
verification completed on 2026-10-24.

The reference contains 279 command paths (previously 276). New commands are
`charter pack vendor`, `migrate ledger`, and `migrate activation-ledger`.
Five existing command definitions changed. The source comparison covers
285 changed paths, including refactors checked for observable effects.

The built-in pack is the external open-packs snapshot
`af1c370f34601f2aa00d45a7a9597ab8d6a46ea1`. Assets come from the wheel after
its build hook verifies that pin. All 724 embedded assets match an independently
rebuilt reference wheel byte for byte.

| Area | Implemented behavior and focused evidence |
|---|---|
| Mission capability | `requires_substantive_spec`, explicit false and built-in fallback; mission creation reads the activation authority. |
| Activation | Tracked allow/deny ledger, cross-layer identity, organization-required floor, guarded writes, migration, presets, cascade barriers, diagnostics and bundle freshness. Activation, context, collision and consistency oracle scenarios pass. |
| Migration and upgrade | Ordered 131-entry manifest, tracked/clone-local records, read-only report, settlement, init seeding and adoption. All new RC8 migrations are present. Legacy upgrade corpora, already-current projects and worktree fan-out pass focused comparisons. |
| Agent delivery | Hyphenated command skills, shared/native frontmatter, configured-agent global commands, freshness, Antigravity retirement, narrow ignore rules and ownership-aware legacy retirement. Init matrices, configuration, repair and exported plugin bundles pass comparisons. |
| Global assets | Full-batch recheck under the owner lock, source-role retention, environment/membership/identity checks and bounded rebuild after a destination changes during preparation. Ten recheck scenarios, including a filesystem-root anchor, match the oracle; concurrent cold-start issue writers pass. |
| Charter and languages | Language catalog, configured-language authority, polyglot/JVM detection, interview settlement, synthesis soundness, reach-only edges and creation records. Context 1.4.0, declared identities, denial consumers, optional activation-ledger bundle reporting and authority paths pass integration comparisons. |
| Pack vendor | Offline digest/compatibility checks and scratch fetches that update the pin without replacing the live tree. Local-repository matrix and 75 PEP 440 specifier cases pass. |
| Runtime and owned checkout | Strict review locks, preclaim refusals, owned issue-verdict/acceptance, exact mission lookup, degraded startup, single-branch evidence and profile denials. Concurrent sibling-row preservation, remote-only refusal, owned planning and analysis freshness pass. |
| Bulk edit | Occurrence-map readiness, finalization, implementation inference and review-diff gates. Sixteen readiness cases, 1,800 path/policy/status cases, git-history/ref cases, rendering and CLI refusal scenarios pass. |
| Consolidation and placement | Canceled-contribution attribution, rename/binary/mode/Unicode cases, known coordination-kind projection, single-branch evidence and explicit research/documentation artifact kinds. Fourteen attribution histories, evidence cases and classifier comparisons pass. |
| Glossary events | Pinned event-contract validation before writes, resolution-batch prevalidation, provenance timestamps and locked torn-tail quarantine. All 408 validation cases and five interactive resolution/write/refusal/recovery scenarios match RC8. |
| Governance and diagnostics | Primary-tracking-aware Op commits, tracked team settings preservation, orientation, diagnostic roots, guarded agent configuration and current event-contract version. Focused Op, health, malformed-file and warning comparisons pass. |

The Go executable has no Python runtime dependency. Python is used only to
build the development oracle and compare behavior. Differential fixtures
normalize random IDs, timestamps, local paths and runtime environment metadata.
Renderer freshness signatures include implementation code and its installation
path, so legacy-project upgrade fixtures warm each executable's own global
cache before comparing project changes.

### Reference and verification (previous pin)

The isolated reference is `$HOME/.cache/spec-kitty-parity/rc8-ae97982033`.
Use an isolated `HOME` and `SPEC_KITTY_NO_UPGRADE_CHECK=1`, as described in
[the porting guide](../PORTING.md). The reference setup script has been run
end to end, including rebuilding its wheel and comparing assets. Optional
reference-agent and Python autorebase checks also pass.

All 279 plain help snapshots were captured with
`scripts/capture-reference-help.py` in the subprocess environment used by the
parity test. A `CliRunner` capture wraps long usage lines differently.
The full colour and usage-error matrices pass in the broad campaign.

The complete updated campaign passed using immutable source at
`/tmp/spec-kitty-rc8-final-source` and executable
`/tmp/spec-kitty-rc8-final-bin`, with bounded package/test concurrency.
It recorded 1,019 passing test/subtest results, 10 skipped tests and no
failures. All 69 packages with tests passed; 30 packages had no test files.
The command, run from that source snapshot, was:

```sh
GOMAXPROCS=6 \
SPEC_KITTY_GO_BIN=/tmp/spec-kitty-rc8-final-bin \
SPEC_KITTY_ORACLE="$REF/oracle-bin/spec-kitty" \
go test -p 8 -parallel 4 -json -count=1 -timeout 120m ./...
```

Later artifact-classification, glossary-ledger and filesystem-root-anchor
changes also pass their focused oracle comparisons. The latest executable
additionally passes replays of dispatch, invocation completion, glossary
documentation/entity pages, owned planning, setup-plan wiring, safe commits
and finalization ownership inference. These follow-up results, together with
the latest native suite, cover changes made after the immutable campaign
snapshot.

The final working tree passes the native suite, static analysis and targeted
race checks:

```sh
GOMAXPROCS=4 go test -p 4 -parallel 3 -timeout 20m ./...
go vet ./...
go test -race ./internal/bootstrap ./internal/glossary ./internal/writecheckout
```

The standalone executable is built with:

```sh
CGO_ENABLED=0 go build -trimpath -o bin/spec-kitty ./cmd/spec-kitty
```

It reports `spec-kitty-cli version 4.0.0rc8`. An isolated
`init . --ai codex --non-interactive` smoke test creates command skills,
the activation ledger and the migration ledger. Its execution trace contains
no Python execution.

Glossary producer model and JSON Schema data can be reproduced with the
reference interpreter:

```sh
$REF/oracle-venv/bin/python scripts/sync-event-contracts.py --check
```

All verification listed above completed successfully. Parity here describes
the tested CLI behavior, generated artifacts and Git effects against the
selected upstream commit; the Go port does not expose upstream's Python
library API.
