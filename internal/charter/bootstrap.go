package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// This file ports charter.activation.context_renderers.bootstrap_text and
// its collaborators (reference_pointers, section_bodies, authority_paths,
// governance_references, selection_block, spdd_reasons).

const (
	noPolicySummaryMessage  = "  - No explicit policy summary section found in charter.md."
	missingReferencesMsg    = "  - No references manifest found."
	actionDoctrineLinkWhen  = "are about to apply a code change"
	referencePointerLimit   = 12
	glossaryFetchWhen       = "encounter one of these terms"
	authorityPathsHeader    = "Project authority paths:"
	charterDeclaredWhen     = "consult when you change content under this directory"
	terminologyAuthorityWhn = "canonical terminology — when you encounter a domain term in the diff, grep this directory"
	adrAuthorityWhen        = "architectural intent — when you change a structural boundary, read the relevant ADR"
)

// ---------------------------------------------------------------- sections

var actionCriticalSections = map[string][]string{
	"implement": {"Terminology Canon", "Code Review Checklist", "Regression Vigilance"},
	"review":    {"Terminology Canon", "Code Review Checklist", "Regression Vigilance"},
}

var fenceOpenRe = regexp.MustCompile("^[ \t]{0,3}(`{3,}|~{3,})")
var anyHeadingRe = regexp.MustCompile(`^(#{1,6})\s+`)

func splitKeepEnds(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexAny(s, "\n\r")
		if i < 0 {
			out = append(out, s)
			break
		}
		j := i + 1
		if s[i] == '\r' && j < len(s) && s[j] == '\n' {
			j++
		}
		out = append(out, s[:j])
		s = s[j:]
	}
	return out
}

func isFenceClose(line, marker string, n int) bool {
	re := regexp.MustCompile(`^[ \t]{0,3}` + regexp.QuoteMeta(marker) + `{` + fmt.Sprint(n) + `,}[ \t]*\r?\n?$`)
	return re.MatchString(line)
}

func findHeadingEnd(content, heading string) (int, int, bool) {
	pat := regexp.MustCompile(`^(#{2,6})\s+` + regexp.QuoteMeta(strings.TrimSpace(heading)) + `\b.*`)
	marker, flen, off := "", 0, 0
	for _, line := range splitKeepEnds(content) {
		if marker != "" {
			if isFenceClose(line, marker, flen) {
				marker, flen = "", 0
			}
		} else {
			body := strings.TrimRight(line, "\r\n")
			if m := pat.FindStringSubmatchIndex(body); m != nil {
				return off + m[1], m[3] - m[2], true
			}
			if fm := fenceOpenRe.FindStringSubmatch(line); fm != nil {
				marker, flen = fm[1][:1], len(fm[1])
			}
		}
		off += len(line)
	}
	return 0, 0, false
}

func findNextSectionStart(body string, level int) (int, bool) {
	lines := splitKeepEnds(body)
	marker, flen, off := "", 0, 0
	for i, line := range lines {
		if marker != "" {
			if isFenceClose(line, marker, flen) {
				marker, flen = "", 0
			}
		} else {
			if m := anyHeadingRe.FindStringSubmatch(line); m != nil && len(m[1]) <= level {
				return off, true
			}
			if fm := fenceOpenRe.FindStringSubmatch(line); fm != nil {
				marker, flen = fm[1][:1], len(fm[1])
				closed := false
				for _, l := range lines[i+1:] {
					if isFenceClose(l, marker, flen) {
						closed = true
						break
					}
				}
				if !closed {
					return off, true
				}
			}
		}
		off += len(line)
	}
	return 0, false
}

// extractSectionBody is section_bodies._extract_section_body.
func extractSectionBody(content, heading string) (string, bool) {
	start, level, ok := findHeadingEnd(content, heading)
	if !ok {
		return "", false
	}
	rem := content[start:]
	body := rem
	if n, found := findNextSectionStart(rem, level); found {
		body = content[start : start+n]
	}
	return strings.TrimRight(strings.Trim(body, "\n"), " \t\n\r\f\v"), true
}

// renderCriticalSectionBodies is section_bodies.render_critical_section_bodies.
func renderCriticalSectionBodies(content, action string) string {
	headings := actionCriticalSections[action]
	if len(headings) == 0 {
		return ""
	}
	blocks := []string{"Action-Critical Charter Sections (" + action + "):"}
	for _, h := range headings {
		body, _ := extractSectionBody(content, h)
		blocks = append(blocks, "", "### "+h)
		if body != "" {
			blocks = append(blocks, body)
		}
		blocks = append(blocks, "  Run: spec-kitty charter context --include "+criticalSectionSelector(h),
			"  When you "+criticalSectionWhen(h)+", run this command and apply the returned rule.")
	}
	return strings.Join(blocks, "\n")
}

// ----------------------------------------------------- authority / refs

func normalizeAuthorityPath(raw string) string {
	c := strings.TrimSpace(raw)
	if c == "" || strings.HasSuffix(c, "/") {
		return c
	}
	return c + "/"
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// renderAuthorityPaths is authority_paths.render_authority_paths.
func renderAuthorityPaths(root string, sel Selection) string {
	var lines []string
	seen := map[string]bool{}
	for _, d := range [][2]string{{"docs/context/", terminologyAuthorityWhn}, {"docs/adr/3.x/", adrAuthorityWhen}} {
		if !isDir(filepath.Join(root, d[0])) || seen[d[0]] {
			continue
		}
		seen[d[0]] = true
		lines = append(lines, "  - "+d[0]+"    ("+d[1]+")")
	}
	for _, raw := range sel.AuthorityPaths {
		n := normalizeAuthorityPath(pyStrOf(raw))
		if n == "" || seen[n] || !isDir(filepath.Join(root, n)) {
			continue
		}
		seen[n] = true
		lines = append(lines, "  - "+n+"    ("+charterDeclaredWhen+")")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(append([]string{authorityPathsHeader}, lines...), "\n")
}

// GovernanceReferenceStatus is governance_references.GovernanceReferenceStatus.
type GovernanceReferenceStatus struct {
	Path         string
	Exists, Safe bool
	Warning      string
	HasWarning   bool
}

// Object is GovernanceReferenceStatus.to_dict.
func (s GovernanceReferenceStatus) Object() *jsonx.Object {
	var w any
	if s.HasWarning {
		w = s.Warning
	}
	return jsonx.NewObject("path", s.Path, "exists", s.Exists, "safe", s.Safe, "warning", w)
}

// CollectGovernanceReferenceStatus is collect_governance_reference_status.
func CollectGovernanceReferenceStatus(root string, refs []any) []GovernanceReferenceStatus {
	abs, _ := filepath.Abs(root)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	var out []GovernanceReferenceStatus
	for _, raw := range refs {
		p := strings.TrimSpace(pyStrOf(raw))
		if p == "" {
			continue
		}
		warn := ""
		switch {
		case filepath.IsAbs(p):
			warn = "Unsafe governance reference " + p + ": paths must be repository-relative, not absolute."
		case func() bool {
			for _, part := range strings.Split(filepath.ToSlash(p), "/") {
				if part == ".." {
					return true
				}
			}
			return false
		}():
			warn = "Unsafe governance reference " + p + ": parent-directory traversal is not allowed."
		default:
			cand := filepath.Join(abs, p)
			if r, err := filepath.EvalSymlinks(cand); err == nil {
				cand = r
			}
			if rel, err := filepath.Rel(abs, cand); err != nil || strings.HasPrefix(rel, "..") {
				warn = "Unsafe governance reference " + p + ": resolved path escapes the repository root."
			}
		}
		if warn != "" {
			out = append(out, GovernanceReferenceStatus{Path: p, Warning: warn, HasWarning: true})
			continue
		}
		st, err := os.Stat(filepath.Join(abs, p))
		s := GovernanceReferenceStatus{Path: p, Exists: err == nil, Safe: true}
		if err != nil {
			s.Warning, s.HasWarning = "Missing governance reference "+p+". Create it under the repository root or remove it from governance_references in .kittify/charter/charter.md.", true
		} else if !st.Mode().IsRegular() {
			s.Warning, s.HasWarning = "Governance reference "+p+" is not a file. Point governance_references at a markdown or text document under the repository root.", true
		}
		out = append(out, s)
	}
	return out
}

func renderGovernanceReferences(root string, refs []any) string {
	st := CollectGovernanceReferenceStatus(root, refs)
	if len(st) == 0 {
		return ""
	}
	lines := []string{"Required Governance Reading:"}
	for _, s := range st {
		if s.HasWarning {
			lines = append(lines, "  - WARNING: "+s.Warning)
		} else {
			lines = append(lines, "  - "+s.Path)
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------- selection

func trimmedField(o *jsonx.Object, k string) string {
	s, ok := strField(o, k)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func paradigmBody(o *jsonx.Object) []string {
	var out []string
	if s := trimmedField(o, "name"); s != "" {
		out = append(out, "    Name: "+s)
	}
	if s := trimmedField(o, "summary"); s != "" {
		out = append(out, "    Summary: "+s)
	}
	return out
}

func styleguideBody(o *jsonx.Object) []string {
	var out []string
	if s := trimmedField(o, "title"); s != "" {
		out = append(out, "    Title: "+s)
	}
	if v, ok := o.Get("scope"); ok && v != nil {
		if s := pyStrOf(v); s != "" {
			out = append(out, "    Scope: "+s)
		}
	}
	if l := listField(o, "principles"); len(l) > 0 {
		out = append(out, "    Principles:")
		for _, p := range l {
			out = append(out, "      - "+itemText(p))
		}
	}
	return out
}

func toolguideBody(o *jsonx.Object) []string {
	var out []string
	for _, f := range [][2]string{{"Title", "title"}, {"Tool", "tool"}, {"Summary", "summary"}} {
		if s := trimmedField(o, f[1]); s != "" {
			out = append(out, "    "+f[0]+": "+s)
		}
	}
	return out
}

func procedureBody(o *jsonx.Object) []string {
	var out []string
	for _, f := range [][2]string{{"Name", "name"}, {"Purpose", "purpose"}, {"Entry condition", "entry_condition"}, {"Exit condition", "exit_condition"}} {
		if s := trimmedField(o, f[1]); s != "" {
			out = append(out, "    "+f[0]+": "+s)
		}
	}
	if steps := listField(o, "steps"); len(steps) > 0 {
		out = append(out, "    Steps:")
		for _, st := range steps {
			so, ok := st.(*jsonx.Object)
			if !ok {
				out = append(out, "      - "+itemText(st))
				continue
			}
			out = append(out, "      - "+so.GetString("title"))
			if d := trimmedField(so, "description"); d != "" {
				out = append(out, "        "+d)
			}
		}
	}
	return out
}

func agentProfileBody(o *jsonx.Object) []string {
	var out []string
	if s := trimmedField(o, "name"); s != "" {
		out = append(out, "    Name: "+s)
	}
	if s := trimmedField(o, "purpose"); s != "" {
		out = append(out, "    Purpose: "+s)
	}
	if l := listField(o, "roles"); len(l) > 0 {
		var rs []string
		for _, r := range l {
			rs = append(rs, itemText(r))
		}
		out = append(out, "    Roles: "+strings.Join(rs, ", "))
	}
	return out
}

func stepContractBody(o *jsonx.Object) []string {
	var out []string
	if s := trimmedField(o, "action"); s != "" {
		out = append(out, "    Action: "+s)
	}
	if s := trimmedField(o, "mission"); s != "" {
		out = append(out, "    Mission: "+s)
	}
	if steps := listField(o, "steps"); len(steps) > 0 {
		out = append(out, "    Steps:")
		for _, st := range steps {
			so, _ := st.(*jsonx.Object)
			id, desc := "", ""
			if so != nil {
				id, desc = so.GetString("id"), so.GetString("description")
			}
			if id != "" {
				out = append(out, "      - "+id+": "+desc)
			} else {
				out = append(out, "      - "+desc)
			}
		}
	}
	return out
}

func normalizeDirectiveID(raw string) string {
	if directiveCodeRe.MatchString(raw) {
		return raw
	}
	if m := regexp.MustCompile(`^(\d+)`).FindStringSubmatch(raw); m != nil {
		n := m[1]
		for len(n) < 3 {
			n = "0" + n
		}
		return "DIRECTIVE_" + n
	}
	return strings.ReplaceAll(strings.ToUpper(raw), "-", "_")
}

type selectionGroup struct {
	header, kind, when string
	kindT              dct.Kind
	ids                []string
	body               func(*jsonx.Object) []string
	norm               func(string) string
}

// renderSelectionBlock is selection_block._render_selection_block.
func renderSelectionBlock(root string, sel Selection, off *dct.Offering) string {
	orgReq := OrgRequiredSelections(root)
	plural := map[dct.Kind]string{dct.Paradigm: "paradigms", dct.Directive: "directives", dct.Tactic: "tactics",
		dct.Styleguide: "styleguides", dct.Toolguide: "toolguides", dct.Procedure: "procedures",
		dct.AgentProfile: "agent_profiles", dct.MissionStepContract: "mission_step_contracts"}
	groups := []selectionGroup{
		{"Selected paradigms:", "paradigm", "are about to choose a reasoning frame", dct.Paradigm, sel.Paradigms, paradigmBody, nil},
		{"Selected directives:", "directive", actionDoctrineLinkWhen, dct.Directive, sel.Directives, directiveBody, normalizeDirectiveID},
		{"Selected tactics:", "tactic", actionDoctrineLinkWhen, dct.Tactic, sel.Tactics, namedBody, nil},
		{"Selected styleguides:", "styleguide", "are about to write a code comment or styled output", dct.Styleguide, sel.Styleguides, styleguideBody, nil},
		{"Selected toolguides:", "toolguide", "are about to invoke a project tool", dct.Toolguide, sel.Toolguides, toolguideBody, nil},
		{"Selected procedures:", "procedure", "are about to follow a multi-step workflow", dct.Procedure, sel.Procedures, procedureBody, nil},
		{"Selected agent profiles:", "agent_profile", actionDoctrineLinkWhen, dct.AgentProfile, sel.AgentProfiles, agentProfileBody, nil},
		{"Selected mission step contracts:", "mission_step_contract", "are about to step a mission action", dct.MissionStepContract, sel.MissionStepContracts, stepContractBody, nil},
	}
	var blocks []string
	for _, g := range groups {
		if len(g.ids) == 0 {
			continue
		}
		repo := off.Repo(g.kindT)
		lines := []string{g.header}
		seen := map[string]bool{}
		for _, id := range g.ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			// _provenance_suffix: org-sourced (repository provenance) or
			// org-required ids carry "(source: org)" (no per-pack attribution).
			suffix := ""
			if a := repo.Get(id); a != nil && a.ID == id && a.Source == dct.LayerOrg {
				suffix = " (source: org)"
			} else {
				for _, r := range orgReq[plural[g.kindT]] {
					if r == id {
						suffix = " (source: org)"
					}
				}
			}
			lines = append(lines, "  - "+id+suffix)
			lookup := id
			if g.norm != nil {
				lookup = g.norm(id)
			}
			var a *dct.Artifact
			if repo != nil {
				a = repo.Get(lookup)
			}
			if a == nil {
				if repo != nil {
					lines = append(lines, catalogMissLines(g.kind, id, repo)...)
				}
				lines = append(lines, fetchStanzaLines(g.kind+":"+id, g.when, "    ")...)
				continue
			}
			bl := g.body(a.Data)
			if len(bl) > 0 && budgetEstimate(bl) <= profileInlineLimit {
				lines = append(lines, bl...)
			} else {
				lines = append(lines, fetchStanzaLines(g.kind+":"+id, g.when, "    ")...)
			}
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

// ------------------------------------------------------ action doctrine

type actionRow struct {
	heading     string
	kind        dct.Kind
	titleAttr   string
	summaryAttr string
	progressive string
}

var actionRenderRows = []actionRow{
	{"Directives", dct.Directive, "title", "intent", "directive"},
	{"Tactics", dct.Tactic, "name", "purpose", "tactic"},
	{"Styleguides", dct.Styleguide, "title", "", "styleguide"},
	{"Toolguides", dct.Toolguide, "title", "", "toolguide"},
	{"Procedures", dct.Procedure, "name", "purpose", ""},
	{"Assets", dct.Asset, "title", "", ""},
}

func attrText(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return "None"
	}
	return pyStrOf(v)
}

func renderActionDoctrineLines(lines []string, b *ActionBundle) []string {
	if b == nil {
		return lines
	}
	inline := map[string]bool{}
	if b.Merged != nil {
		inline = requiresClosure(b.Merged, b.Roots)
	}
	for _, row := range actionRenderRows {
		ids := b.IDs[row.kind]
		if len(ids) == 0 {
			continue
		}
		var repo *dct.Repository
		if row.kind != dct.Asset && b.Offer != nil {
			repo = b.Offer.Repo(row.kind)
		}
		var f []string
		for _, id := range ids {
			var a *dct.Artifact
			if repo != nil {
				a = repo.Get(id)
			}
			if a == nil {
				f = append(f, "    - "+id)
				continue
			}
			title := attrText(a.Data, row.titleAttr)
			if row.progressive != "" && !inline[row.progressive+":"+id] {
				f = append(f, "    - "+id+": "+title)
				f = append(f, fetchStanzaLines(row.progressive+":"+id, actionDoctrineLinkWhen, "    ")...)
				continue
			}
			if row.summaryAttr != "" {
				if s, ok := strField(a.Data, row.summaryAttr); ok && s != "" {
					f = append(f, "    - "+id+": "+title+" — "+s)
					continue
				}
			}
			f = append(f, "    - "+id+": "+title)
		}
		lines = append(lines, "  "+row.heading+":")
		lines = append(lines, f...)
	}
	if packs := b.IDs[dct.GlossaryPack]; len(packs) > 0 && b.Offer != nil {
		repo := b.Offer.Repo(dct.GlossaryPack)
		var r []string
		for _, id := range packs {
			var a *dct.Artifact
			if repo != nil {
				a = repo.Get(id)
			}
			if a == nil {
				r = append(r, "    - "+id)
				continue
			}
			pid := a.Data.GetString("id")
			r = append(r, "    - "+pid)
			var surf []string
			for _, t := range a.Data.GetList("terms") {
				if to, ok := t.(*jsonx.Object); ok {
					if s := strings.TrimSpace(to.GetString("surface")); s != "" {
						surf = append(surf, s)
					}
				}
			}
			if len(surf) > 0 {
				r = append(r, "      Terms: "+strings.Join(surf, ", "))
			}
			r = append(r, fetchStanzaLines("glossary-pack:"+pid, glossaryFetchWhen, "    ")...)
		}
		if len(r) > 0 {
			lines = append(lines, "  Glossaries:")
			lines = append(lines, r...)
		}
	}
	return lines
}

// ------------------------------------------------------- guidelines/SPDD

func appendGuidelines(lines []string, mission, action string) []string {
	if mission == "" {
		return lines
	}
	b, err := dct.BuiltIn().ReadFile("missions/mission-steps/" + mission + "/" + action + "/guidelines.md")
	if err != nil {
		return lines
	}
	c := strings.TrimSpace(string(b))
	if c == "" {
		return lines
	}
	lines = append(lines, "  Guidelines:")
	for _, l := range pySplitLines(c) {
		lines = append(lines, "    "+l)
	}
	return lines
}

// pySplitLines is str.splitlines for the common separators.
func pySplitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	out := strings.Split(s, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

var spddGuidance = map[string][]string{
	"specify": {
		"Capture Requirements (R): user-visible behaviour and acceptance signals.",
		"Capture Entities (E): nouns, identifiers, and data shapes the spec must name.",
		"Cite source quotes from the user prompt; mark gaps as [NEEDS CLARIFICATION].",
		"Defer Approach (A) and Structure (S) decisions to the plan phase.",
	},
	"plan": {
		"Translate Requirements/Entities into Approach (A): chosen tactic and trade-offs.",
		"Document Structure (S): module/package boundaries and contract surfaces.",
		"Surface Norms (N): performance, security, reliability budgets and policy constraints.",
		"Note Safeguards (S) — guardrails and risk mitigations the plan must honour.",
	},
	"tasks": {
		"Decompose Approach into Operations (O): ordered, owner-scoped work items.",
		"Set WP boundaries: each WP owns one slice of (A, S) and lists its Requirements refs.",
		"Cross-reference each WP back to Requirements and Entities it satisfies.",
		"Keep Operations (O) small enough that each WP can be reviewed in one sitting.",
	},
	"implement": {
		"Honour the full canvas: Requirements, Entities, Approach, Structure, Operations, Norms, Safeguards.",
		"Before coding, restate the WP-scoped Requirements (R) and Entities (E) in your own words.",
		"Execute Operations (O) in the order recorded; do not skip ahead silently.",
		"Capture deviations in the canvas 'Deviations' subsection — never in scattered comments.",
		"Verify Norms (N) and Safeguards (S) before marking the WP for review.",
	},
	"review": {
		"Reviewer's comparison surface is the Requirements, Operations, Norms, Safeguards quartet (R, O, N, S).",
		"Verify Requirements (R) are met by the diff and tests.",
		"Verify Operations (O) ran end-to-end and produced expected artefacts.",
		"Verify Norms (N) — performance, type-checking, coverage, and policy budgets.",
		"Verify Safeguards (S) — guardrails honoured; flag undocumented deviations.",
	},
}

// IsSPDDReasonsActive is spdd_reasons.activation.is_spdd_reasons_active
// (malformed sources read as inactive instead of raising).
func IsSPDDReasonsActive(root string) bool {
	cfg, err := dct.LoadConfig(root)
	if err != nil || cfg == nil {
		return false
	}
	if _, statErr := os.Stat(dct.ConfigPath(root)); statErr != nil {
		return false
	}
	src := cfg
	if v, ok := cfg.Get("charter"); ok {
		if s, isStr := v.(string); isStr {
			p := s
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			o := readYAMLObject(p)
			if o == nil {
				return false
			}
			src = o
		}
	}
	read := func(k string) (map[string]bool, bool) {
		v, ok := src.Get(k)
		if !ok || v == nil {
			return nil, false
		}
		l, _ := v.([]any)
		out := map[string]bool{}
		for _, x := range l {
			out[pyStrOf(x)] = true
		}
		return out, true
	}
	par, pok := read("activated_paradigms")
	tac, tok := read("activated_tactics")
	dir, dok := read("activated_directives")
	if !pok || par["structured-prompt-driven-development"] {
		return true
	}
	if !tok || tac["reasons-canvas-fill"] || tac["reasons-canvas-review"] {
		return true
	}
	if !dok {
		return true
	}
	numRe := regexp.MustCompile(`^(\d+)`)
	for d := range dir {
		if d == "DIRECTIVE_038" || strings.ToUpper(d) == "DIRECTIVE_038" {
			return true
		}
		if m := numRe.FindStringSubmatch(d); m != nil {
			n := m[1]
			for len(n) < 3 {
				n = "0" + n
			}
			if n == "038" {
				return true
			}
		}
	}
	return false
}

func appendSPDDGuidance(lines []string, mission, action string) []string {
	n := strings.ToLower(strings.TrimSpace(action))
	lines = append(lines, "", "  SPDD/REASONS Guidance (action: "+n+"):")
	if bullets, ok := spddGuidance[n]; ok {
		for _, b := range bullets {
			lines = append(lines, "    - "+b)
		}
	} else {
		lines = append(lines, "    - SPDD/REASONS pack active. No action-scoped bullets registered for action '"+n+"'.")
	}
	canvas := "kitty-specs/<mission>/reasons-canvas.md"
	if mission != "" {
		canvas = "kitty-specs/" + mission + "/reasons-canvas.md"
	}
	return append(lines, "    - Reference: "+canvas+" (when present).")
}

// ------------------------------------------------------ reference docs

var referenceKindDirs = map[string]string{
	"tactic": "tactics", "directive": "directives", "paradigm": "paradigms", "procedure": "procedures",
	"styleguide": "styleguides", "toolguide": "toolguides", "agent_profile": "agent_profiles",
}

var refActionRe = regexp.MustCompile(`\(action:\s*(\w+)\)`)
var numericPrefixRe = regexp.MustCompile(`^(\d+)-`)

func filterReferencesForAction(refs []Reference, action string) []Reference {
	var out []Reference
	for _, r := range refs {
		if r.Kind != "local_support" {
			out = append(out, r)
			continue
		}
		if m := refActionRe.FindStringSubmatch(r.Summary); m != nil {
			if strings.ToLower(strings.TrimSpace(m[1])) == strings.ToLower(action) {
				out = append(out, r)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func referenceSourceIndex() map[string]map[string]string {
	bi := dct.BuiltIn()
	idx := map[string]map[string]string{}
	for kind, sub := range referenceKindDirs {
		ki := map[string]string{}
		for _, p := range bi.Walk(sub, ".yaml", true) {
			name := p[strings.LastIndex(p, "/")+1:]
			stem := name
			if i := strings.Index(name, "."); i >= 0 {
				stem = name[:i]
			}
			if _, ok := ki[stem]; !ok {
				ki[stem] = bi.Path(p)
			}
			if m := numericPrefixRe.FindStringSubmatch(stem); m != nil {
				if _, ok := ki["DIRECTIVE_"+m[1]]; !ok {
					ki["DIRECTIVE_"+m[1]] = bi.Path(p)
				}
			}
		}
		idx[kind] = ki
	}
	return idx
}

func actionOffset(action string) int {
	n := 0
	for i, r := range []rune(action) {
		n += (i + 1) * int(r)
	}
	return n
}

func distributeReferences(refs []Reference, action string) []Reference {
	var order []string
	by := map[string][]Reference{}
	for _, r := range refs {
		if _, ok := by[r.Kind]; !ok {
			order = append(order, r.Kind)
		}
		by[r.Kind] = append(by[r.Kind], r)
	}
	off := actionOffset(action)
	for _, k := range order {
		m := by[k]
		s := off % len(m)
		by[k] = append(append([]Reference{}, m[s:]...), m[:s]...)
	}
	var out []Reference
	for round := 0; ; round++ {
		added := false
		for _, k := range order {
			if round < len(by[k]) {
				out = append(out, by[k][round])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}

type selectedReference struct {
	ref  Reference
	path string
}

func selectReferencePointers(refs []Reference, action string) []selectedReference {
	idx := referenceSourceIndex()
	var out []selectedReference
	for _, r := range distributeReferences(filterReferencesForAction(refs, action), action) {
		ki := idx[r.Kind]
		if len(ki) == 0 {
			continue
		}
		id := r.ID
		if i := strings.Index(id, ":"); i >= 0 {
			id = id[i+1:]
		}
		p, ok := ki[id]
		if !ok {
			lp := r.LocalPath
			slug := filepath.Base(lp)
			if i := strings.LastIndex(slug, "."); i > 0 {
				slug = slug[:i]
			}
			slug = strings.TrimPrefix(slug, r.Kind+"-")
			p, ok = ki[slug]
		}
		if !ok {
			continue
		}
		out = append(out, selectedReference{r, p})
		if len(out) >= referencePointerLimit {
			break
		}
	}
	return out
}

// ------------------------------------------------------------- render

// renderBootstrap is bootstrap_text._render_bootstrap_text.
func renderBootstrap(root, action string, b *ActionBundle, refs []Reference, profile *dct.Profile) (string, error) {
	mdPath := Path(root, CharterMD)
	raw, _ := os.ReadFile(mdPath)
	content := string(raw)
	var sum []string
	if raw != nil {
		sum = PolicySummary(content)
	}
	lines := []string{"Charter Context (Bootstrap):", "  - Source: " + mdPath,
		"  - This is the first load for this action. Use the summary and follow references as needed.", "", "Policy Summary:"}
	if len(sum) == 0 {
		lines = append(lines, noPolicySummaryMessage)
	}
	for i, s := range sum {
		if i >= 8 {
			break
		}
		lines = append(lines, "  - "+s)
	}
	block := func(s string) {
		if s != "" {
			lines = append(lines, "", s)
		}
	}
	sel := LoadSelection(root)
	block(renderAuthorityPaths(root, sel))
	block(renderGovernanceReferences(root, sel.GovernanceReferences))
	sectionBlock := renderCriticalSectionBodies(content, action)
	block(sectionBlock)
	profileBlock := RenderProfileSections(root, profile)
	block(profileBlock)
	off := dct.NewOffering(root, dct.OrgRoots(root))
	if b != nil && b.Offer != nil {
		off = b.Offer
	}
	selectionBlock := renderSelectionBlock(root, sel, off)
	block(selectionBlock)
	mission := ""
	if b != nil {
		mission = b.Mission
	}
	activationBlock, err := renderActivationBlock(root, off, mission, action)
	if err != nil {
		return "", err
	}
	block(activationBlock)
	lines = append(lines, "", "Action Doctrine ("+action+"):")
	lines = renderActionDoctrineLines(lines, b)
	lines = appendGuidelines(lines, mission, action)
	if IsSPDDReasonsActive(root) {
		lines = appendSPDDGuidance(lines, mission, action)
	}
	lines = append(lines, "", "Reference Docs:")
	selected := selectReferencePointers(refs, action)
	if len(selected) == 0 {
		lines = append(lines, missingReferencesMsg)
	}
	for _, s := range selected {
		lines = append(lines, fmt.Sprintf("  - %s: %s (%s)", s.ref.ID, s.ref.Title, s.path))
	}
	if langs, known := InferRepoLanguages(root, nil, false); LacksSpecialistGuidance(langs, known, root) {
		lines = append(lines, "", "  - Advisory: "+CharterExtensionAdvisory)
	}
	return enforceTokenBudgetSel(strings.Join(lines, "\n"), action, profileBlock, sectionBlock, selectionBlock), nil
}
