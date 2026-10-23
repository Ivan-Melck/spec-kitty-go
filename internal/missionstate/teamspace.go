package missionstate

import (
	"crypto/sha1"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// EventsPackageVersion is the spec-kitty-events version the oracle ships
// (reported as events_package_version).
const EventsPackageVersion = "11.0.0"

// RowMapping is TeamspaceDryRunRowMapping.
type RowMapping struct {
	Slug, ArtifactPath   string
	LineNumber           *int
	SourceEventID        string
	SynthesizedEventID   string
	SynthesizedEventType string
	AggregateID          string
	RowSHA               *string
	EnvelopeSHA          string
}

// ToObject is TeamspaceDryRunRowMapping.to_dict.
func (m RowMapping) ToObject() *jsonx.Object {
	return jsonx.NewObject("mission_slug", m.Slug, "artifact_path", m.ArtifactPath, "line_number", optI(m.LineNumber),
		"source_event_id", m.SourceEventID, "synthesized_event_id", m.SynthesizedEventID,
		"synthesized_event_type", m.SynthesizedEventType, "aggregate_id", m.AggregateID,
		"row_sha256", optS(m.RowSHA), "envelope_sha256", m.EnvelopeSHA)
}

// DryRunReport is TeamspaceDryRunReport.
type DryRunReport struct {
	EnvelopeCount   int
	Valid           bool
	Errors          []*jsonx.Object
	RowMappings     []RowMapping
	ContextWarnings []*jsonx.Object
	SideLogs        []*jsonx.Object
}

func objs(xs []*jsonx.Object) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// ToObject is TeamspaceDryRunReport.to_dict.
func (r *DryRunReport) ToObject() *jsonx.Object {
	rm := []any{}
	for _, m := range r.RowMappings {
		rm = append(rm, m.ToObject())
	}
	return jsonx.NewObject("schema_version", CanonicalEnvelopeSchemaVersion, "events_package_version", EventsPackageVersion,
		"envelope_count", r.EnvelopeCount, "valid", r.Valid, "errors", objs(r.Errors), "row_mappings", rm,
		"context_warnings", objs(r.ContextWarnings), "side_logs", objs(r.SideLogs))
}

// ToJSON is TeamspaceDryRunReport.to_json.
func (r *DryRunReport) ToJSON() string { return jsonx.Dumps(r.ToObject(), jsonx.SortedPretty) + "\n" }

// uuid5URL is uuid.uuid5(uuid.NAMESPACE_URL, name).
func uuid5URL(name string) string {
	ns := []byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(name))
	u := h.Sum(nil)[:16]
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// repoSlug is _repo_slug.
func repoSlug(root string) string {
	out, _ := gitOK(root, "config", "--get", "remote.origin.url")
	remote := strings.TrimSpace(out)
	if remote == "" {
		return filepath.Base(root)
	}
	slug := strings.TrimRight(strings.TrimSuffix(remote, ".git"), "/")
	isURL := strings.HasPrefix(slug, "http://") || strings.HasPrefix(slug, "https://")
	if strings.Contains(slug, ":") && !isURL {
		slug = slug[strings.LastIndex(slug, ":")+1:]
	} else if strings.Contains(slug, "/") {
		parts := strings.Split(slug, "/")
		if len(parts) >= 2 {
			slug = strings.Join(parts[len(parts)-2:], "/")
		} else {
			slug = parts[len(parts)-1]
		}
	}
	if slug == "" {
		return filepath.Base(root)
	}
	return slug
}

func countJSONLRows(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range splitUniversal(string(b)) {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// classifySideLogs is _classify_side_logs.
func classifySideLogs(root, dir string) []*jsonx.Object {
	cands := []string{filepath.Join(dir, "decisions", "events.jsonl"), filepath.Join(dir, "handoff", "events.jsonl"), filepath.Join(dir, "mission-events.jsonl")}
	if fileExists(filepath.Join(dir, "_archive")) {
		m, _ := filepath.Glob(filepath.Join(dir, "_archive", "*.events.jsonl"))
		sort.SliceStable(m, func(i, j int) bool { return migrate.PyPathLess(m[i], m[j]) })
		cands = append(cands, m...)
	}
	var out []*jsonx.Object
	for _, p := range cands {
		if !fileExists(p) {
			continue
		}
		out = append(out, jsonx.NewObject("artifact_path", repoRelpath(root, p), "row_count", countJSONLRows(p),
			"disposition", skippedLocalSideLog, "reason", outOfScopeForLaunchImport))
	}
	rt := filepath.Join(root, ".kittify", "runtime")
	if fileExists(rt) {
		var found []string
		_ = filepath.WalkDir(rt, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Name() == "run.events.jsonl" {
				found = append(found, p)
			}
			return nil
		})
		sort.SliceStable(found, func(i, j int) bool { return migrate.PyPathLess(found[i], found[j]) })
		for _, p := range found {
			out = append(out, jsonx.NewObject("artifact_path", repoRelpath(root, p), "row_count", countJSONLRows(p),
				"disposition", skippedRuntimeSideLog, "reason", outOfScopeForLaunchImport))
		}
	}
	return out
}

func forbiddenKeyPaths(v any, prefix string) [][2]string {
	var out [][2]string
	switch t := v.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			cp := prefix + "." + kv.Key
			for _, k := range forbiddenLegacyKeys {
				if k == kv.Key {
					out = append(out, [2]string{cp, kv.Key})
				}
			}
			out = append(out, forbiddenKeyPaths(kv.Value, cp)...)
		}
	case []any:
		for i, c := range t {
			out = append(out, forbiddenKeyPaths(c, fmt.Sprintf("%s[%d]", prefix, i))...)
		}
	}
	return out
}

// scanRawStatusRows is _scan_raw_status_rows.
func scanRawStatusRows(root, dir string) []*jsonx.Object {
	path := filepath.Join(dir, EventsFile)
	if !fileExists(path) {
		return nil
	}
	rel := repoRelpath(root, path)
	rows, err := readJSONLRows(path)
	if err != nil {
		return []*jsonx.Object{jsonx.NewObject("mission_slug", filepath.Base(dir), "artifact_path", rel, "error", statusEventsUnreadable, "message", err.Error())}
	}
	var out []*jsonx.Object
	for _, r := range rows {
		if isPreservedNonLaneRow(r.data) {
			continue
		}
		eid := optS(eventIDOf(r.data))
		if r.data.Has("event_type") || r.data.Has("event_name") {
			out = append(out, jsonx.NewObject("mission_slug", filepath.Base(dir), "artifact_path", rel, "line_number", r.line,
				"event_id", eid, "error", statusRowNotRepaired, "message", typedSideLogMessage))
		}
		for _, fk := range forbiddenKeyPaths(r.data, "$") {
			out = append(out, jsonx.NewObject("mission_slug", filepath.Base(dir), "artifact_path", rel, "line_number", r.line,
				"event_id", eid, "error", forbiddenLegacyKey, "path", fk[0], "key", fk[1]))
		}
	}
	return out
}

func isOutOfScopeSideLogPath(p string) bool {
	return strings.HasSuffix(p, "/mission-events.jsonl") || strings.Contains(p, "/decisions/events.jsonl") ||
		strings.Contains(p, "/handoff/events.jsonl") || strings.Contains(p, "/_archive/") || strings.HasSuffix(p, "/run.events.jsonl")
}

// teamspaceAuditBlockers is _teamspace_audit_blockers.
func teamspaceAuditBlockers(root, scanRoot string, dirs []string) []*jsonx.Object {
	rep := healthcheck.RunAuditIn(root, scanRoot, nil)
	selected := map[string]bool{}
	for _, d := range dirs {
		selected[filepath.Base(d)] = true
	}
	type keyed struct {
		k   [3]string
		obj *jsonx.Object
	}
	var errs []keyed
	for _, m := range rep.Missions {
		if !selected[m.Slug] {
			continue
		}
		for _, f := range m.Findings {
			if !healthcheck.IsTeamspaceBlocker(f) || isOutOfScopeSideLogPath(f.ArtifactPath) {
				continue
			}
			msg := f.Code
			if f.Detail != nil && *f.Detail != "" {
				msg = *f.Detail
			}
			errs = append(errs, keyed{[3]string{m.Slug, f.ArtifactPath, f.Code}, jsonx.NewObject(
				"mission_slug", m.Slug, "artifact_path", f.ArtifactPath, "error", missionStateAuditBlocker,
				"finding_code", f.Code, "severity", f.Severity, "message", msg, "remediation", teamspaceRemediation)})
		}
	}
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i].k, errs[j].k
		for x := 0; x < 3; x++ {
			if a[x] != b[x] {
				return a[x] < b[x]
			}
		}
		return false
	})
	out := make([]*jsonx.Object, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.obj)
	}
	return out
}

// TeamspaceDryRun is teamspace_dry_run: synthesize TeamSpace envelopes from
// the local status logs and validate them against the event contract.
func TeamspaceDryRun(root string, handle *string) (*DryRunReport, error) {
	return TeamspaceDryRunIn(root, "", handle)
}

// TeamspaceDryRunIn is teamspace_dry_run(scan_root=...).
func TeamspaceDryRunIn(root, scanRoot string, handle *string) (*DryRunReport, error) {
	root = absResolved(root)
	dirs, _, err := SelectMissionDirsIn(root, scanRoot, handle)
	if err != nil {
		return nil, err
	}
	if blockers := teamspaceAuditBlockers(root, scanRoot, dirs); len(blockers) > 0 {
		rep := &DryRunReport{Errors: blockers}
		for _, d := range dirs {
			rep.SideLogs = append(rep.SideLogs, classifySideLogs(root, d)...)
		}
		return rep, nil
	}
	var names []string
	for _, d := range dirs {
		names = append(names, filepath.Base(d))
	}
	projectUUID := uuid5URL("spec-kitty:teamspace-dry-run:" + strings.Join(names, "|"))
	rslug := repoSlug(root)
	projectSlug := rslug
	rep := &DryRunReport{}
	if u, _ := mission.ProjectIdentity(root); u == "" {
		rep.ContextWarnings = append(rep.ContextWarnings, jsonx.NewObject("code", teamspaceProjectContextMissing,
			"message", teamspaceProjectContextMissingMessage, "dry_run_project_uuid", projectUUID))
	}
	rep.ContextWarnings = append(rep.ContextWarnings, jsonx.NewObject("code", teamspaceTeamContextNotValidated, "message", teamspaceTeamContextNotValidatedMessage))
	var errs []*jsonx.Object
	for _, d := range dirs {
		rep.SideLogs = append(rep.SideLogs, classifySideLogs(root, d)...)
		raw := scanRawStatusRows(root, d)
		errs = append(errs, raw...)
		if len(raw) > 0 {
			continue
		}
		locations := map[string]rawRow{}
		if rows, err := readJSONLRows(filepath.Join(d, EventsFile)); err == nil {
			for _, r := range rows {
				if id := eventIDOf(r.data); id != nil {
					if _, ok := locations[*id]; !ok {
						locations[*id] = r
					}
				}
			}
		}
		stream, err := status.ReadStream(d, "")
		if err != nil {
			errs = append(errs, jsonx.NewObject("mission_slug", filepath.Base(d), "error", statusEventsUnreadable, "message", err.Error()))
			continue
		}
		events := append([]*status.Event(nil), stream.Transitions...)
		sort.SliceStable(events, func(i, j int) bool {
			if events[i].At != events[j].At {
				return events[i].At < events[j].At
			}
			return events[i].EventID < events[j].EventID
		})
		rel := repoRelpath(root, filepath.Join(d, EventsFile))
		for idx, ev := range events {
			rep.EnvelopeCount++
			env := statusEventEnvelope(ev, projectUUID, idx+1, projectSlug, rslug)
			m := RowMapping{Slug: ev.MissionSlug, ArtifactPath: rel, SourceEventID: ev.EventID,
				SynthesizedEventID: env.GetString("event_id"), SynthesizedEventType: env.GetString("event_type"),
				AggregateID: env.GetString("aggregate_id"), EnvelopeSHA: sha256Text(jsonx.Dumps(env, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true, Compact: true}))}
			if loc, ok := locations[ev.EventID]; ok {
				n, s := loc.line, sha256Text(loc.text)
				m.LineNumber, m.RowSHA = &n, &s
			}
			rep.RowMappings = append(rep.RowMappings, m)
			if msg := validateEnvelope(env); msg != "" {
				errs = append(errs, jsonx.NewObject("mission_slug", ev.MissionSlug, "event_id", ev.EventID, "error", envelopeInvalid, "message", msg))
				continue
			}
			if vs := validatePayload(env.GetObject("payload")); len(vs) > 0 {
				errs = append(errs, jsonx.NewObject("mission_slug", ev.MissionSlug, "event_id", ev.EventID, "error", payloadInvalid, "model_violations", objs(vs)))
			}
		}
	}
	rep.Errors = errs
	rep.Valid = len(errs) == 0
	return rep, nil
}

// statusEventEnvelope is _status_event_to_teamspace_envelope (model_dump
// order of _TeamspaceEnvelope).
func statusEventEnvelope(ev *status.Event, projectUUID string, lamport int, projectSlug, rslug string) *jsonx.Object {
	var evidence any
	if ev.Evidence != nil {
		evidence = ev.Evidence.ToObject()
	}
	if ev.To == status.Approved || ev.To == status.Done {
		resolved := jsonx.NewObject()
		if o, ok := evidence.(*jsonx.Object); ok {
			resolved = o.Clone()
		}
		if !pyjson.Truthy(valueOf(resolved, "review")) {
			reviewer := any(historicalRepairMarker)
			if a := ev.Actor.Value(); pyjson.Truthy(a) {
				reviewer = a
			}
			ref := fmt.Sprintf("%s:%s:%s:%s", historicalRepairMarker, ev.MissionSlug, ev.WPID, ev.EventID)
			if ev.ReviewRef != nil && *ev.ReviewRef != "" {
				ref = *ev.ReviewRef
			}
			resolved.Set("review", jsonx.NewObject("reviewer", reviewer, "verdict", "approved", "reference", ref))
		}
		if !pyjson.Truthy(valueOf(resolved, "repos")) {
			repo := rslug
			if repo == "" {
				repo = projectSlug
			}
			resolved.Set("repos", []any{jsonx.NewObject("repo", repo, "branch", historicalRepairMarker, "commit", historicalRepairMarker)})
		}
		setDefault(resolved, "verification", []any{})
		evidence = resolved
	}
	from, to := string(ev.From), string(ev.To)
	if ev.From == status.InProgress && ev.To == status.InReview {
		from = string(status.ForReview)
	}
	var reason, reviewRef any
	if ev.Reason != nil {
		reason = *ev.Reason
	}
	if ev.ReviewRef != nil {
		reviewRef = *ev.ReviewRef
	}
	payload := jsonx.NewObject("mission_slug", ev.MissionSlug, "wp_id", ev.WPID, "from_lane", from, "to_lane", to,
		"actor", ev.Actor.Value(), "force", ev.Force, "reason", reason, "execution_mode", ev.ExecutionMode,
		"review_ref", reviewRef, "evidence", evidence)
	var rs any
	if rslug != "" {
		rs = rslug
	}
	return jsonx.NewObject("event_id", ev.EventID, "event_type", envelopeEventType, "aggregate_id", ev.WPID,
		"aggregate_type", envelopeAggregateType, "payload", payload, "timestamp", ev.At,
		"build_id", teamspaceDryRunBuildID, "node_id", teamspaceDryRunBuildID, "lamport_clock", lamport,
		"causation_id", nil, "project_uuid", projectUUID, "project_slug", projectSlug, "repo_slug", rs,
		"correlation_id", DeterministicULID(fmt.Sprintf("teamspace-dry-run:%s:%s", ev.MissionSlug, ev.EventID)),
		"schema_version", CanonicalEnvelopeSchemaVersion)
}

var (
	eventsULIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{26}$`)
)

// validateEnvelope is str(spec_kitty_events.Event.model_validate(env)'s
// ValidationError) for the fields a synthesized envelope can get wrong
// (event_id, aggregate_id, timestamp); "" when valid.
func validateEnvelope(env *jsonx.Object) string {
	type lineErr struct{ loc, msg, typ, input string }
	var errs []lineErr
	id := env.GetString("event_id")
	switch {
	case len(id) == 26 && !eventsULIDRe.MatchString(id):
		errs = append(errs, lineErr{"event_id", fmt.Sprintf("Value error, 26-char event_id must be valid Crockford base32 (chars I, L, O, U are not allowed); got %s", pyjson.ReprString(id)), "value_error", id})
	case len(id) == 26, len(id) == 36 && uuidHyphenRe.MatchString(id), len(id) == 32 && uuidBareRe.MatchString(id):
	default:
		errs = append(errs, lineErr{"event_id", fmt.Sprintf("Value error, event_id must be 26-char ULID (Crockford base32), 36-char UUID, or 32-char hex UUID; got %d chars", len([]rune(id))), "value_error", id})
	}
	if agg := env.GetString("aggregate_id"); agg == "" {
		errs = append(errs, lineErr{"aggregate_id", "String should have at least 1 character", "string_too_short", agg})
	}
	ts := env.GetString("timestamp")
	if msg := pydanticDatetimeError(ts); msg != "" {
		errs = append(errs, lineErr{"timestamp", msg, "datetime_from_date_parsing", ts})
	}
	if len(errs) == 0 {
		return ""
	}
	noun := "error"
	if len(errs) > 1 {
		noun = "errors"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d validation %s for Event", len(errs), noun)
	for _, e := range errs {
		r := []rune(pyjson.ReprString(e.input))
		repr := string(r)
		if len(r) > 50 {
			repr = string(r[:25]) + "..." + string(r[len(r)-24:])
		}
		fmt.Fprintf(&b, "\n%s\n  %s [type=%s, input_value=%s, input_type=str]\n    For further information visit https://errors.pydantic.dev/2.14/v/%s", e.loc, e.msg, e.typ, repr, e.typ)
	}
	return b.String()
}

func violation(field, msg, typ string) *jsonx.Object {
	return jsonx.NewObject("field", field, "message", msg, "violation_type", typ)
}

var eventsLanes = map[string]bool{"genesis": true, "planned": true, "claimed": true, "in_progress": true, "for_review": true,
	"in_review": true, "approved": true, "done": true, "blocked": true, "canceled": true}

var allowedTransitions = map[[2]string]bool{
	{"", "planned"}: true, {"genesis", "planned"}: true,
	{"planned", "claimed"}: true, {"claimed", "in_progress"}: true, {"in_progress", "for_review"}: true,
	{"in_progress", "approved"}: true, {"for_review", "in_review"}: true, {"for_review", "approved"}: true,
	{"for_review", "done"}: true, {"in_review", "approved"}: true, {"in_review", "done"}: true, {"approved", "done"}: true,
	{"for_review", "in_progress"}: true, {"for_review", "planned"}: true, {"in_review", "in_progress"}: true,
	{"in_review", "for_review"}: true, {"in_review", "planned"}: true, {"approved", "in_progress"}: true,
	{"approved", "planned"}: true, {"in_progress", "planned"}: true, {"blocked", "in_progress"}: true,
}

var reviewRejection = map[[2]string]bool{{"in_progress", "planned"}: true, {"for_review", "planned"}: true, {"in_review", "planned"}: true, {"approved", "planned"}: true}

func laneEnumName(l string) string {
	if l == "" {
		return "None"
	}
	return "Lane." + strings.ToUpper(l)
}

// validatePayload is conformance.validate_event(payload, "WPStatusChanged"):
// StatusTransitionPayload shape/business rules, then the transition matrix.
func validatePayload(p *jsonx.Object) []*jsonx.Object {
	var vs []*jsonx.Object
	for _, f := range []string{"mission_slug", "wp_id"} {
		if s, _ := valueOf(p, f).(string); s == "" {
			vs = append(vs, violation(f, "String should have at least 1 character", "string_too_short"))
		}
	}
	from, _ := valueOf(p, "from_lane").(string)
	to, _ := valueOf(p, "to_lane").(string)
	lanesMsg := "Input should be 'genesis', 'planned', 'claimed', 'in_progress', 'for_review', 'in_review', 'approved', 'done', 'blocked' or 'canceled'"
	if from != "" && !eventsLanes[from] {
		vs = append(vs, violation("from_lane", lanesMsg, "enum"))
	}
	if !eventsLanes[to] {
		vs = append(vs, violation("to_lane", lanesMsg, "enum"))
	}
	switch a := valueOf(p, "actor").(type) {
	case string:
		if strings.TrimSpace(a) == "" {
			vs = append(vs, violation("actor", "Value error, actor string must be non-empty", "value_error"))
		}
	case *jsonx.Object:
		if a.Len() == 0 {
			vs = append(vs, violation("actor", "Value error, actor dict must be non-empty", "value_error"))
		}
	}
	if m, _ := valueOf(p, "execution_mode").(string); m != "worktree" && m != "direct_repo" {
		vs = append(vs, violation("execution_mode", "Input should be 'worktree' or 'direct_repo'", "enum"))
	}
	evidence, _ := valueOf(p, "evidence").(*jsonx.Object)
	if evidence != nil {
		if repos, ok := evidence.Get("repos"); !ok {
			vs = append(vs, violation("evidence.repos", "Field required", "missing"))
		} else if l, _ := repos.([]any); len(l) == 0 {
			vs = append(vs, violation("evidence.repos", "List should have at least 1 item after validation, not 0", "too_short"))
		}
		if !evidence.Has("review") {
			vs = append(vs, violation("evidence.review", "Field required", "missing"))
		}
	}
	if len(vs) > 0 {
		return vs
	}
	force, _ := valueOf(p, "force").(bool)
	reason, _ := valueOf(p, "reason").(string)
	if force && strings.TrimSpace(reason) == "" {
		return []*jsonx.Object{violation("", "Value error, force=True requires a non-empty reason", "value_error")}
	}
	if (to == "approved" || to == "done") && evidence == nil {
		return []*jsonx.Object{violation("", "Value error, to_lane in {'approved', 'done'} requires evidence", "value_error")}
	}
	// semantic: status.validate_transition
	tr := func(msg string) { vs = append(vs, violation("transition", msg, "transition_rule")) }
	terminal := func(l string) bool { return l == "done" || l == "canceled" }
	if from != "" && terminal(from) && !force {
		tr(from + " is terminal; requires force=True to exit")
	}
	if !force && from != "" && reviewRejection[[2]string{from, to}] {
		tr(fmt.Sprintf("review-rejection rollback %s -> %s requires force=True", from, to))
	}
	if !force {
		in := allowedTransitions[[2]string{from, to}]
		toBlocked := to == "blocked" && from != "" && !terminal(from)
		toCanceled := to == "canceled" && from != "" && !terminal(from)
		if !(in || toBlocked || toCanceled) {
			tr(fmt.Sprintf("Transition %s -> %s is not allowed", laneEnumName(from), laneEnumName(to)))
		}
	}
	reviewRef, _ := valueOf(p, "review_ref").(string)
	if (from == "for_review" || from == "in_review" || from == "approved") && (to == "in_progress" || to == "planned") &&
		!(force && reviewRejection[[2]string{from, to}]) && strings.TrimSpace(reviewRef) == "" {
		f := from
		if f == "" {
			f = "None"
		}
		tr(fmt.Sprintf("%s -> %s requires review_ref", f, to))
	}
	if from == "in_progress" && to == "planned" && strings.TrimSpace(reason) == "" {
		tr("in_progress -> planned requires reason")
	}
	return vs
}
