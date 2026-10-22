package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

const (
	helpDecisionID      = "Decision ledger entry ID (ULID)"
	helpRationaleReq    = "Explanation of why (required)"
	helpResolvedBy      = "Identity of the resolving/deferring/canceling party (falls back to --actor)"
	helpOwnedCheckout   = "Path to a linked owned checkout that owns this mission (single_branch only); omit for the repository-root checkout"
	decisionFallback    = "DECISION_OPERATION_FAILED"
	decisionIndexFailed = "DESIGN_STATUS_EVENT_LOG_UNREADABLE"
)

func ownedFlag() *cli.Flag { return str("owned-checkout", helpOwnedCheckout, false) }

// decisionContext is _resolve_owned_decision_context (non-owned arm).
func (v *verb) decisionContext(ctx *cli.Context) (*orch.Mission, *decision.Context, error) {
	root := v.root()
	if claim := ctx.String("owned-checkout"); claim != "" {
		if _, err := os.Stat(claim); err != nil {
			return nil, nil, v.fail(decisionFallback, fmt.Sprintf("Cannot validate claimed checkout %s against resolved primary %s: git topology probe failed for PosixPath(%s): git binary not found on PATH",
				claim, root, pyjson.ReprString(claim)), jsonx.NewObject("unregistered_error_code", "OWNERSHIP_BROKEN_POINTER"))
		}
		return nil, nil, v.fail(decisionFallback, "--owned-checkout is not supported by this build",
			jsonx.NewObject("unregistered_error_code", "OWNED_OPTION_UNSUPPORTED"))
	}
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return nil, nil, err
	}
	return m, &decision.Context{Root: root, Slug: m.Slug}, nil
}

// decisionFailure maps a decision service error onto the envelope.
func (v *verb) decisionFailure(handle string, err error) error {
	var de *decision.Error
	if errors.As(err, &de) {
		details := de.Details
		if details == nil {
			details = jsonx.NewObject()
		}
		if orch.IsAllowedErrorCode(de.Code) {
			return v.fail(de.Code, de.Error(), details)
		}
		d := details.Clone()
		d.Set("unregistered_error_code", de.Code)
		return v.fail(decisionFallback, de.Error(), d)
	}
	var re *decision.ReadError
	if errors.As(err, &re) {
		return v.fail(decisionIndexFailed, fmt.Sprintf("decisions/index.json could not be read cleanly for mission %s: %s", pyjson.ReprString(handle), re.Error()),
			jsonx.NewObject("mission_slug", handle, "error", re.Error()))
	}
	return v.fail(decisionIndexFailed, fmt.Sprintf("status.events.jsonl could not be read cleanly for mission %s: %s", pyjson.ReprString(handle), err.Error()),
		jsonx.NewObject("mission_slug", handle, "error", err.Error()))
}

func lamportAny(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// ── open-decision ─────────────────────────────────────────────────────────

func openDecisionCmd() *cli.Command {
	return &cli.Command{
		Name:  "open-decision",
		Short: "Open a new Decision Moment ledger entry, or return idempotently if one\nalready exists (FR-006). Wraps ``decisions/service.py.open_decision`` 1:1.",
		Flags: []*cli.Flag{
			missionFlag(),
			str("origin", "Origin flow: charter | specify | plan", true),
			str("input-key", "The input key this decision governs", true),
			str("question", "Human-readable question text", true),
			str("step-id", "Interview step identifier", false),
			str("slot-key", "Slot key (use when step_id unavailable)", false),
			str("options", "Candidate answers as a JSON array string", false),
			str("actor", helpActor, true),
			str("policy", helpPolicy, false),
			ownedFlag(),
		},
		Run: runOpenDecision,
	}
}

func runOpenDecision(ctx *cli.Context) error {
	v := newVerb(ctx, "open-decision")
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for open-decision"); err != nil {
		return err
	}
	origin := ctx.String("origin")
	valid := false
	for _, f := range decision.Flows {
		if f == origin {
			valid = true
		}
	}
	if !valid {
		vals := strings.Join(decision.Flows, ", ")
		return v.fail("INVALID_ORIGIN_FLOW", fmt.Sprintf("Invalid --origin value %s. Must be one of: %s", pyjson.ReprString(origin), vals),
			jsonx.NewObject("origin", origin, "valid_values", vals))
	}
	var options []string
	if raw := optString(ctx, "options"); raw != nil {
		parsed, err := pyjson.Loads(*raw)
		if err != nil {
			return v.fail("USAGE_ERROR", fmt.Sprintf("--options must be a valid JSON array string, got: %s", pyjson.ReprString(*raw)),
				jsonx.NewObject("options", *raw, "parse_error", err.Error()))
		}
		list, ok := parsed.([]any)
		if !ok {
			return v.fail("USAGE_ERROR", "--options must be a JSON array (list), got a non-list value", jsonx.NewObject("options", *raw))
		}
		for _, x := range list {
			options = append(options, pyStr(x))
		}
	}
	m, dc, err := v.decisionContext(ctx)
	if err != nil {
		return err
	}
	resp, err := dc.Open(decision.OpenRequest{Flow: origin, InputKey: ctx.String("input-key"), Question: ctx.String("question"),
		Options: options, StepID: optString(ctx, "step-id"), SlotKey: optString(ctx, "slot-key"), Actor: ctx.String("actor")})
	if err != nil {
		return v.decisionFailure(ctx.String("mission"), err)
	}
	return v.ok(identity(m.StatusDir, "decision_id", resp.DecisionID, "status", "open", "idempotent", resp.Idempotent,
		"artifact_path", resp.ArtifactPath, "event_lamport", lamportAny(resp.Lamport)))
}

// ── resolve / defer / cancel ──────────────────────────────────────────────

func resolveDecisionCmd() *cli.Command {
	return &cli.Command{
		Name:  "resolve-decision",
		Short: "Resolve a decision with a concrete final answer (FR-007). Wraps\n``decisions/service.py.resolve_decision`` 1:1.",
		Flags: []*cli.Flag{
			missionFlag(),
			str("decision-id", helpDecisionID, true),
			str("final-answer", "The chosen answer (non-empty)", true),
			boolFlag("other-answer", "True if answer is a write-in"),
			str("rationale", "Explanation of the choice", false),
			str("resolved-by", helpResolvedBy, false),
			str("actor", helpActor, true),
			str("policy", helpPolicy, false),
			ownedFlag(),
		},
		Run: func(ctx *cli.Context) error {
			return runTerminalDecision(ctx, "resolve-decision", decision.StatusResolved)
		},
	}
}

func deferDecisionCmd() *cli.Command {
	return &cli.Command{
		Name:  "defer-decision",
		Short: "Defer a decision for later resolution (FR-008). Wraps\n``decisions/service.py.defer_decision`` 1:1.",
		Flags: []*cli.Flag{
			missionFlag(),
			str("decision-id", helpDecisionID, true),
			str("rationale", helpRationaleReq, true),
			str("resolved-by", helpResolvedBy, false),
			str("actor", helpActor, true),
			str("policy", helpPolicy, false),
			ownedFlag(),
		},
		Run: func(ctx *cli.Context) error {
			return runTerminalDecision(ctx, "defer-decision", decision.StatusDeferred)
		},
	}
}

func cancelDecisionCmd() *cli.Command {
	return &cli.Command{
		Name:  "cancel-decision",
		Short: "Cancel a decision (deemed no longer relevant) (FR-009). Wraps\n``decisions/service.py.cancel_decision`` 1:1.",
		Flags: []*cli.Flag{
			missionFlag(),
			str("decision-id", helpDecisionID, true),
			str("rationale", helpRationaleReq, true),
			str("resolved-by", helpResolvedBy, false),
			str("actor", helpActor, true),
			str("policy", helpPolicy, false),
			ownedFlag(),
		},
		Run: func(ctx *cli.Context) error {
			return runTerminalDecision(ctx, "cancel-decision", decision.StatusCanceled)
		},
	}
}

func runTerminalDecision(ctx *cli.Context, name, target string) error {
	v := newVerb(ctx, name)
	if _, err := v.requirePolicy(ctx.String("policy"), "--policy is required for "+name); err != nil {
		return err
	}
	if target != decision.StatusResolved && strings.TrimSpace(ctx.String("rationale")) == "" {
		return v.fail(decision.CodeMissingStepOrSlot, "--rationale must be a non-empty string", jsonx.NewObject("field", "rationale"))
	}
	m, dc, err := v.decisionContext(ctx)
	if err != nil {
		return err
	}
	req := decision.TerminalRequest{DecisionID: ctx.String("decision-id"), Status: target, Rationale: optString(ctx, "rationale"),
		ResolvedBy: optString(ctx, "resolved-by"), Actor: ctx.String("actor")}
	if target == decision.StatusResolved {
		fa := ctx.String("final-answer")
		req.FinalAnswer = &fa
		req.OtherAnswer = ctx.Bool("other-answer")
	}
	resp, err := dc.Terminal(req)
	if err != nil {
		return v.decisionFailure(ctx.String("mission"), err)
	}
	return v.ok(identity(m.StatusDir, "decision_id", resp.DecisionID, "status", resp.Status, "terminal_outcome", resp.Outcome,
		"idempotent", resp.Idempotent, "event_lamport", lamportAny(resp.Lamport)))
}
