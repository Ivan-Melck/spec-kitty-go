// Package acceptance implements the mission acceptance surface: the
// acceptance matrix (acceptance-matrix.json), the per-surface commit outcome
// contract, the readiness summary (collect_feature_summary) and the
// acceptance commit sequence (perform_acceptance).
package acceptance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// MatrixFile is the acceptance matrix filename.
const MatrixFile = "acceptance-matrix.json"

// Verdict / result vocabularies.
const (
	VerdictPassPendingConsolidation = "pass_pending_consolidation"
	DeferredToConsolidation         = "deferred_to_consolidation"
	ProvenanceRecorded              = "recorded"
	ProvenanceLegacyUnrecorded      = "legacy_unrecorded"
	ScaffoldTODOMarker              = "TODO: replace with a real acceptance criterion"
	autoDerivedNote                 = "Auto-derived from WP review evidence (IC-04 gate-side capture, FR-008)."
)

var (
	criterionVerdicts  = map[string]bool{"pass": true, "fail": true, "pending": true}
	terminalInvariants = map[string]bool{"confirmed_absent": true, "still_present": true, "verification_error": true}
	invariantResults   = map[string]bool{"confirmed_absent": true, "still_present": true, "verification_error": true, "pending": true, DeferredToConsolidation: true}
	provenanceOrigins  = map[string]bool{ProvenanceRecorded: true, ProvenanceLegacyUnrecorded: true}
)

// criterionFields is the AcceptanceCriterion dataclass field order.
var criterionFields = []string{"criterion_id", "description", "proof_type", "evidence", "pass_fail", "verified_by", "verified_at", "notes"}

// invariantFields is the NegativeInvariant dataclass field order.
var invariantFields = []string{"invariant_id", "description", "verification_method", "verification_command", "result", "evidence",
	"scope", "verified_ref", "verified_surface_kind", "deferred_reason", "deferred_to_phase", "provenance_origin"}

var invariantOmitNull = map[string]bool{"scope": true, "verified_ref": true, "verified_surface_kind": true, "deferred_reason": true, "deferred_to_phase": true}

// Matrix is an AcceptanceMatrix. Criteria and NegativeInvariants are rows in
// their dataclass shape (known fields + preserved extras).
type Matrix struct {
	MissionSlug        string
	MissionNumber      any // string or nil
	MissionType        any // string or nil
	Criteria           []*jsonx.Object
	NegativeInvariants []*jsonx.Object
	Extras             *jsonx.Object
}

// ParseError is AcceptanceMatrixParseError.
type ParseError struct {
	Section string
	Index   int
	Reason  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s[%d]: malformed shape (%s)", e.Section, e.Index, e.Reason)
}

func str(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func isNull(o *jsonx.Object, k string) bool {
	v, ok := o.Get(k)
	return !ok || v == nil
}

// normalizeRow returns a row with known fields in dataclass order (defaults
// filled) followed by extras.
func normalizeRow(raw *jsonx.Object, fields []string, defaults map[string]any) *jsonx.Object {
	out := &jsonx.Object{}
	known := map[string]bool{}
	for _, f := range fields {
		known[f] = true
		if v, ok := raw.Get(f); ok {
			out.Set(f, v)
		} else {
			out.Set(f, defaults[f])
		}
	}
	for _, kv := range raw.Pairs() {
		if !known[kv.Key] {
			out.Set(kv.Key, kv.Value)
		}
	}
	return out
}

var criterionDefaults = map[string]any{"pass_fail": "pending"}
var invariantDefaults = map[string]any{"result": "pending", "provenance_origin": ProvenanceLegacyUnrecorded}

func parseRows(raw []any, section string, required []string, fields []string, defaults map[string]any) ([]*jsonx.Object, error) {
	out := []*jsonx.Object{}
	for i, r := range raw {
		o, ok := r.(*jsonx.Object)
		if !ok {
			return nil, &ParseError{section, i, "argument after ** must be a mapping"}
		}
		for _, k := range required {
			if !o.Has(k) {
				return nil, &ParseError{section, i, fmt.Sprintf("missing required argument: '%s'", k)}
			}
		}
		row := normalizeRow(o, fields, defaults)
		for _, f := range fields {
			if s, ok := row.Get(f); ok {
				if t, ok := s.(string); ok {
					for _, m := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
						if strings.Contains(t, m) {
							id := str(row, fields[0])
							return nil, &ParseError{section, i, fmt.Sprintf("field %s on row %s contains an unresolved conflict marker %s",
								mission.PyRepr(f), mission.PyRepr(id), mission.PyRepr(m))}
						}
					}
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// MatrixFromObject is AcceptanceMatrix.from_dict.
func MatrixFromObject(data *jsonx.Object) (*Matrix, error) {
	m := &Matrix{MissionSlug: strings.TrimSpace(str(data, "mission_slug")), Extras: &jsonx.Object{}}
	var err error
	if m.Criteria, err = parseRows(data.GetList("criteria"), "criteria", []string{"criterion_id", "description", "proof_type"}, criterionFields, criterionDefaults); err != nil {
		return nil, err
	}
	if m.NegativeInvariants, err = parseRows(data.GetList("negative_invariants"), "negative_invariants",
		[]string{"invariant_id", "description", "verification_method"}, invariantFields, invariantDefaults); err != nil {
		return nil, err
	}
	idNumber, idType := identityNumberType(m.MissionSlug, mustGet(data, "mission_number"), mustGet(data, "mission_type"))
	if v, ok := data.Get("mission_number"); ok {
		m.MissionNumber = v
	} else {
		m.MissionNumber = idNumber
	}
	if v, ok := data.Get("mission_type"); ok {
		m.MissionType = v
	} else {
		m.MissionType = idType
	}
	for _, kv := range data.Pairs() {
		switch kv.Key {
		case "mission_slug", "mission_number", "mission_type", "criteria", "negative_invariants", "overall_verdict":
		default:
			m.Extras.Set(kv.Key, kv.Value)
		}
	}
	return m, nil
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// identityNumberType is mission_identity_fields' number/type normalisation.
func identityNumberType(slug string, number, missionType any) (string, string) {
	var n string
	switch v := number.(type) {
	case nil:
	case string:
		n = strings.TrimSpace(v)
	case int:
		n = fmt.Sprint(v)
	case float64:
		n = fmt.Sprint(int(v))
	default:
		n = strings.TrimSpace(fmt.Sprint(v))
	}
	if n == "" {
		if k, ok := mission.NumberFromSlug(slug); ok {
			n = fmt.Sprint(k)
		}
	}
	t, _ := missionType.(string)
	return n, strings.TrimSpace(t)
}

func allowed(v any, set map[string]bool) bool {
	s, ok := v.(string)
	return ok && set[s]
}

func isEmptyScaffold(c *jsonx.Object) bool { return str(c, "description") == ScaffoldTODOMarker }

// OverallVerdict is AcceptanceMatrix.overall_verdict.
func (m *Matrix) OverallVerdict() string {
	var crit, inv []any
	for _, c := range m.Criteria {
		crit = append(crit, mustGet(c, "pass_fail"))
	}
	for _, n := range m.NegativeInvariants {
		inv = append(inv, mustGet(n, "result"))
	}
	if len(crit) == 0 && len(inv) == 0 {
		return "pending"
	}
	for _, v := range crit {
		if !allowed(v, criterionVerdicts) {
			return "fail"
		}
	}
	for _, v := range inv {
		if !allowed(v, invariantResults) {
			return "fail"
		}
	}
	for _, v := range crit {
		if v == "fail" {
			return "fail"
		}
	}
	for _, v := range inv {
		if v == "still_present" || v == "verification_error" {
			return "fail"
		}
	}
	var dominant []any
	for _, c := range m.Criteria {
		if !isEmptyScaffold(c) {
			dominant = append(dominant, mustGet(c, "pass_fail"))
		}
	}
	if len(dominant) == 0 {
		dominant = crit
	}
	for _, v := range append(append([]any{}, dominant...), inv...) {
		if v == "pending" {
			return "pending"
		}
	}
	for _, v := range inv {
		if v == DeferredToConsolidation {
			return VerdictPassPendingConsolidation
		}
	}
	return "pass"
}

func invariantToObject(n *jsonx.Object) *jsonx.Object {
	out := &jsonx.Object{}
	for _, kv := range n.Pairs() {
		if invariantOmitNull[kv.Key] && kv.Value == nil {
			continue
		}
		if kv.Key == "provenance_origin" && kv.Value == ProvenanceLegacyUnrecorded {
			continue
		}
		out.Set(kv.Key, kv.Value)
	}
	return out
}

// ToObject is AcceptanceMatrix.to_dict.
func (m *Matrix) ToObject() *jsonx.Object {
	num, typ := identityNumberType(m.MissionSlug, m.MissionNumber, m.MissionType)
	crit := []any{}
	for _, c := range m.Criteria {
		crit = append(crit, c)
	}
	inv := []any{}
	for _, n := range m.NegativeInvariants {
		inv = append(inv, invariantToObject(n))
	}
	o := jsonx.NewObject("mission_slug", m.MissionSlug, "mission_number", num, "mission_type", typ,
		"overall_verdict", m.OverallVerdict(), "criteria", crit, "negative_invariants", inv)
	if m.Extras != nil {
		for _, kv := range m.Extras.Pairs() {
			o.Set(kv.Key, kv.Value)
		}
	}
	return o
}

// Clone deep-copies the matrix.
func (m *Matrix) Clone() *Matrix {
	c := &Matrix{MissionSlug: m.MissionSlug, MissionNumber: m.MissionNumber, MissionType: m.MissionType}
	for _, r := range m.Criteria {
		c.Criteria = append(c.Criteria, r.Clone())
	}
	for _, r := range m.NegativeInvariants {
		c.NegativeInvariants = append(c.NegativeInvariants, r.Clone())
	}
	if m.Extras != nil {
		c.Extras = m.Extras.Clone()
	}
	return c
}

// ReadMatrix reads dir/acceptance-matrix.json (nil, nil when absent).
func ReadMatrix(dir string) (*Matrix, error) {
	b, err := os.ReadFile(filepath.Join(dir, MatrixFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, err
	}
	return MatrixFromObject(o)
}

// RenderMatrix is json.dumps(matrix.to_dict(), indent=2) + "\n".
func RenderMatrix(m *Matrix) []byte {
	return []byte(jsonx.Dumps(m.ToObject(), jsonx.Pretty) + "\n")
}

// WriteMatrix is write_acceptance_matrix: re-stamps identity from a sibling
// meta.json and writes atomically. Returns the written path.
func WriteMatrix(dir string, m *Matrix) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err == nil {
		meta := mission.ReadMetaOrEmpty(dir)
		slug := meta.GetString("mission_slug")
		if slug == "" {
			slug = meta.GetString("slug")
		}
		if slug == "" || !mission.SafePathSegment(slug) {
			slug = filepath.Base(dir)
		}
		m.MissionSlug = slug
		if n, ok := mission.MissionNumber(meta); ok {
			m.MissionNumber = fmt.Sprint(n)
		} else {
			m.MissionNumber = nil
		}
		if t := mission.MissionType(meta); t != "" {
			m.MissionType = t
		} else {
			m.MissionType = nil
		}
	}
	p := filepath.Join(dir, MatrixFile)
	return p, atomicfile.WriteMode(p, RenderMatrix(m), 0o644)
}

// ValidateEvidence is validate_matrix_evidence.
func ValidateEvidence(m *Matrix) []string {
	var errs []string
	keys := func(set map[string]bool) string {
		var l []string
		for k := range set {
			l = append(l, k)
		}
		sort.Strings(l)
		return strings.Join(l, ", ")
	}
	for _, c := range m.Criteria {
		id := str(c, "criterion_id")
		if !allowed(mustGet(c, "pass_fail"), criterionVerdicts) {
			errs = append(errs, fmt.Sprintf("%s: pass_fail must be one of %s; got %s", id, keys(criterionVerdicts), pyReprAny(mustGet(c, "pass_fail"))))
		}
		if str(c, "proof_type") == "manual_qa" {
			if !truthy(mustGet(c, "evidence")) {
				errs = append(errs, id+": manual QA requires evidence (URL/screenshot)")
			}
			if !truthy(mustGet(c, "verified_at")) {
				errs = append(errs, id+": manual QA requires verified_at timestamp")
			}
			if !truthy(mustGet(c, "verified_by")) {
				errs = append(errs, id+": manual QA requires verified_by identity")
			}
		}
	}
	for _, n := range m.NegativeInvariants {
		id := str(n, "invariant_id")
		res := mustGet(n, "result")
		if !allowed(res, invariantResults) {
			errs = append(errs, fmt.Sprintf("%s: result must be one of %s; got %s", id, keys(invariantResults), pyReprAny(res)))
			continue
		}
		po := mustGet(n, "provenance_origin")
		if !allowed(po, provenanceOrigins) {
			errs = append(errs, fmt.Sprintf("%s: provenance_origin must be one of %s; got %s", id, keys(provenanceOrigins), pyReprAny(po)))
			continue
		}
		if terminalInvariants[res.(string)] && po == ProvenanceRecorded && (isNull(n, "verified_ref") || isNull(n, "verified_surface_kind")) {
			errs = append(errs, fmt.Sprintf("%s: a recorded %s result requires both verified_ref and verified_surface_kind (NI-1)", id, mission.PyRepr(res.(string))))
		}
	}
	return errs
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	}
	return true
}

func pyReprAny(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return mission.PyRepr(x)
	case bool:
		if x {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}

// SetCriterion splices row into m.Criteria by id (insert-if-absent).
func (m *Matrix) SetCriterion(row *jsonx.Object) {
	id := str(row, "criterion_id")
	for i, c := range m.Criteria {
		if str(c, "criterion_id") == id {
			m.Criteria[i] = row
			return
		}
	}
	m.Criteria = append(m.Criteria, row)
}

// SetInvariant splices row into m.NegativeInvariants by id.
func (m *Matrix) SetInvariant(row *jsonx.Object) {
	id := str(row, "invariant_id")
	for i, c := range m.NegativeInvariants {
		if str(c, "invariant_id") == id {
			m.NegativeInvariants[i] = row
			return
		}
	}
	m.NegativeInvariants = append(m.NegativeInvariants, row)
}

// Criterion returns the row for id (nil when absent).
func (m *Matrix) Criterion(id string) *jsonx.Object {
	for _, c := range m.Criteria {
		if str(c, "criterion_id") == id {
			return c
		}
	}
	return nil
}

// UpdateCriterion is _resolve_criterion_update: applies the CLI fields and
// bumps verified_at only on an observable change.
func UpdateCriterion(existing *jsonx.Object, result string, method, actor, evidence *string) *jsonx.Object {
	cand := existing.Clone()
	cand.Set("pass_fail", result)
	if method != nil {
		cand.Set("proof_type", *method)
	}
	if actor != nil {
		cand.Set("verified_by", *actor)
	}
	if evidence != nil {
		cand.Set("evidence", *evidence)
	}
	if jsonx.Dumps(cand, compact) == jsonx.Dumps(existing, compact) {
		return existing
	}
	cand.Set("verified_at", clock.NowUTCISO())
	return cand
}

// NewInvariant builds a fresh pending NegativeInvariant row.
func NewInvariant(id, description, method string, command, scope *string) *jsonx.Object {
	var cmd, sc any
	if command != nil {
		cmd = *command
	}
	if scope != nil {
		sc = *scope
	}
	return jsonx.NewObject("invariant_id", id, "description", description, "verification_method", method,
		"verification_command", cmd, "result", "pending", "evidence", nil, "scope", sc, "verified_ref", nil,
		"verified_surface_kind", nil, "deferred_reason", nil, "deferred_to_phase", nil, "provenance_origin", ProvenanceLegacyUnrecorded)
}

// GateContext carries the surface/ref an invariant judgement is stamped with.
type GateContext struct {
	SurfaceKind string // "primary" | "coord" | ...
	Ref         string
	PostConsol  bool
}

// EnforceNegativeInvariants is enforce_negative_invariants.
func EnforceNegativeInvariants(root string, invs []*jsonx.Object, ctx *GateContext) []*jsonx.Object {
	var out []*jsonx.Object
	for _, n := range invs {
		res := str(n, "result")
		if terminalInvariants[res] || res == DeferredToConsolidation {
			out = append(out, n)
			continue
		}
		if ctx != nil && shouldDefer(root, n, ctx) {
			d := n.Clone()
			scope := pyReprAny(mustGet(n, "scope"))
			d.Set("result", DeferredToConsolidation)
			d.Set("evidence", fmt.Sprintf("Scoped subject %s is absent on the %s surface pre-consolidation; deferred to post-consolidation verification.", scope, ctx.SurfaceKind))
			d.Set("deferred_reason", fmt.Sprintf("Scoped path(s) %s do not exist on the %s surface at ref %s; judging here would report a false still_present (FR-003). Deferred to the post-consolidation verification op.", scope, ctx.SurfaceKind, mission.PyRepr(ctx.Ref)))
			d.Set("deferred_to_phase", "POST_CONSOLIDATION")
			out = append(out, d)
			continue
		}
		u := checkInvariant(root, n)
		if ctx != nil && terminalInvariants[str(u, "result")] {
			u.Set("provenance_origin", ProvenanceRecorded)
			u.Set("verified_ref", ctx.Ref)
			u.Set("verified_surface_kind", ctx.SurfaceKind)
		}
		out = append(out, u)
	}
	return out
}

func shouldDefer(root string, n *jsonx.Object, ctx *GateContext) bool {
	if ctx.PostConsol || str(n, "verification_method") != "grep_absence" || str(n, "scope") == "" {
		return false
	}
	for _, r := range strings.Fields(str(n, "scope")) {
		if _, err := os.Stat(filepath.Join(root, r)); err != nil {
			return true
		}
	}
	return false
}

// judged builds the replacement row the upstream checkers construct: the
// identity/definition fields are kept, provenance resets to the defaults.
func judged(n *jsonx.Object, result, evidence string, keepScope bool) *jsonx.Object {
	var scope any
	if keepScope {
		scope = mustGet(n, "scope")
	}
	o := jsonx.NewObject("invariant_id", mustGet(n, "invariant_id"), "description", mustGet(n, "description"),
		"verification_method", mustGet(n, "verification_method"), "verification_command", mustGet(n, "verification_command"),
		"result", result, "evidence", evidence, "scope", scope, "verified_ref", nil, "verified_surface_kind", nil,
		"deferred_reason", nil, "deferred_to_phase", nil, "provenance_origin", ProvenanceLegacyUnrecorded)
	for _, kv := range n.Pairs() {
		if !o.Has(kv.Key) {
			o.Set(kv.Key, kv.Value)
		}
	}
	return o
}

func checkInvariant(root string, n *jsonx.Object) *jsonx.Object {
	cmd := str(n, "verification_command")
	switch str(n, "verification_method") {
	case "grep_absence":
		if cmd == "" {
			return judged(n, "pending", "No grep pattern specified in verification_command", true)
		}
		roots := strings.Fields(str(n, "scope"))
		if len(roots) == 0 {
			roots = []string{"."}
		}
		args := append([]string{"-r", "--exclude=acceptance-matrix.json", "--exclude-dir=.git", "--", cmd}, roots...)
		c := exec.Command("grep", args...)
		c.Dir = root
		var stdout, stderr strings.Builder
		c.Stdout, c.Stderr = &stdout, &stderr
		err := c.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				return judged(n, "verification_error", "grep failed to start: "+err.Error(), true)
			}
		}
		switch code {
		case 1:
			return judged(n, "confirmed_absent", "grep found zero matches", true)
		case 0:
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if len(lines) > 5 {
				lines = lines[:5]
			}
			return judged(n, "still_present", "grep found matches: "+strings.Join(lines, "; "), true)
		}
		details := strings.TrimSpace(stderr.String())
		if details == "" {
			details = strings.TrimSpace(stdout.String())
		}
		if len(details) > 500 {
			details = details[:500]
		}
		return judged(n, "verification_error", fmt.Sprintf("grep verification failed (exit %d): %s", code, details), true)
	case "custom_command":
		if cmd == "" {
			return judged(n, "pending", "No command specified in verification_command", false)
		}
		c := exec.Command("sh", "-c", cmd)
		c.Dir = root
		var stdout, stderr strings.Builder
		c.Stdout, c.Stderr = &stdout, &stderr
		err := c.Run()
		if err == nil {
			return judged(n, "confirmed_absent", "Command exited 0: "+trunc(strings.TrimSpace(stdout.String()), 200), false)
		}
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return judged(n, "still_present", fmt.Sprintf("Command exited %d: %s", code, trunc(strings.TrimSpace(stderr.String()), 200)), false)
	}
	return judged(n, "pending", "Unknown verification method: "+str(n, "verification_method"), false)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// SpliceOwnedRows is splice_owned_rows (FR-003 row ownership).
func SpliceOwnedRows(fresh, snapshot, judgedM *Matrix) {
	splice := func(fr, sn, ju []*jsonx.Object, idKey, resKey string, def []string) {
		if len(fr) == 0 {
			return
		}
		byID := func(rows []*jsonx.Object) map[string]*jsonx.Object {
			m := map[string]*jsonx.Object{}
			for _, r := range rows {
				m[str(r, idKey)] = r
			}
			return m
		}
		snap, fm := byID(sn), byID(fr)
		for _, j := range ju {
			id := str(j, idKey)
			s, f := snap[id], fm[id]
			if s == nil || f == nil || str(s, resKey) != "pending" || str(j, resKey) == "pending" || str(f, resKey) != "pending" {
				continue
			}
			same := true
			for _, d := range def {
				if jsonx.Dumps(mustGet(s, d), compact) != jsonx.Dumps(mustGet(f, d), compact) {
					same = false
				}
			}
			if !same {
				continue
			}
			for i, r := range fr {
				if str(r, idKey) == id {
					fr[i] = j
				}
			}
		}
	}
	splice(fresh.Criteria, snapshot.Criteria, judgedM.Criteria, "criterion_id", "pass_fail", []string{"proof_type", "description"})
	splice(fresh.NegativeInvariants, snapshot.NegativeInvariants, judgedM.NegativeInvariants, "invariant_id", "result",
		[]string{"verification_method", "verification_command", "scope"})
}
