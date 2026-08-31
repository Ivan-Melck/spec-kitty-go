package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
	manifest := m1.EvidenceManifest{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", RunID: trial.RunID, AdmittedTupleSHA256: m1.AdmittedTupleSHA256, OrderedTrial: crownArtifact.Trial, IndependentReview: m1.IndependentReview{Verdict: "fail"}, Outcome: "incomplete"}
	inputs := []m1.ArtifactInput{{Kind: "source-binding", Data: []byte("{}\n")}, {Kind: "sidecar-provenance", Data: []byte("{}\n")}, {Kind: "proving-binary", Data: []byte("binary")}, {Kind: "contracts", Data: []byte("contracts")}, {Kind: "budgets", Data: []byte("budgets")}, {Kind: "crown-ledger", Data: crownBytes}}
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
