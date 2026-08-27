package workgraph

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestGenerationOneCandidateValidatesAndPinsCompleteEvidence(t *testing.T) {
	observation := finalizationObservationFixture(t)
	candidate, err := BuildGenerationOneCandidate(GenerationOneCandidateParams{ID: "revision-final", Observation: observation, PlanningSourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, DependencyPolicy: DependencyApprovedOrDone, FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC), GovernanceContextID: "governance-final"})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Generation() != 1 || candidate.PredecessorID() != "" || len(candidate.Lanes()) != 2 || len(candidate.ProviderObservations()) != 2 {
		t.Fatalf("incomplete candidate: %#v", candidate)
	}
	if candidate.WorkItems()[0].Criteria()[0].Metadata()[CriterionMetadataEvidenceHint] != "go test output" {
		t.Fatal("criterion evidence was not projected")
	}
}

func TestFinalizationObservationRecheckDetectsEveryBoundaryChange(t *testing.T) {
	first := finalizationObservationFixture(t)
	if err := CompareFinalizationObservations(first, finalizationObservationFixture(t)); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*FinalizationObservation){
		"revision":        func(value *FinalizationObservation) { value.evidence[0].Revision = "changed" },
		"mapping":         func(value *FinalizationObservation) { value.evidence[0].ExternalID = "rebound" },
		"metadata schema": func(value *FinalizationObservation) { value.evidence[0].MetadataSchema++ },
		"definition": func(value *FinalizationObservation) {
			value.evidence[0].DefinitionDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		},
		"criterion evidence": func(value *FinalizationObservation) {
			value.evidence[0].CriterionEvidenceHints["criterion-work-a"] = "different evidence"
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			second := finalizationObservationFixture(t)
			mutate(&second)
			second.canonical, _ = canonicalFinalizationObservation(second.draft, second.evidence)
			if err := CompareFinalizationObservations(first, second); !IsKind(err, KindProviderChanged) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestFinalizationObservationRejectsIncompleteCoverageAndDefendsCopies(t *testing.T) {
	full := finalizationObservationFixture(t)
	if _, err := NewFinalizationObservation(full.Draft(), full.Evidence()[:1]); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("incomplete coverage = %v", err)
	}
	evidence := full.Evidence()
	evidence[0].Revision = "mutated"
	evidence[0].CriterionEvidenceHints["criterion-work-a"] = "mutated"
	canonical := full.CanonicalEvidence()
	canonical[0] ^= 0xff
	if err := CompareFinalizationObservations(full, finalizationObservationFixture(t)); err != nil {
		t.Fatal("caller mutated observation")
	}
}

func TestCandidateRefusesSemanticAndLaneCycleMutants(t *testing.T) {
	invalid := finalizationObservationFixture(t)
	work := invalid.draft.Work()
	params := WorkObservationParams{ProjectID: work[0].ProjectID(), WorkID: work[0].WorkID(), Title: " ", Description: work[0].Description(), DesignContext: work[0].DesignContext(), Criteria: work[0].Criteria(), Dependencies: work[0].Dependencies(), WriteScopes: work[0].WriteScopes(), Provenance: work[0].Provenance()}
	mutated, _ := NewWorkObservation(params)
	draft, _ := NewDraftObservation(invalid.draft.ProjectID(), invalid.draft.MissionID(), invalid.draft.PlanRevisionID(), invalid.draft.PlanDigest(), invalid.draft.ApprovedScope(), []WorkObservation{mutated, work[1]})
	invalid.draft = draft
	invalid.canonical, _ = canonicalFinalizationObservation(draft, invalid.evidence)
	if _, err := BuildGenerationOneCandidate(GenerationOneCandidateParams{ID: "revision-invalid", Observation: invalid, PlanningSourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, DependencyPolicy: DependencyApprovedOrDone, FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC), GovernanceContextID: "governance-final"}); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("semantic mutant = %v", err)
	}
}

func finalizationObservationFixture(t *testing.T) FinalizationObservation {
	t.Helper()
	draft := laneObservation(t, []laneWork{{id: "work-a", path: "internal/a"}, {id: "work-b", path: "internal/b", dependencies: []ids.WorkID{"work-a"}}})
	evidence := []ObservationEvidence{{WorkID: "work-a", Provider: "durable_fake", ExternalID: "external-a", Revision: "revision-1", MetadataSchema: 1, DefinitionDigest: definitionDigest("work-a"), CriterionEvidenceHints: map[string]string{"criterion-work-a": "go test output"}}, {WorkID: "work-b", Provider: "durable_fake", ExternalID: "external-b", Revision: "revision-2", MetadataSchema: 1, DefinitionDigest: definitionDigest("work-b"), CriterionEvidenceHints: map[string]string{"criterion-work-b": "review record"}}}
	observation, err := NewFinalizationObservation(draft, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
func definitionDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}
