package m2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const wp14CampaignSourceSHA256 = "a8917de26648c54aca5bc1b730594a7482576254a7fde25c8ae8f7b844d7c470"

type WP14CampaignReceipt struct {
	SchemaVersion    int      `json:"schema_version"`
	SourceSHA256     string   `json:"source_sha256"`
	RawSHA256        string   `json:"raw_sha256"`
	ExpectedChildren []string `json:"expected_children"`
	RunChildren      []string `json:"run_children"`
	PassedChildren   []string `json:"passed_children"`
	Families         int      `json:"families"`
	FamilyClaims     int      `json:"family_claims"`
	PublicationCuts  int      `json:"publication_cuts"`
	MutantSuites     int      `json:"mutant_suites"`
	Concurrency      int      `json:"concurrency"`
	Universal        int      `json:"universal"`
	Verdict          string   `json:"verdict"`
}

var wp14FamilyClaims = map[string][]string{
	"acceptance":          {"TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2", "TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2", "TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2"},
	"artifact_catalog":    {"TestArtifactV2RefusesStaleCatalogAndFailedValidationApproval", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing", "TestPublishReplayConflictAndAmbiguousCommitRecovery"},
	"artifact_revision":   {"TestArtifactV2RequiresExactObservedContentBytes", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing", "TestPublishReplayConflictAndAmbiguousCommitRecovery"},
	"execution_slot":      {"TestExecutionAuthorityMintsGrantFenceAndEligibilityOnlyForGrantsV2", "TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2", "TestExecutionAuthorityRestartCutsHeartbeatExpiryAndReplacementV2"},
	"external_effect":     {"TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2", "TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2", "TestExternalEffectRetryProgressionReplaysEveryPublicationV2"},
	"handoff_slot":        {"TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2", "TestHandoffGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2", "TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2"},
	"integration_attempt": {"TestIntegrationCannotDowngradeZeroEligibilityBeforeGitV2", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", "TestIntegrationConflictAndManualMovementRequireRepairV2"},
	"integration_target":  {"TestIntegrationRefusesBeforeAnyRepositoryObservationV2", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", "TestIntegrationRepairRequiredRestoresThroughCheckpointSuffixWithoutGitV2"},
	"mission":             {"TestReadinessRelatedFactRefusesCompetingFactRevisionBeforePublication", "TestReadinessServiceExactReplayAndCollisionPrecedeEpochAndAuthorityReads", "TestReadinessAuthorityRealGapDBCheckpointSuffixMatchesOriginAndProjectionLossV2"},
	"operation":           {"TestServiceOneBatchStableIdentityAndNoSilentRetry", "TestServiceAmbiguityVisibleThenSameIDReadReconciles", "TestServiceMalformedAcknowledgementIsIndeterminate"},
	"outbox":              {"TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2", "TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2", "TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2"},
	"review_slot":         {"TestReviewRejectsPartialCriterionSetChangedVerdictAndReducerRewriteV2", "TestReviewGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2", "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2"},
	"runtime_host":        {"TestFreshEpochRequiresSealedRestoreAndRotatesPredecessorDurably", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", "TestHostProjectionRefreshRetriesConcurrentStablePrefixMovement"},
	"workgraph":           {"TestWorkgraphV2RefusesIncompleteEligibilityAndWrongLineage", "TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB", "TestWorkgraphV2ExactReplayPrecedesEpochAndChangedBindingConflicts"},
	"workspace":           {"TestPreparationRefusesRepositoryObservationDriftV2", "TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2", "TestPreparationRestartFromEveryDurableStageIsCanonicalV2"},
}

var wp14PublicationCuts = []string{"after_durable_commit_before_response", "before_publication", "lost_response", "malformed_acknowledgement", "restart_after_reconciliation", "restart_before_reconciliation"}
var wp14MutantSuites = []string{"effect_authority_mutants", "execution_authority_mutants", "integration_source_overlay", "journal_acknowledgement_mutants", "production_composition_bypass", "production_host_source_overlay", "readiness_source_overlay", "terminal_semantic_oracle", "universal_progress_oracle", "work_coordinate_oracle", "workspace_semantic_mutants"}
var wp14Concurrency = []string{"TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2", "TestExecutionAuthorityProductionContentionMatrixV2", "TestHostIndependentCommandsRunConcurrentlyWithoutGlobalCommandMutex", "TestIndependentTargetsProgressConcurrentlyWithoutGlobalLockV2", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", "TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2", "TestWorkspacePathClaimAndIndependentWorkspaceCampaignV2"}
var wp14Universal = []string{"TestDishonestFakeTrackerMatrixPreservesAuthorityV2", "TestHarnessProductionLifecycleAndTypedQueries", "TestProductionCapabilityCompositionHasOnePublicationBoundary", "TestTwoItemMissionRestartAfterEachDurableBoundary", "TestUniversalInvariantOracleControlledMutants", "TestZombieWorkspaceRemainsWritableButAuthorityIsFenced"}

func VerifyWP14CampaignReceipt(repository string, raw []byte) (WP14CampaignReceipt, error) {
	source, err := os.ReadFile(filepath.Join(repository, "internal/scenario/campaign_test.go"))
	if err != nil {
		return WP14CampaignReceipt{}, err
	}
	return VerifyWP14CampaignReceiptBytes(source, raw)
}

func VerifyWP14CampaignReceiptBytes(source, raw []byte) (WP14CampaignReceipt, error) {
	receipt := WP14CampaignReceipt{SchemaVersion: 1, RawSHA256: SHA256(raw), Families: len(wp14FamilyClaims), PublicationCuts: len(wp14PublicationCuts), MutantSuites: len(wp14MutantSuites), Concurrency: len(wp14Concurrency), Universal: len(wp14Universal), Verdict: "fail"}
	receipt.SourceSHA256 = SHA256(source)
	if receipt.SourceSHA256 != wp14CampaignSourceSHA256 {
		return receipt, fmt.Errorf("WP14 structured campaign source differs from frozen accepted bytes")
	}
	parent := "TestCrashAmbiguousDishonestUniversalCampaign100"
	receipt.ExpectedChildren = expectedWP14Children()
	receipt.FamilyClaims = len(wp14FamilyClaims) * 3
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return receipt, err
		}
		if !strings.HasPrefix(event.Test, parent+"/") {
			continue
		}
		switch event.Action {
		case "run":
			receipt.RunChildren = append(receipt.RunChildren, event.Test)
		case "pass":
			receipt.PassedChildren = append(receipt.PassedChildren, event.Test)
		case "fail", "skip":
			return receipt, fmt.Errorf("WP14 campaign child %s ended %s", event.Test, event.Action)
		}
	}
	if err := scanner.Err(); err != nil {
		return receipt, err
	}
	slices.Sort(receipt.RunChildren)
	slices.Sort(receipt.PassedChildren)
	if !slices.Equal(receipt.RunChildren, receipt.ExpectedChildren) || !slices.Equal(receipt.PassedChildren, receipt.ExpectedChildren) {
		return receipt, fmt.Errorf("WP14 campaign child census is missing, duplicated, extra, or substituted")
	}
	receipt.Verdict = "pass"
	return receipt, nil
}

func expectedWP14Children() []string {
	parent := "TestCrashAmbiguousDishonestUniversalCampaign100"
	var expected []string
	for family, tests := range wp14FamilyClaims {
		for _, test := range tests {
			expected = append(expected, parent+"/family/"+family+"/"+test)
		}
	}
	for _, cut := range wp14PublicationCuts {
		expected = append(expected, parent+"/publication-cut/"+cut)
	}
	for _, mutant := range wp14MutantSuites {
		expected = append(expected, parent+"/mutant-suite/"+mutant)
	}
	for _, test := range wp14Concurrency {
		expected = append(expected, parent+"/concurrency/"+test)
	}
	for _, test := range wp14Universal {
		expected = append(expected, parent+"/universal/"+test)
	}
	slices.Sort(expected)
	return expected
}
