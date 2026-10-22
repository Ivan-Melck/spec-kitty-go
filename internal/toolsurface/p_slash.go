package toolsurface

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
)

const slashRepairHint = "spec-kitty doctor tool-surfaces --kind command-file --fix"

type slashCommandsProvider struct{}

func (slashCommandsProvider) key() string              { return "slash_commands" }
func (slashCommandsProvider) handles(kind string) bool { return kind == KindCommandFile }
func (slashCommandsProvider) definitions() []Definition {
	return []Definition{{Kind: KindCommandFile, SourceKind: "user_global", Scope: "user_global", PathPattern: "<user-global>/spec-kitty.{command}",
		Required: "repairable_required", Activation: "user_invoked", Provider: "slash_commands", RepairHint: slashRepairHint}}
}

func (slashCommandsProvider) expand(def Definition, tool, root string) []Instance {
	if _, ok := agents.CommandConfigs[tool]; !ok {
		return []Instance{{Def: def, Path: "<unsupported>", Owner: tool}}
	}
	dir := agents.GlobalCommandDir(tool)
	cmds := append([]string(nil), agents.ConsumerCommands...)
	sort.Strings(cmds)
	var out []Instance
	for _, c := range cmds {
		out = append(out, Instance{Def: def, Path: filepath.Join(dir, agents.CommandFilename(c, tool)), Owner: tool})
	}
	return out
}

func (slashCommandsProvider) probe(c *collectCtx, inst Instance) Status {
	if inst.Path == "<unsupported>" {
		f := Finding{Code: "research-gap-surface", Severity: "info", Message: "No known slash-command implementation for " + inst.Owner + ".",
			Tool: inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true}
		return Status{inst, StateNotApplicable, []Finding{f}}
	}
	if _, err := os.Stat(inst.Path); err != nil {
		return Status{inst, StateMissing, []Finding{instFinding("generated-surface-missing", "error", "User-global slash command missing: "+inst.Path, inst, true, slashRepairHint)}}
	}
	if b, err := os.ReadFile(inst.Path); err == nil {
		lines := strings.Split(strings.ReplaceAll(strings.ToValidUTF8(string(b), "�"), "\r\n", "\n"), "\n")
		if len(lines) > 20 {
			lines = lines[:20]
		}
		if !strings.Contains(strings.Join(lines, "\n"), agents.VersionMarkerPrefix+" "+app.Version) {
			return Status{inst, StateStale, []Finding{instFinding("stale-generated-surface", "warning", "User-global slash command is stale: "+inst.Path, inst, true, slashRepairHint)}}
		}
	}
	return Status{inst, StatePresent, nil}
}

func (slashCommandsProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	return nil
}

func (slashCommandsProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing, StateStale)
	if len(actionable) == 0 {
		return RepairResult{}
	}
	skipped := idsOf(statusesOf(statuses, StateUnsupported))
	var keys []string
	for _, s := range actionable {
		keys = append(keys, s.Inst.Owner)
	}
	var repaired, failed []string
	for _, a := range uniqueSorted(keys) {
		if _, err := bootstrap.EnsureGlobalCommands([]string{a}); err != nil {
			failed = append(failed, a+": "+err.Error())
			continue
		}
		for _, s := range actionable {
			if s.Inst.Owner == a {
				repaired = append(repaired, s.Inst.ID())
			}
		}
	}
	return RepairResult{Repaired: repaired, Skipped: skipped, Failed: failed}
}
