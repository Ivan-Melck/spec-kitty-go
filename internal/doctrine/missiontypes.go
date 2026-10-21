package doctrine

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// MissionType is a resolved mission type (upstream MissionType after the
// step projection of action_sequence / template_set).
type MissionType struct {
	ID             string
	DisplayName    string
	Extends        *string
	ActionSequence []string
	// TemplateSet maps artifact_key -> template file in sequence order (nil = none).
	TemplateSet *jsonx.Object
	SourceLayer string
}

// MissionStep is one step.yaml of a mission type.
type MissionStep struct {
	ID               string
	SequenceIndex    *int
	InActionSequence bool
	ArtifactKey      string
	TemplateFile     string
	Data             *jsonx.Object
	Path             string
}

// ProjectMissionTypesDir is the project-layer mission type directory.
const ProjectMissionTypesDir = ".kittify/missions/mission_types"

func parseStep(b []byte, p string) *MissionStep {
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil
	}
	s := &MissionStep{ID: o.GetString("id"), InActionSequence: o.GetBool("in_action_sequence"), Data: o, Path: p}
	if n, ok := o.GetInt("sequence_index"); ok {
		s.SequenceIndex = &n
	}
	if t := o.GetObject("template"); t != nil {
		s.ArtifactKey, s.TemplateFile = t.GetString("artifact_key"), t.GetString("template_file")
	}
	return s
}

// MissionSteps loads the steps of a mission type: built-in
// missions/mission-steps/<type>/*/step.yaml overlaid by org packs and the
// project pack (<root>/mission-steps/<type>/*/step.yaml), keyed by step id.
func MissionSteps(repoRoot, missionType string) map[string]*MissionStep {
	out := map[string]*MissionStep{}
	bi := BuiltIn()
	for _, rel := range bi.Walk(path.Join("missions/mission-steps", missionType), "step.yaml", true) {
		b, err := bi.ReadFile(rel)
		if err != nil {
			continue
		}
		if s := parseStep(b, bi.Path(rel)); s != nil && s.ID != "" {
			out[s.ID] = s
		}
	}
	var roots []string
	if repoRoot != "" {
		roots = append(roots, OrgRoots(repoRoot)...)
		roots = append(roots, filepath.Join(repoRoot, filepath.FromSlash(ProjectPackRoot)))
	}
	for _, r := range roots {
		p := DiskPack(filepath.Join(r, "mission-steps", missionType))
		for _, rel := range p.Walk(".", "step.yaml", true) {
			b, err := p.ReadFile(rel)
			if err != nil {
				continue
			}
			if s := parseStep(b, p.Path(rel)); s != nil && s.ID != "" {
				out[s.ID] = s
			}
		}
	}
	return out
}

// OrderedSteps sorts steps by (sequence_index, id), unindexed last.
func OrderedSteps(steps map[string]*MissionStep) []*MissionStep {
	var l []*MissionStep
	for _, s := range steps {
		l = append(l, s)
	}
	idx := func(s *MissionStep) int {
		if s.SequenceIndex == nil {
			return 1 << 30
		}
		return *s.SequenceIndex
	}
	sort.Slice(l, func(i, j int) bool {
		if idx(l[i]) != idx(l[j]) {
			return idx(l[i]) < idx(l[j])
		}
		return l[i].ID < l[j].ID
	})
	return l
}

func projectSequence(steps map[string]*MissionStep) []string {
	var out []string
	for _, s := range OrderedSteps(steps) {
		if s.InActionSequence {
			out = append(out, s.ID)
		}
	}
	return out
}

func projectTemplateSet(steps map[string]*MissionStep) *jsonx.Object {
	o := &jsonx.Object{}
	for _, s := range OrderedSteps(steps) {
		if s.ArtifactKey != "" {
			o.Set(s.ArtifactKey, s.TemplateFile)
		}
	}
	if o.Len() == 0 {
		return nil
	}
	return o
}

func loadMissionTypeFile(b []byte, stem, layer, repoRoot string) (*MissionType, error) {
	o, err := yamlx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	mt := &MissionType{ID: o.GetString("id"), DisplayName: o.GetString("display_name"), SourceLayer: layer}
	if mt.ID == "" {
		mt.ID = stem
	}
	if v, ok := o.Get("extends"); ok && v != nil {
		s := pyStr(v)
		mt.Extends = &s
	}
	steps := MissionSteps(repoRoot, mt.ID)
	if l := o.GetList("action_sequence"); len(l) > 0 {
		mt.ActionSequence = strList(l)
	} else {
		mt.ActionSequence = projectSequence(steps)
	}
	if ts := o.GetObject("template_set"); ts != nil {
		mt.TemplateSet = ts
	} else {
		mt.TemplateSet = projectTemplateSet(steps)
	}
	return mt, nil
}

// MissionTypeRoster returns the layered roster (built-in -> org -> project;
// later layers win).
func MissionTypeRoster(repoRoot string) map[string]*MissionType {
	out := map[string]*MissionType{}
	bi := BuiltIn()
	for _, rel := range bi.Walk("missions/mission_types", ".yaml", false) {
		b, err := bi.ReadFile(rel)
		if err != nil {
			continue
		}
		stem := strings.TrimSuffix(path.Base(rel), ".yaml")
		if mt, err := loadMissionTypeFile(b, stem, LayerBuiltIn, repoRoot); err == nil {
			out[mt.ID] = mt
		}
	}
	if repoRoot == "" {
		return out
	}
	layer := func(dir, name string) {
		m, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
		sort.Strings(m)
		for _, f := range m {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			stem := strings.TrimSuffix(filepath.Base(f), ".yaml")
			if mt, err := loadMissionTypeFile(b, stem, name, repoRoot); err == nil {
				out[mt.ID] = mt
			}
		}
	}
	orgs := OrgRoots(repoRoot)
	for i := len(orgs) - 1; i >= 0; i-- { // earliest org root wins
		layer(filepath.Join(orgs[i], "mission_types"), LayerOrg)
	}
	layer(filepath.Join(repoRoot, filepath.FromSlash(ProjectMissionTypesDir)), LayerProject)
	// extends fallback for an empty own sequence
	for _, mt := range out {
		if len(mt.ActionSequence) == 0 && mt.Extends != nil {
			if p := out[*mt.Extends]; p != nil {
				mt.ActionSequence = append([]string(nil), p.ActionSequence...)
			}
		}
	}
	return out
}

// ActivatedMissionTypes returns the sorted, deduplicated activated mission
// type ids (upstream existing_mission_types).
func ActivatedMissionTypes(repoRoot string) ([]string, error) {
	act, err := LoadActivation(repoRoot)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, m := range act.MissionTypes {
		set[m] = true
	}
	return sortedKeys(set), nil
}

// UnknownMissionTypeError is upstream UnknownMissionTypeError.
type UnknownMissionTypeError struct {
	ID         string
	Registered []string
}

func (e *UnknownMissionTypeError) Error() string {
	for _, r := range e.Registered {
		if r == e.ID {
			msg := fmt.Sprintf("Mission type %s is activated but has no loadable profile.", pyRepr(e.ID))
			var others []string
			for _, o := range e.Registered {
				if o != e.ID {
					others = append(others, o)
				}
			}
			if len(others) > 0 {
				msg += " Other activated mission types: " + strings.Join(others, ", ") + "."
			}
			return msg
		}
	}
	if len(e.Registered) > 0 {
		return fmt.Sprintf("Unknown mission type %s. Registered types: %s.", pyRepr(e.ID), strings.Join(e.Registered, ", "))
	}
	return fmt.Sprintf("Unknown mission type %s. No registered mission types are available.", pyRepr(e.ID))
}
