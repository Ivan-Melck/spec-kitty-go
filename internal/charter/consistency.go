package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.activation.consistency_check
// (run_consistency_check / ConsistencyReport).

// TensionFinding is one co-activated, unreconciled in_tension_with pair.
type TensionFinding struct{ A, B string }

// ConsistencyReport is the consistency-check result.
type ConsistencyReport struct {
	Coherent                                                 bool
	UnknownReferences, MissingFromOffering, KindViolations   []string
	ReferenceIDDivergences, GraphKindGaps, VerificationErrors []string
	UnreconciledTensions                                     []TensionFinding
	LatticeViolations, DecisionDocViolations, Suggestions    []string
}

func ccList(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// Object is the to_json payload.
func (r *ConsistencyReport) Object() *jsonx.Object {
	ts := []any{}
	for _, t := range r.UnreconciledTensions {
		ts = append(ts, jsonx.NewObject("type", "tension_unreconciled", "pair", []any{t.A, t.B},
			"resolution_paths", []any{"deactivate one side", "activate a reconciler"}))
	}
	return jsonx.NewObject("coherent", r.Coherent, "unknown_references", ccList(r.UnknownReferences),
		"missing_from_offering", ccList(r.MissingFromOffering), "kind_violations", ccList(r.KindViolations),
		"reference_id_divergences", ccList(r.ReferenceIDDivergences), "graph_kind_gaps", ccList(r.GraphKindGaps),
		"verification_errors", ccList(r.VerificationErrors), "unreconciled_tensions", ts,
		"enforcement_lattice_violations", ccList(r.LatticeViolations),
		"decision_documentation_on_implement_violations", ccList(r.DecisionDocViolations),
		"suggestions", ccList(r.Suggestions))
}

// cliKindsToDRG is _CLI_KIND_TO_DRG_SINGULAR (ArtifactKind order).
var cliKindsToDRG = [][2]string{{"directive", "directive"}, {"tactic", "tactic"}, {"styleguide", "styleguide"},
	{"toolguide", "toolguide"}, {"paradigm", "paradigm"}, {"procedure", "procedure"}, {"agent-profile", "agent_profile"},
	{"mission-step-contract", "mission_step_contract"}, {"glossary-pack", "glossary_pack"}}

func drgSingularToCLI(s string) (string, bool) {
	for _, kv := range cliKindsToDRG {
		if kv[1] == s {
			return kv[0], true
		}
	}
	return "", false
}

// loadRawActivationSource is _load_raw_activation_source.
func loadRawActivationSource(repoRoot string) (*jsonx.Object, error) {
	cp := filepath.Join(repoRoot, ".kittify", "config.yaml")
	cfg := jsonx.NewObject()
	if b, err := os.ReadFile(cp); err == nil {
		v, err := yamlx.Parse(b)
		if err != nil {
			return nil, &dct.ConfigError{Body: fmt.Sprintf("Cannot read configuration %s: %v", cp, err)}
		}
		if o, ok := v.(*jsonx.Object); ok {
			cfg = o
		}
	}
	pv, _ := cfg.Get("charter")
	ptr, ok := pv.(string)
	if !ok {
		return cfg, nil
	}
	p := ptr
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoRoot, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &dct.ConfigError{Body: fmt.Sprintf(".kittify/config.yaml 'charter:' pointer names %s, which does not exist.", p)}
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, &dct.ConfigError{Body: fmt.Sprintf("Invalid YAML in %s: %v", p, err)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &dct.ConfigError{Body: fmt.Sprintf("%s root must be a mapping.", p)}
	}
	return o, nil
}

type rawLists struct {
	order []string
	lists map[string][]string
	set   map[string]bool // key present (not None)
}

func loadRawActivationLists(repoRoot string) (*rawLists, error) {
	data, err := loadRawActivationSource(repoRoot)
	if err != nil {
		return nil, err
	}
	rl := &rawLists{lists: map[string][]string{}, set: map[string]bool{}}
	for _, tok := range dct.CharterKindTokens {
		rl.order = append(rl.order, tok)
		v, ok := data.Get(dct.YAMLKey(tok))
		if !ok || v == nil {
			continue
		}
		rl.set[tok] = true
		l, ok := v.([]any)
		if !ok {
			rl.lists[tok] = []string{}
			continue
		}
		var out []string
		for _, x := range l {
			out = append(out, pyStrOf(x))
		}
		rl.lists[tok] = out
	}
	return rl, nil
}

func uniqSorted(xs []string) []string {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

type gateRes struct {
	root   string
	full   *dct.Graph
	err    error
	active map[string]bool
	off    *dct.Offering
}

func newGateRes(root string) *gateRes {
	g := &gateRes{root: root}
	orgs := dct.OrgRoots(root)
	g.off = dct.NewOffering(root, orgs)
	g.full, g.err = dct.LoadGraph(root, orgs)
	if g.err == nil {
		act, err := dct.LoadActivation(root)
		if err != nil {
			g.err = err
			return g
		}
		g.active = dct.FilterByActivation(g.full, act, g.off).NodeURNs()
	}
	return g
}

func (g *gateRes) failClosed(stem string, rep *ConsistencyReport) bool {
	if g.err == nil {
		return false
	}
	msg := fmt.Sprintf("drg: Could not verify %s (%s: %v).", stem, errTypeName(g.err), g.err)
	rep.VerificationErrors = append(rep.VerificationErrors, msg)
	rep.Suggestions = append(rep.Suggestions, strings.TrimSuffix(msg, ".")+". Regenerate graph.yaml / run 'spec-kitty charter resynthesize' and retry.")
	return true
}

func errTypeName(err error) string {
	switch err.(type) {
	case *dct.ConfigError:
		return "ActiveCharterConfigError"
	}
	return "DRGLoadError"
}

func (g *gateRes) tensions(rep *ConsistencyReport) {
	if g.failClosed("tension reconciliation", rep) {
		return
	}
	pairs := map[[2]string]bool{}
	for _, e := range g.full.Edges {
		if e.Relation == dct.RelInTensionWith && g.active[e.Source] && g.active[e.Target] {
			a, b := e.Source, e.Target
			if b < a {
				a, b = b, a
			}
			pairs[[2]string{a, b}] = true
		}
	}
	if len(pairs) == 0 {
		return
	}
	reconciled := map[string]bool{}
	for _, e := range g.reconcileEdges() {
		reconciled[e.Target] = true
	}
	var keys [][2]string
	for p := range pairs {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, p := range keys {
		if !(reconciled[p[0]] && reconciled[p[1]]) {
			rep.UnreconciledTensions = append(rep.UnreconciledTensions, TensionFinding{p[0], p[1]})
		}
	}
}

func (g *gateRes) reconcileEdges() []dct.Edge {
	var out []dct.Edge
	for _, e := range g.full.Edges {
		if e.Relation == dct.RelReconcilesTension && g.active[e.Source] && g.active[e.Target] {
			out = append(out, e)
		}
	}
	return out
}

var enforcementRank = map[string]int{"advisory": 0, "lenient-adherence": 1, "required": 2}

func (g *gateRes) lattice(rep *ConsistencyReport) {
	if g.failClosed("enforcement lattice", rep) {
		return
	}
	repo := g.off.Repo(dct.Directive)
	var out []string
	for _, e := range g.reconcileEdges() {
		sk, sid := dct.SplitURN(e.Source)
		tk, tid := dct.SplitURN(e.Target)
		if sk != "directive" || tk != "directive" {
			continue
		}
		r, o := repo.Get(sid), repo.Get(tid)
		if r == nil || o == nil {
			missing := e.Source
			if r != nil {
				missing = e.Target
			}
			msg := fmt.Sprintf("reconciles_tension edge %s -> %s names %s, which the DRG reports active but the directive repository cannot resolve.", e.Source, e.Target, missing)
			rep.VerificationErrors = append(rep.VerificationErrors, fmt.Sprintf("drg: Could not verify enforcement lattice (RuntimeError: %s).", msg))
			rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("drg: Could not verify enforcement lattice (RuntimeError: %s). Regenerate graph.yaml / run 'spec-kitty charter resynthesize' and retry.", msg))
			return
		}
		re, oe := r.Str("enforcement"), o.Str("enforcement")
		if re == "required" {
			out = append(out, fmt.Sprintf("reconciles_tension %s -> %s: reconciler %s must never be promoted to 'required'.", e.Source, e.Target, e.Source))
			continue
		}
		if enforcementRank[re] < enforcementRank[oe] {
			out = append(out, fmt.Sprintf("reconciles_tension %s -> %s: reconciler rank (%s) is below operand rank (%s).", e.Source, e.Target, re, oe))
		}
	}
	rep.LatticeViolations = append(rep.LatticeViolations, out...)
}

func (g *gateRes) decisionDoc(rep *ConsistencyReport) {
	if g.failClosed("decision-documentation-on-implement gate", rep) {
		return
	}
	const action = "action:software-dev/implement"
	res := dct.ResolveContext(g.full, action, 2)
	var urns []string
	for u := range res.ArtifactURNs {
		if k, _ := dct.SplitURN(u); k == "directive" {
			urns = append(urns, u)
		}
	}
	sort.Strings(urns)
	if len(urns) == 0 {
		return
	}
	repo := g.off.Repo(dct.Directive)
	var out []string
	for _, u := range urns {
		_, id := dct.SplitURN(u)
		d := repo.Get(id)
		if d == nil {
			msg := fmt.Sprintf("%s delivers %s, which the DRG reports resolvable but the directive repository cannot resolve.", action, u)
			rep.VerificationErrors = append(rep.VerificationErrors, fmt.Sprintf("drg: Could not verify decision-documentation-on-implement gate (RuntimeError: %s).", msg))
			rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("drg: Could not verify decision-documentation-on-implement gate (RuntimeError: %s). Regenerate graph.yaml / run 'spec-kitty charter resynthesize' and retry.", msg))
			return
		}
		if d.Str("enforcement") == "required" && strings.Contains(strings.ToLower(d.Str("title")), "decision documentation") {
			out = append(out, fmt.Sprintf("%s delivers %s ('%s'), a required decision-documentation directive; decision documentation must not be delivered to implement.", action, u, d.Str("title")))
		}
	}
	rep.DecisionDocViolations = append(rep.DecisionDocViolations, out...)
}

// unknownArtifactMessage is UnknownArtifactIdError's message.
func unknownArtifactMessage(k dct.Kind, stem string, orgRoots []string) string {
	clause := ""
	if len(orgRoots) > 0 {
		q := make([]string, len(orgRoots))
		for i, r := range orgRoots {
			q[i] = r
		}
		clause = " (or org roots " + strings.Join(q, ", ") + ")"
	}
	return fmt.Sprintf("No %s artifact with config ID %s found under doctrine root %s%s. Searched layers: built-in. Check activated_%s in the charter.yaml activation store selected by `.kittify/config.yaml` (or its legacy inline activations) for a stale or misspelled entry, or run `spec-kitty doctor charter-packs` to verify the doctrine corpus (including any org packs) is intact.",
		string(k), dct.PyRepr(stem), dct.BuiltInRoot(), clause, k.Plural())
}

// resolveArtifactURN is resolve_artifact_urn (built-in + org roots).
func resolveArtifactURN(k dct.Kind, stem string, orgRoots []string) (string, bool) {
	if id := dct.DeclaredIDForStem(k, stem, nil, orgRoots); id != "" {
		return string(k) + ":" + id, true
	}
	if k == dct.Directive {
		if dct.NewOffering("", orgRoots).Repo(dct.Directive).Get(stem) != nil {
			return "directive:" + stem, true
		}
	}
	return "", false
}

// RunConsistencyCheck is run_consistency_check.
func RunConsistencyCheck(repoRoot string) *ConsistencyReport {
	rep := &ConsistencyReport{}
	raw, err := loadRawActivationLists(repoRoot)
	if err != nil {
		body := err.Error()
		if ce, ok := err.(*dct.ConfigError); ok {
			body = ce.Body
		}
		rep.VerificationErrors = []string{"charter.yaml: " + body}
		rep.Suggestions = []string{fmt.Sprintf("charter.yaml: Could not verify config<->charter.yaml activation parity (%s). Fix the .kittify/config.yaml 'charter:' pointer, or restore charter.yaml from version control.", body)}
		return rep
	}
	explicit := len(raw.set) > 0
	gates := func() {
		g := newGateRes(repoRoot)
		g.tensions(rep)
		g.lattice(rep)
		g.decisionDoc(rep)
	}
	if !explicit {
		gates()
		rep.Coherent = len(rep.LatticeViolations) == 0 && len(rep.DecisionDocViolations) == 0 && len(rep.VerificationErrors) == 0
		return rep
	}
	allIDs := map[string]map[string]bool{}
	for _, tok := range raw.order {
		allIDs[tok] = dct.AvailableIDs(tok, nil, nil)
	}
	// unknown references
	for _, tok := range raw.order {
		if !raw.set[tok] {
			continue
		}
		for _, id := range uniqSorted(raw.lists[tok]) {
			if !allIDs[tok][id] {
				rep.UnknownReferences = append(rep.UnknownReferences, tok+"/"+id)
				rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("%s/%s: Not found in doctrine. Run 'charter deactivate %s %s' to remove.", tok, id, tok, id))
			}
		}
	}
	// cross-kind DRG refs
	orgRoots := dct.OrgRoots(repoRoot)
	off := dct.NewOffering(repoRoot, orgRoots)
	if full, err := dct.LoadGraph(repoRoot, orgRoots); err == nil {
		if act, err := dct.LoadActivation(repoRoot); err == nil {
			activated := dct.FilterByActivation(full, act, off)
			reported := map[[2]string]bool{}
			for _, e := range activated.Edges {
				sk, _ := dct.SplitURN(e.Source)
				tk, _ := dct.SplitURN(e.Target)
				switch sk {
				case "directive", "tactic", "styleguide", "toolguide":
				default:
					continue
				}
				if sk == tk {
					continue
				}
				tcli, ok := drgSingularToCLI(tk)
				if !ok {
					continue
				}
				if !raw.set[tcli] || len(uniqSorted(raw.lists[tcli])) > 0 {
					continue
				}
				scli, ok := drgSingularToCLI(sk)
				if !ok {
					scli = sk
				}
				pk := [2]string{scli, tcli}
				if reported[pk] {
					continue
				}
				reported[pk] = true
				entry := tcli + "/<all>"
				dup := false
				for _, m := range rep.MissingFromOffering {
					if m == entry {
						dup = true
					}
				}
				if !dup {
					rep.MissingFromOffering = append(rep.MissingFromOffering, entry)
					rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("%s/<all>: Kind '%s' is referenced by activated '%s' artifacts via DRG edges but its activation set is empty. Run 'charter activate %s <id>' or add --cascade when activating the source.", tcli, tcli, scli, tcli))
				}
			}
		}
	}
	// duplicates
	for _, tok := range raw.order {
		if !raw.set[tok] {
			continue
		}
		seen := map[string]bool{}
		for _, x := range raw.lists[tok] {
			if seen[x] {
				rep.KindViolations = append(rep.KindViolations, fmt.Sprintf("%s/%s: Duplicate entry in activation set.", tok, x))
			}
			seen[x] = true
		}
	}
	// kind violations
	unknown := map[string]bool{}
	for _, u := range rep.UnknownReferences {
		unknown[u] = true
	}
	for _, tok := range raw.order {
		if !raw.set[tok] {
			continue
		}
		for _, id := range uniqSorted(raw.lists[tok]) {
			if unknown[tok+"/"+id] || allIDs[tok][id] {
				continue
			}
			for _, other := range raw.order {
				if other != tok && allIDs[other][id] {
					rep.KindViolations = append(rep.KindViolations, fmt.Sprintf("%s/%s: ID belongs to kind '%s', not '%s'.", tok, id, other, tok))
					break
				}
			}
		}
	}
	checkReferenceParity(repoRoot, raw, rep, orgRoots)
	checkGraphKindParity(repoRoot, raw, rep, orgRoots, off)
	gates()
	rep.Coherent = len(rep.UnknownReferences) == 0 && len(rep.MissingFromOffering) == 0 && len(rep.KindViolations) == 0 &&
		len(rep.ReferenceIDDivergences) == 0 && len(rep.GraphKindGaps) == 0 && len(rep.VerificationErrors) == 0 &&
		len(rep.LatticeViolations) == 0 && len(rep.DecisionDocViolations) == 0
	return rep
}

func checkReferenceParity(repoRoot string, raw *rawLists, rep *ConsistencyReport, orgRoots []string) {
	cfgPath := filepath.Join(repoRoot, ".kittify", "charter", "charter.yaml")
	if src, err := loadRawActivationSourceConfigOnly(repoRoot); err == nil {
		if pv, ok := src.Get("charter"); ok {
			if ptr, ok := pv.(string); ok {
				cfgPath = ptr
				if !filepath.IsAbs(cfgPath) {
					cfgPath = filepath.Join(repoRoot, cfgPath)
				}
			}
		}
	}
	if _, err := os.Stat(cfgPath); err != nil {
		return
	}
	corrupt := func(msg string) {
		rep.VerificationErrors = append(rep.VerificationErrors, "charter.yaml: "+msg)
		rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("charter.yaml: Could not verify config<->references parity (%s). Run 'spec-kitty charter generate' (or 'charter activate --resynthesize') to regenerate .kittify/charter/charter.yaml's catalog, or restore it from version control.", msg))
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		corrupt(fmt.Sprintf("%s could not be parsed: %v", cfgPath, err))
		return
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		corrupt(fmt.Sprintf("%s could not be parsed: %v", cfgPath, err))
		return
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		corrupt(fmt.Sprintf("%s does not contain a YAML mapping at its document root.", cfgPath))
		return
	}
	cat := o.GetObject("catalog")
	if cat == nil {
		corrupt(fmt.Sprintf("%s is missing a valid 'catalog' mapping.", cfgPath))
		return
	}
	rv, _ := cat.Get("references")
	refs, ok := rv.([]any)
	if !ok {
		corrupt(fmt.Sprintf("%s catalog is missing a valid 'references' list.", cfgPath))
		return
	}
	byKind := map[string]map[string]bool{}
	for _, x := range refs {
		e, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		kv, _ := e.Get("kind")
		iv, _ := e.Get("id")
		k, ok1 := kv.(string)
		id, ok2 := iv.(string)
		if !ok1 || !ok2 {
			continue
		}
		_, bare, _ := strings.Cut(id, ":")
		if byKind[k] == nil {
			byKind[k] = map[string]bool{}
		}
		byKind[k][bare] = true
	}
	for _, tok := range raw.order {
		if tok == dct.MissionTypeToken {
			continue
		}
		l := raw.lists[tok]
		if len(l) == 0 {
			continue
		}
		k, err := dct.ParseKind(tok)
		if err != nil {
			continue
		}
		for _, stem := range uniqSorted(l) {
			urn, ok := resolveArtifactURN(k, stem, orgRoots)
			if !ok {
				continue
			}
			_, cid, _ := strings.Cut(urn, ":")
			if !byKind[string(k)][cid] {
				rep.ReferenceIDDivergences = append(rep.ReferenceIDDivergences, tok+"/"+stem)
				rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("%s/%s: Activated in config.yaml but does not resolve in .kittify/charter/charter.yaml's catalog. Run 'spec-kitty charter generate' (or 'charter activate --resynthesize') to regenerate the compiled reference set.", tok, stem))
			}
		}
	}
	if raw.set["paradigm"] {
		known := map[string]bool{}
		for _, x := range raw.lists["paradigm"] {
			known[x] = true
		}
		var ids []string
		for id := range byKind["paradigm"] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !known[id] {
				rep.ReferenceIDDivergences = append(rep.ReferenceIDDivergences, "paradigm/"+id)
				rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("paradigm/%s: Resolves in .kittify/charter/charter.yaml's catalog but is not in config.activated_paradigms. Run 'charter deactivate paradigm %s' or reconcile config.yaml.", id, id))
			}
		}
	}
}

func loadRawActivationSourceConfigOnly(repoRoot string) (*jsonx.Object, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".kittify", "config.yaml"))
	if err != nil {
		return jsonx.NewObject(), nil
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	if o, ok := v.(*jsonx.Object); ok {
		return o, nil
	}
	return jsonx.NewObject(), nil
}

func checkGraphKindParity(repoRoot string, raw *rawLists, rep *ConsistencyReport, orgRoots []string, off *dct.Offering) {
	full, err := dct.LoadGraph(repoRoot, orgRoots)
	var act *dct.Activation
	if err == nil {
		act, err = dct.LoadActivation(repoRoot)
	}
	if err != nil {
		msg := fmt.Sprintf("drg: Could not verify config<->graph kind parity (%s: %v).", errTypeName(err), err)
		rep.VerificationErrors = append(rep.VerificationErrors, msg)
		rep.Suggestions = append(rep.Suggestions, strings.TrimSuffix(msg, ".")+". Regenerate graph.yaml / run 'spec-kitty charter resynthesize' and retry.")
		return
	}
	surviving := dct.FilterByActivation(full, act, off).NodeURNs()
	for _, kv := range cliKindsToDRG {
		tok := kv[0]
		l := raw.lists[tok]
		if len(l) == 0 {
			continue
		}
		k, _ := dct.ParseKind(tok)
		for _, stem := range uniqSorted(l) {
			urn, ok := resolveArtifactURN(k, stem, orgRoots)
			if !ok {
				msg := unknownArtifactMessage(k, stem, orgRoots)
				rep.VerificationErrors = append(rep.VerificationErrors, fmt.Sprintf("%s/%s: %s", tok, stem, msg))
				rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("%s/%s: Could not resolve to a canonical doctrine id while checking config<->graph parity (%s). Run 'charter deactivate %s %s' to remove, or fix the stem.", tok, stem, msg, tok, stem))
				continue
			}
			if !surviving[urn] {
				rep.GraphKindGaps = append(rep.GraphKindGaps, tok+"/"+stem)
				rep.Suggestions = append(rep.Suggestions, fmt.Sprintf("%s/%s: Activated in config.yaml but does not survive in the activation-filtered DRG graph. Regenerate graph.yaml / run 'spec-kitty charter resynthesize' and check 'activated_kinds' in config.yaml.", tok, stem))
			}
		}
	}
}
