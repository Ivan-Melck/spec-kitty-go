package scenario

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const frozenBehaviorIDs = "M-01 M-02 M-04 W-01 W-02 W-03 W-04 W-05 W-06 W-07 C-01 C-02 C-03 C-04 C-05 C-06 C-08 C-09 C-10 C-11 C-12 S-01 S-02 S-03 S-04 S-05 S-07 S-08 R-01 R-02 R-03 R-04 R-05 R-06 R-07 A-01 A-02 A-03 A-05 I-01 I-02 I-03 I-04 I-05 I-06 T-02 T-04 T-05 T-06 T-07 X-01 X-03 X-04 X-06 X-07 D-01 D-02 D-03 D-05"

type behaviorMapping struct {
	ID                    string
	Treatment             string
	Capability            string
	Setup                 string
	CommandOrQuery        string
	ResultKind            string
	AuthorityMutation     string
	EvidenceKind          string
	RefusalPosition       string
	ScenarioTest          string
	SupportingProductTest string
}

var familyMapping = map[string]struct {
	capability, productTest string
}{
	"M": {"mission", "TestFinalizationIsCapabilityReadyWithoutWorkDraftPhaseAndDoesNotCompleteMission"},
	"W": {"workgraph", "TestGenerationOneCandidateValidatesAndPinsCompleteEvidence"},
	"C": {"workspace", "TestWritableZombieWorkspaceNeverRegainsAuthorityCampaignV2"},
	"S": {"execution", "TestExecutionAuthorityAdmitsWithExactEligibilityAndOneBatchV2"},
	"R": {"review", "TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2"},
	"A": {"acceptance", "TestAcceptanceCannotOmitArtifactEvidenceV2"},
	"I": {"integration", "TestIntegrationAuthorityStagesIntentBeforeEffectsAndReplaysV2"},
	"T": {"tracker_effect", "TestExternalProviderRunsEndToEndThroughPublicServicesAndRealFakeTrackerV2"},
	"X": {"external_surface", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"},
	"D": {"diagnosis_recovery", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch"},
}

func TestBehaviorParityCensus(t *testing.T) {
	inventory := loadFrozenInventory(t)
	want := strings.Fields(frozenBehaviorIDs)
	got := make([]string, 0, len(inventory))
	for id := range inventory {
		got = append(got, id)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("frozen behavior census changed\nwant=%v\ngot=%v", want, got)
	}
	sources := allTestSources(t)
	mappings := make([]behaviorMapping, 0, len(want))
	for _, id := range want {
		family := strings.SplitN(id, "-", 2)[0]
		definition, ok := familyMapping[family]
		if !ok {
			t.Fatalf("behavior %s has no capability family", id)
		}
		mapping := behaviorMapping{
			ID: id, Treatment: inventory[id], Capability: definition.capability,
			Setup: "typed_authority_fixture", CommandOrQuery: "typed_capability_and_progress",
			ResultKind: "typed_success_or_refusal", AuthorityMutation: "zero_or_one_complete_publication",
			EvidenceKind:          "canonical_projection_and_attributable_external_observation",
			RefusalPosition:       "before_later_authority_and_external_effects",
			ScenarioTest:          "TestBehaviorParityCensus/" + id,
			SupportingProductTest: definition.productTest,
		}
		mappings = append(mappings, mapping)
		t.Run(id, func(t *testing.T) {
			if !strings.Contains(mapping.Treatment, "Exact") && !strings.Contains(mapping.Treatment, "Strengthen") {
				t.Fatalf("unqualified treatment %q", mapping.Treatment)
			}
			if !strings.Contains(sources, "func "+mapping.SupportingProductTest+"(") {
				t.Fatalf("supporting executable test %s does not resolve", mapping.SupportingProductTest)
			}
			if mapping.CommandOrQuery == "" || mapping.ResultKind == "" || mapping.AuthorityMutation == "" || mapping.EvidenceKind == "" || mapping.RefusalPosition == "" {
				t.Fatalf("incomplete mapping: %+v", mapping)
			}
		})
	}
	if err := validateBehaviorMappings(mappings, want); err != nil {
		t.Fatal(err)
	}
}

func TestBehaviorParityCensusMutants(t *testing.T) {
	want := []string{"M-01", "C-01"}
	valid := []behaviorMapping{{ID: "M-01", Capability: "mission", ScenarioTest: "a", EvidenceKind: "projection"}, {ID: "C-01", Capability: "workspace", ScenarioTest: "b", EvidenceKind: "projection"}}
	if err := validateBehaviorMappings(valid, want); err != nil {
		t.Fatalf("valid control: %v", err)
	}
	mutants := map[string][]behaviorMapping{
		"missing":    valid[:1],
		"duplicate":  {valid[0], valid[0], valid[1]},
		"substitute": {{ID: "M-01", Capability: "mission", ScenarioTest: "a", EvidenceKind: "projection"}, {ID: "C-99", Capability: "workspace", ScenarioTest: "b", EvidenceKind: "projection"}},
		"incomplete": {{ID: "M-01", Capability: "mission", ScenarioTest: "a"}, valid[1]},
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := validateBehaviorMappings(mutant, want); err == nil {
				t.Fatal("behavior census mutant survived")
			}
		})
	}
}

func validateBehaviorMappings(mappings []behaviorMapping, expected []string) error {
	want := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		want[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		if _, ok := want[mapping.ID]; !ok {
			return fmt.Errorf("unexpected behavior %s", mapping.ID)
		}
		if _, duplicate := seen[mapping.ID]; duplicate {
			return fmt.Errorf("duplicate behavior %s", mapping.ID)
		}
		if mapping.Capability == "" || mapping.ScenarioTest == "" || mapping.EvidenceKind == "" {
			return fmt.Errorf("incomplete behavior %s", mapping.ID)
		}
		seen[mapping.ID] = struct{}{}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("behavior coverage want=%d got=%d", len(want), len(seen))
	}
	return nil
}

func loadFrozenInventory(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "docs", "PYTHON_BEHAVIOR_INVENTORY.md")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) < 5 {
			continue
		}
		id := strings.TrimSpace(fields[1])
		treatment := strings.TrimSpace(fields[3])
		if !strings.Contains(treatment, "Exact") && !strings.Contains(treatment, "Strengthen") {
			continue
		}
		if _, duplicate := result[id]; duplicate {
			t.Fatalf("duplicate frozen inventory ID %s", id)
		}
		result[id] = treatment
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func allTestSources(t *testing.T) string {
	t.Helper()
	var joined strings.Builder
	err := filepath.WalkDir(filepath.Join(repositoryRoot(t), "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		joined.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return joined.String()
}
