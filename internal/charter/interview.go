package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// QuestionOrder / MinimalQuestionOrder / QuestionPrompts mirror the
// charter interview question set.
var (
	QuestionOrder = []string{"project_intent", "languages_frameworks", "testing_requirements", "quality_gates", "review_policy",
		"performance_targets", "deployment_constraints", "documentation_policy", "risk_boundaries", "amendment_process", "exception_policy"}
	MinimalQuestionOrder = QuestionOrder[:7]
	QuestionPrompts      = map[string]string{
		"project_intent":         "What is the core user outcome this project optimizes for?",
		"languages_frameworks":   "What languages/frameworks are expected?",
		"testing_requirements":   "What testing and coverage expectations apply?",
		"quality_gates":          "What quality gates must pass before merge?",
		"review_policy":          "What review/approval policy should contributors follow?",
		"performance_targets":    "What performance targets matter most (or N/A)?",
		"deployment_constraints": "What deployment/platform constraints apply?",
		"documentation_policy":   "What documentation standards should be enforced?",
		"risk_boundaries":        "What safety, privacy, or reliability boundaries are non-negotiable?",
		"amendment_process":      "How should charter changes be proposed and approved?",
		"exception_policy":       "How should exceptions to the charter be handled?",
	}
)

// Interview is CharterInterview.
type Interview struct {
	Mission, Profile   string
	Answers            *jsonx.Object // ordered str -> str
	SelectedParadigms  []string
	SelectedDirectives []string
	AvailableTools     []string
	AgentProfile       *string
	AgentRole          *string
	LocalSupport       []*jsonx.Object
	SelectedTactics    []string
}

// ToObject is CharterInterview.to_dict.
func (iv *Interview) ToObject() *jsonx.Object {
	var ap, ar any
	if iv.AgentProfile != nil {
		ap = *iv.AgentProfile
	}
	if iv.AgentRole != nil {
		ar = *iv.AgentRole
	}
	o := jsonx.NewObject("schema_version", "1.0.0", "mission", iv.Mission, "profile", iv.Profile, "answers", iv.Answers.Clone(),
		"selected_paradigms", strsAny(nz(iv.SelectedParadigms)), "selected_directives", strsAny(nz(iv.SelectedDirectives)),
		"selected_tactics", strsAny(nz(iv.SelectedTactics)), "available_tools", strsAny(nz(iv.AvailableTools)),
		"agent_profile", ap, "agent_role", ar)
	if len(iv.LocalSupport) > 0 {
		l := []any{}
		for _, d := range iv.LocalSupport {
			l = append(l, d)
		}
		o.Set("local_supporting_files", l)
	}
	return o
}

func nz(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func normalizeCSV(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeIter(vals []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func normalizeListAny(v any) []string {
	switch t := v.(type) {
	case string:
		return normalizeCSV(t)
	case []any:
		var s []string
		for _, x := range t {
			s = append(s, pyStrOf(x))
		}
		return normalizeIter(s)
	}
	return []string{}
}

// packagedDefaults is _load_packaged_defaults (charter/defaults.yaml).
func packagedDefaults() *jsonx.Object {
	b, err := assets.ReadFile("charter/defaults.yaml")
	if err != nil {
		return &jsonx.Object{}
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return &jsonx.Object{}
	}
	return o
}

// PackagedDefaultAnswer is packaged_default_answer.
func PackagedDefaultAnswer(key string) string {
	a := packagedDefaults().GetObject("answers")
	if a == nil {
		return ""
	}
	v, ok := a.Get(key)
	if !ok || v == nil {
		return ""
	}
	return pyStrOf(v)
}

// DefaultInterview is default_interview.
func DefaultInterview(mission, profile string) *Interview {
	d := packagedDefaults()
	answers := &jsonx.Object{}
	if a := d.GetObject("answers"); a != nil {
		for _, kv := range a.Pairs() {
			answers.Set(kv.Key, pyStrOf(kv.Value))
		}
	}
	if profile == "minimal" {
		m := &jsonx.Object{}
		for _, k := range MinimalQuestionOrder {
			if v, ok := answers.Get(k); ok {
				m.Set(k, v)
			}
		}
		answers = m
	}
	listOr := func(key string, fallback []string) []string {
		if v, ok := d.Get(key); ok && v != nil {
			return normalizeIter(normalizeListAny(v))
		}
		return fallback
	}
	iv := &Interview{Mission: mission, Profile: profile, Answers: answers,
		SelectedParadigms:  listOr("selected_paradigms", []string{}),
		SelectedDirectives: listOr("selected_directives", []string{}),
		AvailableTools:     listOr("available_tools", append([]string{}, DefaultToolRegistry...)),
		SelectedTactics:    listOr("selected_tactics", []string{}),
	}
	return applyDoctrineIntentAliases(iv)
}

var (
	lynnExplicit = []*regexp.Regexp{regexp.MustCompile(`\blynn\s+cole\b`)}
	lynnConcern  = []*regexp.Regexp{
		regexp.MustCompile(`\bagents?\s+(?:write|produce|generate|create)\s+too\s+much\s+code\b`),
		regexp.MustCompile(`\b(?:ai|llm|agentic|generated)\s+code\s+(?:is\s+)?(?:bloated|sprawling|too\s+long)\b`),
		regexp.MustCompile(`\b(?:code|implementation)\s+bloat\b`),
		regexp.MustCompile(`\b(?:avoid|stop|prevent|reduce)\s+(?:agentic\s+)?(?:code\s+)?bloat\b`),
		regexp.MustCompile(`\bover[- ]?abstract(?:ion|ed|ing)?\b`),
		regexp.MustCompile(`\bsprawling\s+(?:helpers?|functions?|code|implementation)\b`),
		regexp.MustCompile(`\btoo\s+many\s+(?:helpers?|abstractions?|files?|layers?)\b`),
	}
)

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(append([]string{}, xs...), x)
}

// applyDoctrineIntentAliases is apply_doctrine_intent_aliases (Lynn Cole).
func applyDoctrineIntentAliases(iv *Interview) *Interview {
	var parts []string
	for _, kv := range iv.Answers.Pairs() {
		parts = append(parts, strings.ToLower(pyStrOf(kv.Value)))
	}
	hay := strings.Join(parts, " ")
	if strings.TrimSpace(hay) == "" {
		return iv
	}
	match := false
	for _, re := range append(append([]*regexp.Regexp{}, lynnExplicit...), lynnConcern...) {
		match = match || re.MatchString(hay)
	}
	if !match {
		return iv
	}
	iv.SelectedDirectives = appendUnique(iv.SelectedDirectives, "DIRECTIVE_039")
	iv.SelectedParadigms = appendUnique(iv.SelectedParadigms, "deep-module-design")
	return iv
}

// ApplyOverrides is apply_answer_overrides (nil slice = keep).
func ApplyOverrides(iv *Interview, answers *jsonx.Object, paradigms, directives, tools []string, hasP, hasD, hasT bool) *Interview {
	if answers != nil {
		for _, kv := range answers.Pairs() {
			iv.Answers.Set(kv.Key, pyStrOf(kv.Value))
		}
	}
	if hasP {
		iv.SelectedParadigms = normalizeIter(paradigms)
	}
	if hasD {
		iv.SelectedDirectives = normalizeIter(directives)
	}
	if hasT {
		iv.AvailableTools = normalizeIter(tools)
	}
	return applyDoctrineIntentAliases(iv)
}

// InterviewPath is .kittify/charter/interview/answers.yaml.
func InterviewPath(root string) string { return Path(root, AnswersYAML) }

// WriteInterview is write_interview_answers: merged onto the existing file.
func WriteInterview(path string, iv *Interview) error {
	merged := &jsonx.Object{}
	if b, err := os.ReadFile(path); err == nil {
		if o, err := yamlx.ParseObject(b); err == nil && o != nil {
			merged = o
		}
	}
	for _, kv := range iv.ToObject().Pairs() {
		merged.Set(kv.Key, kv.Value)
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte(yamlx.EmitWith(merged, yamlx.Options{Width: 80})), 0o644)
}

// ReadInterview is read_interview_answers (nil when missing/invalid).
func ReadInterview(path string) *Interview {
	if !exists(path) {
		return nil
	}
	// Decoded through the encoding chokepoint (provenance record per load).
	b, ok := loadCharterText(path)
	if !ok {
		return nil
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return nil
	}
	mission := strings.TrimSpace(pyStrOrDefault(o, "mission", "software-dev"))
	if mission == "" {
		mission = "software-dev"
	}
	profile := strings.TrimSpace(pyStrOrDefault(o, "profile", "minimal"))
	if profile == "" {
		profile = "minimal"
	}
	answers := &jsonx.Object{}
	if a := o.GetObject("answers"); a != nil {
		for _, kv := range a.Pairs() {
			answers.Set(kv.Key, pyStrOf(kv.Value))
		}
	}
	optStr := func(k string) *string {
		v, ok := o.Get(k)
		if !ok || v == nil {
			return nil
		}
		s := strings.TrimSpace(pyStrOf(v))
		if s == "" {
			return nil
		}
		return &s
	}
	iv := &Interview{Mission: mission, Profile: profile, Answers: answers,
		SelectedParadigms: normalizeListAny(mustGetAny(o, "selected_paradigms")), SelectedDirectives: normalizeListAny(mustGetAny(o, "selected_directives")),
		AvailableTools: normalizeListAny(mustGetAny(o, "available_tools")), AgentProfile: optStr("agent_profile"), AgentRole: optStr("agent_role"),
		SelectedTactics: normalizeListAny(mustGetAny(o, "selected_tactics"))}
	for _, x := range o.GetList("local_supporting_files") {
		if d, ok := x.(*jsonx.Object); ok && strings.TrimSpace(d.GetString("path")) != "" {
			iv.LocalSupport = append(iv.LocalSupport, d)
		}
	}
	return applyDoctrineIntentAliases(iv)
}

func mustGetAny(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func pyStrOrDefault(o *jsonx.Object, k, def string) string {
	v, ok := o.Get(k)
	if !ok {
		return def
	}
	return pyStrOf(v)
}

// SelectedIDToStem is resolve_selected_id_to_stem ("" = unresolved).
func SelectedIDToStem(k dct.Kind, raw string, lr map[string]string, chain []string) string {
	stems := dct.StemsByDeclaredID(k, lr, chain)
	byStem := map[string]bool{}
	for _, s := range stems {
		byStem[s] = true
	}
	if k == dct.Directive {
		if s, ok := stems[raw]; ok {
			return s
		}
		if s, ok := stems[dct.NormalizeDirectiveID(raw)]; ok && regexp.MustCompile(`^\d`).MatchString(raw) {
			return s
		}
	}
	if byStem[raw] {
		return raw
	}
	if s, ok := stems[raw]; ok {
		return s
	}
	return ""
}

// PromoteSelections is interview._promote_interview_selections: append-only
// promotion of selected directives/tactics/paradigms into activated_* keys.
func PromoteSelections(root string, iv *Interview) ([]string, error) {
	lr, chain := dct.LayerRoots(root), dct.OrgRoots(root)
	type promo struct {
		key   string
		token string
		stems []string
	}
	var warnings []string
	var promos []promo
	for _, sel := range []struct {
		k   dct.Kind
		ids []string
	}{{dct.Directive, iv.SelectedDirectives}, {dct.Tactic, iv.SelectedTactics}, {dct.Paradigm, iv.SelectedParadigms}} {
		var stems []string
		for _, raw := range sel.ids {
			s := SelectedIDToStem(sel.k, raw, lr, chain)
			if s == "" {
				warnings = append(warnings, fmt.Sprintf("Could not resolve selected %s id %s; skipped.", sel.k.OperatorToken(), dct.PyRepr(raw)))
				continue
			}
			stems = appendUnique(stems, s)
		}
		if len(stems) > 0 {
			promos = append(promos, promo{"activated_" + sel.k.Plural(), sel.k.OperatorToken(), stems})
		}
	}
	if len(promos) == 0 {
		return warnings, nil
	}
	t, err := LoadWriteTarget(root)
	if err != nil {
		return warnings, err
	}
	var leftAbsent []string
	changed := false
	for _, p := range promos {
		cur, present, err := currentList(t.Data, p.key)
		if err != nil {
			return warnings, err
		}
		var newList []string
		if !present {
			eff, reason := EffectiveSet(root, p.token)
			if reason != "" {
				leftAbsent = append(leftAbsent, fmt.Sprintf("%s was left absent and nothing was promoted into it: the effective set could not be resolved (%s).", p.key, reason))
				continue
			}
			newList = append([]string{}, eff...)
			sort.Strings(newList)
			warnings = append(warnings, fmt.Sprintf("Key %s had no explicit activation set. Preserved %d effective entries before promotion (absent-key parity).", dct.PyRepr(p.key), len(newList)))
		} else {
			newList = append([]string{}, cur...)
		}
		for _, id := range p.stems {
			found := false
			for _, x := range newList {
				found = found || x == id
			}
			if found {
				warnings = append(warnings, fmt.Sprintf("%s is already activated for key %s.", dct.PyRepr(id), dct.PyRepr(p.key)))
			} else {
				newList = append(newList, id)
			}
		}
		t.Data.Set(p.key, strsAny(newList))
		changed = true
		if err := t.Save(); err != nil {
			return warnings, err
		}
	}
	_ = changed
	return append(warnings, leftAbsent...), nil
}
