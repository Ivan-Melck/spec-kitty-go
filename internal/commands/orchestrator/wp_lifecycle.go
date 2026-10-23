package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/commands/agentcore"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// transitionRequiresPolicy is _transition_requires_policy.
func transitionRequiresPolicy(lane string) bool {
	b := status.ProgressBucket(status.Lane(lane))
	return b != "not_started" && b != "terminal" && lane != string(status.Blocked)
}

// wpDependencies is parse_wp_dependencies(wp_path).
func wpDependencies(path string) []string {
	w, err := wp.Read(path)
	if err != nil {
		return []string{}
	}
	d := w.Dependencies()
	if d == nil {
		return []string{}
	}
	return d
}

// claimedBy extracts the holder from a WorkPackageClaimConflict message.
func claimedBy(msg string) string {
	if i := strings.LastIndex(msg, " by '"); i >= 0 && strings.HasSuffix(msg, "'") {
		return msg[i+5 : len(msg)-1]
	}
	if i := strings.Index(msg, "implementer of record ("); i >= 0 {
		rest := msg[i+len("implementer of record ("):]
		if j := strings.Index(rest, ")"); j >= 0 {
			if h := rest[:j]; h != "none on record" {
				return h
			}
		}
	}
	return "unknown"
}

func (v *verb) claimFailure(m *orch.Mission, err error, actor string) error {
	var cc *workflow.ClaimConflict
	if errors.As(err, &cc) {
		return v.fail("WP_ALREADY_CLAIMED", cc.Msg, identity(m.StatusDir, "claimed_by", claimedBy(cc.Msg), "requesting_actor", actor))
	}
	return v.fail("TRANSITION_REJECTED", err.Error(), nil)
}

// resolveStartWorkspace is _resolve_start_workspace (allocating the lane
// worktree when needed).
func (v *verb) resolveStartWorkspace(m *orch.Mission, handle, wpID string) (*startWorkspace, error) {
	manifest, l, legacy := laneAssignment(m, handle, wpID)
	if legacy != nil {
		return legacy, nil
	}
	base := lanes.LaneBaseRef(m.Root, m.Slug, manifest)
	if l.LaneID == lanes.PlanningLaneID {
		if err := v.ensureRepoRootCheckoutOrFail(m, handle, wpID); err != nil {
			return nil, err
		}
		recordClaimBase(m.Root, handle, wpID)
		return &startWorkspace{path: m.Root, laneID: l.LaneID,
			laneBranch: lanes.SingleBranchWriteRef(m.Root, m.Slug, manifest.TargetBranch), laneBaseRef: base}, nil
	}
	o := workflow.NewOut(v.ctx.Stderr, v.ctx.Stderr)
	wt, branch, err := workflow.AllocateLaneWorktreeFor(o, m.Root, handle, m.Slug, wpID, manifest)
	if err != nil {
		data := identity(m.StatusDir, "wp_id", wpID, "reason", err.Error())
		return nil, v.fail("LANE_ALLOCATION_FAILED", err.Error(), data)
	}
	return &startWorkspace{path: wt, laneID: l.LaneID, laneBranch: branch, laneBaseRef: base}, nil
}

// recordClaimBase is lanes.claim_base.record_claim_base (idempotent by
// absence): refs/spec-kitty/wp-base/<mission>/<wp> at HEAD.
func recordClaimBase(root, slug, wpID string) {
	if lanes.ReadClaimBase(root, slug, wpID) != "" {
		return
	}
	head, err := gitx.HeadSHA(root)
	if err != nil {
		return
	}
	gitx.Run(root, "update-ref", "refs/spec-kitty/wp-base/"+slug+"/"+wpID, strings.TrimSpace(head))
}

// enforceClaimAncestry is _enforce_claim_ancestry: the shared
// post-materialize claim-ancestry gate (recorded planning commit + approved
// dependency-lane tips, with one self-heal re-entry).
func (v *verb) enforceClaimAncestry(m *orch.Mission, wpID, workspace string) error {
	r := workflow.ResolveClaimAncestry(m.Root, m.Slug, m.StatusDir, wpID, workspace)
	if r.OK {
		return nil
	}
	return v.fail("ANCESTRY_NOT_ESTABLISHED",
		fmt.Sprintf("cannot claim %s: ancestry could not be established after self-heal for: %s", wpID, strings.Join(r.Missing, ", ")),
		identity(m.StatusDir, "wp_id", wpID, "missing_refs", strList(r.Missing)))
}

// surfaceFor resolves the mission's coordination surface; status events are
// stamped with the raw --mission handle like upstream's TransitionRequest
// (mission_slug=mission).
func surfaceFor(m *orch.Mission, handle string) *coord.Surface {
	s := coord.Resolve(m.Root, m.Slug)
	s.Slug = handle
	return s
}

// forReviewGate is evaluate_for_review_gate(main_repo_root, mission, wp):
// the lane worktree / branch names key on the raw handle, the manifest and
// base ref on the canonical mission.
func forReviewGate(m *orch.Mission, handle, wpID string, force bool) lanes.GateDecision {
	if force {
		return lanes.GateDecision{Passed: true}
	}
	manifest, err := lanes.Read(m.PrimaryDir)
	if err != nil || manifest == nil {
		return lanes.GateDecision{Passed: true}
	}
	l := manifest.LaneForWP(wpID)
	if l == nil {
		return lanes.GateDecision{Passed: true}
	}
	if l.LaneID == lanes.PlanningLaneID {
		base := lanes.ReadClaimBase(m.Root, handle, wpID)
		checkout := m.Root
		if ws, err := lanes.ResolveWorkspace(m.Root, m.Slug, wpID); err == nil {
			checkout = ws.WorktreePath
		}
		if base != "" && qualifyingCommitSince(checkout, base, handle) {
			return lanes.GateDecision{Passed: true, LaneID: l.LaneID, LaneBranch: manifest.TargetBranch, BaseRef: base}
		}
		shown := base
		if shown == "" {
			shown = "<no recorded claim base>"
		}
		return lanes.GateDecision{Reason: fmt.Sprintf("%s cannot move to for_review: no implementation commit since claim in the repository root checkout beyond %s. Commit the work first, or pass --force if there is genuinely nothing to commit.", wpID, shown),
			LaneID: l.LaneID, LaneBranch: manifest.TargetBranch, BaseRef: base}
	}
	wt := lanes.WorktreePath(m.Root, handle, l.LaneID)
	branch := lanes.LaneBranchName(handle, l.LaneID, manifest.TargetBranch)
	base := lanes.LaneBaseRef(m.Root, m.Slug, manifest)
	if isDir(wt) && lanes.HasCommitBeyondBase(wt, base) {
		return lanes.GateDecision{Passed: true, LaneID: l.LaneID, LaneBranch: branch, BaseRef: base}
	}
	return lanes.GateDecision{Reason: fmt.Sprintf("%s cannot move to for_review: no implementation commit on lane %s (%s) beyond %s. Commit the work in the lane worktree first, or pass --force if there is genuinely nothing to commit.", wpID, l.LaneID, branch, base),
		LaneID: l.LaneID, LaneBranch: branch, BaseRef: base}
}

// qualifyingCommitSince is the repo-root lane's "implementation commit
// since claim" probe: a commit beyond base touching anything but the
// mission's (handle-named) status / issue-matrix / meta bookkeeping.
func qualifyingCommitSince(checkout, base, slug string) bool {
	out, err := gitx.Run(checkout, "log", "--no-renames", "--format=", "--name-only", base+"..HEAD")
	if err != nil {
		return false
	}
	excluded := []string{"kitty-specs/" + slug + "/status.", "kitty-specs/" + slug + "/issue-matrix.", "kitty-specs/" + slug + "/meta.json", ".kittify/"}
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		hit := false
		for _, e := range excluded {
			if strings.HasPrefix(p, e) {
				hit = true
			}
		}
		if !hit {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// existingWorkspaceForStamp is _existing_workspace_for_stamp: only a stored
// single_branch mission consults the lane manifest.
func existingWorkspaceForStamp(m *orch.Mission, handle, wpID string) *startWorkspace {
	if coord.Resolve(m.Root, m.Slug).Topology != coord.SingleBranch {
		return nil
	}
	return existingWorkspace(m, handle, wpID)
}

// ── start-implementation ──────────────────────────────────────────────────

func startImplementationCmd() *cli.Command {
	return &cli.Command{
		Name:  "start-implementation",
		Short: "Composite transition: planned->claimed->in_progress (idempotent).",
		Flags: []*cli.Flag{missionFlag(), str("wp", helpWP, true), str("actor", helpActor, true), str("policy", helpPolicy, false)},
		Run:   runStartImplementation,
	}
}

func runStartImplementation(ctx *cli.Context) error {
	v := newVerb(ctx, "start-implementation")
	pol, err := v.requirePolicy(ctx.String("policy"), "--policy is required for start-implementation")
	if err != nil {
		return err
	}
	root := v.root()
	handle, wpID, actor := ctx.String("mission"), ctx.String("wp"), ctx.String("actor")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	wpPath := orch.ResolveWPFile(filepath.Join(m.PrimaryDir, "tasks"), wpID)
	if wpPath == "" {
		return v.wpNotFound(wpID, handle)
	}
	snap := reduceStatus(m.StatusDir)
	self := status.ResolveLaneAlias(wpLane(snap, wpID))
	if self == string(status.Planned) || self == string(status.Claimed) {
		if un := unsatisfiedDeps(snap, wpDependencies(wpPath)); len(un) > 0 {
			return v.fail("DEPENDENCIES_NOT_SATISFIED",
				fmt.Sprintf("dependencies_not_satisfied: %s depends on %s; all dependencies must be approved or done before implementation can start", wpID, strings.Join(un, ", ")),
				identity(m.StatusDir, "wp_id", wpID, "unsatisfied_dependencies", strList(un)))
		}
	}
	release, err := v.enterCheckoutClaimLock(m, handle, wpID)
	defer release()
	if err != nil {
		return err
	}
	ws, err := v.resolveStartWorkspace(m, handle, wpID)
	if err != nil {
		return err
	}
	if err := v.enforceClaimAncestry(m, wpID, ws.path); err != nil {
		return err
	}
	workflow.RefreshReusedLaneContext(root, m.Slug, ws.laneID, wpID, wpDependencies(wpPath))
	s := surfaceFor(m, handle)
	res, err := workflow.StartImplementation(s, wpID, status.StrActor(actor), ws.path, ws.executionMode(), pol.Object(), nil, false, nil)
	if err != nil {
		return v.claimFailure(m, err, actor)
	}
	data := identity(m.StatusDir, "wp_id", wpID, "from_lane", res.From, "to_lane", "in_progress", "workspace_path", ws.path,
		"prompt_path", wpPath, "policy_metadata_recorded", true, "no_op", res.NoOp)
	ws.addLaneFields(data)
	return v.ok(data)
}

// ── start-review ──────────────────────────────────────────────────────────

func startReviewCmd() *cli.Command {
	return &cli.Command{
		Name:  "start-review",
		Short: "Transition a WP from for_review to in_review (reviewer claims review).",
		Flags: []*cli.Flag{missionFlag(), str("wp", helpWP, true), str("actor", helpActor, true), str("policy", helpPolicy, false),
			str("review-ref", "Review feedback reference (optional, not required for for_review→in_review)", false)},
		Run: runStartReview,
	}
}

func runStartReview(ctx *cli.Context) error {
	v := newVerb(ctx, "start-review")
	pol, err := v.requirePolicy(ctx.String("policy"), "--policy is required for start-review")
	if err != nil {
		return err
	}
	root := v.root()
	handle, wpID, actor := ctx.String("mission"), ctx.String("wp"), ctx.String("actor")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	wpPath := orch.ResolveWPFile(filepath.Join(m.PrimaryDir, "tasks"), wpID)
	if wpPath == "" {
		return v.wpNotFound(wpID, handle)
	}
	ws := existingWorkspaceForStamp(m, handle, wpID)
	s := surfaceFor(m, handle)
	res, err := workflow.StartReviewRef(s, wpID, status.StrActor(actor), "orchestrator-api:"+root, ws.executionMode(), pol.Object(), optString(ctx, "review-ref"))
	if err != nil {
		var cc *workflow.ClaimConflict
		if errors.As(err, &cc) {
			return v.fail("WP_ALREADY_CLAIMED", cc.Msg, identity(m.StatusDir, "claimed_by", claimedBy(cc.Msg), "requesting_actor", actor))
		}
		return v.fail("TRANSITION_REJECTED", err.Error(), nil)
	}
	return v.ok(identity(m.StatusDir, "wp_id", wpID, "from_lane", res.From, "to_lane", "in_review", "prompt_path", wpPath,
		"policy_metadata_recorded", true, "no_op", res.NoOp))
}

// ── transition ────────────────────────────────────────────────────────────

func transitionCmd() *cli.Command {
	return &cli.Command{
		Name:  "transition",
		Short: "Emit a single lane transition for a WP.",
		Flags: []*cli.Flag{
			missionFlag(), str("wp", helpWP, true), str("to", "Target lane", true), str("actor", helpActor, true),
			str("note", "Reason/note for the transition (required with --force out of in_review or approved)", false),
			str("policy", "Policy metadata JSON (required for run-affecting lanes)", false),
			boolFlag("force", "Force the transition"),
			str("review-ref", "Review reference", false),
			str("review-result-json", "JSON structured review outcome for transitions from in_review", false),
			str("evidence-json", "JSON string with done evidence", false),
			boolFlag("subtasks-complete", "Whether required subtasks are complete for in_progress->for_review"),
			boolFlag("implementation-evidence-present", "Whether implementation evidence exists for in_progress->for_review"),
		},
		Run: runTransition,
	}
}

func runTransition(ctx *cli.Context) error {
	v := newVerb(ctx, "transition")
	toLane := status.ResolveLaneAlias(ctx.String("to"))
	var pm *jsonx.Object
	raw := ctx.String("policy")
	if transitionRequiresPolicy(toLane) {
		if raw == "" {
			return v.fail("POLICY_METADATA_REQUIRED", fmt.Sprintf("--policy is required when transitioning to '%s'", toLane), nil)
		}
		p, err := v.parsePolicy(raw)
		if err != nil {
			return err
		}
		pm = p.Object()
	} else if raw != "" {
		p, err := v.parsePolicy(raw)
		if err != nil {
			return err
		}
		pm = p.Object()
	}
	var evidenceRaw any
	if ev := optString(ctx, "evidence-json"); ev != nil {
		parsed, err := pyjson.Loads(*ev)
		if err != nil {
			return v.fail("USAGE_ERROR", "Invalid JSON in --evidence-json: "+err.Error(), nil)
		}
		if _, ok := parsed.(*jsonx.Object); !ok {
			return v.fail("USAGE_ERROR", "--evidence-json must decode to a JSON object", nil)
		}
		evidenceRaw = parsed
	}
	var rr *status.ReviewResult
	if r := optString(ctx, "review-result-json"); r != nil {
		parsed, err := agentcore.ParseReviewResultJSON(*r)
		if err != nil {
			return v.fail("USAGE_ERROR", err.Error(), nil)
		}
		rr = parsed
	}
	root := v.root()
	handle, wpID, actor := ctx.String("mission"), ctx.String("wp"), ctx.String("actor")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	if orch.ResolveWPFile(filepath.Join(m.PrimaryDir, "tasks"), wpID) == "" {
		return v.wpNotFound(wpID, handle)
	}
	force := ctx.Bool("force")
	var ws *startWorkspace
	switch toLane {
	case string(status.ForReview):
		if d := forReviewGate(m, handle, wpID, force); !d.Passed {
			var lane any
			if d.LaneID != "" {
				lane = d.LaneID
			}
			return v.fail("TRANSITION_REJECTED", d.Reason, identity(m.StatusDir, "wp_id", wpID, "lane_id", lane))
		}
		ws = existingWorkspaceForStamp(m, handle, wpID)
	case string(status.Claimed):
		ws, err = v.resolveStartWorkspace(m, handle, wpID)
		if err != nil {
			return err
		}
		if err := v.enforceClaimAncestry(m, wpID, ws.path); err != nil {
			return err
		}
	default:
		ws = existingWorkspaceForStamp(m, handle, wpID)
	}
	req := &status.TransitionRequest{WPID: wpID, ToLane: toLane, Actor: status.StrActor(actor), Force: force,
		Reason: optString(ctx, "note"), ReviewRef: optString(ctx, "review-ref"), ReviewResult: rr,
		ExecutionMode: ws.executionMode(), RepoRoot: root, PolicyMetadata: pm}
	if ctx.IsSet("subtasks-complete") {
		req.SubtasksComplete = status.Bool(true)
	}
	if ctx.IsSet("implementation-evidence-present") {
		req.ImplementationEvidence = status.Bool(true)
	}
	if evidenceRaw != nil {
		ev, err := agentcore.BuildDoneEvidence(evidenceRaw)
		if err != nil {
			return v.fail("TRANSITION_REJECTED", err.Error(), nil)
		}
		req.Evidence = ev
	}
	s := surfaceFor(m, handle)
	res, err := s.EmitTransition(req)
	if err != nil {
		return v.fail("TRANSITION_REJECTED", err.Error(), nil)
	}
	from, to := "", ""
	if res.Event != nil {
		from, to = string(res.Event.From), string(res.Event.To)
		if res.Event.To == status.ForReview {
			lanes.RecordTipForWP(root, m.Slug, wpID)
		}
	}
	return v.ok(identity(m.StatusDir, "wp_id", wpID, "from_lane", from, "to_lane", to, "policy_metadata_recorded", pm != nil))
}

// ── append-history ────────────────────────────────────────────────────────

func appendHistoryCmd() *cli.Command {
	return &cli.Command{
		Name:  "append-history",
		Short: "Append a history entry via an ``InnerStateChanged`` ``note`` annotation.",
		Flags: []*cli.Flag{missionFlag(), str("wp", helpWP, true), str("actor", helpActor, true), str("note", "History note to append", true)},
		Run:   runAppendHistory,
	}
}

func runAppendHistory(ctx *cli.Context) error {
	v := newVerb(ctx, "append-history")
	root := v.root()
	handle, wpID, actor, note := ctx.String("mission"), ctx.String("wp"), ctx.String("actor"), ctx.String("note")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	if orch.ResolveWPFile(filepath.Join(m.PrimaryDir, "tasks"), wpID) == "" {
		return v.wpNotFound(wpID, handle)
	}
	entry := fmt.Sprintf("- [%s] %s: %s", clock.Now().UTC().Format("2006-01-02T15:04:05Z"), actor, note)
	a, err := status.NewAnnotation(wpID, status.Delta{Note: &entry}, status.StrActor(actor), "")
	if err == nil {
		err = status.AppendEvents(m.StatusDir, a)
	}
	if err != nil {
		return v.fail("HISTORY_COMMIT_FAILED", err.Error(), nil)
	}
	_, _ = status.Materialize(m.StatusDir)
	return v.ok(identity(m.PrimaryDir, "wp_id", wpID, "history_entry_id", orch.NewHistoryEntryID()))
}
