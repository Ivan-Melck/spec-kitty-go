package kittify_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffAgentConfigMissingWarnings: with .kittify/ present and no
// .kittify/config.yaml, upstream's load_agent_config logs "Config file not
// found: <path>" on every call; each command logs it as many times as the
// oracle (human mode; --json silences logging).
func TestDiffAgentConfigMissingWarnings(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, ".kittify", "config.yaml"))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "drop config")
	})
	count := func(s string) int { return strings.Count(s, "Config file not found") }
	for _, args := range [][]string{
		{"agent", "config", "list"},
		{"agent", "config", "status"},
		{"agent", "config", "sync"},
		{"agent", "config", "status", "--json"},
		{"session-start"},
		{"doctor", "command-files"},
		{"doctor", "tool-surfaces"},
		{"doctor", "skills"},
		{"doctor"},
		{"upgrade", "--dry-run"},
		{"agent", "config", "add", "codex"},
		{"agent", "config", "remove", "codex"},
		{"agent", "config", "add", "gemini"},
		{"agent", "config", "sync", "--json"},
		{"upgrade", "--yes"},
		{"init", ".", "--ai", "claude", "--non-interactive"},
	} {
		g := p.Go.Run(args...)
		o := p.Oracle.RunOracle(args...)
		warnLines := func(s *testkit.Sandbox, out string) string {
			var ls []string
			for _, l := range strings.Split(s.Normalize(out), "\n") {
				if strings.Contains(l, "Config file not found") {
					ls = append(ls, strings.TrimRight(l, " "))
				}
			}
			return strings.Join(ls, "\n")
		}
		if gw, ow := warnLines(p.Go, g.Stderr), warnLines(p.Oracle, o.Stderr); gw != ow {
			t.Errorf("%v: warning text differs\n%s", args, testkit.Diff(ow, gw))
		}
		if gc, oc := count(g.Stderr+g.Stdout), count(o.Stderr+o.Stdout); gc != oc || g.Code != o.Code {
			var trace []string
			for _, l := range strings.Split(o.Stderr, "\n") {
				if strings.HasPrefix(l, "CFGWARN") {
					trace = append(trace, l)
				}
			}
			t.Errorf("%v: config-not-found warnings oracle=%d (exit %d) go=%d (exit %d)\n%s", args, oc, o.Code, gc, g.Code, strings.Join(trace, "\n"))
		}
	}
}
