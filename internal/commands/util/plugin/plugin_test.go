package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// compareModes diffs the permission bits of every file and directory under
// rel in both sandboxes.
func compareModes(t *testing.T, p *testkit.Pair, rel string) {
	t.Helper()
	modes := func(base string) map[string]os.FileMode {
		out := map[string]os.FileMode{}
		filepath.Walk(filepath.Join(base, rel), func(path string, info os.FileInfo, err error) error {
			if err == nil {
				r, _ := filepath.Rel(base, path)
				out[r] = info.Mode()
			}
			return nil
		})
		return out
	}
	g, o := modes(p.Go.Dir), modes(p.Oracle.Dir)
	for k, om := range o {
		if gm, ok := g[k]; !ok || gm != om {
			t.Errorf("mode %s: go=%v oracle=%v", k, g[k], om)
		}
	}
	for k := range g {
		if _, ok := o[k]; !ok {
			t.Errorf("extra go path %s", k)
		}
	}
}

// fakeClaude puts a `claude` stub first on PATH that exits with code and
// prints msg on stdout and stderr.
func fakeClaude(code, msg string) func(s *testkit.Sandbox) {
	return func(s *testkit.Sandbox) {
		bin := filepath.Join(s.Base, "fakebin")
		os.MkdirAll(bin, 0o755)
		script := "#!/bin/sh\necho \"" + msg + " $*\"\necho \"" + msg + " err\" >&2\nexit " + code + "\n"
		os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755)
		s.Env = append(s.Env, "PATH="+bin+":"+os.Getenv("PATH"))
	}
}

func TestPluginBuildParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(true, "plugin", "build", "--target", "vscode")
	p.Compare(true, "plugin", "build")
	p.Compare(true, "plugin", "build", "--target", "claude-code", "--skip-validate")
	p.Compare(true, "plugin", "build", "--target", "codex", "--skip-validate")
	p.CompareTree("dist", true)
	compareModes(t, p, "dist")
	// Rebuild over identical content is a no-op.
	p.Compare(true, "plugin", "build", "--target", "claude-code", "--skip-validate")
	p.Compare(true, "plugin", "build", "--target", "codex")
	p.CompareTree("dist", true)

	// Validation through a stub claude CLI: pass, then fail.
	p.Both(fakeClaude("0", "ok"))
	p.Compare(true, "plugin", "build", "--target", "claude-code", "--output-dir", "./out//v")
	p.Both(fakeClaude("3", "bad"))
	p.Compare(true, "plugin", "build", "--target", "claude-code", "--output-dir", "out/v")

	// A locally edited member and a mode drift: the edit is preserved, the
	// manifests are withheld, the ledger records the rest, and the build
	// fails with a BuildError traceback.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("dist/spec-kitty-plugins/claude-code/skills/spec-kitty-plan/SKILL.md", "edited\n")
		os.Chmod(filepath.Join(s.Dir, "dist/spec-kitty-plugins/claude-code/agents/generic-agent.md"), 0o600)
		os.Remove(filepath.Join(s.Dir, "dist/spec-kitty-plugins/claude-code/.claude-plugin/plugin.json"))
		os.Remove(filepath.Join(s.Dir, "dist/spec-kitty-plugins/claude-code/agents/scribe-sally.md"))
	})
	g, o := p.Compare(false, "plugin", "build", "--target", "claude-code", "--skip-validate")
	if gl, ol := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr)); gl != ol {
		t.Errorf("traceback differs\n%s", testkit.Diff(ol, gl))
	}
	p.CompareTree("dist", true)
	compareModes(t, p, "dist")

	// User-supplied hooks are kept (unmanaged) and declared in the manifest.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("hooked/claude-code/hooks/hooks.json", `{"hooks": {"PreToolUse": []}}`)
		os.Chmod(filepath.Join(s.Dir, "hooked/claude-code/hooks/hooks.json"), 0o640)
	})
	p.Compare(true, "plugin", "build", "--target", "claude-code", "--skip-validate", "--output-dir", "hooked")
	p.CompareTree("hooked", true)
	compareModes(t, p, "hooked")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("badhooks/claude-code/hooks/hooks.json", `[1]`) })
	g, o = p.Compare(false, "plugin", "build", "--target", "claude-code", "--skip-validate", "--output-dir", "badhooks")
	if gl, ol := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr)); gl != ol {
		t.Errorf("traceback differs\n%s", testkit.Diff(ol, gl))
	}
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("badledger/codex/.spec-kitty-bundle.json", `{"owner": "x"}`) })
	g, o = p.Compare(false, "plugin", "build", "--target", "codex", "--output-dir", "badledger")
	if gl, ol := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr)); gl != ol {
		t.Errorf("traceback differs\n%s", testkit.Diff(ol, gl))
	}
}

func TestPluginBuildE2E(t *testing.T) {
	s := testkit.NewSandbox(t)
	r := s.Run("plugin", "build", "--target", "codex", "--skip-validate")
	if r.Code != 0 || !strings.HasSuffix(r.Stdout, "Bundle written to dist/spec-kitty-plugins/codex\n") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !s.Exists("dist/spec-kitty-plugins/codex/skills/spec-kitty-specify/SKILL.md") || !s.Exists("dist/spec-kitty-plugins/codex/.spec-kitty-bundle.json") {
		t.Fatal("bundle files missing")
	}
}
