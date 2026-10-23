package charter

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// Python value semantics for the DRG extractor's untyped YAML reads: the
// extractor applies dict.get / iteration / f-strings / re.match to whatever
// ruamel loaded, so malformed artifacts crash with Python's TypeError,
// AttributeError or a pydantic ValidationError (uncaught: a traceback).

// site is the upstream call chain (below regenerate_graph and the overlay
// frames) down to, and including, the current call line.
type site []fr

func (s site) at(name string, line int) site {
	return append(append(site{}, s...), fr{Name: name, Line: line})
}

func (s site) crash(typ, msg string, tail ...fr) *RegenCrash {
	return graphCrash(typ, msg, append(append([]fr{}, s...), tail...)...)
}

var (
	// frNormalize is artifact_to_urn("directive", raw) reaching re.match
	// with a non-str raw id.
	frNormalize = []fr{{Name: "artifact_to_urn", Line: 60}, {Name: "directive_to_urn", Line: 51},
		{Name: "normalize_directive_id", Line: 34}, {Name: "match", Line: 167}}
	// frModelInit is pydantic BaseModel.__init__.
	frModelInit = fr{Name: "__init__", Line: 280}
	// frEnsureNode is _ensure_node constructing a DRGNode.
	frEnsureNode = fr{Name: "_ensure_node", Line: 687}
)

// pyTpName is type(v).__name__ as CPython's error messages print it
// (tp_name: "datetime.date" for dates).
func pyTpName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64, *big.Int:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.Object:
		return "dict"
	case pyyaml.Date:
		return "datetime.date"
	case pyyaml.Timestamp:
		return "datetime.datetime"
	case []byte:
		return "bytes"
	case pyyaml.Set:
		return "set"
	}
	return pydantic.TypeName(v)
}

// pyStrAny is str(v) (the extractor's f-string formatting).
func pyStrAny(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case *big.Int:
		return t.String()
	case float64:
		return pyjson.Repr(t)
	case pyyaml.Date:
		return t.ISO()
	case pyyaml.Timestamp:
		return t.Repr
	case pyyaml.Set:
		if len(t) == 0 {
			return "set()"
		}
		return "{" + strings.Join(func() []string {
			out := make([]string, len(t))
			for i, s := range t {
				out[i] = pyjson.ReprString(s)
			}
			return out
		}(), ", ") + "}"
	}
	return pydantic.Repr(v)
}

// pyTruthyAny is bool(v).
func pyTruthyAny(v any) bool {
	switch t := v.(type) {
	case *big.Int:
		return t.Sign() != 0
	case pyyaml.Date, pyyaml.Timestamp:
		return true
	case []byte:
		return len(t) > 0
	case pyyaml.Set:
		return len(t) > 0
	}
	return pyTruthy(v)
}

func pyUnhashable(v any) bool {
	switch v.(type) {
	case []any, *jsonx.Object, pyyaml.Set:
		return true
	}
	return false
}

// pyIterOr is iter(v or []): the items, or the TypeError message.
func pyIterOr(v any) ([]any, string) {
	if !pyTruthyAny(v) {
		return nil, ""
	}
	switch t := v.(type) {
	case []any:
		return t, ""
	case *jsonx.Object:
		out := make([]any, 0, t.Len())
		for _, k := range t.Keys() {
			out = append(out, k)
		}
		return out, ""
	case string:
		out := []any{}
		for _, r := range t {
			out = append(out, string(r))
		}
		return out, ""
	case []byte:
		out := []any{}
		for _, b := range t {
			out = append(out, int64(b))
		}
		return out, ""
	case pyyaml.Set:
		out := []any{}
		for _, s := range t {
			out = append(out, s)
		}
		return out, ""
	}
	return nil, fmt.Sprintf("'%s' object is not iterable", pyTpName(v))
}

// iterGet is `for x in data.get(key, []) or []` at s.
func iterGet(s site, data *jsonx.Object, key string) ([]any, error) {
	v, _ := data.Get(key)
	items, msg := pyIterOr(v)
	if msg != "" {
		return nil, s.crash("TypeError", msg)
	}
	return items, nil
}

// asDict is the receiver of a .get call at s: AttributeError unless v is a
// dict.
func asDict(s site, v any) (*jsonx.Object, error) {
	if o, ok := v.(*jsonx.Object); ok && o != nil {
		return o, nil
	}
	return nil, s.crash("AttributeError", fmt.Sprintf("'%s' object has no attribute 'get'", pyTpName(v)))
}

// dget is d.get(key, def).
func dget(d *jsonx.Object, key string, def any) any {
	if v, ok := d.Get(key); ok {
		return v
	}
	return def
}

// pyURN is artifact_to_urn(kind, raw) called at s.
func pyURN(s site, kind string, raw any) (string, error) {
	if kind == "directive" {
		str, ok := raw.(string)
		if !ok {
			return "", s.crash("TypeError", fmt.Sprintf("expected string or bytes-like object, got '%s'", pyTpName(raw)), frNormalize...)
		}
		return ArtifactToURN(kind, str), nil
	}
	return kind + ":" + pyStrAny(raw), nil
}

// kindForType is _kind_for_type(ref_type) called at s ("" = None).
func kindForType(s site, refType any) (string, error) {
	if pyUnhashable(refType) {
		return "", s.crash("TypeError", fmt.Sprintf("unhashable type: '%s'", pyTpName(refType)), fr{Name: "_kind_for_type", Line: 722})
	}
	if str, ok := refType.(string); ok && graphNodeKinds[str] {
		return str, nil
	}
	return "", nil
}

// enumRepr is repr() of a NodeKind / Relation member.
func enumRepr(class, value string) string {
	return "<" + class + "." + strings.ToUpper(value) + ": " + pyjson.ReprString(value) + ">"
}

// kwargsRepr renders the kwargs dict pydantic reports as input_value.
type kwargsRepr struct{ s string }

func (k kwargsRepr) PyRepr() string     { return k.s }
func (k kwargsRepr) PyTypeName() string { return "dict" }

func kwargs(pairs ...string) kwargsRepr {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pyjson.ReprString(pairs[i])+": "+pairs[i+1])
	}
	return kwargsRepr{"{" + strings.Join(parts, ", ") + "}"}
}

func checkOptStr(ve *pydantic.ValidationError, name string, v any) {
	if v == nil {
		return
	}
	if _, ok := v.(string); !ok {
		ve.Errors = append(ve.Errors, pydantic.ErrorDetail{Loc: []any{name}, Msg: pydantic.MsgStringType, Type: pydantic.TypeStringType, Input: v})
	}
}

func optStrPtr(v any) *string {
	if v == nil {
		return nil
	}
	s := v.(string)
	return &s
}

// validateDRGNode is DRGNode(urn=urn, kind=kind, label=label) validation.
func validateDRGNode(urn, kind string, label any) *pydantic.ValidationError {
	ve := pydantic.New("DRGNode")
	checkOptStr(ve, "label", label)
	if ve.Len() > 0 {
		return ve
	}
	if !reGraphURN.MatchString(urn) {
		in := kwargs("urn", pyjson.ReprString(urn), "kind", enumRepr("NodeKind", kind), "label", pydantic.Repr(label))
		ve.Errors = append(ve.Errors, pydantic.ErrorDetail{Msg: pydantic.ValueErrorMsg(fmt.Sprintf("URN %s does not match pattern %s", pyjson.ReprString(urn), reGraphURN.String())),
			Type: pydantic.TypeValueError, Input: in})
		return ve
	}
	return nil
}

func validationCrash(s site, ve *pydantic.ValidationError, tail ...fr) *RegenCrash {
	return s.crash("pydantic_core._pydantic_core.ValidationError", ve.Error(), tail...)
}

// ensureNode is _ensure_node(nodes, urn, kind, label) called at s. label
// is the raw value (nil = None).
func (ns *nodeSet) ensurePy(s site, urn, kind string, label any) error {
	if n, ok := ns.m[urn]; ok {
		// model_copy(update=...) does not validate.
		if pyTruthyAny(label) && n.Label == nil && n.RawLabel == nil {
			if str, isStr := label.(string); isStr {
				n.Label = &str
			} else {
				n.RawLabel = label
			}
		}
		return nil
	}
	if ve := validateDRGNode(urn, kind, label); ve != nil {
		return validationCrash(s, ve, frEnsureNode, frModelInit)
	}
	var l *string
	if label != nil {
		str := label.(string)
		l = &str
	}
	ns.order = append(ns.order, urn)
	ns.m[urn] = &regenNode{URN: urn, Kind: kind, Label: l}
	return nil
}

// newEdgePy is DRGEdge(source=, target=, relation=[, when=, reason=])
// constructed at s; withMeta says whether when/reason were passed.
func newEdgePy(s site, src, tgt, rel string, withMeta bool, when, reason any) (regenEdge, error) {
	ve := pydantic.New("DRGEdge")
	checkOptStr(ve, "when", when)
	checkOptStr(ve, "reason", reason)
	if ve.Len() == 0 {
		for _, f := range [][2]string{{"source", src}, {"target", tgt}} {
			if !reGraphURN.MatchString(f[1]) {
				pairs := []string{"source", pyjson.ReprString(src), "target", pyjson.ReprString(tgt), "relation", enumRepr("Relation", rel)}
				if withMeta {
					pairs = append(pairs, "when", pydantic.Repr(when), "reason", pydantic.Repr(reason))
				}
				ve.Errors = append(ve.Errors, pydantic.ErrorDetail{Msg: pydantic.ValueErrorMsg(fmt.Sprintf("Edge %s %s does not match URN pattern %s", f[0], pyjson.ReprString(f[1]), reGraphURN.String())),
					Type: pydantic.TypeValueError, Input: kwargs(pairs...)})
				break
			}
		}
	}
	if ve.Len() > 0 {
		return regenEdge{}, validationCrash(s, ve, frModelInit)
	}
	return regenEdge{src, tgt, rel, optStrPtr(when), optStrPtr(reason)}, nil
}
