package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// ManifestError is an uncaught upstream exception from the expected-artifacts
// loader (MalformedManifestError / ManifestSchemaError): Type is the
// qualified exception class, Error() its str().
type ManifestLoadError struct {
	Type, Msg string
}

func (e *ManifestLoadError) Error() string { return e.Msg }

var artifactClasses = map[string]bool{"input": true, "workflow": true, "output": true, "evidence": true, "policy": true, "runtime": true}

// manifestDump is ExpectedArtifactManifest.model_validate(raw).model_dump():
// the validated manifest in model field order with defaults filled.
func manifestDump(raw *jsonx.Object, title string) (*jsonx.Object, *pdValidator) {
	pv := &pdValidator{}
	known := map[string]bool{"schema_version": true, "mission_type": true, "manifest_version": true, "required_always": true, "required_by_step": true, "optional_always": true}
	out := jsonx.NewObject()
	str := func(k, def string, required bool) {
		v, ok := raw.Get(k)
		if !ok {
			if required {
				pv.add([]any{k}, "Field required", "missing", raw)
				return
			}
			out.Set(k, def)
			return
		}
		s, isS := v.(string)
		if !isS {
			pv.add([]any{k}, "Input should be a valid string", "string_type", v)
			return
		}
		out.Set(k, s)
	}
	specList := func(loc []any, v any) []any {
		l, ok := v.([]any)
		if !ok {
			pv.add(loc, "Input should be a valid list", "list_type", v)
			return nil
		}
		res := []any{}
		for i, item := range l {
			il := with(loc, i)
			o, ok := item.(*jsonx.Object)
			if !ok || o == nil {
				pv.add(il, "Input should be a valid dictionary or instance of ExpectedArtifactSpec", "model_type", item)
				continue
			}
			spec := jsonx.NewObject()
			for _, k := range []string{"artifact_key", "artifact_class", "path_pattern"} {
				x, ok := o.Get(k)
				if !ok {
					pv.add(with(il, k), "Field required", "missing", o)
					continue
				}
				s, isS := x.(string)
				switch {
				case k == "artifact_class" && (!isS || !artifactClasses[s]):
					pv.add(with(il, k), "Input should be 'input', 'workflow', 'output', 'evidence', 'policy' or 'runtime'", "enum", x)
				case !isS:
					pv.add(with(il, k), "Input should be a valid string", "string_type", x)
				case s == "":
					pv.add(with(il, k), "String should have at least 1 character", "string_too_short", x)
				default:
					spec.Set(k, s)
				}
			}
			blocking := false
			if b, ok := o.Get("blocking"); ok {
				blocking = pv.boolean(with(il, "blocking"), b)
			}
			spec.Set("blocking", blocking)
			for _, kv := range o.Pairs() {
				switch kv.Key {
				case "artifact_key", "artifact_class", "path_pattern", "blocking":
				default:
					pv.add(with(il, kv.Key), "Extra inputs are not permitted", "extra_forbidden", kv.Value)
				}
			}
			res = append(res, spec)
		}
		return res
	}
	str("schema_version", "1.0", false)
	str("mission_type", "", true)
	str("manifest_version", "1", false)
	if v, ok := raw.Get("required_always"); ok {
		out.Set("required_always", specList([]any{"required_always"}, v))
	} else {
		out.Set("required_always", []any{})
	}
	byStep := jsonx.NewObject()
	if v, ok := raw.Get("required_by_step"); ok {
		o, isO := v.(*jsonx.Object)
		if !isO || o == nil {
			pv.add([]any{"required_by_step"}, "Input should be a valid dictionary", "dict_type", v)
		} else {
			for _, kv := range o.Pairs() {
				byStep.Set(kv.Key, specList([]any{"required_by_step", kv.Key}, kv.Value))
			}
		}
	}
	out.Set("required_by_step", byStep)
	if v, ok := raw.Get("optional_always"); ok {
		out.Set("optional_always", specList([]any{"optional_always"}, v))
	} else {
		out.Set("optional_always", []any{})
	}
	for _, kv := range raw.Pairs() {
		if !known[kv.Key] {
			pv.add([]any{kv.Key}, "Extra inputs are not permitted", "extra_forbidden", kv.Value)
		}
	}
	pv.title = title
	return out, pv
}

func readManifestMapping(path string) (*jsonx.Object, bool, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, true, &ManifestLoadError{"charter.offering.missions.repository.MalformedManifestError", fmt.Sprintf("Malformed expected-artifacts manifest at %s: %v", path, err)}
	}
	text, derr := pyjson.DecodeUTF8(b)
	if derr != nil {
		return nil, true, &ManifestLoadError{"charter.offering.missions.repository.MalformedManifestError", fmt.Sprintf("Malformed expected-artifacts manifest at %s: %s", path, derr.Error())}
	}
	v, err := yamlx.Parse([]byte(text))
	if err != nil {
		return nil, true, &ManifestLoadError{"charter.offering.missions.repository.MalformedManifestError", fmt.Sprintf("Malformed expected-artifacts manifest at %s: %s", path, err.Error())}
	}
	o, ok := v.(*jsonx.Object)
	if !ok || o == nil {
		return nil, true, &ManifestLoadError{"charter.offering.missions.repository.MalformedManifestError", fmt.Sprintf("Malformed expected-artifacts manifest at %s: expected a YAML mapping, got %s", path, pyjson.TypeName(v))}
	}
	return o, true, nil
}

// LoadExpectedArtifacts is charter.activation.manifest_loader.load_manifest
// (model_dump()): the last configured org pack carrying
// missions/<type>/expected-artifacts.yaml replaces the built-in manifest
// wholesale; nil when no tier has one.
func LoadExpectedArtifacts(root, missionType string) (*jsonx.Object, error) {
	roots := charter.ResolvePackChain(root)
	var org *jsonx.Object
	for _, r := range roots {
		o, present, err := readManifestMapping(filepath.Join(r, "missions", missionType, "expected-artifacts.yaml"))
		if err != nil {
			return nil, err
		}
		if present {
			org = o
		}
	}
	if org != nil {
		dump, pv := manifestDump(org, "ExpectedArtifactManifest")
		if len(pv.errs) > 0 {
			origin := fmt.Sprintf("org-tier expected-artifacts.yaml for mission type %s (no single source file path available; checked org roots: %s)", pyjson.ReprString(missionType), strings.Join(roots, ", "))
			return nil, &ManifestLoadError{"charter.activation.manifest_loader.ManifestSchemaError",
				fmt.Sprintf("expected-artifacts.yaml schema-invalid for mission type %s (%s): %s", pyjson.ReprString(missionType), origin, pv.err().Error())}
		}
		return dump, nil
	}
	ap := "packs/built-in/missions/" + missionType + "/expected-artifacts.yaml"
	b, err := assets.ReadFile(ap)
	if err != nil {
		return nil, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, nil
	}
	o, _ := v.(*jsonx.Object)
	if o == nil {
		return nil, nil
	}
	dump, pv := manifestDump(o, "ExpectedArtifactManifest")
	if len(pv.errs) > 0 {
		return nil, errors.New(pv.err().Error())
	}
	return dump, nil
}
