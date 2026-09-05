package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type executableProof struct {
	Package      string `json:"package"`
	Test         string `json:"test"`
	TrialsPerRun int    `json:"trials_per_run"`
}

type evidenceClaim struct {
	Behavior string          `json:"behavior"`
	Proof    executableProof `json:"proof"`
}

// Every event family has source-bound product behavior evidence. A claim names
// only the behavior its executable proof actually exercises; cut-specific
// qualification is accounted for separately and must never be inferred from
// this baseline.
var familyEvidenceProofs = map[string][]evidenceClaim{
	"runtime_host": familyClaims(
		proof("./internal/productionruntime", "TestFreshEpochRequiresSealedRestoreAndRotatesPredecessorDurably"),
		proof("./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"),
		proof("./internal/productionruntime", "TestHostProjectionRefreshRetriesConcurrentStablePrefixMovement"),
	),
	"operation": familyClaims(
		proof("./internal/controljournal/application", "TestServiceOneBatchStableIdentityAndNoSilentRetry"),
		proof("./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles"),
		proof("./internal/controljournal/application", "TestServiceMalformedAcknowledgementIsIndeterminate"),
	),
	"mission": familyClaims(
		proof("./internal/mission", "TestReadinessRelatedFactRefusesCompetingFactRevisionBeforePublication"),
		proof("./internal/mission", "TestReadinessServiceExactReplayAndCollisionPrecedeEpochAndAuthorityReads"),
		proof("./internal/controlstore/gapdb", "TestReadinessAuthorityRealGapDBCheckpointSuffixMatchesOriginAndProjectionLossV2"),
	),
	"artifact_catalog": familyClaims(
		proof("./internal/artifact", "TestArtifactV2RefusesStaleCatalogAndFailedValidationApproval"),
		proof("./internal/controlstore/gapdb", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing"),
		proof("./internal/artifact", "TestPublishReplayConflictAndAmbiguousCommitRecovery"),
	),
	"artifact_revision": familyClaims(
		proof("./internal/artifact", "TestArtifactV2RequiresExactObservedContentBytes"),
		proof("./internal/controlstore/gapdb", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing"),
		proof("./internal/artifact", "TestPublishReplayConflictAndAmbiguousCommitRecovery"),
	),
	"workgraph": familyClaims(
		proof("./internal/workgraph", "TestWorkgraphV2RefusesIncompleteEligibilityAndWrongLineage"),
		proof("./internal/controlstore/gapdb", "TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB"),
		proof("./internal/workgraph", "TestWorkgraphV2ExactReplayPrecedesEpochAndChangedBindingConflicts"),
	),
	"execution_slot": familyClaims(
		proof("./internal/execution", "TestExecutionAuthorityMintsGrantFenceAndEligibilityOnlyForGrantsV2"),
		proof("./internal/controlstore/gapdb", "TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2"),
		proof("./internal/execution", "TestExecutionAuthorityRestartCutsHeartbeatExpiryAndReplacementV2"),
	),
	"workspace": familyClaims(
		proof("./internal/workspace", "TestPreparationRefusesRepositoryObservationDriftV2"),
		proof("./internal/controlstore/gapdb", "TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2"),
		proof("./internal/workspace", "TestPreparationRestartFromEveryDurableStageIsCanonicalV2"),
	),
	"handoff_slot": familyClaims(
		proof("./internal/handoff", "TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2"),
		proof("./internal/controlstore/gapdb", "TestHandoffGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2"),
		proof("./internal/handoff", "TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2"),
	),
	"review_slot": familyClaims(
		proof("./internal/review", "TestReviewRejectsPartialCriterionSetChangedVerdictAndReducerRewriteV2"),
		proof("./internal/controlstore/gapdb", "TestReviewGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2"),
		proof("./internal/review", "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2"),
	),
	"acceptance": familyClaims(
		proof("./internal/integration", "TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2"),
		proof("./internal/controlstore/gapdb", "TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2"),
		proofTrials("./internal/integration", "TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2", 100),
	),
	"integration_target": familyClaims(
		proof("./internal/integration", "TestIntegrationRefusesBeforeAnyRepositoryObservationV2"),
		proofTrials("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", 100),
		proof("./internal/controlstore/gapdb", "TestIntegrationRepairRequiredRestoresThroughCheckpointSuffixWithoutGitV2"),
	),
	"integration_attempt": familyClaims(
		proof("./internal/integration", "TestIntegrationCannotDowngradeZeroEligibilityBeforeGitV2"),
		proofTrials("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", 100),
		proof("./internal/integration", "TestIntegrationConflictAndManualMovementRequireRepairV2"),
	),
	"external_effect": familyClaims(
		proof("./internal/controlstore/gapdb", "TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2"),
		proof("./internal/controlstore/gapdb", "TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2"),
		proof("./internal/execution", "TestExternalEffectRetryProgressionReplaysEveryPublicationV2"),
	),
	"outbox": familyClaims(
		proof("./internal/controlstore/gapdb", "TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2"),
		proof("./internal/controlstore/gapdb", "TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2"),
		proof("./internal/controlstore/gapdb", "TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2"),
	),
}

type publicationCutProof struct {
	Cut   string          `json:"cut"`
	Proof executableProof `json:"proof"`
}

// Production publication cuts are an orthogonal evidence axis. Each entry is
// one real experiment at the shared v2 coordinator/GapDB boundary; family
// claims above are never multiplied by these labels.
var productionPublicationCutProofs = []publicationCutProof{
	{"before_publication", proof("./internal/controlstore/gapdb", "TestV2CommitCanceledBeforeDeliveryMakesNoProviderCall")},
	{"after_durable_commit_before_response", proof("./internal/controlstore/gapdb", "TestV2AssertionCandidateSidecarQualification")},
	{"lost_response", proof("./internal/controlstore/gapdb", "TestV2AmbiguousCommitCanBeFollowedByPublicReconciliation")},
	{"malformed_acknowledgement", proof("./internal/controlstore/gapdb", "TestV2CommitRefusesWireBudgetBeforeProviderAndValidatesAck")},
	{"restart_before_reconciliation", proof("./internal/controlstore/gapdb", "TestProductionPublicationRecoveryAcrossOwnerRestartCuts/restart_before_reconciliation")},
	{"restart_after_reconciliation", proof("./internal/controlstore/gapdb", "TestProductionPublicationRecoveryAcrossOwnerRestartCuts/restart_after_reconciliation")},
}

// familyClaims records only the behavior each executable test actually proves.
// A behavior is never expanded into several synthetic fault-cut labels.
func familyClaims(proofs ...executableProof) []evidenceClaim {
	claims := make([]evidenceClaim, len(proofs))
	for index, item := range proofs {
		claims[index] = evidenceClaim{Behavior: item.Test, Proof: item}
	}
	return claims
}

type mutantSuite struct {
	ID    string          `json:"id"`
	Proof executableProof `json:"proof"`
}

// These are executable controlled substitutions in the product packages. The
// campaign runs them; merely listing a mutant is never counted as a kill.
var controlledMutantSuites = []mutantSuite{
	{"readiness_source_overlay", proof("./internal/mission", "TestAutonomousReadinessExecutableControlledMutants")},
	{"integration_source_overlay", proof("./internal/integration", "TestWP10ExecutableControlledMutantCensusV2")},
	{"production_host_source_overlay", proof("./internal/productionruntime", "TestWP13MandatoryProductionMutantCampaign")},
	{"workspace_semantic_mutants", proof("./internal/workspace", "TestWorkspaceControlledMutantCensusV2")},
	{"execution_authority_mutants", proof("./internal/execution", "TestWP07ExecutableExecutionMutantsV2")},
	{"effect_authority_mutants", proof("./internal/execution", "TestWP07ExecutableEffectMutantsV2")},
	{"journal_acknowledgement_mutants", proof("./internal/controljournal/v2", "TestAcknowledgementAttributionAndOpaqueSealMutants")},
	{"production_composition_bypass", proof("./internal/scenario", "TestProductionCapabilityCompositionBoundaryBypassMutant")},
	{"universal_progress_oracle", proof("./internal/scenario", "TestUniversalInvariantOracleControlledMutants")},
	{"work_coordinate_oracle", proof("./internal/scenario", "TestWorkCoordinateAgreementControlledMutants")},
}

var concurrencyProofs = []executableProof{
	proof("./internal/review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2"),
	proofTrials("./internal/integration", "TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2", 100),
	proofTrials("./internal/integration", "TestIndependentTargetsProgressConcurrentlyWithoutGlobalLockV2", 100),
	proofTrials("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", 100),
	proofTrials("./internal/controlstore/gapdb", "TestExecutionAuthorityProductionContentionMatrixV2", 100),
	proofTrials("./internal/controlstore/gapdb", "TestEffectAuthoritySameSlotAndUnrelatedSlotContentionCampaignV2", 100),
	proofTrials("./internal/controlstore/gapdb", "TestWorkspacePathClaimAndIndependentWorkspaceCampaignV2", 100),
	proof("./internal/productionruntime", "TestHostIndependentCommandsRunConcurrentlyWithoutGlobalCommandMutex"),
}

var universalScenarioProofs = []executableProof{
	proof("./internal/scenario", "TestProductionCapabilityCompositionHasOnePublicationBoundary"),
	proof("./internal/scenario", "TestHarnessProductionLifecycleAndTypedQueries"),
	proof("./internal/scenario", "TestTwoItemMissionRestartAfterEachDurableBoundary"),
	proof("./internal/scenario", "TestUniversalInvariantOracleControlledMutants"),
	proof("./internal/scenario", "TestZombieWorkspaceRemainsWritableButAuthorityIsFenced"),
}

type familyContract struct {
	Family      string `json:"family"`
	EventSchema uint32 `json:"event_schema"`
	Reducer     uint32 `json:"reducer"`
	Owner       string `json:"owner"`
}

type campaignEntry struct {
	Family      string          `json:"family"`
	Claims      []evidenceClaim `json:"claims"`
	Repetitions int             `json:"repetitions"`
	Outcome     string          `json:"outcome"`
}

type campaignCensus struct {
	SchemaVersion   uint32                `json:"schema_version"`
	SourceRevision  string                `json:"source_revision"`
	Seed            string                `json:"seed"`
	Entries         []campaignEntry       `json:"entries"`
	PublicationCuts []publicationCutProof `json:"publication_cuts"`
	MutantSuites    []mutantSuite         `json:"mutant_suites"`
	Concurrency     []executableProof     `json:"concurrency"`
	Universal       []executableProof     `json:"universal_scenarios"`
}

func TestCrashAmbiguousDishonestUniversalCensus(t *testing.T) {
	census := buildCampaignCensus(t, "working-tree", 1)
	validateCampaignCensus(t, census, 1)
}

func TestCrashAmbiguousDishonestUniversalExecutableSmoke(t *testing.T) {
	census := buildCampaignCensus(t, "working-tree", 1)
	validateCampaignCensus(t, census, 1)
	executeProofSet(t, census, 1)
}

func TestCrashAmbiguousDishonestUniversalCampaign100(t *testing.T) {
	requireFullCampaign(t)
	revision := os.Getenv("SPEC_KITTY_WP14_SOURCE_REVISION")
	if revision == "" {
		t.Fatal("full campaign requires SPEC_KITTY_WP14_SOURCE_REVISION")
	}
	census := buildCampaignCensus(t, revision, 100)
	validateCampaignCensus(t, census, 100)
	executeQualificationProofs(t, revision, census, 100)
	raw, err := json.Marshal(census)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	claimCount := 0
	for _, entry := range census.Entries {
		claimCount += len(entry.Claims)
	}
	t.Logf("campaign source=%s repetitions=100 family_behavior_trials=%d publication_cut_trials=%d controlled_mutant_suites=%d concurrency_proofs=%d universal_scenarios=%d digest=%s outcome=pass", revision, claimCount*100, len(census.PublicationCuts)*100, len(census.MutantSuites), len(census.Concurrency), len(census.Universal), hex.EncodeToString(sum[:]))
}

func TestUniversalCampaignCensusMutants(t *testing.T) {
	valid := buildCampaignCensus(t, "mutant-control", 100)
	validateCampaignCensus(t, valid, 100)
	mutants := map[string]func(*campaignCensus){
		"missing_family":           func(c *campaignCensus) { c.Entries = c.Entries[1:] },
		"missing_claim":            func(c *campaignCensus) { c.Entries[0].Claims = c.Entries[0].Claims[1:] },
		"substituted_family_proof": func(c *campaignCensus) { c.Entries[0].Claims[0].Proof = c.Entries[1].Claims[0].Proof },
		"missing_publication_cut":  func(c *campaignCensus) { c.PublicationCuts = c.PublicationCuts[1:] },
		"substituted_cut_proof":    func(c *campaignCensus) { c.PublicationCuts[0].Proof = c.PublicationCuts[1].Proof },
		"reduced_repetition":       func(c *campaignCensus) { c.Entries[0].Repetitions = 99 },
		"swallowed_refusal":        func(c *campaignCensus) { c.Entries[0].Outcome = "ignored" },
		"missing_mutant_suite":     func(c *campaignCensus) { c.MutantSuites = c.MutantSuites[1:] },
		"substituted_mutant_proof": func(c *campaignCensus) { c.MutantSuites[0].Proof = c.MutantSuites[1].Proof },
		"missing_concurrency":      func(c *campaignCensus) { c.Concurrency = c.Concurrency[1:] },
		"missing_universal":        func(c *campaignCensus) { c.Universal = c.Universal[1:] },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			clone := cloneCampaign(t, valid)
			mutate(&clone)
			if campaignCensusError(clone, 100) == nil {
				t.Fatal("campaign integrity mutant survived")
			}
		})
	}
}

func buildCampaignCensus(t *testing.T, revision string, repetitions int) campaignCensus {
	t.Helper()
	registry := loadEventFamilies(t)
	entries := make([]campaignEntry, 0, len(registry))
	for _, family := range registry {
		claims, ok := familyEvidenceProofs[family.Family]
		if !ok {
			t.Fatalf("event family %s has no family-bound evidence claims", family.Family)
		}
		entries = append(entries, campaignEntry{Family: family.Family, Claims: cloneEvidenceClaims(claims), Repetitions: repetitions, Outcome: "pass"})
	}
	return campaignCensus{
		SchemaVersion: productionScenarioSchemaVersion, SourceRevision: revision, Seed: t.Name(), Entries: entries,
		PublicationCuts: append([]publicationCutProof(nil), productionPublicationCutProofs...), MutantSuites: append([]mutantSuite(nil), controlledMutantSuites...), Concurrency: append([]executableProof(nil), concurrencyProofs...), Universal: append([]executableProof(nil), universalScenarioProofs...),
	}
}

func validateCampaignCensus(t *testing.T, census campaignCensus, repetitions int) {
	t.Helper()
	if err := campaignCensusError(census, repetitions); err != nil {
		t.Fatal(err)
	}
}

func campaignCensusError(census campaignCensus, repetitions int) error {
	if census.SchemaVersion != productionScenarioSchemaVersion || census.SourceRevision == "" || census.Seed == "" || len(census.Entries) != len(familyEvidenceProofs) || !equalPublicationCutProofs(census.PublicationCuts, productionPublicationCutProofs) || len(census.MutantSuites) != len(controlledMutantSuites) || !equalProofs(census.Concurrency, concurrencyProofs) || !equalProofs(census.Universal, universalScenarioProofs) {
		return errCampaign("incomplete envelope")
	}
	seen := make(map[string]struct{}, len(census.Entries))
	for _, entry := range census.Entries {
		if _, duplicate := seen[entry.Family]; duplicate {
			return errCampaign("duplicate family " + entry.Family)
		}
		seen[entry.Family] = struct{}{}
		want, ok := familyEvidenceProofs[entry.Family]
		if !ok || entry.Repetitions != repetitions || entry.Outcome != "pass" || !equalEvidenceClaims(entry.Claims, want) {
			return errCampaign("incomplete family " + entry.Family)
		}
		seenBehavior := make(map[string]struct{}, len(entry.Claims))
		for _, claim := range entry.Claims {
			if claim.Behavior == "" || claim.Behavior != claim.Proof.Test {
				return errCampaign("unattributed behavior " + entry.Family)
			}
			if _, duplicate := seenBehavior[claim.Behavior]; duplicate {
				return errCampaign("duplicate behavior " + entry.Family)
			}
			seenBehavior[claim.Behavior] = struct{}{}
		}
	}
	for index, mutant := range census.MutantSuites {
		if mutant != controlledMutantSuites[index] {
			return errCampaign("substituted mutant " + mutant.ID)
		}
	}
	return nil
}

func equalPublicationCutProofs(got, want []publicationCutProof) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(got))
	for index := range got {
		if got[index] != want[index] || got[index].Cut == "" {
			return false
		}
		if _, duplicate := seen[got[index].Cut]; duplicate {
			return false
		}
		seen[got[index].Cut] = struct{}{}
	}
	return true
}

func equalEvidenceClaims(got, want []evidenceClaim) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func cloneEvidenceClaims(input []evidenceClaim) []evidenceClaim {
	return append([]evidenceClaim(nil), input...)
}

func equalProofs(got, want []executableProof) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func executeQualificationProofs(t *testing.T, revision string, census campaignCensus, repetitions int) {
	t.Helper()
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != revision {
		t.Fatalf("campaign source revision is not checked-out HEAD: declared=%s actual=%s err=%v", revision, strings.TrimSpace(string(head)), err)
	}
	executeProofSet(t, census, repetitions)
}

func executeProofSet(t *testing.T, census campaignCensus, repetitions int) {
	t.Helper()
	root := repositoryRoot(t)
	for _, entry := range census.Entries {
		for _, claim := range entry.Claims {
			entry, claim := entry, claim
			t.Run("family/"+entry.Family+"/"+claim.Behavior, func(t *testing.T) {
				runExecutableProof(t, root, claim.Proof, repetitions)
			})
		}
	}
	for _, cut := range census.PublicationCuts {
		cut := cut
		t.Run("publication-cut/"+cut.Cut, func(t *testing.T) { runExecutableProof(t, root, cut.Proof, repetitions) })
	}
	for _, proof := range census.Concurrency {
		proof := proof
		t.Run("concurrency/"+proof.Test, func(t *testing.T) { runExecutableProof(t, root, proof, repetitions) })
	}
	for _, proof := range census.Universal {
		proof := proof
		t.Run("universal/"+proof.Test, func(t *testing.T) { runExecutableProof(t, root, proof, 1) })
	}
	for _, suite := range census.MutantSuites {
		suite := suite
		t.Run("mutant-suite/"+suite.ID, func(t *testing.T) { runExecutableProof(t, root, suite.Proof, 1) })
	}
}

func runExecutableProof(t *testing.T, root string, proof executableProof, repetitions int) {
	t.Helper()
	if proof.TrialsPerRun <= 0 {
		t.Fatalf("proof %s has invalid trials_per_run=%d", proof.Test, proof.TrialsPerRun)
	}
	externalRuns := (repetitions + proof.TrialsPerRun - 1) / proof.TrialsPerRun
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-json", fmt.Sprintf("-count=%d", externalRuns), "-run", "^"+proof.Test+"$", proof.Package)
	command.Dir = root
	command.Env = append(os.Environ(), "SPEC_KITTY_WP14_CAMPAIGN=", "SPEC_KITTY_WP13_GAPDBD="+os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE"), "SPEC_KITTY_M1_GAPDBD="+os.Getenv("SPEC_KITTY_WP14_BASE_AUTHORITY_EXECUTABLE"))
	switch proof.Test {
	case "TestV2AssertionCandidateSidecarQualification":
		dataRoot, err := os.MkdirTemp("/tmp", "spk-wp14-cut-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dataRoot)
		command.Env = append(command.Env,
			"SPEC_KITTY_ASSERTION_CANDIDATE_QUALIFICATION=true",
			"SPEC_KITTY_ASSERTION_CANDIDATE_GAPDBD="+os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE"),
			"SPEC_KITTY_ASSERTION_CANDIDATE_DATA_ROOT="+dataRoot,
		)
	case "TestAutonomousReadinessExecutableControlledMutants":
		command.Env = append(command.Env, "SPEC_KITTY_RUN_WP18_MUTANTS=1")
	case "TestWP10ExecutableControlledMutantCensusV2":
		command.Env = append(command.Env, "SPEC_KITTY_RUN_WP10_MUTANTS=1")
	case "TestWP13MandatoryProductionMutantCampaign":
		command.Env = append(command.Env, "SPEC_KITTY_RUN_WP13_MUTANTS=1")
	}
	output, err := command.CombinedOutput()
	if len(output) > 8<<20 {
		t.Fatalf("qualification output exceeded 8 MiB for %s", proof.Test)
	}
	type testEvent struct {
		Action string `json:"Action"`
		Test   string `json:"Test"`
	}
	passes, skips, failures := 0, 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		var event testEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.Test != proof.Test {
			continue
		}
		switch event.Action {
		case "pass":
			passes++
		case "skip":
			skips++
		case "fail":
			failures++
		}
	}
	if err != nil || ctx.Err() != nil || passes != externalRuns || skips != 0 || failures != 0 {
		t.Fatalf("qualification proof %s %s: passes=%d skips=%d failures=%d err=%v context=%v output=%s", proof.Package, proof.Test, passes, skips, failures, err, ctx.Err(), boundedCampaignOutput(output))
	}
	digest := sha256.Sum256(output)
	t.Logf("proof package=%s test=%s requested_trials=%d trials_per_run=%d external_runs=%d output_sha256=%s", proof.Package, proof.Test, repetitions, proof.TrialsPerRun, externalRuns, hex.EncodeToString(digest[:]))
}

func boundedCampaignOutput(output []byte) string {
	const limit = 4096
	if len(output) <= limit {
		return string(output)
	}
	return string(output[:limit]) + "...[truncated]"
}

type errCampaign string

func (e errCampaign) Error() string { return string(e) }

func loadEventFamilies(t *testing.T) []familyContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "kitty-specs", "gapdb-only-production-authority-01M1GWDJ", "contracts", "event-family-registry-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Families []familyContract `json:"families"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	families := registry.Families
	sort.Slice(families, func(i, j int) bool { return families[i].Family < families[j].Family })
	return families
}

func cloneCampaign(t *testing.T, input campaignCensus) campaignCensus {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var clone campaignCensus
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
