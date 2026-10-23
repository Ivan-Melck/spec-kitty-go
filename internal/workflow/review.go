package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionwrite"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// isReviewClaimEvent is workflow_cores.event_is_review_claim.
func isReviewClaimEvent(e *status.Event) bool {
	if e.To == status.InReview {
		return true
	}
	return e.To == status.InProgress && e.ReviewRef != nil && *e.ReviewRef == "action-review-claim"
}

func wpEvents(s *coord.Surface, wpID string) []*status.Event {
	stream, err := status.ReadStream(s.StatusDir(), "")
	if err != nil {
		return nil
	}
	var out []*status.Event
	for _, e := range stream.Transitions {
		if e.WPID == wpID {
			out = append(out, e)
		}
	}
	return out
}

// findFirstForReviewWP is _find_first_for_review_wp.
func findFirstForReviewWP(root, slug string) string {
	primary := project.Specs(root, slug)
	files := wp.Files(primary)
	sort.Strings(files)
	s := coord.Resolve(root, slug)
	snap := statusSnapshot(s)
	ids := []string{}
	for _, p := range files {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		ids = append(ids, w.String("work_package_id"))
	}
	for _, id := range ids {
		if id != "" && snap.Lane(id) == status.ForReview {
			return id
		}
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		l := snap.Lane(id)
		if l != status.InProgress && l != status.InReview {
			continue
		}
		evs := wpEvents(s, id)
		if len(evs) > 0 && isReviewClaimEvent(evs[len(evs)-1]) {
			return id
		}
	}
	return ""
}

// reviewContext is _resolve_review_context.
type reviewContext struct {
	BranchName, BaseBranch, MissionBranch, LaneBranch, BaseRef string
	CommitCount                                                int
}

func resolveReviewContext(root, slug, wpID, wsPath string, w *wp.WP) *reviewContext {
	ctx := &reviewContext{BranchName: "unknown", BaseBranch: "unknown", MissionBranch: "unknown", LaneBranch: "unknown", BaseRef: "unknown"}
	if _, err := os.Stat(wsPath); err != nil {
		return ctx
	}
	ws, err := lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		return ctx
	}
	m, _ := lanes.Read(project.Specs(root, slug))
	if m != nil && m.MissionBranch != "" {
		ctx.MissionBranch = m.MissionBranch
	} else if ws.Context != nil && ws.Context.BaseBranch != "" {
		ctx.MissionBranch = ws.Context.BaseBranch
	}
	if ws.ResolutionKind == "repo_root" {
		return ctx
	}
	branch := gitOut(wsPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branch == "" {
		return ctx
	}
	ctx.BranchName, ctx.LaneBranch = branch, branch
	base := "unknown"
	if ws.Context != nil && ws.Context.BaseBranch != "" {
		base = ws.Context.BaseBranch
	} else if ctx.MissionBranch != "unknown" {
		base = ctx.MissionBranch
	}
	if base == "unknown" {
		return ctx
	}
	n, _ := strconv.Atoi(gitOut(root, "rev-list", "--count", base+".."+branch))
	ctx.BaseBranch, ctx.BaseRef, ctx.CommitCount = base, base, n
	return ctx
}

// reviewDependentsWarning is review_compute_dependents_warning.
func reviewDependentsWarning(root, slug, wpID string) []string {
	primary := project.Specs(root, slug)
	var dependents []string
	for _, p := range wp.Files(primary) {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		for _, d := range w.Dependencies() {
			if d == wpID {
				dependents = append(dependents, w.String("work_package_id"))
				break
			}
		}
	}
	if len(dependents) == 0 {
		return nil
	}
	snap := statusSnapshot(coord.Resolve(root, slug))
	var inc []string
	for _, d := range dependents {
		l := snap.Lane(d)
		if l == "" {
			l = status.Planned
		}
		if l == status.Planned || l == status.InProgress || l == status.ForReview {
			inc = append(inc, d)
		}
	}
	if len(inc) == 0 {
		return nil
	}
	sort.Strings(inc)
	return []string{fmt.Sprintf("⚠️  Dependency Alert: %s depend on %s (not yet done)", strings.Join(inc, ", "), wpID),
		"   If you request changes, notify those agents to rebase."}
}

// reviewCompletionReviewer is resolve_review_completion_reviewer.
func reviewCompletionReviewer(s *coord.Surface, wpID string) string {
	evs := wpEvents(s, wpID)
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].To == status.InReview {
			if id := strings.TrimSpace(evs[i].Actor.Identity()); id != "" {
				return id
			}
		}
	}
	return "user"
}

func reviewCommands(wpID, slug, feedbackPath, reviewer, approveNote string) (string, string) {
	approve := fmt.Sprintf("spec-kitty agent tasks move-task %s --to approved --mission %s --agent %s --note \"%s\"", wpID, slug, reviewer, approveNote)
	reject := fmt.Sprintf("spec-kitty agent tasks move-task %s --to planned --review-feedback-file %s --mission %s --agent %s", wpID, feedbackPath, slug, reviewer)
	return approve, reject
}

// acquireReviewLock is ReviewLock.acquire (.spec-kitty/review-lock.json).
func acquireReviewLock(wsPath, wpID, agent string) error {
	p := filepath.Join(wsPath, ".spec-kitty", "review-lock.json")
	if b, err := os.ReadFile(p); err == nil {
		if o, err := jsonx.DecodeObject(b); err == nil {
			pid, _ := o.GetInt("pid")
			if pid > 0 && status.IsProcessAlive(pid) {
				return fmt.Errorf("Worktree %s has an active review by agent '%s' on %s (PID %d, started %s). Wait for that review to complete or use a different lane.",
					wsPath, o.GetString("agent"), o.GetString("wp_id"), pid, o.GetString("started_at"))
			}
		}
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	o := jsonx.NewObject("worktree_path", wsPath, "wp_id", wpID, "agent", agent, "started_at", clock.NowUTCISO(), "pid", os.Getpid())
	return os.WriteFile(p, []byte(jsonx.Dumps(o, jsonx.Pretty)), 0o644)
}

// ActionReview is `agent action review` after mission resolution.
func ActionReview(o *Out, rc *Receipts, root, slug, rawWP, agent string, binding *status.ResolvedBinding) error {
	return ActionReviewDispatch(o, rc, root, slug, rawWP, agent, binding, nil)
}

// ActionReviewDispatch is ActionReview with the dispatch binding resolved at
// the claim point (req != nil replaces binding).
func ActionReviewDispatch(o *Out, rc *Receipts, root, slug, rawWP, agent string, binding *status.ResolvedBinding, req *BindingRequest) error {
	target, err := BranchBanner(o, root, slug)
	if err != nil {
		return err
	}
	primary := project.Specs(root, slug)
	s := coord.Resolve(root, slug)
	wpID := ""
	if rawWP != "" {
		wpID = NormalizeWPID(rawWP)
	} else {
		wpID = findFirstForReviewWP(root, slug)
		if wpID == "" {
			o.Print("Error: No work packages ready for review. Specify a WP ID explicitly.")
			return ExitErr(1)
		}
	}
	wpFile, ferr := wp.Find(primary, wpID)
	if ferr == nil && wpFile == "" {
		ferr = fmt.Errorf("Work package '%s' not found under kitty-specs/%s/tasks (resolved: %s under %s).", wpID, slug, wp.TasksDir(primary), root)
	}
	if ferr != nil {
		o.Printf("Error: %v", ferr)
		return ExitErr(1)
	}
	w, err := wp.Read(wpFile)
	if err != nil {
		o.Printf("Error: %v", err)
		return ExitErr(1)
	}
	// locate_work_package's reconstruct_wp_view reads the status stream.
	status.ExtraReadPasses(s.StatusDir(), 1)
	snap := statusSnapshot(s)
	if snap.WP(wpID) == nil {
		o.Printf("Error: %s", MissingCanonicalStatus(wpID, slug))
		return ExitErr(1)
	}
	lane := string(snap.Lane(wpID))
	ws, err := lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		o.Printf("Error: %v", err)
		return ExitErr(1)
	}
	evs := wpEvents(s, wpID)
	claimed := len(evs) > 0 && isReviewClaimEvent(evs[len(evs)-1])
	if lane == "in_progress" && !claimed {
		o.Printf("Error: %s is still being implemented, not claimed for review.", wpID)
		o.Print("Only work packages in 'for_review' (or already review-claimed in_review) can start workflow review.")
		o.Printf("Move it first: spec-kitty agent tasks move-task %s --to for_review --mission %s", wpID, slug)
		return ExitErr(1)
	}
	if lane != "for_review" && lane != "in_review" && !claimed {
		o.Printf("Error: %s is in lane '%s', not 'for_review'.", wpID, lane)
		o.Print("Only work packages in 'for_review' (or already claimed for review) can start workflow review.")
		o.Printf("Move it first: spec-kitty agent tasks move-task %s --to for_review --mission %s", wpID, slug)
		return ExitErr(1)
	}
	// #5758: the lane is brought to the remote's view before the review
	// claim, so a refusal leaves the WP status untouched.
	createFrom, rerr := reconcileReviewLane(o, ws, root, wpID, agent)
	if rerr != nil {
		if _, ok := rerr.(*reviewLaneRefusal); ok {
			o.Console("[red]" + rich.EscapeMarkup(rerr.Error()) + "[/red]")
		} else {
			o.Console(rerr.Error())
		}
		return ExitErr(1)
	}

	if req != nil {
		b, err := req.Resolve(root, slug, wpID, "review")
		if err != nil {
			o.Printf("Error: %v", err)
			return ExitErr(1)
		}
		binding = b
	}

	// review claim
	if lane != "for_review" && !(lane == "in_review" && agent != "") {
		o.Printf("⚠️  %s is already in lane: %s. Workflow review will not move it to in_review.", wpID, lane)
	} else {
		if agent == "" {
			o.Print("Error: --agent parameter required when starting review.")
			o.Printf("  Usage: spec-kitty agent action review %s --agent <your-name>", wpID)
			o.Print("  Example: spec-kitty agent action review WP01 --agent claude")
			o.Print("")
			o.Print("If you're using a generated agent command file, --agent is already included.")
			o.Print("This tracks WHO is reviewing the WP (prevents abandoned reviews).")
			return ExitErr(1)
		}
		pid := ShellPID()
		pm := ClaimPolicyMetadata(pid, agent)
		var d status.Delta
		p := pid
		d.ShellPID = &p
		if v, ok := pm.Get("shell_pid_created_at"); ok {
			if sv, ok := v.(string); ok {
				d.ShellPIDCreatedAt = &sv
			}
		}
		a := agent
		d.Agent = &a
		if binding != nil {
			bd := binding.ToDelta("reviewer")
			d.Role, d.AgentProfile, d.AgentProfileVersion, d.Model, d.Provider = bd.Role, bd.AgentProfile, bd.AgentProfileVersion, bd.Model, bd.Provider
		}
		actor := status.SelfAssertingActor("reviewer", agent, "", binding)
		rb := missionwrite.Capture(s.StatusDir())
		if _, err := StartReview(s, wpID, actor, "action-review:"+root, ws.StatusExecutionMode(), pm, &d); err != nil {
			o.Printf("Error: %v", err)
			return ExitErr(1)
		}
		if lane == "in_review" {
			if _, _, err := s.AppendAnnotationUncommitted(wpID, d, status.StrActor(agent)); err != nil {
				o.Printf("Error: %v", err)
				return ExitErr(1)
			}
		}
		paths := append([]string{w.Path}, statusPair(s.StatusDir())...)
		if err := CommitWorkflowChange(o, rc, root, slug, target, paths, fmt.Sprintf("chore: Start %s review [%s]", wpID, agent),
			"for_review -> in_review for "+wpID, wpID, true, rb); err != nil {
			return err
		}
		o.Printf("✓ Claimed %s for review (agent: %s, PID: %d, target: %s)", wpID, agent, pid, target)
		w, _ = wp.Read(wpFile)
	}

	// review workspace
	ws, err = lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		o.Printf("Error: %v", err)
		return ExitErr(1)
	}
	wsPath := ws.WorktreePath
	if !ws.Exists() {
		if st, err := os.Stat(wsPath); err == nil && st.IsDir() && !ws.RunsInCheckoutRoot() {
			o.Printf("Error: Workspace path %s exists but is not a git worktree (husk). Remove it, then re-run the claim.", wsPath)
			return ExitErr(1)
		}
		os.MkdirAll(filepath.Join(root, ".worktrees"), 0o755)
		if ws.BranchName == "" {
			o.Printf("Error: cannot create review workspace %s for %s: resolved workspace has no branch name.", wsPath, wpID)
			return ExitErr(1)
		}
		args := []string{"worktree", "add", wsPath, ws.BranchName}
		created := false
		if git(root, "rev-parse", "--verify", ws.BranchName).Code != 0 {
			args = []string{"worktree", "add", wsPath, "-b", ws.BranchName}
			if createFrom != "" {
				args = append(args, createFrom)
			}
			created = true
		}
		if r := git(root, args...); r.Code != 0 {
			o.Printf("Error: could not create review workspace %s for %s: `git %s` failed: %s", wsPath, wpID, strings.Join(args, " "), strings.TrimSpace(r.Stderr))
			return ExitErr(1)
		}
		o.Printf("✓ Created workspace: %s", wsPath)
		if created && createFrom != "" {
			o.Printf("Created review workspace from %s", strings.TrimPrefix(createFrom, "refs/remotes/"))
		}
	}
	if err := acquireReviewLock(wsPath, wpID, agentOr(agent, "unknown")); err != nil {
		o.Console(err.Error())
		return ExitErr(1)
	}
	rctx := resolveReviewContext(root, slug, wpID, wsPath, w)
	deps := reviewDependentsWarning(root, slug, wpID)
	assign := ResolveAgent(w.Meta())
	wpSlug := w.Stem()
	cycleDir := reviewCycleDir(s, wpSlug)
	os.MkdirAll(cycleDir, 0o755)
	n, err := review.NextCycleNumber(cycleDir, filepath.Join(primary, "tasks", wpSlug))
	if err != nil {
		o.Printf("Error: cannot determine the next review-cycle number for %s: %v\nNote: %s is already claimed for review (in_review); remove or rename the unparseable sibling file named above, then re-run this same review command to resume.", wpID, err, wpID)
		return ExitErr(1)
	}
	feedbackPath := filepath.Join(cycleDir, fmt.Sprintf("review-feedback-%d.md", n))
	reviewer := reviewCompletionReviewer(s, wpID)
	lines := buildReviewPrompt(root, slug, wpID, w, ws, wsPath, assign, target, deps, rctx, feedbackPath, reviewer)
	meta := mission.ReadMetaOrEmpty(primary)
	mb := rctx.MissionBranch
	if mb == "" || mb == "unknown" {
		mb = target
	}
	lb := rctx.LaneBranch
	if lb == "unknown" {
		lb = rctx.BranchName
	}
	if lb == "unknown" || lb == "" {
		lb = "HEAD"
	}
	br := rctx.BaseRef
	if br == "unknown" {
		br = rctx.BaseBranch
	}
	if br == "unknown" || br == "" {
		br = target
	}
	absWS, _ := filepath.Abs(wsPath)
	pm := NewReviewPromptMeta(root, meta.GetString("mission_id"), slug, wpID, absWS, mb, lb, br)
	promptFile, err := pm.Write(strings.Join(lines, "\n"))
	if err != nil {
		o.Printf("Error: %v", err)
		return ExitErr(1)
	}
	o.Print("")
	if len(deps) > 0 {
		for _, l := range deps {
			o.Print(l)
		}
		o.Print("")
	}
	o.Printf("📍 Workspace: cd %s", wsPath)
	if ws.LaneID != "" {
		shared := strings.Join(ws.LaneWPIDs, ", ")
		if len(ws.LaneWPIDs) == 0 {
			shared = wpID
		}
		o.Printf("   Lane workspace: %s (shared by %s)", ws.LaneID, shared)
	} else {
		o.Print("   Repository-root planning workspace")
	}
	if rctx.BaseBranch != "unknown" {
		o.Printf("🔀 Branch: %s (based on %s, %d commits)", rctx.BranchName, rctx.BaseBranch, rctx.CommitCount)
		o.Printf("   Review diff: git log %s..%s --oneline", rctx.BaseBranch, rctx.BranchName)
	} else if ws.ResolutionKind == "repo_root" {
		o.Print("🔀 Review diff unavailable: no deterministic implementation claim commit found for this WP")
	}
	o.Print("")
	o.Print("▶▶▶ NEXT STEP: Read the full prompt file now:")
	o.Printf("    cat %s", promptFile)
	approve, reject := reviewCommands(wpID, slug, feedbackPath, reviewer, "Review passed")
	o.Print("")
	o.Print("After review, run:")
	o.Printf("  ✅ %s", approve)
	o.Printf("  ❌ %s", reject)
	return nil
}

func buildReviewPrompt(root, slug, wpID string, w *wp.WP, ws *lanes.Workspace, wsPath string, assign AgentAssignment, target string,
	deps []string, rctx *reviewContext, feedbackPath, reviewer string) []string {
	bar := strings.Repeat("=", 80)
	var l []string
	l = append(l, bar, "REVIEW: "+wpID, bar, "", "Source: "+w.Path, "", "Workspace: "+wsPath, WorkspaceContractDescription(ws, wpID), "")
	l = append(l, RenderResolvedAgentIdentity(assign)...)
	l = append(l, "")
	mt := mission.MissionType(mission.ReadMetaOrEmpty(project.Specs(root, slug)))
	l = append(l, CharterContextText(root, "review", mt), "")
	if len(deps) > 0 {
		l = append(l, deps...)
		l = append(l, "")
	}
	l = append(l, IsolationBanner(wpID, "review")...)
	l = append(l, "")
	if topo, err := TopologyJSON(root, slug, wpID); err != nil {
		l = append(l, fmt.Sprintf("[Topology unavailable: %v]", err), "")
	} else if topo != nil {
		l = append(l, topo...)
		l = append(l, "")
	}
	if rctx.BaseBranch != "unknown" {
		base, br := rctx.BaseBranch, rctx.BranchName
		l = append(l, "─── GIT REVIEW CONTEXT "+strings.Repeat("─", 57), "Branch:      "+br,
			fmt.Sprintf("Base branch: %s (%d commits ahead)", base, rctx.CommitCount), "", "Review commands (run in the workspace):",
			"  cd "+wsPath,
			fmt.Sprintf("  git log %s..%s --oneline           # WP commits only", base, br),
			fmt.Sprintf("  git diff %s..%s --stat             # Changed files", base, br),
			fmt.Sprintf("  git diff %s..%s                    # Full diff", base, br),
			strings.Repeat("─", 80), "")
	} else if ws.ResolutionKind == "repo_root" {
		l = append(l, "─── GIT REVIEW CONTEXT "+strings.Repeat("─", 57),
			"Review commands unavailable: no deterministic implementation claim commit found for this WP.",
			"Re-run review after the WP has a committed implementation claim on this mission.", strings.Repeat("─", 80), "")
	}
	approve, reject := reviewCommands(wpID, slug, feedbackPath, reviewer, "Review passed")
	l = append(l, bar, "WHEN YOU'RE DONE:", bar, "✓ Review passed, no issues:", "  "+approve, "", "⚠️  Changes requested:",
		"  1. Write feedback to (in-repo, committed with the project):", "     "+feedbackPath, "  2. "+reject,
		"  3. move-task stores feedback reference in the event log and WP frontmatter", bar, "", "📍 WORKING DIRECTORY:", "   cd "+wsPath)
	if ws.LaneID != "" {
		l = append(l, "   # Review the implementation in this workspace", "   # Read code, run tests, check against requirements", "   # When done, return to repo root: cd "+root)
	} else {
		l = append(l, "   # Review the planning-artifact changes directly in the repository root")
	}
	l = append(l, "")
	l = append(l, SharedArtifactGuidance(ws, root, slug)...)
	l = append(l, "", "📋 STATUS TRACKING:", fmt.Sprintf("   kitty-specs/ status is tracked in %s branch (visible to all agents)", target),
		"   Status changes auto-commit to the coordination branch (visible to all agents)",
		"   ⚠️  You will see commits from other agents - IGNORE THEM", bar, "",
		"Review the implementation against the requirements below.", "Check code quality, tests, documentation, and adherence to spec.", "",
		ReviewAntipatternChecklist, "")
	raw, _ := os.ReadFile(w.Path)
	l = append(l, WPPromptWrapper(string(raw))...)
	approve2, reject2 := reviewCommands(wpID, slug, feedbackPath, reviewer, "Review passed: <summary>")
	l = append(l, bar, "🎯 REVIEW COMPLETE? RUN ONE OF THESE COMMANDS:", bar, "", "✅ APPROVE (no issues found):", "   "+approve2, "",
		"❌ REQUEST CHANGES (issues found):", "   1. Write feedback to the in-repo path (committed with the project):",
		"      cat > "+feedbackPath+" <<'EOF'", "**Issue 1**: <description and how to fix>", "**Issue 2**: <description and how to fix>", "EOF", "",
		"   2. Move to planned with feedback:", "      "+reject2, "", "⚠️  NOTE: You MUST run one of these commands to complete the review!",
		"     The Python script handles all file updates automatically.", bar)
	return l
}
