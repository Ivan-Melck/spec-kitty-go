package status

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Snapshot is the materialized status.json (StatusSnapshot).
type Snapshot struct {
	MissionSlug    string
	MaterializedAt string
	EventCount     int
	LastEventID    *string
	WorkPackages   *jsonx.Object // wp id -> *jsonx.Object state
	Summary        *jsonx.Object // lane -> count
	MissionNumber  *string
	MissionType    *string
	Retrospective  *jsonx.Object
	SchemaVersion  int // 0 = absent
}

// WP returns the reduced state of wpID (nil when absent).
func (s *Snapshot) WP(wpID string) *jsonx.Object { return s.WorkPackages.GetObject(wpID) }

// Lane returns the reduced lane of wpID ("" when absent).
func (s *Snapshot) Lane(wpID string) Lane {
	if st := s.WP(wpID); st != nil {
		return Lane(st.GetString("lane"))
	}
	return ""
}

// ToObject renders StatusSnapshot.to_dict().
func (s *Snapshot) ToObject() *jsonx.Object {
	var num any
	if s.MissionNumber != nil {
		num = *s.MissionNumber
	}
	var typ any
	if s.MissionType != nil {
		typ = *s.MissionType
	}
	o := IdentityFields(s.MissionSlug, num, typ)
	var last any
	if s.LastEventID != nil {
		last = *s.LastEventID
	}
	o.Set("materialized_at", s.MaterializedAt)
	o.Set("event_count", s.EventCount)
	o.Set("last_event_id", last)
	o.Set("work_packages", s.WorkPackages)
	o.Set("summary", s.Summary)
	if s.Retrospective != nil {
		o.Set("retrospective", s.Retrospective)
	}
	if s.SchemaVersion != 0 {
		o.Set("schema_version", s.SchemaVersion)
	}
	return o
}

// SnapshotOptions is json.dumps(sort_keys=True, indent=2, ensure_ascii=False).
var SnapshotOptions = jsonx.Options{Indent: 2, SortKeys: true}

// Render returns the status.json text (with trailing newline).
func (s *Snapshot) Render() string { return jsonx.Dumps(s.ToObject(), SnapshotOptions) + "\n" }

// IdentityFields is mission_identity_fields (CLI variant: no software-dev
// default for the type).
func IdentityFields(slug string, number any, missionType any) *jsonx.Object {
	slug = strings.TrimSpace(slug)
	num := ""
	switch t := number.(type) {
	case int:
		num = itoa(t)
	case nil:
	default:
		num = strings.TrimSpace(PyStr(t))
		if s, ok := t.(string); ok {
			num = strings.TrimSpace(s)
		}
	}
	if num == "" {
		if n, ok := mission.NumberFromSlug(slug); ok {
			num = itoa(n)
		}
	}
	typ := ""
	if s, ok := missionType.(string); ok {
		typ = strings.TrimSpace(s)
	}
	return jsonx.NewObject("mission_slug", slug, "mission_number", num, "mission_type", typ)
}

func itoa(n int) string { return PyStr(n) }

// MigrationActorPrefix marks reconstruction-seed actors.
const MigrationActorPrefix = "migration:"

func isSeed(e *Event) bool { return strings.HasPrefix(e.Actor.Identity(), MigrationActorPrefix) }

var rejectionTargets = map[Lane]bool{Planned: true, Claimed: true, InProgress: true}

func isRollback(e *Event) bool {
	if e.From == InReview {
		return rejectionTargets[e.To]
	}
	if e.From == ForReview && e.ReviewRef != nil {
		return rejectionTargets[e.To]
	}
	return false
}

func isRollbackSeed(e *Event) bool { return isSeed(e) && isRollback(e) }

func evKey(e *Event) string { return e.At + "\x00" + e.EventID }

func keyLess(a, b *Event) bool {
	if a.At != b.At {
		return a.At < b.At
	}
	return a.EventID < b.EventID
}

func isBareSibling(approval, seed *Event, all []*Event) bool {
	if isRollback(approval) || approval.To != Approved || approval.From != seed.From {
		return false
	}
	for _, ev := range all {
		if ev.WPID == approval.WPID && ev.From == Approved && !isSeed(ev) && keyLess(approval, ev) {
			return false
		}
	}
	return true
}

func yieldsToReal(setter, ev *Event, all []*Event) bool {
	return isRollbackSeed(setter) && !isSeed(ev) && !isBareSibling(ev, setter, all)
}

func claimSidecarWrites(e *Event) []jsonx.KV {
	if e.From != Planned || e.To != Claimed {
		return nil
	}
	meta := e.PolicyMetadata
	if meta == nil {
		return nil
	}
	var out []jsonx.KV
	for _, slot := range []string{"shell_pid", "shell_pid_created_at"} {
		if v, ok := meta.Get(slot); ok && v != nil {
			out = append(out, jsonx.KV{Key: slot, Value: v})
		}
	}
	if v, ok := meta.Get("agent"); ok && truthyAny(v) {
		out = append(out, jsonx.KV{Key: "agent", Value: v})
	}
	return out
}

var runtimeSlots = []string{"shell_pid", "shell_pid_created_at", "subtasks", "notes", "tracker_refs", "agent",
	"assignee", "review", "role", "agent_profile", "agent_profile_version", "model", "provider"}

var replaceSlots = []string{"shell_pid", "shell_pid_created_at", "agent", "assignee", "role", "agent_profile",
	"agent_profile_version", "model", "provider"}

func writesVerdict(e *Event) bool { return e.ReviewResult != nil || e.From == InReview }

func wpStateFromEvent(e *Event, prev *jsonx.Object) *jsonx.Object {
	force := 0
	if prev != nil {
		force, _ = prev.GetInt("force_count")
	}
	if e.Force {
		force++
	}
	st := jsonx.NewObject("lane", string(e.To), "actor", e.Actor.Identity(), "last_transition_at", e.At,
		"last_event_id", e.EventID, "force_count", force)
	if prev != nil {
		for _, s := range runtimeSlots {
			if v, ok := prev.Get(s); ok {
				st.Set(s, v)
			}
		}
	}
	for _, kv := range claimSidecarWrites(e) {
		st.Set(kv.Key, kv.Value)
	}
	if writesVerdict(e) {
		if e.ReviewResult != nil {
			st.Set("review_result", e.ReviewResult.ToObject())
		} else {
			st.Set("review_result", nil)
		}
	} else if prev != nil {
		if v, ok := prev.Get("review_result"); ok {
			st.Set("review_result", v)
		}
	}
	return st
}

func deltaScalar(d *Delta, name string) any {
	switch name {
	case "shell_pid":
		if d.ShellPID != nil {
			return *d.ShellPID
		}
		return nil
	}
	for _, s := range d.scalars() {
		if s.Name == name && *s.P != nil {
			return **s.P
		}
	}
	return nil
}

func applyDelta(st *jsonx.Object, d *Delta) {
	if d.ReleaseRuntimeClaim {
		for _, n := range []string{"agent", "shell_pid", "shell_pid_created_at"} {
			st.Set(n, nil)
		}
	}
	for _, n := range replaceSlots {
		v := deltaScalar(d, n)
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		st.Set(n, v)
	}
	if d.Subtasks != nil {
		cur := &jsonx.Object{}
		if o := st.GetObject("subtasks"); o != nil {
			cur = o.Clone()
		}
		for _, kv := range d.Subtasks.Pairs() {
			cur.Set(kv.Key, PyStr(kv.Value))
		}
		st.Set("subtasks", cur)
	}
	if d.Note != nil {
		notes := append([]any{}, st.GetList("notes")...)
		notes = append(notes, *d.Note)
		st.Set("notes", notes)
	}
	if d.TrackerRefsReplace != nil {
		seen := map[string]bool{}
		out := []any{}
		for _, r := range *d.TrackerRefsReplace {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		st.Set("tracker_refs", out)
	} else if d.TrackerRefs != nil {
		merged := append([]any{}, st.GetList("tracker_refs")...)
		for _, r := range *d.TrackerRefs {
			found := false
			for _, m := range merged {
				if m == r {
					found = true
					break
				}
			}
			if !found {
				merged = append(merged, r)
			}
		}
		st.Set("tracker_refs", merged)
	}
	if d.Review != nil {
		if d.Review.IsReleaseSentinel() {
			st.Set("review", nil)
		} else {
			st.Set("review", d.Review.ToObject())
		}
	}
}

type arbitration int

const (
	arbApply arbitration = iota
	arbLostToRollback
	arbLostOnContinuity
)

func findEvent(id any, all []*Event) *Event {
	s, ok := id.(string)
	if !ok {
		return nil
	}
	for _, e := range all {
		if e.EventID == s {
			return e
		}
	}
	return nil
}

func sameTimestampVerdict(ev, setter *Event, all []*Event) (arbitration, bool) {
	if setter == nil {
		return 0, false
	}
	if isRollback(ev) {
		if isRollback(setter) {
			return 0, false
		}
		if !isSeed(ev) || isBareSibling(setter, ev, all) {
			return arbApply, true
		}
		return 0, false
	}
	if isRollback(setter) && !yieldsToReal(setter, ev, all) {
		return arbLostToRollback, true
	}
	return 0, false
}

func continuityExempt(ev, setter *Event, all []*Event) bool {
	if isRollback(ev) {
		if !isSeed(ev) {
			return true
		}
		return isRollbackSeed(setter) || isBareSibling(setter, ev, all)
	}
	if isSeed(ev) {
		return false
	}
	if isSeed(setter) && !isRollback(setter) {
		return true
	}
	return yieldsToReal(setter, ev, all)
}

func arbitrate(cur *jsonx.Object, ev *Event, all []*Event) arbitration {
	if cur == nil {
		return arbApply
	}
	id, _ := cur.Get("last_event_id")
	ts, _ := cur.Get("last_transition_at")
	lane := cur.GetString("lane")
	setter := findEvent(id, all)
	if tss, ok := ts.(string); ok && tss == ev.At {
		if v, ok := sameTimestampVerdict(ev, setter, all); ok {
			return v
		}
	}
	if setter != nil && !ev.Force && string(ev.From) != lane && !continuityExempt(ev, setter, all) {
		return arbLostOnContinuity
	}
	return arbApply
}

type provenance struct{ real, seed map[string]bool }

func recordApplied(e *Event, st *jsonx.Object, p *provenance) {
	writes := claimSidecarWrites(e)
	wrote := map[string]bool{}
	for _, kv := range writes {
		wrote[kv.Key] = true
	}
	if writesVerdict(e) {
		wrote["review_result"] = true
	}
	if isSeed(e) && !isRollback(e) {
		for k := range wrote {
			p.seed[k] = true
		}
		return
	}
	if wrote["shell_pid"] && !wrote["shell_pid_created_at"] && p.seed["shell_pid_created_at"] {
		st.Delete("shell_pid_created_at")
		delete(p.seed, "shell_pid_created_at")
	}
	for k := range wrote {
		p.real[k] = true
		delete(p.seed, k)
	}
}

func foldLosingSeed(e *Event, st *jsonx.Object, p *provenance) {
	for _, kv := range claimSidecarWrites(e) {
		if p.real[kv.Key] {
			continue
		}
		if kv.Key == "shell_pid_created_at" && p.real["shell_pid"] {
			continue
		}
		st.Set(kv.Key, kv.Value)
		p.seed[kv.Key] = true
	}
	if e.ReviewResult != nil && !p.real["review_result"] {
		st.Set("review_result", e.ReviewResult.ToObject())
		p.seed["review_result"] = true
	}
}

func approvedDepartureSortAt(events []*Event) map[string]string {
	arrivals := map[string][]*Event{}
	departures := map[string][]*Event{}
	var depOrder []string
	for _, e := range events {
		if isSeed(e) {
			continue
		}
		if e.To == Approved && e.From != Approved {
			arrivals[e.WPID] = append(arrivals[e.WPID], e)
		} else if e.From == Approved && e.To != Approved {
			if _, ok := departures[e.WPID]; !ok {
				depOrder = append(depOrder, e.WPID)
			}
			departures[e.WPID] = append(departures[e.WPID], e)
		}
	}
	lifted := map[string]string{}
	for _, wpid := range depOrder {
		unbound := append([]*Event(nil), arrivals[wpid]...)
		sort.SliceStable(unbound, func(i, j int) bool { return keyLess(unbound[i], unbound[j]) })
		ordered := append([]*Event(nil), departures[wpid]...)
		sort.SliceStable(ordered, func(i, j int) bool { return keyLess(ordered[i], ordered[j]) })
		remove := func(x *Event) {
			for i, a := range unbound {
				if a == x {
					unbound = append(unbound[:i], unbound[i+1:]...)
					return
				}
			}
		}
		var skewed []*Event
		for _, d := range ordered {
			var last *Event
			for _, a := range unbound {
				if a.At <= d.At {
					last = a
				}
			}
			if last != nil {
				remove(last)
			} else {
				skewed = append(skewed, d)
			}
		}
		for _, d := range skewed {
			var first *Event
			for _, a := range unbound {
				if a.At > d.At {
					first = a
					break
				}
			}
			if first != nil {
				remove(first)
				lifted[d.EventID] = first.At
			}
		}
	}
	return lifted
}

func carryLiftedSortAt(events []*Event, lifted map[string]string) (map[string]string, map[string]int) {
	if len(lifted) == 0 {
		return map[string]string{}, map[string]int{}
	}
	eff := map[string]string{}
	for k, v := range lifted {
		eff[k] = v
	}
	seq := map[string]int{}
	floors := map[string]string{}
	sorted := append([]*Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool { return keyLess(sorted[i], sorted[j]) })
	for i, e := range sorted {
		pos := i + 1
		floor, has := floors[e.WPID]
		if l, ok := lifted[e.EventID]; ok {
			if l > floor {
				floors[e.WPID] = l
			} else {
				floors[e.WPID] = floor
			}
			seq[e.EventID] = pos
		} else if has && e.At < floor {
			eff[e.EventID] = floor
			seq[e.EventID] = pos
		}
	}
	return eff, seq
}

func orderTransitions(events []*Event) []*Event {
	approvals := map[string]bool{}
	for _, e := range events {
		if e.To == Approved && e.From != Approved && !isSeed(e) {
			approvals[e.WPID+"\x00"+e.At] = true
		}
	}
	eff, seq := carryLiftedSortAt(events, approvedDepartureSortAt(events))
	type key struct {
		at   string
		flag bool
		seq  int
		id   string
	}
	keys := map[*Event]key{}
	for _, e := range events {
		at := e.At
		if v, ok := eff[e.EventID]; ok {
			at = v
		}
		_, inSeq := seq[e.EventID]
		flag := inSeq || (e.From == Approved && !isSeed(e) && approvals[e.WPID+"\x00"+e.At])
		keys[e] = key{at, flag, seq[e.EventID], e.EventID}
	}
	out := append([]*Event(nil), events...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := keys[out[i]], keys[out[j]]
		if a.at != b.at {
			return a.at < b.at
		}
		if a.flag != b.flag {
			return !a.flag
		}
		if a.seq != b.seq {
			return a.seq < b.seq
		}
		return a.id < b.id
	})
	return out
}

func dedupTransitions(ts []*Event) []*Event {
	groups := map[string][]*Event{}
	var order []string
	for _, e := range ts {
		if _, ok := groups[e.EventID]; !ok {
			order = append(order, e.EventID)
		}
		groups[e.EventID] = append(groups[e.EventID], e)
	}
	var out []*Event
	for _, id := range order {
		rows := groups[id]
		allSeeds := len(rows) > 1
		for _, r := range rows {
			if !isSeed(r) {
				allSeeds = false
			}
		}
		if allSeeds {
			best := rows[0]
			bk := jsonx.Dumps(best.ToObject(), jsonx.Line)
			for _, r := range rows[1:] {
				if k := jsonx.Dumps(r.ToObject(), jsonx.Line); k < bk {
					best, bk = r, k
				}
			}
			out = append(out, best)
		} else {
			out = append(out, rows[0])
		}
	}
	return out
}

func emptySummary() *jsonx.Object {
	o := &jsonx.Object{}
	for _, l := range DisplayLanes {
		o.Set(string(l), 0)
	}
	return o
}

// Reduce folds transitions and annotations into a snapshot (reduce_parsed
// plus the CLI cancellation-provenance projection). Implementer attribution
// is applied when withImplementer is set.
func Reduce(transitions []*Event, annotations []*Annotation, withImplementer bool) *Snapshot {
	if len(transitions) == 0 && len(annotations) == 0 {
		return &Snapshot{WorkPackages: &jsonx.Object{}, Summary: emptySummary()}
	}
	unique := dedupTransitions(transitions)
	sorted := orderTransitions(unique)
	slug := ""
	if len(sorted) > 0 && mission.SafePathSegment(sorted[0].MissionSlug) {
		slug = sorted[0].MissionSlug
	}
	wps := &jsonx.Object{}
	prov := map[string]*provenance{}
	for _, e := range sorted {
		cur := wps.GetObject(e.WPID)
		p := prov[e.WPID]
		if p == nil {
			p = &provenance{real: map[string]bool{}, seed: map[string]bool{}}
			prov[e.WPID] = p
		}
		switch arbitrate(cur, e, sorted) {
		case arbApply:
			ns := wpStateFromEvent(e, cur)
			recordApplied(e, ns, p)
			wps.Set(e.WPID, ns)
		case arbLostOnContinuity:
			if cur != nil && isSeed(e) {
				foldLosingSeed(e, cur, p)
			}
		}
	}
	anns := append([]*Annotation(nil), annotations...)
	sort.SliceStable(anns, func(i, j int) bool {
		if anns[i].At != anns[j].At {
			return anns[i].At < anns[j].At
		}
		return anns[i].EventID < anns[j].EventID
	})
	for _, a := range anns {
		st := wps.GetObject(a.WPID)
		if st == nil {
			st = jsonx.NewObject("lane", string(Uninitialized), "actor", a.Actor.Identity(),
				"last_transition_at", nil, "last_event_id", nil, "force_count", 0)
			wps.Set(a.WPID, st)
		}
		applyDelta(st, &a.Delta)
	}
	summary := emptySummary()
	for _, kv := range wps.Pairs() {
		st := kv.Value.(*jsonx.Object)
		l := st.GetString("lane")
		if n, ok := summary.GetInt(l); ok {
			summary.Set(l, n+1)
		}
	}
	snap := &Snapshot{MissionSlug: slug, WorkPackages: wps, Summary: summary, EventCount: len(sorted)}
	if len(sorted) > 0 {
		last := sorted[len(sorted)-1]
		snap.MaterializedAt = last.At
		id := last.EventID
		snap.LastEventID = &id
	}
	projectCancellation(transitions, snap)
	if withImplementer {
		projectImplementer(transitions, snap)
	}
	return snap
}

func uniqueSorted(events []*Event) []*Event {
	s := append([]*Event(nil), events...)
	sort.SliceStable(s, func(i, j int) bool { return keyLess(s[i], s[j]) })
	seen := map[string]bool{}
	var out []*Event
	for _, e := range s {
		if seen[e.EventID] {
			continue
		}
		seen[e.EventID] = true
		out = append(out, e)
	}
	return out
}

func projectCancellation(events []*Event, snap *Snapshot) {
	for _, e := range uniqueSorted(events) {
		if e.To != Canceled {
			continue
		}
		st := snap.WP(e.WPID)
		if st == nil || st.GetString("lane") != string(e.To) {
			continue
		}
		st.Set("cancellation_reason", ptrVal(e.Reason))
		st.Set("reason_source", cancellationSource(e))
	}
}

func cancellationSource(e *Event) string {
	if e.ReasonSource != nil {
		return *e.ReasonSource
	}
	if e.Reason == nil || strings.TrimSpace(*e.Reason) == "" {
		return "synthetic"
	}
	if strings.HasPrefix(*e.Reason, "Force move to ") || strings.HasPrefix(*e.Reason, "move-task: ") {
		return "synthetic"
	}
	return "operator"
}

func projectImplementer(events []*Event, snap *Snapshot) {
	var claims []*Event
	for _, e := range uniqueSorted(events) {
		if e.From == Planned && e.To == Claimed && e.PolicyMetadata != nil {
			if v, ok := e.PolicyMetadata.Get("agent"); ok && truthyAny(v) {
				claims = append(claims, e)
			}
		}
	}
	latest := map[string]string{}
	for _, e := range claims {
		if !isSeed(e) && e.At > latest[e.WPID] {
			latest[e.WPID] = e.At
		}
	}
	for _, e := range claims {
		if isSeed(e) && latest[e.WPID] >= e.At {
			continue
		}
		st := snap.WP(e.WPID)
		if st == nil {
			continue
		}
		v, _ := e.PolicyMetadata.Get("agent")
		st.Set("implementer_of_record", PyStr(v))
	}
}

// CurrentSchemaVersion is the snapshot generation a new mission gets.
const CurrentSchemaVersion = 2

func targetSchemaVersion(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, SnapshotFile))
	if err != nil {
		if os.IsNotExist(err) {
			return CurrentSchemaVersion
		}
		return 1
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return 1
	}
	if v, ok := o.Get("schema_version"); ok {
		if n, ok := v.(int); ok {
			return n
		}
	}
	return 1
}

// ReduceDir reads <dir>/status.events.jsonl and reduces it (no identity /
// schema handling): the in-memory view used by readers.
func ReduceDir(dir string) (*Snapshot, error) {
	st, err := ReadStream(dir, "")
	if err != nil {
		return nil, err
	}
	return Reduce(st.Transitions, st.Annotations, true), nil
}

// MaterializeSnapshot computes exactly the snapshot Materialize writes.
func MaterializeSnapshot(dir string) (*Snapshot, error) {
	// materialize_snapshot reads the CLI stream first (store errors), then
	// reduces the raw rows through the shared diary parser.
	if _, err := ReadStream(dir, ""); err != nil {
		return nil, err
	}
	rows, err := ReadRows(dir)
	if err != nil {
		return nil, err
	}
	st, err := ParseDiary(rows)
	if err != nil {
		return nil, err
	}
	target := targetSchemaVersion(dir)
	snap := Reduce(st.Transitions, st.Annotations, target >= 2)
	if target > 1 {
		snap.SchemaVersion = target
	}
	meta := mission.ReadMetaOrEmpty(dir)
	if n, ok := mission.MissionNumber(meta); ok {
		s := itoa(n)
		snap.MissionNumber = &s
	}
	mt := mission.MissionType(meta)
	snap.MissionType = &mt
	snap.Retrospective = reduceRetrospective(rows)
	return snap, nil
}

// Materialize reduces dir's event log and writes status.json when changed.
func Materialize(dir string) (*Snapshot, error) {
	snap, err := MaterializeSnapshot(dir)
	if err != nil {
		return nil, err
	}
	text := snap.Render()
	out := filepath.Join(dir, SnapshotFile)
	if b, err := os.ReadFile(out); err == nil && string(b) == text {
		return snap, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, err
	}
	return snap, nil
}

func reduceRetrospective(rows []*jsonx.Object) *jsonx.Object {
	var retro []*jsonx.Object
	for _, r := range rows {
		if strings.HasPrefix(PyStr(valueOr(r, "event_name", "")), "retrospective.") {
			retro = append(retro, r)
		}
	}
	if len(retro) == 0 {
		return nil
	}
	sorted := append([]*jsonx.Object(nil), retro...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := PyStr(valueOr(sorted[i], "at", "")), PyStr(valueOr(sorted[j], "at", ""))
		if ai != aj {
			return ai < aj
		}
		return PyStr(valueOr(sorted[i], "event_id", "")) < PyStr(valueOr(sorted[j], "event_id", ""))
	})
	var mode any
	for _, r := range sorted {
		if r.GetString("event_name") == "retrospective.requested" {
			mode = nil
			if p := r.GetObject("payload"); p != nil {
				if m, ok := p.Get("mode"); ok {
					mode = m
				}
			}
		}
	}
	status := "pending"
	var recordPath any
	for _, r := range sorted {
		switch r.GetString("event_name") {
		case "retrospective.completed", "retrospective.skipped", "retrospective.failed":
			status = strings.TrimPrefix(r.GetString("event_name"), "retrospective.")
			recordPath = nil
			if p := r.GetObject("payload"); p != nil {
				if s, ok := p.Get("record_path"); ok {
					if ss, ok := s.(string); ok {
						recordPath = ss
					}
				}
			}
		}
	}
	count := func(name string) int {
		n := 0
		for _, r := range retro {
			if r.GetString("event_name") == name {
				n++
			}
		}
		return n
	}
	total, applied, rejected := count("retrospective.proposal.generated"), count("retrospective.proposal.applied"), count("retrospective.proposal.rejected")
	pending := total - applied - rejected
	if pending < 0 {
		pending = 0
	}
	return jsonx.NewObject("status", status, "mode", mode, "record_path", recordPath,
		"proposals_total", total, "proposals_applied", applied, "proposals_rejected", rejected, "proposals_pending", pending)
}

func valueOr(o *jsonx.Object, k string, def any) any {
	if v, ok := o.Get(k); ok && v != nil {
		return v
	}
	return def
}
