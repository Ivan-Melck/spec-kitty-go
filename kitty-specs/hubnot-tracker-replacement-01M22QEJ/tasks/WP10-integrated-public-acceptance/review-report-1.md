# WP10 independent review — PASS

Reviewer: `codex-independent-integrated-acceptance-review`, resolved profile `reviewer-renata`. The full generated prompt and review governance were read. Its inherited implementer identity is template context, not the reviewer's role. No implementation changes were made by this reviewer.

Reviewed and qualified source / approval reference: `e9b1ac659dcb40fe62ac6c991f26ed9fed9e544e`.
Tree: `7df4e8f59a3942cec80dd818c9da9326f52a1151`.

## Scope and independent findings

Exactly three owned files are added: two acceptance-audit test files and qualification methodology. Their final SHA-256 values are:

- `internal/helpercmd/hubnot_acceptance_test.go`: `66b4d4ea31abb395f06c22f01ddaea6d3c683267c13aacd51e11c33b623c1188`
- `internal/helpercmd/hubnot_performance_test.go`: `818e823a97b873938f3bc526561713eba34b262bd9f861233d69306dbf55417d`
- `docs/HUBNOT_QUALIFICATION.md`: `938e21628789b5cf69a6ec96d48857d974dd669e7be3942f482d96473ef4a14e`

The prior root-owned `753a41a9fc7d897a15b2d619798ab8a1662bba2f` synchronization changes only `.kittify/config.yaml` and the mission dossier. It precedes measurement and preserves application bytes. The independently reviewed Go-layout mapping uses `internal/` for actual source and colocated tests; documentation/artifact routing, strict acceptance and test thresholds remain unchanged. This governance synchronization is not an implementer edit to an unowned application file. Approved dependency implementation remains separately attributed. The runtime-created lane review lock is preserved.

Independent bounded review found and reproduced two acceptance-audit defects: unrelated-repository snapshots could prove no-write, and unknown proof kinds bypassed identity checks. The owner corrected both before the measured commit. Unchanged external probes now refuse at the corrected helpers before their explicit unexpected-acceptance markers. Valid retained-artifact controls and permanent mutants passed independently under race in 11.094 seconds. The final normal receipt then exercised those corrected auditors against fresh real behavior. Original red/negative logs, checkpoints and observer-harness corrections are retained.

## Eight-item anti-pattern checklist

1. **Dead code — N/A for new production surface.** WP10 deliberately adds only tests/documentation and introduces no production API requiring a production caller. Both new integrated audit entrypoints are actually executed by the documented final acceptance command; their parsing/comparison helpers have test callers. They do not recursively invoke a campaign or duplicate the provider engine.
2. **Synthetic-fixture test — PASS.** Behavioral evidence comes from the normal compiled qualifier/product/admitted provider on disposable repositories. The new tests independently inspect those captured real requests/results. Minimal malformed-input mutants are explicitly audit-boundary tests, not synthetic passing provider receipts. Removing the source-bound real execution would fail full admission and the auditors.
3. **Silent empty return — PASS.** Missing/ambiguous JSON, omitted fields, unknown/context-mismatched proof kinds, failed or unrelated sensors, incomplete traces and invalid full scope fail explicitly. Command-selection helpers may return a nonmatch for unrelated commands, but every required action/census has a subsequent explicit completeness check. Ordinary disabled external audits skip only without explicit activation; final activated audits require inputs and passed without skips.
4. **FR coverage — PASS.** Every declared FR/NFR/C has concrete executed normal/scoped/gate evidence, as mapped below; all four stories are covered. The matrix itself is not treated as an execution witness.
5. **Frozen surface — PASS.** The measured commit adds only its three owned files. No dependency implementation, frozen mission/receipt, deletion census, original provider file or old ref is edited. Full-local and root preservation checks verify authentic historical objects and recovery material; the prior governance synchronization is explicitly separated.
6. **Locked decision — PASS.** Full-local 28-row admission is distinct from hosted 27-row and five-row smoke evidence. Public versus injected/persisted-partial modes remain explicit. The accepted public CLI tuple, original/current proof separation, no-authority boundary and unsupported guarded/atomic/compensation/backup guarantees are preserved. No old measurement is relabeled as this source.
7. **Shared-file ownership — PASS.** Three new paths match ownership and create intent exactly. Root's separately reviewed two-file governance synchronization is documented above. The approved dependency runner is invoked without edits; no cross-owner source change is hidden in this package.
8. **Production fragility — N/A for production changes.** No production request handler, worker or runtime code is changed. Audit failures stop acceptance explicitly. Artifact readers use bounded nonblocking regular-file reads and reject symlinks; proof/ref/trace checks cannot silently convert missing evidence into success.

## All 25 criteria and four stories

Each row is PASS for WP10's evidence obligation. Scenario IDs refer to the fresh final full-local receipt. Complementary source tests retain their actual execution/dependency provenance; source-symbol existence alone is not an execution claim.

| Criterion | Final behavioral evidence | Complementary witness |
|---|---|---|
| FR-001 | `binding.admitted`, `binding.refusals` | `TestInspectExistingBindingReadOnlyAndActorDrift`, `TestBindingRefusesSelfAdmittedExecutable` |
| FR-002 | `work.rich-roundtrip`, `performance.inventory-1000` | `TestCodecCompleteMeaningAndForeignPreservation`, `TestCodecPreservesDeepestSupportedOpaqueAndOperation` |
| FR-003 | `work.simple-update-guards`, `operation.historical-replay` | `TestCatalogEmptyCriteriaAndPrewriteRefusals`, `TestCatalogCannotRemoveOpeningCompleteMarker` |
| FR-004 | `work.rich-roundtrip`, `graph.partial-definition` | `TestDefinitionHistoricalSagaReplayAndGuardRefusal`, `TestDefinitionPartialSagaPreservesPriorVersusFreshEffects` |
| FR-005 | `graph.lifecycle`, `graph.invalid-suppliers`, `graph.partial-definition` | `TestGraphReadyUsesOpenSuppliersAndStaysDiagnostic`, `TestGraphPublicCanonicalEdgesClosedReadinessAndCycleRefusal` |
| FR-006 | `assignment.provenance`, `assignment.refusals` | `TestAssignmentOriginalRecoveryAndRefusals`, `TestAssignmentAncestryPreservesOnlyContinuousOrigin` |
| FR-007 | `discussion.attribution`, `discussion.partial-pair` | `TestDiscussionForeignSignerAndNativeClaims`, `TestDiscussionPairRejectsAttributionContradictions` |
| FR-008 | `operation.historical-replay`, `operation.key-attempt-refusal`, `operation.lookup-boundaries`, `graph.partial-definition`, `discussion.partial-pair`, `performance.original-recovery` | `TestActorLookupPreservesPinnedValidatorAndBinding`, `TestOperationLookupFailureNeverReconstructsOrAppends` |
| FR-009 | `bounds.snapshot-pages`, `bounds.large-heads`, `performance.known-reference` | `TestInventoryRefusesIncompleteOrContradictoryViews`, `TestKnownObservationRequiresCurrentSignerAndCompletePages` |
| FR-010 | `integration.compiled-dispatch`, `work.rich-roundtrip`, `graph.lifecycle`, `assignment.provenance`, `discussion.attribution` | `TestTrackerEarlyDispatchNeverTouchesHostFactory`, `TestTrackerProofBytesAndPrivateRecordFields` |
| FR-011 | `cutover.active-successor`, `binding.refusals` | `TestHubnotActiveCutover`, `TestHubnotDocumentationDeliveryClaims` |
| FR-012 | `replicas.selected-withheld`, `replicas.reconstruction` | `TestCapabilitiesDescribeOnlyImplementedGuarantees` |
| FR-013 | `binding.admitted`, `binding.refusals` | `TestInstallerQualifiedBuildAndDestinationPreservation`, `TestInstallerWrongToolchainAndTargetRefuseWithoutWrite` |
| FR-014 | All 28 fixed full-local rows | `TestHubnotManifestScopesAreClosedAndIndependent`, `TestHubnotEvidenceNeverAdmitsMissingExecution` |
| FR-015 | `cutover.local-recovery` | `TestPublicRecordedPreservationLeaves`, `TestHubnotReferenceContainsAuthenticBaselineCounts` |
| NFR-001 | `bounds.public-input-output`, `bounds.transport`, `bounds.snapshot-pages`, `bounds.large-heads` | `TestAdapterMutationPinAndLimits`, `TestAdapterLogicalCaptureStopsAtRemainingByte` |
| NFR-002 | `performance.inventory-1000`, `performance.known-reference`, `performance.original-recovery` | `TestHubnotIntegratedPerformanceEvidence`, `TestHubnotPerformanceTraceCensus` |
| NFR-003 | Final quality, race and source-bound gate receipts |  |
| NFR-004 | `binding.refusals`, `work.simple-update-guards`, `graph.invalid-suppliers`, `assignment.refusals`, `operation.lookup-boundaries`, `operation.key-attempt-refusal`, `bounds.public-input-output`, `graph.partial-definition`, `discussion.partial-pair` | `TestTrackerOuterMutationFactsAndSafeErrors`, `TestAdapterOutcomeAccounting` |
| NFR-005 | Final quality, race and source-bound gate receipts | `TestM2AbsenceMutants`, `TestM2GateSourcesDoNotShelterExecutableBehavior` |
| C-001 | `integration.authority-boundaries`, `integration.compiled-dispatch`, `discussion.attribution`, `assignment.refusals` | `TestQualificationProductionPlaybackMaterializedCheckpoint`, `TestPlaybackProductionSurfaceHasStructurallyNoIO` |
| C-002 | `work.simple-update-guards`, `assignment.provenance`, `assignment.refusals`, `graph.partial-definition` | `TestCapabilitiesDescribeOnlyImplementedGuarantees`, `TestDefinitionHistoricalSagaReplayAndGuardRefusal` |
| C-003 | `binding.admitted`, `binding.refusals`, `integration.authority-boundaries` | `TestDefaultReachabilityRegistryPassesCurrentRepository`, `TestPolicyHubnotTransportBoundary` |
| C-004 | `cutover.frozen-source`, `cutover.local-recovery` | `TestHubnotActiveCutover`, `TestM2LegacyExemptionsAreExactAndProvenanceBound` |
| C-005 | `integration.authority-boundaries`, `integration.compiled-dispatch`, `cutover.frozen-source` | `TestM2CapabilityReachabilityStopsAtFoundationCoordinator`, `TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed` |

Story 1 executes binding, rich/simple work, graph, assignment and discussion with full-field observations. Story 2 exercises completed-response discard, later human changes, original replay and actual public persisted partials, with separately injected duplicate/incomplete lookup. Story 3 covers two actual signers for one accountable author, selected replicas/fresh-path reconstruction, 201 heads/staged resolution, actual HostFactory and production-composed playback witnesses. Story 4 covers compiled installation, live successor dispatch, absence of retired executables and authentic preserved history.

FR-015 provenance is limited honestly: the contemporaneous recon note records qualified old-provider inspection on an isolated copied database and empty active export/list. Retained export and original-file before/after evidence exist; a separate structured original command/exit receipt and saved list stdout were not located. Neither is invented. Backup history was not restored/classified and is not declared empty. Current actual preservation and refusal to import/rebind are separately proved.

## Actual qualification, semantic and artifact evidence

The normal full-local command exited 0 in 955.498 seconds. All 28 expected rows started and terminated passing; `full_admissible=true`. Clean source/tree, pinned provider tuple, independently reproduced product/qualifier builds and observed user/network/PID isolation agree.

Independent full raw audit verifies 8,650 contiguous commands, 25,950 durable intent/start/terminal records, 44,090 artifact references and 46,450 unique files totaling 172,741,114 bytes, with zero errors. It checks actual bytes/hashes, run/source/scenario lineage, bounds, expected exits, ordered intervals and worker/final consistency. Manifest SHA-256: `5cdfaab6da33b5774a8ccbd7e8a8c7e91fb9c81c69410045404d7e50f0278aaf`.

Independent public-input/native-effect lineage comparison checks all 1,000 definitions, 2,000 ordered criteria, scopes/provenance, exact opaque number lexemes, original mapping/metadata, current native state, graph and discussion. Complete inventory takes 65.000369754 seconds, below 120 seconds. Setup is separately 536.755177366 seconds and comparison 0.128260157 seconds. The full inventory output contains 11,177,103 bytes; its separate trace observes 125 detailed pages and 627 Git executions.

Known-reference observation takes 66.454266425 seconds, with a separate 88.434733853-second trace showing 130 Hubnot and 647 Git successful executions and 777 distinct successful-exec child PIDs. Original recovery takes 0.445156617 seconds, with a separate 0.609564601-second trace showing 2 Hubnot, 7 Git and 9 such child PIDs. Raw traces were independently reparsed and matched to exact indexed argv/input/directory and full stable results. No fork-only census is claimed. Both query sensor pairs succeed on the affected repository, bracket the operations and preserve ref bytes; known/original proof kind and identity/token checks remain distinct.

Independent raw supplemental census checks 12 groups, 22 top-level tests and 30 subtests, each started/passed once with no skip/failure. It includes the real HostFactory tripwire, actual production reducer composition through RunV2, typed checkpoint/suffix equivalence and structural no-I/O evidence. The actual explicitly activated same-receipt audits pass: `TestHubnotIntegratedPublicAcceptance` 0.90 seconds and `TestHubnotIntegratedPerformanceEvidence` 2.19 seconds; process exit 0. These are evidence audits, not a second claimed provider run.

## Final gates and preservation

Actual complete `make -k quality` exits 0 in 568.894 seconds, including GapDB race 280.513 seconds. Nine exact-source clean receipts and seven raw censuses verify 120 top-level tests and 549 subtests with no required skips, failures, omissions or duplicate starts/results. Full log SHA-256: `e07d1f301dcb167ee6eef162fd9e01f0b20ae0355c02ebc782beb1efc227bcc9`.

Required helpercmd package race exits 0 in 2.107 seconds (package 1.658 seconds). Exact-source external all-package build, staticcheck, vet and module verify pass. Ordinary disabled external-audit skips in routine package mode do not substitute for the separately enabled passing acceptance audits.

Full-local preservation rows and root's separately executed final source report verify all 829 historical paths, 19 original provider files, five allocation branches, two other historical branches and two stashes against authentic baselines. Postmerge actual preservation remains required. Local evidence does not establish a hosted workflow execution or branch-protection update. M2 remains release-ready and unadopted; M3/M4 admission, private signer recovery and control-store migration remain outside this completion claim.

## Review evidence and verdict

External evidence root: `/home/lynn/Documents/Codex/2026-09-09/h/work/`.

- `go-wp10-qualification/e9b1ac65/`: immutable full-local receipt/captures, actual command records, activated-audit logs and terminal gates.
- `go-wp10-review/`: original/corrected bounded probes, exact source hashes, `final-full-local-evidence-review.md`, `final-gate-evidence-review.md` and independent artifact, semantic, raw trace, supplemental and quality verification scripts/results.
- `go-final-requirement-evidence-mapping-proposal.json`: criterion/source/test mapping, with explicit historical recon limits; it is not itself a runtime verdict.
- `go-kitty-preservation-WP10-e9b1ac65.json`: root's actual final preservation observation, separately attributed.
- `go-wp10-planning-hygiene.json`: root-owned pre-measurement governance synchronization.

PASS, no unresolved WP10 blocker. Record normal approval with the exact measured source reference and independent reviewer identity, without force. Mission acceptance, merge and independent postmerge review remain subsequent canonical gates. Runtime authentication/outbox/publication warnings and optional Python `no_coverage` are retained separately from the actual Go qualification and local verdict; they do not create a hosted-success claim.
