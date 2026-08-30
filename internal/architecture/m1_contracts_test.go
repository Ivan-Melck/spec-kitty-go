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

func TestM1SchemasFR016EventEnvelopeRefusals(t *testing.T) {
	schema := compileM1ContractSchema(t, "event-envelope-v1.schema.json")
	hash := strings.Repeat("a", 64)
	valid := map[string]any{
		"envelope_version": 1, "event_type": "work-claimed", "event_schema_version": 1,
		"reducer_version": "work-v1", "event_id": "event-1", "operation_id": "operation-1",
		"project_id": "project-1", "aggregate_kind": "work", "aggregate_id": "work-1",
		"aggregate_logical_version": 1, "runtime_epoch_id": "epoch-1",
		"event_index": 0, "event_count": 1, "payload": map[string]any{},
		"canonical_event_digest": hash,
	}
	if err := schema.Validate(valid); err != nil {
		t.Fatalf("positive event envelope rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown envelope version", func(v map[string]any) { v["envelope_version"] = 2 }},
		{"missing event schema version", func(v map[string]any) { delete(v, "event_schema_version") }},
		{"missing reducer version", func(v map[string]any) { delete(v, "reducer_version") }},
		{"negative event index", func(v map[string]any) { v["event_index"] = -1 }},
		{"event index beyond product bound", func(v map[string]any) { v["event_index"] = 128 }},
		{"zero event count", func(v map[string]any) { v["event_count"] = 0 }},
		{"event count beyond product bound", func(v map[string]any) { v["event_count"] = 129 }},
		{"malformed canonical digest", func(v map[string]any) { v["canonical_event_digest"] = "not-a-digest" }},
		{"unversioned extra field", func(v map[string]any) { v["future_field"] = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneMap(t, valid)
			test.mutate(fixture)
			if err := schema.Validate(fixture); err == nil {
				t.Fatal("closed event envelope accepted an invalid structural fixture")
			}
		})
	}

	// Event-schema and reducer versions are registry keys, not schema enums.
	// Their unknown-value refusal and the cross-record ordering/group/digest
	// semantics are enforced against the production seams by the M1 dependency
	// guard, so a later runtime cannot make the schema test pass by self-testing
	// a test-only comparator or registry.
	registryUnknown := cloneMap(t, valid)
	registryUnknown["event_schema_version"] = 2
	registryUnknown["reducer_version"] = "unknown-v2"
	if err := schema.Validate(registryUnknown); err != nil {
		t.Fatalf("structural schema incorrectly owns runtime registry compatibility: %v", err)
	}
}

func compileM1ContractSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(m1ContractsDir(t), name)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	id, ok := document.(map[string]any)["$id"].(string)
	if !ok || id == "" {
		t.Fatalf("%s: missing $id", name)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(id, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return schema
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
