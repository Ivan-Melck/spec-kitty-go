// Package agentcore registers the core mission/status agent commands:
// `agent mission create|branch-context`, `agent tasks *` and
// `agent status *`.
package agentcore

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

const (
	agentHelp   = "Commands for AI agents to execute spec-kitty mission actions programmatically"
	missionHelp = "Mission lifecycle commands for AI agents"
	tasksHelp   = "Task workflow commands for AI agents"
	statusHelp  = "Canonical status management commands"
)

// Groups returns (agent mission, agent tasks, agent status), creating them.
func Groups(root *cli.Command) (missionG, tasksG, statusG *cli.Command) {
	agent := app.Group(root, "agent", agentHelp)
	missionG = app.Group(agent, "mission", missionHelp)
	tasksG = app.Group(agent, "tasks", tasksHelp)
	statusG = app.Group(agent, "status", statusHelp)
	// typer no_args_is_help on these nested groups: help, exit 2.
	missionG.NoArgsIsHelp, tasksG.NoArgsIsHelp, statusG.NoArgsIsHelp = true, true, true
	return
}

// registrations collected from this package's files (init order is file
// order; each file appends its commands to the right group).
var registrations []func(missionG, tasksG, statusG *cli.Command)

func addCommands(fn func(missionG, tasksG, statusG *cli.Command)) {
	registrations = append(registrations, fn)
}

func init() {
	app.Register(func(root *cli.Command) {
		m, t, s := Groups(root)
		for _, fn := range registrations {
			fn(m, t, s)
		}
		sortByOrder(m, missionOrder)
		sortByOrder(t, tasksOrder)
		sortByOrder(s, statusOrder)
	})
}

// Upstream registration order (help listings).
var (
	missionOrder = []string{"record-analysis", "acceptance-verdict", "branch-context", "create", "check-prerequisites", "setup-plan", "accept", "merge", "finalize-tasks", "repair"}
	tasksOrder   = []string{"move-task", "mark-status", "list-tasks", "add-history", "finalize-tasks", "map-requirements", "validate-workflow", "status", "list-dependents", "check-terminability"}
	statusOrder  = []string{"emit", "materialize", "doctor", "lifecycle", "migrate", "validate", "reconcile"}
)

func sortByOrder(g *cli.Command, order []string) {
	rank := map[string]int{}
	for i, n := range order {
		rank[n] = i
	}
	subs := g.Subcommands
	// stable insertion sort: known names by rank, unknown after, in place
	out := make([]*cli.Command, 0, len(subs))
	for _, n := range order {
		for _, c := range subs {
			if c.Name == n {
				out = append(out, c)
			}
		}
	}
	for _, c := range subs {
		if _, ok := rank[c.Name]; !ok {
			out = append(out, c)
		}
	}
	g.Subcommands = out
}
