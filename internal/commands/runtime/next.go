package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/nextrt"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const nextLong = `Decide and emit the next agent action for the current mission.

Agents call this command repeatedly in a loop.  The system inspects the
mission state machine, evaluates guards, and returns a deterministic
decision with an action and prompt file.

Examples:
    spec-kitty next --mission 034-my-feature --json                            # query mode
    spec-kitty next --agent claude --mission 034-my-feature --result success --json
    spec-kitty next --agent codex --mission 034-my-feature
    spec-kitty next --agent gemini --mission 034-my-feature --result failed --json
    spec-kitty next --agent claude --mission 034-my-feature --answer "yes" --result success --json
    spec-kitty next --agent claude --mission 034-my-feature --answer "approve" --decision-id "input:review" --result success --json`

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name: "next", Short: "Decide and emit the next agent action for the current mission.", Long: nextLong,
			Flags: []*cli.Flag{
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name (required for advancing mode)"},
				{Name: "result", Kind: cli.String, Metavar: "<str>", Help: "Result of previous step: success|failed|blocked. If omitted, returns current state without advancing (query mode)."},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON decision only"},
				{Name: "answer", Kind: cli.String, Metavar: "<str>", Help: "Answer to a pending decision"},
				{Name: "decision-id", Kind: cli.String, Metavar: "<str>", Help: "Decision ID (required if multiple pending)"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."},
			},
			Run: runNext,
		})
	})
}

func printIndentJSON(ctx *cli.Context, o *jsonx.Object) {
	fmt.Fprintln(ctx.Stdout, jsonx.Dumps(o, jsonx.Pretty))
}

// resolveNextSlug is next_cmd._resolve_slug_or_exit.
func resolveNextSlug(ctx *cli.Context, root, raw string, jsonMode bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		all := mission.List(root)
		if len(all) == 1 {
			if all[0].Mid8 == "" {
				ctx.Eprintf("1 mission needs `spec-kitty migrate backfill-identity`.\n")
			}
			return all[0].Slug, nil
		}
		if len(all) == 0 {
			if jsonMode {
				printIndentJSON(ctx, jsonx.NewObject("result", "error", "error_code", "NO_MISSIONS_FOUND",
					"next_step", "Run `spec-kitty specify` to create a mission.", "spec_kitty_version", app.Version))
			} else {
				ctx.Eprintf("No missions found in this repository.\n  Next: run `spec-kitty specify` (or `/spec-kitty.specify`) to create one.\n")
			}
			return "", cli.Exit(1)
		}
		if jsonMode {
			var items []any
			for _, l := range all {
				var mid any
				if l.Mid8 != "" {
					mid = l.Mid8
				}
				items = append(items, jsonx.NewObject("mission_slug", l.Slug, "mid8", mid, "friendly_name", l.FriendlyName))
			}
			printIndentJSON(ctx, jsonx.NewObject("result", "error", "error_code", "MISSION_SELECTION_REQUIRED", "available_missions", items,
				"next_step", "Re-run with: spec-kitty next --mission <handle>", "spec_kitty_version", app.Version))
			return "", cli.Exit(1)
		}
		ctx.Eprintf("Multiple missions found; pass --mission <handle> to select one:\n")
		unmigrated := 0
		for i, l := range all {
			mid := l.Mid8
			if mid == "" {
				mid = "no mission_id"
				unmigrated++
			}
			if i < 10 {
				ctx.Eprintf("  %s (%s) — %s\n", l.Slug, mid, l.FriendlyName)
			}
		}
		if len(all) > 10 {
			ctx.Eprintf("  … and %d more\n", len(all)-10)
		}
		ctx.Eprintf("Re-run with: spec-kitty next --mission <handle>\n")
		if unmigrated > 0 {
			ctx.Eprintf("%d mission(s) need `spec-kitty migrate backfill-identity`.\n", unmigrated)
		}
		return "", cli.Exit(1)
	}
	if st, err := os.Stat(project.Specs(root, raw)); err == nil && st.IsDir() {
		return raw, nil
	}
	slug, err := mission.ResolveHandle(root, raw)
	if err == nil {
		return slug, nil
	}
	if _, ok := err.(*mission.NotFoundError); ok {
		return raw, nil
	}
	msg := err.Error()
	if jsonMode {
		printIndentJSON(ctx, jsonx.NewObject("result", "error", "error_code", "INTERNAL_RESOLUTION_ERROR", "error", msg, "spec_kitty_version", app.Version))
	} else {
		ctx.Eprintf("Error: %s\n", msg)
	}
	return "", cli.Exit(2)
}

func emitNotFound(ctx *cli.Context, e *nextrt.MissionNotFoundError, jsonMode bool) error {
	if jsonMode {
		printIndentJSON(ctx, jsonx.NewObject("result", "error", "error_code", "MISSION_NOT_FOUND", "handle", e.Handle,
			"next_step", e.NextStep(), "remediation", e.NextStep(), "spec_kitty_version", app.Version))
	} else {
		ctx.Eprintf("Error: Mission not found: '%s'\nNo mission matching '%s' exists in this repository.\n", e.Handle, e.Handle)
		ctx.Eprintf("  Next: %s\n", e.NextStep())
	}
	return cli.Exit(1)
}

func runNext(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	// Mint the ownership fact exactly once (explicit --owned-checkout or
	// flagless adoption; handle-less discovery inside the claimed checkout).
	owned, oerr := resolveNextOwned(ctx, root, jsonMode)
	if oerr != nil {
		return oerr
	}
	if root == "" {
		ctx.Eprintf("Error: Could not locate project root\n")
		return cli.Exit(1)
	}
	var slug string
	if owned != nil {
		// Every runtime surface (run state, kitty-ops, charter, prompt cache)
		// resolves from the owned checkout P.
		root, slug = owned.OwnedRoot, owned.MissionSlug
		nextrt.AnalysisGoverningRoot = owned.RepositoryRoot
	}
	if err := charterPreflightForNext(ctx, root, ctx.IsSet("result"), jsonMode); err != nil {
		return err
	}
	if owned == nil {
		var err error
		slug, err = resolveNextSlug(ctx, root, ctx.String("mission"), jsonMode)
		if err != nil {
			return err
		}
	}
	result := ctx.OptString("result")
	answer := ctx.OptString("answer")
	if result != nil && *result != "success" && *result != "failed" && *result != "blocked" {
		ctx.Eprintf("Error: --result must be one of ('success', 'failed', 'blocked'), got '%s'\n", *result)
		return cli.Exit(1)
	}
	if answer != nil && result == nil {
		printNextError(ctx, "Error: --answer requires --result because query mode is read-only", jsonMode)
		return cli.Exit(1)
	}
	agent := ctx.String("agent")
	var answeredID *string
	if answer != nil {
		if agent == "" {
			printNextError(ctx, "Error: --agent is required when --answer is provided", jsonMode)
			return cli.Exit(1)
		}
		id, err := nextrt.AnswerDecision(root, slug, ctx.String("decision-id"), *answer, agent)
		if err != nil {
			msg := "Error answering decision: " + err.Error()
			switch err.(type) {
			case nextrt.NoPendingDecisionError, nextrt.AmbiguousPendingDecisionError:
				msg = "Error: " + err.Error()
			}
			if jsonMode {
				fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("error", msg), compact))
			} else {
				ctx.Eprintf("%s\n", msg)
			}
			return cli.Exit(1)
		}
		answeredID = &id
	}
	if result == nil {
		d, err := nextrt.QueryCurrentState(root, slug, agent)
		if err != nil {
			switch e := err.(type) {
			case *nextrt.MissionNotFoundError:
				return emitNotFound(ctx, e, jsonMode)
			case *nextrt.QueryValidationError:
				if jsonMode {
					printIndentJSON(ctx, jsonx.NewObject("error", e.Msg, "error_code", "QUERY_MODE_VALIDATION_FAILED"))
				} else {
					ctx.Eprintf("Error: %s\n", e.Msg)
				}
				return cli.Exit(1)
			}
			return err
		}
		printDecisionOwned(ctx, d, jsonMode, answeredID, answer, owned)
		return nil
	}
	if agent == "" {
		ctx.Eprintf("Error: --agent is required when --result is provided\n")
		return cli.Exit(1)
	}
	if st, err := os.Stat(project.Specs(root, slug)); err != nil || !st.IsDir() {
		return emitNotFound(ctx, &nextrt.MissionNotFoundError{Handle: slug}, jsonMode)
	}
	nextrt.PairPreviousLifecycleRecord(root, slug, agent, *result)
	d := nextrt.DecideNext(root, slug, agent, *result)
	nextrt.EmitMissionNextInvoked(root, slug, agent, *result, d)
	nextrt.WriteIssuanceRecord(root, slug, agent, d)
	if owned != nil {
		if err := commitOwnedNextMutations(owned); err != nil {
			ctx.Eprintf("Error: %s\n", err.Error())
			return cli.Exit(1)
		}
	}
	printDecisionOwned(ctx, d, jsonMode, answeredID, answer, owned)
	if !jsonMode {
		printStalledInterventions(ctx, root, slug)
	}
	if d.Kind == nextrt.KindBlocked {
		return cli.Exit(1)
	}
	return nil
}

func printNextError(ctx *cli.Context, msg string, jsonMode bool) {
	if jsonMode {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("error", msg), compact))
	} else {
		ctx.Eprintf("%s\n", msg)
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// printDecision is next_cmd._print_decision.
func printDecision(ctx *cli.Context, d *nextrt.Decision, jsonMode bool, answeredID, answer *string) {
	if jsonMode {
		o := d.ToObject()
		if answeredID != nil {
			o.Set("answered", *answeredID)
			o.Set("answer", *answer)
		}
		printIndentJSON(ctx, o)
		return
	}
	if answeredID != nil {
		fmt.Fprintf(ctx.Stdout, "  Answered decision: %s\n", *answeredID)
	}
	p := func(f string, a ...any) { fmt.Fprintf(ctx.Stdout, f+"\n", a...) }
	progress := func() {
		if d.Progress == nil {
			return
		}
		total, _ := d.Progress.GetInt("total_wps")
		done, _ := d.Progress.GetInt("done_wps")
		if total > 0 {
			pct := 0
			if v, ok := d.Progress.Get("weighted_percentage"); ok {
				switch t := v.(type) {
				case jsonx.Float:
					pct = int(t)
				case float64:
					pct = int(t)
				case int:
					pct = t
				}
			}
			p("  Progress: %d%% (%d/%d done)", pct, done, total)
		}
	}
	if d.IsQuery {
		p("[QUERY — no result provided, state not advanced]")
		p("  Mission: %s @ %s", d.MissionSlug, d.MissionState)
		if d.Mission != "" {
			p("  Mission Type: %s", d.Mission)
		}
		if d.PreviewStep != nil {
			p("  Next step: %s", *d.PreviewStep)
		}
		if d.Question != nil {
			p("  Question: %s", *d.Question)
			if len(d.Options) > 0 {
				p("  Options: %s", strings.Join(d.Options, ", "))
			}
			if d.DecisionID != nil {
				p("  Decision ID: %s", *d.DecisionID)
			}
		} else if d.Reason != nil {
			p("  Reason: %s", *d.Reason)
		}
		progress()
		if d.RunID != nil {
			p("  Run ID: %s", *d.RunID)
		}
		return
	}
	p("[%s] %s @ %s", strings.ToUpper(d.Kind), d.MissionSlug, d.MissionState)
	if d.Mission != "" {
		p("  Mission Type: %s", d.Mission)
	}
	if d.Action != nil {
		if d.WPID != nil {
			p("  Action: %s %s", *d.Action, *d.WPID)
		} else {
			p("  Action: %s", *d.Action)
		}
	}
	if d.WorkspacePath != nil {
		p("  Workspace: %s", *d.WorkspacePath)
	}
	if len(d.GuardFailures) > 0 {
		p("  Guards pending: %s", strings.Join(d.GuardFailures, ", "))
		if d.GuardFailurePaths != nil {
			keys := d.GuardFailurePaths.Keys()
			sortStrings(keys)
			for _, k := range keys {
				p("    - %s: looked for %s", k, d.GuardFailurePaths.GetString(k))
			}
		}
	}
	if d.Reason != nil {
		p("  Reason: %s", *d.Reason)
	}
	if d.Question != nil {
		p("  Question: %s", *d.Question)
	}
	for i, o := range d.Options {
		p("    %d. %s", i+1, o)
	}
	if d.DecisionID != nil {
		p("  Decision ID: %s", *d.DecisionID)
	}
	progress()
	if d.RunID != nil {
		p("  Run ID: %s", *d.RunID)
	}
	if d.PromptFile != nil {
		p("")
		p("  Next step: read the prompt file:")
		p("    cat %s", deref(d.PromptFile))
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
