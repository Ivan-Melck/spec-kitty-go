package testcampaign

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestBeadsEvidencePassFailAndNonAdmissibleMatchCheckedInSchema(t *testing.T) {
	tests := []struct {
		name     string
		evidence BeadsQualificationEvidence
	}{
		{"pass", validBeadsPassEvidence()},
		{"fail", validBeadsFailEvidence()},
		{"non_admissible", validBeadsNonAdmissibleEvidence()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := EncodeBeadsEvidence(test.evidence)
			if err != nil {
				t.Fatal(err)
			}
			validateBeadsEvidenceSchema(t, payload)
			second, err := EncodeBeadsEvidence(test.evidence)
			if err != nil || string(second) != string(payload) {
				t.Fatalf("evidence encoding is not deterministic: err=%v", err)
			}
		})
	}
}

func TestBeadsEvidenceLawMutantsFailForImplicatedControl(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*BeadsQualificationEvidence)
	}{
		{"missing terminal", "complete successful scenario census", func(e *BeadsQualificationEvidence) {
			e.Scenarios.Terminal, e.Scenarios.Passed = 10, 10
			e.Proof.TerminalIDs, e.Proof.PassedIDs = e.Proof.TerminalIDs[:10], e.Proof.PassedIDs[:10]
		}},
		{"invariant failure", "invariant failures", func(e *BeadsQualificationEvidence) { e.AuthorityInvariantFailures = 1 }},
		{"wrong digest", "64-character provider digest", func(e *BeadsQualificationEvidence) { e.ProviderDigestSHA256 = strings.Repeat("d", 63) }},
		{"wrong version", "compatibility tuple", func(e *BeadsQualificationEvidence) { e.ProviderVersion = "1.2.3" }},
		{"wrong protocol schema", "compatibility tuple", func(e *BeadsQualificationEvidence) { e.ProtocolSchema = 2 }},
		{"required skip", "zero skips", func(e *BeadsQualificationEvidence) {
			e.Scenarios.Passed = 10
			e.Proof.PassedIDs = e.Proof.PassedIDs[:10]
			e.Proof.SkippedIDs = []string{e.Proof.TerminalIDs[10]}
		}},
		{"seeded secret", "unsafe", func(e *BeadsQualificationEvidence) {
			e.Issues = []BeadsIssueEvidence{{Kind: "setup", Message: "credential sk_live_seeded"}}
		}},
		{"comment body", "unsafe", func(e *BeadsQualificationEvidence) {
			e.Issues = []BeadsIssueEvidence{{Kind: "setup", Message: "comment_body portable human content"}}
		}},
		{"mixed source", "mixed or dirty source", func(e *BeadsQualificationEvidence) { e.Proof.SourceBindings[0].Tree = strings.Repeat("e", 40) }},
		{"dirty source", "mixed or dirty source", func(e *BeadsQualificationEvidence) { e.Proof.SourceBindings[0].Dirty = true }},
		{"missing stale authority proof", "stale-authority probe", func(e *BeadsQualificationEvidence) { e.Proof.StaleAuthorityVerified = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validBeadsPassEvidence()
			test.mutate(&evidence)
			if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mutant error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestBeadsEvidenceRejectsLatencyAndScenarioAggregationTricks(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BeadsQualificationEvidence)
	}{
		{"duplicate latency", func(e *BeadsQualificationEvidence) { e.Latency[1].Operation = e.Latency[0].Operation }},
		{"nan latency", func(e *BeadsQualificationEvidence) { e.Latency[0].P95MS = math.NaN() }},
		{"unordered latency", func(e *BeadsQualificationEvidence) { e.Latency[0].P50MS, e.Latency[0].P95MS = 5, 4 }},
		{"too many latency operations", func(e *BeadsQualificationEvidence) {
			e.Latency = append(e.Latency, BeadsLatencyEvidence{Operation: "extra", Samples: 1})
		}},
		{"latency exceeds schema maximum", func(e *BeadsQualificationEvidence) { e.Latency[0].MaxMS = 1_800_001 }},
		{"duplicate scenario id", func(e *BeadsQualificationEvidence) { e.Proof.ExpectedIDs[1] = e.Proof.ExpectedIDs[0] }},
		{"hidden unexpected id", func(e *BeadsQualificationEvidence) { e.Proof.PassedIDs[10] = "scenario-99" }},
		{"contention terminal exceeds attempts", func(e *BeadsQualificationEvidence) { e.Contention.Terminal = e.Contention.Attempted + 1 }},
		{"assignment terminal exceeds attempts", func(e *BeadsQualificationEvidence) {
			e.AssignmentPreservation.Terminal = e.AssignmentPreservation.Attempted + 1
		}},
		{"authority failures exceed audits", func(e *BeadsQualificationEvidence) { e.AuthorityInvariantFailures = e.AuthorityAudits + 1 }},
		{"green non-admissible", func(e *BeadsQualificationEvidence) { e.Verdict = "non_admissible" }},
		{"green fail", func(e *BeadsQualificationEvidence) { e.Verdict = "fail" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validBeadsPassEvidence()
			test.mutate(&evidence)
			if err := evidence.Validate(); err == nil {
				t.Fatal("aggregation or verdict mutant was accepted")
			}
		})
	}
}

func TestBeadsEvidenceObservedFailureCannotMasqueradeAsNonAdmissible(t *testing.T) {
	evidence := validBeadsNonAdmissibleEvidence()
	evidence.Scenarios.Passed = 9
	evidence.Scenarios.Failed = 1
	evidence.Proof.PassedIDs = evidence.Proof.PassedIDs[:9]
	evidence.Proof.FailedIDs = []string{"scenario-10"}
	evidence.Proof.AssertionFailed = true
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "requires verdict fail") {
		t.Fatalf("observed failure classification error=%v", err)
	}
}

func TestBeadsEvidenceEncodingDoesNotReorderCallerCollections(t *testing.T) {
	evidence := validBeadsPassEvidence()
	before := append([]BeadsLatencyEvidence(nil), evidence.Latency...)
	if _, err := EncodeBeadsEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(evidence.Latency, before) {
		t.Fatalf("encoder mutated caller latency order: before=%v after=%v", before, evidence.Latency)
	}
}

func TestBeadsEvidenceAtomicWriteIsRestrictedAndRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qualification.json")
	first := validBeadsNonAdmissibleEvidence()
	if err := WriteBeadsEvidence(path, first); err != nil {
		t.Fatal(err)
	}
	if err := WriteBeadsEvidence(path, validBeadsPassEvidence()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence mode=%v err=%v", info.Mode().Perm(), err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	validateBeadsEvidenceSchema(t, payload)
	if strings.Contains(string(payload), "portable human content") || strings.Contains(string(payload), "sk_live") {
		t.Fatal("encoded evidence leaked seeded content")
	}
	if err := WriteBeadsEvidence("relative.json", validBeadsPassEvidence()); err == nil {
		t.Fatal("relative evidence path was accepted")
	}
}

func validBeadsPassEvidence() BeadsQualificationEvidence {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	ids := scenarioIDs(11)
	return BeadsQualificationEvidence{
		SchemaVersion: 1, SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
		ProviderVersion: "1.2.2", ProviderDigestSHA256: strings.Repeat("c", 64), ProtocolSchema: 1,
		StorageMode: "server_multiwriter", Host: BeadsHostEvidence{GOOS: "linux", GOARCH: "amd64", Runner: "hosted"},
		StartedAt: now, FinishedAt: now.Add(time.Minute), Scenarios: BeadsScenarioCensus{Expected: 11, Started: 11, Terminal: 11, Passed: 11},
		Latency: []BeadsLatencyEvidence{
			{Operation: "warm_single_item_read", Samples: 200, P50MS: 100, P95MS: 200, MaxMS: 250},
			{Operation: "preflight", Samples: 100, P50MS: 200, P95MS: 400, MaxMS: 500},
		},
		Contention:             &BeadsContentionEvidence{Mode: "server_multiwriter", Processes: 8, Attempted: 1000, Terminal: 1000, Passed: 1000, DurationMS: 1000},
		AssignmentPreservation: &BeadsAssignmentEvidence{Attempted: 20, Terminal: 20, Preserved: 20, AttributionLost: 10, SafeCompensationUnsupported: 10},
		AuthorityAudits:        23, Verdict: "pass",
		Proof: BeadsEvidenceProof{
			ExpectedIDs: ids, StartedIDs: append([]string(nil), ids...), TerminalIDs: append([]string(nil), ids...), PassedIDs: append([]string(nil), ids...),
			SourceBindings: []BeadsSourceBinding{
				{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
				{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
			},
			ProviderVerified: true, OfflineVerified: true, StaleAuthorityVerified: true, SourceClean: true,
		},
	}
}

func validBeadsFailEvidence() BeadsQualificationEvidence {
	evidence := validBeadsPassEvidence()
	evidence.Scenarios = BeadsScenarioCensus{Expected: 11, Started: 9, Terminal: 8, Passed: 7, Failed: 1}
	evidence.Proof.ExpectedIDs = scenarioIDs(11)
	evidence.Proof.StartedIDs = scenarioIDs(9)
	evidence.Proof.TerminalIDs = scenarioIDs(8)
	evidence.Proof.PassedIDs = scenarioIDs(7)
	evidence.Proof.FailedIDs = []string{"scenario-08"}
	evidence.Proof.AssertionFailed = true
	evidence.Latency = evidence.Latency[:1]
	evidence.Contention = nil
	evidence.AssignmentPreservation = nil
	evidence.AuthorityInvariantFailures = 1
	evidence.Issues = []BeadsIssueEvidence{{Kind: "scenario_failed", Message: "bounded assertion failed"}}
	evidence.Verdict = "fail"
	return evidence
}

func validBeadsNonAdmissibleEvidence() BeadsQualificationEvidence {
	evidence := validBeadsPassEvidence()
	evidence.StorageMode = "embedded_serialized"
	evidence.Scenarios.Passed = 10
	evidence.Proof.PassedIDs = evidence.Proof.PassedIDs[:10]
	evidence.Proof.SkippedIDs = []string{"scenario-11"}
	evidence.Proof.ProviderVerified = false
	evidence.Proof.OfflineVerified = false
	evidence.Proof.PrerequisiteMissing = true
	evidence.Latency = nil
	evidence.Contention = nil
	evidence.AssignmentPreservation = nil
	evidence.AuthorityAudits = 0
	evidence.AuthorityInvariantFailures = 0
	evidence.Issues = []BeadsIssueEvidence{{Kind: "scenario_skipped", Message: "qualified server topology unavailable"}}
	evidence.Verdict = "non_admissible"
	return evidence
}

func scenarioIDs(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = "scenario-" + string(rune('0'+(index+1)/10)) + string(rune('0'+(index+1)%10))
	}
	return result
}

func validateBeadsEvidenceSchema(t *testing.T, payload []byte) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "kitty-specs", "beads-tracker-integration-01M0YG7E", "contracts", "qualification-evidence.schema.json")
	schemaPayload, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaPayload, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaPath, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("evidence violates checked-in schema: %v", err)
	}
}

func TestScenarioIDFixtureIsSorted(t *testing.T) {
	ids := scenarioIDs(11)
	if !slices.IsSorted(ids) || len(ids) != 11 {
		t.Fatalf("scenario fixture is unstable: %v", ids)
	}
}
