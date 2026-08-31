package m1

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidencePublishesAndIndependentlyVerifiesExactCensus(t *testing.T) {
	manifest, inputs, source := validEvidence(t, "run-evidence")
	publication, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	verified, got, err := VerifyEvidence(t.Context(), publication.Directory, VerifyOptions{ExpectedSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	if !verified.AdoptionEligible || verified.Outcome != "pass" || verified.ManifestSHA256 != publication.ManifestSHA256 || verified.TechnicalPass != publication.TechnicalPass || got.RunID != manifest.RunID {
		t.Fatalf("verification=%+v manifest=%+v", verified, got)
	}
}

func TestEvidenceSemanticMutantsCannotMintPass(t *testing.T) {
	base, _, _ := validEvidence(t, "run-mutants")
	mutants := []func(*EvidenceManifest){
		func(m *EvidenceManifest) { m.ScenarioResults = m.ScenarioResults[1:] },
		func(m *EvidenceManifest) { m.ScenarioResults[1].ScenarioID = m.ScenarioResults[0].ScenarioID },
		func(m *EvidenceManifest) { m.FaultResults[2].RunID = "foreign" },
		func(m *EvidenceManifest) { m.FocusedSubtrialSummaries[0].RepetitionsPassed = 99 },
		func(m *EvidenceManifest) { m.MutantResults[18].Outcome = "survived" },
		func(m *EvidenceManifest) { m.InvariantSummary.StaleAuthorityRefused = false },
		func(m *EvidenceManifest) { m.OrderedTrial.Reviews[0].ReviewerActorID = "B" },
		func(m *EvidenceManifest) { m.OrderedTrial.RestorePlayback.PlaybackDurableWrites = 1 },
		func(m *EvidenceManifest) { m.IndependentReview.Verdict = "incomplete" },
	}
	for i, mutate := range mutants {
		candidate := base
		candidate.ScenarioResults = append([]ScenarioResult(nil), base.ScenarioResults...)
		candidate.FaultResults = append([]FaultResult(nil), base.FaultResults...)
		candidate.FocusedSubtrialSummaries = append([]FocusedSummary(nil), base.FocusedSubtrialSummaries...)
		candidate.MutantResults = append([]MutantResult(nil), base.MutantResults...)
		candidate.OrderedTrial.Reviews = append([]CrownReview(nil), base.OrderedTrial.Reviews...)
		mutate(&candidate)
		if err := SemanticOutcome(candidate); err == nil {
			t.Fatalf("semantic mutant %d passed", i)
		}
	}
}

func TestEvidenceArtifactMutationTraversalAndSymlinkFail(t *testing.T) {
	manifest, inputs, source := validEvidence(t, "run-artifacts")
	publication, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if cleanArtifactPath("../escape") || cleanArtifactPath("artifacts/../escape") || cleanArtifactPath("/absolute") || cleanArtifactPath("artifacts\\escape") {
		t.Fatal("unsafe artifact path passed")
	}
	member := filepath.Join(publication.Directory, "artifacts", "001-source-binding.json")
	if err := os.Chmod(member, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(member, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyEvidence(t.Context(), publication.Directory, VerifyOptions{ExpectedSource: &source}); err == nil {
		t.Fatal("changed artifact passed")
	}

	manifest, inputs, source = validEvidence(t, "run-symlink")
	publication, err = PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	member = filepath.Join(publication.Directory, "artifacts", "001-source-binding.json")
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", member); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyEvidence(context.Background(), publication.Directory, VerifyOptions{ExpectedSource: &source}); err == nil {
		t.Fatal("symlinked artifact passed")
	}
}

func TestEvidenceNonPassNeverPublishesTechnicalSignature(t *testing.T) {
	manifest, inputs, _ := validEvidence(t, "run-incomplete")
	manifest.Outcome = "incomplete"
	manifest.ScenarioResults[4].Started, manifest.ScenarioResults[4].Terminal, manifest.ScenarioResults[4].Outcome = false, false, "incomplete"
	publication, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if publication.TechnicalPass != "" {
		t.Fatal("incomplete evidence received a pass signature")
	}
	if _, err := os.Stat(filepath.Join(publication.Directory, TechnicalPassName)); !os.IsNotExist(err) {
		t.Fatalf("technical pass exists: %v", err)
	}
}

func validEvidence(t *testing.T, runID string) (EvidenceManifest, []ArtifactInput, SourceBinding) {
	t.Helper()
	source := SourceBinding{Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), WorktreeTree: strings.Repeat("2", 40), WorktreeStatusSHA256: emptySHA256, AdmittedTupleSHA256: AdmittedTupleSHA256, SidecarProvenanceSHA256: strings.Repeat("3", 64), SidecarExecutableSHA256: AdmittedSidecarSHA256, DatabaseID: "database-one", OwnerPID: 123}
	source.seal = sourceSeal(source)
	crown := validCrownArtifact(t, runID)
	crownBytes, err := CanonicalJSON(crown)
	if err != nil {
		t.Fatal(err)
	}
	trial := crown.Trial
	inputs := []ArtifactInput{
		{Kind: "source-binding", Data: mustJSON(t, source)}, {Kind: "sidecar-provenance", Data: []byte("{\"schema_version\":1}\n")},
		{Kind: "proving-binary", Data: []byte("synthetic proving binary")}, {Kind: "contracts", Data: []byte("contracts-v1")},
		{Kind: "budgets", Data: []byte("budgets-v1")}, {Kind: "crown-ledger", Data: crownBytes},
	}
	manifest := EvidenceManifest{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree, AdmittedTupleSHA256: AdmittedTupleSHA256, SidecarProvenanceSHA256: source.SidecarProvenanceSHA256, OrderedTrial: trial, InvariantSummary: EvidenceInvariantSummary{true, true, true, true, true, true, true, true}, Outcome: "pass"}
	for i, id := range ScenarioIDs {
		artifact := mustJSON(t, map[string]any{"run_id": runID, "scenario_id": id, "sequence": i + 1})
		inputs = append(inputs, ArtifactInput{Kind: "scenario", Data: artifact})
		manifest.ScenarioResults = append(manifest.ScenarioResults, ScenarioResult{id, runID, uint64(i + 1), true, true, "pass", SHA256Hex(artifact)})
	}
	for i, id := range FaultIDs {
		artifact := mustJSON(t, map[string]any{"run_id": runID, "fault_id": id, "cut": FaultCuts[i]})
		inputs = append(inputs, ArtifactInput{Kind: "fault", Data: artifact})
		manifest.FaultResults = append(manifest.FaultResults, FaultResult{id, runID, uint64(100 + i), FaultCuts[i], true, true, "pass", SHA256Hex(artifact)})
	}
	for _, id := range FocusedIDs {
		artifact := mustJSON(t, map[string]any{"run_id": runID, "focused_id": id, "passed": 100})
		inputs = append(inputs, ArtifactInput{Kind: "focused", Data: artifact})
		manifest.FocusedSubtrialSummaries = append(manifest.FocusedSubtrialSummaries, FocusedSummary{id, runID, 100, 100, 100, SHA256Hex(artifact)})
	}
	for _, id := range MutantIDs {
		artifact := mustJSON(t, map[string]any{"run_id": runID, "mutant_id": id, "outcome": "killed"})
		inputs = append(inputs, ArtifactInput{Kind: "mutant", Data: artifact})
		manifest.MutantResults = append(manifest.MutantResults, MutantResult{id, runID, "killed", SHA256Hex(artifact)})
	}
	reviewArtifact := mustJSON(t, map[string]any{"run_id": runID, "reviewer_actor_id": "campaign-reviewer", "verdict": "pass"})
	inputs = append(inputs, ArtifactInput{Kind: "independent-review", Data: reviewArtifact})
	manifest.IndependentReview = IndependentReview{"campaign-reviewer", "campaign-review-process", "pass", SHA256Hex(reviewArtifact)}
	return manifest, inputs, source
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
