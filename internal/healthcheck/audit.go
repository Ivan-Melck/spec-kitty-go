package healthcheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Read-only mission-state audit (specify_cli/audit): per-artifact
// classifiers plus repo-level identity findings.

// Finding is MissionFinding.
type Finding struct {
	Code, Severity, ArtifactPath string
	Detail                       *string
}

func (f Finding) obj() *jsonx.Object {
	var d any
	if f.Detail != nil {
		d = *f.Detail
	}
	return jsonx.NewObject("artifact_path", f.ArtifactPath, "code", f.Code, "detail", d, "severity", f.Severity)
}

func fnd(code, sev, path, detail string) Finding { return Finding{code, sev, path, &detail} }

var teamspaceBlockerCodes = map[string]bool{"CORRUPT_JSON": true, "CORRUPT_JSONL": true, "DUPLICATE_MISSION_ID": true, "FORBIDDEN_KEY": true,
	"IDENTITY_INVALID": true, "IDENTITY_MISSING": true, "LEGACY_KEY": true, "SNAPSHOT_DRIFT": true}

// IsTeamspaceBlocker is is_teamspace_blocker.
func IsTeamspaceBlocker(f Finding) bool {
	return f.Severity == "error" || teamspaceBlockerCodes[f.Code]
}

// MissionAudit is MissionAuditResult.
type MissionAudit struct {
	Slug, Dir string
	Findings  []Finding
}

func (m *MissionAudit) has(sev string) bool {
	for _, f := range m.Findings {
		if f.Severity == sev {
			return true
		}
	}
	return false
}

func (m *MissionAudit) blockers() bool {
	for _, f := range m.Findings {
		if IsTeamspaceBlocker(f) {
			return true
		}
	}
	return false
}

// AuditReport is RepoAuditReport.
type AuditReport struct {
	Missions []*MissionAudit
}

var legacyKeys = map[string]bool{"feature_slug": true, "feature_number": true, "mission_key": true, "legacy_aggregate_id": true}
var forbiddenKeys = map[string]bool{"event_type": true, "event_name": true}

var knownKeys = map[string]map[string]bool{}

func keyset(s string) map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields(s) {
		m[k] = true
	}
	return m
}

func init() {
	knownKeys["meta.json"] = keyset(`accept_commit acceptance_history acceptance_mode accepted_at accepted_by accepted_from_commit change_mode commit_to_target contracts contracts_rationale coordination_branch created_at documentation_state flattened friendly_name merge_history merged_at merged_by merged_commit merged_into merged_push merged_strategy mission_branch mission_id mission_number mission_slug mission_type origin_ticket pr_bound purpose_context purpose_tldr retain_branches retain_worktrees slug source_description target_branch topology vcs vcs_locked_at`)
	knownKeys["status.json"] = keyset(`mission_slug feature_slug mission_number mission_type materialized_at event_count last_event_id work_packages summary retrospective`)
	knownKeys["status_event_row"] = keyset(`actor at event_id evidence execution_mode feature_slug force from_lane reason review_ref review_result to_lane wp_id mission_id mission_slug policy_metadata event_type event_name`)
	knownKeys["mission_lifecycle_row"] = keyset(`aggregate_id aggregate_type event_id event_type payload project_slug project_uuid schema_version timestamp`)
	knownKeys["wp_frontmatter"] = keyset(`work_package_id title dependencies subtasks execution_mode owned_files authoritative_surface planning_base_branch merge_target_branch branch_strategy agent_profile role agent model history requirement_refs tracker_refs id status lane actor evidence review_ref reason force depends_on tags`)
	knownKeys["mission_event_row"] = keyset(`mission payload timestamp type event_type at event_id`)
	knownKeys["decision_event_row"] = keyset(`at event_id event_type payload timestamp type`)
	knownKeys["handoff_event_row"] = keyset(`actor at event_id evidence execution_mode feature_slug force from_lane mission_id mission_slug policy_metadata reason review_ref to_lane wp_id`)
}

func detectLegacy(o *jsonx.Object, path string, extra ...string) []Finding {
	var out []Finding
	for _, k := range o.Keys() {
		isExtra := false
		for _, e := range extra {
			if e == k {
				isExtra = true
			}
		}
		if legacyKeys[k] || isExtra {
			out = append(out, fnd("LEGACY_KEY", "warning", path, "legacy key: "+pyjson.ReprString(k)))
		}
	}
	return out
}

func unknownKeys(typ string, o *jsonx.Object, path string) []Finding {
	known := knownKeys[typ]
	var out []Finding
	for _, k := range o.Keys() {
		if !known[k] && !legacyKeys[k] && !forbiddenKeys[k] {
			out = append(out, fnd("UNKNOWN_SHAPE", "info", path, fmt.Sprintf("unknown key: %s (artifact_type=%s)", pyjson.ReprString(k), pyjson.ReprString(typ))))
		}
	}
	return out
}

func isLifecycleRow(o *jsonx.Object) bool {
	if o.Has("from_lane") || o.Has("to_lane") {
		return false
	}
	switch valueOf(o, "aggregate_type") {
	case "Mission", "Project", "WorkPackage", "MissionDossier":
	default:
		return false
	}
	s, ok := valueOf(o, "event_type").(string)
	return ok && s != ""
}

func isDecisionPointRow(o *jsonx.Object) bool {
	known := knownKeys["decision_event_row"]
	for _, k := range o.Keys() {
		if !known[k] {
			return false
		}
	}
	s, ok := valueOf(o, "event_type").(string)
	if !ok || !strings.HasPrefix(s, "DecisionPoint") {
		return false
	}
	_, ok = valueOf(o, "payload").(*jsonx.Object)
	return ok
}

var (
	ulidRe  = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	actorRe = regexp.MustCompile(`^[a-z][a-z0-9_:-]*$`)
)

func classifyMeta(dir string) []Finding {
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return nil
	}
	o, err := migrate.LoadMetaFailClosed(dir)
	if err != nil {
		var me *migrate.MetaReadError
		detail := "top-level JSON value must be an object"
		if errors.As(err, &me) {
			b, rerr := os.ReadFile(filepath.Join(dir, "meta.json"))
			if rerr != nil {
				detail = "cannot read meta.json: " + rerr.Error()
			} else if _, uerr := pyjson.DecodeUTF8(b); uerr != nil {
				detail = "cannot read meta.json: " + uerr.Error()
			}
			// A JSON syntax error is chained through MetaDecodeError upstream,
			// so it reports the generic object message too.
		}
		return []Finding{fnd("CORRUPT_JSON", "error", "meta.json", detail)}
	}
	if o == nil {
		return nil
	}
	out := detectLegacy(o, "meta.json")
	mid, ok := o.Get("mission_id")
	switch {
	case !ok || mid == nil:
		out = append(out, fnd("IDENTITY_MISSING", "error", "meta.json", "missing mission_id field"))
	default:
		if s, isStr := mid.(string); !isStr || !ulidRe.MatchString(s) {
			out = append(out, fnd("IDENTITY_INVALID", "error", "meta.json", "mission_id is not a valid ULID: "+pyjson.Repr(mid)))
		}
	}
	return append(out, unknownKeys("meta.json", o, "meta.json")...)
}

func jsonLines(path, artifact string) ([]*jsonx.Object, []Finding, bool, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, false
		}
		return nil, []Finding{fnd("CORRUPT_JSONL", "error", artifact, "could not read file: "+err.Error())}, true, true
	}
	var rows []*jsonx.Object
	var out []Finding
	corrupt := false
	text := string(b)
	for i, line := range splitPyLines(text) {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, perr := pyjson.Loads(s)
		if perr != nil {
			var de *pyjson.DecodeError
			msg := perr.Error()
			if errors.As(perr, &de) {
				msg = de.Msg
			}
			out = append(out, fnd("CORRUPT_JSONL", "error", artifact, fmt.Sprintf("line %d: %s", i+1, msg)))
			corrupt = true
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			out = append(out, fnd("CORRUPT_JSONL", "error", artifact, fmt.Sprintf("line %d: expected JSON object, got %s", i+1, pyjson.TypeName(v))))
			corrupt = true
			continue
		}
		rows = append(rows, o)
		out = append(out, nil...)
		_ = o
	}
	return rows, out, corrupt, true
}

// splitPyLines is str.splitlines() for \n / \r\n / \r.
func splitPyLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func classifyStatusEvents(dir string) ([]Finding, bool) {
	path := filepath.Join(dir, "status.events.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		return []Finding{fnd("CORRUPT_JSONL", "error", "status.events.jsonl", "could not read file: "+err.Error())}, true
	}
	var out []Finding
	corrupt := false
	for i, line := range splitPyLines(string(b)) {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, perr := pyjson.Loads(s)
		if perr != nil {
			var de *pyjson.DecodeError
			msg := perr.Error()
			if errors.As(perr, &de) {
				msg = de.Msg
			}
			out = append(out, fnd("CORRUPT_JSONL", "error", "status.events.jsonl", fmt.Sprintf("line %d: %s", i+1, msg)))
			corrupt = true
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			out = append(out, fnd("CORRUPT_JSONL", "error", "status.events.jsonl", fmt.Sprintf("line %d: expected JSON object, got %s", i+1, pyjson.TypeName(v))))
			corrupt = true
			continue
		}
		out = append(out, detectLegacy(o, "status.events.jsonl", "work_package_id")...)
		lifecycle, decision := isLifecycleRow(o), isDecisionPointRow(o)
		if !lifecycle && !decision {
			for _, k := range o.Keys() {
				if forbiddenKeys[k] {
					out = append(out, fnd("FORBIDDEN_KEY", "warning", "status.events.jsonl", "forbidden key: "+pyjson.ReprString(k)))
				}
			}
		}
		if a, ok := o.Get("actor"); ok && a != nil {
			if s, isStr := a.(string); !isStr || !actorRe.MatchString(s) {
				out = append(out, fnd("ACTOR_DRIFT", "warning", "status.events.jsonl", "actor format unexpected: "+pyjson.Repr(a)))
			}
		}
		typ := "status_event_row"
		if lifecycle {
			typ = "mission_lifecycle_row"
		} else if decision {
			typ = "decision_event_row"
		}
		out = append(out, unknownKeys(typ, o, "status.events.jsonl")...)
	}
	return out, corrupt
}

func classifyJSONLFile(path, artifact, typ string) []Finding {
	rows, out, _, present := jsonLines(path, artifact)
	if !present {
		return nil
	}
	for _, o := range rows {
		out = append(out, detectLegacy(o, artifact)...)
		out = append(out, unknownKeys(typ, o, artifact)...)
	}
	// keep line order: rebuild in sequence
	return reorderJSONLFindings(path, artifact, typ, out)
}

// reorderJSONLFindings re-walks the file so findings follow line order
// (corruption findings interleaved with per-row findings, as upstream).
func reorderJSONLFindings(path, artifact, typ string, _ []Finding) []Finding {
	b, err := os.ReadFile(path)
	if err != nil {
		return []Finding{fnd("CORRUPT_JSONL", "error", artifact, "could not read file: "+err.Error())}
	}
	var out []Finding
	for i, line := range splitPyLines(string(b)) {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		v, perr := pyjson.Loads(s)
		if perr != nil {
			var de *pyjson.DecodeError
			msg := perr.Error()
			if errors.As(perr, &de) {
				msg = de.Msg
			}
			out = append(out, fnd("CORRUPT_JSONL", "error", artifact, fmt.Sprintf("line %d: %s", i+1, msg)))
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			out = append(out, fnd("CORRUPT_JSONL", "error", artifact, fmt.Sprintf("line %d: expected JSON object, got %s", i+1, pyjson.TypeName(v))))
			continue
		}
		out = append(out, detectLegacy(o, artifact)...)
		out = append(out, unknownKeys(typ, o, artifact)...)
	}
	return out
}

func classifyStatusJSON(dir string, skipDrift bool) []Finding {
	path := filepath.Join(dir, "status.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []Finding{fnd("CORRUPT_JSON", "error", "status.json", "could not read file: "+err.Error())}
	}
	raw := string(b)
	v, perr := pyjson.Loads(raw)
	if perr != nil {
		var de *pyjson.DecodeError
		msg := perr.Error()
		if errors.As(perr, &de) {
			msg = de.Msg
		}
		return []Finding{fnd("CORRUPT_JSON", "error", "status.json", "JSON decode error: "+msg)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return []Finding{fnd("CORRUPT_JSON", "error", "status.json", "top-level JSON value must be an object")}
	}
	out := detectLegacy(o, "status.json")
	out = append(out, unknownKeys("status.json", o, "status.json")...)
	if skipDrift {
		return out
	}
	snap, serr := status.MaterializeSnapshot(dir)
	if serr != nil {
		return append(out, fnd("SNAPSHOT_DRIFT", "error", "status.json", "reducer raised during drift check: "+migrate.PyStoreError(dir, serr).Error()))
	}
	computed := snap.Render()
	persisted := jsonx.Dumps(o, jsonx.Options{Indent: 2, SortKeys: true}) + "\n"
	if computed == persisted {
		return out
	}
	cv, _ := pyjson.Loads(computed)
	co, _ := cv.(*jsonx.Object)
	switch {
	case provenanceOnlyDrift(co, o):
		out = append(out, fnd("SNAPSHOT_DRIFT_PROVENANCE", "warning", "status.json", "reducer output differs from persisted status.json only in per-work-package provenance fields (actor, last_event_id, last_transition_at); a reducer-version change re-attributed provenance without altering lane/outcome state, so this drift is not an actionable TeamSpace-readiness problem — an active mission's snapshot self-heals on the next materialize, and an archived mission's is frozen by the archive gate"))
	case terminalSnapshot(co):
		out = append(out, fnd("SNAPSHOT_DRIFT_TERMINAL", "warning", "status.json", "a completed mission's frozen status.json no longer matches the current reducer output, but every work package is 'done' and the dossier is immutable (the archive gate forbids editing it), so this drift is not an actionable TeamSpace-readiness problem"))
	default:
		out = append(out, fnd("SNAPSHOT_DRIFT", "error", "status.json", "reducer output does not match persisted status.json"))
	}
	return out
}

func provenanceOnlyDrift(c, p *jsonx.Object) bool {
	if c == nil || p == nil {
		return false
	}
	strip := func(o *jsonx.Object) *jsonx.Object {
		x := o.Clone()
		x.Delete("work_packages")
		return x
	}
	if !pyjson.Equal(strip(c), strip(p)) {
		return false
	}
	cw, ok1 := valueOf(c, "work_packages").(*jsonx.Object)
	pw, ok2 := valueOf(p, "work_packages").(*jsonx.Object)
	if !ok1 || !ok2 || cw.Len() != pw.Len() {
		return false
	}
	saw := false
	for _, kv := range cw.Pairs() {
		pv, ok := pw.Get(kv.Key)
		if !ok {
			return false
		}
		a, ok1 := kv.Value.(*jsonx.Object)
		b, ok2 := pv.(*jsonx.Object)
		if !ok1 || !ok2 {
			return false
		}
		keys := map[string]bool{}
		for _, k := range a.Keys() {
			keys[k] = true
		}
		for _, k := range b.Keys() {
			keys[k] = true
		}
		for k := range keys {
			if pyjson.Equal(valueOf(a, k), valueOf(b, k)) {
				continue
			}
			if k != "actor" && k != "last_event_id" && k != "last_transition_at" {
				return false
			}
			saw = true
		}
	}
	return saw
}

func terminalSnapshot(c *jsonx.Object) bool {
	if c == nil {
		return false
	}
	w, ok := valueOf(c, "work_packages").(*jsonx.Object)
	if !ok || w.Len() == 0 {
		return false
	}
	for _, kv := range w.Pairs() {
		o, ok := kv.Value.(*jsonx.Object)
		if !ok || valueOf(o, "lane") != "done" {
			return false
		}
	}
	return true
}

func classifyWPFiles(dir string) []Finding {
	tasks := filepath.Join(dir, "tasks")
	files, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	sort.Strings(files)
	if len(files) == 0 {
		return nil
	}
	var snap *status.Snapshot
	hasLog := false
	if _, err := os.Stat(filepath.Join(dir, "status.events.jsonl")); err == nil {
		hasLog = true
		snap, _ = status.MaterializeSnapshot(dir)
	}
	var out []Finding
	for _, p := range files {
		ap := "tasks/" + filepath.Base(p)
		d, err := fm.Read(p)
		if err != nil {
			b, rerr := os.ReadFile(p)
			if rerr != nil || !strings.HasPrefix(strings.TrimPrefix(string(b), "\ufeff"), "---") {
				continue
			}
			out = append(out, fnd("UNKNOWN_SHAPE", "info", ap, "could not parse YAML frontmatter"))
			continue
		}
		if d.Meta == nil || d.Meta.Len() == 0 {
			continue
		}
		out = append(out, detectLegacy(d.Meta, ap)...)
		out = append(out, unknownKeys("wp_frontmatter", d.Meta, ap)...)
		if hasLog && snap != nil {
			id := strings.TrimSuffix(filepath.Base(p), ".md")
			if v, ok := d.Meta.Get("work_package_id"); ok && v != nil && strings.TrimSpace(pyjson.Str(v)) != "" {
				id = strings.TrimSpace(pyjson.Str(v))
			}
			if wp := snap.WorkPackages.GetObject(id); wp != nil {
				lane := wp.GetString("lane")
				if lane == "done" || lane == "approved" {
					if v, _ := d.Meta.Get("evidence"); v == nil {
						out = append(out, fnd("MISSING_EVIDENCE", "warning", ap, fmt.Sprintf("terminal lane %s but evidence is absent", pyjson.ReprString(lane))))
					}
				}
			}
		}
	}
	return out
}

func trackedSpecs(root string) map[string]bool {
	out, err := gitx.Run(root, "ls-files", "--", "kitty-specs")
	if err != nil {
		return nil
	}
	m := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			m[l] = true
		}
	}
	return m
}

func isMissionDir(dir, name string, tracked map[string]bool) bool {
	meta := mission.ReadMetaOrEmpty(dir)
	if s, ok := valueOf(meta, "mission_id").(string); ok && s != "" {
		return true
	}
	if tracked != nil {
		return tracked["kitty-specs/"+name+"/spec.md"] || tracked["kitty-specs/"+name+"/meta.json"]
	}
	return fileExistsHC(filepath.Join(dir, "spec.md")) || fileExistsHC(filepath.Join(dir, "meta.json"))
}

func fileExistsHC(p string) bool { _, err := os.Stat(p); return err == nil }

// RunAudit is audit.run_audit over kitty-specs/ (optionally one dir name).
func RunAudit(root string, only *string) *AuditReport {
	states := AuditRepo(root)
	byslug := map[string]IdentityState{}
	for _, s := range states {
		byslug[s.Slug] = s
	}
	tracked := trackedSpecs(root)
	var results []*MissionAudit
	for _, n := range migrate.MissionDirs(root) {
		if only != nil && n != *only {
			continue
		}
		dir := project.Specs(root, n)
		if !isMissionDir(dir, n, tracked) {
			results = append(results, &MissionAudit{Slug: n, Dir: dir, Findings: []Finding{fnd("RESIDUE_DIRECTORY", "info", ".", "directory holds no tracked spec.md or meta.json and no mission_id; not a Mission. If this is a Mission created before identity existed, run `spec-kitty migrate backfill-identity` (or commit its spec.md)")}})
			continue
		}
		var fs []Finding
		fs = append(fs, classifyMeta(dir)...)
		ev, corrupt := classifyStatusEvents(dir)
		fs = append(fs, ev...)
		fs = append(fs, classifyStatusJSON(dir, corrupt)...)
		fs = append(fs, classifyJSONLFile(filepath.Join(dir, "mission-events.jsonl"), "mission-events.jsonl", "mission_event_row")...)
		fs = append(fs, classifyJSONLFile(filepath.Join(dir, "decisions", "events.jsonl"), "decisions/events.jsonl", "decision_event_row")...)
		fs = append(fs, classifyJSONLFile(filepath.Join(dir, "handoff", "events.jsonl"), "handoff/events.jsonl", "handoff_event_row")...)
		fs = append(fs, classifyWPFiles(dir)...)
		if st, ok := byslug[n]; ok && st.State == "orphan" {
			fs = append(fs, fnd("IDENTITY_MISSING", "error", "meta.json", "meta.json absent"))
		}
		results = append(results, &MissionAudit{Slug: n, Dir: dir, Findings: fs})
	}
	// repo-level findings
	bySlug := map[string]*MissionAudit{}
	for _, r := range results {
		bySlug[r.Slug] = r
	}
	add := func(slug string, f Finding) {
		if r, ok := bySlug[slug]; ok {
			r.Findings = append(r.Findings, f)
		}
	}
	dupOrder, dups := DuplicatePrefixes(root)
	for _, p := range dupOrder {
		group := dups[p]
		var all []string
		for _, s := range group {
			all = append(all, s.Slug)
		}
		sort.Strings(all)
		for _, s := range group {
			add(s.Slug, fnd("DUPLICATE_PREFIX", "warning", "meta.json", fmt.Sprintf("prefix %s shared with: %s", pyjson.ReprString(p), others(all, s.Slug))))
		}
	}
	ambOrder, amb := AmbiguousSelectors(states)
	for _, h := range ambOrder {
		group := amb[h]
		var all []string
		for _, s := range group {
			all = append(all, s.Slug)
		}
		sort.Strings(all)
		for _, s := range group {
			add(s.Slug, fnd("AMBIGUOUS_SELECTOR", "warning", "meta.json", fmt.Sprintf("handle %s also matches: %s", pyjson.ReprString(h), others(all, s.Slug))))
		}
	}
	byID := map[string][]IdentityState{}
	var idOrder []string
	for _, s := range states {
		if s.MissionID == nil {
			continue
		}
		k := pyjson.Str(s.MissionID)
		if _, ok := byID[k]; !ok {
			idOrder = append(idOrder, k)
		}
		byID[k] = append(byID[k], s)
	}
	for _, id := range idOrder {
		group := byID[id]
		if len(group) < 2 {
			continue
		}
		var all []string
		for _, s := range group {
			all = append(all, s.Slug)
		}
		sort.Strings(all)
		for _, s := range group {
			add(s.Slug, fnd("DUPLICATE_MISSION_ID", "error", "meta.json", fmt.Sprintf("mission_id %s also used by: %s", pyjson.ReprString(id), others(all, s.Slug))))
		}
	}
	for _, r := range results {
		sort.SliceStable(r.Findings, func(i, j int) bool {
			a, b := r.Findings[i], r.Findings[j]
			if a.ArtifactPath != b.ArtifactPath {
				return a.ArtifactPath < b.ArtifactPath
			}
			return a.Code < b.Code
		})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Slug < results[j].Slug })
	return &AuditReport{Missions: results}
}

func others(all []string, me string) string {
	var o []string
	for _, s := range all {
		if s != me {
			o = append(o, s)
		}
	}
	return strings.Join(o, ", ")
}

// Summary is repo_summary.
func (r *AuditReport) Summary() *jsonx.Object {
	sev := map[string]int{"error": 0, "warning": 0, "info": 0}
	blockers, total := 0, 0
	withErr, withWarn, withBlk := 0, 0, 0
	for _, m := range r.Missions {
		for _, f := range m.Findings {
			sev[f.Severity]++
			if IsTeamspaceBlocker(f) {
				blockers++
			}
		}
		total += len(m.Findings)
		if m.has("error") {
			withErr++
		}
		if m.has("warning") {
			withWarn++
		}
		if m.blockers() {
			withBlk++
		}
	}
	return jsonx.NewObject("findings_by_severity", jsonx.NewObject("error", sev["error"], "info", sev["info"], "warning", sev["warning"]),
		"missions_with_errors", withErr, "missions_with_teamspace_blockers", withBlk, "missions_with_warnings", withWarn,
		"teamspace_blockers", blockers, "total_findings", total, "total_missions", len(r.Missions))
}

// ToObject is RepoAuditReport.to_dict() (keys sorted by the serializer).
func (r *AuditReport) ToObject() *jsonx.Object {
	ms := []any{}
	counter := map[string]int{}
	for _, m := range r.Missions {
		fs := []any{}
		for _, f := range m.Findings {
			fs = append(fs, f.obj())
			counter[f.Code]++
		}
		ms = append(ms, jsonx.NewObject("finding_count", len(m.Findings), "findings", fs, "has_errors", m.has("error"),
			"has_teamspace_blockers", m.blockers(), "mission_dir", "kitty-specs/"+m.Slug, "mission_slug", m.Slug))
	}
	sc := &jsonx.Object{}
	var keys []string
	for k := range counter {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sc.Set(k, counter[k])
	}
	return jsonx.NewObject("missions", ms, "repo_summary", r.Summary(), "shape_counters", sc)
}
