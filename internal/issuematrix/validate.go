package issuematrix

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Row is IssueMatrixRow.
type Row struct {
	Issue, Verdict, EvidenceRef         string
	Title, Scope, WP, FR, NFR, SC, Repo *string
}

// Diagnostic is one {diagnostic_code, message[, detail]} entry.
type Diagnostic struct{ Code, Message, Detail string }

// Result is IssueMatrixValidationResult.
type Result struct {
	Path        string
	Passed      bool
	Rows        []Row
	Diagnostics []Diagnostic
}

func (r *Result) add(code, msg, detail string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{code, msg, detail})
	r.Passed = false
}

// DiagnosticObjects renders the diagnostics list.
func (r *Result) DiagnosticObjects() []any {
	out := []any{}
	for _, d := range r.Diagnostics {
		o := jsonx.NewObject("diagnostic_code", d.Code, "message", d.Message)
		if d.Detail != "" {
			o.Set("detail", d.Detail)
		}
		out = append(out, o)
	}
	return out
}

var (
	tableRow   = regexp.MustCompile(`^\s*\|(.+)\|\s*$`)
	issueLink  = regexp.MustCompile(`^\[#(\d+)\]\(https?://[^)]+\)`)
	followupRe = regexp.MustCompile(`#\d+`)
	aliases    = map[string]string{"evidence ref": "evidence_ref", "wp_id": "wp", "fr(s)": "fr", "nfr(s)": "nfr", "theme": "scope"}
	mandatory  = []string{"issue", "verdict", "evidence_ref"}
	optional   = []string{"title", "scope", "wp", "fr", "nfr", "sc", "repo"}
)

func verdictsRepr() string {
	var p []string
	for _, v := range Verdicts {
		p = append(p, "'"+v+"'")
	}
	return "[" + strings.Join(p, ", ") + "]"
}

func validVerdict(v string) bool {
	for _, x := range Verdicts {
		if x == v {
			return true
		}
	}
	return false
}

func stripBackticks(v string) string {
	s := strings.TrimSpace(v)
	if len(s) >= 2 && strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		return s[1 : len(s)-1]
	}
	return s
}

func splitCells(row string) []string {
	parts := strings.Split(row, "|")
	if len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}
	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func pyStrList(xs []string) string {
	var p []string
	for _, x := range xs {
		p = append(p, "'"+strings.ReplaceAll(x, "'", `\'`)+"'")
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// Validate is validate_issue_matrix(path[, content]).
func Validate(path string, content *string) *Result {
	r := &Result{Path: path, Passed: true}
	var text string
	if content != nil {
		if strings.HasPrefix(strings.TrimLeft(*content, " \t\r\n"), "{") {
			return validateStructuredText(path, r, *content)
		}
		text = *content
	} else {
		jp := path
		if filepath.Base(path) != JSONFile {
			jp = filepath.Join(filepath.Dir(path), JSONFile)
		}
		if b, err := os.ReadFile(jp); err == nil {
			r.Path = jp
			return validateStructuredText(jp, r, string(b))
		}
		b, err := os.ReadFile(path)
		if err != nil {
			r.add(DiagMissing, "issue-matrix.md not found at "+path, "")
			return r
		}
		text = string(b)
	}
	lines := splitLines(text)
	type span struct{ s, e int }
	var tables []span
	for i := 0; i < len(lines); {
		if tableRow.MatchString(lines[i]) {
			s := i
			for i < len(lines) && tableRow.MatchString(lines[i]) {
				i++
			}
			tables = append(tables, span{s, i})
		} else {
			i++
		}
	}
	if len(tables) > 1 {
		var p []string
		for _, t := range tables {
			p = append(p, fmt.Sprintf("(%d, %d)", t.s+1, t.e))
		}
		r.add(DiagMultiTable, fmt.Sprintf("issue-matrix.md contains %d Markdown tables; exactly one is allowed.", len(tables)), "Table spans found at lines: ["+strings.Join(p, ", ")+"]")
		return r
	}
	if len(tables) == 0 {
		r.add(DiagMissing, "issue-matrix.md contains no Markdown table.", "")
		return r
	}
	tl := lines[tables[0].s:tables[0].e]
	if len(tl) < 2 {
		r.add(DiagSchemaDrift, "issue-matrix.md table has fewer than 2 rows (header + separator expected).", "")
		return r
	}
	var headers []string
	for _, h := range splitCells(tl[0]) {
		k := strings.ToLower(strings.TrimSpace(h))
		if a, ok := aliases[k]; ok {
			k = a
		}
		headers = append(headers, k)
	}
	has := func(c string) bool {
		for _, h := range headers {
			if h == c {
				return true
			}
		}
		return false
	}
	var missing []string
	for _, c := range mandatory {
		if !has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		r.add(DiagSchemaDrift, "Mandatory column(s) missing: "+strings.Join(missing, ", "), "Found columns after normalization: "+pyStrList(headers))
	}
	valid := map[string]bool{}
	for _, c := range append(append([]string{}, mandatory...), optional...) {
		valid[c] = true
	}
	var unknown []string
	for _, h := range headers {
		if !valid[h] {
			unknown = append(unknown, h)
		}
	}
	if len(unknown) > 0 {
		r.add(DiagSchemaDrift, "Unknown column(s) not in mandatory or named-optional vocabulary: "+strings.Join(unknown, ", "),
			"Valid columns: "+pyStrList(mandatory)+" (mandatory) + "+pyStrList(optional)+" (optional)")
	}
	if !r.Passed && len(missing) > 0 {
		return r
	}
	idx := map[string]int{}
	for i, h := range headers {
		idx[h] = i
	}
	cell := func(cells []string, col string) *string {
		i, ok := idx[col]
		if !ok || i >= len(cells) {
			return nil
		}
		v := stripBackticks(cells[i])
		if v == "" {
			return nil
		}
		return &v
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	for _, raw := range tl[2:] {
		if !tableRow.MatchString(raw) {
			continue
		}
		cells := splitCells(raw)
		issue := strings.TrimSpace(stripBackticks(deref(cell(cells, "issue"))))
		if m := issueLink.FindStringSubmatch(issue); m != nil {
			issue = "#" + m[1]
		}
		rawVerdict := deref(cell(cells, "verdict"))
		ev := deref(cell(cells, "evidence_ref"))
		if ev == "" {
			r.add(DiagEvidenceEmpty, fmt.Sprintf("Row for issue '%s': evidence_ref is empty.", issue), "")
		}
		v := strings.ToLower(strings.TrimSpace(rawVerdict))
		ok := validVerdict(v)
		if !ok {
			r.add(DiagVerdictUnknown, fmt.Sprintf("Row for issue '%s': verdict '%s' is not in the allowed set: %s", issue, rawVerdict, verdictsRepr()), "")
		}
		if ok && v == "deferred-with-followup" && !followupRe.MatchString(ev) && !strings.Contains(ev, "Follow-up:") {
			r.add(DiagDeferredNoHandle, fmt.Sprintf("Row for issue '%s': verdict is 'deferred-with-followup' but evidence_ref contains no follow-up handle (expected '#NNN' or 'Follow-up:' substring); got: '%s'", issue, ev), "")
		}
		if ok && (r.Passed || (len(missing) == 0 && len(unknown) == 0)) {
			r.Rows = append(r.Rows, Row{Issue: issue, Verdict: v, EvidenceRef: ev, Title: cell(cells, "title"), Scope: cell(cells, "scope"),
				WP: cell(cells, "wp"), FR: cell(cells, "fr"), NFR: cell(cells, "nfr"), SC: cell(cells, "sc"), Repo: cell(cells, "repo")})
		}
	}
	return r
}

func validateStructuredText(path string, r *Result, content string) *Result {
	r.Path = path
	rows, diags := ParseStructuredRows(content)
	r.Rows, r.Diagnostics = rows, diags
	r.Passed = len(diags) == 0
	return r
}

// ParseStructuredRows is _parse_structured_rows_text.
func ParseStructuredRows(content string) ([]Row, []Diagnostic) {
	v, err := pyjson.Loads(content)
	if err != nil {
		return nil, []Diagnostic{{DiagSchemaDrift, "issue-matrix.json could not be parsed: " + err.Error(), ""}}
	}
	var raw any
	if o, ok := v.(*jsonx.Object); ok {
		if x, ok := o.Get("rows"); ok {
			raw = x
		} else {
			raw = jsonx.NewObject()
		}
	} else {
		raw = jsonx.NewObject()
	}
	ro, ok := raw.(*jsonx.Object)
	if !ok {
		return nil, []Diagnostic{{DiagSchemaDrift, "issue-matrix.json 'rows' must be an object keyed by issue ref.", ""}}
	}
	var rows []Row
	var diags []Diagnostic
	for _, kv := range ro.Pairs() {
		e, ok := kv.Value.(*jsonx.Object)
		if !ok {
			diags = append(diags, Diagnostic{DiagSchemaDrift, fmt.Sprintf("Row for issue '%s': malformed entry (expected an object).", kv.Key), ""})
			continue
		}
		ev := ""
		if x, _ := e.Get("evidence_ref"); pyjson.Truthy(x) {
			ev = pyjson.Str(x)
		}
		if ev == "" {
			diags = append(diags, Diagnostic{DiagEvidenceEmpty, fmt.Sprintf("Row for issue '%s': evidence_ref is empty.", kv.Key), ""})
		}
		rv := ""
		x, _ := e.Get("verdict")
		if pyjson.Truthy(x) {
			rv = pyjson.Str(x)
		}
		verdict := strings.ToLower(strings.TrimSpace(rv))
		if !validVerdict(verdict) {
			shown := "None"
			if x != nil {
				shown = pyjson.Str(x)
			}
			diags = append(diags, Diagnostic{DiagVerdictUnknown, fmt.Sprintf("Row for issue '%s': verdict '%s' is not in the allowed set: %s", kv.Key, shown, verdictsRepr()), ""})
			continue
		}
		if verdict == "deferred-with-followup" && !followupRe.MatchString(ev) && !strings.Contains(ev, "Follow-up:") {
			diags = append(diags, Diagnostic{DiagDeferredNoHandle, fmt.Sprintf("Row for issue '%s': verdict is 'deferred-with-followup' but evidence_ref contains no follow-up handle (expected '#NNN' or 'Follow-up:' substring); got: '%s'", kv.Key, ev), ""})
		}
		rows = append(rows, Row{Issue: kv.Key, Verdict: verdict, EvidenceRef: ev, Title: strOrNil(e, "title"), Scope: strOrNil(e, "scope"),
			WP: strOrNil(e, "wp"), FR: strOrNil(e, "fr"), NFR: strOrNil(e, "nfr"), SC: strOrNil(e, "sc"), Repo: strOrNil(e, "repo")})
	}
	return rows, diags
}

const remedy = "Record a verdict per row with: spec-kitty agent issue-verdict --mission <handle> --issue <#NNN> --verdict <fixed|verified-already-fixed|deferred-with-followup|in-mission|not-applicable> --actor <actor> [--wp <WPnn>] [--evidence-ref <evidence>]"

func errorPrefix(dir string) string {
	_, ej := os.Stat(filepath.Join(dir, JSONFile))
	_, em := os.Stat(filepath.Join(dir, MDFile))
	if em == nil && ej != nil {
		return "ERROR: " + MDFile
	}
	return "ERROR: " + JSONFile
}

var rowIssueRe = regexp.MustCompile(`Row for issue '([^']+)'`)

// ApprovalBlocker is _issue_matrix_approval_blocker(feature_dir, target,
// primary_feature_dir) ("" = not blocked).
func ApprovalBlocker(featureDir, target, primaryDir string) string {
	specDir := featureDir
	if primaryDir != "" {
		if _, err := os.Stat(filepath.Join(primaryDir, "spec.md")); err == nil {
			specDir = primaryDir
		}
	}
	refs, err := DiscoverReferences(specDir)
	if err != nil {
		return fmt.Sprintf("%s could not be evaluated before approval.\nReason: %v\nFix the issue-matrix check before approving.", errorPrefix(featureDir), err)
	}
	var gating []string
	gset := map[string]bool{}
	for _, r := range refs {
		if r.Gating() {
			ref := fmt.Sprintf("#%d", r.Number)
			gating = append(gating, ref)
			gset[ref] = true
		}
	}
	if len(gating) == 0 {
		return ""
	}
	if !ArtifactPresent(featureDir) {
		return fmt.Sprintf("%s is required before approval.\nReferenced issues: %s\nFill verdicts before approving.\nThis file is normally scaffolded automatically. If it is missing, regenerate it: spec-kitty agent mission finalize-tasks --mission %s\n%s\nSchema and worked example: src/specify_cli/cli/commands/review/ERROR_CODES.md",
			errorPrefix(featureDir), strings.Join(gating, ", "), filepath.Base(featureDir), remedy)
	}
	res := Validate(filepath.Join(featureDir, MDFile), nil)
	inMatrix := map[string]bool{}
	for _, row := range res.Rows {
		inMatrix[row.Issue] = true
	}
	for _, d := range res.Diagnostics {
		if m := rowIssueRe.FindStringSubmatch(d.Message); m != nil {
			inMatrix[m[1]] = true
		}
	}
	var missing, inMission []string
	for ref := range gset {
		if !inMatrix[ref] {
			missing = append(missing, ref)
		}
	}
	sort.Strings(missing)
	if target == "done" {
		for _, row := range res.Rows {
			if row.Verdict == "in-mission" && gset[row.Issue] {
				inMission = append(inMission, row.Issue)
			}
		}
		sort.Strings(inMission)
	}
	if res.Passed && len(missing) == 0 && len(inMission) == 0 {
		return ""
	}
	lines := []string{errorPrefix(featureDir) + " has unresolved entries. Fill in verdicts before approving."}
	for _, d := range res.Diagnostics {
		msg := d.Message
		if d.Code == DiagSchemaDrift && d.Detail != "" {
			msg = fmt.Sprintf("%s (%s)", msg, d.Detail)
		}
		lines = append(lines, "- "+msg)
	}
	if len(missing) > 0 && len(res.Rows) > 0 {
		lines = append(lines, "Missing rows: "+strings.Join(missing, ", "))
	}
	if len(inMission) > 0 {
		lines = append(lines, "Still 'in-mission' (resolve to fixed / verified-already-fixed / deferred-with-followup before done): "+strings.Join(inMission, ", "))
	}
	lines = append(lines, remedy)
	return strings.Join(lines, "\n")
}

// RowsFromLegacy converts validated md rows into structured entries.
func RowsFromLegacy(rows []Row) *Rows {
	out := &Rows{}
	for _, r := range rows {
		out.Set(r.Issue, Entry{Verdict: r.Verdict, EvidenceRef: r.EvidenceRef, Title: r.Title, Scope: r.Scope, WP: r.WP, FR: r.FR, NFR: r.NFR, SC: r.SC, Repo: r.Repo})
	}
	return out
}
