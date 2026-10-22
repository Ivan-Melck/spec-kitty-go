package status

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Derived view filenames under .kittify/derived/<slug>/.
const (
	BoardSummaryFile    = "board-summary.json"
	DerivedProgressFile = "progress.json"
	DerivedLifecycle    = "lifecycle.json"
)

// LaneWeights is DEFAULT_LANE_WEIGHTS (weighted readiness).
var LaneWeights = map[string]float64{
	"genesis": 0, "uninitialized": 0, "planned": 0, "claimed": 0.05, "in_progress": 0.3,
	"for_review": 0.6, "in_review": 0.7, "approved": 0.8, "done": 1.0, "blocked": 0, "canceled": 0,
}

// PyRound mirrors Python round(x, n) for finite floats.
func PyRound(x float64, n int) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
	return v
}

// Progress is compute_weighted_progress's result.
type Progress struct {
	Percentage float64
	DoneCount  int
	TotalCount int
	PerLane    *jsonx.Object
	PerWP      []any
}

// DonePercentage is compute_done_percentage.
func (p *Progress) DonePercentage() float64 {
	if p.TotalCount <= 0 {
		return 0
	}
	return float64(p.DoneCount) / float64(p.TotalCount) * 100.0
}

// ComputeProgress is compute_weighted_progress(snapshot) with default weights.
func ComputeProgress(snap *Snapshot) *Progress {
	p := &Progress{PerLane: &jsonx.Object{}, PerWP: []any{}}
	ids := snap.WorkPackages.Keys()
	sort.Strings(ids)
	var sum, total float64
	for _, id := range ids {
		st := snap.WorkPackages.GetObject(id)
		lane := string(Genesis)
		if st != nil {
			if v, ok := st.Get("lane"); ok {
				lane = PyStr(v)
			}
		}
		lw := LaneWeights[lane]
		sum += lw
		total += 1.0
		n, _ := p.PerLane.GetInt(lane)
		p.PerLane.Set(lane, n+1)
		if lane == string(Done) {
			p.DoneCount++
		}
		p.PerWP = append(p.PerWP, jsonx.NewObject("wp_id", id, "lane", lane, "lane_weight", lw, "wp_weight", 1.0, "fractional_progress", 1.0*lw))
	}
	p.TotalCount = len(ids)
	if total > 0 {
		p.Percentage = sum / total * 100.0
	}
	return p
}

// ProgressObject renders ProgressResult.to_dict().
func ProgressObject(snap *Snapshot) *jsonx.Object {
	p := ComputeProgress(snap)
	var num, typ any
	if snap.MissionNumber != nil {
		num = *snap.MissionNumber
	}
	if snap.MissionType != nil {
		typ = *snap.MissionType
	}
	o := IdentityFields(snap.MissionSlug, num, typ)
	o.Set("percentage", PyRound(p.Percentage, 4))
	o.Set("progress_semantics", "weighted_readiness")
	o.Set("weighted_percentage", PyRound(p.Percentage, 4))
	o.Set("done_percentage", PyRound(p.DonePercentage(), 4))
	o.Set("done_count", p.DoneCount)
	o.Set("total_count", p.TotalCount)
	o.Set("per_lane_counts", p.PerLane)
	o.Set("per_wp", p.PerWP)
	return o
}

// BoardSummary renders _build_board_summary(snapshot).
func BoardSummary(snap *Snapshot) *jsonx.Object {
	lanes := &jsonx.Object{}
	ids := snap.WorkPackages.Keys()
	sort.Strings(ids)
	for _, id := range ids {
		lane := string(Genesis)
		if st := snap.WorkPackages.GetObject(id); st != nil {
			if v, ok := st.Get("lane"); ok {
				lane = PyStr(v)
			}
		}
		l, _ := lanes.Get(lane)
		list, _ := l.([]any)
		lanes.Set(lane, append(list, id))
	}
	var num any
	if snap.MissionNumber != nil {
		num = *snap.MissionNumber
		if n, err := strconv.Atoi(*snap.MissionNumber); err == nil && isDigitString(*snap.MissionNumber) {
			num = n
		}
	}
	var typ any
	if snap.MissionType != nil {
		typ = *snap.MissionType
	}
	return jsonx.NewObject("mission_slug", snap.MissionSlug, "mission_number", num, "mission_type", typ,
		"total_wps", snap.WorkPackages.Len(), "summary", snap.Summary, "lanes", lanes, "materialized_at", snap.MaterializedAt)
}

func isDigitString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var activeLanes = map[Lane]bool{Claimed: true, InProgress: true, ForReview: true, InReview: true, Approved: true, Blocked: true}

func parseDT(raw any) (time.Time, bool) {
	s, ok := raw.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return time.Time{}, false
	}
	t, err := clock.ParseISO(strings.Replace(s, "Z", "+00:00", 1))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// LifecycleObject is derive_mission_lifecycle(feature_dir, snapshot).to_dict().
func LifecycleObject(featureDir string, snap *Snapshot, now time.Time) *jsonx.Object {
	meta := mission.ReadMetaOrEmpty(featureDir)
	_, hasLogErr := os.Stat(EventsPath(featureDir))
	hasLog := hasLogErr == nil
	slug := snap.MissionSlug
	if slug == "" {
		slug = meta.GetString("mission_slug")
		if slug == "" {
			slug = meta.GetString("slug")
		}
		if slug == "" || !mission.SafePathSegment(slug) {
			slug = filepath.Base(featureDir)
		}
	}
	prog := ComputeProgress(snap)
	total := snap.WorkPackages.Len()
	active, blocked, review, terminal := 0, 0, 0, 0
	var lastTransition time.Time
	haveLast := false
	for _, kv := range snap.WorkPackages.Pairs() {
		st, _ := kv.Value.(*jsonx.Object)
		if st == nil {
			continue
		}
		lane := Lane(st.GetString("lane"))
		if !IsLane(string(lane)) {
			lane = Genesis
		}
		if activeLanes[lane] {
			active++
		}
		if lane == Blocked {
			blocked++
		}
		if ProgressBucket(lane) == "review" {
			review++
		}
		if IsTerminal(lane) {
			terminal++
		}
		if v, ok := st.Get("last_transition_at"); ok {
			if t, ok := parseDT(v); ok && (!haveLast || t.After(lastTransition)) {
				lastTransition, haveLast = t, true
			}
		}
	}
	if !haveLast {
		if t, ok := parseDT(snap.MaterializedAt); ok {
			lastTransition, haveLast = t, true
		}
	}
	lastActivity, haveActivity := lastTransition, haveLast
	if !haveActivity {
		if t, ok := parseDT(meta.GetString("created_at")); ok {
			lastActivity, haveActivity = t, true
		} else if st, err := os.Stat(featureDir); err == nil {
			lastActivity, haveActivity = st.ModTime().UTC(), true
		}
	}
	var ageDays any
	age := -1
	if haveActivity {
		d := int(now.UTC().Sub(lastActivity).Hours() / 24)
		if now.UTC().Sub(lastActivity) < 0 {
			d = 0
		}
		age = d
		ageDays = d
	}
	post := PostMissionEvents(featureDir)
	postList := []any{}
	for _, e := range post {
		postList = append(postList, e)
	}
	var lastFollowUp any
	if t, ok := lastEventAt(post, FollowUpRecordedEvent); ok {
		lastFollowUp = clock.ISO(t)
	}
	state, surface, reason := classifyLifecycle(total, active, terminal, age)
	if isReopened(featureDir, post) {
		state, surface, reason = "reopened", "reopened", "Mission was re-opened after merge and is actionable again."
	}
	var mnum any
	if n, ok := mission.MissionNumber(meta); ok {
		mnum = n
	}
	var mid any
	if v, ok := meta.Get("mission_id"); ok {
		mid = v
	}
	iso := func(ok bool, t time.Time) any {
		if !ok {
			return nil
		}
		return clock.ISO(t)
	}
	var surf any
	if surface != "" {
		surf = surface
	}
	return jsonx.NewObject(
		"mission_slug", slug, "mission_id", mid, "mission_number", mnum, "mission_type", mission.MissionType(meta),
		"state", state, "surface_state", surf, "reason", reason,
		"last_activity_at", iso(haveActivity, lastActivity), "last_transition_at", iso(haveLast, lastTransition),
		"age_days", ageDays, "completion_pct", PyRound(prog.Percentage, 1), "event_count", snap.EventCount,
		"total_wps", total, "active_wp_count", active, "blocked_wp_count", blocked, "review_wp_count", review,
		"terminal_wp_count", terminal, "has_event_log", hasLog, "post_mission_events", postList,
		"last_follow_up_at", lastFollowUp, "stale_after_days", 14, "abandoned_after_days", 30)
}

func classifyLifecycle(total, active, terminal, age int) (string, string, string) {
	if active > 0 {
		if age >= 30 {
			return "abandoned", "", "Active work appears abandoned and should stay off primary surfaces."
		}
		if age >= 14 {
			return "stale", "", "Active work has gone stale and should be recovered before it resurfaces."
		}
		return "active", "active", "Mission has live work in progress or review."
	}
	if total > 0 && terminal == total {
		if age >= 0 && age <= 3 {
			return "recently_completed", "recently_completed", "Mission completed recently and still deserves short-lived visibility."
		}
		return "archived", "", "Mission is completed and now belongs in history rather than the default surface."
	}
	if age >= 30 {
		return "abandoned", "", "Mission never reached an active terminal state and now looks abandoned."
	}
	if age >= 14 {
		return "stale", "", "Mission is recoverable but stale and should not pollute primary surfaces."
	}
	return "recoverable", "", "Mission is recoverable history and not active enough for the default surface."
}

// LedgerProjectionEnabled is ledger_posture(root).enabled (default true).
func LedgerProjectionEnabled(root string) bool {
	b, err := os.ReadFile(project.Kittify(root, "config.yaml"))
	if err != nil {
		return true
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return true
	}
	led := o.GetObject("ledger")
	if led == nil {
		return true
	}
	if v, ok := led.Get("projection"); ok {
		if bv, ok := v.(bool); ok {
			return bv
		}
	}
	return true
}

var gitOpMarkers = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer", "index.lock"}

// GitOperationInProgress reports an in-progress merge/rebase/... in root.
func GitOperationInProgress(root string) bool {
	var dirs []string
	if d, err := gitx.Run(root, "rev-parse", "--absolute-git-dir"); err == nil && d != "" {
		dirs = append(dirs, d)
	}
	if d, err := gitx.CommonDir(root); err == nil && d != "" {
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		for _, m := range gitOpMarkers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return true
			}
		}
	}
	return false
}

func writeDerivedJSON(path string, o *jsonx.Object) error {
	text := jsonx.Dumps(o, SnapshotOptions) + "\n"
	tmp := strings.TrimSuffix(path, ".json") + ".json.tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RefreshDerivedViews is refresh_execution_projection: when the ledger
// projection is enabled and no git operation is in progress, write
// .kittify/derived/<slug>/{status,board-summary,progress,lifecycle}.json
// from the write-free snapshot of featureDir. Best effort (returns false on
// a no-op or failure).
func RefreshDerivedViews(featureDir, repoRoot string) bool {
	if repoRoot == "" || !LedgerProjectionEnabled(repoRoot) || GitOperationInProgress(repoRoot) {
		return false
	}
	snap, err := MaterializeSnapshot(featureDir)
	if err != nil {
		return false
	}
	slug := snap.MissionSlug
	if slug == "" {
		slug = filepath.Base(featureDir)
	}
	out := project.Kittify(repoRoot, "derived", slug)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return false
	}
	if writeDerivedJSON(filepath.Join(out, SnapshotFile), snap.ToObject()) != nil ||
		writeDerivedJSON(filepath.Join(out, BoardSummaryFile), BoardSummary(snap)) != nil ||
		writeDerivedJSON(filepath.Join(out, DerivedProgressFile), ProgressObject(snap)) != nil {
		return false
	}
	lc := LifecycleObject(featureDir, snap, clock.Now())
	if s := lc.GetString("mission_slug"); s != "" && s != slug {
		out = project.Kittify(repoRoot, "derived", s)
		os.MkdirAll(out, 0o755)
	}
	return writeDerivedJSON(filepath.Join(out, DerivedLifecycle), lc) == nil
}
