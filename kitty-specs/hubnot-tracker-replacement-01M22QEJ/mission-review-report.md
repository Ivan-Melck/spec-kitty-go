---
verdict: fail
mode: post-merge
review_scope: go-application
reviewed_at: 2026-09-09T22:02:11.809530+00:00
findings: 1
reviewed_source: 20dea2fa4d174ff9b2d6a76b26ffc9dfc748c443
reviewed_tree: 5ca8c835da81a4f5719c52f94eb3d51a13be2445
---

# Post-merge mission review: Hubnot Tracker Replacement

Reviewer: `codex-independent-merged-mission-review`, doctrine profile `reviewer-renata`.
Scope: Go application and its actual mission contracts. Final independent application verdict: **FAIL**. One blocking scanner/preserved-artifact integration defect prevents completion. This report records the actual failed merged-source gate; it does not rewrite earlier package approvals or claim publication.

## Source and integration identity

- Mission: `hubnot-tracker-replacement-01M22QEJ`, mission ID `01M22QEJPAAQJM91R01MCBR8QV`, assigned number 4.
- Original full implementation baseline: `94e7c8542d7e543558391ab6fffda7a28e6e3759`.
- Runtime bookkeeping `baseline_merge_commit`: `b3cb5f69d4db844af19d82120764dc4d557d52ea`; this is not substituted for the original implementation baseline.
- Qualified candidate: `e9b1ac659dcb40fe62ac6c991f26ed9fed9e544e`, tree `7df4e8f59a3942cec80dd818c9da9326f52a1151`.
- Merged source under review: `20dea2fa4d174ff9b2d6a76b26ffc9dfc748c443`, tree `5ca8c835da81a4f5719c52f94eb3d51a13be2445`.

The qualified candidate is an ancestor of the merged source. Independently inspected `git diff e9b1ac65..20dea2fa` has no changes outside `kitty-specs`, `.kittify` and `.spec-kitty`. The governance differences include the final canonical contracts and review history; those contracts were read rather than treated as irrelevant merely because they are not application code. Root's normal merge/resume integrated all ten packages and cleaned only managed mission lanes/refs. The final status journal contains all ten normal done transitions and independent final approval records.

Normal integration first stopped on a generated dossier snapshot. Root's accepted-target snapshot reconciliation did not change application source or verdicts. The metadata driver then retained a null mission number despite prior runtime assignment of 4 in `42aa13fb171d056646cb4272c9c7d2c545215d64`. Inspection confirms the supported canonical writer repair at `20dea2fa` changes only that meta field from null to 4; the other changes are a generated dossier and an explicit recovery note. It allocates no new identity. See canonical `reviews/merge-recovery.md` and independent `history-and-source-verification.json`.

## Gate Results

### Go contract and architectural gates

The final qualified candidate's actual complete `make -k quality` exited 0 in 568.894 seconds, including GapDB race 280.513 seconds. Nine exact-source clean receipts and seven raw censuses were independently verified: 120 top-level tests and 549 subtests, no required skip, failure, omission or duplicate result. This includes the portable/adversarial contracts, actual reachability/import/authority guards, exact absence census and historical evidence checks. Log SHA-256 `e07d1f301dcb167ee6eef162fd9e01f0b20ae0355c02ebc782beb1efc227bcc9`.

The new complete `make -k quality` command on exact merged `20dea2fa` terminated with **exit 2 in 596.899525 seconds**. `Makefile:257` / `m2-short` failed in `TestM2SQLiteAbsence`; later core and race passes, including GapDB race 286.080 seconds, cannot override that aggregate failure. The full log SHA-256 is `5d0f13e01c9e8d6c89ccf2ca910829938510aa039c78049134837ad8967c40f5`. The failed architecture short receipt raw SHA-256 is `5502e5b2c231a3a117e4571b88e744b83b6a45b7f30932232949921cfdcea094`. An owner-run focused diagnostic also exited 1 and identified the same scanner finding. See `go-postmerge-qualification/20dea2fa/records/make-quality.json`, `make-quality.log`, `m2-core-diagnostic.log` and independent `go-hubnot-postmerge-review/final-quality-failure-verification.json`. There is no post-merge complete-quality PASS.

### Compiled public provider qualification and acceptance

The actual normal full-local external qualification at e9 exited 0 in 955.498 seconds, with all 28 fixed rows passing and `full_admissible=true`. This is the measured source, not a new post-merge provider run. Since the merged application is unchanged and both product and qualifier binaries reproduce byte for byte, the real provider behavior remains applicable by explicit equivalence.

Independent auditing verified 8,650 contiguous commands, 25,950 durable intent/start/terminal records, 44,090 artifact references and 46,450 unique files totaling 172,741,114 bytes; zero artifact/lineage/census errors. Manifest SHA-256 `5cdfaab6da33b5774a8ccbd7e8a8c7e91fb9c81c69410045404d7e50f0278aaf`. Exact argv, stdin, workspace, source/run/scenario identity, observed/captured bytes, command intervals, expected exits and worker/final receipt consistency were checked.

The actual explicitly enabled same-receipt tests `TestHubnotIntegratedPublicAcceptance` and `TestHubnotIntegratedPerformanceEvidence` passed in 0.90 and 2.19 seconds, respectively (outer exit 0). They audit real captured behavior; they do not claim another provider execution. Twelve supplemental raw Go test groups have 22 top-level and 30 subtest passes with complete start/terminal census. Injected malformed protocol/history tests remain distinct from public operations and public persisted-partial scenarios.

### Builds, race and binary equivalence

Exact-source e9 module verification, vet, staticcheck and external all-package build passed. Required helpercmd package race exited 0 (1.658-second package); routine external-audit skips are not substituted for the separate explicitly activated audits above. Earlier package-specific real-provider races remain source-bound in their independent approval reports.

Exact merged-source product build, qualifier build and external all-package build have actual terminal exit 0. Independent hashes of the newly built files equal the qualified binaries:

- Product `spec-kitty`: `ecd5fc241b72ea30afe8572fcfab40df99deb312b0ed90c415e48e6fcef7e898` (8,642,722 bytes).
- Qualifier: `75061524ad8ba85ce1fc7c9aeab7c0f5a577906a9a4fbf437ba8b6bdfd81cbbf` (3,690,658 bytes).

Build records include the pinned Go 1.26.7 path, argv and sanitized environment; deterministic product/qualifier recipes use `-trimpath -buildvcs=false -mod=readonly -ldflags=-s -w`, Linux amd64/v1 and CGO disabled. See `go-postmerge-qualification/20dea2fa/application-equivalence.json` and `records/build-*.json`.

### Historical preservation

Actual post-merge observation at b8d58756 passed all 829 frozen tracked paths/modes, 19 original provider files, five allocation branches, two other historical branches and two stashes against authentic recorded baselines. The later 20dea commit changes none of these. Root's report is `go-kitty-preservation-postmerge-b8d58756.json`; this is actual file/ref observation, not admission based only on a receipt hash. Historical ref checkpoint provenance is explicitly during WP06, not a fabricated pre-mission timestamp.

### Python governor and issue-matrix applicability

The generic skill's Python SaaS/contract/architectural floor scenarios concern another repository and unclaimed FR-023/038–041/C-010. They are N/A here, not a fabricated PASS or environmental exception. The installed Python mission-review CLI's dead-code scanner cannot determine Go code and offers no Go runner: no dummy source or empty successful GateRecord is substituted. The independent skill-authored Go review is the applicable report path; actual Go contracts/architecture/public integration are above.

Canonical specification has no tracked GitHub issue references requiring an issue matrix. No issue-matrix file or fictional passing issue row is created. References to accepted upstream PRs are qualification dependencies, not unresolved issue-matrix rows.

## Requirement and story fidelity

All 25 canonical criterion IDs match the acceptance matrix, whose actual dated verdicts are PASS. Older prose marked Open/pending is historical planning text; it does not supersede the subsequent source-bound approval and acceptance events. Package ownership and code boundaries are explicit in `tasks.md`: WP01 transport, WP02 codec/reads, WP03 operation recovery/definitions, WP04 graph, WP05 assignment/discussion, WP06 compiled consumer, WP07 qualifier, WP08 scanner, WP09 cutover, WP10 independent integrated audits. Concrete test files and code symbols are linked in `go-final-requirement-evidence-mapping-proposal.json` and verified against merged source. Symbol presence alone is not execution proof; the actual final scenarios and independently verified raw gates close those chains.

The following table records actual qualified behavior and source-bound tests, not a blanket merged-source PASS. **NFR-003 fails the final merged-source gate**, and the unresolved scanner/preservation conflict affects completion of NFR-005 together with FR-015/C-004. FR-015 also has the explicit historical provenance limitation below.

| Criterion | Actual final e9 behavior | Complementary source-bound witness |
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
| NFR-003 | e9 final gates pass; **20dea complete quality FAIL (exit 2)** | Blocking finding POSTMERGE-1 |
| NFR-004 | `binding.refusals`, `work.simple-update-guards`, `graph.invalid-suppliers`, `assignment.refusals`, `operation.lookup-boundaries`, `operation.key-attempt-refusal`, `bounds.public-input-output`, `graph.partial-definition`, `discussion.partial-pair` | `TestTrackerOuterMutationFactsAndSafeErrors`, `TestAdapterOutcomeAccounting` |
| NFR-005 | Final quality, race and source-bound gate receipts | `TestM2AbsenceMutants`, `TestM2GateSourcesDoNotShelterExecutableBehavior` |
| C-001 | `integration.authority-boundaries`, `integration.compiled-dispatch`, `discussion.attribution`, `assignment.refusals` | `TestQualificationProductionPlaybackMaterializedCheckpoint`, `TestPlaybackProductionSurfaceHasStructurallyNoIO` |
| C-002 | `work.simple-update-guards`, `assignment.provenance`, `assignment.refusals`, `graph.partial-definition` | `TestCapabilitiesDescribeOnlyImplementedGuarantees`, `TestDefinitionHistoricalSagaReplayAndGuardRefusal` |
| C-003 | `binding.admitted`, `binding.refusals`, `integration.authority-boundaries` | `TestDefaultReachabilityRegistryPassesCurrentRepository`, `TestPolicyHubnotTransportBoundary` |
| C-004 | `cutover.frozen-source`, `cutover.local-recovery` | `TestHubnotActiveCutover`, `TestM2LegacyExemptionsAreExactAndProvenanceBound` |
| C-005 | `integration.authority-boundaries`, `integration.compiled-dispatch`, `cutover.frozen-source` | `TestM2CapabilityReachabilityStopsAtFoundationCoordinator`, `TestM2ResurrectedSQLiteAdapterAndLaterCapabilityBothFailClosed` |

Story 1 exercises complete/simple work and all useful graph/assignment/discussion ports through real compiled entrypoints. Story 2 proves original signed recovery after completed response discard and later edits, with genuine public persisted-partial recovery and separate injected lookup adversity. Story 3 verifies actual distinct signers/accountable author, selected replicas, reconstruction, 201 heads/staged resolution and authority boundaries. Story 4 exercises current compiled installation/cutover without retired tools and authentic preservation.

The final qualification contract clarifies the story's abstract panic-provider wording: actual production reducer composition executes RunV2 with checkpoint/suffix typed equivalence plus the structural no-I/O guard. Pure playback has no provider port to attach a trap to. The compiled tracker route separately uses its real HostFactory tripwire. No unused panic object or invented provider capability is counted as evidence.

## Performance and full-field evidence

The independently verified final fixture returns all 1,000 complete definitions and 2,000 ordered criteria with full context, scopes, provenance, exact opaque number lexemes, immutable opening mappings, current state, 20 blocking plus 20 unrelated edges and 20 attributed comments. No float conversion, dropped private-field DTO, summarized title list or per-issue history inventory replaces complete definitions.

Untraced inventory takes 65.000369754 seconds (limit 120), full output 11,177,103 bytes; setup is separately 536.755177366 seconds and comparison 0.128260157 seconds. A separate trace takes 90.801767713 seconds and observes 125 detail pages and 627 Git successful executions. Known-reference observation takes 66.454266425 seconds, separate trace 88.434733853 seconds with 130 Hubnot/647 Git successful executions and 777 distinct successful-exec child PIDs. Recovery takes 0.445156617 seconds, separate trace 0.609564601 seconds with 2 Hubnot/7 Git and 9 such child PIDs. These are qualified-host observations, not platform-wide latency promises or fork-only censuses.

Full stable traced/untraced results, proof kinds/identity/tokens, raw unfinished/resumed trace lines and exact indexed command/input lineage were independently checked. Successful equal before/after sensors run on the affected repository and bracket the action. Original recovery reports replay and prior mutation, not a fresh effect. Known-reference cost remains visible; it is not implied to be as cheap as original-operation lookup.

## Independent review history

| WP | Final approval event | Independent reviewer | Forced approval | Final lane |
|---|---|---|---|---|
| WP01 | `01M22XWRRV9B4WVZRPZYAXF1V2` | `codex-independent-transport-review` | false | done |
| WP02 | `01M230HEBJ38SP83NCAH7G9SGA` | `codex-independent-readmodel-review` | false | done |
| WP03 | `01M233QQNCQC2DNPMM2AZWG90B` | `codex-independent-operation-review` | false | done |
| WP04 | `01M237NYTPYGGN0EEA1QRQSKER` | `codex-independent-graph-review` | false | done |
| WP05 | `01M23AF33ERDBCZKY0BPYCEWNJ` | `codex-independent-effects-review` | false | done |
| WP06 | `01M23DEDHBCRZK8BPCRR9Y8MQ9` | `codex-independent-consumer-review` | false | done |
| WP07 | `01M23V4VVMH134Y50QWHMJYVKZ` | `codex-independent-qualification-review` | false | done |
| WP08 | `01M22XBACKKWRVR60EMVKX5BCP` | `codex-independent-scanner-review` | false | done |
| WP09 | `01M23XHWPZFVKB4X3J6ED3V5A0` | `codex-independent-cutover-review` | false | done |
| WP10 | `01M24192AH0RJY1RATGAN46WGJ` | `codex-independent-integrated-acceptance-review` | false | done |

All ten referenced canonical review-cycle files exist and were bound by SHA-256 in `history-and-source-verification.json`. Exactly three journal events have force=true: WP08 in_review→planned rejection, WP05 approved→planned rejection and WP07 approved→planned rejection. These are supported backward-rewind semantics; none is a forced approval or self-review exception. There is no arbiter override replacing independent review.

WP08 closed the executable text/no-shebang scanner bypass, including executable mode independent of suffix/UTF-8 and conservative binary ambiguity. WP05 was reopened after approval for quadratic common-proof copying; shared immutable payloads now keep construction/retention/accessors linear while defensive mutation and mismatch guards remain. WP07 was reopened for missing separately measured known-reference/recovery process counts; real traces now bind successful execution classes and distinct child PIDs, and null counts refuse. Earlier individual reviews also corrected strict null fields, observed versus retained cumulative bytes, deep JSON framing, saga aggregate provenance/replay, absent ready proof, checked writes, real stdin/stdout cancellation, supervisor descendant cleanup, durable active-command evidence and fixed-policy system-tool resolution. Their red evidence is preserved rather than erased by later greens.

## Drift, error paths and security

One blocking scanner/artifact integration defect was found, as documented below. No separate provider runtime or authority regression was identified. The replacement is live through `cmd/spec-kitty` → helper tracker dispatch → neutral ports → the qualified public CLI adapter; active Make/campaign/CI source points to the successor. The retired shell bootstrap is deliberately absent, as amended in the canonical plan; its historical owned path is not a missing deliverable. Historical Beads validators were preserved and supplied explicit immutable grammar inputs, with current publication and successor delivery tested separately. The live grant list removes the five retired provider edges while the frozen sixteen-row historical identity remains intact. No `go.mod` or `go.sum` dependency expansion occurred.

The critical failure boundaries remain explicit: malformed binding/schema/Unicode/duplicates or unsupported guarantees refuse before writes; incomplete views never become empty success; readiness with no proof is explicitly unavailable; historical replay uses verified originals before current-state checks. New effects that survive response loss or a later conflict retain applied/ambiguous facts and are never deleted or described as unstarted. Public graph/assignment guarantees remain honestly unguarded, not global compare-and-swap.

Signer pinning is enforced at the actual child mutation; accountable authors are signed descriptive claims distinct from transport keys and host permissions. Exact native comment bodies are paired with recoverable signed attribution rather than rewritten or keyed by mutable author content. Binding uses public identity JSON, not private key-file inspection. Dynamic provider input is argv/strict bounded JSON, not shell code. The compiled installer fixes parent-side system-tool resolution as well as child environment; artifacts are exclusive, bounded, regular and non-symlink, and actual supervisor namespace cleanup preserves cancellation safety and durable partial evidence. Consumer pipe cancellation restores shared descriptor flags and leaves caller-owned descriptors usable.

No silent empty-success candidate remains open in reviewed provider/read/consumer paths. Diagnostic-only no-proof output and portable-relations-only dependencies have explicit scope; they do not fabricate stronger observations. Control-host startup and provider-free playback remain separate. No tracker comment/assignment/metadata grants lease, review, acceptance or integration authority.

## Notes and current limitations

1. FR-015: contemporaneous qualified old-provider recon on a copied database reports empty active export/list; retained export and before/after original-file evidence exist. A separate structured original command/exit receipt and saved list stdout were not located. Neither is invented. Backups were not restored/classified or declared empty; no importer or automatic operation rebinding ran.
2. Full local 28-row evidence is distinct from fixed hosted 27-row selection and five-row identity smoke. Hosted cannot prove the original local nineteen-file preservation. No Go PR/hosted run/branch-protection completion is claimed by this local review. Root's current remote observation reports prerequisite Hubnot PR3 still open at `1cfbd300`; the accepted pinned baf350d1 source remains the consumer's verified dependency, not a claim of upstream main integration.
3. M2 remains release_ready=true and adopted=false. This replacement does not admit/complete M3/M4, restore all API surfaces, migrate GapDB/control data, or activate the real project's provider binding. Private signer recovery, complete backup, guarded definition updates, atomic claims and safe conditional compensation remain unsupported as documented.
4. The generated retrospective presently misclassifies WP05/WP07 as completed without rejection cycles, although their durable changes_requested rewind events exist. Its original null number predates the canonical number repair. Preserve the original and correct/explain these generator labels through the supported retrospective update or explicit accompanying review note. This reporting defect does not invalidate independently verified application behavior.
5. Optional Python no_coverage and runtime authentication/outbox warnings remain real limitations of those tools; local durable verdicts and actual Go gates do not establish remote publication.

## Blocking finding POSTMERGE-1 — preserved executable-mode JSON fails the scanner

**Severity:** blocking integration defect. **Requirements:** NFR-003 final gates, NFR-005 conservative scanner, FR-015/C-004 preservation. **Location:** `internal/architecture/m2_absence_test.go:288`, reached from `TestM2SQLiteAbsence` on the primary repository.

The original ignored file `.beads/embeddeddolt/r6/.dolt/repo_state.json` is a regular 316-byte JSON file with mode `0777` and SHA-256 `d0f6078373e0c84e8d598ba2f6f56d405a13b4aab0e1ca8dad86e1fe19b7fd7e`. Independent inspection confirms its content matches the original nineteen-file baseline exactly. Its bytes contain native head, remote and backup-location metadata. The scanner intentionally treats any executable-mode non-ELF text as potential shell fallback, regardless of suffix; this JSON triggers `ambiguous-shell: unsupported-group-boundary`.

The qualified isolated lanes did not contain these ignored original files. Therefore identical tracked application source and reproducible binary bytes did not imply identical filesystem inputs to the root-wide scanner. The actual preserved artifact makes the required gate fail. This is neither a random test failure nor a provider behavior change, and later passing campaigns cannot waive it.

The correction must preserve the original bytes and mode, retain general executable analysis and forbidden-token scans, and prove the actual original-root input can coexist with a passing gate. Deleting, moving or changing permissions on the recovery file, ignoring `.beads`, exempting all JSON, or weakening the frozen scanner census would not establish that result. A separately governed bounded correction is being assessed under Op `01M242XANGF9SGW0ZKVH25JYJJ`; this report does not approve an unimplemented remedy or reopen completed WPs.

## Final verdict

**FAIL**, reviewed source `20dea2fa4d174ff9b2d6a76b26ffc9dfc748c443`.

POSTMERGE-1 is the sole identified blocker. Earlier e9 full-local qualification, requirement evidence, independent approvals, source/binary parity and historical preservation remain valid at their stated scopes and source identities. They do not satisfy the failed merged-source quality gate. Fix the scanner/artifact integration through the governed correction, independently review the bounded change, and obtain truthful required validation on the corrected source before superseding this verdict. The original failed record and this report must remain in history. No source fix or approval-event override is part of this review commit.

## Retrospective Reminder

The actual canonical record exists at `kitty-specs/hubnot-tracker-replacement-01M22QEJ/retrospective.yaml`, generated at normal merge. After canonical review, verify/update it using normal `spec-kitty retrospect create --mission hubnot-tracker-replacement-01M22QEJ --update --json`; preserve provenance and resolve the misleading labels noted above. Then surface `retrospect summary` and dry-run `agent retrospect synthesize` (without --apply). A generic “report present with findings” label or empty legacy proposal batch is not evidence of an application defect or that every recommendation was applied.

External evidence root: `/home/lynn/Documents/Codex/2026-09-09/h/work/`. Primary retained sources are `go-wp10-review`, `go-wp10-qualification/e9b1ac65`, `go-postmerge-qualification/20dea2fa`, `go-hubnot-postmerge-review`, the actual post-merge preservation report and canonical mission reviews. This report retains exact measured-source attribution and actual terminal gate records; later corrections must not relabel them.
