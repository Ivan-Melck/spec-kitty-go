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
		"TestBeadsDocumentationDeliveryClaims",
		"TestBeadsDocumentationLinksMermaidAndCommands",
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
		{"provider-adversarial", filepath.Join(root, "internal", "tracker", "beads"), adversarialSelector.MatchString},
		{"provider-real", filepath.Join(root, "internal", "tracker", "beads"), func(name string) bool {
			return strings.HasPrefix(name, "TestRealBeads") && name != "TestRealBeadsProcessCreate"
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
		{MissionRealIdentityProfile, filepath.Join(root, "internal", "tracker", "beads")},
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
		{"human preservation", "TestDeletedHumanPreservation", func(c *Campaign) { c.RequiredTests[0] = "TestDeletedHumanPreservation" }},
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
		return name == "TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces"
	})
	mutantProfile.Pattern = exactProfilePattern(mutantProfile.ExpectedTests)
	profiles["provider-adversarial"] = mutantProfile
	t.Cleanup(func() { profiles["provider-adversarial"] = original })
	if err := ValidateCampaign(bounded); err == nil || !strings.Contains(err.Error(), "TestAssignmentHumanChangesSurviveOneHundredIndependentRecoveryRaces") {
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
		"TestBeadsDocumentationDeliveryClaims",
		"TestBeadsDocumentationLinksMermaidAndCommands",
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
	for target, campaign := range map[string]string{"quality-bounded": "quality", "full-campaign": "full_campaign", "beads-contract": "provider_bounded"} {
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
		{"PATH fallback", `case "$(BEADS_BIN)" in /*)`, `command -v bd >/dev/null; case "$(BEADS_BIN)" in *)`},
		{"missing digest comparison", `test "$$observed" = "$(QUALIFIED_BEADS_DIGEST)"`, `test -n "$$observed"`},
		{"missing offline boundary", `test "$(OFFLINE_ENFORCED)" = linux-network-namespace`, `test -n "$(OFFLINE_ENFORCED)"`},
		{"missing namespace proof", `test "$$current_net" != "$$host_net"`, `test -n "$$current_net"`},
		{"missing inherited offline Go modules", `GOENV="$$go_env"`, `GOENV=`},
		{"quality delegated campaigns", "quality: beads-contract m2-short quality-core", "quality: m2-short quality-core"},
		{"quality delegated campaigns", "quality: beads-contract m2-short quality-core", "quality: beads-contract quality-core"},
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

func TestBeadsQualificationWorkflowControlsAndMutants(t *testing.T) {
	path := filepath.Join(campaignRepositoryRoot(t), ".github", "workflows", "beads-qualification.yml")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)
	if err := validateBeadsWorkflow(workflow); err != nil {
		t.Fatal(err)
	}
	mutants := []struct {
		name string
		old  string
		new  string
	}{
		{"unpinned action", "actions/checkout@11d5960a326750d5838078e36cf38b85af677262", "actions/checkout@v4"},
		{"checkout credentials", "persist-credentials: false", "persist-credentials: true"},
		{"missing digest comparison", `test "$(sha256sum -- "$BEADS_BIN" | awk '{print $1}')" = "$QUALIFIED_BEADS_DIGEST"`, `test -x "$BEADS_BIN"`},
		{"missing installer schema", `(keys | length == 9) and .schema_version == 1`, `.schema_version == 1`},
		{"PATH fallback", `case "$beads_bin" in "$RUNNER_TEMP"/*)`, `command -v bd; case "$beads_bin" in *)`},
		{"network-enabled qualification", "unshare --net --mount-proc", "sh"},
		{"unpinned dolt server", "github.com/dolthub/dolt/go/cmd/dolt@v0.40.5-0.20260605230755-1bf533220ab0", "github.com/dolthub/dolt/go/cmd/dolt@latest"},
		{"missing shared server", `"$DOLT_BIN" sql-server --host 127.0.0.1 --port 3307`, `test -x "$DOLT_BIN"`},
		{"missing exact server tuple", "export BEADS_SERVER_ID=dolt-v0.40.5-1bf533220ab0", "export BEADS_SERVER_ID="},
		{"missing server cleanup", "trap cleanup EXIT INT TERM", "true # cleanup omitted"},
		{"omitted artifact on failure", "if: always()\n        uses: actions/upload-artifact", "if: success()\n        uses: actions/upload-artifact"},
		{"accepted skip", `test "$(jq -r .verdict "$evidence")" = pass`, `test "$(jq -r .verdict "$evidence")" != fail`},
		{"raised PR budget", `"budget_ms":600000`, `"budget_ms":700000`},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			changed := strings.Replace(workflow, mutant.old, mutant.new, 1)
			if changed == workflow {
				t.Fatal("mutant did not alter workflow fixture")
			}
			if err := validateBeadsWorkflow(changed); err == nil || !strings.Contains(err.Error(), mutant.name) {
				t.Fatalf("mutant error=%v want=%q", err, mutant.name)
			}
		})
	}
}

func validateBeadsWorkflow(workflow string) error {
	controls := []struct {
		name string
		text string
	}{
		{"permissions", "permissions:\n  contents: read"},
		{"checkout credentials", "persist-credentials: false"},
		{"concurrency cancellation", "cancel-in-progress:"},
		{"bounded trigger", "if: github.event_name == 'pull_request' || github.event_name == 'push'"},
		{"real trigger", "if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"},
		{"bounded timeout", "timeout-minutes: 15"},
		{"real timeout", "timeout-minutes: 45"},
		{"missing digest comparison", `test "$(sha256sum -- "$BEADS_BIN" | awk '{print $1}')" = "$QUALIFIED_BEADS_DIGEST"`},
		{"missing installer schema", `(keys | length == 9) and .schema_version == 1`},
		{"PATH fallback", `case "$beads_bin" in "$RUNNER_TEMP"/*)`},
		{"network-enabled qualification", "unshare --net --mount-proc"},
		{"unpinned dolt server", "github.com/dolthub/dolt/go/cmd/dolt@v0.40.5-0.20260605230755-1bf533220ab0"},
		{"missing shared server", `"$DOLT_BIN" sql-server --host 127.0.0.1 --port 3307`},
		{"missing exact server tuple", "export BEADS_SERVER_ID=dolt-v0.40.5-1bf533220ab0"},
		{"missing server endpoint", "export BEADS_SERVER_ENDPOINT=127.0.0.1:3307"},
		{"missing server host", "export DOLT_SERVER_HOST=127.0.0.1"},
		{"missing server port", "export DOLT_SERVER_PORT=3307"},
		{"missing server cleanup", "trap cleanup EXIT INT TERM"},
		{"missing server termination", `kill "$server_pid" 2>/dev/null || true`},
		{"offline modules", "GOPROXY: \"off\""},
		{"setup evidence", "setup.non-admissible.json"},
		{"isolated runner paths", "$RUNNER_TEMP/beads-provider-"},
		{"pinned installer", "scripts/install-qualified-beads.sh --dest \"$TOOL_DIR\""},
		{"accepted skip", `test "$(jq -r .verdict "$evidence")" = pass`},
		{"raised PR budget", `"budget_ms":600000`},
		{"real budget", `"budget_ms":2700000`},
		{"baseline pending", `HOSTED_BASELINE_STATE: baseline_pending`},
		{"baseline count", `HOSTED_BASELINE_SAMPLE_COUNT: "0"`},
		{"monotonic timing", "time.monotonic_ns()"},
		{"finite retention", "retention-days: 14"},
	}
	for _, control := range controls {
		if !strings.Contains(workflow, control.text) {
			return fmt.Errorf("%s control is missing", control.name)
		}
	}
	install := strings.Index(workflow, `go install "$QUALIFIED_DOLT_MODULE"`)
	offline := strings.Index(workflow, "unshare --net --mount-proc")
	server := strings.Index(workflow, `"$DOLT_BIN" sql-server`)
	qualification := strings.Index(workflow, "make beads-qualification")
	if install < 0 || offline < 0 || server < 0 || qualification < 0 || !(install < offline && offline < server && server < qualification) {
		return errors.New("offline pinned shared-server lifecycle ordering is invalid")
	}
	if strings.Count(workflow, `GOPROXY: "off"`) < 2 || strings.Count(workflow, `GOSUMDB: "off"`) < 2 {
		return errors.New("offline modules control is missing")
	}
	if strings.Count(workflow, "persist-credentials: false") != 2 {
		return errors.New("checkout credentials control is missing")
	}
	if strings.Count(workflow, "if: always()\n        uses: actions/upload-artifact") != 2 {
		return errors.New("omitted artifact on failure control is missing")
	}
	pinnedAction := regexp.MustCompile(`(?m)^\s*uses:\s*[^@\s]+@[0-9a-f]{40}(?:\s+#.*)?$`)
	for _, line := range strings.Split(workflow, "\n") {
		if strings.Contains(line, "uses:") && !pinnedAction.MatchString(line) {
			return fmt.Errorf("unpinned action control rejected %q", line)
		}
	}
	for _, forbidden := range []string{"*.stderr.log", "*.go-test.jsonl", "provider database", "native-backup"} {
		if strings.Contains(workflow, forbidden) {
			return fmt.Errorf("artifact allowlist contains forbidden %q", forbidden)
		}
	}
	return nil
}

func validateProviderMakeControls(makefile string) error {
	required := []struct {
		control string
		text    string
	}{
		{"one profile", "-campaign provider_bounded"},
		{"raised PR budget", `-campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m`},
		{"PATH fallback", `case "$(BEADS_BIN)" in /*)`},
		{"missing digest comparison", `test "$$observed" = "$(QUALIFIED_BEADS_DIGEST)"`},
		{"missing offline boundary", `test "$(OFFLINE_ENFORCED)" = linux-network-namespace`},
		{"missing namespace proof", `test "$$current_net" != "$$host_net"`},
		{"quality delegated campaigns", "quality: beads-contract m2-short quality-core"},
		{"installer tuple", `.source_commit == "6c124203e771433a3550c348771a5b5e27fd3c21"`},
		{"installer schema", `(keys | length == 9) and .schema_version == 1`},
		{"offline Go modules", "GOPROXY=off GOSUMDB=off"},
		{"inherited offline Go modules", `GOENV="$$go_env"`},
		{"failure evidence", "beads-qualification.non-admissible.json"},
	}
	for _, requirement := range required {
		if !strings.Contains(makefile, requirement.text) {
			return fmt.Errorf("%s control is missing", requirement.control)
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
