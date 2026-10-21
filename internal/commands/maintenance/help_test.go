package maintenance_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// helpText trims trailing whitespace and blank lines (box layout and
// padding are best-effort in the shared help renderer).
func helpText(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "╭") {
			break // option/command boxes: layout comes from the shared renderer
		}
		if l = strings.TrimRight(l, " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestDiffMaintenanceHelp(t *testing.T) {
	p := newOraclePair(t)
	steps := [][]string{{"materialize", "--help"}, {"migrate", "--help"}}
	for _, l := range []string{"backfill-identity", "backfill-merge-commit", "backfill-topology", "backfill-mission-type", "charter-encoding",
		"backfill-provenance", "normalize-lifecycle", "rewrite-opposed-by", "backfill-runtime-state", "backfill-wp-status",
		"rebaseline-dossier-hashes", "repin-hooks"} {
		steps = append(steps, []string{"migrate", l, "--help"})
	}
	for _, l := range doctorLeaves {
		steps = append(steps, []string{"doctor", l, "--help"})
	}
	for _, s := range steps {
		g, o := p.Run(s...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d", s, g.Code, o.Code)
		}
		if gh, oh := helpText(g.Stdout), helpText(o.Stdout); gh != oh {
			t.Errorf("%v: help differs\n%s", s, testkit.Diff(oh, gh))
		}
	}
}
