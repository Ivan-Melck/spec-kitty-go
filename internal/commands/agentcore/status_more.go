package agentcore

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func init() {
	addCommands(func(_, _, statusG *cli.Command) {
		statusG.Add(&cli.Command{
			Name:  "lifecycle",
			Short: "Show the canonical lifecycle state for one mission.",
			Long: "Show the canonical lifecycle state for one mission.\n\nThis is the product-facing state layer above raw WP lanes. It answers\n" +
				"whether a mission is active, recently completed, stale, abandoned, or\nnow just recoverable/archive history.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: runStatusLifecycle,
		})
		statusG.Add(&cli.Command{
			Name:  "migrate",
			Short: "[REMOVED] Frontmatter-to-event-log bootstrap migration has been removed.",
			Long: "[REMOVED] Frontmatter-to-event-log bootstrap migration has been removed.\n\nThe canonical status model uses the event log as sole authority.\n" +
				"One-shot bootstrap migration from frontmatter is handled by the\nupgrade migration system (``spec-kitty upgrade``), not this command.\n\n" +
				"Examples:\n    spec-kitty upgrade  # applies all pending migrations",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Single mission slug to migrate"},
				{Name: "all", Kind: cli.Bool, Help: "Migrate all features in kitty-specs/"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Preview migration without writing events"},
				{Name: "json", Kind: cli.Bool, Help: "Output results as JSON"},
				{Name: "actor", Kind: cli.String, Default: "migration", Metavar: "<str>", Help: "Actor name for bootstrap events"},
			},
			Run: removedCommand("The migrate command has been removed. Bootstrap migration from frontmatter is handled by the upgrade system. Run 'spec-kitty upgrade' to apply pending migrations."),
		})
		statusG.Add(&cli.Command{
			Name:  "validate",
			Short: "Validate canonical status model integrity.",
			Long: "Validate canonical status model integrity.\n\nRuns all validation checks: event schema, transition legality,\n" +
				"done-evidence completeness, materialization drift, and derived-view drift.\n\n" +
				"Exit code 0 for pass (no errors), exit code 1 for fail (any errors).\nWarnings do not cause failure.\n\n" +
				"Examples:\n    spec-kitty agent status validate\n    spec-kitty agent status validate --mission 034-my-feature\n    spec-kitty agent status validate --json",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: runStatusValidate,
		})
		statusG.Add(&cli.Command{
			Name:  "reconcile",
			Short: "[REMOVED] Cross-repo reconciliation has been removed.",
			Long: "[REMOVED] Cross-repo reconciliation has been removed.\n\nThe canonical status model uses the event log as sole authority.\n" +
				"Cross-repo drift detection via frontmatter scanning is no longer\nsupported. Use ``spec-kitty agent status validate`` to check\nevent log integrity.\n\n" +
				"Examples:\n    spec-kitty agent status validate --mission 034-feature-name",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "dry-run", Kind: cli.Bool, Negatable: true, NoName: "apply", Default: true, Help: "Preview vs persist reconciliation events"},
				{Name: "target-repo", Short: "t", Kind: cli.StringList, Metavar: "<path>", Help: "Target repo path(s) to scan"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: removedCommand("The reconcile command has been removed. The event log is now the sole authority for WP state. Use 'spec-kitty agent status validate' to check event log integrity."),
		})
	})
}

func removedCommand(msg string) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		jsonMode := ctx.Bool("json")
		if ctx.IsSet("mission") && strings.TrimSpace(ctx.String("mission")) == "" {
			statusOutputError(ctx, jsonMode, "--mission <slug> is required", nil)
			return cli.Exit(1)
		}
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("error", msg))
		} else {
			consolePrint(ctx.Stdout, "[red]Removed:[/red] "+msg)
		}
		return cli.Exit(1)
	}
}

// statusSurface is _resolve_status_surface: (read dir, slug, root).
func statusSurface(ctx *cli.Context, jsonMode bool) (*missionCtx, error) {
	root := project.Locate("")
	if root == "" {
		consolePrint(ctx.Stdout, "[red]Error:[/red] Could not locate project root")
		return nil, cli.Exit(1)
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return nil, err
	}
	return newMissionCtx(root, slug), nil
}

func runStatusLifecycle(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := statusSurface(ctx, jsonMode)
	if err != nil {
		return err
	}
	dir := m.ReadDir()
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		statusOutputError(ctx, jsonMode, err.Error(), nil)
		return cli.Exit(1)
	}
	snap := status.Reduce(stream.Transitions, nil, true)
	slugResolverWarn(ctx, jsonMode, dir, 1)
	lc := status.LifecycleObject(dir, snap, clock.Now())
	if jsonMode {
		printJSON(ctx.Stdout, lc)
		return nil
	}
	str := func(k string) string { v, _ := lc.Get(k); return status.PyStr(v) }
	surface := "off"
	if v, _ := lc.Get("surface_state"); v != nil {
		surface = status.PyStr(v)
	}
	age := "unknown"
	if v, _ := lc.Get("age_days"); v != nil {
		age = status.PyStr(v) + "d"
	}
	last := "unknown"
	if v, _ := lc.Get("last_activity_at"); v != nil {
		last = status.PyStr(v)
	}
	completion, _ := lc.Get("completion_pct")
	cf, _ := completion.(float64)
	logState := "missing"
	if lc.GetBool("has_event_log") {
		logState = "present"
	}
	rows := [][2]string{
		{"Mission", str("mission_slug")}, {"State", str("state")}, {"Default Surface", surface}, {"Reason", str("reason")},
		{"Completion", fmt.Sprintf("%.1f%%", cf)}, {"Work Packages", str("total_wps")}, {"Active WPs", str("active_wp_count")},
		{"Review WPs", str("review_wp_count")}, {"Blocked WPs", str("blocked_wp_count")}, {"Terminal WPs", str("terminal_wp_count")},
		{"Event Log", logState}, {"Age", age}, {"Last Activity", last},
	}
	t := rich.NewTable()
	t.ShowHeader, t.Box = false, nil
	t.AddColumn("Key", rich.Column{Style: "dim"})
	t.AddColumn("Value", rich.Column{})
	for _, r := range rows {
		t.AddRow(r[0], r[1])
	}
	fmt.Fprintln(ctx.Stdout)
	consolePrint(ctx.Stdout, "[bold]Mission Lifecycle: "+m.Slug+"[/bold]")
	rich.PrintTo(ctx.Stdout, t)
	if lines := status.FormatPostMissionEvents(lc.GetList("post_mission_events")); len(lines) > 0 {
		fmt.Fprintln(ctx.Stdout)
		consolePrint(ctx.Stdout, "[bold]Post-mission History[/bold]")
		for _, l := range lines {
			richPrint(ctx.Stdout, "  • "+l)
		}
	}
	return nil
}

func runStatusValidate(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		statusOutputError(ctx, jsonMode, "Could not locate project root", nil)
		return cli.Exit(1)
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	m := newMissionCtx(root, slug)
	dir := m.ReadDir()
	if _, err := os.Stat(dir); err != nil {
		statusOutputError(ctx, jsonMode, "Mission directory not found: "+dir, nil)
		return cli.Exit(1)
	}
	res, has := status.Validate(dir)
	if has {
		slugResolverWarn(ctx, jsonMode, dir, 2)
	}
	payload := func() *jsonx.Object {
		return jsonx.NewObject("mission_slug", slug, "passed", res.Passed(), "errors", res.Errors, "warnings", res.Warnings,
			"error_count", len(res.Errors), "warning_count", len(res.Warnings))
	}
	if !has {
		if jsonMode {
			printJSON(ctx.Stdout, payload())
		} else {
			consolePrint(ctx.Stdout, "[green]Status Validation: "+slug+"[/green]")
			consolePrint(ctx.Stdout, "No events to validate.")
			consolePrint(ctx.Stdout, "[green]Result: PASS[/green]")
		}
		return nil
	}
	if jsonMode {
		printJSON(ctx.Stdout, payload())
	} else {
		consolePrint(ctx.Stdout, "\n[bold]Status Validation: "+slug+"[/bold]")
		consolePrint(ctx.Stdout, strings.Repeat("-", 50))
		if len(res.Errors) > 0 {
			consolePrint(ctx.Stdout, fmt.Sprintf("[red]Errors: %d[/red]", len(res.Errors)))
			for _, e := range res.Errors {
				consolePrint(ctx.Stdout, "  - "+e)
			}
		}
		if len(res.Warnings) > 0 {
			consolePrint(ctx.Stdout, fmt.Sprintf("[yellow]Warnings: %d[/yellow]", len(res.Warnings)))
			for _, w := range res.Warnings {
				consolePrint(ctx.Stdout, "  - "+w)
			}
		}
		switch {
		case res.Passed() && len(res.Warnings) > 0:
			consolePrint(ctx.Stdout, fmt.Sprintf("\n[green]Result: PASS[/green] (%d warning(s))", len(res.Warnings)))
		case res.Passed():
			consolePrint(ctx.Stdout, "\n[green]Result: PASS[/green]")
		default:
			consolePrint(ctx.Stdout, "\n[red]Result: FAIL[/red]")
		}
	}
	if !res.Passed() {
		return cli.Exit(1)
	}
	return nil
}

// slugResolverWarn replays the store._SlugResolver logger warnings
// (orphaned / unsafe legacy event slugs) once per upstream read_events()
// pass of the command; logging is silent in --json mode.
func slugResolverWarn(ctx *cli.Context, jsonMode bool, dir string, passes int) {
	if jsonMode {
		return
	}
	ws := status.SlugResolverWarnings(dir)
	for i := 0; i < passes; i++ {
		for _, w := range ws {
			warnLog(ctx.Stderr, w)
		}
	}
}
