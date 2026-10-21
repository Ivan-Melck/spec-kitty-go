package runtime

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
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
		o.Console("Error: --recover is not supported by this build.")
		if jsonMode {
			emitJSONError(summary())
		}
		return cli.Exit(1)
	}
	if ctx.IsSet("base") {
		o.Console("Error: --base is not supported by this build.")
		if jsonMode {
			emitJSONError(summary())
		}
		return cli.Exit(1)
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
		JSON: jsonMode, Actor: ctx.String("actor"), SkipPreamble: true})
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
