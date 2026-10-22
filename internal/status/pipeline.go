package status

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// TransitionError is status.emit.TransitionError (a refused transition).
type TransitionError struct{ Msg string }

func (e *TransitionError) Error() string { return e.Msg }

// Force-note refusal fragments (status/models.py).
const (
	ForceNoteHint     = "`--force --note <why>`"
	ForceNoteRequired = "--force requires a non-blank --note explaining why the review lane is being left"
)

// TransitionRequest is the status TransitionRequest value object.
type TransitionRequest struct {
	WPID                   string
	ToLane                 string
	Actor                  Actor
	Force                  bool
	Reason                 *string
	ReasonSource           *string
	Evidence               *DoneEvidence
	ReviewRef              *string
	WorkspaceContext       *string // nil: "<execution_mode>:<repo_root|feature_dir>"
	SubtasksComplete       *bool
	ImplementationEvidence *bool
	ExecutionMode          string // "" = "worktree"
	RepoRoot               string
	PolicyMetadata         *jsonx.Object
	ReviewResult           *ReviewResult
	AnnotationDelta        *Delta
	CurrentActor           string
}

func (r *TransitionRequest) execMode() string {
	if r.ExecutionMode == "" {
		return "worktree"
	}
	return r.ExecutionMode
}

// Prepared is PreparedTransition: Event nil marks the alias-collapse no-op.
type Prepared struct {
	Event        *Event
	ResolvedLane string
	Annotation   *Annotation
}

// PrepareInput carries the shell-resolved facts for Prepare.
type PrepareInput struct {
	WriteDir    string  // status write surface (event log dir)
	PlanningDir string  // primary mission dir (WP files)
	MissionSlug string  // stamped on the event
	MissionID   *string // canonical ULID (nil for legacy)
	FromLane    string
	// DependencyReady is the in-lock dependency verdict (nil: none supplied).
	DependencyReady *bool
	// LaneHead is the lane-branch head stamp ("" = none).
	LaneHead string
	At       string // "" = now
}

// DeriveFromLane returns the WP's current lane on snap (genesis when absent).
func DeriveFromLane(snap *Snapshot, wpID string) string {
	st := snap.WP(wpID)
	if st == nil {
		return string(Genesis)
	}
	if l := st.GetString("lane"); l != "" {
		return l
	}
	return string(Genesis)
}

// IsSubtaskTerminal reports done/skipped subtask values.
func IsSubtaskTerminal(v string) bool { return v == "done" || v == "skipped" }

// UncheckedSubtasks returns roster ids whose snapshot status is not terminal.
func UncheckedSubtasks(snap *Snapshot, wpID string, roster []string) []string {
	var sub *jsonx.Object
	if st := snap.WP(wpID); st != nil {
		sub = st.GetObject("subtasks")
	}
	out := []string{}
	for _, id := range roster {
		v := ""
		if sub != nil {
			if x, ok := sub.Get(id); ok {
				v = PyStr(x)
			}
		}
		if !IsSubtaskTerminal(v) {
			out = append(out, id)
		}
	}
	return out
}

// DependencyReadiness resolves the dependency verdict for wpID: declared
// dependencies (primary WP file) against snap. A dependency is satisfied
// at approved/done or an operator-provenance cancellation. Unreadable
// declarations yield an unsatisfied verdict.
func DependencyReadiness(planningDir, wpID string, snap *Snapshot) bool {
	matches := wp.Match(planningDir, wpID)
	if len(matches) > 1 {
		return false
	}
	if len(matches) == 0 {
		return true
	}
	w, err := wp.Read(matches[0])
	if err != nil {
		return false
	}
	for _, dep := range w.Dependencies() {
		lane := string(Planned)
		st := snap.WP(dep)
		if st != nil {
			if l := st.GetString("lane"); l != "" {
				lane = l
			}
		}
		lane = ResolveLaneAlias(lane)
		switch lane {
		case string(Approved), string(Done):
			continue
		case string(Canceled):
			if st != nil && st.GetString("reason_source") == "operator" {
				continue
			}
		}
		return false
	}
	return true
}

func inferReviewGates(req *TransitionRequest, in PrepareInput, resolved string, stream *Stream) (sub, impl *bool) {
	sub, impl = req.SubtasksComplete, req.ImplementationEvidence
	handoff := in.FromLane == string(InProgress) && resolved == string(ForReview)
	if !req.Force && handoff {
		roster, err := wp.AuthoredRoster(in.PlanningDir, req.WPID)
		switch {
		case err != nil:
			sub = Bool(false)
		case len(roster) == 0:
			sub = Bool(true)
		default:
			// unchecked_subtask_ids_from_snapshot reads the log.
			ExtraReadPasses(in.WriteDir, 1)
			snap := Reduce(stream.Transitions, stream.Annotations, true)
			sub = Bool(len(UncheckedSubtasks(snap, req.WPID, roster)) == 0)
		}
	}
	if impl == nil && handoff {
		// _infer_implementation_evidence reads the log.
		ExtraReadPasses(in.WriteDir, 1)
		found := false
		for _, e := range stream.Transitions {
			if e.WPID == req.WPID {
				found = true
				break
			}
		}
		impl = Bool(found)
	}
	return
}

// Prepare is prepare_transition: validate req against in.FromLane and build
// the event (+ claim annotation). Pure apart from reading the WP file and
// the event log for the review-gate inference.
func Prepare(req *TransitionRequest, in PrepareInput) (*Prepared, error) {
	if req.WPID == "" || req.ToLane == "" {
		return nil, fmt.Errorf("Each status transition requires wp_id, to_lane, and actor")
	}
	raw := strings.ToLower(strings.TrimSpace(req.ToLane))
	resolved := ResolveLaneAlias(req.ToLane)
	wsCtx := ""
	if req.WorkspaceContext != nil {
		wsCtx = *req.WorkspaceContext
	} else {
		root := req.RepoRoot
		if root == "" {
			root = in.WriteDir
		}
		wsCtx = req.execMode() + ":" + root
	}
	// prepare_transition reads the log only inside the review-gate
	// inference (which logs its own passes).
	var stream *Stream
	var err error
	Quietly(func() { stream, err = ReadStream(in.WriteDir, "") })
	if err != nil {
		return nil, err
	}
	sub, impl := inferReviewGates(req, in, resolved, stream)
	if raw != resolved && resolved == in.FromLane {
		return &Prepared{ResolvedLane: resolved}, nil
	}
	if req.Force && (in.FromLane == string(InReview) || in.FromLane == string(Approved)) && req.ReviewResult == nil &&
		!strings.HasPrefix(req.Actor.Identity(), MigrationActorPrefix) && !nonBlank(req.Reason) {
		return nil, &TransitionError{Msg: fmt.Sprintf("%s cannot leave %s by force without a note. %s; as an operator pass %s (`--reason <why>` on `agent status emit`).",
			req.WPID, in.FromLane, ForceNoteRequired, ForceNoteHint)}
	}
	if req.Evidence != nil {
		ev := req.Evidence
		if ev.Review.Reviewer == "" || ev.Review.Verdict == "" || strings.TrimSpace(ev.Review.Reference) == "" {
			return nil, &TransitionError{Msg: "Moving to done requires evidence with review.reviewer review.verdict, and review.reference"}
		}
	}
	ctx := GuardContext{
		Force: req.Force, Actor: req.Actor.Identity(), WorkspaceContext: wsCtx,
		SubtasksComplete: sub, ImplementationEvidence: impl, Reason: req.Reason, ReviewRef: req.ReviewRef,
		Evidence: req.Evidence, ReviewResult: req.ReviewResult, DependencyReady: in.DependencyReady,
	}
	if ok, msg := ValidateTransition(in.FromLane, resolved, ctx); !ok {
		return nil, &TransitionError{Msg: msg}
	}
	at := in.At
	if at == "" {
		at = clock.NowUTCISO()
	}
	pm := req.PolicyMetadata
	if in.LaneHead != "" {
		if pm == nil {
			pm = &jsonx.Object{}
		} else {
			pm = pm.Clone()
		}
		pm.Set("lane_head", in.LaneHead)
	}
	ev := &Event{
		EventID: ulid.New(), MissionSlug: in.MissionSlug, WPID: req.WPID,
		From: Lane(in.FromLane), To: Lane(resolved), At: at, Actor: req.Actor, Force: req.Force,
		ExecutionMode: req.execMode(), Reason: req.Reason, ReasonSource: req.ReasonSource,
		ReviewRef: req.ReviewRef, Evidence: req.Evidence, ReviewResult: req.ReviewResult,
		PolicyMetadata: pm, MissionID: in.MissionID,
	}
	p := &Prepared{Event: ev, ResolvedLane: resolved}
	if req.AnnotationDelta != nil {
		d := *req.AnnotationDelta
		d.Normalize()
		if !d.IsEmpty() {
			p.Annotation = &Annotation{EventID: ulid.New(), WPID: req.WPID, At: at, Actor: req.Actor, Delta: d}
		}
	}
	return p, nil
}

// MissionIDOf returns meta.json's mission_id in dir (nil for legacy).
func MissionIDOf(dir string) *string {
	meta := mission.ReadMetaOrEmpty(dir)
	if s := meta.GetString("mission_id"); s != "" {
		return &s
	}
	return nil
}

// NewAnnotation builds an InnerStateChanged row (annotate()).
func NewAnnotation(wpID string, delta Delta, actor Actor, at string) (*Annotation, error) {
	if !WPIDPattern.MatchString(wpID) {
		return nil, fmt.Errorf("annotate() refuses wp_id not matching the WP-id pattern: %s", PyRepr(wpID))
	}
	delta.Normalize()
	if delta.IsEmpty() {
		return nil, fmt.Errorf("annotate() refuses an empty delta (it folds to a no-op)")
	}
	if at == "" {
		at = clock.NowUTCISO()
	}
	return &Annotation{EventID: ulid.New(), WPID: wpID, At: at, Actor: actor, Delta: delta}, nil
}

// PyRepr renders a Python repr() of a string.
func PyRepr(s string) string { return mission.PyRepr(s) }
