package charter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports the bootstrap "Selected activations:" stanza
// (context_renderers/activation_block.py, charter/activation/_activation_render.py,
// charter/activation/activations.py) and the selection-block org
// provenance suffix (selection_block._provenance_suffix).

// ActivationEntry is activations.ActivationEntry.
type ActivationEntry struct {
	Context      *jsonx.Object // activation_context (str -> str)
	CharterPack  string
	ArtifactID   string
	ArtifactKind string // canonical plural, "" when absent
}

var allowedActions = map[string]bool{"specify": true, "plan": true, "tasks": true, "implement": true, "review": true,
	"merge": true, "accept": true, "charter.interview": true, "charter.generate": true, "charter.context": true}

var fineGrainedTriggers = map[string]bool{"write_comment": true, "write_docstring": true, "rename_identifier": true, "add_dependency": true}

var activationKindSingularToPlural = [][2]string{{"directive", "directives"}, {"tactic", "tactics"}, {"styleguide", "styleguides"},
	{"toolguide", "toolguides"}, {"paradigm", "paradigms"}, {"procedure", "procedures"}, {"agent_profile", "agent_profiles"},
	{"mission_step_contract", "mission_step_contracts"}, {"glossary_pack", "glossary_packs"}, {"anti_pattern", "anti_patterns"}, {"skill", "skills"}}

var activationAllowedKinds = []string{"agent_profiles", "anti_patterns", "assets", "directives", "glossary_packs", "mission_step_contracts",
	"paradigms", "procedures", "skills", "styleguides", "tactics", "templates", "toolguides"}

func normalizeActivationKind(k string) string {
	for _, kv := range activationKindSingularToPlural {
		if kv[0] == k {
			return kv[1]
		}
	}
	return k
}

func singularActivationKind(plural string) string {
	for _, kv := range activationKindSingularToPlural {
		if kv[1] == plural {
			return kv[0]
		}
	}
	return plural
}

// allowedMissionTypes is ALLOWED_MISSION_TYPES (built-in mission type ids
// plus the wildcards).
func allowedMissionTypes() []string {
	set := map[string]bool{"any": true, "generic": true}
	for id := range dct.MissionTypeRoster("") {
		set[id] = true
	}
	var out []string
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// parseActivationEntry is ActivationEntry.model_validate (extra="forbid").
func parseActivationEntry(raw any) (*ActivationEntry, error) {
	o, ok := raw.(*jsonx.Object)
	if !ok {
		return nil, pydantic.New("ActivationEntry", pydantic.ErrorDetail{Type: pydantic.TypeModelType, Msg: pydantic.ModelTypeMsg("ActivationEntry"), Input: raw})
	}
	ve := pydantic.New("ActivationEntry")
	e := &ActivationEntry{}
	known := map[string]bool{"activation_context": true, "charter_pack_id": true, "artifact_id": true, "artifact_kind": true}
	if v, ok := o.Get("activation_context"); !ok {
		ve.Add("activation_context", pydantic.TypeMissing, pydantic.MsgMissing, o)
	} else if ctxObj, isObj := v.(*jsonx.Object); !isObj {
		ve.Add("activation_context", pydantic.TypeDictType, pydantic.MsgDictType, v)
	} else {
		bad := false
		for _, kv := range ctxObj.Pairs() {
			if _, isStr := kv.Value.(string); !isStr {
				ve.Add("activation_context."+kv.Key, pydantic.TypeStringType, pydantic.MsgStringType, kv.Value)
				bad = true
			}
		}
		if !bad {
			if mt := ctxObj.GetString("mission_type"); mt != "" || ctxObj.Has("mission_type") {
				allowed := allowedMissionTypes()
				in := false
				for _, a := range allowed {
					in = in || a == mt
				}
				if !in {
					ve.Add("activation_context", pydantic.TypeValueError, pydantic.ValueErrorMsg(fmt.Sprintf("activation_context.mission_type=%s is not in ALLOWED_MISSION_TYPES=%s", dct.PyRepr(mt), pyStrList(allowed))), ctxObj)
				}
			}
			if ve.Len() == 0 && ctxObj.Has("action") {
				act := ctxObj.GetString("action")
				if !allowedActions[act] && !fineGrainedTriggers[act] && act != "any" && act != "generic" {
					var trig []string
					for a := range allowedActions {
						trig = append(trig, a)
					}
					for a := range fineGrainedTriggers {
						trig = append(trig, a)
					}
					sort.Strings(trig)
					ve.Add("activation_context", pydantic.TypeValueError, pydantic.ValueErrorMsg(fmt.Sprintf("activation_context.action=%s is not in REGISTERED_TRIGGERS=%s and is not one of the wildcards ['any', 'generic']", dct.PyRepr(act), pyStrList(trig))), ctxObj)
				}
			}
		}
		e.Context = ctxObj
	}
	for _, f := range []string{"charter_pack_id", "artifact_id"} {
		v, ok := o.Get(f)
		switch {
		case !ok:
			ve.Add(f, pydantic.TypeMissing, pydantic.MsgMissing, o)
		default:
			s, isStr := v.(string)
			if !isStr {
				ve.Add(f, pydantic.TypeStringType, pydantic.MsgStringType, v)
			} else if f == "charter_pack_id" {
				e.CharterPack = s
			} else {
				e.ArtifactID = s
			}
		}
	}
	if v, ok := o.Get("artifact_kind"); ok && v != nil {
		s, isStr := v.(string)
		if !isStr {
			ve.Add("artifact_kind", pydantic.TypeStringType, pydantic.MsgStringType, v)
		} else {
			n := normalizeActivationKind(s)
			in := false
			for _, k := range activationAllowedKinds {
				in = in || k == n
			}
			if !in {
				var sing []string
				for _, kv := range activationKindSingularToPlural {
					sing = append(sing, kv[0])
				}
				sort.Strings(sing)
				ve.Add("artifact_kind", pydantic.TypeValueError, pydantic.ValueErrorMsg(fmt.Sprintf("artifact_kind=%s is not a known ActiveCharterService kind. Accepted (plural): %s. Accepted (singular alias): %s.", dct.PyRepr(s), pyStrList(activationAllowedKinds), pyStrList(sing))), s)
			} else {
				e.ArtifactKind = n
			}
		}
	}
	for _, kv := range o.Pairs() {
		if !known[kv.Key] {
			ve.Add(kv.Key, pydantic.TypeExtraForbidden, pydantic.MsgExtraForbidden, kv.Value)
		}
	}
	if ve.Len() > 0 {
		return nil, ve
	}
	return e, nil
}

func pyStrList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = dct.PyRepr(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// identityKey is _activation_identity_key.
func (e *ActivationEntry) identityKey() string {
	keys := make([]string, 0, e.Context.Len())
	for _, kv := range e.Context.Pairs() {
		keys = append(keys, kv.Key)
	}
	sort.Strings(keys)
	m := map[string]string{}
	for _, kv := range e.Context.Pairs() {
		m[kv.Key] = fmt.Sprint(kv.Value)
	}
	b, _ := json.Marshal(m) // sorted keys, like json.dumps(sort_keys=True) for str values
	return string(b) + "\x00" + e.CharterPack + "\x00" + e.ArtifactID + "\x00" + e.ArtifactKind
}

// loadGovernanceActivations is _load_governance_activations: charter.yaml
// governance.activations, [] when the governance section does not validate.
func loadGovernanceActivations(root string) []*ActivationEntry {
	b := LoadBundle(root)
	if b == nil {
		return nil
	}
	g := b.GetObject("governance")
	if g == nil {
		return nil
	}
	var out []*ActivationEntry
	for _, raw := range g.GetList("activations") {
		e, err := parseActivationEntry(raw)
		if err != nil {
			return nil
		}
		out = append(out, e)
	}
	return out
}

// orgCharterDocs is org_pack_discovery._iter_org_charter_docs.
func orgCharterDocs(root string) [][2]any {
	var out [][2]any
	for _, p := range LoadPackRegistry(root).Packs {
		r, err := p.EffectiveRoot(root)
		if err != nil {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(r, "org-charter.yaml"))
		if err != nil {
			continue
		}
		v, err := pyyaml.SafeLoad(string(b))
		if err != nil {
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		out = append(out, [2]any{p.Name, o})
	}
	return out
}

// readOrgActivations is _read_org_activations (a malformed entry raises).
func readOrgActivations(root string) ([]*ActivationEntry, error) {
	var out []*ActivationEntry
	for _, d := range orgCharterDocs(root) {
		o := d[1].(*jsonx.Object)
		l, ok := mustGetAny(o, "activations").([]any)
		if !ok {
			continue
		}
		for _, item := range l {
			e, err := parseActivationEntry(item)
			if err != nil {
				return nil, fmt.Errorf("org pack `%s` declares a malformed activations entry %s: %s", d[0], pydantic.Repr(item), err.Error())
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// OrgRequiredSelections is _read_org_required_selections (raw union of
// every pack's required_<kind>, first-seen order).
func OrgRequiredSelections(root string) map[string][]string {
	out := map[string][]string{}
	for _, d := range orgCharterDocs(root) {
		o := d[1].(*jsonx.Object)
		for _, k := range OrgRequirableKinds {
			l, ok := mustGetAny(o, "required_"+k).([]any)
			if !ok {
				continue
			}
			for _, x := range l {
				t := strings.TrimSpace(pyStrValue(x))
				if t == "" {
					continue
				}
				dup := false
				for _, y := range out[k] {
					dup = dup || y == t
				}
				if !dup {
					out[k] = append(out[k], t)
				}
			}
		}
	}
	return out
}

var actionProse = map[string]string{"write_comment": "write a code comment", "write_docstring": "write a docstring",
	"rename_identifier": "rename an identifier", "add_dependency": "add a dependency", "tasks": "work on tasks",
	"charter.interview": "conduct a charter interview", "charter.generate": "generate a charter", "charter.context": "load charter context"}

func actionLabel(tok string) string {
	if p, ok := actionProse[tok]; ok {
		return p
	}
	return tok
}

// renderActivationBlock is activation_block._render_activation_block.
func renderActivationBlock(root string, off *dct.Offering, missionType, action string) (string, error) {
	project := loadGovernanceActivations(root)
	org, err := readOrgActivations(root)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var entries []*ActivationEntry
	for _, e := range append(project, org...) {
		k := e.identityKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return "", nil
	}
	var matched []*ActivationEntry
	for _, e := range entries {
		mt, hasMT := e.Context.Get("mission_type")
		ac, hasAc := e.Context.Get("action")
		mtOK := !hasMT || mt == "generic" || mt == "any" || mt == missionType
		acOK := !hasAc || ac == "generic" || ac == "any" || ac == action || fineGrainedTriggers[fmt.Sprint(ac)]
		if mtOK && acOK {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		return "", nil
	}
	lines := []string{"Selected activations:"}
	for _, e := range matched {
		if len(lines) > 1 {
			lines = append(lines, "")
		}
		kind := e.ArtifactKind
		if kind == "" {
			kind = inferActivationKind(e.ArtifactID, off)
		}
		singular := "artifact"
		if kind != "" {
			singular = singularActivationKind(kind)
		}
		mt, hasMT := e.Context.Get("mission_type")
		ac, hasAc := e.Context.Get("action")
		label := actionLabel(action)
		if hasAc && ac != "generic" && ac != "any" {
			label = actionLabel(fmt.Sprint(ac))
		}
		qual := ""
		if hasMT && mt != "generic" && mt != "any" {
			qual = fmt.Sprintf(" in a %v mission", mt)
		}
		lines = append(lines, fetchStanzaLines(formatSelector(singular, e.ArtifactID), "are about to "+label+qual, "")...)
	}
	return strings.Join(lines, "\n"), nil
}

// formatSelector is fetch_stanza.format_selector.
func formatSelector(kind, id string) string {
	k, i := strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(id)
	if k == "" || i == "" {
		return ""
	}
	return k + ":" + i
}

// inferActivationKind is _activation_render._infer_kind over the offering
// repositories (charter-activatable kind order).
func inferActivationKind(id string, off *dct.Offering) string {
	if off == nil {
		return ""
	}
	for _, kv := range activationKindSingularToPlural {
		k, ok := dct.KindFromPlural(kv[1])
		if !ok {
			continue
		}
		if a := off.Repo(k).Get(id); a != nil && a.ID == id {
			return kv[1]
		}
	}
	return ""
}

// OrgRequiredIDs is org_pack_discovery.read_org_required_ids: the union of
// every org pack's required_<plural> (stripped, non-empty, first-seen
// order); a non-list value is skipped.
func OrgRequiredIDs(root, plural string) []string {
	var out []string
	for _, d := range orgCharterDocs(root) {
		l, ok := mustGetAny(d[1].(*jsonx.Object), "required_"+plural).([]any)
		if !ok {
			continue
		}
		for _, x := range l {
			t := strings.TrimSpace(pyStrValue(x))
			if t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}
