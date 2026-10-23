// Package placement is the read side of mission_runtime's placement seam:
// placement_seam(repo_root, handle).read_dir(kind) for the repository-root
// (non-owned) case, i.e. resolve_artifact_surface → resolve_planning_read_dir
// (+ the composed-slug backfill recovery) and the four-CoordState classifier
// for coordination-partition kinds.
package placement

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Kind is a MissionArtifactKind reduced to its partition.
type Kind int

const (
	// Primary is a PRIMARY-partition kind (PRIMARY_METADATA,
	// WORK_PACKAGE_TASK, TASKS_INDEX, DECISION_LEDGER, ...).
	Primary Kind = iota
	// Coord is a coordination-partition kind (STATUS_STATE, REVIEW_CYCLE,
	// DECISION_LOG, ACCEPTANCE_MATRIX, ...).
	Coord
)

// UnsafeSegmentError is assert_safe_path_segment's UnsafePathSegmentError
// (a ValueError).
type UnsafeSegmentError struct{ Msg string }

func (e *UnsafeSegmentError) Error() string { return e.Msg }

// ReadPathNotFound is StatusReadPathNotFound: the declared coordination
// surface cannot be read (Deleted: CoordinationBranchDeleted; otherwise
// CoordinationWorktreeUnmaterialized).
type ReadPathNotFound struct {
	Deleted     bool
	Slug, Mid8  string
	Branch      string
	PrimaryDir  string
	CoordDirAbs string
}

func (e *ReadPathNotFound) Error() string {
	if e.Deleted {
		return fmt.Sprintf("Coordination branch %s for mission %s was deleted; its coordination surface cannot be read.", mission.PyRepr(e.Branch), mission.PyRepr(e.Slug))
	}
	return fmt.Sprintf("Coordination worktree for mission %s (branch %s) is not materialized at %s.", mission.PyRepr(e.Slug), mission.PyRepr(e.Branch), e.CoordDirAbs)
}

func composePrimary(root, name string) (string, error) {
	if r := mission.UnsafeSegmentReason(name); r != "" {
		return "", &UnsafeSegmentError{r}
	}
	return project.Specs(project.MainRepoRoot(root), name), nil
}

// canonicalizeHandle is _canonicalize_handle: the identity resolver's dir
// name, "" when not found; ambiguity is MissionSelectorAmbiguous.
func canonicalizeHandle(root, handle string) (string, error) {
	r, err := mission.ResolveIdentity(root, handle)
	if err == nil {
		return r.Slug, nil
	}
	if amb, ok := err.(*mission.AmbiguousError); ok {
		var c []string
		for _, x := range amb.Candidates {
			c = append(c, x.Slug)
		}
		return "", &mission.SelectorAmbiguousError{Handle: handle, Candidates: c}
	}
	return "", nil
}

// canonicalPrimaryName is _canonicalize_primary_read_handle.
func canonicalPrimaryName(root, handle string) (string, error) {
	literal, err := composePrimary(root, handle)
	if err != nil {
		return "", err
	}
	bare := handle
	if !isFile(filepath.Join(literal, mission.MetaFile)) {
		name, err := canonicalizeHandle(root, handle)
		if err != nil {
			return "", err
		}
		if name == "" {
			folded, err := mission.BareModernDirName(root, handle)
			if err != nil {
				return "", err
			}
			if folded != "" {
				bare = folded
			}
		}
	}
	if bare != handle {
		return bare, nil
	}
	name, err := canonicalizeHandle(root, handle)
	if err != nil {
		return "", err
	}
	if name != "" {
		return name, nil
	}
	return handle, nil
}

// planningPrimaryDir is resolve_planning_read_dir(root, handle,
// kind=<PRIMARY kind>).
func planningPrimaryDir(root, handle string) (string, error) {
	name, err := canonicalPrimaryName(root, handle)
	if err != nil {
		return "", err
	}
	return composePrimary(root, name)
}

// PlanningReadDir is resolve_planning_read_dir(root, handle, kind=<PRIMARY
// kind>) — the canonicalizing primary read leaf (no composed-slug backfill
// recovery). Errors: *UnsafeSegmentError (ValueError) or
// *mission.SelectorAmbiguousError.
func PlanningReadDir(root, handle string) (string, error) {
	return planningPrimaryDir(root, handle)
}

// PrimaryDir is placement_seam(root, handle).read_dir(<PRIMARY kind>):
// the canonical primary mission dir (possibly nonexistent) under the main
// repository root.
func PrimaryDir(root, handle string) (string, error) {
	primaryRoot := project.MainRepoRoot(root)
	dir, err := planningPrimaryDir(primaryRoot, handle)
	if err != nil {
		return "", err
	}
	if rec := backfilledPrimaryDir(primaryRoot, handle, dir); rec != "" {
		dir = rec
	}
	return dir, nil
}

// backfilledPrimaryDir is _backfilled_primary_dir: an already-composed
// <slug>-<mid8> handle whose directory is the bare slug.
func backfilledPrimaryDir(primaryRoot, slug, dir string) string {
	if exists(dir) {
		return ""
	}
	tail := mission.Mid8FromSlug(slug)
	if tail == "" || len(slug) <= len(tail)+1 {
		return ""
	}
	bare := slug[:len(slug)-len(tail)-1]
	cand, err := planningPrimaryDir(primaryRoot, bare)
	if err != nil || cand == dir || !isDir(cand) {
		return ""
	}
	meta := mission.ReadMetaOrEmpty(project.Specs(primaryRoot, bare))
	mid8 := meta.GetString("mid8")
	if mid8 == "" {
		if id := meta.GetString("mission_id"); len(id) >= 8 {
			mid8 = id[:8]
		}
	}
	if !equalFold(mid8, tail) {
		return ""
	}
	return cand
}

// ReadDir is placement_seam(root, handle).read_dir(kind).
func ReadDir(root, handle string, kind Kind) (string, error) {
	primary, err := PrimaryDir(root, handle)
	if err != nil || kind == Primary {
		return primary, err
	}
	primaryRoot := project.MainRepoRoot(root)
	slug := filepath.Base(primary)
	meta := mission.ReadMetaOrEmpty(primary)
	if !coord.RoutesThroughCoordination(coord.TopologyFromMeta(meta, primary)) {
		return primary, nil
	}
	branch := meta.GetString("coordination_branch")
	mid8 := mission.ResolveMid8(slug, meta.GetString("mission_id"))
	if mid8 == "" {
		return primary, nil
	}
	coordDir := lanes.CoordMissionDir(primaryRoot, slug, mid8)
	if wt := lanes.CoordWorktreePath(primaryRoot, slug, mid8); exists(wt) {
		// probe_coord_state: a real, materialized worktree must still be on
		// its declared ref.
		if branch != "" && exists(filepath.Join(wt, ".git")) {
			if err := RequireCoordWorktreeBranch(wt, branch); err != nil {
				return "", err
			}
		}
		if exists(coordDir) {
			return coordDir, nil
		}
		return primary, nil // EMPTY
	}
	nf := &ReadPathNotFound{Slug: slug, Mid8: mid8, Branch: branch, PrimaryDir: primary, CoordDirAbs: coordDir}
	if branch != "" && !gitx.BranchExists(primaryRoot, branch) && !CoordBranchExists(primaryRoot, branch) {
		nf.Deleted = true
	}
	return "", nf
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'a' <= x && x <= 'z' {
			x -= 32
		}
		if 'a' <= y && y <= 'z' {
			y -= 32
		}
		if x != y {
			return false
		}
	}
	return true
}

// CoordBranchExists is surface_resolver._coord_branch_exists (fails closed
// toward "present" outside a git checkout rooted at root).
func CoordBranchExists(root, branch string) bool {
	top, err := gitx.Toplevel(root)
	if err != nil {
		return true
	}
	if r1, e1 := filepath.EvalSymlinks(top); e1 == nil {
		if r2, e2 := filepath.EvalSymlinks(root); e2 == nil && r1 != r2 {
			return true
		}
	}
	if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) {
		return true
	}
	if out, err := gitx.Run(root, "for-each-ref", "--format=%(refname)", "refs/remotes/"); err == nil {
		for _, line := range splitLines(out) {
			if len(line) > len(branch)+1 && line[len(line)-len(branch)-1:] == "/"+branch {
				return true
			}
		}
	}
	remotes, _ := gitx.Run(root, "remote")
	for _, r := range splitLines(remotes) {
		res, err := gitx.Exec(root, nil, nil, "ls-remote", "--heads", r, branch)
		if err != nil || res.Code != 0 || len(res.Stdout) > 0 {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
