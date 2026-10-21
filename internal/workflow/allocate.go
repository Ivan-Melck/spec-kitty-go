package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

var dbUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func dbSlug(v string) string { return strings.Trim(dbUnsafe.ReplaceAllString(v, "_"), "_") }

// LaneTestEnv is lanes.lane_env.lane_test_env.
func LaneTestEnv(missionSlug, laneID string) *jsonx.Object {
	return jsonx.NewObject("SPEC_KITTY_TEST_DB_NAME", "test_"+dbSlug(missionSlug)+"_"+dbSlug(laneID))
}

// LaneWorkspaceResult is implement_support.LaneWorkspaceResult.
type LaneWorkspaceResult struct {
	WorkspacePath  string
	BranchName     string // "" = None
	LaneID         string // "" = None
	MissionBranch  string
	IsReuse        bool
	ExecutionMode  string
	ResolutionKind string
	LaneTestEnv    *jsonx.Object
	HookBackup     string
	WPStamped      bool
}

// coordinationBranch reads meta.coordination_branch ("" for legacy).
func coordinationBranch(root, slug string) string {
	return strings.TrimSpace(mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("coordination_branch"))
}

// AllocError is a lane-allocation failure with an optional next step.
type AllocError struct {
	Msg      string
	NextStep string
}

func (e *AllocError) Error() string { return e.Msg }

func revParseVerify(root, ref string) string {
	r := git(root, "rev-parse", "--verify", "--end-of-options", ref)
	if r.Code != 0 {
		return "unknown"
	}
	return strings.TrimSpace(r.Stdout)
}

func branchTip(root, ref string) string {
	r := git(root, "rev-parse", "--verify", "--quiet", ref)
	if r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

func isAncestorIn(dir, a, b string) bool {
	return git(dir, "merge-base", "--is-ancestor", a, b).Code == 0
}

// validateWorktreeClean is _validate_worktree_clean.
func validateWorktreeClean(path, laneID string) error {
	r := git(path, "status", "--porcelain")
	if r.Code != 0 {
		return fmt.Errorf("git status failed in %s: %s", path, strings.TrimSpace(r.Stderr))
	}
	if strings.TrimSpace(r.Stdout) != "" {
		return fmt.Errorf("Lane %s worktree at %s has uncommitted changes. Commit or stash before starting the next WP.", laneID, path)
	}
	return nil
}

// registerSparseCheckout is coordination.workspace.register_lane_sparse_checkout.
func registerSparseCheckout(lane, missionSlug string) error {
	if r := git(lane, "sparse-checkout", "init", "--no-cone"); r.Code != 0 {
		return fmt.Errorf("git sparse-checkout init failed: %s", strings.TrimSpace(r.Stderr))
	}
	raw := gitOut(lane, "rev-parse", "--git-path", "info/sparse-checkout")
	if raw == "" {
		return fmt.Errorf("cannot resolve sparse-checkout path in %s", lane)
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(lane, raw)
	}
	os.MkdirAll(filepath.Dir(raw), 0o755)
	patterns := "/*\n!kitty-specs/" + missionSlug + "/status.events.jsonl\n!kitty-specs/" + missionSlug + "/status.json\n"
	if err := os.WriteFile(raw, []byte(patterns), 0o644); err != nil {
		return err
	}
	if r := git(lane, "read-tree", "-mu", "HEAD"); r.Code != 0 {
		return fmt.Errorf("git read-tree failed: %s", strings.TrimSpace(r.Stderr))
	}
	return nil
}

// unmergedPaths lists unresolved index paths.
func unmergedPaths(dir string) []string {
	out := gitOut(dir, "diff", "--name-only", "--diff-filter=U")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func mergeInProgress(dir string) bool {
	return git(dir, "rev-parse", "-q", "--verify", "MERGE_HEAD").Code == 0
}

// reconcileStatusSnapshotConflicts is reconcile_derived_status_snapshot_conflicts:
// regenerate every unmerged status.json from its (union-merged) event log.
func reconcileStatusSnapshotConflicts(dir string) bool {
	un := unmergedPaths(dir)
	if len(un) == 0 {
		return false
	}
	for _, rel := range un {
		if filepath.Base(rel) != status.SnapshotFile {
			return false
		}
	}
	for _, rel := range un {
		fd := filepath.Join(dir, filepath.Dir(rel))
		b, err := os.ReadFile(status.EventsPath(fd))
		if err != nil || strings.TrimSpace(string(b)) == "" {
			return false
		}
		if _, err := status.Materialize(fd); err != nil {
			return false
		}
		if git(dir, "add", "--", rel).Code != 0 {
			return false
		}
	}
	return len(unmergedPaths(dir)) == 0
}

// autoResolveDependencyMerge is _auto_resolve_dependency_merge (status.json
// regeneration; target-owned bookkeeping keeps "ours").
func autoResolveDependencyMerge(dir string) bool {
	resolvedTarget := false
	for _, rel := range unmergedPaths(dir) {
		if !isTargetOwnedPath(rel) {
			continue
		}
		if git(dir, "checkout", "--ours", "--", rel).Code == 0 && git(dir, "add", "--", rel).Code == 0 {
			resolvedTarget = true
		}
	}
	if len(unmergedPaths(dir)) > 0 && !reconcileStatusSnapshotConflicts(dir) {
		return false
	}
	_ = resolvedTarget
	if !mergeInProgress(dir) {
		return false
	}
	return git(dir, "commit", "--no-edit").Code == 0
}

// isTargetOwnedPath is the project-global bookkeeping a lane keeps (#5457).
func isTargetOwnedPath(rel string) bool {
	switch rel {
	case ".kittify/metadata.yaml", ".kittify/config.yaml", ".kittify/canonical-events.jsonl", ".kittify/agent_profiles_manifest.json":
		return true
	}
	return false
}

func abortMerge(dir string) { git(dir, "merge", "--abort") }

// mergeRecordedPlanningCommit is _merge_recorded_planning_commit.
func mergeRecordedPlanningCommit(root, wt, laneID string, sha *string) error {
	if sha == nil || *sha == "" {
		return nil
	}
	if isAncestorIn(wt, *sha, "HEAD") {
		return nil
	}
	r := git(wt, "merge", "--no-edit", "-m", fmt.Sprintf("Merge recorded planning-artifact commit into %s (FR-009)", laneID), *sha)
	if r.Code == 0 {
		return nil
	}
	if autoResolveDependencyMerge(wt) {
		return nil
	}
	abortMerge(wt)
	return &AllocError{
		Msg:      fmt.Sprintf("Merging the recorded planning-artifact commit %s into %s conflicted; the merge was aborted.", *sha, laneID),
		NextStep: fmt.Sprintf("Resolve the conflict by merging %s into the lane branch manually, then re-run the claim.", *sha),
	}
}

// mergeDependencyLaneTips is _merge_dependency_lane_tips (#1684).
func mergeDependencyLaneTips(o *Out, root, wt, slug string, lane *lanes.Lane, m *lanes.Manifest) error {
	byID := map[string]*lanes.Lane{}
	for i := range m.Lanes {
		byID[m.Lanes[i].LaneID] = &m.Lanes[i]
	}
	var deps []*lanes.Lane
	for _, id := range lane.DependsOnLanes {
		if l, ok := byID[id]; ok {
			deps = append(deps, l)
		}
	}
	if len(deps) == 0 {
		return nil
	}
	sortLanes(deps)
	pre := branchTip(wt, "HEAD")
	for _, dep := range deps {
		branch := lanes.LaneBranchName(slug, dep.LaneID, m.TargetBranch)
		if !gitx.BranchExists(root, branch) {
			o.Print(fmt.Sprintf("WARNING: dependency lane '%s' branch '%s' does not resolve; lane '%s' will not contain its tip (it may have been merged-and-deleted). If you need its code, re-run with an explicit --base.", dep.LaneID, branch, lane.LaneID))
			continue
		}
		if isAncestorIn(wt, branch, "HEAD") {
			continue
		}
		r := git(wt, "merge", "--no-edit", "-m", fmt.Sprintf("Merge dependency lane %s into %s", dep.LaneID, lane.LaneID), branch)
		if r.Code == 0 {
			continue
		}
		if autoResolveDependencyMerge(wt) {
			continue
		}
		abortMerge(wt)
		if pre != "" {
			git(wt, "reset", "--hard", pre)
		}
		return &AllocError{
			Msg:      fmt.Sprintf("Dependency lane %s (%s) could not be merged into %s without conflicts; the merge was aborted.", dep.LaneID, branch, lane.LaneID),
			NextStep: fmt.Sprintf("Merge %s into the %s lane branch manually, resolve the conflicts, then re-run the claim.", branch, lane.LaneID),
		}
	}
	return nil
}

func sortLanes(ls []*lanes.Lane) {
	for i := 1; i < len(ls); i++ {
		for j := i; j > 0; j-- {
			a, b := ls[j-1], ls[j]
			if a.ParallelGroup > b.ParallelGroup || (a.ParallelGroup == b.ParallelGroup && a.LaneID > b.LaneID) {
				ls[j-1], ls[j] = b, a
			}
		}
	}
}

// backfillContextIfMissing is _backfill_context_if_missing (FR-022).
func backfillContextIfMissing(root, slug, wpID string, lane *lanes.Lane, wt, branch string, m *lanes.Manifest) {
	if lanes.FindContextForWP(root, slug, wpID) != nil {
		return
	}
	base := coordinationBranch(root, slug)
	if base == "" {
		base = m.MissionBranch
	}
	var bc *string
	if tip := branchTip(root, base); tip != "" {
		bc = &tip
	}
	rel, _ := filepath.Rel(root, wt)
	cur := wpID
	lanes.SaveContext(root, &lanes.WorkspaceContext{
		WPID: wpID, MissionSlug: slug, WorktreePath: rel, BranchName: branch, BaseBranch: base, BaseCommit: bc,
		Dependencies: []string{}, CreatedAt: clock.NowUTCISO(), CreatedBy: "allocate-lane-worktree-backfill",
		VCSBackend: "git", LaneID: lane.LaneID, LaneWPIDs: append([]string{}, lane.WPIDs...), CurrentWP: &cur,
		LaneTestEnv: LaneTestEnv(slug, lane.LaneID),
	})
}

// AllocateLaneWorktree is worktree_allocator.allocate_lane_worktree (no
// --base support): REUSE, CRASH_RECOVERY and FRESH (coord / legacy) routes.
func AllocateLaneWorktree(o *Out, root, slug, wpID string, m *lanes.Manifest) (string, string, error) {
	lane := m.LaneForWP(wpID)
	if lane == nil {
		return "", "", fmt.Errorf("%s is not assigned to any execution lane in lanes.json", wpID)
	}
	wt := lanes.WorktreePath(root, slug, lane.LaneID)
	branch := lanes.LaneBranchName(slug, lane.LaneID, m.TargetBranch)
	InstallLaneTipRecorder(o, root)
	ExcludeFromGitIndex(root, []string{".spec-kitty/"})
	coordBranch := coordinationBranch(root, slug)

	if _, err := os.Stat(wt); err == nil {
		if err := validateWorktreeClean(wt, lane.LaneID); err != nil {
			return "", "", err
		}
		if err := mergeRecordedPlanningCommit(root, wt, lane.LaneID, m.PlanningCommitSHA); err != nil {
			return "", "", err
		}
		if err := mergeDependencyLaneTips(o, root, wt, slug, lane, m); err != nil {
			return "", "", err
		}
		lanes.RecordTip(root, branch)
		backfillContextIfMissing(root, slug, wpID, lane, wt, branch, m)
		return wt, branch, nil
	}

	if gitx.BranchExists(root, branch) {
		git(root, "worktree", "prune")
		os.MkdirAll(filepath.Dir(wt), 0o755)
		if r := git(root, "worktree", "add", wt, branch); r.Code != 0 {
			return "", "", fmt.Errorf("Failed to recover worktree at %s: %s", wt, strings.TrimSpace(r.Stderr))
		}
		if err := validateWorktreeClean(wt, lane.LaneID); err != nil {
			return "", "", err
		}
		if coordBranch != "" {
			if err := registerSparseCheckout(wt, slug); err != nil {
				return "", "", err
			}
		}
		if err := mergeRecordedPlanningCommit(root, wt, lane.LaneID, m.PlanningCommitSHA); err != nil {
			return "", "", err
		}
		if err := mergeDependencyLaneTips(o, root, wt, slug, lane, m); err != nil {
			return "", "", err
		}
		lanes.RecordTip(root, branch)
		backfillContextIfMissing(root, slug, wpID, lane, wt, branch, m)
		return wt, branch, nil
	}

	parent := m.MissionBranch
	if coordBranch != "" {
		parent = coordBranch
		if !gitx.BranchExists(root, coordBranch) {
			if r := git(root, "branch", coordBranch, m.TargetBranch); r.Code != 0 {
				return "", "", fmt.Errorf("Failed to create branch %s from %s: %s", coordBranch, m.TargetBranch, strings.TrimSpace(r.Stderr))
			}
		}
	} else if !gitx.BranchExists(root, m.MissionBranch) {
		if r := git(root, "branch", m.MissionBranch, m.TargetBranch); r.Code != 0 {
			return "", "", fmt.Errorf("Failed to create mission branch %s from %s: %s", m.MissionBranch, m.TargetBranch, strings.TrimSpace(r.Stderr))
		}
	}
	if origin := "refs/remotes/origin/" + branch; branchTip(root, origin) != "" {
		parent = origin
	}
	os.MkdirAll(filepath.Dir(wt), 0o755)
	if r := git(root, "worktree", "add", "-b", branch, wt, parent); r.Code != 0 {
		return "", "", fmt.Errorf("Failed to create lane worktree at %s: %s", wt, strings.TrimSpace(r.Stderr))
	}
	if coordBranch != "" {
		if err := registerSparseCheckout(wt, slug); err != nil {
			return "", "", err
		}
	}
	if err := mergeRecordedPlanningCommit(root, wt, lane.LaneID, m.PlanningCommitSHA); err != nil {
		git(root, "worktree", "remove", "--force", wt)
		return "", "", err
	}
	if err := mergeDependencyLaneTips(o, root, wt, slug, lane, m); err != nil {
		return "", "", err
	}
	lanes.RecordTip(root, branch)
	backfillContextIfMissing(root, slug, wpID, lane, wt, branch, m)
	return wt, branch, nil
}

// CreateLaneWorkspace is implement_support.create_lane_workspace.
func CreateLaneWorkspace(o *Out, root, slug, wpID, wpFile string, ws *lanes.Workspace, m *lanes.Manifest, deps []string) (*LaneWorkspaceResult, error) {
	if ws.RunsInCheckoutRoot() {
		return &LaneWorkspaceResult{WorkspacePath: ws.WorktreePath, BranchName: ws.BranchName, LaneID: ws.LaneID,
			ExecutionMode: ws.ExecutionMode, ResolutionKind: ws.ResolutionKind, LaneTestEnv: &jsonx.Object{}}, nil
	}
	if m == nil {
		return nil, fmt.Errorf("%s requires lanes.json workspace allocation metadata", wpID)
	}
	lane := m.LaneForWP(wpID)
	laneID := "unknown"
	var laneWPs []string
	if lane != nil {
		laneID = lane.LaneID
		laneWPs = append([]string{}, lane.WPIDs...)
	}
	predicted := lanes.WorktreePath(root, slug, laneID)
	predictedBranch := lanes.LaneBranchName(slug, laneID, m.TargetBranch)
	_, statErr := os.Stat(predicted)
	isReuse := statErr == nil || gitx.BranchExists(root, predictedBranch)
	wt, branch, err := AllocateLaneWorktree(o, root, slug, wpID, m)
	if err != nil {
		return nil, err
	}
	backup := InstallCommitGuard(root)
	base := coordinationBranch(root, slug)
	if base == "" {
		base = m.MissionBranch
	}
	if origin := "refs/remotes/origin/" + predictedBranch; branchTip(root, origin) != "" {
		base = origin
	}
	res := &LaneWorkspaceResult{WorkspacePath: wt, BranchName: branch, LaneID: laneID, MissionBranch: m.MissionBranch,
		IsReuse: isReuse, ExecutionMode: ws.ExecutionMode, ResolutionKind: ws.ResolutionKind,
		LaneTestEnv: LaneTestEnv(slug, laneID), HookBackup: backup}
	if isReuse {
		if c := loadLaneContext(root, slug, laneID); c != nil {
			c.WPID = wpID
			cur := wpID
			c.CurrentWP = &cur
			c.Dependencies = deps
			lanes.SaveContext(root, c)
		}
		return res, nil
	}
	sha := revParseVerify(root, base)
	created := clock.NowUTCISO()
	if w, err := wp.Read(wpFile); err == nil {
		w.Meta().Set("base_branch", base)
		w.Meta().Set("base_commit", sha)
		w.Meta().Set("created_at", created)
		if err := w.Write(); err == nil {
			res.WPStamped = true
		}
	}
	rel, _ := filepath.Rel(root, wt)
	bc := sha
	cur := wpID
	if deps == nil {
		deps = []string{}
	}
	lanes.SaveContext(root, &lanes.WorkspaceContext{
		WPID: wpID, MissionSlug: slug, WorktreePath: rel, BranchName: branch, BaseBranch: base, BaseCommit: &bc,
		Dependencies: deps, CreatedAt: created, CreatedBy: "implement-command-lane", VCSBackend: "git",
		LaneID: laneID, LaneWPIDs: laneWPs, CurrentWP: &cur, LaneTestEnv: LaneTestEnv(slug, laneID),
	})
	return res, nil
}

// loadLaneContext loads .kittify/workspaces/<slug>-<lane>.json.
func loadLaneContext(root, slug, laneID string) *lanes.WorkspaceContext {
	for _, c := range lanes.ListContexts(root) {
		if c.MissionSlug == slug && c.LaneID == laneID {
			return c
		}
	}
	return nil
}
