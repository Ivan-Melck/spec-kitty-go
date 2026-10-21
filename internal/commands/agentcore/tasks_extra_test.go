package agentcore_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffTasksExtras(t *testing.T) {
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "list-dependents", "WP01", "--mission", "$M"},
		{"agent", "tasks", "list-dependents", "WP01", "--mission", "$M", "--json"},
		{"agent", "tasks", "list-dependents", "WP02", "--mission", "$M", "--json"},
		{"agent", "tasks", "list-dependents", "WP02", "--mission", "$M"},
		{"agent", "tasks", "list-dependents", "WP09", "--mission", "$M", "--json"},
		{"agent", "tasks", "list-dependents", "WP01", "--json"},
		{"agent", "tasks", "validate-workflow", "WP01", "--mission", "$M"},
		{"agent", "tasks", "validate-workflow", "WP02", "--mission", "$M", "--json"},
		{"agent", "tasks", "validate-workflow", "WP09", "--mission", "$M", "--json"},
		{"agent", "tasks", "check-terminability", "--mission", "$M"},
		{"agent", "tasks", "check-terminability", "--mission", "$M", "--json"},
	})
	// TRACE 9.7 / 9.8: bare slug, full mission_id and mid8 handles.
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "status", "--mission", "calc-ops"},
		{"agent", "tasks", "status", "--mission", "calc-ops", "--json"},
	})
	idOf := func(s *testkit.Sandbox) (string, string) {
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(filepath.Join("kitty-specs", missionSlug(t, s), "meta.json"))))
		if err != nil {
			t.Fatal(err)
		}
		id := meta.GetString("mission_id")
		return id, id[:8]
	}
	gid, gmid := idOf(p.Go)
	oid, omid := idOf(p.Oracle)
	for _, h := range [][2]string{{gid, oid}, {gmid, omid}} {
		gr := p.Go.Run("agent", "tasks", "status", "--mission", h[0], "--json")
		or := p.Oracle.RunOracle("agent", "tasks", "status", "--mission", h[1], "--json")
		if gr.Code != or.Code || p.Go.Normalize(gr.Stdout) != p.Oracle.Normalize(or.Stdout) {
			t.Errorf("handle %s: go %d vs oracle %d\n%s", h[0], gr.Code, or.Code, testkit.Diff(p.Oracle.Normalize(or.Stdout), p.Go.Normalize(gr.Stdout)))
		}
	}
	p.Both(func(s *testkit.Sandbox) {
		f := filepath.Join("kitty-specs", missionSlug(t, s), "tasks", "WP02-multiply.md")
		s.WriteFile(f, s.ReadFile(f)+"\n- [ ] Verified post-merge on main\n- CI is green in CI once enabled for consecutive runs\n")
	})
	comparePairSteps(t, p, g, o, [][]string{
		{"agent", "tasks", "check-terminability", "--mission", "$M"},
		{"agent", "tasks", "check-terminability", "--mission", "$M", "--json"},
	})
}

// TestDiffCoreUsageErrors compares click usage errors (exit 2 + stderr).
func TestDiffCoreUsageErrors(t *testing.T) {
	p := newOraclePair(t)
	for _, c := range [][]string{
		{"agent", "tasks", "move-task", "WP01"},
		{"agent", "tasks", "move-task", "--to", "planned"},
		{"agent", "tasks", "mark-status", "T001"},
		{"agent", "tasks", "mark-status", "--status", "done"},
		{"agent", "tasks", "add-history", "--note", "x"},
		{"agent", "tasks", "add-history", "WP01"},
		{"agent", "tasks", "list-dependents"},
		{"agent", "tasks", "validate-workflow"},
		{"agent", "status", "emit", "WP01", "--to", "planned"},
		{"agent", "status", "emit", "WP01", "--actor", "x"},
		{"agent", "tasks", "move-task", "WP01", "--to", "planned", "--bogus"},
	} {
		p.Compare(true, c...)
	}
}
