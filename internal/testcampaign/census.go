package testcampaign

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const ReportSchemaVersion = 1

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

// Report is the machine-readable evidence produced for one curated selector.
type Report struct {
	SchemaVersion int               `json:"schema_version"`
	Campaign      string            `json:"campaign"`
	Package       string            `json:"package"`
	Pattern       string            `json:"pattern,omitempty"`
	StartedAt     time.Time         `json:"started_at"`
	FinishedAt    time.Time         `json:"finished_at"`
	DurationMS    int64             `json:"duration_ms"`
	ExpectedTests []string          `json:"expected_tests"`
	StartedTests  []string          `json:"started_tests"`
	TerminalTests map[string]string `json:"terminal_tests"`
	ProcessExit   int               `json:"process_exit"`
	Verdict       string            `json:"verdict"`
	Issues        []string          `json:"issues,omitempty"`
}

type VerifyRequest struct {
	Campaign    string
	Package     string
	Pattern     string
	Expected    []string
	ProcessExit int
	StartedAt   time.Time
	FinishedAt  time.Time
}

// Verify parses only go test's JSON event protocol. It deliberately ignores
// human output payloads and counts exact top-level tests; child events belong
// to the selected parent's terminal result.
func Verify(reader io.Reader, request VerifyRequest) (Report, error) {
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Campaign:      request.Campaign, Package: request.Package, Pattern: request.Pattern,
		StartedAt: request.StartedAt.UTC(), FinishedAt: request.FinishedAt.UTC(),
		ExpectedTests: append([]string(nil), request.Expected...), ProcessExit: request.ProcessExit,
		TerminalTests: map[string]string{}, Verdict: "fail",
	}
	if !request.FinishedAt.IsZero() && !request.StartedAt.IsZero() {
		report.DurationMS = request.FinishedAt.Sub(request.StartedAt).Milliseconds()
	}
	issues := make([]string, 0)
	if len(request.Expected) == 0 {
		issues = append(issues, "expected test census is empty")
	}
	expected := make(map[string]struct{}, len(request.Expected))
	for _, name := range request.Expected {
		if name == "" || strings.Contains(name, "/") {
			issues = append(issues, fmt.Sprintf("invalid expected top-level test %q", name))
			continue
		}
		if _, duplicate := expected[name]; duplicate {
			issues = append(issues, "duplicate expected test "+name)
		}
		expected[name] = struct{}{}
	}

	started := map[string]bool{}
	packageFailed := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			issues = append(issues, fmt.Sprintf("malformed go test JSON at line %d: %v", line, err))
			continue
		}
		if event.Test == "" {
			packageFailed = packageFailed || event.Action == "fail"
			continue
		}
		if strings.Contains(event.Test, "/") {
			continue
		}
		if event.Action != "run" && event.Action != "pass" && event.Action != "fail" && event.Action != "skip" {
			continue
		}
		if _, ok := expected[event.Test]; !ok {
			issues = append(issues, "unexpected top-level test event "+event.Test)
			continue
		}
		switch event.Action {
		case "run":
			if started[event.Test] {
				issues = append(issues, "duplicate start event for "+event.Test)
			}
			started[event.Test] = true
		case "pass", "fail", "skip":
			if !started[event.Test] {
				issues = append(issues, "terminal event without start for "+event.Test)
			}
			if prior := report.TerminalTests[event.Test]; prior != "" {
				issues = append(issues, fmt.Sprintf("duplicate terminal event for %s: %s then %s", event.Test, prior, event.Action))
			}
			report.TerminalTests[event.Test] = event.Action
		}
	}
	if err := scanner.Err(); err != nil {
		issues = append(issues, "read go test transcript: "+err.Error())
	}
	for name := range started {
		report.StartedTests = append(report.StartedTests, name)
	}
	slices.Sort(report.StartedTests)
	for _, name := range request.Expected {
		if !started[name] {
			issues = append(issues, "missing start event for "+name)
		}
		if report.TerminalTests[name] == "" {
			issues = append(issues, "missing terminal event for "+name)
		}
	}
	failedTest := false
	for _, terminal := range report.TerminalTests {
		failedTest = failedTest || terminal == "fail"
	}
	if request.ProcessExit == 0 && (failedTest || packageFailed) {
		issues = append(issues, "process exited zero despite a failing JSON terminal")
	}
	if request.ProcessExit != 0 {
		if !failedTest && !packageFailed {
			issues = append(issues, "process exited nonzero without a failing JSON terminal")
		}
		issues = append(issues, fmt.Sprintf("go test process exited %d", request.ProcessExit))
	}
	if failedTest {
		issues = append(issues, "one or more expected tests failed")
	}
	report.Issues = uniqueStrings(issues)
	if len(report.Issues) == 0 {
		report.Verdict = "pass"
		return report, nil
	}
	return report, fmt.Errorf("campaign census failed: %s", strings.Join(report.Issues, "; "))
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
