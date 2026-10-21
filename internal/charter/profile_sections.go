package charter

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file ports charter.activation.context_renderers.profile_sections,
// fetch_stanza and token_budget: the profile-channel sections a dispatch
// capsule (and any profile-aware render) carries, and the NFR-001 budget.

const (
	defaultWhenClause    = "are about to apply a code change"
	profileInlineLimit   = 2400
	budgetDefault        = 40000
	procedureWhenClause  = "are about to run this procedure"
	profileCitationsWhen = "need to consult the profile-cited directives and tactics"
)

var (
	leadInRe     = regexp.MustCompile(`(?i)^(?:are\s+about\s+to|need\s+to|encounter|introduce|rename|review)\b`)
	leadingWhen  = regexp.MustCompile(`(?i)^when\s+`)
	gerundLeadRe = regexp.MustCompile(`^[A-Za-z]+ing\b`)
)

// normalizeWhenClause is fetch_stanza._normalize_when_clause.
func normalizeWhenClause(clause string) string {
	s := strings.TrimSpace(clause)
	if s == "" {
		return defaultWhenClause
	}
	if loc := leadingWhen.FindStringIndex(s); loc != nil {
		s = s[loc[1]:]
	}
	if leadInRe.MatchString(s) {
		return s
	}
	if gerundLeadRe.MatchString(s) {
		return defaultWhenClause
	}
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return defaultWhenClause
	}
	r, n := utf8.DecodeRuneInString(s)
	return "need to " + strings.ToLower(string(r)) + s[n:]
}

// fetchStanzaLines is fetch_stanza_lines.
func fetchStanzaLines(selector, when, indent string) []string {
	clause := strings.TrimSpace(when)
	if clause == "" {
		clause = defaultWhenClause
	}
	clause = normalizeWhenClause(clause)
	return []string{
		indent + "Run: spec-kitty charter context --include " + selector,
		indent + "When you " + clause + ", run this command and apply the returned rule.",
	}
}

func budgetEstimate(lines []string) int {
	n := 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l) + 1
	}
	return n
}

func strField(o *jsonx.Object, k string) (string, bool) {
	if o == nil {
		return "", false
	}
	v, ok := o.Get(k)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

func listField(o *jsonx.Object, k string) []any {
	if o == nil {
		return nil
	}
	return o.GetList(k)
}

func itemText(x any) string {
	if s, ok := x.(string); ok {
		return s
	}
	return fmt.Sprint(x)
}

// directiveBody is _format_inline_directive_body.
func directiveBody(d *jsonx.Object) []string {
	var out []string
	if s, ok := strField(d, "intent"); ok && strings.TrimSpace(s) != "" {
		out = append(out, "    Intent: "+strings.TrimSpace(s))
	}
	if s, ok := strField(d, "scope"); ok && strings.TrimSpace(s) != "" {
		out = append(out, "    Scope: "+strings.TrimSpace(s))
	}
	for _, f := range [][2]string{{"Procedures", "procedures"}, {"Integrity rules", "integrity_rules"}, {"Validation criteria", "validation_criteria"}} {
		items := listField(d, f[1])
		if len(items) == 0 {
			continue
		}
		out = append(out, "    "+f[0]+":")
		for _, it := range items {
			out = append(out, "      - "+itemText(it))
		}
	}
	return out
}

// namedBody is format_inline_named_body (tactics, procedures).
func namedBody(a *jsonx.Object) []string {
	var out []string
	if s, ok := strField(a, "name"); ok && s != "" {
		out = append(out, "    Name: "+s)
	}
	if s, ok := strField(a, "purpose"); ok && strings.TrimSpace(s) != "" {
		out = append(out, "    Purpose: "+strings.TrimSpace(s))
	}
	steps := listField(a, "steps")
	if len(steps) > 0 {
		out = append(out, "    Steps:")
		for _, st := range steps {
			so, isObj := st.(*jsonx.Object)
			if !isObj {
				out = append(out, "      - "+itemText(st))
				continue
			}
			title, _ := strField(so, "title")
			desc, hasDesc := strField(so, "description")
			header := title
			if header == "" {
				header = desc
			}
			out = append(out, "      - "+header)
			if strings.TrimSpace(title) != "" && hasDesc && strings.TrimSpace(desc) != "" {
				out = append(out, "        "+strings.TrimSpace(desc))
			}
		}
	}
	return out
}

var directiveCodeRe = regexp.MustCompile(`^DIRECTIVE_\d+$`)
var numericCodeRe = regexp.MustCompile(`^(\d+)(?:[-_].*)?$`)

// profileDirectiveCode is _format_profile_directive_code.
func profileDirectiveCode(raw string) string {
	t := strings.TrimSpace(raw)
	if directiveCodeRe.MatchString(t) {
		return t
	}
	if m := numericCodeRe.FindStringSubmatch(t); m != nil {
		n := m[1]
		for len(n) < 3 {
			n = "0" + n
		}
		return "DIRECTIVE_" + n
	}
	return t
}

// catalogMissLines is format_catalog_miss_stanza for a MISSING_ARTIFACT /
// TYPO_SUSPECTED miss.
func catalogMissLines(kind, id string, repo *dct.Repository) []string {
	sel := kind + ":" + id
	lines := []string{"    (catalog entry not found for " + sel + ")"}
	if s := closeMatch(id, repo.IDs()); s != "" {
		lines = append(lines, "      Cause: typo_suspected",
			"      Suggestion: did you mean '"+s+"'? Update the charter selection to match the canonical ID.")
		return lines
	}
	return append(lines, "      Cause: missing_artifact",
		"      Suggestion: confirm the artifact exists (check project, org, and built-in layers) or run `spec-kitty charter validate` to check for a silent schema validation drop.")
}

// closeMatch is difflib.get_close_matches(id, ids, n=1, cutoff=0.75).
func closeMatch(word string, cands []string) string {
	best, bestScore := "", 0.0
	for _, c := range cands {
		r := seqRatio(word, c)
		if r >= 0.75 && r > bestScore {
			best, bestScore = c, r
		}
	}
	return best
}

// seqRatio approximates difflib.SequenceMatcher.ratio via LCS-of-blocks.
func seqRatio(a, b string) float64 {
	if len(a)+len(b) == 0 {
		return 1
	}
	var m func(a, b string) int
	m = func(a, b string) int {
		if a == "" || b == "" {
			return 0
		}
		bi, bj, bk := 0, 0, 0
		for i := 0; i < len(a); i++ {
			for j := 0; j < len(b); j++ {
				k := 0
				for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
					k++
				}
				if k > bk {
					bi, bj, bk = i, j, k
				}
			}
		}
		if bk == 0 {
			return 0
		}
		return bk + m(a[:bi], b[:bj]) + m(a[bi+bk:], b[bj+bk:])
	}
	return 2 * float64(m(a, b)) / float64(len(a)+len(b))
}

type selEntry struct{ id, rationale string }

// selectorRefs is render_profile_selector_refs.
func selectorRefs(header string, entries []selEntry, repo *dct.Repository, kind, when string, whenByID map[string]string, body func(*jsonx.Object) []string) []string {
	lines := []string{header}
	for _, e := range entries {
		id := strings.TrimSpace(e.id)
		h := "  - " + id
		if e.rationale != "" {
			h += ": " + e.rationale
		}
		lines = append(lines, h)
		a := repo.Get(id)
		if a == nil {
			lines = append(lines, catalogMissLines(kind, id, repo)...)
			continue
		}
		var bl []string
		if body != nil {
			bl = body(a.Data)
		}
		if len(bl) > 0 && budgetEstimate(bl) <= profileInlineLimit {
			lines = append(lines, bl...)
			continue
		}
		w := when
		if v := whenByID[id]; v != "" {
			w = v
		}
		lines = append(lines, fetchStanzaLines(kind+":"+id, w, "    ")...)
	}
	return lines
}

func refEntries(p *dct.Profile, key string) []selEntry {
	var out []selEntry
	for _, x := range listField(p.Data, key) {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		id, _ := strField(o, "id")
		r, _ := strField(o, "rationale")
		out = append(out, selEntry{id, r})
	}
	return out
}

// profileRenderCtx bundles the ungated offering + DRG the renderers use.
type profileRenderCtx struct {
	off   *dct.Offering
	graph *dct.Graph
}

func (c *profileRenderCtx) channelReached(id string) map[string]bool {
	if c.graph == nil {
		return nil
	}
	seed := "agent_profile:" + id
	n := c.graph.Node(seed)
	if n == nil || n.Kind != "agent_profile" {
		return nil
	}
	v := dct.WalkEdges(c.graph, []string{seed}, map[string]bool{dct.RelRequires: true, dct.RelSpecializesFrom: true, dct.RelSuggests: true}, -1)
	delete(v, seed)
	return v
}

func kindOf(urn string) string { k, _ := dct.SplitURN(urn); return k }
func bareID(urn string) string {
	_, id := dct.SplitURN(urn)
	return id
}

func renderProfileDirectives(p *dct.Profile, c *profileRenderCtx) []string {
	refs := listField(p.Data, "directive-references")
	if len(refs) == 0 {
		return nil
	}
	repo := c.off.Repo(dct.Directive)
	lines := []string{"Profile-Cited Directives (" + p.ID + "):"}
	for _, x := range refs {
		o, _ := x.(*jsonx.Object)
		rawCode, _ := o.Get("code")
		code := profileDirectiveCode(itemText(rawCode))
		title, _ := strField(o, "name")
		rat, _ := strField(o, "rationale")
		h := "  - " + code + ": " + title
		if rat != "" {
			h += " — " + rat
		}
		lines = append(lines, h)
		a := repo.Get(code)
		if a == nil {
			lines = append(lines, catalogMissLines("directive", code, repo)...)
			continue
		}
		bl := directiveBody(a.Data)
		if len(bl) > 0 && budgetEstimate(bl) <= profileInlineLimit {
			lines = append(lines, bl...)
		} else {
			lines = append(lines, fetchStanzaLines("directive:"+code, defaultWhenClause, "    ")...)
		}
	}
	return lines
}

func renderProfileTactics(p *dct.Profile, c *profileRenderCtx) []string {
	e := refEntries(p, "tactic-references")
	if len(e) == 0 {
		return nil
	}
	return selectorRefs("Profile-Cited Tactics ("+p.ID+"):", e, c.off.Repo(dct.Tactic), "tactic", defaultWhenClause, nil, namedBody)
}

func renderProfileStyleguides(p *dct.Profile, c *profileRenderCtx) []string {
	e := refEntries(p, "styleguide-references")
	if len(e) == 0 {
		return nil
	}
	return selectorRefs("Profile-Cited Styleguides ("+p.ID+"):", e, c.off.Repo(dct.Styleguide), "styleguide", defaultWhenClause, nil, nil)
}

func renderProfileToolguides(p *dct.Profile, c *profileRenderCtx) []string {
	e := refEntries(p, "toolguide-references")
	if len(e) == 0 {
		return nil
	}
	return selectorRefs("Profile-Cited Toolguides ("+p.ID+"):", e, c.off.Repo(dct.Toolguide), "toolguide", defaultWhenClause, nil, nil)
}

func renderProfileProcedures(p *dct.Profile, c *profileRenderCtx) []string {
	var ids []string
	for u := range c.channelReached(p.ID) {
		if strings.HasPrefix(u, "procedure:") {
			ids = append(ids, strings.TrimPrefix(u, "procedure:"))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	var e []selEntry
	for _, id := range ids {
		e = append(e, selEntry{id, ""})
	}
	return selectorRefs("Profile-Resolved Procedures ("+p.ID+"):", e, c.off.Repo(dct.Procedure), "procedure", procedureWhenClause, nil, namedBody)
}

var suggestedKinds = []struct {
	kind  string
	k     dct.Kind
	title string
}{
	{"paradigm", dct.Paradigm, "Paradigms"},
	{"directive", dct.Directive, "Directives"},
	{"tactic", dct.Tactic, "Tactics"},
	{"styleguide", dct.Styleguide, "Styleguides"},
	{"toolguide", dct.Toolguide, "Toolguides"},
	{"procedure", dct.Procedure, "Procedures"},
}

type linkRef struct{ id, when, reason string }

// profileChannelReferences is progressive_disclosure.profile_channel_references.
func profileChannelReferences(g *dct.Graph, seed string, reached, delivered map[string]bool) []linkRef {
	// requires-closure of the seed (eager) is excluded from the link set.
	inline := map[string]bool{seed: true}
	stack := []string{seed}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range g.EdgesFrom(n, dct.RelRequires) {
			if !inline[e.Target] {
				inline[e.Target] = true
				stack = append(stack, e.Target)
			}
		}
	}
	link := map[string]bool{}
	for u := range delivered {
		if !inline[u] {
			link[u] = true
		}
	}
	sources := map[string]bool{seed: true}
	for u := range link {
		sources[u] = true
	}
	for u := range reached {
		sources[u] = true
	}
	var srcs []string
	for s := range sources {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	type key struct{ id, rel string }
	seen := map[key]bool{}
	var out []linkRef
	for _, s := range srcs {
		for _, e := range g.EdgesFrom(s, "") {
			if !link[e.Target] {
				continue
			}
			k := key{bareID(e.Target), e.Relation}
			if seen[k] {
				continue
			}
			seen[k] = true
			when := ""
			if e.When != nil {
				when = *e.When
			}
			if when == "" && e.Relation == dct.RelSuggests {
				when = statedDefaultWhen
			}
			reason := ""
			if e.Reason != nil {
				reason = *e.Reason
			}
			out = append(out, linkRef{bareID(e.Target), when, reason})
		}
	}
	return out
}

func renderProfileSuggested(p *dct.Profile, c *profileRenderCtx) []string {
	reached := c.channelReached(p.ID)
	if len(reached) == 0 {
		return nil
	}
	delivered := map[string]bool{}
	for u := range reached {
		switch kindOf(u) {
		case "paradigm", "directive", "tactic", "styleguide", "toolguide", "procedure":
			delivered[u] = true
		}
	}
	if len(delivered) == 0 {
		return nil
	}
	refs := profileChannelReferences(c.graph, "agent_profile:"+p.ID, reached, delivered)
	var lines []string
	for _, sk := range suggestedKinds {
		ids := map[string]bool{}
		for u := range delivered {
			if kindOf(u) == sk.kind {
				ids[bareID(u)] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		var entries []selEntry
		whenBy := map[string]string{}
		seen := map[string]bool{}
		for _, r := range refs {
			if !ids[r.id] {
				continue
			}
			if !seen[r.id] {
				seen[r.id] = true
				entries = append(entries, selEntry{r.id, r.reason})
			}
			if whenBy[r.id] == "" {
				whenBy[r.id] = r.when
			}
		}
		if len(entries) == 0 {
			continue
		}
		lines = append(lines, selectorRefs("Profile-Suggested "+sk.title+" ("+p.ID+"):", entries, c.off.Repo(sk.k), sk.kind, statedDefaultWhen, whenBy, nil)...)
	}
	return lines
}

// RenderProfileSections is _render_profile_sections: every profile-channel
// section the (lineage-resolved) profile attests, blank-line separated.
func RenderProfileSections(root string, p *dct.Profile) string {
	if p == nil {
		return ""
	}
	c := &profileRenderCtx{off: dct.NewOffering(root, nil)}
	c.graph, _ = dct.LoadGraph(root, nil)
	var blocks []string
	for _, r := range []func(*dct.Profile, *profileRenderCtx) []string{
		renderProfileDirectives, renderProfileTactics, renderProfileStyleguides,
		renderProfileToolguides, renderProfileProcedures, renderProfileSuggested,
	} {
		if l := r(p, c); len(l) > 0 {
			blocks = append(blocks, strings.Join(l, "\n"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// ---- token budget ----------------------------------------------------------

type budgetSection struct {
	id, header, body, selector, when, indent string
	replacement                              *string
}

func rlen(s string) int { return utf8.RuneCountInString(s) }

func warningLine(count, budget int) string {
	return fmt.Sprintf("# Governance payload: %d sections substituted with fetch commands (budget=%d).", count, budget)
}

func budgetWithoutWarning(count, budget int) int {
	if count <= 0 {
		return budget
	}
	return budget - rlen("\n\n"+warningLine(count, budget))
}

func splitLeadingHeader(text string) (string, string) {
	h, rest, ok := strings.Cut(text, "\n")
	if !ok {
		return "", text
	}
	return h, rest
}

var headingLineRe = regexp.MustCompile(`^###\s+(\S.*?)\s*$`)

// enforceTokenBudget is _enforce_token_budget (section/profile candidates).
func enforceTokenBudget(text, action, profileBlock, sectionBlock string) string {
	return enforceTokenBudgetSel(text, action, profileBlock, sectionBlock, "")
}

// selectionGroups is token_budget._SELECTION_GROUPS.
var selectionGroups = map[string][2]string{
	"Selected paradigms:":              {"paradigm", "are about to choose a reasoning frame"},
	"Selected directives:":             {"directive", "are about to apply a code change"},
	"Selected tactics:":                {"tactic", "are about to apply a code change"},
	"Selected styleguides:":            {"styleguide", "are about to write styled output"},
	"Selected toolguides:":             {"toolguide", "are about to invoke a project tool"},
	"Selected procedures:":             {"procedure", "are about to follow a workflow"},
	"Selected agent profiles:":         {"agent_profile", "are about to apply a code change"},
	"Selected mission step contracts:": {"mission_step_contract", "are about to step a mission action"},
}

// enforceTokenBudgetSel is _enforce_token_budget with a selection block.
func enforceTokenBudgetSel(text, action, profileBlock, sectionBlock, selectionBlock string) string {
	budget := budgetDefault
	if rlen(text) <= budget {
		return text
	}
	var cands []budgetSection
	if sectionBlock != "" {
		headed := false
		for _, chunk := range splitBeforeHeadings(sectionBlock) {
			first, rest, _ := strings.Cut(chunk, "\n")
			m := headingLineRe.FindStringSubmatch(first)
			if m == nil {
				continue
			}
			headed = true
			if rest == "" {
				continue
			}
			cands = append(cands, budgetSection{id: "action-critical-sections:" + m[1], header: first, body: rest,
				selector: criticalSectionSelector(m[1]), when: criticalSectionWhen(m[1]), indent: "  "})
		}
		if !headed {
			h, b := splitLeadingHeader(sectionBlock)
			if b != "" {
				cands = append(cands, budgetSection{id: "action-critical-sections", header: h, body: b,
					selector: "section:critical-" + action, when: "need to consult the action-critical charter sections", indent: "  "})
			}
		}
	}
	if profileBlock != "" {
		idx := 0
		for _, chunk := range strings.Split(profileBlock, "\n\n") {
			if chunk == "" {
				continue
			}
			h, b := splitLeadingHeader(chunk)
			if b != "" {
				cands = append(cands, budgetSection{id: fmt.Sprintf("profile-cited-sections-%d", idx), header: h, body: b,
					selector: "section:profile-citations", when: profileCitationsWhen, indent: "  "})
			}
			idx++
		}
	}
	if selectionBlock != "" {
		for _, chunk := range strings.Split(selectionBlock, "\n\n") {
			if chunk == "" {
				continue
			}
			h, b := splitLeadingHeader(chunk)
			g, ok := selectionGroups[h]
			if !ok || b == "" {
				continue
			}
			var ids []string
			for _, l := range strings.Split(b, "\n") {
				if strings.HasPrefix(l, "  - ") {
					ids = append(ids, l)
				}
			}
			if len(ids) == 0 {
				continue
			}
			rep := strings.Join(append(ids, "  Run: spec-kitty charter context --include "+g[0]+":SELECTED_ID",
				"  When you "+g[1]+", replace SELECTED_ID with an ID above and apply the returned rule."), "\n")
			cands = append(cands, budgetSection{id: "global-selection:" + g[0], header: h, body: b, replacement: &rep})
		}
	}
	if len(cands) == 0 {
		return text
	}
	sort.SliceStable(cands, func(i, j int) bool {
		li, lj := rlen(cands[i].body), rlen(cands[j].body)
		if li != lj {
			return li > lj
		}
		return cands[i].id < cands[j].id
	})
	cur := text
	swapped := 0
	for _, s := range cands {
		if rlen(cur) <= budgetWithoutWarning(swapped, budget) {
			break
		}
		stanza := ""
		if s.replacement != nil {
			stanza = *s.replacement
		} else {
			stanza = strings.Join(fetchStanzaLines(s.selector, s.when, s.indent), "\n")
		}
		nt := strings.Replace(cur, s.body, stanza, 1)
		if nt == cur {
			continue
		}
		cur = nt
		swapped++
	}
	if swapped > 0 {
		cur += "\n\n" + warningLine(swapped, budget)
	}
	return cur
}

// splitBeforeHeadings is re.split(r"\n\n(?=### )", text).
func splitBeforeHeadings(text string) []string {
	var out []string
	for {
		i := strings.Index(text, "\n\n### ")
		if i < 0 {
			out = append(out, text)
			return out
		}
		out = append(out, text[:i])
		text = text[i+2:]
	}
}

var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// criticalSectionSelector is section_bodies.critical_section_selector.
func criticalSectionSelector(heading string) string {
	return "section:" + strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(heading), "-"), "-")
}

// criticalSectionWhen is section_bodies.critical_section_when_clause.
func criticalSectionWhen(heading string) string {
	if w, ok := criticalSectionWhenClauses[heading]; ok {
		return w
	}
	return defaultWhenClause
}

// criticalSectionWhenClauses is CRITICAL_SECTION_WHEN_CLAUSES.
var criticalSectionWhenClauses = map[string]string{
	"Terminology Canon":     "rename or introduce a term in the diff",
	"Code Review Checklist": "prepare a WP for review",
	"Regression Vigilance":  "perform a terminology cutover",
}
