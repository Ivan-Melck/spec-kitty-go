package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentIgnoreScopeAndBlock(t *testing.T) {
	root := t.TempDir()
	entries := AgentIgnoreEntries(root, []string{"claude"})
	joined := strings.Join(entries, "\n")
	for _, want := range []string{".claude/settings.local.json", ".codex/auth.json", ".claude/commands/spec-kitty.*", ".agents/skills/spec-kitty-*/", ".claude/skills/adversarial-squad/"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, line := range entries {
		if line == ".claude/" || line == ".gemini/commands/spec-kitty.*" {
			t.Errorf("unexpected folder scope %s", line)
		}
	}
	before := "# team config\r\n.claude/private/\r\n"
	out, changed, err := ReplaceAgentBlock(before, entries, true)
	if err != nil || !changed {
		t.Fatalf("block: %v %v", changed, err)
	}
	if !strings.HasPrefix(out, before) || !strings.Contains(out, AgentBlockBegin+"\r\n") {
		t.Fatalf("source formatting lost %q", out)
	}
	again, changed, err := ReplaceAgentBlock(out, entries, true)
	if err != nil || changed || again != out {
		t.Fatal("block not idempotent")
	}
	if _, _, err := ReplaceAgentBlock(AgentBlockBegin+"\n", entries, true); err == nil {
		t.Fatal("damaged block not refused")
	}
	if err := os.MkdirAll(filepath.Join(root, ".kittify"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kittify/config.yaml"), []byte("agents:\n  available: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	keys, err := IgnoredAgentKeys(root)
	if err != nil || len(keys) != 0 {
		t.Fatalf("explicit empty selection widened: %v %v", keys, err)
	}
}
