package testcampaign

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMissionMethodMakeAndWorkflowContracts(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	read := func(path string) string {
		contents, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}
	makefile := read("Makefile")
	for _, required := range []string{
		"mission-contract:", "-profile mission-contract", "mission-acceptance:", "-profile mission-acceptance",
		"mission-adversarial:", "-profile mission-adversarial", "-campaign mission_full",
		"quality: preflight contract beads-contract mission-contract mission-acceptance race static dependencies",
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile contract omitted %q", required)
		}
	}
	quality := read(".github/workflows/quality.yml")
	for _, required := range []string{"permissions:\n  contents: read", "timeout-minutes: 15", "go-version: 1.26.5", "duration_seconds\" -ge 600", "if: always()", "make quality"} {
		if !strings.Contains(quality, required) {
			t.Fatalf("quality workflow contract omitted %q", required)
		}
	}
	if strings.Contains(quality, "mission-adversarial") {
		t.Fatal("slow mission campaign leaked into pull-request workflow")
	}
	full := read(".github/workflows/full-campaign.yml")
	for _, required := range []string{"schedule:", "workflow_dispatch:", "go-version: 1.26.5", "make full-campaign", "if: always()"} {
		if !strings.Contains(full, required) {
			t.Fatalf("full workflow contract omitted %q", required)
		}
	}
	for path, contents := range map[string]string{"quality": quality, "full": full} {
		if strings.Contains(contents, "actions/checkout@v4") || strings.Contains(contents, "actions/setup-go@v6") || strings.Contains(contents, "actions/upload-artifact@v4") {
			t.Fatalf("%s workflow uses a mutable action tag", path)
		}
	}
}

func TestMissionMethodProfilesAreExactSeparatedAndDefensive(t *testing.T) {
	historical, err := ProfileByName("full-scenario")
	if err != nil || historical.Pattern == "" || strings.Contains(historical.Pattern, "TestMissionMethodSemanticDriftMutations1000") {
		t.Fatalf("historical full-scenario census was not frozen independently: profile=%+v err=%v", historical, err)
	}
	for _, name := range []string{MissionContractProfile, MissionAcceptanceProfile, MissionAdversarialProfile, MissionRealIdentityProfile} {
		profile, err := ProfileByName(name)
		if err != nil {
			t.Fatalf("profile %s: %v", name, err)
		}
		if profile.Pattern == "" || !strings.HasPrefix(profile.Pattern, "^(") || !strings.HasSuffix(profile.Pattern, ")$") {
			t.Fatalf("profile %s selector is not exact: %q", name, profile.Pattern)
		}
		profile.ExpectedTests[0] = "mutated"
		again, err := ProfileByName(name)
		if err != nil || again.ExpectedTests[0] == "mutated" {
			t.Fatalf("profile %s leaked caller mutation", name)
		}
	}
	fast, _ := ProfileByName(MissionAcceptanceProfile)
	slow, _ := ProfileByName(MissionAdversarialProfile)
	for _, expensive := range []string{
		"TestDishonestMissionAuthorityDetectsSameCountOutboxMutation",
		"TestMissionMethodBeadsDishonestProviderRefusesBeforePublication",
		"TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse",
		"TestMissionMethodCrashBoundaryCensus",
		"TestMissionMethodFinalizationProcessRaces100",
		"TestMissionMethodSemanticDriftMutations1000",
	} {
		if slices.Contains(fast.ExpectedTests, expensive) || !slices.Contains(slow.ExpectedTests, expensive) {
			t.Fatalf("expensive test %s crossed fast/slow boundary", expensive)
		}
	}
	for _, portable := range []string{"TestMissionMethodNormalizationAndPureQueries100", "TestMissionMethodProjectionAndGovernanceIndependence100", "TestMissionMethodProviderParityPortableCampaign"} {
		if !slices.Contains(fast.ExpectedTests, portable) || slices.Contains(slow.ExpectedTests, portable) {
			t.Fatalf("portable acceptance test %s crossed fast/slow boundary", portable)
		}
	}
	contract, _ := ProfileByName(MissionContractProfile)
	for _, identity := range []string{"TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation", "TestMissionMethodQualificationContract", "TestQualityCampaignUniversalInvariants", "TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries"} {
		if !slices.Contains(contract.ExpectedTests, identity) {
			t.Fatalf("mission contract profile omitted %s", identity)
		}
	}
	if slices.Contains(contract.ExpectedTests, "TestMissionMethodBeadsQualificationProfileIdentity") {
		t.Fatal("opt-in real-provider identity leaked into the portable contract profile")
	}
	realIdentity, err := ProfileByName(MissionRealIdentityProfile)
	if err != nil || !slices.Equal(realIdentity.ExpectedTests, []string{"TestMissionMethodBeadsQualificationProfileIdentity"}) {
		t.Fatalf("real-provider identity profile changed: profile=%+v err=%v", realIdentity, err)
	}
	full, err := CampaignByName(MissionFullCampaign)
	if err != nil || slices.Contains(full.Profiles, MissionRealIdentityProfile) || slices.Contains(full.RequiredTests, "TestMissionMethodBeadsQualificationProfileIdentity") {
		t.Fatalf("portable mission campaign acquired opt-in real-provider identity: campaign=%+v err=%v", full, err)
	}
}

func TestMissionMethodFullCampaignMutantsNameMissingControls(t *testing.T) {
	campaign, err := CampaignByName(MissionFullCampaign)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{MissionAcceptanceProfile, MissionAdversarialProfile, MissionContractProfile} {
		mutant := campaign
		mutant.Profiles = slices.DeleteFunc(append([]string(nil), campaign.Profiles...), func(value string) bool { return value == profile })
		if err := ValidateMissionCampaign(mutant); err == nil {
			t.Fatalf("missing profile mutant %s: %v", profile, err)
		}
	}
	for _, required := range campaign.RequiredTests {
		mutant := campaign
		mutant.RequiredTests = slices.DeleteFunc(append([]string(nil), campaign.RequiredTests...), func(value string) bool { return value == required })
		if err := ValidateMissionCampaign(mutant); err == nil {
			t.Fatalf("required-test removal mutant survived for %s", required)
		}
	}
}

func TestMissionMethodReportRejectsSkipMissingExtraDuplicateAndUnterminated(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	exact := Report{Verdict: "pass", ExpectedTests: []string{"TestAlpha"}, StartedTests: []string{"TestAlpha"}, TerminalTests: map[string]string{"TestAlpha": "pass"}, StartedAt: now, FinishedAt: now.Add(time.Second)}
	if err := ValidateMissionReport(exact); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Report){
		"skip":    func(r *Report) { r.TerminalTests["TestAlpha"] = "skip" },
		"missing": func(r *Report) { delete(r.TerminalTests, "TestAlpha") },
		"failed":  func(r *Report) { r.TerminalTests["TestAlpha"] = "fail" },
		"noncanonical": func(r *Report) {
			r.ExpectedTests = []string{"TestBeta", "TestAlpha"}
			r.TerminalTests["TestBeta"] = "pass"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := exact
			candidate.ExpectedTests = append([]string(nil), exact.ExpectedTests...)
			candidate.TerminalTests = map[string]string{"TestAlpha": "pass"}
			mutate(&candidate)
			if err := ValidateMissionReport(candidate); err == nil {
				t.Fatal("census mutant survived")
			}
		})
	}
}
