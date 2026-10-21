package doctrine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func createMission(t *testing.T, s *testkit.Sandbox, exe string) string {
	t.Helper()
	r := s.Exec("", exe, "agent", "mission", "create", "calc-ops", "--mission-type", "software-dev",
		"--friendly-name", "Calculator Operations", "--purpose-tldr", "Add ops.", "--purpose-context", "Ctx.",
		"--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	if r.Code != 0 {
		t.Fatalf("mission create: %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("mission create json: %v\n%s", err, r.Stdout)
	}
	return out["mission_slug"].(string)
}

func TestAgentContextResolveGoOnly(t *testing.T) {
	s := newProject(t)
	slug := createMission(t, s, testkit.Binary(t))
	r := s.MustRun("agent", "context", "resolve", "--action", "plan", "--mission", slug, "--json")
	for _, want := range []string{`"success": true`, `"target_branch": "feat/calc-ops"`, `"check_prerequisites": "spec-kitty agent mission check-prerequisites --json --paths-only --include-tasks --mission ` + slug + `"`} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %s in:\n%s", want, r.Stdout)
		}
	}
	r = s.Run("agent", "context", "resolve", "--action", "implement", "--mission", slug, "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, "WORK_PACKAGE_UNRESOLVED") {
		t.Fatalf("implement without WPs: %d %s", r.Code, r.Stdout)
	}
	r = s.Run("agent", "context", "resolve", "--action", "bogus", "--mission", slug, "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, "INVALID_ACTION") {
		t.Fatalf("bogus action: %d %s", r.Code, r.Stdout)
	}
}

func TestParityAgentContextResolve(t *testing.T) {
	p := newOraclePair(t)
	slugs := map[*testkit.Sandbox]string{}
	p.Both(func(s *testkit.Sandbox) { slugs[s] = createMission(t, s, testkit.Oracle(t)) })
	for _, tmpl := range [][]string{
		{"--action", "plan", "--mission", "{slug}", "--json"},
		{"--action", "specify", "--mission", "{slug}"},
		{"--action", "status", "--mission", "{slug}", "--json"},
		{"--action", "accept", "--mission", "{slug}", "--json"},
		{"--action", "implement", "--mission", "{slug}", "--json"},
		{"--action", "review", "--mission", "{slug}", "--json"},
		{"--action", "implement", "--mission", "{slug}", "--wp-id", "WP01", "--json"},
		{"--action", "bogus", "--mission", "{slug}", "--json"},
		{"--action", "plan", "--json"},
		{"--action", "plan", "--mission", "calc-ops", "--json"},
		{"--action", "plan", "--mission", "nope"},
	} {
		run := func(s *testkit.Sandbox, exe string) testkit.Result {
			args := []string{"agent", "context", "resolve"}
			for _, a := range tmpl {
				args = append(args, strings.ReplaceAll(a, "{slug}", slugs[s]))
			}
			return s.Exec("", exe, args...)
		}
		g, o := run(p.Go, testkit.Binary(t)), run(p.Oracle, testkit.Oracle(t))
		name := strings.Join(tmpl, " ")
		if g.Code != o.Code {
			t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", name, g.Code, o.Code, g.Stdout, o.Stdout)
		}
		if gs, os_ := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); gs != os_ {
			t.Errorf("%s: stdout differs\n%s", name, testkit.Diff(os_, gs))
		}
	}
}
