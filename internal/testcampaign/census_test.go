package testcampaign

import (
	"fmt"
	"os"
	"path/filepath"
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
		`{"Action":"skip","Package":"example.test/pkg","Test":"TestBeta"}`,
		`{"Action":"pass","Package":"example.test/pkg"}`,
	}, "\n")), verifyFixture([]string{"TestAlpha", "TestBeta"}, 0))
	if err != nil || report.Verdict != "pass" || len(report.StartedTests) != 2 || report.TerminalTests["TestAlpha"] != "pass" || report.TerminalTests["TestBeta"] != "skip" {
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
	for _, name := range []string{"acceptance", "architecture", "scope", "fault", "soak", "full-scenario"} {
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
	for _, name := range []string{"quality", "full_campaign"} {
		if _, err := CampaignByName(name); err != nil {
			t.Fatalf("campaign %s: %v", name, err)
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
	for target, campaign := range map[string]string{"quality": "quality", "full-campaign": "full_campaign"} {
		if err := validateMakeCampaignBinding(makefile, target, campaign); err != nil {
			t.Fatal(err)
		}
		mutant := strings.Replace(makefile, "-campaign "+campaign, "-profile acceptance", 1)
		if err := validateMakeCampaignBinding(mutant, target, campaign); err == nil {
			t.Fatalf("real Make binding mutant survived for %s", target)
		}
	}
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
	return VerifyRequest{Campaign: "fixture", Package: "example.test/pkg", Expected: expected, ProcessExit: exit, StartedAt: started, FinishedAt: started.Add(time.Second)}
}
