package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Runtime-state cutover (migration/backfill_runtime_state.py +
// runtime_state_cutover.py): seed legacy frontmatter/checkbox runtime state as
// deterministic events, verify fail-closed, flip meta.json status_phase.
//
// verify_backfill tolerates exactly the upstream shapes: an old (pre-floor)
// `at`, the pre-#2985 full-payload claim carrier and a planned subtask seed
// superseded by legitimate later history.

// RuntimeBackfillActor is BACKFILL_ACTOR.
const RuntimeBackfillActor = "migration:backfill_runtime_state"

var claimSlots = []string{"shell_pid", "shell_pid_created_at", "agent"}

// OrderingError is MigrationOrderingError.
type OrderingError struct{ Msg string }

func (e *OrderingError) Error() string { return e.Msg }

type legacyRuntime struct {
	wpID              string
	shellPID          *int
	shellPIDCreatedAt *string
	agent             *string
	assignee          *string
	trackerRefs       []string
	subtasks          *jsonx.Object // task id -> "done"/"planned"
	review            *status.ReviewOverride
	keys              map[string]bool
}

func (r *legacyRuntime) hasClaimState() bool {
	return r.shellPID != nil || r.shellPIDCreatedAt != nil || r.agent != nil
}

func (r *legacyRuntime) hasFrontmatterRuntime() bool {
	return r.hasClaimState() || r.assignee != nil || len(r.trackerRefs) > 0 || (r.review != nil && r.review.Complete())
}

func (r *legacyRuntime) hasEvictableState() bool {
	return r.hasFrontmatterRuntime() || (r.subtasks != nil && r.subtasks.Len() > 0)
}

func (r *legacyRuntime) claimSlotValues() *jsonx.Object {
	o := &jsonx.Object{}
	if r.shellPID != nil {
		o.Set("shell_pid", *r.shellPID)
	}
	if r.shellPIDCreatedAt != nil {
		o.Set("shell_pid_created_at", *r.shellPIDCreatedAt)
	}
	if r.agent != nil {
		o.Set("agent", *r.agent)
	}
	return o
}

var (
	uncheckedRow  = regexp.MustCompile(`^-\s*\[\s*\]\s*(T\d{3,})\b`)
	checkedRow    = regexp.MustCompile(`^-\s*\[[xX]\]\s*(T\d{3,})\b`)
	headingRe     = regexp.MustCompile(`^#{2,4}[^#]`)
	headingWPRe   = regexp.MustCompile(`\bWP\d{2,}\b`)
	wpCodeLooseRe = regexp.MustCompile(`^(WP\d+)`)
	tzSuffixRe    = regexp.MustCompile(`(Z|[+-]\d\d:?\d\d)$`)
)

// SectionSubtaskRows is core.subtask_rows.iter_wp_section_subtask_rows.
func SectionSubtaskRows(text, wpID string) [][2]string {
	var out [][2]string
	in, fence := false, false
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		h := ""
		if headingRe.MatchString(line) {
			h = headingWPRe.FindString(line)
		}
		if h == wpID {
			in = true
			continue
		}
		if in && h != "" && h != wpID {
			break
		}
		if !in {
			continue
		}
		if m := checkedRow.FindStringSubmatch(s); m != nil {
			out = append(out, [2]string{m[1], "done"})
		} else if m := uncheckedRow.FindStringSubmatch(s); m != nil {
			out = append(out, [2]string{m[1], "planned"})
		}
	}
	return out
}

func readLegacyRuntime(dir string) (map[string]*legacyRuntime, error) {
	tasks := filepath.Join(dir, "tasks")
	if !isDirFollow(tasks) {
		return map[string]*legacyRuntime{}, nil
	}
	tasksMD := ""
	if b, err := os.ReadFile(filepath.Join(dir, "tasks.md")); err == nil {
		tasksMD = string(b)
	}
	files, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	sort.Strings(files)
	out := map[string]*legacyRuntime{}
	for _, f := range files {
		d, err := fm.Read(f)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s for legacy runtime: %v", filepath.Base(f), err)
		}
		stem := strings.TrimSuffix(filepath.Base(f), ".md")
		id := stem
		if m := wpCodeLooseRe.FindStringSubmatch(stem); m != nil {
			id = m[1]
		}
		meta := d.Meta
		if meta == nil {
			meta = &jsonx.Object{}
		}
		r := &legacyRuntime{wpID: id, keys: map[string]bool{}}
		for _, k := range meta.Keys() {
			r.keys[k] = true
		}
		switch v := valueOf(meta, "shell_pid").(type) {
		case int:
			if v >= 0 {
				n := v
				r.shellPID = &n
			}
		case string:
			s := strings.TrimSpace(v)
			if s != "" && strings.Trim(s, "0123456789") == "" {
				if n, err := strconv.Atoi(s); err == nil {
					r.shellPID = &n
				}
			}
		}
		if v := valueOf(meta, "shell_pid_created_at"); pyjson.Truthy(v) {
			s := pyjson.Str(v)
			r.shellPIDCreatedAt = &s
		}
		if s, ok := valueOf(meta, "agent").(string); ok && strings.TrimSpace(s) != "" {
			r.agent = &s
		}
		if v := valueOf(meta, "assignee"); pyjson.Truthy(v) {
			s := pyjson.Str(v)
			r.assignee = &s
		}
		switch t := valueOf(meta, "tracker_refs").(type) {
		case string:
			for _, p := range strings.Split(t, ",") {
				if p = strings.TrimSpace(p); p != "" {
					r.trackerRefs = append(r.trackerRefs, p)
				}
			}
		case []any:
			for _, x := range t {
				if p := strings.TrimSpace(pyjson.Str(x)); p != "" {
					r.trackerRefs = append(r.trackerRefs, p)
				}
			}
		}
		rows := SectionSubtaskRows(tasksMD, id)
		if len(rows) > 0 {
			r.subtasks = &jsonx.Object{}
			for _, row := range rows {
				r.subtasks.Set(row[0], row[1])
			}
		}
		at, actor := valueOf(meta, "review_artifact_override_at"), valueOf(meta, "review_artifact_override_actor")
		reason := valueOf(meta, "review_artifact_override_reason")
		owp := valueOf(meta, "review_artifact_override_wp_id")
		if !pyjson.Truthy(owp) {
			owp = id
		}
		if pyjson.Truthy(at) && pyjson.Truthy(actor) && pyjson.Truthy(reason) {
			ro := status.ReviewOverride{At: pyjson.Str(at), Actor: pyjson.Str(actor), WPID: pyjson.Str(owp), Reason: pyjson.Str(reason)}
			if ro.Complete() {
				r.review = &ro
			}
		}
		if r.hasEvictableState() || len(r.keys) > 0 {
			out[id] = r
		}
	}
	return out, nil
}

func seedMissionID(readDir string) string {
	meta, err := mission.ReadMeta(readDir)
	if err == nil && meta != nil {
		if v := valueOf(meta, "mission_id"); pyjson.Truthy(v) {
			return pyjson.Str(v)
		}
	}
	return filepath.Base(readDir)
}

func seedID(missionID, wpID, field string) string {
	return DeterministicID(missionID + "|" + wpID + "|" + field)
}

func isMigrationActor(a status.Actor) bool { return a.Value() == RuntimeBackfillActor }

func authentic(st *status.Stream) *status.Stream {
	out := &status.Stream{}
	for _, e := range st.Transitions {
		if !isMigrationActor(e.Actor) {
			out.Transitions = append(out.Transitions, e)
		}
	}
	for _, a := range st.Annotations {
		if !isMigrationActor(a.Actor) {
			out.Annotations = append(out.Annotations, a)
		}
	}
	return out
}

func parseAware(raw, wp string) (time.Time, error) {
	t, ok := pyParseISO(raw)
	if !ok {
		return time.Time{}, &OrderingError{fmt.Sprintf("%s: cannot represent a strict seed history floor below malformed event timestamp %s", wp, pyjson.ReprString(raw))}
	}
	if !tzSuffixRe.MatchString(strings.TrimSpace(raw)) {
		return time.Time{}, &OrderingError{fmt.Sprintf("%s: cannot represent a strict seed history floor below timezone-naive event timestamp %s", wp, pyjson.ReprString(raw))}
	}
	return t, nil
}

func encodeLike(t time.Time, like string) string {
	s := clock.ISO(t)
	if strings.HasSuffix(like, "Z") && strings.HasSuffix(s, "+00:00") {
		s = strings.TrimSuffix(s, "+00:00") + "Z"
	}
	return s
}

func shiftBefore(raw, wp string) (string, error) {
	t, err := parseAware(raw, wp)
	if err != nil {
		return "", err
	}
	enc := encodeLike(t.Add(-time.Microsecond), raw)
	if !(enc < raw) {
		enc = encodeLike(t.Truncate(time.Second).Add(-time.Second), raw)
	}
	return enc, nil
}

func wpHistoryFloor(st *status.Stream, wp string) (string, error) {
	a := authentic(st)
	type key struct{ at, id string }
	var keys []key
	for _, e := range a.Transitions {
		if e.WPID == wp {
			keys = append(keys, key{e.At, e.EventID})
		}
	}
	for _, e := range a.Annotations {
		if e.WPID == wp {
			keys = append(keys, key{e.At, e.EventID})
		}
	}
	if len(keys) == 0 {
		return "", nil
	}
	for _, k := range keys {
		if _, err := parseAware(k.at, wp); err != nil {
			return "", err
		}
	}
	min := keys[0]
	for _, k := range keys[1:] {
		if k.at < min.at || (k.at == min.at && k.id < min.id) {
			min = k
		}
	}
	floor, err := shiftBefore(min.at, wp)
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		if !(floor < k.at || (floor == k.at && "" < k.id)) {
			return "", &OrderingError{fmt.Sprintf("%s: cannot represent a strict seed history floor below the reducer key ('%s', '%s')", wp, min.at, min.id)}
		}
	}
	return floor, nil
}

func earliestTransitionAts(st *status.Stream) map[string]string {
	out := map[string]string{}
	for _, e := range authentic(st).Transitions {
		if cur, ok := out[e.WPID]; !ok || e.At < cur {
			out[e.WPID] = e.At
		}
	}
	return out
}

func claimAnchors(st *status.Stream) map[string]string {
	earliest := earliestTransitionAts(st)
	claimed := map[string]string{}
	for _, e := range authentic(st).Transitions {
		if string(e.To) == "claimed" {
			if cur, ok := claimed[e.WPID]; !ok || e.At < cur {
				claimed[e.WPID] = e.At
			}
		}
	}
	out := map[string]string{}
	for wp, at := range earliest {
		if c, ok := claimed[wp]; ok {
			out[wp] = c
		} else {
			out[wp] = at
		}
	}
	return out
}

func parseEpochOrISO(raw *string) *string {
	if raw == nil {
		return nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := int64(f)
		us := int64((f - float64(sec)) * 1e6)
		r := clock.ISO(time.Unix(sec, us*1000).UTC())
		return &r
	}
	t, ok := pyParseISO(s)
	if !ok {
		return nil
	}
	r := clock.ISO(t)
	return &r
}

func resolveAnchor(readDir, wp string, r *legacyRuntime, anchors map[string]string) (string, bool) {
	if a, ok := anchors[wp]; ok {
		return a, false
	}
	if !r.hasClaimState() {
		return "", false
	}
	if s := parseEpochOrISO(r.shellPIDCreatedAt); s != nil {
		return *s, true
	}
	if c, ok := valueOf(mission.ReadMetaOrEmpty(readDir), "created_at").(string); ok && strings.TrimSpace(c) != "" {
		return c, true
	}
	return "", false
}

func resolveSeedAnchor(st *status.Stream, readDir, wp string, r *legacyRuntime, anchors map[string]string) (string, bool, error) {
	floor, err := wpHistoryFloor(st, wp)
	if err != nil {
		return "", false, err
	}
	if floor != "" {
		return floor, false, nil
	}
	a, syn := resolveAnchor(readDir, wp, r, anchors)
	return a, syn, nil
}

func snapshotClaimSlots(st *status.Stream) map[string]*jsonx.Object {
	a := authentic(st)
	snap := status.Reduce(a.Transitions, a.Annotations, true)
	out := map[string]*jsonx.Object{}
	for _, kv := range snap.WorkPackages.Pairs() {
		wp, _ := kv.Value.(*jsonx.Object)
		o := &jsonx.Object{}
		for _, s := range claimSlots {
			if v, ok := wp.Get(s); ok && !emptyValue(v) {
				o.Set(s, v)
			}
		}
		out[kv.Key] = o
	}
	return out
}

func emptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case *jsonx.Object:
		return t.Len() == 0
	}
	return false
}

func unmigratedClaimSlots(r *legacyRuntime, present *jsonx.Object) *jsonx.Object {
	out := &jsonx.Object{}
	for _, kv := range r.claimSlotValues().Pairs() {
		if present != nil {
			if pv, ok := present.Get(kv.Key); ok && pyjson.Equal(pv, kv.Value) {
				continue
			}
		}
		out.Set(kv.Key, kv.Value)
	}
	return out
}

func retroClaimAt(anchor, earliest string) string {
	if earliest == "" || anchor < earliest {
		return anchor
	}
	t, ok := pyParseISO(earliest)
	if !ok {
		return anchor
	}
	return clock.ISO(t.Add(-time.Microsecond))
}

func sortedRuntimeIDs(m map[string]*legacyRuntime) []string {
	var ids []string
	for k := range m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

func buildRuntimeSeeds(writeDir, readDir string, legacy map[string]*legacyRuntime, st *status.Stream, warnings *[]string) ([]*status.Event, []*status.Annotation, error) {
	slug := filepath.Base(writeDir)
	mid := seedMissionID(readDir)
	earliest := earliestTransitionAts(st)
	anchors := claimAnchors(st)
	present := snapshotClaimSlots(st)
	var trans []*status.Event
	var anns []*status.Annotation
	for _, wp := range sortedRuntimeIDs(legacy) {
		r := legacy[wp]
		anchor, syn, err := resolveSeedAnchor(st, readDir, wp, r, anchors)
		if err != nil {
			return nil, nil, err
		}
		if anchor == "" {
			if r.hasEvictableState() {
				*warnings = append(*warnings, wp+": no claim anchor (never-claimed WP) — runtime seed skipped")
			}
			continue
		}
		if syn {
			*warnings = append(*warnings, wp+": no claim anchor in event log — synthesized from frontmatter claim state")
		}
		if pm := unmigratedClaimSlots(r, present[wp]); pm.Len() > 0 {
			trans = append(trans, claimCarrier(slug, mid, wp, retroClaimAt(anchor, earliest[wp]), pm))
		}
		add := func(field string, d status.Delta) {
			if d.IsEmpty() {
				return
			}
			anns = append(anns, &status.Annotation{EventID: seedID(mid, wp, field), WPID: wp, At: anchor, Actor: status.StrActor(RuntimeBackfillActor), Delta: d})
		}
		if r.assignee != nil {
			add("assignee", status.Delta{Assignee: r.assignee})
		}
		if len(r.trackerRefs) > 0 {
			refs := append([]string{}, r.trackerRefs...)
			add("tracker_refs", status.Delta{TrackerRefs: &refs})
		}
		if r.subtasks != nil && r.subtasks.Len() > 0 {
			add("subtasks", status.Delta{Subtasks: r.subtasks.Clone()})
		}
		if r.review != nil && r.review.Complete() {
			rv := *r.review
			add("review", status.Delta{Review: &rv})
		}
	}
	return trans, anns, nil
}

// claimCarrier is _claim_carrier: the seed planned -> claimed row carrying
// the claim slots in policy_metadata.
func claimCarrier(slug, mid, wp, at string, pm *jsonx.Object) *status.Event {
	var mptr *string
	if mid != slug {
		m := mid
		mptr = &m
	}
	return &status.Event{EventID: seedID(mid, wp, "claim"), MissionSlug: slug, WPID: wp, From: "planned", To: "claimed",
		At: at, Actor: status.StrActor(RuntimeBackfillActor), ExecutionMode: "worktree", PolicyMetadata: pm, MissionID: mptr}
}

// pyEqualJSON is Python == over two to_dict() payloads (key order ignored).
func pyEqualJSON(a, b *jsonx.Object) bool {
	av, err1 := pyjson.Loads(jsonx.Dumps(a, jsonx.Line))
	bv, err2 := pyjson.Loads(jsonx.Dumps(b, jsonx.Line))
	return err1 == nil && err2 == nil && pyjson.Equal(av, bv)
}

// subtaskSeedSuperseded is _subtask_seed_is_superseded_by_legitimate_history:
// an immutable seed that recorded a subtask as planned is accepted when the
// legacy source now says done and real later history proves the completion.
func subtaskSeedSuperseded(expected, actual *status.Annotation, st *status.Stream) bool {
	es, as := expected.Delta.Subtasks, actual.Delta.Subtasks
	if es == nil || as == nil || es.Len() != as.Len() {
		return false
	}
	changed := false
	for _, kv := range es.Pairs() {
		av, ok := as.Get(kv.Key)
		if !ok {
			return false
		}
		if pyjson.Equal(av, kv.Value) {
			continue
		}
		if av != "planned" || kv.Value != "done" {
			return false
		}
		changed = true
	}
	if !changed {
		return false
	}
	seedAt, err := parseAware(actual.At, actual.WPID)
	if err != nil {
		return false
	}
	witness := false
	for _, a := range st.Annotations {
		if a.WPID != actual.WPID || isMigrationActor(a.Actor) {
			continue
		}
		at, err := parseAware(a.At, a.WPID)
		if err != nil {
			return false
		}
		if at.After(seedAt) && a.Delta.Subtasks != nil && pyEqualJSON(a.Delta.Subtasks, es) {
			witness = true
		}
	}
	if !witness {
		return false
	}
	au := authentic(st)
	snap := status.Reduce(au.Transitions, au.Annotations, true)
	wp := snap.WorkPackages.GetObject(actual.WPID)
	if wp == nil {
		return false
	}
	got, _ := wp.Get("subtasks")
	g, ok := got.(*jsonx.Object)
	return ok && pyEqualJSON(g, es)
}

// RuntimeBackfillResult is backfill_runtime_state.BackfillResult.
type RuntimeBackfillResult struct {
	Slug, Action string
	Seeded       int
	Reason       string
	Warnings     []string
}

func readStream(dir string) (*status.Stream, error) {
	st, err := status.ReadStream(dir, "")
	if err != nil {
		return nil, PyStoreError(dir, err)
	}
	return st, nil
}

// BackfillRuntimeState is backfill_runtime_state(feature_dir).
func BackfillRuntimeState(dir string, dryRun bool) (*RuntimeBackfillResult, error) {
	slug := filepath.Base(dir)
	if !isDirFollow(filepath.Join(dir, "tasks")) {
		return &RuntimeBackfillResult{Slug: slug, Action: "skip", Reason: "no tasks/ directory"}, nil
	}
	var warnings []string
	legacy, err := readLegacyRuntime(dir)
	if err != nil {
		return &RuntimeBackfillResult{Slug: slug, Action: "error", Reason: "event log unreadable: " + err.Error()}, nil
	}
	st, err := readStream(dir)
	if err != nil {
		return &RuntimeBackfillResult{Slug: slug, Action: "error", Reason: "event log unreadable: " + err.Error()}, nil
	}
	trans, anns, err := buildRuntimeSeeds(dir, dir, legacy, st, &warnings)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, e := range st.Transitions {
		existing[e.EventID] = true
	}
	for _, a := range st.Annotations {
		existing[a.EventID] = true
	}
	var fresh []any
	for _, e := range trans {
		if !existing[e.EventID] {
			fresh = append(fresh, e)
		}
	}
	for _, a := range anns {
		if !existing[a.EventID] {
			fresh = append(fresh, a)
		}
	}
	refresh := func() {
		if fileExists(filepath.Join(dir, "status.json")) {
			if _, err := status.Materialize(dir); err != nil {
				warnings = append(warnings, fmt.Sprintf("status.json refresh failed after the backfill appended events: StoreError: %v", err))
			}
		}
	}
	if len(fresh) == 0 {
		if !dryRun {
			refresh()
		}
		return &RuntimeBackfillResult{Slug: slug, Action: "skip", Reason: "nothing new to seed (idempotent)", Warnings: warnings}, nil
	}
	if dryRun {
		return &RuntimeBackfillResult{Slug: slug, Action: "wrote", Seeded: len(fresh), Reason: "dry-run (no write)", Warnings: warnings}, nil
	}
	if err := status.AppendEvents(dir, fresh...); err != nil {
		return &RuntimeBackfillResult{Slug: slug, Action: "error", Reason: "event log unreadable: " + err.Error(), Warnings: warnings}, nil
	}
	refresh()
	return &RuntimeBackfillResult{Slug: slug, Action: "wrote", Seeded: len(fresh), Warnings: warnings}, nil
}

// RuntimeVerify is VerifyResult.
type RuntimeVerify struct {
	OK         bool
	WPCount    int
	Mismatches []string
}

var snapshotRuntimeSlots = []string{"shell_pid", "shell_pid_created_at", "agent", "assignee", "tracker_refs", "subtasks", "review", "role", "agent_profile", "agent_profile_version", "model", "provider"}

func withoutAt(o *jsonx.Object) *jsonx.Object {
	c := o.Clone()
	c.Delete("at")
	return c
}

// VerifyRuntimeBackfill is verify_backfill(feature_dir).
func VerifyRuntimeBackfill(dir string) (*RuntimeVerify, error) {
	legacy, err := readLegacyRuntime(dir)
	if err != nil {
		return &RuntimeVerify{Mismatches: []string{"legacy runtime unreadable: " + err.Error()}}, nil
	}
	snap, err := status.MaterializeSnapshot(dir)
	if err != nil {
		return &RuntimeVerify{Mismatches: []string{"event log unreadable: " + PyStoreError(dir, err).Error()}}, nil
	}
	st, err := readStream(dir)
	if err != nil {
		return &RuntimeVerify{Mismatches: []string{"event log unreadable: " + err.Error()}}, nil
	}
	anchors := claimAnchors(st)
	var mism []string
	seeded := map[string]bool{}
	for wp, r := range legacy {
		if a, _ := resolveAnchor(dir, wp, r, anchors); r.hasEvictableState() && a != "" {
			seeded[wp] = true
		}
	}
	snapRuntime := map[string]bool{}
	for _, kv := range snap.WorkPackages.Pairs() {
		wp, _ := kv.Value.(*jsonx.Object)
		for _, s := range snapshotRuntimeSlots {
			if v, ok := wp.Get(s); ok && v != nil && !(isEmptyList(v)) {
				snapRuntime[kv.Key] = true
			}
		}
	}
	for _, wp := range sortedKeys(seeded) {
		if !snapRuntime[wp] {
			mism = append(mism, wp+": legacy carries runtime state but snapshot has none (count mismatch)")
		}
	}
	for _, wp := range sortedKeys(snapRuntime) {
		if legacy[wp] == nil {
			mism = append(mism, wp+": snapshot carries runtime state but no legacy WP row exists (phantom / injected)")
		}
	}
	var noWarn []string
	trans, anns, err := buildRuntimeSeeds(dir, dir, legacy, st, &noWarn)
	if err != nil {
		return nil, err
	}
	actual := map[string]*jsonx.Object{}
	actualAnn := map[string]*status.Annotation{}
	for _, e := range st.Transitions {
		actual[e.EventID] = e.ToObject()
	}
	for _, a := range st.Annotations {
		actual[a.EventID] = a.ToObject()
		actualAnn[a.EventID] = a
	}
	// _legacy_contract_carriers: the pre-#2985 builder put every non-null
	// legacy claim slot on one carrier at the claim anchor.
	legacyCarriers := map[string]*jsonx.Object{}
	slug := filepath.Base(dir)
	for _, wp := range sortedRuntimeIDs(legacy) {
		if pm := legacy[wp].claimSlotValues(); pm.Len() > 0 {
			c := claimCarrier(slug, seedMissionID(dir), wp, "", pm).ToObject()
			legacyCarriers[c.GetString("event_id")] = c
		}
	}
	check := func(wp, label string, exp *jsonx.Object, expAnn *status.Annotation) {
		id := exp.GetString("event_id")
		act, ok := actual[id]
		if !ok {
			mism = append(mism, fmt.Sprintf("%s: %s mismatch (deterministic seed missing)", wp, label))
			return
		}
		if pyEqualJSON(act, exp) || pyEqualJSON(withoutAt(act), withoutAt(exp)) {
			return
		}
		if c := legacyCarriers[id]; c != nil && pyEqualJSON(withoutAt(act), withoutAt(c)) {
			return
		}
		if label == "subtasks" && expAnn != nil && actualAnn[id] != nil && subtaskSeedSuperseded(expAnn, actualAnn[id], st) {
			return
		}
		mism = append(mism, fmt.Sprintf("%s: %s mismatch (deterministic seed payload diverged)", wp, label))
	}
	for _, e := range trans {
		check(e.WPID, "claim", e.ToObject(), nil)
	}
	for _, a := range anns {
		label := "annotation"
		for _, kv := range a.Delta.ToObject().Pairs() {
			if kv.Value != nil {
				label = kv.Key
				break
			}
		}
		check(a.WPID, label, a.ToObject(), a)
	}
	// claim-slot witnesses
	mid := seedMissionID(dir)
	a := authentic(st)
	legit := status.Reduce(a.Transitions, a.Annotations, true)
	full := status.Reduce(st.Transitions, st.Annotations, true)
	present := snapshotClaimSlots(st)
	for _, wp := range sortedRuntimeIDs(legacy) {
		r := legacy[wp]
		slots := r.claimSlotValues()
		if slots.Len() == 0 {
			continue
		}
		anchor, _, err := resolveSeedAnchor(st, dir, wp, r, anchors)
		if err != nil {
			return nil, err
		}
		if anchor == "" {
			continue
		}
		carrier := unmigratedClaimSlots(r, present[wp])
		claimObj := actual[seedID(mid, wp, "claim")]
		var raw *jsonx.Object
		if claimObj != nil {
			raw = claimObj.GetObject("policy_metadata")
		}
		lw := legit.WorkPackages.GetObject(wp)
		aw := full.WorkPackages.GetObject(wp)
		keys := slots.Keys()
		sort.Strings(keys)
		for _, s := range keys {
			lv, _ := slots.Get(s)
			if carrier.Has(s) {
				if claimObj == nil {
					mism = append(mism, fmt.Sprintf("%s: raw claim-slot witness missing for %s (deterministic claim seed absent)", wp, s))
				} else if rv, _ := raw.Get(s); raw == nil || !pyjson.Equal(rv, lv) {
					mism = append(mism, fmt.Sprintf("%s: raw claim-slot witness for %s diverged", wp, s))
				}
			}
			var later any
			if lw != nil {
				later, _ = lw.Get(s)
			}
			expected := lv
			owner := "legacy seed"
			if later != nil {
				expected, owner = later, "later legitimate writer"
			}
			var got any
			if aw != nil {
				got, _ = aw.Get(s)
			}
			if !pyjson.Equal(got, expected) {
				mism = append(mism, fmt.Sprintf("%s: reduced claim-slot witness for %s does not match %s", wp, s, owner))
			}
		}
	}
	// ordering guard
	for _, wp := range sortedRuntimeIDs(legacy) {
		r := legacy[wp]
		var slots []string
		if c := actual[seedID(mid, wp, "claim")]; c != nil {
			if pm := c.GetObject("policy_metadata"); pm != nil {
				for _, s := range claimSlots {
					if pm.Has(s) {
						slots = append(slots, s)
					}
				}
			}
		}
		for _, f := range []string{"assignee", "tracker_refs", "review"} {
			if _, ok := actual[seedID(mid, wp, f)]; ok {
				slots = append(slots, f)
			}
		}
		sort.Strings(slots)
		for _, s := range slots {
			key := s
			if s == "review" {
				key = "review_artifact_override_at"
			}
			if !r.keys[key] {
				return nil, &OrderingError{fmt.Sprintf("%s: deterministic seed carries '%s' but frontmatter key '%s' is absent — strip_mutable_fields ran before verify (pinned order is backfill -> verify(pre-strip) -> cutover -> strip)", wp, s, key)}
			}
		}
	}
	return &RuntimeVerify{OK: len(mism) == 0, WPCount: len(seeded), Mismatches: mism}, nil
}

func isEmptyList(v any) bool {
	switch t := v.(type) {
	case []any:
		return len(t) == 0
	case *jsonx.Object:
		return t.Len() == 0
	}
	return false
}

// CutoverResult is runtime_state_cutover.CutoverResult.
type CutoverResult struct {
	Slug                                string
	Flipped, WouldFlip, AlreadyMigrated bool
	Seeded                              int
	Verify                              *RuntimeVerify
	Error                               *string
	// PlacementMismatch marks Error as _flip_phase's PlacementMismatchError
	// (raised out of cutover_mission; cutover_repo records it).
	PlacementMismatch bool
}

// canonicalRootOf is resolve_canonical_root(dir): the main repository root
// of the checkout holding dir ("" when dir is in no git checkout).
func canonicalRootOf(dir string) string {
	top, err := gitx.Toplevel(dir)
	if err != nil {
		return ""
	}
	return project.MainRepoRoot(top)
}

// flipTarget is _flip_target → canonicalize_feature_dir(dir).
func flipTarget(dir string) string {
	if filepath.Base(filepath.Dir(dir)) != project.SpecsDir {
		return dir
	}
	for c := dir; c != filepath.Dir(c); c = filepath.Dir(c) {
		if filepath.Base(filepath.Dir(c)) == project.WorktreesDir && strings.HasSuffix(filepath.Base(c), "-coord") {
			return dir
		}
	}
	root := canonicalRootOf(dir)
	if root == "" {
		return dir
	}
	if cand := project.Specs(root, filepath.Base(dir)); pathExists(cand) {
		return cand
	}
	return dir
}

// primaryHomeOrDegrade is _resolve_primary_home_or_degrade: the placement
// port's PRIMARY home, "" when resolution degrades.
func primaryHomeOrDegrade(dir string) string {
	root := canonicalRootOf(dir)
	if root == "" {
		return ""
	}
	home, err := placement.PrimaryDir(root, filepath.Base(dir))
	if err != nil {
		return ""
	}
	return home
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isSnapshotAuthority(meta *jsonx.Object) bool {
	v, _ := meta.Get("status_phase")
	n, err := strconv.Atoi(strings.TrimSpace(pyjson.Str(v)))
	return err == nil && n >= 1
}

// CutoverMission is runtime_state_cutover.cutover_mission(feature_dir).
func CutoverMission(dir string, dryRun bool) CutoverResult {
	slug := filepath.Base(dir)
	seed, err := BackfillRuntimeState(dir, dryRun)
	if err != nil {
		return CutoverResult{Slug: slug, Error: strPtr(err.Error())}
	}
	if seed.Action == "error" {
		r := seed.Reason
		return CutoverResult{Slug: slug, Seeded: seed.Seeded, Error: &r}
	}
	v, err := VerifyRuntimeBackfill(dir)
	if err != nil {
		return CutoverResult{Slug: slug, Seeded: seed.Seeded, Error: strPtr(err.Error())}
	}
	if !v.OK {
		return CutoverResult{Slug: slug, Seeded: seed.Seeded, Verify: v}
	}
	already := isSnapshotAuthority(mission.ReadMetaOrEmpty(dir))
	if dryRun {
		return CutoverResult{Slug: slug, WouldFlip: true, AlreadyMigrated: already, Seeded: seed.Seeded, Verify: v}
	}
	// _flip_phase: the placement port's PRIMARY home must be the write target.
	target := flipTarget(dir)
	if home := primaryHomeOrDegrade(dir); home != "" && home != target {
		msg := fmt.Sprintf("_flip_phase refuses to write status_phase for %s: the placement port resolved its PRIMARY home to %s, which does not match the write target %s (fail-closed, FR-001).",
			mission.PyRepr(slug), home, target)
		return CutoverResult{Slug: slug, Seeded: seed.Seeded, Error: &msg, PlacementMismatch: true}
	}
	meta, merr := LoadMetaFailClosed(target)
	if merr == nil {
		if meta == nil {
			meta = &jsonx.Object{}
		}
		if !isSnapshotAuthority(meta) {
			meta.Set("status_phase", "1")
			mission.WriteMeta(target, meta, false)
		}
	} else {
		return CutoverResult{Slug: slug, Seeded: seed.Seeded, Verify: v, Error: strPtr(merr.Error())}
	}
	return CutoverResult{Slug: slug, Flipped: true, AlreadyMigrated: already, Seeded: seed.Seeded, Verify: v}
}

// CutoverRepo is runtime_state_cutover.cutover_repo.
func CutoverRepo(root string, dryRun bool) []CutoverResult {
	if !SpecsExist(root) {
		warnf("kitty-specs/ not found at %s", root)
		return nil
	}
	var out []CutoverResult
	for _, n := range MissionDirs(root) {
		out = append(out, CutoverMission(project.Specs(root, n), dryRun))
	}
	return out
}
