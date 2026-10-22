package create

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// PendingOriginRel is the staged pending-origin file (upstream
// core/adapters._PENDING_ORIGIN_RELPATH).
var PendingOriginRel = filepath.Join(".kittify", "pending-origin.yaml")

// pendingOriginError mirrors upstream PendingOriginConsumerNotRegisteredError:
// rc8's tracker package no longer registers a pending-origin consumer, so a
// staged binding makes create fail closed after the scaffold commit.
func pendingOriginError(root string) error {
	p := filepath.Join(root, PendingOriginRel)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return nil
	}
	return fail("A staged pending origin (%s) exists but no pending-origin consumer is registered. Ensure 'import specify_cli.tracker' runs before create_mission_core so the tracker startup hook registers its consumer — otherwise the staged ticket binding would be silently dropped.", p)
}

// rollbackState is the pre-create git state upstream's
// _restore_git_state_after_failed_create restores.
type rollbackState struct {
	root      string
	branch    string
	commit    string
	indexTree string
	coordPre  map[string]bool
	coordWT   string
}

func coordinationBranches(root string) map[string]bool {
	out, _ := gitx.Run(root, "for-each-ref", "--format=%(refname:short)", "refs/heads/kitty/")
	set := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "kitty/mission-") {
			set[l] = true
		}
	}
	return set
}

func captureRollback(root, branch string) *rollbackState {
	rs := &rollbackState{root: root, branch: branch, coordPre: coordinationBranches(root)}
	rs.commit, _ = gitx.HeadSHA(root)
	rs.indexTree, _ = gitx.Run(root, "write-tree")
	return rs
}

// restore undoes a failed create's git side effects (best effort, never
// fails): checkout + CAS branch-ref restore + index restore, coordination
// worktree teardown, and deletion of coordination branches this run minted.
// The on-disk scaffold is kept for diagnosis, like upstream.
func (rs *rollbackState) restore() {
	root := rs.root
	if rs.branch != "" {
		if cur := gitx.CurrentBranch(root); cur != "" && cur != rs.branch {
			gitx.Run(root, "checkout", rs.branch)
		}
		if rs.commit != "" {
			if tip, err := gitx.HeadSHA(root); err == nil && tip != "" && tip != rs.commit {
				gitx.Run(root, "update-ref", "refs/heads/"+rs.branch, rs.commit, tip)
			}
			if rs.indexTree != "" {
				gitx.Run(root, "read-tree", rs.indexTree)
			}
		}
	}
	if rs.coordWT != "" {
		if st, err := os.Stat(rs.coordWT); err == nil && st.IsDir() {
			gitx.Run(root, "worktree", "remove", "--force", rs.coordWT)
		}
		gitx.Run(root, "worktree", "prune")
	}
	var orphaned []string
	for b := range coordinationBranches(root) {
		if !rs.coordPre[b] {
			orphaned = append(orphaned, b)
		}
	}
	sort.Strings(orphaned)
	for _, b := range orphaned {
		gitx.Run(root, "branch", "-D", b)
	}
}
