package charter

import (
	"fmt"
	"strconv"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// pydantic emulation of charter.offering.drg.models.DRGGraph (DRGNode /
// DRGEdge with their URN model validators).

var drgNodeModel = &sModel{"DRGNode", true, []sField{req("urn", tStr), req("kind", tEnum(NodeKinds...)), optN("label", tStr),
	optN("provenance", tStr), opt("tags", tStrList)}}

var drgEdgeModel = &sModel{"DRGEdge", true, []sField{req("source", tStr), req("target", tStr), req("relation", tEnum(Relations...)),
	optN("when", tStr), optN("reason", tStr), optN("provenance", tStr)}}

// drgGraphErrors validates a parsed graph document; nil when valid.
func drgGraphErrors(v any) []pydErr {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return []pydErr{{"", "Input should be a valid dictionary or instance of DRGGraph", "model_type", v}}
	}
	var errs []pydErr
	validateValue := func(key string, t sType) {
		x, ok := o.Get(key)
		if !ok {
			errs = append(errs, pydErr{key, "Field required", "missing", o})
			return
		}
		validateValueInto(x, t, key, &errs)
	}
	validateValue("schema_version", tPattern(`^1\.0$`))
	validateValue("generated_at", tStr)
	validateValue("generated_by", tStr)
	list := func(key string, m *sModel, post func(*jsonx.Object) string) {
		x, ok := o.Get(key)
		if !ok {
			errs = append(errs, pydErr{key, "Field required", "missing", o})
			return
		}
		l, ok := x.([]any)
		if !ok {
			errs = append(errs, pydErr{key, "Input should be a valid list", "list_type", x})
			return
		}
		for i, item := range l {
			loc := key + "." + strconv.Itoa(i)
			io, ok := item.(*jsonx.Object)
			if !ok {
				errs = append(errs, pydErr{loc, "Input should be a valid dictionary or instance of " + m.name, "model_type", item})
				continue
			}
			before := len(errs)
			validateModel(io, m, loc, &errs)
			if len(errs) == before {
				if msg := post(io); msg != "" {
					errs = append(errs, pydErr{loc, "Value error, " + msg, "value_error", io})
				}
			}
		}
	}
	list("nodes", drgNodeModel, func(n *jsonx.Object) string {
		urn := n.GetString("urn")
		if !IsValidURN(urn) {
			return fmt.Sprintf("URN %s does not match pattern %s", dct.PyRepr(urn), urnRe.String())
		}
		if p := strings.SplitN(urn, ":", 2)[0]; p != n.GetString("kind") {
			return fmt.Sprintf("URN prefix %s does not match kind %s", dct.PyRepr(p), dct.PyRepr(n.GetString("kind")))
		}
		return ""
	})
	list("edges", drgEdgeModel, func(e *jsonx.Object) string {
		for _, f := range []string{"source", "target"} {
			if v := e.GetString(f); !IsValidURN(v) {
				return fmt.Sprintf("Edge %s %s does not match URN pattern %s", f, dct.PyRepr(v), urnRe.String())
			}
		}
		return ""
	})
	for _, kv := range o.Pairs() {
		switch kv.Key {
		case "schema_version", "generated_at", "generated_by", "nodes", "edges":
		default:
			errs = append(errs, pydErr{kv.Key, "Extra inputs are not permitted", "extra_forbidden", kv.Value})
		}
	}
	return errs
}

// validateValueInto exposes validateValue for one top-level field.
func validateValueInto(v any, t sType, loc string, errs *[]pydErr) { validateValue(v, t, loc, errs) }
