package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
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
	if err := strictDecodeQualificationEvidence(unknownField); err == nil {
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
	nonAdmissible.AuthorityInvariantFailures = 0
	nonAdmissible.Issues = []issueEvidence{{Kind: "scenario_skipped", Message: "server qualification was not configured"}}
	nonAdmissible.Verdict = "non_admissible"
	nonAdmissible.Proof.PassedIDs = nonAdmissible.Proof.PassedIDs[:10]
	nonAdmissible.Proof.SkippedIDs = []string{nonAdmissible.Proof.TerminalIDs[10]}
	nonAdmissible.Proof.OfflineVerified = false
	nonAdmissible.Proof.PrerequisiteMissing = true
	embeddedPath := filepath.Join(directory, "embedded.json")
	if err := writeEvidence(embeddedPath, nonAdmissible); err != nil {
		t.Fatalf("truthful embedded non-admissibility was not representable: %v", err)
	}
	assertEvidenceFileMatchesSchema(t, schemaPath, embeddedPath)

	failed := validQualificationEvidence()
	failed.Scenarios = scenarioCensus{Expected: 11, Started: 9, Terminal: 8, Passed: 7, Failed: 1}
	failed.Latency = []latencyEvidence{{Operation: "unexpected_bounded_operation", Samples: 37, P50MS: 100, P95MS: 250, MaxMS: 300}}
	failed.Contention = &contentionEvidence{Mode: "unexpected_bounded_mode", Processes: 8, Attempted: 1000, Terminal: 413, Passed: 412, Collisions: 1, DurationMS: 250}
	failed.AssignmentPreservation = &assignmentPreservationEvidence{Attempted: 20, Terminal: 12, Preserved: 11, Failed: 1, AttributionLost: 6, SafeCompensationUnsupported: 6}
	failed.AuthorityAudits = 5
	failed.AuthorityInvariantFailures = 1
	failed.Issues = []issueEvidence{{Kind: "scenario_failed", Message: "bounded provider scenario failed"}}
	failed.Verdict = "fail"
	failed.Proof.StartedIDs = failed.Proof.StartedIDs[:9]
	failed.Proof.TerminalIDs = failed.Proof.TerminalIDs[:8]
	failed.Proof.PassedIDs = failed.Proof.PassedIDs[:7]
	failed.Proof.FailedIDs = []string{failed.Proof.TerminalIDs[7]}
	failed.Proof.AssertionFailed = true
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

type inertQualificationFixture struct{}

func (inertQualificationFixture) initialize(context.Context) error { return nil }
func (inertQualificationFixture) cleanup() error                   { return nil }

func TestRunQualificationPublishesOnlyCanonicalCommandEvidence(t *testing.T) {
	restore := installQualificationSeams(t)
	defer restore()
	repository := t.TempDir()
	path := filepath.Join(t.TempDir(), "qualification.json")
	result, err := runQualification(context.Background(), options{BinaryPath: "/qualified/bd", Digest: strings.Repeat("c", 64), Evidence: path, Repository: repository, Timeout: time.Minute})
	if err != nil || result.Verdict != "pass" {
		t.Fatalf("command result=%+v err=%v", result, err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSerializedEvidence(filepath.Join(qualificationRepositoryRoot(t), qualificationEvidenceSchemaPath), payload); err != nil {
		t.Fatalf("actual command artifact violates schema: %v", err)
	}
	var encoded qualificationEvidence
	if err := json.Unmarshal(payload, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Verdict != "pass" || encoded.Scenarios.Passed != 11 || encoded.AuthorityAudits < 1 {
		t.Fatalf("unexpected command artifact=%+v", encoded)
	}
}

func TestRunQualificationDisconnectingStaleAuthorityCannotPublishPass(t *testing.T) {
	restore := installQualificationSeams(t)
	defer restore()
	executeQualificationProviderTests = func(context.Context, string, binaryIdentity, time.Duration) providerTestResult {
		result := validProviderTestResult()
		result.StaleAuthority = nil
		result.Issues = []issueEvidence{{Kind: "stale_authority_missing", Message: "real stale lease, fence, and workspace probe was not emitted"}}
		return result
	}
	path := filepath.Join(t.TempDir(), "qualification.json")
	result, err := runQualification(context.Background(), options{BinaryPath: "/qualified/bd", Digest: strings.Repeat("c", 64), Evidence: path, Repository: t.TempDir(), Timeout: time.Minute})
	if err == nil || result.Verdict == "pass" {
		t.Fatalf("disconnected stale probe result=%+v err=%v", result, err)
	}
	payload, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(payload), `"verdict": "pass"`) {
		t.Fatal("disconnected stale probe published pass")
	}
}

func TestRunQualificationRebindMutantCannotPublishPass(t *testing.T) {
	restore := installQualificationSeams(t)
	defer restore()
	calls := 0
	readQualificationSourceIdentity = func(context.Context, string) (string, string, bool, error) {
		calls++
		if calls == 1 {
			return strings.Repeat("a", 40), strings.Repeat("b", 40), false, nil
		}
		return strings.Repeat("a", 40), strings.Repeat("d", 40), false, nil
	}
	path := filepath.Join(t.TempDir(), "qualification.json")
	result, err := runQualification(context.Background(), options{BinaryPath: "/qualified/bd", Digest: strings.Repeat("c", 64), Evidence: path, Repository: t.TempDir(), Timeout: time.Minute})
	if err == nil || result.Verdict == "pass" || !strings.Contains(result.Issues[0].Kind, "source_drift") {
		t.Fatalf("source-rebind mutant result=%+v err=%v", result, err)
	}
}

func TestRunQualificationRefusesDirtySourceBeforeFixtureOrProvider(t *testing.T) {
	restore := installQualificationSeams(t)
	defer restore()
	fixtureCalled, providerCalled := false, false
	readQualificationSourceIdentity = func(context.Context, string) (string, string, bool, error) {
		return strings.Repeat("a", 40), strings.Repeat("b", 40), true, nil
	}
	makeQualificationFixture = func(binaryIdentity, bool) (qualificationFixture, error) {
		fixtureCalled = true
		return inertQualificationFixture{}, nil
	}
	executeQualificationProviderTests = func(context.Context, string, binaryIdentity, time.Duration) providerTestResult {
		providerCalled = true
		return validProviderTestResult()
	}
	path := filepath.Join(t.TempDir(), "qualification.json")
	_, err := runQualification(context.Background(), options{BinaryPath: "/qualified/bd", Digest: strings.Repeat("c", 64), Evidence: path, Repository: t.TempDir(), Timeout: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "dirty") || fixtureCalled || providerCalled {
		t.Fatalf("dirty preflight err=%v fixture=%t provider=%t", err, fixtureCalled, providerCalled)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("dirty preflight published evidence: %v", statErr)
	}
}

func installQualificationSeams(t *testing.T) func() {
	t.Helper()
	for key, value := range map[string]string{"BEADS_SERVER_ID": "wp10-server", "BEADS_SERVER_ENDPOINT": "127.0.0.1:3307/wp10", "DOLT_SERVER_HOST": "127.0.0.1", "DOLT_SERVER_PORT": "3307"} {
		t.Setenv(key, value)
	}
	originalVerify, originalFixture, originalExecute := verifyQualificationBinary, makeQualificationFixture, executeQualificationProviderTests
	originalSource, originalOffline := readQualificationSourceIdentity, verifyQualificationOffline
	verifyQualificationBinary = func(context.Context, string, string) (binaryIdentity, error) {
		return binaryIdentity{Path: "/qualified/bd", Version: qualifiedVersion, Digest: strings.Repeat("c", 64), Schema: qualifiedSchema}, nil
	}
	makeQualificationFixture = func(binaryIdentity, bool) (qualificationFixture, error) { return inertQualificationFixture{}, nil }
	executeQualificationProviderTests = func(context.Context, string, binaryIdentity, time.Duration) providerTestResult {
		return validProviderTestResult()
	}
	readQualificationSourceIdentity = func(context.Context, string) (string, string, bool, error) {
		return strings.Repeat("a", 40), strings.Repeat("b", 40), false, nil
	}
	verifyQualificationOffline = func() bool { return true }
	return func() {
		verifyQualificationBinary, makeQualificationFixture, executeQualificationProviderTests = originalVerify, originalFixture, originalExecute
		readQualificationSourceIdentity, verifyQualificationOffline = originalSource, originalOffline
	}
}

func validProviderTestResult() providerTestResult {
	ids := requiredProviderScenariosSorted()
	return providerTestResult{
		Census:         scenarioCensus{Expected: 11, Started: 11, Terminal: 11, Passed: 11},
		Latencies:      []latencyEvidence{{Operation: "preflight", Samples: 100, P50MS: 1, P95MS: 2, MaxMS: 3}, {Operation: "warm_single_item_read", Samples: 200, P50MS: 1, P95MS: 2, MaxMS: 3}},
		Contention:     &contentionEvidence{Mode: "server_multiwriter", Processes: 8, Attempted: 1000, Terminal: 1000, Passed: 1000, DurationMS: 1},
		Assignment:     &assignmentPreservationEvidence{Attempted: 20, Terminal: 20, Preserved: 20, AttributionLost: 10, SafeCompensationUnsupported: 10},
		Authority:      &authorityAuditEvidence{Audits: 6},
		StaleAuthority: &staleAuthorityEvidence{Attempted: 1, Refused: 1, ReplacementWorkspacePreserved: 1},
		Proof:          testcampaign.BeadsEvidenceProof{ExpectedIDs: ids, StartedIDs: append([]string(nil), ids...), TerminalIDs: append([]string(nil), ids...), PassedIDs: append([]string(nil), ids...)},
	}
}

func validQualificationEvidence() qualificationEvidence {
	now := time.Now().UTC()
	ids := requiredProviderScenariosSorted()
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
		Proof: testcampaign.BeadsEvidenceProof{
			ExpectedIDs: ids, StartedIDs: append([]string(nil), ids...), TerminalIDs: append([]string(nil), ids...), PassedIDs: append([]string(nil), ids...),
			SourceBindings: []testcampaign.BeadsSourceBinding{
				{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
				{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
			},
			ProviderVerified: true, OfflineVerified: true, StaleAuthorityVerified: true, SourceClean: true,
		},
	}
}

func strictDecodeQualificationEvidence(payload []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var evidence qualificationEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err == nil {
		return errors.New("trailing JSON")
	}
	return nil
}

func qualificationRepositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(working, "..", "..", "..", ".."))
}
