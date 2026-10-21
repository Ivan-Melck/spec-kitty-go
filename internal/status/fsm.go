package status

import (
	"sort"
	"strings"
)

// Guard / force parity messages (status/wp_state.py).
const (
	msgForceRequiresActorReason = "Force transitions require actor and reason"
	msgReviewerApprovalRequired = "Transition to approved/done requires evidence (reviewer identity and approval reference)"
)

// GuardContext carries the facts the lane FSM guards consult (GuardContext /
// TransitionContext). Nil pointers mean "not supplied" (Python None).
type GuardContext struct {
	Force                  bool
	Actor                  string
	WorkspaceContext       string
	SubtasksComplete       *bool
	ImplementationEvidence *bool
	Reason                 *string
	ReviewRef              *string
	Evidence               *DoneEvidence
	ReviewResult           *ReviewResult
	DependencyReady        *bool
}

// Bool returns a pointer to b.
func Bool(b bool) *bool { return &b }

var allowedTargets = map[Lane][]Lane{
	Genesis:       {Planned, Canceled},
	Uninitialized: {},
	Planned:       {Claimed, Blocked, Canceled},
	Claimed:       {InProgress, Blocked, Canceled},
	InProgress:    {ForReview, Approved, Planned, Blocked, Canceled},
	ForReview:     {InReview, Blocked, Canceled},
	InReview:      {Approved, Done, InProgress, Planned, Blocked, Canceled},
	Approved:      {Done, InProgress, Planned, Blocked, Canceled},
	Done:          {},
	Blocked:       {InProgress, Canceled},
	Canceled:      {},
}

// AllowedTargets returns the structural edges out of l, sorted by value.
func AllowedTargets(l Lane) []Lane {
	out := append([]Lane(nil), allowedTargets[l]...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MayTransition reports a structural edge from -> to.
func MayTransition(from, to Lane) bool {
	for _, t := range allowedTargets[from] {
		if t == to {
			return true
		}
	}
	return false
}

// IsBlockedLane reports the blocked lane.
func IsBlockedLane(l Lane) bool { return l == Blocked }

// ProgressBucket is WPState.progress_bucket().
func ProgressBucket(l Lane) string {
	switch l {
	case Claimed, InProgress, Blocked:
		return "in_flight"
	case ForReview, InReview, Approved:
		return "review"
	case Done, Canceled:
		return "terminal"
	}
	return "not_started"
}

// DisplayCategory is WPState.display_category().
func DisplayCategory(l Lane) string {
	switch l {
	case Claimed, InProgress, InReview:
		return "In Progress"
	case ForReview:
		return "Review"
	case Approved:
		return "Approved"
	case Done:
		return "Done"
	case Blocked:
		return "Blocked"
	case Canceled:
		return "Canceled"
	}
	return "Planned"
}

func nonBlank(p *string) bool { return p != nil && strings.TrimSpace(*p) != "" }

func checkReviewerApproval(ctx GuardContext) (bool, string) {
	ev := ctx.Evidence
	if ev == nil || strings.TrimSpace(ev.Review.Reviewer) == "" || strings.TrimSpace(ev.Review.Reference) == "" || ev.Review.Verdict != "approved" {
		return false, msgReviewerApprovalRequired
	}
	return true, ""
}

func guardFor(from, to Lane, ctx GuardContext) (bool, string) {
	hasActor := strings.TrimSpace(ctx.Actor) != ""
	switch from {
	case Planned:
		if to == Claimed && !hasActor {
			return false, "Transition requires actor identity"
		}
		if to == Claimed && ctx.DependencyReady != nil && !*ctx.DependencyReady {
			return false, "Transition planned -> claimed blocked: unsatisfied dependencies (force with reason to override)"
		}
	case Claimed:
		if to == InProgress && strings.TrimSpace(ctx.WorkspaceContext) == "" {
			return false, "Transition claimed -> in_progress requires workspace context"
		}
		if to == InProgress && ctx.DependencyReady != nil && !*ctx.DependencyReady {
			return false, "Transition claimed -> in_progress blocked: unsatisfied dependencies (force with reason to override)"
		}
	case InProgress:
		switch to {
		case ForReview:
			if ctx.SubtasksComplete == nil || !*ctx.SubtasksComplete {
				return false, "Transition in_progress -> for_review requires completed subtasks or force with reason"
			}
			if ctx.ImplementationEvidence == nil || !*ctx.ImplementationEvidence {
				return false, "Transition in_progress -> for_review requires implementation evidence or force with reason"
			}
		case Approved:
			return checkReviewerApproval(ctx)
		case Planned:
			if !nonBlank(ctx.Reason) {
				return false, "Transition in_progress -> planned requires reason"
			}
		}
	case ForReview:
		if to == InReview && !hasActor {
			return false, "Transition requires actor identity"
		}
	case InReview:
		rr := ctx.ReviewResult
		if rr == nil {
			return false, "Transition from in_review requires review_result (structured review outcome)"
		}
		if strings.TrimSpace(rr.Reviewer) == "" {
			return false, "Transition from in_review requires review_result with reviewer"
		}
		if strings.TrimSpace(rr.Verdict) == "" {
			return false, "Transition from in_review requires review_result with verdict"
		}
		if strings.TrimSpace(rr.Reference) == "" {
			return false, "Transition from in_review requires review_result with reference"
		}
		if to == Approved || to == Done {
			if ctx.Evidence != nil {
				if ok, msg := checkReviewerApproval(ctx); !ok {
					return ok, msg
				}
			} else if !(rr.Reviewer != "" && rr.Verdict == "approved" && rr.Reference != "") {
				return false, msgReviewerApprovalRequired
			}
		}
		if ctx.ReviewRef != nil && *ctx.ReviewRef != rr.Reference {
			return false, "review_ref must match review_result.reference"
		}
		if ev := ctx.Evidence; ev != nil {
			if ev.Review.Reviewer != rr.Reviewer || ev.Review.Verdict != rr.Verdict || ev.Review.Reference != rr.Reference {
				return false, "Review evidence must match review_result"
			}
		}
	case Approved:
		if to == Done {
			return checkReviewerApproval(ctx)
		}
		if (to == InProgress || to == Planned) && !nonBlank(ctx.ReviewRef) {
			return false, "Transition requires review_ref (review feedback reference)"
		}
	}
	return true, ""
}

func checkForce(ctx GuardContext) (bool, string) {
	if strings.TrimSpace(ctx.Actor) == "" || !nonBlank(ctx.Reason) {
		return false, msgForceRequiresActorReason
	}
	return true, ""
}

// CheckTransition is WPState.check_transition: edge + guard + force.
func CheckTransition(from, to Lane, ctx GuardContext) (bool, string) {
	illegal := "Illegal transition: " + string(from) + " -> " + string(to)
	if to == Genesis {
		return false, illegal
	}
	if !MayTransition(from, to) {
		if ctx.Force {
			return checkForce(ctx)
		}
		return false, illegal
	}
	if ctx.Force {
		return checkForce(ctx)
	}
	return guardFor(from, to, ctx)
}

// ResolveLaneAlias is resolve_lane_alias: trim/lower, "doing" -> in_progress.
func ResolveLaneAlias(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if a, ok := LaneAliases[v]; ok {
		return string(a)
	}
	return v
}

// ValidateTransition is validate_transition (alias resolution + FSM).
func ValidateTransition(from, to string, ctx GuardContext) (bool, string) {
	f, t := ResolveLaneAlias(from), ResolveLaneAlias(to)
	if !IsLane(f) {
		return false, "Unknown lane: " + from
	}
	if !IsLane(t) {
		return false, "Unknown lane: " + to
	}
	ok, msg := CheckTransition(Lane(f), Lane(t), ctx)
	if !ok && msg == "" {
		msg = "Illegal transition: " + f + " -> " + t
	}
	return ok, msg
}

// IsBackwardTransition reports target preceding current on the forward axis.
func IsBackwardTransition(current, target string) bool {
	c, t := ForwardIndex(Lane(ResolveLaneAlias(current))), ForwardIndex(Lane(ResolveLaneAlias(target)))
	return c >= 0 && t >= 0 && t < c
}

// LaneTargetsForEmit returns the forward intermediate hops current -> target
// (just [target] for non-forward moves).
func LaneTargetsForEmit(current, requested string) []string {
	c, t := ResolveLaneAlias(current), ResolveLaneAlias(requested)
	ci, ti := ForwardIndex(Lane(c)), ForwardIndex(Lane(t))
	if ci >= 0 && ti > ci {
		var out []string
		for _, l := range ForwardOrder[ci+1 : ti+1] {
			out = append(out, string(l))
		}
		return out
	}
	return []string{t}
}

// CLILanes is task_utils LANES (the move-task --to vocabulary).
var CLILanes = []string{"planned", "claimed", "in_progress", "for_review", "in_review", "approved", "done", "blocked", "canceled"}

// EnsureLane is task_utils.ensure_lane: alias-resolve and validate a CLI lane.
func EnsureLane(value string) (string, error) {
	l := ResolveLaneAlias(value)
	for _, x := range CLILanes {
		if x == l {
			return l, nil
		}
	}
	return "", &StoreError{Msg: "Invalid lane '" + value + "'. Expected one of " + strings.Join(CLILanes, ", ") + "."}
}
