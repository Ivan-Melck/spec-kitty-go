package agentcore

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const statusEmitLong = "Emit a status transition event for a work package.\n\n" +
	"Records a lane transition in the canonical event log, validates the\ntransition against the state machine, materializes a snapshot, and\nupdates legacy compatibility views.\n\n" +
	"Examples:\n    spec-kitty agent status emit WP01 --to claimed --actor claude\n" +
	"    spec-kitty agent status emit --wp WP01 --to claimed --actor claude\n" +
	"    spec-kitty agent status emit WP01 --to approved --actor claude --review-result-json '{\"reviewer\": \"alice\", \"verdict\": \"approved\", \"reference\": \"PR#1\"}'\n" +
	"    spec-kitty agent status emit WP01 --to in_progress --actor claude --force --reason \"resuming after crash\""

func init() {
	addCommands(func(_, _, statusG *cli.Command) {
		statusG.Add(&cli.Command{
			Name:  "emit",
			Short: "Emit a status transition event for a work package.",
			Long:  statusEmitLong,
			Args:  []*cli.Arg{{Name: "wp_id", Metavar: "wp_id", Help: "Work package ID (e.g., WP01)"}},
			Flags: []*cli.Flag{
				{Name: "to", Kind: cli.String, Required: true, Metavar: "<str>", Help: "Target lane (e.g., claimed, in_progress, for_review, approved, done)"},
				{Name: "actor", Kind: cli.String, Required: true, Metavar: "<str>", Help: "Who is making this transition"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "wp", Kind: cli.String, Metavar: "<str>", Help: "Work package ID (alternative to positional ID)"},
				{Name: "force", Kind: cli.Bool, Help: "Force transition bypassing guards"},
				{Name: "reason", Kind: cli.String, Metavar: "<str>", Help: "Reason for forced transition (required with --force out of in_review or approved)"},
				{Name: "evidence-json", Kind: cli.String, Metavar: "<str>", Help: "JSON string with done evidence"},
				{Name: "review-ref", Kind: cli.String, Metavar: "<str>", Help: "Review feedback reference"},
				{Name: "review-result-json", Kind: cli.String, Metavar: "<str>", Help: "JSON structured review outcome for transitions from in_review"},
				{Name: "workspace-context", Kind: cli.String, Metavar: "<str>", Help: "Workspace context identifier for claimed->in_progress"},
				{Name: "subtasks-complete", Kind: cli.Bool, Help: "Whether required subtasks are complete for in_progress->for_review"},
				{Name: "implementation-evidence-present", Kind: cli.Bool, Help: "Whether implementation evidence exists for in_progress->for_review"},
				{Name: "execution-mode", Kind: cli.String, Default: "worktree", Metavar: "<str>", Help: "Execution mode (worktree or direct_repo) [default: worktree]"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: runStatusEmit,
		})
	})
}

var wpIDRe = regexp.MustCompile(`^WP\d+$`)

// NormalizeWPID is status.models.normalize_wp_id.
func NormalizeWPID(v string) (string, error) {
	n := strings.ToUpper(strings.TrimSpace(v))
	if !wpIDRe.MatchString(n) {
		return "", fmt.Errorf("Invalid work package ID '%s': expected WP<digits> (e.g. WP01)", v)
	}
	return n, nil
}

func resolveWPArg(ctx *cli.Context) (string, error) {
	pos, hasPos := ctx.Arg("wp_id"), ctx.HasArg("wp_id")
	flag, hasFlag := ctx.String("wp"), ctx.IsSet("wp")
	if hasPos && hasFlag {
		a, e1 := NormalizeWPID(pos)
		b, e2 := NormalizeWPID(flag)
		if e1 != nil || e2 != nil || a != b {
			if e1 == nil && e2 == nil {
				return "", fmt.Errorf("Positional WP ID '%s' and --wp '%s' must match when both are supplied", pos, flag)
			}
			if e1 != nil {
				return "", e1
			}
			return "", e2
		}
	}
	chosen := flag
	if hasPos {
		chosen = pos
	} else if !hasFlag {
		return "", fmt.Errorf("A work package ID is required (positional or --wp)")
	}
	return NormalizeWPID(chosen)
}

// parseReviewResultJSON is status.review_result_parse.parse_review_result_json.
func parseReviewResultJSON(raw string) (*status.ReviewResult, error) {
	v, err := pyjson.Loads(raw)
	if err != nil {
		return nil, fmt.Errorf("Invalid JSON in --review-result-json: %v", err)
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("--review-result-json must decode to a JSON object")
	}
	get := func(k string) (string, bool) {
		x, _ := o.Get(k)
		s, ok := x.(string)
		return s, ok && strings.TrimSpace(s) != ""
	}
	r, ok1 := get("reviewer")
	vd, ok2 := get("verdict")
	ref, ok3 := get("reference")
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("--review-result-json requires non-empty reviewer, verdict, and reference strings")
	}
	if vd != "approved" && vd != "changes_requested" {
		return nil, fmt.Errorf("--review-result-json verdict must be 'approved' or 'changes_requested'")
	}
	rr := &status.ReviewResult{Reviewer: r, Verdict: vd, Reference: ref}
	if fp, ok := o.Get("feedback_path"); ok && fp != nil {
		s, isStr := fp.(string)
		if !isStr {
			return nil, fmt.Errorf("--review-result-json feedback_path must be a string")
		}
		rr.FeedbackPath = &s
	}
	return rr, nil
}

func buildDoneEvidence(v any) (*status.DoneEvidence, error) {
	bad := fmt.Errorf("Moving to done requires evidence with review.reviewer review.verdict, and review.reference")
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, bad
	}
	rv := o.GetObject("review")
	if rv == nil {
		return nil, bad
	}
	str := func(x *jsonx.Object, k string) string {
		y, _ := x.Get(k)
		if y == nil {
			return ""
		}
		return status.PyStr(y)
	}
	d := &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: str(rv, "reviewer"), Verdict: str(rv, "verdict"), Reference: str(rv, "reference")}}
	if d.Review.Reviewer == "" || d.Review.Verdict == "" || strings.TrimSpace(d.Review.Reference) == "" {
		return nil, bad
	}
	for _, x := range o.GetList("repos") {
		if ro, ok := x.(*jsonx.Object); ok {
			r := status.RepoEvidence{Repo: str(ro, "repo"), Branch: str(ro, "branch"), Commit: str(ro, "commit")}
			for _, f := range ro.GetList("files_touched") {
				r.FilesTouched = append(r.FilesTouched, status.PyStr(f))
			}
			d.Repos = append(d.Repos, r)
		}
	}
	for _, x := range o.GetList("verification") {
		if vo, ok := x.(*jsonx.Object); ok {
			d.Verification = append(d.Verification, status.VerificationResult{Command: str(vo, "command"), Result: str(vo, "result"), Summary: str(vo, "summary")})
		}
	}
	return d, nil
}

// statusOutputError is agent status's _output_error (json.dumps default).
func statusOutputError(ctx *cli.Context, jsonMode bool, msg string, diag *jsonx.Object) {
	if jsonMode {
		if diag == nil {
			diag = jsonx.NewObject("error", msg)
		}
		printJSON(ctx.Stdout, diag)
		return
	}
	consolePrint(ctx.Stdout, "Error: "+msg)
}

func runStatusEmit(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string, diag *jsonx.Object) error {
		statusOutputError(ctx, jsonMode, msg, diag)
		return cli.Exit(1)
	}
	wpID, err := resolveWPArg(ctx)
	if err != nil {
		return fail(err.Error(), nil)
	}
	root := project.Locate("")
	if root == "" {
		return fail("Could not locate project root", nil)
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	m := newMissionCtx(root, slug)
	var evidence *status.DoneEvidence
	var evidenceRaw any
	if ctx.IsSet("evidence-json") {
		v, err := pyjson.Loads(ctx.String("evidence-json"))
		if err != nil {
			return fail(fmt.Sprintf("Invalid JSON in --evidence-json: %v\nExpected valid JSON object, e.g.: '%s'", err,
				`{"review": {"reviewer": "alice", "verdict": "approved", "reference": "PR#1"}}`), nil)
		}
		evidenceRaw = v
	}
	var rr *status.ReviewResult
	if ctx.IsSet("review-result-json") {
		rr, err = parseReviewResultJSON(ctx.String("review-result-json"))
		if err != nil {
			return fail(err.Error(), nil)
		}
	}
	to := ctx.String("to")
	force := ctx.Bool("force")
	if status.ResolveLaneAlias(to) == string(status.ForReview) {
		if d := lanes.EvaluateForReviewGate(root, slug, wpID, force); !d.Passed {
			var lane any
			if d.LaneID != "" {
				lane = d.LaneID
			}
			return fail(d.Reason, jsonx.NewObject("error", d.Reason, "wp_id", wpID, "lane_id", lane))
		}
	}
	if evidenceRaw != nil {
		evidence, err = buildDoneEvidence(evidenceRaw)
		if err != nil {
			return fail(err.Error(), nil)
		}
	}
	req := &status.TransitionRequest{WPID: wpID, ToLane: to, Actor: status.StrActor(ctx.String("actor")), Force: force,
		Reason: ctx.OptString("reason"), Evidence: evidence, ReviewRef: ctx.OptString("review-ref"), ReviewResult: rr,
		WorkspaceContext: ctx.OptString("workspace-context"), ExecutionMode: ctx.String("execution-mode"), RepoRoot: root}
	if ctx.IsSet("subtasks-complete") {
		req.SubtasksComplete = status.Bool(ctx.Bool("subtasks-complete"))
	}
	if ctx.IsSet("implementation-evidence-present") {
		req.ImplementationEvidence = status.Bool(ctx.Bool("implementation-evidence-present"))
	}
	res, err := m.Surface.EmitTransition(req)
	if err != nil {
		return fail(err.Error(), nil)
	}
	ev := res.Event
	if ev.To == status.ForReview {
		lanes.RecordTipForWP(root, slug, wpID)
	}
	m = newMissionCtx(root, slug)
	if ev.To == status.Approved || (ev.To == status.Done && !ev.Force && ev.From != status.Approved) {
		if !ev.Force && (ev.PolicyMetadata == nil || !ev.PolicyMetadata.Has("lane_head")) {
			if man, _ := lanes.Read(m.PrimaryDir()); man != nil {
				if l := man.LaneForWP(wpID); l != nil && l.LaneID != lanes.PlanningLaneID {
					fmt.Fprintf(ctx.Stderr, "Warning: no lane head could be recorded for %s's approval; `spec-kitty consolidate` will refuse it (APPROVAL_STAMP_MISSING) until it is approved again or attested.\n", wpID)
				}
			}
		}
	}
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("event_id", ev.EventID, "wp_id", ev.WPID, "work_package_id", ev.WPID,
			"from_lane", string(ev.From), "to_lane", string(ev.To), "status_events_path", status.EventsPath(m.ReadDir()),
			"actor", ev.Actor.Value()))
		return nil
	}
	short := ev.EventID
	if len(short) > 12 {
		short = short[:12]
	}
	consolePrint(ctx.Stdout, fmt.Sprintf("OK %s: %s -> %s (event: %s...)", ev.WPID, ev.From, ev.To, short))
	return nil
}
