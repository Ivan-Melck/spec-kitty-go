package charter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ActionBundle is upstream _ActionGovernanceBundle.
type ActionBundle struct {
	Mission string
	IDs     map[dct.Kind][]string
	Merged  *dct.Graph // nil when the mission type is unresolved
	Roots   []string
	Bridge  []string
	Offer   *dct.Offering
	// Warning is the logged "DRG action resolution skipped" message when the
	// DRG failed to load or the project overlay broke validation.
	Warning string
	// Err is an uncaught DRGValidationError (built-in/org layer invalid).
	Err error
}

var bundleKinds = []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure, dct.Asset, dct.GlossaryPack}

func graphIDs(g *dct.Graph, kind dct.Kind) map[string]bool {
	out := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Kind == string(kind) {
			out[n.ID()] = true
		}
	}
	return out
}

// ResolveActionBundle resolves the action doctrine grain for
// action:<missionType>/<action> (empty grain when missionType is "").
func ResolveActionBundle(root, action, missionType string, depth int) *ActionBundle {
	orgRoots := dct.OrgRoots(root)
	off := dct.NewOffering(root, orgRoots)
	langs, langKnown := InferRepoLanguages(root, nil, false)
	if langKnown {
		off.Languages = langs
		if off.Languages == nil {
			off.Languages = []string{}
		}
	}
	b := &ActionBundle{Mission: missionType, IDs: map[dct.Kind][]string{}, Offer: off}
	if missionType == "" {
		return b
	}
	g, err := dct.LoadValidatedGraph(root, orgRoots)
	if err != nil {
		// load_validated_graph(project_degrade=True): a load error or a
		// validation error introduced only by the project overlay degrades
		// to an empty bundle with a warning; any other validation error
		// propagates.
		var ve *dct.DRGValidationError
		if errors.As(err, &ve) && !projectOnlyErrors(root, orgRoots, ve) {
			b.Err = err
			return b
		}
		b.Warning = fmt.Sprintf("DRG action resolution skipped for %s/%s: %v. Charter-level selections still render.", missionType, action, err)
		return b
	}
	act, err := dct.LoadActivation(root)
	if err != nil {
		return b
	}
	merged := dct.FilterByActivation(g, act, off)
	pick := func(k dct.Kind) map[string]bool {
		if a := act.Activated(k); a != nil {
			out := map[string]bool{}
			for id := range a {
				out[id] = true
			}
			return out
		}
		out := graphIDs(merged, k)
		for _, id := range dct.NewOffering("", nil).Repo(k).IDs() {
			out[id] = true
		}
		return out
	}
	directives := map[string]bool{}
	for id := range pick(dct.Directive) {
		directives[dct.NormalizeDirectiveID(id)] = true
	}
	tactics, paradigms := pick(dct.Tactic), pick(dct.Paradigm)
	local := map[string]bool{}
	if act.Activated(dct.Directive) != nil {
		for _, n := range merged.Nodes {
			if n.Kind == "directive" && off.Repo(dct.Directive).Provenance(n.ID()) == dct.LayerProject {
				local[n.ID()] = true
			}
		}
	}
	actionURN := "action:" + missionType + "/" + action
	rc := dct.ResolveContext(merged, actionURN, depth)

	// selected-closure widening (_classify_artifact_urns)
	var start []string
	for d := range directives {
		start = append(start, "directive:"+d)
	}
	for d := range local {
		start = append(start, "directive:"+d)
	}
	for t := range tactics {
		start = append(start, "tactic:"+t)
	}
	for p := range paradigms {
		start = append(start, "paradigm:"+p)
	}
	sort.Strings(start)
	closure := dct.WalkEdges(merged, start, map[string]bool{dct.RelRequires: true, dct.RelSuggests: true}, -1)
	urns := map[string]bool{}
	for u := range rc.ArtifactURNs {
		urns[u] = true
	}
	owners := map[string]map[string]bool{}
	for _, e := range merged.Edges {
		if e.Relation == dct.RelScope && len(e.Source) > 7 && e.Source[:7] == "action:" {
			if owners[e.Target] == nil {
				owners[e.Target] = map[string]bool{}
			}
			owners[e.Target][e.Source] = true
		}
	}
	for u := range closure {
		n := merged.Node(u)
		if n == nil {
			continue
		}
		switch n.Kind {
		case "directive", "tactic", "styleguide", "toolguide":
		default:
			continue
		}
		other := false
		for o := range owners[u] {
			if o != actionURN {
				other = true
			}
		}
		if !other {
			urns[u] = true
		}
	}
	for _, u := range dct.SortedKeys(urns) {
		n := merged.Node(u)
		if n == nil {
			continue
		}
		k := dct.Kind(n.Kind)
		ok := false
		for _, bk := range bundleKinds {
			ok = ok || bk == k
		}
		if !ok {
			continue
		}
		id := n.ID()
		if k == dct.Directive && !directives[id] && !local[id] {
			continue
		}
		if langKnown && (k == dct.Tactic || k == dct.Styleguide || k == dct.Toolguide || k == dct.Procedure) && off.Repo(k).ScopeFiltered[id] {
			continue
		}
		b.IDs[k] = append(b.IDs[k], id)
	}
	b.Merged = merged
	b.Roots = []string{actionURN}
	for _, d := range dct.SortedKeys(unionSets(directives, local)) {
		b.Roots = append(b.Roots, "directive:"+d)
	}
	for _, t := range dct.SortedKeys(tactics) {
		b.Roots = append(b.Roots, "tactic:"+t)
	}
	for _, p := range dct.SortedKeys(paradigms) {
		b.Roots = append(b.Roots, "paradigm:"+p)
	}
	b.Bridge = dct.SortedKeys(rc.ArtifactURNs)
	return b
}

func unionSets(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// statedDefaultWhen is STATED_DEFAULT_WHEN.
const statedDefaultWhen = "Fetch when this artefact's guidance applies to the work at hand (no specific trigger authored yet)."

func edgeRef(e dct.Edge) *jsonx.Object {
	_, id := dct.SplitURN(e.Target)
	var when, reason any
	if e.When != nil && *e.When != "" {
		when = *e.When
	} else if e.Relation == dct.RelSuggests {
		when = statedDefaultWhen
	} else if e.When != nil {
		when = *e.When
	}
	if e.Reason != nil {
		reason = *e.Reason
	}
	return jsonx.NewObject("id", id, "relation", e.Relation, "when", when, "reason", reason)
}

func requiresClosure(g *dct.Graph, roots []string) map[string]bool {
	seen := map[string]bool{}
	var stack []string
	for _, r := range roots {
		if !seen[r] {
			seen[r] = true
			stack = append(stack, r)
		}
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range g.EdgesFrom(n, dct.RelRequires) {
			if !seen[e.Target] {
				seen[e.Target] = true
				stack = append(stack, e.Target)
			}
		}
	}
	return seen
}

// ArtifactDTO is upstream artifact_to_dict.
func ArtifactDTO(a *dct.Artifact, id string) *jsonx.Object {
	if a == nil {
		return jsonx.NewObject("id", id, "source", dct.LayerBuiltIn)
	}
	o := jsonx.NewObject("id", a.ID, "source", a.Source)
	title := a.Data.GetString("title")
	if title == "" {
		title = a.Data.GetString("name")
	}
	if title != "" {
		o.Set("title", title)
	}
	sum := a.Data.GetString("intent")
	if sum == "" {
		sum = a.Data.GetString("purpose")
	}
	if sum != "" {
		o.Set("summary", sum)
	}
	return o
}

// disclosure renders the typed arrays + references for a resolved bundle.
func (b *ActionBundle) disclosure(includeAll bool) *jsonx.Object {
	out := &jsonx.Object{}
	inline := requiresClosure(b.Merged, b.Roots)
	delivered := map[string]bool{}
	for _, k := range []dct.Kind{dct.Directive, dct.Tactic, dct.Styleguide, dct.Toolguide, dct.Procedure} {
		var arr []any
		repo := b.Offer.Repo(k)
		for _, id := range b.IDs[k] {
			a := repo.Get(id)
			e := ArtifactDTO(a, id)
			urn := string(k) + ":" + id
			delivered[urn] = true
			refs := []any{}
			for _, ed := range b.Merged.EdgesFrom(urn, "") {
				refs = append(refs, edgeRef(ed))
			}
			e.Set("references", refs)
			isInline := includeAll || inline[urn]
			if isInline {
				e.Set("delivery", "inline")
				if a != nil {
					e.Set("body", ArtifactBody(k, a.Data))
				}
			} else {
				e.Set("delivery", "link")
			}
			arr = append(arr, e)
		}
		if arr == nil {
			arr = []any{}
		}
		out.Set(k.Plural(), arr)
	}
	for _, k := range []dct.Kind{dct.Asset, dct.GlossaryPack} {
		for _, id := range b.IDs[k] {
			delivered[string(k)+":"+id] = true
		}
	}
	sources := map[string]bool{}
	for _, s := range b.Roots {
		sources[s] = true
	}
	for s := range delivered {
		sources[s] = true
	}
	for _, s := range b.Bridge {
		sources[s] = true
	}
	refs := []any{}
	seen := map[string]bool{}
	for _, s := range dct.SortedKeys(sources) {
		for _, e := range b.Merged.EdgesFrom(s, "") {
			if !delivered[e.Target] {
				continue
			}
			_, id := dct.SplitURN(e.Target)
			key := id + "\x00" + e.Relation
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, edgeRef(e))
		}
	}
	out.Set("references", refs)
	return out
}

// projectOnlyErrors reports whether ve is a DRGProjectValidationError: the
// project overlay exists and none of its errors occur without it.
func projectOnlyErrors(root string, orgRoots []string, ve *dct.DRGValidationError) bool {
	pd := filepath.Join(root, filepath.FromSlash(dct.ProjectPackRoot))
	if st, err := os.Stat(pd); err != nil || !st.IsDir() || !dct.HasGraphFiles(pd) {
		return false
	}
	base, err := dct.LoadGraph("", orgRoots)
	if err != nil {
		return false
	}
	baseErrs := map[string]bool{}
	for _, e := range dct.ValidateGraph(base) {
		baseErrs[e] = true
	}
	for _, e := range ve.Errors {
		if baseErrs[e] {
			return false
		}
	}
	return true
}
