package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	evidence := qualificationEvidence{
		SchemaVersion: 1,
		SourceCommit:  strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
		ProviderVersion: qualifiedVersion, ProviderDigestSHA256: strings.Repeat("c", 64),
		ProtocolSchema: qualifiedSchema, StorageMode: "embedded_serialized",
		Host:      hostEvidence{GOOS: "linux", GOARCH: "amd64"},
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		Scenarios: scenarioCensus{Expected: 1, Started: 1, Terminal: 1, Passed: 1}, Verdict: "pass",
	}
	if err := writeEvidence(path, evidence); err != nil {
		t.Fatal(err)
	}
	if _, err := validateEvidencePath(path); err == nil {
		t.Fatal("existing evidence path was accepted for overwrite")
	}
	evidence.Issues = []issueEvidence{{Kind: "raw_stderr", Message: "do not persist"}}
	if err := writeEvidence(filepath.Join(directory, "leaky.json"), evidence); err == nil {
		t.Fatal("sensitive evidence field was accepted")
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
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Clean(filepath.Join(working, "..", "..", "..", ".."))
	commit, tree, _, err := sourceIdentity(context.Background(), repository)
	if err != nil || len(commit) != 40 || len(tree) != 40 {
		t.Fatalf("commit=%q tree=%q err=%v", commit, tree, err)
	}
}
