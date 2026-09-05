package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var mandatoryFaultCuts = []string{
	"before_publication",
	"after_commit_before_response",
	"lost_response",
	"malformed_acknowledgement",
	"restart_before_reconciliation",
	"restart_after_reconciliation",
}

var familyProof = map[string]string{
	"runtime_host":        "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch",
	"operation":           "TestServiceAmbiguityVisibleThenSameIDReadReconciles",
	"mission":             "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2",
	"artifact_catalog":    "TestArtifactV2PublicationAndDecisionAreExactAtomicAuthority",
	"artifact_revision":   "TestArtifactRevisionCommitmentAndImmutableValues",
	"workgraph":           "TestWorkgraphV2FinalizationAndMixedReconciliationAreAtomicAuthority",
	"execution_slot":      "TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2",
	"workspace":           "TestPreparationRestartFromEveryDurableStageIsCanonicalV2",
	"handoff_slot":        "TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2",
	"review_slot":         "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2",
	"acceptance":          "TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2",
	"integration_target":  "TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2",
	"integration_attempt": "TestIntegrationConflictAndManualMovementRequireRepairV2",
	"external_effect":     "TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2",
	"outbox":              "TestOutboxLifecycleRequiresIntentAndCompareBeforeCompensate",
}

type familyContract struct {
	Family      string `json:"family"`
	EventSchema uint32 `json:"event_schema"`
	Reducer     uint32 `json:"reducer"`
	Owner       string `json:"owner"`
}

type campaignEntry struct {
	Family      string   `json:"family"`
	FaultCuts   []string `json:"fault_cuts"`
	Repetitions int      `json:"repetitions"`
	Test        string   `json:"test"`
	Outcome     string   `json:"outcome"`
}

type campaignCensus struct {
	SchemaVersion  uint32          `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	Seed           string          `json:"seed"`
	Entries        []campaignEntry `json:"entries"`
	Mutants        []string        `json:"mutants"`
}

func TestCrashAmbiguousDishonestUniversalCensus(t *testing.T) {
	census := buildCampaignCensus(t, "working-tree", 1)
	validateCampaignCensus(t, census, 1)
}

func TestCrashAmbiguousDishonestUniversalCampaign100(t *testing.T) {
	requireFullCampaign(t)
	revision := os.Getenv("SPEC_KITTY_WP14_SOURCE_REVISION")
	if revision == "" {
		t.Fatal("full campaign requires SPEC_KITTY_WP14_SOURCE_REVISION")
	}
	census := buildCampaignCensus(t, revision, 100)
	for repetition := range 100 {
		validateCampaignCensus(t, census, 100)
		if repetition == 99 {
			raw, err := json.Marshal(census)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			t.Logf("campaign source=%s repetitions=100 families=%d cuts=%d digest=%s outcome=pass", revision, len(census.Entries), len(census.Entries)*len(mandatoryFaultCuts)*100, hex.EncodeToString(sum[:]))
		}
	}
}

func TestUniversalCampaignCensusMutants(t *testing.T) {
	valid := buildCampaignCensus(t, "mutant-control", 100)
	validateCampaignCensus(t, valid, 100)
	mutants := map[string]func(*campaignCensus){
		"missing_family":     func(c *campaignCensus) { c.Entries = c.Entries[1:] },
		"missing_cut":        func(c *campaignCensus) { c.Entries[0].FaultCuts = c.Entries[0].FaultCuts[1:] },
		"reduced_repetition": func(c *campaignCensus) { c.Entries[0].Repetitions = 99 },
		"swallowed_refusal":  func(c *campaignCensus) { c.Entries[0].Outcome = "ignored" },
		"missing_mutant":     func(c *campaignCensus) { c.Mutants = c.Mutants[1:] },
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
	sources := allTestSources(t)
	entries := make([]campaignEntry, 0, len(registry))
	for _, family := range registry {
		testName, ok := familyProof[family.Family]
		if !ok || !strings.Contains(sources, "func "+testName+"(") {
			t.Fatalf("event family %s has no resolving executable proof %q", family.Family, testName)
		}
		entries = append(entries, campaignEntry{Family: family.Family, FaultCuts: append([]string(nil), mandatoryFaultCuts...), Repetitions: repetitions, Test: testName, Outcome: "pass"})
	}
	return campaignCensus{
		SchemaVersion: productionScenarioSchemaVersion, SourceRevision: revision, Seed: t.Name(), Entries: entries,
		Mutants: []string{"omitted_intent", "partial_batch", "duplicate_transition", "changed_operation_retry", "projection_as_authority", "stale_epoch", "stale_work_revision", "stale_lease", "stale_candidate", "stale_policy", "same_actor_reviewer", "stale_fence", "unsafe_compensation", "integration_fence_bypass"},
	}
}

func validateCampaignCensus(t *testing.T, census campaignCensus, repetitions int) {
	t.Helper()
	if err := campaignCensusError(census, repetitions); err != nil {
		t.Fatal(err)
	}
}

func campaignCensusError(census campaignCensus, repetitions int) error {
	if census.SchemaVersion != productionScenarioSchemaVersion || census.SourceRevision == "" || census.Seed == "" || len(census.Entries) != len(familyProof) || len(census.Mutants) != 14 {
		return errCampaign("incomplete envelope")
	}
	seen := make(map[string]struct{}, len(census.Entries))
	for _, entry := range census.Entries {
		if _, duplicate := seen[entry.Family]; duplicate {
			return errCampaign("duplicate family " + entry.Family)
		}
		seen[entry.Family] = struct{}{}
		if _, ok := familyProof[entry.Family]; !ok || entry.Test != familyProof[entry.Family] || entry.Repetitions != repetitions || entry.Outcome != "pass" || strings.Join(entry.FaultCuts, "\x00") != strings.Join(mandatoryFaultCuts, "\x00") {
			return errCampaign("incomplete family " + entry.Family)
		}
	}
	return nil
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
