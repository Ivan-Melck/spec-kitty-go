package coord

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Transactional reports whether status writes for this mission ride a
// commit-durable bookkeeping transaction (_transaction_topology_available):
// a git checkout whose mission declares a coordination branch or carries
// meta.json.
func (s *Surface) Transactional() bool {
	if !gitx.IsRepo(s.Root) {
		return false
	}
	if strings.TrimSpace(s.Meta.GetString("coordination_branch")) != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(s.PrimaryDir, mission.MetaFile))
	return err == nil
}

// annotationTransactional is _annotation_transaction_topology: coord
// missions, or stored lanes/single_branch topologies.
func (s *Surface) annotationTransactional() bool {
	if !gitx.IsRepo(s.Root) {
		return false
	}
	if strings.TrimSpace(s.Meta.GetString("coordination_branch")) != "" {
		return true
	}
	switch s.Meta.GetString("topology") {
	case Lanes, SingleBranch:
		return s.Transactional()
	}
	return false
}

// LaneHead is probe_lane_head: the lane branch head sha of wpID's execution
// lane ("" when there is no lanes.json, the WP is unassigned / on the
// planning lane, or the branch does not exist).
func LaneHead(root, primaryDir, wpID string) string {
	m, err := lanes.Read(primaryDir)
	if err != nil || m == nil {
		return ""
	}
	l := m.LaneForWP(wpID)
	if l == nil || l.LaneID == lanes.PlanningLaneID {
		return ""
	}
	branch := lanes.LaneBranchName(m.MissionSlug, l.LaneID, m.TargetBranch)
	sha, err := gitx.Run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sha)
}

// statusCommitTarget returns (write dir, commit checkout) for a
// transactional status write, materializing the coordination worktree.
func (s *Surface) statusCommitTarget() (string, string, error) {
	if s.Routed() {
		if err := s.EnsureWorktree(); err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(s.CoordDir, 0o755); err != nil {
			return "", "", err
		}
		return s.CoordDir, s.CoordWorktree, nil
	}
	return s.PrimaryDir, s.Root, nil
}

func (s *Surface) commitStatusFiles(checkout, dir, message string) error {
	paths := []string{status.EventsPath(dir)}
	if _, err := os.Stat(filepath.Join(dir, status.SnapshotFile)); err == nil {
		paths = append(paths, filepath.Join(dir, status.SnapshotFile))
	}
	_, err := CommitPaths(checkout, message, paths...)
	return err
}

// EmitResult is the outcome of a status write.
type EmitResult struct {
	Event      *status.Event
	Annotation *status.Annotation
	WriteDir   string
	Appended   bool // false for the alias-collapse no-op
}

// EmitTransition is the transactional status door
// (emit_status_transition_transactional): derive from_lane in the write
// surface, run the status pipeline, append event + claim annotation,
// materialize, commit ("chore(spec-kitty): status transition WPnn") and
// refresh the derived views. Non-transactional missions write uncommitted.
func (s *Surface) EmitTransition(req *status.TransitionRequest) (*EmitResult, error) {
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
	snap := status.Reduce(stream.Transitions, stream.Annotations, true)
	from := status.DeriveFromLane(snap, req.WPID)
	ready := status.DependencyReadiness(s.PrimaryDir, req.WPID, snap)
	if req.RepoRoot == "" {
		req.RepoRoot = s.Root
	}
	p, err := status.Prepare(req, status.PrepareInput{
		WriteDir: dir, PlanningDir: s.PrimaryDir, MissionSlug: s.Slug, MissionID: status.MissionIDOf(s.PrimaryDir),
		FromLane: from, DependencyReady: &ready, LaneHead: LaneHead(s.Root, s.PrimaryDir, req.WPID),
	})
	if err != nil {
		return nil, err
	}
	res := &EmitResult{WriteDir: dir}
	if p.Event == nil {
		res.Event = &status.Event{MissionSlug: s.Slug, WPID: req.WPID, From: status.Lane(from), To: status.Lane(from), Actor: req.Actor}
		return res, nil
	}
	items := []any{p.Event}
	if p.Annotation != nil {
		items = append(items, p.Annotation)
	}
	rb := txnPoint(transactional, dir)
	if err := status.AppendEvents(dir, items...); err != nil {
		return nil, err
	}
	status.Materialize(dir)
	res.Event, res.Annotation, res.Appended = p.Event, p.Annotation, true
	if transactional {
		if err := s.commitStatusTxn(checkout, dir, "chore(spec-kitty): status transition "+req.WPID, rb, itemEventIDs(items)); err != nil {
			return res, err
		}
	}
	status.RefreshDerivedViews(dir, s.Root)
	return res, nil
}

// EmitAnnotation is emit_runtime_annotation: with autoCommit on a
// coordination / stored lanes|single_branch mission the annotation is
// committed ("chore(spec-kitty): inner-state annotation WPnn") and the
// derived views refreshed; otherwise it is an uncommitted write.
func (s *Surface) EmitAnnotation(wpID string, delta status.Delta, actor status.Actor, autoCommit bool) (*status.Annotation, error) {
	return s.EmitAnnotationAt(wpID, delta, actor, autoCommit, "")
}

// EmitAnnotationAt is EmitAnnotation with an explicit event timestamp ("" =
// now), like emit_inner_state_changed_transactional(at=...).
func (s *Surface) EmitAnnotationAt(wpID string, delta status.Delta, actor status.Actor, autoCommit bool, at string) (*status.Annotation, error) {
	a, err := status.NewAnnotation(wpID, delta, actor, at)
	if err != nil {
		return nil, err
	}
	if autoCommit && s.annotationTransactional() {
		dir, checkout, err := s.statusCommitTarget()
		if err == nil {
			rb := txnPoint(true, dir)
			if err := status.AppendEvents(dir, a); err != nil {
				return nil, err
			}
			if _, merr := status.Materialize(dir); merr != nil {
				a.MaterializeFailed = true
			}
			if err := s.commitStatusTxn(checkout, dir, "chore(spec-kitty): inner-state annotation "+wpID, rb, []string{a.EventID}); err != nil {
				return a, err
			}
			status.RefreshDerivedViews(dir, s.Root)
			return a, nil
		}
	}
	dir, err := s.WriteDir()
	if err != nil {
		return nil, err
	}
	if err := status.AppendEvents(dir, a); err != nil {
		return nil, err
	}
	if _, merr := status.Materialize(dir); merr != nil {
		a.MaterializeFailed = true
	}
	return a, nil
}
