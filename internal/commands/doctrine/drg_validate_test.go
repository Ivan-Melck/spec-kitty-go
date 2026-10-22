package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const danglingOverlay = `schema_version: "1.0"
generated_at: "2026-01-01T00:00:00+00:00"
generated_by: test
nodes: []
edges:
- source: directive:DIRECTIVE_024
  target: directive:NOPE_999
  relation: requires
`

// lastLine is the final non-empty line block of a traceback (the
// "<Type>: <message>" record, possibly wrapped).
func tracebackTail(s string) string {
	i := strings.LastIndex(s, "DRGValidationError: ")
	if i < 0 {
		return ""
	}
	return s[i:]
}

// TestParityLoadValidatedGraph covers the load_validated_graph validation
// pass (DOC-16): a project overlay with a dangling edge.
func TestParityLoadValidatedGraph(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/directive.graph.yaml", danglingOverlay)
	})
	compareCharter(t, p, "charter", "consistency-check", "--json")
	compareCharter(t, p, "charter", "interview", "--defaults")
	compareCharter(t, p, "charter", "generate", "--json")
	if g, o := p.Run("charter", "context", "--action", "implement", "--mission-type", "software-dev", "--no-mark-loaded"); g.Code != o.Code || g.Stderr != o.Stderr {
		t.Errorf("bootstrap warning: go=%d %q oracle=%d %q", g.Code, g.Stderr, o.Code, o.Stderr)
	}
	compareCharter(t, p, "charter", "context", "--action", "implement", "--mission-type", "software-dev", "--json")
	compareMasked(t, p, true, "charter", "context", "--action", "implement", "--mission-type", "software-dev")
	// Uncaught DRGValidationError: exit 1, empty stdout, same final line.
	for _, args := range [][]string{
		{"charter", "activate", "directive", "024-locality-of-change", "--no-compile"},
	} {
		g, o := p.Run(args...)
		if g.Code != o.Code || g.Stdout != o.Stdout || tracebackTail(g.Stderr) != tracebackTail(o.Stderr) || tracebackTail(o.Stderr) == "" {
			t.Errorf("%v: go=%d %q %q oracle=%d %q %q", args, g.Code, g.Stdout, tracebackTail(g.Stderr), o.Code, o.Stdout, tracebackTail(o.Stderr))
		}
		compareTracebackFrames(t, strings.Join(args, " "), g, o)
	}
}
