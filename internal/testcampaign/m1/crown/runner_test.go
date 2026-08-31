package crown

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
)

func TestPreflightRefusesImplicitOrRelativeAuthority(t *testing.T) {
	_, err := Preflight(Config{RunID: "run-crown", Seed: 1, Repetitions: 100, RepositoryRoot: ".", GapDBDPath: "gapdbd", AdmissionRecordPath: "admission.json", ProductPolicyPath: "policy.json", RuntimeRoot: "runtime", DataRoot: "data", EvidenceRoot: "evidence", ProvingBinaryPath: "spk-m1-proving-ground"})
	if err == nil {
		t.Fatal("relative paths were accepted as execution authority")
	}
	_, err = Preflight(Config{})
	if err == nil {
		t.Fatal("missing immutable RunID/seed/repetition census was accepted")
	}
}

func TestContractBundleIsDeterministicAndSeparatesBudget(t *testing.T) {
	root := t.TempDir()
	contracts := filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	if err := os.MkdirAll(contracts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/crown\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("sum\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	budget := filepath.Join(contracts, "m1-product-budgets-v1.json")
	if err := os.WriteFile(filepath.Join(contracts, "z.schema.json"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contracts, "a.schema.json"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(budget, []byte("budget-secret-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := contractBundle(root, budget)
	if err != nil {
		t.Fatal(err)
	}
	second, err := contractBundle(root, budget)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("contract bundle is not canonical")
	}
	if bytes.Contains(first, []byte("budget-secret-marker")) {
		t.Fatal("budget bytes leaked into the separate contracts artifact")
	}
	if bytes.Index(first, []byte("a.schema.json")) > bytes.Index(first, []byte("z.schema.json")) {
		t.Fatal("contract bundle order is not stable")
	}
}

func TestCancellationPublishesVerifiableIncompleteEvidenceWithoutPassSignature(t *testing.T) {
	root := t.TempDir()
	contracts := filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	if err := os.MkdirAll(contracts, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Join(root, "go.mod"):                          []byte("module example.test/crown\n"),
		filepath.Join(root, "go.sum"):                          []byte("sum\n"),
		filepath.Join(contracts, "contract.json"):              []byte("{}\n"),
		filepath.Join(contracts, "m1-product-budgets-v1.json"): []byte("{}\n"),
		filepath.Join(root, "proving-binary"):                  []byte("binary"),
	}
	for path, contents := range files {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{RunID: "run-incomplete", RepositoryRoot: root, ProductPolicyPath: filepath.Join(contracts, "m1-product-budgets-v1.json"), EvidenceRoot: filepath.Join(root, "evidence"), ProvingBinaryPath: filepath.Join(root, "proving-binary")}
	publication, manifest, err := publishNonPass(context.Background(), config, PreflightResult{AdmissionRecordSHA256: m1.AdmittedTupleSHA256}, nil, &runEvidenceState{}, context.Canceled)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Outcome != "incomplete" || publication.TechnicalPass != "" {
		t.Fatalf("cancellation evidence was promoted: %+v %+v", manifest, publication)
	}
	verification, verified, err := m1.VerifyEvidence(context.Background(), publication.Directory, m1.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if verification.Outcome != "incomplete" || verification.AdoptionEligible || verified.Outcome != "incomplete" {
		t.Fatalf("incomplete verification was promoted: %+v", verification)
	}
	if _, err := os.Stat(filepath.Join(publication.Directory, m1.TechnicalPassName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("technical pass signature exists: %v", err)
	}
}
