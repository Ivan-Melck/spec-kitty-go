package m2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type GoTestReceipt struct {
	SchemaVersion        int      `json:"schema_version"`
	RunID                string   `json:"run_id"`
	SourceCommit         string   `json:"source_commit"`
	SourceTree           string   `json:"source_tree"`
	DefinitionID         string   `json:"definition_id"`
	Package              string   `json:"package"`
	Selector             string   `json:"selector"`
	ExpectedExecutions   int      `json:"expected_executions"`
	StartedExecutions    int      `json:"started_executions"`
	PassedExecutions     int      `json:"passed_executions"`
	FailedExecutions     int      `json:"failed_executions"`
	SkippedExecutions    int      `json:"skipped_executions"`
	UnexpectedTests      []string `json:"unexpected_tests"`
	ChildStarted         int      `json:"child_started"`
	ChildPassed          int      `json:"child_passed"`
	ProcessExit          int      `json:"process_exit"`
	RawTranscriptSHA256  string   `json:"raw_transcript_sha256"`
	CertifiedRepetitions int      `json:"certified_repetitions"`
	CertificationMode    string   `json:"certification_mode"`
	Verdict              string   `json:"verdict"`
}

type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

func VerifyGoTestReceipt(raw []byte, runID string, source Source, definition Definition, executions, exit int) (GoTestReceipt, error) {
	receipt := GoTestReceipt{SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree, DefinitionID: definition.ID, Package: definition.Package, Selector: definition.Selector, ExpectedExecutions: executions, ProcessExit: exit, RawTranscriptSHA256: SHA256(raw), CertificationMode: "direct-process-repetitions", Verdict: "fail"}
	if definition.ExpectedRepetitions <= 0 || definition.TrialsPerRun <= 0 || definition.ExpectedRepetitions%definition.TrialsPerRun != 0 {
		return receipt, fmt.Errorf("frozen trial accounting is invalid")
	}
	expectedProcesses := definition.ExpectedRepetitions / definition.TrialsPerRun
	if executions != expectedProcesses {
		return receipt, fmt.Errorf("executed processes=%d want frozen=%d", executions, expectedProcesses)
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	unexpected := map[string]struct{}{}
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return receipt, fmt.Errorf("malformed go test JSON: %w", err)
		}
		if event.Test == "" {
			continue
		}
		if event.Package != strings.TrimPrefix(definition.Package, "./") && !strings.HasSuffix(event.Package, strings.TrimPrefix(definition.Package, ".")) {
			return receipt, fmt.Errorf("foreign package event %q", event.Package)
		}
		if strings.Contains(event.Test, "/") {
			if !strings.HasPrefix(event.Test, definition.Selector+"/") {
				unexpected[event.Test] = struct{}{}
				continue
			}
			switch event.Action {
			case "run":
				receipt.ChildStarted++
			case "pass":
				receipt.ChildPassed++
			case "fail":
				receipt.FailedExecutions++
			case "skip":
				receipt.SkippedExecutions++
			}
			continue
		}
		if event.Test != definition.Selector {
			if event.Action == "run" || event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
				unexpected[event.Test] = struct{}{}
			}
			continue
		}
		switch event.Action {
		case "run":
			receipt.StartedExecutions++
		case "pass":
			receipt.PassedExecutions++
		case "fail":
			receipt.FailedExecutions++
		case "skip":
			receipt.SkippedExecutions++
		}
	}
	if err := scanner.Err(); err != nil {
		return receipt, err
	}
	for name := range unexpected {
		receipt.UnexpectedTests = append(receipt.UnexpectedTests, name)
	}
	sort.Strings(receipt.UnexpectedTests)
	if exit != 0 || receipt.StartedExecutions != executions || receipt.PassedExecutions != executions || receipt.FailedExecutions != 0 || receipt.SkippedExecutions != 0 || len(receipt.UnexpectedTests) != 0 || receipt.ChildStarted != receipt.ChildPassed {
		return receipt, fmt.Errorf("test receipt is incomplete: start=%d pass=%d fail=%d skip=%d child=%d/%d exit=%d unexpected=%v", receipt.StartedExecutions, receipt.PassedExecutions, receipt.FailedExecutions, receipt.SkippedExecutions, receipt.ChildStarted, receipt.ChildPassed, exit, receipt.UnexpectedTests)
	}
	receipt.CertifiedRepetitions = receipt.PassedExecutions * definition.TrialsPerRun
	receipt.Verdict = "pass"
	return receipt, nil
}
