package toolsurface

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/agents"

	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// DriftPolicySummary is repair.DriftPolicySummary (paths as instance paths).
type DriftPolicySummary struct {
	Created, Repaired, Regenerated, DriftedOverwritten, DriftedReported, Skipped []string
	Warnings                                                                     []string
}

// Lines is repair.render_surface_summary_lines (rich markup).
func (s DriftPolicySummary) Lines() []string {
	var l []string
	if len(s.Created) > 0 {
		l = append(l, fmt.Sprintf("[dim]Created %d tool surface(s)[/dim]", len(s.Created)))
	}
	if len(s.Repaired) > 0 {
		l = append(l, fmt.Sprintf("[dim]Repaired %d stale tool surface(s)[/dim]", len(s.Repaired)))
	}
	if len(s.DriftedOverwritten) > 0 {
		l = append(l, fmt.Sprintf("[dim]Overwrote %d drifted tool surface(s)[/dim]", len(s.DriftedOverwritten)))
	}
	if len(s.DriftedReported) > 0 {
		l = append(l, fmt.Sprintf("[dim]Note: %d tool surface(s) have local edits — run 'spec-kitty doctor tool-surfaces' to review[/dim]", len(s.DriftedReported)))
	}
	if len(s.Skipped) > 0 {
		l = append(l, fmt.Sprintf("  %d surface(s) not applicable, skipped.", len(s.Skipped)))
	}
	for _, warning := range s.Warnings {
		l = append(l, "[yellow]Warning: "+warning+"[/yellow]")
	}
	return l
}

// agentDirKeys is AGENT_DIR_TO_KEY.values() (the fallback roster).
var agentDirKeys = []string{"claude", "copilot", "gemini", "cursor", "qwen", "opencode", "windsurf", "kilocode", "auggie", "q", "kiro", "antigravity", "llxprt"}

func initAutoRepairable(s Status) bool {
	d := s.Inst.Def
	if d.Required != "repairable_required" || !d.enabled() {
		return false
	}
	return !(d.Kind == KindAgentProfile && qAliases[s.Inst.Owner])
}

// RunSurfaceRepair is repair.run_surface_repair (init): auto-create missing
// and repair stale surfaces; drifted ones are overwritten only when
// repairDrift or the interactive prompt answers yes, else reported.
func RunSurfaceRepair(root string, interactive, repairDrift bool, prompt func(path string) bool) DriftPolicySummary {
	var sum DriftPolicySummary
	var configured []string
	if ac, err := kittify.LoadAgentConfig(root); err == nil {
		configured = ac.Available
	} else {
		configured = agentDirKeys
	}
	if len(configured) == 0 {
		return sum
	}
	ps := providers()
	c := newCtx(root)
	plans := buildPlansWith(c, ps, &registry{keys: configured}, configured, root, nil)
	report := collect(c, ps, root, plans, configured)
	var missing, stale, overwrite []Status
	for _, s := range report.Surfaces {
		switch s.State {
		case StateNotApplicable:
			sum.Skipped = append(sum.Skipped, s.Inst.Path)
		case StateMissing:
			missing = append(missing, s)
		case StateStale:
			stale = append(stale, s)
		case StateDrifted:
			if repairDrift || (interactive && prompt != nil && prompt(s.Inst.Path)) {
				overwrite = append(overwrite, s)
			} else {
				sum.DriftedReported = append(sum.DriftedReported, s.Inst.Path)
			}
		}
	}
	var auto []Status
	for _, s := range append(append([]Status(nil), missing...), stale...) {
		if initAutoRepairable(s) {
			auto = append(auto, s)
		}
	}
	if len(auto) > 0 {
		res := repairStatuses(newCtx(root), ps, root, auto, nil)
		for _, sid := range res.Repaired {
			for _, s := range auto {
				if s.Inst.ID() == sid {
					if s.State == StateMissing {
						sum.Created = append(sum.Created, s.Inst.Path)
					} else {
						sum.Repaired = append(sum.Repaired, s.Inst.Path)
					}
					break
				}
			}
		}
	}
	if len(overwrite) > 0 {
		repairStatuses(newCtx(root), ps, root, overwrite, nil)
		for _, s := range overwrite {
			sum.DriftedOverwritten = append(sum.DriftedOverwritten, s.Inst.Path)
		}
	}
	if warning := agents.RefreshAgentBlock(root); warning != "" {
		sum.Warnings = append(sum.Warnings, warning)
	}
	return sum
}
