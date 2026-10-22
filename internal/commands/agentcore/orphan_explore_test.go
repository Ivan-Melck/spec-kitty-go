package agentcore_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestExploreOrphanWarningCounts (KITTY_EXPLORE=1) reports, per human
// command, how many "(orphaned event)" warnings each side logs.
func TestExploreOrphanWarningCounts(t *testing.T) {
	if os.Getenv("KITTY_EXPLORE") == "" {
		t.Skip("exploration only")
	}
	p := newOraclePair(t)
	g, o := oracleCalcMission(t, p)
	p.Go.Env = append(p.Go.Env, "SWEEP_TRACE_READS=1")
	p.Both(func(s *testkit.Sandbox) {
		slug := missionSlug(t, s)
		f := ".worktrees/" + slug + "-coord/kitty-specs/" + slug + "/status.events.jsonl"
		s.WriteFile(f, s.ReadFile(f)+`{"actor": "x", "at": "2026-01-01T00:00:00+00:00", "event_id": "01JAAAAAAAAAAAAAAAAAAAAAAA", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "ghost-mission", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP09"}`+"\n")
	})
	cmds := [][]string{
		{"agent", "tasks", "list-dependents", "WP01", "--mission", "$M"},
		{"agent", "tasks", "validate-workflow", "WP01", "--mission", "$M"},
		{"materialize", "--mission", "$M"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "add-history", "WP01", "--note", "x", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "T001", "--status", "done", "--mission", "$M"},
		{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
		{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		{"agent", "tasks", "move-task", "WP02", "--to", "claimed", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_progress", "--mission", "$M", "--agent", "claude", "--force"},
		{"agent", "tasks", "move-task", "WP02", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--force"},
		{"agent", "tasks", "move-task", "WP02", "--to", "in_review", "--mission", "$M", "--agent", "rev", "--force"},
		{"agent", "tasks", "move-task", "WP02", "--to", "approved", "--mission", "$M", "--agent", "rev", "--force", "--note", "ok"},
		{"agent", "tasks", "move-task", "WP02", "--to", "done", "--mission", "$M", "--agent", "rev", "--force"},
		{"agent", "status", "emit", "WP01", "--to", "in_progress", "--mission", "$M", "--actor", "x", "--force"},
		{"agent", "tasks", "status", "--mission", "$M"},
		{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"},
		{"orchestrator-api", "list-ready", "--mission", "$M"},
		{"next", "--agent", "claude", "--mission", "$M"},
		{"accept", "--mission", "$M", "--mode", "checklist"},
	}
	cmds = append(cmds, [][]string{
		{"agent", "status", "validate", "--mission", "$M"},
		{"agent", "status", "lifecycle", "--mission", "$M"},
		{"agent", "status", "doctor", "--mission", "$M"},
		{"agent", "tasks", "list-tasks", "--mission", "$M"},
		{"agent", "tasks", "status", "--mission", "$M"},
		{"agent", "status", "materialize", "--mission", "$M"},
		{"next", "--agent", "claude", "--mission", "$M"},
		{"mission", "current"},
		{"mission", "info", "--mission", "$M"},
		{"mission", "show", "--mission", "$M"},
		{"mission", "list"},
		{"context", "mission-show", "--mission", "$M"},
		{"context", "info"},
		{"agent", "tasks", "list-dependents", "WP01", "--mission", "$M"},
		{"agent", "tasks", "validate-workflow", "WP01", "--mission", "$M"},
		{"agent", "tasks", "check-terminability", "--mission", "$M"},
		{"agent", "context", "resolve", "--mission", "$M"},
		{"agent", "status", "reconcile", "--mission", "$M"},
		{"agent", "mission", "check-prerequisites", "--mission", "$M"},
		{"agent", "mission", "branch-context", "--mission", "$M"},
		{"materialize", "--mission", "$M"},
		{"reconcile", "--mission", "$M"},
		{"doctor", "mission-state"},
		{"doctor", "workspaces"},
		{"doctor", "topology"},
		{"doctor", "coordination"},
		{"doctor", "identity"},
		{"doctor", "review-cycle-reconcile"},
		{"retrospect", "summary"},
		{"agent", "retrospect", "summary"},
		{"events", "tail"},
		{"issue-matrix", "--mission", "$M"},
		{"orchestrator-api", "mission-state", "--mission", "$M"},
		{"orchestrator-api", "list-ready", "--mission", "$M"},
		{"merge", "--dry-run", "--mission", "$M"},
		{"accept", "--mission", "$M", "--mode", "checklist"},
		{"agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", "$M", "--agent", "claude"},
		{"agent", "tasks", "add-history", "WP01", "--note", "x", "--mission", "$M"},
		{"agent", "tasks", "mark-status", "T001", "--status", "done", "--mission", "$M"},
		{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
		{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		{"agent", "status", "emit", "WP02", "--to", "claimed", "--mission", "$M", "--actor", "x"},
		{"implement", "WP02", "--mission", "$M"},
		{"orchestrator-api", "start-implementation", "--mission", "$M", "--wp", "WP01", "--actor", "bot"},
		{"orchestrator-api", "transition", "--mission", "$M", "--wp", "WP01", "--to", "for_review", "--actor", "bot"},
		{"orchestrator-api", "start-review", "--mission", "$M", "--wp", "WP01", "--actor", "rev"},
		{"orchestrator-api", "append-history", "--mission", "$M", "--wp", "WP01", "--actor", "bot", "--note", "x"},
		{"orchestrator-api", "next", "--mission", "$M", "--agent", "claude"},
		{"mission", "close", "--mission", "$M"},
	}...)
	count := func(s string) int { return strings.Count(s, "(orphaned event)") }
	for _, c := range cmds {
		gr := p.Go.Run(subst(c, g)...)
		or := p.Oracle.RunOracle(subst(c, o)...)
		mark := ""
		if count(gr.Stderr+gr.Stdout) != count(or.Stderr+or.Stdout) {
			mark = "   <<<< DIFF"
		}
		t.Logf("%-60s oracle=%d(rc %d) go=%d(rc %d)%s", strings.Join(c, " "), count(or.Stderr+or.Stdout), or.Code, count(gr.Stderr+gr.Stdout), gr.Code, mark)
		if mark != "" {
			for _, l := range strings.Split(or.Stderr+gr.Stderr, "\n") {
				if strings.HasPrefix(l, "READPASS") || strings.HasPrefix(l, "GOREAD") {
					t.Log("    " + l)
				}
			}
		}
	}
}
