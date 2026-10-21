package consolidate

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// targetOwnedPaths is target_owned_paths(): bookkeeping every branch writes.
var targetOwnedPaths = map[string]bool{".kittify/metadata.yaml": true}

func diffNames(root, base, head string) []string {
	out, err := gitx.Run(root, "diff", "--name-only", base, head)
	if err != nil {
		return nil
	}
	var l []string
	for _, s := range strings.Split(out, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			l = append(l, s)
		}
	}
	return l
}

// treeEntries maps each path present at ref to "mode oid" (nil on failure).
func treeEntries(root, ref string, paths []string) map[string]string {
	out, err := gitx.Run(root, append([]string{"ls-tree", "-r", "--full-tree", ref, "--"}, paths...)...)
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) == 3 {
			m[p] = f[0] + " " + f[2]
		}
	}
	return m
}

// staleOverlap is check_lane_staleness: the files both the mission branch and
// the lane changed since their merge-base whose end states differ.
func staleOverlap(root, laneBranch, missionBranch string) []string {
	base, err := gitx.Run(root, "merge-base", laneBranch, missionBranch)
	if err != nil || strings.TrimSpace(base) == "" {
		return nil
	}
	base = strings.TrimSpace(base)
	missionFiles := diffNames(root, base, missionBranch)
	if len(missionFiles) == 0 {
		return nil
	}
	laneSet := map[string]bool{}
	for _, f := range diffNames(root, base, laneBranch) {
		laneSet[f] = true
	}
	var cand []string
	for _, f := range missionFiles {
		if laneSet[f] && !targetOwnedPaths[f] {
			cand = append(cand, f)
		}
	}
	sort.Strings(cand)
	if len(cand) == 0 {
		return nil
	}
	le, me := treeEntries(root, laneBranch, cand), treeEntries(root, missionBranch, cand)
	if le == nil || me == nil {
		return cand
	}
	var out []string
	for _, p := range cand {
		if le[p] != me[p] {
			out = append(out, p)
		}
	}
	return out
}

func staleRemediation(l lanes.Lane, laneBranch, missionBranch string) string {
	if l.LaneID == lanes.PlanningLaneID {
		return fmt.Sprintf("Lane %s must incorporate mission changes before merging. This lane runs in the repository-root checkout (no worktree) on branch '%s'. From the repository root: git checkout %s && git merge %s. If that merge reports a conflict on status.json (a derived projection of status.events.jsonl that git cannot text-merge), do not hand-edit it -- rebuild it from the event log instead: spec-kitty agent status materialize --mission <id> && git add kitty-specs/<id>/status.json",
			l.LaneID, laneBranch, laneBranch, missionBranch)
	}
	return fmt.Sprintf("Lane %s must incorporate mission changes before merging. Run: cd .worktrees/*-%s && git merge %s", l.LaneID, l.LaneID, missionBranch)
}

// autoRebase is attempt_auto_rebase for the clean-merge case: merge the
// mission branch into the lane worktree (--no-ff), refresh staged status
// snapshots and commit with git's default merge message. A conflicted merge
// is aborted (the conflict classifier is not ported) and reported as failed.
func autoRebase(root, wt, laneBranch, missionBranch string) bool {
	res, _ := gitEnv(wt, mergeEnv(false), "merge", "--no-edit", "--no-ff", "--no-commit", missionBranch)
	if res.Code != 0 {
		gitEnv(wt, nil, "merge", "--abort")
		return false
	}
	staged, _ := gitx.Run(wt, "diff", "--cached", "--name-only")
	dirs := map[string]bool{}
	for _, p := range strings.Split(staged, "\n") {
		p = strings.TrimSpace(p)
		if b := path.Base(p); strings.HasPrefix(p, "kitty-specs/") && (b == status.EventsFile || b == "status.json") {
			dirs[path.Dir(p)] = true
		}
	}
	keys := make([]string, 0, len(dirs))
	for d := range dirs {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, d := range keys {
		if snap, err := status.MaterializeSnapshot(filepath.Join(wt, d)); err == nil {
			os.WriteFile(filepath.Join(wt, d, "status.json"), []byte(snap.Render()), 0o644)
			gitx.Run(wt, "add", "--", path.Join(d, "status.json"))
		}
	}
	if gitx.OK(wt, "rev-parse", "-q", "--verify", "MERGE_HEAD") {
		if r, _ := gitEnv(wt, mergeEnv(false), "commit", "--no-edit"); r.Code != 0 {
			gitEnv(wt, nil, "merge", "--abort")
			return false
		}
	}
	lanes.RecordTip(root, laneBranch)
	return true
}

// checkStale runs the stale-lane gate with the worktree auto-rebase; it
// returns the error text when the lane stays stale ("" when mergeable).
func (r *run) checkStale(l lanes.Lane, branch string) string {
	ov := staleOverlap(r.root, branch, r.m.MissionBranch)
	if len(ov) == 0 {
		return ""
	}
	wt := lanes.WorktreePath(r.root, r.slug, l.LaneID)
	if dirExists(wt) && autoRebase(r.root, wt, branch, r.m.MissionBranch) {
		ov = staleOverlap(r.root, branch, r.m.MissionBranch)
		if len(ov) == 0 {
			return ""
		}
	}
	return fmt.Sprintf("Lane %s is stale: overlapping files %s. %s", l.LaneID, pyjson.ReprStrings(ov), staleRemediation(l, branch, r.m.MissionBranch))
}
