package planning

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

var safeSlugRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func jsonFlag() *cli.Flag {
	return &cli.Flag{Name: "json", Kind: cli.Bool, Negatable: true, Default: true, Help: "Output JSON (default true)"}
}

func ownedFlag() *cli.Flag {
	return &cli.Flag{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp}
}

func init() {
	addCommands(func(_, agent, _, _ *cli.Command) {
		g := &cli.Command{Name: "decision", Short: "Decision Moment ledger for interview questions.", InvokeWithoutCommand: true, Run: helpExit2}
		agent.Add(g)
		decisionID := func(verb string) []*cli.Arg {
			return []*cli.Arg{{Name: "decision_id", Metavar: "{decision_id}", Required: true, Help: "ULID identifier of the decision to " + verb}}
		}
		g.Add(&cli.Command{
			Name:  "open",
			Short: "Open a new Decision Moment or return idempotently if one already exists.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (slug, mission_id, or mid8)"},
				{Name: "flow", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Origin flow: charter | specify | plan"},
				{Name: "input-key", Kind: cli.String, Metavar: "<str>", Required: true, Help: "The input key this decision governs"},
				{Name: "question", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Human-readable question text"},
				{Name: "step-id", Kind: cli.String, Metavar: "<str>", Help: "Interview step identifier"},
				{Name: "slot-key", Kind: cli.String, Metavar: "<str>", Help: "Slot key (use when step_id unavailable)"},
				{Name: "options", Kind: cli.String, Metavar: "<str>", Help: "Candidate answers as a JSON array string"},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Default: "cli", Help: "Identity of the opening actor"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Validate without writing"},
				jsonFlag(), ownedFlag(),
			},
			Run: runDecisionOpen,
		})
		g.Add(&cli.Command{
			Name:  "resolve",
			Short: "Resolve a decision with a concrete final answer.",
			Args:  decisionID("resolve"),
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle"},
				{Name: "final-answer", Kind: cli.String, Metavar: "<str>", Required: true, Help: "The chosen answer (non-empty)"},
				{Name: "other-answer", Kind: cli.Bool, Help: "True if answer is a write-in"},
				{Name: "rationale", Kind: cli.String, Metavar: "<str>", Help: "Explanation of the choice"},
				{Name: "resolved-by", Kind: cli.String, Metavar: "<str>", Help: "Identity of resolver"},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Default: "cli", Help: "Identity of the acting agent"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Validate without writing"},
				jsonFlag(), ownedFlag(),
			},
			Run: func(ctx *cli.Context) error { return runDecisionTerminal(ctx, decision.StatusResolved) },
		})
		for _, v := range []struct{ name, status, short, why, who string }{
			{"defer", decision.StatusDeferred, "Defer a decision for later resolution.", "deferred", "deferring"},
			{"cancel", decision.StatusCanceled, "Cancel a decision (deemed no longer relevant).", "canceled", "canceling"},
		} {
			v := v
			g.Add(&cli.Command{
				Name:  v.name,
				Short: v.short,
				Args:  decisionID(v.name),
				Flags: []*cli.Flag{
					{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle"},
					{Name: "rationale", Kind: cli.String, Metavar: "<str>", Required: true, Help: fmt.Sprintf("Explanation of why it's being %s (required)", v.why)},
					{Name: "resolved-by", Kind: cli.String, Metavar: "<str>", Help: fmt.Sprintf("Identity of %s party", v.who)},
					{Name: "actor", Kind: cli.String, Metavar: "<str>", Default: "cli", Help: "Identity of the acting agent"},
					{Name: "dry-run", Kind: cli.Bool, Help: "Validate without writing"},
					jsonFlag(), ownedFlag(),
				},
				Run: func(ctx *cli.Context) error { return runDecisionTerminal(ctx, v.status) },
			})
		}
		g.Add(&cli.Command{
			Name:  "verify",
			Short: "Cross-check deferred decisions against inline sentinel markers.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle"},
				{Name: "fail-on-stale", Kind: cli.Bool, Negatable: true, Default: true, Help: "Exit non-zero when findings are present (default true)"},
				jsonFlag(), ownedFlag(),
			},
			Run: runDecisionVerify,
		})
		g.Add(&cli.Command{
			Name:  "list",
			Short: "List the mission's recorded decision moments (read-only).",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (slug, mission_id, or mid8)"},
				{Name: "status", Kind: cli.String, Metavar: "<str>", Help: "Only list decisions in this status: open | resolved | deferred | canceled"},
				jsonFlag(), ownedFlag(),
			},
			Run: runDecisionList,
		})
		g.Add(&cli.Command{
			Name:  "repair-runtime-lock",
			Short: "Stop tracking the exact decision lock in an explicitly owned mission.",
			Long:  "Stop tracking the exact decision lock in an explicitly owned mission.\n\nAlso adds the exact ignore rule to the Mission's decisions/.gitignore when\nthe lock is untracked but not ignored.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission whose decision runtime lock needs repair"},
				ownedFlag(),
				{Name: "dry-run", Kind: cli.Bool, Help: "Validate and inspect without acquiring the lock or writing"},
			},
			Run: runRepairRuntimeLock,
		})
	})
}

// echoSorted is typer.echo(json.dumps(payload, sort_keys=True)).
func echoSorted(ctx *cli.Context, err bool, v *jsonx.Object) {
	w := ctx.Stdout
	if err {
		w = ctx.Stderr
	}
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Line))
}

func decisionErr(ctx *cli.Context, e *decision.Error) error {
	details := e.Details
	if details == nil {
		details = &jsonx.Object{}
	}
	echoSorted(ctx, true, jsonx.NewObject("error", e.Error(), "code", e.Code, "details", details))
	return cli.Exit(1)
}

// decisionContext is _resolve_decision_context.
func decisionContext(ctx *cli.Context) (*decision.Context, error) {
	handle := ctx.String("mission")
	if !safeSlugRe.MatchString(handle) {
		return nil, cli.BadParameter(ctx.Cmd, "--mission", fmt.Sprintf("Invalid --mission value %s", mission.PyRepr(handle)))
	}
	if ctx.String("owned-checkout") != "" {
		echoSorted(ctx, true, jsonx.NewObject("error", "--owned-checkout is not supported by this build", "code", "OWNED_OPTION_UNSUPPORTED"))
		return nil, cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		root = cwd()
	}
	slug := handle
	if name, err := mission.ResolveHandle(root, handle); err == nil {
		slug = name
	} else if amb, ok := err.(*mission.SelectorAmbiguousError); ok {
		echoSorted(ctx, true, jsonx.NewObject("error", amb.Error(), "code", amb.Code()))
		return nil, cli.Exit(1)
	} else {
		echoSorted(ctx, true, jsonx.NewObject("error", fmt.Sprintf("Mission directory not found: %s. Check that %s is the correct mission slug.",
			project.Specs(root, handle), mission.PyRepr(handle)), "code", "MISSION_CONTEXT_UNRESOLVED"))
		return nil, cli.Exit(1)
	}
	return &decision.Context{Root: root, Slug: slug}, nil
}

func handleServiceErr(ctx *cli.Context, err error) error {
	switch e := err.(type) {
	case *decision.Error:
		return decisionErr(ctx, e)
	case *decision.ReadError:
		echoSorted(ctx, true, jsonx.NewObject("code", "DECISION_INDEX_UNREADABLE", "error", e.Error(), "details", jsonx.NewObject("index_path", e.Path, "cause", e.Cause)))
		return cli.Exit(1)
	}
	echoSorted(ctx, true, jsonx.NewObject("error", err.Error(), "code", "STATUS_READ_PATH_NOT_FOUND"))
	return cli.Exit(1)
}

func optFlag(ctx *cli.Context, name string) *string {
	if !ctx.IsSet(name) {
		return nil
	}
	v := ctx.String(name)
	return &v
}

func lamport(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func runDecisionOpen(ctx *cli.Context) error {
	flow := ctx.String("flow")
	if !contains(decision.Flows, flow) {
		valid := strings.Join(decision.Flows, ", ")
		return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, fmt.Sprintf("Invalid --flow value %s. Must be one of: %s", mission.PyRepr(flow), valid),
			"flow", flow, "valid_values", valid))
	}
	var options []string
	if ctx.IsSet("options") {
		raw := ctx.String("options")
		v, err := pyjson.Loads(raw)
		if err != nil {
			return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, fmt.Sprintf("--options must be a valid JSON array string, got: %s", mission.PyRepr(raw)),
				"options", raw, "parse_error", err.Error()))
		}
		list, ok := v.([]any)
		if !ok {
			return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, "--options must be a JSON array (list), got a non-list value", "options", raw))
		}
		for _, item := range list {
			options = append(options, pyjson.Str(item))
		}
	}
	dc, err := decisionContext(ctx)
	if err != nil {
		return err
	}
	stepID, slotKey := optFlag(ctx, "step-id"), optFlag(ctx, "slot-key")
	resp, err := dc.Open(decision.OpenRequest{Flow: flow, InputKey: ctx.String("input-key"), Question: ctx.String("question"), Options: options,
		StepID: stepID, SlotKey: slotKey, Actor: ctx.String("actor"), DryRun: ctx.Bool("dry-run")})
	if err != nil {
		return handleServiceErr(ctx, err)
	}
	payload := jsonx.NewObject("contract", "decision_open_v2", "decision_id", resp.DecisionID, "idempotent", resp.Idempotent,
		"mission_id", resp.MissionID, "artifact_path", resp.ArtifactPath, "event_lamport", lamport(resp.Lamport),
		"recovery", jsonx.NewObject("rerun_safe", resp.DecisionID != "DRY_RUN", "idempotency_key", jsonx.NewObject(
			"mission_id", resp.MissionID, "mission_slug", dc.Slug, "origin_flow", flow, "step_id", strPtrAny(stepID), "slot_key", strPtrAny(slotKey),
			"input_key", ctx.String("input-key"))))
	echoSorted(ctx, false, payload)
	return nil
}

func strPtrAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func runDecisionTerminal(ctx *cli.Context, status string) error {
	req := decision.TerminalRequest{DecisionID: ctx.Arg("decision_id"), Status: status, ResolvedBy: optFlag(ctx, "resolved-by"),
		Actor: ctx.String("actor"), DryRun: ctx.Bool("dry-run")}
	if status == decision.StatusResolved {
		fa := ctx.String("final-answer")
		req.FinalAnswer = &fa
		req.OtherAnswer = ctx.Bool("other-answer")
		req.Rationale = optFlag(ctx, "rationale")
	} else {
		r := ctx.String("rationale")
		if strings.TrimSpace(r) == "" {
			return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, "--rationale must be a non-empty string", "field", "rationale"))
		}
		req.Rationale = &r
	}
	dc, err := decisionContext(ctx)
	if err != nil {
		return err
	}
	if status == decision.StatusResolved && strings.TrimSpace(*req.FinalAnswer) == "" {
		return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, "final_answer must be a non-empty string", "field", "final_answer"))
	}
	resp, err := dc.Terminal(req)
	if err != nil {
		return handleServiceErr(ctx, err)
	}
	echoSorted(ctx, false, jsonx.NewObject("decision_id", resp.DecisionID, "status", resp.Status, "terminal_outcome", resp.Outcome,
		"idempotent", resp.Idempotent, "event_lamport", lamport(resp.Lamport)))
	return nil
}

func runDecisionVerify(ctx *cli.Context) error {
	dc, err := decisionContext(ctx)
	if err != nil {
		return err
	}
	res, err := decision.Verify(dc.LedgerDir())
	if err != nil {
		return handleServiceErr(ctx, err)
	}
	findings := []any{}
	for _, f := range res.Findings {
		findings = append(findings, jsonx.NewObject("kind", f.Kind, "decision_id_or_ref", f.Ref, "location", strPtrAny(f.Location), "detail", f.Detail))
	}
	st := "clean"
	if len(findings) > 0 {
		st = "drift"
	}
	// FR-010a: a decision-event stream forked between the PRIMARY and
	// coordination surfaces is its own finding and always exits 1.
	fork := healthcheck.DetectDecisionForks(dc.Root, dc.Slug)
	if fork.Forked {
		set := map[string]bool{}
		for _, s := range fork.Streams {
			if s.State == "forked" {
				for _, id := range append(append([]string{}, s.OnlyPrimary...), s.OnlyCoord...) {
					set[id] = true
				}
			}
		}
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var ref any
		if len(ids) > 0 {
			ref = strings.Join(ids, ", ")
		}
		findings = append(findings, jsonx.NewObject("kind", "DECISION_LOG_FORKED", "decision_id_or_ref", ref, "location", nil,
			"detail", "A decision-event stream diverges between the PRIMARY and coordination surfaces (never auto-merged, C-003): "+strings.Join(fork.ReconcileSteps, "; ")))
		st = "drift"
	}
	echoSorted(ctx, false, jsonx.NewObject("status", st, "deferred_count", res.DeferredCount, "marker_count", res.MarkerCount, "findings", findings))
	if fork.Forked {
		return cli.Exit(1)
	}
	if len(res.Findings) > 0 && ctx.Bool("fail-on-stale") {
		return cli.Exit(1)
	}
	return nil
}

func runDecisionList(ctx *cli.Context) error {
	filter := ""
	if ctx.IsSet("status") {
		filter = ctx.String("status")
		if !contains(decision.AllStatuses, filter) {
			valid := strings.Join(decision.AllStatuses, ", ")
			return decisionErr(ctx, decision.NewError(decision.CodeMissingStepOrSlot, fmt.Sprintf("Invalid --status value %s. Must be one of: %s", mission.PyRepr(filter), valid),
				"status", filter, "valid_values", valid))
		}
	}
	dc, err := decisionContext(ctx)
	if err != nil {
		return err
	}
	idx, err := decision.Load(dc.LedgerDir())
	if err != nil {
		return handleServiceErr(ctx, err)
	}
	entries := append([]*decision.Entry(nil), idx.Entries...)
	sortDecisionEntries(entries)
	list := []any{}
	for _, e := range entries {
		if filter != "" && e.Status != filter {
			continue
		}
		var resolved any
		if e.ResolvedAt != nil {
			resolved = decision.ISO(*e.ResolvedAt)
		}
		list = append(list, jsonx.NewObject("decision_id", e.DecisionID, "status", e.Status, "origin_flow", e.OriginFlow, "input_key", e.InputKey,
			"question", e.Question, "step_id", strPtrAny(e.StepID), "slot_key", strPtrAny(e.SlotKey), "final_answer", strPtrAny(e.FinalAnswer),
			"rationale", strPtrAny(e.Rationale), "created_at", decision.ISO(e.CreatedAt), "resolved_at", resolved, "resolved_by", strPtrAny(e.ResolvedBy)))
	}
	var mid any
	if idx.MissionID != "" {
		mid = idx.MissionID
	}
	echoSorted(ctx, false, jsonx.NewObject("contract", "decision_list_v1", "mission_slug", dc.Slug, "mission_id", mid, "count", len(list), "decisions", list))
	return nil
}

func sortDecisionEntries(es []*decision.Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0; j-- {
			a, b := es[j-1], es[j]
			if a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.DecisionID > b.DecisionID) {
				es[j-1], es[j] = b, a
			} else {
				break
			}
		}
	}
}

func runRepairRuntimeLock(ctx *cli.Context) error {
	if ctx.String("owned-checkout") == "" {
		return cli.Usage(ctx.Cmd, "Invalid value: repair-runtime-lock requires explicit --owned-checkout")
	}
	fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "error", "code", "OWNED_OPTION_UNSUPPORTED", "message", "--owned-checkout is not supported by this build"), compact))
	return cli.Exit(1)
}
