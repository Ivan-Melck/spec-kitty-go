package charter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports the `doctor charter-packs` health model and collectors
// (cli/commands/_charter_pack_health.py, _charter_pack_collect.py).

// SkippedProfile is a skipped agent-profile file.
type SkippedProfile struct {
	Layer, Path  string
	ProfileID    *string
	ErrorSummary string
}

// PackHealth is the per-layer agent-profile health.
type PackHealth struct {
	Layer      string
	Discovered int
	Valid      int
	Invalid    []SkippedProfile
}

// Healthy is the derived health.
func (p PackHealth) Healthy() bool { return p.Valid == p.Discovered && len(p.Invalid) == 0 }

// Object is PackHealth.to_dict.
func (p PackHealth) Object() *jsonx.Object {
	inv := []any{}
	for _, s := range p.Invalid {
		inv = append(inv, jsonx.NewObject("layer", s.Layer, "path", s.Path, "profile_id", optAny(s.ProfileID), "error_summary", s.ErrorSummary))
	}
	return jsonx.NewObject("pack_id", p.Layer, "layer", p.Layer, "discovered_count", p.Discovered, "valid_count", p.Valid,
		"healthy", p.Healthy(), "invalid_profiles", inv)
}

// SkippedFile is a skipped glossary-pack / pack-skill file.
type SkippedFile struct{ Layer, Path, ErrorSummary string }

// CharterPackHealthReport is the doctor charter-packs report.
type CharterPackHealthReport struct {
	Packs         []PackHealth
	OrgDRG        *jsonx.Object
	GlossaryCount int
	TermCount     int
	GlossaryBad   []SkippedFile
	SkillCount    int
	SkillBad      []SkippedFile
}

// Healthy is CharterPackHealthReport.healthy.
func (r *CharterPackHealthReport) Healthy() bool {
	if len(r.Packs) == 0 {
		return false
	}
	for _, p := range r.Packs {
		if !p.Healthy() {
			return false
		}
	}
	if l, ok := r.OrgDRG.Get("errors"); ok {
		if xs, ok := l.([]any); ok && len(xs) > 0 {
			return false
		}
	}
	return len(r.GlossaryBad) == 0 && len(r.SkillBad) == 0
}

func skippedFiles(xs []SkippedFile) []any {
	out := []any{}
	for _, s := range xs {
		out = append(out, jsonx.NewObject("layer", s.Layer, "path", s.Path, "error_summary", s.ErrorSummary))
	}
	return out
}

// Object is CharterPackHealthReport.to_dict.
func (r *CharterPackHealthReport) Object() *jsonx.Object {
	packs := []any{}
	for _, p := range r.Packs {
		packs = append(packs, p.Object())
	}
	return jsonx.NewObject("healthy", r.Healthy(), "packs", packs, "org_drg", r.OrgDRG,
		"glossary_packs", jsonx.NewObject("pack_count", r.GlossaryCount, "term_count", r.TermCount, "healthy", len(r.GlossaryBad) == 0, "invalid_packs", skippedFiles(r.GlossaryBad)),
		"skills", jsonx.NewObject("skill_count", r.SkillCount, "loaded", r.SkillCount, "skipped", len(r.SkillBad), "healthy", len(r.SkillBad) == 0, "invalid_skills", skippedFiles(r.SkillBad)))
}

// AppendOrgError appends to org_drg["errors"].
func AppendOrgError(o *jsonx.Object, msgs ...string) {
	v, _ := o.Get("errors")
	l, _ := v.([]any)
	l = append([]any{}, l...)
	for _, m := range msgs {
		l = append(l, m)
	}
	o.Set("errors", l)
}

var layerRank = map[string]int{dct.LayerBuiltIn: 0, dct.LayerOrg: 1, dct.LayerProject: 2}

// profileSchemaRequired are the agent-profile JSON schema's top-level
// required keys (jsonschema messages for a profile with no profile-id).
var profileSchemaRequired = []string{"profile-id", "name", "purpose", "specialization"}

func profileSchemaErrors(data *jsonx.Object) []string {
	var out []string
	for _, k := range profileSchemaRequired {
		if _, ok := data.Get(k); !ok {
			out = append(out, fmt.Sprintf("root: '%s' is a required property", k))
		}
	}
	_, hasRole := data.Get("role")
	_, hasRoles := data.Get("roles")
	if !hasRole && !hasRoles {
		out = append(out, "root: "+pyValueRepr(data)+" is not valid under any of the given schemas")
	}
	return out
}

// profileLayerScan loads one agent-profile layer; returns id -> data.
type profileLoad struct {
	provenance map[string]string
	data       map[string]*jsonx.Object
	skipped    []SkippedProfile
	collisions []LayerCollision
}

var agentProfileFields = []string{"profile_id", "name", "description", "schema_version", "roles", "avatar_image", "capabilities",
	"routing_priority", "max_concurrent_tasks", "preferred_model", "effort", "purpose", "specialization", "collaboration",
	"mode_defaults", "initialization_declaration", "sentinel", "specialization_context", "applies_to_languages",
	"directive_references", "tactic_references", "toolguide_references", "styleguide_references", "self_review_protocol", "excluding"}

func deepMergeObj(base, over *jsonx.Object) *jsonx.Object {
	out := base.Clone()
	for _, kv := range over.Pairs() {
		if bv, ok := out.Get(kv.Key); ok {
			bo, ok1 := bv.(*jsonx.Object)
			oo, ok2 := kv.Value.(*jsonx.Object)
			if ok1 && ok2 {
				out.Set(kv.Key, deepMergeObj(bo, oo))
				continue
			}
		}
		out.Set(kv.Key, kv.Value)
	}
	return out
}

func loadAgentProfiles(builtInDir string, builtInFS bool, orgDirs []string, projectDir string) *profileLoad {
	pl := &profileLoad{provenance: map[string]string{}, data: map[string]*jsonx.Object{}}
	builtIn := map[string]*jsonx.Object{}
	layer := func(files []string, read func(string) ([]byte, error), name string) {
		for _, f := range files {
			b, err := read(f)
			if err != nil {
				pl.skipped = append(pl.skipped, SkippedProfile{name, f, nil, "YAML/read error: " + err.Error()})
				continue
			}
			v, err := yamlx.Parse(b)
			if err != nil {
				pl.skipped = append(pl.skipped, SkippedProfile{name, f, nil, "YAML/read error: " + err.Error()})
				continue
			}
			if v == nil {
				pl.skipped = append(pl.skipped, SkippedProfile{name, f, nil, "Empty profile file (no YAML document)"})
				continue
			}
			data, isObj := v.(*jsonx.Object)
			var pid *string
			if isObj {
				for _, k := range []string{"profile-id", "profile_id"} {
					if x, ok := data.Get(k); ok && x != nil && x != "" && x != false {
						s := fmt.Sprint(x)
						pid = &s
						break
					}
				}
			}
			if pid == nil {
				summary := "Missing required 'profile-id'"
				if isObj {
					if errs := profileSchemaErrors(data); len(errs) > 0 {
						summary = strings.Join(errs, "; ")
					}
				}
				pl.skipped = append(pl.skipped, SkippedProfile{name, f, nil, summary})
				continue
			}
			merged := data
			if name != dct.LayerBuiltIn {
				if base, ok := builtIn[*pid]; ok {
					merged = deepMergeObj(base, data)
				}
			}
			if verr := ValidateArtifactSchema("agent_profiles", merged); verr != "" {
				summary := verr
				if errs := profileSchemaErrors(data); len(errs) > 0 {
					summary = strings.Join(errs, "; ")
				}
				pl.skipped = append(pl.skipped, SkippedProfile{name, f, pid, summary})
				continue
			}
			if name != dct.LayerBuiltIn {
				if lower, ok := pl.provenance[*pid]; ok {
					pl.collisions = append(pl.collisions, collisionFor("agent_profile", *pid, name, lower, data, agentProfileFields))
				}
			}
			pl.data[*pid] = merged
			pl.provenance[*pid] = name
			if name == dct.LayerBuiltIn {
				builtIn[*pid] = merged
			}
		}
	}
	if builtInFS {
		bi := dct.BuiltIn()
		var files []string
		files = append(files, bi.Walk("agent_profiles", ".agent.yaml", true)...)
		sort.Strings(files)
		layer(files, bi.ReadFile, dct.LayerBuiltIn)
	}
	for _, d := range orgDirs {
		layer(rglob(d, ".agent.yaml"), os.ReadFile, dct.LayerOrg)
	}
	if projectDir != "" {
		layer(rglob(projectDir, ".agent.yaml"), os.ReadFile, dct.LayerProject)
	}
	sort.SliceStable(pl.skipped, func(i, j int) bool {
		ri, rj := layerRank[pl.skipped[i].Layer], layerRank[pl.skipped[j].Layer]
		if ri != rj {
			return ri < rj
		}
		return pl.skipped[i].Path < pl.skipped[j].Path
	})
	return pl
}

// LayerCollision is one ArtifactLayerCollisionWarning.
type LayerCollision struct {
	Kind, ItemID, Higher, Lower string
	Replaced, Inherited         int
}

// Object renders the collision descriptor.
func (c LayerCollision) Object() *jsonx.Object {
	return jsonx.NewObject("kind", c.Kind, "item_id", c.ItemID, "higher_layer", c.Higher, "lower_layer", c.Lower,
		"replaced", c.Replaced, "inherited", c.Inherited)
}

func collisionFor(kind, id, higher, lower string, data *jsonx.Object, fields []string) LayerCollision {
	hk := map[string]bool{}
	for _, kv := range data.Pairs() {
		hk[kv.Key] = true
	}
	rep, inh := 0, 0
	for _, f := range fields {
		if hk[f] {
			rep++
		} else {
			inh++
		}
	}
	return LayerCollision{kind, id, higher, lower, rep, inh}
}

// repoKinds are LAYERED_REPOSITORY_KIND_PLURALS with their repository
// _kind label and model field names.
var repoKinds = []struct {
	plural, label string
	fields        []string
}{
	{"directives", "directive", []string{"id", "schema_version", "title", "intent", "enforcement", "scope", "procedures", "integrity_rules", "validation_criteria", "explicit_allowances", "references"}},
	{"tactics", "tactic", []string{"id", "schema_version", "name", "purpose", "steps", "failure_modes", "applies_to_languages", "references", "notes"}},
	{"styleguides", "styleguide", []string{"id", "schema_version", "title", "scope", "principles", "patterns", "anti_patterns", "tooling", "quality_test", "applies_to_languages", "references", "structural_lint_config"}},
	{"toolguides", "toolguide", []string{"id", "schema_version", "tool", "title", "guide_path", "summary", "commands", "applies_to_languages", "last_updated", "references"}},
	{"paradigms", "paradigm", []string{"schema_version", "id", "name", "summary", "directive_refs", "references"}},
	{"procedures", "procedure", []string{"schema_version", "id", "name", "purpose", "entry_condition", "exit_condition", "steps", "anti_patterns", "applies_to_languages", "notes", "references"}},
	{"agent_profiles", "agent_profile", agentProfileFields},
	{"mission_step_contracts", "missionstepcontract", []string{"id", "schema_version", "action", "mission", "steps", "gates"}},
	{"assets", "asset", []string{"id", "mime", "path", "title"}},
	{"glossary_packs", "glossarypack", []string{"id", "provenance", "terms", "description"}},
	{"skills", "pack-skill", []string{"schema_version", "id", "title", "description", "triggers", "form", "body_path", "expands_to", "parameters", "invocation", "tools", "version", "maintainers", "overrides", "enhances"}},
}

// LayeredRepositoryKindPlurals is LAYERED_REPOSITORY_KIND_PLURALS.
var LayeredRepositoryKindPlurals = func() []string {
	out := make([]string, len(repoKinds))
	for i, k := range repoKinds {
		out[i] = k.plural
	}
	return out
}()

// repoLoad is a BaseArtifactRepository load (built-in + overlays) that
// records skips and collisions.
type repoLoad struct {
	items      map[string]*jsonx.Object
	provenance map[string]string
	skipped    []string // warning texts ("Skipping invalid ...", "Skipping ... no id")
	collisions []LayerCollision
}

func loadKindRepo(kind dct.Kind, label string, fields []string, orgDirs []string, projectDir string) *repoLoad {
	rl := &repoLoad{items: map[string]*jsonx.Object{}, provenance: map[string]string{}}
	suffix := kind.Suffix()
	if suffix == "" {
		return rl
	}
	plural := kind.Plural()
	validate := func(data *jsonx.Object) string {
		if schemaModels[plural] != nil {
			return ValidateArtifactSchema(plural, data)
		}
		return ""
	}
	bi := dct.BuiltIn()
	biDir := plural
	if kind == dct.MissionStepContract {
		biDir = "missions/built_in_step_contracts"
	}
	builtIn := map[string]*jsonx.Object{}
	files := bi.Walk(biDir, suffix, true)
	sort.Strings(files)
	for _, f := range files {
		b, err := bi.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := yamlx.Parse(b)
		if err != nil {
			rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid built-in %s %s: %v", label, filepath.Base(f), err))
			continue
		}
		data, ok := v.(*jsonx.Object)
		if !ok || data == nil {
			continue
		}
		if verr := validate(data); verr != "" {
			rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid built-in %s %s: %s", label, filepath.Base(f), verr))
			continue
		}
		id := data.GetString("id")
		builtIn[id] = data
	}
	for id, d := range builtIn {
		rl.items[id] = d
		rl.provenance[id] = dct.LayerBuiltIn
	}
	overlay := func(dir, layer string) {
		for _, f := range rglob(dir, suffix) {
			b, err := os.ReadFile(f)
			if err != nil {
				rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid %s %s %s: %v", layer, label, filepath.Base(f), err))
				continue
			}
			v, err := yamlx.Parse(b)
			if err != nil {
				rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid %s %s %s: %v", layer, label, filepath.Base(f), err))
				continue
			}
			if v == nil {
				continue
			}
			data, ok := v.(*jsonx.Object)
			if !ok {
				rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid %s %s %s: 'list' object has no attribute 'get'", layer, label, filepath.Base(f)))
				continue
			}
			idv, _ := data.Get("id")
			if idv == nil || idv == "" || idv == false {
				rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping %s %s %s: no id", layer, label, filepath.Base(f)))
				continue
			}
			id := fmt.Sprint(idv)
			merged := data
			if base, ok := builtIn[id]; ok {
				merged = base.Clone()
				for _, kv := range data.Pairs() {
					merged.Set(kv.Key, kv.Value)
				}
			}
			if verr := validate(merged); verr != "" {
				rl.skipped = append(rl.skipped, fmt.Sprintf("Skipping invalid %s %s %s: %s", layer, label, filepath.Base(f), verr))
				continue
			}
			if lower, ok := rl.provenance[id]; ok {
				rl.collisions = append(rl.collisions, collisionFor(label, id, layer, lower, data, fields))
			}
			rl.items[id] = merged
			rl.provenance[id] = layer
		}
	}
	for _, d := range orgDirs {
		overlay(d, dct.LayerOrg)
	}
	if projectDir != "" {
		overlay(projectDir, dct.LayerProject)
	}
	return rl
}

var skippedPattern = regexp.MustCompile(`(?s)^Skipping invalid (\S+) \S+ (\S+): (.*)$`)

func parseSkipped(msg string) SkippedFile {
	if m := skippedPattern.FindStringSubmatch(msg); m != nil {
		return SkippedFile{m[1], m[2], m[3]}
	}
	return SkippedFile{"unknown", "unknown", msg}
}

func kindDirs(orgRoots []string, projectRoot string, k dct.Kind) ([]string, string) {
	var org []string
	for _, r := range orgRoots {
		org = append(org, filepath.Join(r, k.Plural()))
	}
	proj := ""
	if projectRoot != "" {
		proj = filepath.Join(projectRoot, dct.ProjectKindDir(k))
	}
	return org, proj
}

// CollectCharterPackHealth is _collect_profile_health (plus glossary/skill
// health and the org-layer data).
func CollectCharterPackHealth(repoRoot string) *CharterPackHealthReport {
	rep := &CharterPackHealthReport{}
	orgRoots := ResolvePackChain(repoRoot)
	projectRoot := ProjectPackRootIfExists(repoRoot)
	org, proj := kindDirs(orgRoots, projectRoot, dct.AgentProfile)
	pl := loadAgentProfiles("", true, org, proj)
	byLayer := map[string]int{}
	for _, l := range pl.provenance {
		byLayer[l]++
	}
	skippedBy := map[string][]SkippedProfile{}
	for _, s := range pl.skipped {
		skippedBy[s.Layer] = append(skippedBy[s.Layer], s)
	}
	layers := map[string]bool{}
	for l := range byLayer {
		layers[l] = true
	}
	for l := range skippedBy {
		layers[l] = true
	}
	var ordered []string
	for _, l := range []string{dct.LayerBuiltIn, dct.LayerOrg, dct.LayerProject} {
		if layers[l] {
			ordered = append(ordered, l)
			delete(layers, l)
		}
	}
	var rest []string
	for l := range layers {
		rest = append(rest, l)
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	for _, l := range ordered {
		rep.Packs = append(rep.Packs, PackHealth{Layer: l, Discovered: byLayer[l] + len(skippedBy[l]), Valid: byLayer[l], Invalid: skippedBy[l]})
	}
	rep.OrgDRG = CollectOrgLayerData(repoRoot)
	og, pg := kindDirs(orgRoots, projectRoot, dct.GlossaryPack)
	gl := loadKindRepo(dct.GlossaryPack, "glossarypack", nil, og, pg)
	rep.GlossaryCount = len(gl.items)
	for _, d := range gl.items {
		rep.TermCount += len(d.GetList("terms"))
	}
	for _, s := range gl.skipped {
		rep.GlossaryBad = append(rep.GlossaryBad, parseSkipped(s))
	}
	so, sp := kindDirs(orgRoots, projectRoot, dct.Skill)
	sk := loadKindRepo(dct.Skill, "pack-skill", nil, so, sp)
	rep.SkillCount = len(sk.items)
	for _, s := range sk.skipped {
		rep.SkillBad = append(rep.SkillBad, parseSkipped(s))
	}
	return rep
}

// CollectOrgLayerData is _collect_org_layer_data.
func CollectOrgLayerData(repoRoot string) *jsonx.Object {
	res := jsonx.NewObject("configured_packs", []any{}, "collision_warnings", []any{}, "dangling_endpoints", []any{}, "errors", []any{})
	frags, err := LoadOrgDRG(repoRoot)
	if err != nil {
		if _, ok := err.(*OrgPackMissingError); ok {
			res.Set("errors", []any{err.Error()})
		} else {
			res.Set("errors", []any{"org-DRG load error: " + err.Error()})
		}
		return res
	}
	packs := []any{}
	for _, f := range frags {
		packs = append(packs, jsonx.NewObject("name", f.PackName, "source_kind", f.SourceKind, "source_ref", f.SourceRef,
			"layer_index", f.LayerIndex, "node_count", len(f.Nodes), "edge_count", len(f.Edges), "fetched", true))
	}
	res.Set("configured_packs", packs)
	if len(frags) == 0 {
		return res
	}
	bi, err := BuiltInMergedGraph()
	if err != nil {
		AppendOrgError(res, "org-layer merge check failed: "+err.Error())
		return res
	}
	merged, err := MergeThreeLayers(bi, frags, nil)
	if err != nil {
		if ce, ok := err.(*OrgConflictError); ok {
			cw := []any{}
			for _, c := range ce.Conflicts {
				cw = append(cw, jsonx.NewObject("kind", c.Kind, "target_id", c.TargetID, "conflicting_layers", strsAny(c.ConflictingLayers), "resolution", c.ResolutionApplied))
			}
			res.Set("collision_warnings", cw)
			AppendOrgError(res, ce.HardFailureMessages()...)
		} else {
			AppendOrgError(res, "org-layer merge check failed: "+err.Error())
		}
		return res
	}
	if dangling := ValidateDanglingReferences(merged); len(dangling) > 0 {
		res.Set("dangling_endpoints", strsAny(dangling))
		AppendOrgError(res, dangling...)
	}
	builtInURNs := bi.NodeURNs()
	recordOverrideFindings(res, merged, builtInURNs, repoRoot, frags)
	return res
}

func recordOverrideFindings(res *jsonx.Object, merged *MergedGraph, builtInURNs map[string]bool, repoRoot string, frags []*OrgFragment) {
	roots, order := PackRootsFromFragments(frags, repoRoot)
	configured := append(LoadPackRegistry(repoRoot).Names(), order...)
	eff := LoadEffectiveOverridePolicy(repoRoot, roots, order, configured)
	overrides := FindOverriddenBuiltins(merged, builtInURNs)
	sanc, unsanc := AdjudicateOverrides(overrides, eff)
	var errs []string
	errs = append(errs, eff.PackErrors...)
	if len(eff.PackErrors) > 0 {
		res.Set("pack_sanction_errors", strsAny(eff.PackErrors))
	}
	if eff.ConsumerError != "" {
		errs = append(errs, eff.ConsumerError)
	}
	errs = append(errs, eff.RevocationErrors...)
	if len(sanc) > 0 {
		var l []any
		for _, s := range sanc {
			l = append(l, jsonx.NewObject("urn", s.URN, "kind", s.Kind, "pack", s.Pack, "source", s.Source, "reason", s.Reason))
		}
		res.Set("sanctioned_overrides", l)
	}
	if len(unsanc) > 0 {
		packOf := map[string]string{}
		for _, o := range overrides {
			packOf[o.URN] = o.Pack
		}
		urnsByPack := map[string][]string{}
		var packOrder []string
		for _, u := range unsanc {
			p := packOf[u.URN]
			if _, ok := urnsByPack[p]; !ok {
				packOrder = append(packOrder, p)
			}
			urnsByPack[p] = append(urnsByPack[p], u.URN)
		}
		type legacy struct{ path, reason string }
		leg := map[string]legacy{}
		for _, p := range packOrder {
			root, ok := roots[p]
			if !ok {
				continue
			}
			for urn, reason := range LegacyTemplateEntries(root, urnsByPack[p]) {
				leg[urn] = legacy{filepath.Join(root, LegacyTemplateRelpath), reason}
			}
		}
		var l []any
		for _, u := range unsanc {
			e := jsonx.NewObject("urn", u.URN, "kind", u.Kind, "why", u.Why)
			if lg, ok := leg[u.URN]; ok {
				e.Set("legacy_template", jsonx.NewObject("path", lg.path, "reason", lg.reason))
			}
			l = append(l, e)
			errs = append(errs, fmt.Sprintf("unsanctioned built-in override: %s (%s) — %s", u.URN, u.Kind, u.Why))
		}
		res.Set("unsanctioned_overrides", l)
	}
	if len(errs) > 0 {
		AppendOrgError(res, errs...)
	}
}

// RunRetiredGovernanceKeyCheck is _run_retired_governance_key_check.
func RunRetiredGovernanceKeyCheck(rep *CharterPackHealthReport, repoRoot string) {
	cy := filepath.Join(repoRoot, ".kittify", "charter", "charter.yaml")
	b, err := os.ReadFile(cy)
	if err != nil {
		return
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return
	}
	g, ok := o.Get("governance")
	if !ok {
		return
	}
	gov, ok := g.(*jsonx.Object)
	if !ok {
		return
	}
	if _, ok := gov.Get("doctrine"); ok {
		body := fmt.Sprintf("%s: `governance.doctrine` is the retired selection key and is no longer read. Run `spec-kitty upgrade` to rewrite it as `governance.charter`. See docs/migrations/charter-pack-cutover.md.", cy)
		AppendOrgError(rep.OrgDRG, body)
		rep.OrgDRG.Set("retired_governance_key", jsonx.NewObject("file", cy, "key", "governance.doctrine", "message", body))
	}
}

func isDirPath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// RunRetiredLayoutCheck is _run_retired_layout_check.
func RunRetiredLayoutCheck(rep *CharterPackHealthReport, repoRoot string) {
	var findings []any
	var msgs []string
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			continue
		}
		nested := filepath.Join(root, "doctrine")
		if !isDirPath(nested) {
			continue
		}
		entries, err := os.ReadDir(nested)
		hasDir := err != nil
		for _, e := range entries {
			if isDirPath(filepath.Join(nested, e.Name())) {
				hasDir = true
			}
		}
		if !hasDir {
			continue
		}
		m := fmt.Sprintf("org pack %s uses the retired nested layout %s/<kind>/<layer>/, which is not read; move its artifacts to the flat layout %s/<kind>/ (see docs/migrations/charter-pack-cutover.md).", dct.PyRepr(p.Name), nested, root)
		findings = append(findings, jsonx.NewObject("path", nested, "message", m))
		msgs = append(msgs, m)
	}
	fb := filepath.Join(repoRoot, "doctrine")
	if isDirPath(fb) && !isDirPath(filepath.Join(repoRoot, ".kittify", "charter-packs")) && !isDirPath(filepath.Join(repoRoot, "src", "charter", "offering")) {
		m := fmt.Sprintf("%s is no longer read as the project layer; move its artifacts to the project pack root .kittify/charter-packs/ (see docs/migrations/charter-pack-cutover.md).", fb)
		findings = append(findings, jsonx.NewObject("path", fb, "message", m))
		msgs = append(msgs, m)
	}
	if len(findings) == 0 {
		return
	}
	AppendOrgError(rep.OrgDRG, msgs...)
	rep.OrgDRG.Set("retired_layouts", findings)
}

// SelectionKindPlurals is _SELECTION_KIND_PLURALS.
var SelectionKindPlurals = []string{"directives", "tactics", "paradigms", "styleguides", "toolguides", "procedures", "mission_step_contracts", "agent_profiles"}

func readProjectSelections(repoRoot string) map[string][]string {
	out := map[string][]string{}
	cy := filepath.Join(repoRoot, ".kittify", "charter", "charter.yaml")
	b, err := os.ReadFile(cy)
	if err != nil {
		return out
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return out
	}
	o, _ := v.(*jsonx.Object)
	if o == nil {
		return out
	}
	gov := o.GetObject("governance")
	if gov == nil {
		return out
	}
	if _, ok := gov.Get("doctrine"); ok {
		return out
	}
	ch := gov.GetObject("charter")
	if ch == nil {
		return out
	}
	for _, k := range SelectionKindPlurals {
		if l, ok := ch.Get("selected_" + k); ok {
			if xs, ok := l.([]any); ok {
				for _, x := range xs {
					out[k] = append(out[k], fmt.Sprint(pyStrOf(x)))
				}
			}
		}
	}
	return out
}

// OrgCharterSummary is _summarize_org_charter.
func OrgCharterSummary(packRoot string) *jsonx.Object {
	p := filepath.Join(packRoot, "org-charter.yaml")
	if _, err := os.Stat(p); err != nil {
		return jsonx.NewObject("present", false)
	}
	pol, err := LoadOrgCharterPolicy(packRoot)
	if err != nil {
		return jsonx.NewObject("present", true, "module_available", true, "load_error", true)
	}
	if pol == nil {
		return jsonx.NewObject("present", false)
	}
	id := pol.GetObject("interview_defaults")
	idn := 0
	if id != nil {
		idn = id.Len()
	}
	return jsonx.NewObject("present", true, "module_available", true, "interview_defaults_count", idn,
		"required_directives_count", len(pol.GetList("required_directives")), "governance_policies_count", len(pol.GetList("governance_policies")))
}

var orgCharterFields = map[string]string{"schema_version": "int", "extends": "str", "org_name": "str", "interview_defaults": "dict",
	"required_directives": "list", "required_tactics": "list", "required_paradigms": "list", "required_styleguides": "list",
	"required_toolguides": "list", "required_procedures": "list", "required_agent_profiles": "list",
	"required_mission_step_contracts": "list", "required_glossary_packs": "list", "required_assets": "list",
	"required_skills": "list", "skill_namespace": "str", "governance_policies": "list", "activations": "list"}

// LoadOrgCharterPolicy is load_org_charter_policy: nil when absent,
// unreadable or not a mapping; an error when schema validation fails.
func LoadOrgCharterPolicy(packRoot string) (*jsonx.Object, error) {
	p := filepath.Join(packRoot, "org-charter.yaml")
	b, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return nil, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	var errs []pydErr
	for _, kv := range o.Pairs() {
		typ, known := orgCharterFields[kv.Key]
		if !known {
			errs = append(errs, pydErr{kv.Key, "Extra inputs are not permitted", "extra_forbidden", kv.Value})
			continue
		}
		val := kv.Value
		switch typ {
		case "int":
			switch x := val.(type) {
			case int:
			case string:
				if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", new(int)); err != nil {
					errs = append(errs, pydErr{kv.Key, "Value error, invalid literal for int() with base 10: " + pyValueRepr(x), "value_error", val})
				}
			default:
				errs = append(errs, pydErr{kv.Key, "Value error, schema_version must be an int or numeric string", "value_error", val})
			}
		case "str":
			if val != nil {
				if _, ok := val.(string); !ok {
					errs = append(errs, pydErr{kv.Key, "Input should be a valid string", "string_type", val})
				}
			}
		case "dict":
			if _, ok := val.(*jsonx.Object); !ok {
				errs = append(errs, pydErr{kv.Key, "Input should be a valid dictionary", "dict_type", val})
			}
		case "list":
			l, ok := val.([]any)
			if !ok {
				errs = append(errs, pydErr{kv.Key, "Input should be a valid list", "list_type", val})
				continue
			}
			if strings.HasPrefix(kv.Key, "required_") {
				for i, x := range l {
					if _, ok := x.(string); !ok {
						errs = append(errs, pydErr{fmt.Sprintf("%s.%d", kv.Key, i), "Input should be a valid string", "string_type", x})
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", formatPydErrors("OrgCharterPolicy", errs))
	}
	return o, nil
}

// MergedOrgRequired is load_org_charter_policies' required_<kind> union.
func MergedOrgRequired(repoRoot string) map[string][]string {
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return map[string][]string{}
		}
		pol, err := LoadOrgCharterPolicy(root)
		if err != nil || pol == nil {
			continue
		}
		for _, k := range append(append([]string{}, SelectionKindPlurals...), "glossary_packs", "assets", "skills") {
			if seen[k] == nil {
				seen[k] = map[string]bool{}
			}
			for _, x := range pol.GetList("required_" + k) {
				s := fmt.Sprint(x)
				if !seen[k][s] {
					seen[k][s] = true
					out[k] = append(out[k], s)
				}
			}
		}
	}
	return out
}

// SelectionEntry is one selection-block entry.
type SelectionEntry struct{ ID, Source string }

// BuildSelectionBlock is _build_selection_block.
func BuildSelectionBlock(repoRoot string) map[string][]SelectionEntry {
	proj := readProjectSelections(repoRoot)
	orgReq := MergedOrgRequired(repoRoot)
	off := dct.NewOffering(repoRoot, ResolvePackChain(repoRoot))
	if ProjectPackRootIfExists(repoRoot) == "" {
		off.ProjectRoot = ""
	}
	out := map[string][]SelectionEntry{}
	for _, k := range SelectionKindPlurals {
		kind, _ := dct.KindFromPlural(k)
		seen := map[string]bool{}
		var ordered []string
		for _, id := range append(append([]string{}, proj[k]...), orgReq[k]...) {
			if !seen[id] {
				seen[id] = true
				ordered = append(ordered, id)
			}
		}
		projSet := map[string]bool{}
		for _, id := range proj[k] {
			projSet[id] = true
		}
		orgSet := map[string]bool{}
		for _, id := range orgReq[k] {
			orgSet[id] = true
		}
		repo := off.Repo(kind)
		entries := []SelectionEntry{}
		for _, id := range ordered {
			src := ""
			for _, a := range repo.List() {
				if a.ID == id {
					src = a.Source
				}
			}
			switch {
			case src == dct.LayerBuiltIn || src == dct.LayerProject || src == dct.LayerOrg:
			case projSet[id]:
				src = "charter"
			case orgSet[id]:
				src = "org-required"
			default:
				src = "unknown"
			}
			entries = append(entries, SelectionEntry{id, src})
		}
		out[k] = entries
	}
	return out
}

// SelectionBlockObject renders the selections JSON block.
func SelectionBlockObject(sel map[string][]SelectionEntry) *jsonx.Object {
	o := jsonx.NewObject()
	for _, k := range SelectionKindPlurals {
		l := []any{}
		for _, e := range sel[k] {
			l = append(l, jsonx.NewObject("id", e.ID, "source", e.Source))
		}
		o.Set(k, l)
	}
	return o
}

// SelectionBlockLines is _render_selection_block_lines.
func SelectionBlockLines(sel map[string][]SelectionEntry) []string {
	lines := []string{"Selections (active globally-selected artifacts):"}
	for _, k := range SelectionKindPlurals {
		es := sel[k]
		if len(es) == 0 {
			lines = append(lines, "  "+k+": (none)")
			continue
		}
		lines = append(lines, "  "+k+":")
		for _, e := range es {
			lines = append(lines, fmt.Sprintf("    - %-24s(source: %s)", e.ID, e.Source))
		}
	}
	return append(lines, "Active charter (activated artifacts): spec-kitty charter list")
}

// readAuthoredPackVersion is _read_authored_pack_version.
func readAuthoredPackVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "pack.yaml"))
	if err != nil {
		return ""
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return ""
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return ""
	}
	pv, ok := o.Get("pack_version")
	if !ok || pv == nil || pv == "" || pv == false || pv == 0 {
		return ""
	}
	return pyStrOf(pv)
}

// ResolvePackVersion is _resolve_pack_version: (version, fetched_at, is_git).
func ResolvePackVersion(root string) (string, *string, bool) {
	if v := readAuthoredPackVersion(root); v != "" {
		return v, nil, false
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		out, err := exec.Command("git", "-C", root, "describe", "--tags", "--always").Output()
		if err != nil {
			return "git (version unavailable)", nil, true
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			return s, nil, true
		}
		return "git (version unavailable)", nil, true
	}
	mp := filepath.Join(root, "pack-manifest.yaml")
	if b, err := os.ReadFile(mp); err == nil {
		v, err := yamlx.Parse(b)
		if err != nil {
			return "unknown", nil, false
		}
		if o, ok := v.(*jsonx.Object); ok {
			ver := "unknown"
			if x, ok := o.Get("pack_version"); ok && x != nil && x != "" && x != false && x != 0 {
				ver = pyStrOf(x)
			}
			var fa *string
			if x, ok := o.Get("fetched_at"); ok && x != nil && x != "" && x != false {
				s := pyStrOf(x)
				fa = &s
			}
			return ver, fa, false
		}
	}
	return "unknown", nil, false
}

// CountPackArtifacts is _count_pack_artifacts.
func CountPackArtifacts(root string) *jsonx.Object {
	o := jsonx.NewObject()
	for _, k := range LayeredRepositoryKindPlurals {
		d := filepath.Join(root, k)
		if _, err := os.Stat(d); err == nil {
			m, _ := filepath.Glob(filepath.Join(d, "*.yaml"))
			o.Set(k, len(m))
		}
	}
	return o
}

// BuildPackEntries is _build_pack_entries.
func BuildPackEntries(repoRoot string) ([]*jsonx.Object, error) {
	var out []*jsonx.Object
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil, err
		}
		_, serr := os.Stat(root)
		e := jsonx.NewObject("name", p.Name, "local_path", root, "source_type", optAny(p.SourceType), "url", optAny(p.URL),
			"ref", optAny(p.Ref), "snapshot_present", serr == nil)
		if serr == nil {
			ver, fa, isGit := ResolvePackVersion(root)
			e.Set("pack_version", ver)
			e.Set("fetched_at", optAny(fa))
			e.Set("is_git_pack", isGit)
			e.Set("artifact_counts", CountPackArtifacts(root))
			e.Set("org_charter", OrgCharterSummary(root))
		}
		out = append(out, e)
	}
	return out, nil
}

// CollectLayerCollisions is _collect_layer_collisions (sorted by kind,
// item id).
func CollectLayerCollisions(repoRoot string) []LayerCollision {
	orgRoots := ResolvePackChain(repoRoot)
	projectRoot := ProjectPackRootIfExists(repoRoot)
	var out []LayerCollision
	for _, rk := range repoKinds {
		kind, _ := dct.KindFromPlural(rk.plural)
		org, proj := kindDirs(orgRoots, projectRoot, kind)
		if kind == dct.AgentProfile {
			out = append(out, loadAgentProfiles("", true, org, proj).collisions...)
			continue
		}
		out = append(out, loadKindRepo(kind, rk.label, rk.fields, org, proj).collisions...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out
}

// SanctionEntrySnippet is _sanction_entry_snippet (nil when the entry
// would be unusable).
func SanctionEntrySnippet(urn, reason string) []string {
	if SanctionReasonMissing(urn, reason) {
		return nil
	}
	s := RenderSanctionEntries([]ReplaceableBuiltin{{urn, reason}})
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

var _ = pyyaml.SafeLoad
