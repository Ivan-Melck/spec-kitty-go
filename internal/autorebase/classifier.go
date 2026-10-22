// Package autorebase ports the stale-lane auto-rebase
// (specify_cli.lanes.auto_rebase) and its pure conflict classifier
// (specify_cli.consolidation.conflict_classifier).
package autorebase

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/pyast"
)

// Rule identifiers stamped into Auto.RuleID / Manual reasons.
const (
	RulePyprojectDeps  = "R-PYPROJECT-DEPS-UNION"
	RuleInitImports    = "R-INIT-IMPORTS-UNION"
	RuleURLsList       = "R-URLS-LIST-UNION"
	RuleUVLock         = "R-UVLOCK-REGENERATE"
	RuleDefaultManual  = "R-DEFAULT-MANUAL"
	RuleStatusEvents   = "R-STATUS-EVENTS-JSONL-UNION"
	RuleStatusJSON     = "R-STATUS-JSON-REMATERIALIZE"
	RuleCoordArtifact  = "R-COORDINATION-ARTIFACT-THEIRS"
	RuleTargetOwned    = "R-TARGET-OWNED-BOOKKEEPING"
	uvLockFilename     = "uv.lock"
	statusJSONFilename = "status.json"
)

// Resolution is Auto | Manual: Auto carries MergedText and RuleID, Manual
// carries Reason.
type Resolution struct {
	Auto       bool
	MergedText string
	RuleID     string
	Reason     string
}

func auto(text, rule string) Resolution {
	return Resolution{Auto: true, MergedText: text, RuleID: rule}
}
func manual(reason string) Resolution { return Resolution{Reason: reason} }

// Classification is ConflictClassification for one conflict region.
type Classification struct {
	FilePath string
	HunkText string
	Res      Resolution
}

var (
	reOursHeader   = regexp.MustCompile(`(?m)^<{7}[^\n]*\n`)
	reBaseHeader   = regexp.MustCompile(`(?m)^\|{7}[^\n]*\n`)
	reSep          = regexp.MustCompile(`(?m)^={7}\s*\n`)
	reTheirsHeader = regexp.MustCompile(`(?m)^>{7}[^\n]*\n`)
	reDepPkg       = regexp.MustCompile(`^\s*"([^"<>=!~\s]+)`)
	reImportTarget = regexp.MustCompile(`^\s*(?:from\s+\S+\s+import\s+[A-Za-z_][\w.,\s]*|import\s+[A-Za-z_][\w.,\s]*)\s*$`)
	reFromModule   = regexp.MustCompile(`^\s*from\s+(\S+)\s+import`)
	reImportModule = regexp.MustCompile(`^\s*import\s+(\S+)`)
	reURLEntry     = regexp.MustCompile(`^\s*("[^"\n]*"|'[^']*')`)
	reConflict     = regexp.MustCompile(`(?s)<{7}[^\n]*\n.*?>{7}[^\n]*\n`)
)

// search is pattern.search(text, pos) for the line-anchored headers (pos is
// always at a line start here).
func search(re *regexp.Regexp, text string, pos int) []int {
	if pos > len(text) {
		return nil
	}
	loc := re.FindStringIndex(text[pos:])
	if loc == nil {
		return nil
	}
	return []int{loc[0] + pos, loc[1] + pos}
}

// splitConflictRegion is _split_conflict_region.
func splitConflictRegion(hunk string) (string, string, bool) {
	ours := search(reOursHeader, hunk, 0)
	if ours == nil {
		return "", "", false
	}
	sep := search(reSep, hunk, ours[1])
	from := 0
	if sep != nil {
		from = sep[1]
	}
	theirs := search(reTheirsHeader, hunk, from)
	if sep == nil || theirs == nil {
		return "", "", false
	}
	base := search(reBaseHeader, hunk, ours[1])
	var o string
	if base != nil && base[0] < sep[0] {
		o = hunk[ours[1]:base[0]]
	} else {
		o = hunk[ours[1]:sep[0]]
	}
	return o, hunk[sep[1]:theirs[0]], true
}

// pySplitLines is str.splitlines().
func pySplitLines(s string) []string {
	var out []string
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, b.String())
			b.Reset()
		case '\r':
			out = append(out, b.String())
			b.Reset()
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func isPySpace(r rune) bool    { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }
func pyStrip(s string) string  { return strings.TrimFunc(s, isPySpace) }
func pyRStrip(s string) string { return strings.TrimRightFunc(s, isPySpace) }
func pyLStrip(s string) string { return strings.TrimLeftFunc(s, isPySpace) }

type entry struct{ key, line string }

func parseDepLines(block string) ([]entry, bool) {
	var out []entry
	for _, raw := range pySplitLines(block) {
		st := pyStrip(raw)
		if st == "" || strings.HasPrefix(st, "#") {
			continue
		}
		if !strings.HasPrefix(st, `"`) && !strings.HasPrefix(st, "'") {
			return nil, false
		}
		m := reDepPkg.FindStringSubmatch(raw)
		if m == nil {
			return nil, false
		}
		line := pyRStrip(raw)
		if !strings.HasSuffix(line, ",") {
			line += ","
		}
		out = append(out, entry{strings.ToLower(m[1]), line})
	}
	return out, true
}

func isSortedAlpha(es []entry) bool {
	return sort.SliceIsSorted(es, func(i, j int) bool { return es[i].key < es[j].key })
}

// lastByKey is dict(entries): the last line wins per key, keys in first
// appearance order.
func lastByKey(es []entry) ([]string, map[string]string) {
	m := map[string]string{}
	var order []string
	for _, e := range es {
		if _, ok := m[e.key]; !ok {
			order = append(order, e.key)
		}
		m[e.key] = e.line
	}
	return order, m
}

// union is merged.setdefault over ours then theirs.
func union(ours, theirs []entry) []entry {
	seen := map[string]bool{}
	var out []entry
	for _, e := range append(append([]entry{}, ours...), theirs...) {
		if !seen[e.key] {
			seen[e.key] = true
			out = append(out, e)
		}
	}
	return out
}

func leadingIndentOf(block string) string {
	for _, raw := range pySplitLines(block) {
		if pyStrip(raw) != "" {
			return raw[:len(raw)-len(pyLStrip(raw))]
		}
	}
	return ""
}

func base(p string) string { return filepath.Base(p) }

// rPyprojectDepsUnion is r_pyproject_deps_union.
func rPyprojectDepsUnion(file, hunk string) *Classification {
	if base(file) != "pyproject.toml" {
		return nil
	}
	ours, theirs, ok := splitConflictRegion(hunk)
	if !ok {
		return nil
	}
	oe, ok1 := parseDepLines(ours)
	te, ok2 := parseDepLines(theirs)
	if !ok1 || !ok2 {
		return nil
	}
	oOrder, oBy := lastByKey(oe)
	_, tBy := lastByKey(te)
	for _, name := range oOrder {
		if t, ok := tBy[name]; ok && pyStrip(oBy[name]) != pyStrip(t) {
			return &Classification{file, hunk, manual(fmt.Sprintf("%s: same-package version drift on '%s' — semantic conflict", RulePyprojectDeps, name))}
		}
	}
	merged := union(oe, te)
	if isSortedAlpha(oe) {
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].key < merged[j].key })
	}
	indent := leadingIndentOf(ours)
	var lines []string
	for _, e := range merged {
		lines = append(lines, indent+pyLStrip(e.line))
	}
	return &Classification{file, hunk, auto(strings.Join(lines, "\n")+"\n", RulePyprojectDeps)}
}

func parseImportLines(block string) ([]entry, bool) {
	var out []entry
	for _, raw := range pySplitLines(block) {
		st := pyStrip(raw)
		if st == "" || strings.HasPrefix(st, "#") {
			continue
		}
		if !reImportTarget.MatchString(raw) {
			return nil, false
		}
		out = append(out, entry{strings.ToLower(st), pyRStrip(raw)})
	}
	return out, true
}

func extractModule(raw string) (string, bool) {
	if m := reFromModule.FindStringSubmatch(raw); m != nil {
		return m[1], true
	}
	if m := reImportModule.FindStringSubmatch(raw); m != nil {
		return pyStrip(strings.SplitN(m[1], ",", 2)[0]), true
	}
	return "", false
}

func detectImportRename(ours, theirs []entry) (string, bool) {
	byMod := map[string]string{}
	for _, e := range ours {
		if mod, ok := extractModule(e.line); ok {
			if _, seen := byMod[mod]; !seen {
				byMod[mod] = pyStrip(e.line)
			}
		}
	}
	for _, e := range theirs {
		mod, ok := extractModule(e.line)
		if !ok {
			continue
		}
		if other, ok := byMod[mod]; ok && other != pyStrip(e.line) {
			return mod, true
		}
	}
	return "", false
}

// rInitImportsUnion is r_init_imports_union.
func rInitImportsUnion(file, hunk string) *Classification {
	if base(file) != "__init__.py" {
		return nil
	}
	ours, theirs, ok := splitConflictRegion(hunk)
	if !ok {
		return nil
	}
	oe, ok1 := parseImportLines(ours)
	te, ok2 := parseImportLines(theirs)
	if !ok1 || !ok2 {
		return nil
	}
	if mod, ok := detectImportRename(oe, te); ok {
		return &Classification{file, hunk, manual(fmt.Sprintf("%s: import statement modified for module '%s' — semantic conflict", RuleInitImports, mod))}
	}
	merged := union(oe, te)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].key < merged[j].key })
	var lines []string
	for _, e := range merged {
		lines = append(lines, e.line)
	}
	return &Classification{file, hunk, auto(strings.Join(lines, "\n")+"\n", RuleInitImports)}
}

func parseURLEntries(block string) ([]entry, bool) {
	var out []entry
	for _, raw := range pySplitLines(block) {
		st := pyStrip(raw)
		if st == "" || strings.HasPrefix(st, "#") {
			continue
		}
		m := reURLEntry.FindStringSubmatch(raw)
		if m == nil {
			return nil, false
		}
		key := strings.ToLower(strings.Trim(m[1], `'"`))
		line := pyRStrip(raw)
		if !strings.HasSuffix(line, ",") {
			line += ","
		}
		out = append(out, entry{key, line})
	}
	return out, true
}

func looksLikeURLsAssignment(line string) bool {
	st := pyStrip(line)
	if st == "" || strings.HasPrefix(st, "#") {
		return false
	}
	name, value, sep := strings.Cut(st, "=")
	if sep {
		name = strings.SplitN(name, ":", 2)[0]
	} else {
		name, value, sep = strings.Cut(st, ":")
	}
	if !sep {
		return false
	}
	cand := pyStrip(name)
	for _, prefix := range []string{"URL_PATTERNS", "_URLS", "URLS", "_URL", "URL"} {
		if cand != prefix {
			continue
		}
		value = pyLStrip(value)
		if value == "" {
			return false
		}
		r := []rune(value)[0]
		return unicode.IsLetter(r) || r == '['
	}
	return false
}

func isURLsListEligible(file, hunk string) bool {
	if base(file) == "urls.py" {
		return true
	}
	for _, l := range pySplitLines(hunk) {
		if looksLikeURLsAssignment(l) {
			return true
		}
	}
	return false
}

// rURLsListUnion is r_urls_list_union.
func rURLsListUnion(file, hunk string) *Classification {
	if !isURLsListEligible(file, hunk) {
		return nil
	}
	ours, theirs, ok := splitConflictRegion(hunk)
	if !ok {
		return nil
	}
	oe, ok1 := parseURLEntries(ours)
	te, ok2 := parseURLEntries(theirs)
	if !ok1 || !ok2 {
		return nil
	}
	tBy := map[string]string{}
	for _, e := range te {
		tBy[e.key] = pyStrip(e.line)
	}
	oOrder, oBy := lastByKey(oe)
	for _, k := range oOrder {
		if t, ok := tBy[k]; ok && pyStrip(oBy[k]) != t {
			return &Classification{file, hunk, manual(fmt.Sprintf("%s: same entry '%s' modified on both sides — semantic conflict", RuleURLsList, k))}
		}
	}
	merged := union(oe, te)
	if isSortedAlpha(oe) {
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].key < merged[j].key })
	}
	indent := leadingIndentOf(ours)
	var lines []string
	for _, e := range merged {
		lines = append(lines, indent+pyLStrip(e.line))
	}
	return &Classification{file, hunk, auto(strings.Join(lines, "\n")+"\n", RuleURLsList)}
}

func rUVLockRegenerate(file, hunk string) *Classification {
	if base(file) != uvLockFilename {
		return nil
	}
	return &Classification{file, hunk, auto("", RuleUVLock)}
}

func rDefaultManual(file, hunk string) *Classification {
	return &Classification{file, hunk, manual("no classifier rule matched " + file)}
}

var rules = []func(string, string) *Classification{
	rPyprojectDepsUnion, rInitImportsUnion, rURLsListUnion, rUVLockRegenerate, rDefaultManual,
}

// Classify is classify: the first rule that applies.
func Classify(file, hunk string) Classification {
	for _, r := range rules {
		if c := r(file, hunk); c != nil {
			return *c
		}
	}
	return Classification{file, hunk, manual("classifier exhausted with no match")}
}

// ValidateResolution is validate_resolution: an Auto resolution whose
// spliced file does not parse (.toml via a TOML 1.0 parser, .py via the
// Python parser) becomes Manual.
func ValidateResolution(c Classification, full string) Classification {
	if !c.Res.Auto || c.Res.RuleID == RuleUVLock {
		return c
	}
	var err error
	switch filepath.Ext(c.FilePath) {
	case ".toml":
		err = ValidateTOML(full)
	case ".py":
		_, err = pyast.Parse(full)
	}
	if err != nil {
		return Classification{c.FilePath, c.HunkText, manual(fmt.Sprintf("post-merge validation failed for %s: %s", c.FilePath, errRepr(c.FilePath, err)))}
	}
	return c
}

func errRepr(file string, err error) string {
	if filepath.Ext(file) == ".toml" {
		return "TOMLDecodeError(" + pyQuote(err.Error()) + ")"
	}
	return "SyntaxError(" + pyQuote(err.Error()) + ")"
}

func pyQuote(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// splitIntoRegions is _split_into_regions.
func splitIntoRegions(text string) ([]segment, int) {
	var segs []segment
	last, count := 0, 0
	for _, m := range reConflict.FindAllStringIndex(text, -1) {
		if m[0] > last {
			segs = append(segs, segment{false, text[last:m[0]]})
		}
		segs = append(segs, segment{true, text[m[0]:m[1]]})
		count++
		last = m[1]
	}
	if last < len(text) {
		segs = append(segs, segment{false, text[last:]})
	}
	return segs, count
}

type segment struct {
	conflict bool
	text     string
}

// ResolveText is the pure part of _process_conflicted_file: classify each
// conflict region of body, splice the Auto resolutions and validate the
// result. halt is the first Manual reason ("" when every region resolved).
func ResolveText(file, body string) (string, []Classification, string) {
	segs, n := splitIntoRegions(body)
	if n == 0 {
		return "", nil, fmt.Sprintf("file %s marked conflicted but contains no conflict markers", file)
	}
	var cls []Classification
	for _, s := range segs {
		if !s.conflict {
			continue
		}
		c := Classify(file, s.text)
		cls = append(cls, c)
		if !c.Res.Auto {
			return "", cls, c.Res.Reason
		}
	}
	var b strings.Builder
	i := 0
	for _, s := range segs {
		if s.conflict {
			b.WriteString(cls[i].Res.MergedText)
			i++
		} else {
			b.WriteString(s.text)
		}
	}
	rebuilt := b.String()
	var validated []Classification
	for _, c := range cls {
		v := ValidateResolution(c, rebuilt)
		validated = append(validated, v)
		if !v.Res.Auto {
			return "", validated, v.Res.Reason
		}
	}
	return rebuilt, validated, ""
}
