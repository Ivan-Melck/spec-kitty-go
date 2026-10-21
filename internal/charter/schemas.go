package charter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file is a small pydantic-v2 emulation for the doctrine artifact
// schemas (charter new / charter validate): model-order field checks,
// extra='forbid', and pydantic's error text.

type sType struct {
	kind     string // str | int | bool | list | model | enum | dict_str | any | union_excluding
	nullable bool
	pattern  string
	minLen   int
	item     *sType
	model    *sModel
	enum     []string
	ge, le   *int
	gt       *int
}

type sField struct {
	name     string // alias as written in YAML
	typ      sType
	required bool
}

type sModel struct {
	name   string
	forbid bool
	fields []sField
}

func ip(i int) *int { return &i }

var (
	tStr     = sType{kind: "str"}
	tStrNull = sType{kind: "str", nullable: true}
	tStrList = sType{kind: "list", item: &tStr}
	tBool    = sType{kind: "bool"}
)

func tPattern(p string) sType       { return sType{kind: "str", pattern: p} }
func tMinStr(n int) sType           { return sType{kind: "str", minLen: n} }
func tList(it sType, n int) sType   { return sType{kind: "list", item: &it, minLen: n} }
func tModel(m *sModel) sType        { return sType{kind: "model", model: m} }
func tEnum(vals ...string) sType    { return sType{kind: "enum", enum: vals} }
func req(n string, t sType) sField  { return sField{n, t, true} }
func opt(n string, t sType) sField  { return sField{n, t, false} }
func optN(n string, t sType) sField { t.nullable = true; return sField{n, t, false} }

var artifactKindValues = []string{"directive", "tactic", "styleguide", "toolguide", "paradigm", "procedure", "agent_profile",
	"mission_step_contract", "template", "asset", "glossary_pack", "anti_pattern", "skill"}

var schemaModels = func() map[string]*sModel {
	dirRef := &sModel{"DirectiveReference", true, []sField{req("type", tEnum(artifactKindValues...)), req("id", tStr), optN("when", tStr), optN("reason", tStr)}}
	tacRef := &sModel{"TacticReference", true, []sField{req("name", tStr), req("type", tEnum(artifactKindValues...)), req("id", tPattern(`^[A-Za-z][A-Za-z0-9_-]*$`)), req("when", tStr)}}
	tacStep := &sModel{"TacticStep", true, []sField{req("title", tStr), optN("description", tStr), opt("examples", tStrList), opt("references", tList(tModel(tacRef), 0))}}
	pattern := &sModel{"Pattern", true, []sField{req("name", tStr), req("description", tStr), optN("bad_example", tStr), optN("good_example", tStr)}}
	anti := &sModel{"AntiPattern", true, []sField{req("name", tStr), req("description", tStr), req("bad_example", tStr), req("good_example", tStr)}}
	procAnti := &sModel{"ProcedureAntiPattern", true, []sField{req("name", tMinStr(1)), req("description", tMinStr(1))}}
	procRef := &sModel{"ProcedureReference", true, []sField{req("type", tEnum(artifactKindValues...)), req("id", tStr), optN("reason", tStr)}}
	procStep := &sModel{"ProcedureStep", true, []sField{req("title", tStr), optN("description", tStr), opt("actor", tEnum("human", "agent", "system")), optN("on_success", tStr), optN("on_failure", tStr)}}
	parRef := &sModel{"ParadigmReference", true, []sField{req("type", tEnum(artifactKindValues...)), req("id", tStr)}}
	delegates := &sModel{"DelegatesTo", true, []sField{req("kind", tEnum(artifactKindValues...)), req("candidates", tList(tStr, 1))}}
	stepInput := &sModel{"MissionStepInput", true, []sField{req("flag", tMinStr(1)), req("source", tMinStr(1)), opt("optional", tBool)}}
	mscStep := &sModel{"MissionStepContractStep", true, []sField{req("id", tStr), req("description", tStr), optN("command", tStr),
		opt("inputs", tList(tModel(stepInput), 0)), optN("delegates_to", tModel(delegates)), optN("guidance", tStr)}}
	gate := &sModel{"GateBinding", true, []sField{req("on_transition", tStr), req("handler", tStr), opt("handler_kind", tEnum("mission_step_contract", "asset")),
		req("schema_version", tStr), opt("fail_open", tBool), optN("provenance", tStr)}}
	spec := &sModel{"Specialization", false, []sField{req("primary-focus", tStr), opt("secondary-awareness", tStr), opt("avoidance-boundary", tStr), opt("success-definition", tStr)}}
	collab := &sModel{"CollaborationContract", false, []sField{opt("handoff-to", tStrList), opt("handoff-from", tStrList), opt("works-with", tStrList),
		opt("output-artifacts", tStrList), opt("operating-procedures", tStrList), opt("canonical-verbs", tStrList)}}
	specCtx := &sModel{"SpecializationContext", false, []sField{opt("languages", tStrList), opt("frameworks", tStrList), opt("file-patterns", tStrList),
		opt("domain-keywords", tStrList), opt("writing-style", tStrList), opt("complexity-preference", tStrList)}}
	modeDef := &sModel{"ModeDefault", false, []sField{req("mode", tStr), req("description", tStr), req("use-case", tStr)}}
	dRef := &sModel{"DirectiveRef", false, []sField{req("code", tStr), req("name", tStr), req("rationale", tStr)}}
	aRef := &sModel{"ArtifactRef", false, []sField{req("id", tStr), req("rationale", tStr)}}
	srStep := &sModel{"SelfReviewStep", false, []sField{req("name", tStr), optN("command", tStr), req("gate", tStr)}}
	srProto := &sModel{"SelfReviewProtocol", false, []sField{opt("steps", tList(tModel(srStep), 0))}}
	return map[string]*sModel{
		"directives": {"Directive", true, []sField{req("id", tPattern(`^[A-Z][A-Z0-9_-]*$`)), req("schema_version", tPattern(`^1\.0$`)), req("title", tStr),
			req("intent", tStr), req("enforcement", tEnum("required", "lenient-adherence", "advisory")), optN("scope", tStr),
			opt("procedures", tStrList), opt("integrity_rules", tStrList), opt("validation_criteria", tStrList), opt("explicit_allowances", tStrList),
			opt("references", tList(tModel(dirRef), 0))}},
		"tactics": {"Tactic", true, []sField{req("id", tPattern(`^[a-z][a-z0-9-]*$`)), req("schema_version", tPattern(`^1\.0$`)), req("name", tStr),
			optN("purpose", tStr), req("steps", tList(tModel(tacStep), 1)), opt("failure_modes", tStrList), opt("applies_to_languages", tStrList),
			opt("references", tList(tModel(tacRef), 0)), optN("notes", tStr)}},
		"styleguides": {"Styleguide", true, []sField{req("id", tPattern(`^[a-z][a-z0-9-]*$`)), req("schema_version", tPattern(`^1\.0$`)), req("title", tStr),
			req("scope", tEnum("code", "docs", "architecture", "testing", "operations", "glossary")), req("principles", tList(tStr, 1)),
			opt("patterns", tList(tModel(pattern), 1)), opt("anti_patterns", tList(tModel(anti), 1)), opt("tooling", sType{kind: "dict_str"}),
			optN("quality_test", tStr), opt("applies_to_languages", tStrList), opt("references", tStrList), optN("structural_lint_config", sType{kind: "dict_any"})}},
		"toolguides": {"Toolguide", true, []sField{req("id", tPattern(`^[a-z][a-z0-9-]*$`)), req("schema_version", tPattern(`^1\.0$`)), req("tool", tStr),
			req("title", tStr), req("guide_path", tPattern(`^[^/].*\.md$`)), req("summary", tStr), opt("commands", tStrList),
			opt("applies_to_languages", tStrList), optN("last_updated", tStr), opt("references", tStrList)}},
		"paradigms": {"Paradigm", true, []sField{req("schema_version", tPattern(`^1\.0$`)), req("id", tPattern(`^[a-z][a-z0-9-]*$`)), req("name", tStr),
			req("summary", tStr), opt("directive_refs", tStrList), opt("references", tList(tModel(parRef), 0))}},
		"procedures": {"Procedure", true, []sField{req("schema_version", tPattern(`^1\.0$`)), req("id", tPattern(`^[a-z][a-z0-9-]*$`)), req("name", tStr),
			req("purpose", tStr), req("entry_condition", tStr), req("exit_condition", tStr), req("steps", tList(tModel(procStep), 1)),
			opt("anti_patterns", tList(tModel(procAnti), 0)), opt("applies_to_languages", tStrList), optN("notes", tStr),
			opt("references", tList(tModel(procRef), 0))}},
		"agent_profiles": {"AgentProfile", true, []sField{req("profile-id", tStr), req("name", tStr), opt("description", tStr),
			opt("schema-version", tPattern(`^(1\.0|2)$`)), req("roles", tList(tStr, 1)), optN("avatar-image", tStr), opt("capabilities", tStrList),
			opt("routing-priority", sType{kind: "int", ge: ip(0), le: ip(100)}), opt("max-concurrent-tasks", sType{kind: "int", gt: ip(0)}),
			optN("model", tStr), optN("effort", tStr), req("purpose", tStr), req("specialization", tModel(spec)), opt("collaboration", tModel(collab)),
			opt("mode-defaults", tList(tModel(modeDef), 0)), opt("initialization-declaration", tStr), opt("sentinel", tBool),
			optN("specialization-context", tModel(specCtx)), opt("applies_to_languages", tStrList), opt("directive-references", tList(tModel(dRef), 0)),
			opt("tactic-references", tList(tModel(aRef), 0)), opt("toolguide-references", tList(tModel(aRef), 0)),
			opt("styleguide-references", tList(tModel(aRef), 0)), optN("self-review-protocol", tModel(srProto)), optN("excluding", sType{kind: "any"})}},
		"mission_step_contracts": {"MissionStepContract", true, []sField{req("id", tStr), req("schema_version", tStr), req("action", tStr), req("mission", tStr),
			req("steps", tList(tModel(mscStep), 1)), opt("gates", tList(tModel(gate), 0))}},
		"assets": {"AssetManifest", true, []sField{req("id", tMinStr(1)), req("mime", tMinStr(1)), req("path", tMinStr(1)), optN("title", tStr)}},
	}
}()

type pydErr struct {
	loc, msg, typ string
	input         any
}

func pyTypeOf(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case *jsonx.Object:
		return "dict"
	}
	return "str"
}

// pyValueRepr is Python repr() of a parsed YAML value.
func pyValueRepr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return dct.PyRepr(t)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case float64:
		return jsonx.FormatFloat(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = pyValueRepr(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *jsonx.Object:
		var parts []string
		for _, kv := range t.Pairs() {
			parts = append(parts, dct.PyRepr(kv.Key)+": "+pyValueRepr(kv.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

// truncRepr is pydantic-core's input_value truncation (> 50 chars).
func truncRepr(s string) string {
	if utf8.RuneCountInString(s) <= 50 {
		return s
	}
	r := []rune(s)
	return string(r[:25]) + "..." + string(r[len(r)-24:])
}

func joinLoc(base, part string) string {
	if base == "" {
		return part
	}
	return base + "." + part
}

func enumMsg(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = "'" + v + "'"
	}
	if len(q) == 1 {
		return "Input should be " + q[0]
	}
	return "Input should be " + strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

func validateValue(v any, t sType, loc string, errs *[]pydErr) {
	if v == nil && t.nullable {
		return
	}
	add := func(msg, typ string) { *errs = append(*errs, pydErr{loc, msg, typ, v}) }
	switch t.kind {
	case "any":
		return
	case "str":
		s, ok := v.(string)
		if !ok {
			add("Input should be a valid string", "string_type")
			return
		}
		if t.minLen > 0 && utf8.RuneCountInString(s) < t.minLen {
			add(fmt.Sprintf("String should have at least %d character", t.minLen)+plural(t.minLen), "string_too_short")
			return
		}
		if t.pattern != "" && !regexp.MustCompile(t.pattern).MatchString(s) {
			add("String should match pattern '"+t.pattern+"'", "string_pattern_mismatch")
		}
	case "enum":
		s, ok := v.(string)
		if ok {
			for _, e := range t.enum {
				if e == s {
					return
				}
			}
		}
		add(enumMsg(t.enum), "enum")
	case "bool":
		switch x := v.(type) {
		case bool:
			return
		case int:
			if x == 0 || x == 1 {
				return
			}
		case string:
			switch strings.ToLower(x) {
			case "0", "off", "f", "false", "n", "no", "1", "on", "t", "true", "y", "yes":
				return
			}
			add("Input should be a valid boolean, unable to interpret input", "bool_parsing")
			return
		}
		add("Input should be a valid boolean", "bool_type")
	case "int":
		var n int
		switch x := v.(type) {
		case int:
			n = x
		case bool:
			n = 0
			if x {
				n = 1
			}
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(x))
			if err != nil {
				add("Input should be a valid integer, unable to parse string as an integer", "int_parsing")
				return
			}
			n = i
		default:
			add("Input should be a valid integer", "int_type")
			return
		}
		switch {
		case t.ge != nil && n < *t.ge:
			add(fmt.Sprintf("Input should be greater than or equal to %d", *t.ge), "greater_than_equal")
		case t.le != nil && n > *t.le:
			add(fmt.Sprintf("Input should be less than or equal to %d", *t.le), "less_than_equal")
		case t.gt != nil && n <= *t.gt:
			add(fmt.Sprintf("Input should be greater than %d", *t.gt), "greater_than")
		}
	case "list":
		l, ok := v.([]any)
		if !ok {
			add("Input should be a valid list", "list_type")
			return
		}
		for i, x := range l {
			validateValue(x, *t.item, joinLoc(loc, strconv.Itoa(i)), errs)
		}
		if len(l) < t.minLen {
			add(fmt.Sprintf("List should have at least %d item%s after validation, not %d", t.minLen, plural(t.minLen), len(l)), "too_short")
		}
	case "dict_str", "dict_any":
		o, ok := v.(*jsonx.Object)
		if !ok {
			add("Input should be a valid dictionary", "dict_type")
			return
		}
		if t.kind == "dict_str" {
			for _, kv := range o.Pairs() {
				validateValue(kv.Value, tStr, joinLoc(loc, kv.Key), errs)
			}
		}
	case "model":
		o, ok := v.(*jsonx.Object)
		if !ok {
			add("Input should be a valid dictionary or instance of "+t.model.name, "model_type")
			return
		}
		validateModel(o, t.model, loc, errs)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func validateModel(o *jsonx.Object, m *sModel, loc string, errs *[]pydErr) {
	known := map[string]bool{}
	for _, f := range m.fields {
		known[f.name] = true
		v, ok := o.Get(f.name)
		if !ok {
			if f.required {
				*errs = append(*errs, pydErr{joinLoc(loc, f.name), "Field required", "missing", o})
			}
			continue
		}
		validateValue(v, f.typ, joinLoc(loc, f.name), errs)
	}
	if m.forbid {
		for _, kv := range o.Pairs() {
			if !known[kv.Key] {
				*errs = append(*errs, pydErr{joinLoc(loc, kv.Key), "Extra inputs are not permitted", "extra_forbidden", kv.Value})
			}
		}
	}
}

// ValidateArtifactSchema validates data against the plural kind's schema
// and returns pydantic's ValidationError text ("" when valid).
func ValidateArtifactSchema(plural string, data *jsonx.Object) string {
	m := schemaModels[plural]
	if m == nil {
		return ""
	}
	var errs []pydErr
	if plural == "agent_profiles" {
		if _, hasRole := data.Get("role"); hasRole {
			if _, hasRoles := data.Get("roles"); !hasRoles {
				d := data.Clone()
				rv, _ := d.Get("role")
				if s, ok := rv.(string); ok {
					rv = strings.ToLower(s)
				}
				nd := &jsonx.Object{}
				for _, kv := range d.Pairs() {
					if kv.Key != "role" {
						nd.Set(kv.Key, kv.Value)
					}
				}
				nd.Set("roles", []any{rv})
				data = nd
			}
		}
	}
	validateModel(data, m, "", &errs)
	if len(errs) == 0 {
		return ""
	}
	head := fmt.Sprintf("%d validation error%s for %s", len(errs), plural1(len(errs)), m.name)
	lines := []string{head}
	for _, e := range errs {
		lines = append(lines, e.loc, fmt.Sprintf("  %s [type=%s, input_value=%s, input_type=%s]", e.msg, e.typ, truncRepr(pyValueRepr(e.input)), pyTypeOf(e.input)),
			"    For further information visit https://errors.pydantic.dev/2.14/v/"+e.typ)
	}
	return strings.Join(lines, "\n")
}

func plural1(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
