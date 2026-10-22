package charter

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports specify_cli.charter_runtime.lint (LintEngine, the
// graph-native decay checkers and DecayReport).

// Graph states.
const (
	GraphMerged      = "merged"
	GraphBuiltInOnly = "built_in_only"
	GraphMissing     = "missing"
)

// LintFinding is one decay finding.
type LintFinding struct {
	Category, Type, ID, Severity, Message string
	FeatureID, Remediation                *string
}

// Object is LintFinding.to_dict.
func (f LintFinding) Object() *jsonx.Object {
	return jsonx.NewObject("category", f.Category, "type", f.Type, "id", f.ID, "severity", f.Severity,
		"message", f.Message, "feature_id", optAny(f.FeatureID), "remediation_hint", optAny(f.Remediation))
}

// DecayReport is the lint report.
type DecayReport struct {
	Findings     []LintFinding
	ScannedAt    string
	FeatureScope *string
	Duration     float64
	NodeCount    int
	EdgeCount    int
	GraphState   string
}

// Object is DecayReport.to_dict.
func (r *DecayReport) Object() *jsonx.Object {
	fs := []any{}
	for _, f := range r.Findings {
		fs = append(fs, f.Object())
	}
	return jsonx.NewObject("scanned_at", r.ScannedAt, "feature_scope", optAny(r.FeatureScope), "duration_seconds", jsonx.Float(r.Duration),
		"drg_node_count", r.NodeCount, "drg_edge_count", r.EdgeCount, "graph_state", r.GraphState,
		"finding_count", len(r.Findings), "findings", fs)
}

// JSON is DecayReport.to_json (indent 2).
func (r *DecayReport) JSON() string { return jsonx.Dumps(r.Object(), jsonx.Pretty) }

var severityOrder = map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}

// LintChecks are the check categories.
var LintChecks = []string{"contradictions", "org_charter_deviation", "org_overrides_builtin", "orphans", "reference_integrity", "staleness"}

var graphModelNode = map[string]bool{"urn": true, "kind": true, "label": true, "provenance": true, "tags": true}
var graphModelEdge = map[string]bool{"source": true, "target": true, "relation": true, "when": true, "reason": true, "provenance": true}

func strOrNil(v any) (*string, bool) {
	if v == nil {
		return nil, true
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	return &s, true
}

// ParseDRGGraphDocument validates a parsed graph document against
// DRGGraph (strict); returns nil when it does not validate.
func ParseDRGGraphDocument(v any) *MergedGraph {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	for _, kv := range o.Pairs() {
		switch kv.Key {
		case "schema_version", "generated_at", "generated_by", "nodes", "edges":
		default:
			return nil
		}
	}
	sv, _ := o.Get("schema_version")
	if s, ok := sv.(string); !ok || s != "1.0" {
		return nil
	}
	for _, k := range []string{"generated_at", "generated_by"} {
		if x, ok := o.Get(k); !ok {
			return nil
		} else if _, isStr := x.(string); !isStr {
			return nil
		}
	}
	rn, ok1 := o.Get("nodes")
	re, ok2 := o.Get("edges")
	nl, ok3 := rn.([]any)
	el, ok4 := re.([]any)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil
	}
	g := &MergedGraph{}
	for _, x := range nl {
		n, ok := x.(*jsonx.Object)
		if !ok {
			return nil
		}
		for _, kv := range n.Pairs() {
			if !graphModelNode[kv.Key] {
				return nil
			}
		}
		urn, _ := n.Get("urn")
		kind, _ := n.Get("kind")
		us, ok1 := urn.(string)
		ks, ok2 := kind.(string)
		if !ok1 || !ok2 || !isNodeKind(ks) || !IsValidURN(us) || strings.SplitN(us, ":", 2)[0] != ks {
			return nil
		}
		lv, _ := n.Get("label")
		label, ok := strOrNil(lv)
		if !ok {
			return nil
		}
		pv, _ := n.Get("provenance")
		prov, ok := strOrNil(pv)
		if !ok {
			return nil
		}
		mn := MergedNode{URN: us, Kind: ks, Label: label}
		if prov != nil {
			mn.Provenance = *prov
		}
		g.Nodes = append(g.Nodes, mn)
	}
	validRel := map[string]bool{}
	for _, r := range Relations {
		validRel[r] = true
	}
	for _, x := range el {
		e, ok := x.(*jsonx.Object)
		if !ok {
			return nil
		}
		for _, kv := range e.Pairs() {
			if !graphModelEdge[kv.Key] {
				return nil
			}
		}
		s, _ := e.Get("source")
		t, _ := e.Get("target")
		r, _ := e.Get("relation")
		ss, ok1 := s.(string)
		ts, ok2 := t.(string)
		rs, ok3 := r.(string)
		if !ok1 || !ok2 || !ok3 || !validRel[rs] || !IsValidURN(ss) || !IsValidURN(ts) {
			return nil
		}
		rv, _ := e.Get("reason")
		reason, ok := strOrNil(rv)
		if !ok {
			return nil
		}
		g.Edges = append(g.Edges, MergedEdge{Source: ss, Target: ts, Relation: rs, Reason: reason})
	}
	return g
}

func loadGraphFile(p string) *MergedGraph {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var v any
	if strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
		v, err = yamlx.Parse(b)
	} else {
		v, err = pyjson.LoadsBytes(b)
	}
	if err != nil {
		return nil
	}
	return ParseDRGGraphDocument(v)
}

// LoadLintDRG is load_merged_drg: the project DRG (graph.yaml >
// merged_drg.json > drg.json > compiled_drg.json under the project pack
// root) or the built-in DRG.
func LoadLintDRG(repoRoot string) (*MergedGraph, string) {
	dir := filepath.Join(repoRoot, filepath.FromSlash(dct.ProjectPackRoot))
	for _, n := range []string{"graph.yaml", "merged_drg.json", "drg.json", "compiled_drg.json"} {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			if g := loadGraphFile(p); g != nil {
				return g, GraphMerged
			}
		}
	}
	if g, err := BuiltInMergedGraph(); err == nil {
		return g, GraphBuiltInOnly
	}
	return nil, GraphMissing
}

func pyListRepr(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = dct.PyRepr(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func lintOrphans(g *MergedGraph, scope *string) []LintFinding {
	rules := map[string][]string{
		"directive":      {"applies", "refines", "requires", "scope", "suggests"},
		"glossary_scope": {"vocabulary"},
	}
	var out []LintFinding
	for _, n := range g.Nodes {
		rels, ok := rules[n.Kind]
		if !ok {
			continue
		}
		relSet := map[string]bool{}
		for _, r := range rels {
			relSet[r] = true
		}
		found := false
		for _, e := range g.Edges {
			if e.Target == n.URN && relSet[e.Relation] {
				found = true
				break
			}
		}
		if !found && n.Kind == "directive" {
			for _, e := range g.Edges {
				if e.Source == n.URN && e.Relation == "reconciles_tension" {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}
		label := n.URN
		if n.Label != nil && *n.Label != "" {
			label = *n.Label
		}
		q := make([]string, len(rels))
		for i, r := range rels {
			q[i] = "'" + r + "'"
		}
		rem := fmt.Sprintf("Link another node to this %s node via one of: %s", n.Kind, strings.Join(q, ", "))
		out = append(out, LintFinding{Category: "orphan", Type: "orphaned_" + n.Kind, ID: n.URN, Severity: "medium",
			Message: fmt.Sprintf("Node '%s' (%s) has no incoming edges with relation %s.", label, n.URN, pyListRepr(rels)), FeatureID: scope, Remediation: &rem})
	}
	return out
}

func lintContradictions(g *MergedGraph, scope *string) []LintFinding {
	by := map[string][]string{}
	var order []string
	for _, n := range g.Nodes {
		if n.Kind != "glossary_scope" || n.Label == nil || *n.Label == "" {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(*n.Label))
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] = append(by[k], n.URN)
	}
	var out []LintFinding
	for _, k := range order {
		urns := by[k]
		if len(urns) > 1 {
			rem := "Merge the duplicate definitions or make them distinct terms."
			out = append(out, LintFinding{Category: "contradiction", Type: "duplicate_glossary_sense", ID: "label:" + k, Severity: "medium",
				Message: fmt.Sprintf("Glossary label '%s' is defined by %d nodes: %s", k, len(urns), strings.Join(urns, ", ")), FeatureID: scope, Remediation: &rem})
		}
	}
	return out
}

func lintReferenceIntegrity(g *MergedGraph, scope *string) []LintFinding {
	urns := g.NodeURNs()
	var out []LintFinding
	for _, e := range g.Edges {
		if e.Target == "" || urns[e.Target] {
			continue
		}
		rem := fmt.Sprintf("Remove the edge or add the missing node '%s'.", e.Target)
		out = append(out, LintFinding{Category: "reference_integrity", Type: "dangling_edge", ID: fmt.Sprintf("edge:%s->%s", e.Source, e.Target), Severity: "high",
			Message:   fmt.Sprintf("Edge from '%s' to '%s' via '%s' is dangling — target URN '%s' does not exist in the DRG.", e.Source, e.Target, e.Relation, e.Target),
			FeatureID: scope, Remediation: &rem})
	}
	return out
}

func lintOrgOverridesBuiltin(repoRoot string) []LintFinding {
	reg := LoadPackRegistry(repoRoot)
	if len(reg.Packs) == 0 {
		return nil
	}
	var roots []string
	for _, p := range reg.Packs {
		r, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil
		}
		if _, err := os.Stat(r); err == nil {
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	projectRoot := ProjectPackRootIfExists(repoRoot)
	var out []LintFinding
	for _, rk := range repoKinds {
		kind, _ := dct.KindFromPlural(rk.plural)
		if kind == dct.AgentProfile {
			continue // profiles expose profile_id, not id
		}
		org, proj := kindDirs(roots, projectRoot, kind)
		withOrg := loadKindRepo(kind, rk.label, rk.fields, org, proj)
		_, proj2 := kindDirs(nil, projectRoot, kind)
		base := loadKindRepo(kind, rk.label, rk.fields, nil, proj2)
		ids := make([]string, 0, len(withOrg.items))
		for id := range withOrg.items {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if withOrg.provenance[id] != dct.LayerOrg {
				continue
			}
			if _, ok := base.items[id]; !ok {
				continue
			}
			rem := "Verify the override is intentional; remove the org pack copy if the built-in artifact already meets policy."
			out = append(out, LintFinding{Category: "org_layer", Type: "org_overrides_builtin", ID: rk.plural + ":" + id, Severity: "low",
				Message: fmt.Sprintf("org layer overrides built-in %s %s", rk.plural[:len(rk.plural)-1], dct.PyRepr(id)), Remediation: &rem})
		}
	}
	return out
}

func lintOrgCharterDeviation(repoRoot string) []LintFinding {
	var policies []*jsonx.Object
	seen := map[[2]string]int{}
	for _, p := range LoadPackRegistry(repoRoot).Packs {
		root, err := p.EffectiveRoot(repoRoot)
		if err != nil {
			return nil
		}
		pol, err := LoadOrgCharterPolicy(root)
		if err != nil || pol == nil {
			continue
		}
		for _, x := range pol.GetList("governance_policies") {
			o, ok := x.(*jsonx.Object)
			if !ok {
				continue
			}
			k := [2]string{fmt.Sprint(pyStrOf(mustGet(o, "field"))), fmt.Sprint(pyStrOf(mustGet(o, "value")))}
			if i, ok := seen[k]; ok {
				policies[i] = nil
			}
			seen[k] = len(policies)
			policies = append(policies, o)
		}
	}
	if len(policies) == 0 {
		return nil
	}
	answers := loadProjectCharterFields(repoRoot)
	var out []LintFinding
	for _, pol := range policies {
		if pol == nil {
			continue
		}
		fv, _ := pol.Get("field")
		field, ok := fv.(string)
		if !ok {
			continue
		}
		pv, ok := answers.Get(field)
		if !ok || pv == nil {
			continue
		}
		expected, _ := pol.Get("value")
		if pyStrOf(pv) == pyStrOf(expected) {
			continue
		}
		rem := "Reconcile via charter interview, or document an explicit deviation in the project charter."
		out = append(out, LintFinding{Category: "org_layer", Type: "org_charter_deviation", ID: "governance:" + field, Severity: "low",
			Message: fmt.Sprintf("project charter field %s = %s; org charter recommends %s", dct.PyRepr(field), pyValueRepr(pv), pyValueRepr(expected)), Remediation: &rem})
	}
	return out
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func loadProjectCharterFields(repoRoot string) *jsonx.Object {
	out := jsonx.NewObject()
	b, err := os.ReadFile(filepath.Join(repoRoot, ".kittify", "charter", "interview", "answers.yaml"))
	if err != nil {
		return out
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return out
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return out
	}
	if nested := o.GetObject("answers"); nested != nil {
		for _, kv := range nested.Pairs() {
			out.Set(kv.Key, kv.Value)
		}
	}
	for _, kv := range o.Pairs() {
		if kv.Key == "answers" {
			continue
		}
		if _, ok := out.Get(kv.Key); !ok {
			out.Set(kv.Key, kv.Value)
		}
	}
	return out
}

// RunLint is LintEngine.run: run the requested checks (nil = all),
// persist .kittify/lint-report.json and return the report.
func RunLint(repoRoot string, scope *string, checks map[string]bool, minSeverity string) *DecayReport {
	g, state := LoadLintDRG(repoRoot)
	if g == nil {
		rep := &DecayReport{ScannedAt: clock.NowUTCISO(), FeatureScope: scope, GraphState: GraphMissing}
		persistLint(repoRoot, rep)
		return rep
	}
	t0 := time.Now()
	var all []LintFinding
	for _, c := range LintChecks {
		if checks != nil && !checks[c] {
			continue
		}
		switch c {
		case "contradictions":
			all = append(all, lintContradictions(g, scope)...)
		case "org_charter_deviation":
			all = append(all, lintOrgCharterDeviation(repoRoot)...)
		case "org_overrides_builtin":
			all = append(all, lintOrgOverridesBuiltin(repoRoot)...)
		case "orphans":
			all = append(all, lintOrphans(g, scope)...)
		case "reference_integrity":
			all = append(all, lintReferenceIntegrity(g, scope)...)
		}
	}
	d := time.Since(t0).Seconds()
	rep := &DecayReport{Findings: all, ScannedAt: clock.NowUTCISO(), FeatureScope: scope, Duration: math.Round(d*1000) / 1000,
		NodeCount: len(g.Nodes), EdgeCount: len(g.Edges), GraphState: state}
	if minSeverity != "low" {
		th := severityOrder[minSeverity]
		var kept []LintFinding
		for _, f := range rep.Findings {
			if severityOrder[f.Severity] >= th {
				kept = append(kept, f)
			}
		}
		rep.Findings = kept
	}
	persistLint(repoRoot, rep)
	return rep
}

func persistLint(repoRoot string, rep *DecayReport) {
	p := filepath.Join(repoRoot, ".kittify", "lint-report.json")
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, []byte(rep.JSON()), 0o644)
	}
}

// EmptyGraph is an empty DRG (charter lint merges org fragments against
// it to isolate pack-level conflicts).
func EmptyGraph() *MergedGraph { return &MergedGraph{} }
