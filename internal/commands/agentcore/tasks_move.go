package agentcore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/lifecycle"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const moveTaskLong = "Move task between lanes (planned → doing → for_review → approved → done).\n\n" +
	"Review-rejection edges (a backward move out of review — ``--to planned`` from\nin_progress/for_review/in_review/approved, or ``in_review → in_progress``) MUST\n" +
	"carry a rationale: pass ``--review-feedback-file`` (or ``--note``/``--reason``).\nThat rationale is emitted as the status event's ``review_ref``/reason and is\n" +
	"required by the shared status contract; a rejection emitted without it is\naccepted locally but silently rejected by hosted sync, so it never propagates.\n\n" +
	"``--actor``/``--reason`` are accepted as aliases of ``--agent``/``--note``\n(matching ``issue-verdict``'s vocabulary, #3469) — use whichever spelling\nreads naturally; both apply the same value.\n\n" +
	"Subtask completion is NOT tracked by ticking a checkbox in ``tasks.md`` —\nthat file is guidance/documentation only. Completion is event-sourced; mark\n" +
	"a subtask done with ``spec-kitty agent tasks mark-status T0NN --status done\n--mission <handle>`` (#2816). Ticking a checkbox alone will not clear the\nunchecked-subtasks gate on a ``--to for_review`` move.\n\n" +
	"Examples:\n    spec-kitty agent tasks move-task WP01 --to doing --assignee claude --json\n" +
	"    spec-kitty agent tasks move-task WP02 --to for_review --agent claude --shell-pid $$\n" +
	"    spec-kitty agent tasks move-task WP02 --to for_review --actor claude --shell-pid $$\n" +
	"    spec-kitty agent tasks move-task WP03 --to approved --note \"Review passed\"\n" +
	"    spec-kitty agent tasks move-task WP03 --to approved --reason \"Review passed\"\n" +
	"    spec-kitty agent tasks move-task WP03 --to done --done-override-reason \"Branch deleted after hotfix merge\"\n" +
	"    spec-kitty agent tasks move-task WP03 --to planned --review-feedback-file feedback.md"

func init() {
	addCommands(func(_, tasksG, _ *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "move-task",
			Short: "Move task between lanes (planned → doing → for_review → approved → done).",
			Long:  moveTaskLong,
			Args:  []*cli.Arg{{Name: "task_id", Metavar: "{task_id}", Required: true, Help: "Task ID (e.g., WP01)"}},
			Flags: []*cli.Flag{
				{Name: "to", Kind: cli.String, Required: true, Metavar: "<str>", Help: "Target lane (planned/doing/for_review/approved/done)"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "agent", Aliases: []string{"actor"}, Kind: cli.String, Metavar: "<str>", Help: "Agent name (alias: --actor, matching issue-verdict's vocabulary). If both --agent and --actor are given, the one supplied LAST on the command line wins."},
				{Name: "model", Kind: cli.String, Metavar: "<str>", Help: "Dispatch-resolved model actual"},
				{Name: "profile", Kind: cli.String, Metavar: "<str>", Help: "Dispatch-resolved agent profile actual"},
				{Name: "invocation-id", Kind: cli.String, Metavar: "<str>", Help: "Authoritative dispatch Op record id"},
				{Name: "assignee", Kind: cli.String, Metavar: "<str>", Help: "Assignee name, recorded on this transition regardless of the target lane (not limited to --to doing)."},
				{Name: "shell-pid", Kind: cli.String, Metavar: "<str>", Help: "Shell PID"},
				{Name: "note", Aliases: []string{"reason"}, Kind: cli.String, Metavar: "<str>", Help: "History note (alias: --reason, matching issue-verdict's vocabulary). If both --note and --reason are given, the one supplied LAST on the command line wins."},
				{Name: "review-feedback-file", Kind: cli.String, Metavar: "<path>", Help: "Path to review feedback file. Required for review-rejection edges (--to planned, incl. with --force; and in_review->in_progress). Recorded as the transition's on-wire review_ref rationale; omitting it makes the status event contract-invalid and hosted sync drops it."},
				{Name: "approval-ref", Kind: cli.String, Metavar: "<str>", Help: "Approval reference for approval/done transitions (e.g., PR#42)"},
				{Name: "reviewer", Kind: cli.String, Metavar: "<str>", Help: "Reviewer name (auto-detected from git if omitted)"},
				{Name: "self-review-fallback", Kind: cli.Bool, Help: "Record that approval is a self-review fallback after the intended reviewer failed."},
				{Name: "intended-reviewer", Kind: cli.String, Metavar: "<str>", Help: "Reviewer that should have reviewed this WP before fallback."},
				{Name: "reviewer-failure-reason", Kind: cli.String, Metavar: "<str>", Help: "Reason the intended reviewer failed."},
				{Name: "done-override-reason", Kind: cli.String, Metavar: "<str>", Help: "Required when --to done and merge ancestry cannot be verified; recorded in history/event reason"},
				{Name: "force", Kind: cli.Bool, Help: "Force move even with unchecked subtasks (does not bypass planned rollback feedback requirement). Out of in_review or approved it needs --note."},
				{Name: "tracker-ref", Kind: cli.StringList, Metavar: "<str>", Help: "External tracker reference (e.g., '#1298' or 'JIRA-123'). Repeatable; appended to the WP frontmatter tracker_refs."},
				{Name: "skip-review-artifact-check", Kind: cli.Bool, Help: "Override a rejected latest review artifact when arbiter-approving; requires --note and records override evidence."},
				{Name: "auto-commit", Kind: cli.Bool, Negatable: true, Help: "Automatically commit WP file changes to target branch (default: from project config)"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "skip-pre-review-gate", Kind: cli.Bool, Help: "Skip the pre-review regression gate on a --to for_review move (also honored via the SPEC_KITTY_SKIP_PRE_REVIEW_GATE env var). The gate still runs and enforces by default."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Use an owned single_branch checkout for the local review lifecycle (force/skip, done, and arbiter modes unsupported)."},
			},
			Run: runMoveTask,
		})
	})
}

// errExit is a sentinel for "output already printed, exit 1".
var errExit = errors.New("exit")

type moveState struct {
	ctx  *cli.Context
	json bool

	taskID, to                                  string
	agent, note, assignee, shellPID             string
	reviewer, approvalRef, doneOverride         string
	intendedReviewer, reviewerFailureReason     string
	model, profile, invocationID                string
	feedbackFile                                string
	hasFeedback, hasModel, hasProfile, hasInvID bool
	force, selfReviewFallback, skipArtifact     bool
	skipGate                                    bool
	trackerRefs                                 []string
	autoCommitFlag                              *bool
	// owned is the validated owned-checkout fact (the local review
	// lifecycle in P); reviewBaseRef its resolved review base.
	owned         *ownedcheckout.Fact
	reviewBaseRef string

	// phase A
	target             string
	root               string
	m                  *missionCtx
	targetBranch       string
	autoCommit         bool
	skipPrimary        bool
	binding            *status.ResolvedBinding
	featureDir         string // status write surface
	w                  *wp.WP
	oldLane            string
	currentAgent       string
	latestImplementer  string
	feedbackSource     string // resolved absolute feedback path ("" when not usable)
	feedbackExists     bool
	feedbackIsFile     bool
	feedbackContent    string
	reviewVerdict      string // "" = none
	reviewArtifactName string // "" = none
	verdictArtifact    string
	unchecked          []string
	reviewReady        bool
	reviewGuidance     []string
	doneExecMode       string
	doneMerged         bool
	doneMergeMsg       string
	issueMatrixBlocker string // area E: issue-matrix approval blocker
	effectiveReviewer  string
	effectiveApproval  string
	isArbiter          bool
	arbReviewRef       *string

	// decision / execution
	noteText          string
	doneOverrideNote  bool
	plan              *transitionPlan
	evidence          *status.DoneEvidence
	actor             string
	canonicalLane     string
	feedbackPointer   string
	rejectedResult    *status.ReviewResult
	planReviewResult  *status.ReviewResult
	verdictWrite      *review.Cycle
	gateMeta          *jsonx.Object
	event             *status.Event
	finalHopActor     string
	claimEmitted      bool
	rollback          *rollbackSummary
	authoritativeLane string
	transitionApplied bool
}

type transitionPlan struct {
	canonical string
	targets   []string
	force     bool
	reason    *string
	reviewRef *string
}

type rollbackSummary struct {
	resetIDs, previouslyCompleted, neverCompleted []string
}

func (st *moveState) outErr(msg string) error {
	outputError(st.ctx, st.json, msg)
	return errExit
}

func (st *moveState) outDiag(msg string, diag *jsonx.Object) error {
	if st.json && diag != nil {
		printJSON(st.ctx.Stdout, diag)
	} else {
		outputError(st.ctx, st.json, msg)
	}
	return errExit
}

func (st *moveState) console(text string) {
	if !st.json {
		consolePrint(st.ctx.Stdout, text)
	}
}

func runMoveTask(ctx *cli.Context) error {
	st := &moveState{ctx: ctx, json: ctx.Bool("json"), taskID: ctx.Arg("task_id"), to: ctx.String("to"),
		agent: ctx.String("agent"), note: ctx.String("note"), assignee: ctx.String("assignee"), shellPID: ctx.String("shell-pid"),
		reviewer: ctx.String("reviewer"), approvalRef: ctx.String("approval-ref"), doneOverride: ctx.String("done-override-reason"),
		intendedReviewer: ctx.String("intended-reviewer"), reviewerFailureReason: ctx.String("reviewer-failure-reason"),
		model: ctx.String("model"), profile: ctx.String("profile"), invocationID: ctx.String("invocation-id"),
		feedbackFile: ctx.String("review-feedback-file"), hasFeedback: ctx.IsSet("review-feedback-file"),
		hasModel: ctx.IsSet("model"), hasProfile: ctx.IsSet("profile"), hasInvID: ctx.IsSet("invocation-id"),
		force: ctx.Bool("force"), selfReviewFallback: ctx.Bool("self-review-fallback"), skipArtifact: ctx.Bool("skip-review-artifact-check"),
		skipGate: ctx.Bool("skip-pre-review-gate"), trackerRefs: ctx.Strings("tracker-ref")}
	if ctx.IsSet("auto-commit") {
		v := ctx.Bool("auto-commit")
		st.autoCommitFlag = &v
	}
	if root := project.Locate(""); root != "" {
		owned, err := resolveOwned(ctx, root, ctx.String("mission"), st.json, ownedcheckout.FlatErrorEnvelope, ownedcheckout.Options{})
		if err != nil {
			if _, ok := err.(*ownedcheckout.MetaReadError); ok {
				outputError(ctx, st.json, err.Error())
				return cli.Exit(1)
			}
			return err
		}
		st.owned = owned
	}
	err := st.run()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errExit):
		return cli.Exit(1)
	}
	var oe *ownedcheckout.Error
	if st.owned != nil && errors.As(err, &oe) {
		emitOwnedError(st, oe)
		return cli.Exit(1)
	}
	var ce *cli.ExitError
	if errors.As(err, &ce) {
		return err
	}
	if te, ok := err.(*status.TransitionError); ok {
		cur := st.authoritativeLane
		if cur == "" {
			cur = st.oldLane
		}
		req := st.canonicalLane
		if req == "" {
			req = status.ResolveLaneAlias(st.target)
		}
		st.outDiag(te.Msg, jsonx.NewObject("result", "error", "code", "invalid_transition", "error", te.Msg,
			"current_lane", cur, "requested_lane", req, "verdict_durably_persisted", false, "evidence_ref", nil, "destination_ref", nil))
		return cli.Exit(1)
	}
	outputError(ctx, st.json, err.Error())
	return cli.Exit(1)
}

func (st *moveState) run() error {
	if err := st.resolveTargets(); err != nil {
		return err
	}
	if _, err := st.currentEventLane(); err != nil {
		return err
	}
	if err := st.gatherReviewFacts(); err != nil {
		return err
	}
	if err := st.runDecision(); err != nil {
		return err
	}
	if err := st.runPreReviewGate(); err != nil {
		return err
	}
	if err := st.completeDeferredReadiness(); err != nil {
		return err
	}
	if err := st.finalizePlan(); err != nil {
		return err
	}
	if err := st.execute(); err != nil {
		if rerr := st.revertVerdictWrite(); rerr != nil {
			return fmt.Errorf("Transition emit failed for %s (%v); the FR-002 revert-compensator ALSO failed to undo the already-committed verdict write (%v). Operator attention required -- a committed verdict may still exist.", st.taskID, err, rerr)
		}
		return err
	}
	st.output()
	return nil
}

// --- phase A ------------------------------------------------------------

func (st *moveState) resolveTargets() error {
	t, err := status.EnsureLane(st.to)
	if err != nil {
		return st.outErr(err.Error())
	}
	st.target = t
	var root string
	if st.owned != nil {
		if err := st.preflightOwned(); err != nil {
			return err
		}
		root = st.owned.OwnedRoot
	} else {
		root = project.Locate("")
		if root == "" {
			return st.outErr("Could not locate project root")
		}
	}
	st.root = root
	if st.autoCommitFlag != nil {
		st.autoCommit = *st.autoCommitFlag
	} else {
		st.autoCommit = mission.AutoCommit(root)
	}
	var slug string
	if st.owned != nil {
		if err := st.applyOwnedTargets(); err != nil {
			return err
		}
		slug = st.owned.MissionSlug
		st.m = newMissionCtx(root, slug)
	} else {
		var err error
		slug, err = resolveTasksMission(st.ctx, root, st.ctx.String("mission"), st.json)
		if err != nil {
			return err
		}
		st.m = newMissionCtx(root, slug)
		cur, err := st.m.banner(st.ctx, st.json)
		if err != nil {
			return st.outErr(err.Error())
		}
		st.targetBranch = cur
	}
	// _resolve_dispatch_binding: APPROVED/DONE (and IN_REVIEW) are reviewer
	// decisions, everything else resolves the implement action.
	action := "implement"
	switch status.Lane(st.target) {
	case status.InReview, status.Approved, status.Done:
		action = "review"
	}
	req := workflow.BindingRequest{ProfileVersion: func(id string) (string, error) { return resolveProfileVersion(root, id) }}
	if st.hasModel {
		req.Model = &st.model
	}
	if st.hasProfile {
		req.Profile = &st.profile
	}
	if st.hasInvID {
		req.InvocationID = &st.invocationID
	}
	b, err := req.Resolve(root, slug, st.taskID, action)
	if err != nil {
		return st.outErr(err.Error())
	}
	st.binding = b
	protRoot := root
	if st.owned != nil {
		protRoot = st.owned.RepositoryRoot
	}
	if st.autoCommit {
		st.skipPrimary = st.owned == nil && isDir(st.m.Surface.CoordWorktree) && st.m.Surface.CoordWorktree != "" && coord.IsProtected(root, slug, st.targetBranch)
		if !st.skipPrimary && coord.IsProtected(protRoot, slug, st.targetBranch) {
			if e := selfReviewOptionError(st); e != "" {
				return st.outErr(e)
			}
			return st.outErr(fmt.Sprintf("Refusing to run `spec-kitty agent tasks move-task` with auto-commit on protected branch '%s' before mutating status files. Run status commit operations from an allowed coordination/lane branch, or unblock with: %s.", st.targetBranch, coord.ProtectedRemedy))
		}
	}
	var refs []string
	for _, r := range st.trackerRefs {
		if s := strings.TrimSpace(r); s != "" {
			refs = append(refs, s)
		}
	}
	st.trackerRefs = refs
	st.warnWorktreeKittySpecs()
	dir, err := st.m.Surface.WriteDir()
	if err != nil {
		return err
	}
	st.featureDir = dir
	w, err := locateWorkPackage(st.m, st.taskID)
	if err != nil {
		return st.outErr(err.Error())
	}
	st.w = w
	snap, err := status.ReduceDir(st.featureDir)
	if err != nil {
		return err
	}
	st.oldLane = status.DeriveFromLane(snap, st.taskID)
	if st.owned != nil && !ownedLifecycleLanes[st.oldLane] {
		return ownedRefusal("OWNED_TRANSITION_UNSUPPORTED", "Owned checkout does not support transitions from this lane.")
	}
	if st.owned == nil && st.oldLane == string(status.Planned) && st.target != string(status.Planned) {
		if m, _ := lanes.Read(st.m.PrimaryDir()); m == nil {
			return st.outErr(fmt.Sprintf("Cannot move %s out of 'planned': lanes.json is absent for mission '%s'. Run `spec-kitty doctor mission-state --fix --mission %s` to rebuild execution lanes from the canonical event log, then retry.", st.taskID, slug, slug))
		}
	}
	if st.owned != nil && (st.target == string(status.ForReview) || st.target == string(status.Approved)) {
		base, err := st.resolveOwnedReviewBase()
		if err != nil {
			return err
		}
		st.reviewBaseRef = base
	}
	if s := snap.WP(st.taskID); s != nil {
		if v, ok := s.Get("agent"); ok && v != nil && status.PyStr(v) != "" {
			st.currentAgent = status.PyStr(v)
		}
	}
	return nil
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	s, err := os.Stat(p)
	return err == nil && s.IsDir()
}

func (st *moveState) warnWorktreeKittySpecs() {
	if st.json {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	cwd, _ = filepath.Abs(cwd)
	mainRoot := st.root
	if st.owned != nil {
		mainRoot = st.owned.RepositoryRoot
	}
	if cwd == mainRoot || !strings.Contains(cwd, string(filepath.Separator)+project.WorktreesDir) {
		return
	}
	for cur := cwd; cur != filepath.Dir(cur) && strings.Contains(cur, project.WorktreesDir); cur = filepath.Dir(cur) {
		if isDir(filepath.Join(cur, project.SpecsDir)) {
			if mission.SafePathSegment(st.m.Slug) && isDir(filepath.Join(cur, project.SpecsDir, st.m.Slug, "tasks")) {
				st.console(fmt.Sprintf("Note: Using planning repo's kitty-specs/ on %s (worktree copy ignored)", st.targetBranch))
			}
			return
		}
	}
}

func selfReviewOptionError(st *moveState) string {
	if !st.selfReviewFallback {
		if st.intendedReviewer != "" || st.reviewerFailureReason != "" {
			return "--intended-reviewer/--reviewer-failure-reason require --self-review-fallback."
		}
		return ""
	}
	if t := status.ResolveLaneAlias(st.target); t != string(status.Approved) && t != string(status.Done) {
		return "--self-review-fallback is only valid when approving or marking done."
	}
	if !st.force {
		return "--self-review-fallback requires --force so force_count records the independence override."
	}
	if strings.TrimSpace(st.intendedReviewer) == "" {
		return "--self-review-fallback requires --intended-reviewer <agent>."
	}
	if strings.TrimSpace(st.reviewerFailureReason) == "" {
		return "--self-review-fallback requires --reviewer-failure-reason <reason>."
	}
	return ""
}

// currentEventLane is _mt_current_event_lane (raw last event for the WP).
func (st *moveState) currentEventLane() (string, error) {
	stream, err := status.ReadStream(st.featureDir, "")
	if err != nil {
		return "", err
	}
	for i := len(stream.Transitions) - 1; i >= 0; i-- {
		if stream.Transitions[i].WPID == st.taskID {
			return string(stream.Transitions[i].To), nil
		}
	}
	return "", fmt.Errorf("WP %s has no canonical status in feature %s. Run `spec-kitty agent mission finalize-tasks --mission %s` to initialize.", st.taskID, st.m.Slug, st.m.Slug)
}

// --- phase B ------------------------------------------------------------

func isReviewRejectionEdge(old, target string) bool {
	o, t := status.ResolveLaneAlias(old), status.ResolveLaneAlias(target)
	return t == string(status.Planned) || (o == string(status.InReview) && t == string(status.InProgress))
}

func (st *moveState) gatherReviewFacts() error {
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		st.resolveReviewVerdictFacts()
	}
	if st.hasFeedback {
		p := st.feedbackFile
		if strings.HasPrefix(p, "~") {
			if h, err := os.UserHomeDir(); err == nil {
				p = h + p[1:]
			}
		}
		if !filepath.IsAbs(p) {
			cwd, _ := os.Getwd()
			p = filepath.Join(cwd, p)
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		p = filepath.Clean(p)
		st.feedbackSource = p
		if s, err := os.Stat(p); err == nil {
			st.feedbackExists = true
			st.feedbackIsFile = !s.IsDir()
			if st.feedbackIsFile && isReviewRejectionEdge(st.oldLane, st.target) {
				b, _ := os.ReadFile(p)
				st.feedbackContent = strings.TrimSpace(string(b))
			}
		}
	}
	st.reviewReady = true
	if !st.force && (st.target == string(status.ForReview) || st.target == string(status.Approved) || st.target == string(status.Done)) {
		roster, err := wp.AuthoredRoster(st.m.PrimaryDir(), st.taskID)
		if err != nil {
			if _, statErr := os.Stat(st.m.TasksDir()); statErr == nil {
				return err
			}
		} else if len(roster) > 0 {
			snap, err := status.ReduceDir(st.m.ReadDir())
			if err != nil {
				return err
			}
			st.unchecked = status.UncheckedSubtasks(snap, st.taskID, roster)
		}
	}
	if st.target == string(status.ForReview) || st.target == string(status.Approved) || st.target == string(status.Done) {
		defer_ := st.target == string(status.ForReview) && st.autoCommit && !st.force
		if !defer_ {
			ok, guidance, err := st.validateReadyForReview()
			if err != nil {
				return err
			}
			st.reviewReady, st.reviewGuidance = ok, guidance
		}
	}
	if stream, err := status.ReadStream(st.featureDir, ""); err == nil {
		st.latestImplementer = status.LatestImplementer(stream.Transitions, st.taskID)
	}
	return nil
}

// resolveReviewVerdictFacts is resolve_review_verdict_facts from the event log.
func (st *moveState) resolveReviewVerdictFacts() {
	snap, err := status.ReduceDir(st.featureDir)
	if err != nil {
		return
	}
	state := snap.WP(st.taskID)
	if state == nil {
		return
	}
	raw, ok := state.Get("review_result")
	if !ok || raw == nil {
		return
	}
	wpDir := filepath.Join(filepath.Dir(st.w.Path), st.w.Stem())
	rr, isObj := raw.(*jsonx.Object)
	if !isObj || rr.GetString("reviewer") == "" || rr.GetString("verdict") == "" || rr.GetString("reference") == "" {
		st.verdictArtifact = filepath.Join(wpDir, "review-cycle-damaged-event-record.md")
		st.reviewArtifactName = filepath.Base(st.verdictArtifact)
		return
	}
	v := rr.GetString("verdict")
	if v == "changes_requested" {
		v = "rejected"
	}
	st.reviewVerdict = v
	p := rr.GetString("feedback_path")
	if p == "" {
		p = filepath.Join(wpDir, "review-cycle-event-recorded.md")
	}
	st.verdictArtifact = p
	st.reviewArtifactName = filepath.Base(p)
}

// --- readiness (tasks_parsing_validation._validate_ready_for_review) ----

func (st *moveState) validateReadyForReview() (bool, []string, error) {
	if st.force {
		return true, nil, nil
	}
	meta := mission.ReadMetaOrEmpty(st.m.PrimaryDir())
	mt := mission.MissionType(meta)
	if mt == "" {
		mt = "software-dev"
	}
	if g := st.researchArtifactsGuidance(mt); g != nil {
		return false, g, nil
	}
	if mt != "software-dev" {
		return true, nil, nil
	}
	var ws *lanes.Workspace
	if st.owned != nil {
		ws = st.ownedWorkspace()
	} else {
		var err error
		ws, err = lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
		if err != nil {
			ws = nil
		}
	}
	if ws != nil && ws.ResolutionKind == "repo_root" {
		return true, nil, nil
	}
	wt := lanes.WorktreePath(st.root, st.m.Slug, "lane-a")
	if ws != nil {
		wt = ws.WorktreePath
	}
	if !isDir(wt) {
		return true, nil, nil
	}
	retry := fmt.Sprintf("Then retry: spec-kitty agent tasks move-task %s --to %s", st.taskID, st.target)
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		return false, []string{fmt.Sprintf("Workspace %s is a husk (no .git entry). Run `spec-kitty doctor workspaces --fix` to repair it.", wt)}, nil
	}
	branch := gitx.CurrentBranch(wt)
	if branch == "" || branch == "HEAD" {
		return false, []string{"Detached HEAD detected in worktree!", "", "Please reattach to a branch before review:", "  cd " + wt, "  git checkout <your-branch>", "", retry}, nil
	}
	var inProgress []string
	for _, x := range [][2]string{{"MERGE_HEAD", "merge"}, {"REBASE_HEAD", "rebase"}, {"CHERRY_PICK_HEAD", "cherry-pick"}} {
		if gitx.OK(wt, "rev-parse", "-q", "--verify", x[0]) {
			inProgress = append(inProgress, x[1])
		}
	}
	if len(inProgress) > 0 {
		return false, []string{"In-progress git operation detected in worktree!", "", "Active operation(s): " + strings.Join(inProgress, ", "), "",
			"Resolve or abort before review:", "  cd " + wt, "  git status", "  git merge --abort   # if merge", "  git rebase --abort  # if rebase",
			"  git cherry-pick --abort  # if cherry-pick", "", retry}, nil
	}
	check := st.reviewCurrencyBranch(ws)
	if st.reviewBaseRef != "" {
		check = st.reviewBaseRef
	}
	behind := 0
	if out, err := gitx.Run(wt, "rev-list", "--count", "HEAD.."+check); err == nil {
		behind, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if behind > 0 && !behindOnlyLedger(wt, check) {
		nonLedger := behind
		if out, err := gitx.Run(wt, "rev-list", "--count", "--full-history", "HEAD.."+check, "--", ".", ":(exclude)kitty-specs", ":(exclude).kittify"); err == nil {
			nonLedger, _ = strconv.Atoi(strings.TrimSpace(out))
		}
		return false, []string{check + " branch has new commits not in this worktree!", "",
			fmt.Sprintf("Your branch is behind %s by %d commit(s) (%d non-ledger commit(s) touching files outside kitty-specs/ and .kittify/).", check, behind, nonLedger),
			"Rebase before review:", "  cd " + wt, "  git rebase " + check, "", retry}, nil
	}
	entries, err := statusEntries(wt)
	if err != nil {
		return false, nil, err
	}
	entries = filterRuntimeState(entries)
	if len(entries) > 0 {
		var staged, unstaged bool
		for _, e := range entries {
			if e.untracked() {
				unstaged = true
				continue
			}
			if e.x != ' ' {
				staged = true
			}
			if e.y != ' ' {
				unstaged = true
			}
		}
		head := "Uncommitted implementation changes in worktree!"
		if staged && !unstaged {
			head = "Staged but uncommitted changes in worktree!"
		} else if staged && unstaged {
			head = "Staged and unstaged changes in worktree!"
		}
		g := []string{head, "", "Modified files:"}
		for i, e := range entries {
			if i >= 5 {
				break
			}
			g = append(g, "  "+e.display())
		}
		g = append(g, "", "Commit your work first:", "  cd "+wt,
			"  "+safeCommitRecipe([]string{"<deliverable-path-1>", "<deliverable-path-2>", "..."}, fmt.Sprintf("feat(%s): <describe implementation>", st.taskID), gitx.CurrentBranch(wt)),
			"", retry)
		return false, g, nil
	}
	if !laneHasCommitBeyondBase(wt, check) {
		return false, []string{"No implementation commits on lane branch!", "",
			fmt.Sprintf("The worktree exists but has no commits beyond %s.", check), "Either:",
			"  1. Commit your implementation work to the worktree", "  2. Or verify work is complete (use --force if nothing to commit)", "",
			"  cd " + wt,
			"  " + safeCommitRecipe([]string{"<deliverable-path-1>", "<deliverable-path-2>", "..."}, fmt.Sprintf("feat(%s): <describe implementation>", st.taskID), gitx.CurrentBranch(wt)),
			"", retry}, nil
	}
	if st.owned == nil {
		if g := st.kittySpecsContamination(wt, check, retry); g != nil {
			return false, g, nil
		}
	}
	return true, nil, nil
}

func (st *moveState) reviewCurrencyBranch(ws *lanes.Workspace) string {
	if ws != nil && ws.Context != nil && ws.Context.BaseBranch != "" {
		return ws.Context.BaseBranch
	}
	if st.m.Surface.Routed() {
		return st.m.Surface.CoordBranch
	}
	return st.m.targetBranch()
}

func behindOnlyLedger(wt, check string) bool {
	mb, err := gitx.Run(wt, "merge-base", "HEAD", check)
	if err != nil || strings.TrimSpace(mb) == "" {
		return false
	}
	out, err := gitx.Run(wt, "diff", "--name-only", strings.TrimSpace(mb), check)
	if err != nil {
		return false
	}
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f == "" {
			continue
		}
		if !strings.HasPrefix(f, "kitty-specs/") && !strings.HasPrefix(f, ".kittify/") {
			return false
		}
	}
	return true
}

func laneHasCommitBeyondBase(wt, base string) bool {
	out, err := gitx.Run(wt, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n > 0
}

func safeCommitRecipe(paths []string, message, branch string) string {
	return fmt.Sprintf("spec-kitty safe-commit %s -m %s --to-branch %s", strings.Join(paths, " "), shellQuote(message), branch)
}

func shellQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func (st *moveState) kittySpecsContamination(wt, check, retry string) []string {
	meta := mission.ReadMetaOrEmpty(st.m.PrimaryDir())
	planning := meta.GetString("planning_base_branch")
	if planning == "" {
		planning = meta.GetString("target_branch")
	}
	base := planning
	if base == "" {
		base = check
	}
	mb, err := gitx.Run(wt, "merge-base", "HEAD", base)
	if err != nil {
		return nil
	}
	out, err := gitx.Run(wt, "diff", "--name-only", strings.TrimSpace(mb), "HEAD", "--", "kitty-specs/")
	if err != nil {
		return nil
	}
	var files []string
	seen := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f == "" || !strings.HasPrefix(f, "kitty-specs/") || seen[f] || isOccurrenceMap(f) || mission.IsCoordResidue(f) {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil
	}
	if div, err := gitx.Run(wt, "diff", "--name-only", base, "HEAD", "--", "kitty-specs/"); err == nil {
		keep := map[string]bool{}
		for _, f := range strings.Split(strings.TrimSpace(div), "\n") {
			keep[f] = true
		}
		var kept []string
		for _, f := range files {
			if keep[f] {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	if len(files) == 0 {
		return nil
	}
	g := []string{"Committed kitty-specs files on this lane branch:"}
	for i, f := range files {
		if i >= 5 {
			break
		}
		g = append(g, "  "+f)
	}
	if len(files) > 5 {
		g = append(g, fmt.Sprintf("  ... and %d more", len(files)-5))
	}
	g = append(g, "")
	if planning != "" {
		g = append(g, fmt.Sprintf("kitty-specs/ changes are not allowed on lane branches.\nPlanning artifacts must live on: %s\n\nTo verify a file exists on the planning branch:\n  git show %s:%s", planning, planning, files[0]))
	} else {
		g = append(g, "kitty-specs/ changes are not allowed on lane branches (planning branch unknown — check kitty-specs/ on the base branch).")
	}
	g = append(g, "", fmt.Sprintf("Clean the branch before moving to %s:", st.target), "  cd "+wt,
		fmt.Sprintf("  git restore --source %s -- %s", strings.TrimSpace(mb), strings.Join(files, " ")),
		"  "+safeCommitRecipe([]string{"kitty-specs/"}, "chore: remove planning artifacts from lane branch", gitx.CurrentBranch(wt)), "", retry)
	return g
}

func (st *moveState) researchArtifactsGuidance(missionType string) []string {
	entries, err := statusEntriesPaths(st.root, "kitty-specs/"+st.m.Slug+"/")
	if err != nil || len(entries) == 0 {
		return nil
	}
	_ = missionType
	return nil
}

// --- git status entries ---------------------------------------------------

type statusEntry struct {
	x, y     byte
	path     string
	origPath string
}

func (e statusEntry) untracked() bool { return e.x == '?' && e.y == '?' }

func (e statusEntry) display() string {
	if e.origPath != "" {
		return fmt.Sprintf("%c%c %s -> %s", e.x, e.y, e.origPath, e.path)
	}
	return fmt.Sprintf("%c%c %s", e.x, e.y, e.path)
}

func statusEntries(dir string) ([]statusEntry, error) {
	res, err := gitx.Exec(dir, nil, nil, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("git status failed in %s: %s", dir, strings.TrimSpace(res.Stderr))
	}
	var out []statusEntry
	parts := strings.Split(res.Stdout, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		e := statusEntry{x: p[0], y: p[1], path: p[3:]}
		if e.x == 'R' || e.x == 'C' {
			if i+1 < len(parts) {
				e.origPath = parts[i+1]
				i++
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func statusEntriesPaths(dir, prefix string) ([]statusEntry, error) {
	all, err := statusEntries(dir)
	if err != nil {
		return nil, err
	}
	var out []statusEntry
	for _, e := range all {
		if strings.HasPrefix(e.path, prefix) {
			out = append(out, e)
		}
	}
	return out, nil
}

func isRuntimeStatePath(p string) bool {
	return strings.HasPrefix(p, ".spec-kitty/") || strings.HasPrefix(p, ".kittify/") ||
		(strings.Contains(p, ".kittify/dossiers/") && strings.HasSuffix(p, "snapshot-latest.json"))
}

func filterRuntimeState(entries []statusEntry) []statusEntry {
	var out []statusEntry
	for _, e := range entries {
		paths := []string{e.path}
		if e.origPath != "" {
			paths = append(paths, e.origPath)
		}
		all := true
		for _, p := range paths {
			if !isRuntimeStatePath(p) {
				all = false
			}
		}
		if !all {
			out = append(out, e)
		}
	}
	return out
}

// --- phase C: decision --------------------------------------------------

type refusal struct {
	err     string
	diag    *jsonx.Object
	warning []string
}

// precedingGuards are the guards ahead of the rejected-verdict arm.
func (st *moveState) precedingGuards() *refusal {
	if e := selfReviewOptionError(st); e != "" {
		return &refusal{err: e}
	}
	if st.skipPrimary {
		var unsupported []string
		if len(st.trackerRefs) > 0 {
			unsupported = append(unsupported, "tracker_refs")
		}
		if st.assignee != "" {
			unsupported = append(unsupported, "assignee")
		}
		if st.shellPID != "" {
			unsupported = append(unsupported, "shell_pid")
		}
		if st.note != "" {
			unsupported = append(unsupported, "activity_log")
		}
		if len(unsupported) > 0 {
			msg := fmt.Sprintf("Cannot persist WP frontmatter/activity metadata on protected branch '%s' while coordination topology is active: %s. Rerun from an allowed branch, omit those metadata flags, use --no-auto-commit, or run `spec-kitty doctor mission-state --fix --mission %s` if canonical state also needs to be re-established.",
				st.targetBranch, strings.Join(unsupported, ", "), st.m.Slug)
			return &refusal{err: msg, diag: jsonx.NewObject("error", "WP_METADATA_UNSUPPORTED_ON_PROTECTED_COORD_BRANCH", "target_branch", st.targetBranch, "fields", unsupported)}
		}
	}
	if r := st.guardOwnership(); r != nil {
		return r
	}
	return st.guardDoneExit()
}

// authorizeReviewOverride is _authorize_review_override.
func (st *moveState) authorizeReviewOverride() bool {
	return (st.target == string(status.Approved) || st.target == string(status.Done)) && st.reviewArtifactName != "" &&
		st.reviewVerdict == "rejected" && st.skipArtifact && strings.TrimSpace(st.note) != ""
}

func (st *moveState) guards() *refusal {
	if r := st.precedingGuards(); r != nil {
		return r
	}
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		if st.reviewArtifactName != "" {
			if st.reviewVerdict == "" {
				return &refusal{err: fmt.Sprintf("%s has no parseable review verdict: the review result recorded in the status event log is unreadable.\nSend %s back with `spec-kitty agent tasks move-task %s --to planned --review-feedback-file <file>`, then run a new review before approving or marking done.", st.taskID, st.taskID, st.taskID)}
			}
			if st.reviewVerdict == "rejected" && st.skipArtifact && strings.TrimSpace(st.note) == "" {
				return &refusal{err: "--skip-review-artifact-check requires --note so override evidence is durable."}
			}
		}
	}
	if st.hasFeedback {
		if !st.feedbackExists {
			return &refusal{err: "Review feedback file not found: " + st.feedbackSource}
		}
		if !st.feedbackIsFile {
			return &refusal{err: "Review feedback path is not a file: " + st.feedbackSource}
		}
	}
	if isReviewRejectionEdge(st.oldLane, st.target) {
		if !(st.hasFeedback && st.feedbackExists && st.feedbackIsFile) {
			return &refusal{err: plannedRollbackMessage(st.taskID, st.oldLane, st.target)}
		}
		if strings.TrimSpace(st.feedbackContent) == "" {
			return &refusal{err: "Review feedback file is empty: " + st.feedbackSource}
		}
	}
	if !st.force && (st.target == string(status.ForReview) || st.target == string(status.Approved) || st.target == string(status.Done)) && len(st.unchecked) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Cannot move %s to %s - unchecked subtasks:\n", st.taskID, st.target)
		for _, t := range st.unchecked {
			fmt.Fprintf(&b, "  - [ ] %s\n", t)
		}
		b.WriteString("\nMark these complete first:\n")
		for i, t := range st.unchecked {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  spec-kitty agent tasks mark-status %s --status done\n", t)
		}
		b.WriteString("\nOr use --force to override (not recommended)")
		return &refusal{err: b.String()}
	}
	if (st.target == string(status.ForReview) || st.target == string(status.Approved) || st.target == string(status.Done)) && !st.reviewReady {
		msg := fmt.Sprintf("Cannot move %s to %s\n\n", st.taskID, st.target) + strings.Join(st.reviewGuidance, "\n")
		if !st.force {
			msg += "\n\nOr use --force to override (not recommended)"
		}
		return &refusal{err: msg}
	}
	if st.target == string(status.Done) && st.doneExecMode == "code_change" && !st.doneMerged && strings.TrimSpace(st.doneOverride) == "" {
		return &refusal{err: fmt.Sprintf("Cannot move %s to done without verified merge ancestry.\n%s\nIf review just passed, move it to approved first:\n  spec-kitty agent tasks move-task %s --to approved --note \"Review passed\"\nTo proceed anyway, provide --done-override-reason \"<why this is acceptable>\".", st.taskID, st.doneMergeMsg, st.taskID)}
	}
	if (st.target == string(status.Approved) || st.target == string(status.Done)) && st.issueMatrixBlocker != "" {
		return &refusal{err: st.issueMatrixBlocker}
	}
	return nil
}

func (st *moveState) guardOwnership() *refusal {
	cur, req := status.ActorKeyStr(st.currentAgent), status.ActorKeyStr(st.agent)
	if status.GenericImplementationActors[cur] {
		return nil
	}
	if !(cur != "" && req != "" && cur != req && !st.force) {
		return nil
	}
	if st.roleAllowance() {
		return nil
	}
	warning := []string{"", "⚠️  AGENT OWNERSHIP WARNING", fmt.Sprintf("   %s is currently assigned to: %s", st.taskID, st.currentAgent),
		fmt.Sprintf("   You are trying to move it as: %s", st.agent), "", "   If you are the correct agent, use --force to override.",
		"   If not, you may be modifying the wrong WP!",
		"   After a review rejection, the implementer resumes with its own --agent and a reviewer distinct from the implementer can review; neither needs --force.", ""}
	msg := fmt.Sprintf("Agent mismatch: %s is assigned to '%s', not '%s'. Use --force to override.", st.taskID, st.currentAgent, st.agent)
	tl, cl := status.ResolveLaneAlias(st.target), status.ResolveLaneAlias(st.oldLane)
	var diag *jsonx.Object
	if st.autoCommit && ((tl == string(status.Planned) && st.hasFeedback) || tl == string(status.Approved) || tl == string(status.Done)) {
		diag = jsonx.NewObject("result", "error", "code", "ownership_refusal", "error", msg, "current_lane", cl, "requested_lane", tl,
			"assigned_agent", st.currentAgent, "requesting_agent", st.agent, "verdict_durably_persisted", false, "evidence_ref", nil, "destination_ref", nil)
	}
	return &refusal{err: msg, diag: diag, warning: warning}
}

func (st *moveState) roleAllowance() bool {
	lk := ""
	if st.latestImplementer != "" {
		lk = status.ActorKeyStr(st.latestImplementer)
	}
	if lk == "" || status.GenericImplementationActors[lk] {
		return false
	}
	o, t := status.ResolveLaneAlias(st.oldLane), status.ResolveLaneAlias(st.target)
	rk := status.ActorKeyStr(st.agent)
	reviewer := o == string(status.ForReview) && (t == string(status.InReview) || t == string(status.Approved) || t == string(status.Planned)) &&
		!status.GenericImplementationActors[rk] && rk != lk
	implementer := (o == string(status.Planned) || o == string(status.Claimed) || o == string(status.InProgress)) &&
		(t == string(status.Claimed) || t == string(status.InProgress) || t == string(status.ForReview)) &&
		status.IsLatestImplementer(st.latestImplementer, st.agent)
	return reviewer || implementer
}

func (st *moveState) guardDoneExit() *refusal {
	if st.force || status.ResolveLaneAlias(st.oldLane) != string(status.Done) {
		return nil
	}
	t := status.ResolveLaneAlias(st.target)
	if !status.IsBackwardTransition(string(status.Done), t) {
		return nil
	}
	if t == string(status.Planned) && !(st.hasFeedback && st.feedbackExists && st.feedbackIsFile && strings.TrimSpace(st.feedbackContent) != "") {
		return nil
	}
	fb := ""
	if t == string(status.Planned) {
		fb = " --review-feedback-file <file>"
	}
	return &refusal{err: fmt.Sprintf("❌ Cannot move %s from 'done' to '%s' without --force: 'done' is a terminal lane, and leaving it needs an explicit force.\n\nTo rework this work package deliberately, run:\n  spec-kitty agent tasks move-task %s --mission %s --to %s%s --force --note \"<why>\"\n\nFor a merged mission, reopen it first (this does not move work package lanes):\n  spec-kitty mission reopen %s --reason \"<why>\"",
		st.taskID, t, st.taskID, st.m.Slug, t, fb, st.m.Slug)}
}

func plannedRollbackMessage(taskID, old, target string) string {
	src := status.Lane(status.ResolveLaneAlias(old))
	tgt := status.ResolveLaneAlias(target)
	allowed := status.AllowedTargets(src)
	if status.MayTransition(src, status.Planned) {
		return fmt.Sprintf("❌ Moving %s to '%s' requires review feedback.\n\nPlease provide feedback:\n  1. Create feedback file: echo '**Issue**: Description' > feedback.md\n  2. Run: spec-kitty agent tasks move-task %s --to %s --review-feedback-file feedback.md\n\nThis requirement cannot be bypassed with --force.", taskID, tgt, taskID, tgt)
	}
	s := fmt.Sprintf("❌ Cannot move %s from '%s' to 'planned': 'planned' is not reachable from '%s'.", taskID, src, src)
	if len(allowed) == 0 {
		return s + fmt.Sprintf("\n\n'%s' is a terminal lane with no onward transitions.", src)
	}
	var legal []string
	for _, l := range allowed {
		legal = append(legal, "--to "+string(l))
	}
	s += fmt.Sprintf("\n\nLegal targets from '%s': %s.", src, strings.Join(legal, ", "))
	if src == status.Blocked && status.MayTransition(src, status.InProgress) {
		s += fmt.Sprintf("\nTo resume this work package, run: spec-kitty agent tasks move-task %s --to in_progress.", taskID)
	}
	return s
}

func (st *moveState) gatherLateFacts() {
	if st.target == string(status.Done) {
		ws, err := lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
		if err != nil {
			st.doneExecMode = "code_change"
		} else {
			st.doneExecMode = ws.ExecutionMode
		}
		if st.doneExecMode == "code_change" {
			st.doneMerged, st.doneMergeMsg = st.branchMergedIntoTarget(ws)
		}
	}
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		st.effectiveReviewer = st.reviewer
		if st.effectiveReviewer == "" {
			st.effectiveReviewer = detectReviewerName()
		}
		st.effectiveApproval = st.approvalRef
		if st.effectiveApproval == "" {
			st.effectiveApproval = review.SyntheticAutoApprovalRef(st.taskID, clock.Now().UTC().Format("20060102"))
		}
	}
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		st.issueMatrixBlocker = issuematrix.ApprovalBlocker(st.m.ReadDir(), st.target, st.m.PrimaryDir())
	}
	st.isArbiter = st.arbiterOverride()
}

func (st *moveState) branchMergedIntoTarget(ws *lanes.Workspace) (bool, string) {
	if ws == nil {
		var err error
		ws, err = lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
		if err != nil {
			return false, err.Error()
		}
	}
	if ws.BranchName == "" {
		return false, fmt.Sprintf("Cannot verify merge ancestry: no branch name resolved for workspace of %s.\nEither merge and keep the branch ref available, or provide --done-override-reason.", st.taskID)
	}
	if !gitx.OK(st.root, "rev-parse", "--verify", ws.BranchName) {
		return false, fmt.Sprintf("Cannot verify merge ancestry: branch '%s' not found.\nEither merge and keep branch ref available, or provide --done-override-reason.", ws.BranchName)
	}
	if gitx.OK(st.root, "merge-base", "--is-ancestor", ws.BranchName, st.targetBranch) {
		return true, fmt.Sprintf("Merge ancestry verified: %s is merged into %s.", ws.BranchName, st.targetBranch)
	}
	return false, fmt.Sprintf("Merge ancestry check failed: %s is not merged into %s.\nMerge first, or provide --done-override-reason to record a conscious exception.", ws.BranchName, st.targetBranch)
}

func detectReviewerName() string {
	out, err := gitx.Run("", "config", "user.name")
	if err != nil || strings.TrimSpace(out) == "" {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

func (st *moveState) printRefusal(r *refusal) error {
	if !st.json {
		for _, l := range r.warning {
			consolePrint(st.ctx.Stdout, l)
		}
	}
	return st.outDiag(r.err, r.diag)
}

func (st *moveState) runDecision() error {
	if st.precedingGuards() == nil && st.authorizeReviewOverride() && st.verdictArtifact != "" {
		actor := st.agent
		if actor == "" {
			actor = "operator"
		}
		if err := st.persistReviewOverride(actor, strings.TrimSpace(st.note)); err != nil {
			return err
		}
	}
	r := st.guards()
	if r == nil {
		st.gatherLateFacts()
		// arbiter_persist_signal: the OLD-timing arbiter persist fires when
		// every guard preceding the issue-matrix guard (the last one) clears,
		// so an issue-matrix refusal still leaves the arbiter write on disk.
		blocker := st.issueMatrixBlocker
		st.issueMatrixBlocker = ""
		preceding := st.guards()
		st.issueMatrixBlocker = blocker
		if preceding == nil && st.isArbiter {
			if err := st.runArbiterOverride(); err != nil {
				return err
			}
		}
		r = st.guards()
	}
	if r != nil {
		return st.printRefusal(r)
	}
	st.decide()
	return nil
}

// persistReviewOverride is _persist_review_artifact_override: an
// uncommitted InnerStateChanged `review` delta on the status read surface.
func (st *moveState) persistReviewOverride(actor, reason string) error {
	stamp := clock.Now().UTC().Format("2006-01-02T15:04:05Z")
	a, err := status.NewAnnotation(st.taskID, status.Delta{Review: &status.ReviewOverride{At: stamp, Actor: actor, WPID: st.taskID, Reason: reason}}, status.StrActor(actor), stamp)
	if err != nil {
		return err
	}
	dir := st.m.ReadDir()
	if err := status.AppendEvents(dir, a); err != nil {
		return err
	}
	status.Materialize(dir)
	return nil
}

var arbiterNoteRe = regexp.MustCompile(`(?s)^\s*\[([a-z_]+)\]\s*(.*)`)

var arbiterDefaults = map[string]string{
	"pre_existing_failure": "Failure is pre-existing on the base branch and unrelated to this WP.",
	"wrong_context":        "Reviewer is discussing the wrong feature or WP.",
	"cross_scope":          "Finding is outside this WP's defined scope.",
	"infra_environmental":  "Failure is environmental or infrastructure-related, not a code defect.",
	"custom":               "",
}

// parseArbiterNote is review.arbiter.parse_category_from_note.
func parseArbiterNote(note string) (string, string) {
	if note == "" {
		return "custom", "Override without explanation"
	}
	if m := arbiterNoteRe.FindStringSubmatch(note); m != nil {
		cat, expl := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if _, ok := arbiterDefaults[cat]; !ok {
			cat, expl = "custom", strings.TrimSpace(note)
		}
		if expl == "" {
			expl = arbiterDefaults[cat]
			if expl == "" {
				expl = "Override: " + cat
			}
		}
		return cat, expl
	}
	return "custom", strings.TrimSpace(note)
}

// arbiterOverride is review.arbiter.is_arbiter_override_history: a forced
// planned -> approved/done move whose latest WP event is a review rejection.
func (st *moveState) arbiterOverride() bool {
	t := status.ResolveLaneAlias(st.target)
	if !st.force || st.oldLane != string(status.Planned) || (t != string(status.Approved) && t != string(status.Done)) {
		return false
	}
	stream, err := status.ReadStream(st.featureDir, "")
	if err != nil {
		return false
	}
	var last *status.Event
	for _, e := range stream.Transitions {
		if e.WPID == st.taskID {
			last = e
		}
	}
	return last != nil && last.To == status.Planned && (last.From == status.ForReview || last.From == status.InReview) && last.ReviewRef != nil
}

// latestCycleNumber is ReviewCycleArtifact.latest_cycle_number (filenames
// only; 0 when none).
func latestCycleNumber(dir string) int {
	max := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		var n int
		if _, err := fmt.Sscanf(e.Name(), "review-cycle-%d.md", &n); err == nil && e.Name() == fmt.Sprintf("review-cycle-%d.md", n) && n > max {
			max = n
		}
	}
	return max
}

// runArbiterOverride is _run_arbiter_override: persist the decision as a
// ReviewOverride annotation and remember the rejection's review_ref.
func (st *moveState) runArbiterOverride() error {
	note := strings.TrimSpace(st.note)
	if st.target == string(status.Done) && st.doneExecMode == "code_change" && !st.doneMerged {
		if o := strings.TrimSpace(st.doneOverride); o != "" {
			if note != "" {
				note += " | Done override: " + o
			} else {
				note = "Done override: " + o
			}
		}
	}
	if stream, err := status.ReadStream(st.featureDir, ""); err == nil {
		for _, e := range stream.Transitions {
			if e.WPID == st.taskID {
				st.arbReviewRef = e.ReviewRef
			}
		}
	}
	cat, expl := parseArbiterNote(note)
	actor := st.agent
	if actor == "" {
		actor = "operator"
	}
	sub := filepath.Join(st.m.ReadDir(), "tasks", st.w.Stem())
	path := filepath.Join(sub, fmt.Sprintf("review-cycle-%d.md", latestCycleNumber(sub)))
	if err := st.persistReviewOverride(actor, fmt.Sprintf("[%s] %s", cat, expl)); err != nil {
		return err
	}
	st.console(fmt.Sprintf("Arbiter override recorded: %s — %s", cat, expl))
	st.console("  Decision persisted: " + path)
	return nil
}

func (st *moveState) decide() {
	user := strings.TrimSpace(st.note)
	st.noteText = user
	st.doneOverrideNote = false
	if st.target == string(status.Done) && st.doneExecMode == "code_change" && !st.doneMerged {
		if o := strings.TrimSpace(st.doneOverride); o != "" {
			on := "Done override: " + o
			if st.noteText != "" {
				st.noteText = st.noteText + " | " + on
			} else {
				st.noteText = on
			}
			st.doneOverrideNote = true
		}
	}
	st.plan = buildTransitionPlan(st.oldLane, st.target, st.force, "", nil, st.noteText, nil)
	st.evidence = nil
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		ref := st.effectiveApproval
		if ref == "" {
			ref = "force-override"
		}
		st.evidence = &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: st.effectiveReviewer, Verdict: "approved", Reference: ref}}
	}
}

// wireAllowsForceFree mirrors the shared spec-kitty-events contract: the
// review-rejection family (* -> planned out of review/implementation lanes,
// in_review -> in_progress) requires force.
func wireAllowsForceFree(old, nw string, reason, reviewRef *string) bool {
	if old == string(status.Done) || old == string(status.Canceled) {
		return false
	}
	if nw == string(status.Planned) && (old == string(status.InProgress) || old == string(status.ForReview) || old == string(status.InReview) || old == string(status.Approved)) {
		return false
	}
	if !wireMatrix[old+">"+nw] && nw != string(status.Blocked) && nw != string(status.Canceled) {
		return false
	}
	if (old == string(status.ForReview) || old == string(status.InReview) || old == string(status.Approved)) &&
		(nw == string(status.InProgress) || nw == string(status.Planned)) && (reviewRef == nil || strings.TrimSpace(*reviewRef) == "") {
		return false
	}
	if old == string(status.InProgress) && nw == string(status.Planned) && (reason == nil || strings.TrimSpace(*reason) == "") {
		return false
	}
	return true
}

// wireMatrix is spec_kitty_events.status._ALLOWED_TRANSITIONS.
var wireMatrix = map[string]bool{
	"genesis>planned": true, "planned>claimed": true, "claimed>in_progress": true, "in_progress>for_review": true,
	"in_progress>approved": true, "for_review>in_review": true, "for_review>approved": true, "for_review>done": true,
	"in_review>approved": true, "in_review>done": true, "approved>done": true, "for_review>in_progress": true,
	"for_review>planned": true, "in_review>in_progress": true, "in_review>for_review": true, "in_review>planned": true,
	"approved>in_progress": true, "approved>planned": true, "in_progress>planned": true, "blocked>in_progress": true,
}

func buildTransitionPlan(old, target string, force bool, feedbackPointer string, arbReviewRef *string, noteText string, rr *status.ReviewResult) *transitionPlan {
	canonical := status.ResolveLaneAlias(target)
	var reviewRef *string
	if isReviewRejectionEdge(old, target) && feedbackPointer != "" {
		reviewRef = strp(feedbackPointer)
	} else if old == string(status.ForReview) && (canonical == string(status.InProgress) || canonical == string(status.Planned)) && force {
		reviewRef = strp("force-override")
	}
	if reviewRef == nil && arbReviewRef != nil && *arbReviewRef != "" {
		reviewRef = strp(*arbReviewRef)
	}
	if reviewRef == nil && rr != nil && strings.TrimSpace(rr.Reference) != "" {
		reviewRef = strp(rr.Reference)
	}
	var reason *string
	if noteText != "" {
		reason = strp(noteText)
	}
	bareReviewExitForce := force && reason == nil && rr == nil && (old == string(status.InReview) || old == string(status.Approved))
	if force && reason == nil && !bareReviewExitForce {
		reason = strp("Force move to " + target)
	}
	emitForce := force
	if reason == nil && !bareReviewExitForce {
		if force {
			reason = strp("Force move to " + target)
		} else {
			reason = strp(fmt.Sprintf("move-task: %s -> %s", old, target))
		}
	}
	if !force && status.ResolveLaneAlias(old) != string(status.Done) && status.IsBackwardTransition(old, canonical) {
		ok, _ := status.ValidateTransition(old, canonical, status.GuardContext{Reason: reason, ReviewRef: reviewRef, ReviewResult: rr})
		emitForce = !(ok && wireAllowsForceFree(old, canonical, reason, reviewRef))
		var original string
		if reason != nil && !strings.HasPrefix(*reason, "move-task: ") {
			original = *reason
		}
		parts := []string{fmt.Sprintf("backward rewind: %s -> %s", old, canonical)}
		if feedbackPointer != "" && feedbackPointer != "force-override" {
			parts = append(parts, feedbackPointer)
		}
		if original != "" {
			parts = append(parts, original)
		}
		reason = strp(strings.Join(parts, ": "))
	}
	targets := []string{canonical}
	if !emitForce {
		targets = status.LaneTargetsForEmit(old, canonical)
	}
	return &transitionPlan{canonical: canonical, targets: targets, force: emitForce, reason: reason, reviewRef: reviewRef}
}

func strp(s string) *string { return &s }

// --- phase C.5: pre-review gate ------------------------------------------

func gateMetadata(outcome, reason string) *jsonx.Object {
	return jsonx.NewObject("outcome", outcome, "reason", reason, "new_failure_count", 0, "new_failure_nodeids", []any{},
		"pre_existing_failure_count", 0, "affected_shard_count", 0, "matched_shard_groups", []any{}, "matched_composite_dirs", []any{},
		"test_targets", []any{}, "block_enabled", false, "blocked", false, "force_bypassed", false, "run_state", "completed")
}

func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y", "t":
		return true
	}
	return false
}

// --- deferred for_review readiness ----------------------------------------

func (st *moveState) completeDeferredReadiness() error {
	if !(st.target == string(status.ForReview) && st.autoCommit && !st.force) {
		return nil
	}
	if err := st.commitLaneDeliverables(); err != nil {
		return err
	}
	if st.owned != nil {
		if err := st.requireOwnedImplementation(); err != nil {
			return err
		}
	}
	ok, guidance, err := st.validateReadyForReview()
	if err != nil {
		return err
	}
	st.reviewReady, st.reviewGuidance = ok, guidance
	return st.runDecision()
}

func (st *moveState) commitLaneDeliverables() error {
	var ws *lanes.Workspace
	if st.owned != nil {
		ws = st.ownedWorkspace()
	} else {
		var err error
		ws, err = lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
		if err != nil {
			return nil
		}
	}
	if ws.BranchName == "" {
		return nil
	}
	if st.owned == nil && ws.ResolutionKind == "repo_root" {
		st.console(fmt.Sprintf("Note: %s runs in the repository root checkout; commit your WP work yourself before moving to for_review (no auto-commit).", st.taskID))
		return nil
	}
	if !isDir(ws.WorktreePath) {
		return nil
	}
	entries, err := statusEntries(ws.WorktreePath)
	if err != nil {
		return nil
	}
	entries = filterRuntimeState(entries)
	var paths []string
	for _, e := range entries {
		for _, p := range []string{e.origPath, e.path} {
			if p == "" || isRuntimeStatePath(p) || isCoordResidue(p) {
				continue
			}
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	if st.owned != nil {
		if err := st.ownedDeliverableScope(paths); err != nil {
			return err
		}
	}
	if err := safeCommitPaths(ws.WorktreePath, fmt.Sprintf("chore(%s): commit lane deliverables for review", st.taskID), paths, st.logWarn); err != nil {
		st.console(fmt.Sprintf("Warning: could not auto-commit lane deliverables for %s: %v", st.taskID, err))
		return nil
	}
	tipRoot := st.root
	if st.owned != nil {
		tipRoot = st.owned.RepositoryRoot
	}
	lanes.RecordTip(tipRoot, ws.BranchName)
	st.console(fmt.Sprintf("Committed lane deliverables for %s on %s before review.", st.taskID, ws.BranchName))
	return nil
}

func isCoordResidue(rel string) bool { return mission.IsCoordResidue(rel) }

func isOccurrenceMap(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "kitty-specs" && parts[2] == "occurrence_map.yaml"
}

// safeCommitPaths is the safe_commit primitive for worktree-relative paths:
// `add --force -- paths` + `commit --only -- paths`, then refuse (after the
// commit landed) when the commit carries unrequested paths.
func safeCommitPaths(dir, message string, paths []string, warn func(string)) error {
	if _, err := gitx.Run(dir, append([]string{"add", "--force", "--"}, paths...)...); err != nil {
		return err
	}
	res, err := gitx.Exec(dir, nil, nil, append([]string{"commit", "-q", "--only", "-m", message, "--"}, paths...)...)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(res.Stdout+"\n"+res.Stderr))
	}
	if e := strings.TrimSpace(res.Stderr); e != "" && warn != nil {
		warn(fmt.Sprintf("git commit in %s produced warnings on a successful commit: %s", dir, e))
	}
	sha, _ := gitx.HeadSHA(dir)
	out, err := gitx.Run(dir, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", "HEAD")
	if err != nil {
		return nil
	}
	req := map[string]bool{}
	for _, p := range paths {
		req[strings.TrimSuffix(filepath.ToSlash(p), "/")] = true
	}
	var extra []string
	for _, f := range strings.Split(strings.TrimSpace(out), "\n") {
		if f != "" && !req[f] {
			extra = append(extra, f)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("safe_commit: commit %s landed, but it also carries path(s) the operation did not request (a pre-commit hook staged them): %s", strings.TrimSpace(sha), strings.Join(extra, ", "))
	}
	return nil
}

// --- phase D: finalize plan -------------------------------------------------

func (st *moveState) resolveReviewerIdentity() string {
	for _, s := range []string{st.reviewer, st.agent, st.actor} {
		if s != "" {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
			return "unknown"
		}
	}
	return "unknown"
}

func (st *moveState) activeReviewerIdentity() string {
	stream, err := status.ReadStream(st.featureDir, "")
	if err != nil {
		return ""
	}
	for i := len(stream.Transitions) - 1; i >= 0; i-- {
		e := stream.Transitions[i]
		if e.WPID == st.taskID && e.To == status.InReview {
			return strings.TrimSpace(e.Actor.Identity())
		}
	}
	return ""
}

func (st *moveState) finalizePlan() error {
	st.actor = st.agent
	if st.actor == "" {
		st.actor = st.activeReviewerIdentity()
	}
	if st.actor == "" {
		st.actor = "user"
	}
	st.canonicalLane = st.plan.canonical
	isRejection := isReviewRejectionEdge(st.oldLane, st.target)
	if isRejection && st.feedbackSource != "" && st.feedbackExists && st.feedbackIsFile {
		reviewer := st.resolveReviewerIdentity()
		c, err := review.Create(review.CreateInput{Surface: st.m.Surface, WPID: st.taskID, WPSlug: st.w.Stem(),
			FeedbackSource: st.feedbackSource, Reviewer: reviewer, Verdict: "rejected", Commit: st.autoCommit && !st.skipPrimary})
		if err != nil {
			return err
		}
		if err := st.checkVerdictDurable(c); err != nil {
			return err
		}
		st.verdictWrite = c
		st.feedbackPointer = c.Pointer
		rr := c.Result
		st.rejectedResult = &rr
		st.announceDurabilityGap(c)
	}
	if (st.target == string(status.Approved) || st.target == string(status.Done)) && !st.isArbiter {
		c, err := st.persistApprovedCycle()
		if err != nil {
			return err
		}
		if c != nil {
			st.verdictWrite = c
			if c.Outcome.Durable && st.evidence != nil {
				st.evidence.Review = status.ReviewApproval{Reviewer: c.Result.Reviewer, Verdict: c.Result.Verdict, Reference: c.Result.Reference}
			}
		}
	}
	if st.doneOverrideNote {
		st.console("⚠️  Proceeding with done override; reason recorded in history/events.")
	}
	st.planReviewResult = st.planReview()
	if isRejection || st.isArbiter {
		st.plan = buildTransitionPlan(st.oldLane, st.target, st.force, st.feedbackPointer, st.arbReviewRef, st.noteText, st.planReviewResult)
	}
	return nil
}

// revertVerdictWrite is revert_committed_verdict_write (FR-002): when the
// transition emit fails after a review-cycle verdict was durably committed,
// delete the artifact and commit the deletion on the same surface.
func (st *moveState) revertVerdictWrite() error {
	c := st.verdictWrite
	if c == nil || !c.Outcome.Durable || c.ArtifactPath == "" {
		return nil
	}
	if _, err := os.Stat(c.ArtifactPath); err != nil {
		return nil
	}
	if err := os.Remove(c.ArtifactPath); err != nil {
		return err
	}
	root, _, err := review.WriteRoot(st.m.Surface)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("revert: undo review-cycle-%d verdict for %s on %s (transition emit failed, FR-002)", c.CycleNumber, st.taskID, st.m.Slug)
	if _, err := coord.CommitPaths(root, msg, c.ArtifactPath); err != nil {
		return fmt.Errorf("Deleted %s on disk but could not commit the deletion (%v). A committed verdict may still be reachable at a prior commit for %s -- operator attention required.", c.ArtifactPath, err, st.taskID)
	}
	return nil
}

func (st *moveState) checkVerdictDurable(c *review.Cycle) error {
	if !st.autoCommit || c.Outcome.Durable {
		return nil
	}
	if st.skipPrimary {
		return fmt.Errorf("Automatic verdict evidence could not be committed because the governed destination branch is protected; complete evidence is retained at %s.", deref(c.Outcome.EvidenceRef))
	}
	return fmt.Errorf("Review-cycle evidence for %s could not be committed (%s).", st.taskID, deref(c.Outcome.Reason))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (st *moveState) announceDurabilityGap(c *review.Cycle) {
	if st.json || c.Outcome.Durable || c.Outcome.Reason == nil {
		return
	}
	if *c.Outcome.Reason == "no_auto_commit" {
		st.console(fmt.Sprintf("Note: review-cycle verdict for %s was written but NOT committed (--no-auto-commit).", st.taskID))
	}
}

func (st *moveState) persistApprovedCycle() (*review.Cycle, error) {
	snap, _ := status.ReduceDir(st.featureDir)
	if state := snap.WP(st.taskID); state != nil {
		if raw, ok := state.Get("review_result"); ok {
			rr, isObj := raw.(*jsonx.Object)
			if raw != nil && (!isObj || rr.GetString("verdict") == "") {
				return nil, nil
			}
			if isObj && rr.GetString("verdict") != "changes_requested" {
				return nil, nil
			}
		}
	}
	reviewer := st.resolveReviewerIdentity()
	ref := strings.TrimSpace(st.approvalRef)
	if ref == "" {
		ref = review.SyntheticApprovalRef(st.taskID)
	}
	repro := fmt.Sprintf("spec-kitty agent tasks move-task %s --to approved --mission %s --agent %s", st.taskID, st.m.Slug, reviewer)
	c, err := review.Create(review.CreateInput{Surface: st.m.Surface, WPID: st.taskID, WPSlug: st.w.Stem(),
		Body: fmt.Sprintf("Approved by %s: %s\n", reviewer, ref), Reviewer: reviewer, Verdict: "approved",
		ReproductionCommand: &repro, Commit: st.autoCommit && !st.skipPrimary})
	if err != nil {
		return nil, err
	}
	if err := st.checkVerdictDurable(c); err != nil {
		return nil, err
	}
	st.announceDurabilityGap(c)
	return c, nil
}

func (st *moveState) planReview() *status.ReviewResult {
	if st.oldLane != string(status.InReview) {
		return nil
	}
	if st.rejectedResult != nil {
		return st.rejectedResult
	}
	reviewer := st.resolveReviewerIdentity()
	if st.target == string(status.Approved) || st.target == string(status.Done) {
		if st.verdictWrite != nil && st.verdictWrite.Outcome.Durable {
			rr := st.verdictWrite.Result
			return &rr
		}
		ref := strings.TrimSpace(st.approvalRef)
		if ref == "" {
			ref = review.SyntheticApprovalRef(st.taskID)
		}
		return &status.ReviewResult{Reviewer: reviewer, Verdict: "approved", Reference: ref}
	}
	if !isReviewRejectionEdge(st.oldLane, st.target) {
		return nil
	}
	ref := strings.TrimSpace(st.feedbackPointer)
	if ref == "" {
		ref = review.SyntheticReviewRef(st.taskID)
	}
	return &status.ReviewResult{Reviewer: reviewer, Verdict: "changes_requested", Reference: ref}
}

// --- phase E: execute -------------------------------------------------------

func bindingRoleForLane(l string) string {
	switch l {
	case string(status.Claimed):
		return "implementer"
	case string(status.InReview), string(status.Approved), string(status.Done):
		return "reviewer"
	}
	return ""
}

func (st *moveState) hopActor(prev *status.Event, current, target string) string {
	from := status.ResolveLaneAlias(current)
	if prev != nil {
		from = string(prev.To)
	}
	if st.agent != "" {
		return st.agent
	}
	if from == string(status.InProgress) && target == string(status.ForReview) && st.currentAgent != "" {
		return st.currentAgent
	}
	if from == string(status.InReview) {
		if r := st.activeReviewerIdentity(); r != "" {
			return r
		}
	}
	return "user"
}

func (st *moveState) hopReviewResult(prev *status.Event, current, target, hopActor string) *status.ReviewResult {
	inReview := (prev != nil && prev.To == status.InReview) || (prev == nil && current == string(status.InReview))
	durable := st.verdictWrite != nil && st.verdictWrite.Outcome.Durable
	if isReviewRejectionEdge(st.oldLane, target) && st.rejectedResult != nil {
		if inReview {
			return st.rejectedResult
		}
		if durable {
			rr := st.verdictWrite.Result
			return &rr
		}
	}
	if inReview && st.planReviewResult != nil && durable {
		return st.planReviewResult
	}
	if inReview && st.evidence != nil {
		r := st.evidence.Review
		return &status.ReviewResult{Reviewer: r.Reviewer, Verdict: r.Verdict, Reference: r.Reference}
	}
	if inReview && st.planReviewResult != nil {
		return st.planReviewResult
	}
	return nil
}

func (st *moveState) hopPolicyMetadata(target string) *jsonx.Object {
	if target == string(status.Claimed) && st.shellPID != "" {
		pid, _ := strconv.Atoi(st.shellPID)
		agent := st.agent
		if agent == "" {
			agent = st.actor
		}
		if agent == "" {
			agent = "unknown"
		}
		baseline, _ := status.CreationTimeBaseline(pid)
		return jsonx.NewObject("shell_pid", pid, "shell_pid_created_at", baseline, "agent", agent)
	}
	if target == string(status.ForReview) && st.gateMeta != nil {
		return jsonx.NewObject("pre_review_gate", st.gateMeta)
	}
	if target == string(status.Approved) || target == string(status.Done) {
		tool := st.effectiveReviewer
		for _, s := range []string{st.reviewer, st.agent, st.actor} {
			if tool == "" {
				tool = s
			}
		}
		if tool == "" {
			tool = "unknown"
		}
		var prof, model any
		if st.binding != nil && st.binding.AgentProfile != nil {
			prof = *st.binding.AgentProfile
		}
		if st.binding != nil && st.binding.Model != nil {
			model = *st.binding.Model
		}
		o := jsonx.NewObject("tool", tool, "profile", prof, "model", model)
		if st.shellPID != "" {
			o.Set("shell_pid", st.shellPID)
		}
		return o
	}
	return nil
}

func (st *moveState) execute() error {
	current, err := st.currentEventLane()
	if err != nil {
		return err
	}
	ws, wsErr := lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
	execMode := "worktree"
	if wsErr == nil {
		execMode = ws.StatusExecutionMode()
	}
	plan := st.plan
	reviewRef := plan.reviewRef
	var prev *status.Event
	st.finalHopActor = st.actor
	for _, target := range plan.targets {
		if prev != nil {
			st.authoritativeLane = string(prev.To)
		} else {
			st.authoritativeLane = status.ResolveLaneAlias(current)
		}
		hopActor := st.hopActor(prev, current, target)
		hopRR := st.hopReviewResult(prev, current, target, hopActor)
		actor := status.StrActor(hopActor)
		var delta *status.Delta
		if role := bindingRoleForLane(target); role != "" && st.binding != nil {
			actor = status.SelfAssertingActor(role, st.agent, hopActor, st.binding)
			d := st.binding.ToDelta(role)
			if target == string(status.Claimed) && st.agent != "" {
				a := st.agent
				d.Agent = &a
			}
			delta = &d
		}
		if target == string(status.Claimed) && st.shellPID != "" {
			st.claimEmitted = true
		}
		var reasonSource *string
		if target == string(status.Canceled) {
			if strings.TrimSpace(st.note) != "" {
				reasonSource = strp("operator")
			} else {
				reasonSource = strp("synthetic")
			}
		}
		var ev *status.DoneEvidence
		if target == string(status.Approved) || target == string(status.Done) {
			ev = st.evidence
		}
		hopRef := reviewRef
		if hopRef == nil && (target == string(status.Approved) || target == string(status.Done)) && hopRR != nil && strings.TrimSpace(hopRR.Reference) != "" {
			hopRef = strp(hopRR.Reference)
		}
		var sc, ie *bool
		if (target == string(status.ForReview) || target == string(status.Approved)) && !plan.force {
			sc, ie = status.Bool(true), status.Bool(true)
		}
		ws := "move-task:" + st.root
		res, err := st.m.Surface.EmitTransition(&status.TransitionRequest{
			WPID: st.taskID, ToLane: target, Actor: actor, Force: plan.force, Reason: plan.reason, ReasonSource: reasonSource,
			Evidence: ev, ReviewRef: hopRef, WorkspaceContext: &ws, SubtasksComplete: sc, ImplementationEvidence: ie,
			ExecutionMode: execMode, RepoRoot: st.root, PolicyMetadata: st.hopPolicyMetadata(target), ReviewResult: hopRR,
			AnnotationDelta: delta,
		})
		if err != nil {
			return err
		}
		prev = res.Event
		st.transitionApplied = true
		st.finalHopActor = hopActor
		reviewRef = nil
	}
	if st.selfReviewFallback {
		if _, err := lifecycle.EmitReviewerSelfApproval(st.featureDir, st.m.Slug, st.taskID, st.finalHopActor,
			strings.TrimSpace(st.intendedReviewer), strings.TrimSpace(st.reviewerFailureReason)); err != nil {
			return err
		}
	}
	st.event = prev
	st.warnApproval()
	if err := st.emitRuntimeState(); err != nil {
		return err
	}
	st.releaseReviewLock()
	return nil
}

func (st *moveState) warnApproval() {
	for _, w := range approvalWarnings(st.event, st.m.PrimaryDir(), st.featureDir) {
		fmt.Fprintln(st.ctx.Stderr, w)
	}
}

func (st *moveState) emitRuntimeState() error {
	d := status.Delta{}
	if !st.claimEmitted {
		if st.agent != "" && !(st.target == string(status.Planned) && status.ActorKeyStr(st.agent) == status.ActorKeyStr(st.currentAgent)) {
			a := st.agent
			d.Agent = &a
			if st.binding != nil {
				role := bindingRoleForLane(st.target)
				if role == "" {
					role = "implementer"
				}
				bd := st.binding.ToDelta(role)
				d.Role, d.AgentProfile, d.AgentProfileVersion, d.Model, d.Provider = bd.Role, bd.AgentProfile, bd.AgentProfileVersion, bd.Model, bd.Provider
			}
		}
		if st.shellPID != "" {
			pid, err := strconv.Atoi(st.shellPID)
			if err != nil {
				return fmt.Errorf("invalid literal for int() with base 10: '%s'", st.shellPID)
			}
			d.ShellPID = &pid
			if b, ok := status.CreationTimeBaseline(pid); ok {
				d.ShellPIDCreatedAt = &b
			}
		}
	}
	if st.assignee != "" {
		a := st.assignee
		d.Assignee = &a
	}
	if st.noteText != "" {
		n := st.noteText
		d.Note = &n
	}
	if len(st.trackerRefs) > 0 {
		r := append([]string{}, st.trackerRefs...)
		d.TrackerRefs = &r
	}
	if st.target == string(status.Planned) {
		st.rollbackAdditions(&d)
	}
	d.Normalize()
	if d.IsEmpty() {
		return nil
	}
	actor := st.finalHopActor
	if actor == "" {
		actor = st.actor
	}
	a, err := st.m.Surface.EmitAnnotation(st.taskID, d, status.StrActor(actor), st.autoCommit)
	if a != nil && a.MaterializeFailed && !st.json {
		warnLog(st.ctx.Stderr, fmt.Sprintf("Materialization failed after annotation %s was persisted; run 'status materialize' to recover", a.EventID))
	}
	return err
}

func (st *moveState) rollbackAdditions(d *status.Delta) {
	roster, _ := wp.AuthoredRoster(st.m.PrimaryDir(), st.taskID)
	// Upstream reads the subtask snapshot from the PRIMARY (TASKS_INDEX)
	// dir, which carries no event log under a coordination topology.
	snap, _ := status.ReduceDir(st.m.PrimaryDir())
	var recorded *jsonx.Object
	if s := snap.WP(st.taskID); s != nil {
		recorded = s.GetObject("subtasks")
	}
	reset := &jsonx.Object{}
	var ids []string
	for _, id := range roster {
		if recorded != nil && recorded.GetString(id) == "skipped" {
			continue
		}
		reset.Set(id, string(status.Planned))
		ids = append(ids, id)
	}
	if reset.Len() > 0 {
		d.Subtasks = reset
	}
	d.ReleaseRuntimeClaim = true
	d.Review = &status.ReviewOverride{}
	sum := &rollbackSummary{resetIDs: []string{}, previouslyCompleted: []string{}, neverCompleted: []string{}}
	if len(ids) > 0 {
		notDone := map[string]bool{}
		for _, id := range status.UncheckedSubtasks(snap, st.taskID, ids) {
			notDone[id] = true
		}
		for _, id := range ids {
			sum.resetIDs = append(sum.resetIDs, id)
			if notDone[id] {
				sum.neverCompleted = append(sum.neverCompleted, id)
			} else {
				sum.previouslyCompleted = append(sum.previouslyCompleted, id)
			}
		}
	}
	st.rollback = sum
}

func (st *moveState) releaseReviewLock() {
	old := st.oldLane
	releaseFrom := old == string(status.ForReview) || old == string(status.InReview) || old == string(status.InProgress)
	releases := st.target == string(status.Approved) || st.target == string(status.Planned) || isReviewRejectionEdge(old, st.target)
	if !(releaseFrom && releases) {
		return
	}
	ws, err := lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
	if err != nil {
		return
	}
	dir := filepath.Join(ws.WorktreePath, ".spec-kitty")
	os.Remove(filepath.Join(dir, "review-lock.json"))
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}

// --- phase H: output ----------------------------------------------------------

func (st *moveState) output() {
	var eventID, toLane any
	if st.event != nil {
		if st.event.EventID != "" {
			eventID = st.event.EventID
		}
		toLane = string(st.event.To)
	}
	if toLane == nil {
		toLane = st.canonicalLane
	}
	eventsPath := status.EventsPath(st.featureDir)
	res := jsonx.NewObject("result", "success", "transition_applied", true, "task_id", st.taskID, "old_lane", st.oldLane,
		"new_lane", st.target, "path", st.w.Path, "event_id", eventID, "work_package_id", st.taskID, "to_lane", toLane,
		"status_events_path", eventsPath)
	if st.skipPrimary {
		res.Set("wp_file_update", "skipped")
		res.Set("wp_file_update_reason", "protected branch with coordination topology; status event is authoritative on the coordination branch")
		if st.agent != "" {
			res.Set("frontmatter_fields_skipped", []any{"agent"})
		}
	}
	if st.feedbackPointer != "" {
		res.Set("review_feedback", st.feedbackPointer)
	}
	if st.gateMeta != nil {
		res.Set("pre_review_gate", st.gateMeta)
	}
	if c := st.verdictWrite; c != nil {
		if !res.Has("review_feedback") {
			res.Set("review_feedback", c.Pointer)
		}
		o := c.Outcome
		res.Set("verdict_durably_persisted", o.Durable)
		res.Set("durability_classification", o.Classification)
		res.Set("durability_reason", ptrAny(o.Reason))
		res.Set("evidence_ref", ptrAny(o.EvidenceRef))
		res.Set("destination_ref", ptrAny(o.DestinationRef))
		if o.Reason != nil {
			res.Set("verdict_durability_skip_reason", *o.Reason)
		}
	}
	msg := fmt.Sprintf("✓ Moved %s from %s to %s", st.taskID, st.oldLane, st.target)
	if s := st.rollback; s != nil {
		res.Set("subtasks_reset_count", len(s.resetIDs))
		res.Set("subtasks_reset_ids", s.resetIDs)
		res.Set("subtasks_previously_completed", s.previouslyCompleted)
		res.Set("subtasks_never_completed", s.neverCompleted)
		res.Set("runtime_claim_released", true)
		res.Set("review_override_cleared", true)
		lines := []string{fmt.Sprintf("↺ Reset %d subtask(s) to planned — re-mark completed work before this WP re-enters review.", len(s.resetIDs))}
		if len(s.previouslyCompleted) > 0 {
			lines = append(lines, fmt.Sprintf("  • %d completed in an earlier cycle (%s): re-verify, don't rebuild from scratch.", len(s.previouslyCompleted), strings.Join(s.previouslyCompleted, ", ")))
		}
		if len(s.neverCompleted) > 0 {
			lines = append(lines, fmt.Sprintf("  • %d never completed (%s).", len(s.neverCompleted), strings.Join(s.neverCompleted, ", ")))
		}
		lines = append(lines, "  • runtime claim released; any review-override cleared (the WP exposes no live claim and no superseded approval).")
		msg += "\n" + strings.Join(lines, "\n")
	}
	if st.json {
		printJSON(st.ctx.Stdout, res)
	} else {
		consolePrint(st.ctx.Stdout, msg)
	}
	st.dependentWarnings()
}

func ptrAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func (st *moveState) dependentWarnings() {
	if st.target != string(status.ForReview) || st.json {
		return
	}
	graph := map[string][]string{}
	var order []string
	for _, p := range wp.Files(st.m.PrimaryDir()) {
		w, err := wp.Read(p)
		if err != nil {
			return
		}
		id := w.ID
		if fid := wp.IDFromFilename(p); w.Doc.Meta.GetString("work_package_id") != "" && w.Doc.Meta.GetString("work_package_id") != fid {
			return
		}
		graph[id] = w.Dependencies()
		order = append(order, id)
	}
	var dependents []string
	for _, id := range order {
		for _, d := range graph[id] {
			if d == st.taskID {
				dependents = append(dependents, id)
			}
		}
	}
	if len(dependents) == 0 {
		return
	}
	snap, _ := status.ReduceDir(st.m.ReadDir())
	var incomplete []string
	for _, d := range dependents {
		lane := string(status.Planned)
		if s := snap.WP(d); s != nil && s.GetString("lane") != "" {
			lane = s.GetString("lane")
		}
		switch status.ResolveLaneAlias(lane) {
		case string(status.Planned), string(status.InProgress), string(status.Claimed):
			incomplete = append(incomplete, d)
		}
	}
	if len(incomplete) == 0 {
		return
	}
	cur, err := lanes.ResolveWorkspace(st.root, st.m.Slug, st.taskID)
	if err != nil {
		return
	}
	out := st.ctx.Stdout
	consolePrint(out, "\n⚠️  Dependency Alert")
	consolePrint(out, fmt.Sprintf("%s depend on %s (not yet done)", strings.Join(incomplete, ", "), st.taskID))
	consolePrint(out, "\nIf changes are requested during review:")
	consolePrint(out, "  1. Notify dependent WP agents")
	consolePrint(out, "  2. Dependent workspaces may need to incorporate your changes")
	for _, d := range incomplete {
		dw, err := lanes.ResolveWorkspace(st.root, st.m.Slug, d)
		if err != nil {
			return
		}
		switch {
		case dw.BranchName == "":
			consolePrint(out, fmt.Sprintf("     %s: planning-lane workspace (main repo checkout) — no rebase needed; ensure main is up to date", d))
		case dw.BranchName == cur.BranchName:
			consolePrint(out, fmt.Sprintf("     %s: shares %s (same lane, no separate rebase command)", d, cur.BranchName))
		default:
			consolePrint(out, fmt.Sprintf("     cd %s && git rebase %s", dw.WorktreePath, cur.BranchName))
		}
	}
	consolePrint(out, "")
}

// resolveProfileVersion is workflow._resolved_profile_version: the local
// profile registry's schema version for --profile.
func resolveProfileVersion(root, id string) (string, error) {
	cat := doctrine.LoadProfileCatalog(root)
	p := cat.Resolve(id)
	if p == nil {
		ids := cat.IDs()
		sort.Strings(ids)
		var quoted []string
		for _, x := range ids {
			quoted = append(quoted, "'"+x+"'")
		}
		// workflow.ResolveBinding wraps this as "Could not resolve --profile …".
		return "", fmt.Errorf("Profile '%s' not found. Available: [%s]", id, strings.Join(quoted, ", "))
	}
	for _, k := range []string{"schema-version", "schema_version"} {
		if v, ok := p.Data.Get(k); ok && v != nil {
			return status.PyStr(v), nil
		}
	}
	return "1.0", nil
}
