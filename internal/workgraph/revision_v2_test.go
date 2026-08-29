package workgraph

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestRevisionV2RoundTripsStrictlyAndDefendsEveryCollection(t *testing.T) {
	params := revisionV2Fixture()
	revision, err := NewRevisionV2(params)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := revision.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeRevisionV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, _ := restored.Encode()
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", encoded, reencoded)
	}
	if len(restored.GraphDigest()) != 64 || strings.HasPrefix(restored.GraphDigest(), "sha256:") || restored.SchemaVersion() != 2 {
		t.Fatalf("invalid v2 digest/version: %q/%d", restored.GraphDigest(), restored.SchemaVersion())
	}
	criteria := restored.WorkItems()[0].Criteria()
	if criteria[0].Metadata()[CriterionMetadataOutcome] != "The unit test passes." || criteria[0].Metadata()[CriterionMetadataEvidenceHint] != "go test output" {
		t.Fatalf("reserved semantics were not preserved: %#v", criteria[0].Metadata())
	}
	if got := restored.WorkItems()[0].WriteScopes()[0]; got.Path != "internal/a" || got.Domain != "workgraph" || got.Access != ScopeWriteExclusive || got.SerializationKey != "serial-a" {
		t.Fatalf("scope did not round trip losslessly: %#v", got)
	}

	params.WorkItems[0].Criteria[0].Metadata["nested"].(map[string]any)["key"] = "mutated"
	params.WorkItems[0].Dependencies = append(params.WorkItems[0].Dependencies, "work-z")
	params.Lanes[0].workIDs[0] = "work-z"
	params.ProviderObservations[0].Revision = "changed"
	returned := restored.WorkItems()
	returned[0].criteria[0].metadata[CriterionMetadataOutcome] = "mutated"
	returned[0].writeScopes[0].Path = "changed"
	lanes := restored.Lanes()
	lanes[0].workIDs[0] = "work-z"
	observations := restored.ProviderObservations()
	observations[0].Revision = "changed"
	canonical := restored.CanonicalGraph()
	canonical[0] ^= 0xff
	again, _ := restored.Encode()
	if !bytes.Equal(encoded, again) {
		t.Fatal("caller mutation altered immutable revision")
	}
}

func TestRevisionV2GenerationAndStrictSchemaRefusals(t *testing.T) {
	params := revisionV2Fixture()
	params.Generation = 2
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("missing predecessor = %v", err)
	}
	params.PredecessorID = "revision-predecessor"
	if _, err := NewRevisionV2(params); err != nil {
		t.Fatalf("valid successor is not constructible: %v", err)
	}
	params.Generation = 1
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("generation-1 predecessor = %v", err)
	}

	valid, _ := NewRevisionV2(revisionV2Fixture())
	encoded, _ := valid.Encode()
	mutants := map[string][]byte{
		"future version":         bytes.Replace(encoded, []byte(`"schema_version":2`), []byte(`"schema_version":3`), 1),
		"unknown top level":      append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"surprise":true}`)...),
		"duplicate key":          bytes.Replace(encoded, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1),
		"trailing value":         append(append([]byte(nil), encoded...), []byte(` {}`)...),
		"tampered digest":        bytes.Replace(encoded, []byte(valid.GraphDigest()), []byte(strings.Repeat("0", 64)), 1),
		"null work items":        bytes.Replace(encoded, mustJSONField(t, encoded, "work_items"), []byte("null"), 1),
		"null lanes":             bytes.Replace(encoded, mustJSONField(t, encoded, "lanes"), []byte("null"), 1),
		"null observations":      bytes.Replace(encoded, mustJSONField(t, encoded, "provider_observations"), []byte("null"), 1),
		"null dependencies":      bytes.Replace(encoded, []byte(`"dependencies":[]`), []byte(`"dependencies":null`), 1),
		"null metadata":          bytes.Replace(encoded, mustFirstMetadata(t, encoded), []byte("null"), 1),
		"null lane dependencies": bytes.Replace(encoded, []byte(`"dependency_lane_ids":[]`), []byte(`"dependency_lane_ids":null`), 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRevisionV2(mutant); !IsKind(err, KindInvalidRevision) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRevisionV2StrictScalarPresenceTypeAndNullability(t *testing.T) {
	params := revisionV2Fixture()
	params.WorkItems[0].Criteria[0].Required = false
	params.WorkItems[0].DesignContext = ""
	revision, err := NewRevisionV2(params)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := revision.Encode()
	mutants := map[string][]byte{
		"required null":          bytes.Replace(encoded, []byte(`"required":false`), []byte(`"required":null`), 1),
		"required missing":       bytes.Replace(encoded, []byte(`,"required":false`), nil, 1),
		"required wrong type":    bytes.Replace(encoded, []byte(`"required":false`), []byte(`"required":"false"`), 1),
		"design context null":    bytes.Replace(encoded, []byte(`"description":"Implement the first bounded unit."`), []byte(`"description":"Implement the first bounded unit.","design_context":null`), 1),
		"predecessor null":       bytes.Replace(encoded, []byte(`"generation":1`), []byte(`"generation":1,"predecessor_id":null`), 1),
		"finalized by null":      bytes.Replace(encoded, []byte(`"finalized_by":"actor-finalizer"`), []byte(`"finalized_by":null`), 1),
		"mission version null":   bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":null`), 1),
		"criterion text missing": bytes.Replace(encoded, []byte(`"text":"Run the unit test.",`), nil, 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRevisionV2(mutant); !IsKind(err, KindInvalidRevision) {
				t.Fatalf("shape mutant accepted: %s", mutant)
			}
		})
	}
}

func TestRevisionV2RejectsDuplicateExternalMappingButAllowsSharedRevision(t *testing.T) {
	params := revisionV2Fixture()
	params.ProviderObservations[1].Provider = params.ProviderObservations[0].Provider
	params.ProviderObservations[1].ExternalID = params.ProviderObservations[0].ExternalID
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("duplicate mapping = %v", err)
	}
	params = revisionV2Fixture()
	params.ProviderObservations[1].Revision = params.ProviderObservations[0].Revision
	if _, err := NewRevisionV2(params); err != nil {
		t.Fatalf("shared revision across distinct mappings refused: %v", err)
	}
}

func mustJSONField(t *testing.T, encoded []byte, field string) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	return root[field]
}

func mustFirstMetadata(t *testing.T, encoded []byte) []byte {
	t.Helper()
	var root struct {
		WorkItems []struct {
			Criteria []struct {
				Metadata json.RawMessage `json:"metadata"`
			} `json:"criteria"`
		} `json:"work_items"`
	}
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	return root.WorkItems[0].Criteria[0].Metadata
}

func TestRevisionV2SemanticDigestsSeparateMappingFromOpaqueRevision(t *testing.T) {
	baseParams := revisionV2Fixture()
	base, err := NewRevisionV2(baseParams)
	if err != nil {
		t.Fatal(err)
	}
	revisionNoise := revisionV2Fixture()
	revisionNoise.ProviderObservations[0].Revision = "provider-revision-999"
	noise, err := NewRevisionV2(revisionNoise)
	if err != nil {
		t.Fatal(err)
	}
	if base.GraphDigest() != noise.GraphDigest() || base.CriteriaDigest() != noise.CriteriaDigest() {
		t.Fatal("opaque revision noise changed semantic digests")
	}
	baseBytes, _ := base.Encode()
	noiseBytes, _ := noise.Encode()
	if bytes.Equal(baseBytes, noiseBytes) {
		t.Fatal("opaque observation evidence was not pinned")
	}
	mappingChange := revisionV2Fixture()
	mappingChange.ProviderObservations[0].ExternalID = "external-a-rebound"
	mapped, err := NewRevisionV2(mappingChange)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.GraphDigest() == base.GraphDigest() {
		t.Fatal("provider mapping semantics did not change graph digest")
	}
	criterionChange := revisionV2Fixture()
	criterionChange.WorkItems[0].Criteria[0].Outcome = "A different observable outcome."
	changed, err := NewRevisionV2(criterionChange)
	if err != nil {
		t.Fatal(err)
	}
	if changed.GraphDigest() == base.GraphDigest() || changed.CriteriaDigest() == base.CriteriaDigest() {
		t.Fatal("criterion outcome did not change both semantic digests")
	}
	scopeChange := revisionV2Fixture()
	scopeChange.WorkItems[0].WriteScopes[0].SerializationKey = "serial-other"
	scoped, err := NewRevisionV2(scopeChange)
	if err != nil {
		t.Fatal(err)
	}
	if scoped.GraphDigest() == base.GraphDigest() {
		t.Fatal("scope semantics did not change graph digest")
	}
}

func TestRevisionV2GraphDigestCoversEverySemanticField(t *testing.T) {
	base, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*RevisionV2Params){
		"title":                   func(p *RevisionV2Params) { p.WorkItems[0].Title = "Changed title" },
		"description":             func(p *RevisionV2Params) { p.WorkItems[0].Description = "Changed standalone description." },
		"design context":          func(p *RevisionV2Params) { p.WorkItems[0].DesignContext = "Changed approved design." },
		"criterion text":          func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].Text = "Run a different test." },
		"criterion outcome":       func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].Outcome = "A different outcome is observed." },
		"criterion required":      func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].Required = false },
		"criterion verifier":      func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].VerifierKind = "review" },
		"criterion metadata":      func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].Metadata["new_key"] = "new value" },
		"criterion evidence hint": func(p *RevisionV2Params) { p.WorkItems[0].Criteria[0].EvidenceHint = "different bounded evidence" },
		"dependency": func(p *RevisionV2Params) {
			p.WorkItems[1].Dependencies = []ids.WorkID{}
			p.Lanes[1].dependencyLaneIDs = []ids.LaneID{}
		},
		"scope path":          func(p *RevisionV2Params) { p.WorkItems[0].WriteScopes[0].Path = "internal/a2" },
		"scope domain":        func(p *RevisionV2Params) { p.WorkItems[0].WriteScopes[0].Domain = "changed-domain" },
		"scope access":        func(p *RevisionV2Params) { p.WorkItems[0].WriteScopes[0].Access = ScopeReadOnly },
		"scope serialization": func(p *RevisionV2Params) { p.WorkItems[0].WriteScopes[0].SerializationKey = "changed-serial" },
		"provider mapping":    func(p *RevisionV2Params) { p.ProviderObservations[0].Provider = "other-provider" },
		"external mapping":    func(p *RevisionV2Params) { p.ProviderObservations[0].ExternalID = "other-external" },
		"planning identity":   func(p *RevisionV2Params) { p.PlanningSourceCommit = strings.Repeat("c", 40) },
		"mission identity":    func(p *RevisionV2Params) { p.MissionDefinition.Digest = strings.Repeat("d", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			params := revisionV2Fixture()
			mutate(&params)
			got, buildErr := NewRevisionV2(params)
			if buildErr != nil {
				t.Fatalf("valid semantic mutant refused: %v", buildErr)
			}
			if got.GraphDigest() == base.GraphDigest() {
				t.Fatal("semantic mutation did not change graph digest")
			}
			if strings.HasPrefix(name, "criterion ") && got.CriteriaDigest() == base.CriteriaDigest() {
				t.Fatal("criterion mutation did not change complete criteria digest")
			}
		})
	}
	invalidPolicy := revisionV2Fixture()
	invalidPolicy.DependencyPolicy = "future-policy"
	if _, err := NewRevisionV2(invalidPolicy); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("unsupported dependency policy = %v", err)
	}

	for name, mutate := range map[string]func(*RevisionV2Params){"finalizer": func(p *RevisionV2Params) { p.FinalizedBy = "actor-other" }, "finalized time": func(p *RevisionV2Params) { p.FinalizedAt = p.FinalizedAt.Add(time.Minute) }, "opaque revision": func(p *RevisionV2Params) { p.ProviderObservations[0].Revision = "opaque-other" }} {
		t.Run("non-semantic "+name, func(t *testing.T) {
			params := revisionV2Fixture()
			mutate(&params)
			got, buildErr := NewRevisionV2(params)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if got.GraphDigest() != base.GraphDigest() || got.CriteriaDigest() != base.CriteriaDigest() {
				t.Fatal("provenance/noise changed semantic digest")
			}
		})
	}
}

func TestCanonicalGraphMaterialIsSensitiveToLaneMembershipAndEdge(t *testing.T) {
	params := revisionV2Fixture()
	revision, err := NewRevisionV2(params)
	if err != nil {
		t.Fatal(err)
	}
	items := revision.WorkItems()
	lanes := revision.Lanes()
	observations := revision.ProviderObservations()
	lanes[0].workIDs, lanes[1].workIDs = lanes[1].workIDs, lanes[0].workIDs
	changedMembership, _, err := canonicalizeV2(params.ProjectID, params.MissionID, params.PlanningSourceCommit, params.MissionDefinition, params.DependencyPolicy, items, lanes, observations)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(changedMembership, revision.CanonicalGraph()) {
		t.Fatal("lane membership absent from canonical graph")
	}
	lanes = revision.Lanes()
	lanes[1].dependencyLaneIDs = nil
	changedEdge, _, err := canonicalizeV2(params.ProjectID, params.MissionID, params.PlanningSourceCommit, params.MissionDefinition, params.DependencyPolicy, items, lanes, observations)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(changedEdge, revision.CanonicalGraph()) {
		t.Fatal("lane edge absent from canonical graph")
	}
}

func TestRevisionV2RejectsReservedMetadataCollisionAndAmbiguousNumbers(t *testing.T) {
	params := revisionV2Fixture()
	params.WorkItems[0].Criteria[0].Metadata[CriterionMetadataOutcome] = "collision"
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("reserved collision = %v", err)
	}
	params = revisionV2Fixture()
	params.WorkItems[0].Criteria[0].Metadata["ratio"] = 1.5
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("ambiguous number = %v", err)
	}
	params = revisionV2Fixture()
	params.WorkItems[0].Criteria[0].Metadata["unsupported"] = make(chan int)
	if _, err := NewRevisionV2(params); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("unsupported value = %v", err)
	}
	params = revisionV2Fixture()
	params.WorkItems[0].Criteria[0].Metadata["attempts"] = 3
	if _, err := NewRevisionV2(params); err != nil {
		t.Fatalf("unambiguous integer metadata was refused: %v", err)
	}
}

func TestRevisionV2CanonicalizationSurvivesOneHundredPermutations(t *testing.T) {
	want, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, _ := want.Encode()
	for seed := int64(0); seed < 100; seed++ {
		params := revisionV2Fixture()
		random := rand.New(rand.NewSource(seed))
		random.Shuffle(len(params.WorkItems), func(i, j int) { params.WorkItems[i], params.WorkItems[j] = params.WorkItems[j], params.WorkItems[i] })
		for i := range params.WorkItems {
			random.Shuffle(len(params.WorkItems[i].Criteria), func(a, b int) {
				params.WorkItems[i].Criteria[a], params.WorkItems[i].Criteria[b] = params.WorkItems[i].Criteria[b], params.WorkItems[i].Criteria[a]
			})
			random.Shuffle(len(params.WorkItems[i].WriteScopes), func(a, b int) {
				params.WorkItems[i].WriteScopes[a], params.WorkItems[i].WriteScopes[b] = params.WorkItems[i].WriteScopes[b], params.WorkItems[i].WriteScopes[a]
			})
		}
		random.Shuffle(len(params.Lanes), func(i, j int) { params.Lanes[i], params.Lanes[j] = params.Lanes[j], params.Lanes[i] })
		random.Shuffle(len(params.ProviderObservations), func(i, j int) {
			params.ProviderObservations[i], params.ProviderObservations[j] = params.ProviderObservations[j], params.ProviderObservations[i]
		})
		got, buildErr := NewRevisionV2(params)
		if buildErr != nil {
			t.Fatalf("seed %d: %v", seed, buildErr)
		}
		gotBytes, _ := got.Encode()
		if !bytes.Equal(gotBytes, wantBytes) || got.GraphDigest() != want.GraphDigest() || got.CriteriaDigest() != want.CriteriaDigest() {
			t.Fatalf("seed %d changed canonical revision", seed)
		}
	}
}

func TestRevisionV2WireHasOnlyPublishedTopLevelFields(t *testing.T) {
	revision, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := revision.Encode()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	want := []string{"schema_version", "work_revision_id", "project_id", "mission_id", "generation", "planning_source_commit", "mission_definition", "dependency_policy", "work_items", "lanes", "provider_observations", "graph_digest", "criteria_digest", "finalized_by", "finalized_at", "governance_context_id"}
	if len(value) != len(want) {
		t.Fatalf("top-level fields = %v", value)
	}
	for _, field := range want {
		if _, ok := value[field]; !ok {
			t.Fatalf("missing published field %q", field)
		}
	}
}

func revisionV2Fixture() RevisionV2Params {
	metadataA := map[string]any{"nested": map[string]any{"key": "value"}, "tags": []any{"unit", "fast"}}
	items := []WorkItemV2Params{
		{WorkID: "work-a", Title: "Implement A", Description: "Implement the first bounded unit.", DesignContext: "Follow the approved port.", Criteria: []CriterionV2Params{{ID: "criterion-a", Text: "Run the unit test.", VerifierKind: "test", Required: true, Outcome: "The unit test passes.", EvidenceHint: "go test output", Metadata: metadataA}}, WriteScopes: []NormalizedWriteScope{{Path: "internal/a", Domain: "workgraph", Access: ScopeWriteExclusive, SerializationKey: "serial-a"}}},
		{WorkID: "work-b", Title: "Implement B", Description: "Implement the dependent bounded unit.", DesignContext: "Consume the first unit.", Criteria: []CriterionV2Params{{ID: "criterion-b", Text: "Review the behavior.", VerifierKind: "review", Required: true, Outcome: "The behavior is approved."}}, Dependencies: []ids.WorkID{"work-a"}, WriteScopes: []NormalizedWriteScope{{Path: "internal/b", Domain: "mission", Access: ScopeWriteExclusive}}},
	}
	laneA := LaneV2{id: stableLaneID([]ids.WorkID{"work-a"}), workIDs: []ids.WorkID{"work-a"}}
	laneB := LaneV2{id: stableLaneID([]ids.WorkID{"work-b"}), workIDs: []ids.WorkID{"work-b"}, dependencyLaneIDs: []ids.LaneID{laneA.id}}
	return RevisionV2Params{ID: "revision-v2", ProjectID: "project-v2", MissionID: "mission-v2", Generation: 1, PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("b", 64)}, DependencyPolicy: DependencyApprovedOrDone, WorkItems: items, Lanes: []LaneV2{laneA, laneB}, ProviderObservations: []ProviderObservationV2{{WorkID: "work-a", Provider: "durable_fake", ExternalID: "external-a", Revision: "provider-revision-1"}, {WorkID: "work-b", Provider: "durable_fake", ExternalID: "external-b", Revision: "provider-revision-2"}}, FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC), GovernanceContextID: "governance-finalize"}
}
