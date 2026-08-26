package testcampaign

import (
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Verify(strings.NewReader(test.transcript), verifyFixture(test.expected, 0))
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
	for _, name := range []string{"acceptance", "scope", "fault", "soak", "full-scenario"} {
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
