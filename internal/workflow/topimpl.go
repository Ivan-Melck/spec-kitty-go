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
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
	"github.com/spec-kitty/go-kitty3/internal/writecheckout"
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
	Base                   string
	AcknowledgeNotBulkEdit bool
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

func missionDirStatus(root, primary string) ([]porcelainEntry, error) {
	rel := relTo(root, primary)
	r := git(root, "status", "--porcelain", "--untracked-files=all", "--", rel)
	if r.Code != 0 {
		// kernel GitCommandError: the probe fails closed
		detail := "no error output"
		if s := strings.TrimSpace(r.Stderr); s != "" {
			detail = strings.SplitN(s, "\n", 2)[0]
		}
		return nil, fmt.Errorf("git --literal-pathspecs status --porcelain=v1 -z --untracked-files=all -- %s failed in %s (exit %d): %s", primary, root, r.Code, detail)
	}
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
	return out, nil
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
	entries, err := missionDirStatus(root, primary)
	if err != nil {
		o.Console(fmt.Sprintf("\n[red]Error:[/red] Could not read git status for the planning artifacts in %s, so the claim is refused: %v", primary, err))
		return ExitErr(1)
	}
	var structural []porcelainEntry
	for _, e := range entries {
		if strings.Contains(e.xy, "D") || strings.Contains(e.xy, "R") || strings.Contains(e.xy, "C") {
			structural = append(structural, e)
		}
	}
	if len(structural) > 0 {
		o.Console("\n[red]Error:[/red] Uncommitted structural planning-artifact changes (deletions/renames) cannot be auto-committed to the coordination branch:")
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
	o.Console("\n[cyan]Planning artifacts not committed:[/cyan]")
	for _, f := range files {
		o.Console("  " + f)
	}
	current := gitx.CurrentBranch(root)
	if current == "" {
		current = "HEAD"
	}
	if current != planningBranch {
		o.Console(fmt.Sprintf("\n[red]Error:[/red] Planning artifacts must be committed on %s.", planningBranch))
		o.Console("Current branch: " + current)
		return ExitErr(1)
	}
	if !autoCommit {
		o.Console("\n[yellow]Auto-commit disabled.[/yellow] Commit planning artifacts first:")
		o.Console(fmt.Sprintf("  spec-kitty safe-commit %s -m \"chore: planning artifacts for %s\" --to-branch %s", primary, slug, planningBranch))
		o.Console("  Run it from the primary checkout; protected primary branches refuse safe-commit unless you pass --allow-protected-branch.")
		return ExitErr(1)
	}
	meta := mission.ReadMetaOrEmpty(primary)
	legacy := coordBranch == "" || meta.GetString("mission_id") == ""
	if legacy {
		o.Console(fmt.Sprintf("\n[cyan]Auto-committing planning artifacts to %s...[/cyan] [dim](legacy path -- mission has no coordination_branch; routed through BookkeepingTransaction for FR-020/FR-027 atomicity)[/dim]", planningBranch))
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
			o.Console(fmt.Sprintf("[red]Error:[/red] Failed to commit planning artifacts to %s: %v", planningBranch, err))
			return ExitErr(1)
		}
	}
	if legacy {
		o.Console("[green]✓[/green] Planning artifacts committed to " + planningBranch)
	} else {
		o.Console("[green]✓[/green] Planning artifacts committed to coordination branch " + coordBranch)
	}
	return nil
}

// ensureVCSLocked is implement_support.ensure_vcs_locked.
func ensureVCSLocked(o *Out, primary string) (bool, error) {
	meta, err := mission.ReadMeta(primary)
	if err != nil {
		o.Console("[red]Error:[/red] meta.json not found in " + primary)
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
	o.Console("[cyan]→ VCS locked to git in meta.json[/cyan]")
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
		if err := CharterPreflightOrAbort(o.Err, root, "implement"); err != nil {
			// The preflight's typer.Exit(1) lands in the detect phase's
			// error handler (str(Exit(1)) == "1").
			tracker.Start("detect")
			tracker.Error("detect", "1")
			o.ConsoleR(tracker.Render())
			return nil, err
		}
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
		o.ConsoleR(tracker.Render())
		return nil, ExitErr(1)
	}
	w, err := wp.Read(wpFile)
	if err != nil {
		tracker.Error("detect", err.Error())
		o.ConsoleR(tracker.Render())
		return nil, ExitErr(1)
	}
	deps := w.Dependencies()
	tracker.Complete("detect", "Mission: "+slug)

	tracker.Start("validate")
	fail := func(msg string) (*ImplementResult, error) {
		tracker.Error("validate", msg)
		o.ConsoleR(tracker.Render())
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
		// tracker.error("validate", str(exc)): str(typer.Exit(1)) is "1"
		detail := err.Error()
		if e, ok := err.(*Exit); ok {
			detail = fmt.Sprint(e.Code)
		}
		tracker.Error("validate", detail)
		o.ConsoleR(tracker.Render())
		return nil, err
	}
	if err := bulkImplementGate(o, primary, slug, wpID, w, opt.AcknowledgeNotBulkEdit); err != nil {
		if e, ok := err.(*Exit); ok {
			return fail(fmt.Sprint(e.Code))
		}
		return fail(err.Error())
	}
	ws, err := lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		return fail(err.Error())
	}
	manifest, _ := lanes.Read(primary)
	var lane *lanes.Lane
	if ws.LaneID != lanes.PlanningLaneID { // resolve_execution_lane: none for a repo-root lane
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
	createFailure := func(err error) (*ImplementResult, error) {
		tracker.Error("create", "workspace allocation failed: "+err.Error())
		o.ConsoleR(tracker.Render())
		o.Console("\n[red]Error:[/red] Workspace allocation failed: " + err.Error())
		return nil, ExitErr(1)
	}
	// #5796: the checkout claim lock (single_branch repo-root lane only) is
	// held from before the occupancy scan through the claim commit.
	if writecheckout.IsSingleBranchRepoRootLane(root, slug, ws) {
		release, err := writecheckout.AcquireClaimLock(root, writecheckout.ClaimLockTimeout)
		if err != nil {
			if lt, ok := err.(*writecheckout.LockTimeoutError); ok {
				tracker.Error("create", lt.ErrorCode()+": status lock held by another writer")
				o.ConsoleR(tracker.Render())
				o.Console("\n[red]Error:[/red] " + lt.ErrorCode() + ": " + rich.EscapeMarkup(lt.Msg) + ". Another writer holds the lock; retry the claim.")
				return nil, ExitErr(1)
			}
			return createFailure(err)
		}
		defer release()
	}
	// #5100 A3: the write-checkout refusals run before the VCS lock.
	if ws.LaneID == lanes.PlanningLaneID {
		if err := writecheckout.EnsureAvailable(root, slug, slug, wpID, ws); err != nil {
			return createFailure(err)
		}
	}
	metaPath := filepath.Join(primary, mission.MetaFile)
	metaDirty := pathDirty(root, metaPath)
	if metaDirty && isVCSLockOnlyMetaChange(root, relTo(root, metaPath)) {
		metaDirty = false
	}
	wpDirty := pathDirty(root, wpFile)
	metaWritten, err := ensureVCSLocked(o, primary)
	if err != nil {
		o.ConsoleR(tracker.Render())
		return nil, err
	}
	effectiveBase := ""
	if opt.Base != "" {
		if ws.LaneID == lanes.PlanningLaneID {
			o.Console("[yellow]Warning:[/yellow] --base is ignored for repository-root planning work")
		} else if ref, _, ok := ResolveBaseRef(root, opt.Base); ok {
			effectiveBase = ref
		} else {
			o.Console(BaseRefUnresolvedMessage(opt.Base))
			o.ConsoleR(tracker.Render())
			return nil, ExitErr(1)
		}
	}
	res, err := CreateLaneWorkspaceBase(o, root, slug, wpID, wpFile, ws, manifest, deps, effectiveBase)
	if err != nil {
		tracker.Error("create", "workspace allocation failed: "+err.Error())
		o.ConsoleR(tracker.Render())
		o.Console("\n[red]Error:[/red] Workspace allocation failed: " + err.Error())
		if ae, ok := err.(*AllocError); ok && ae.NextStep != "" {
			o.Console("[yellow]Next step:[/yellow] " + ae.NextStep)
		}
		return nil, ExitErr(1)
	}
	if res.HookBackup != "" {
		o.Console(fmt.Sprintf("[yellow]⚠ Existing .git/hooks/pre-commit was not spec-kitty-managed; backed up to %s before installing the commit guard.[/yellow]", res.HookBackup))
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
			o.Console("[red]Error:[/red] " + err.Error())
		default:
			o.Console("[red]Error:[/red] Could not start implementation status: " + err.Error())
		}
		o.ConsoleR(tracker.Render())
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
	o.ConsoleR(tracker.Render())
	if res.MissionBranch != "" {
		o.Console("[cyan]→ Mission branch: " + res.MissionBranch + "[/cyan]")
	}
	if res.BranchName != "" {
		o.Console("[cyan]→ Lane branch: " + res.BranchName + "[/cyan]")
	} else {
		o.Console("[cyan]→ Workspace contract: repository root planning workspace[/cyan]")
	}
	if effectiveBase != "" && ws.LaneID != lanes.PlanningLaneID {
		o.Console("[cyan]→ Using explicit base ref: " + effectiveBase + "[/cyan]")
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
		o.Console(fmt.Sprintf("[yellow]Warning:[/yellow] kitty-specs/%s/meta.json had uncommitted changes before the claim; the claim's VCS lock was left uncommitted with them -- commit meta.json yourself.", slug))
	}
	commitWP := wpStamped && !wpDirty
	if wpStamped && wpDirty {
		o.Console(fmt.Sprintf("[yellow]Warning:[/yellow] %s had uncommitted changes before the claim; the claim's workspace stamp was left uncommitted with them -- commit the WP prompt yourself.", filepath.Base(wpFile)))
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
				o.Console("[yellow]Warning:[/yellow] Could not stage the claim's changes: " + strings.TrimSpace(r.Stderr))
				o.Console(fmt.Sprintf("[cyan]→ %s moved to 'doing' (auto-commit disabled, changes left unstaged)[/cyan]", wpID))
				return
			}
		}
		o.Console(fmt.Sprintf("[cyan]→ %s moved to 'doing' (auto-commit disabled, changes staged only)[/cyan]", wpID))
		return
	}
	if len(paths) == 0 {
		o.Console(fmt.Sprintf("[cyan]→ %s moved to 'doing'[/cyan]", wpID))
		return
	}
	if _, err := coord.CommitPaths(root, "chore: "+wpID+" claimed for implementation", paths...); err != nil {
		o.Console("[yellow]Warning:[/yellow] Could not auto-commit lane change: " + err.Error())
		return
	}
	o.Console(fmt.Sprintf("[cyan]→ %s moved to 'doing'[/cyan]", wpID))
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
	const open, close = "[bold yellow]", "[/bold yellow]"
	bar := open + strings.Repeat("=", 72) + close
	if res.LaneID == "" {
		o.Console("\n[bold green]✓ Repository-root workspace ready[/bold green]")
		o.Console("")
		o.Console(bar)
		o.Console(open + "Planning-artifact work for this WP happens in the repository root" + close)
		o.Console(bar)
		o.Console("")
		o.Console("  [bold]cd " + res.WorkspacePath + "[/bold]")
		o.Console("")
		o.Console("[dim]This WP does not get a lane worktree or workspace context file.[/dim]")
		o.Console("[dim]Make planning-artifact changes directly in the repository root.[/dim]")
		return
	}
	if res.ResolutionKind == "repo_root" && res.BranchName != "" {
		o.Console("\n[bold green]✓ Repository-root workspace ready[/bold green]")
		o.Console("")
		o.Console("  Work in the repository root checkout on branch [bold]" + res.BranchName + "[/bold]")
		o.Console("  [bold]cd " + res.WorkspacePath + "[/bold]")
		o.Console("")
		o.Console("[dim]This WP runs directly in the repository root; commit your work on this branch yourself.[/dim]")
		return
	}
	o.Console("\n[bold green]✓ Lane worktree ready[/bold green]")
	o.Console("")
	o.Console(bar)
	o.Console(open + "CRITICAL: Change to the lane worktree before editing files" + close)
	o.Console(bar)
	o.Console("")
	o.Console("  [bold]cd " + res.WorkspacePath + "[/bold]")
	o.Console("")
	o.Console("[dim]All file edits, writes, and commits MUST happen in this directory.[/dim]")
	o.Console("[dim]Writing to the main repository instead of the lane worktree is a critical error.[/dim]")
	if res.LaneTestEnv != nil && res.LaneTestEnv.Len() > 0 {
		o.Console("")
		o.Console("[bold cyan]Lane-specific test environment (FR-006):[/bold cyan]")
		keys := res.LaneTestEnv.Keys()
		sort.Strings(keys)
		for _, k := range keys {
			o.Console(fmt.Sprintf("  export %s=%s", k, res.LaneTestEnv.GetString(k)))
		}
		o.Console("[dim]Two parallel SaaS / Django lanes will collide on a single shared test DB unless these are exported in the lane's test process.[/dim]")
	}
}
