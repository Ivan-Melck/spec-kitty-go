package runtime

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
)

const topImplementLong = "Internal — allocate or reuse the lane worktree for a work package.\n\n" +
	"This command is internal infrastructure, used by ``spec-kitty agent action implement``\n" +
	"for workspace creation. It is not the canonical user-facing implementation path for\n" +
	"spec-kitty 3.1.1.\n\n" +
	"Canonical user workflow::\n\n" +
	"  spec-kitty next --agent <name> --mission <slug>   (loop entry)\n" +
	"  spec-kitty agent action implement <WP> --agent <name>  (per-WP verb)\n\n" +
	"This command remains available as a compatibility surface for direct callers.\n" +
	"See FR-503 and D-4 in the 3.1.1 spec."

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name: "implement", Short: "Internal — allocate or reuse the lane worktree for a work package.", Long: topImplementLong,
			Args: []*cli.Arg{{Name: "wp_id", Metavar: "{wp_id}", Required: true, ErrorHint: "wp_id", Help: "Work package ID (for example, WP01)"}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (for example, 001-my-feature)"},
				{Name: "auto-commit", Kind: cli.Bool, Negatable: true, Help: "Auto-commit status and planning changes (default: from project config)"},
				{Name: "json", Kind: cli.Bool, Help: "Output in JSON format"},
				{Name: "recover", Kind: cli.Bool, Help: "Recover from crashed implementation session"},
				{Name: "base", Kind: cli.String, Metavar: "<str>", Help: "Explicit base ref for the lane workspace (default: auto-detect). Use this when upstream dependency branches have been merged-and-deleted and you want to start from the current target branch tip, e.g. --base main."},
				{Name: "acknowledge-not-bulk-edit", Kind: cli.Bool, Help: "Suppress the bulk-edit inference warning when spec language resembles a bulk edit but the mission is not one."},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Hidden: true, Help: "Actor identity for programmatic callers"},
			},
			Run: runTopImplement,
		})
	})
}

func runTopImplement(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	wpID := ctx.Arg("wp_id")
	o := workflow.NewOut(ctx.Stdout, ctx.Stderr)
	if jsonMode {
		o.Capture()
	}
	emitJSONError := func(msg string) {
		obj := jsonx.NewObject("status", "error", "error", msg)
		if wpID != "" {
			obj.Set("wp_id", wpID)
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(obj, compact))
	}
	summary := func() string {
		var lines []string
		for _, l := range strings.Split(o.Captured(), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, strings.TrimRight(l, " \t"))
			}
		}
		if len(lines) == 0 {
			return "implement command failed"
		}
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	if !ctx.IsSet("mission") {
		o.Console("Error: --mission <slug> is required")
		if jsonMode {
			emitJSONError(summary())
		}
		return cli.Exit(2)
	}
	if ctx.Bool("recover") {
		return runRecover(ctx, jsonMode)
	}
	root := locateRoot()
	if root == "" {
		rich.Print(o.W, "Error: Could not locate project root")
		return cli.Exit(1)
	}
	o.Console("")
	workflow.EmitCharterPreflight(ctx.Stderr, root, "implement")
	slug, err := resolveMission(ctx.Stdout, ctx.Stderr, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	var ac *bool
	if ctx.IsSet("auto-commit") {
		v := ctx.Bool("auto-commit")
		ac = &v
	}
	res, err := workflow.TopImplement(o, workflow.ImplementOptions{Root: root, MissionDir: slug, WPID: wpID, AutoCommit: ac,
		JSON: jsonMode, Actor: ctx.String("actor"), SkipPreamble: true, Base: ctx.String("base")})
	if err != nil {
		if jsonMode {
			emitJSONError(summary())
		}
		return exitFrom(err)
	}
	if jsonMode {
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(workflow.ImplementJSON(root, slug, wpID, res.Workspace), compact))
		return nil
	}
	workflow.PrintWorkspaceReadyBanner(o, res.Workspace)
	return nil
}

// runRecover is implement_recover._run_recover_mode.
func runRecover(ctx *cli.Context, jsonMode bool) error {
	root := locateRoot()
	if root == "" {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "error", "error", "Could not locate project root"), compact))
		}
		return cli.Exit(1)
	}
	slug, err := mission.ResolveHandle(root, ctx.String("mission"))
	if err != nil {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "error", "error", err.Error()), compact))
		} else {
			rich.Print(ctx.Stdout, "[red]Error:[/red] "+err.Error())
		}
		return cli.Exit(1)
	}
	states := workflow.ScanRecoveryState(root, slug)
	var needs []*workflow.RecoveryState
	for _, s := range states {
		if s.RecoveryAction != "no_action" {
			needs = append(needs, s)
		}
	}
	if len(needs) == 0 {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "ok", "message", "No crashed implementation sessions found.",
				"recovered_wps", []any{}, "worktrees_recreated", 0, "transitions_emitted", 0, "errors", []any{}), compact))
		} else {
			rich.Print(ctx.Stdout, "No crashed implementation sessions found.")
		}
		return nil
	}
	if !jsonMode {
		yn := func(b bool) string {
			if b {
				return "yes"
			}
			return "NO"
		}
		var rows [][]string
		for _, s := range needs {
			rows = append(rows, []string{s.WPID, s.LaneID, s.BranchName, yn(s.WorktreeExists), yn(s.ContextExists), s.StatusLane, s.RecoveryAction})
		}
		rich.Table(ctx.Stdout, "Recovery Scan Results", []string{"WP", "Lane", "Branch", "Worktree", "Context", "Status", "Action"}, rows)
		fmt.Fprintln(ctx.Stdout)
	}
	rep := workflow.RunRecovery(root, slug)
	if jsonMode {
		wps, errs := []any{}, []any{}
		for _, w := range rep.RecoveredWPs {
			wps = append(wps, w)
		}
		for _, e := range rep.Errors {
			errs = append(errs, e)
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("status", "ok", "recovered_wps", wps, "worktrees_recreated", rep.WorktreesRecreated,
			"transitions_emitted", rep.TransitionsEmitted, "errors", errs), compact))
		return nil
	}
	none := strings.Join(rep.RecoveredWPs, ", ")
	if none == "" {
		none = "none"
	}
	fmt.Fprintln(ctx.Stdout, "Recovery complete")
	fmt.Fprintf(ctx.Stdout, "  WPs recovered: %s\n", none)
	fmt.Fprintf(ctx.Stdout, "  Worktrees recreated: %d\n", rep.WorktreesRecreated)
	fmt.Fprintf(ctx.Stdout, "  Contexts recreated: %d\n", rep.ContextsRecreated)
	fmt.Fprintf(ctx.Stdout, "  Status transitions emitted: %d\n", rep.TransitionsEmitted)
	if len(rep.Errors) > 0 {
		fmt.Fprintln(ctx.Stdout, "  Errors:")
		for _, e := range rep.Errors {
			fmt.Fprintf(ctx.Stdout, "    - %s\n", e)
		}
	}
	return nil
}
