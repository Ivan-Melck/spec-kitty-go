package setup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// The oracle may query PyPI for the latest CLI version; the Go port never
// does. Mask the compat "cli" block's latest-version fields.
var (
	reLatest  = regexp.MustCompile(`"latest_version": (null|"[^"]*")`)
	reSource  = regexp.MustCompile(`"latest_source": "[a-z_]+"`)
	reFetched = regexp.MustCompile(`"fetched_at": (null|"[^"]*")`)
)

func maskCompat(s string) string {
	s = reLatest.ReplaceAllString(s, `"latest_version": <L>`)
	s = reSource.ReplaceAllString(s, `"latest_source": <S>`)
	return reFetched.ReplaceAllString(s, `"fetched_at": <F>`)
}

func compareMasked(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stderr, o.Stderr)
	}
	gs, os_ := maskCompat(p.Go.Normalize(g.Stdout)), maskCompat(p.Oracle.Normalize(o.Stdout))
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	return g, o
}

// comparePlan compares a --plan-json document (effect inventory included,
// run-specific content digests masked).
func comparePlan(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	compareHashed(t, p, args...)
}

func lastCommit(s *testkit.Sandbox) string {
	return s.Git("log", "-1", "--format=%s", "--name-status")
}

func compareFiles(t *testing.T, p *testkit.Pair, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		g, gerr := os.ReadFile(filepath.Join(p.Go.Dir, rel))
		o, oerr := os.ReadFile(filepath.Join(p.Oracle.Dir, rel))
		if (gerr == nil) != (oerr == nil) {
			t.Errorf("%s: exists go=%v oracle=%v", rel, gerr == nil, oerr == nil)
			continue
		}
		gn, on := p.Go.Normalize(string(g)), p.Oracle.Normalize(string(o))
		gn, on = reEnvLine.ReplaceAllString(gn, "$1"), reEnvLine.ReplaceAllString(on, "$1")
		if gn != on {
			t.Errorf("%s differs:\n%s", rel, testkit.Diff(on, gn))
		}
	}
}

func TestUpgradeCurrentProjectMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude,codex", "--non-interactive")
	p.Both(func(s *testkit.Sandbox) { s.Git("add", "-A"); s.Git("commit", "-qm", "init") })
	p.Compare(false, "upgrade", "--dry-run")
	compareMasked(t, p, "upgrade", "--dry-run", "--json")
	compareMasked(t, p, "upgrade", "--project", "--json")
	p.Compare(false, "upgrade")
	if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
		t.Errorf("upgrade commit go=%q oracle=%q", g, o)
	}
	compareFiles(t, p, ".kittify/metadata.yaml", ".kittify/config.yaml")
	p.Compare(false, "upgrade", "--json")
	p.Compare(false, "upgrade")

	// Damaged surfaces + missing activations: preview, repair, commit.
	p.Both(func(s *testkit.Sandbox) {
		os.RemoveAll(filepath.Join(s.Dir, ".claude/skills/spec-kitty-git-workflow"))
		os.RemoveAll(filepath.Join(s.Dir, ".agents/skills/spec-kitty.plan"))
		cfg := s.ReadFile(".kittify/config.yaml")
		cfg = regexp.MustCompile(`(?s)mission_type_activations:\n(- [^\n]*\n)+`).ReplaceAllString(cfg, "")
		os.Chmod(filepath.Join(s.Dir, ".kittify/config.yaml"), 0o644)
		s.WriteFile(".kittify/config.yaml", cfg)
		s.Git("add", "-A")
		s.Git("commit", "-qm", "damage")
	})
	p.Compare(false, "upgrade", "--dry-run")
	compareMasked(t, p, "upgrade", "--dry-run", "--json")
	p.Compare(false, "upgrade")
	if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
		t.Errorf("repair commit go=%q oracle=%q", g, o)
	}
	compareFiles(t, p, ".kittify/config.yaml", ".claude/skills/spec-kitty-git-workflow/SKILL.md", ".agents/skills/spec-kitty.plan/SKILL.md",
		".kittify/skills-manifest.json", ".kittify/command-skills-manifest.json")
}

func TestUpgradeStaleAndLegacyMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude,codex", "--non-interactive")
	p.Both(func(s *testkit.Sandbox) {
		md := strings.Replace(s.ReadFile(".kittify/metadata.yaml"), "  version: 4.0.0rc8", "  version: 4.0.0rc5", 1)
		os.Chmod(filepath.Join(s.Dir, ".kittify/metadata.yaml"), 0o644)
		s.WriteFile(".kittify/metadata.yaml", md)
		s.Git("add", "-A")
		s.Git("commit", "-qm", "init")
	})
	p.Compare(false, "upgrade", "--dry-run")
	compareMasked(t, p, "upgrade", "--dry-run", "--json")
	comparePlan(t, p, "upgrade", "--plan-json")
	p.Compare(false, "upgrade", "--yes")
	if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
		t.Errorf("upgrade commit go=%q oracle=%q", g, o)
	}
	compareFiles(t, p, ".kittify/metadata.yaml", ".git/hooks/post-commit", ".git/hooks/post-rewrite", ".git/info/exclude")

	// Legacy: no schema stamp → unsafe commands are refused (exit 4).
	p.Both(func(s *testkit.Sandbox) {
		md := strings.Replace(s.ReadFile(".kittify/metadata.yaml"), "  schema_version: 3\n", "", 1)
		s.WriteFile(".kittify/metadata.yaml", md)
	})
	p.Compare(true, "agent", "config", "list")
	p.Compare(true, "agent", "tasks", "status")
	compareMasked(t, p, "upgrade", "--project", "--json")
	p.Compare(false, "upgrade", "--yes")
	compareFiles(t, p, ".kittify/metadata.yaml")
	p.Compare(true, "agent", "config", "list")

	// Too new: exit 5 everywhere, upgrade included.
	p.Both(func(s *testkit.Sandbox) {
		md := strings.Replace(s.ReadFile(".kittify/metadata.yaml"), "  schema_version: 3\n", "  schema_version: 9\n", 1)
		s.WriteFile(".kittify/metadata.yaml", md)
	})
	p.Compare(true, "agent", "config", "list")
	p.Compare(false, "upgrade")
	compareMasked(t, p, "upgrade", "--json")
	compareMasked(t, p, "upgrade", "--plan-json")
}

func TestUpgradeFlagsMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(false, "upgrade", "--project")
	p.Compare(false, "upgrade", "--project", "--json")
	compareMasked(t, p, "upgrade", "--plan-json")
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	p.Compare(false, "upgrade", "--cli", "--project")
	compareMasked(t, p, "upgrade", "--cli", "--project", "--json")
	compareMasked(t, p, "upgrade", "--plan-json", "--cli")
	p.Compare(false, "upgrade", "--target", "0.1.0")
	p.Compare(false, "upgrade", "--target", "0.1.0", "--json")
	p.Compare(false, "upgrade", "--target", "bogus")
	compareMasked(t, p, "upgrade", "--target", "bogus", "--plan-json")
	compareMasked(t, p, "upgrade", "--target", "0.1.0", "--dry-run", "--json")
	p.Compare(false, "upgrade", "--agent-choice", "bogus", "--json")
	p.Compare(false, "upgrade", "--agent-check", "--agent-choice", "always")
}

func TestUpgradeWithoutOracle(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	s.MustRun("init", ".", "--ai", "claude", "--non-interactive")
	s.Git("add", "-A")
	s.Git("commit", "-qm", "init")
	md := strings.Replace(s.ReadFile(".kittify/metadata.yaml"), "  version: 4.0.0rc8", "  version: 4.0.0rc5", 1)
	os.Chmod(filepath.Join(s.Dir, ".kittify/metadata.yaml"), 0o644)
	s.WriteFile(".kittify/metadata.yaml", md)
	r := s.MustRun("upgrade", "--yes")
	if !strings.Contains(testkit.StripANSI(r.Stdout), "Upgrade complete! 4.0.0rc5 -> 4.0.0rc8") || !strings.Contains(testkit.StripANSI(r.Stdout), "4.0.0rc7_exclude_spec_kitty") {
		t.Fatalf("upgrade:\n%s", r.Stdout)
	}
	if !strings.Contains(s.ReadFile(".kittify/migration-ledger.jsonl"), `"result": "success"`) || strings.Contains(s.ReadFile(".kittify/metadata.yaml"), "migrations:") || !s.Exists(".git/hooks/post-commit") {
		t.Fatal("migrations not recorded / applied")
	}
	md = strings.Replace(s.ReadFile(".kittify/metadata.yaml"), "  schema_version: 3\n", "", 1)
	s.WriteFile(".kittify/metadata.yaml", md)
	if r := s.Run("agent", "config", "list"); r.Code != 4 || !strings.Contains(testkit.StripANSI(r.Stderr), "needs Spec Kitty project migrations") {
		t.Fatalf("gate: %d %s", r.Code, r.Stderr)
	}
	s.MustRun("upgrade", "--yes")
	s.MustRun("agent", "config", "list")
}
