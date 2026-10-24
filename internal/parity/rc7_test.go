package parity

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestRC7ProjectScaffold(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	s.MustRun("init", ".", "--ai", "codex", "--non-interactive")
	if !s.Exists(".agents/skills/spec-kitty.specify/SKILL.md") {
		t.Fatal("RC7 dotted command skill is missing")
	}
	for _, path := range []string{
		".agents/skills/spec-kitty-specify/SKILL.md",
		".kittify/charter/activation.yaml",
		".kittify/migration-ledger.jsonl",
	} {
		if s.Exists(path) {
			t.Errorf("RC8-only scaffold %s exists", path)
		}
	}
	metadata := s.ReadFile(".kittify/metadata.yaml")
	if !strings.Contains(metadata, "version: 4.0.0rc7") || strings.Contains(metadata, "4.0.0rc8") {
		t.Fatalf("wrong project version:\n%s", metadata)
	}
	for _, args := range [][]string{
		{"charter", "pack", "vendor"},
		{"migrate", "ledger"},
		{"migrate", "activation-ledger"},
		{"agent", "acceptance-verdict", "--owned-checkout", "."},
	} {
		if r := s.Run(args...); r.Code != 2 {
			t.Errorf("RC8-only command %v: exit=%d\n%s%s", args, r.Code, r.Stdout, r.Stderr)
		}
	}
}
