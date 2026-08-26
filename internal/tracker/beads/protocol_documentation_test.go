package beads

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestV122PreflightDocumentationMatchesShippedProtocol(t *testing.T) {
	root := beadsRepositoryRoot(t)
	documents := map[string]string{
		"contract":   beadsReadDocument(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/contracts/beads-json-protocol.md"),
		"quickstart": beadsReadDocument(t, root, "kitty-specs/beads-tracker-integration-01M0YG7E/quickstart.md"),
	}
	for name, document := range documents {
		for _, command := range []string{
			"bd version --json",
			"bd where --json",
			"bd dolt show --json",
			"bd dolt status --json",
		} {
			if !strings.Contains(document, command) {
				t.Errorf("%s lacks qualified preflight command %q", name, command)
			}
		}
		for _, environment := range []string{
			"BD_JSON_ENVELOPE=1",
			"BD_NON_INTERACTIVE=1",
			"BD_DISABLE_METRICS=1",
			"BD_DISABLE_EVENT_FLUSH=1",
		} {
			if !strings.Contains(document, environment) {
				t.Errorf("%s lacks fixed preflight environment %q", name, environment)
			}
		}
		for _, obsolete := range []string{"bd info --schema --json", "bd doctor --agent --json"} {
			if strings.Contains(document, obsolete) {
				t.Errorf("%s retains obsolete product-state command %q", name, obsolete)
			}
		}
		flat := strings.ToLower(strings.Join(strings.Fields(document), " "))
		for _, policy := range []string{"read-only", "logical server identity", "sealed local binding policy"} {
			if !strings.Contains(flat, policy) {
				t.Errorf("%s lacks local policy statement %q", name, policy)
			}
		}
	}
}

func beadsRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve protocol documentation test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}

func beadsReadDocument(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
