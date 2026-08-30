package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestM1SchemasCompile(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(m1ContractsDir(t), "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no contract schemas found")
	}
	wantSchemas := []string{
		"adoption-record-v1.schema.json",
		"authority-capsule-v1.schema.json",
		"checkpoint-v1.schema.json",
		"event-envelope-v1.schema.json",
		"evidence-manifest-v1.schema.json",
		"operation-record-v1.schema.json",
		"ordered-trial-v1.schema.json",
		"runtime-current-v1.schema.json",
		"sidecar-provenance-v1.schema.json",
	}
	gotSchemas := make([]string, len(paths))
	for index, path := range paths {
		gotSchemas[index] = filepath.Base(path)
	}
	if !slices.Equal(gotSchemas, wantSchemas) {
		t.Fatalf("mission schema census changed: got %v, want %v", gotSchemas, wantSchemas)
	}

	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document map[string]any
		if decodeErr := json.Unmarshal(payload, &document); decodeErr != nil {
			t.Fatalf("%s: %v", path, decodeErr)
		}
		id, ok := document["$id"].(string)
		if !ok || id == "" {
			t.Fatalf("%s: missing $id", path)
		}
		if addErr := compiler.AddResource(id, document); addErr != nil {
			t.Fatalf("%s: %v", path, addErr)
		}
		ids = append(ids, id)
	}
	for index, id := range ids {
		if _, compileErr := compiler.Compile(id); compileErr != nil {
			t.Fatalf("%s: %v", paths[index], compileErr)
		}
	}
}

func TestM1SchemasAdoptionStateIsExplicit(t *testing.T) {
	path := filepath.Join(m1ContractsDir(t), "adoption-record-v1.schema.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	id := document.(map[string]any)["$id"].(string)
	if err := compiler.AddResource(id, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	record := map[string]any{
		"schema_version": 1, "program_id": "production-runtime", "mission_id": "M1",
		"decision": "adopt", "evidence_manifest_sha256": hash,
		"admitted_tuple_sha256": "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d",
		"source_commit":         strings.Repeat("b", 40), "source_tree": strings.Repeat("c", 40),
		"sidecar_sha256": hash, "signer": "github:lynncoleart", "rationale": "exact evidence reviewed",
		"signed_at": "2026-08-30T00:00:00Z",
		"signature": map[string]any{"kind": "approved-local-reference", "value": "local-signature-1"},
	}
	for _, decision := range []string{"adopt", "refuse"} {
		record["decision"] = decision
		if err := schema.Validate(record); err != nil {
			t.Fatalf("explicit %s adoption state rejected: %v", decision, err)
		}
	}
	delete(record, "decision")
	if err := schema.Validate(record); err == nil {
		t.Fatal("adoption record without an explicit state was accepted")
	}
	record["decision"] = "pending"
	if err := schema.Validate(record); err == nil {
		t.Fatal("unratified adoption state was accepted")
	}
}

func TestM1SchemasEventOrderingContract(t *testing.T) {
	ordered := []m1EventPosition{
		{CommittedRevision: 7, EventIndex: 0, EventID: "event-b"},
		{CommittedRevision: 6, EventIndex: 9, EventID: "event-z"},
		{CommittedRevision: 7, EventIndex: 0, EventID: "event-a"},
		{CommittedRevision: 7, EventIndex: 1, EventID: "event-a"},
	}
	slices.SortFunc(ordered, compareM1EventPosition)
	want := []m1EventPosition{
		{CommittedRevision: 6, EventIndex: 9, EventID: "event-z"},
		{CommittedRevision: 7, EventIndex: 0, EventID: "event-a"},
		{CommittedRevision: 7, EventIndex: 0, EventID: "event-b"},
		{CommittedRevision: 7, EventIndex: 1, EventID: "event-a"},
	}
	if !slices.Equal(ordered, want) {
		t.Fatalf("event order is not (committed revision, event index, EventID): got %v", ordered)
	}
}

type m1EventPosition struct {
	CommittedRevision uint64
	EventIndex        uint32
	EventID           string
}

func compareM1EventPosition(left, right m1EventPosition) int {
	if left.CommittedRevision < right.CommittedRevision {
		return -1
	}
	if left.CommittedRevision > right.CommittedRevision {
		return 1
	}
	if left.EventIndex < right.EventIndex {
		return -1
	}
	if left.EventIndex > right.EventIndex {
		return 1
	}
	return strings.Compare(left.EventID, right.EventID)
}

func m1ContractsDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	return filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
}
