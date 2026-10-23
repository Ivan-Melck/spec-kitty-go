package placement

// Wave 5 FIX2 (additive): require_coord_worktree_branch and the owned read
// arm (_owned_read_dir_for_kind / _resolve_status_surface_dir_owned).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// DetachedOrUnreadableHead is worktree_branch.DETACHED_OR_UNREADABLE_HEAD.
const DetachedOrUnreadableHead = "<detached or unreadable HEAD>"

// BranchMismatchError is CoordinationWorkspaceBranchMismatch
// (COORDINATION_WORKTREE_BRANCH_MISMATCH): a materialized coordination
// worktree checked out on another branch (or a detached HEAD).
type BranchMismatchError struct{ WorktreePath, Expected, Actual string }

func (e *BranchMismatchError) Error() string {
	return fmt.Sprintf("Coordination worktree at %s is on %s, expected %s. Manual intervention required.",
		e.WorktreePath, mission.PyRepr(e.Actual), mission.PyRepr(e.Expected))
}

// ErrorCode is the exception's error_code.
func (e *BranchMismatchError) ErrorCode() string { return "COORDINATION_WORKTREE_BRANCH_MISMATCH" }

// TypeName is the Python exception class name.
func (e *BranchMismatchError) TypeName() string { return "CoordinationWorkspaceBranchMismatch" }

// headRefFromGitdirFile is _head_ref_from_gitdir_file: HEAD of a linked
// worktree read through its `.git` file ("" for any other shape).
func headRefFromGitdirFile(path string) string {
	b, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return ""
	}
	pointer := strings.TrimSpace(string(b))
	if !strings.HasPrefix(pointer, "gitdir:") {
		return ""
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(pointer, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(path, gitdir)
	}
	h, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(h))
	if !strings.HasPrefix(head, "ref: ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
}

// ProbeCoordBranch is worktree_branch.probe_coord_branch: (symbolic ref,
// failure label); the label is meaningful only when the ref is "".
func ProbeCoordBranch(path string) (string, string) {
	if ref := headRefFromGitdirFile(path); ref != "" {
		return ref, ""
	}
	r, err := gitx.Exec(path, nil, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return "", fmt.Sprintf("<git unavailable: %v>", err)
	}
	if r.Code != 0 {
		return "", DetachedOrUnreadableHead
	}
	return strings.TrimSpace(r.Stdout), DetachedOrUnreadableHead
}

// RequireCoordWorktreeBranch is workspace.require_coord_worktree_branch.
func RequireCoordWorktreeBranch(path, expected string) error {
	actual, label := ProbeCoordBranch(path)
	norm := func(r string) string { return strings.TrimPrefix(r, "refs/heads/") }
	if actual == "" || norm(actual) != norm(expected) {
		if actual == "" {
			actual = label
		}
		return &BranchMismatchError{WorktreePath: path, Expected: expected, Actual: actual}
	}
	return nil
}

// OwnedCoordUnavailableError is the ActionContextError
// (OWNED_COORDINATION_WORKSPACE_UNAVAILABLE) the owned read arm raises for a
// coordination kind whose coordination worktree is not materialized.
type OwnedCoordUnavailableError struct{ Msg string }

func (e *OwnedCoordUnavailableError) Error() string { return e.Msg }

// ErrorCode is OwnedRefusalCode.OWNED_COORDINATION_WORKSPACE_UNAVAILABLE.
func (e *OwnedCoordUnavailableError) ErrorCode() string {
	return "OWNED_COORDINATION_WORKSPACE_UNAVAILABLE"
}

// Owned is the part of an OwnedCheckout the owned read arm consumes.
type Owned struct {
	RepositoryRoot string // the repository-root checkout R
	MissionDir     string // P/kitty-specs/<slug>
	Topology       string
}

// ReadDirOwned is placement_seam(..., owned=...).read_dir(kind)
// (_owned_read_dir_for_kind): a primary kind is the owned mission dir; a
// coordination kind of a coordination-routed Mission is the
// coordination worktree's mission dir under the repository root, failing
// closed (*OwnedCoordUnavailableError) for every non-materialized state
// except UNMATERIALIZED with tolerateUnmaterialized, *ReadPathNotFound
// (Deleted) for a deleted branch, *BranchMismatchError for a worktree on
// another branch.
func ReadDirOwned(o Owned, slug string, kind Kind, tolerateUnmaterialized bool) (string, error) {
	if kind == Primary {
		return o.MissionDir, nil
	}
	meta := mission.ReadMetaOrEmpty(o.MissionDir)
	branch := meta.GetString("coordination_branch")
	if !coord.RoutesThroughCoordination(o.Topology) || branch == "" {
		return o.MissionDir, nil
	}
	mid8 := mission.ResolveMid8(slug, meta.GetString("mission_id"))
	unavailable := func(dir, state string) error {
		return &OwnedCoordUnavailableError{fmt.Sprintf(
			"the coordination worktree for mission %s (declared branch %s) is not materialised at %s (state=%s); run the command that materialises it before resolving this surface.",
			mission.PyRepr(slug), mission.PyRepr(branch), dir, state)}
	}
	if mid8 == "" {
		return "", unavailable(o.MissionDir, "none")
	}
	coordDir := lanes.CoordMissionDir(o.RepositoryRoot, slug, mid8)
	wt := lanes.CoordWorktreePath(o.RepositoryRoot, slug, mid8)
	if exists(wt) {
		if exists(filepath.Join(wt, ".git")) {
			if err := RequireCoordWorktreeBranch(wt, branch); err != nil {
				return "", err
			}
		}
		if exists(coordDir) {
			return coordDir, nil
		}
		return "", unavailable(coordDir, "empty")
	}
	if !CoordBranchExists(o.RepositoryRoot, branch) {
		return "", &ReadPathNotFound{Deleted: true, Slug: slug, Mid8: mid8, Branch: branch, PrimaryDir: o.MissionDir, CoordDirAbs: coordDir}
	}
	if tolerateUnmaterialized {
		return coordDir, nil
	}
	return "", unavailable(coordDir, "unmaterialized")
}
