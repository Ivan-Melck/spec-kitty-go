package charter

import (
	"fmt"
	"path/filepath"
	"sort"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file ports charter.activation.org_charter's interview pre-fill
// (apply_org_charter_to_interview, _promote_org_required_to_config) and the
// legacy (config-registry) load_org_charter_policies fold.

// OrgRequirableKinds is ORG_REQUIRABLE_KIND_FIELDS (ArtifactKind order).
var OrgRequirableKinds = []string{"directives", "tactics", "styleguides", "toolguides", "paradigms", "procedures",
	"agent_profiles", "mission_step_contracts", "assets", "glossary_packs", "skills"}

// interviewSeededKinds is _INTERVIEW_SEEDED_KIND_FIELDS (skills are in force
// while absent, so never seeded).
var interviewSeededKinds = []string{"directives", "tactics", "styleguides", "toolguides", "paradigms", "procedures",
	"agent_profiles", "mission_step_contracts", "assets", "glossary_packs"}

// OrgPolicy is the folded OrgCharterPolicy subset the CLI consumes.
type OrgPolicy struct {
	Required          map[string][]string
	InterviewDefaults [][2]string // key -> str(value), first-set order
}

func (p *OrgPolicy) hasAnyRequired() bool {
	for _, k := range OrgRequirableKinds {
		if len(p.Required[k]) > 0 {
			return true
		}
	}
	return false
}

// pyStrValue is str(v) for a YAML scalar.
func pyStrValue(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return pyStrOf(v)
}

// foldOrgPolicies is _fold_policies (required union, per-key defaults).
func foldOrgPolicies(pols []*jsonx.Object) *OrgPolicy {
	out := &OrgPolicy{Required: foldRequired(pols)}
	idx := map[string]int{}
	for _, pol := range pols {
		d := pol.GetObject("interview_defaults")
		if d == nil {
			continue
		}
		for _, kv := range d.Pairs() {
			if i, ok := idx[kv.Key]; ok {
				out.InterviewDefaults[i][1] = pyStrValue(kv.Value)
				continue
			}
			idx[kv.Key] = len(out.InterviewDefaults)
			out.InterviewDefaults = append(out.InterviewDefaults, [2]string{kv.Key, pyStrValue(kv.Value)})
		}
	}
	return out
}

// LegacyOrgPolicy is load_org_charter_policies(repo_root) without a pack
// context: every configured pack's org-charter.yaml in declaration order
// (no extends: chains). A pack root that cannot be resolved is an error.
func LegacyOrgPolicy(repoRoot string) (*OrgPolicy, error) {
	var pols []*jsonx.Object
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil, err
		}
		pol, err := LoadOrgCharterPolicy(root)
		if err != nil || pol == nil {
			continue
		}
		pols = append(pols, pol)
	}
	return foldOrgPolicies(pols), nil
}

// ApplyOrgCharterToInterview is apply_org_charter_to_interview: pre-fill
// missing answers from interview_defaults, promote org-required ids into the
// activation store, and pre-select required directives/tactics/paradigms.
func ApplyOrgCharterToInterview(repoRoot string, iv *Interview) ([]string, error) {
	if len(LoadPackRegistry(repoRoot).Packs) == 0 {
		return nil, nil
	}
	pol, err := LegacyOrgPolicy(repoRoot)
	if err != nil {
		return nil, err
	}
	if len(pol.InterviewDefaults) == 0 && !pol.hasAnyRequired() {
		return nil, nil
	}
	if iv.Answers == nil {
		iv.Answers = jsonx.NewObject()
	}
	prefilled := 0
	for _, kv := range pol.InterviewDefaults {
		if _, ok := iv.Answers.Get(kv[0]); !ok {
			iv.Answers.Set(kv[0], kv[1])
			prefilled++
		}
	}
	msgs, err := promoteOrgRequired(repoRoot, pol)
	if err != nil {
		return nil, err
	}
	for _, kind := range interviewSeededKinds {
		req := pol.Required[kind]
		if len(req) == 0 {
			continue
		}
		var sel *[]string
		switch kind {
		case "directives":
			sel = &iv.SelectedDirectives
		case "tactics":
			sel = &iv.SelectedTactics
		case "paradigms":
			sel = &iv.SelectedParadigms
		default:
			continue // the frozen CharterInterview has no selected_<kind> field
		}
		var added []string
		for _, id := range req {
			found := false
			for _, x := range *sel {
				found = found || x == id
			}
			if !found {
				added = append(added, id)
			}
		}
		if len(added) > 0 {
			*sel = append(*sel, added...)
			label := kind
			if kind == "directives" {
				label = "directive(s)"
			}
			msgs = append(msgs, fmt.Sprintf("Pre-selected %d %s from org charter required_%s.", len(added), label, kind))
		}
	}
	if prefilled > 0 {
		msgs = append(msgs, fmt.Sprintf("Pre-filled %d interview default(s) from org charter.", prefilled))
	}
	return msgs, nil
}

// promoteOrgRequired is _promote_org_required_to_config.
func promoteOrgRequired(repoRoot string, pol *OrgPolicy) ([]string, error) {
	t, err := LoadWriteTarget(repoRoot)
	if err != nil {
		return nil, err
	}
	activationKeys := map[string]bool{}
	for _, tok := range dct.CharterKindTokens {
		activationKeys[dct.YAMLKey(tok)] = true
	}
	seeded := map[string]bool{}
	for _, k := range interviewSeededKinds {
		seeded[k] = true
	}
	lr, chain := dct.LayerRoots(repoRoot), dct.OrgRoots(repoRoot)
	type promo struct {
		key, kind string
		ids       []string
	}
	var promos []promo
	for _, kind := range OrgRequirableKinds {
		key := "activated_" + kind
		if _, present := t.Data.Get(key); !seeded[kind] && !present {
			continue
		}
		req := pol.Required[kind]
		if len(req) == 0 {
			continue
		}
		k, _ := dct.KindFromPlural(kind)
		ids := make([]string, 0, len(req))
		for _, raw := range req {
			if s := SelectedIDToStem(k, raw, lr, chain); s != "" {
				ids = append(ids, s)
			} else {
				ids = append(ids, raw)
			}
		}
		promos = append(promos, promo{key, kind, ids})
	}
	var promoted, leftAbsent []string
	for _, p := range promos {
		cur, present, err := currentList(t.Data, p.key)
		if err != nil {
			return nil, err
		}
		var list []string
		switch {
		case present:
			list = append([]string{}, cur...)
		case activationKeys[p.key]:
			tok := ""
			for _, x := range dct.CharterKindTokens {
				if dct.YAMLKey(x) == p.key {
					tok = x
				}
			}
			eff, reason := EffectiveSet(repoRoot, tok)
			if reason != "" {
				leftAbsent = append(leftAbsent, fmt.Sprintf("%s was left absent and nothing was promoted into it: the effective set could not be resolved (%s).", p.key, reason))
				continue
			}
			list = append([]string{}, eff...)
			sort.Strings(list)
		default:
			list = []string{}
		}
		activated := 0
		seen := map[string]bool{}
		for _, id := range p.ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			found := false
			for _, x := range list {
				found = found || x == id
			}
			if !found {
				list = append(list, id)
				activated++
			}
		}
		t.Data.Set(p.key, strsAny(list))
		if err := t.Save(); err != nil {
			return nil, err
		}
		if activated > 0 {
			promoted = append(promoted, fmt.Sprintf("Promoted %d org-required id(s) into %s (config-authority).", activated, p.key))
		}
	}
	return append(promoted, leftAbsent...), nil
}

// OrgCharterJSONBlock is org_charter_loader.load_org_charter_json_block over
// resolve_pack_chain(strict=False): one entry per pack shipping a valid
// org-charter.yaml.
func OrgCharterJSONBlock(repoRoot string) *jsonx.Object {
	packs := []any{}
	for _, r := range ResolvePackChain(repoRoot) {
		pol, err := LoadOrgCharterPolicy(r)
		if err != nil || pol == nil {
			continue
		}
		gps := []any{}
		for _, x := range pol.GetList("governance_policies") {
			gp, ok := x.(*jsonx.Object)
			if !ok {
				continue
			}
			enf := any("advisory")
			if v, ok := gp.Get("enforcement"); ok {
				enf = v
			}
			field, _ := gp.Get("field")
			value, _ := gp.Get("value")
			gps = append(gps, jsonx.NewObject("field", field, "value", value, "enforcement", enf, "source", "org"))
		}
		name := pol.GetString("org_name")
		if name == "" {
			name = filepath.Base(r)
		}
		req := []any{}
		for _, d := range pol.GetList("required_directives") {
			req = append(req, pyStrValue(d))
		}
		packs = append(packs, jsonx.NewObject("pack_name", name, "governance_policies", gps, "required_directives", req))
	}
	return jsonx.NewObject("present", len(packs) > 0, "packs", packs)
}
