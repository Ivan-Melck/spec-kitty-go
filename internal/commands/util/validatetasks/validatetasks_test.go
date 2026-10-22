package validatetasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestBuildDocument(t *testing.T) {
	if got := buildDocument("a: 1\n", "body", "\n"); got != "---\na: 1\n---\nbody\n" {
		t.Fatalf("got %q", got)
	}
	if got := buildDocument("a: 1", "", ""); got != "---\na: 1\n---\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDetectLaneMismatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m", "tasks", "doing", "WP01.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("\ufeff---\r\nlane:  planned \r\n---\r\nbody\r\n"), 0o644)
	mm, exp, act, err := DetectLaneMismatch(p, nil)
	if err != nil || !mm || exp != "doing" || act != "planned" {
		t.Fatalf("got %v %q %q %v", mm, exp, act, err)
	}
	os.WriteFile(p, []byte("---\nlane:\n---\n"), 0o644)
	if _, _, _, err := DetectLaneMismatch(p, nil); err == nil || !strings.Contains(err.Error(), "'NoneType' object has no attribute 'strip'") {
		t.Fatalf("got %v", err)
	}
}

const wpListLog = `---
work_package_id: WP02
title: "Two — ünïcode"
lane: done
subtasks: [T001, T002]
activity_log:
- timestamp: '2025-01-01T00:00:00Z'
  lane: planned
  agent: claude
  action: created
---
# WP02

Body text.
`

const wpStrLog = `---
work_package_id: WP03
lane: planned
activity_log: |
  - timestamp: "2025-01-01T00:00:00Z"
    lane: "planned"

dependencies: []
---

# WP03
`

func legacyFixtures(s *testkit.Sandbox) {
	s.WriteFile(".kittify/config.yaml", "agents:\n  available: [claude]\n")
	s.WriteFile("kitty-specs/001-legacy/spec.md", "# spec\n")
	s.WriteFile("kitty-specs/001-legacy/tasks/planned/WP01.md", "---\nwork_package_id: WP01\nlane: planned\n---\nok\n")
	s.WriteFile("kitty-specs/001-legacy/tasks/doing/WP02-two.md", wpListLog)
	s.WriteFile("kitty-specs/001-legacy/tasks/for_review/WP03.md", wpStrLog)
	s.WriteFile("kitty-specs/001-legacy/tasks/done/WP04.md", "---\nwork_package_id: WP04\n---\nno lane\n")
	s.WriteFile("kitty-specs/001-legacy/tasks/done/nested/WP05.md", "no frontmatter at all\n")
	s.WriteFile("kitty-specs/001-legacy/tasks/done/README.md", "---\nlane: planned\n---\n")
	s.WriteFile("kitty-specs/002-flat/tasks/WP01.md", "---\nwork_package_id: WP01\nlane: planned\n---\n")
	s.WriteFile("kitty-specs/003-clean/tasks/doing/WP01.md", "---\nlane: doing\n---\n")
	s.WriteFile("kitty-specs/004-notasks/spec.md", "# spec\n")
}

func TestValidateTasksParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(legacyFixtures)
	p.Compare(true, "validate-tasks")
	p.Compare(true, "validate-tasks", "--mission", "  ")
	p.Compare(true, "validate-tasks", "--mission", "nope")
	p.Compare(true, "validate-tasks", "--mission", "002-flat")
	p.Compare(true, "validate-tasks", "--mission", "003-clean")
	p.Compare(true, "validate-tasks", "--mission", "004-notasks")
	p.Compare(true, "validate-tasks", "--mission", "001-legacy")
	p.Compare(true, "validate-tasks", "--all")
	p.CompareTree("kitty-specs", true)
	p.Compare(true, "validate-tasks", "--mission", "001-legacy", "--fix", "--agent", "codex", "--shell-pid", "4242")
	p.CompareTree("kitty-specs", true)
	p.Compare(true, "validate-tasks", "--mission", "001-legacy")

	p.Both(legacyFixtures)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".worktrees/001-legacy-lane-a/kitty-specs/001-legacy/tasks/doing/WP09.md", "---\nlane: for_review\n---\n")
	})
	p.Compare(true, "validate-tasks", "--all", "--fix", "--agent", "codex", "--shell-pid", "7")
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.Compare(true, "validate-tasks", "--all")

	// A narrow console collapses and wraps the table and summary panel.
	p.Both(legacyFixtures)
	p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, "COLUMNS=52") })
	p.Compare(true, "validate-tasks", "--all")
}

func TestValidateTasksParityEdgeCases(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", "agents:\n  available: [claude]\n")
	})
	p.Compare(true, "validate-tasks", "--all")
	// A YAML error degrades to empty metadata plus a logged warning with
	// PyYAML's exact error text.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/001-x/tasks/doing/WP01.md", "---\nlane: [a\n---\n")
	})
	p.Compare(true, "validate-tasks", "--mission", "001-x")
	// A ValueError while constructing (an impossible date) is not a
	// YAMLError; detect_lane_mismatch's except Exception swallows it.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/001-x/tasks/doing/WP01.md", "---\nlane: doing\nat: 2024-02-30\n---\n")
	})
	g, o := p.Compare(false, "validate-tasks", "--mission", "001-x")
	if a, b := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr)); a != b {
		t.Errorf("traceback differs\n%s", testkit.Diff(b, a))
	}
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/001-x/tasks/doing/WP01.md", "---\nlane: [a\n---\n")
	})
	// A non-string lane is an uncaught AttributeError: compare the rich
	// traceback (frames masked).
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/001-x/tasks/doing/WP02.md", "---\nlane: 5\n---\n")
	})
	g, o = p.Compare(false, "validate-tasks", "--mission", "001-x")
	if gl, ol := testkit.MaskTracebackFrames(p.Go.Normalize(g.Stderr)), testkit.MaskTracebackFrames(p.Oracle.Normalize(o.Stderr)); gl != ol {
		t.Errorf("traceback differs\n%s", testkit.Diff(ol, gl))
	}
}

func TestValidateTasksE2E(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	legacyFixtures(s)
	r := s.Run("validate-tasks", "--mission", "001-legacy")
	if r.Code != 1 || !strings.Contains(r.Stdout, "Found 4 metadata mismatch(es).") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("validate-tasks", "--mission", "001-legacy", "--fix", "--agent", "codex", "--shell-pid", "1")
	if r.Code != 0 || !strings.Contains(r.Stdout, "Fixed 4 metadata mismatch(es).") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	got := s.ReadFile("kitty-specs/001-legacy/tasks/done/WP04.md")
	if !strings.HasPrefix(got, "\ufeff---\nwork_package_id: WP04\nlane: done\nactivity_log:") {
		t.Fatalf("got %q", got)
	}
}
