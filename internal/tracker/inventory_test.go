package tracker

import (
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"testing"
	"time"
)

func TestInventoryReturnsIndependentCompleteValuesAndCommonProof(t *testing.T) {
	metadata, err := NewOpaqueMetadata([]byte(`{"nested":{"n":12345678901234567890}}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NewWorkDefinition(WorkDefinitionParams{ProjectID: "project", WorkID: "work", Title: "Title", Description: "Body", DesignContext: "Design", Criteria: []WorkCriterion{{ID: "check", Text: "text", Outcome: "outcome", Verifier: VerifierTest}}, WriteScopes: []WriteScope{{Path: "file", Domain: "domain", Access: ScopeWriteExclusive}}, Provenance: []ScopeRef{{ID: "FR-002", Kind: ScopeRequiredOutcome}}, MetadataSchema: 1, ProviderMetadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewProviderObservation("inventory:project", []byte(`{"kind":"inventory"}`), 5, "full-digest", "provider-snapshot", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ref := ExternalWorkRef{ProjectID: "project", WorkID: "work", Provider: "test", ExternalID: "native", MetadataSchema: 1, CreatedByOperation: "create", LastObservation: 5}
	item, err := NewInventoryItem(definition, ref)
	if err != nil {
		t.Fatal(err)
	}
	input := []InventoryItem{item}
	result, err := NewInventoryObservation("project", input, proof)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = InventoryItem{}
	items := result.Items()
	items[0].definition.criteria[0].Text = "changed"
	items[0].reference.WorkID = ids.WorkID("changed")
	canonical := result.CommonProof().CanonicalFields()
	canonical[0] = '!'
	if result.Items()[0].Definition().Criteria()[0].Text != "text" || result.Items()[0].ExternalRef().WorkID != "work" || result.CommonProof().CanonicalFields()[0] != '{' {
		t.Fatal("inventory shared mutable storage")
	}
	if _, err := NewInventoryObservation("project", []InventoryItem{item, item}, proof); err == nil {
		t.Fatal("accepted duplicate work")
	}
	if _, err := NewInventoryObservation("other", []InventoryItem{item}, proof); err == nil {
		t.Fatal("accepted cross-project mapping")
	}
}
