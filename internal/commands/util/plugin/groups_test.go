package plugin

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Bare utility groups print their help; compare exit codes.
func TestUtilGroupsBareParity(t *testing.T) {
	p := testkit.NewPair(t)
	for _, g := range []string{"plugin", "docs", "workflow", "events"} {
		gr, or := p.Run(g)
		if gr.Code != or.Code {
			t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", g, gr.Code, or.Code, gr.Stdout+gr.Stderr, or.Stdout+or.Stderr)
		}
	}
}
