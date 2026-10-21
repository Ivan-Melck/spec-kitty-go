// Package issuematrix ports the issue-matrix artifact (tasks/issue_matrix*.py,
// review/_issue_matrix.py): GitHub issue-reference discovery and gating
// classification, the structured issue-matrix.json document and the legacy
// issue-matrix.md table reader/validator, the finalize scaffold, the
// md->json migration and the move-task approval blocker.
package issuematrix

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// File names.
const (
	JSONFile = "issue-matrix.json"
	MDFile   = "issue-matrix.md"
)

// Gating classes.
const (
	ImplementationTarget = "implementation_target"
	ContextOnly          = "context_only"
	PROrCommitRef        = "pr_or_commit_ref"
)

// Verdicts (IssueMatrixVerdict) in enum order.
var Verdicts = []string{"fixed", "verified-already-fixed", "deferred-with-followup", "in-mission", "not-applicable"}

// Diagnostic codes.
const (
	DiagMissing          = "MISSION_REVIEW_ISSUE_MATRIX_MISSING"
	DiagSchemaDrift      = "MISSION_REVIEW_ISSUE_MATRIX_SCHEMA_DRIFT"
	DiagVerdictUnknown   = "MISSION_REVIEW_ISSUE_MATRIX_VERDICT_UNKNOWN"
	DiagMultiTable       = "MISSION_REVIEW_ISSUE_MATRIX_MULTI_TABLE"
	DiagEvidenceEmpty    = "MISSION_REVIEW_ISSUE_MATRIX_EVIDENCE_REF_EMPTY"
	DiagDeferredNoHandle = "MISSION_REVIEW_ISSUE_MATRIX_DEFERRED_WITHOUT_HANDLE"
)

// Occurrence is one (file, line) mention of an issue.
type Occurrence struct{ SourceFile, LineText string }

// Reference is IssueReference.
type Reference struct {
	Number         int
	FirstLine      string
	SourceFile     string
	Occurrences    []Occurrence
	Classification string
}

// Gating reports an implementation-target reference.
func (r Reference) Gating() bool { return r.Classification == ImplementationTarget }

var (
	prOrCommit    = regexp.MustCompile(`(?i)\bpr\s*#|\bpull\b|/pull/\d+`)
	contextMarker = regexp.MustCompile(`(?i)follow-up:|baseline-red|^\s*see\s*#|(?:parent|epic)\b[^#\n]{0,12}#`)
	ghURL         = regexp.MustCompile(`https?://github\.com/([\p{L}\p{N}_.-]+)/([\p{L}\p{N}_.-]+)/issues/(\d+)`)
	sameRepo      = map[string]bool{"spec-kitty/spec-kitty": true, "priivacy-ai/spec-kitty": true}
)

func classifyOccurrence(o Occurrence) string {
	if prOrCommit.MatchString(o.LineText) {
		return PROrCommitRef
	}
	if contextMarker.MatchString(o.LineText) {
		return ContextOnly
	}
	return ImplementationTarget
}

func classifyOccurrences(os []Occurrence) string {
	if len(os) == 0 {
		return ImplementationTarget
	}
	all := true
	for _, o := range os {
		c := classifyOccurrence(o)
		if c == ImplementationTarget {
			return ImplementationTarget
		}
		if c != PROrCommitRef {
			all = false
		}
	}
	if all {
		return PROrCommitRef
	}
	return ContextOnly
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

type issueMatch struct{ pos, num int }

// scanLine is _GH_ISSUE_PATTERN.finditer over one line (lookarounds done by hand).
func scanLine(line string) []issueMatch {
	var out []issueMatch
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '#' {
			continue
		}
		ok := i == 0
		if !ok {
			p := rs[i-1]
			ok = unicode.IsSpace(p) || p == '(' || p == '[' || p == '`' || p == '*' || (p == '/' && i >= 2 && unicode.IsDigit(rs[i-2]))
		}
		if !ok {
			continue
		}
		j := i + 1
		for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
			j++
		}
		n := j - i - 1
		if n < 2 || n > 6 {
			continue
		}
		follow := true
		if j < len(rs) {
			c := rs[j]
			switch {
			case unicode.IsSpace(c), strings.ContainsRune(")],;.`*\"'|—–", c):
			case strings.ContainsRune(":!?_", c):
				follow = !(j+1 < len(rs) && isWord(rs[j+1]))
			case c == '/':
				follow = j+1 < len(rs) && rs[j+1] == '#'
			default:
				follow = false
			}
		}
		if !follow {
			continue
		}
		v, _ := strconv.Atoi(string(rs[i+1 : j]))
		out = append(out, issueMatch{pos: i, num: v})
	}
	for _, m := range ghURL.FindAllStringSubmatchIndex(line, -1) {
		digits := line[m[6]:m[7]]
		if len(digits) < 2 || len(digits) > 6 {
			continue
		}
		if m[1] < len(line) {
			r, _ := utf8.DecodeRuneInString(line[m[1]:])
			if isWord(r) {
				continue
			}
		}
		if !sameRepo[strings.ToLower(line[m[2]:m[3]]+"/"+line[m[4]:m[5]])] {
			continue
		}
		v, _ := strconv.Atoi(digits)
		out = append(out, issueMatch{pos: utf8.RuneCountInString(line[:m[0]]), num: v})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].pos < out[b].pos })
	return out
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return nil
	}
	l := strings.Split(s, "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

// DetectReferences is detect_issue_references(path).
func DetectReferences(path string) ([]Reference, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := filepath.Base(path)
	first := map[int]string{}
	var order []int
	occ := map[int][]Occurrence{}
	seen := map[string]bool{}
	for _, line := range splitLines(string(b)) {
		st := strings.TrimSpace(line)
		for _, m := range scanLine(line) {
			if _, ok := first[m.num]; !ok {
				first[m.num] = st
				order = append(order, m.num)
			}
			k := strconv.Itoa(m.num) + "\x00" + st
			if !seen[k] {
				seen[k] = true
				occ[m.num] = append(occ[m.num], Occurrence{src, st})
			}
		}
	}
	var out []Reference
	for _, n := range order {
		out = append(out, Reference{Number: n, FirstLine: first[n], SourceFile: src, Occurrences: occ[n], Classification: classifyOccurrences(occ[n])})
	}
	return out, nil
}

// DiscoverReferences is discover_issue_references(feature_dir).
func DiscoverReferences(dir string) ([]Reference, error) {
	var paths []string
	for _, n := range []string{"spec.md", "plan.md", "research.md", "analysis-report.md"} {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.Mode().IsRegular() {
			paths = append(paths, filepath.Join(dir, n))
		}
	}
	for _, d := range []string{"tasks", "contracts"} {
		m, _ := filepath.Glob(filepath.Join(dir, d, "*.md"))
		sort.Strings(m)
		for _, p := range m {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				paths = append(paths, p)
			}
		}
	}
	type fs struct{ ctx, src string }
	first := map[int]fs{}
	var order []int
	occ := map[int][]Occurrence{}
	for _, p := range paths {
		refs, err := DetectReferences(p)
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			if _, ok := first[r.Number]; !ok {
				first[r.Number] = fs{r.FirstLine, r.SourceFile}
				order = append(order, r.Number)
			}
			occ[r.Number] = append(occ[r.Number], r.Occurrences...)
		}
	}
	var out []Reference
	for _, n := range order {
		out = append(out, Reference{Number: n, FirstLine: first[n].ctx, SourceFile: first[n].src, Occurrences: occ[n], Classification: classifyOccurrences(occ[n])})
	}
	return out, nil
}

// --- document -----------------------------------------------------------------

// EntryFields is _ENTRY_FIELDS.
var EntryFields = []string{"verdict", "evidence_ref", "title", "scope", "wp", "fr", "nfr", "sc", "repo", "source_file"}

// Entry is IssueMatrixEntry (nil pointers = None).
type Entry struct {
	Verdict, EvidenceRef                            string
	Title, Scope, WP, FR, NFR, SC, Repo, SourceFile *string
}

func ptr(s string) *string { return &s }

func sp(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Object renders to_dict().
func (e Entry) Object() *jsonx.Object {
	return jsonx.NewObject("verdict", e.Verdict, "evidence_ref", e.EvidenceRef, "title", sp(e.Title), "scope", sp(e.Scope), "wp", sp(e.WP),
		"fr", sp(e.FR), "nfr", sp(e.NFR), "sc", sp(e.SC), "repo", sp(e.Repo), "source_file", sp(e.SourceFile))
}

func strOrNil(o *jsonx.Object, k string) *string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return nil
	}
	s := pyjson.Str(v)
	return &s
}

// EntryFromObject is IssueMatrixEntry.from_dict.
func EntryFromObject(o *jsonx.Object) Entry {
	e := Entry{Verdict: "unknown"}
	if v, ok := o.Get("verdict"); ok && v != nil {
		e.Verdict = pyjson.Str(v)
	}
	if v, ok := o.Get("evidence_ref"); ok && v != nil {
		e.EvidenceRef = pyjson.Str(v)
	}
	e.Title, e.Scope, e.WP, e.FR = strOrNil(o, "title"), strOrNil(o, "scope"), strOrNil(o, "wp"), strOrNil(o, "fr")
	e.NFR, e.SC, e.Repo, e.SourceFile = strOrNil(o, "nfr"), strOrNil(o, "sc"), strOrNil(o, "repo"), strOrNil(o, "source_file")
	return e
}

// Rows is an ordered issue_ref -> Entry map.
type Rows struct {
	Order []string
	By    map[string]Entry
}

// Set adds or replaces a row (keeps first insertion position).
func (r *Rows) Set(ref string, e Entry) {
	if r.By == nil {
		r.By = map[string]Entry{}
	}
	if _, ok := r.By[ref]; !ok {
		r.Order = append(r.Order, ref)
	}
	r.By[ref] = e
}

// Render is json.dumps(build_issue_matrix_document(rows), indent=2, sort_keys=True)+"\n".
func (r *Rows) Render() []byte {
	rows := jsonx.NewObject()
	for _, k := range r.Order {
		rows.Set(k, r.By[k].Object())
	}
	return []byte(jsonx.Dumps(jsonx.NewObject("schema_version", 1, "rows", rows), jsonx.SortedPretty) + "\n")
}

// ParseDocument is parse_issue_matrix_document.
func ParseDocument(o *jsonx.Object) *Rows {
	r := &Rows{}
	raw := o.GetObject("rows")
	if raw == nil {
		return r
	}
	for _, kv := range raw.Pairs() {
		if eo := jsonx.ToObject(kv.Value); eo != nil {
			r.Set(kv.Key, EntryFromObject(eo))
		}
	}
	return r
}

// LoadRows reads <dir>/issue-matrix.json rows (empty when missing/invalid).
func LoadRows(dir string) *Rows {
	b, err := os.ReadFile(filepath.Join(dir, JSONFile))
	if err != nil {
		return &Rows{}
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return &Rows{}
	}
	return ParseDocument(o)
}

// ScaffoldRows builds the scaffold rows for refs.
func ScaffoldRows(refs []Reference) *Rows {
	r := &Rows{}
	for _, ref := range refs {
		src := ptr(ref.SourceFile)
		if ref.Gating() {
			r.Set(fmt.Sprintf("#%d", ref.Number), Entry{Verdict: "unknown", EvidenceRef: "<link or commit>", Title: ptr("<fill at WP-implementation time>"), SourceFile: src})
		} else {
			r.Set(fmt.Sprintf("#%d", ref.Number), Entry{Verdict: "not-applicable", EvidenceRef: "<auto-classified: no action required>", Title: ptr("<auto-classified: " + ref.Classification + ">"), SourceFile: src})
		}
	}
	return r
}

// ArtifactPresent is issue_matrix_artifact_present.
func ArtifactPresent(dir string) bool {
	for _, n := range []string{JSONFile, MDFile} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}
