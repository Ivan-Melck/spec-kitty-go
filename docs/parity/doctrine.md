# Areas C + K parity: doctrine, charter, profiles, Ops, glossary

Packages: `internal/doctrine` (offering/pack loading, activation, DRG,
mission types, profiles, `load_validated_graph`), `internal/charter` (charter bundle, context
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
`internal/doctrine/doctrine_test.go`, `internal/yamlx/wrap_test.go`, and the wave-5
library goldens (`internal/pydantic`, `internal/pydifflib`, `internal/jsonschema`,
`internal/invocation/pyunicode_test.go`, `internal/glossary/notices_test.go`).

| Command | Status | Tests | Deviations / notes |
|---|---|---|---|
| `charter context` | done | TestParityProfilesMissionCharterContext, TestParityCharterGenerateAndRuntime, TestParityCharterContextOrgSelectionsAndActivations, TestParityLoadValidatedGraph | bootstrap renders org-provenance suffixes and the `Selected activations:` stanza (DOC-01/02); compact mode has the languages, project-root, authority and action-critical blocks; DRG load failures degrade with the upstream warning; built-in paths point at the Go cache (irreducible, DOC-20) |
| `charter activate` / `deactivate` | done | TestParityCharterActivation, TestCharterActivationGoOnly, TestParityCharterActivateRegistersDirectWrite, TestParityCharterActivateResynthesize, TestCharterActivateResynthesizeGoOnly | `--resynthesize` preflight + full generate/synthesize, tension warnings, linked-worktree refusal (DOC-15) |
| `charter list` | done | TestParityCharterActivation | |
| `charter interview` | done | TestParityCharterGenerateAndRuntime (defaults), TestParityCharterInterviewOrgPrefill, manual (interactive) | org charter prefill/promotion messages and `org_charter` JSON block |
| `charter generate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly, TestDiffOwnedCharterAndOrchestratorDecisions | `--owned-checkout` validated and resolved (explicit claim + `--mission-handle`, owned destinations); see [owned.md](owned.md) |
| `charter status` | done | TestParityCharterGenerateAndRuntime, TestParityDoctorCharterPacksWarnings, TestParityCharterStatusManifestProvenanceErrors | org layer merges the full graph incl. augmentation edges (DOC-03); manifest/provenance errors are pydantic-exact (DOC-04) |
| `charter synthesize` | done | TestParityCharterGenerateAndRuntime, TestParityCharterSynthesizePipeline, TestParityCharterSynthesizeDirectWrite, TestCharterSynthesizeGoOnly, TestParityCharterStatusManifestProvenanceErrors | full pipeline (area K). Irreducible: fixture root location (DOC-05) and the Python frames in `.failed/cause.yaml` (DOC-06) |
| `charter resynthesize` | done | TestParityCharterSynthesizePipeline | topic resolver (kind:slug / DRG URN / interview section, Levenshtein candidates, exit 2), bounded promote, EC-4 no-op |
| `charter preflight` | done | TestParityCharterGenerateAndRuntime | auto-refresh runs this binary for `spec-kitty ...` steps |
| `charter bundle validate` | done | TestParityCharterGenerateAndRuntime | |
| `charter new` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | |
| `charter validate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | ruamel safe-loader parse-error text |
| `charter pack list/path`, `pack asset list/path` | done | TestParityCharterActivation | built-in pack path is the Go cache, by design |
| `charter mission-type list` | done | manual | |
| `agent retrospect synthesize` (closeout package, synthesize path only) | done | TestDiffRetrospectSynthesizeLegacy (closeout) | legacy-schema records: dry-run/`--apply`, `--proposal-id` refusals, staleness against the primary feature dir, conflicts (exit 4), stale/invalid (exit 5), glossary/DRG/charter-pack/flag writes, provenance sidecars, `retrospective.proposal.*` events on the coordination status surface; `--fabricate-empty` (DOC-19) |
| `charter consistency-check` | done | TestParityCharterConsistencyCheck, TestCharterConsistencyCheckGoOnly, TestParityLoadValidatedGraph | area K; `load_validated_graph` errors (DOC-16) |
| `charter lint` | done | TestParityCharterLint, TestParityCharterLintOrg, TestCharterLintGoOnly, TestParityDoctorCharterPacksWarnings | area K; layer-collision warnings on stderr |
| `charter fetch` | done | TestParityCharterFetch, TestParityCharterFetchHTTPS, TestCharterFetchHTTPSGoOnly, TestParityCharterFetchUntrustedTLS | python-requests texts for refused/unresolvable hosts, TLS verification failures and read timeouts (DOC-09) |
| `charter org` (group), `org init`, `org validate` | done | TestParityCharterOrgInitAndValidate, TestCharterPackToolsGoOnly | area K |
| `charter pack validate/assemble/regenerate-graph` | done | TestParityCharterOrgInitAndValidate, TestParityCharterPackAssemble, TestCharterPackToolsGoOnly, TestParityAgentProfileSchemaMessages, TestParityRegenerateGraphExtractor | agent-profile errors from a Draft 7 jsonschema port (DOC-08); regenerate-graph runs the ported DRG extractor + calibrator + hand-authored overlay + built-in manifest generator (DOC-07), `--check`, `invalid` and fail-closed ValueError tracebacks |
| `mission list/show`, `mission-type list/show` | done | TestParityProfilesMissionCharterContext | |
| `mission current/info/switch` (and `mission-type` aliases) | done | manual, `TestDiffPlacementSeamHandleReads` (`mission current` over every handle form) | `mission current` resolves `--mission` like `resolve_feature_dir_for_mission` (identity ladder + bare-modern fold; ambiguous / missing handle = `ActionContextError` crash, OWN-10) |
| `mission close/follow-up/reopen/run` | done | see runtime.md | registered by the runtime area (DOCS-02) |
| `profiles list/show/get` | done | TestParityProfilesMissionCharterContext | |
| `agent profile list/show/get` (hidden) | done | TestParityProfilesMissionCharterContext | |
| `dispatch` | done | TestParityDispatchFallback, TestParityDispatchRouting, TestParityDispatchLanguageFilter, TestDispatch*, TestCollectNoticesAndRender (Go-only: a fresh invocation id cannot match pre-seeded oracle events) | language-scope filter (DOC-11); inline glossary drift notices (DOC-10); CPython `\w`/`str.lower` tables (DOC-18) |
| `profile-invocation complete` | done | TestParityProfileInvocationComplete, TestDispatchOpenCompleteList | |
| `doctor ops` (`--close-stale`), `doctor invocation-pairing` | done | TestParityDoctorOpsAndPairing, TestParityDoctorOpsSweepTopic, TestDoctorOpsGoOnly | area K; sweep appends `kitty-ops/op-closures.jsonl` and commits |
| `doctor mission-type` | done | TestParityDoctorMissionType, TestDoctorMissionTypeGoOnly | area K |
| `doctor charter-packs` | done | TestParityDoctorCharterPacks, TestParityDoctorCharterPacksFragments, TestDoctorCharterPacksGoOnly, TestParityDoctorCharterPacksWarnings, TestParityDoctorCharterPacksExtends | logger/warnings records on stderr with the warnings-registry resets (DOC-12); org_charter `extends` chains (DOC-13) |
| `invocations list` | done | TestParityDispatchFallback, TestParityProfileInvocationComplete | |
| `ops log` / `ops undo` | done | TestParityDispatchFallback, TestDispatchRoutingErrors | |
| `agent context resolve` | done | TestParityAgentContextResolve, TestAgentContextResolveGoOnly, TestDiffOwnedLifecycleWalk | `--owned-checkout` / flagless adoption resolved (stale-copy warning in `warnings`); see [owned.md](owned.md) |
| `glossary list/conflicts/show/validate/resolve` | done | TestParityGlossaryAndDocs, TestGlossaryGoOnly, TestParityGlossaryShowEntityPages | `show` renders compiled entity pages (DOC-14) |
| `docs query` | done | TestParityGlossaryAndDocs | registered once, by `internal/commands/util/docs` (DOCS-02) |

## Known deviations

- Help screens are byte-identical (`go test ./internal/helpparity`).
- YAML parse errors carry ruamel's / PyYAML's exact text (PYF's
  `internal/pyyaml`); the DOC loaders select the upstream loader per call
  site (`YAML(typ="safe")` over a path or a str, PyYAML `safe_load`).
- Uncaught Python exceptions print the rich traceback panel
  (`rich.Traceback`) with the upstream function names and line numbers;
  frames show `in <name>:<line>` because the Python source is not on disk,
  and ruamel-internal frames below a YAML load are not listed.

## Irreducible differences

- DOC-20: built-in doctrine paths (`charter context` reference lists,
  `charter pack path`) name the materialised Go cache
  (`~/.kittify/cache/packs/<hash>/built-in`) instead of the Python
  site-packages tree; there is no site-packages in a Go install.
- DOC-05: the fixture adapter root is resolved from the package location
  upstream (`<site-packages>/../../tests/charter/fixtures/synthesizer`); the
  Go binary uses `<exe dir>/tests/charter/fixtures/synthesizer`
  (`SPEC_KITTY_SYNTH_FIXTURE_ROOT` overrides). Neither exists in an install;
  only the path in the "fixture not found" text differs.
- DOC-06: `.failed/cause.yaml` `traceback` is `traceback.format_exc()` upstream
  (frames with site-packages paths and line numbers of the Python source);
  Go records the reason instead.
- Python `warnings` lines name the emitting site-packages file and line
  (`.../charter/org_pack_loader.py:624: UserWarning: ...`); Go prints the
  same text with the upstream relative module path.
- Set-iteration order upstream (nondeterministic): additionalProperties
  jsonschema messages listing several keys, and the order of
  `retrospective.proposal.rejected` events for multi-proposal conflict groups
  (Go uses first-group order).

## Remaining work

- Mission-type activation step-removal warnings (FR-008).
  (`reproject_pack_skills` after skill (de)activation is done by area TSE,
  see setup.md.)
- Preset anti-pattern DRG validation at `charter activate --preset` (rare
  authoring error path).
- `charter pack regenerate-graph` extractor edge cases: YAML 1.2 typing of
  unquoted ids (`id: 024`) and non-mapping reference entries crash upstream
  with Python TypeErrors/AttributeErrors; Go skips them.

## Wave 5 follow-up (after the ENC / PYF merges)

- Tracebacks: `regenerate-graph` crashes (`RegenCrash.Phase`/`Frames`),
  `charter activate` registration and the activate/deactivate cascade
  DRGValidationError go through `rich.Traceback`; tests compare the frame
  lists (`TestParityRegenerateGraphExtractor`, `TestParityLoadValidatedGraph`).
- YAML error text masks removed from the pack-tool and synthesizer parity
  tests.
- Encoding provenance: the extra `infer_repo_languages` passes upstream makes
  (bundle `_drop_scope_filtered_ids`, the dispatch executor's second service
  build, the first activation-aware profile lookup, pack-skill health,
  consistency-check's cwd-relative directive service) now record their
  interview read (`TestParityEncodingProvenanceDoctrineFlows`).
- `render_yaml_document` (activation writes to config.yaml / charter.yaml,
  the `charter:` pointer write) renders changed entries through the ruamel
  round trip (`internal/rtyaml`): lead comments stay, the key's line and inner
  comments are kept as ruamel keeps them (`TestParityCommentedConfigRoundTrip`).
- `internal/migrate` renders `str(ValidationError)` through `internal/pydantic`
  (its private formatter is gone; output unchanged, maintenance reindex tests).

## Wave 5 (area DOC) library additions

New packages / files (all additive):

- `internal/pydantic` — pydantic v2 `ValidationError` text: `ErrorDetail`,
  `ValidationError` (`New`, `Add`, `Len`, `Error`, `ErrorsRepr`), `Format`,
  `Detail`, `SplitLoc`, `FormatLoc`, `LocRepr`, `Truncate`, `InputRepr`,
  `Repr`, `TypeName`, `RuamelTypeName`, `ReprDatetime`, `PyValue`, `URL`,
  `Version`; message helpers `ModelTypeMsg`, `ValueErrorMsg`,
  `AssertionErrorMsg`, `ExpectedOr`, `ExpectedStrings`, `LiteralMsg`,
  `LiteralCtx`, `StringTooShortMsg`, `StringTooLongMsg`, `TooShortMsg`,
  `TooLongMsg`, `PatternMsg`, `GreaterEqualMsg`, `LessEqualMsg`,
  `GreaterThanMsg`.
- `internal/pydifflib` — exact CPython `difflib`: `Matcher`, `New`, `Block`,
  `Matcher.MatchingBlocks`, `Matcher.Ratio`, `GetCloseMatches`.
- `internal/jsonschema` — Draft 7 validator with python-jsonschema 4.26
  messages: `Validator`, `New`, `Validator.IterErrors`, `Error`,
  `Error.PathString`.
- `internal/doctrine/drgvalidate.go` — `DRGValidationError`,
  `DanglingEndpoints`, `DuplicateEdgeTriples`, `ValidateDangling`,
  `CycleErrors`, `ValidateGraph`, `AssertValid`, `LoadValidatedGraph`,
  `DRGErrorClass`.
- `internal/charter`: `drgload.go` (`DRGProjectValidationError`,
  `GraphOptions`, `LoadOrgDRGLenient`, `LoadOrgValidatedGraph`,
  `IsUnexpectedError`, `PyExceptionClass`); `resynth_preflight.go`
  (`ScanUnreconciledTensions`, `FutureSelections`, `PreflightActivationRoots`,
  `PreflightProjectLayer`); `orgprefill.go` (`OrgRequirableKinds`, `OrgPolicy`,
  `LegacyOrgPolicy`, `ApplyOrgCharterToInterview`, `OrgCharterJSONBlock`);
  `activations_render.go` (`ActivationEntry`, `OrgRequiredSelections`);
  `entity_pages.go` (`GenerateEntityPage`, `ErrTermNotFound`);
  `pydmodel.go`/`pydmodel_prov.go` (`ValidateProvenanceEntry`);
  `warnlog.go` (`WarnLog`, `PyWarnLog` sinks); `regengraph.go` /
  `regengraph_extract.go` / `regengraph_data.go` (`RegenStatus`,
  `CheckBuiltInGraph`, `WriteBuiltInGraph`, `BuildBuiltinManifest`,
  `BuiltinManifestFile`, `GraphFragments`, `RegenCrash`; data generated by
  `$REF/work-DOC/gen_regen_data.py`).
- `internal/glossary/notices.go` — `SemanticConflict`,
  `IterSemanticConflicts`, `IterSemanticConflictRecords`, `DriftNotice`,
  `CollectNotices`, `NoticeLines`.
- `internal/invocation/pyunicode.go` (+ generated table) — CPython 3.13 `\w`
  and `str.lower` (unexported, used by the router).
- `internal/retrospective/legacy_record.go`, `synth_apply.go` —
  `LegacyActor`, `LegacyProposal`, `LegacyRecord`, `ParseLegacyRecord`,
  `ReadLegacyRecord`, `ConflictGroup`, `Rejected`, `Planned`, `Applied`,
  `SynthResult`, `DetectConflicts`, `ApplyProposals`, `NextLamport`.
- `internal/cli/invoke.go` — `Invoke(cmd, argv, io)` (in-process command run;
  used by `--resynthesize`).

Process-level change: `internal/commands/doctrine/traceback.go` installs the
CLI logging bootstrap (WARNING records to stderr unless argv requests JSON),
which every command shares, as upstream.

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
commits direct-write project registration (area K; `--resynthesize` added in wave 5).
