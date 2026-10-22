package charter

import (
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
)

// A small declarative pydantic-v2 validator (lax mode) for the charter
// models whose ValidationError text reaches the CLI (SynthesisManifest,
// ManifestArtifactEntry, ProvenanceEntry): field order, defaults, Literal,
// str length bounds, Optional, list[str], list[Model], bool, extra=forbid.

type pydKind int

const (
	pkStr pydKind = iota
	pkLiteral
	pkBool
	pkStrList
	pkModelList
)

type pydField struct {
	name     string
	kind     pydKind
	required bool
	nullable bool
	minLen   int
	maxLen   int
	literals []string
	model    *pydModel
}

type pydModel struct {
	name        string
	fields      []pydField
	forbidExtra bool
}

type pydValidator struct {
	errs    []pydantic.ErrorDetail
	typeOf  func(any) string
	topName string
}

func (v *pydValidator) add(loc []any, typ, msg string, input any, ctx *jsonx.Object) {
	v.errs = append(v.errs, pydantic.ErrorDetail{Loc: append([]any(nil), loc...), Type: typ, Msg: msg, Input: input, Ctx: ctx, InputType: v.typeOf(input)})
}

func (v *pydValidator) model(m *pydModel, raw any, loc []any) {
	o, ok := raw.(*jsonx.Object)
	if !ok {
		v.add(loc, pydantic.TypeModelType, pydantic.ModelTypeMsg(m.name), raw, jsonx.NewObject("class_name", m.name))
		return
	}
	known := map[string]bool{}
	for _, f := range m.fields {
		known[f.name] = true
		val, present := o.Get(f.name)
		floc := append(append([]any(nil), loc...), f.name)
		if !present {
			if f.required {
				v.add(floc, pydantic.TypeMissing, pydantic.MsgMissing, o, nil)
			}
			continue
		}
		if val == nil && f.nullable {
			continue
		}
		v.value(f, val, floc)
	}
	if m.forbidExtra {
		for _, kv := range o.Pairs() {
			if !known[kv.Key] {
				v.add(append(append([]any(nil), loc...), kv.Key), pydantic.TypeExtraForbidden, pydantic.MsgExtraForbidden, kv.Value, nil)
			}
		}
	}
}

func (v *pydValidator) value(f pydField, val any, loc []any) {
	switch f.kind {
	case pkStr:
		s, ok := val.(string)
		if !ok {
			v.add(loc, pydantic.TypeStringType, pydantic.MsgStringType, val, nil)
			return
		}
		n := len([]rune(s))
		if f.minLen > 0 && n < f.minLen {
			v.add(loc, pydantic.TypeStringTooShort, pydantic.StringTooShortMsg(f.minLen), val, jsonx.NewObject("min_length", f.minLen))
		} else if f.maxLen > 0 && n > f.maxLen {
			v.add(loc, pydantic.TypeStringTooLong, pydantic.StringTooLongMsg(f.maxLen), val, jsonx.NewObject("max_length", f.maxLen))
		}
	case pkLiteral:
		s, ok := val.(string)
		in := false
		for _, l := range f.literals {
			in = in || (ok && s == l)
		}
		if !in {
			v.add(loc, pydantic.TypeLiteralError, pydantic.LiteralMsg(f.literals), val, pydantic.LiteralCtx(f.literals))
		}
	case pkBool:
		switch t := val.(type) {
		case bool:
		case int, int64:
			if n, _ := strconv.Atoi(pydantic.Repr(t)); n != 0 && n != 1 {
				v.add(loc, pydantic.TypeBoolParsing, pydantic.MsgBoolParsing, val, nil)
			}
		case string:
			switch strings.ToLower(t) {
			case "0", "off", "f", "false", "n", "no", "1", "on", "t", "true", "y", "yes":
			default:
				v.add(loc, pydantic.TypeBoolParsing, pydantic.MsgBoolParsing, val, nil)
			}
		default:
			v.add(loc, pydantic.TypeBoolType, pydantic.MsgBoolType, val, nil)
		}
	case pkStrList:
		l, ok := val.([]any)
		if !ok {
			v.add(loc, pydantic.TypeListType, pydantic.MsgListType, val, nil)
			return
		}
		for i, x := range l {
			if _, ok := x.(string); !ok {
				v.add(append(append([]any(nil), loc...), i), pydantic.TypeStringType, pydantic.MsgStringType, x, nil)
			}
		}
	case pkModelList:
		l, ok := val.([]any)
		if !ok {
			v.add(loc, pydantic.TypeListType, pydantic.MsgListType, val, nil)
			return
		}
		for i, x := range l {
			v.model(f.model, x, append(append([]any(nil), loc...), i))
		}
	}
}

// validatePyd validates raw against m; ruamel selects ruamel's loader types
// for input_type. Returns nil when valid.
func validatePyd(m *pydModel, raw any, ruamel bool) *pydantic.ValidationError {
	v := &pydValidator{typeOf: pydantic.TypeName}
	if ruamel {
		v.typeOf = pydantic.RuamelTypeName
	}
	v.model(m, raw, nil)
	if len(v.errs) == 0 {
		return nil
	}
	return pydantic.New(m.name, v.errs...)
}

var manifestEntryModel = &pydModel{"ManifestArtifactEntry", []pydField{
	{name: "kind", kind: pkLiteral, required: true, literals: []string{"directive", "tactic", "styleguide", "procedure", "agent_profile"}},
	{name: "slug", kind: pkStr, required: true},
	{name: "path", kind: pkStr, required: true},
	{name: "provenance_path", kind: pkStr, required: true},
	{name: "content_hash", kind: pkStr, required: true},
}, true}

// synthesisManifestModel is synthesizer.manifest.SynthesisManifest.
var synthesisManifestModel = &pydModel{"SynthesisManifest", []pydField{
	{name: "schema_version", kind: pkLiteral, literals: []string{"2", "3"}},
	{name: "mission_id", kind: pkStr, nullable: true},
	{name: "created_at", kind: pkStr, required: true},
	{name: "run_id", kind: pkStr, required: true},
	{name: "adapter_id", kind: pkStr, required: true},
	{name: "adapter_version", kind: pkStr, required: true},
	{name: "synthesizer_version", kind: pkStr, required: true, minLen: 1},
	{name: "manifest_hash", kind: pkStr, required: true, minLen: 64, maxLen: 64},
	{name: "artifacts", kind: pkModelList, model: manifestEntryModel},
	{name: "built_in_only", kind: pkBool},
	{name: "bundle_content_hash", kind: pkStr, nullable: true},
}, true}
