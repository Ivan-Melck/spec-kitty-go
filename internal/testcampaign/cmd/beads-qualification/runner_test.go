package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRequiredScenarioRegistryIsExactAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(requiredProviderScenarios))
	for _, name := range requiredProviderScenarios {
		if !isRequiredScenario(name) {
			t.Fatalf("registered scenario not recognized: %s", name)
		}
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("duplicate scenario: %s", name)
		}
		seen[name] = struct{}{}
	}
	if isRequiredScenario("TestRealBeadsQualification") || isRequiredScenario("TestRealBeadsQualification/fabricated") {
		t.Fatal("campaign accepted a non-required scenario")
	}
}

func TestEvidenceIsCreateOnlyRedactedAndSchemaShaped(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "qualification.json")
	validated, err := validateEvidencePath(path)
	if err != nil || validated != path {
		t.Fatalf("path=%q err=%v", validated, err)
	}
	evidence := validQualificationEvidence()
	repository := qualificationRepositoryRoot(t)
	schemaPath := filepath.Join(repository, qualificationEvidenceSchemaPath)
	if err := writeEvidence(path, evidence); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		t.Fatal(err)
	}
	if err := validateSerializedEvidence(schemaPath, payload); err != nil {
		t.Fatalf("emitted evidence violates the checked-in contract: %v", err)
	}
	unknownField := append([]byte(`{"unexpected":true,`), payload[1:]...)
	if err := validateSerializedRoundTrip(unknownField, evidence); err == nil {
		t.Fatal("strict serialized evidence decoder accepted an unknown field")
	}
	if err := writeEvidence(path, validQualificationEvidence()); err == nil {
		t.Fatal("atomic evidence publication overwrote an existing record")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(payload) {
		t.Fatalf("failed overwrite changed published evidence: err=%v", err)
	}
	object := instance.(map[string]any)
	contention := object["contention"].(map[string]any)
	contention["processes"] = float64(7)
	mutant, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSerializedEvidence(schemaPath, mutant); err == nil {
		t.Fatal("checked-in schema accepted a contention-shape mutant")
	}
	contention["processes"] = float64(8)
	delete(object, "contention")
	mutant, err = json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSerializedEvidence(schemaPath, mutant); err == nil {
		t.Fatal("checked-in schema accepted missing contention evidence")
	}
	invalidPath := filepath.Join(directory, "invalid.json")
	evidence.Contention.Processes = 7
	if err := writeEvidence(invalidPath, evidence); err == nil {
		t.Fatal("writer published passing evidence rejected by the checked-in schema")
	}
	if _, err := os.Stat(invalidPath); !os.IsNotExist(err) {
		t.Fatalf("schema-invalid evidence reached the destination: %v", err)
	}
	if _, err := validateEvidencePath(path); err == nil {
		t.Fatal("existing evidence path was accepted for overwrite")
	}
	evidence = validQualificationEvidence()
	evidence.Issues = []issueEvidence{{Kind: "raw_stderr", Message: "do not persist"}}
	if err := writeEvidence(filepath.Join(directory, "leaky.json"), evidence); err == nil {
		t.Fatal("sensitive evidence field was accepted")
	}
}

func TestEvidenceSchemaRepresentsHonestNonPassObservations(t *testing.T) {
	directory := t.TempDir()
	schemaPath := filepath.Join(qualificationRepositoryRoot(t), qualificationEvidenceSchemaPath)

	nonAdmissible := validQualificationEvidence()
	nonAdmissible.StorageMode = "embedded_serialized"
	nonAdmissible.Scenarios = scenarioCensus{Expected: 11, Started: 11, Terminal: 11, Passed: 10}
	nonAdmissible.Latency = nil
	nonAdmissible.Contention = nil
	nonAdmissible.AssignmentPreservation = nil
	nonAdmissible.AuthorityAudits = 0
	nonAdmissible.AuthorityInvariantFailures = 1
	nonAdmissible.Issues = []issueEvidence{{Kind: "scenario_skipped", Message: "server qualification was not configured"}}
	nonAdmissible.Verdict = "non_admissible"
	embeddedPath := filepath.Join(directory, "embedded.json")
	if err := writeEvidence(embeddedPath, nonAdmissible); err != nil {
		t.Fatalf("truthful embedded non-admissibility was not representable: %v", err)
	}
	assertEvidenceFileMatchesSchema(t, schemaPath, embeddedPath)

	failed := validQualificationEvidence()
	failed.Scenarios = scenarioCensus{Expected: 11, Started: 9, Terminal: 8, Passed: 7, Failed: 1}
	failed.Latency = []latencyEvidence{{Operation: "unexpected_bounded_operation", Samples: 37, P50MS: 100, P95MS: 250, MaxMS: 300}}
	failed.Contention = &contentionEvidence{Mode: "unexpected_bounded_mode", Processes: 8, Attempted: 1000, Terminal: 413, Passed: 412, Collisions: 1, DurationMS: 250}
	failed.AssignmentPreservation = &assignmentPreservationEvidence{Attempted: 20, Terminal: 12, Preserved: 12, Failed: 8, AttributionLost: 6, SafeCompensationUnsupported: 6}
	failed.AuthorityAudits = 5
	failed.AuthorityInvariantFailures = 1
	failed.Issues = []issueEvidence{{Kind: "scenario_failed", Message: "bounded provider scenario failed"}}
	failed.Verdict = "fail"
	failedPath := filepath.Join(directory, "partial-failure.json")
	if err := writeEvidence(failedPath, failed); err != nil {
		t.Fatalf("truthful partial failure census was not representable: %v", err)
	}
	assertEvidenceFileMatchesSchema(t, schemaPath, failedPath)
}

func TestPassingEvidenceSchemaRejectsIncompleteClaims(t *testing.T) {
	schemaPath := filepath.Join(qualificationRepositoryRoot(t), qualificationEvidenceSchemaPath)
	tests := []struct {
		name   string
		mutate func(*qualificationEvidence)
	}{
		{"embedded storage", func(e *qualificationEvidence) { e.StorageMode = "embedded_serialized" }},
		{"incomplete scenario census", func(e *qualificationEvidence) { e.Scenarios.Passed = 10 }},
		{"missing latency operation", func(e *qualificationEvidence) { e.Latency = e.Latency[:1] }},
		{"latency threshold exceeded", func(e *qualificationEvidence) { e.Latency[0].P95MS = 1001 }},
		{"missing contention", func(e *qualificationEvidence) { e.Contention = nil }},
		{"partial assignment census", func(e *qualificationEvidence) { e.AssignmentPreservation.Preserved = 19 }},
		{"missing authority audit", func(e *qualificationEvidence) { e.AuthorityAudits = 0 }},
		{"authority invariant failure", func(e *qualificationEvidence) { e.AuthorityInvariantFailures = 1 }},
		{"reported issue", func(e *qualificationEvidence) {
			e.Issues = []issueEvidence{{Kind: "scenario_failed", Message: "bounded failure"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := validQualificationEvidence()
			test.mutate(&evidence)
			payload, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateSerializedEvidence(schemaPath, payload); err == nil {
				t.Fatal("checked-in schema accepted an incomplete passing claim")
			}
			path := filepath.Join(t.TempDir(), "invalid-pass.json")
			if err := writeEvidence(path, evidence); err == nil {
				t.Fatal("writer published an incomplete passing claim")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid passing claim reached the destination: %v", err)
			}
		})
	}
}

func assertEvidenceFileMatchesSchema(t *testing.T, schemaPath, evidencePath string) {
	t.Helper()
	payload, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSerializedEvidence(schemaPath, payload); err != nil {
		t.Fatalf("serialized evidence violates checked-in schema: %v", err)
	}
}

func validateSerializedEvidence(schemaPath string, payload []byte) error {
	schemaPayload, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaPayload, &schemaDocument); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaPath, schemaDocument); err != nil {
		return err
	}
	compiled, err := compiler.Compile(schemaPath)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		return err
	}
	return compiled.Validate(instance)
}

func TestProviderEvidenceValidationIsMandatoryAndExact(t *testing.T) {
	latencies := []latencyEvidence{
		{Operation: "preflight", Samples: 100, P50MS: 100, P95MS: 900, MaxMS: 950},
		{Operation: "warm_single_item_read", Samples: 200, P50MS: 100, P95MS: 400, MaxMS: 450},
	}
	contention := &contentionEvidence{Mode: "server_multiwriter", Processes: 8, Attempted: 1000, Terminal: 1000, Passed: 1000, DurationMS: 1}
	assignment := &assignmentPreservationEvidence{Attempted: 20, Terminal: 20, Preserved: 20, AttributionLost: 10, SafeCompensationUnsupported: 10}
	authority := &authorityAuditEvidence{Audits: 6}
	if issues := validateProviderEvidence("server_multiwriter", latencies, contention, assignment, authority); len(issues) != 0 {
		t.Fatalf("valid evidence rejected: %+v", issues)
	}
	tests := []struct {
		name       string
		latencies  []latencyEvidence
		assignment *assignmentPreservationEvidence
		authority  *authorityAuditEvidence
	}{
		{"missing latency", latencies[:1], assignment, authority},
		{"duplicate latency", []latencyEvidence{latencies[0], latencies[0], latencies[1]}, assignment, authority},
		{"wrong sample count", []latencyEvidence{{Operation: "preflight", Samples: 99, P50MS: 1, P95MS: 2, MaxMS: 3}, latencies[1]}, assignment, authority},
		{"threshold exceeded", []latencyEvidence{{Operation: "preflight", Samples: 100, P50MS: 1, P95MS: 1001, MaxMS: 1001}, latencies[1]}, assignment, authority},
		{"missing assignment", latencies, nil, authority},
		{"missing authority", latencies, assignment, nil},
		{"authority failure", latencies, assignment, &authorityAuditEvidence{Audits: 6, Failures: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if issues := validateProviderEvidence("server_multiwriter", test.latencies, contention, test.assignment, test.authority); len(issues) == 0 {
				t.Fatal("non-admissible evidence was accepted")
			}
		})
	}
	var marker latencyEvidence
	for _, encoded := range []string{
		`{"operation":"preflight","samples":"100","p50_ms":1,"p95_ms":2,"max_ms":3}`,
		`{"operation":"preflight","samples":100,"p50_ms":1,"p95_ms":2,"max_ms":3,"extra":true}`,
		`{"operation":"preflight","samples":100,"p50_ms":1,"p95_ms":2,"max_ms":3} {}`,
	} {
		if err := decodeEvidenceMarker(encoded, &marker); err == nil {
			t.Fatalf("malformed marker accepted: %s", encoded)
		}
	}
}

func TestInstallerRejectsDestinationBytesThatCannotBeEmittedAsJSON(t *testing.T) {
	repository := qualificationRepositoryRoot(t)
	destination := filepath.Join(t.TempDir(), `quote"path`)
	command := exec.Command(filepath.Join(repository, "scripts", "install-qualified-beads.sh"), "--dest", destination, "--offline")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "destination contains bytes unsupported by the JSON result contract") {
		t.Fatalf("adversarial destination was not rejected before machine output: err=%v output=%q", err, output)
	}
}

func TestEvidencePathRefusesRelativeMissingParentAndSymlink(t *testing.T) {
	if _, err := validateEvidencePath("relative.json"); err == nil {
		t.Fatal("relative evidence path was accepted")
	}
	if _, err := validateEvidencePath(filepath.Join(t.TempDir(), "missing", "evidence.json")); err == nil {
		t.Fatal("missing evidence parent was accepted")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "evidence.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEvidencePath(link); err == nil {
		t.Fatal("symlink evidence target was accepted")
	}
}

func TestSourceIdentityIsBoundToCommitAndTree(t *testing.T) {
	repository := qualificationRepositoryRoot(t)
	commit, tree, _, err := sourceIdentity(context.Background(), repository)
	if err != nil || len(commit) != 40 || len(tree) != 40 {
		t.Fatalf("commit=%q tree=%q err=%v", commit, tree, err)
	}
}

func validQualificationEvidence() qualificationEvidence {
	now := time.Now().UTC()
	return qualificationEvidence{
		SchemaVersion: 1,
		SourceCommit:  strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
		ProviderVersion: qualifiedVersion, ProviderDigestSHA256: strings.Repeat("c", 64),
		ProtocolSchema: qualifiedSchema, StorageMode: "server_multiwriter",
		Host: hostEvidence{GOOS: "linux", GOARCH: "amd64"}, StartedAt: now, FinishedAt: now,
		Scenarios: scenarioCensus{Expected: 11, Started: 11, Terminal: 11, Passed: 11},
		Latency: []latencyEvidence{
			{Operation: "preflight", Samples: 100, P50MS: 100, P95MS: 200, MaxMS: 300},
			{Operation: "warm_single_item_read", Samples: 200, P50MS: 50, P95MS: 100, MaxMS: 150},
		},
		Contention:             &contentionEvidence{Mode: "server_multiwriter", Processes: 8, Attempted: 1000, Terminal: 1000, Passed: 1000, DurationMS: 1},
		AssignmentPreservation: &assignmentPreservationEvidence{Attempted: 20, Terminal: 20, Preserved: 20, AttributionLost: 10, SafeCompensationUnsupported: 10},
		AuthorityAudits:        6, Verdict: "pass",
	}
}

func qualificationRepositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(working, "..", "..", "..", ".."))
}
