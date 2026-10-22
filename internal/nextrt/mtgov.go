package nextrt

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// missionTypeProfile is the field-merged MissionTypeProfile for missionType
// (built-in packs/built-in/missions/<type>/governance-profile.yaml, then org
// mission_types/<type>/, then the project .kittify/charter-packs/mission_types/<type>/
// override); nil when no layer defines it.
func missionTypeProfile(root, missionType string) *jsonx.Object {
	var merged *jsonx.Object
	overlay := func(b []byte) {
		o, err := yamlx.ParseObject(b)
		if err != nil || o == nil {
			return
		}
		if merged == nil {
			merged = jsonx.NewObject()
		}
		for _, kv := range o.Pairs() {
			merged.Set(kv.Key, kv.Value)
		}
	}
	if b, err := assets.ReadFile("packs/built-in/missions/" + missionType + "/governance-profile.yaml"); err == nil {
		overlay(b)
	}
	for _, org := range dct.OrgRoots(root) {
		if b, err := os.ReadFile(filepath.Join(org, "mission_types", missionType, "governance-profile.yaml")); err == nil {
			overlay(b)
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot), "mission_types", missionType, "governance-profile.yaml")); err == nil {
		overlay(b)
	}
	return merged
}

// projectHasPackOverrides is _project_has_pack_overrides: charter.yaml's
// governance.charter declares a non-empty selected_<kind> list.
func projectHasPackOverrides(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "charter", "charter.yaml"))
	if err != nil {
		return false
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return false
	}
	gov := o.GetObject("governance")
	if gov == nil {
		return false
	}
	ch := gov.GetObject("charter")
	if ch == nil {
		return false
	}
	for _, kv := range ch.Pairs() {
		if l, ok := kv.Value.([]any); ok && strings.HasPrefix(kv.Key, "selected_") && len(l) > 0 {
			return true
		}
	}
	return false
}

// missionTypeGovernancePayload is prompt_builder._mission_type_governance_payload:
// the compact "Mission-Type Governance Profile" block of a WP prompt ("" when
// the feature dir has no meta.json or the mission is typeless). An unknown,
// unregistered type without project overrides is UnknownMissionTypeError.
func missionTypeGovernancePayload(root, featureDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(featureDir, mission.MetaFile)); err != nil {
		return "", nil
	}
	mt := mission.MissionType(mission.ReadMetaOrEmpty(featureDir))
	if mt == "" {
		return "", nil
	}
	registered, err := dct.ActivatedMissionTypes(root)
	if err != nil {
		return "", nil
	}
	isReg := false
	for _, r := range registered {
		if r == mt {
			isReg = true
		}
	}
	if !isReg && !projectHasPackOverrides(root) {
		return "", &dct.UnknownMissionTypeError{ID: mt, Registered: registered}
	}
	p := missionTypeProfile(root, mt)
	lines := []string{"Mission-Type Governance Profile: " + mt}
	if p == nil {
		lines = append(lines, "  - No built-in profile; project overrides apply.")
		return strings.Join(lines, "\n"), nil
	}
	if ts, _ := p.Get("template_set"); ts != nil {
		lines = append(lines, "  template_set: "+yamlScalar(ts))
	} else {
		lines = append(lines, "  template_set: (none — mission resolves its own)")
	}
	for _, f := range []string{"selected_directives", "selected_tactics", "selected_paradigms", "selected_styleguides",
		"selected_toolguides", "selected_procedures", "selected_agent_profiles", "selected_mission_step_contracts"} {
		ids := strItems(p.GetList(f))
		if len(ids) > 0 {
			lines = append(lines, "  "+f+": "+strings.Join(ids, ", "))
		} else {
			lines = append(lines, "  "+f+": (none)")
		}
	}
	if tools := strItems(p.GetList("available_tools")); len(tools) > 0 {
		lines = append(lines, "  available_tools: "+strings.Join(tools, ", "))
	}
	if acts := p.GetList("activations"); len(acts) > 0 {
		lines = append(lines, "  activations: "+strconv.Itoa(len(acts))+" entries")
	}
	return strings.Join(lines, "\n"), nil
}

func yamlScalar(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonx.Dumps(v, jsonx.Options{Indent: -1})
}

func strItems(l []any) []string {
	var out []string
	for _, x := range l {
		out = append(out, yamlScalar(x))
	}
	return out
}
