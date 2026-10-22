# Areas C + K parity: doctrine, charter, profiles, Ops, glossary

Packages: `internal/doctrine` (offering/pack loading, activation, DRG,
mission types, profiles), `internal/charter` (charter bundle, context
render, bootstrap renderer, compiler, activation writes, presets, interview,
language inference, status/freshness, synthesis seed, schema validator),
`internal/invocation` (governed Ops: router, routing catalog, model-routing
recommendation, kitty-ops trail, executor), `internal/glossary` (seed store,
validation, chokepoint, events), commands in `internal/commands/doctrine`.

Status legend: **done** = output/JSON/exit codes/files match the oracle in
the tested scenarios; **partial** = main paths match, listed gaps remain;
**missing** = not registered.

Tests: `internal/commands/doctrine/*_test.go` (Go-only fixtures always run;
`TestParity*` run against the oracle when `SPEC_KITTY_ORACLE` is set),
`internal/doctrine/doctrine_test.go`, `internal/yamlx/wrap_test.go`.

| Command | Status | Tests | Deviations / notes |
|---|---|---|---|
| `charter context` | done | TestParityProfilesMissionCharterContext, TestParityCharterGenerateAndRuntime | bootstrap: org-provenance suffixes and the activations stanza (`governance.activations`) are not rendered |
| `charter activate` / `deactivate` | done | TestParityCharterActivation, TestCharterActivationGoOnly, TestParityCharterActivateRegistersDirectWrite | direct-write registration added by area K |
| `charter list` | done | TestParityCharterActivation | |
| `charter interview` | done | TestParityCharterGenerateAndRuntime (defaults), manual (interactive) | |
| `charter generate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | `--owned-checkout` accepted, not resolved |
| `charter status` | done | TestParityCharterGenerateAndRuntime | org-layer edge counts exclude augmentation-derived edges; manifest/provenance pydantic messages approximated |
| `charter synthesize` | done | TestParityCharterGenerateAndRuntime, TestParityCharterSynthesizePipeline, TestParityCharterSynthesizeDirectWrite, TestCharterSynthesizeGoOnly | full pipeline (area K): generated/fixture adapters, schema gate (pydantic error dicts), neutrality gate, staging/.failed, project overlay emit + validation gate, reconcile preserve/prune/orphan refusal, manifest-last promote, post-condition, direct-write registration lane. Deviations: fixture root is `<exe dir>/tests/charter/fixtures/synthesizer` (override `SPEC_KITTY_SYNTH_FIXTURE_ROOT`); YAML parse-error text (yaml.v3); `.failed/cause.yaml` carries no Python traceback |
| `charter resynthesize` | done | TestParityCharterSynthesizePipeline | topic resolver (kind:slug / DRG URN / interview section, Levenshtein candidates, exit 2), bounded promote, EC-4 no-op |
| `charter preflight` | done | TestParityCharterGenerateAndRuntime | auto-refresh runs this binary for `spec-kitty ...` steps |
| `charter bundle validate` | done | TestParityCharterGenerateAndRuntime | |
| `charter new` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | |
| `charter validate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | YAML parser error text differs (yaml.v3 vs ruamel) |
| `charter pack list/path`, `pack asset list/path` | done | TestParityCharterActivation | built-in pack path is the Go cache, by design |
| `charter mission-type list` | done | manual | |
| `charter consistency-check` | done | TestParityCharterConsistencyCheck, TestCharterConsistencyCheckGoOnly | area K |
| `charter lint` | done | TestParityCharterLint, TestParityCharterLintOrg, TestCharterLintGoOnly | area K; YAML parse-error text differs |
| `charter fetch` | done | TestParityCharterFetch, TestParityCharterFetchHTTPS, TestCharterFetchHTTPSGoOnly | area K: git, https/artifactory bundle (local TLS CA via SSL_CERT_FILE), api sources; python-requests error text emulated for refused/unresolvable hosts only |
| `charter org` (group), `org init`, `org validate` | done | TestParityCharterOrgInitAndValidate, TestCharterPackToolsGoOnly | area K |
| `charter pack validate/assemble/regenerate-graph` | done (regenerate-graph partial) | TestParityCharterOrgInitAndValidate, TestParityCharterPackAssemble, TestCharterPackToolsGoOnly | area K; regenerate-graph writes the embedded shipped fragments (the DRG extractor is not ported); agent-profile jsonschema errors approximated |
| `mission list/show`, `mission-type list/show` | done | TestParityProfilesMissionCharterContext | |
| `mission current/info/switch` (and `mission-type` aliases) | done | manual | |
| `mission close/follow-up/reopen/run` | missing | | mission-state commands (area A territory) |
| `profiles list/show/get` | done | TestParityProfilesMissionCharterContext | |
| `agent profile list/show/get` (hidden) | done | TestParityProfilesMissionCharterContext | |
| `dispatch` | done | TestParityDispatchFallback, TestParityDispatchRouting, TestDispatch* | compiled glossary semantic notices not rendered |
| `profile-invocation complete` | done | TestParityProfileInvocationComplete, TestDispatchOpenCompleteList | |
| `doctor ops` (`--close-stale`), `doctor invocation-pairing` | done | TestParityDoctorOpsAndPairing, TestParityDoctorOpsSweepTopic, TestDoctorOpsGoOnly | area K; sweep appends `kitty-ops/op-closures.jsonl` and commits |
| `doctor mission-type` | done | TestParityDoctorMissionType, TestDoctorMissionTypeGoOnly | area K |
| `doctor charter-packs` | done | TestParityDoctorCharterPacks, TestParityDoctorCharterPacksFragments, TestDoctorCharterPacksGoOnly | area K; python `warnings` lines (PACK_LEVEL_MISSING, collisions) not emitted on stderr; org_charter `extends` chains ignored |
| `invocations list` | done | TestParityDispatchFallback, TestParityProfileInvocationComplete | |
| `ops log` / `ops undo` | done | TestParityDispatchFallback, TestDispatchRoutingErrors | |
| `agent context resolve` | done | TestParityAgentContextResolve, TestAgentContextResolveGoOnly | `--owned-checkout` accepted but not resolved |
| `glossary list/conflicts/show/validate/resolve` | done | TestParityGlossaryAndDocs, TestGlossaryGoOnly | `show` never renders compiled entity pages |
| `docs query` | done | TestParityGlossaryAndDocs | duplicates D0's `internal/commands/util/docs` (one must be dropped at merge) |

## Known deviations

- Help screens are rendered by the shared `internal/cli` renderer (column
  padding differs from rich-click); command/flag/argument sets match.
- Built-in doctrine paths printed in `charter context` reference lists and
  `pack path` point at the materialised Go cache instead of site-packages.
- Language inference (`catalog.languages`, scope-filtered references, the
  bundle language gate, the charter-extension advisory) follows upstream;
  dispatch routing does not yet apply the language filter.

## Area K additions (wave 4)

New library code (all additive):

- `internal/charter/orgpacks.go` — org pack registry: `OrgPackConfig`, `PackRegistry`,
  `LoadPackRegistry`, `LocalPathRoot`, `EffectiveRoot`, `ResolveWithinRoot`,
  `ResolvePackChain`, `ProjectPackRootIfExists`.
- `internal/charter/orgdrg.go` — org DRG fragments and three-layer merge:
  `LoadOrgPack`, `LoadOrgDRG`, `MergeThreeLayers`, `MergedGraph`, `OrgConflictError`,
  `ValidateDanglingReferences`, `BuiltInMergedGraph`, `GraphFromDoctrine`, `IsValidURN`.
- `internal/charter/override_policy.go` — `LoadEffectiveOverridePolicy`,
  `FindOverriddenBuiltins`, `AdjudicateOverrides`, `RenderSanctionEntries`.
- `internal/charter/packhealth.go` — `CollectCharterPackHealth`, `CollectOrgLayerData`,
  retired key/layout checks, selection blocks, org charter policy.
- `internal/charter/lint.go`, `consistency.go`, `drgschema.go` — `RunLint`,
  `RunConsistencyCheck`, DRGGraph pydantic emulation.
- `internal/charter/packvalidate.go`, `packassemble.go`, `regengraph.go`,
  `orgscaffold.go`, `fetch.go` — `ValidatePack`, `AssemblePack`, `WritePackManifest`,
  `WriteBuiltInGraph`, `WriteOrgScaffold`, `RenderOrgPack`, `FetchPack`.
- `internal/charter/synth_request.go`, `synth_pipeline.go`, `synth_write.go`,
  `synth_register.go` — synthesizer: `BuildSynthesisRequest`, `CollectEvidence`,
  `NormalizeInterviewSnapshot`, `RunAll`, `Synthesize` (`ModePreserve`/`ModePrune`),
  `SynthesisDryRun`, `ReconciliationPreview`, `Resynthesize`, `SynthManifest`,
  `ScanProjectArtifacts`, `PlanProjectRegistration`, `CommitProjectRegistration`,
  `SynthesizeProjectLayer`, error type `SynthError` (Type = Python class name),
  `DRGLoadError`.
- `internal/invocation/doctor.go` — `ListOrphanOps`, `CloseStaleOps`, `SweepReport`,
  `ReadLifecycleRecords`, `DoctorOrphanReport`.
- `internal/rich/table_doctrine.go` — `BoxTable`, `FitPanel`, `GridTable`
  (`internal/commands/doctrine/rich.go` keeps thin wrappers).

Foundation changes: `internal/charter/synthesis.go` `canonicalYAML` now wraps at
ruamel width 80 (matches upstream `canonical_yaml`). `charter activate` now plans and
commits direct-write project registration (area K; `--resynthesize` preflight not ported).
