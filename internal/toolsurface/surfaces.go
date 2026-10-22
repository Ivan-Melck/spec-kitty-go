package toolsurface

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Surface kinds (ToolSurfaceKind).
const (
	KindCommandSkill   = "command_skill"
	KindCharterSkill   = "charter_skill"
	KindContextFile    = "context_file"
	KindRule           = "rule"
	KindHook           = "hook"
	KindAgentProfile   = "agent_profile"
	KindPluginManifest = "plugin_manifest"
	KindNativeConfig   = "native_config"
	KindCommandFile    = "command_file"
)

// AllKinds is ToolSurfaceKind in declaration order.
var AllKinds = []string{KindCommandSkill, KindCharterSkill, KindContextFile, KindRule, KindHook, KindAgentProfile, KindPluginManifest, KindNativeConfig, KindCommandFile}

// Surface states.
const (
	StatePresent       = "present"
	StateMissing       = "missing"
	StateDrifted       = "drifted"
	StateStale         = "stale"
	StateOrphaned      = "orphaned"
	StateUnsafe        = "unsafe"
	StateUnsupported   = "unsupported"
	StateNotApplicable = "not_applicable"
)

// Definition is model.SurfaceDefinition.
type Definition struct {
	Kind, SourceKind, Scope, PathPattern, Required, Activation, Provider, RepairHint string
}

func (d Definition) enabled() bool { return d.Activation != "disabled" }

func (d Definition) autoRequired() bool {
	return d.Required == "required" || d.Required == "repairable_required"
}

// Instance is model.SurfaceInstance.
type Instance struct {
	Def       Definition
	Path      string // absolute, or a sentinel such as "<unsupported>"
	Owner     string
	SurfaceID string // explicit id ("" = derived)
	FileHash  string
	HasHash   bool
}

// ID is status._surface_id.
func (i Instance) ID() string {
	if i.SurfaceID != "" {
		return i.SurfaceID
	}
	name := filepath.Base(i.Path)
	parent := filepath.Base(filepath.Dir(i.Path))
	suffix := name
	if name == "SKILL.md" && parent != "" && parent != "." && parent != string(filepath.Separator) {
		suffix = parent + ".SKILL.md"
	}
	return i.Owner + "." + i.Def.Kind + "." + suffix
}

// Finding is findings.SurfaceFinding.
type Finding struct {
	Code, Severity, Message       string
	Tool, SurfaceID, Path, Repair string
	HasTool, HasSurface, HasPath  bool
	Details                       *jsonx.Object
}

// JSON is SurfaceFinding.to_json.
func (f Finding) JSON() *jsonx.Object {
	opt := func(has bool, s string) any {
		if !has {
			return nil
		}
		return s
	}
	var repair any
	if f.Repair != "" {
		repair = f.Repair
	}
	d := f.Details
	if d == nil {
		d = &jsonx.Object{}
	}
	return jsonx.NewObject("code", f.Code, "severity", f.Severity, "message", f.Message, "tool_key", opt(f.HasTool, f.Tool),
		"surface_id", opt(f.HasSurface, f.SurfaceID), "path", opt(f.HasPath, f.Path), "repair_command", repair, "docs_ref", nil, "details", d)
}

// instFinding builds a finding about inst (tool, surface id and path set).
func instFinding(code, sev, msg string, inst Instance, withPath bool, repair string) Finding {
	f := Finding{Code: code, Severity: sev, Message: msg, Tool: inst.Owner, HasTool: true, SurfaceID: inst.ID(), HasSurface: true, Repair: repair}
	if withPath {
		f.Path, f.HasPath = inst.Path, true
	}
	return f
}

// Status is status.SurfaceStatus.
type Status struct {
	Inst     Instance
	State    string
	Findings []Finding
}

// JSON is SurfaceStatus.to_json.
func (s Status) JSON() *jsonx.Object {
	var repair any
	for _, f := range s.Findings {
		if f.Repair != "" {
			repair = f.Repair
			break
		}
	}
	return jsonx.NewObject("id", s.Inst.ID(), "tool", s.Inst.Owner, "kind", s.Inst.Def.Kind, "provider", s.Inst.Def.Provider, "path", s.Inst.Path,
		"state", s.State, "source_kind", s.Inst.Def.SourceKind, "manifest", nil, "repair_command", repair)
}

// Plan is model.SurfacePlan.
type Plan struct {
	Tool        string
	Instances   []Instance
	Definitions []Definition
	Diagnostics []Diagnostic
}

// Selection is model.SurfaceSelection.
type Selection struct {
	Tool string
	Def  Definition
}

// RepairResult is repair.RepairResult.
type RepairResult struct {
	Repaired, Skipped, Failed []string
	DryRun                    bool
}

// JSON is RepairResult.to_json (findings_after is always empty from the
// service).
func (r RepairResult) JSON() *jsonx.Object {
	l := func(xs []string) []any {
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	}
	return jsonx.NewObject("repaired", l(r.Repaired), "skipped", l(r.Skipped), "failed", l(r.Failed), "dry_run", r.DryRun, "findings_after", []any{})
}

// AssessInputs is operations.AssessmentInputs (projected is owner-specific).
type AssessInputs struct {
	Root      Root
	Projected any
	Overwrite []string // ApplyConsent.overwrite_paths
	Automatic bool
}

// provider is the reporting + assessing provider protocol.
type provider interface {
	key() string
	handles(kind string) bool
	definitions() []Definition
	expand(def Definition, tool, root string) []Instance
	probe(c *collectCtx, inst Instance) Status
	repair(c *collectCtx, root string, statuses []Status) RepairResult
	assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment
}

// PluginBundleToolKey is the synthetic registry key of plugin manifests.
const PluginBundleToolKey = "plugin_bundle"

func providers() []provider {
	return []provider{&commandSkillsProvider{}, &slashCommandsProvider{}, &sessionPresenceProvider{}, &nativeConfigProvider{},
		&managedSkillsProvider{}, &agentProfilesProvider{}, &pluginBundleProvider{}}
}

// KindTokens is service._KIND_TOKENS.
var KindTokens = map[string]string{
	"command-skill": KindCommandSkill, "command-file": KindCommandFile, "context-file": KindContextFile, "context_file": KindContextFile,
	"hook": KindHook, "rule": KindRule, "native-config": KindNativeConfig, "native_config": KindNativeConfig, "charter-skill": KindCharterSkill,
	"agent-profile": KindAgentProfile, "plugin-manifest": KindPluginManifest, "plugin_manifest": KindPluginManifest,
}

// KindFromToken maps a --kind token, or returns upstream's error message.
func KindFromToken(tok string) (string, error) {
	if k, ok := KindTokens[tok]; ok {
		return k, nil
	}
	known := make([]string, 0, len(KindTokens))
	for t := range KindTokens {
		known = append(known, t)
	}
	sort.Strings(known)
	return "", fmt.Errorf("Unknown surface kind '%s'. Known kinds: %s.", tok, strings.Join(known, ", "))
}

// registry is ToolSurfaceRegistry as built by build_registry(keys): every
// non-synthetic definition per registered key, the plugin manifest under
// its synthetic key unconditionally.
type registry struct{ keys []string }

func (r *registry) definitionsFor(ps []provider, tool string) []Definition {
	var out []Definition
	registered := 0
	for _, k := range r.keys {
		if k == tool {
			registered++
		}
	}
	for _, p := range ps {
		if _, ok := p.(*pluginBundleProvider); ok {
			if tool == PluginBundleToolKey {
				out = append(out, p.definitions()...)
			}
			continue
		}
		for i := 0; i < registered; i++ {
			out = append(out, p.definitions()...)
		}
	}
	return out
}

func providerFor(ps []provider, kind string) provider {
	for _, p := range ps {
		if p.handles(kind) {
			return p
		}
	}
	return nil
}

// collectCtx caches per-run reads shared by many probes.
type collectCtx struct {
	root     string
	verify   map[string]*CommandVerify
	profiles map[string]*profileModel
}

func newCtx(root string) *collectCtx {
	return &collectCtx{root: root, verify: map[string]*CommandVerify{}, profiles: map[string]*profileModel{}}
}

// buildPlansWith is SurfacePlanBuilder.build.
func buildPlansWith(c *collectCtx, ps []provider, reg *registry, tools []string, root string, kinds map[string]bool) []Plan {
	var plans []Plan
	for _, t := range tools {
		pl := Plan{Tool: t}
		for _, d := range reg.definitionsFor(ps, t) {
			if kinds != nil && !kinds[d.Kind] {
				continue
			}
			pl.Definitions = append(pl.Definitions, d)
			p := providerFor(ps, d.Kind)
			if p == nil {
				continue
			}
			pl.Instances = append(pl.Instances, p.expand(d, t, root)...)
		}
		plans = append(plans, pl)
	}
	return plans
}

// Report is status.SurfaceReport.
type Report struct {
	OK                                          bool
	ProjectRoot                                 string
	ConfiguredTools                             []string
	Surfaces                                    []Status
	Findings                                    []Finding
	Present, Missing, Drifted, Warnings, Errors int
}

func collect(c *collectCtx, ps []provider, root string, plans []Plan, tools []string) *Report {
	r := &Report{ProjectRoot: root, ConfiguredTools: tools}
	for _, pl := range plans {
		for _, inst := range pl.Instances {
			var st Status
			if p := providerFor(ps, inst.Def.Kind); p != nil {
				st = p.probe(c, inst)
			} else {
				st = Status{Inst: inst, State: StateUnsupported}
			}
			r.Surfaces = append(r.Surfaces, st)
			switch st.State {
			case StatePresent:
				r.Present++
			case StateMissing:
				r.Missing++
			case StateDrifted:
				r.Drifted++
			}
			for _, f := range st.Findings {
				r.Findings = append(r.Findings, f)
				switch f.Severity {
				case "error":
					r.Errors++
				case "warning":
					r.Warnings++
				}
			}
		}
	}
	r.OK = r.Errors == 0
	return r
}

// JSON is SurfaceReport.to_json.
func (r *Report) JSON() *jsonx.Object {
	tools := make([]any, len(r.ConfiguredTools))
	for i, t := range r.ConfiguredTools {
		tools[i] = t
	}
	sl, fl := []any{}, []any{}
	for _, s := range r.Surfaces {
		sl = append(sl, s.JSON())
	}
	for _, f := range r.Findings {
		fl = append(fl, f.JSON())
	}
	return jsonx.NewObject("ok", r.OK, "schema_version", 1, "project_root", r.ProjectRoot, "configured_tools", tools,
		"summary", jsonx.NewObject("surfaces", len(r.Surfaces), "present", r.Present, "missing", r.Missing, "drifted", r.Drifted, "warnings", r.Warnings, "errors", r.Errors),
		"surfaces", sl, "findings", fl)
}

func filterPlans(plans []Plan, kinds map[string]bool) []Plan {
	if kinds == nil {
		return plans
	}
	out := make([]Plan, len(plans))
	for i, pl := range plans {
		np := Plan{Tool: pl.Tool, Diagnostics: pl.Diagnostics}
		for _, inst := range pl.Instances {
			if kinds[inst.Def.Kind] {
				np.Instances = append(np.Instances, inst)
			}
		}
		for _, d := range pl.Definitions {
			if kinds[d.Kind] {
				np.Definitions = append(np.Definitions, d)
			}
		}
		out[i] = np
	}
	return out
}

// Outcome is service.ToolSurfaceOutcome.
type Outcome struct {
	Report *Report
	Repair *RepairResult
}

// JSON is ToolSurfaceOutcome.to_json.
func (o Outcome) JSON() *jsonx.Object {
	p := o.Report.JSON()
	if o.Repair != nil {
		p.Set("repair", o.Repair.JSON())
	}
	return p
}

// RunToolSurfaces is service.run_tool_surfaces.
func RunToolSurfaces(root string, configured []string, toolFilter *string, kinds map[string]bool, fix bool) Outcome {
	tools := configured
	if toolFilter != nil {
		tools = nil
		for _, t := range configured {
			if t == *toolFilter {
				tools = append(tools, t)
			}
		}
	}
	ps := providers()
	planTools := append([]string(nil), tools...)
	if len(tools) > 0 && toolFilter == nil {
		planTools = append(planTools, PluginBundleToolKey)
	}
	c := newCtx(root)
	reg := &registry{keys: tools}
	plans := filterPlans(buildPlansWith(c, ps, reg, planTools, root, nil), kinds)
	report := collect(c, ps, root, plans, tools)
	if !fix {
		return Outcome{Report: report}
	}
	rr := repairStatuses(c, ps, root, report.Surfaces, kinds)
	c = newCtx(root)
	plans = filterPlans(buildPlansWith(c, ps, reg, planTools, root, nil), kinds)
	return Outcome{Report: collect(c, ps, root, plans, tools), Repair: &rr}
}

// repairStatuses is SurfaceRepairService.repair (grouped by provider in
// first-seen order).
func repairStatuses(c *collectCtx, ps []provider, root string, statuses []Status, kinds map[string]bool) RepairResult {
	var res RepairResult
	type bucket struct {
		p  provider
		ss []Status
	}
	var buckets []*bucket
	idx := map[string]*bucket{}
	for _, s := range statuses {
		if kinds != nil && !kinds[s.Inst.Def.Kind] {
			continue
		}
		p := providerFor(ps, s.Inst.Def.Kind)
		if p == nil {
			res.Failed = append(res.Failed, s.Inst.ID())
			continue
		}
		b, ok := idx[p.key()]
		if !ok {
			b = &bucket{p: p}
			idx[p.key()] = b
			buckets = append(buckets, b)
		}
		b.ss = append(b.ss, s)
	}
	for _, b := range buckets {
		r := b.p.repair(c, root, b.ss)
		res.Repaired = append(res.Repaired, r.Repaired...)
		res.Skipped = append(res.Skipped, r.Skipped...)
		res.Failed = append(res.Failed, r.Failed...)
	}
	return res
}

// CountSurfaces is the number of statuses collected for tools plus the
// plugin bundle (surface_repair_wiring's read-only probe).
func CountSurfaces(root string, tools []string) int {
	if len(tools) == 0 {
		return 0
	}
	ps := providers()
	c := newCtx(root)
	plans := buildPlansWith(c, ps, &registry{keys: tools}, append(append([]string(nil), tools...), PluginBundleToolKey), root, nil)
	return len(collect(c, ps, root, plans, tools).Surfaces)
}

// statusesOf filters statuses to those with the given states.
func statusesOf(ss []Status, states ...string) []Status {
	var out []Status
	for _, s := range ss {
		for _, st := range states {
			if s.State == st {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

func idsOf(ss []Status) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Inst.ID())
	}
	return out
}
