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
  only the site-packages-derived path in the "fixture not found" text differs.
- DOC-06: `.failed/cause.yaml` `traceback` is `traceback.format_exc()` upstream
  (Python source frames: site-packages paths, line numbers and excerpts);
  Go records the reason instead.
- Python `warnings` records name the emitting file by its absolute
  site-packages path (`.../charter/org_pack_loader.py:624: UserWarning: ...`);
  Go prints the same text with the site-packages-relative module path, so
  rich's wrapping of the record (by that path's length) differs too.
- `additionalProperties` with a schema value: errors for several extra keys
  come in the iteration order of jsonschema's
  `set(find_additional_properties(...))`, which follows per-process
  string-hash randomisation. (FIX5 check: the `additionalProperties: false`
  message is not affected — python-jsonschema 4.26 sorts the keys,
  `sorted(extras, key=str)`, and Go matches.)
- `retrospective.proposal.rejected` events for multi-proposal conflict
  groups: upstream iterates a `set[str]` of proposal ids (hash-randomised per
  process); Go uses first-group order.

## Remaining work

- None. (`reproject_pack_skills` after skill (de)activation is done by area
  TSE, see setup.md.)

## Wave 5 sweep (area SWEEP)

- `charter activate mission-type` runs `_emit_step_removal_warnings`
  (FR-008: steps of the registered type's resolved sequence, with the
  single-level `extends` fallback, missing from the incoming own sequence;
  one `⚠ Step '<id>' removed by mission-type override.` block per step,
  listing every in-flight WP — lanes in_progress / for_review / in_review /
  claimed — of every `kitty-specs/*` mission) and the FR-001 preflight
  `validate_activatable_mission_type` (`mission type `<id>` resolved from
  layer `<layer>` has an empty action sequence.`), both over the strict
  layered roster (`resolve_layered_mission_types`): a malformed
  mission-type file anywhere in the built-in / org / project layers refuses
  with the loader's ValueError (malformed YAML, non-mapping document,
  pydantic `MissionType` validation, id/stem mismatch). The invented
  "declares an empty action_sequence" message is gone.
  `TestParityMissionTypeStepRemoval`.
- `charter activate --preset` id resolution is `_resolve_preset_ids`:
  anti-pattern ids are checked against `load_validated_graph` (org roots and
  fragments included); a DRG load / validation failure becomes the
  `activated_anti_patterns: the DRG cannot be loaded to check anti-pattern
  ids: ...` reason (headline "lists ids that cannot be checked against the
  offering"); an org-merge refusal crashes uncaught (traceback); the strict
  org-pack chain (`_org_roots_or_refuse`: unreadable registry, unfetched
  declared pack) and the org charter (`_org_required`, whose required ids are
  now unioned into the written keys and the kind gate) refuse under
  `charter_packs.org.packs`; mission types use the layered roster (a
  malformed file is the `the mission-type roster cannot be read: ...`
  reason) and `validate_activatable_mission_type` refusals.
  `TestParityPresetResolution`.
- `charter pack regenerate-graph` applies Python value semantics to the
  artifact YAML (ruamel safe value space: YAML 1.2 ints, dates): non-str
  directive ids reach `re.match` (`TypeError: expected string or
  bytes-like object, got 'int'`), non-mapping reference / step entries
  (`AttributeError: 'str' object has no attribute 'get'`), non-iterable
  lists (`'int' object is not iterable`), unhashable reference types, f-string
  formatting of non-str ids, DRGNode / DRGEdge pydantic validation at
  construction (labels, `when`/`reason`, URNs, with the kwargs input repr),
  unvalidated `model_copy` labels failing at the `generate_graph` merge, the
  operating-procedure collection (non-mapping collaboration, mixed-type sort
  TypeError) and a non-str mission-type id reaching the step repository
  (`unsupported operand type(s) for /: 'PosixPath' and 'int'`); every crash
  carries upstream's frames. `TestParityRegenerateGraphMalformedYAML`.
- `ensure_charter_bundle_fresh` logs `Failed to evaluate charter bundle
  freshness: <UnicodeDecodeError>` and `Charter auto-sync failed while
  refreshing extracted artifacts: <code>` for an undecodable charter.md
  (human mode); `next`'s prompt build runs under upstream's
  stderr redirect, so it stays silent there.
  `charterenc.TestDiffPromptCommandsAmbiguousCharter`.
- `TestParityDispatchFallback`: the `ops log` reflog minute is masked (the
  two sandboxes' commits may straddle a minute).

New / extended library API (additive):

- `internal/charter/regengraph_py.go`, `regengraph_walk.go` (unexported
  extractor walkers); `presets_resolve.go`: `RequireDeclaredOrgRoots`,
  `PresetUnexpectedError`.
- `internal/doctrine/missiontypes_layered.go`: `LayeredMissionTypes`,
  `ExtendsFallbackSequence`, `MissionTypeSourceLayer`,
  `EmptyActionSequenceMessage`, `MissionTypeLoadError`,
  `ScanMissionTypeIDs`, `BuiltInMissionTypesDir`.
- `internal/pyyaml/load_input_py.go`: `LoadRuamelInputPy`.

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

## Wave 5 FIX4: declared-but-unfetched org packs

Every upstream caller of the strict chain (`resolve_pack_chain(strict=True)`
/ `require_declared_org_roots`) and of the effective-set seam was audited
against the oracle with a declared `charter_packs.org.packs` entry whose
root is missing.

| Command path(s) | Status | Differential test | Notes |
|---|---|---|---|
| `upgrade --yes [--json]` | done | `TestUpgradeCutoverUnfetchedOrgPackMatchesOracle` (setup) | agent-profile assessment (`_profile_input_roots`) fails closed: `repair_preparation_failed`, the not-fetched error, the repair-errors no-commit warning, no commit, old version stamp kept; the re-run re-reports (human output compared too) |
| `upgrade` (`unify_charter_activation_promote_answers`) | done | `TestUpgradePromoteAnswersMatchesOracle`, `TestUpgradePromoteAnswersUnfetchedPackMatchesOracle` | selections resolve via `resolve_selected_id_to_stem` (declared directive ids); an absent key is seeded from the effective set (absent-key parity warnings) or left absent with the reason; dry-run preview |
| `charter activate <kind> <id>` | done | `TestUnfetchedOrgPackMutationsMatchOracle` | strict chain read before the activation (`Error: declared org pack ...`) |
| `charter activate ... --resynthesize` | done | same | preflight reports the effective-set reason |
| `charter activate mission-type`, `mission-type list/show`, `charter mission-type list`, `agent mission setup-plan`, `agent mission create`, `orchestrator-api design-context` | done | `TestUnfetchedOrgPackSweepMatchesOracle`, `TestUnfetchedOrgPackMutationsMatchOracle`, `TestDiffUnfetchedOrgPackMissionFlow` (runtime) | `resolve_org_dirs`' dropped-root WARNING per `resolve_mission_type_context` call (`charter.WarnMissionTypeContext`) |
| `agent tasks move-task --to for_review` | done | `TestDiffUnfetchedOrgPackMissionFlow` | gate bindings read the chain strictly: `Pre-review regression gate: no_coverage — pre-review gate resolution failed — unverified: ...`, plus the `mission_step_contracts` WARNING |
| `dispatch` | done | `TestUnfetchedOrgPackMutationsMatchOracle` | `is_charter_empty` counts declared packs (`PackContext.org_roots`), so the router runs (`ROUTER_AMBIGUOUS`) instead of the generic fallback |
| `charter deactivate/interview/generate/synthesize/resynthesize/status/context/list/lint/validate/consistency-check/preflight/bundle validate/pack list/pack validate`, `doctor skills/charter-packs/tool-surfaces`, `profiles list`, `agent profile list`, `agent config status/sync`, `verify-setup`, `regen`, `next`, `agent action implement/review`, `agent mission check-prerequisites/record-analysis`, `mission info` | matched (no change needed) | the three sweeps above, `TestDiffUnfetchedOrgPackNextEarly` | lenient surfaces skip the pack exactly as upstream |

Library: `charter.EffectiveSet(root, token) (ids, reason)` (fail-closed
effective set; `EffectiveIDs` wraps it), `charter.WarnDroppedOrgDirs(root,
subdir)`, `charter.WarnMissionTypeContext(root, typeKey)`,
`charter.ProjectHasPackOverrides(root)`; `agents.ProfileAdmitter` (one
profile gate per projector build, replacing `agents.ProfileAdmit`).

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

## Wave 5 (area COLOR)

Console output keeps upstream's markup and rich renderables (tables, panels,
grids, trees, glossary Markdown pages) so a colour terminal gets the same SGR
stream; see help.md "Colour". Functional fixes found by the colour harness
(`internal/colorparity` `TestNoProjectColour`, every command run bare in an
empty directory):

- `charter context / generate / interview / status / synthesize /
  resynthesize / preflight` resolve the root with `task_utils.find_repo_root`
  (`findRepoRoot`): outside any project or repository they print "Unable to
  locate repository root (missing .git or .kittify)." (exit 1; preflight
  exit 2 on stderr) instead of running against the cwd.
- `profiles list/show`, `agent profile list`, `invocations list` and
  `profile-invocation complete` let that TaskCliError escape as rich's
  traceback (`repoRootOrCrash`), as upstream does.
- `mission-type current/info` use `get_project_root_or_exit`
  (`projectRootOrExit`).
- `ops log` only needs git on PATH (upstream `get_vcs`): outside a
  repository the reflog is empty ("No operations found").
- `charter interview`: end of input at a prompt is click's Abort, reported as
  an empty "Unexpected error:" (exit 1).
- `charter synthesize/resynthesize` error panels: red border, bold red title.
