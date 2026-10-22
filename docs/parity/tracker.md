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
stderr (uncaught exceptions: the final `Exception: message` line),
`.kittify/config.yaml`, `~/.spec-kitty/credentials`, the fake CLI's argv log
and a `sqlite3` dump of the cache (`PRAGMA integrity_check`, issue rows with
their JSON payloads, checkpoints).

| Command path | Status | Tests | Deviations |
|---|---|---|---|
| `tracker` (bare group: help, exit 0) | done | `TestOracleProvidersAndHelp` | help layout is the shared renderer's; content compared whitespace-collapsed |
| `tracker providers` (`--json`) | done | `TestOracleProvidersAndHelp`, `TestGoTrackerLifecycle` | none |
| `tracker bind` (`--provider/--workspace/--ownership-mode/--field-owner/--credential`) | done | `TestOracleUnboundAndBindErrors` (removed/unknown provider, missing workspace, bad mode, bad key=value), `TestOracleBindStatusMapUnbind`, `TestOracleEgressVerdicts` (egress + unknown keys + quote style carried over) | `.kittify/config.yaml` is re-emitted by `yamlx`: YAML comments and quote styles elsewhere in the file are not preserved (ruamel round-trip keeps them); the quote style of `tracker.egress` itself is preserved |
| `tracker unbind` | done | `TestOracleBindStatusMapUnbind`, `TestOracleEgressVerdicts` (recorded egress outlives the binding), `TestOracleRemovedAndIncompleteBindings` | same YAML note |
| `tracker status` (`--json`) | done | all oracle tests (unbound, configured, inert removed/unknown provider, provider without workspace, non-mapping tracker block) | none known |
| `tracker map` (bare: `Missing command.`, exit 2) | done | `TestOracleProvidersAndHelp` | none |
| `tracker map add` (`--wp-id/--external-id/--external-key/--external-url`) | done | `TestOracleBindStatusMapUnbind` (insert, upsert, UNIQUE violation, blank id), `TestOracleDBInterop` | the `IntegrityError` traceback is reduced to `Traceback` + final line |
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

Other deviations / notes:

- Uncaught exceptions (`sqlite3.IntegrityError`, `FileNotFoundError` for a
  missing `bd`/`fp`, `spec_kitty_tracker` errors such as
  `IssuePayloadContractError`, `ConnectorRequestError` from push's remote
  index, `TrackerConfigError` from the sync commands' pre-boundary root
  lookup) print `Traceback (most recent call last):` plus the final
  exception line instead of rich's framed traceback; exit code 1 as upstream.
- A malformed `~/.spec-kitty/credentials` reports
  `Failed to load credentials: <reason>`; the reason text is this TOML
  parser's, not `tomllib`'s. An unparseable config.yaml likewise reports
  `Failed to parse <path>: <reason>` with yaml.v3's reason.
- The SQLite cache is rewritten whole on every mutation (sqlitedb has no
  in-place page updates). Readers see the same rows; page layout, the
  header's library version number and the change counter cadence differ from
  a file the real SQLite library would produce. Tables/indexes not in the
  tracker schema would be dropped on rewrite (upstream never creates any).
- `bd`/`fp` subprocess timeouts (30 s), `getpass.getuser()` lookup order and
  the gateway token's 300 s TTL are reproduced but not oracle-tested
  (timing-dependent). The gateway token always covers its own connector's
  context, so scope violations cannot occur through the CLI, as upstream.
- `agent tasks map-requirements --tracker-ref` / `move-task --tracker-ref`
  (areas E/A) only record free-form `tracker_refs` strings in WP
  frontmatter; upstream does not link them to tracker bindings or the
  mapping table, so there is nothing to share with this library.
- Observed but out of scope: upstream `agent mission create` raises
  `PendingOriginConsumerNotRegisteredError` when `.kittify/pending-origin.yaml`
  exists (rc8's `specify_cli.tracker` no longer registers a consumer); the Go
  mission-create port does not check for that file.

## Library API

### `internal/tracker`

Configuration (`.kittify/config.yaml` `tracker:` block):

- `LocalProviders`, `RemovedProviders`, `OwnershipModes`; `IsLocal`,
  `IsRemoved`, `NormalizeProvider`.
- `ProjectConfig` (`Provider`, `BindingRef`, `ProjectSlug`, `DisplayLabel`,
  `ProviderContext`, `Workspace`, `OwnershipMode`, `FieldOwners`, `Egress`
  (raw value or `EgressAbsent`), `EgressStyle`, `Extra`), `NewProjectConfig`,
  `FromDict`, `(ProjectConfig).ToDict/IsConfigured/EgressFault`.
- `LoadConfig(root)`, `SaveConfig(root, cfg)` (atomic, 0600 like
  `atomic_write`), `ClearConfig(root)` (keeps a recorded egress),
  `ConfigPath`, `RequireRepoRoot`.
- Egress: `Verdict(root) EgressVerdict` (`Refused`, `State`, `Raw`,
  `Message`, `Remedies`, `Render()`), constants `EgressRefused`,
  `EgressPermitted`, `LocalSubprocess`.

Credentials (`<runtime root>/credentials`, TOML, owner-only, sidecar lock):

- `RuntimeRoot()`, `DefaultCredentialsPath()`, `CredentialStore{Path}`,
  `NewCredentialStore`, `Load/Save/GetProvider/SetProvider/ClearProvider`.
- `ParseTOML(src) (*jsonx.Object, error)` (tomllib-compatible subset incl.
  tables, arrays of tables, inline tables, all string forms, numbers,
  dates as `TOMLDatetime`), `DumpTOML(obj)` (python `toml.dumps`, quirks
  included).

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
  `*ValueError`, `*TrackerError` (spec_kitty_tracker exceptions),
  `*Crash` (uncaught: traceback), `AsCrash`.

### `internal/tracker/sqlitedb`

Dependency-free SQLite 3 file format reader/writer (rowid tables plus
automatic indexes, overflow pages, multi-level b-trees):
`Read(path)`, `Parse(data)` -> `*File` (`ChangeCounter`, `SchemaCookie`,
`Master`, `HasTable`, `Rows`); `Write(path, specs, data, changeCounter,
schemaCookie)`, `Build(...)`; `TableSpec{Name, SQL, Indexes}`, `Row{RowID,
Values}`, `MasterEntry`; record helpers `EncodeRecord`, `DecodeRecord`,
`Compare` (BINARY collation order). Files pass `PRAGMA integrity_check` and
round-trip through the sqlite3 CLI (`sqlitedb_test.go`).

## Foundation changes

None. No existing package was modified.
