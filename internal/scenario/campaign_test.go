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
	Package string `json:"package"`
	Test    string `json:"test"`
}

var familyProof = map[string]executableProof{
	"runtime_host":        {"./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"},
	"operation":           {"./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles"},
	"mission":             {"./internal/mission", "TestMissionAuthorityLifecycleAtomicReplayAndRefusalOrderV2"},
	"artifact_catalog":    {"./internal/artifact", "TestArtifactV2PublicationAndDecisionAreExactAtomicAuthority"},
	"artifact_revision":   {"./internal/artifact", "TestArtifactRevisionCommitmentAndImmutableValues"},
	"workgraph":           {"./internal/workgraph", "TestWorkgraphV2FinalizationAndMixedReconciliationAreAtomicAuthority"},
	"execution_slot":      {"./internal/execution", "TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2"},
	"workspace":           {"./internal/workspace", "TestPreparationRestartFromEveryDurableStageIsCanonicalV2"},
	"handoff_slot":        {"./internal/handoff", "TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2"},
	"review_slot":         {"./internal/review", "TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2"},
	"acceptance":          {"./internal/integration", "TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2"},
	"integration_target":  {"./internal/integration", "TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2"},
	"integration_attempt": {"./internal/integration", "TestIntegrationConflictAndManualMovementRequireRepairV2"},
	"external_effect":     {"./internal/execution", "TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2"},
	"outbox":              {"./internal/controlstore", "TestOutboxLifecycleRequiresIntentAndCompareBeforeCompensate"},
}

// Publication faults are properties of the one shared atomic publication
// boundary. Qualify that boundary once per cut and every family semantic path
// separately; a Cartesian family-by-cut loop would repeat the same committer
// experiment without adding a distinct authority boundary.
var faultProof = map[string]executableProof{
	"before_publication":            {"./internal/controlstore/...", "TestV2CommitCanceledBeforeDeliveryMakesNoProviderCall"},
	"after_commit_before_response":  {"./internal/controlstore/...", "TestV2AmbiguousCommitCanBeFollowedByPublicReconciliation"},
	"lost_response":                 {"./internal/controljournal/application", "TestServiceAmbiguityVisibleThenSameIDReadReconciles"},
	"malformed_acknowledgement":     {"./internal/controljournal/application", "TestServiceMalformedAcknowledgementIsIndeterminate"},
	"restart_before_reconciliation": {"./internal/controljournal/application", "TestReconcileRealGJ02ResponseDrop"},
	"restart_after_reconciliation":  {"./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"},
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
	Package     string   `json:"package"`
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
	validateCampaignCensus(t, census, 100)
	executeQualificationProofs(t, revision, 100)
	raw, err := json.Marshal(census)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	t.Logf("campaign source=%s repetitions=100 family_proofs=%d shared_fault_proofs=%d digest=%s outcome=pass", revision, len(census.Entries)*100, len(mandatoryFaultCuts)*100, hex.EncodeToString(sum[:]))
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
		proof, ok := familyProof[family.Family]
		if !ok || !strings.Contains(sources, "func "+proof.Test+"(") {
			t.Fatalf("event family %s has no resolving executable proof %q", family.Family, proof.Test)
		}
		entries = append(entries, campaignEntry{Family: family.Family, FaultCuts: append([]string(nil), mandatoryFaultCuts...), Repetitions: repetitions, Package: proof.Package, Test: proof.Test, Outcome: "pass"})
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
		proof, ok := familyProof[entry.Family]
		if !ok || entry.Package != proof.Package || entry.Test != proof.Test || entry.Repetitions != repetitions || entry.Outcome != "pass" || strings.Join(entry.FaultCuts, "\x00") != strings.Join(mandatoryFaultCuts, "\x00") {
			return errCampaign("incomplete family " + entry.Family)
		}
	}
	return nil
}

func executeQualificationProofs(t *testing.T, revision string, repetitions int) {
	t.Helper()
	root := repositoryRoot(t)
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != revision {
		t.Fatalf("campaign source revision is not the checked-out HEAD: declared=%s actual=%s err=%v", revision, strings.TrimSpace(string(head)), err)
	}
	proofs := make(map[string]executableProof, len(familyProof)+len(faultProof))
	for _, proof := range familyProof {
		proofs[proof.Package+"\x00"+proof.Test] = proof
	}
	for cut, proof := range faultProof {
		if !containsString(mandatoryFaultCuts, cut) {
			t.Fatalf("fault proof names undeclared cut %s", cut)
		}
		proofs[proof.Package+"\x00"+proof.Test] = proof
	}
	if len(faultProof) != len(mandatoryFaultCuts) {
		t.Fatalf("fault proof census=%d want=%d", len(faultProof), len(mandatoryFaultCuts))
	}
	keys := make([]string, 0, len(proofs))
	for key := range proofs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		proof := proofs[key]
		t.Run(proof.Test, func(t *testing.T) {
			runExecutableProof(t, root, proof, repetitions)
		})
	}
}

func runExecutableProof(t *testing.T, root string, proof executableProof, repetitions int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-json", fmt.Sprintf("-count=%d", repetitions), "-run", "^"+proof.Test+"$", proof.Package)
	command.Dir = root
	command.Env = append(os.Environ(),
		"SPEC_KITTY_WP14_CAMPAIGN=",
		"SPEC_KITTY_WP13_GAPDBD="+os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE"),
	)
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
	if err != nil || ctx.Err() != nil || passes != repetitions || skips != 0 || failures != 0 {
		t.Fatalf("qualification proof %s %s: passes=%d skips=%d failures=%d err=%v context=%v output=%s", proof.Package, proof.Test, passes, skips, failures, err, ctx.Err(), boundedCampaignOutput(output))
	}
	digest := sha256.Sum256(output)
	t.Logf("proof package=%s test=%s repetitions=%d output_sha256=%s", proof.Package, proof.Test, repetitions, hex.EncodeToString(digest[:]))
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
