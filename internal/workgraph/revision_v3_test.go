package workgraph

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRevisionV3PinsExactArtifactsAndCompleteProviderEvidence(t *testing.T) {
	observation := finalizationObservationFixture(t)
	revision, err := BuildGenerationOneCandidateV3(GenerationOneCandidateV3Params{Observation: observation, PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("b", 64)}, DependencyPolicy: DependencyApprovedOrDone, FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC), GovernanceContextID: "governance-v3"})
	if err != nil {
		t.Fatal(err)
	}
	if revision.SchemaVersion() != RevisionSchemaV3 || !strings.HasPrefix(revision.ID().String(), "wrv3-") || len(revision.ContentDigest()) != 64 {
		t.Fatalf("revision is not content-addressed v3: %s/%d/%s", revision.ID(), revision.SchemaVersion(), revision.ContentDigest())
	}
	artifacts := revision.ArtifactObservations()
	if len(artifacts) != 1 || artifacts[0].Kind != "plan" || artifacts[0].ArtifactRevisionID != observation.Draft().PlanRevisionID() || artifacts[0].ContentDigest != observation.Draft().PlanDigest() {
		t.Fatalf("plan artifact was not pinned: %+v", artifacts)
	}
	evidence := revision.ProviderEvidence()
	if len(evidence) != len(observation.Evidence()) || evidence[0].MetadataSchema == 0 || evidence[0].DefinitionDigest == "" {
		t.Fatalf("provider evidence is incomplete: %+v", evidence)
	}
	encoded, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeRevisionV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := restored.Encode()
	if !bytes.Equal(encoded, again) || restored.ID() != revision.ID() || restored.ContentDigest() != revision.ContentDigest() {
		t.Fatal("v3 round trip changed immutable authority")
	}
	evidence[0].Revision = "caller-mutated"
	evidence[0].CriterionEvidenceHints["criterion-work-a"] = "mutated"
	artifacts[0].ContentDigest = strings.Repeat("0", 64)
	defended, _ := revision.Encode()
	if !bytes.Equal(encoded, defended) {
		t.Fatal("v3 collections are not defensive")
	}
}

func TestRevisionV3SeparatesContentIdentityFromProviderSemanticNoise(t *testing.T) {
	base := finalizationObservationFixture(t)
	first := buildV3FromObservation(t, base)
	changed := base.Evidence()
	changed[0].Revision = "provider-revision-changed"
	noise, err := NewFinalizationObservation(base.Draft(), changed)
	if err != nil {
		t.Fatal(err)
	}
	second := buildV3FromObservation(t, noise)
	if first.ID() == second.ID() || first.ContentDigest() == second.ContentDigest() {
		t.Fatal("provider evidence was omitted from content identity")
	}
	if first.GraphDigest() != second.GraphDigest() {
		t.Fatal("provider concurrency revision became semantic graph authority")
	}
	changedDraft, err := NewDraftObservation(base.Draft().ProjectID(), base.Draft().MissionID(), "plan-revision-changed", strings.Repeat("c", 64), base.Draft().ApprovedScope(), base.Draft().Work())
	if err != nil {
		t.Fatal(err)
	}
	changedPlan, err := NewFinalizationObservation(changedDraft, base.Evidence())
	if err != nil {
		t.Fatal(err)
	}
	third := buildV3FromObservation(t, changedPlan)
	if first.GraphDigest() == third.GraphDigest() || first.ID() == third.ID() {
		t.Fatal("artifact definition change was not semantic and content-addressed")
	}
}

func TestRevisionV3ContentIdentityCommitsFinalizationProvenance(t *testing.T) {
	base := revisionV3ParamsFromObservation(t, finalizationObservationFixture(t))
	first, err := NewRevisionV3(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*RevisionV3Params){
		func(value *RevisionV3Params) { value.FinalizedBy = "actor-other" },
		func(value *RevisionV3Params) { value.FinalizedAt = value.FinalizedAt.Add(time.Second) },
		func(value *RevisionV3Params) { value.GovernanceContextID = "governance-other" },
	}
	for index, mutate := range mutations {
		candidate := revisionV3ParamsFromObservation(t, finalizationObservationFixture(t))
		mutate(&candidate)
		changed, buildErr := NewRevisionV3(candidate)
		if buildErr != nil {
			t.Fatalf("mutation %d: %v", index, buildErr)
		}
		if changed.ID() == first.ID() || changed.ContentDigest() == first.ContentDigest() {
			t.Fatalf("finalization provenance mutation %d did not change content identity", index)
		}
		if changed.GraphDigest() != first.GraphDigest() {
			t.Fatalf("finalization provenance mutation %d changed executable graph identity", index)
		}
	}
}

func TestRevisionV3RefusesIncompleteAndV2CannotBecomeNewAuthority(t *testing.T) {
	v2, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{Context: workgraphV2Context("legacy"), Revision: v2}); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("historical v2 became executable M2 authority: %v", err)
	}
	params := revisionV3ParamsFromObservation(t, finalizationObservationFixture(t))
	params.ArtifactObservations = nil
	if _, err := NewRevisionV3(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("missing artifact observation admitted: %v", err)
	}
	params = revisionV3ParamsFromObservation(t, finalizationObservationFixture(t))
	params.ProviderEvidence[0].DefinitionDigest = ""
	if _, err := NewRevisionV3(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("incomplete provider evidence admitted: %v", err)
	}
}

func buildV3FromObservation(t *testing.T, observation FinalizationObservation) RevisionV2 {
	t.Helper()
	revision, err := BuildGenerationOneCandidateV3(GenerationOneCandidateV3Params{Observation: observation, PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("b", 64)}, DependencyPolicy: DependencyApprovedOrDone, FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC), GovernanceContextID: "governance-v3"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func revisionV3ParamsFromObservation(t *testing.T, observation FinalizationObservation) RevisionV3Params {
	t.Helper()
	revision := buildV3FromObservation(t, observation)
	items := make([]WorkItemV2Params, 0, len(revision.WorkItems()))
	for _, item := range revision.WorkItems() {
		criteria := make([]CriterionV2Params, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			metadata := criterion.Metadata()
			outcome, _ := metadata[CriterionMetadataOutcome].(string)
			hint, _ := metadata[CriterionMetadataEvidenceHint].(string)
			criteria = append(criteria, CriterionV2Params{ID: criterion.ID(), Text: criterion.Text(), VerifierKind: criterion.VerifierKind(), Required: criterion.Required(), Outcome: outcome, EvidenceHint: hint})
		}
		items = append(items, WorkItemV2Params{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: item.WriteScopes()})
	}
	return RevisionV3Params{ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), Generation: 1, PlanningSourceCommit: revision.PlanningSourceCommit(), MissionDefinition: revision.MissionDefinition(), DependencyPolicy: revision.DependencyPolicy(), WorkItems: items, Lanes: revision.Lanes(), ArtifactObservations: revision.ArtifactObservations(), ProviderEvidence: revision.ProviderEvidence(), FinalizedBy: revision.FinalizedBy(), FinalizedAt: revision.FinalizedAt(), GovernanceContextID: revision.GovernanceContextID()}
}
