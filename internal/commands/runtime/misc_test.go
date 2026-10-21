package runtime_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

var reCtxToken = regexp.MustCompile(`ctx-<ULID>`)

// TestDiffRuntimeMisc covers context *, mission run/close/follow-up/reopen
// refusals, agent tests stale-check, reconcile and the command help pages.
func TestDiffRuntimeMisc(t *testing.T) {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	steps(t, p, true,
		[]string{"context"},
		[]string{"context", "info", "--json"},
		[]string{"context", "info", "--workspace", "nope"},
		[]string{"context", "info", "--workspace", "nope", "--json"},
		[]string{"context", "list"},
		[]string{"context", "list", "--json"},
		[]string{"context", "list", "--orphaned"},
		[]string{"context", "list", "--orphaned", "--json"},
		[]string{"context", "cleanup", "--dry-run"},
		[]string{"context", "mission-resolve", "--wp", "WP01", "--mission", "$M", "--json"},
		[]string{"context", "mission-resolve", "--wp", "WP09", "--mission", "$M", "--json"},
		[]string{"context", "mission-resolve", "--wp", "WP01", "--json"},
		[]string{"context", "mission-show", "--context", "ctx-nope"},
		[]string{"context", "mission-show", "--context", "ctx-nope", "--json"},
		[]string{"mission", "run", "custom-x", "--mission", "$M", "--json"},
		[]string{"mission", "run", "custom-x", "--mission", "$M"},
		[]string{"mission", "run", "software-dev", "--mission", "$M", "--json"},
		[]string{"mission", "close", "--mission", "$M", "--json"},
		[]string{"mission", "close", "--mission", "$M"},
		[]string{"mission-type", "close", "--mission", "$M"},
		[]string{"mission", "follow-up", "$M", "--pr", "3", "--json"},
		[]string{"mission", "follow-up", "$M", "--pr", "3"},
		[]string{"mission", "follow-up", "$M", "--json"},
		[]string{"mission", "follow-up", "$M", "--commit", "abc", "--json"},
		[]string{"mission", "reopen", "$M", "--reason", "why", "--json"},
		[]string{"mission", "reopen", "$M", "--reason", "why"},
		[]string{"reconcile", "--mission", "$M", "--json"},
		[]string{"reconcile", "--mission", "$M"},
		[]string{"reconcile", "--mission", "nope"},
		[]string{"agent", "tests", "stale-check", "--base", "HEAD~1", "--json"},
	)
	// context info from inside the lane worktree; resolve + show a token
	p.Both(func(s *testkit.Sandbox) {
		_ = s
	})
	g := p.Go.RunIn(laneDir(t, p.Go, "lane-a"), "context", "info")
	o := p.Oracle.Exec(laneDir(t, p.Oracle, "lane-a"), testkit.Oracle(t), "context", "info")
	if g.Code != o.Code || norm(p.Go, g.Stdout) != norm(p.Oracle, o.Stdout) {
		t.Errorf("context info in worktree differs:\n%s", testkit.Diff(norm(p.Oracle, o.Stdout), norm(p.Go, g.Stdout)))
	}
	gt := strings.TrimSpace(p.Go.Run("context", "mission-resolve", "--wp", "WP02", "--mission", missionfx.MissionSlug(t, p.Go)).Stdout)
	ot := strings.TrimSpace(p.Oracle.RunOracle("context", "mission-resolve", "--wp", "WP02", "--mission", missionfx.MissionSlug(t, p.Oracle)).Stdout)
	for _, mode := range [][]string{{}, {"--json"}} {
		g := p.Go.Run(append([]string{"context", "mission-show", "--context", gt}, mode...)...)
		o := p.Oracle.RunOracle(append([]string{"context", "mission-show", "--context", ot}, mode...)...)
		gs := reCtxToken.ReplaceAllString(norm(p.Go, g.Stdout), "ctx-<T>")
		os_ := reCtxToken.ReplaceAllString(norm(p.Oracle, o.Stdout), "ctx-<T>")
		if g.Code != o.Code || gs != os_ {
			t.Errorf("mission-show %v differs (exit %d/%d):\n%s", mode, g.Code, o.Code, testkit.Diff(os_, gs))
		}
	}
	// orphaned context: remove the worktree dir registration
	p.Both(func(s *testkit.Sandbox) {
		s.Git("worktree", "remove", "--force", filepath.Join(s.Dir, laneDir(t, s, "lane-a")))
	})
	steps(t, p, false,
		[]string{"context", "list"},
		[]string{"context", "list", "--orphaned"},
		[]string{"context", "list", "--orphaned", "--json"},
		[]string{"context", "cleanup", "--dry-run"},
		[]string{"context", "cleanup"},
		[]string{"context", "list", "--json"},
	)
}

// TestDiffRuntimeHelp checks the help pages of the area-F commands: exit
// code and the usage line (the box layout is the shared renderer's).
func TestDiffRuntimeHelp(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	usage := func(out string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "Usage:") {
				return strings.TrimSpace(l)
			}
		}
		return ""
	}
	for _, c := range [][]string{
		{"agent", "action", "--help"}, {"agent", "action", "implement", "--help"}, {"agent", "action", "review", "--help"},
		{"implement", "--help"}, {"next", "--help"}, {"context", "--help"}, {"context", "info", "--help"},
		{"context", "list", "--help"}, {"context", "cleanup", "--help"}, {"context", "mission-resolve", "--help"},
		{"context", "mission-show", "--help"}, {"mission", "run", "--help"}, {"mission", "close", "--help"},
		{"mission", "follow-up", "--help"}, {"mission", "reopen", "--help"}, {"agent", "tests", "--help"},
		{"agent", "tests", "stale-check", "--help"}, {"reconcile", "--help"}, {"implement"}, {"agent", "action"},
	} {
		g, o := p.Run(c...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d", c, g.Code, o.Code)
		}
		if gu, ou := usage(g.Stdout+g.Stderr), usage(o.Stdout+o.Stderr); gu != ou {
			t.Errorf("%v: usage differs\n oracle: %s\n go:     %s", c, ou, gu)
		}
	}
}
