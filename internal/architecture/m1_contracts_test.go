package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func m1ContractsDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	return filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
}
