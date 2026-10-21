package doctrine

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// charterBuilders lists the charter subcommand constructors in upstream
// help order (activate, deactivate, consistency-check, new, validate, fetch,
// interview, generate, context, status, synthesize, resynthesize, lint,
// preflight, bundle, mission-type, list, pack, org). Commands not ported yet
// are registered by their own files through registerCharterLeaf.
var charterBuilders = []func() *cli.Command{
	charterActivateCmd,
	charterDeactivateCmd,
	charterContextCmd,
	charterMissionTypeGroup,
	charterListCmd,
}

// charterOrder is the upstream help order of charter subcommands.
var charterOrder = []string{"activate", "deactivate", "consistency-check", "new", "validate", "fetch", "interview",
	"generate", "context", "status", "synthesize", "resynthesize", "lint", "preflight", "bundle", "mission-type",
	"list", "pack", "org"}

// registerCharterLeaf adds a constructor (called from other files' init).
func registerCharterLeaf(f func() *cli.Command) { charterBuilders = append(charterBuilders, f) }

// charterLeaves returns the charter subcommands in upstream help order.
func charterLeaves() []*cli.Command {
	byName := map[string]*cli.Command{}
	for _, f := range charterBuilders {
		if c := f(); c != nil {
			byName[c.Name] = c
		}
	}
	var out []*cli.Command
	for _, n := range charterOrder {
		if c := byName[n]; c != nil {
			out = append(out, c)
			delete(byName, n)
		}
	}
	for _, c := range byName {
		out = append(out, c)
	}
	return out
}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "charter", "Charter management commands")
		g.Add(charterLeaves()...)
	})
}
