package m1

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEvidencePublishesAndIndependentlyVerifiesExactCensus(t *testing.T) {
	manifest, inputs, source := validEvidence(t, "run-evidence")
	publication, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(publication.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var exactManifest any
	if err := json.Unmarshal(manifestBytes, &exactManifest); err != nil {
		t.Fatal(err)
	}
	if err := compileCheckedInEvidenceSchema(t).Validate(exactManifest); err != nil {
		t.Fatalf("exact production-published manifest violates the frozen schema: %v", err)
	}
	verified, got, err := VerifyEvidence(t.Context(), publication.Directory, VerifyOptions{ExpectedSource: &source})
	if err != nil {
		t.Fatal(err)
	}
	if !verified.AdoptionEligible || verified.Outcome != "pass" || verified.ManifestSHA256 != publication.ManifestSHA256 || verified.TechnicalPass != publication.TechnicalPass || got.RunID != manifest.RunID {
		t.Fatalf("verification=%+v manifest=%+v", verified, got)
	}
	want := CrownIdentities{CrownParticipant{"A", "A", "W1"}, CrownParticipant{"B", "B", "W2"}, CrownParticipant{"A", "C", "W3"}}
	if got.OrderedTrial.Identities != want {
		t.Fatalf("published participant vocabulary=%+v want=%+v", got.OrderedTrial.Identities, want)
	}

	t.Run("publisher and verifier reject frozen schema drift", testEvidencePublisherAndVerifierRejectFrozenSchemaDrift)
}

func testEvidencePublisherAndVerifierRejectFrozenSchemaDrift(t *testing.T) {
	manifest, inputs, _ := validEvidence(t, "run schema drift")
	if _, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("schema-invalid RunID was published: %v", err)
	}

	manifest, inputs, _ = validEvidence(t, "run-schema-verifier")
	publication, err := PublishEvidence(filepath.Join(t.TempDir(), "evidence"), manifest, inputs)
	if err != nil {
		t.Fatal(err)
	}
	publishedBytes, err := os.ReadFile(publication.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshalStrict(publishedBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SourceCommit = strings.Repeat("A", 40) // SemanticOutcome accepts hex; the frozen schema requires lowercase Git IDs.
	manifestBytes, err := CanonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(publication.ManifestPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publication.ManifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	signaturePath := filepath.Join(publication.Directory, TechnicalPassName)
	if err := os.Chmod(signaturePath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, []byte(TechnicalSignature(SHA256Hex(manifestBytes))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyEvidence(t.Context(), publication.Directory, VerifyOptions{}); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("exact schema-invalid manifest passed independent verification: %v", err)
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
	provenance := []byte("{\"schema_version\":1}\n")
	provenanceDigest := SHA256Hex(bytes.TrimSuffix(provenance, []byte{'\n'}))
	source := SourceBinding{Commit: strings.Repeat("1", 40), Tree: strings.Repeat("2", 40), WorktreeTree: strings.Repeat("2", 40), WorktreeStatusSHA256: emptySHA256, AdmittedTupleSHA256: AdmittedTupleSHA256, SidecarProvenanceSHA256: provenanceDigest, SidecarExecutableSHA256: AdmittedSidecarSHA256, DatabaseID: "database-one", OwnerPID: 123}
	source.seal = sourceSeal(source)
	crown := validCrownArtifact(t, runID)
	crownBytes, err := CanonicalJSON(crown)
	if err != nil {
		t.Fatal(err)
	}
	trial := crown.Trial
	inputs := []ArtifactInput{
		{Kind: "source-binding", Data: mustJSON(t, source)}, {Kind: "sidecar-provenance", Data: provenance},
		{Kind: "proving-binary", Data: []byte("synthetic proving binary")}, {Kind: "contracts", Data: testContractsBundle(t)},
		{Kind: "budgets", Data: []byte("budgets-v1")}, {Kind: "crown-ledger", Data: crownBytes},
	}
	for _, id := range CrownSegmentIDs {
		inputs = append(inputs, ArtifactInput{Kind: "crown-segment", Data: crownSegmentFixture(id)})
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

func testContractsBundle(t *testing.T) []byte {
	t.Helper()
	contractRoot := filepath.Join("..", "..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
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
		bundle.Entries = append(bundle.Entries, entry{Path: filepath.ToSlash(filepath.Join("kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", filepath.Base(path))), SHA256: SHA256Hex(contents), Contents: contents})
	}
	sort.Slice(bundle.Entries, func(i, j int) bool { return bundle.Entries[i].Path < bundle.Entries[j].Path })
	return mustJSON(t, bundle)
}

func compileCheckedInEvidenceSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	contractRoot := filepath.Join("..", "..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	paths, err := filepath.Glob(filepath.Join(contractRoot, "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document any
		if err := json.Unmarshal(contents, &document); err != nil {
			t.Fatal(err)
		}
		id := document.(map[string]any)["$id"].(string)
		if err := compiler.AddResource(id, document); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile(evidenceManifestSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestEvidenceCanonicalFixtures(t *testing.T) {
	for _, test := range []struct {
		name            string
		outcome         string
		semanticPass    bool
		orderedTerminal string
	}{
		{name: "pass", outcome: "pass", semanticPass: true, orderedTerminal: "pass"},
		{name: "incomplete", outcome: "incomplete", semanticPass: false, orderedTerminal: "incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "evidence", test.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest EvidenceManifest
			if err := jsonUnmarshalStrict(contents, &manifest); err != nil {
				t.Fatal(err)
			}
			canonical, err := CanonicalJSON(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(contents, canonical) {
				t.Fatal("fixture is not canonical JSON")
			}
			if manifest.Outcome != test.outcome || manifest.OrderedTrial.Outcome != test.orderedTerminal {
				t.Fatalf("fixture outcomes manifest=%q trial=%q", manifest.Outcome, manifest.OrderedTrial.Outcome)
			}
			if err := SemanticOutcome(manifest); (err == nil) != test.semanticPass {
				t.Fatalf("semantic pass=%t error=%v", err == nil, err)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
