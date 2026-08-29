package scenario

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

func TestQualityCampaignUniversalInvariants(t *testing.T) {
	assertCampaignUniversalInvariants(t, "quality")
}

func TestFullCampaignUniversalInvariants(t *testing.T) {
	assertCampaignUniversalInvariants(t, "full-campaign")
}

func TestCampaignProfilesPreserveUniversalInvariants(t *testing.T) {
	for _, name := range []string{"quality", "full_campaign"} {
		campaign, err := testcampaign.CampaignByName(name)
		if err != nil || !campaign.RequireUniversal || !campaign.RequireStaleProbe || campaign.UniversalTest == "" {
			t.Fatalf("campaign %s lost universal invariant policy: %+v err=%v", name, campaign, err)
		}
	}
	covered := scenarioCampaignTopLevelTests(t)
	actual := scenarioTopLevelTests(t)
	if err := compareScenarioCampaignCensus(covered, actual); err != nil {
		t.Fatalf("scenario campaign census drifted: %v", err)
	}

	t.Run("missing-registered-test-mutant", func(t *testing.T) {
		missing := slices.DeleteFunc(append([]string(nil), covered...), func(name string) bool {
			return name == "TestMissionMethodProviderParityPortableCampaign"
		})
		if err := compareScenarioCampaignCensus(missing, actual); err == nil || !strings.Contains(err.Error(), "TestMissionMethodProviderParityPortableCampaign") {
			t.Fatalf("missing registered-test mutant survived: %v", err)
		}
	})

	t.Run("uncatalogued-top-level-mutant", func(t *testing.T) {
		extra := append(append([]string(nil), actual...), "TestUncataloguedScenarioMutant")
		slices.Sort(extra)
		if err := compareScenarioCampaignCensus(covered, extra); err == nil || !strings.Contains(err.Error(), "TestUncataloguedScenarioMutant") {
			t.Fatalf("uncatalogued top-level mutant survived: %v", err)
		}
	})
}

func TestCampaignProfileMutantControls(t *testing.T) {
	for _, name := range []string{"quality", "full_campaign"} {
		campaign, err := testcampaign.CampaignByName(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name+"-stale-probe-removed", func(t *testing.T) {
			mutant := campaign
			mutant.RequireStaleProbe = false
			if err := testcampaign.ValidateCampaign(mutant); err == nil || !strings.Contains(err.Error(), "stale probing") {
				t.Fatalf("stale-probe mutant survived: %v", err)
			}
		})
		t.Run(name+"-universal-test-removed", func(t *testing.T) {
			mutant := campaign
			mutant.UniversalTest = "TestDeletedUniversalInvariant"
			if err := testcampaign.ValidateCampaign(mutant); err == nil || !strings.Contains(err.Error(), "omits universal test") {
				t.Fatalf("universal-test mutant survived: %v", err)
			}
		})
	}
}

func TestCampaignWorkflowContracts(t *testing.T) {
	root, err := phase1RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	makefile := readCampaignFile(t, filepath.Join(root, "Makefile"))
	for _, target := range []string{"preflight", "contract", "acceptance", "scope", "race", "static", "dependencies", "quality", "fault", "soak", "scenario-full", "full-campaign"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(makefile) {
			t.Fatalf("Makefile omits canonical target %s", target)
		}
	}

	workflows := map[string][]string{
		"quality.yml": {
			"pull_request:", "push:", "contents: read", "cancel-in-progress: true", "timeout-minutes: 15",
			"make quality-core", "hosted_baseline", "if: always()", "retention-days: 14", "github.run_id", "github.run_attempt",
		},
		"full-campaign.yml": {
			"schedule:", "workflow_dispatch:", "contents: read", "cancel-in-progress: false", "timeout-minutes: 90",
			"make full-campaign", "hosted_credentials", "if: always()", "retention-days: 14", "github.run_id", "github.run_attempt",
		},
	}
	pinnedAction := regexp.MustCompile(`(?m)^\s*uses:\s*[^@\s]+@[0-9a-f]{40}(?:\s+#.*)?$`)
	for name, required := range workflows {
		content := readCampaignFile(t, filepath.Join(root, ".github", "workflows", name))
		for _, text := range required {
			if !strings.Contains(content, text) {
				t.Fatalf("workflow %s omits %q", name, text)
			}
		}
		if strings.Contains(content, "go test") {
			t.Fatalf("workflow %s duplicates Make-owned Go test commands", name)
		}
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, "uses:") && !pinnedAction.MatchString(line) {
				t.Fatalf("workflow %s has unpinned action line %q", name, line)
			}
		}
	}
}

func scenarioCampaignTopLevelTests(t *testing.T) []string {
	t.Helper()
	profiles := []string{
		"full-scenario",
		"provider-adversarial",
		testcampaign.MissionContractProfile,
		testcampaign.MissionAcceptanceProfile,
		testcampaign.MissionAdversarialProfile,
		testcampaign.MissionRealIdentityProfile,
	}
	seen := make(map[string]struct{})
	for _, name := range profiles {
		profile, err := testcampaign.ProfileByName(name)
		if err != nil {
			t.Fatalf("load scenario campaign profile %s: %v", name, err)
		}
		if profile.Package != "./internal/scenario" {
			t.Fatalf("scenario campaign profile %s targets %s", name, profile.Package)
		}
		for _, test := range profile.ExpectedTests {
			seen[test] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func compareScenarioCampaignCensus(expected, actual []string) error {
	missing := make([]string, 0)
	extra := make([]string, 0)
	for _, name := range expected {
		if !slices.Contains(actual, name) {
			missing = append(missing, name)
		}
	}
	for _, name := range actual {
		if !slices.Contains(expected, name) {
			extra = append(extra, name)
		}
	}
	if len(missing) != 0 || len(extra) != 0 {
		return fmt.Errorf("missing=%v extra=%v", missing, extra)
	}
	return nil
}

func assertCampaignUniversalInvariants(t *testing.T, suffix string) {
	t.Helper()
	fixture := newWorkspaceScenario(t, []workspaceItem{{
		work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE",
	}})
	stale := provisionPhase1StaleAuthority(t, fixture, suffix)
	state := phase1UniversalState(t, fixture, "CAMPAIGN-"+suffix, fixture.primary.Binding().Primary, stale.expected, []string{stale.candidate})
	state.StaleProbe = stale.probe(t, fixture, suffix)
	state.RequireStaleProbe = true
	if err := assertUniversalInvariants(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func scenarioTopLevelTests(t *testing.T) []string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("campaign source location is unavailable")
	}
	directory := filepath.Dir(source)
	matches, err := filepath.Glob(filepath.Join(directory, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0)
	files := token.NewFileSet()
	for _, match := range matches {
		parsed, err := parser.ParseFile(files, match, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", match, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name == "TestMain" || !strings.HasPrefix(function.Name.Name, "Test") {
				continue
			}
			names = append(names, function.Name.Name)
		}
	}
	slices.Sort(names)
	return names
}

func readCampaignFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
