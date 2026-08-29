package beads

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func sampleKittyMetadata() kittyMetadata {
	return kittyMetadata{
		SchemaVersion: 1, ProjectID: "project-1", WorkID: "work-1",
		CreatedByOperation: "operation-create", LastOperation: "operation-create",
		Criteria: []tracker.Criterion{
			{ID: "criterion-test", Text: "tests pass", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."},
			{ID: "criterion-human", Text: "operator approves", Verifier: tracker.VerifierHumanDecision, Required: false},
		},
		DesignContext: "bounded adapter", ContentDigest: "digest",
	}
}

func sampleDefinitionMetadata() kittyMetadata {
	value := sampleKittyMetadata()
	value.WorkCriteria = []tracker.WorkCriterion{
		{ID: "criterion-test", Text: "tests pass", Outcome: "focused tests pass", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."},
		{ID: "criterion-human", Text: "operator approves", Outcome: "approval recorded", Verifier: tracker.VerifierHumanDecision, Required: false},
	}
	value.CrossDomainReason = ""
	value.Dependencies = []ids.WorkID{"work-0"}
	value.WriteScopes = []tracker.WriteScope{{Path: "internal/tracker/beads", Domain: "tracker", Access: tracker.ScopeWriteExclusive, SerializationKey: "beads-adapter"}}
	value.Provenance = []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}, {ID: "requirement-parity", Kind: tracker.ScopeRequiredOutcome}}
	value.completeDefinition = true
	return value
}

func TestMetadataCompleteDefinitionRoundTripPreservesEveryNeutralField(t *testing.T) {
	want := sampleDefinitionMetadata()
	encoded, err := mergeMetadata([]byte(`{"foreign":{"keep":true},"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[],"future":{"keep":true}}}`), want)
	if err != nil {
		t.Fatal(err)
	}
	document, err := decodeMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.kitty, want) {
		t.Fatalf("round trip=%#v want=%#v", document.kitty, want)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	if string(root["foreign"]) != `{"keep":true}` {
		t.Fatalf("foreign metadata changed: %s", root["foreign"])
	}
	var namespace map[string]json.RawMessage
	if err := json.Unmarshal(root[metadataNamespace], &namespace); err != nil {
		t.Fatal(err)
	}
	if string(namespace["future"]) != `{"keep":true}` {
		t.Fatalf("unknown namespace metadata changed: %s", namespace["future"])
	}
}

func TestMetadataLegacySchemaOneIsReadableButNotComplete(t *testing.T) {
	document, err := decodeMetadata([]byte(`{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[{"id":"criterion-1","text":"legacy","verifier":"test","required":true}],"design_context":"legacy","content_digest":"digest"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if document.kitty.completeDefinition || len(document.kitty.Criteria) != 1 || document.kitty.Criteria[0].Text != "legacy" {
		t.Fatalf("legacy metadata=%#v", document.kitty)
	}
}

func TestMetadataIncompleteDefinitionFieldsFailClosed(t *testing.T) {
	complete, err := mergeMetadata(nil, sampleDefinitionMetadata())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"dependencies", "write_scopes", "provenance", "cross_domain_reason"} {
		t.Run(field, func(t *testing.T) {
			var root map[string]json.RawMessage
			if err := json.Unmarshal(complete, &root); err != nil {
				t.Fatal(err)
			}
			var namespace map[string]json.RawMessage
			if err := json.Unmarshal(root[metadataNamespace], &namespace); err != nil {
				t.Fatal(err)
			}
			delete(namespace, field)
			root[metadataNamespace], _ = json.Marshal(namespace)
			mutant, _ := json.Marshal(root)
			if _, err := decodeMetadata(mutant); !tracker.IsKind(err, tracker.ErrorSchemaSkew) {
				t.Fatalf("missing %s error=%v", field, err)
			}
		})
	}

	var root map[string]json.RawMessage
	_ = json.Unmarshal(complete, &root)
	var namespace map[string]json.RawMessage
	_ = json.Unmarshal(root[metadataNamespace], &namespace)
	var criteria []map[string]any
	_ = json.Unmarshal(namespace["criteria"], &criteria)
	delete(criteria[0], "outcome")
	namespace["criteria"], _ = json.Marshal(criteria)
	root[metadataNamespace], _ = json.Marshal(namespace)
	mutant, _ := json.Marshal(root)
	if _, err := decodeMetadata(mutant); !tracker.IsKind(err, tracker.ErrorSchemaSkew) {
		t.Fatalf("criterion without outcome error=%v", err)
	}
}

func TestMetadataEmptyMergeAndCriteriaRoundTrip(t *testing.T) {
	want := sampleKittyMetadata()
	encoded, err := mergeMetadata(nil, want)
	if err != nil {
		t.Fatal(err)
	}
	document, err := decodeMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.kitty, want) {
		t.Fatalf("round trip=%#v want=%#v", document.kitty, want)
	}
}

func TestMetadataPreservesForeignAndUnknownSchemaOneFields(t *testing.T) {
	initial := []byte(`{"human":{"nested":[1,true,"x"]},"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-old","criteria":[],"future":{"keep":true}}}`)
	want := sampleKittyMetadata()
	want.LastOperation = "operation-update"
	encoded, err := mergeMetadata(initial, want)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["human"]) != `{"nested":[1,true,"x"]}` {
		t.Fatalf("foreign metadata changed: %s", got["human"])
	}
	var namespace map[string]json.RawMessage
	if err := json.Unmarshal(got[metadataNamespace], &namespace); err != nil {
		t.Fatal(err)
	}
	if string(namespace["future"]) != `{"keep":true}` {
		t.Fatalf("unknown namespace field changed: %s", namespace["future"])
	}
}

func TestMetadataConflictsAndFutureSchemaAreNonMutating(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind tracker.ErrorKind
	}{
		{"project", `{"spec_kitty":{"schema_version":1,"project_id":"other","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[]}}`, tracker.ErrorRevisionConflict},
		{"work", `{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"other","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[]}}`, tracker.ErrorRevisionConflict},
		{"creation operation", `{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"other-operation","last_operation":"other-operation","criteria":[]}}`, tracker.ErrorRevisionConflict},
		{"future", `{"spec_kitty":{"schema_version":2,"opaque":"keep"}}`, tracker.ErrorSchemaSkew},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if encoded, err := mergeMetadata([]byte(test.raw), sampleKittyMetadata()); encoded != nil || !tracker.IsKind(err, test.kind) {
				t.Fatalf("encoded=%s error=%v", encoded, err)
			}
		})
	}
}

func TestMetadataMalformedAndDuplicateCriteriaFailDeterministically(t *testing.T) {
	tests := []string{
		`[]`, `null`, `{} trailing`, `{"spec_kitty":true}`,
		`{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[{"id":"same","text":"one","verifier":"test","required":true},{"id":"same","text":"two","verifier":"review","required":false}]}}`,
	}
	for _, payload := range tests {
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := decodeMetadata([]byte(payload)); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
				t.Fatalf("payload=%s attempt=%d error=%v", payload, attempt, err)
			}
		}
	}
}

func TestMetadataEncodingIsDeterministicAcrossInputMapOrder(t *testing.T) {
	left := []byte(`{"z":1,"a":{"x":2}}`)
	right := []byte(`{"a":{"x":2},"z":1}`)
	want := sampleKittyMetadata()
	a, err := mergeMetadata(left, want)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mergeMetadata(right, want)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("encoding differs:\n%s\n%s", a, b)
	}
}
