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
	repository := qualificationRepositoryRoot(t)
	schemaPath := filepath.Join(repository, "kitty-specs", "beads-tracker-integration-01M0YG7E", "contracts", "qualification-evidence.schema.json")
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
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("emitted evidence violates the checked-in contract: %v", err)
	}
	object := instance.(map[string]any)
	contention := object["contention"].(map[string]any)
	contention["processes"] = float64(7)
	if err := compiled.Validate(instance); err == nil {
		t.Fatal("checked-in schema accepted a contention-shape mutant")
	}
	contention["processes"] = float64(8)
	delete(object, "contention")
	if err := compiled.Validate(instance); err == nil {
		t.Fatal("checked-in schema accepted missing contention evidence")
	}
	if _, err := validateEvidencePath(path); err == nil {
		t.Fatal("existing evidence path was accepted for overwrite")
	}
	evidence.Issues = []issueEvidence{{Kind: "raw_stderr", Message: "do not persist"}}
	if err := writeEvidence(filepath.Join(directory, "leaky.json"), evidence); err == nil {
		t.Fatal("sensitive evidence field was accepted")
	}
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
