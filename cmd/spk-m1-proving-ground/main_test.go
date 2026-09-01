package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/crown"
)

func TestCommandsEmitOneStableJSONEnvelopeAndExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		command string
	}{
		{"missing command", nil, exitInvalid, ""},
		{"unknown command", []string{"invent"}, exitInvalid, "invent"},
		{"relative preflight", []string{"preflight", "--repo", ".", "--run-id", "run", "--seed", "1"}, exitInvalid, "preflight"},
		{"status recovery", []string{"status", "--evidence", filepath.Join(t.TempDir(), "evidence"), "--run-id", "run-missing"}, exitRecovery, "status"},
		{"unexpected prose", []string{"verify", "human output"}, exitInvalid, "verify"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if got := execute(context.Background(), test.args, &output); got != test.code {
				t.Fatalf("exit=%d want=%d output=%s", got, test.code, output.String())
			}
			var got envelope
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatalf("non-JSON output: %v: %q", err, output.String())
			}
			if got.SchemaVersion != 1 || got.Command != test.command || got.Outcome == "" {
				t.Fatalf("unstable envelope: %+v", got)
			}
		})
	}
}

func TestCommandContextPropagatesCancellationToTheCrownRunner(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	ctx, stop := commandContext(parent)
	t.Cleanup(stop)
	cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatalf("command cancellation cause=%v", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("command context did not propagate cancellation")
	}
}

func TestStatusReportsActiveProgressWithoutMintingReadiness(t *testing.T) {
	root := t.TempDir()
	progress := crown.Progress{SchemaVersion: 1, RunID: "run-active", Phase: "focused/concurrent-claims-nfr003", Completed: 21, Total: len(m1.CrownSegmentIDs), Outcome: "running", StartedAt: time.Now().UTC().Add(-time.Minute), UpdatedAt: time.Now().UTC()}
	encoded, err := m1.CanonicalJSON(progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".status-run-active.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := execute(context.Background(), []string{"status", "--evidence", root, "--run-id", "run-active"}, &output); code != exitIncomplete {
		t.Fatalf("active status exit=%d output=%s", code, output.String())
	}
	var got envelope
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "running" {
		t.Fatalf("active progress was promoted or hidden: %+v", got)
	}
}

func TestStatusRecomputesTerminalOutcomeFromImmutableEvidence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "evidence")
	trial := m1.OrderedTrial{SchemaVersion: 1, RunID: "run-terminal", TrialID: m1.OrderedTrialID, Started: true, Terminal: true, Outcome: "incomplete"}
	crownArtifact, err := m1.SealCrownArtifact(m1.CrownArtifact{SchemaVersion: 1, RunID: trial.RunID, Trial: trial})
	if err != nil {
		t.Fatal(err)
	}
	crownBytes, _ := m1.CanonicalJSON(crownArtifact)
	manifest := m1.EvidenceManifest{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", RunID: trial.RunID, SourceCommit: strings.Repeat("1", 40), SourceTree: strings.Repeat("2", 40), AdmittedTupleSHA256: m1.AdmittedTupleSHA256, SidecarProvenanceSHA256: strings.Repeat("3", 64), ScenarioResults: []m1.ScenarioResult{}, FaultResults: []m1.FaultResult{}, OrderedTrial: crownArtifact.Trial, FocusedSubtrialSummaries: []m1.FocusedSummary{}, MutantResults: []m1.MutantResult{}, IndependentReview: m1.IndependentReview{Verdict: "fail"}, Outcome: "incomplete"}
	inputs := []m1.ArtifactInput{{Kind: "source-binding", Data: []byte("{}\n")}, {Kind: "sidecar-provenance", Data: []byte("{}\n")}, {Kind: "proving-binary", Data: []byte("binary")}, {Kind: "contracts", Data: testM1ContractsBundle(t)}, {Kind: "budgets", Data: []byte("budgets")}, {Kind: "crown-ledger", Data: crownBytes}}
	for i, id := range m1.ScenarioIDs {
		artifact := []byte("incomplete-scenario:" + id)
		manifest.ScenarioResults = append(manifest.ScenarioResults, m1.ScenarioResult{ScenarioID: id, RunID: trial.RunID, Seed: uint64(i + 1), Started: i == 0, Terminal: true, Outcome: "incomplete", ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "scenario", Data: artifact})
	}
	for i, id := range m1.FaultIDs {
		artifact := []byte("incomplete-fault:" + id)
		manifest.FaultResults = append(manifest.FaultResults, m1.FaultResult{FaultID: id, RunID: trial.RunID, Seed: uint64(i + 1), Cut: m1.FaultCuts[i], Terminal: true, Outcome: "incomplete", ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "fault", Data: artifact})
	}
	for _, id := range m1.MutantIDs {
		artifact := []byte("incomplete-mutant:" + id)
		manifest.MutantResults = append(manifest.MutantResults, m1.MutantResult{ID: id, RunID: trial.RunID, Outcome: "not_run", ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "mutant", Data: artifact})
	}
	if _, err := m1.PublishEvidence(root, manifest, inputs); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := execute(context.Background(), []string{"status", "--evidence", root, "--run-id", trial.RunID}, &output); code != exitIncomplete {
		t.Fatalf("terminal status exit=%d output=%s", code, output.String())
	}
	var got envelope
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "incomplete" {
		t.Fatalf("terminal outcome was not recomputed: %+v", got)
	}
}

func testM1ContractsBundle(t *testing.T) []byte {
	t.Helper()
	contractRoot := filepath.Join("..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	paths, err := filepath.Glob(filepath.Join(contractRoot, "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Path     string `json:"path"`
		SHA256   string `json:"sha256"`
		Contents []byte `json:"contents"`
	}
	bundle := struct {
		SchemaVersion int     `json:"schema_version"`
		Entries       []entry `json:"entries"`
	}{SchemaVersion: 1}
	for _, path := range paths {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		bundle.Entries = append(bundle.Entries, entry{Path: filepath.ToSlash(filepath.Join("kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", filepath.Base(path))), SHA256: m1.SHA256Hex(contents), Contents: contents})
	}
	sort.Slice(bundle.Entries, func(i, j int) bool { return bundle.Entries[i].Path < bundle.Entries[j].Path })
	encoded, err := m1.CanonicalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPublicKeyRequiresExplicitEd25519Bytes(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "human.pub")
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(public)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readPublicKey(path)
	if err != nil || !bytes.Equal(got, public) {
		t.Fatalf("key=%x err=%v", got, err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPublicKey(path); err == nil {
		t.Fatal("malformed public key was accepted")
	}

	t.Run("trust configuration is exact for each frozen signature kind", testAdoptionTrustConfigurationIsExactForEachFrozenSignatureKind)
}

func testAdoptionTrustConfigurationIsExactForEachFrozenSignatureKind(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "human.pub")
	if err := os.WriteFile(keyPath, []byte(base64.RawURLEncoding.EncodeToString(public)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	detached := m1.AdoptionRecord{Signature: m1.AdoptionSignature{Kind: "detached", Value: "signature"}}
	trusted, err := trustedAdoptionSigner(detached, m1.AccountableSigner, keyPath, "", "")
	if err != nil || !bytes.Equal(trusted.PublicKey, public) || len(trusted.LocalApprovals) != 0 {
		t.Fatalf("detached trust=%+v err=%v", trusted, err)
	}

	local := m1.AdoptionRecord{Signature: m1.AdoptionSignature{Kind: "approved-local-reference", Value: "local-approval-1"}}
	digest := strings.Repeat("a", 64)
	trusted, err = trustedAdoptionSigner(local, m1.AccountableSigner, "", local.Signature.Value, digest)
	if err != nil || len(trusted.PublicKey) != 0 || len(trusted.LocalApprovals) != 1 || trusted.LocalApprovals[0].Reference != local.Signature.Value || trusted.LocalApprovals[0].SigningBytesSHA256 != digest {
		t.Fatalf("local trust=%+v err=%v", trusted, err)
	}
	for _, test := range []struct {
		name, key, reference, digest string
	}{
		{"missing reference", "", "", digest},
		{"different reference", "", "other", digest},
		{"malformed digest", "", local.Signature.Value, "not-a-digest"},
		{"mixed public key", keyPath, local.Signature.Value, digest},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := trustedAdoptionSigner(local, m1.AccountableSigner, test.key, test.reference, test.digest); err == nil {
				t.Fatal("unsafe local trust configuration passed")
			}
		})
	}
}

func TestWriteExclusiveNeverOverwritesHumanArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adoption-template.json")
	if err := writeExclusive(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(path, []byte("second\n")); err == nil {
		t.Fatal("existing human artifact was overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "first\n" {
		t.Fatalf("artifact changed: %q err=%v", got, err)
	}
}
