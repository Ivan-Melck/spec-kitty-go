package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubnotDocumentationDeliveryClaims(t *testing.T) {
	root := phase2RepositoryRoot(t)
	doc := phase2Read(t, root, "docs/HUBNOT_TRACKER.md")
	for _, required := range []string{"go-kitty.hubnot-binding/1", "go-kitty.tracker-request/1", "go-kitty.tracker-result/1", "provider_metadata_json", "original-operation/1", "expected_observation", "signing_actor", "proof_scope=portable_relations_only", "unguarded", "250 ms", "27 of 28", "all 28", "M2", "M3", "nineteen", "32 fixed"} {
		if !strings.Contains(doc, required) {
			t.Errorf("runbook omits %q", required)
		}
	}
	readme := phase2Read(t, root, "README.md")
	if !strings.Contains(readme, "docs/HUBNOT_TRACKER.md") || strings.Contains(readme, "tracker inspect-beads") || strings.Contains(readme, "make beads-contract") {
		t.Fatal("current README routes to retired provider")
	}
	// Executable request examples use the real external schema, never prose-shaped JSON.
	for _, block := range strings.Split(doc, "```json\n")[1:] {
		raw := strings.SplitN(block, "```", 2)[0]
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil || v["schema"] != "go-kitty.tracker-request/1" || v["action"] != "create" {
			t.Fatalf("request example invalid: %v", err)
		}
	}
}
func TestHubnotDocumentationLinksMermaidAndCommands(t *testing.T) {
	root := phase2RepositoryRoot(t)
	for _, name := range []string{"README.md", "docs/HUBNOT_TRACKER.md"} {
		doc := phase2Read(t, root, name)
		phase2CheckRelativeLinks(t, root, name, doc)
		phase2CheckMermaid(t, name, doc)
		if strings.Count(doc, "```")%2 != 0 {
			t.Fatal("unbalanced documentation fence")
		}
	}
	binary := filepath.Join(t.TempDir(), "spec-kitty")
	phase2RunCommand(t, root, 0, "go", "build", "-o", binary, "./cmd/spec-kitty")
	help := phase2RunCommand(t, root, 0, binary, "tracker", "--help")
	for _, action := range []string{"inspect", "inventory", "read", "create", "update", "define", "definition", "assign", "comment", "discussion", "dependencies", "link", "unlink", "ready"} {
		if !strings.Contains(help, action) {
			t.Errorf("real help missing %s", action)
		}
	}
	control := filepath.Join(t.TempDir(), "not-created")
	phase2RunCommand(t, root, 2, binary, "--control-root", control, "--output", "json", "tracker", "inspect", "--binding", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := os.Stat(control); !os.IsNotExist(err) {
		t.Fatal("tracker refusal touched control root")
	}
	for _, target := range []string{"hubnot-build", "hubnot-install", "hubnot-product", "hubnot-contract", "hubnot-hosted-provider", "hubnot-qualification", "hubnot-identity"} {
		phase2RunCommand(t, root, 0, "make", "-n", target, "ARTIFACT_DIR=/absolute/evidence", "BUILD_DIR=/absolute/build", "HUBNOT_GO=/absolute/go", "HUBNOT_SOURCE=/absolute/accepted-source")
	}
}
