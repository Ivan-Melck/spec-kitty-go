package setup_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestUpgradeProbeDegradedWarningMatchesOracle: with a corrupt git index the
// residue probe (probe_tracked_mission_paths) degrades to file presence and
// upstream's CLI logging bootstrap prints one WARNING on stderr per run. The
// 2.0.6 ledger records are dropped so the residue-safe 2.0.6 sweep's detect
// walks kitty-specs/. --json silences the warning.
func TestUpgradeProbeDegradedWarningMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	drop := regexp.MustCompile(`"2\.0\.6_`)
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("init", ".", "--ai", "claude", "--non-interactive"); r.Code != 0 {
			t.Fatalf("oracle init: %s%s", r.Stdout, r.Stderr)
		}
		setVersion(s, "2.0.0", true)
		var keep []string
		for _, l := range strings.Split(s.ReadFile(".kittify/migration-ledger.jsonl"), "\n") {
			if !drop.MatchString(l) {
				keep = append(keep, l)
			}
		}
		s.WriteFile(".kittify/migration-ledger.jsonl", strings.Join(keep, "\n"))
		s.WriteFile("kitty-specs/001-demo/spec.md", "# Spec\n")
		s.WriteFile("kitty-specs/002-residue/tasks/WP01.md", "---\nwork_package_id: WP01\n---\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "legacy")
		s.WriteFile(".git/index", "not an index\n")
	})
	p.Go.Run("mission", "list", "--json")
	for _, args := range [][]string{
		{"upgrade", "--dry-run"},
		{"upgrade", "--dry-run", "--json"},
		{"upgrade", "--yes"},
	} {
		g, o := compareMasked(t, p, args...)
		ge, oe := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr)
		if !strings.Contains(oe, "git could not list tracked kitty-specs/ paths") && !strings.Contains(strings.Join(args, " "), "--json") {
			t.Fatalf("%v: oracle did not print the degraded-probe warning; the fixture no longer reaches the probe:\n%s", args, o.Stderr)
		}
		// The message wraps at the console width, which depends on the
		// sandbox path length: compare the words, then the layout when the
		// two paths are equally long.
		if gw, ow := strings.Join(strings.Fields(ge), " "), strings.Join(strings.Fields(oe), " "); gw != ow {
			t.Errorf("%v: stderr differs\n oracle: %s\n go:     %s", args, ow, gw)
		} else if len(p.Go.Dir) == len(p.Oracle.Dir) && ge != oe {
			t.Errorf("%v: stderr layout differs\n%s", args, testkit.Diff(oe, ge))
		}
	}
}
