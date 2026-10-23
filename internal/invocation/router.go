package invocation

import (
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// Registry is ProfileRegistry: the dispatch routing catalog (activation-gated
// doctrine profiles of every layer, overlaid with the ungated legacy
// .kittify/profiles project layer).
type Registry struct {
	merged map[string]*dct.Profile
}

// NewRegistry builds the routing catalog for root.
func NewRegistry(root string) *Registry {
	r := &Registry{merged: map[string]*dct.Profile{}}
	off := dct.NewOffering(root, dct.OrgRoots(root))
	// build_active_charter_service: language-scope filter with
	// active_languages=infer_repo_languages(repo_root) (None admits all).
	if langs, known := charter.InferRepoLanguages(root, nil, false); known {
		off.Languages = langs
		if off.Languages == nil {
			off.Languages = []string{}
		}
	}
	act, _ := dct.ActivatedProfiles(root)
	for _, a := range off.Repo(dct.AgentProfile).List() {
		if act != nil && !act[a.ID] {
			continue
		}
		r.merged[a.ID] = dct.AsProfile(a)
	}
	if st, err := os.Stat(dct.LegacyProfilesDir(root)); err == nil && st.IsDir() {
		legacy := dct.LoadRepository(dct.AgentProfile, dct.LayerDirs{ProjectDir: dct.LegacyProfilesDir(root)})
		for _, a := range legacy.List() {
			if a.Source == dct.LayerProject {
				r.merged[a.ID] = dct.AsProfile(a)
			}
		}
	}
	return r
}

// IDs returns the sorted catalog ids.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.merged))
	for id := range r.merged {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// List returns profiles sorted by id.
func (r *Registry) List() []*dct.Profile {
	var out []*dct.Profile
	for _, id := range r.IDs() {
		out = append(out, r.merged[id])
	}
	return out
}

// Get returns a profile or nil.
func (r *Registry) Get(id string) *dct.Profile { return r.merged[id] }

// ProfileNotFoundError is raised for an unknown --profile.
type ProfileNotFoundError struct {
	ID        string
	Available []string
}

func (e *ProfileNotFoundError) Error() string {
	return "Profile '" + e.ID + "' not found. Available: " + pyListRepr(e.Available)
}

// Resolve returns the profile or a ProfileNotFoundError.
func (r *Registry) Resolve(id string) (*dct.Profile, error) {
	if p := r.merged[id]; p != nil {
		return p, nil
	}
	return nil, &ProfileNotFoundError{ID: id, Available: r.IDs()}
}

// stopWords is router.STOP_WORDS.
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an the this that these those is are was were be been being have has had do does did will would could should may might must can please kindly some for me us our my to and or in on at of with by") {
		stopWords[w] = true
	}
}

type verbEntry struct{ action, role string }

// canonicalVerbs is CANONICAL_VERB_MAP.
var canonicalVerbs = map[string]verbEntry{}

func init() {
	add := func(action, role, words string) {
		for _, w := range strings.Fields(words) {
			canonicalVerbs[w] = verbEntry{action, role}
		}
	}
	add("implement", "implementer", "implement build code develop create write generate produce output refine improve fix patch repair debug")
	add("review", "reviewer", "review check inspect evaluate audit assess")
	add("plan", "planner", "plan decompose break outline schedule prioritize triage rank order")
	add("specify", "architect", "specify spec define requirements scope")
	add("design", "designer", "design mockup prototype wireframe")
	add("analyze", "researcher", "analyze investigate research explore study summarize synthesize compile report")
	add("curate", "curator", "curate classify organize tag validate verify")
	add("coordinate", "manager", "coordinate manage delegate monitor track")
}

// NormalizeTokens is _normalize_tokens: lowercase, split on [\s\W]+, drop
// stop words.
func NormalizeTokens(text string) []string {
	parts := strings.FieldsFunc(pyLower(text), func(r rune) bool { return !isWordRune(r) })
	var out []string
	for _, p := range parts {
		if p != "" && !stopWords[p] {
			out = append(out, p)
		}
	}
	return out
}

// Decision is RouterDecision.
type Decision struct {
	ProfileID    string
	Action       string
	Confidence   string
	MatchReason  string
	Alternatives []Alternative
}

// Alternative is one losing candidate.
type Alternative struct {
	ProfileID, Action, Confidence, MatchReason string
}

// RouterError is RouterAmbiguityError.
type RouterError struct {
	Request    string
	Code       string // ROUTER_AMBIGUOUS | ROUTER_NO_MATCH | PROFILE_NOT_FOUND
	Candidates []Alternative
	Suggestion string
}

func (e *RouterError) Error() string { return e.Code + ": " + e.Suggestion }

// roleDefaultVerb is DEFAULT_ROLE_CAPABILITIES[role].canonical_verbs[0].
func roleDefaultVerb(role, fallback string) string {
	if v := dct.RoleVerbs[role]; len(v) > 0 {
		return v[0]
	}
	return fallback
}

// ActionFromTokens is _derive_action_from_tokens.
func ActionFromTokens(text, role string) string {
	for _, t := range NormalizeTokens(text) {
		if e, ok := canonicalVerbs[t]; ok {
			return e.action
		}
	}
	return roleDefaultVerb(role, "review")
}

type candidate struct {
	Alternative
}

// Route is ActionRouter.route (no profile hint).
func Route(reg *Registry, text string) (*Decision, error) {
	profiles := reg.List()
	if len(profiles) == 0 {
		return nil, &RouterError{text, "ROUTER_NO_MATCH", nil,
			"No profiles available. Run 'spec-kitty charter activate agent-profile <id>' to activate one, or 'spec-kitty profiles list --all' to see the catalog."}
	}
	tokens := NormalizeTokens(text)
	tokSet := map[string]bool{}
	for _, t := range tokens {
		tokSet[t] = true
	}
	type vm struct{ role, action, token string }
	var verbMatches []vm
	seenRole := map[string]bool{}
	for _, t := range tokens {
		if e, ok := canonicalVerbs[t]; ok && !seenRole[e.role] {
			seenRole[e.role] = true
			verbMatches = append(verbMatches, vm{e.role, e.action, t})
		}
	}
	type km struct{ id, action, kw string }
	var kwMatches []km
	for _, p := range profiles {
		kws := append([]string(nil), p.DomainKeywords()...)
		have := map[string]bool{}
		for _, k := range kws {
			have[k] = true
		}
		for _, cv := range p.CollabVerbs() {
			if !have[cv] {
				kws = append(kws, cv)
				have[cv] = true
			}
		}
		for _, kw := range kws {
			if tokSet[pyLower(kw)] {
				kwMatches = append(kwMatches, km{p.ID, roleDefaultVerb(p.Role(), "review"), kw})
			}
		}
	}
	var cands []candidate
	for _, m := range verbMatches {
		for _, p := range profiles {
			if p.Role() == m.role {
				cands = append(cands, candidate{Alternative{p.ID, m.action, "canonical_verb", "token '" + m.token + "' matched " + m.role + " canonical verb"}})
			}
		}
	}
	existing := map[string]bool{}
	for _, c := range cands {
		existing[c.ProfileID] = true
	}
	for _, m := range kwMatches {
		if !existing[m.id] {
			cands = append(cands, candidate{Alternative{m.id, m.action, "domain_keyword", "domain keyword '" + m.kw + "' matched"}})
			existing[m.id] = true
		}
	}
	if len(cands) == 0 {
		return nil, &RouterError{text, "ROUTER_NO_MATCH", nil,
			"No profile matched. Use 'spec-kitty dispatch \"<request>\" --profile <id>' or run 'spec-kitty profiles list'."}
	}
	var pool []int
	for i, c := range cands {
		if c.Confidence == "canonical_verb" {
			pool = append(pool, i)
		}
	}
	if len(pool) == 0 {
		for i := range cands {
			pool = append(pool, i)
		}
	}
	alts := func(win int) []Alternative {
		out := []Alternative{}
		for i, c := range cands {
			if i != win {
				out = append(out, c.Alternative)
			}
		}
		return out
	}
	if len(pool) == 1 {
		c := cands[pool[0]]
		return &Decision{c.ProfileID, c.Action, c.Confidence, c.MatchReason, alts(pool[0])}, nil
	}
	prio := func(i int) int {
		if p := reg.Get(cands[i].ProfileID); p != nil {
			return p.RoutingPriority()
		}
		return 0
	}
	sort.SliceStable(pool, func(a, b int) bool { return prio(pool[a]) > prio(pool[b]) })
	top := prio(pool[0])
	var tops []int
	for _, i := range pool {
		if prio(i) == top {
			tops = append(tops, i)
		}
	}
	if len(tops) == 1 {
		c := cands[tops[0]]
		return &Decision{c.ProfileID, c.Action, c.Confidence, c.MatchReason + " (selected by routing_priority)", alts(tops[0])}, nil
	}
	var amb []Alternative
	for _, i := range tops {
		amb = append(amb, cands[i].Alternative)
	}
	return nil, &RouterError{text, "ROUTER_AMBIGUOUS", amb,
		"Multiple profiles matched. Use 'spec-kitty dispatch \"<request>\" --profile <id>' or run 'spec-kitty profiles list'."}
}

// GenericAgentID is the empty-charter fallback profile.
const GenericAgentID = "generic-agent"

const fallbackMatchReason = "empty charter: no compiled charter bundle (.kittify/charter/charter.yaml), no org/project pack, and no explicit agent-profile activation found"

// IsCharterEmpty is empty_charter.is_charter_empty.
func IsCharterEmpty(root string) bool {
	if _, err := os.Stat(root + "/.kittify/charter/charter.yaml"); err == nil {
		return false
	}
	// PackContext.org_roots: every declared pack root, fetched or not.
	if len(dct.OrgPacks(root)) > 0 {
		return false
	}
	act, err := dct.ActivatedProfiles(root)
	if err != nil {
		return false
	}
	return act == nil
}

// GenericFallback is resolve_generic_fallback (nil when the charter is
// configured).
func GenericFallback(root, text string) *Decision {
	if !IsCharterEmpty(root) {
		return nil
	}
	action := ""
	for _, t := range NormalizeTokens(text) {
		if e, ok := canonicalVerbs[t]; ok {
			action = e.action
			break
		}
	}
	if action == "" {
		action = roleDefaultVerb("implementer", "implement")
	}
	return &Decision{GenericAgentID, action, "generic_fallback", fallbackMatchReason, []Alternative{}}
}
