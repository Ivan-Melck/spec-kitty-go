package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// OfferingPack is one pack of the project's offering.
type OfferingPack struct {
	Name, Tier, Root string
}

// ShipsPresets reports whether presets are read from this pack.
func (p OfferingPack) ShipsPresets() bool { return p.Tier != "project" }

// ListOfferingPacks is list_offering_packs: built-in, org packs, project.
func ListOfferingPacks(root string) []OfferingPack {
	out := []OfferingPack{{"built-in", "built-in", dct.BuiltInRoot()}}
	for _, p := range dct.OrgPacks(root) {
		out = append(out, OfferingPack{p.Name, "org", p.Root})
	}
	return append(out, OfferingPack{"project", "project", filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))})
}

// governedKinds are the preset-governed kinds (activatable minus skill and
// glossary_pack), in ArtifactKind order.
var governedKinds = func() []dct.Kind {
	var out []dct.Kind
	for _, k := range dct.AllKinds {
		if k.Activatable() && k != dct.Skill && k != dct.GlossaryPack {
			out = append(out, k)
		}
	}
	return out
}()

// PresetActivationKeys is preset_activation_keys().
func PresetActivationKeys() []string {
	var out []string
	for _, k := range governedKinds {
		out = append(out, "activated_"+k.Plural())
	}
	return out
}

// GovernedKeys is GOVERNED_KEYS.
func GovernedKeys() []string {
	return append(PresetActivationKeys(), "activated_kinds", "mission_type_activations")
}

// Preset is one loaded activation preset.
type Preset struct {
	Name, Description string
	Activations       map[string][]string // present per-kind keys only
	ActivatedKinds    []string            // nil = absent
	MissionTypes      []string            // nil = absent
	Source            string
}

// PresetFormatError is a malformed preset file.
type PresetFormatError struct{ Path, Field, Detail string }

func (e *PresetFormatError) Error() string {
	return fmt.Sprintf("%s: %s: %s", filepath.Base(e.Path), e.Field, e.Detail)
}

var presetNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// PresetFiles lists <pack>/presets/*.yaml sorted by stem.
func PresetFiles(packRoot string) []string {
	m, _ := filepath.Glob(filepath.Join(packRoot, "presets", "*.yaml"))
	var out []string
	for _, f := range m {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return stem(out[i]) < stem(out[j]) })
	return out
}

func stem(p string) string { return strings.TrimSuffix(filepath.Base(p), ".yaml") }

func checkIDList(path, key string, v any) ([]string, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, &PresetFormatError{path, key, "must be a list of strings"}
	}
	var ids []string
	seen := map[string]bool{}
	for _, x := range l {
		s, ok := x.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, &PresetFormatError{path, key, fmt.Sprintf("entry %s is not a non-empty string", reprVal(x))}
		}
		if seen[s] {
			return nil, &PresetFormatError{path, key, fmt.Sprintf("duplicate entry %s", dct.PyRepr(s))}
		}
		seen[s] = true
		ids = append(ids, s)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func reprVal(v any) string {
	if s, ok := v.(string); ok {
		return dct.PyRepr(s)
	}
	if v == nil {
		return "None"
	}
	return pyStrOf(v)
}

// LoadPresetFile is load_preset_file (strict validation).
func LoadPresetFile(path string) (*Preset, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, &PresetFormatError{path, "<file>", "cannot be read: " + err.Error()}
	}
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true})
	if err != nil {
		return nil, &PresetFormatError{path, "<file>", "YAML parse error: " + err.Error()}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &PresetFormatError{path, "<file>", "expected a YAML mapping at top level"}
	}
	allowed := map[string]bool{"name": true, "description": true, "activated_kinds": true, "mission_type_activations": true}
	for _, k := range PresetActivationKeys() {
		allowed[k] = true
	}
	var unknown []string
	for _, k := range o.Keys() {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		for _, k := range unknown {
			if k == "activations" {
				return nil, &PresetFormatError{path, "activations", "a preset carries no context-scoped activation entries"}
			}
		}
		for _, k := range unknown {
			if k == "activated_skills" || k == "activated_glossary_packs" {
				return nil, &PresetFormatError{path, k, "presets do not govern this kind; it keeps its own activation contract"}
			}
		}
		return nil, &PresetFormatError{path, unknown[0], "unknown key(s): " + strings.Join(unknown, ", ")}
	}
	if !o.Has("name") {
		return nil, &PresetFormatError{path, "name", "is required"}
	}
	if !o.Has("description") {
		return nil, &PresetFormatError{path, "description", "is required"}
	}
	nv, _ := o.Get("name")
	name, ok := nv.(string)
	switch {
	case !ok:
		return nil, &PresetFormatError{path, "name", "must be a string"}
	case len([]rune(name)) > 64:
		return nil, &PresetFormatError{path, "name", "is longer than 64 characters"}
	case !presetNameRe.MatchString(name):
		return nil, &PresetFormatError{path, "name", fmt.Sprintf("%s does not match %s", dct.PyRepr(name), presetNameRe.String())}
	case name != stem(path):
		return nil, &PresetFormatError{path, "name", fmt.Sprintf("%s must equal the file stem %s", dct.PyRepr(name), dct.PyRepr(stem(path)))}
	}
	dv, _ := o.Get("description")
	desc, ok := dv.(string)
	if !ok || strings.TrimSpace(desc) == "" {
		return nil, &PresetFormatError{path, "description", "must be a non-empty string"}
	}
	p := &Preset{Name: name, Description: desc, Activations: map[string][]string{}, Source: path}
	for _, k := range PresetActivationKeys() {
		if v, ok := o.Get(k); ok {
			ids, err := checkIDList(path, k, v)
			if err != nil {
				return nil, err
			}
			p.Activations[k] = ids
		}
	}
	if v, ok := o.Get("activated_kinds"); ok {
		ids, err := checkIDList(path, "activated_kinds", v)
		if err != nil {
			return nil, err
		}
		plurals := map[string]bool{}
		for _, k := range dct.AllKinds {
			plurals[k.Plural()] = true
		}
		var bad []string
		for _, x := range ids {
			if !plurals[x] {
				bad = append(bad, x)
			}
		}
		if len(bad) > 0 {
			return nil, &PresetFormatError{path, "activated_kinds", "unknown kind(s): " + strings.Join(bad, ", ")}
		}
		p.ActivatedKinds = ids
	}
	if v, ok := o.Get("mission_type_activations"); ok {
		ids, err := checkIDList(path, "mission_type_activations", v)
		if err != nil {
			return nil, err
		}
		p.MissionTypes = ids
	}
	return p, nil
}

// CodedError is a preset-application error with a stable code.
type CodedError struct {
	Code    string
	Msg     string
	Details []string
	Payload *jsonx.Object
}

func (e *CodedError) Error() string { return e.Msg }

func listing(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// FindOfferingPack is find_offering_pack.
func FindOfferingPack(root, name string) (*OfferingPack, error) {
	packs := ListOfferingPacks(root)
	var names []string
	for i := range packs {
		if packs[i].Name == name {
			return &packs[i], nil
		}
		names = append(names, packs[i].Name)
	}
	return nil, &CodedError{Code: "PACK_NOT_FOUND", Msg: fmt.Sprintf("pack %s not found. Available packs: %s", dct.PyRepr(name), listing(names)),
		Payload: jsonx.NewObject("pack", name, "available", strsAny(names))}
}

// LoadPackPreset is load_pack_preset.
func LoadPackPreset(pack *OfferingPack, name string) (*Preset, error) {
	notFound := func(avail []string) error {
		if avail == nil {
			avail = []string{}
		}
		return &CodedError{Code: "PRESET_NOT_FOUND", Msg: fmt.Sprintf("preset %s not found in pack %s. Presets of %s: %s", dct.PyRepr(name), dct.PyRepr(pack.Name), dct.PyRepr(pack.Name), listing(avail)),
			Payload: jsonx.NewObject("pack", pack.Name, "preset", name, "available", strsAny(avail))}
	}
	if !pack.ShipsPresets() {
		return nil, notFound(nil)
	}
	files := PresetFiles(pack.Root)
	var names []string
	for _, f := range files {
		names = append(names, stem(f))
		if stem(f) == name && presetNameRe.MatchString(name) {
			return LoadPresetFile(f)
		}
	}
	return nil, notFound(names)
}

// PresetPlan is the computed preset application.
type PresetPlan struct {
	Pack, Preset, TargetFile string
	Written                  [][2]any // ordered key -> []string
	Removed                  []string
	Changes                  [][3]any // key, before, after
	Customised               []string
}

// IsNoop reports whether nothing changes.
func (p *PresetPlan) IsNoop() bool { return len(p.Written) == 0 && len(p.Removed) == 0 }

func idKey(key, id string) string {
	if key == "activated_directives" {
		return dct.NormalizeDirectiveID(id)
	}
	return id
}

func union(key string, listed, extra []string) []string {
	out := append([]string{}, listed...)
	seen := map[string]bool{}
	for _, x := range out {
		seen[idKey(key, x)] = true
	}
	for _, x := range extra {
		if k := idKey(key, x); !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}

func sameList(a, b any) bool {
	al, aok := a.([]string)
	bl, bok := b.([]string)
	if aok && bok {
		x := append([]string(nil), al...)
		y := append([]string(nil), bl...)
		sort.Strings(x)
		sort.Strings(y)
		return strings.Join(x, "\x00") == strings.Join(y, "\x00") && len(x) == len(y)
	}
	return equalVals(a, b)
}

func currentValue(doc *jsonx.Object, key string) any {
	v, ok := doc.Get(key)
	if !ok {
		return nil
	}
	if l, ok := v.([]any); ok {
		out := []string{}
		for _, x := range l {
			out = append(out, pyStrOf(x))
		}
		return out
	}
	return v
}

func equalVals(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return jsonx.Dumps(a, jsonx.Line) == jsonx.Dumps(b, jsonx.Line)
}

// resolveIDs checks every listed id resolves in the offering.
func resolveIDs(root string, p *Preset) error {
	lr := dct.LayerRoots(root)
	chain := dct.OrgRoots(root)
	unresolved := map[string][]string{}
	var order []string
	for _, k := range governedKinds {
		key := "activated_" + k.Plural()
		ids, ok := p.Activations[key]
		if !ok || len(ids) == 0 {
			continue
		}
		var missing []string
		if k == dct.AntiPattern {
			g, _ := dct.LoadGraph(root, chain)
			known := map[string]bool{}
			if g != nil {
				for _, n := range g.Nodes {
					if n.Kind == "anti_pattern" {
						known[n.ID()] = true
					}
				}
			}
			for _, id := range ids {
				if !known[id] {
					missing = append(missing, id)
				}
			}
		} else {
			stems := dct.StemsByDeclaredID(k, lr, chain)
			byStem := map[string]bool{}
			for _, s := range stems {
				byStem[s] = true
			}
			for _, id := range ids {
				_, isID := stems[id]
				if !isID && !byStem[id] && !(k == dct.Directive && stems[dct.NormalizeDirectiveID(id)] != "") {
					missing = append(missing, id)
				}
			}
		}
		if len(missing) > 0 {
			unresolved[key] = missing
			order = append(order, key)
		}
	}
	if p.MissionTypes != nil {
		roster := dct.MissionTypeRoster(root)
		var missing []string
		for _, mt := range p.MissionTypes {
			if roster[mt] == nil {
				missing = append(missing, mt)
			}
		}
		if len(missing) > 0 {
			unresolved["mission_type_activations"] = missing
			order = append(order, "mission_type_activations")
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	var named []string
	var lines []string
	un := &jsonx.Object{}
	for _, k := range order {
		named = append(named, k+": "+strings.Join(unresolved[k], ", "))
		for _, id := range unresolved[k] {
			lines = append(lines, "  "+k+": "+id)
		}
		un.Set(k, strsAny(unresolved[k]))
	}
	sort.Strings(named)
	return &CodedError{Code: "PRESET_ID_UNRESOLVED", Msg: fmt.Sprintf("%s lists ids that resolve nowhere in the offering (%s)", p.Source, strings.Join(named, "; ")),
		Details: lines, Payload: jsonx.NewObject("preset_file", p.Source, "unresolved", un, "reasons", &jsonx.Object{})}
}

// PlanPreset is plan_preset_application.
func PlanPreset(root, packName, presetName string) (*PresetPlan, error) {
	pack, err := FindOfferingPack(root, packName)
	if err != nil {
		return nil, err
	}
	p, err := LoadPackPreset(pack, presetName)
	if err != nil {
		return nil, err
	}
	if err := resolveIDs(root, p); err != nil {
		return nil, err
	}
	target := map[string]any{}
	for _, key := range PresetActivationKeys() {
		if l, ok := p.Activations[key]; ok {
			target[key] = union(key, l, nil)
		} else {
			target[key] = nil
		}
	}
	if p.ActivatedKinds == nil {
		target["activated_kinds"] = nil
	} else {
		target["activated_kinds"] = union("activated_kinds", p.ActivatedKinds, nil)
	}
	hasMT := p.MissionTypes != nil
	if hasMT {
		target["mission_type_activations"] = append([]string{}, p.MissionTypes...)
	}
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	var defaultMT any
	if dp, err := LoadPresetFile(filepath.Join(dct.BuiltInRoot(), "presets", "default.yaml")); err == nil && dp.MissionTypes != nil {
		defaultMT = append([]string{}, dp.MissionTypes...)
	}
	plan := &PresetPlan{Pack: pack.Name, Preset: p.Name, TargetFile: t.Path}
	for _, key := range GovernedKeys() {
		after, ok := target[key]
		if !ok {
			continue
		}
		before := currentValue(t.Data, key)
		if equalVals(before, after) || (before == nil && after == nil) {
			continue
		}
		if l, isL := after.([]string); isL && l == nil {
			after = nil
		}
		if before == nil && after == nil {
			continue
		}
		plan.Changes = append(plan.Changes, [3]any{key, before, after})
		if after == nil {
			plan.Removed = append(plan.Removed, key)
		} else {
			plan.Written = append(plan.Written, [2]any{key, after})
		}
		var def any
		if key == "mission_type_activations" {
			def = defaultMT
		}
		if before != nil && !sameList(before, after) && !sameList(before, def) {
			plan.Customised = append(plan.Customised, key)
		}
	}
	return plan, nil
}

func renderValue(v any) string {
	if v == nil {
		return "absent"
	}
	if l, ok := v.([]string); ok {
		return "[" + strings.Join(l, ", ") + "]"
	}
	return pyStrOf(v)
}

// ApplyPresetPlan writes the plan (PRESET_WOULD_OVERWRITE without force).
func ApplyPresetPlan(root string, plan *PresetPlan, force bool) error {
	if len(plan.Customised) > 0 && !force {
		var lines []string
		diff := &jsonx.Object{}
		for _, c := range plan.Changes {
			key := c[0].(string)
			lines = append(lines, fmt.Sprintf("  %s: %s -> %s", key, renderValue(c[1]), renderValue(c[2])))
			jv := func(v any) any {
				if l, ok := v.([]string); ok {
					return strsAny(l)
				}
				return v
			}
			diff.Set(key, jsonx.NewObject("before", jv(c[1]), "after", jv(c[2])))
		}
		return &CodedError{Code: "PRESET_WOULD_OVERWRITE",
			Msg:     fmt.Sprintf("preset %s of pack %s would change customised key(s) %s in %s; --force applies it", dct.PyRepr(plan.Preset), dct.PyRepr(plan.Pack), strings.Join(plan.Customised, ", "), plan.TargetFile),
			Details: lines, Payload: jsonx.NewObject("pack", plan.Pack, "preset", plan.Preset, "customised", strsAny(plan.Customised), "diff", diff)}
	}
	if plan.IsNoop() {
		return nil
	}
	t, err := LoadWriteTarget(root)
	if err != nil {
		return err
	}
	for _, w := range plan.Written {
		t.Data.Set(w[0].(string), strsAny(w[1].([]string)))
	}
	for _, k := range plan.Removed {
		t.Data.Delete(k)
	}
	return t.Save()
}

// PresetPayload is the --json shape of an applied preset.
func PresetPayload(plan *PresetPlan) *jsonx.Object {
	w := &jsonx.Object{}
	for _, x := range plan.Written {
		w.Set(x[0].(string), strsAny(x[1].([]string)))
	}
	rm := strsAny(plan.Removed)
	return jsonx.NewObject("pack", plan.Pack, "preset", plan.Preset, "written", w, "removed", rm, "target_file", plan.TargetFile)
}
