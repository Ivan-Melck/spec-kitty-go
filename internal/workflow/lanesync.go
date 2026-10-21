package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// LaneSyncError is lifecycle_sync.LaneAutoRebaseSyncError.
type LaneSyncError struct {
	LaneID, LaneBranch, WorktreePath, CoordBranch, CoordHead, HaltReason string
}

func (e *LaneSyncError) Error() string {
	return fmt.Sprintf("LANE_AUTO_REBASE_FAILED: auto-rebase refused for %s: %s", e.LaneID, e.HaltReason)
}

// Render prints _render_lane_auto_rebase_failure.
func (e *LaneSyncError) Render(o *Out) {
	o.Printf("Error: LANE_AUTO_REBASE_FAILED: %s", e.HaltReason)
	o.Printf("  lane_id: %s", e.LaneID)
	o.Printf("  lane_worktree_path: %s", e.WorktreePath)
	o.Printf("  coordination_branch: %s", e.CoordBranch)
	head := "None"
	if e.CoordHead != "" {
		head = e.CoordHead
	}
	o.Printf("  coordination_head: %s", head)
}

func isStatusEventsRel(rel string) bool {
	return strings.HasPrefix(rel, "kitty-specs/") && filepath.Base(rel) == status.EventsFile
}

func isStatusJSONRel(rel string) bool {
	return strings.HasPrefix(rel, "kitty-specs/") && filepath.Base(rel) == status.SnapshotFile
}

func sparseEnabled(wt string) bool {
	return strings.EqualFold(gitOut(wt, "config", "--bool", "core.sparseCheckout"), "true")
}

func stageSparse(wt, rel string) error {
	if sparseEnabled(wt) {
		if r := git(wt, "add", "--sparse", rel); r.Code == 0 {
			return nil
		}
	}
	if r := git(wt, "add", rel); r.Code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return nil
}

// resolveStatusJSON is _resolve_status_json: hydrate the hidden event log
// from the index, re-materialize and stage the snapshot.
func resolveStatusJSON(wt, rel string) string {
	fd := filepath.Join(wt, filepath.Dir(rel))
	events := status.EventsPath(fd)
	if _, err := os.Stat(events); err != nil {
		erel := filepath.ToSlash(filepath.Join(filepath.Dir(rel), status.EventsFile))
		r := git(wt, "show", ":0:"+erel)
		if r.Code != 0 {
			return "R-STATUS-JSON: missing authoritative " + erel
		}
		os.MkdirAll(fd, 0o755)
		if err := os.WriteFile(events, []byte(r.Stdout), 0o644); err != nil {
			return "R-STATUS-JSON: could not hydrate " + erel
		}
		if err := stageSparse(wt, erel); err != nil {
			return fmt.Sprintf("R-STATUS-JSON: git add %s failed: %s", erel, err)
		}
	}
	if _, err := status.Materialize(fd); err != nil {
		return fmt.Sprintf("R-STATUS-JSON: could not materialize %s: %v", rel, err)
	}
	if err := stageSparse(wt, rel); err != nil {
		return fmt.Sprintf("R-STATUS-JSON: git add %s failed: %s", rel, err)
	}
	return ""
}

// refreshStatusJSONForStaged is _refresh_status_json_for_staged_artifacts.
func refreshStatusJSONForStaged(wt string, done map[string]bool) string {
	out := gitOut(wt, "diff", "--cached", "--name-status", "-M")
	dirs := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		paths := f[1:]
		for _, p := range paths {
			if isStatusEventsRel(p) {
				if strings.HasPrefix(f[0], "D") || strings.HasPrefix(f[0], "R") {
					return "R-STATUS-EVENTS-JSONL-UNION: refusing staged deletion of " + p
				}
				dirs[filepath.Dir(p)] = true
			} else if isStatusJSONRel(p) {
				dirs[filepath.Dir(p)] = true
			}
		}
	}
	var keys []string
	for d := range dirs {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, d := range keys {
		rel := filepath.ToSlash(filepath.Join(d, status.SnapshotFile))
		if done[rel] {
			continue
		}
		if h := resolveStatusJSON(wt, rel); h != "" {
			return h
		}
	}
	return ""
}

func reapplySparse(wt string) string {
	if !sparseEnabled(wt) {
		return ""
	}
	if r := git(wt, "sparse-checkout", "reapply"); r.Code != 0 {
		return strings.TrimSpace(r.Stderr + r.Stdout)
	}
	return ""
}

// resolveManagedConflicts resolves status artifact conflicts (event log
// union, snapshot regeneration, coordination-owned take-theirs).
func resolveManagedConflicts(wt string, conflicted []string) ([]string, map[string]bool, string) {
	var remaining []string
	done := map[string]bool{}
	refresh := map[string]bool{}
	for _, rel := range conflicted {
		switch {
		case isStatusEventsRel(rel):
			var texts []string
			for _, st := range []string{"1", "2", "3"} {
				if r := git(wt, "show", ":"+st+":"+rel); r.Code == 0 {
					texts = append(texts, r.Stdout)
				}
			}
			merged, err := mergedriver.MergeEventLogTexts(texts...)
			if err != nil {
				return nil, nil, "R-STATUS-EVENTS-JSONL-UNION: " + err.Error()
			}
			p := filepath.Join(wt, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(merged), 0o644)
			if err := stageSparse(wt, rel); err != nil {
				return nil, nil, fmt.Sprintf("R-STATUS-EVENTS-JSONL-UNION: git add %s failed: %s", rel, err)
			}
			refresh[filepath.Dir(rel)] = true
		case isStatusJSONRel(rel):
			refresh[filepath.Dir(rel)] = true
		default:
			remaining = append(remaining, rel)
		}
	}
	var keys []string
	for d := range refresh {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, d := range keys {
		rel := filepath.ToSlash(filepath.Join(d, status.SnapshotFile))
		if h := resolveStatusJSON(wt, rel); h != "" {
			return nil, nil, h
		}
		done[rel] = true
	}
	return remaining, done, ""
}

// SyncLaneAfterCoordinationCommit is lifecycle_sync.sync_lane_after_coordination_commit:
// merge the coordination branch into the WP's lane worktree.
func SyncLaneAfterCoordinationCommit(root, slug, wpID, coordBranch string) error {
	m, err := lanes.Read(project.Specs(root, slug))
	if err != nil || m == nil {
		return nil
	}
	lane := m.LaneForWP(wpID)
	if lane == nil || lane.LaneID == lanes.PlanningLaneID {
		return nil
	}
	wt := lanes.WorktreePath(root, m.MissionSlug, lane.LaneID)
	branch := lanes.LaneBranchName(m.MissionSlug, lane.LaneID, m.TargetBranch)
	head := branchTip(root, coordBranch)
	fail := func(reason string) error {
		return &LaneSyncError{LaneID: lane.LaneID, LaneBranch: branch, WorktreePath: wt, CoordBranch: coordBranch, CoordHead: head, HaltReason: reason}
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		os.MkdirAll(filepath.Dir(wt), 0o755)
		if r := git(root, "worktree", "add", wt, branch); r.Code != 0 {
			return fail("could not create lane worktree for auto-rebase: " + strings.TrimSpace(r.Stderr+r.Stdout))
		}
	}
	abort := func(reason string) error {
		abortMerge(wt)
		return fail(reason)
	}
	r := git(wt, "merge", "--no-edit", "--no-ff", "--no-commit", coordBranch)
	if r.Code == 0 {
		if h := refreshStatusJSONForStaged(wt, map[string]bool{}); h != "" {
			return abort(h)
		}
		if h := reapplySparse(wt); h != "" {
			return abort("sparse checkout cleanup failed: " + h)
		}
		if mergeInProgress(wt) {
			if c := git(wt, "-c", "commit.gpgsign=false", "commit", "--no-edit"); c.Code != 0 {
				return abort("merge commit failed: " + strings.TrimSpace(c.Stderr+c.Stdout))
			}
		}
		return nil
	}
	conflicted := unmergedPaths(wt)
	if len(conflicted) == 0 {
		return abort(fmt.Sprintf("git merge failed without conflicts on %s: %s", branch, strings.TrimSpace(r.Stderr+r.Stdout)))
	}
	remaining, done, halt := resolveManagedConflicts(wt, conflicted)
	if halt != "" {
		return abort(halt)
	}
	if len(remaining) > 0 {
		return abort("manual conflict resolution required: " + strings.Join(remaining, ", "))
	}
	if h := refreshStatusJSONForStaged(wt, done); h != "" {
		return abort(h)
	}
	if h := reapplySparse(wt); h != "" {
		return abort("sparse checkout cleanup failed: " + h)
	}
	n := len(conflicted)
	msg := fmt.Sprintf("auto-rebase(lane=%s): %d conflicts resolved by classifier rules [R-STATUS-EVENTS-JSONL-UNION, R-STATUS-JSON]", lane.LaneID, n)
	if c := git(wt, "-c", "commit.gpgsign=false", "commit", "-m", msg); c.Code != 0 {
		return abort("merge commit failed: " + strings.TrimSpace(c.Stderr+c.Stdout))
	}
	lanes.RecordTip(root, branch)
	return nil
}
