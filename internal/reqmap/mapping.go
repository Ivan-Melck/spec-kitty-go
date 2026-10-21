package reqmap

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// pySplitLines is str.splitlines() for the common line terminators.
func pySplitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// DeclaredIDs is _declared_ids: ids written in one of the four declared
// shapes (first id per line).
func DeclaredIDs(spec, missionType string) map[string]bool {
	found := map[string]bool{}
	for _, line := range pySplitLines(BlankHTMLComments(spec)) {
		for _, p := range DeclaredShapePatterns(missionType) {
			m := p.FindStringSubmatchIndex(line)
			if m == nil {
				continue
			}
			if IsCompoundTail(line, m[3]) {
				continue
			}
			if c := Canonical(line[m[2]:m[3]], missionType); c != "" {
				found[c] = true
			}
			break
		}
	}
	return found
}

var bareProseKinds = map[string]bool{"FR": true, "NFR": true, "C": true}

func unqualifiedUnsuffixed(text string) []ID {
	var out []ID
	for _, id := range FindAll(text, true, "software-dev") {
		if id.Mission == "" && id.Suffix == "" && bareProseKinds[id.Kind] {
			out = append(out, id)
		}
	}
	return out
}

func rawRefTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, id := range unqualifiedUnsuffixed(text) {
		out[id.Canonical()] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type section struct{ heading, body string }

func requirementSections(spec string) []section {
	lines := pySplitLines(spec)
	var idxs []int
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t\n\r\f\v"), "#") {
			idxs = append(idxs, i)
		}
	}
	var out []section
	for pos, idx := range idxs {
		h := lines[idx]
		st := strings.TrimLeft(h, " \t\n\r\f\v")
		if !(strings.HasPrefix(st, "#") && strings.Contains(strings.ToLower(st), "requirement")) {
			continue
		}
		end := len(lines)
		if pos+1 < len(idxs) {
			end = idxs[pos+1]
		}
		out = append(out, section{heading: pyLstripHashStrip(h), body: strings.Join(lines[idx+1:end], "\n")})
	}
	return out
}

// pyLstripHashStrip is heading_line.lstrip("#").strip().
func pyLstripHashStrip(h string) string {
	return strings.TrimSpace(strings.TrimLeft(h, "#"))
}

func undeclaredWarning(tokens []string, scope string) string {
	return fmt.Sprintf("%s mentions requirement-shaped token(s) (%s) that matched none of the recognized declared shapes (table row / id-naming heading / bulleted item / bold-led paragraph) -- none of them count toward FR coverage. If these ARE this spec's own requirements, rewrite them in one of the recognized shapes; if they are citations of another mission's requirement, no action is needed.", scope, strings.Join(tokens, ", "))
}

// FindUndeclaredRequirementCitations is find_undeclared_requirement_citations.
func FindUndeclaredRequirementCitations(spec string) []string {
	declared := DeclaredIDs(spec, "software-dev")
	doc := rawRefTokens(spec)
	if len(doc) > 0 && len(declared) == 0 {
		return []string{undeclaredWarning(sortedKeys(doc), "spec.md")}
	}
	var out []string
	for _, s := range requirementSections(spec) {
		raw := rawRefTokens(s.body)
		if len(raw) > 0 && len(DeclaredIDs(s.body, "software-dev")) == 0 {
			out = append(out, undeclaredWarning(sortedKeys(raw), "The "+mission.PyRepr(s.heading)+" section"))
		}
	}
	return out
}

// BareProseCandidate is one requirement section's bare-prose ids.
type BareProseCandidate struct {
	Heading string
	IDs     []string
}

// FindBareProseRequirementIDs is find_bare_prose_requirement_ids.
func FindBareProseRequirementIDs(spec string) []BareProseCandidate {
	declared := DeclaredIDs(spec, "software-dev")
	var out []BareProseCandidate
	for _, s := range requirementSections(spec) {
		var ids []string
		seen := map[string]bool{}
		for _, line := range pySplitLines(s.body) {
			matched := false
			for _, p := range DeclaredShapes {
				if p.MatchString(line) {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			for _, id := range unqualifiedUnsuffixed(line) {
				t := id.Canonical()
				if !declared[t] && !seen[t] {
					seen[t] = true
					ids = append(ids, t)
				}
			}
		}
		if len(ids) > 0 {
			out = append(out, BareProseCandidate{Heading: s.heading, IDs: ids})
		}
	}
	return out
}

// Coverage is compute_coverage's summary.
type Coverage struct {
	Total, Mapped int
	Unmapped      []string
}

// ComputeCoverage is compute_coverage.
func ComputeCoverage(mappings map[string][]string, functional map[string]bool) Coverage {
	mapped := map[string]bool{}
	for _, refs := range mappings {
		for _, r := range refs {
			if c := Canonical(r, "software-dev"); c != "" {
				mapped[c] = true
			}
		}
	}
	n := 0
	var unmapped []string
	for f := range functional {
		if mapped[f] {
			n++
		} else {
			unmapped = append(unmapped, f)
		}
	}
	sort.Strings(unmapped)
	return Coverage{Total: len(functional), Mapped: n, Unmapped: unmapped}
}

var kindBucket = map[string]string{"FR": "functional", "NFR": "non_functional", "C": "constraint", "SC": "success_criteria",
	"DR": "data_collection", "AR": "analysis", "QR": "quality"}

// SpecIDs is parse_requirement_ids_from_spec_md's result.
type SpecIDs struct {
	All      []string
	Buckets  map[string][]string // functional, non_functional, constraint, success_criteria (+ research buckets)
	Research bool                // research buckets present
}

// Functional returns the FR bucket.
func (s SpecIDs) Functional() []string { return s.Buckets["functional"] }

// BucketOrder is the JSON key order of a parsed_spec_ids object.
var BucketOrder = []string{"functional", "non_functional", "constraint", "success_criteria"}

// ParseSpecIDs is parse_requirement_ids_from_spec_md.
func ParseSpecIDs(spec, missionType string) SpecIDs {
	declared := DeclaredIDs(spec, missionType)
	b := map[string][]string{}
	for _, k := range kindBucket {
		b[k] = []string{}
	}
	for id := range declared {
		r, ok := Parse(id, missionType)
		if !ok {
			continue
		}
		b[kindBucket[r.Kind]] = append(b[kindBucket[r.Kind]], id)
	}
	for k := range b {
		sort.Strings(b[k])
	}
	res := SpecIDs{All: sortedKeys(declared), Buckets: b}
	res.Research = len(b["data_collection"])+len(b["analysis"])+len(b["quality"]) > 0
	return res
}

var wpFileRe = regexp.MustCompile(`^(WP\d{2})`)

// ReadAllWPRawRefs is read_all_wp_raw_requirement_refs: raw requirement_refs
// tokens per WP (sorted WP*.md glob order).
func ReadAllWPRawRefs(tasksDir string) (map[string][]string, []string) {
	out := map[string][]string{}
	var order []string
	matches, _ := filepath.Glob(filepath.Join(tasksDir, "WP*.md"))
	sort.Strings(matches)
	for _, f := range matches {
		m := wpFileRe.FindStringSubmatch(filepath.Base(f))
		if m == nil {
			continue
		}
		id := m[1]
		if _, dup := out[id]; !dup {
			order = append(order, id)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			out[id] = []string{}
			continue
		}
		doc, err := fm.Parse(string(b))
		if err != nil || doc.Meta == nil {
			out[id] = []string{}
			continue
		}
		v, _ := doc.Meta.Get("requirement_refs")
		toks := TokenizeRefs(v)
		if toks == nil {
			toks = []string{}
		}
		out[id] = toks
	}
	return out, order
}

var (
	wpSectionRe = regexp.MustCompile(`(?m)^#{2,4}\s+(?:Work Package\s+)?(WP\d{2})(?:\b|:)`)
	refLineRe   = regexp.MustCompile(`(?i)\*?\*?Requirements?\s*(?:Refs)?\*?\*?\s*:\s*(.+)`)
)

// TasksMDSections is _parse_wp_sections_from_tasks_md (WP id -> section).
func TasksMDSections(content string) (map[string]string, []string) {
	out := map[string]string{}
	var order []string
	ms := wpSectionRe.FindAllStringSubmatchIndex(content, -1)
	for i, m := range ms {
		id := content[m[2]:m[3]]
		end := len(content)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		if _, ok := out[id]; !ok {
			order = append(order, id)
		}
		out[id] = content[m[1]:end]
	}
	return out, order
}

// TasksMDRefs is _parse_requirement_refs_from_tasks_md.
func TasksMDRefs(content, missionType string) map[string][]string {
	secs, _ := TasksMDSections(content)
	out := map[string][]string{}
	for id, sec := range secs {
		var refs []string
		seen := map[string]bool{}
		for _, m := range refLineRe.FindAllStringSubmatch(sec, -1) {
			for _, r := range FindAll(m[1], false, missionType) {
				s := r.String()
				if !seen[s] {
					seen[s] = true
					refs = append(refs, s)
				}
			}
		}
		if refs == nil {
			refs = []string{}
		}
		out[id] = refs
	}
	return out
}
