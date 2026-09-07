package m2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

type GoTestReceipt struct {
	SchemaVersion        int                                `json:"schema_version"`
	RunID                string                             `json:"run_id"`
	SourceCommit         string                             `json:"source_commit"`
	SourceTree           string                             `json:"source_tree"`
	DefinitionID         string                             `json:"definition_id"`
	Package              string                             `json:"package"`
	Selector             string                             `json:"selector"`
	ExpectedExecutions   int                                `json:"expected_executions"`
	StartedExecutions    int                                `json:"started_executions"`
	PassedExecutions     int                                `json:"passed_executions"`
	FailedExecutions     int                                `json:"failed_executions"`
	SkippedExecutions    int                                `json:"skipped_executions"`
	UnexpectedTests      []string                           `json:"unexpected_tests"`
	ChildStarted         int                                `json:"child_started"`
	ChildPassed          int                                `json:"child_passed"`
	ProcessExit          int                                `json:"process_exit"`
	RawTranscriptSHA256  string                             `json:"raw_transcript_sha256"`
	CertifiedRepetitions int                                `json:"certified_repetitions"`
	CertificationMode    string                             `json:"certification_mode"`
	Iterations           []iterationreceipt.Record          `json:"iterations,omitempty"`
	FamilyCutIterations  []iterationreceipt.FamilyCutRecord `json:"family_cut_iterations,omitempty"`
	Verdict              string                             `json:"verdict"`
}

type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type iterationContract struct {
	boundary string
	outcomes map[string]struct{}
}

var iterationContracts = map[string]iterationContract{
	"behavior/zombie-workspace-isolation": {"runtime-epoch-replacement", stringSet("stale-refused-authority-unchanged")},
	"behavior/serialized-integration":     {"integration-target-authority", stringSet("target-authority-completed", "target-cas-conflict")},
	"fault/concurrent-claims":             {"mission-head-cas", stringSet("one-winner-one-version-conflict")},
	"fault/clean-unclean-restart":         {"restore-parallel-replay", stringSet("terminal-equivalent")},
}

var childContracts = map[string][]string{
	"fault/cut-restart-reconciliation": {
		"TestProductionPublicationRecoveryAcrossOwnerRestartCuts/restart_after_reconciliation",
		"TestProductionPublicationRecoveryAcrossOwnerRestartCuts/restart_before_reconciliation",
	},
}

const (
	// A machine record can carry two independently bounded canonical evidence
	// objects. JSON string escaping can double those bytes; the remaining fixed
	// fields and go-test envelope are bounded by the text contract.
	maxGoTestRecordBytes = 4*iterationreceipt.MaxEvidenceBytes + 16*iterationreceipt.MaxTextBytes + (8 << 10)
	// Each process admits a closed amount of go-test control/output envelopes in
	// addition to the exact iteration receipt census. The aggregate remains
	// capped by the evidence member budget.
	maxGoTestAuxiliaryRecordsPerProcess = 512
)

type goTestReceiptLimits struct {
	recordBytes     int
	transcriptBytes int
}

func admittedGoTestReceiptLimits(definition Definition, executions int) goTestReceiptLimits {
	records := definition.ExpectedRepetitions + executions*maxGoTestAuxiliaryRecordsPerProcess
	if records < 1 {
		records = 1
	}
	total := int64(records) * int64(maxGoTestRecordBytes)
	if total > maximumFile {
		total = maximumFile
	}
	return goTestReceiptLimits{recordBytes: maxGoTestRecordBytes, transcriptBytes: int(total)}
}

func VerifyGoTestReceipt(raw []byte, runID string, source Source, definition Definition, executions, exit int) (GoTestReceipt, error) {
	return verifyGoTestReceiptWithLimits(raw, runID, source, definition, executions, exit, admittedGoTestReceiptLimits(definition, executions))
}

func verifyGoTestReceiptWithLimits(raw []byte, runID string, source Source, definition Definition, executions, exit int, limits goTestReceiptLimits) (GoTestReceipt, error) {
	receipt := GoTestReceipt{SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree, DefinitionID: definition.ID, Package: definition.Package, Selector: definition.Selector, ExpectedExecutions: executions, ProcessExit: exit, RawTranscriptSHA256: SHA256(raw), CertificationMode: "direct-process-repetitions", Verdict: "fail"}
	if limits.recordBytes <= 0 || limits.transcriptBytes <= 0 || len(raw) > limits.transcriptBytes {
		return receipt, fmt.Errorf("go test transcript total budget exceeded: bytes=%d limit=%d", len(raw), limits.transcriptBytes)
	}
	if definition.ExpectedRepetitions <= 0 || definition.TrialsPerRun <= 0 || definition.ExpectedRepetitions%definition.TrialsPerRun != 0 {
		return receipt, fmt.Errorf("frozen trial accounting is invalid")
	}
	expectedProcesses := definition.ExpectedRepetitions / definition.TrialsPerRun
	if executions != expectedProcesses {
		return receipt, fmt.Errorf("executed processes=%d want frozen=%d", executions, expectedProcesses)
	}
	reader := bufio.NewReaderSize(bytes.NewReader(raw), limits.recordBytes+1)
	unexpected := map[string]struct{}{}
	childRuns := map[string]int{}
	childPasses := map[string]int{}
	var failedTests []string
	var processTranscript bytes.Buffer
	for {
		line, readErr := reader.ReadSlice('\n')
		if errors.Is(readErr, bufio.ErrBufferFull) {
			return receipt, fmt.Errorf("go test JSON record budget exceeded: limit=%d", limits.recordBytes)
		}
		if errors.Is(readErr, io.EOF) {
			if len(line) != 0 {
				return receipt, fmt.Errorf("unterminated go test JSON record: bytes=%d", len(line))
			}
			break
		}
		if readErr != nil {
			return receipt, fmt.Errorf("read go test JSON record: %w", readErr)
		}
		if len(line) > limits.recordBytes {
			return receipt, fmt.Errorf("go test JSON record budget exceeded: bytes=%d limit=%d", len(line), limits.recordBytes)
		}
		eventBytes := bytes.TrimSuffix(line, []byte{'\n'})
		var event goTestEvent
		if err := json.Unmarshal(eventBytes, &event); err != nil {
			return receipt, fmt.Errorf("malformed go test JSON: %w", err)
		}
		if event.Action == "fail" && event.Test != "" {
			failedTests = append(failedTests, event.Test)
		}
		if index := strings.Index(event.Output, iterationreceipt.Prefix); index >= 0 {
			_, internal := iterationContracts[definition.ID]
			process := requiresProcessIterationReceipts(definition)
			if process && event.Test != definition.Selector && definition.ID == "fault/adversarial-tracker" {
				_, _ = processTranscript.Write(line)
				continue // nested receipts are owned by the WP14 structured verifier
			}
			if !internal && !process {
				if definition.ID == "fault/adversarial-tracker" && event.Test != definition.Selector {
					continue // verified by the separately sealed WP14 structured receipt
				}
				return receipt, fmt.Errorf("unexpected iteration receipt for %q", definition.ID)
			}
			if event.Test != definition.Selector {
				return receipt, fmt.Errorf("iteration receipt is not bound to exact parent %q", event.Test)
			}
			var iteration iterationreceipt.Record
			payload := strings.TrimSpace(event.Output[index+len(iterationreceipt.Prefix):])
			decoder := json.NewDecoder(strings.NewReader(payload))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&iteration); err != nil || iteration.Validate() != nil {
				return receipt, fmt.Errorf("malformed iteration receipt")
			}
			if process {
				if err := validateProcessIteration(definition, runID, processTranscript.Bytes(), iteration); err != nil {
					return receipt, err
				}
				processTranscript.Reset()
			}
			receipt.Iterations = append(receipt.Iterations, iteration)
		} else if index := strings.Index(event.Output, iterationreceipt.FamilyCutPrefix); index >= 0 {
			if !isFamilyCutDefinition(definition.ID) || event.Test != definition.Selector {
				return receipt, fmt.Errorf("unexpected family/cut iteration receipt for %q", definition.ID)
			}
			var iteration iterationreceipt.FamilyCutRecord
			decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(event.Output[index+len(iterationreceipt.FamilyCutPrefix):])))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&iteration); err != nil || iteration.Validate() != nil {
				return receipt, fmt.Errorf("malformed family/cut iteration receipt")
			}
			receipt.FamilyCutIterations = append(receipt.FamilyCutIterations, iteration)
		} else if requiresProcessIterationReceipts(definition) {
			_, _ = processTranscript.Write(line)
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
				childRuns[event.Test]++
			case "pass":
				receipt.ChildPassed++
				childPasses[event.Test]++
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
	if exit != 0 {
		sort.Strings(failedTests)
		return receipt, fmt.Errorf("go test process failed: exit=%d failed_tests=%v skip_events=%d started=%d passed=%d raw_sha256=%s", exit, failedTests, receipt.SkippedExecutions, receipt.StartedExecutions, receipt.PassedExecutions, receipt.RawTranscriptSHA256)
	}
	if requiresProcessIterationReceipts(definition) && processTranscript.Len() != 0 {
		return receipt, fmt.Errorf("unterminated process transcript bytes=%d", processTranscript.Len())
	}
	for name := range unexpected {
		receipt.UnexpectedTests = append(receipt.UnexpectedTests, name)
	}
	sort.Strings(receipt.UnexpectedTests)
	if receipt.StartedExecutions != executions || receipt.PassedExecutions != executions || receipt.FailedExecutions != 0 || receipt.SkippedExecutions != 0 || len(receipt.UnexpectedTests) != 0 || receipt.ChildStarted != receipt.ChildPassed {
		return receipt, fmt.Errorf("test receipt is incomplete: start=%d pass=%d fail=%d skip=%d child=%d/%d exit=%d unexpected=%v", receipt.StartedExecutions, receipt.PassedExecutions, receipt.FailedExecutions, receipt.SkippedExecutions, receipt.ChildStarted, receipt.ChildPassed, exit, receipt.UnexpectedTests)
	}
	if expectedChildren, exact := childContracts[definition.ID]; exact {
		if len(childRuns) != len(expectedChildren) || len(childPasses) != len(expectedChildren) {
			return receipt, fmt.Errorf("child test census differs for %s", definition.ID)
		}
		for _, child := range expectedChildren {
			if childRuns[child] != executions || childPasses[child] != executions {
				return receipt, fmt.Errorf("child test %s run/pass=%d/%d want=%d/%d", child, childRuns[child], childPasses[child], executions, executions)
			}
		}
	}
	if contract, required := iterationContracts[definition.ID]; required {
		if err := validateIterations(definition, contract, receipt.Iterations); err != nil {
			return receipt, err
		}
		receipt.CertificationMode = "executing-iteration-receipts-v1"
		receipt.CertifiedRepetitions = len(receipt.Iterations)
	} else if requiresProcessIterationReceipts(definition) {
		contract := iterationContract{"go-test-process", stringSet("pass")}
		if err := validateIterations(definition, contract, receipt.Iterations); err != nil {
			return receipt, err
		}
		receipt.CertificationMode = "runner-owned-process-receipts-v1"
		receipt.CertifiedRepetitions = len(receipt.Iterations)
	} else if isFamilyCutDefinition(definition.ID) {
		if err := validateFamilyCutIterations(definition, receipt.FamilyCutIterations); err != nil {
			return receipt, err
		}
		receipt.CertificationMode = "family-bound-publication-cut-receipts-v1"
		receipt.CertifiedRepetitions = len(receipt.FamilyCutIterations)
	} else {
		receipt.CertifiedRepetitions = receipt.PassedExecutions * definition.TrialsPerRun
	}
	receipt.Verdict = "pass"
	return receipt, nil
}

func isFamilyCutDefinition(id string) bool {
	return id == "fault/family-bound-publication-cuts" || id == "fault/outbox-bound-publication-cuts"
}

func validateFamilyCutIterations(definition Definition, records []iterationreceipt.FamilyCutRecord) error {
	families := []string{"acceptance", "artifact_catalog", "artifact_revision", "execution_slot", "external_effect", "handoff_slot", "integration_attempt", "integration_target", "mission", "operation", "review_slot", "workgraph", "workspace"}
	if definition.ID == "fault/outbox-bound-publication-cuts" {
		families = []string{"outbox"}
	}
	cuts := []string{"acknowledgement-before-projection", "before-publication", "durable-before-acknowledgement", "lost-acknowledgement", "malformed-acknowledgement"}
	want := len(families) * len(cuts) * 100
	if len(records) != want || definition.ExpectedRepetitions != want {
		return fmt.Errorf("family/cut receipt census=%d want=%d", len(records), want)
	}
	seen := make(map[string]struct{}, want)
	type observedFacts struct {
		Cut             string   `json:"cut"`
		QualifiedFamily string   `json:"qualified_family"`
		PlanIndex       int      `json:"plan_index"`
		ProviderCalls   int      `json:"provider_calls"`
		PublicationID   string   `json:"publication_id"`
		EmittedFamilies []string `json:"emitted_families"`
	}
	type invariantFacts struct {
		Unresolved            int  `json:"unresolved"`
		LedgerSize            int  `json:"ledger_size"`
		ExpectedLedgerSize    int  `json:"expected_ledger_size"`
		OneTerminalProjection bool `json:"one_terminal_projection"`
		SubstitutionRefused   bool `json:"substitution_refused"`
	}
	planByFamilyTrial := map[string]string{}
	for _, record := range records {
		if record.DefinitionID != definition.ID || !slices.Contains(families, record.Family) || !slices.Contains(cuts, record.Cut) || record.Index < 0 || record.Index >= 100 || record.Outcome != "interrupted-reconciled-once" || !slices.Contains(record.EmittedFamilies, record.Family) {
			return fmt.Errorf("foreign family/cut iteration: family=%q cut=%q index=%d", record.Family, record.Cut, record.Index)
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", record.Family, record.Cut, record.Index)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate family/cut iteration %s/%s/%d", record.Family, record.Cut, record.Index)
		}
		var observed observedFacts
		var invariants invariantFacts
		if strictJSON(record.ObservedFacts, &observed) != nil || strictJSON(record.InvariantEvidence, &invariants) != nil || observed.Cut != record.Cut || observed.QualifiedFamily != record.Family || observed.PlanIndex < 0 || observed.ProviderCalls <= 0 || observed.PublicationID != record.PublicationID || !slices.Equal(observed.EmittedFamilies, record.EmittedFamilies) || invariants.Unresolved != 0 || invariants.LedgerSize <= 0 || invariants.LedgerSize != invariants.ExpectedLedgerSize || !invariants.OneTerminalProjection || !invariants.SubstitutionRefused {
			return fmt.Errorf("family/cut observed facts or universal invariants differ for %s", key)
		}
		wantSeed := fmt.Sprintf("%s/%s/%03d/%d", record.Family, record.Cut, record.Index, observed.PlanIndex)
		if record.Family == "outbox" {
			wantSeed = fmt.Sprintf("outbox/%s/%03d", record.Cut, record.Index)
		}
		if record.Seed != wantSeed {
			return fmt.Errorf("family/cut deterministic seed differs for %s", key)
		}
		if record.Family == "outbox" {
			if !slices.Equal(record.EmittedFamilies, []string{"operation", "outbox"}) {
				return fmt.Errorf("outbox cut used foreign emitted families %v", record.EmittedFamilies)
			}
		} else if slices.Contains(record.EmittedFamilies, "outbox") || slices.Contains(record.EmittedFamilies, "runtime_host") {
			return fmt.Errorf("host family cut included non-command family %v", record.EmittedFamilies)
		}
		planKey := fmt.Sprintf("%s\x00%d", record.Family, record.Index)
		planIdentity := record.PlanSHA256 + "\x00" + record.PublicationID + "\x00" + strings.Join(record.EmittedFamilies, "\x00")
		if prior, ok := planByFamilyTrial[planKey]; ok && prior != planIdentity {
			return fmt.Errorf("family/cut plan changed across cuts for %s", planKey)
		}
		planByFamilyTrial[planKey] = planIdentity
		seen[key] = struct{}{}
	}
	return nil
}

func validateProcessIteration(definition Definition, runID string, transcript []byte, record iterationreceipt.Record) error {
	type facts struct {
		ProcessTranscriptSHA256 string `json:"process_transcript_sha256"`
		ProcessIndex            int    `json:"process_index"`
		ShuffleSeed             string `json:"shuffle_seed"`
	}
	type invariants struct {
		SelectedTest string `json:"selected_test"`
		ProcessExit  int    `json:"process_exit"`
	}
	var observed facts
	var checked invariants
	if strictJSON(record.ObservedFacts, &observed) != nil || strictJSON(record.InvariantEvidence, &checked) != nil {
		return fmt.Errorf("process iteration evidence has foreign fields")
	}
	wantSeed := SHA256([]byte(fmt.Sprintf("%s\x00%s\x00%d", runID, definition.ID, record.Index)))
	shuffleNumber, err := strconv.ParseUint(wantSeed[:15], 16, 64)
	if err != nil {
		return fmt.Errorf("invalid deterministic shuffle seed: %w", err)
	}
	if record.Seed != wantSeed || observed.ProcessIndex != record.Index || observed.ShuffleSeed != strconv.FormatUint(shuffleNumber, 10) || observed.ProcessTranscriptSHA256 != SHA256(transcript) || checked.SelectedTest != definition.Selector || checked.ProcessExit != 0 {
		return fmt.Errorf("process iteration evidence is not bound to execution %d", record.Index)
	}
	return nil
}

func strictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func requiresProcessIterationReceipts(definition Definition) bool {
	if definition.TrialsPerRun != 1 {
		return false
	}
	for _, frozen := range FrozenDefinitions() {
		if frozen.ID == definition.ID && frozen == definition {
			return true
		}
	}
	return false
}

func validateIterations(definition Definition, contract iterationContract, records []iterationreceipt.Record) error {
	if len(records) != definition.ExpectedRepetitions {
		return fmt.Errorf("iteration receipts=%d want=%d", len(records), definition.ExpectedRepetitions)
	}
	seenIndex := make(map[int]struct{}, len(records))
	seenSeed := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.DefinitionID != definition.ID || record.FaultBoundary != contract.boundary {
			return fmt.Errorf("foreign iteration receipt definition=%q boundary=%q", record.DefinitionID, record.FaultBoundary)
		}
		if _, ok := contract.outcomes[record.Outcome]; !ok {
			return fmt.Errorf("foreign iteration outcome %q", record.Outcome)
		}
		if record.Index < 0 || record.Index >= definition.ExpectedRepetitions {
			return fmt.Errorf("iteration index %d out of range", record.Index)
		}
		if _, duplicate := seenIndex[record.Index]; duplicate {
			return fmt.Errorf("duplicate iteration index %d", record.Index)
		}
		if _, duplicate := seenSeed[record.Seed]; duplicate {
			return fmt.Errorf("duplicate iteration seed %q", record.Seed)
		}
		seenIndex[record.Index], seenSeed[record.Seed] = struct{}{}, struct{}{}
	}
	return nil
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
