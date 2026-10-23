package toolsurface

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
)

func init() {
	agents.CommandSkillInstallHook = func(root, agent, now string) (agents.CommandSkillReport, error) {
		return InstallCommandsForAgents(root, []string{agent}, now, false)
	}
}

func commandInstallReport(a *Assessment) agents.CommandSkillReport {
	var r agents.CommandSkillReport
	if p, ok := a.Prepared.(*PreparedCommands); ok {
		for _, command := range p.Commands {
			switch command.ReportKind {
			case "added":
				r.Added = append(r.Added, command.Path)
			case "already_installed":
				r.AlreadyInstalled = append(r.AlreadyInstalled, command.Path)
			case "reused_shared":
				r.ReusedShared = append(r.ReusedShared, command.Path)
			case "regenerated":
				r.Regenerated = append(r.Regenerated, command.Path)
			case "collision":
				r.Collisions = append(r.Collisions, command.Path)
			case "legacy_removed":
				r.LegacyRemoved = append(r.LegacyRemoved, command.Path)
			case "legacy_kept":
				r.LegacyKept = append(r.LegacyKept, command.Path)
			case "legacy_unrecorded":
				r.LegacyUnrecorded = append(r.LegacyUnrecorded, command.Path)
			}
		}
	}
	return r
}

func InstallCommandsForAgents(root string, keys []string, now string, dry bool) (agents.CommandSkillReport, error) {
	a := PrepareCommands(Root{"project", "project", absPath(root)}, keys, CommandOptions{Now: now})
	if !a.Complete {
		messages := []string{}
		for _, d := range a.Diagnostics {
			messages = append(messages, d.Message)
		}
		return agents.CommandSkillReport{}, fmt.Errorf("%s", strings.Join(messages, "; "))
	}
	r := commandInstallReport(a)
	if !dry {
		result := ApplyCommands(a)
		if result.Outcome != "applied" && result.Outcome != "skipped" {
			messages := []string{}
			for _, d := range result.Diagnostics {
				messages = append(messages, d.Message)
			}
			return r, fmt.Errorf("%s", strings.Join(messages, "; "))
		}
	}
	return r, nil
}
