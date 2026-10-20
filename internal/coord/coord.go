// Package coord resolves a mission's coordination topology: where the
// status surface (status.events.jsonl, status.json, review cycles, ...) lives
// and how bookkeeping commits are made (coordination branch/worktree vs. the
// primary checkout).
package coord

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Topologies.
const (
	SingleBranch   = "single_branch"
	Lanes          = "lanes"
	Coord          = "coord"
	LanesWithCoord = "lanes_with_coord"
)

// RoutesThroughCoordination reports coord / lanes_with_coord.
func RoutesThroughCoordination(topology string) bool {
	return topology == Coord || topology == LanesWithCoord
}

// TopologyFromMeta returns the stored topology, or classifies a legacy meta
// from coordination_branch + lanes.json presence.
func TopologyFromMeta(meta *jsonx.Object, missionDir string) string {
	switch t := meta.GetString("topology"); t {
	case SingleBranch, Lanes, Coord, LanesWithCoord:
		return t
	}
	hasCoord := strings.TrimSpace(meta.GetString("coordination_branch")) != ""
	_, err := os.Stat(filepath.Join(missionDir, lanes.File))
	hasLanes := err == nil
	switch {
	case hasCoord && hasLanes:
		return LanesWithCoord
	case hasCoord:
		return Coord
	case hasLanes:
		return Lanes
	}
	return SingleBranch
}

// Surface is the resolved placement of one mission.
type Surface struct {
	Root          string        // main repository root
	Slug          string        // mission directory name
	PrimaryDir    string        // <root>/kitty-specs/<slug>
	Meta          *jsonx.Object // primary meta.json (empty when absent)
	Topology      string
	Mid8          string
	MissionID     string
	CoordBranch   string // "" when not coordination-routed
	CoordWorktree string // coordination worktree root ("" when not routed)
	CoordDir      string // mission dir inside the coordination worktree
}

// Resolve computes the surface for mission dir name slug.
func Resolve(root, slug string) *Surface {
	s := &Surface{Root: root, Slug: slug, PrimaryDir: project.Specs(root, slug)}
	s.Meta = mission.ReadMetaOrEmpty(s.PrimaryDir)
	s.MissionID = s.Meta.GetString("mission_id")
	s.Mid8 = s.Meta.GetString("mid8")
	if s.Mid8 == "" {
		s.Mid8 = mission.ResolveMid8(slug, s.MissionID)
	}
	if s.Mid8 == "" {
		s.Mid8 = mission.Mid8FromSlug(slug)
	}
	s.Topology = TopologyFromMeta(s.Meta, s.PrimaryDir)
	if RoutesThroughCoordination(s.Topology) && s.Mid8 != "" {
		s.CoordBranch = strings.TrimSpace(s.Meta.GetString("coordination_branch"))
		if s.CoordBranch == "" {
			s.CoordBranch = lanes.CoordReconstructBranch(slug, s.Mid8)
		}
		s.CoordWorktree = lanes.CoordWorktreePath(root, slug, s.Mid8)
		s.CoordDir = lanes.CoordMissionDir(root, slug, s.Mid8)
	}
	return s
}

// Routed reports whether status is coordination-routed.
func (s *Surface) Routed() bool { return s.CoordWorktree != "" }

// TargetBranch returns meta.target_branch.
func (s *Surface) TargetBranch() string { return s.Meta.GetString("target_branch") }

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// StatusDir is the directory holding status.events.jsonl for reads: the
// coordination mission dir when materialized, else the primary mission dir.
func (s *Surface) StatusDir() string {
	if s.Routed() && isDir(s.CoordDir) {
		return s.CoordDir
	}
	return s.PrimaryDir
}

// ErrNoCoordBranch is returned when the coordination branch is missing.
var ErrNoCoordBranch = errors.New("coordination branch does not exist")

// EnsureWorktree materializes the coordination worktree when its branch
// exists but the worktree directory is absent.
func (s *Surface) EnsureWorktree() error {
	if !s.Routed() {
		return nil
	}
	if isDir(s.CoordWorktree) {
		return nil
	}
	if !gitx.BranchExists(s.Root, s.CoordBranch) {
		return ErrNoCoordBranch
	}
	os.MkdirAll(filepath.Dir(s.CoordWorktree), 0o755)
	_, err := gitx.Run(s.Root, "worktree", "add", s.CoordWorktree, s.CoordBranch)
	return err
}

// WriteDir is the directory status writes go to (materializing the
// coordination worktree / mission dir when needed).
func (s *Surface) WriteDir() (string, error) {
	if !s.Routed() {
		return s.PrimaryDir, nil
	}
	if err := s.EnsureWorktree(); err != nil {
		if errors.Is(err, ErrNoCoordBranch) {
			return s.PrimaryDir, nil
		}
		return "", err
	}
	if err := os.MkdirAll(s.CoordDir, 0o755); err != nil {
		return "", err
	}
	return s.CoordDir, nil
}

// CommitRoot is the checkout bookkeeping commits for the status surface are
// made in.
func (s *Surface) CommitRoot() string {
	if s.Routed() && isDir(s.CoordWorktree) {
		return s.CoordWorktree
	}
	return s.Root
}

// Commit stages paths (absolute or relative to the commit root) in the
// status checkout and commits them with message when anything changed.
// Returns the new commit SHA ("" when nothing was committed).
func (s *Surface) Commit(message string, paths ...string) (string, error) {
	return CommitPaths(s.CommitRoot(), message, paths...)
}

// CommitPaths stages and commits paths in checkout dir (no-op when the
// paths carry no changes).
func CommitPaths(dir, message string, paths ...string) (string, error) {
	var rel []string
	for _, p := range paths {
		if filepath.IsAbs(p) {
			r, err := filepath.Rel(dir, p)
			if err != nil {
				return "", err
			}
			p = r
		}
		rel = append(rel, filepath.ToSlash(p))
	}
	args := append([]string{"add", "--"}, rel...)
	if _, err := gitx.Run(dir, args...); err != nil {
		return "", err
	}
	diffArgs := append([]string{"diff", "--cached", "--quiet", "--"}, rel...)
	if gitx.OK(dir, diffArgs...) {
		return "", nil
	}
	commitArgs := append([]string{"commit", "-q", "-m", message, "--"}, rel...)
	if _, err := gitx.Run(dir, commitArgs...); err != nil {
		return "", err
	}
	return gitx.HeadSHA(dir)
}
