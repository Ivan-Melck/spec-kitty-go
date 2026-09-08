package m2

import "fmt"

type Definition struct {
	ID                  string
	Category            string
	OwnerWP             string
	Package             string
	Selector            string
	ExpectedRepetitions int
	TrialsPerRun        int
}

var authorityFamilies = []Definition{
	{"family/acceptance", "authority_family", "WP10", "./internal/integration", "TestAcceptancePublicationCannotOmitWorkgraphGuardV2", 100, 1},
	{"family/artifact_catalog", "authority_family", "WP06", "./internal/artifact", "TestArtifactServiceCompleteFreshnessProgression", 100, 1},
	{"family/artifact_revision", "authority_family", "WP06", "./internal/artifact", "TestArtifactV2PublicationAndDecisionAreExactAtomicAuthority", 100, 1},
	{"family/execution_slot", "authority_family", "WP07", "./internal/execution", "TestWorkspaceBindingAndExpiryComposeAtomicallyWithExecutionV2", 100, 1},
	{"family/external_effect", "authority_family", "WP07", "./internal/execution", "TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2", 100, 1},
	{"family/handoff_slot", "authority_family", "WP09", "./internal/handoff", "TestSubmitBindsVerifiedCandidateAndDurableAuthority", 100, 1},
	{"family/integration_attempt", "authority_family", "WP10", "./internal/integration", "TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2", 100, 1},
	{"family/integration_target", "authority_family", "WP10", "./internal/integration", "TestIntegrationStageRetryKeepsOperationIDV2", 100, 1},
	{"family/mission", "authority_family", "WP05", "./internal/mission", "TestFinalizationSagaObservesTwiceThenPublishesGenerationOne", 100, 1},
	{"family/operation", "authority_family", "WP02", "./internal/controljournal/application", "TestServiceOneBatchStableIdentityAndNoSilentRetry", 100, 1},
	{"family/outbox", "authority_family", "WP11", "./internal/controlstore/gapdb", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing", 100, 1},
	{"family/review_slot", "authority_family", "WP09", "./internal/review", "TestReviewAuthorityStagesEveryCriterionThenOneTerminalVerdictAndReplaysV2", 100, 1},
	{"family/runtime_host", "authority_family", "WP13", "./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", 100, 1},
	{"family/workgraph", "authority_family", "WP05", "./internal/workgraph", "TestFinalizationObservationRecheckDetectsEveryBoundaryChange", 100, 1},
	{"family/workspace", "authority_family", "WP08", "./internal/workspace", "TestWritableZombieWorkspaceNeverRegainsAuthorityCampaignV2", 100, 1},
}

func AuthorityFamilyIDs() []string {
	result := make([]string, len(authorityFamilies))
	for index, definition := range authorityFamilies {
		result[index] = definition.ID
	}
	return result
}

var crownDefinitions = []Definition{
	{"behavior/autonomous-capability-readiness", "behavior", "WP15", "./internal/helpercmd", "TestDriveExecutesIndependentWorkScopesInOneMissionConcurrently", 1, 1},
	{"behavior/two-item-restart-every-boundary", "behavior", "WP14", "./internal/scenario", "TestTwoItemMissionRestartAfterEachDurableBoundary", 1, 1},
	{"behavior/real-git-gapdb-two-item-crown", "behavior", "WP17", "./internal/scenario", "TestTwoItemRealGitGapDBCrown", 1, 1},
	{"behavior/zombie-workspace-isolation", "behavior", "WP08", "./internal/scenario", "TestZombieOwnerReplacementCampaign100", 100, 100},
	{"behavior/reviewer-independence", "behavior", "WP09", "./internal/review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2", 100, 1},
	{"behavior/serialized-integration", "behavior", "WP10", "./internal/integration", "TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2", 100, 100},
	{"behavior/restore-deadline-structural", "behavior", "WP01", "./internal/architecture", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly", 1, 1},
	{"behavior/restore-deadline-runtime", "behavior", "WP04", "./internal/controljournal/v2/restore", "TestRestoreDeadlineInputSensitiveAndBeforeFirstIO", 1, 1},
	{"behavior/sqlite-absence", "behavior", "WP16", "./internal/architecture", "TestM2SQLiteAbsence", 1, 1},
	{"fault/cut-before-publication", "fault_cut", "WP04", "./internal/controlstore/gapdb", "TestV2CommitCanceledBeforeDeliveryMakesNoProviderCall", 100, 1},
	{"fault/cut-durable-before-response", "fault_cut", "WP04", "./internal/controlstore/gapdb", "TestV2AssertionCandidateSidecarQualification", 100, 1},
	{"fault/cut-lost-response", "fault_cut", "WP04", "./internal/controlstore/gapdb", "TestV2AmbiguousCommitCanBeFollowedByPublicReconciliation", 100, 1},
	{"fault/cut-malformed-ack", "fault_cut", "WP04", "./internal/controlstore/gapdb", "TestV2CommitRefusesWireBudgetBeforeProviderAndValidatesAck", 100, 1},
	{"fault/cut-restart-reconciliation", "fault_cut", "WP04", "./internal/controlstore/gapdb", "TestProductionPublicationRecoveryAcrossOwnerRestartCuts", 100, 1},
	{"fault/external-before-call", "fault_cut", "WP07", "./internal/execution", "TestM2ExternalEffectCutBeforeProviderCall", 100, 1},
	{"fault/external-after-success-before-observation", "fault_cut", "WP07", "./internal/execution", "TestM2ExternalEffectCutAfterCallBeforeDurableObservation", 100, 1},
	{"fault/external-after-observation-before-result", "fault_cut", "WP07", "./internal/execution", "TestM2ExternalEffectCutAfterObservationBeforeResultPublication", 100, 1},
	{"fault/acknowledgement-ambiguity", "fault_cut", "WP04", "./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles", 100, 1},
	{"fault/concurrent-claims", "fault_cut", "WP07", "./internal/scenario", "TestConcurrentSameAggregateConflictCampaign100", 100, 100},
	{"fault/clean-unclean-restart", "fault_cut", "WP04", "./internal/controljournal/v2/restore", "TestRestoreParallelWorkerDeterminism100Runs", 100, 100},
	// The wrapper executes and verifies WP14's own closed structured 100-trial
	// family/cut census. Its single process is not itself a synthetic 100 trials.
	{"fault/adversarial-tracker", "fault_cut", "WP14", "./internal/scenario", "TestCrashAmbiguousDishonestUniversalCampaign100", 1, 1},
	{"fault/family-bound-publication-cuts", "fault_cut", "WP17", "./internal/scenario", "TestM2FamilyBoundPublicationCutCampaign", 6500, 6500},
	{"fault/outbox-bound-publication-cuts", "fault_cut", "WP17", "./internal/controlstore/gapdb", "TestM2OutboxFamilyBoundPublicationCutCampaign", 500, 500},
	{"mutant/restore-deadline-overlay", "mutant", "WP04", "./internal/controljournal/v2/restore", "TestRestoreDeadlineSourceOverlayMutants", 7, 7},
	{"mutant/autonomy-ceremony", "mutant", "WP15", "./internal/architecture", "TestM2AutonomyMutationCampaignCoversCeremonyFailureClasses", 1, 1},
	{"mutant/sqlite-absence-layers", "mutant", "WP16", "./internal/architecture", "TestM2AbsenceLayerDetectors", 8, 8},
	{"mutant/production-runtime", "mutant", "WP13", "./internal/productionruntime", "TestWP13MandatoryProductionMutantCampaign", 1, 1},
}

func FrozenDefinitions() []Definition {
	result := append([]Definition(nil), authorityFamilies...)
	result = append(result, crownDefinitions...)
	return result
}

func ValidateDefinitions(definitions []Definition) error {
	want := FrozenDefinitions()
	if len(definitions) != len(want) {
		return fmt.Errorf("definition census=%d want=%d", len(definitions), len(want))
	}
	seen := make(map[string]struct{}, len(definitions))
	for index, definition := range definitions {
		if definition != want[index] {
			return fmt.Errorf("definition %d is reordered or substituted: got %q want %q", index, definition.ID, want[index].ID)
		}
		if definition.ID == "" || definition.OwnerWP == "" || definition.Package == "" || definition.Selector == "" || definition.ExpectedRepetitions <= 0 || definition.TrialsPerRun <= 0 || definition.ExpectedRepetitions%definition.TrialsPerRun != 0 {
			return fmt.Errorf("definition %q is incomplete", definition.ID)
		}
		if _, duplicate := seen[definition.ID]; duplicate {
			return fmt.Errorf("definition %q is duplicated", definition.ID)
		}
		seen[definition.ID] = struct{}{}
	}
	return validateIterationContractCensus(definitions, iterationContracts)
}

func validateIterationContractCensus(definitions []Definition, contracts map[string]iterationContract) error {
	expected := map[string]struct{}{
		"behavior/zombie-workspace-isolation": {},
		"behavior/serialized-integration":     {},
		"fault/concurrent-claims":             {},
		"fault/clean-unclean-restart":         {},
	}
	if len(contracts) != len(expected) {
		return fmt.Errorf("iteration receipt contract census=%d want=%d", len(contracts), len(expected))
	}
	definitionsByID := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		definitionsByID[definition.ID] = definition
	}
	for id := range expected {
		definition, ok := definitionsByID[id]
		contract, contracted := contracts[id]
		if !ok || !contracted || definition.ExpectedRepetitions != 100 || definition.TrialsPerRun != 100 || contract.boundary == "" || len(contract.outcomes) == 0 {
			return fmt.Errorf("iteration receipt contract %q is missing or incomplete", id)
		}
	}
	for id := range contracts {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("foreign iteration receipt contract %q", id)
		}
	}
	return nil
}

func Summarize(entries []CensusEntry, sqliteEdges int) (CensusSummary, error) {
	if len(entries) != len(FrozenDefinitions()) {
		return CensusSummary{}, fmt.Errorf("observed census=%d want=%d", len(entries), len(FrozenDefinitions()))
	}
	definitions := FrozenDefinitions()
	var summary CensusSummary
	for index, entry := range entries {
		definition := definitions[index]
		if entry.ID != definition.ID || entry.Category != definition.Category || entry.OwnerWP != definition.OwnerWP || entry.Package != definition.Package || entry.Selector != definition.Selector || entry.ExpectedRepetitions != definition.ExpectedRepetitions || entry.TrialsPerRun != definition.TrialsPerRun {
			return CensusSummary{}, fmt.Errorf("entry %d/%s is foreign or substituted", index, definition.ID)
		}
		if entry.Substituted || entry.ObservedCount != entry.ExpectedRepetitions || entry.PassedCount != entry.ExpectedRepetitions || entry.FailedCount != 0 || entry.SkippedCount != 0 || entry.ArtifactPath == "" || !validDigest(entry.ArtifactSHA256) {
			return CensusSummary{}, fmt.Errorf("entry %s is incomplete, non-pass, or substituted", entry.ID)
		}
		switch entry.Category {
		case "authority_family":
			summary.AuthorityFamiliesTotal++
			summary.AuthorityFamiliesPassed++
		case "behavior":
			summary.BehaviorsTotal++
			summary.BehaviorsPassed++
		case "fault_cut":
			summary.FaultCutsTotal++
			summary.FaultCutsPassed++
		case "mutant":
			summary.MutantsTotal += entry.ExpectedRepetitions
			summary.MutantsKilled += entry.PassedCount
		default:
			return CensusSummary{}, fmt.Errorf("entry %s has unknown category %q", entry.ID, entry.Category)
		}
	}
	summary.SQLiteEdges = sqliteEdges
	if sqliteEdges != 0 {
		return summary, fmt.Errorf("SQLite edge census=%d want=0", sqliteEdges)
	}
	return summary, nil
}
