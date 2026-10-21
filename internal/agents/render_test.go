package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitLines(t *testing.T) {
	got := SplitLines("a\nb\r\nc\rd\n")
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q", got)
		}
	}
}

func TestRewritePaths(t *testing.T) {
	cases := map[string]string{
		"see scripts/x":          "see .kittify/scripts/x",
		".kittify/scripts/x":     ".kittify/scripts/x",
		"templates/a.md":         ".kittify/templates/a.md",
		"src/foo/templates/a.md": "src/foo/templates/a.md",
		"memory/charter.md":      ".kittify/memory/charter.md",
		".kittify/memory/x":      ".kittify/memory/x",
		"`/templates/x`":         "`/.kittify/templates/x`",
		"dir-/templates/":        "dir-/templates/",
	}
	for in, want := range cases {
		if got := RewritePaths(in); got != want {
			t.Errorf("RewritePaths(%q) = %q want %q", in, got, want)
		}
	}
}

func TestShimMarkdown(t *testing.T) {
	got, err := ShimContent("implement", "claude", "4.0.0rc8")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ndescription: Execute a work package implementation\n---\n<!-- spec-kitty-command-version: 4.0.0rc8 -->\n" +
		"Run this exact command and treat its output as authoritative.\n" +
		"Do not rediscover context from branches, files, prompt contents, or separate charter loads.\n" +
		"When mission selection is required, pass --mission <handle> (mission_id, mid8, or mission_slug).\n\n" +
		"`spec-kitty agent action implement $ARGUMENTS --agent claude`\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

// TestReferenceHome compares rendered global command files against a
// reference HOME captured from the oracle (set SK_REF_HOME).
func TestReferenceHome(t *testing.T) {
	ref := os.Getenv("SK_REF_HOME")
	if ref == "" {
		t.Skip("SK_REF_HOME not set")
	}
	t.Setenv("HOME", ref)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(ref, ".config"))
	for _, key := range CommandAgents {
		files, err := RenderAgentCommands(key)
		if err != nil {
			t.Fatal(err)
		}
		dir := GlobalCommandDir(key)
		for _, f := range files {
			b, err := os.ReadFile(filepath.Join(dir, f.Name))
			if err != nil {
				t.Errorf("%s: %v", key, err)
				continue
			}
			if string(b) != string(f.Content) {
				t.Errorf("%s/%s differs", key, f.Name)
				os.WriteFile(filepath.Join(t.TempDir(), "x"), f.Content, 0o644)
				firstDiff(t, string(b), string(f.Content))
			}
		}
	}
}

func firstDiff(t *testing.T, want, got string) {
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			lo := i - 80
			if lo < 0 {
				lo = 0
			}
			hi := i + 80
			if hi > len(want) {
				hi = len(want)
			}
			hg := i + 80
			if hg > len(got) {
				hg = len(got)
			}
			t.Logf("at %d:\nwant: %q\ngot:  %q", i, want[lo:hi], got[lo:hg])
			return
		}
	}
	t.Logf("length differs: want %d got %d", len(want), len(got))
}
