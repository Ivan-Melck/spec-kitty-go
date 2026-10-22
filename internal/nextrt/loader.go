package nextrt

// Custom mission loader (specify_cli.mission_loader + the runtime's
// mission discovery): discovery tiers, template loading with the
// MissionTemplate schema's required fields, structural validation and
// `mission run` (run_custom_mission).

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// reservedMissionKeys is RESERVED_BUILTIN_KEYS.
var reservedMissionKeys = []string{"documentation", "plan", "research", "software-dev"}

// DiscoveredMission is one discovery entry.
type DiscoveredMission struct {
	Key, Path, Origin, Tier string
	Selected                bool
}

type discoveryWarning struct{ Path, Tier, Origin, Error string }

// templateLoadError classifies load_mission_template_file failures.
type templateLoadError struct {
	kind    string // "yaml" | "pydantic" | "nosteps" | "runtime"
	msg     string
	missing []string
}

func (e *templateLoadError) Error() string { return e.msg }

type pydErr struct {
	loc   string
	typ   string
	msg   string
	input any
}

func truncRepr(v any) string {
	r := pyjson.Repr(v)
	if rs := []rune(r); len(rs) > 50 {
		return string(rs[:25]) + "..." + string(rs[len(rs)-24:])
	}
	return r
}

func pydanticMessage(errs []pydErr) string {
	s := "s"
	if len(errs) == 1 {
		s = ""
	}
	lines := []string{fmt.Sprintf("%d validation error%s for MissionTemplate", len(errs), s)}
	for _, e := range errs {
		lines = append(lines, e.loc, fmt.Sprintf("  %s [type=%s, input_value=%s, input_type=%s]", e.msg, e.typ, truncRepr(e.input), pyjson.TypeName(e.input)),
			fmt.Sprintf("    For further information visit https://errors.pydantic.dev/2.14/v/%s", e.typ))
	}
	return strings.Join(lines, "\n")
}

// validateTemplateSchema checks the MissionTemplate fields upstream's
// pydantic model requires (missing / string / list shapes).
func validateTemplateSchema(raw *jsonx.Object) []pydErr {
	var errs []pydErr
	reqStr := func(o *jsonx.Object, loc, k string) {
		v, ok := o.Get(k)
		switch {
		case !ok:
			errs = append(errs, pydErr{loc + k, "missing", "Field required", o})
		case v == nil:
			errs = append(errs, pydErr{loc + k, "string_type", "Input should be a valid string", v})
		default:
			if s, isStr := v.(string); !isStr {
				errs = append(errs, pydErr{loc + k, "string_type", "Input should be a valid string", v})
			} else if s == "" {
				errs = append(errs, pydErr{loc + k, "string_too_short", "String should have at least 1 character", v})
			}
		}
	}
	mv, _ := raw.Get("mission")
	if m, ok := mv.(*jsonx.Object); ok {
		for _, k := range []string{"key", "name", "version"} {
			reqStr(m, "mission.", k)
		}
	} else if !raw.Has("mission") {
		errs = append(errs, pydErr{"mission", "missing", "Field required", raw})
	} else {
		errs = append(errs, pydErr{"mission", "model_type", "Input should be a valid dictionary or instance of MissionMeta", mv})
	}
	for _, sec := range []struct {
		key  string
		reqs []string
	}{{"steps", []string{"id", "title"}}, {"audit_steps", []string{"id", "title"}}} {
		v, ok := raw.Get(sec.key)
		if !ok || v == nil {
			if ok {
				errs = append(errs, pydErr{sec.key, "list_type", "Input should be a valid list", v})
			}
			continue
		}
		l, isList := v.([]any)
		if !isList {
			errs = append(errs, pydErr{sec.key, "list_type", "Input should be a valid list", v})
			continue
		}
		for i, it := range l {
			so, ok := it.(*jsonx.Object)
			loc := fmt.Sprintf("%s.%d.", sec.key, i)
			if !ok {
				cls := "PromptStep"
				if sec.key == "audit_steps" {
					cls = "AuditStep"
				}
				errs = append(errs, pydErr{strings.TrimSuffix(loc, "."), "model_type", "Input should be a valid dictionary or instance of " + cls, it})
				continue
			}
			for _, k := range sec.reqs {
				reqStr(so, loc, k)
			}
			if sec.key == "audit_steps" && !so.Has("audit") {
				errs = append(errs, pydErr{loc + "audit", "missing", "Field required", so})
			}
		}
	}
	return errs
}

// loadMissionTemplateFile is load_mission_template_file.
func loadMissionTemplateFile(p string, b []byte) (*Template, error) {
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil, &templateLoadError{kind: "yaml", msg: err.Error()}
	}
	if v == nil {
		v = &jsonx.Object{}
	}
	raw, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &templateLoadError{kind: "runtime", msg: "Mission template must be a mapping: " + p}
	}
	if !raw.Has("mission") {
		key := raw.GetString("key")
		if key == "" {
			key = raw.GetString("name")
		}
		if key == "" {
			key = filepath.Base(filepath.Dir(p))
		}
		name := raw.GetString("name")
		if name == "" {
			name = filepath.Base(filepath.Dir(p))
		}
		ver := "1.0.0"
		if x, ok := raw.Get("version"); ok {
			ver = pyjson.Str(x)
		}
		desc, _ := raw.Get("description")
		if desc == nil {
			desc = ""
		}
		steps, _ := raw.Get("steps")
		if steps == nil {
			steps = []any{}
		}
		audits, _ := raw.Get("audit_steps")
		if audits == nil {
			audits = []any{}
		}
		raw = jsonx.NewObject("mission", jsonx.NewObject("key", key, "name", name, "version", ver, "description", desc),
			"steps", steps, "audit_steps", audits)
	}
	if errs := validateTemplateSchema(raw); len(errs) > 0 {
		var missing []string
		for _, e := range errs {
			if e.typ == "missing" {
				missing = append(missing, e.loc)
			}
		}
		return nil, &templateLoadError{kind: "pydantic", msg: pydanticMessage(errs), missing: missing}
	}
	t := templateFromObject(raw)
	if len(t.Steps) == 0 && len(t.AuditSteps) == 0 {
		return nil, &templateLoadError{kind: "nosteps", msg: "Mission template has no steps: " + p}
	}
	return t, nil
}

// templateFromObject builds a Template from a validated document.
func templateFromObject(o *jsonx.Object) *Template {
	t := &Template{}
	if m := o.GetObject("mission"); m != nil {
		t.Key = m.GetString("key")
	}
	t.Steps = stepsOf(o.GetList("steps"))
	t.AuditSteps = stepsOf(o.GetList("audit_steps"))
	return t
}

func stepsOf(list []any) []TemplateStep {
	var out []TemplateStep
	for _, it := range list {
		so, ok := it.(*jsonx.Object)
		if !ok {
			continue
		}
		prof := so.GetString("agent_profile")
		if so.Has("agent-profile") {
			prof = so.GetString("agent-profile")
		}
		st := TemplateStep{ID: so.GetString("id"), Title: so.GetString("title"), Description: so.GetString("description"),
			PromptTemplate: so.GetString("prompt_template"), AgentProfile: prof, ContractRef: so.GetString("contract_ref")}
		st.DependsOn = strList(so, "depends_on")
		st.RequiresInputs = strList(so, "requires_inputs")
		out = append(out, st)
	}
	return out
}

// scanMissionRoot is discovery._scan_root.
func scanMissionRoot(root string) []string {
	st, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		if filepath.Base(root) == "mission.yaml" {
			return []string{root}
		}
		return nil
	}
	var cands []string
	if b, err := os.ReadFile(filepath.Join(root, "mission-pack.yaml")); err == nil {
		if v, err := pyyaml.SafeLoad(string(b)); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				for _, e := range o.GetList("missions") {
					if eo, ok := e.(*jsonx.Object); ok && eo.GetString("path") != "" {
						cands = append(cands, filepath.Join(root, eo.GetString("path")))
					}
				}
			}
		}
	}
	g1, _ := filepath.Glob(filepath.Join(root, "*", "mission.yaml"))
	sort.Strings(g1)
	cands = append(cands, g1...)
	g2, _ := filepath.Glob(filepath.Join(root, "missions", "*", "mission.yaml"))
	sort.Strings(g2)
	cands = append(cands, g2...)
	if _, err := os.Stat(filepath.Join(root, "mission.yaml")); err == nil {
		cands = append(cands, filepath.Join(root, "mission.yaml"))
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		r := resolvedPath(c)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, c)
	}
	return out
}

func resolvedPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

type discoveryTier struct {
	tier, origin string
	roots        []string
}

// discoveryTiers is discovery._build_tiers for a project.
func discoveryTiers(root string) []discoveryTier {
	k := filepath.Join(root, ".kittify")
	return []discoveryTier{
		{"explicit", "explicit_paths", nil},
		{"env", "SPEC_KITTY_MISSION_PATHS", envMissionPaths()},
		{"project_override", filepath.Join(k, "overrides", "missions"), []string{filepath.Join(k, "overrides", "missions")}},
		{"project_legacy", filepath.Join(k, "missions"), []string{filepath.Join(k, "missions")}},
		{"org", "org_roots", dct.OrgRoots(root)},
		{"user_global", filepath.Join(project.GlobalKittify(), "missions"), []string{filepath.Join(project.GlobalKittify(), "missions")}},
		{"project_config", filepath.Join(k, "config.yaml"), projectConfigPackPaths(root)},
		{"built_in", "builtin_roots", []string{"<builtin>"}},
	}
}

// DiscoverMissions is discover_missions_with_warnings.
func DiscoverMissions(root string) ([]DiscoveredMission, []discoveryWarning) {
	var found []DiscoveredMission
	var warns []discoveryWarning
	selected := map[string]bool{}
	add := func(t discoveryTier, p, display string, b []byte) {
		tpl, err := loadMissionTemplateFile(display, b)
		if err != nil {
			warns = append(warns, discoveryWarning{display, t.tier, t.origin, err.Error()})
			return
		}
		sel := !selected[tpl.Key]
		selected[tpl.Key] = true
		found = append(found, DiscoveredMission{Key: tpl.Key, Path: p, Origin: t.origin, Tier: t.tier, Selected: sel})
	}
	for _, t := range discoveryTiers(root) {
		for _, r := range t.roots {
			if r == "<builtin>" {
				bi := dct.BuiltIn()
				names := bi.Walk("missions", "mission.yaml", true)
				sort.Strings(names)
				for _, rel := range names {
					if strings.Count(strings.TrimPrefix(rel, "missions/"), "/") != 1 {
						continue
					}
					b, err := assets.ReadFile(path.Join("packs/built-in", rel))
					if err != nil {
						continue
					}
					add(t, bi.Path(rel), bi.Path(rel), b)
				}
				continue
			}
			for _, f := range scanMissionRoot(r) {
				b, err := os.ReadFile(f)
				if err != nil {
					warns = append(warns, discoveryWarning{f, t.tier, t.origin, "Mission template not found: " + f})
					continue
				}
				add(t, resolvedPath(f), f, b)
			}
		}
	}
	return found, warns
}

func loaderError(code, msg string, details *jsonx.Object) *jsonx.Object {
	return jsonx.NewObject("result", "error", "error_code", code, "message", msg, "details", details)
}

func templateLoadFailure(err error, file, key string) *jsonx.Object {
	le, _ := err.(*templateLoadError)
	if le == nil {
		le = &templateLoadError{kind: "runtime", msg: err.Error()}
	}
	switch le.kind {
	case "pydantic":
		if len(le.missing) > 0 {
			l := []any{}
			for _, m := range le.missing {
				l = append(l, m)
			}
			return loaderError("MISSION_REQUIRED_FIELD_MISSING", fmt.Sprintf("Required mission field(s) missing in %s: %s", file, strings.Join(le.missing, ", ")),
				jsonx.NewObject("file", file, "mission_key", key, "field", le.missing[0], "missing_fields", l, "parse_error", le.msg))
		}
		return loaderError("MISSION_YAML_MALFORMED", "Mission file failed schema validation: "+file,
			jsonx.NewObject("file", file, "mission_key", key, "parse_error", le.msg))
	case "yaml":
		return loaderError("MISSION_YAML_MALFORMED", "Mission file is not valid YAML: "+file,
			jsonx.NewObject("file", file, "mission_key", key, "parse_error", le.msg))
	case "nosteps":
		return loaderError("MISSION_REQUIRED_FIELD_MISSING", fmt.Sprintf("Required mission field(s) missing in %s: steps", file),
			jsonx.NewObject("file", file, "mission_key", key, "field", "steps", "missing_fields", []any{"steps"},
				"parse_error", "Mission template must declare at least one step."))
	}
	return loaderError("MISSION_YAML_MALFORMED", le.msg, jsonx.NewObject("file", file, "mission_key", key, "parse_error", le.msg))
}

// validateCustomMission is validate_custom_mission: (template, selected,
// error envelope or nil, warnings).
func validateCustomMission(root, key string) (*Template, *DiscoveredMission, *jsonx.Object, []any) {
	found, warns := DiscoverMissions(root)
	var sel *DiscoveredMission
	for i := range found {
		if found[i].Key == key && found[i].Selected {
			sel = &found[i]
			break
		}
	}
	warnings := []any{}
	var shadowed []any
	for _, f := range found {
		if f.Key == key && !f.Selected {
			shadowed = append(shadowed, f.Path)
		}
	}
	if len(shadowed) > 0 && sel != nil {
		warnings = append(warnings, jsonx.NewObject("code", "MISSION_KEY_SHADOWED",
			"message", fmt.Sprintf("Mission '%s' is defined in multiple tiers; the %s tier wins.", key, pyjson.ReprString(sel.Tier)),
			"details", jsonx.NewObject("mission_key", key, "selected_path", sel.Path, "selected_tier", sel.Tier, "shadowed_paths", shadowed)))
	}
	for _, w := range warns {
		if w.Tier == "project_config" {
			warnings = append(warnings, jsonx.NewObject("code", "MISSION_PACK_LOAD_FAILED", "message", "Mission pack entry failed to load: "+w.Path,
				"details", jsonx.NewObject("pack_root", w.Origin, "failed_path", w.Path, "parse_error", w.Error, "tier", w.Tier)))
		}
	}
	if sel == nil {
		for _, w := range warns {
			if filepath.Base(w.Path) == "mission.yaml" && filepath.Base(filepath.Dir(w.Path)) == key {
				b, err := os.ReadFile(w.Path)
				if err != nil {
					return nil, nil, loaderError("MISSION_YAML_MALFORMED", "Mission template not found: "+w.Path,
						jsonx.NewObject("file", w.Path, "mission_key", key, "parse_error", "Mission template not found: "+w.Path)), warnings
				}
				_, lerr := loadMissionTemplateFile(w.Path, b)
				if lerr == nil {
					lerr = &templateLoadError{kind: "runtime", msg: "Mission file failed to load (transient): " + w.Error}
				}
				return nil, nil, templateLoadFailure(lerr, w.Path, key), warnings
			}
		}
		tiers := map[string]bool{}
		for _, f := range found {
			tiers[f.Tier] = true
		}
		tl := []any{}
		for _, t := range dct.SortedKeys(tiers) {
			tl = append(tl, t)
		}
		return nil, nil, loaderError("MISSION_KEY_UNKNOWN", fmt.Sprintf("Mission '%s' was not found in any discovery tier.", key),
			jsonx.NewObject("mission_key", key, "tiers_searched", tl)), warnings
	}
	reserved := false
	for _, r := range reservedMissionKeys {
		reserved = reserved || r == key
	}
	if sel.Tier != "built_in" && reserved {
		rk := []any{}
		for _, r := range reservedMissionKeys {
			rk = append(rk, r)
		}
		return nil, sel, loaderError("MISSION_KEY_RESERVED", fmt.Sprintf("Mission key '%s' is reserved for the built-in tier; the %s tier may not shadow built-in keys.", key, pyjson.ReprString(sel.Tier)),
			jsonx.NewObject("mission_key", key, "file", sel.Path, "tier", sel.Tier, "reserved_keys", rk)), warnings
	}
	b, err := os.ReadFile(sel.Path)
	if err != nil {
		if sel.Tier == "built_in" {
			b, err = assets.ReadFile(path.Join("packs/built-in", dct.BuiltInRel(sel.Path)))
		}
	}
	var tpl *Template
	if err == nil {
		tpl, err = loadMissionTemplateFile(sel.Path, b)
	}
	if err != nil {
		return nil, sel, templateLoadFailure(err, sel.Path, key), warnings
	}
	var errs []*jsonx.Object
	if len(tpl.Steps) == 0 {
		errs = append(errs, loaderError("MISSION_REQUIRED_FIELD_MISSING", fmt.Sprintf("Required mission field(s) missing in %s: steps", sel.Path),
			jsonx.NewObject("file", sel.Path, "mission_key", key, "field", "steps", "missing_fields", []any{"steps"},
				"parse_error", "Mission template must declare at least one step.")))
	} else if last := tpl.Steps[len(tpl.Steps)-1].ID; last != "retrospective" {
		errs = append(errs, loaderError("MISSION_RETROSPECTIVE_MISSING", fmt.Sprintf("Mission '%s' must end with a step whose id is 'retrospective'; got %s.", key, pyjson.ReprString(last)),
			jsonx.NewObject("file", sel.Path, "mission_key", key, "actual_last_step_id", last, "expected", "retrospective")))
	}
	for _, st := range tpl.Steps {
		if st.ID == "retrospective" {
			continue
		}
		prof, ref := strings.TrimSpace(st.AgentProfile) != "", strings.TrimSpace(st.ContractRef) != ""
		if len(st.RequiresInputs) == 0 && !prof && !ref {
			errs = append(errs, loaderError("MISSION_STEP_NO_PROFILE_BINDING", fmt.Sprintf("Step '%s' has no requires_inputs, agent_profile, or contract_ref binding; one is required so the composition gate can dispatch it.", st.ID),
				jsonx.NewObject("file", sel.Path, "mission_key", key, "step_id", st.ID)))
		}
		if prof && ref {
			errs = append(errs, loaderError("MISSION_STEP_AMBIGUOUS_BINDING", fmt.Sprintf("Step '%s' declares BOTH agent_profile and contract_ref; pick one.", st.ID),
				jsonx.NewObject("file", sel.Path, "mission_key", key, "step_id", st.ID)))
		}
	}
	errs = append(errs, dependencyErrors(tpl, sel.Path, key)...)
	if len(errs) > 0 {
		return tpl, sel, errs[0], warnings
	}
	return tpl, sel, nil, warnings
}

// dependencyErrors is validator._dependency_errors.
func dependencyErrors(t *Template, file, key string) []*jsonx.Object {
	all := append(append([]TemplateStep{}, t.Steps...), t.AuditSteps...)
	deps := map[string][]string{}
	for _, s := range all {
		deps[s.ID] = s.DependsOn
	}
	var errs []*jsonx.Object
	for _, s := range all {
		for _, d := range s.DependsOn {
			if _, ok := deps[d]; !ok {
				errs = append(errs, loaderError("MISSION_STEP_DEPENDENCY_INVALID", fmt.Sprintf("Step '%s' depends on unknown step '%s'.", s.ID, d),
					jsonx.NewObject("file", file, "mission_key", key, "step_id", s.ID, "dependency", d)))
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	visited := map[string]bool{}
	var visiting []string
	var visit func(id string) []string
	visit = func(id string) []string {
		for i, v := range visiting {
			if v == id {
				return append(append([]string{}, visiting[i:]...), id)
			}
		}
		if visited[id] {
			return nil
		}
		visiting = append(visiting, id)
		for _, d := range deps[id] {
			if c := visit(d); c != nil {
				return c
			}
		}
		visiting = visiting[:len(visiting)-1]
		visited[id] = true
		return nil
	}
	for _, s := range all {
		if c := visit(s.ID); c != nil {
			l := []any{}
			for _, x := range c {
				l = append(l, x)
			}
			return []*jsonx.Object{loaderError("MISSION_STEP_DEPENDENCY_INVALID", fmt.Sprintf("Mission dependency cycle: %s.", strings.Join(c, " -> ")),
				jsonx.NewObject("file", file, "mission_key", key, "step_id", c[0], "cycle", l))}
		}
	}
	return nil
}

// RunCustomMission is mission_loader.command.run_custom_mission: the
// wire envelope and the exit code (0 success, 1 run start failed, 2
// validation error).
func RunCustomMission(root, key, slug string) (*jsonx.Object, int) {
	tpl, sel, errEnv, warnings := validateCustomMission(root, key)
	if errEnv != nil {
		errEnv.Set("warnings", warnings)
		return errEnv, 2
	}
	for _, st := range tpl.Steps {
		if st.ContractRef == "" {
			continue
		}
		if LookupStepContractByID(root, st.ContractRef) == nil {
			e := loaderError("MISSION_CONTRACT_REF_UNRESOLVED", fmt.Sprintf("Step %s references contract %s, which is not present in the on-disk MissionStepContractRepository.", pyjson.ReprString(st.ID), pyjson.ReprString(st.ContractRef)),
				jsonx.NewObject("file", sel.Path, "mission_key", key, "step_id", st.ID, "contract_ref", st.ContractRef))
			e.Set("warnings", warnings)
			return e, 2
		}
	}
	dir := project.Specs(root, slug)
	meta := mission.ReadMetaOrEmpty(dir)
	if existing := readMissionType(meta); existing != "" && existing != key {
		e := loaderError("MISSION_TYPE_CONFLICT", fmt.Sprintf("Mission %s already has mission type %s; refusing to start a %s run on it. Pass a new --mission slug to create a separate %s mission.",
			pyjson.ReprString(slug), pyjson.ReprString(existing), pyjson.ReprString(key), pyjson.ReprString(key)),
			jsonx.NewObject("mission_key", key, "mission_slug", slug, "existing_mission_type", existing, "feature_dir", dir))
		e.Set("warnings", warnings)
		return e, 2
	}
	ref, err := GetOrStartRun(root, slug, meta.GetString("mission_id"), key)
	if err != nil {
		return jsonx.NewObject("result", "error", "error_code", "RUN_START_FAILED", "message", err.Error(),
			"details", jsonx.NewObject("mission_key", key, "mission_slug", slug), "warnings", warnings), 1
	}
	ensureCustomMeta(dir, key)
	var mid any
	if id := mission.ReadMetaOrEmpty(dir).GetString("mission_id"); id != "" {
		mid = id
	}
	return jsonx.NewObject("result", "success", "mission_key", key, "mission_slug", slug, "mission_id", mid,
		"feature_dir", dir, "run_dir", ref.RunDir, "warnings", warnings), 0
}

// readMissionType is charter.activation.api.read_mission_type (the
// canonical mission_type field only).
func readMissionType(meta *jsonx.Object) string {
	return strings.TrimSpace(meta.GetString("mission_type"))
}

// ensureCustomMeta is _ensure_feature_metadata.
func ensureCustomMeta(dir, key string) {
	os.MkdirAll(dir, 0o755)
	meta := mission.ReadMetaOrEmpty(dir)
	meta.Set("mission_type", key)
	meta.Set("mission_key", key)
	if !meta.Has("mission") {
		meta.Set("mission", key)
	}
	mission.WriteMeta(dir, meta, false)
}
