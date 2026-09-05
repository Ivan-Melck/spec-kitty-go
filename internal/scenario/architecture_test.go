package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sourceViolation struct {
	file, rule string
}

func portableSourceViolations(files map[string]string) []sourceViolation {
	forbidden := map[string]string{
		"relational_driver":    "database/" + "sql",
		"legacy_adapter":       "controlstore/" + "sqlite",
		"legacy_module":        "modernc.org/" + "sqlite",
		"raw_store":            "\"github.com/spec-kitty/go-kitty3/internal/controlstore/" + "gapdb\"",
		"helper_shortcut":      "internal/" + "helpercmd",
		"path_argument":        "--" + "db",
		"legacy_eligibility":   "legacy-" + "v1",
		"schema_catalog":       "sqlite_" + "master",
		"lease_relation":       "FROM " + "leases",
		"workspace_relation":   "FROM " + "workspaces",
		"integration_relation": "FROM " + "integrations",
	}
	var violations []sourceViolation
	for name, source := range files {
		lower := strings.ToLower(source)
		for rule, fragment := range forbidden {
			if strings.Contains(lower, strings.ToLower(fragment)) {
				violations = append(violations, sourceViolation{file: name, rule: rule})
			}
		}
	}
	return violations
}

func TestHarnessArchitectureIsPortable(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "scenario")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[entry.Name()] = string(raw)
	}
	if violations := portableSourceViolations(files); len(violations) != 0 {
		t.Fatalf("portable scenario boundary violations: %+v", violations)
	}
}

func TestHarnessArchitectureGuardKillsStorageMutants(t *testing.T) {
	mutants := map[string]string{
		"driver":   "import _ \"modernc.org/" + "sqlite\"",
		"query":    "rows, err := q.Query(\"SELECT * FROM " + "lea" + "ses\")",
		"raw":      "import \"github.com/spec-kitty/go-kitty3/internal/controlstore/" + "gapdb\"",
		"shortcut": "exec.Command(helper, \"inspect\", \"--" + "db\", path)",
	}
	for name, source := range mutants {
		t.Run(name, func(t *testing.T) {
			if got := portableSourceViolations(map[string]string{"mutant.go": source}); len(got) == 0 {
				t.Fatal(fmt.Errorf("architecture mutant survived: %s", name))
			}
		})
	}
}
