package coord

// Area F extension (additive): the batch transactional door and the
// uncommitted annotation append used by the runtime-loop claims.

import (
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// EmitTransitionBatch is emit_status_transition_batch_transactional: every
// request (same WP) runs the status pipeline in order with the from-lane
// chained in memory, all rows are appended at once, the snapshot is
// materialized and (transactional missions) committed as
// "chore(spec-kitty): status transition batch WPnn".
func (s *Surface) EmitTransitionBatch(reqs []*status.TransitionRequest) ([]*status.Event, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	transactional := s.Transactional()
	var dir, checkout string
	var err error
	if transactional {
		dir, checkout, err = s.statusCommitTarget()
	} else {
		dir, err = s.WriteDir()
	}
	if err != nil {
		return nil, err
	}
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return nil, err
	}
	first := reqs[0]
	snap := status.Reduce(stream.Transitions, stream.Annotations, true)
	from := status.DeriveFromLane(snap, first.WPID)
	ready := status.DependencyReadiness(s.PrimaryDir, first.WPID, snap)
	head := LaneHead(s.Root, s.PrimaryDir, first.WPID)
	var items []any
	var events []*status.Event
	for _, req := range reqs {
		if req.RepoRoot == "" {
			req.RepoRoot = s.Root
		}
		p, err := status.Prepare(req, status.PrepareInput{
			WriteDir: dir, PlanningDir: s.PrimaryDir, MissionSlug: s.Slug, MissionID: status.MissionIDOf(s.PrimaryDir),
			FromLane: from, DependencyReady: &ready, LaneHead: head,
		})
		if err != nil {
			return nil, err
		}
		if p.Event == nil {
			continue
		}
		items = append(items, p.Event)
		if p.Annotation != nil {
			items = append(items, p.Annotation)
		}
		events = append(events, p.Event)
		from = string(p.Event.To)
	}
	if len(items) == 0 {
		return events, nil
	}
	if err := status.AppendEvents(dir, items...); err != nil {
		return nil, err
	}
	status.Materialize(dir)
	if transactional {
		if err := s.commitStatusFiles(checkout, dir, "chore(spec-kitty): status transition batch "+first.WPID); err != nil {
			return events, err
		}
	}
	status.RefreshDerivedViews(dir, s.Root)
	return events, nil
}

// AppendAnnotationUncommitted is emit_inner_state_changed without a
// commit: the annotation is appended to the status write surface and the
// snapshot re-materialized (a later workflow commit carries it).
func (s *Surface) AppendAnnotationUncommitted(wpID string, delta status.Delta, actor status.Actor) (*status.Annotation, string, error) {
	a, err := status.NewAnnotation(wpID, delta, actor, "")
	if err != nil {
		return nil, "", err
	}
	var dir string
	if s.Transactional() {
		dir, _, err = s.statusCommitTarget()
	} else {
		dir, err = s.WriteDir()
	}
	if err != nil {
		return nil, "", err
	}
	if err := status.AppendEvents(dir, a); err != nil {
		return nil, "", err
	}
	if _, merr := status.Materialize(dir); merr != nil {
		a.MaterializeFailed = true
	}
	return a, dir, nil
}
