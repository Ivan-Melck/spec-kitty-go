package commitguard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestFnmatch(t *testing.T) {
	for _, c := range []struct {
		name, pat string
		want      bool
	}{
		{"src/a/b.py", "src/*", true},
		{"src/a.py", "src/*.py", true},
		{"src/a.pyc", "src/*.py", false},
		{"a.md", "?.md", true},
		{"b.md", "[abc].md", true},
		{"d.md", "[!abc].md", true},
		{"a.md", "[!abc].md", false},
		{"x[.md", "x[.md", true},
		{"Src/a", "src/*", false},
	} {
		if got := Fnmatch(c.name, c.pat); got != c.want {
			t.Errorf("Fnmatch(%q, %q) = %v", c.name, c.pat, got)
		}
	}
	if !matchesAnyGlob("src/deep/x.go", []string{"src/**"}) || matchesAnyGlob("srcx/a", []string{"src/**"}) {
		t.Fatal("prefix glob")
	}
}

func TestValidateStagedFilesModes(t *testing.T) {
	cfg := DefaultConfig()
	branch := "kitty/mission-001-demo-lane-a"
	r := ValidateStagedFiles([]string{"kitty-specs/001-demo/spec.md", "kitty-specs/001-demo/occurrence_map.yaml"}, nil, branch, cfg, &Scope{ContextSource: "absent"})
	if !r.Allowed || len(r.Warnings) != 1 {
		t.Fatalf("warn mode: %+v", r)
	}
	cfg.Mode = "block"
	if r := ValidateStagedFiles([]string{"kitty-specs/x/spec.md"}, nil, branch, cfg, nil); r.Allowed {
		t.Fatalf("block mode: %+v", r)
	}
	if r := ValidateStagedFiles([]string{"kitty-specs/x/spec.md"}, nil, "main", cfg, nil); !r.Allowed {
		t.Fatal("non-lane branch must be allowed")
	}
}

const lane = "kitty/mission-001-demo-lane-a"

func event(id, wp, from, to, at string) string {
	return fmt.Sprintf(`{"actor": "t", "at": "%s", "event_id": "%s", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "%s", "mission_slug": "001-demo", "policy_metadata": null, "reason": null, "review_ref": null, "to_lane": "%s", "wp_id": "%s"}`,
		at, id, from, to, wp)
}

func context(branch, currentWP string, laneWPs ...string) string {
	q := make([]string, len(laneWPs))
	for i, w := range laneWPs {
		q[i] = `"` + w + `"`
	}
	cur := "null"
	if currentWP != "" {
		cur = `"` + currentWP + `"`
	}
	return fmt.Sprintf(`{"wp_id": "WP01", "mission_slug": "001-demo", "worktree_path": ".worktrees/001-demo-lane-a", "branch_name": "%s", "base_branch": "main", "base_commit": "unknown", "dependencies": [], "created_at": "2026", "created_by": "implement-command", "vcs_backend": "git", "lane_id": "lane-a", "lane_wp_ids": [%s], "current_wp": %s}`,
		branch, strings.Join(q, ", "), cur)
}

type guardScenario struct {
	name   string
	branch string
	config string
	files  map[string]string
	stage  []string
}

var wpFile = "---\nwork_package_id: WP01\ntitle: Demo\nowned_files:\n- src/**\n- docs/readme.md\n---\n# WP01\n"

func baseMission(extra map[string]string) map[string]string {
	m := map[string]string{
		"kitty-specs/001-demo/meta.json":           `{"mission_slug": "001-demo"}`,
		"kitty-specs/001-demo/tasks/WP01-demo.md":  wpFile,
		"kitty-specs/001-demo/tasks/WP02-other.md": "---\nwork_package_id: WP02\n---\n",
		"kitty-specs/001-demo/status.events.jsonl": event("01A", "WP01", "planned", "in_progress", "2026-01-01T00:00:00+00:00") + "\n" +
			event("01B", "WP02", "planned", "claimed", "2026-01-01T00:00:01+00:00") + "\n",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

var guardScenarios = []guardScenario{
	{name: "main-branch", branch: "main", stage: []string{"kitty-specs/x/spec.md"}},
	{name: "nothing-staged", branch: lane},
	{name: "lane-no-context-warn", branch: lane, stage: []string{"kitty-specs/001-demo/spec.md", "kitty-specs/001-demo/occurrence_map.yaml", "src/a.py"}},
	{name: "lane-no-context-block", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n", stage: []string{"kitty-specs/001-demo/spec.md"}},
	{name: "invalid-mode", branch: lane, config: "policy:\n  commit_guard:\n    mode: Blockx\n", stage: []string{"kitty-specs/a/b.md"}},
	{name: "disabled", branch: lane, config: "policy:\n  commit_guard:\n    enabled: false\n    mode: block\n", stage: []string{"kitty-specs/a/b.md"}},
	{name: "legacy-block-key", branch: lane, config: "policy:\n  commit_guard:\n    mode: ' BLOCK '\n    block_kitty_specs: false\n", stage: []string{"kitty-specs/a/b.md"}},
	{name: "scope-violation", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n",
		files: baseMission(map[string]string{".kittify/workspaces/001-demo-lane-a.json": context(lane, "WP01", "WP01", "WP02")}),
		stage: []string{"src/deep/a.py", "docs/readme.md", "docs/other.md"}},
	{name: "stale-current-wp", branch: lane,
		files: baseMission(map[string]string{".kittify/workspaces/001-demo-lane-a.json": context(lane, "WP02", "WP01", "WP02")}),
		stage: []string{"other.txt"}},
	{name: "no-active-wp", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n",
		files: baseMission(map[string]string{".kittify/workspaces/001-demo-lane-a.json": context(lane, "", "WP02", "WP03")}),
		stage: []string{"src/a.py"}},
	{name: "ambiguous-contexts", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n",
		files: map[string]string{".kittify/workspaces/b.json": context(lane, "", "WP01"), ".kittify/workspaces/a.json": strings.Replace(context(lane, "", "WP01"), "lane-a\"", "lane-b\"", 1)},
		stage: []string{"src/a.py"}},
	{name: "missing-mission-dir", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n",
		files: map[string]string{".kittify/workspaces/x.json": context(lane, "", "WP01")}, stage: []string{"src/a.py"}},
	{name: "missing-event-log", branch: lane,
		files: map[string]string{".kittify/workspaces/x.json": context(lane, "", "WP01"), "kitty-specs/001-demo/meta.json": "{}"}, stage: []string{"src/a.py"}},
	{name: "missing-wp-file", branch: lane,
		files: map[string]string{".kittify/workspaces/x.json": context(lane, "", "WP01"),
			"kitty-specs/001-demo/status.events.jsonl": event("01A", "WP01", "planned", "in_progress", "2026-01-01T00:00:00+00:00") + "\n"},
		stage: []string{"src/a.py"}},
	{name: "wp-without-owned-files", branch: lane, config: "policy:\n  commit_guard:\n    mode: block\n",
		files: baseMission(map[string]string{".kittify/workspaces/001-demo-lane-a.json": context(lane, "", "WP01"),
			"kitty-specs/001-demo/tasks/WP01-demo.md": "---\nwork_package_id: WP01\ntitle: x\n---\n"}),
		stage: []string{"src/a.py"}},
	{name: "malformed-context-ignored", branch: lane,
		files: map[string]string{".kittify/workspaces/x.json": `{"lane_id": "lane-a", "lane_wp_ids": "WP01"}`, ".kittify/workspaces/y.json": "{"},
		stage: []string{"kitty-specs/a/b.md"}},
}

func prepare(s *testkit.Sandbox, sc guardScenario) {
	if sc.branch != "main" {
		s.Git("checkout", "-q", "-b", sc.branch)
	}
	if sc.config != "" {
		s.WriteFile(".kittify/config.yaml", sc.config)
	}
	for p, c := range sc.files {
		s.WriteFile(p, c)
	}
	for _, p := range sc.stage {
		if !s.Exists(p) {
			s.WriteFile(p, "x\n")
		}
		s.Git("add", "-f", p)
	}
}

func TestCommitGuardParity(t *testing.T) {
	testkit.Oracle(t)
	for _, sc := range guardScenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) { prepare(s, sc) })
			p.Compare(true, "commit-guard-hook")
			p.CompareTree(".kittify", false)
		})
	}
}

func TestCommitGuardE2E(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	prepare(s, guardScenarios[3])
	r := s.Run("commit-guard-hook", "ignored", "args")
	if r.Code != 1 || !strings.Contains(r.Stderr, "[spec-kitty guard] Commit blocked by ownership policy.") {
		t.Fatalf("got %d %q", r.Code, r.Stderr)
	}
}
