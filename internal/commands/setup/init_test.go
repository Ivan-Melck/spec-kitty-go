package setup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var reEnvLine = regexp.MustCompile(`(?m)^  (python_version|platform_version): .*$`)

// maskInitFiles masks values that legitimately differ between the Python
// oracle and the Go port (interpreter version / platform string).
func maskInitFiles(t *testing.T, dir string) {
	p := filepath.Join(dir, ".kittify", "metadata.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	st, _ := os.Stat(p)
	os.Chmod(p, 0o600)
	os.WriteFile(p, reEnvLine.ReplaceAll(b, []byte("  $1: <ENV>")), st.Mode().Perm())
}

func gitConfig(t *testing.T, dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, ".git", "config"))
	return string(b)
}

func TestInitMatchesOracle(t *testing.T) {
	testkit.Oracle(t)
	all := "copilot,claude,gemini,cursor,qwen,opencode,codex,windsurf,kilocode,auggie,q,kiro,antigravity,vibe,pi,letta,llxprt"
	for _, sel := range []string{"claude", "codex", "copilot", "auggie", "q", "gemini,cursor", "vibe,pi,letta", all} {
		sel := sel
		name := strings.ReplaceAll(sel, ",", "_")
		if sel == all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			p := testkit.NewPair(t)
			g, o := compareInit(p, "init", ".", "--ai", sel, "--non-interactive")
			if g.Code != 0 || o.Code != 0 {
				t.Fatalf("exit go=%d oracle=%d\n%s", g.Code, o.Code, g.Stderr)
			}
			maskInitFiles(t, p.Go.Dir)
			maskInitFiles(t, p.Oracle.Dir)
			compareDirs(t, p, p.Go.Dir, p.Oracle.Dir)
			compareDirs(t, p, p.Go.Home, p.Oracle.Home, ".spec-kitty-cold-install", ".kittify/cache/agent-commands-freshness.lock")
			if gc, oc := gitConfig(t, p.Go.Dir), gitConfig(t, p.Oracle.Dir); gc != oc {
				t.Errorf(".git/config differs:\n%s", testkit.Diff(oc, gc))
			}
		})
	}
}

func TestInitErrorsMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "bogus", "--non-interactive")
	compareInit(p, "init", ".", "--non-interactive")
	compareInit(p, "init", ".", "--ai", ",", "--non-interactive")
	// Second run on an initialized project.
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	compareInit(p, "init", ".", "--ai", "codex", "--non-interactive")
}

func TestInitNamedDirAndNoGit(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", "newproj", "--ai", "codex", "--non-interactive")
	maskInitFiles(t, filepath.Join(p.Go.Dir, "newproj"))
	maskInitFiles(t, filepath.Join(p.Oracle.Dir, "newproj"))
	compareDirs(t, p, filepath.Join(p.Go.Dir, "newproj"), filepath.Join(p.Oracle.Dir, "newproj"))
	compareInit(p, "init", "newproj", "--ai", "codex", "--non-interactive")

	q := &testkit.Pair{T: t, Go: testkit.NewSandbox(t), Oracle: testkit.NewSandbox(t)}
	compareInit(q, "init", "--ai", "claude", "--yes")
	maskInitFiles(t, q.Go.Dir)
	maskInitFiles(t, q.Oracle.Dir)
	compareDirs(t, q, q.Go.Dir, q.Oracle.Dir)
}

func TestInitWithoutOracle(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	r := s.MustRun("init", ".", "--ai", "claude,codex", "--non-interactive")
	if !strings.Contains(r.Stdout, "Project ready.") {
		t.Fatalf("stdout:\n%s", r.Stdout)
	}
	for _, rel := range []string{".kittify/config.yaml", ".kittify/metadata.yaml", ".kittify/canonical-events.jsonl", ".kittify/skills-manifest.json",
		".kittify/command-skills-manifest.json", ".kittify/agent_profiles_manifest.json", ".claude/CLAUDE.md", ".claude/settings.json", "AGENTS.md",
		".agents/skills/spec-kitty-plan/SKILL.md", ".claude/skills/spec-kitty/SKILL.md", ".codex/agents/architect-alphonso.toml", ".gitignore", ".gitattributes"} {
		if !s.Exists(rel) {
			t.Errorf("missing %s", rel)
		}
	}
	if s.Exists(".kittify/init-command-skills.pending.json") {
		t.Error("pending record left behind")
	}
	cfg := s.ReadFile(".kittify/config.yaml")
	if !strings.Contains(cfg, "available:\n  - claude\n  - codex\n") || strings.Contains(cfg, "mission_type_activations:") || !strings.Contains(s.ReadFile(".kittify/charter/activation.yaml"), "mission_type_activations:\n- software-dev") {
		t.Errorf("config.yaml:\n%s", cfg)
	}
	if r := s.Run("init", ".", "--ai", "claude", "--non-interactive"); r.Code != 0 || !strings.Contains(r.Stdout, "Already initialized") {
		t.Fatalf("rerun: %d\n%s", r.Code, r.Stdout)
	}
	os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty-plan"))
	if r := s.Run("init", ".", "--non-interactive"); r.Code != 1 || !strings.Contains(r.Stdout, "agent config sync --create-missing") {
		t.Fatalf("missing command skill rerun: %d\n%s", r.Code, r.Stdout)
	}
}
