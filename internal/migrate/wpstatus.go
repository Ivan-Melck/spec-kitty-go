package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// WP-status snapshot backfill (#5579): migration/wp_status_backfill.py +
// backfill_runtime_state.apply_wp_status_backfill_repo.

// Seed constants.
const (
	WPStatusActor       = "migration:backfill_wp_status"
	CoordSurfaceLive    = "COORD_SURFACE_LIVE"
	CoordSurfaceLiveMsg = "its status log lives on a live coordination surface, so the PRIMARY-partition log is not the authority and seeding it would split-brain the Mission. Consolidate the Mission first (`spec-kitty consolidate --mission <slug>`) and rerun once the coordination branch is gone."
	fallbackSeedAt      = "1970-01-01T00:00:00+00:00"
)

// WPStatusResult is WpStatusBackfillResult.
type WPStatusResult struct {
	Slug                                            string
	Seeded, WouldSeed                               int
	FilesOnly, SnapshotOnly, Malformed              []string
	TerminalReason, RefreshError, SkipReason, Error *string
	StatusJSONRefreshed                             bool
}

// SelectorRejectedError is MissionSelectorRejectedError.
type SelectorRejectedError struct{ Msg string }

func (e *SelectorRejectedError) Error() string { return e.Msg }

var wpMetadataFields = map[string]bool{}

func init() {
	for _, f := range strings.Fields(`work_package_id title dependencies base_branch base_commit created_at planning_base_branch
		merge_target_branch branch_strategy requirement_refs tracker_refs priority execution_mode owned_files create_intent
		authoritative_surface scope task_type subtasks phase phases assignee agent model agent_profile role shell_pid
		shell_pid_created_at history lane feature_slug activity_log review_status reviewed_by approved_by reviewer
		reviewer_agent reviewer_shell_pid review_feedback review_feedback_file subtitle description estimated_duration tags
		mission_id mission_number mission_slug status wp_code work_package_title`) {
		wpMetadataFields[f] = true
	}
}

var (
	wpIDRe    = regexp.MustCompile(`^WP\d{2,}$`)
	baseSHARe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// wpTaskFiles is status.wp_task_files.
func wpTaskFiles(tasks string) []string {
	entries, err := os.ReadDir(tasks)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "WP") && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.Join(tasks, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// authoredWPID approximates read_authored_wp_frontmatter (WPMetadata strict
// validation): the work_package_id, or ok=false for a malformed file.
func authoredWPID(path string) (string, bool) {
	d, err := fm.Read(path)
	if err != nil || d.Meta == nil {
		return "", false
	}
	for _, k := range d.Meta.Keys() {
		if !wpMetadataFields[k] {
			return "", false
		}
	}
	id, ok := d.Meta.Get("work_package_id")
	s, isStr := id.(string)
	if !ok || !isStr || !wpIDRe.MatchString(s) {
		return "", false
	}
	if t, ok := d.Meta.Get("title"); ok && t != nil {
		if ts, isStr := t.(string); !isStr || strings.TrimSpace(ts) == "" {
			return "", false
		}
	}
	if b, ok := d.Meta.Get("base_commit"); ok && b != nil {
		if bs, isStr := b.(string); !isStr || (bs != "unknown" && !baseSHARe.MatchString(bs)) {
			return "", false
		}
	}
	return s, true
}

func seedTimestamp(meta *jsonx.Object) string {
	raw, ok := valueOf(meta, "created_at").(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallbackSeedAt
	}
	t, ok := pyParseISO(strings.TrimSpace(raw))
	if !ok {
		return fallbackSeedAt
	}
	return clock.ISO(t.UTC())
}

type wpPlan struct {
	slug                               string
	filesOnly, snapshotOnly, malformed []string
	evidence                           *string
	events                             []*status.Event
}

func planWPStatus(dir string, manifest map[string]string) (*wpPlan, error) {
	slug := filepath.Base(dir)
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		return nil, err
	}
	var missionID *string
	if v := valueOf(meta, "mission_id"); pyjson.Truthy(v) {
		s := pyjson.Str(v)
		missionID = &s
	}
	files := map[string]bool{}
	var malformed []string
	for _, f := range wpTaskFiles(filepath.Join(dir, "tasks")) {
		id, ok := authoredWPID(f)
		if !ok {
			warnf("Skipping %s: malformed frontmatter", filepath.Base(f))
			malformed = append(malformed, filepath.Base(f))
			continue
		}
		files[id] = true
	}
	snap, err := status.MaterializeSnapshot(dir)
	if err != nil {
		return nil, PyStoreError(dir, err)
	}
	snapIDs := map[string]bool{}
	for _, k := range snap.WorkPackages.Keys() {
		snapIDs[k] = true
	}
	p := &wpPlan{slug: slug, malformed: malformed}
	for id := range files {
		if !snapIDs[id] {
			p.filesOnly = append(p.filesOnly, id)
		}
	}
	for id := range snapIDs {
		if !files[id] {
			p.snapshotOnly = append(p.snapshotOnly, id)
		}
	}
	sort.Strings(p.filesOnly)
	sort.Strings(p.snapshotOnly)
	for _, k := range []string{"merged_at", "accepted_at"} {
		if s, ok := valueOf(meta, k).(string); ok && strings.TrimSpace(s) != "" {
			d := fmt.Sprintf("meta.json %s=%s", k, strings.TrimSpace(s))
			p.evidence = &d
			break
		}
	}
	if p.evidence == nil {
		if r := strings.TrimSpace(manifest[slug]); r != "" {
			d := "evidence manifest: " + r
			p.evidence = &d
		}
	}
	if missionID != nil && *missionID == slug {
		missionID = nil
	}
	key := slug
	if missionID != nil {
		key = *missionID
	}
	planned := seedTimestamp(meta)
	pt, _ := pyParseISO(planned)
	doneAt := clock.ISO(pt.Add(time.Second))
	for _, wp := range p.filesOnly {
		reason := "wp-status backfill (#5579): WP file has no lane event; seeded planned"
		p.events = append(p.events, &status.Event{EventID: DeterministicID(key + "|" + wp + "|planned|wp-status-backfill"), MissionSlug: slug, WPID: wp,
			From: "genesis", To: "planned", At: planned, Actor: status.StrActor(WPStatusActor), ExecutionMode: "worktree", Reason: &reason, MissionID: missionID})
		if p.evidence != nil {
			r := fmt.Sprintf("wp-status backfill (#5579): Mission finished (%s); seeded WP driven to done", *p.evidence)
			p.events = append(p.events, &status.Event{EventID: DeterministicID(key + "|" + wp + "|done|wp-status-backfill"), MissionSlug: slug, WPID: wp,
				From: "planned", To: "done", At: doneAt, Actor: status.StrActor(WPStatusActor), Force: true, ExecutionMode: "worktree", Reason: &r, MissionID: missionID})
		}
	}
	return p, nil
}

// CoordinationSurfaceIsLive is wp_status_backfill.coordination_surface_is_live.
func CoordinationSurfaceIsLive(root, dir string) bool {
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		return true
	}
	branch := valueOf(meta, "coordination_branch")
	if !pyjson.Truthy(branch) {
		return false
	}
	if IsMissionCompleted(dir) {
		return false
	}
	s := coord.Resolve(root, filepath.Base(dir))
	if !s.Routed() {
		return false
	}
	if fileExists(filepath.Join(s.CoordDir, "status.events.jsonl")) {
		return true
	}
	return CoordBranchExists(root, pyjson.Str(branch))
}

func (p *wpPlan) result(fields WPStatusResult) WPStatusResult {
	fields.Slug = p.slug
	fields.FilesOnly, fields.SnapshotOnly, fields.Malformed = p.filesOnly, p.snapshotOnly, p.malformed
	fields.TerminalReason = p.evidence
	return fields
}

func errResult(slug string, err error) WPStatusResult {
	typ := "StoreError"
	if _, ok := err.(*MetaReadError); ok {
		typ = "MissionMetaReadError"
	}
	return WPStatusResult{Slug: slug, Error: strPtr(typ + ": " + err.Error())}
}

// ApplyWPStatusBackfill is apply_wp_status_backfill(feature_dir).
func ApplyWPStatusBackfill(root, dir string, dryRun bool, manifest map[string]string) WPStatusResult {
	slug := filepath.Base(dir)
	if !isDirFollow(filepath.Join(dir, "tasks")) {
		return WPStatusResult{Slug: slug, SkipReason: strPtr("no tasks/ directory")}
	}
	probed := false
	if pre, err := planWPStatus(dir, nil); err == nil && len(pre.filesOnly) > 0 {
		probed = true
		if CoordinationSurfaceIsLive(root, dir) {
			return WPStatusResult{Slug: slug, SkipReason: strPtr(CoordSurfaceLive)}
		}
	}
	p, err := planWPStatus(dir, manifest)
	if err != nil {
		return errResult(slug, err)
	}
	rows, err := status.ReadRows(dir)
	if err != nil {
		return errResult(slug, PyStoreError(dir, err))
	}
	existing := map[string]bool{}
	for _, r := range rows {
		existing[pyjson.Str(valueOf(r, "event_id"))] = true
	}
	var fresh []any
	for _, e := range p.events {
		if !existing[e.EventID] {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return p.result(WPStatusResult{SkipReason: strPtr("nothing new to seed (idempotent)")})
	}
	if !probed && CoordinationSurfaceIsLive(root, dir) {
		return WPStatusResult{Slug: slug, SkipReason: strPtr(CoordSurfaceLive)}
	}
	if dryRun {
		return p.result(WPStatusResult{WouldSeed: len(fresh), SkipReason: strPtr("dry-run (no write)")})
	}
	if err := status.AppendEvents(dir, fresh...); err != nil {
		return errResult(slug, err)
	}
	res := WPStatusResult{Seeded: len(fresh), WouldSeed: len(fresh)}
	if fileExists(filepath.Join(dir, "status.json")) {
		if _, err := status.Materialize(dir); err != nil {
			res.RefreshError = strPtr(fmt.Sprintf("status.json refresh failed after the backfill appended events: StoreError: %v", err))
		} else {
			res.StatusJSONRefreshed = true
		}
	}
	return p.result(res)
}

// ApplyWPStatusBackfillRepo is apply_wp_status_backfill_repo.
func ApplyWPStatusBackfillRepo(root string, mission *string, dryRun bool, manifest map[string]string) ([]WPStatusResult, error) {
	var names []string
	if !SpecsExist(root) {
		warnf("kitty-specs/ not found at %s", root)
		return nil, nil
	}
	if mission != nil {
		if !(len(*mission) > 0 && !strings.ContainsAny(*mission, "/\\") && *mission != "." && *mission != "..") {
			return nil, &SelectorRejectedError{fmt.Sprintf("Unsafe path segment: %s", pyjson.ReprString(*mission))}
		}
		if !isDirFollow(project.Specs(root, *mission)) {
			warnf("No mission directory found for slug %s", pyjson.ReprString(*mission))
			return nil, nil
		}
		names = []string{*mission}
	} else {
		names = MissionDirs(root)
	}
	var out []WPStatusResult
	for _, n := range names {
		out = append(out, ApplyWPStatusBackfill(root, project.Specs(root, n), dryRun, manifest))
	}
	return out, nil
}
