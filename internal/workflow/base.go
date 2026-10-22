package workflow

// `implement --base` (#3571/#4969): origin-preferring base resolution and the
// allocator's "unhonorable base" refusals.

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
)

// ResolveRemote is kernel.git.remote.resolve_remote ("" = none).
func ResolveRemote(root, branch string) string {
	if r := git(root, "config", "--get", "branch."+branch+".remote"); r.Code == 0 {
		if v := strings.TrimSpace(r.Stdout); v != "" && v != "." {
			return v
		}
	}
	var remotes []string
	if r := git(root, "remote"); r.Code == 0 {
		for _, l := range strings.Split(r.Stdout, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				remotes = append(remotes, l)
			}
		}
	}
	if len(remotes) == 1 {
		return remotes[0]
	}
	for _, x := range remotes {
		if x == "origin" {
			return "origin"
		}
	}
	return ""
}

// ResolveBaseRef is implement_support.resolve_base_ref: (effective ref,
// sha), preferring <remote>/<base> when the local ref is absent or behind.
func ResolveBaseRef(root, base string) (string, string, bool) {
	local := branchTip(root, base)
	remote := ResolveRemote(root, base)
	if remote == "" {
		remote = "origin"
	}
	originSHA := branchTip(root, "refs/remotes/"+remote+"/"+base)
	if originSHA != "" && (local == "" || isAncestorIn(root, local, originSHA)) {
		return remote + "/" + base, originSHA, true
	}
	if local != "" {
		return base, local, true
	}
	return "", "", false
}

// BaseRefUnresolvedMessage is implement_phases._BASE_REF_UNRESOLVED_MSG.
func BaseRefUnresolvedMessage(base string) string {
	return fmt.Sprintf("Error: Base ref '%s' does not resolve. Try 'git fetch' or 'git branch -a' to see available refs.", base)
}

// UnhonorableBaseError is worktree_allocator.UnhonorableBaseError.
type UnhonorableBaseError struct{ Route, WPID, Base string }

func (e *UnhonorableBaseError) Error() string {
	return fmt.Sprintf("Cannot honor --base %s for %s: the %s route cannot re-parent an already-committed lane. See the mission M8 two-route reconciliation for the deferred general fix.",
		invocation.PyRepr(e.Base), invocation.PyRepr(e.WPID), invocation.PyRepr(e.Route))
}

// guardFreshBase is _guard_route_base for the fresh routes.
func guardFreshBase(root, base, wpID string, lane *lanes.Lane, planningSHA *string) error {
	if base == "" {
		return nil
	}
	if planningSHA != nil && *planningSHA != "" {
		if r := git(root, "merge-base", base, *planningSHA); r.Code != 0 {
			return &UnhonorableBaseError{"detached_base", wpID, base}
		}
	}
	if lane != nil && len(lane.DependsOnLanes) > 0 {
		return &UnhonorableBaseError{"dependency_lane", wpID, base}
	}
	return nil
}
