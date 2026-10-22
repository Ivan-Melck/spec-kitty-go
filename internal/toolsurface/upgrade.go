package toolsurface

import (
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// UpgradeRepairs is upgrade.assessment.PreparedUpgradeRepairs.
type UpgradeRepairs struct {
	Root         Root
	Provisioning *Provisioning
	Installation *SkillInstallation
	Commands     []*Assessment
	Composition  []Effect // nil when no single concrete command batch
	HasComp      bool
	Remaining    []*Assessment
	Diagnostics  []Diagnostic
	commandKeys  []string
}

// Owners is PreparedUpgradeRepairs.owners.
func (u *UpgradeRepairs) Owners() []*Assessment {
	out := []*Assessment{u.Installation.Global, u.Installation.Project}
	out = append(out, u.Commands...)
	return append(out, u.Remaining...)
}

// Complete is PreparedUpgradeRepairs.complete.
func (u *UpgradeRepairs) Complete() bool {
	for _, o := range u.Owners() {
		if !o.Complete {
			return false
		}
	}
	for _, d := range u.Diagnostics {
		if d.Severity == "error" {
			return false
		}
	}
	return true
}

// Effects is PreparedUpgradeRepairs.effects.
func (u *UpgradeRepairs) Effects() ([]Effect, error) {
	var effects []Effect
	if u.HasComp {
		effects = append(effects, u.Composition...)
	} else {
		for _, o := range append([]*Assessment{u.Installation.Global, u.Installation.Project}, u.Commands...) {
			effects = append(effects, o.Effects...)
		}
	}
	effects = append(effects, u.Provisioning.Effects(u.Root)...)
	for _, o := range u.Remaining {
		effects = append(effects, o.Effects...)
	}
	return Coalesce(effects)
}

// assessService is SurfaceRepairService.assess: one assessment per
// provider key (sorted), with the selections drawn from the plans.
func assessService(c *collectCtx, ps []provider, in AssessInputs, statuses []Status, plans []Plan) []*Assessment {
	sels := map[string][]Selection{}
	selected := map[string]provider{}
	for _, pl := range plans {
		var defs []Definition
		seen := map[Definition]bool{}
		for _, d := range pl.Definitions {
			if !seen[d] {
				seen[d] = true
				defs = append(defs, d)
			}
		}
		for _, inst := range pl.Instances {
			if !seen[inst.Def] {
				seen[inst.Def] = true
				defs = append(defs, inst.Def)
			}
		}
		for _, d := range defs {
			p := providerFor(ps, d.Kind)
			key := d.Provider
			if p != nil {
				key = p.key()
			}
			s := Selection{pl.Tool, d}
			dup := false
			for _, x := range sels[key] {
				dup = dup || x == s
			}
			if !dup {
				sels[key] = append(sels[key], s)
			}
			selected[key] = p
		}
	}
	for _, st := range statuses {
		p := providerFor(ps, st.Inst.Def.Kind)
		key := st.Inst.Def.Provider
		if p != nil {
			key = p.key()
		}
		selected[key] = p
		found := false
		for _, x := range sels[key] {
			found = found || x.Def == st.Inst.Def
		}
		if !found {
			sels[key] = append(sels[key], Selection{st.Inst.Owner, st.Inst.Def})
		}
	}
	keys := make([]string, 0, len(selected))
	for k := range selected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []*Assessment
	for _, k := range keys {
		p := selected[k]
		var owned []Status
		for _, st := range statuses {
			if sp := providerFor(ps, st.Inst.Def.Kind); sp != nil && p != nil && sp.key() == p.key() {
				owned = append(owned, st)
			}
		}
		out = append(out, assessOwner(c, k, p, in, owned, sels[k]))
	}
	return out
}

func assessOwner(c *collectCtx, key string, p provider, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	enabled, required := false, false
	for _, s := range sels {
		if s.Def.enabled() {
			enabled = true
			required = required || s.Def.autoRequired()
		}
	}
	if enabled && p != nil {
		if a := p.assess(c, in, statuses, sels); a != nil {
			co, err := Coalesce(a.Effects)
			if err != nil {
				cp := *a
				cp.Complete = false
				cp.Diagnostics = append(append([]Diagnostic(nil), a.Diagnostics...), Diag("owner_conflict", key, "error", err.Error()))
				return &cp
			}
			cp := *a
			cp.Effects = co
			return &cp
		}
	}
	if required {
		code := "assessment_unsupported"
		if p == nil {
			code = "missing_provider"
		}
		return &Assessment{OwnerKey: key, Root: in.Root, Diagnostics: []Diagnostic{Diag(code, key, "error", "Selected required owner lacks assessment support")}}
	}
	reason := "Optional/advisory owner lacks assessment support"
	if !enabled {
		reason = "Disabled surface selection"
	}
	return &Assessment{OwnerKey: key, Root: in.Root, Complete: true, Dispositions: []Disposition{NewDisposition(key, in.Root.ID, "", false, "not_applicable", reason)}}
}

// builderAssess is SurfacePlanBuilder.assess: build plans for tools
// (kind-filtered at build), collect their statuses and assess every owner.
func builderAssess(c *collectCtx, ps []provider, reg *registry, tools []string, in AssessInputs, kinds map[string]bool, configured []string) []*Assessment {
	plans := buildPlansWith(c, ps, reg, tools, in.Root.Path, kinds)
	report := collect(c, ps, in.Root.Path, plans, configured)
	return assessService(c, ps, in, report.Surfaces, plans)
}

// PrepareUpgradeRepairs is upgrade.assessment.prepare_upgrade_repairs: the
// complete retained repair inventory of the project's configured surfaces.
func PrepareUpgradeRepairs(projectPath string) (*UpgradeRepairs, error) {
	abs := absPath(projectPath)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	root := Root{"project", "project", abs}
	ac, err := kittify.LoadAgentConfig(abs)
	if err != nil {
		return nil, err
	}
	agentKeys := append([]string(nil), ac.Available...)
	var slash, cmdAgents []string
	for _, a := range agentKeys {
		if _, ok := agents.CommandConfigs[a]; ok {
			slash = append(slash, a)
		}
		if agents.IsCommandSkillAgent(a) {
			cmdAgents = append(cmdAgents, a)
		}
	}
	prov, err := PrepareProvisioning(abs)
	if err != nil {
		return nil, err
	}
	catalog, err := ProjectSkillCatalog(abs, false)
	if err != nil {
		return nil, err
	}
	now := nowISO()
	if slash == nil {
		slash = []string{}
	}
	inst := AssessSkillInstallation(root, catalog, agentKeys, true, true, slash, SkillAssessOptions{Provisioning: prov, Now: now})
	ps := providers()
	reg := &registry{keys: append(append([]string(nil), agentKeys...), PluginBundleToolKey)}
	c := newCtx(abs)
	managed := builderAssess(c, ps, reg, agentKeys, AssessInputs{Root: root, Projected: inst}, map[string]bool{KindCharterSkill: true}, agentKeys)
	commands := builderAssess(c, ps, reg, cmdAgents, AssessInputs{Root: root, Projected: prov}, map[string]bool{KindCommandSkill: true}, cmdAgents)
	remainingKinds := map[string]bool{}
	for _, k := range AllKinds {
		if k != KindCharterSkill && k != KindCommandSkill && k != KindCommandFile {
			remainingKinds[k] = true
		}
	}
	remaining := builderAssess(c, ps, reg, append(append([]string(nil), agentKeys...), PluginBundleToolKey), AssessInputs{Root: root}, remainingKinds, agentKeys)
	u := &UpgradeRepairs{Root: root, Provisioning: prov, Installation: inst, Commands: commands, Remaining: remaining, commandKeys: slash}
	owners := []*Assessment{inst.Global, inst.Project}
	owners = append(owners, managed...)
	owners = append(owners, commands...)
	owners = append(owners, remaining...)
	for _, o := range owners {
		u.Diagnostics = append(u.Diagnostics, o.Diagnostics...)
	}
	u.Diagnostics = append(u.Diagnostics, packSkillDiagnostics(abs)...)
	var concrete []*Assessment
	for _, a := range commands {
		if _, ok := a.Prepared.(*PreparedCommands); ok {
			concrete = append(concrete, a)
		}
	}
	errs := false
	for _, d := range u.Diagnostics {
		errs = errs || d.Severity == "error"
	}
	if len(concrete) == 1 && !errs && inst.Global.Complete && inst.Project.Complete && concrete[0].Complete {
		co, err := Coalesce(append(append(append([]Effect(nil), inst.Global.Effects...), inst.Project.Effects...), concrete[0].Effects...))
		if err != nil {
			return nil, err
		}
		u.Composition, u.HasComp = co, true
	} else if len(concrete) > 1 {
		u.Diagnostics = append(u.Diagnostics, Diag("command_owner_conflict", "command_skills", "error", "Expected one whole-root command preparation"))
	}
	if _, err := u.Effects(); err != nil {
		return nil, err
	}
	return u, nil
}

// PlanEffectsJSON renders the plan-json "effects" list.
func (u *UpgradeRepairs) PlanEffectsJSON() ([]any, error) {
	effects, err := u.Effects()
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, e := range effects {
		logical := make([]any, len(e.LogicalOwners))
		for i, x := range e.LogicalOwners {
			logical[i] = x
		}
		ids := make([]any, len(e.SurfaceIDs))
		for i, x := range e.SurfaceIDs {
			ids[i] = x
		}
		out = append(out, jsonx.NewObject("id", e.ID(), "phase", e.Phase, "owner", e.Owner, "logical_owners", logical, "surface_ids", ids,
			"root_id", e.Root.ID, "path", e.Path, "action", e.Action, "before", e.Before.JSON(), "after", e.After.JSON(), "reason", e.Reason,
			"ownership", jsonx.NewObject("kind", e.Ownership[0].Kind, "reference", e.Ownership[0].Reference)))
	}
	return out, nil
}

// PlanDispositionsJSON renders the plan-json "dispositions" list.
func (u *UpgradeRepairs) PlanDispositionsJSON() []any {
	out := []any{}
	for _, o := range u.Owners() {
		for _, d := range o.Dispositions {
			out = append(out, d.JSON())
		}
	}
	return out
}

// PlanDiagnosticsJSON renders the plan-json "diagnostics" list.
func (u *UpgradeRepairs) PlanDiagnosticsJSON() []any {
	out := []any{}
	for _, d := range u.Diagnostics {
		out = append(out, d.JSON())
	}
	return out
}

// PlanRootsJSON renders the plan-json "roots" list (sorted by id).
func (u *UpgradeRepairs) PlanRootsJSON() []any {
	roots := map[string]Root{}
	for _, o := range u.Owners() {
		roots[o.Root.ID] = o.Root
	}
	roots[u.Root.ID] = u.Root
	ids := make([]string, 0, len(roots))
	for k := range roots {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	out := []any{}
	for _, id := range ids {
		r := roots[id]
		out = append(out, jsonx.NewObject("id", r.ID, "scope", r.Scope, "path", r.Path))
	}
	return out
}

// PlanArtifactsJSON renders the plan-json "execution_artifacts" list.
func (u *UpgradeRepairs) PlanArtifactsJSON() []any {
	out := []any{}
	for _, o := range u.Owners() {
		for _, a := range o.Artifacts {
			out = append(out, jsonx.NewObject("owner", o.OwnerKey, "root_id", o.Root.ID, "directory", a.Directory, "name_pattern", a.NamePattern, "purpose", a.Purpose))
		}
	}
	return out
}

// PreservedCount is the number of consent_required dispositions.
func (u *UpgradeRepairs) PreservedCount() int {
	n := 0
	for _, o := range u.Owners() {
		for _, d := range o.Dispositions {
			if d.State == "consent_required" {
				n++
			}
		}
	}
	return n
}

// ErrorMessages lists the error diagnostics' messages.
func (u *UpgradeRepairs) ErrorMessages() []string {
	var out []string
	for _, d := range u.Diagnostics {
		if d.Severity == "error" {
			out = append(out, d.Message)
		}
	}
	return out
}

func packSkillDiagnostics(root string) []Diagnostic {
	var out []Diagnostic
	for _, f := range FindPackSkillFindings(root) {
		out = append(out, Diag("pack_skill_"+f.Kind, f.InstalledPath, "warning", f.Message))
	}
	return out
}
