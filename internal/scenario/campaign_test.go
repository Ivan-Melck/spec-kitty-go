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

var mandatoryFaultCuts = []string{
	"before_publication",
	"after_commit_before_response",
	"lost_response",
	"malformed_acknowledgement",
	"restart_before_reconciliation",
	"restart_after_reconciliation",
}

type executableProof struct {
	Package      string `json:"package"`
	Test         string `json:"test"`
	TrialsPerRun int    `json:"trials_per_run"`
}

type faultCell struct {
	Cut   string          `json:"cut"`
	Proof executableProof `json:"proof"`
}

// Every event family has a source-bound product test for every publication
// cut. A test may cover several cuts in one table, but unlike the old campaign
// it may not borrow an unrelated shared-committer test from another family.
var familyFaultProofs = map[string][]faultCell{
	"runtime_host": familyCells(
		proof("./internal/productionruntime", "TestFreshEpochRequiresSealedRestoreAndRotatesPredecessorDurably"),
		proof("./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"),
		proof("./internal/productionruntime", "TestHostProjectionRefreshRetriesConcurrentStablePrefixMovement"),
	),
	"operation": familyCells(
		proof("./internal/controljournal/application", "TestServiceOneBatchStableIdentityAndNoSilentRetry"),
		proof("./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles"),
		proof("./internal/controljournal/application", "TestServiceMalformedAcknowledgementIsIndeterminate"),
	),
	"mission": familyCells(
		proof("./internal/mission", "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2"),
		proof("./internal/controlstore/gapdb", "TestMissionAuthorityAdapterResponseLossReconcilesExactPublicationV2"),
		proof("./internal/controlstore/gapdb", "TestMissionAuthorityAdapterOneBatchRestartReplayV2"),
	),
	"artifact_catalog": familyCells(
		proof("./internal/artifact", "TestArtifactV2RefusesStaleCatalogAndFailedValidationApproval"),
		proof("./internal/controlstore/gapdb", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing"),
		proof("./internal/artifact", "TestPublishReplayConflictAndAmbiguousCommitRecovery"),
	),
	"artifact_revision": familyCells(
		proof("./internal/artifact", "TestArtifactV2RequiresExactObservedContentBytes"),
		proof("./internal/controlstore/gapdb", "TestCapabilityAuthorityRecoversCommittedLostResponseWithoutRepublishing"),
		proof("./internal/artifact", "TestPublishReplayConflictAndAmbiguousCommitRecovery"),
	),
	"workgraph": familyCells(
		proof("./internal/workgraph", "TestWorkgraphV2RefusesIncompleteEligibilityAndWrongLineage"),
		proof("./internal/controlstore/gapdb", "TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB"),
		proof("./internal/workgraph", "TestWorkgraphV2ExactReplayPrecedesEpochAndChangedBindingConflicts"),
	),
	"execution_slot": familyCells(
		proof("./internal/execution", "TestExecutionAuthorityMintsGrantFenceAndEligibilityOnlyForGrantsV2"),
		proof("./internal/controlstore/gapdb", "TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2"),
		proof("./internal/execution", "TestExecutionAuthorityRestartCutsHeartbeatExpiryAndReplacementV2"),
	),
	"workspace": familyCells(
		proof("./internal/workspace", "TestPreparationRefusesRepositoryObservationDriftV2"),
		proof("./internal/controlstore/gapdb", "TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2"),
		proof("./internal/workspace", "TestPreparationRestartFromEveryDurableStageIsCanonicalV2"),
	),
	"handoff_slot": familyCells(
		proof("./internal/handoff", "TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2"),
		proof("./internal/controlstore/gapdb", "TestHandoffGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2"),
		proof("./internal/handoff", "TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2"),
	),
	"review_slot": familyCells(
		proof("./internal/review", "TestReviewRejectsPartialCriterionSetChangedVerdictAndReducerRewriteV2"),
		proof("./internal/controlstore/gapdb", "TestReviewGapDBRegisteredLostAcknowledgementReconcilesExactPublicationV2"),
		proof("./internal/review", "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2"),
	),
	"acceptance": familyCells(
		proof("./internal/integration", "TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2"),
		proof("./internal/controlstore/gapdb", "TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2"),
		proofTrials("./internal/integration", "TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2", 100),
	),
	"integration_target": familyCells(
		proof("./internal/integration", "TestIntegrationRefusesBeforeAnyRepositoryObservationV2"),
		proofTrials("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", 100),
		proof("./internal/controlstore/gapdb", "TestIntegrationRepairRequiredRestoresThroughCheckpointSuffixWithoutGitV2"),
	),
	"integration_attempt": familyCells(
		proof("./internal/integration", "TestIntegrationCannotDowngradeZeroEligibilityBeforeGitV2"),
		proofTrials("./internal/integration", "TestIntegrationLostAuthorityResponseAtEveryStageOneHundredTrialsV2", 100),
		proof("./internal/integration", "TestIntegrationConflictAndManualMovementRequireRepairV2"),
	),
	"external_effect": familyCells(
		proof("./internal/controlstore/gapdb", "TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2"),
		proof("./internal/controlstore/gapdb", "TestAuthorityAdapterReconcilesRegisteredAmbiguousPublicationV2"),
		proof("./internal/execution", "TestExternalEffectRetryProgressionReplaysEveryPublicationV2"),
	),
	"outbox": familyCells(
		proof("./internal/controlstore/gapdb", "TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2"),
		proof("./internal/controlstore/gapdb", "TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2"),
		proof("./internal/controlstore/gapdb", "TestOutboxGapDBFaultsPreserveStableIdentityAndNeverMintPermissionV2"),
	),
}

// familyCells binds the six mandatory cuts to the family's refusal,
// ambiguity/acknowledgement, and restart proof respectively.
func familyCells(before, ambiguous, restart executableProof) []faultCell {
	return []faultCell{
		{Cut: "before_publication", Proof: before},
		{Cut: "after_commit_before_response", Proof: ambiguous},
		{Cut: "lost_response", Proof: ambiguous},
		{Cut: "malformed_acknowledgement", Proof: ambiguous},
		{Cut: "restart_before_reconciliation", Proof: restart},
		{Cut: "restart_after_reconciliation", Proof: restart},
	}
}

type mutantEntry struct {
	ID    string          `json:"id"`
	Proof executableProof `json:"proof"`
}

// These are executable controlled substitutions in the product packages. The
// campaign runs them; merely listing a mutant is never counted as a kill.
var productMutantProofs = []mutantEntry{
	{"omitted_intent", proof("./internal/controlstore", "TestOutboxPlanContainsWholeAtomicMembershipMutant")},
	{"partial_batch", proof("./internal/controlstore/gapdb", "TestV2AmbiguityPartialMixedAndContradictoryMembershipCloseAuthority")},
	{"duplicate_transition", proof("./internal/mission", "TestMissionAuthorityExecutableControlledMutantsV2")},
	{"changed_operation_retry", proof("./internal/execution", "TestExecutionAuthorityReplayAndCollisionPrecedeEpochAndCapabilityReadsV2")},
	{"projection_as_authority", proof("./internal/controlstore", "TestOutboxDeliveryEvidenceCannotBecomeProductAuthorityMutant")},
	{"stale_epoch", proof("./internal/productionruntime", "TestHostBarrierRejectsEveryNonReadyStateAndStaleEpochBeforeCommand")},
	{"stale_work_revision", proof("./internal/review", "TestReviewRejectsSubstitutedWorkRevisionAssertionV2")},
	{"stale_lease", proof("./internal/workspace", "TestWritableZombieWorkspaceNeverRegainsAuthorityCampaignV2")},
	{"stale_candidate", proof("./internal/handoff", "TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2")},
	{"stale_policy", proof("./internal/integration", "TestAcceptanceCannotOmitPolicyAuthorityV2")},
	{"same_actor_reviewer", proof("./internal/review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2")},
	{"stale_fence", proof("./internal/integration", "TestIntegrationRejectsSubstitutedPublishObservationV2")},
	{"unsafe_compensation", proof("./internal/execution", "TestAssignmentEffectDispatcherPreservesBeadsSafeCompensationRefusalEvidenceV2")},
	{"integration_fence_bypass", proof("./internal/integration", "TestIntegrationRefusesBeforeAnyRepositoryObservationV2")},
}

type familyContract struct {
	Family      string `json:"family"`
	EventSchema uint32 `json:"event_schema"`
	Reducer     uint32 `json:"reducer"`
	Owner       string `json:"owner"`
}

type campaignEntry struct {
	Family      string      `json:"family"`
	Cells       []faultCell `json:"cells"`
	Repetitions int         `json:"repetitions"`
	Outcome     string      `json:"outcome"`
}

type campaignCensus struct {
	SchemaVersion  uint32          `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	Seed           string          `json:"seed"`
	Entries        []campaignEntry `json:"entries"`
	Mutants        []mutantEntry   `json:"mutants"`
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
	cellCount := 0
	for _, entry := range census.Entries {
		cellCount += len(entry.Cells)
	}
	t.Logf("campaign source=%s repetitions=100 family_fault_cells=%d executed_mutants=%d digest=%s outcome=pass", revision, cellCount*100, len(census.Mutants), hex.EncodeToString(sum[:]))
}

func TestUniversalCampaignCensusMutants(t *testing.T) {
	valid := buildCampaignCensus(t, "mutant-control", 100)
	validateCampaignCensus(t, valid, 100)
	mutants := map[string]func(*campaignCensus){
		"missing_family":           func(c *campaignCensus) { c.Entries = c.Entries[1:] },
		"missing_cut":              func(c *campaignCensus) { c.Entries[0].Cells = c.Entries[0].Cells[1:] },
		"substituted_family_proof": func(c *campaignCensus) { c.Entries[0].Cells[0].Proof = c.Entries[1].Cells[0].Proof },
		"reduced_repetition":       func(c *campaignCensus) { c.Entries[0].Repetitions = 99 },
		"swallowed_refusal":        func(c *campaignCensus) { c.Entries[0].Outcome = "ignored" },
		"missing_mutant":           func(c *campaignCensus) { c.Mutants = c.Mutants[1:] },
		"substituted_mutant_proof": func(c *campaignCensus) { c.Mutants[0].Proof = c.Mutants[1].Proof },
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
		cells, ok := familyFaultProofs[family.Family]
		if !ok {
			t.Fatalf("event family %s has no family-bound fault cells", family.Family)
		}
		entries = append(entries, campaignEntry{Family: family.Family, Cells: cloneFaultCells(cells), Repetitions: repetitions, Outcome: "pass"})
	}
	return campaignCensus{SchemaVersion: productionScenarioSchemaVersion, SourceRevision: revision, Seed: t.Name(), Entries: entries, Mutants: append([]mutantEntry(nil), productMutantProofs...)}
}

func validateCampaignCensus(t *testing.T, census campaignCensus, repetitions int) {
	t.Helper()
	if err := campaignCensusError(census, repetitions); err != nil {
		t.Fatal(err)
	}
}

func campaignCensusError(census campaignCensus, repetitions int) error {
	if census.SchemaVersion != productionScenarioSchemaVersion || census.SourceRevision == "" || census.Seed == "" || len(census.Entries) != len(familyFaultProofs) || len(census.Mutants) != len(productMutantProofs) {
		return errCampaign("incomplete envelope")
	}
	seen := make(map[string]struct{}, len(census.Entries))
	for _, entry := range census.Entries {
		if _, duplicate := seen[entry.Family]; duplicate {
			return errCampaign("duplicate family " + entry.Family)
		}
		seen[entry.Family] = struct{}{}
		want, ok := familyFaultProofs[entry.Family]
		if !ok || entry.Repetitions != repetitions || entry.Outcome != "pass" || !equalFaultCells(entry.Cells, want) {
			return errCampaign("incomplete family " + entry.Family)
		}
	}
	for index, mutant := range census.Mutants {
		if mutant != productMutantProofs[index] {
			return errCampaign("substituted mutant " + mutant.ID)
		}
	}
	return nil
}

func equalFaultCells(got, want []faultCell) bool {
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

func cloneFaultCells(input []faultCell) []faultCell { return append([]faultCell(nil), input...) }

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
	proofs := make(map[string]executableProof)
	for _, entry := range census.Entries {
		for _, cell := range entry.Cells {
			proofs[cell.Proof.Package+"\x00"+cell.Proof.Test] = cell.Proof
		}
	}
	keys := make([]string, 0, len(proofs))
	for key := range proofs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		proof := proofs[key]
		t.Run(proof.Test, func(t *testing.T) { runExecutableProof(t, root, proof, repetitions) })
	}
	mutantProofs := make(map[string]executableProof)
	for _, mutant := range census.Mutants {
		mutantProofs[mutant.Proof.Package+"\x00"+mutant.Proof.Test] = mutant.Proof
	}
	keys = keys[:0]
	for key := range mutantProofs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		proof := mutantProofs[key]
		t.Run("mutant/"+proof.Test, func(t *testing.T) { runExecutableProof(t, root, proof, 1) })
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
	if proof.Test == "TestMissionAuthorityExecutableControlledMutantsV2" {
		command.Env = append(command.Env, "SPEC_KITTY_RUN_WP05_MUTANTS=1")
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
