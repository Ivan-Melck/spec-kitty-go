package charter

import (
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// modelField is one pydantic model field: name, default ("list", "dict",
// "agent" for ActorRole.AGENT, "" = required/None) and the nested model of
// list items / the value.
type modelField struct {
	name, def string
	nested    []modelField
}

func mf(name, def string, nested ...modelField) modelField {
	return modelField{name: name, def: def, nested: nested}
}

var (
	directiveRefModel = []modelField{mf("type", ""), mf("id", ""), mf("when", ""), mf("reason", "")}
	tacticRefModel    = []modelField{mf("name", ""), mf("type", ""), mf("id", ""), mf("when", "")}
	procedureRefModel = []modelField{mf("type", ""), mf("id", ""), mf("reason", "")}

	artifactModels = map[dct.Kind][]modelField{
		dct.Directive: {mf("id", ""), mf("schema_version", ""), mf("title", ""), mf("intent", ""), mf("enforcement", ""),
			mf("scope", ""), mf("procedures", "list"), mf("integrity_rules", "list"), mf("validation_criteria", "list"),
			mf("explicit_allowances", "list"), {name: "references", def: "list", nested: directiveRefModel}},
		dct.Tactic: {mf("id", ""), mf("schema_version", ""), mf("name", ""), mf("purpose", ""),
			{name: "steps", def: "", nested: []modelField{mf("title", ""), mf("description", ""), mf("examples", "list"),
				{name: "references", def: "list", nested: tacticRefModel}}},
			mf("failure_modes", "list"), mf("applies_to_languages", "list"), {name: "references", def: "list", nested: tacticRefModel}, mf("notes", "")},
		dct.Styleguide: {mf("id", ""), mf("schema_version", ""), mf("title", ""), mf("scope", ""), mf("principles", ""),
			{name: "patterns", def: "list", nested: []modelField{mf("name", ""), mf("description", ""), mf("bad_example", ""), mf("good_example", "")}},
			{name: "anti_patterns", def: "list", nested: []modelField{mf("name", ""), mf("description", ""), mf("bad_example", ""), mf("good_example", "")}},
			mf("tooling", "dict"), mf("quality_test", ""), mf("applies_to_languages", "list"), mf("references", "list"), mf("structural_lint_config", "")},
		dct.Toolguide: {mf("id", ""), mf("schema_version", ""), mf("tool", ""), mf("title", ""), mf("guide_path", ""), mf("summary", ""),
			mf("commands", "list"), mf("applies_to_languages", "list"), mf("last_updated", ""), mf("references", "list")},
		dct.Procedure: {mf("schema_version", ""), mf("id", ""), mf("name", ""), mf("purpose", ""), mf("entry_condition", ""), mf("exit_condition", ""),
			{name: "steps", def: "", nested: []modelField{mf("title", ""), mf("description", ""), mf("actor", "agent"), mf("on_success", ""), mf("on_failure", "")}},
			{name: "anti_patterns", def: "list", nested: []modelField{mf("name", ""), mf("description", "")}},
			mf("applies_to_languages", "list"), mf("notes", ""), {name: "references", def: "list", nested: procedureRefModel}},
	}
)

// dumpModel renders data like model_dump(by_alias, mode="json",
// exclude_none=True) of the pydantic model described by fields.
func dumpModel(data *jsonx.Object, fields []modelField) *jsonx.Object {
	out := &jsonx.Object{}
	for _, f := range fields {
		v, ok := data.Get(f.name)
		if !ok || v == nil {
			switch f.def {
			case "list":
				out.Set(f.name, []any{})
			case "dict":
				out.Set(f.name, &jsonx.Object{})
			case "agent":
				out.Set(f.name, "agent")
			}
			continue
		}
		if len(f.nested) > 0 {
			if l, isList := v.([]any); isList {
				nl := make([]any, 0, len(l))
				for _, x := range l {
					if o, isObj := x.(*jsonx.Object); isObj {
						nl = append(nl, dumpModel(o, f.nested))
					} else {
						nl = append(nl, x)
					}
				}
				out.Set(f.name, nl)
				continue
			}
		}
		out.Set(f.name, dropNone(v))
	}
	return out
}

func dropNone(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		o := &jsonx.Object{}
		for _, kv := range t.Pairs() {
			if kv.Value != nil {
				o.Set(kv.Key, dropNone(kv.Value))
			}
		}
		return o
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = dropNone(x)
		}
		return l
	}
	return v
}

// ArtifactBody is _jsonable_artifact_value(artifact) for the typed kinds
// (raw data for kinds without a registered model).
func ArtifactBody(k dct.Kind, data *jsonx.Object) *jsonx.Object {
	if fields, ok := artifactModels[k]; ok {
		return dumpModel(data, fields)
	}
	return dropNone(data).(*jsonx.Object)
}
