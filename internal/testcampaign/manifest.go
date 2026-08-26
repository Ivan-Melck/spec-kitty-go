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
	ExpectedTests []string
}

// Campaign declares how scheduling groups profiles without weakening their
// shared authority invariants.
type Campaign struct {
	Name              string
	Profiles          []string
	UniversalTest     string
	RequireUniversal  bool
	RequireStaleProbe bool
}

var profiles = map[string]Profile{
	"acceptance": {
		Name:    "acceptance",
		Package: "./internal/scenario",
		Pattern: "^(TestQualityCampaignUniversalInvariants|TestReconciliationRejectsPostCollapseCycle|TestReviewImmutableCandidateAndActorIndependence)$",
		ExpectedTests: []string{
			"TestQualityCampaignUniversalInvariants",
			"TestReconciliationRejectsPostCollapseCycle",
			"TestReviewImmutableCandidateAndActorIndependence",
		},
	},
	"scope": {
		Name:    "scope",
		Package: "./internal/scenario",
		Pattern: "^(TestPhase1ProductionDependencyAndArchitectureScope|TestPhase1SourceScopeMutantControl)$",
		ExpectedTests: []string{
			"TestPhase1ProductionDependencyAndArchitectureScope",
			"TestPhase1SourceScopeMutantControl",
		},
	},
	"fault": {
		Name:    "fault",
		Package: "./internal/scenario",
		Pattern: "^(TestWP01CrashAtClaimBoundaryLeavesNoAuthority|TestWorkspaceCrashReplayMatrixSR05ThroughSR09|TestHandoffFaultBoundaryReplayHR00ThroughHR07|TestIntegrationIG00ThroughIG09RecoversFromRealGitFacts|TestStartRunRecoveryCrashMatrix|TestCycle1NamedTrackerCrashBoundariesRecoverAcrossProcesses|TestZombieWriterCommitsAfterExpiryButCannotCrossControlPlane|TestZombieRevisionInvalidationUsesSameExpiryQuarantineTransition)$",
		ExpectedTests: []string{
			"TestCycle1NamedTrackerCrashBoundariesRecoverAcrossProcesses",
			"TestHandoffFaultBoundaryReplayHR00ThroughHR07",
			"TestIntegrationIG00ThroughIG09RecoversFromRealGitFacts",
			"TestStartRunRecoveryCrashMatrix",
			"TestWP01CrashAtClaimBoundaryLeavesNoAuthority",
			"TestWorkspaceCrashReplayMatrixSR05ThroughSR09",
			"TestZombieRevisionInvalidationUsesSameExpiryQuarantineTransition",
			"TestZombieWriterCommitsAfterExpiryButCannotCrossControlPlane",
		},
	},
	"soak": {
		Name:    "soak",
		Package: "./internal/scenario",
		Pattern: "^(TestPhase1TwoAgentTortureChamber|TestWP01ContentionCeiling|TestIntegrationSerializesTwoLanesFromTwoMissions|TestPhase1ExactCatalogScenarios|TestPhase1CatalogRegressionScenarios)$",
		ExpectedTests: []string{
			"TestIntegrationSerializesTwoLanesFromTwoMissions",
			"TestPhase1CatalogRegressionScenarios",
			"TestPhase1ExactCatalogScenarios",
			"TestPhase1TwoAgentTortureChamber",
			"TestWP01ContentionCeiling",
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
		Name: "quality", Profiles: []string{"acceptance", "scope"},
		UniversalTest: "TestQualityCampaignUniversalInvariants", RequireUniversal: true, RequireStaleProbe: true,
	},
	"full_campaign": {
		Name: "full_campaign", Profiles: []string{"full-scenario"},
		UniversalTest: "TestFullCampaignUniversalInvariants", RequireUniversal: true, RequireStaleProbe: true,
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
	for _, name := range campaign.Profiles {
		profile, err := ProfileByName(name)
		if err != nil {
			return err
		}
		foundUniversal = foundUniversal || slices.Contains(profile.ExpectedTests, campaign.UniversalTest)
	}
	if !foundUniversal {
		return fmt.Errorf("campaign %s omits universal test %s", campaign.Name, campaign.UniversalTest)
	}
	return nil
}

func validateProfile(profile Profile) error {
	if profile.Name == "" || profile.Package == "" || len(profile.ExpectedTests) == 0 {
		return fmt.Errorf("profile name, package, and expected tests are required")
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
