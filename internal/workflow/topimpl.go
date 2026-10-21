package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// ImplementOptions are the `spec-kitty implement` inputs.
type ImplementOptions struct {
	Root       string
	MissionDir string // resolved mission directory name
	WPID       string
	AutoCommit *bool
	JSON       bool
	Actor      string
	// SkipPreamble: the caller already printed the leading blank line and
	// the charter preflight (the command resolves --mission after them).
	SkipPreamble bool
	// Base is the explicit --base ref ("" = auto-detect).
	Base string
}

// ImplementResult is the outcome of the top-level implement.
type ImplementResult struct {
	Workspace *LaneWorkspaceResult
	Changed   bool
}

// DependencyBlockers returns the declared dependencies of wpID that are not
// approved/done (or an operator-provenance cancellation) on snap.
func DependencyBlockers(deps []string, snap *status.Snapshot) []string {
	var out []string
	for _, d := range deps {
		lane := string(status.Planned)
		st := snap.WP(d)
		if st != nil {
			if l := st.GetString("lane"); l != "" {
				lane = l
			}
		}
		switch status.ResolveLaneAlias(lane) {
		case "approved", "done":
			continue
		case "canceled":
			if st != nil && st.GetString("reason_source") == "operator" {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// DependencyError formats the dependencies_not_satisfied refusal.
func DependencyError(wpID string, blocked []string) string {
	return fmt.Sprintf("dependencies_not_satisfied: %s depends on %s; all dependencies must be approved or done before implementation can start", wpID, strings.Join(blocked, ", "))
}

// statusSnapshot reduces the mission's status read surface.
func statusSnapshot(s *coord.Surface) *status.Snapshot {
	stream, err := status.ReadStream(s.StatusDir(), "")
	if err != nil {
		return status.Reduce(nil, nil, true)
	}
	return status.Reduce(stream.Transitions, stream.Annotations, true)
}

type porcelainEntry struct{ xy, path string }

func missionDirStatus(root, primary string) []porcelainEntry {
	rel := relTo(root, primary)
	r := git(root, "status", "--porcelain", "--untracked-files=all", "--", rel)
	var out []porcelainEntry
	for _, l := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		if len(l) < 4 {
			continue
		}
		p := l[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		out = append(out, porcelainEntry{xy: l[:2], path: strings.Trim(p, "\"")})
	}
	return out
}

func isVCSLockOnlyMetaChange(root, rel string) bool {
	if filepath.Base(rel) != mission.MetaFile {
		return false
	}
	committed := git(root, "show", "HEAD:"+rel)
	if committed.Code != 0 {
		return false
	}
	before, err1 := jsonx.DecodeObject([]byte(committed.Stdout))
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err1 != nil || err != nil {
		return false
	}
	after, err2 := jsonx.DecodeObject(b)
	if err2 != nil || !after.Has("vcs") || before.Has("vcs") {
		return false
	}
	c := after.Clone()
	c.Delete("vcs")
	c.Delete("vcs_locked_at")
	return jsonx.Dumps(c, jsonx.SortedPretty) == jsonx.Dumps(before, jsonx.SortedPretty)
}

// CommitPlanningArtifacts is implement_planning_commit._ensure_planning_artifacts_committed_git.
func CommitPlanningArtifacts(o *Out, root, slug, wpID, planningBranch string, autoCommit bool) error {
	primary := project.Specs(root, slug)
	entries := missionDirStatus(root, primary)
	var structural []porcelainEntry
	for _, e := range entries {
		if strings.Contains(e.xy, "D") || strings.Contains(e.xy, "R") || strings.Contains(e.xy, "C") {
			structural = append(structural, e)
		}
	}
	if len(structural) > 0 {
		o.Console("\nError: Uncommitted structural planning-artifact changes (deletions/renames) cannot be auto-committed to the coordination branch:")
		for _, e := range structural {
			x := strings.TrimSpace(e.xy)
			if x == "" {
				x = e.xy
			}
			o.Console("  " + x + " " + e.path)
		}
		o.Console("\nCommit these structural changes to the coordination branch yourself (e.g. `git rm`/`git mv` + commit), then re-run the claim.")
		return ExitErr(1)
	}
	coordBranch := coordinationBranch(root, slug)
	var files []string
	for _, e := range entries {
		base := filepath.Base(e.path)
		if coordBranch != "" && (base == status.EventsFile || base == status.SnapshotFile) {
			continue
		}
		if coordBranch != "" && e.xy == "??" && base == "mission-events.jsonl" && strings.Count(e.path, "/") == 2 {
			continue
		}
		if !autoCommit && isVCSLockOnlyMetaChange(root, e.path) {
			continue
		}
		files = append(files, e.path)
	}
	if len(files) == 0 {
		return nil
	}
	o.Console("\nPlanning artifacts not committed:")
	for _, f := range files {
		o.Console("  " + f)
	}
	current := gitx.CurrentBranch(root)
	if current == "" {
		current = "HEAD"
	}
	if current != planningBranch {
		o.Console(fmt.Sprintf("\nError: Planning artifacts must be committed on %s.", planningBranch))
		o.Console("Current branch: " + current)
		return ExitErr(1)
	}
	if !autoCommit {
		o.Console("\nAuto-commit disabled. Commit planning artifacts first:")
		o.Console(fmt.Sprintf("  spec-kitty safe-commit %s -m \"chore: planning artifacts for %s\" --to-branch %s", primary, slug, planningBranch))
		o.Console("  Run it from the primary checkout; protected primary branches refuse safe-commit unless you pass --allow-protected-branch.")
		return ExitErr(1)
	}
	meta := mission.ReadMetaOrEmpty(primary)
	legacy := coordBranch == "" || meta.GetString("mission_id") == ""
	if legacy {
		o.Console(fmt.Sprintf("\nAuto-committing planning artifacts to %s... (legacy path -- mission has no coordination_branch; routed through BookkeepingTransaction for FR-020/FR-027 atomicity)", planningBranch))
	}
	msg := fmt.Sprintf("chore: planning artifacts for %s\n\nAuto-committed by spec-kitty before creating the lane worktree for %s", slug, wpID)
	var first, statusFiles []string
	for _, f := range files {
		base := filepath.Base(f)
		if base == status.EventsFile || base == status.SnapshotFile {
			statusFiles = append(statusFiles, f)
		} else {
			first = append(first, f)
		}
	}
	for _, group := range [][]string{first, statusFiles} {
		if len(group) == 0 {
			continue
		}
		var abs []string
		for _, f := range group {
			abs = append(abs, filepath.Join(root, f))
		}
		if _, _, err := commitIdempotent(root, msg, abs); err != nil {
			o.Console(fmt.Sprintf("Error: Failed to commit planning artifacts to %s: %v", planningBranch, err))
			return ExitErr(1)
		}
	}
	if legacy {
		o.Console("✓ Planning artifacts committed to " + planningBranch)
	} else {
		o.Console("✓ Planning artifacts committed to coordination branch " + coordBranch)
	}
	return nil
}

// ensureVCSLocked is implement_support.ensure_vcs_locked.
func ensureVCSLocked(o *Out, primary string) (bool, error) {
	meta, err := mission.ReadMeta(primary)
	if err != nil {
		o.Console("Error: meta.json not found in " + primary)
		o.Console("Run /spec-kitty.specify inside your coding agent (Claude Code, Codex, Cursor) first to create the mission structure")
		return false, ExitErr(1)
	}
	if meta.Has("vcs") {
		return false, nil
	}
	meta.Set("vcs", "git")
	meta.Set("vcs_locked_at", clock.NowUTCISO())
	if err := mission.WriteMeta(primary, meta, false); err != nil {
		return false, err
	}
	o.Console("→ VCS locked to git in meta.json")
	return true, nil
}

func pathDirty(root, abs string) bool {
	r := git(root, "status", "--porcelain", "--untracked-files=no", "--", relTo(root, abs))
	return r.Code != 0 || strings.TrimSpace(r.Stdout) != ""
}

func protectedStatusCommitError(root, slug, branch string) string {
	if !coord.IsProtected(root, slug, branch) {
		return ""
	}
	return fmt.Sprintf("Refusing to start implementation status on protected branch '%s' before mutating status files. Run this status commit from an allowed coordination/lane branch, or rerun with --no-auto-commit when you intentionally want to handle the status artifact commit manually.", branch)
}

// TopImplement is cli.commands.implement.implement (detect -> validate ->
// create), printing like upstream; it returns the workspace result.
func TopImplement(o *Out, opt ImplementOptions) (*ImplementResult, error) {
	root, slug, wpID := opt.Root, opt.MissionDir, opt.WPID
	tracker := NewTracker("Implement " + wpID)
	tracker.Add("detect", "Detect mission context")
	tracker.Add("validate", "Validate planning state")
	tracker.Add("create", "Resolve execution workspace")
	if !opt.SkipPreamble {
		o.Console("")
		EmitCharterPreflight(o.Err, root, "implement")
	}
	tracker.Start("detect")
	autoCommit := mission.AutoCommit(root)
	if opt.AutoCommit != nil {
		autoCommit = *opt.AutoCommit
	}
	primary := project.Specs(root, slug)
	wpFile, ferr := wp.Find(primary, wpID)
	if ferr == nil && wpFile == "" {
		ferr = fmt.Errorf("WP file not found for %s in %s", wpID, wp.TasksDir(primary))
	}
	if ferr != nil {
		tracker.Error("detect", ferr.Error())
		o.Console(tracker.Render())
		return nil, ExitErr(1)
	}
	w, err := wp.Read(wpFile)
	if err != nil {
		tracker.Error("detect", err.Error())
		o.Console(tracker.Render())
		return nil, ExitErr(1)
	}
	deps := w.Dependencies()
	tracker.Complete("detect", "Mission: "+slug)

	tracker.Start("validate")
	fail := func(msg string) (*ImplementResult, error) {
		tracker.Error("validate", msg)
		o.Console(tracker.Render())
		return nil, ExitErr(1)
	}
	meta := mission.ReadMetaOrEmpty(primary)
	planningBranch := meta.GetString("target_branch")
	if planningBranch == "" {
		planningBranch = gitx.CurrentBranch(root)
	}
	if autoCommit {
		dest := gitx.CurrentBranch(root)
		if dest == "" {
			dest = planningBranch
		}
		if msg := protectedStatusCommitError(root, slug, dest); msg != "" {
			return fail(msg)
		}
	}
	s := coord.Resolve(root, slug)
	snap := statusSnapshot(s)
	if cur := snap.Lane(wpID); cur == "" || cur == status.Genesis {
		return fail(fmt.Sprintf("WP %s is not finalized; run `spec-kitty agent mission finalize-tasks`", wpID))
	}
	if blocked := DependencyBlockers(deps, snap); len(blocked) > 0 {
		return fail(DependencyError(wpID, blocked))
	}
	planBranch := lanes.SingleBranchWriteRef(root, slug, planningBranch)
	if err := CommitPlanningArtifacts(o, root, slug, wpID, planBranch, autoCommit); err != nil {
		tracker.Error("validate", "")
		o.Console(tracker.Render())
		return nil, err
	}
	ws, err := lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		return fail(err.Error())
	}
	manifest, _ := lanes.Read(primary)
	var lane *lanes.Lane
	if !ws.RunsInCheckoutRoot() || ws.BranchName != "" {
		if manifest == nil {
			return fail(fmt.Sprintf("lanes.json not found in %s", primary))
		}
		lane = manifest.LaneForWP(wpID)
		if lane == nil {
			return fail(fmt.Sprintf("%s is not assigned to any lane in lanes.json", wpID))
		}
	}
	if lane == nil || ws.ExecutionMode == "planning_artifact" {
		tracker.Complete("validate", "Execution: repository root planning workspace")
	} else {
		tracker.Complete("validate", "Lane: "+lane.LaneID)
	}

	tracker.Start("create")
	metaPath := filepath.Join(primary, mission.MetaFile)
	metaDirty := pathDirty(root, metaPath)
	if metaDirty && isVCSLockOnlyMetaChange(root, relTo(root, metaPath)) {
		metaDirty = false
	}
	wpDirty := pathDirty(root, wpFile)
	metaWritten, err := ensureVCSLocked(o, primary)
	if err != nil {
		o.Console(tracker.Render())
		return nil, err
	}
	effectiveBase := ""
	if opt.Base != "" {
		if ws.LaneID == lanes.PlanningLaneID {
			o.Console("Warning: --base is ignored for repository-root planning work")
		} else if ref, _, ok := ResolveBaseRef(root, opt.Base); ok {
			effectiveBase = ref
		} else {
			o.Console(BaseRefUnresolvedMessage(opt.Base))
			o.Console(tracker.Render())
			return nil, ExitErr(1)
		}
	}
	res, err := CreateLaneWorkspaceBase(o, root, slug, wpID, wpFile, ws, manifest, deps, effectiveBase)
	if err != nil {
		tracker.Error("create", "workspace allocation failed: "+err.Error())
		o.Console(tracker.Render())
		o.Console("\nError: Workspace allocation failed: " + err.Error())
		if ae, ok := err.(*AllocError); ok && ae.NextStep != "" {
			o.Console("Next step: " + ae.NextStep)
		}
		return nil, ExitErr(1)
	}
	if res.HookBackup != "" {
		o.Console(fmt.Sprintf("⚠ Existing .git/hooks/pre-commit was not spec-kitty-managed; backed up to %s before installing the commit guard.", res.HookBackup))
	}
	actor := opt.Actor
	if actor == "" {
		actor = "implement-command"
	}
	execMode := ws.StatusExecutionMode()
	start, err := StartImplementation(s, wpID, status.StrActor(actor), execMode+":"+res.WorkspacePath, execMode,
		ClaimPolicyMetadata(ShellPID(), actor), nil, false, nil)
	if err != nil {
		switch err.(type) {
		case *ClaimConflict:
			o.Console("Error: " + err.Error())
		default:
			o.Console("Error: Could not start implementation status: " + err.Error())
		}
		o.Console(tracker.Render())
		return nil, ExitErr(1)
	}
	switch {
	case res.LaneID == "" || ws.RunsInCheckoutRoot() && res.BranchName == "":
		tracker.Complete("create", "Repository root: "+relTo(root, res.WorkspacePath))
	case res.IsReuse:
		tracker.Complete("create", fmt.Sprintf("Reused lane %s: %s", res.LaneID, relTo(root, res.WorkspacePath)))
	default:
		tracker.Complete("create", fmt.Sprintf("Lane %s: %s", res.LaneID, relTo(root, res.WorkspacePath)))
	}
	o.Console(tracker.Render())
	if res.MissionBranch != "" {
		o.Console("→ Mission branch: " + res.MissionBranch)
	}
	if res.BranchName != "" {
		o.Console("→ Lane branch: " + res.BranchName)
	} else {
		o.Console("→ Workspace contract: repository root planning workspace")
	}
	if effectiveBase != "" && ws.LaneID != lanes.PlanningLaneID {
		o.Console("→ Using explicit base ref: " + effectiveBase)
	}
	commitClaim(o, root, slug, wpID, wpFile, autoCommit, start, s, metaWritten, metaDirty, res.WPStamped, wpDirty)
	return &ImplementResult{Workspace: res, Changed: len(start.Events) > 0}, nil
}

// commitClaim is implement_claim._commit_wp_claim_status.
func commitClaim(o *Out, root, slug, wpID, wpFile string, autoCommit bool, start *StartResult, s *coord.Surface,
	metaWritten, metaDirty, wpStamped, wpDirty bool) {
	if start == nil || len(start.Events) == 0 {
		return
	}
	commitMeta := metaWritten && !metaDirty
	if metaWritten && metaDirty {
		o.Console(fmt.Sprintf("Warning: kitty-specs/%s/meta.json had uncommitted changes before the claim; the claim's VCS lock was left uncommitted with them -- commit meta.json yourself.", slug))
	}
	commitWP := wpStamped && !wpDirty
	if wpStamped && wpDirty {
		o.Console(fmt.Sprintf("Warning: %s had uncommitted changes before the claim; the claim's workspace stamp was left uncommitted with them -- commit the WP prompt yourself.", filepath.Base(wpFile)))
	}
	var paths []string
	if !s.Routed() {
		paths = append(paths, statusPair(s.PrimaryDir)...)
	}
	if commitMeta {
		paths = append(paths, filepath.Join(s.PrimaryDir, mission.MetaFile))
	}
	if commitWP {
		paths = append(paths, wpFile)
	}
	if !autoCommit {
		var rels []string
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				rels = append(rels, relTo(root, p))
			}
		}
		if len(rels) > 0 {
			if r := git(root, append([]string{"add", "--force", "--"}, rels...)...); r.Code != 0 {
				o.Console("Warning: Could not stage the claim's changes: " + strings.TrimSpace(r.Stderr))
				o.Console(fmt.Sprintf("→ %s moved to 'doing' (auto-commit disabled, changes left unstaged)", wpID))
				return
			}
		}
		o.Console(fmt.Sprintf("→ %s moved to 'doing' (auto-commit disabled, changes staged only)", wpID))
		return
	}
	if len(paths) == 0 {
		o.Console(fmt.Sprintf("→ %s moved to 'doing'", wpID))
		return
	}
	if _, err := coord.CommitPaths(root, "chore: "+wpID+" claimed for implementation", paths...); err != nil {
		o.Console("Warning: Could not auto-commit lane change: " + err.Error())
		return
	}
	o.Console(fmt.Sprintf("→ %s moved to 'doing'", wpID))
}

// ImplementJSON is _build_implement_json_payload.
func ImplementJSON(root, slug, wpID string, res *LaneWorkspaceResult) *jsonx.Object {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, slug))
	rel := relTo(root, res.WorkspacePath)
	var branch, lane any
	if res.BranchName != "" {
		branch = res.BranchName
	}
	if res.LaneID != "" {
		lane = res.LaneID
	}
	env := res.LaneTestEnv
	if env == nil {
		env = &jsonx.Object{}
	}
	return jsonx.NewObject("workspace", rel, "workspace_path", rel, "branch", branch,
		"mission_slug", slug, "mission_number", mission.MissionNumberString(meta, slug), "mission_type", mission.MissionType(meta),
		"wp_id", wpID, "lane_id", lane, "execution_mode", res.ExecutionMode, "status", "created", "lane_test_env", env)
}

// PrintWorkspaceReadyBanner is _print_workspace_ready_banner.
func PrintWorkspaceReadyBanner(o *Out, res *LaneWorkspaceResult) {
	bar := strings.Repeat("=", 72)
	if res.LaneID == "" {
		o.Console("\n✓ Repository-root workspace ready")
		o.Console("")
		o.Console(bar)
		o.Console("Planning-artifact work for this WP happens in the repository root")
		o.Console(bar)
		o.Console("")
		o.Console("  cd " + res.WorkspacePath)
		o.Console("")
		o.Console("This WP does not get a lane worktree or workspace context file.")
		o.Console("Make planning-artifact changes directly in the repository root.")
		return
	}
	if res.ResolutionKind == "repo_root" && res.BranchName != "" {
		o.Console("\n✓ Repository-root workspace ready")
		o.Console("")
		o.Console("  Work in the repository root checkout on branch " + res.BranchName)
		o.Console("  cd " + res.WorkspacePath)
		o.Console("")
		o.Console("This WP runs directly in the repository root; commit your work on this branch yourself.")
		return
	}
	o.Console("\n✓ Lane worktree ready")
	o.Console("")
	o.Console(bar)
	o.Console("CRITICAL: Change to the lane worktree before editing files")
	o.Console(bar)
	o.Console("")
	o.Console("  cd " + res.WorkspacePath)
	o.Console("")
	o.Console("All file edits, writes, and commits MUST happen in this directory.")
	o.Console("Writing to the main repository instead of the lane worktree is a critical error.")
	if res.LaneTestEnv != nil && res.LaneTestEnv.Len() > 0 {
		o.Console("")
		o.Console("Lane-specific test environment (FR-006):")
		keys := res.LaneTestEnv.Keys()
		sort.Strings(keys)
		for _, k := range keys {
			o.Console(fmt.Sprintf("  export %s=%s", k, res.LaneTestEnv.GetString(k)))
		}
		o.Console("Two parallel SaaS / Django lanes will collide on a single shared test DB unless these are exported in the lane's test process.")
	}
}
