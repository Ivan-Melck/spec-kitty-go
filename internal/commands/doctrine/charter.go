package doctrine

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// charterLeaves returns the charter subcommands in upstream help order.
// Constructors live in the per-command files.
func charterLeaves() []*cli.Command {
	var out []*cli.Command
	for _, f := range []func() *cli.Command{
		charterContextCmd,
		charterMissionTypeGroup,
	} {
		if c := f(); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "charter", "Charter management commands")
		g.Add(charterLeaves()...)
	})
}
