package maintenance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

func newDoctorMissionStateCmd() *cli.Command {
	return &cli.Command{
		Name: "mission-state", Short: short("mission-state"), Long: longHelp["doctor_mission-state"],
		Flags: []*cli.Flag{
			boolFlag("audit", "Run mission-state audit (required to proceed)"),
			boolFlag("fix", "Repair mission-state artifacts in place and write a migration manifest"),
			boolFlag("teamspace-dry-run", "Synthesize canonical TeamSpace envelopes from local state and validate them"),
			boolFlag("json", "Emit JSON report to stdout"),
			{Name: "mission", Kind: cli.String, Help: "Scope to a single mission handle"},
			{Name: "fail-on", Kind: cli.String, Help: "Exit 1 if findings meet a gate (error|warning|info|teamspace-blocker)"},
			{Name: "fixture-dir", Kind: cli.String, Help: "Override scan root (for testing)"},
			boolFlag("include-fixtures", "Audit the bundled mission-state survey fixtures"),
			{Name: "manifest-path", Kind: cli.String, Help: "Path for --fix migration manifest"},
			boolFlag("allow-dirty", "Allow --fix when relevant git paths are already dirty"),
		},
		Run: runDoctorMissionState,
	}
}

var sevRank = map[string]int{"error": 0, "warning": 1, "info": 2}

func runDoctorMissionState(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	out := ctx.Stdout
	root := locateRootQuiet()
	n := 0
	for _, f := range []string{"audit", "fix", "teamspace-dry-run"} {
		if ctx.Bool(f) {
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(out, "Use --audit, --fix, or --teamspace-dry-run. See --help for options.")
		return nil
	}
	if n > 1 {
		fmt.Fprintln(ctx.Stderr, "Choose exactly one of --audit, --fix, or --teamspace-dry-run.")
		return cli.Exit(2)
	}
	failSev, failBlocker := "", false
	if ctx.IsSet("fail-on") {
		v := ctx.String("fail-on")
		switch v {
		case "teamspace-blocker":
			failBlocker = true
		case "error", "warning", "info":
			failSev = v
		default:
			fmt.Fprintf(ctx.Stderr, "Invalid --fail-on value: %s. Valid values: error, warning, info, teamspace-blocker\n", pyjson.ReprString(v))
			return cli.Exit(2)
		}
	}
	if ctx.Bool("include-fixtures") {
		if ctx.IsSet("fixture-dir") {
			fmt.Fprintln(ctx.Stderr, "Use only one of --include-fixtures or --fixture-dir.")
			return cli.Exit(2)
		}
		fmt.Fprintln(ctx.Stderr, "Bundled audit fixtures not found: <spec-kitty>/specify_cli/audit/fixtures")
		return cli.Exit(2)
	}
	if root == "" && !ctx.IsSet("fixture-dir") {
		if jsonOut {
			emitJSON(out, jsonError("not_in_project", "Not in a spec-kitty project"))
		} else {
			cprint(out, "[red]Error:[/red] Not in a spec-kitty project")
		}
		return cli.Exit(1)
	}
	if ctx.IsSet("fixture-dir") {
		fmt.Fprintln(ctx.Stderr, "Error: --fixture-dir is not supported by this build.")
		return cli.Exit(2)
	}
	if !ctx.Bool("audit") {
		// TODO(area H): the --fix repair (migration/mission_state.repair_repo)
		// and --teamspace-dry-run envelope synthesis are not ported.
		mode := "--fix"
		code := "MISSION_STATE_REPAIR_FAILED"
		if ctx.Bool("teamspace-dry-run") {
			mode, code = "--teamspace-dry-run", "TEAMSPACE_DRY_RUN_FAILED"
		}
		msg := fmt.Sprintf("doctor mission-state %s is not supported by this build", mode)
		if jsonOut {
			fmt.Fprintln(out, jsonx.Dumps(jsonx.NewObject("error", code, "message", msg), compactJSON))
		} else {
			fmt.Fprintln(ctx.Stderr, "Error: "+msg)
		}
		return cli.Exit(1)
	}
	var only *string
	if ctx.IsSet("mission") {
		m := ctx.String("mission")
		r, err := mission.ResolveIdentity(root, m)
		if err != nil {
			code, human := "MISSION_NOT_FOUND", fmt.Sprintf("Error: Mission not found: %s", pyjson.ReprString(m))
			if _, ok := err.(*mission.AmbiguousError); ok {
				code, human = "AMBIGUOUS_HANDLE", fmt.Sprintf("Error: Ambiguous handle: %s", pyjson.ReprString(m))
			}
			if jsonOut {
				fmt.Fprintln(out, jsonx.Dumps(jsonx.NewObject("error", code, "handle", m), compactJSON))
			} else {
				fmt.Fprintln(ctx.Stderr, human)
			}
			return cli.Exit(1)
		}
		slug := r.Slug
		if st, err := os.Stat(filepath.Join(root, "kitty-specs", slug)); err != nil || !st.IsDir() {
			slug = "\x00none"
		}
		only = &slug
	}
	rep := healthcheck.RunAudit(root, only)
	if jsonOut {
		fmt.Fprint(out, jsonx.Dumps(rep.ToObject(), jsonx.SortedPretty))
	} else {
		var rows [][]string
		for _, m := range rep.Missions {
			if len(m.Findings) == 0 {
				continue
			}
			e, w, i := 0, 0, 0
			codes := map[string]bool{}
			for _, f := range m.Findings {
				switch f.Severity {
				case "error":
					e++
				case "warning":
					w++
				case "info":
					i++
				}
				codes[f.Code] = true
			}
			var cs []string
			for c := range codes {
				cs = append(cs, c)
			}
			sort.Strings(cs)
			rows = append(rows, []string{m.Slug, fmt.Sprint(e), fmt.Sprint(w), fmt.Sprint(i), strings.Join(cs, ", ")})
		}
		if len(rows) == 0 {
			cprint(out, "[green]No findings — all missions are clean.[/green]")
		} else {
			plainTableR(out, []string{"Mission", "Errors", "Warnings", "Info", "Codes"}, []int{28, 7, 9, 6, 0}, []bool{false, true, true, true, false}, rows)
			fmt.Fprintln(out)
			s := rep.Summary()
			cprintf(out, "Total missions: %v | With errors: %v | With warnings: %v | TeamSpace blockers: %v",
				valueGet(s, "total_missions"), valueGet(s, "missions_with_errors"), valueGet(s, "missions_with_warnings"), valueGet(s, "teamspace_blockers"))
		}
	}
	for _, m := range rep.Missions {
		for _, f := range m.Findings {
			if failSev != "" && sevRank[f.Severity] <= sevRank[failSev] {
				return cli.Exit(1)
			}
			if failBlocker && healthcheck.IsTeamspaceBlocker(f) {
				return cli.Exit(1)
			}
		}
	}
	return nil
}
