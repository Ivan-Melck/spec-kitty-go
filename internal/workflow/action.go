package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// NormalizeWPID is workflow_cores.normalize_wp_id.
func NormalizeWPID(arg string) string {
	up := strings.ToUpper(arg)
	if strings.HasPrefix(up, "WP") {
		return strings.Split(up, "-")[0]
	}
	return "WP" + strings.TrimLeft(up, "WP")
}

// BranchBanner prints _ensure_target_branch_checked_out's banner and returns
// the current branch (exit 1 on a detached HEAD).
func BranchBanner(o *Out, root, slug string) (string, error) {
	cur := gitx.CurrentBranch(root)
	if cur == "" {
		o.Print("Error: Detached HEAD — checkout a branch before continuing.")
		return "", ExitErr(1)
	}
	target := mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("target_branch")
	if target == "" {
		target = cur
	}
	if cur == target {
		o.Printf("Branch: %s (target for this mission)", cur)
	} else {
		o.Printf("Branch: on '%s', mission targets '%s'", cur, target)
	}
	return cur, nil
}

// MissingCanonicalStatus is missing_canonical_status_message.
func MissingCanonicalStatus(wpID, slug string) string {
	return fmt.Sprintf("%s has no canonical status in mission %s. Run `spec-kitty agent mission finalize-tasks --mission %s` to seed the event log.", wpID, slug, slug)
}

// analysisGate is _require_current_analysis_report.
func analysisGate(o *Out, root, slug string) error {
	f := CheckAnalysisReport(root, slug)
	if f.OK {
		return nil
	}
	o.Print("Error: analysis_report_required: /spec-kitty.analyze must be run inside your coding agent (Claude Code, Codex, Cursor) before implementation.")
	switch {
	case f.Reason == "carrier_format_not_wrapped":
		o.Print("  Reason: analysis-report.md is in carrier format (analysis-findings/v1) — written directly\n          rather than via record-analysis. The implement gate requires the persisted\n          outer-wrapper format (artifact_type: spec-kitty.analysis-report).")
		o.Printf("  Recovery: spec-kitty agent mission record-analysis --mission %s --input-file %s", slug, f.Path)
	case f.Missing:
		o.Printf("  Missing: %s", f.Path)
		o.Print("  Run step 1: /spec-kitty.analyze")
		o.Printf("  Run step 2: spec-kitty agent mission record-analysis --mission %s --input-file -", slug)
	case len(f.Mismatches) > 0:
		o.Printf("  Reason: %s", f.Reason)
		o.Print("  Stale inputs:")
		for _, m := range f.Mismatches {
			o.Printf("    - %s", m)
		}
		o.Printf("  Run: /spec-kitty.analyze --mission %s", slug)
	default:
		if f.Reason != "" {
			o.Printf("  Reason: %s", f.Reason)
		}
		o.Printf("  Run: /spec-kitty.analyze --mission %s", slug)
	}
	return ExitErr(1)
}

// nonResolvableReviewRef reports operational sentinels and synthetic tokens.
func nonResolvableReviewRef(ref string) bool {
	if ref == "action-review-claim" || ref == "force-override" {
		return true
	}
	for _, p := range []string{"review:", "approval:", "auto-approval:"} {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// resolveReviewCyclePointer maps review-cycle://<slug>/<wp_slug>/<file> to
// the artifact under the review-cycle surface (coord worktree when routed).
func resolveReviewCyclePointer(s *coord.Surface, ref string) string {
	if !strings.HasPrefix(ref, "review-cycle://") {
		if filepath.IsAbs(ref) {
			if _, err := os.Stat(ref); err == nil {
				return ref
			}
		}
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(ref, "review-cycle://"), "/")
	if len(parts) != 3 {
		return ""
	}
	_, dir, err := review.WriteRoot(s)
	if err != nil {
		return ""
	}
	for _, d := range []string{dir, s.PrimaryDir} {
		p := filepath.Join(d, "tasks", parts[1], parts[2])
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// reviewFeedback is latest_review_feedback_reference: (ref, file, index).
func reviewFeedback(s *coord.Surface, wpID string) (string, string, int) {
	stream, err := status.ReadStream(s.StatusDir(), "")
	if err != nil {
		return "", "", -1
	}
	var evs []*status.Event
	for _, e := range stream.Transitions {
		if e.WPID == wpID {
			evs = append(evs, e)
		}
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ReviewRef == nil {
			continue
		}
		ref := strings.TrimSpace(*evs[i].ReviewRef)
		if ref == "" || nonResolvableReviewRef(ref) {
			continue
		}
		return ref, resolveReviewCyclePointer(s, ref), i
	}
	return "", "", -1
}

// reviewCycleDir is _review_cycle_wp_dir: tasks/<wp_slug> on the review-cycle surface.
func reviewCycleDir(s *coord.Surface, wpSlug string) string {
	_, dir, err := review.WriteRoot(s)
	if err != nil {
		dir = s.PrimaryDir
	}
	return filepath.Join(dir, "tasks", wpSlug)
}

// hasPriorRejection is workflow_cores.has_prior_rejection.
func hasPriorRejection(s *coord.Surface, wpSlug, wpID string) bool {
	dir := reviewCycleDir(s, wpSlug)
	matches, _ := filepath.Glob(filepath.Join(dir, "review-cycle-*.md"))
	if len(matches) == 0 {
		return false
	}
	stream, err := status.ReadStream(s.StatusDir(), "")
	if err != nil {
		return false
	}
	var evs []*status.Event
	for _, e := range stream.Transitions {
		if e.WPID == wpID {
			evs = append(evs, e)
		}
	}
	if len(evs) == 0 {
		return false
	}
	ref, file, idx := reviewFeedback(s, wpID)
	if ref == "" || file == "" || idx < 0 {
		return false
	}
	for _, e := range evs[idx+1:] {
		if e.To == status.Approved || e.To == status.Done {
			return false
		}
	}
	last := evs[len(evs)-1]
	return last.To != status.Approved && last.To != status.Done
}

// ActionImplementOptions are `agent action implement` inputs.
type ActionImplementOptions struct {
	Root, Mission, WPID, Agent string
	Profile                    *string
	Force                      bool
	Note                       *string
	// Dispatch, when set, is resolved at the claim point (after workspace
	// creation) and replaces the binding argument.
	Dispatch *BindingRequest
}

// ResolveBinding is _resolve_dispatch_binding without Op records: an
// explicit-absence binding carrying --profile (and its schema version).
func ResolveBinding(profile *string, profileVersion func(string) (string, error)) (*status.ResolvedBinding, error) {
	b := &status.ResolvedBinding{}
	if profile != nil {
		p := *profile
		b.AgentProfile = &p
		if profileVersion != nil {
			v, err := profileVersion(p)
			if err != nil {
				return nil, fmt.Errorf("Could not resolve --profile %s: %v. Omit --profile to use the work package's own frontmatter agent_profile instead.", mission.PyRepr(p), err)
			}
			b.AgentProfileVersion = &v
		}
	}
	return b, nil
}

// ActionImplement is `agent action implement` after the mission handle is
// resolved (slug) and the binding built.
func ActionImplement(o *Out, rc *Receipts, root, slug string, opt ActionImplementOptions, binding *status.ResolvedBinding) error {
	target, err := BranchBanner(o, root, slug)
	if err != nil {
		return err
	}
	primary := project.Specs(root, slug)
	s := coord.Resolve(root, slug)
	wpID := ""
	if opt.WPID != "" {
		wpID = NormalizeWPID(opt.WPID)
	} else {
		id, reason := PreviewClaimableWP(root, slug)
		if id == "" {
			if reason == "dependencies_not_satisfied" {
				o.Print("Error: dependencies_not_satisfied: planned work packages are waiting on dependencies; all dependencies must be approved or done before implementation can start")
			} else {
				o.Print("Error: No planned work packages found. Specify a WP ID explicitly.")
			}
			return ExitErr(1)
		}
		wpID = id
	}
	wpFile, ferr := wp.Find(primary, wpID)
	if ferr == nil && wpFile == "" {
		ferr = fmt.Errorf("Work package '%s' not found under kitty-specs/%s/tasks (resolved: %s under %s).", wpID, slug, wp.TasksDir(primary), root)
	}
	if ferr != nil {
		o.Printf("Error locating work package: %v", ferr)
		return ExitErr(1)
	}
	w, err := wp.Read(wpFile)
	if err != nil {
		o.Printf("Error locating work package: %v", err)
		return ExitErr(1)
	}
	// locate_work_package's reconstruct_wp_view reads the status stream.
	status.ExtraReadPasses(s.StatusDir(), 1)
	snap := statusSnapshot(s)
	if snap.WP(wpID) == nil {
		o.Printf("Error: %s", MissingCanonicalStatus(wpID, slug))
		return ExitErr(1)
	}
	selfLane := status.ResolveLaneAlias(string(snap.Lane(wpID)))
	if selfLane == "planned" || selfLane == "claimed" {
		if blocked := DependencyBlockers(w.Dependencies(), snap); len(blocked) > 0 {
			o.Printf("Error: %s", DependencyError(wpID, blocked))
			return ExitErr(1)
		}
	}
	feedbackRef, feedbackFile, _ := reviewFeedback(s, wpID)
	hasFeedback := feedbackRef != ""
	if hasFeedback && strings.HasPrefix(feedbackRef, "review-cycle://") && feedbackFile == "" {
		o.Printf("Error: %s review feedback artifact is missing or unreadable: %s", wpID, feedbackRef)
		o.Print("Re-run move-task with --review-feedback-file so the fix cycle can attach the canonical review artifact.")
		return ExitErr(1)
	}
	if err := analysisGate(o, root, slug); err != nil {
		return err
	}
	ws, err := lanes.ResolveWorkspace(root, slug, wpID)
	if err != nil {
		o.Printf("Error: %v", err)
		return ExitErr(1)
	}
	if !ws.RunsInCheckoutRoot() {
		if st, err := os.Stat(ws.WorktreePath); err == nil && st.IsDir() && !ws.Exists() {
			o.Printf("Error: Workspace path %s exists but is not a git worktree (husk). Remove it, then re-run the claim.", ws.WorktreePath)
			return ExitErr(1)
		}
	}
	if ws.Exists() {
		if m, err := lanes.Read(primary); err == nil && m != nil {
			if lane := m.LaneForWP(wpID); lane != nil && lane.LaneID != lanes.PlanningLaneID {
				if err := mergeRecordedPlanningCommit(root, ws.WorktreePath, lane.LaneID, m.PlanningCommitSHA, m.TargetBranch); err != nil {
					o.Printf("Error self-healing workspace for %s: %v", wpID, err)
					return ExitErr(1)
				}
				if err := mergeDependencyLaneTips(o, root, ws.WorktreePath, slug, lane, m); err != nil {
					o.Printf("Error self-healing workspace for %s: %v", wpID, err)
					return ExitErr(1)
				}
				lanes.RecordTip(root, lanes.LaneBranchName(slug, lane.LaneID, m.TargetBranch))
			}
		}
	} else {
		if cwd, _ := os.Getwd(); isLinkedWorktree(cwd) {
			o.Print("Error: Workspace does not exist and cannot be created from a worktree.")
			o.Print("Run this command from the main repository:")
			o.Printf("  spec-kitty agent action implement %s --agent <your-name>", wpID)
			return ExitErr(1)
		}
		o.Printf("Creating workspace for %s...", wpID)
		res, err := TopImplement(o, ImplementOptions{Root: root, MissionDir: slug, WPID: wpID, Actor: opt.Agent})
		if err != nil {
			return err
		}
		PrintWorkspaceReadyBanner(o, res.Workspace)
		if !ws.Exists() {
			o.Printf("Error: implement completed but the workspace at %s for %s was not materialized.", ws.WorktreePath, wpID)
			return ExitErr(1)
		}
	}
	wsPath := ws.WorktreePath
	// Seam C-005: the claim-ancestry gate runs post-materialize, keyed on
	// the merged tip, before any claim event is emitted.
	if anc := ResolveClaimAncestry(root, slug, s.StatusDir(), wpID, wsPath); !anc.OK {
		o.Printf("Error: cannot claim %s: ancestry could not be established after self-heal for: %s", wpID, strings.Join(anc.Missing, ", "))
		return ExitErr(1)
	} else if anc.DeferredTo != "" {
		o.Printf("Planning work package: code dependency lanes are not merged into the repository root checkout on the target branch; they reach %s through `spec-kitty consolidate` (#5296).", anc.DeferredTo)
	}
	subtasks := w.Subtasks()
	subtaskCmd := strings.Join(subtasks, " ")
	if subtaskCmd == "" {
		subtaskCmd = "<subtask-ids>"
	}

	if opt.Dispatch != nil {
		b, err := opt.Dispatch.Resolve(root, slug, wpID, "implement")
		if err != nil {
			o.Printf("Error: %v", err)
			return ExitErr(1)
		}
		binding = b
	}

	// claim transition
	lane, _, _ := currentState(s, wpID)
	assign := ResolveAgent(w.Meta())
	needsAgent := assign.Tool == "unknown"
	fixMode := hasPriorRejection(s, w.Stem(), wpID)
	execMode := ws.StatusExecutionMode()
	if lane != "in_progress" || needsAgent || opt.Agent != "" {
		if opt.Agent == "" && !(lane == "in_progress" && !needsAgent) {
			o.Print("Error: --agent parameter required when starting implementation.")
			o.Printf("  Usage: spec-kitty agent action implement %s --agent <your-name>", wpID)
			o.Print("  Example: spec-kitty agent action implement WP01 --agent claude")
			o.Print("")
			o.Print("If you're using a generated agent command file, --agent is already included.")
			o.Print("This tracks WHO is working on the WP (prevents abandoned tasks).")
			return ExitErr(1)
		}
		pid := ShellPID()
		actor := status.SelfAssertingActor("implementer", opt.Agent, assign.Tool, binding)
		var delta *status.Delta
		if binding != nil {
			d := binding.ToDelta("implementer")
			delta = &d
		}
		agentName := opt.Agent
		if agentName == "" {
			agentName = "unknown"
		}
		var note *string
		if opt.Force && opt.Note != nil {
			n := strings.TrimSpace(*opt.Note)
			note = &n
		}
		if _, err := StartImplementation(s, wpID, actor, execMode+":"+wsPath, execMode, ClaimPolicyMetadata(pid, agentName), delta, true, note); err != nil {
			o.Printf("Error: %v", err)
			if _, ok := err.(*ClaimConflict); ok && lane == "in_review" {
				o.Printf("  %s is under review: wait for the reviewer's verdict, or as an operator re-run with %s.", wpID, status.ForceNoteHint)
			}
			return ExitErr(1)
		}
		if lane == "in_progress" {
			if err := resumeRefresh(s, wpID, agentOr(opt.Agent, assign.Tool), binding); err != nil {
				o.Printf("Error: %v", err)
				return ExitErr(1)
			}
		}
		if err := CommitWorkflowChange(o, rc, root, slug, target, statusPair(s.StatusDir()),
			fmt.Sprintf("chore: Start %s implementation [%s]", wpID, opt.Agent), "planned -> claimed for "+wpID, wpID, true); err != nil {
			return err
		}
		o.Printf("✓ Claimed %s (agent: %s, PID: %d, target: %s)", wpID, opt.Agent, pid, target)
		w, _ = wp.Read(wpFile)
	} else {
		o.Printf("⚠️  %s is already in lane: %s. Action implement will not move it to in_progress.", wpID, lane)
		if err := resumeRefresh(s, wpID, agentOr(opt.Agent, assign.Tool), binding); err != nil {
			o.Printf("Error: %v", err)
			return ExitErr(1)
		}
		if err := CommitWorkflowChange(o, rc, root, slug, target, statusPair(s.StatusDir()),
			fmt.Sprintf("chore: Refresh %s implementation liveness", wpID), "refresh implementation liveness for "+wpID, wpID, true); err != nil {
			return err
		}
	}

	if fixMode {
		if done := tryFixMode(o, s, w.Stem(), feedbackRef, feedbackFile, wsPath, slug, wpID, root); done {
			return errFixModeDone
		}
	}
	missionType := mission.MissionType(mission.ReadMetaOrEmpty(primary))
	lines := buildImplementPrompt(root, slug, wpID, w, ws, wsPath, assign, target, subtaskCmd, hasFeedback, feedbackRef, feedbackFile, missionType)
	promptFile := WriteWorkflowPrompt("implement", slug, wpID, strings.Join(lines, "\n"), root)
	o.Print("")
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
	if hasFeedback {
		o.Printf("⚠️  Has review feedback - read reference: %s", feedbackRef)
		if strings.HasPrefix(feedbackRef, "feedback://") {
			o.Print("   Warning: legacy feedback:// reference detected; readable but deprecated.")
		}
	}
	o.Print("")
	o.Print("▶▶▶ NEXT STEP: Read the full prompt file now:")
	o.Printf("    cat %s", promptFile)
	o.Print("")
	o.Print("After implementation, run:")
	o.Printf("  1. git status && %s", SafeCommitRecipe([]string{"<your-files>"}, fmt.Sprintf("feat(%s): <description>", wpID), ws.BranchName))
	o.Printf("  2. spec-kitty agent tasks mark-status %s --status done --mission %s", subtaskCmd, slug)
	o.Printf("  3. spec-kitty agent tasks move-task %s --to for_review --mission %s --note \"Ready for review\"", wpID, slug)
	o.Print("     (Pre-flight check will verify no uncommitted changes)")
	return nil
}

// errFixModeDone signals the fix-mode early return (no commit summary).
var errFixModeDone = fmt.Errorf("fix-mode prompt rendered")

// IsFixModeDone reports the fix-mode early-return sentinel.
func IsFixModeDone(err error) bool { return err == errFixModeDone }

func agentOr(agent, tool string) string {
	if agent != "" {
		return agent
	}
	if tool != "" {
		return tool
	}
	return "unknown"
}

func isLinkedWorktree(dir string) bool {
	gd := gitOut(dir, "rev-parse", "--git-dir")
	cd := gitOut(dir, "rev-parse", "--git-common-dir")
	return gd != "" && cd != "" && filepath.Clean(gd) != filepath.Clean(cd) && !strings.HasSuffix(gd, "/.git") && gd != ".git"
}

// resumeRefresh is _implement_emit_resume_refresh.
func resumeRefresh(s *coord.Surface, wpID, actor string, binding *status.ResolvedBinding) error {
	pid := ShellPID()
	var d status.Delta
	if binding != nil {
		d = binding.ToDelta("implementer")
	}
	setDeltaPID(&d, pid)
	_, _, err := s.AppendAnnotationUncommitted(wpID, d, status.StrActor(actor))
	return err
}

// setDeltaPID sets shell_pid / shell_pid_created_at on a delta.
func setDeltaPID(d *status.Delta, pid int) {
	p := pid
	d.ShellPID = &p
	if b, ok := status.CreationTimeBaseline(pid); ok {
		d.ShellPIDCreatedAt = &b
	}
}

func tryFixMode(o *Out, s *coord.Surface, wpSlug, ref, file, wsPath, slug, wpID, root string) bool {
	dir := reviewCycleDir(s, wpSlug)
	var a *ReviewCycleArtifact
	if strings.HasPrefix(ref, "review-cycle://") && file != "" {
		var err error
		a, err = LoadReviewCycle(file)
		if err != nil {
			a = nil
		}
	} else {
		a, _ = LatestReviewCycle(dir)
	}
	if a == nil {
		return false
	}
	o.Console(fmt.Sprintf("[bold]Fix mode[/bold]: generating focused prompt from review-cycle-%d (Canonical feedback: %s)", a.CycleNumber,
		filepath.Join(dir, fmt.Sprintf("review-cycle-%d.md", a.CycleNumber))))
	p := WriteWorkflowPrompt("implement", slug, wpID, FixPrompt(a, wsPath, slug, wpID), root)
	o.Print("")
	o.Printf("📍 Workspace: cd %s", wsPath)
	o.Printf("🔧 Fix mode — Cycle %d: focused prompt from review artifact", a.CycleNumber)
	o.Print("")
	o.Print("▶▶▶ NEXT STEP: Read the full fix-mode prompt file now:")
	o.Printf("    cat %s", p)
	o.Print("")
	return true
}

func buildImplementPrompt(root, slug, wpID string, w *wp.WP, ws *lanes.Workspace, wsPath string, assign AgentAssignment,
	target, subtaskCmd string, hasFeedback bool, feedbackRef, feedbackFile, missionType string) []string {
	bar := strings.Repeat("=", 80)
	var l []string
	l = append(l, bar, "IMPLEMENT: "+wpID, bar, "", "Source: "+w.Path, "", "Workspace: "+wsPath, WorkspaceContractDescription(ws, wpID), "")
	l = append(l, RenderResolvedAgentIdentity(assign)...)
	l = append(l, "", CharterContextText(root, "implement", missionType), "")
	l = append(l, IsolationBanner(wpID, "implement")...)
	l = append(l, "")
	if topo, err := TopologyJSON(root, slug, wpID); err != nil {
		l = append(l, fmt.Sprintf("[Topology unavailable: %v]", err), "")
	} else if topo != nil {
		l = append(l, topo...)
		l = append(l, "")
	}
	recipe := SafeCommitRecipe([]string{"<your-implementation-files>"}, fmt.Sprintf("feat(%s): <brief description>", wpID), ws.BranchName)
	l = append(l, bar, "WHEN YOU'RE DONE:", bar, "✓ Implementation complete and tested:", "  1. **Commit your implementation files:**",
		"     git status  # Check what you changed", "     "+recipe+"  # NOT WP status files", "     git log -1 --oneline  # Verify commit succeeded",
		"  2. Mark all subtasks as done:", fmt.Sprintf("     spec-kitty agent tasks mark-status %s --status done --mission %s", subtaskCmd, slug),
		"  3. Move WP to review:", fmt.Sprintf("     spec-kitty agent tasks move-task %s --to for_review --mission %s --note \"Ready for review\"", wpID, slug),
		"", "✗ Blocked or cannot complete:", fmt.Sprintf("  spec-kitty agent tasks add-history %s --mission %s --note \"Blocked: <reason>\"", wpID, slug),
		bar, "", "📍 WORKING DIRECTORY:", "   cd "+wsPath)
	if ws.LaneID != "" {
		l = append(l, "   # All implementation work happens in this workspace", "   # When done, return to repo root: cd "+root)
	} else {
		l = append(l, "   # Planning-artifact work for this WP happens in the repository root")
	}
	l = append(l, "")
	l = append(l, SharedArtifactGuidance(ws, root, slug)...)
	l = append(l, "", "📋 STATUS TRACKING:", fmt.Sprintf("   kitty-specs/ status is tracked in %s branch (visible to all agents)", target),
		"   Status changes auto-commit to the coordination branch (visible to all agents)",
		"   ⚠️  You will see commits from other agents - IGNORE THEM", bar, "")
	if hasFeedback {
		l = append(l, "⚠️  This work package has review feedback.")
		l = append(l, "   Canonical feedback reference: "+feedbackRef)
		if strings.HasPrefix(feedbackRef, "feedback://") {
			l = append(l, "   WARNING: legacy feedback:// reference detected; readable but deprecated.")
		}
		if feedbackFile != "" {
			l = append(l, fmt.Sprintf("   Read it first: cat \"%s\"", feedbackFile))
		} else {
			l = append(l, "   WARNING: review feedback reference is set, but the artifact is missing/unreadable.",
				"   Ask reviewer to re-run move-task with --review-feedback-file.")
		}
		l = append(l, "")
	}
	if missionType == "research" {
		deliv := "docs/research/" + slug + "/"
		l = append(l, researchBanner(deliv, target)...)
	}
	raw, _ := os.ReadFile(w.Path)
	l = append(l, WPPromptWrapper(string(raw))...)
	l = append(l, bar, "🎯 IMPLEMENTATION COMPLETE? RUN THESE COMMANDS:", bar, "", "✅ Implementation complete and tested:",
		"   1. **Commit your implementation files:**", "      git status  # Check what you changed", "      "+recipe+"  # NOT WP status files",
		"      git log -1 --oneline  # Verify commit succeeded", "      (Use fix: for bugs, chore: for maintenance, docs: for documentation)",
		"   2. Mark all subtasks as done:", fmt.Sprintf("      spec-kitty agent tasks mark-status %s --status done --mission %s", subtaskCmd, slug),
		"   3. Move WP to review (will check for uncommitted changes):",
		fmt.Sprintf("      spec-kitty agent tasks move-task %s --to for_review --mission %s --note \"Ready for review: <summary>\"", wpID, slug),
		"", "⚠️  Blocked or cannot complete:", fmt.Sprintf("   spec-kitty agent tasks add-history %s --mission %s --note \"Blocked: <reason>\"", wpID, slug),
		"", "⚠️  NOTE: The move-task command will FAIL if you have uncommitted changes!",
		"     Commit all implementation files BEFORE moving to for_review.", "     Dependent work packages need your committed changes.", bar)
	return l
}

func researchBanner(deliv, target string) []string {
	return []string{"╔" + strings.Repeat("=", 78) + "╗",
		"║  🔬 RESEARCH MISSION - TWO ARTIFACT TYPES                                 ║",
		"╠" + strings.Repeat("=", 78) + "╣",
		"║                                                                          ║",
		"║  📁 RESEARCH DELIVERABLES (your output):                                 ║",
		"║     " + ljust(deliv, 69) + " ║",
		"║     ↳ Create findings, reports, data here                                ║",
		"║     ↳ Commit to worktree branch                                          ║",
		"║     ↳ Will merge to " + ljust(target, 62) + " ║",
		"║                                                                          ║",
		"║  📋 PLANNING ARTIFACTS (kitty-specs/):                                   ║",
		"║     ↳ evidence-log.csv, source-register.csv                              ║",
		"║     ↳ Edit in planning repo (rare during implementation)                 ║",
		"║                                                                          ║",
		"║  ⚠️  DO NOT put research deliverables in kitty-specs/!                   ║",
		"╚" + strings.Repeat("=", 78) + "╝", ""}
}

// PreviewClaimableWP is runtime.next.discovery.preview_claimable_wp: the
// first planned WP (file order) whose dependencies are satisfied; reason
// "dependencies_not_satisfied" when planned WPs exist but none is ready.
func PreviewClaimableWP(root, slug string) (string, string) {
	primary := project.Specs(root, slug)
	if st, err := os.Stat(filepath.Join(primary, "tasks")); err != nil || !st.IsDir() {
		return "", ""
	}
	snap := statusSnapshot(coord.Resolve(root, slug))
	files := wp.Files(primary)
	sort.Strings(files)
	anyPlanned := false
	for _, p := range files {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		id := w.String("work_package_id")
		if id == "" {
			id = wp.IDFromFilename(filepath.Base(p))
		}
		lane := string(status.Planned)
		if l := snap.Lane(id); l != "" {
			lane = string(l)
		}
		if lane != "planned" {
			continue
		}
		anyPlanned = true
		if len(DependencyBlockers(w.Dependencies(), snap)) == 0 {
			return id, ""
		}
	}
	if anyPlanned {
		return "", "dependencies_not_satisfied"
	}
	return "", "no_planned"
}

var _ = strconv.Itoa
var _ = jsonx.NewObject
