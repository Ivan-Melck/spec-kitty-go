package beads

import (
	"encoding/json"
	"reflect"
	"testing"

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
