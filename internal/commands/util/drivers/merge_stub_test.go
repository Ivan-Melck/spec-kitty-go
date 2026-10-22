package drivers

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// The deprecated `merge` stub always fails with the rename message,
// whatever flags it is given.
func TestMergeStubParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(true, "merge")
	p.Compare(true, "merge", "--mission", "001-x", "--dry-run", "extra")
	// The refusal is console.print'ed: wrapped at the console width.
	for _, cols := range []string{"COLUMNS=60", "COLUMNS=33"} {
		p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, cols) })
		p.Compare(true, "merge")
	}
}
