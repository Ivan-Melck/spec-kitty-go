package planning

import (
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

const (
	agentHelp   = "Commands for AI agents to execute spec-kitty mission actions programmatically"
	missionHelp = "Mission lifecycle commands for AI agents"
	tasksHelp   = "Task workflow commands for AI agents"
)

// agentGroups returns (agent, agent mission, agent tasks), creating them.
func agentGroups(root *cli.Command) (agent, missionG, tasksG *cli.Command) {
	agent = app.Group(root, "agent", agentHelp)
	missionG = app.Group(agent, "mission", missionHelp)
	tasksG = app.Group(agent, "tasks", tasksHelp)
	return
}

// registrations collected from this package's files.
var registrations []func(root, agent, missionG, tasksG *cli.Command)

func addCommands(fn func(root, agent, missionG, tasksG *cli.Command)) {
	registrations = append(registrations, fn)
}

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(safeCommitCommand(), specCommitCommand())
		a, m, t := agentGroups(root)
		for _, fn := range registrations {
			fn(root, a, m, t)
		}
	})
}
