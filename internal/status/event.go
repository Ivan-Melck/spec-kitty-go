package status

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ULIDPattern validates event ids.
var ULIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// WPIDPattern validates work package ids on events.
var WPIDPattern = regexp.MustCompile(`^WP\d+$`)

// AnnotationKind is the wire discriminator of InnerStateChanged rows.
const AnnotationKind = "annotation"

// Binding is a structured resolved-binding actor {role, profile, tool, model}.
type Binding struct {
	Role, Profile, Tool, Model *string
}

// Actor is either a plain string identity or a structured Binding.
type Actor struct {
	Name    string
	Binding *Binding
}

// StrActor builds a plain string actor.
func StrActor(s string) Actor { return Actor{Name: s} }

// Identity projects the actor to its display identity (tool, then role).
func (a Actor) Identity() string {
	if a.Binding != nil {
		if a.Binding.Tool != nil && *a.Binding.Tool != "" {
			return *a.Binding.Tool
		}
		if a.Binding.Role != nil && *a.Binding.Role != "" {
			return *a.Binding.Role
		}
		return ""
	}
	return a.Name
}

// Value renders the wire value (string or object).
func (a Actor) Value() any {
	if a.Binding == nil {
		return a.Name
	}
	return jsonx.NewObject("role", ptrVal(a.Binding.Role), "profile", ptrVal(a.Binding.Profile),
		"tool", ptrVal(a.Binding.Tool), "model", ptrVal(a.Binding.Model))
}

// DecodeActor decodes a wire actor value.
func DecodeActor(v any) (Actor, error) {
	if o, ok := v.(*jsonx.Object); ok {
		keys := o.Keys()
		want := map[string]bool{"role": true, "profile": true, "tool": true, "model": true}
		if len(keys) != 4 {
			return Actor{}, fmt.Errorf("structured actor must contain exactly ['model', 'profile', 'role', 'tool']; got %s", pyList(sortedCopy(keys)))
		}
		for _, k := range keys {
			if !want[k] {
				return Actor{}, fmt.Errorf("structured actor must contain exactly ['model', 'profile', 'role', 'tool']; got %s", pyList(sortedCopy(keys)))
			}
		}
		b := &Binding{}
		for _, f := range []struct {
			k string
			p **string
		}{{"role", &b.Role}, {"profile", &b.Profile}, {"tool", &b.Tool}, {"model", &b.Model}} {
			x, _ := o.Get(f.k)
			switch t := x.(type) {
			case nil:
			case string:
				s := t
				*f.p = &s
			default:
				return Actor{}, fmt.Errorf("structured actor fields must be strings or null; '%s' was %T", f.k, x)
			}
		}
		return Actor{Binding: b}, nil
	}
	return Actor{Name: PyStr(v)}, nil
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func pyList(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += ", "
		}
		out += "'" + x + "'"
	}
	return out + "]"
}

// PyStr renders a decoded JSON scalar the way Python str() would.
func PyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return jsonx.FormatFloat(t)
	}
	return fmt.Sprint(v)
}

func ptrVal(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Str returns a pointer to s.
func Str(s string) *string { return &s }

// ReviewApproval is the evidence.review triple.
type ReviewApproval struct{ Reviewer, Verdict, Reference string }

// RepoEvidence is an evidence.repos entry.
type RepoEvidence struct {
	Repo, Branch, Commit string
	FilesTouched         []string
}

// VerificationResult is an evidence.verification entry.
type VerificationResult struct{ Command, Result, Summary string }

// DoneEvidence is the evidence payload.
type DoneEvidence struct {
	Review       ReviewApproval
	Repos        []RepoEvidence
	Verification []VerificationResult
}

// ToObject renders DoneEvidence.to_dict().
func (d *DoneEvidence) ToObject() *jsonx.Object {
	o := jsonx.NewObject("review", jsonx.NewObject("reviewer", d.Review.Reviewer, "verdict", d.Review.Verdict, "reference", d.Review.Reference))
	if len(d.Repos) > 0 {
		var l []any
		for _, r := range d.Repos {
			ro := jsonx.NewObject("repo", r.Repo, "branch", r.Branch, "commit", r.Commit)
			if len(r.FilesTouched) > 0 {
				ro.Set("files_touched", append([]string(nil), r.FilesTouched...))
			}
			l = append(l, ro)
		}
		o.Set("repos", l)
	}
	if len(d.Verification) > 0 {
		var l []any
		for _, v := range d.Verification {
			l = append(l, jsonx.NewObject("command", v.Command, "result", v.Result, "summary", v.Summary))
		}
		o.Set("verification", l)
	}
	return o
}

// ReviewResult is the structured review outcome.
type ReviewResult struct {
	Reviewer, Verdict, Reference string
	FeedbackPath                 *string
}

// ToObject renders ReviewResult.to_dict().
func (r *ReviewResult) ToObject() *jsonx.Object {
	o := jsonx.NewObject("reviewer", r.Reviewer, "verdict", r.Verdict, "reference", r.Reference)
	if r.FeedbackPath != nil {
		o.Set("feedback_path", *r.FeedbackPath)
	}
	return o
}

// Event is a lane transition row (StatusEvent).
type Event struct {
	EventID        string
	MissionSlug    string
	WPID           string
	From, To       Lane
	At             string
	Actor          Actor
	Force          bool
	ExecutionMode  string
	Reason         *string
	ReasonSource   *string
	ReviewRef      *string
	Evidence       *DoneEvidence
	ReviewResult   *ReviewResult
	PolicyMetadata *jsonx.Object
	MissionID      *string
}

// ToObject renders StatusEvent.to_dict() (insertion order; the line
// serializer sorts keys).
func (e *Event) ToObject() *jsonx.Object {
	o := jsonx.NewObject(
		"event_id", e.EventID,
		"mission_slug", e.MissionSlug,
		"wp_id", e.WPID,
		"from_lane", string(e.From),
		"to_lane", string(e.To),
		"at", e.At,
		"actor", e.Actor.Value(),
		"force", e.Force,
		"execution_mode", e.ExecutionMode,
		"reason", ptrVal(e.Reason),
		"review_ref", ptrVal(e.ReviewRef),
	)
	if e.Evidence != nil {
		o.Set("evidence", e.Evidence.ToObject())
	} else {
		o.Set("evidence", nil)
	}
	if e.PolicyMetadata != nil {
		o.Set("policy_metadata", e.PolicyMetadata)
	} else {
		o.Set("policy_metadata", nil)
	}
	if e.ReviewResult != nil {
		o.Set("review_result", e.ReviewResult.ToObject())
	}
	if e.ReasonSource != nil {
		o.Set("reason_source", *e.ReasonSource)
	}
	if e.MissionID != nil {
		o.Set("mission_id", *e.MissionID)
	}
	return o
}

// ReviewOverride is the delta.review slot.
type ReviewOverride struct{ At, Actor, WPID, Reason string }

// Complete reports all four fields non-empty.
func (r ReviewOverride) Complete() bool {
	return r.At != "" && r.Actor != "" && r.WPID != "" && r.Reason != ""
}

// IsReleaseSentinel reports all four fields empty.
func (r ReviewOverride) IsReleaseSentinel() bool {
	return r.At == "" && r.Actor == "" && r.WPID == "" && r.Reason == ""
}

// ToObject renders the override.
func (r ReviewOverride) ToObject() *jsonx.Object {
	return jsonx.NewObject("at", r.At, "actor", r.Actor, "wp_id", r.WPID, "reason", r.Reason)
}

// Delta is WPInnerStateDelta. Nil pointers / nil slices mean "absent".
type Delta struct {
	ShellPID            *int
	ShellPIDCreatedAt   *string
	Subtasks            *jsonx.Object // subtask id -> status string
	Note                *string
	TrackerRefs         *[]string
	TrackerRefsReplace  *[]string
	Agent               *string
	Assignee            *string
	Review              *ReviewOverride
	Role                *string
	AgentProfile        *string
	AgentProfileVersion *string
	Model               *string
	Provider            *string
	ReleaseRuntimeClaim bool
}

// ScalarFields of the delta in _SCALAR_FIELDS order.
func (d *Delta) scalars() []struct {
	Name string
	P    **string
} {
	return []struct {
		Name string
		P    **string
	}{
		{"shell_pid_created_at", &d.ShellPIDCreatedAt},
		{"agent", &d.Agent},
		{"assignee", &d.Assignee},
		{"role", &d.Role},
		{"agent_profile", &d.AgentProfile},
		{"agent_profile_version", &d.AgentProfileVersion},
		{"model", &d.Model},
		{"provider", &d.Provider},
	}
}

// Normalize maps "" scalars to absent (write-boundary normalization).
func (d *Delta) Normalize() {
	for _, s := range d.scalars() {
		if *s.P != nil && **s.P == "" {
			*s.P = nil
		}
	}
}

// IsEmpty reports a delta that touches no slot.
func (d *Delta) IsEmpty() bool {
	if d.ReleaseRuntimeClaim {
		return false
	}
	if d.ShellPID != nil || d.Subtasks != nil || d.Note != nil || d.TrackerRefs != nil || d.TrackerRefsReplace != nil || d.Review != nil {
		return false
	}
	for _, s := range d.scalars() {
		if *s.P != nil {
			return false
		}
	}
	return true
}

// ToObject renders WPInnerStateDelta.to_dict().
func (d *Delta) ToObject() *jsonx.Object {
	o := &jsonx.Object{}
	if d.ShellPID != nil {
		o.Set("shell_pid", *d.ShellPID)
	}
	if d.Subtasks != nil {
		o.Set("subtasks", d.Subtasks.Clone())
	}
	if d.Note != nil {
		o.Set("note", *d.Note)
	}
	if d.TrackerRefs != nil {
		o.Set("tracker_refs", append([]string{}, (*d.TrackerRefs)...))
	}
	if d.TrackerRefsReplace != nil {
		o.Set("tracker_refs_replace", append([]string{}, (*d.TrackerRefsReplace)...))
	}
	if d.Review != nil {
		o.Set("review", d.Review.ToObject())
	}
	for _, s := range d.scalars() {
		if *s.P != nil {
			o.Set(s.Name, **s.P)
		}
	}
	if d.ReleaseRuntimeClaim {
		o.Set("release_runtime_claim", true)
	}
	return o
}

// Annotation is an InnerStateChanged row.
type Annotation struct {
	EventID string
	WPID    string
	At      string
	Actor   Actor
	Delta   Delta
	// MaterializeFailed is set by emitters when the post-append snapshot
	// refresh failed (not serialized).
	MaterializeFailed bool
}

// ToObject renders InnerStateChanged.to_dict().
func (a *Annotation) ToObject() *jsonx.Object {
	return jsonx.NewObject("event_id", a.EventID, "kind", AnnotationKind, "wp_id", a.WPID, "at", a.At,
		"actor", a.Actor.Value(), "delta", a.Delta.ToObject())
}

// Stream is the partitioned event log.
type Stream struct {
	Transitions []*Event
	Annotations []*Annotation
}

// --- decoding -----------------------------------------------------------

// keyError mimics a Python KeyError message ('key').
type keyError string

func (k keyError) Error() string { return "'" + string(k) + "'" }

func reqKey(o *jsonx.Object, k string) (any, error) {
	v, ok := o.Get(k)
	if !ok {
		return nil, keyError(k)
	}
	return v, nil
}

func optStr(o *jsonx.Object, k string) *string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return nil
	}
	s := PyStr(v)
	return &s
}

func coerceLane(v any) (Lane, error) {
	s, _ := v.(string)
	if !IsLane(s) {
		return "", fmt.Errorf("%s is not a valid Lane", reprAny(v))
	}
	return Lane(s), nil
}

func reprAny(v any) string {
	if s, ok := v.(string); ok {
		return "'" + s + "'"
	}
	return PyStr(v)
}

// EventFromObject decodes a lane transition row (StatusEvent.from_dict).
func EventFromObject(o *jsonx.Object) (*Event, error) {
	e := &Event{}
	v, err := reqKey(o, "event_id")
	if err != nil {
		return nil, err
	}
	e.EventID = PyStr(v)
	if s := o.GetString("mission_slug"); s != "" {
		e.MissionSlug = s
	} else {
		e.MissionSlug = o.GetString("feature_slug")
	}
	if v, err = reqKey(o, "wp_id"); err != nil {
		return nil, err
	}
	e.WPID = PyStr(v)
	if v, err = reqKey(o, "from_lane"); err != nil {
		return nil, err
	}
	if e.From, err = coerceLane(v); err != nil {
		return nil, err
	}
	if v, err = reqKey(o, "to_lane"); err != nil {
		return nil, err
	}
	if e.To, err = coerceLane(v); err != nil {
		return nil, err
	}
	if v, err = reqKey(o, "at"); err != nil {
		return nil, err
	}
	e.At = PyStr(v)
	if v, err = reqKey(o, "actor"); err != nil {
		return nil, err
	}
	if e.Actor, err = DecodeActor(v); err != nil {
		return nil, err
	}
	if v, err = reqKey(o, "force"); err != nil {
		return nil, err
	}
	e.Force, _ = v.(bool)
	if v, err = reqKey(o, "execution_mode"); err != nil {
		return nil, err
	}
	e.ExecutionMode = PyStr(v)
	e.Reason = optStr(o, "reason")
	e.ReasonSource = optStr(o, "reason_source")
	e.ReviewRef = optStr(o, "review_ref")
	if ev := o.GetObject("evidence"); ev != nil && ev.Len() > 0 {
		d, err := evidenceFromObject(ev)
		if err != nil {
			return nil, err
		}
		e.Evidence = d
	}
	if rr := o.GetObject("review_result"); rr != nil && rr.Len() > 0 {
		r, err := reviewResultFromObject(rr)
		if err != nil {
			return nil, err
		}
		e.ReviewResult = r
	}
	e.PolicyMetadata = o.GetObject("policy_metadata")
	e.MissionID = optStr(o, "mission_id")
	return e, nil
}

func strField(o *jsonx.Object, k string) (string, error) {
	v, err := reqKey(o, k)
	if err != nil {
		return "", err
	}
	return PyStr(v), nil
}

func evidenceFromObject(o *jsonx.Object) (*DoneEvidence, error) {
	rv := o.GetObject("review")
	if rv == nil {
		return nil, keyError("review")
	}
	d := &DoneEvidence{}
	var err error
	if d.Review.Reviewer, err = strField(rv, "reviewer"); err != nil {
		return nil, err
	}
	if d.Review.Verdict, err = strField(rv, "verdict"); err != nil {
		return nil, err
	}
	if d.Review.Reference, err = strField(rv, "reference"); err != nil {
		return nil, err
	}
	for _, x := range o.GetList("repos") {
		ro, _ := x.(*jsonx.Object)
		if ro == nil {
			continue
		}
		r := RepoEvidence{}
		if r.Repo, err = strField(ro, "repo"); err != nil {
			return nil, err
		}
		if r.Branch, err = strField(ro, "branch"); err != nil {
			return nil, err
		}
		if r.Commit, err = strField(ro, "commit"); err != nil {
			return nil, err
		}
		for _, f := range ro.GetList("files_touched") {
			r.FilesTouched = append(r.FilesTouched, PyStr(f))
		}
		d.Repos = append(d.Repos, r)
	}
	for _, x := range o.GetList("verification") {
		vo, _ := x.(*jsonx.Object)
		if vo == nil {
			continue
		}
		v := VerificationResult{}
		if v.Command, err = strField(vo, "command"); err != nil {
			return nil, err
		}
		if v.Result, err = strField(vo, "result"); err != nil {
			return nil, err
		}
		if v.Summary, err = strField(vo, "summary"); err != nil {
			return nil, err
		}
		d.Verification = append(d.Verification, v)
	}
	return d, nil
}

func reviewResultFromObject(o *jsonx.Object) (*ReviewResult, error) {
	r := &ReviewResult{}
	var err error
	if r.Reviewer, err = strField(o, "reviewer"); err != nil {
		return nil, err
	}
	if r.Verdict, err = strField(o, "verdict"); err != nil {
		return nil, err
	}
	if r.Reference, err = strField(o, "reference"); err != nil {
		return nil, err
	}
	r.FeedbackPath = optStr(o, "feedback_path")
	return r, nil
}

// AnnotationFromObject decodes an InnerStateChanged row.
func AnnotationFromObject(o *jsonx.Object) (*Annotation, error) { return annotationFromObject(o, true) }

// DiaryAnnotationFromObject decodes with the shared diary rules (subtask values
// must be lanes; "skipped" is rejected).
func DiaryAnnotationFromObject(o *jsonx.Object) (*Annotation, error) {
	return annotationFromObject(o, false)
}

func annotationFromObject(o *jsonx.Object, allowSkipped bool) (*Annotation, error) {
	v, err := reqKey(o, "event_id")
	if err != nil {
		return nil, err
	}
	id, ok := v.(string)
	if !ok || !ULIDPattern.MatchString(id) {
		return nil, fmt.Errorf("InnerStateChanged.event_id is not a valid ULID: %s", reprAny(v))
	}
	a := &Annotation{EventID: id}
	if v, err = reqKey(o, "wp_id"); err != nil {
		return nil, err
	}
	a.WPID = PyStr(v)
	if v, err = reqKey(o, "at"); err != nil {
		return nil, err
	}
	a.At = PyStr(v)
	if v, err = reqKey(o, "actor"); err != nil {
		return nil, err
	}
	if a.Actor, err = DecodeActor(v); err != nil {
		return nil, err
	}
	dv, err := reqKey(o, "delta")
	if err != nil {
		return nil, err
	}
	do, _ := dv.(*jsonx.Object)
	if do == nil {
		do = &jsonx.Object{}
	}
	d, err := deltaFromObject(do, allowSkipped)
	if err != nil {
		return nil, err
	}
	a.Delta = *d
	return a, nil
}

func deltaFromObject(o *jsonx.Object, allowSkipped bool) (*Delta, error) {
	d := &Delta{}
	if st := o.GetObject("subtasks"); st != nil {
		sub := &jsonx.Object{}
		for _, kv := range st.Pairs() {
			s := PyStr(kv.Value)
			if !IsLane(s) && !(allowSkipped && s == "skipped") {
				return nil, fmt.Errorf("%s is not a valid Lane", reprAny(kv.Value))
			}
			sub.Set(kv.Key, s)
		}
		d.Subtasks = sub
	} else if v, ok := o.Get("subtasks"); ok && v != nil {
		return nil, fmt.Errorf("'%T' object has no attribute 'items'", v)
	}
	if rv := o.GetObject("review"); rv != nil {
		r := ReviewOverride{}
		var err error
		if r.At, err = strField(rv, "at"); err != nil {
			return nil, err
		}
		if r.Actor, err = strField(rv, "actor"); err != nil {
			return nil, err
		}
		if r.WPID, err = strField(rv, "wp_id"); err != nil {
			return nil, err
		}
		if r.Reason, err = strField(rv, "reason"); err != nil {
			return nil, err
		}
		d.Review = &r
	}
	if v, ok := o.Get("shell_pid"); ok && v != nil {
		var n int
		switch t := v.(type) {
		case int:
			n = t
		case float64:
			n = int(t)
		case string:
			x, err := strconv.Atoi(t)
			if err != nil {
				return nil, fmt.Errorf("invalid literal for int() with base 10: '%s'", t)
			}
			n = x
		case bool:
			if t {
				n = 1
			}
		default:
			return nil, fmt.Errorf("int() argument must be a string, a bytes-like object or a real number, not '%T'", v)
		}
		d.ShellPID = &n
	}
	d.Note = optStr(o, "note")
	if l, ok := o.Get("tracker_refs"); ok && l != nil {
		refs := strList(l)
		d.TrackerRefs = &refs
	}
	if l, ok := o.Get("tracker_refs_replace"); ok && l != nil {
		refs := strList(l)
		d.TrackerRefsReplace = &refs
	}
	for _, s := range d.scalars() {
		*s.P = optStr(o, s.Name)
	}
	d.Normalize()
	if v, ok := o.Get("release_runtime_claim"); ok {
		d.ReleaseRuntimeClaim = truthyAny(v)
	}
	return d, nil
}

func strList(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, PyStr(x))
		}
	}
	return out
}

func truthyAny(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}
