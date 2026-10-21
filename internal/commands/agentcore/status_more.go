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
			consolePrint(ctx.Stdout, "Removed: "+msg)
		}
		return cli.Exit(1)
	}
}

// statusSurface is _resolve_status_surface: (read dir, slug, root).
func statusSurface(ctx *cli.Context, jsonMode bool) (*missionCtx, error) {
	root := project.Locate("")
	if root == "" {
		consolePrint(ctx.Stdout, "Error: Could not locate project root")
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
	w0, w1 := 0, 0
	for _, r := range rows {
		w0, w1 = max(w0, rich.CellLen(r[0])), max(w1, rich.CellLen(r[1]))
	}
	fmt.Fprintln(ctx.Stdout)
	consolePrint(ctx.Stdout, "Mission Lifecycle: "+m.Slug)
	for _, r := range rows {
		fmt.Fprintln(ctx.Stdout, " "+padRight(r[0], w0)+"  "+padRight(r[1], w1)+" ")
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
	payload := func() *jsonx.Object {
		return jsonx.NewObject("mission_slug", slug, "passed", res.Passed(), "errors", res.Errors, "warnings", res.Warnings,
			"error_count", len(res.Errors), "warning_count", len(res.Warnings))
	}
	if !has {
		if jsonMode {
			printJSON(ctx.Stdout, payload())
		} else {
			consolePrint(ctx.Stdout, "Status Validation: "+slug)
			consolePrint(ctx.Stdout, "No events to validate.")
			consolePrint(ctx.Stdout, "Result: PASS")
		}
		return nil
	}
	if jsonMode {
		printJSON(ctx.Stdout, payload())
	} else {
		consolePrint(ctx.Stdout, "\nStatus Validation: "+slug)
		consolePrint(ctx.Stdout, strings.Repeat("-", 50))
		if len(res.Errors) > 0 {
			consolePrint(ctx.Stdout, fmt.Sprintf("Errors: %d", len(res.Errors)))
			for _, e := range res.Errors {
				consolePrint(ctx.Stdout, "  - "+e)
			}
		}
		if len(res.Warnings) > 0 {
			consolePrint(ctx.Stdout, fmt.Sprintf("Warnings: %d", len(res.Warnings)))
			for _, w := range res.Warnings {
				consolePrint(ctx.Stdout, "  - "+w)
			}
		}
		switch {
		case res.Passed() && len(res.Warnings) > 0:
			consolePrint(ctx.Stdout, fmt.Sprintf("\nResult: PASS (%d warning(s))", len(res.Warnings)))
		case res.Passed():
			consolePrint(ctx.Stdout, "\nResult: PASS")
		default:
			consolePrint(ctx.Stdout, "\nResult: FAIL")
		}
	}
	if !res.Passed() {
		return cli.Exit(1)
	}
	return nil
}
