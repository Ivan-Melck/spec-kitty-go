package mergedriver

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Verdict sentinels per matrix kind (unset placeholders yield to authored
// values instead of failing closed, #4880).
var (
	issueMatrixSentinels         = map[string]string{"verdict": "unknown"}
	acceptanceCriterionSentinels = map[string]string{"pass_fail": "pending"}
	acceptanceInvariantSentinels = map[string]string{"result": "pending"}
)

// IssueMatrixSchemaVersion is ISSUE_MATRIX_SCHEMA_VERSION.
const IssueMatrixSchemaVersion = 1

func rowErr(msg string) error { return newError(KindRowMatrix, msg) }

// parseJSONDocument is _parse_json_document (missing/blank -> empty).
func parseJSONDocument(path string) (*jsonx.Object, error) {
	if !exists(path) {
		return &jsonx.Object{}, nil
	}
	text, err := readTextCrash(path)
	if err != nil {
		return nil, err
	}
	text = pyStrip(text)
	if text == "" {
		return &jsonx.Object{}, nil
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, rowErr(fmt.Sprintf("%s: not valid JSON (%s)", path, err))
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, rowErr(fmt.Sprintf("%s: matrix document is not a JSON object", path))
	}
	return o, nil
}

func getOr(o *jsonx.Object, k string, def any) any {
	if v, ok := o.Get(k); ok {
		return v
	}
	return def
}

func mergeField(name string, base, ours, theirs any, sentinels map[string]string, rowKey *string) (any, error) {
	if pyjson.Equal(ours, theirs) {
		return ours, nil
	}
	if pyjson.Equal(ours, base) {
		return theirs, nil
	}
	if pyjson.Equal(theirs, base) {
		return ours, nil
	}
	sentinel, ok := sentinels[name]
	if !ok {
		return ours, nil
	}
	if pyjson.Equal(theirs, sentinel) && !pyjson.Equal(ours, sentinel) {
		return ours, nil
	}
	if pyjson.Equal(ours, sentinel) && !pyjson.Equal(theirs, sentinel) {
		return theirs, nil
	}
	label := ""
	if rowKey != nil {
		label = "row " + pyjson.ReprString(*rowKey) + ": "
	}
	return nil, rowErr(fmt.Sprintf("%sverdict field %s diverged on both sides with no common base value (target/ours=%s, incoming/theirs=%s)",
		label, pyjson.ReprString(name), pyjson.Repr(ours), pyjson.Repr(theirs)))
}

func mergeRowFields(base, ours, theirs *jsonx.Object, sentinels map[string]string, rowKey *string) (*jsonx.Object, error) {
	if base == nil {
		base = &jsonx.Object{}
	}
	var names []string
	seen := map[string]bool{}
	for _, o := range []*jsonx.Object{base, ours, theirs} {
		for _, k := range o.Keys() {
			if !seen[k] {
				seen[k] = true
				names = append(names, k)
			}
		}
	}
	out := &jsonx.Object{}
	for _, n := range names {
		b, _ := base.Get(n)
		o, _ := ours.Get(n)
		t, _ := theirs.Get(n)
		v, err := mergeField(n, b, o, t, sentinels, rowKey)
		if err != nil {
			return nil, err
		}
		out.Set(n, v)
	}
	return out, nil
}

func reconcileRow(base, ours, theirs *jsonx.Object, sentinels map[string]string, rowKey *string) (*jsonx.Object, error) {
	if base == nil {
		switch {
		case ours == nil && theirs == nil:
			return nil, nil
		case ours == nil:
			return theirs.Clone(), nil
		case theirs == nil:
			return ours.Clone(), nil
		case pyjson.Equal(ours, theirs):
			return ours.Clone(), nil
		}
		return mergeRowFields(nil, ours, theirs, sentinels, rowKey)
	}
	if ours == nil {
		if theirs == nil || pyjson.Equal(theirs, base) {
			return nil, nil
		}
		return theirs.Clone(), nil
	}
	if theirs == nil {
		if pyjson.Equal(ours, base) {
			return nil, nil
		}
		return ours.Clone(), nil
	}
	if pyjson.Equal(ours, theirs) {
		return ours.Clone(), nil
	}
	return mergeRowFields(base, ours, theirs, sentinels, rowKey)
}

type keyFunc func(rawKey any, row *jsonx.Object) string

type keyedRows struct {
	keys []string
	rows map[string]*jsonx.Object
}

// canonicalizeKeyedRows is _canonicalize_keyed_rows.
func canonicalizeKeyedRows(rows any, keyOf keyFunc) (keyedRows, error) {
	out := keyedRows{rows: map[string]*jsonx.Object{}}
	type item struct {
		k any
		v any
	}
	var items []item
	switch t := rows.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			items = append(items, item{kv.Key, kv.Value})
		}
	case []any:
		for i, v := range t {
			items = append(items, item{i, v})
		}
	case string:
		// iterating a str yields 1-char strs, never rows
	default:
		if pyjson.Truthy(rows) {
			return out, &Crash{Type: "TypeError", Msg: fmt.Sprintf("'%s' object is not iterable", pyjson.TypeName(rows))}
		}
	}
	for _, it := range items {
		row, ok := it.v.(*jsonx.Object)
		if !ok {
			continue
		}
		key := keyOf(it.k, row)
		if prev, ok := out.rows[key]; ok {
			if !pyjson.Equal(prev, row) {
				return out, rowErr(fmt.Sprintf("intra-side duplicate row key %s: two distinct rows on "+
					"one side normalize to the same canonical key — refusing to "+
					"silently collapse either (#2970-adjacent row-merge guard)", pyjson.ReprString(key)))
			}
		} else {
			out.keys = append(out.keys, key)
		}
		out.rows[key] = row.Clone()
	}
	return out, nil
}

// reconcileKeyedRows is _reconcile_keyed_rows: merged rows in sorted key order.
func reconcileKeyedRows(baseRows, oursRows, theirsRows any, keyOf keyFunc, sentinels map[string]string) ([]string, map[string]*jsonx.Object, error) {
	b, err := canonicalizeKeyedRows(baseRows, keyOf)
	if err != nil {
		return nil, nil, err
	}
	o, err := canonicalizeKeyedRows(oursRows, keyOf)
	if err != nil {
		return nil, nil, err
	}
	t, err := canonicalizeKeyedRows(theirsRows, keyOf)
	if err != nil {
		return nil, nil, err
	}
	all := map[string]bool{}
	for _, kr := range []keyedRows{b, o, t} {
		for _, k := range kr.keys {
			all[k] = true
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	merged := map[string]*jsonx.Object{}
	var order []string
	for _, k := range keys {
		key := k
		row, err := reconcileRow(b.rows[k], o.rows[k], t.rows[k], sentinels, &key)
		if err != nil {
			return nil, nil, err
		}
		if row != nil {
			merged[k] = row
			order = append(order, k)
		}
	}
	return order, merged, nil
}

var issueRefDigits = regexp.MustCompile(`(\p{Nd}+)` + reSStar + `$`)

// CanonicalizeIssueRef normalizes "#1726" / "GH-1726" / "1726" to "#1726".
func CanonicalizeIssueRef(raw string) string {
	s := pyStrip(raw)
	if m := issueRefDigits.FindStringSubmatch(s); m != nil {
		return "#" + m[1]
	}
	return s
}

// ReconcileIssueMatrixDocuments is reconcile_issue_matrix_documents.
func ReconcileIssueMatrixDocuments(base, ours, theirs *jsonx.Object) (*jsonx.Object, error) {
	keyOf := func(raw any, _ *jsonx.Object) string { return CanonicalizeIssueRef(pyjson.Str(raw)) }
	order, rows, err := reconcileKeyedRows(getOr(base, "rows", &jsonx.Object{}), getOr(ours, "rows", &jsonx.Object{}), getOr(theirs, "rows", &jsonx.Object{}), keyOf, issueMatrixSentinels)
	if err != nil {
		return nil, err
	}
	ro := &jsonx.Object{}
	for _, k := range order {
		ro.Set(k, rows[k])
	}
	return jsonx.NewObject("schema_version", IssueMatrixSchemaVersion, "rows", ro), nil
}

// RunIssueMatrix is run_issue_matrix_driver (merge-driver-issue-matrix).
func RunIssueMatrix(basePath, oursPath, theirsPath string) (Outcome, error) {
	base, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	docs, err := parseThree(base, ours, theirs)
	if err != nil {
		return Outcome{}, err
	}
	merged, err := ReconcileIssueMatrixDocuments(docs[0], docs[1], docs[2])
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{}, writeText(ours, jsonx.Dumps(merged, jsonx.SortedPretty)+"\n")
}

func parseThree(base, ours, theirs string) ([3]*jsonx.Object, error) {
	var docs [3]*jsonx.Object
	for i, p := range []string{base, ours, theirs} {
		d, err := parseJSONDocument(p)
		if err != nil {
			return docs, err
		}
		docs[i] = d
	}
	return docs, nil
}

// ---------------------------------------------------------------------------
// acceptance-matrix.json
// ---------------------------------------------------------------------------

// AcceptanceMatrixParseError is acceptance.matrix.AcceptanceMatrixParseError.
type AcceptanceMatrixParseError struct {
	Section string
	Index   int
	Reason  string
}

func (e *AcceptanceMatrixParseError) Error() string {
	return fmt.Sprintf("%s[%d]: malformed shape (%s)", e.Section, e.Index, e.Reason)
}

const scaffoldTodoMarker = "TODO: replace with a real acceptance criterion"

type rowSchema struct {
	class    string
	fields   []string // declaration order (excluding extras)
	required int      // first n fields are required
	defaults map[string]any
}

var criterionSchema = rowSchema{
	class:    "AcceptanceCriterion",
	fields:   []string{"criterion_id", "description", "proof_type", "evidence", "pass_fail", "verified_by", "verified_at", "notes"},
	required: 3,
	defaults: map[string]any{"pass_fail": "pending"},
}

var invariantSchema = rowSchema{
	class: "NegativeInvariant",
	fields: []string{"invariant_id", "description", "verification_method", "verification_command", "result", "evidence",
		"scope", "verified_ref", "verified_surface_kind", "deferred_reason", "deferred_to_phase", "provenance_origin"},
	required: 3,
	defaults: map[string]any{"result": "pending", "provenance_origin": "legacy_unrecorded"},
}

// parsedRow is a dataclass instance: known field values + extras.
type parsedRow struct {
	values map[string]any
	extras *jsonx.Object
}

func missingArgs(class string, names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "'" + n + "'"
	}
	var list string
	switch len(q) {
	case 1:
		list = q[0]
	case 2:
		list = q[0] + " and " + q[1]
	default:
		list = strings.Join(q[:len(q)-1], ", ") + ", and " + q[len(q)-1]
	}
	plural := "argument"
	if len(q) > 1 {
		plural = "arguments"
	}
	return fmt.Sprintf("%s.__init__() missing %d required positional %s: %s", class, len(q), plural, list)
}

func (s rowSchema) parse(row *jsonx.Object) (parsedRow, string) {
	known := map[string]bool{}
	for _, f := range s.fields {
		known[f] = true
	}
	pr := parsedRow{values: map[string]any{}, extras: &jsonx.Object{}}
	for _, kv := range row.Pairs() {
		if known[kv.Key] {
			pr.values[kv.Key] = kv.Value
		} else {
			pr.extras.Set(kv.Key, kv.Value)
		}
	}
	var missing []string
	for _, f := range s.fields[:s.required] {
		if _, ok := pr.values[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return pr, missingArgs(s.class, missing)
	}
	for _, f := range s.fields[s.required:] {
		if _, ok := pr.values[f]; !ok {
			pr.values[f] = s.defaults[f]
		}
	}
	return pr, ""
}

var conflictMarkers = []string{"<<<<<<<", "=======", ">>>>>>>"}

func (s rowSchema) rejectMarkers(rows []parsedRow, section string) error {
	for i, r := range rows {
		rowKey := r.values[s.fields[0]]
		for _, f := range s.fields {
			str, ok := r.values[f].(string)
			if !ok {
				continue
			}
			for _, m := range conflictMarkers {
				if strings.Contains(str, m) {
					return &AcceptanceMatrixParseError{section, i, fmt.Sprintf("field %s on row %s contains an unresolved conflict marker %s",
						pyjson.ReprString(f), pyjson.Repr(rowKey), pyjson.ReprString(m))}
				}
			}
		}
	}
	return nil
}

func (s rowSchema) toDict(r parsedRow, omitNone []string, omitDefault map[string]string) *jsonx.Object {
	o := &jsonx.Object{}
	for _, f := range s.fields {
		o.Set(f, pyjson.Copy(r.values[f]))
	}
	for _, k := range omitNone {
		if v, _ := o.Get(k); v == nil {
			o.Delete(k)
		}
	}
	for k, def := range omitDefault {
		if v, _ := o.Get(k); pyjson.Equal(v, def) {
			o.Delete(k)
		}
	}
	for _, kv := range r.extras.Pairs() {
		o.Set(kv.Key, pyjson.Copy(kv.Value))
	}
	return o
}

var missionNumberPrefix = regexp.MustCompile(`^(\p{Nd}+)-`)

func missionNumberFromSlug(slug string) (string, bool) {
	m := missionNumberPrefix.FindStringSubmatch(pyStrip(slug))
	if m == nil {
		return "", false
	}
	digits := []rune(m[1])
	n := 0
	for _, r := range digits {
		n = n*10 + digitValue(r)
	}
	return fmt.Sprint(n), true
}

func digitValue(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	// Unicode Nd digits come in contiguous runs of ten starting at zero.
	z := r
	for r-z < 9 && unicode.Is(unicode.Nd, z-1) {
		z--
	}
	return int(r - z)
}

// MissionIdentityFields is mission_metadata.mission_identity_fields.
func MissionIdentityFields(slug, number, mtype any) (string, string, string) {
	rs := pyStrip(pyjson.Str(slug))
	var rn string
	switch number.(type) {
	case int, int64, bool:
		if b, ok := number.(bool); ok {
			rn = map[bool]string{true: "True", false: "False"}[b]
		} else {
			rn = pyjson.Str(number)
		}
	default:
		if pyjson.Truthy(number) {
			rn = pyStrip(pyjson.Str(number))
		}
	}
	if rn == "" {
		if n, ok := missionNumberFromSlug(rs); ok {
			rn = n
		}
	}
	rt := ""
	if pyjson.Truthy(mtype) {
		rt = pyStrip(pyjson.Str(mtype))
	}
	return rs, rn, rt
}

func overallVerdict(criteria, invariants []parsedRow) string {
	var cr, ir []any
	for _, c := range criteria {
		cr = append(cr, c.values["pass_fail"])
	}
	for _, n := range invariants {
		ir = append(ir, n.values["result"])
	}
	if len(cr) == 0 && len(ir) == 0 {
		return "pending"
	}
	allowed := func(v any, set ...string) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		for _, x := range set {
			if s == x {
				return true
			}
		}
		return false
	}
	for _, v := range cr {
		if !allowed(v, "pass", "fail", "pending") {
			return "fail"
		}
	}
	for _, v := range ir {
		if !allowed(v, "confirmed_absent", "still_present", "verification_error", "pending", "deferred_to_consolidation") {
			return "fail"
		}
	}
	for _, v := range cr {
		if v == "fail" {
			return "fail"
		}
	}
	for _, v := range ir {
		if v == "still_present" || v == "verification_error" {
			return "fail"
		}
	}
	var dominant []any
	for _, c := range criteria {
		if !pyjson.Equal(c.values["description"], scaffoldTodoMarker) {
			dominant = append(dominant, c.values["pass_fail"])
		}
	}
	if len(dominant) == 0 {
		dominant = cr
	}
	for _, v := range append(dominant, ir...) {
		if v == "pending" {
			return "pending"
		}
	}
	for _, v := range ir {
		if v == "deferred_to_consolidation" {
			return "pass_pending_consolidation"
		}
	}
	return "pass"
}

func parseRows(raw []*jsonx.Object, s rowSchema, section string) ([]parsedRow, error) {
	var out []parsedRow
	for i, r := range raw {
		pr, msg := s.parse(r)
		if msg != "" {
			return nil, &AcceptanceMatrixParseError{section, i, msg}
		}
		out = append(out, pr)
	}
	return out, nil
}

// ReconcileAcceptanceMatrixDocuments is reconcile_acceptance_matrix_documents:
// a 3-way row merge round-tripped through AcceptanceMatrix (overall_verdict
// recomputed). A malformed merged row yields *AcceptanceMatrixParseError.
func ReconcileAcceptanceMatrixDocuments(base, ours, theirs *jsonx.Object) (*jsonx.Object, error) {
	field := func(name string) keyFunc {
		return func(_ any, row *jsonx.Object) string { return pyjson.Str(getOr(row, name, "")) }
	}
	cOrder, cRows, err := reconcileKeyedRows(getOr(base, "criteria", []any{}), getOr(ours, "criteria", []any{}), getOr(theirs, "criteria", []any{}), field("criterion_id"), acceptanceCriterionSentinels)
	if err != nil {
		return nil, err
	}
	iOrder, iRows, err := reconcileKeyedRows(getOr(base, "negative_invariants", []any{}), getOr(ours, "negative_invariants", []any{}), getOr(theirs, "negative_invariants", []any{}), field("invariant_id"), acceptanceInvariantSentinels)
	if err != nil {
		return nil, err
	}
	// _reconcile_identity_fields
	ident := map[string]any{}
	for _, name := range []string{"mission_slug", "mission_number", "mission_type"} {
		ov, _ := ours.Get(name)
		if ov == nil || ov == "" {
			ov, _ = theirs.Get(name)
		}
		ident[name] = ov
	}
	if ident["mission_slug"] == nil || ident["mission_slug"] == "" {
		ident["mission_slug"] = getOr(base, "mission_slug", "")
	}
	var crit, inv []*jsonx.Object
	for _, k := range cOrder {
		crit = append(crit, cRows[k])
	}
	for _, k := range iOrder {
		inv = append(inv, iRows[k])
	}
	criteria, err := parseRows(crit, criterionSchema, "criteria")
	if err != nil {
		return nil, err
	}
	invariants, err := parseRows(inv, invariantSchema, "negative_invariants")
	if err != nil {
		return nil, err
	}
	if err := criterionSchema.rejectMarkers(criteria, "criteria"); err != nil {
		return nil, err
	}
	if err := invariantSchema.rejectMarkers(invariants, "negative_invariants"); err != nil {
		return nil, err
	}
	slug, _, _ := MissionIdentityFields(ident["mission_slug"], ident["mission_number"], ident["mission_type"])
	// AcceptanceMatrix keeps the raw mission_number/mission_type and
	// re-normalizes them in to_dict.
	s, n, t := MissionIdentityFields(slug, ident["mission_number"], ident["mission_type"])
	out := jsonx.NewObject("mission_slug", s, "mission_number", n, "mission_type", t, "overall_verdict", overallVerdict(criteria, invariants))
	cl := []any{}
	for _, c := range criteria {
		cl = append(cl, criterionSchema.toDict(c, nil, nil))
	}
	il := []any{}
	for _, n := range invariants {
		il = append(il, invariantSchema.toDict(n, []string{"scope", "verified_ref", "verified_surface_kind", "deferred_reason", "deferred_to_phase"},
			map[string]string{"provenance_origin": "legacy_unrecorded"}))
	}
	out.Set("criteria", cl)
	out.Set("negative_invariants", il)
	return out, nil
}

// RunAcceptanceMatrix is run_acceptance_matrix_driver.
func RunAcceptanceMatrix(basePath, oursPath, theirsPath string) (Outcome, error) {
	base, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	docs, err := parseThree(base, ours, theirs)
	if err != nil {
		return Outcome{}, err
	}
	merged, err := ReconcileAcceptanceMatrixDocuments(docs[0], docs[1], docs[2])
	if err != nil {
		var pe *AcceptanceMatrixParseError
		if errors.As(err, &pe) {
			return Outcome{}, newError(KindGeneric, pe.Error())
		}
		return Outcome{}, err
	}
	return Outcome{}, writeText(ours, jsonx.Dumps(merged, jsonx.Pretty)+"\n")
}
