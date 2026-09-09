package testcampaign

import (
	"errors"
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
	"time"
)

func TestVerifyAcceptsExactTopLevelCensusAndIgnoresOwnedSubtests(t *testing.T) {
	report, err := Verify(strings.NewReader(strings.Join([]string{
		`{"Action":"run","Package":"example.test/pkg","Test":"TestAlpha"}`,
		`{"Action":"run","Package":"example.test/pkg","Test":"TestAlpha/child"}`,
		`{"Action":"pass","Package":"example.test/pkg","Test":"TestAlpha/child"}`,
		`{"Action":"pass","Package":"example.test/pkg","Test":"TestAlpha"}`,
		`{"Action":"run","Package":"example.test/pkg","Test":"TestBeta"}`,
		`{"Action":"pass","Package":"example.test/pkg","Test":"TestBeta"}`,
		`{"Action":"pass","Package":"example.test/pkg"}`,
	}, "\n")), verifyFixture([]string{"TestAlpha", "TestBeta"}, 0))
	if err != nil || report.SchemaVersion != 3 || report.Verdict != "pass" || len(report.StartedTests) != 2 || report.TerminalTests["TestAlpha"] != "pass" || report.TerminalTests["TestBeta"] != "pass" || report.ExpectedCount != 2 || report.StartedCount != 2 || report.TerminalCount != 2 || report.PassedCount != 2 || report.FailedCount != 0 || report.SkippedCount != 0 || report.TimeoutMS != 120_000 || report.TimedOut {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestVerifySerializesTimeoutAndDeadlineOutcomeDistinctly(t *testing.T) {
	request := verifyFixture([]string{"TestAlpha"}, 1)
	request.TimedOut = true
	report, err := Verify(strings.NewReader(events("run")), request)
	if err == nil || report.TimeoutMS != 120_000 || !report.TimedOut || report.ProcessExit != 1 || !strings.Contains(err.Error(), "exceeded configured timeout") {
		t.Fatalf("report=%+v err=%v", report, err)
	}

	request = verifyFixture([]string{"TestAlpha"}, 0)
	request.Timeout = 0
	report, err = Verify(strings.NewReader(events("run", "pass")), request)
	if err == nil || report.TimeoutMS != 0 || report.TimedOut || !strings.Contains(err.Error(), "configured timeout must be positive") {
		t.Fatalf("missing timeout report=%+v err=%v", report, err)
	}
}

func TestVerifyRejectsRequiredSkipAndSerializesDistinctCounts(t *testing.T) {
	report, err := Verify(strings.NewReader(strings.Join([]string{
		`{"Action":"run","Package":"example.test/pkg","Test":"TestAlpha"}`,
		`{"Action":"skip","Package":"example.test/pkg","Test":"TestAlpha"}`,
		`{"Action":"pass","Package":"example.test/pkg"}`,
	}, "\n")), verifyFixture([]string{"TestAlpha"}, 0))
	if err == nil || report.Verdict != "fail" || report.ExpectedCount != 1 || report.StartedCount != 1 || report.TerminalCount != 1 || report.PassedCount != 0 || report.FailedCount != 0 || report.SkippedCount != 1 || !strings.Contains(err.Error(), "required tests skipped") {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestVerifyRejectsMalformedDuplicateMissingUnexpectedAndZeroMatchTranscripts(t *testing.T) {
	tests := []struct {
		name       string
		transcript string
		expected   []string
		want       string
	}{
		{name: "zero expected", transcript: `{"Action":"pass","Package":"example.test/pkg"}`, want: "expected test census is empty"},
		{name: "malformed", transcript: `{definitely-not-json}`, expected: []string{"TestAlpha"}, want: "malformed go test JSON"},
		{name: "duplicate start", transcript: events("run", "run", "pass"), expected: []string{"TestAlpha"}, want: "duplicate start event"},
		{name: "missing terminal", transcript: events("run"), expected: []string{"TestAlpha"}, want: "missing terminal event"},
		{name: "terminal without start", transcript: events("pass"), expected: []string{"TestAlpha"}, want: "terminal event without start"},
		{name: "unexpected", transcript: strings.ReplaceAll(events("run", "pass"), "TestAlpha", "TestOther"), expected: []string{"TestAlpha"}, want: "unexpected top-level test event"},
		{name: "wrong package", transcript: strings.ReplaceAll(events("run", "pass"), "example.test/pkg", "other.test/pkg"), expected: []string{"TestAlpha"}, want: "event package mismatch"},
		{name: "mixed package", transcript: events("run") + "\n" + strings.ReplaceAll(events("pass"), "example.test/pkg", "other.test/pkg"), expected: []string{"TestAlpha"}, want: "event package mismatch"},
		{name: "foreign package output", transcript: events("run", "pass") + "\n" + `{"Action":"output","Package":"other.test/pkg","Output":"foreign"}`, expected: []string{"TestAlpha"}, want: "event package mismatch"},
		{name: "empty package output", transcript: events("run", "pass") + "\n" + `{"Action":"output","Output":"unbound"}`, expected: []string{"TestAlpha"}, want: "event package mismatch"},
		{name: "empty requested package", transcript: events("run", "pass"), expected: []string{"TestAlpha"}, want: "requested package is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := verifyFixture(test.expected, 0)
			if test.name == "empty requested package" {
				request.Package = ""
			}
			report, err := Verify(strings.NewReader(test.transcript), request)
			if err == nil || report.Verdict != "fail" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("report=%+v err=%v want=%q", report, err, test.want)
			}
		})
	}
}

func TestVerifyRejectsProcessAndJSONVerdictDisagreement(t *testing.T) {
	tests := []struct {
		name       string
		transcript string
		exit       int
		want       string
	}{
		{name: "zero with failure", transcript: events("run", "fail"), exit: 0, want: "process exited zero"},
		{name: "nonzero without failure", transcript: events("run", "pass"), exit: 1, want: "without a failing JSON terminal"},
		{name: "matching failure still fails gate", transcript: events("run", "fail") + "\n" + `{"Action":"fail","Package":"example.test/pkg"}`, exit: 1, want: "go test process exited 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Verify(strings.NewReader(test.transcript), verifyFixture([]string{"TestAlpha"}, test.exit))
			if err == nil || report.Verdict != "fail" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("report=%+v err=%v want=%q", report, err, test.want)
			}
		})
	}
}

func TestCampaignManifestsAreValidAndDefensiveCopies(t *testing.T) {
	for _, name := range []string{"acceptance", "architecture", "scope", "fault", "soak", "full-scenario", "portable-scenario", "provider-portable", "provider-adversarial", "provider-real"} {
		profile, err := ProfileByName(name)
		if err != nil {
			t.Fatalf("profile %s: %v", name, err)
		}
		profile.ExpectedTests[0] = "mutated"
		again, err := ProfileByName(name)
		if err != nil || again.ExpectedTests[0] == "mutated" {
			t.Fatalf("profile %s leaked caller mutation: %+v err=%v", name, again, err)
		}
	}
	for _, name := range []string{"quality", "full_campaign", "provider_bounded", "provider_real"} {
		campaign, err := CampaignByName(name)
		if err != nil {
			t.Fatalf("campaign %s: %v", name, err)
		}
		if len(campaign.RequiredTests) > 0 {
			campaign.RequiredTests[0] = "mutated"
			again, err := CampaignByName(name)
			if err != nil || again.RequiredTests[0] == "mutated" {
				t.Fatalf("campaign %s leaked required-test mutation: %+v err=%v", name, again, err)
			}
		}
		if len(campaign.RequiredProfiles) > 0 {
			campaign.RequiredProfiles[0] = "mutated"
			again, err := CampaignByName(name)
			if err != nil || again.RequiredProfiles[0] == "mutated" {
				t.Fatalf("campaign %s leaked required-profile mutation: %+v err=%v", name, again, err)
			}
		}
	}
	quality, err := CampaignByName("quality")
	if err != nil || !slices.Equal(quality.Profiles, []string{"acceptance", "architecture", "scope"}) {
		t.Fatalf("quality execution graph=%+v err=%v", quality, err)
	}
	architecture, err := ProfileByName("architecture")
	if err != nil || !slices.Contains(architecture.ExpectedTests, "TestDefaultPolicyClassifiesCampaignToolsWithoutExposingThemToAuthority") || !slices.Contains(architecture.ExpectedTests, "TestRemovingEachRegisteredTargetsOnlyProductionCallerReddensFixture") {
		t.Fatalf("architecture mutant census=%+v err=%v", architecture, err)
	}
	for _, name := range []string{
		"TestDefaultPolicyClassifiesM1CommandAndBoundsExternalGapDBDirection",
		"TestM1CrownSourceBoundaries",
		"TestM1CrownSourceMutantControls",
		"TestHubnotDocumentationDeliveryClaims",
		"TestHubnotDocumentationLinksMermaidAndCommands",
		"TestPhase2EvidenceDocumentationMutants",
		"TestPhase2EvidenceIntegrity",
	} {
		if !slices.Contains(architecture.ExpectedTests, name) {
			t.Errorf("architecture census omits WP11 control %s", name)
		}
	}
}

func TestProviderAdversarialParallelismIsExplicitAndBounded(t *testing.T) {
	profile, err := ProfileByName("provider-adversarial")
	if err != nil || profile.Parallel != 8 {
		t.Fatalf("provider adversarial parallelism=%d err=%v", profile.Parallel, err)
	}
	profile.Parallel = 33
	if err := validateProfile(profile); err == nil || !strings.Contains(err.Error(), "parallelism") {
		t.Fatalf("unbounded profile parallelism was accepted: %v", err)
	}
}

func TestProviderProfilesMatchAcceptedTopLevelTests(t *testing.T) {
	root := campaignRepositoryRoot(t)
	adversarial, err := ProfileByName("provider-adversarial")
	if err != nil {
		t.Fatal(err)
	}
	adversarialSelector := regexp.MustCompile(adversarial.Pattern)
	tests := []struct {
		profile   string
		directory string
		relevant  func(string) bool
	}{
		{"provider-portable", filepath.Join(root, "internal", "tracker", "conformance"), func(name string) bool { return strings.HasPrefix(name, "Test") }},
		{"provider-adversarial", filepath.Join(root, "internal", "tracker", "hubnot"), adversarialSelector.MatchString},
		{"provider-real", filepath.Join(root, "internal", "testcampaign", "cmd", "hubnot-qualification"), func(name string) bool {
			return name == HubnotHostedTest
		}},
	}
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			profile, err := ProfileByName(test.profile)
			if err != nil {
				t.Fatal(err)
			}
			discovered := discoverTopLevelTests(t, test.directory, test.relevant)
			if err := compareExactCensus(profile.ExpectedTests, discovered); err != nil {
				t.Fatalf("accepted top-level census drifted: %v", err)
			}
			if !strings.HasPrefix(profile.Pattern, "^(") || !strings.HasSuffix(profile.Pattern, ")$") {
				t.Fatalf("profile selector is not exactly anchored: %s", profile.Pattern)
			}
			selector := regexp.MustCompile(profile.Pattern)
			all := discoverTopLevelTests(t, test.directory, func(name string) bool { return strings.HasPrefix(name, "Test") })
			selected := slices.DeleteFunc(append([]string(nil), all...), func(name string) bool { return !selector.MatchString(name) })
			if !slices.Equal(selected, profile.ExpectedTests) {
				t.Fatalf("profile selector is not exact\nexpected=%v\nselected=%v", profile.ExpectedTests, selected)
			}
		})
	}
}

func TestCurrentMissionProfilesResolveToExactTopLevelTests(t *testing.T) {
	root := campaignRepositoryRoot(t)
	profiles := []struct {
		name      string
		directory string
	}{
		{"acceptance", filepath.Join(root, "internal", "scenario")},
		{"architecture", filepath.Join(root, "internal", "architecture")},
		{"fault", filepath.Join(root, "internal", "controlstore", "gapdb")},
		{MissionAcceptanceProfile, filepath.Join(root, "internal", "scenario")},
		{MissionAdversarialProfile, filepath.Join(root, "internal", "architecture")},
		{MissionContractProfile, filepath.Join(root, "internal", "scenario")},
		{MissionRealIdentityProfile, filepath.Join(root, "internal", "testcampaign", "cmd", "hubnot-qualification")},
		{"portable-scenario", filepath.Join(root, "internal", "scenario")},
		{"scope", filepath.Join(root, "internal", "scenario")},
		{"soak", filepath.Join(root, "internal", "controlstore", "gapdb")},
	}
	for _, candidate := range profiles {
		t.Run(candidate.name, func(t *testing.T) {
			profile, err := ProfileByName(candidate.name)
			if err != nil {
				t.Fatal(err)
			}
			selector := regexp.MustCompile(profile.Pattern)
			discovered := discoverTopLevelTests(t, candidate.directory, selector.MatchString)
			if err = compareExactCensus(profile.ExpectedTests, discovered); err != nil {
				t.Fatalf("live profile census drifted: %v", err)
			}
		})
	}
}

func TestProviderCampaignMutantsNameMissingControl(t *testing.T) {
	bounded, err := CampaignByName("provider_bounded")
	if err != nil {
		t.Fatal(err)
	}
	mutants := []struct {
		name   string
		want   string
		mutate func(*Campaign)
	}{
		{"portable profile", "provider-portable", func(c *Campaign) { c.Profiles = c.Profiles[1:] }},
		{"universal", "stale probing", func(c *Campaign) { c.RequireStaleProbe = false }},
		{"universal test", "TestDeletedUniversal", func(c *Campaign) { c.UniversalTest = "TestDeletedUniversal" }},
		{"human preservation", "TestZZDeletedHumanPreservation", func(c *Campaign) { c.RequiredTests[0] = "TestZZDeletedHumanPreservation"; slices.Sort(c.RequiredTests) }},
	}
	for _, test := range mutants {
		t.Run(test.name, func(t *testing.T) {
			mutant := bounded
			mutant.Profiles = append([]string(nil), bounded.Profiles...)
			mutant.RequiredTests = append([]string(nil), bounded.RequiredTests...)
			test.mutate(&mutant)
			if err := ValidateCampaign(mutant); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mutant error=%v want=%q", err, test.want)
			}
		})
	}

	original := profiles["provider-adversarial"]
	mutantProfile := original
	mutantProfile.ExpectedTests = slices.DeleteFunc(append([]string(nil), original.ExpectedTests...), func(name string) bool {
		return name == "TestAdapterOutcomeAccounting"
	})
	mutantProfile.Pattern = exactProfilePattern(mutantProfile.ExpectedTests)
	profiles["provider-adversarial"] = mutantProfile
	t.Cleanup(func() { profiles["provider-adversarial"] = original })
	if err := ValidateCampaign(bounded); err == nil || !strings.Contains(err.Error(), "TestAdapterOutcomeAccounting") {
		t.Fatalf("missing expected-test mutant did not name the control: %v", err)
	}

	architecture := profiles["architecture"]
	architectureMutant := architecture
	architectureMutant.ExpectedTests = slices.DeleteFunc(append([]string(nil), architecture.ExpectedTests...), func(name string) bool {
		return name == "TestDefaultPolicyPinsTrackerConformanceAsOneWayQualityTooling"
	})
	if err := compareExactCensus(architectureMutant.ExpectedTests, architecture.ExpectedTests); err == nil || !strings.Contains(err.Error(), "TestDefaultPolicyPinsTrackerConformanceAsOneWayQualityTooling") {
		t.Fatalf("accepted provider architecture removal mutant error=%v", err)
	}
	widened := architecture
	widened.Pattern = "^Test"
	if err := validateProfile(widened); err == nil || !strings.Contains(err.Error(), "exact expected-test census") {
		t.Fatalf("widened live selector mutant survived: %v", err)
	}

	for _, name := range []string{
		"TestDefaultPolicyClassifiesM1CommandAndBoundsExternalGapDBDirection",
		"TestM1CrownSourceBoundaries",
		"TestM1CrownSourceMutantControls",
		"TestHubnotDocumentationDeliveryClaims",
		"TestHubnotDocumentationLinksMermaidAndCommands",
		"TestPhase2EvidenceDocumentationMutants",
		"TestPhase2EvidenceIntegrity",
	} {
		t.Run("architecture removal "+name, func(t *testing.T) {
			mutant := architecture
			mutant.ExpectedTests = slices.DeleteFunc(append([]string(nil), architecture.ExpectedTests...), func(candidate string) bool {
				return candidate == name
			})
			if err := compareExactCensus(mutant.ExpectedTests, architecture.ExpectedTests); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("accepted WP11 architecture removal mutant error=%v", err)
			}
		})
	}
}

func compareExactCensus(expected, discovered []string) error {
	for _, name := range discovered {
		if !slices.Contains(expected, name) {
			return fmt.Errorf("manifest omits accepted top-level test %s", name)
		}
	}
	for _, name := range expected {
		if !slices.Contains(discovered, name) {
			return fmt.Errorf("manifest names undiscovered top-level test %s", name)
		}
	}
	return nil
}

func discoverTopLevelTests(t *testing.T, directory string, relevant func(string) bool) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	names := make([]string, 0)
	for _, match := range matches {
		parsed, err := parser.ParseFile(set, match, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", match, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name == "TestMain" || !relevant(function.Name.Name) {
				continue
			}
			names = append(names, function.Name.Name)
		}
	}
	slices.Sort(names)
	return names
}

func campaignRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path is unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func TestMakeTargetsInvokeAuthoritativeCampaignsAndMutantsKillRealBindings(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path is unavailable")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(content)
	for target, campaign := range map[string]string{"quality-bounded": "quality", "full-campaign": "full_campaign", "hubnot-contract": "provider_bounded"} {
		if err := validateMakeCampaignBinding(makefile, target, campaign); err != nil {
			t.Fatal(err)
		}
		mutant := strings.Replace(makefile, "-campaign "+campaign, "-profile acceptance", 1)
		if err := validateMakeCampaignBinding(mutant, target, campaign); err == nil {
			t.Fatalf("real Make binding mutant survived for %s", target)
		}
	}
	if err := validateProviderMakeControls(makefile); err != nil {
		t.Fatal(err)
	}
	mutants := []struct {
		name string
		old  string
		new  string
	}{
		{"one profile", "-campaign provider_bounded", "-profile provider-portable"},
		{"raised PR budget", `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m`, `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 9m`},
		{"removed PR budget", `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m`, `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)"`},
		{"explicit compiler", `"$(HUBNOT_GO)" build $(HUBNOT_BUILD_FLAGS)`, `go build $(HUBNOT_BUILD_FLAGS)`},
		{"compiled installer", `install --go-bin "$(HUBNOT_GO)"`, `install --go-bin go`},
		{"installer diagnostics", `2> "$(ARTIFACT_DIR)/hubnot-install.stderr.log"`, `2> /dev/null`},
		{"full local", "-profile provider-full-local", "-profile provider-real"},
		{"activation", "GO_KITTY_HUBNOT_RUN=1", "GO_KITTY_HUBNOT_RUN=0"},
		{"quality delegated campaigns", "quality: hubnot-contract m2-short quality-core", "quality: m2-short quality-core"},
		{"quality delegated campaigns", "quality: hubnot-contract m2-short quality-core", "quality: hubnot-contract quality-core"},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			changed := strings.Replace(makefile, mutant.old, mutant.new, 1)
			if changed == makefile {
				t.Fatal("mutant did not alter Makefile fixture")
			}
			want := mutant.name
			for _, prefix := range []string{"missing ", "removed ", "raised "} {
				want = strings.TrimPrefix(want, prefix)
			}
			if err := validateProviderMakeControls(changed); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("mutant error=%v want control=%q", err, want)
			}
		})
	}
}

func TestHubnotQualificationWorkflowControlsAndMutants(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(campaignRepositoryRoot(t), ".github", "workflows", "hubnot-qualification.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	if err := validateHubnotWorkflow(workflow); err != nil {
		t.Fatal(err)
	}
	for _, required := range hubnotWorkflowControls {
		t.Run(required.name, func(t *testing.T) {
			changed := strings.Replace(workflow, required.text, "REMOVED", 1)
			if changed == workflow {
				t.Fatal("ineffective mutant")
			}
			if err := validateHubnotWorkflow(changed); err == nil {
				t.Fatal("required workflow control omitted")
			}
		})
	}
	if err := validateHubnotWorkflow(strings.Replace(workflow, "actions/checkout@11d5960a326750d5838078e36cf38b85af677262", "actions/checkout@v4", 1)); err == nil {
		t.Fatal("mutable action admitted")
	}
}

var hubnotWorkflowControls = []struct{ name, text string }{
	{"permissions", "permissions:\n  contents: read"}, {"credentials", "persist-credentials: false"},
	{"toolchain", "go-version: 1.26.7"}, {"bounded campaign", "make hubnot-contract"},
	{"real trigger", "if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"},
	{"deadline", "timeout-minutes: 75"}, {"external build", "make hubnot-build"},
	{"immutable tuple", `source_commit=$(jq -er .provider.source_commit`},
	{"accepted source", `fetch --depth=1 https://github.com/LynnColeArt/hubnot.git "$source_commit"`},
	{"compiled install", "make hubnot-install hubnot-product"}, {"selected scope", "make hubnot-hosted-provider"},
	{"setup evidence", "go-kitty.hubnot-setup/1"}, {"non-full", "hubnot-hosted-provider-non-full-"},
	{"offline modules", `GOPROXY: "off"`}, {"offline sums", `GOSUMDB: "off"`},
	{"failure upload", "if: always()\n        uses: actions/upload-artifact"}, {"retention", "retention-days: 14"},
}

func validateHubnotWorkflow(workflow string) error {
	for _, c := range hubnotWorkflowControls {
		want := 1
		if slices.Contains([]string{"credentials", "toolchain", "setup evidence", "offline modules", "offline sums", "failure upload", "retention"}, c.name) {
			want = 2
		}
		if strings.Count(workflow, c.text) != want {
			return fmt.Errorf("%s control missing", c.name)
		}
	}
	for _, bad := range []string{"beads-qualification", "install-qualified-hubnot.sh", "sudo sysctl", "continue-on-error: true", "make hubnot-qualification"} {
		if strings.Contains(workflow, bad) {
			return fmt.Errorf("unsupported hosted workflow %s", bad)
		}
	}
	pinned := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*[^@\s]+@[0-9a-f]{40}(?:\s+#.*)?$`)
	for _, l := range strings.Split(workflow, "\n") {
		if strings.Contains(l, "uses:") && !pinned.MatchString(l) {
			return errors.New("unpinned action")
		}
	}
	if strings.Index(workflow, "make hubnot-build") > strings.Index(workflow, "make hubnot-install hubnot-product") || strings.Index(workflow, "make hubnot-install hubnot-product") > strings.Index(workflow, "make hubnot-hosted-provider") {
		return errors.New("installer order invalid")
	}
	return nil
}
func validateProviderMakeControls(makefile string) error {
	for _, c := range []struct{ control, text string }{
		{"one profile", "-campaign provider_bounded"},
		{"raised PR budget", `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m`},
		{"quality delegated campaigns", "quality: hubnot-contract m2-short quality-core"},
		{"explicit compiler qualifier", `"$(HUBNOT_GO)" build $(HUBNOT_BUILD_FLAGS) -o "$(HUBNOT_QUALIFIER)" ./internal/testcampaign/cmd/hubnot-qualification`},
		{"explicit compiler product", `"$(HUBNOT_GO)" build $(HUBNOT_BUILD_FLAGS) -o "$(HUBNOT_PRODUCT)" ./cmd/spec-kitty`},
		{"compiled installer", `"$(HUBNOT_QUALIFIER)" install --go-bin "$(HUBNOT_GO)" --source "$(HUBNOT_SOURCE)" --destination "$(HUBNOT_BINARY)"`},
		{"installer diagnostics", `2> "$(ARTIFACT_DIR)/hubnot-install.stderr.log"`},
		{"full local", "-profile provider-full-local"}, {"selected scope", "-campaign provider_real"},
		{"activation", "GO_KITTY_HUBNOT_RUN=1"}, {"original recovery", "GO_KITTY_HUBNOT_ORIGINAL_REPOSITORY"},
		{"offline Go modules", "GOPROXY=off GOSUMDB=off"}, {"offline activation", "-u GO_KITTY_HUBNOT_RUN"},
	} {
		if !strings.Contains(makefile, c.text) {
			return fmt.Errorf("%s control missing", c.control)
		}
	}
	return nil
}

func validateMakeCampaignBinding(makefile, target, campaign string) error {
	lines := strings.Split(makefile, "\n")
	header := target + ":"
	for index, line := range lines {
		if !strings.HasPrefix(line, header) {
			continue
		}
		for next := index + 1; next < len(lines) && strings.HasPrefix(lines[next], "\t"); next++ {
			if strings.Contains(lines[next], "-campaign "+campaign) {
				return nil
			}
		}
		return fmt.Errorf("Make target %s does not invoke authoritative campaign %s", target, campaign)
	}
	return fmt.Errorf("Make target %s is absent", target)
}

func events(actions ...string) string {
	lines := make([]string, len(actions))
	for index, action := range actions {
		lines[index] = `{"Action":"` + action + `","Package":"example.test/pkg","Test":"TestAlpha"}`
	}
	return strings.Join(lines, "\n")
}

func verifyFixture(expected []string, exit int) VerifyRequest {
	started := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	return VerifyRequest{Campaign: "fixture", Package: "example.test/pkg", Expected: expected, ProcessExit: exit, StartedAt: started, FinishedAt: started.Add(time.Second), Timeout: 2 * time.Minute}
}
