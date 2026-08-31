package crown

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestPreflightMutantsRejectPolicyBinaryDirtyIgnoredAndOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	contractRoot := filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	if err := os.MkdirAll(contractRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(contractRoot, "m1-product-budgets-v1.json")
	if err := os.WriteFile(policy, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProductPolicy(root, policy, digest); err == nil {
		t.Fatal("arbitrary policy bytes were accepted")
	}
	foreign := filepath.Join(root, "foreign-binary")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateProvingBinary(foreign, "head"); err == nil {
		t.Fatal("foreign proving binary was accepted")
	}
	a, b, c := filepath.Join(root, "runtime"), filepath.Join(root, "runtime", "data"), filepath.Join(root, "evidence")
	for _, path := range []string{a, b, c} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := validateExclusiveRoots(a, b, c); err == nil {
		t.Fatal("overlapping execution roots were accepted")
	}

	repository := filepath.Join(root, "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}} {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if output, commandErr := command.CombinedOutput(); commandErr != nil {
			t.Fatalf("git %v: %s: %v", args, output, commandErr)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.go"), []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "-C", repository, "add", ".gitignore", "tracked.go")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "-C", repository, "commit", "-m", "fixture")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("commit: %s: %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(repository, "ignored"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanRepositoryHead(repository); err == nil {
		t.Fatal("dirty ignored source was accepted")
	}
	if err := os.Remove(filepath.Join(repository, "ignored")); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "-C", repository, "update-index", "--assume-unchanged", "tracked.go")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.go"), []byte("package fixture\nvar changed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanRepositoryHead(repository); err == nil {
		t.Fatal("assume-unchanged compiler input was accepted")
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
	state := &runEvidenceState{}
	state.InitialProvenance.SupervisorRunID = config.RunID
	publication, manifest, err := publishNonPass(context.Background(), config, PreflightResult{AdmissionRecordSHA256: m1.AdmittedTupleSHA256}, nil, state, context.Canceled)
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
	digestCounts := make(map[string]int, len(verified.Artifacts))
	for _, artifact := range verified.Artifacts {
		digestCounts[artifact.SHA256]++
	}
	if len(verified.OrderedTrial.ArtifactSHA256) == 0 {
		t.Fatal("incomplete evidence did not bind its crown ledger")
	}
	for _, artifact := range verified.Artifacts {
		if digestCounts[artifact.SHA256] != 1 {
			t.Fatalf("artifact digest %s was published %d times", artifact.SHA256, digestCounts[artifact.SHA256])
		}
	}
	if _, err := os.Stat(filepath.Join(publication.Directory, m1.TechnicalPassName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("technical pass signature exists: %v", err)
	}
}

func TestProgressSnapshotIsAtomicReadOnlyDiagnostic(t *testing.T) {
	root := t.TempDir()
	started := time.Now().UTC().Add(-time.Minute)
	want := newProgress("run-progress", "focused/concurrent-claims-nfr003", 21, "running", started, "")
	if err := writeProgress(root, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProgress(root, want.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != want.RunID || got.Phase != want.Phase || got.Completed != want.Completed || got.Outcome != "running" || !got.StartedAt.Equal(started) {
		t.Fatalf("progress changed: got=%+v want=%+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, want.RunID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("progress created authoritative run directory: %v", err)
	}
}
