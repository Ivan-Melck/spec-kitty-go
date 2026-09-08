package testcampaign

import (
	"fmt"
	"slices"
	"strings"
)

// Profile is one curated go test invocation. ExpectedTests is the exact set
// of top-level tests that must start and reach one terminal event. Subtests are
// evidence owned by their top-level test and do not expand this stable census.
type Profile struct {
	Name          string
	Package       string
	Pattern       string
	Parallel      int
	ExpectedTests []string
}

// Campaign declares how scheduling groups profiles without weakening their
// shared authority invariants.
type Campaign struct {
	Name              string
	Profiles          []string
	RequiredProfiles  []string
	UniversalTest     string
	RequiredTests     []string
	RequireUniversal  bool
	RequireStaleProbe bool
}

var profiles = map[string]Profile{
	"architecture": {
		Name:    "architecture",
		Package: "./internal/architecture",
		ExpectedTests: []string{
			"TestBeadsDocumentationDeliveryClaims",
			"TestBeadsDocumentationLinksMermaidAndCommands",
			"TestCloseoutBinaryAndReviewPolicyDocumentation",
			"TestCloseoutOrchestratorAPI13ProjectionContract",
			"TestCurrentProductionGraphPassesClosedPolicy",
			"TestDefaultPolicyClassifiesCampaignToolsWithoutExposingThemToAuthority",
			"TestDefaultPolicyClassifiesGapDBAsExactProviderAdapter",
			"TestDefaultPolicyClassifiesM1CommandAndBoundsExternalGapDBDirection",
			"TestDefaultPolicyClassifiesProvingGroundCompositionExactly",
			"TestDefaultPolicyClassifiesPureControlJournalAsFoundation",
			"TestDefaultPolicyPinsTrackerConformanceAsOneWayQualityTooling",
			"TestDefaultPolicyRejectsAuthorityImportOfGitAdapter",
			"TestDefaultPolicySeparatesJournalApplicationComposition",
			"TestDefaultReachabilityRegistryPassesCurrentRepository",
			"TestDeferredRuntimeSurfaceDoesNotGrepMarkdownProse",
			"TestDeferredRuntimeSurfaceDoesNotGuessAmbiguousMarkdownFlows",
			"TestDeferredRuntimeSurfaceRejectsConstantAliasesAndDotImport",
			"TestDeferredRuntimeSurfaceRejectsLocalShortDeclarationAlias",
			"TestDeferredRuntimeSurfaceRejectsMarkdownAuthorityParsing",
			"TestDeferredRuntimeSurfaceRejectsPackageAliasDeclaredInAnotherFile",
			"TestDeferredRuntimeSurfaceRejectsProductionPythonButIgnoresTests",
			"TestLoadGraphDistinguishesProductionAndTestImports",
			"TestLoadGraphReturnsTypedAndBoundedFailures",
			"TestLoadedUnknownRootCannotDisappearThroughGraphExemption",
			"TestM1BudgetDecodeFailsClosed",
			"TestM1BudgetFrozenContract",
			"TestM1BudgetMutantControls",
			"TestM1BudgetRejectsEveryWidenedBound",
			"TestM1BudgetRuntimeComparisonFailsClosed",
			"TestM1CrownSourceBoundaries",
			"TestM1CrownSourceMutantControls",
			"TestM1DependencyBoundaries",
			"TestM1DependencyJournalApplicationIsImpureButSQLiteFree",
			"TestM1DependencyJournalCapabilityControls",
			"TestM1DependencyJournalCapabilityMutants",
			"TestM1DependencyJournalDecorativeDecoysCannotMaskExactSeam",
			"TestM1DependencyJournalPureSiblingControl",
			"TestM1DependencyJournalPurityMutants",
			"TestM1DependencyJournalResolvedTypeCleanControls",
			"TestM1DependencyJournalTypeCheckMutants",
			"TestM1DependencyMutantControls",
			"TestM1EvidenceContributorBindingMutants",
			"TestM1EvidenceExactCensusRejectsEveryRemoval",
			"TestM1EvidenceMutantControls",
			"TestM1EvidencePassPredicateRejectsVacuousPasses",
			"TestM1EvidenceRejectsEveryFalseUniversalInvariant",
			"TestM1EvidenceSchemaConditionalRejectsVacuousPasses",
			"TestM1EvidenceSchemaRetainsHonestFailureAndIncomplete",
			"TestM1ReducerProgramMutantControls",
			"TestM1ReducerProgramsAreClosedData",
			"TestM1SchemasAdoptionStateIsExplicit",
			"TestM1SchemasCompile",
			"TestM1SchemasFR016EventEnvelopeRefusals",
			"TestPackagePatternsAreSlashBoundedAndAmbiguityIsDeterministic",
			"TestPhase2EvidenceDocumentationMutants",
			"TestPhase2EvidenceIntegrity",
			"TestPolicyAcceptsProviderOnlyFromClassifiedAdapter",
			"TestPolicyRejectingFixturesNameExactRuleAndEdge",
			"TestPolicyValidationConfinesProviderAllowancesToAdapters",
			"TestReachabilityCallerPolicies",
			"TestReachabilityHonorsActiveCustomBuildTags",
			"TestReachabilityRejectsBuildExcludedCallers",
			"TestReachabilityRejectsGuardCalledOnlyByTest",
			"TestReachabilityRejectsMissingRegisteredTarget",
			"TestReachabilityRejectsShadowedTargetCalls",
			"TestReachabilityRejectsTargetSelfRecursion",
			"TestReachabilityRejectsUnrootedMutualRecursion",
			"TestRemovingEachRegisteredTargetsOnlyProductionCallerReddensFixture",
			"TestRemovingRealAuthorityToContractRuleNamesRealEdge",
			"TestTierRuleAndViolationValueValidation",
			"TestTypedProductionSourceLoadFailuresAreTyped",
			"TestTypedProductionSourceLoaderCancelsStalledGoList",
		},
	},
	"acceptance": {
		Name:    "acceptance",
		Package: "./internal/scenario",
		Pattern: "^(TestProductionCapabilityCompositionHasOnePublicationBoundary|TestTerminalSemanticEquivalenceControlledMutants|TestWorkCoordinateAgreementControlledMutants)$",
		ExpectedTests: []string{
			"TestProductionCapabilityCompositionHasOnePublicationBoundary",
			"TestTerminalSemanticEquivalenceControlledMutants",
			"TestWorkCoordinateAgreementControlledMutants",
		},
	},
	"scope": {
		Name:    "scope",
		Package: "./internal/scenario",
		Pattern: "^(TestHarnessArchitectureGuardKillsRetiredFileMutant|TestHarnessArchitectureGuardKillsStorageMutants|TestHarnessArchitectureIsPortable)$",
		ExpectedTests: []string{
			"TestHarnessArchitectureGuardKillsRetiredFileMutant",
			"TestHarnessArchitectureGuardKillsStorageMutants",
			"TestHarnessArchitectureIsPortable",
		},
	},
	"provider-portable": {
		Name:    "provider-portable",
		Package: "./internal/tracker/conformance",
		Pattern: "^(TestDurableFakePortableConformance|TestRegistryAndCapabilityMutantsFailClosed)$",
		ExpectedTests: []string{
			"TestDurableFakePortableConformance",
			"TestRegistryAndCapabilityMutantsFailClosed",
		},
	},
	"provider-adversarial": {
		Name:     "provider-adversarial",
		Package:  "./internal/tracker/beads",
		Pattern:  "^(TestAdapterRejectsProseNestedErrorsAndIgnoresWarningWording|TestAssignmentDiagnosticsDoNotExposeProviderPayload|TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces|TestCatalogConfirmedCreateRejectsDishonestLiveProviderState|TestCatalogReplayRefusesLiveProviderDrift|TestDiscussionAmbiguousAppendObservesWithoutBlindRetry|TestDiscussionDuplicateMarkerAndRevisionDriftRefuseMutation|TestGraphAmbiguousMutationUsesObservationWithoutSecondWrite|TestGraphValidationAndProviderRefusalsDoNotBlindlyMutate|TestIncompatiblePreflightAndReadinessRefuseBeforeLockAndMutatingSideEffect|TestInfiniteProducerIsBoundedKilledAndReaped|TestMetadataConflictsAndFutureSchemaAreNonMutating|TestMissionMethodAdapterSourceKeepsProviderAuthorityBounded|TestMissionMethodDefinitionObservationDetectsEqualTimestampSemanticMutation|TestMutationAmbiguityIsMonotonicAndRedacted|TestMutationRefusalPrecedesWriterLockAndSubprocess|TestNonzeroStderrJSONMutantNeverSelectsProviderState|TestProcessRunnerBoundsCancellationAndAmbiguity|TestReadTimeoutAfterStartPreservesStartedEvidence|TestReadinessDetectsExecutableReplacementAfterPreflight|TestReadyProjectionReportsProviderUnavailable|TestSafeCompensationRefusalSensorsKillReadCompareUpdateMutant|TestServerIdentitySpoofAndStaleReadinessRefuseBeforeMutation)$",
		Parallel: 8,
		ExpectedTests: []string{
			"TestAdapterRejectsProseNestedErrorsAndIgnoresWarningWording",
			"TestAssignmentDiagnosticsDoNotExposeProviderPayload",
			"TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces",
			"TestCatalogConfirmedCreateRejectsDishonestLiveProviderState",
			"TestCatalogReplayRefusesLiveProviderDrift",
			"TestDiscussionAmbiguousAppendObservesWithoutBlindRetry",
			"TestDiscussionDuplicateMarkerAndRevisionDriftRefuseMutation",
			"TestGraphAmbiguousMutationUsesObservationWithoutSecondWrite",
			"TestGraphValidationAndProviderRefusalsDoNotBlindlyMutate",
			"TestIncompatiblePreflightAndReadinessRefuseBeforeLockAndMutatingSideEffect",
			"TestInfiniteProducerIsBoundedKilledAndReaped",
			"TestMetadataConflictsAndFutureSchemaAreNonMutating",
			"TestMissionMethodAdapterSourceKeepsProviderAuthorityBounded",
			"TestMissionMethodDefinitionObservationDetectsEqualTimestampSemanticMutation",
			"TestMutationAmbiguityIsMonotonicAndRedacted",
			"TestMutationRefusalPrecedesWriterLockAndSubprocess",
			"TestNonzeroStderrJSONMutantNeverSelectsProviderState",
			"TestProcessRunnerBoundsCancellationAndAmbiguity",
			"TestReadTimeoutAfterStartPreservesStartedEvidence",
			"TestReadinessDetectsExecutableReplacementAfterPreflight",
			"TestReadyProjectionReportsProviderUnavailable",
			"TestSafeCompensationRefusalSensorsKillReadCompareUpdateMutant",
			"TestServerIdentitySpoofAndStaleReadinessRefuseBeforeMutation",
		},
	},
	"provider-real": {
		Name:    "provider-real",
		Package: "./internal/tracker/beads",
		Pattern: "^(TestRealBeadsBackupQualification|TestRealBeadsQualification|TestRealBeadsWorktreeQualification)$",
		ExpectedTests: []string{
			"TestRealBeadsBackupQualification",
			"TestRealBeadsQualification",
			"TestRealBeadsWorktreeQualification",
		},
	},
	"fault": {
		Name:    "fault",
		Package: "./internal/controlstore/gapdb",
		Pattern: "^(TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2|TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing|TestMissionAuthorityAdapterOneBatchRestartReplayV2|TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2|TestWorkspaceProductionZombieReplacementCampaignV2)$",
		ExpectedTests: []string{
			"TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2",
			"TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing",
			"TestMissionAuthorityAdapterOneBatchRestartReplayV2",
			"TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2",
			"TestWorkspaceProductionZombieReplacementCampaignV2",
		},
	},
	"soak": {
		Name:    "soak",
		Package: "./internal/controlstore/gapdb",
		Pattern: "^(TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2|TestExecutionAuthorityProductionContentionMatrixV2|TestHandoffGapDBPublisherCommitsOneCompleteBatchAndContendsV2|TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2|TestWorkspaceProductionZombieReplacementCampaignV2)$",
		ExpectedTests: []string{
			"TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2",
			"TestExecutionAuthorityProductionContentionMatrixV2",
			"TestHandoffGapDBPublisherCommitsOneCompleteBatchAndContendsV2",
			"TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2",
			"TestWorkspaceProductionZombieReplacementCampaignV2",
		},
	},
	"portable-scenario": {
		Name:    "portable-scenario",
		Package: "./internal/scenario",
		Pattern: "^(TestBehaviorParityCensus|TestBehaviorParityCensusMutants|TestCrashAmbiguousDishonestUniversalCensus|TestHarnessArchitectureGuardKillsRetiredFileMutant|TestHarnessArchitectureGuardKillsStorageMutants|TestHarnessArchitectureIsPortable|TestProductionCapabilityCompositionBoundaryBypassMutant|TestProductionCapabilityCompositionHasOnePublicationBoundary|TestTerminalSemanticEquivalenceControlledMutants|TestUniversalCampaignCensusMutants|TestWorkCoordinateAgreementControlledMutants)$",
		ExpectedTests: []string{
			"TestBehaviorParityCensus",
			"TestBehaviorParityCensusMutants",
			"TestCrashAmbiguousDishonestUniversalCensus",
			"TestHarnessArchitectureGuardKillsRetiredFileMutant",
			"TestHarnessArchitectureGuardKillsStorageMutants",
			"TestHarnessArchitectureIsPortable",
			"TestProductionCapabilityCompositionBoundaryBypassMutant",
			"TestProductionCapabilityCompositionHasOnePublicationBoundary",
			"TestTerminalSemanticEquivalenceControlledMutants",
			"TestUniversalCampaignCensusMutants",
			"TestWorkCoordinateAgreementControlledMutants",
		},
	},
	"full-scenario": {
		Name:    "full-scenario",
		Package: "./internal/scenario",
		ExpectedTests: []string{
			"TestCampaignProfileMutantControls",
			"TestCampaignProfilesPreserveUniversalInvariants",
			"TestCampaignWorkflowContracts",
			"TestCycle1CompensationReadRefusalReturnsCompleteDiagnostic",
			"TestCycle1NamedTrackerCrashBoundariesRecoverAcrossProcesses",
			"TestCycle1PostProviderApplyPreObservationRecoveryPreservesCompensationEvidence",
			"TestCycle1ProviderMutationRefusalsAlwaysReturnCompleteDiagnostic",
			"TestCycle1ProviderRefusalsAlwaysReturnCompleteDiagnostic",
			"TestCycle1TrackerMutationEnvelopeValidationIsComplete",
			"TestExpiredPublisherCannotCrossReplacementFenceAfterIG05Pause",
			"TestFullCampaignUniversalInvariants",
			"TestHandoffFaultBoundaryReplayHR00ThroughHR07",
			"TestHandoffMovedOwnedBranchPreservesExactGitFactsAndRefusesReplay",
			"TestIntegrationConflictPersistsRepairAndRefusesCleanup",
			"TestIntegrationDelayedGenerationTwoIntentCannotRollBackActiveGenerationThree",
			"TestIntegrationDifferentOperationSupersedesExpiredOrphanFence",
			"TestIntegrationHelperPreservesGitAndPathDiagnosticKindsAcrossOSBoundary",
			"TestIntegrationIG00ThroughIG09RecoversFromRealGitFacts",
			"TestIntegrationRecoversRepositoryFirstFenceAcquisitionWindows",
			"TestIntegrationSameOperationRecoversAfterProcessDeathBetweenGitFenceAndSQLite",
			"TestIntegrationSerializesTwoLanesFromTwoMissions",
			"TestMissionMethodFakeTwoItemDurableScenario",
			"TestMissionMethodHelperPureQueriesProjectionDamageAndStandaloneComprehension",
			"TestPhase1ApprovedBeforeExpiryRemainsIntegrable",
			"TestPhase1CatalogRegressionScenarios",
			"TestPhase1DurableWorkRevisionConsumerScenarios",
			"TestPhase1EvidenceCatalogMutationControls",
			"TestPhase1ExactCatalogScenarios",
			"TestPhase1FaultManifestIsComplete",
			"TestPhase1MergeConflictPreservesEvidence",
			"TestPhase1MovedCandidateCannotUsePriorApproval",
			"TestPhase1NamedFaultBoundaryMatrix",
			"TestPhase1NewHandoffSupersedesPendingReview",
			"TestPhase1OwnedBranchMovementRefusesHandoff",
			"TestPhase1ProductionDependencyAndArchitectureScope",
			"TestPhase1ReviewRejectionIsDurable",
			"TestPhase1SourceScopeMutantControl",
			"TestPhase1TwoAgentTortureChamber",
			"TestPhase1UniversalAntiVacuityControls",
			"TestPhase1WorkRevisionDispositionCatalog",
			"TestQualityCampaignUniversalInvariants",
			"TestReconciliationGenerationsRemainExplicitAndEnforceable",
			"TestReconciliationRejectsPostCollapseCycle",
			"TestRepositoryFenceMovesBeforeReplacementSQLiteAuthorityAndStopsValidatedPublisher",
			"TestReviewCompetingVerdictWritersAndDistinctRejection",
			"TestReviewImmutableCandidateAndActorIndependence",
			"TestReviewMalformedCriteriaJSONEmitsCompleteTypedContext",
			"TestSR09LostResponseReprobesAndQuarantinesTargetDrift",
			"TestSR09LostResponseReprobesAndQuarantinesWorkspaceDrift",
			"TestStartRunChangedReplayIdentityNeverRevealsPriorToken",
			"TestStartRunExactReplayRejectsDifferentPhysicalRepositoryBinding",
			"TestStartRunPathAttacksFailBeforeOpeningStoreOrMutatingPrimary",
			"TestStartRunRecoveryCrashMatrix",
			"TestStartRunRecoveryRefusesPartialDependencyHeadAndConflictNeverReady",
			"TestStartRunRejectsInventedLaneAndStaleBaseBeforeAuthorityOrWorkspace",
			"TestTrackerSagaAmbiguousCompensationReplaysWithoutDuplicateUndo",
			"TestTrackerSagaAttributionSafeCompensation",
			"TestTrackerSagaLostResponseReplayPersistsObservationWithoutAuthority",
			"TestTrackerSagaProviderUnavailableIsTypedAndDurable",
			"TestTrackerSagaTimeoutBeforeApplyObservesBeforeRetry",
			"TestWP01ClaimRace",
			"TestWP01ConcurrentFirstOpenConvergesAcrossProcesses",
			"TestWP01ContentionCeiling",
			"TestWP01CrashAtClaimBoundaryLeavesNoAuthority",
			"TestWP01HelperRejectsUnsafeIDBeforeFaultPath",
			"TestWP01LostResponseReplay",
			"TestWorkGraphHelperBinaryFinalizesReconcilesAndReturnsTypedEnvelopes",
			"TestWorkGraphHelperConcurrentFinalizeReplayReturnsOneRecordedResult",
			"TestWorkspaceConcurrency",
			"TestWorkspaceCrashReplayMatrixSR05ThroughSR09",
			"TestWorkspaceMaintenanceHelperReportsLiveRefusalsWithoutMutation",
			"TestZombieRevisionInvalidationUsesSameExpiryQuarantineTransition",
			"TestZombieWriterCommitsAfterExpiryButCannotCrossControlPlane",
		},
	},
}

var campaigns = map[string]Campaign{
	"quality": {
		Name: "quality", Profiles: []string{"acceptance", "architecture", "scope"},
		UniversalTest: "TestProductionCapabilityCompositionHasOnePublicationBoundary", RequireUniversal: true, RequireStaleProbe: true,
	},
	"full_campaign": {
		Name: "full_campaign", Profiles: []string{"fault", "portable-scenario", "soak"},
		RequiredProfiles: []string{"fault", "portable-scenario", "soak"},
		UniversalTest:    "TestProductionCapabilityCompositionHasOnePublicationBoundary",
		RequiredTests: []string{
			"TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing",
			"TestExecutionAuthorityProductionContentionMatrixV2",
			"TestProductionCapabilityCompositionHasOnePublicationBoundary",
			"TestWorkspaceProductionZombieReplacementCampaignV2",
		},
		RequireUniversal: true, RequireStaleProbe: true,
	},
	"provider_bounded": {
		Name: "provider_bounded", Profiles: []string{"provider-portable", "provider-adversarial"},
		RequiredProfiles: []string{"provider-adversarial", "provider-portable"},
		UniversalTest:    "TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces",
		RequiredTests: []string{
			"TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces",
			"TestReadinessDetectsExecutableReplacementAfterPreflight",
			"TestSafeCompensationRefusalSensorsKillReadCompareUpdateMutant",
		},
		RequireUniversal: true, RequireStaleProbe: true,
	},
	"provider_real": {
		Name: "provider_real", Profiles: []string{"provider-real"},
		RequiredProfiles: []string{"provider-real"},
		UniversalTest:    "TestRealBeadsQualification",
		RequiredTests:    []string{"TestRealBeadsBackupQualification", "TestRealBeadsQualification", "TestRealBeadsWorktreeQualification"},
		RequireUniversal: true, RequireStaleProbe: true,
	},
}

func ProfileByName(name string) (Profile, error) {
	profile, ok := profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("unknown campaign profile %q", name)
	}
	profile.ExpectedTests = append([]string(nil), profile.ExpectedTests...)
	return profile, validateProfile(profile)
}

func CampaignByName(name string) (Campaign, error) {
	campaign, ok := campaigns[name]
	if !ok {
		return Campaign{}, fmt.Errorf("unknown campaign %q", name)
	}
	campaign.Profiles = append([]string(nil), campaign.Profiles...)
	campaign.RequiredProfiles = append([]string(nil), campaign.RequiredProfiles...)
	campaign.RequiredTests = append([]string(nil), campaign.RequiredTests...)
	return campaign, ValidateCampaign(campaign)
}

func ValidateCampaign(campaign Campaign) error {
	if strings.TrimSpace(campaign.Name) == "" || len(campaign.Profiles) == 0 {
		return fmt.Errorf("campaign name and profiles are required")
	}
	if !campaign.RequireUniversal || !campaign.RequireStaleProbe || campaign.UniversalTest == "" {
		return fmt.Errorf("campaign %s must require universal invariants and stale probing", campaign.Name)
	}
	foundUniversal := false
	foundTests := make(map[string]bool)
	seenProfiles := make(map[string]bool, len(campaign.Profiles))
	for _, name := range campaign.Profiles {
		if seenProfiles[name] {
			return fmt.Errorf("campaign %s repeats profile %s", campaign.Name, name)
		}
		seenProfiles[name] = true
		profile, err := ProfileByName(name)
		if err != nil {
			return err
		}
		foundUniversal = foundUniversal || slices.Contains(profile.ExpectedTests, campaign.UniversalTest)
		for _, test := range profile.ExpectedTests {
			foundTests[test] = true
		}
	}
	if !foundUniversal {
		return fmt.Errorf("campaign %s omits universal test %s", campaign.Name, campaign.UniversalTest)
	}
	for _, required := range campaign.RequiredProfiles {
		if !seenProfiles[required] {
			return fmt.Errorf("campaign %s omits required profile %s", campaign.Name, required)
		}
	}
	if !sortedUniqueNonempty(campaign.RequiredProfiles) {
		return fmt.Errorf("campaign %s required profiles must be sorted, unique, and nonempty", campaign.Name)
	}
	if !sortedUniqueNonempty(campaign.RequiredTests) {
		return fmt.Errorf("campaign %s required tests must be sorted, unique, and nonempty", campaign.Name)
	}
	for _, required := range campaign.RequiredTests {
		if !foundTests[required] {
			return fmt.Errorf("campaign %s omits required test %s", campaign.Name, required)
		}
	}
	return nil
}

func sortedUniqueNonempty(values []string) bool {
	if len(values) == 0 {
		return true
	}
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" || (index > 0 && values[index-1] == value) {
			return false
		}
	}
	return true
}

func validateProfile(profile Profile) error {
	if profile.Name == "" || profile.Package == "" || len(profile.ExpectedTests) == 0 {
		return fmt.Errorf("profile name, package, and expected tests are required")
	}
	if profile.Parallel < 0 || profile.Parallel > 32 {
		return fmt.Errorf("profile %s parallelism must be between 0 and 32", profile.Name)
	}
	if !slices.IsSorted(profile.ExpectedTests) {
		return fmt.Errorf("profile %s expected tests must be sorted", profile.Name)
	}
	for index, name := range profile.ExpectedTests {
		if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
			return fmt.Errorf("profile %s has invalid top-level test %q", profile.Name, name)
		}
		if index > 0 && profile.ExpectedTests[index-1] == name {
			return fmt.Errorf("profile %s repeats test %s", profile.Name, name)
		}
	}
	return nil
}
