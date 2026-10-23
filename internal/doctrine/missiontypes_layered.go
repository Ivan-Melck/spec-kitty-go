package doctrine

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports the strict layered mission-type roster
// (charter.offering.missions.mission_type_repository.resolve_layered_mission_types
// over PackContext.from_config): every mission_types/*.yaml in the built-in,
// org and project layers is loaded and validated as a MissionType model, and
// a malformed file fails the whole roster (ValueError / pydantic
// ValidationError, NFR-002/CL-006) instead of being skipped.
//
// Unlike MissionTypeRoster, ActionSequence is the type's own sequence (no
// extends fallback): see ExtendsFallbackSequence.

// MissionTypeLoadError is the ValueError (or pydantic ValidationError /
// UnicodeDecodeError, both ValueError subclasses) a strict roster load
// raises; Error() is str(exc).
type MissionTypeLoadError struct{ Msg string }

func (e *MissionTypeLoadError) Error() string { return e.Msg }

var (
	reMissionTypeIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	validPathKeys           = []string{"data", "deliverables", "documentation", "tests", "workspace"}
)

// LayeredMissionTypes is resolve_layered_mission_types((built-in
// mission_types,), PackContext.from_config(repoRoot)).
func LayeredMissionTypes(repoRoot string) (map[string]*MissionType, error) {
	out := map[string]*MissionType{}
	bi := BuiltIn()
	for _, rel := range bi.Walk("missions/mission_types", ".yaml", false) {
		b, err := bi.ReadFile(rel)
		if err != nil {
			continue
		}
		mt, err := loadLayeredMissionTypeFile(b, bi.Path(rel), strings.TrimSuffix(path.Base(rel), ".yaml"), LayerBuiltIn, repoRoot)
		if err != nil {
			return nil, err
		}
		out[mt.ID] = mt
	}
	org := map[string]*MissionType{}
	for _, p := range OrgPacks(repoRoot) {
		mts, err := scanMissionTypesDir(filepath.Join(p.Root, "mission_types"), LayerOrg, repoRoot)
		if err != nil {
			return nil, err
		}
		for _, mt := range mts {
			if _, ok := org[mt.ID]; !ok {
				org[mt.ID] = mt
			}
		}
	}
	for id, mt := range org {
		out[id] = mt
	}
	mts, err := scanMissionTypesDir(filepath.Join(repoRoot, filepath.FromSlash(ProjectMissionTypesDir)), LayerProject, repoRoot)
	if err != nil {
		return nil, err
	}
	for _, mt := range mts {
		out[mt.ID] = mt
	}
	return out, nil
}

// ExtendsFallbackSequence is _resolve_with_extends_fallback: the own
// action sequence, or the extends parent's when the own one is empty.
func ExtendsFallbackSequence(mt *MissionType, roster map[string]*MissionType) []string {
	seq := append([]string(nil), mt.ActionSequence...)
	if len(seq) == 0 && mt.Extends != nil {
		if p := roster[*mt.Extends]; p != nil {
			seq = append([]string(nil), p.ActionSequence...)
		}
	}
	return seq
}

// MissionTypeSourceLayer is resolve_action_sequence_layer: "project" when
// the project layer has <id>.yaml, "org" when an org pack does, else
// "built-in".
func MissionTypeSourceLayer(repoRoot, id string) string {
	if isFile(filepath.Join(repoRoot, filepath.FromSlash(ProjectMissionTypesDir), id+".yaml")) {
		return "project"
	}
	for _, p := range OrgPacks(repoRoot) {
		if isFile(filepath.Join(p.Root, "mission_types", id+".yaml")) {
			return "org"
		}
	}
	return "built-in"
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// EmptyActionSequenceMessage is str(MissionTypeEmptyActionSequenceError).
func EmptyActionSequenceMessage(id, layer string) string {
	return fmt.Sprintf("mission type `%s` resolved from layer `%s` has an empty action sequence.", id, layer)
}

// scanMissionTypesDir is scan_mission_types_dir.
func scanMissionTypesDir(dir, layer, repoRoot string) ([]*MissionType, error) {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, &MissionTypeLoadError{fmt.Sprintf("mission-type directory exists but cannot be read: %s: %s", dir, pyOSError(err, dir))}
	}
	var files []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".yaml") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	var out []*MissionType
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, &MissionTypeLoadError{pyOSError(err, f)}
		}
		mt, err := loadLayeredMissionTypeFile(b, f, strings.TrimSuffix(filepath.Base(f), ".yaml"), layer, repoRoot)
		if err != nil {
			return nil, err
		}
		out = append(out, mt)
	}
	return out, nil
}

// pyOSError is str(OSError) for a failed listing / read.
func pyOSError(err error, p string) string {
	if os.IsPermission(err) {
		return fmt.Sprintf("[Errno 13] Permission denied: %s", pyRepr(p))
	}
	if os.IsNotExist(err) {
		return fmt.Sprintf("[Errno 2] No such file or directory: %s", pyRepr(p))
	}
	return err.Error()
}

// loadLayeredMissionTypeFile is _load_layered_mission_type_file.
func loadLayeredMissionTypeFile(b []byte, file, stem, layer, repoRoot string) (*MissionType, error) {
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, &MissionTypeLoadError{err.Error()}
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	raw, err := pyyaml.LoadRuamelPy(text, pyyaml.RuamelSafe)
	if err != nil {
		return nil, &MissionTypeLoadError{fmt.Sprintf("Malformed YAML in mission-type file %s: %s", file, err.Error())}
	}
	o, ok := raw.(*jsonx.Object)
	if !ok || o == nil {
		return nil, &MissionTypeLoadError{fmt.Sprintf("Expected a YAML mapping in %s; got %s", file, pydantic.TypeName(raw))}
	}
	// _inject_projected_fields: a non-empty step projection wins over the
	// authored action_sequence.
	payload := o.Clone()
	if seq := projectSequence(MissionSteps(repoRoot, stem)); len(seq) > 0 {
		l := make([]any, len(seq))
		for i, s := range seq {
			l[i] = s
		}
		payload.Set("action_sequence", l)
	} else {
		v, _ := o.Get("action_sequence")
		payload.Set("action_sequence", v)
	}
	mt, err := validateMissionTypeModel(payload, layer)
	if err != nil {
		return nil, err
	}
	if mt.ID != stem {
		return nil, &MissionTypeLoadError{fmt.Sprintf("MissionType id %s in %s does not match filename stem %s. Rename the file or correct the id field.",
			pyRepr(mt.ID), filepath.Base(file), pyRepr(stem))}
	}
	return mt, nil
}

// validateMissionTypeModel is MissionType.model_validate(payload).
func validateMissionTypeModel(p *jsonx.Object, layer string) (*MissionType, error) {
	ve := pydantic.New("MissionType")
	add := func(loc []any, typ, msg string, in any) {
		ve.Errors = append(ve.Errors, pydantic.ErrorDetail{Loc: loc, Type: typ, Msg: msg, Input: in})
	}
	mt := &MissionType{SourceLayer: layer}
	if v, ok := p.Get("schema_version"); ok {
		pydInt(v, func(typ, msg string) { add([]any{"schema_version"}, typ, msg, v) })
	}
	str := func(name string, required, nullable bool) (string, bool) {
		v, ok := p.Get(name)
		if !ok {
			if required {
				add([]any{name}, pydantic.TypeMissing, pydantic.MsgMissing, p)
			}
			return "", false
		}
		if v == nil && nullable {
			return "", false
		}
		s, isStr := v.(string)
		if !isStr {
			add([]any{name}, pydantic.TypeStringType, pydantic.MsgStringType, v)
			return "", false
		}
		return s, true
	}
	if id, ok := str("id", true, false); ok {
		if !reMissionTypeIdentifier.MatchString(id) {
			add([]any{"id"}, pydantic.TypeValueError, pydantic.ValueErrorMsg(fmt.Sprintf("MissionType id %s does not match IDENTIFIER_PATTERN %s", pyRepr(id), pyRepr(reMissionTypeIdentifier.String()))), id)
		}
		mt.ID = id
	}
	if dn, ok := str("display_name", true, false); ok {
		mt.DisplayName = dn
	}
	if ex, ok := str("extends", false, true); ok {
		mt.Extends = &ex
	}
	var seq []string
	hasSeq := false
	if v, ok := p.Get("action_sequence"); ok && v != nil {
		if l, isList := v.([]any); isList {
			hasSeq = true
			for i, x := range l {
				s, isStr := x.(string)
				if !isStr {
					add([]any{"action_sequence", i}, pydantic.TypeStringType, pydantic.MsgStringType, x)
					continue
				}
				seq = append(seq, s)
			}
		} else {
			add([]any{"action_sequence"}, pydantic.TypeListType, pydantic.MsgListType, v)
		}
	}
	var pathKeys []string
	if v, ok := p.Get("path_conventions"); ok && v != nil {
		if d, isDict := v.(*jsonx.Object); isDict {
			for _, kv := range d.Pairs() {
				pathKeys = append(pathKeys, kv.Key)
				if _, isStr := kv.Value.(string); !isStr {
					add([]any{"path_conventions", kv.Key}, pydantic.TypeStringType, pydantic.MsgStringType, kv.Value)
				}
			}
		} else {
			add([]any{"path_conventions"}, pydantic.TypeDictType, pydantic.MsgDictType, v)
		}
	}
	known := map[string]bool{"schema_version": true, "id": true, "display_name": true, "extends": true, "action_sequence": true, "path_conventions": true}
	for _, kv := range p.Pairs() {
		if !known[kv.Key] {
			add([]any{kv.Key}, pydantic.TypeExtraForbidden, pydantic.MsgExtraForbidden, kv.Value)
		}
	}
	if ve.Len() == 0 {
		// model validators (mode="after"), in declaration order.
		msg := ""
		if hasSeq {
			if len(seq) == 0 {
				msg = "action_sequence must be non-empty"
			} else {
				seen := map[string]bool{}
				for _, s := range seq {
					if seen[s] {
						msg = "action_sequence must contain unique step IDs"
						break
					}
					seen[s] = true
				}
			}
		}
		if msg == "" && len(pathKeys) > 0 {
			valid := map[string]bool{}
			for _, k := range validPathKeys {
				valid[k] = true
			}
			var unknown []string
			seen := map[string]bool{}
			for _, k := range pathKeys {
				if !valid[k] && !seen[k] {
					seen[k] = true
					unknown = append(unknown, k)
				}
			}
			sort.Strings(unknown)
			if len(unknown) > 0 {
				msg = fmt.Sprintf("Unknown path-convention keys: %s. Valid keys: %s", pyjson.ReprStrings(unknown), pyjson.ReprStrings(validPathKeys))
			}
		}
		if msg != "" {
			add(nil, pydantic.TypeValueError, pydantic.ValueErrorMsg(msg), p)
		}
	}
	if ve.Len() > 0 {
		return nil, &MissionTypeLoadError{ve.Error()}
	}
	mt.ActionSequence = seq
	return mt, nil
}

// BuiltInMissionTypesDir names the built-in layer for ScanMissionTypeIDs.
const BuiltInMissionTypesDir = ""

// ScanMissionTypeIDs is [mt.id for mt in scan_mission_types_dir(dir)]
// (pack_context=None): the ids of a layer directory, failing on a
// malformed file. BuiltInMissionTypesDir scans the built-in layer.
func ScanMissionTypeIDs(dir, repoRoot string) ([]string, error) {
	var ids []string
	if dir == BuiltInMissionTypesDir {
		bi := BuiltIn()
		for _, rel := range bi.Walk("missions/mission_types", ".yaml", false) {
			b, err := bi.ReadFile(rel)
			if err != nil {
				continue
			}
			mt, err := loadLayeredMissionTypeFile(b, bi.Path(rel), strings.TrimSuffix(path.Base(rel), ".yaml"), LayerBuiltIn, "")
			if err != nil {
				return nil, err
			}
			ids = append(ids, mt.ID)
		}
		return ids, nil
	}
	mts, err := scanMissionTypesDir(dir, LayerProject, "")
	if err != nil {
		return nil, err
	}
	for _, mt := range mts {
		ids = append(ids, mt.ID)
	}
	return ids, nil
}

// pydInt is pydantic's lax int validation of v (reporting via fail).
func pydInt(v any, fail func(typ, msg string)) {
	switch t := v.(type) {
	case int, int64, *big.Int, bool:
		return
	case float64:
		if t != math.Trunc(t) || math.IsInf(t, 0) || math.IsNaN(t) {
			fail(pydantic.TypeIntFromFloat, pydantic.MsgIntFromFloat)
		}
		return
	case string:
		if _, err := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(t, "_", "")), 10, 64); err != nil {
			fail(pydantic.TypeIntParsing, pydantic.MsgIntParsing)
		}
		return
	}
	fail(pydantic.TypeIntType, pydantic.MsgIntType)
}
