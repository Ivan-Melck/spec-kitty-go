package doctrine

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// The doctrine-owned `doctor` leaves: charter-packs, invocation-pairing,
// ops, mission-type. They attach to the `doctor` group (owned by area B).

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "doctor", "Project health diagnostics")
		g.Add(doctorCharterPacksCmd(), doctorInvocationPairingCmd(), doctorOpsCmd(), doctorMissionTypeCmd())
	})
}

const machineJSONHelp = "Machine-readable JSON output"

func helpK(key string) (string, string) {
	long := longHelpK[key]
	// A usage line wrapped by the upstream renderer leaks into the captured
	// docstring as a leading indented paragraph; the help renderer builds
	// the usage itself.
	if strings.HasPrefix(long, " ") {
		if _, rest, ok := strings.Cut(long, "\n\n"); ok {
			long = rest
		}
	}
	short, _, _ := strings.Cut(long, "\n")
	return short, long
}

func leaf(key, name string, flags []*cli.Flag, run func(*cli.Context) error) *cli.Command {
	short, long := helpK(key)
	return &cli.Command{Name: name, Short: short, Long: long, Flags: flags, Run: run}
}

// doctorRoot is doctor._resolve_project_root_or_exit.
func doctorRoot(ctx *cli.Context, jsonMode bool, code int) (string, error) {
	if r := project.Locate(""); r != "" {
		return r, nil
	}
	if jsonMode {
		jsonError(ctx.Stdout, "not_in_project", "Not in a spec-kitty project")
	} else {
		cprint(ctx.Stdout, "[red]Error:[/red] Not in a spec-kitty project")
	}
	return "", cli.Exit(code)
}

// printJSONRich is CliConsole.print_json(json.dumps(payload, indent=2)):
// the pre-serialised text written verbatim (ensure_ascii=True).
func printJSONRich(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty))
}

func exitCode(code int) error {
	if code == 0 {
		return nil
	}
	return cli.Exit(code)
}

// pyPercent is f"{x:.0%}".
func pyPercent(x float64) string {
	return fmt.Sprintf("%.0f%%", x*100)
}

// --- doctor invocation-pairing -------------------------------------------

func doctorInvocationPairingCmd() *cli.Command {
	return leaf("doctor_invocation-pairing", "invocation-pairing",
		[]*cli.Flag{{Name: "json", Kind: cli.Bool, Help: machineJSONHelp}}, runDoctorInvocationPairing)
}

func runDoctorInvocationPairing(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonMode, 1)
	if err != nil {
		return err
	}
	w := ctx.Stdout
	rep := invocation.DoctorOrphanReport(root)
	count, _ := rep.GetInt("orphan_count")
	rateV, _ := rep.Get("pairing_rate")
	rate := float64(rateV.(jsonx.Float))
	groups, _ := rep.GetInt("total_groups")
	if jsonMode {
		fmt.Fprintln(w, jsonx.Dumps(rep, jsonx.SortedPretty))
		if count > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	if count == 0 {
		cprint(w, "[green]Invocation Pairing[/green]: no orphan started records (pairing rate: %s, groups: %d).", pyPercent(rate), groups)
		return nil
	}
	cprint(w, "\n[bold]Invocation Pairing[/bold] — %d orphan started record(s)\n", count)
	var rows [][]string
	orphans, _ := rep.Get("orphans")
	for _, o := range orphans.([]any) {
		e := o.(*jsonx.Object)
		wp := e.GetString("wp_id")
		if wp == "" {
			wp = "-"
		}
		rows = append(rows, []string{e.GetString("canonical_action_id"), e.GetString("agent"), e.GetString("mission_id"), wp, e.GetString("started_at")})
	}
	gridTable(w, []string{"Canonical Action ID", "Agent", "Mission ID", "WP", "Started At"}, []int{24, 10, 10, 6, 20}, nil, rows)
	cprint(w, "\nPairing rate: %s across %d group(s).", pyPercent(rate), groups)
	fmt.Fprintln(w)
	return cli.Exit(1)
}

// --- doctor ops ------------------------------------------------------------

func doctorOpsCmd() *cli.Command {
	return leaf("doctor_ops", "ops", []*cli.Flag{
		{Name: "json", Kind: cli.Bool, Help: machineJSONHelp},
		{Name: "close-stale", Kind: cli.Bool, Help: "Close open Ops older than --threshold as abandoned (closed_by=doctor_sweep)"},
		{Name: "threshold", Kind: cli.Float, Help: "Staleness threshold in hours (default 24; 0 closes all). Requires --close-stale."},
	}, runDoctorOps)
}

func runDoctorOps(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	closeStale := ctx.Bool("close-stale")
	if ctx.IsSet("threshold") && !closeStale {
		return cli.Usage(ctx.Cmd, "Invalid value: --threshold requires --close-stale")
	}
	root, err := doctorRoot(ctx, jsonMode, 1)
	if err != nil {
		return err
	}
	w := ctx.Stdout
	if closeStale {
		threshold := 24.0
		if ctx.IsSet("threshold") {
			threshold = ctx.Float("threshold")
		}
		return runOpsSweep(ctx, root, threshold, jsonMode)
	}
	orphans := invocation.ListOrphanOps(root)
	rel := func(p string) string {
		r, _ := filepath.Rel(root, p)
		return filepath.ToSlash(r)
	}
	if jsonMode {
		payload := []any{}
		for _, p := range orphans {
			payload = append(payload, jsonx.NewObject("path", rel(p)))
		}
		printJSONRich(w, payload)
		if len(orphans) > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	if len(orphans) == 0 {
		cprint(w, "[green]Ops[/green]: no orphan op records found.")
		return nil
	}
	cprint(w, "\n[bold]Ops[/bold] — %d orphan op record(s)\n", len(orphans))
	var rows [][]string
	for _, p := range orphans {
		rows = append(rows, []string{rel(p)})
	}
	gridTable(w, []string{"Path"}, nil, nil, rows)
	cprint(w, "\nThese op records were started but never completed. Run spec-kitty doctor ops --json for machine-readable output.")
	fmt.Fprintln(w)
	return cli.Exit(1)
}

func runOpsSweep(ctx *cli.Context, root string, threshold float64, jsonMode bool) error {
	w := ctx.Stdout
	rep := invocation.CloseStaleOps(root, threshold, clock.Now().UTC())
	if !jsonMode {
		for _, m := range rep.Warnings {
			rich.LogWarning(ctx.Stderr, m)
		}
	}
	code := 0
	if rep.HasErrors() || rep.SkippedFresh > 0 {
		code = 1
	}
	if jsonMode {
		printJSONRich(w, rep.Object())
		return exitCode(code)
	}
	cprint(w, "\n[bold]Ops sweep[/bold] — threshold %sh: %d closed as abandoned, %d fresh (skipped)\n",
		jsonx.FormatFloat(rep.ThresholdHours), rep.Swept, rep.SkippedFresh)
	if len(rep.OpenOps) > 0 {
		var rows [][]string
		for _, e := range rep.OpenOps {
			age := "?"
			if e.AgeHours != nil {
				age = fmt.Sprintf("%.1f", *e.AgeHours)
			}
			action := e.ActionTaken
			if e.Error != "" {
				action = "error: " + e.Error
			}
			commit := e.Commit
			if commit == "" {
				commit = "-"
			}
			rows = append(rows, []string{e.InvocationID, e.ProfileID, e.StartedAt, age, action, commit})
		}
		gridTable(w, []string{"Invocation ID", "Profile", "Started At", "Age (h)", "Action", "Closure commit"},
			[]int{26, 10, 20, 8, 16, 16}, []bool{false, false, false, true}, rows)
	}
	if rep.Swept > 0 {
		uncommitted := false
		for _, e := range rep.OpenOps {
			if e.Commit != "" && e.Commit != "committed" {
				uncommitted = true
			}
		}
		spine := invocation.ClosureSpine
		if uncommitted {
			cprint(w, "\n[yellow]Closures recorded on the append-only spine [cyan]%s[/cyan] but NOT committed[/yellow] (guard refusal or no git worktree). An uncommitted closure will not survive a checkout or reset — commit the spine yourself to make it durable.", spine)
		} else {
			cprint(w, "\nClosures recorded on the append-only spine [cyan]%s[/cyan] and committed to the current branch. They are visible only on branches that carry that commit.", spine)
		}
	}
	if rep.SkippedFresh > 0 {
		cprint(w, "\nFresh open Ops remain — close them with spec-kitty profile-invocation complete, or re-run with --threshold 0.")
	}
	fmt.Fprintln(w)
	return exitCode(code)
}
