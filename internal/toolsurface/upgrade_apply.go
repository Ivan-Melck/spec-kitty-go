package toolsurface

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
)

// ApplyProvisioning applies the retained activation bytes (the finalizer's
// provisioning step); a no-op when nothing changes.
func (u *UpgradeRepairs) ApplyProvisioning() error {
	p := u.Provisioning
	if p == nil || !p.Changed() {
		return nil
	}
	for _, d := range p.AbsentParents {
		abs := filepath.Join(u.Root.Path, filepath.FromSlash(d))
		if err := os.Mkdir(abs, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		os.Chmod(abs, 0o755)
	}
	f, err := os.OpenFile(p.Target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(p.Mode))
	if err != nil {
		return err
	}
	if _, err := f.Write(p.Desired); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// UpgradeApplyReport is what _apply_prepared_surface_repairs records: the
// DriftPolicySummary paths (absolute) and the SurfaceRepairReport.
type UpgradeApplyReport struct {
	Created, Repaired, Regenerated []string
	DriftedPaths                   []string
	DriftedReasons                 map[string]string
	Failed                         bool
	FailureMessages                []string
	Errors                         []string // error diagnostics of the owner results
}

var okOutcomes = map[string]bool{"applied": true, "skipped": true}

func refused(a *Assessment, code, msg string) ApplyResult {
	return ApplyResult{Owner: a.OwnerKey, Skipped: a.EffectIDs(), Outcome: "failed",
		Diagnostics: append(append([]Diagnostic(nil), a.Diagnostics...), Diag(code, a.OwnerKey, "error", msg))}
}

// applyOwner is repair._apply_assessment with an automatic consent.
func applyOwner(a *Assessment) ApplyResult {
	if !a.Complete || a.HasErrors() {
		return refused(a, "incomplete_assessment", "Fresh complete owner assessment required")
	}
	ids := a.EffectIDs()
	if len(ids) == 0 {
		return ApplyResult{Owner: a.OwnerKey, Outcome: "skipped"}
	}
	if a.Recheck != nil {
		if d := a.Recheck(); len(d) > 0 {
			return ApplyResult{Owner: a.OwnerKey, Skipped: ids, Diagnostics: d, Outcome: "precondition_changed"}
		}
	}
	switch a.OwnerKey {
	case "agent_profiles":
		return applyProfiles(a)
	case "session_presence":
		return applySession(a)
	case "native_config":
		if err := applyNative(a); err != nil {
			return ApplyResult{Owner: a.OwnerKey, Failed: ids, Outcome: "failed", Diagnostics: []Diagnostic{Diag("native_apply_failed", a.OwnerKey, "error", err.Error())}}
		}
		return ApplyResult{Owner: a.OwnerKey, Succeeded: ids, Outcome: "applied"}
	case "plugin_bundle":
		return applyStaging(a)
	case "command_skills":
		return ApplyCommands(a)
	case "managed_skills":
		return ApplyProjectSkills(a)
	}
	return refused(a, "assessment_unsupported", "Selected owner unavailable for checked application")
}

// applyGlobal applies the coordinated global families (runtime, the
// selected slash commands and the selected skill roots).
func (u *UpgradeRepairs) applyGlobal() ApplyResult {
	g := u.Installation.Global
	if !g.Complete {
		return ApplyResult{Owner: g.OwnerKey, Skipped: g.EffectIDs(), Outcome: "precondition_changed", Diagnostics: g.Diagnostics}
	}
	ids := g.EffectIDs()
	if len(ids) == 0 {
		return ApplyResult{Owner: g.OwnerKey, Outcome: "skipped"}
	}
	if err := bootstrap.ApplySelectedFamilies(u.commandKeys, u.Installation.AgentKeys); err != nil {
		return ApplyResult{Owner: g.OwnerKey, Failed: ids, Outcome: "failed", Diagnostics: []Diagnostic{Diag("global_asset_write_failed", g.OwnerKey, "error", err.Error())}}
	}
	return ApplyResult{Owner: g.OwnerKey, Succeeded: ids, Outcome: "applied"}
}

// applyInstallation is ManagedSkillsProvider.apply_installation: the global
// and project owners in batch order, each applied.
func (u *UpgradeRepairs) applyInstallation(delegated map[string]bool) []ApplyResult {
	g, p := u.Installation.Global, u.Installation.Project
	project := func() ApplyResult {
		if !p.Complete || p.HasErrors() {
			return refused(p, "incomplete_assessment", "Fresh complete owner assessment required")
		}
		if len(p.Effects) == 0 {
			return ApplyResult{Owner: p.OwnerKey, Outcome: "skipped"}
		}
		return applyProjectSkillsDelegated(p, delegated)
	}
	if minPhase(p) < minPhase(g) {
		pr := project()
		return []ApplyResult{pr, u.applyGlobal()}
	}
	gr := u.applyGlobal()
	return []ApplyResult{gr, project()}
}

func minPhase(a *Assessment) int {
	m := -1
	for i, e := range a.Effects {
		p := phaseIndex(e.Phase)
		if i == 0 || p < m {
			m = p
		}
	}
	return m
}

// applyBatches is SurfaceRepairService.apply_assessments' ordering.
func applyBatches(owners []*Assessment) []ApplyResult {
	sorted := append([]*Assessment(nil), owners...)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, pj := minPhase(sorted[i]), minPhase(sorted[j])
		if pi != pj {
			return pi < pj
		}
		if sorted[i].OwnerKey != sorted[j].OwnerKey {
			return sorted[i].OwnerKey < sorted[j].OwnerKey
		}
		return sorted[i].Root.ID < sorted[j].Root.ID
	})
	var out []ApplyResult
	for _, a := range sorted {
		out = append(out, applyOwner(a))
	}
	return out
}

// Apply is apply_upgrade_repairs plus _apply_prepared_surface_repairs.
func (u *UpgradeRepairs) Apply() UpgradeApplyReport {
	var results []ApplyResult
	var bundles, others []*Assessment
	for _, o := range u.Remaining {
		if o.OwnerKey == "plugin_bundle" {
			bundles = append(bundles, o)
		} else {
			others = append(others, o)
		}
	}
	notOK := func(rs []ApplyResult) bool {
		for _, r := range rs {
			if !okOutcomes[r.Outcome] {
				return true
			}
		}
		return false
	}
	results = append(results, applyBatches(bundles)...)
	if !notOK(results) {
		var paired []ApplyResult
		if u.HasComp {
			cr := applyOwner(u.Commands[0])
			paired = append(paired, cr)
			if !okOutcomes[cr.Outcome] {
				for _, a := range []*Assessment{u.Installation.Global, u.Installation.Project} {
					paired = append(paired, ApplyResult{Owner: a.OwnerKey, Skipped: a.EffectIDs(), Outcome: "precondition_changed", Diagnostics: cr.Diagnostics})
				}
			} else {
				shared := map[string]bool{}
				cmdDest := map[string]bool{}
				for _, e := range u.Commands[0].Effects {
					cmdDest[e.Destination()] = true
				}
				for _, e := range u.Installation.Project.Effects {
					if cmdDest[e.Destination()] {
						shared[e.Destination()] = true
					}
				}
				paired = append(paired, u.applyInstallation(shared)...)
			}
		} else {
			paired = append(paired, u.applyInstallation(nil)...)
		}
		results = append(results, paired...)
		if !notOK(paired) {
			results = append(results, applyBatches(others)...)
		}
	}
	rep := UpgradeApplyReport{DriftedReasons: map[string]string{}}
	succeeded := map[string]bool{}
	for _, r := range results {
		for _, id := range r.Succeeded {
			succeeded[id] = true
		}
	}
	regenerated := map[string]bool{}
	for _, c := range u.Commands {
		if p, ok := c.Prepared.(*PreparedCommands); ok {
			for _, pc := range p.Commands {
				if pc.ReportKind == "regenerated" {
					regenerated[filepath.Join(c.Root.Path, filepath.FromSlash(pc.Path))] = true
				}
			}
		}
	}
	effects, _ := u.Effects()
	for _, e := range effects {
		if !succeeded[e.ID()] {
			continue
		}
		switch {
		case regenerated[e.Destination()]:
			rep.Regenerated = append(rep.Regenerated, e.Destination())
		case e.Action == "create":
			rep.Created = append(rep.Created, e.Destination())
		default:
			rep.Repaired = append(rep.Repaired, e.Destination())
		}
	}
	for _, o := range u.Owners() {
		for _, d := range o.Dispositions {
			if d.State == "consent_required" && d.HasPath {
				p := filepath.Join(o.Root.Path, filepath.FromSlash(d.Path))
				if _, ok := rep.DriftedReasons[p]; !ok {
					rep.DriftedReasons[p] = d.Reason
					rep.DriftedPaths = append(rep.DriftedPaths, p)
				}
			}
		}
	}
	for _, r := range results {
		hasErr := false
		for _, d := range r.Diagnostics {
			if d.Severity == "error" {
				rep.Errors = append(rep.Errors, d.Message)
				hasErr = true
			}
		}
		if !okOutcomes[r.Outcome] {
			rep.Failed = true
			if !hasErr {
				rep.FailureMessages = append(rep.FailureMessages, fmt.Sprintf("Tool-surface repair for %s was not applied (%s); re-run 'spec-kitty upgrade'.", r.Owner, r.Outcome))
			}
		}
	}
	return rep
}
