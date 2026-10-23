# Parity: tracker (area J)

Go implementation of the upstream `spec-kitty` 4.0.0rc8 `tracker` command
group (`specify_cli/cli/commands/tracker.py`, `specify_cli/tracker/*`) and of
the parts of the bundled `spec_kitty_tracker` 0.5.2 package it drives
(`SyncEngine`, `BeadsConnector`, `FPConnector`, ownership/conflict policy).
Code: `internal/tracker` (library), `internal/tracker/sqlitedb` (SQLite file
format), `internal/commands/tracker` (commands). Registered through
`cmd/spec-kitty/imports_tracker.go`.

In rc8 the hosted/SaaS tracker surface is gone (#6164): `linear`, `jira`,
`github`, `gitlab` and `azure_devops` are *removed* providers that every
command refuses, and no command talks to an HTTP endpoint. The only egress
left is the local subprocess (`bd` / `fp`, or the executable named by the
`command` credential), gated by `tracker.egress` in `.kittify/config.yaml`.
Sync is therefore tested end to end with fake `bd`/`fp` shell scripts
(argv log + canned output) bound via `--credential command=<path>`; nothing
external is contacted.

Differential tests (`SPEC_KITTY_ORACLE=$REF/oracle-bin/spec-kitty go test
./internal/commands/tracker/ -timeout 0`) run each step in two
oracle-initialised projects and compare exit code, normalized stdout and
stderr (uncaught exceptions: rich's framed traceback with the panel body
masked by `testkit.MaskTracebackFrames`, so the box, the chained-exception
separators and every exception line are compared),
`.kittify/config.yaml`, `~/.spec-kitty/credentials`, the fake CLI's argv log
and a `sqlite3` dump of the cache (`PRAGMA integrity_check`, issue rows with
their JSON payloads, checkpoints).

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `tracker` (bare group: help, exit 0) | done | `TestOracleProvidersAndHelp` | none (help byte-identical: `go test ./internal/helpparity`) |
| `tracker providers` (`--json`) | done | `TestOracleProvidersAndHelp`, `TestGoTrackerLifecycle` | none |
| `tracker bind` (`--provider/--workspace/--ownership-mode/--field-owner/--credential`) | done | `TestOracleUnboundAndBindErrors` (removed/unknown provider, missing workspace, bad mode, bad key=value), `TestOracleBindStatusMapUnbind`, `TestOracleEgressVerdicts` (egress + unknown keys + quote style carried over), `TestOracleCommentedConfigRoundTrip`, `TestOracleTrackerBlockKeepsExtrasAndEgressQuotes` | none known: `.kittify/config.yaml` goes through the ruamel round trip (`internal/rtyaml`): comments, quote styles, flow collections, block scalars, number formats and anchors elsewhere survive, indentation is normalised as ruamel does, `_extra` keys and `egress` keep their loaded types |
| `tracker unbind` | done | `TestOracleBindStatusMapUnbind`, `TestOracleEgressVerdicts` (recorded egress outlives the binding), `TestOracleRemovedAndIncompleteBindings`, the two round-trip tests above | none known |
| `tracker status` (`--json`) | done | all oracle tests (unbound, configured, inert removed/unknown provider, provider without workspace, non-mapping tracker block) | none known |
| `tracker map` (bare: `Missing command.`, exit 2) | done | `TestOracleProvidersAndHelp` | none |
| `tracker map add` (`--wp-id/--external-id/--external-key/--external-url`) | done | `TestOracleBindStatusMapUnbind` (insert, upsert, UNIQUE violation, blank id), `TestOracleDBInterop` | traceback frames: see Irreducible differences |
| `tracker map list` (`--json`) | done | `TestOracleBindStatusMapUnbind`, `TestOracleDBInterop` | none |
| `tracker sync` (bare: `Missing command.`, exit 2) | done | `TestOracleProvidersAndHelp` | none |
| `tracker sync pull` (`--limit`, `--json`) | done | `TestOracleEgressVerdicts` (absent/refused/fault refusals), `TestOracleSyncBeads`, `TestOracleSyncFP`, `TestOracleDBInterop` | none known |
| `tracker sync push` (`--limit`, `--json`) | done | `TestOracleSyncBeads` (local-authoritative updates, gateway-refused `--status`), `TestOracleSyncBeadsCreateAndMissingCommand` (create, legacy-key failure, missing executable), `TestOracleSyncFP` | none known |
| `tracker sync run` (`--limit`, `--json`) | done | `TestOracleSyncBeads`, `TestOracleSyncFP` | none known |
| `tracker sync publish` (`--json`) | done | `TestOracleUnboundAndBindErrors`, `TestOracleBindStatusMapUnbind` | none (always refused for local providers, as upstream) |

Also proven: Go and the oracle read and update each other's SQLite cache and
credential file on one project (`TestOracleDBInterop`); Go-only coverage in
`TestGoTrackerLifecycle`, `TestGoTrackerSyncWithFakeBeads` and the library
unit tests.

Also tested: config.yaml / credentials parse errors with PyYAML-ruamel and
`tomllib` message text (`TestOracleMalformedFiles`), the `getpass.getuser()`
actor lookup order and the 30 s `bd`/`fp` timeout (`TestOracleSyncActorLookup`,
`TestOracleSyncTimeout`).

## Irreducible differences

- Uncaught exceptions (`sqlite3.IntegrityError`, `FileNotFoundError` for a
  missing `bd`/`fp`, `spec_kitty_tracker` errors, `TrackerConfigError` from
  the sync commands' root lookup, `CalledProcessError` / `TimeoutExpired`
  chains) print rich's framed traceback: same panel geometry, title,
  "The above exception was the direct cause..." separators and exception
  lines; the frames inside the panel are rendered sourceless (`in
  _run_or_exit:98`), because upstream's show the wheel's site-packages paths
  and source excerpts.
- `.kittify/config.yaml` written for a document whose merged map is shared
  by several `<<:` referers: ruamel updates referers on delete (`_ref`);
  `rtyaml` does not (the tracker never deletes merged keys).
- The SQLite cache's rewritten tracker tables are laid out by sqlitedb's
  bulk loader, not SQLite's incremental b-tree balancing: same rows, indexes
  and integrity, different cell placement inside those tables' pages.

## Remaining work

None. (`agent tasks map-requirements --tracker-ref` / `move-task
--tracker-ref` record free-form `tracker_refs` strings exactly as upstream,
with no link to tracker bindings: not a difference. `agent mission create`
refuses a staged `.kittify/pending-origin.yaml` with upstream's
`PendingOriginConsumerNotRegisteredError` text, CORE-09,
`TestDiffMissionCreatePendingOrigin`.)

## Library API

### `internal/tracker`

Configuration (`.kittify/config.yaml` `tracker:` block):

- `LocalProviders`, `RemovedProviders`, `OwnershipModes`; `IsLocal`,
  `IsRemoved`, `NormalizeProvider`.
- `ProjectConfig` (`Provider`, `BindingRef`, `ProjectSlug`, `DisplayLabel`,
  `ProviderContext`, `Workspace`, `OwnershipMode`, `FieldOwners`, `Egress`
  (raw value or `EgressAbsent`), `EgressStyle`, `Extra`; the loaded
  round-trip tracker block rides along unexported), `NewProjectConfig`,
  `FromDict`, `(ProjectConfig).ToDict/IsConfigured/EgressFault`.
- `LoadConfig(root)`, `SaveConfig(root, cfg)` (ruamel round trip, atomic
  like `atomic_write`), `ClearConfig(root)` (round trip, keeps a recorded
  egress with its quote style),
  `ConfigPath`, `RequireRepoRoot`.
- Egress: `Verdict(root) EgressVerdict` (`Refused`, `State`, `Raw`,
  `Message`, `Remedies`, `Render()`), constants `EgressRefused`,
  `EgressPermitted`, `LocalSubprocess`.

Credentials (`<runtime root>/credentials`, TOML, owner-only, sidecar lock):

- `RuntimeRoot()`, `DefaultCredentialsPath()`, `CredentialStore{Path}`,
  `NewCredentialStore`, `Load/Save/GetProvider/SetProvider/ClearProvider`.
- `ParseTOML(src) (*jsonx.Object, error)` (`internal/pytoml.Loads`:
  CPython 3.13 `tomllib`, exact `TOMLDecodeError` text), `DumpTOML(obj)`
  (python `toml.dumps`, quirks included; `pytoml` date/time values).

Cache (`<runtime root>/trackers/<scope>.db`):

- `BuildScope`, `DefaultDBPath`, `DBPath(cfg, creds)`, `TrackersDir`.
- `Store{Path}`, `OpenStore(path)` (creates missing tables),
  `ListIssues`, `CountIssues`, `GetIssue`, `UpsertIssue`, `GetCheckpoint`,
  `SetCheckpoint`, `UpsertMapping` (ON CONFLICT upsert, UNIQUE ->
  `*Crash{IntegrityError}`), `ListMappings`; `Mapping` (+`JSON()`),
  `Checkpoint`.

Models / sync:

- `ExternalRef` (`NewRef`, `Identity`), `Issue` (`Validate`, `Clone`,
  `Equal`), `Link`, `PyTime` (`ISO`, `DateISO`), `ParseISO`
  (`datetime.fromisoformat`), vocabularies `CanonicalStatuses`,
  `CanonicalIssueTypes`, `LinkTypes`, `CoreIssueFields`.
- `Connector` interface, `BeadsConnector` (program-gateway allow-list
  enforced), `FPConnector`, `Page`, `Patch`, `Capabilities`.
- `Engine{Connector, Store, Policy, Checkpoint}` with `Pull`, `Push`,
  `Sync`; `Policy`/`PolicyFor(cfg)`; `SyncResult` (+`JSON(provider)`),
  `Stats`, `Conflict`, `Failure`.
- Facade: `Service{Root, Creds}`, `NewService`, `Bind`, `Unbind`, `Status`,
  `MapAdd`, `MapList`, `Sync(op, limit)`, `SyncPublish`; `ParseKVPairs`.
- Errors: `*Error` (RuntimeError class: one stderr line, exit 1),
  `*ValueError`, `*TrackerError` (spec_kitty_tracker exceptions, `Cause`),
  `*Crash` (uncaught: `Type`, `Msg`, `Cause`, `Frames`;
  `Exception(frames...) *rich.Exception` for `rich.PrintTraceback`),
  `AsCrash`.

### `internal/tracker/sqlitedb`

Dependency-free SQLite 3 file format reader/writer (rowid tables plus
automatic indexes, overflow pages, multi-level b-trees):
`Read(path)`, `Parse(data)` -> `*File` (`ChangeCounter`, `SchemaCookie`,
`Master`, `HasTable`, `Rows`); `Write(path, specs, data, changeCounter,
schemaCookie)`, `Build(...)` (fresh file); `Update(path, ...)` /
`UpdateImage(old, ...)` (PYF-05: rewrites only the given tables and their
automatic indexes in place: foreign tables, user indexes, header fields
(page size, reserved bytes, user_version, application_id, ...) and their
pages are kept; released pages join the freelist and the freelist is reused
first; change counter / version-valid-for / schema cookie as passed);
`AutoIndexName`; `TableSpec{Name, SQL, Indexes}`, `Row{RowID, Values}`,
`MasterEntry`; record helpers `EncodeRecord`, `DecodeRecord`, `Compare`
(BINARY collation order). Files pass `PRAGMA integrity_check` and
round-trip through the sqlite3 CLI (`sqlitedb_test.go`, `update_test.go`).
`Store` mutations use `Update`.

## Foundation changes

None in this area's files beyond the above. Shared packages this area
added or extended (`internal/rich`, `internal/pyyaml`, `internal/yamlx`,
`internal/pytoml`, `internal/rtyaml`) are documented in util.md.
