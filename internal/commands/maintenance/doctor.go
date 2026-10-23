package maintenance

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// registerDoctor attaches the area-H mission/git-state leaves to the
// `doctor` group owned by area B.
func registerDoctor(root *cli.Command) {
	g := app.Group(root, "doctor", "Project health diagnostics")
	g.Add(doctorLeafCommands()...)
}

func doctorJSONFlag(help string) *cli.Flag { return boolFlag("json", help) }

const machineJSON = "Machine-readable JSON output"

func short(name string) string {
	return firstLine(longHelp["doctor_"+name])
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func doctorLeafCommands() []*cli.Command {
	return []*cli.Command{
		newDoctorIdentityCmd(),
		newDoctorTopologyCmd(),
		newDoctorWorkspacesCmd(),
		newDoctorSparseCheckoutCmd(),
		newDoctorRunIndexCmd(),
		newDoctorProvenanceCmd(),
		newDoctorCutoverCmd(),
		newDoctorReviewCycleReconcileCmd(),
		newDoctorDecisionsCmd(),
		newDoctorMissionStateCmd(),
		newDoctorCoordinationCmd(),
		{Name: "shim-registry", Short: short("shim-registry"), Long: longHelp["doctor_shim-registry"], Flags: []*cli.Flag{doctorJSONFlag(machineJSON)}, Run: runDoctorShimRegistry},
		{Name: "contracts", Short: short("contracts"), Long: longHelp["doctor_contracts"], Flags: []*cli.Flag{doctorJSONFlag(machineJSON)}, Run: runDoctorContracts},
	}
}

// doctorRoot is _doctor_shared.resolve_project_root_or_exit.
func doctorRoot(ctx *cli.Context, jsonOut bool, code int) (string, error) {
	if r := locateRootQuiet(); r != "" {
		return r, nil
	}
	if jsonOut {
		emitJSON(ctx.Stdout, jsonError("not_in_project", "Not in a spec-kitty project"))
	} else {
		cprint(ctx.Stdout, "[red]Error:[/red] Not in a spec-kitty project")
	}
	return "", cli.Exit(code)
}

// plainTable prints rich Table(box=None, padding=(0, 2), show_edge=False)
// with a cyan first column: a header row and data rows (markup cells).
func plainTable(w io.Writer, headers []string, minWidths []int, rows [][]string) {
	plainTableR(w, headers, minWidths, nil, rows)
}

// plainTableR is plainTable with per-column right justification.
func plainTableR(w io.Writer, headers []string, minWidths []int, right []bool, rows [][]string) {
	rich.GridTableStyled(w, headers, minWidths, right, []string{"cyan"}, rows)
}

func maxWord(s string) int {
	m := 0
	for _, f := range strings.Fields(s) {
		if l := utf8.RuneCountInString(f); l > m {
			m = l
		}
	}
	return m
}

func runDoctorShimRegistry(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 2)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	rep, err := healthcheck.CheckShimRegistry(root)
	if err != nil {
		switch e := err.(type) {
		case *healthcheck.SchemaError:
			cprint(out, "[red]Registry schema error:[/red]")
			for _, x := range e.Errors {
				cprintf(out, "  %s", x)
			}
		default:
			cprintf(out, "[red]Configuration error:[/red] %s", err.Error())
		}
		return cli.Exit(2)
	}
	code := 0
	if rep.HasOverdue() {
		code = 1
	}
	if jsonOut {
		entries := []any{}
		for _, e := range rep.Entries {
			entries = append(entries, jsonx.NewObject("legacy_path", e.LegacyPath, "canonical_import", e.CanonicalImport, "removal_target_release", e.RemovalTargetRelease,
				"grandfathered", e.Grandfathered, "tracker_issue", e.TrackerIssue, "status", e.Status, "shim_exists", e.ShimExists))
		}
		printJSON(out, jsonx.NewObject("project_version", rep.ProjectVersion, "registry_path", rep.RegistryPath, "entries", entries,
			"has_overdue", rep.HasOverdue(), "exit_code", code))
		return exitCode(code)
	}
	if len(rep.Entries) == 0 {
		cprint(out, "[green]Shim Registry[/green]: registry is empty — no shims to check.")
		return nil
	}
	cprintf(out, "\n[bold]Shim Registry[/bold] — %d entry/entries (project version: %s)\n", len(rep.Entries), rep.ProjectVersion)
	canon := func(v any) string {
		if l, ok := v.([]any); ok {
			var s []string
			for _, x := range l {
				s = append(s, fmt.Sprint(x))
			}
			return strings.Join(s, ", ")
		}
		return fmt.Sprint(v)
	}
	var rows [][]string
	for _, e := range rep.Entries {
		st := map[string]string{"pending": "[cyan]pending[/cyan]", "overdue": "[bold red]OVERDUE[/bold red]", "grandfathered": "[yellow]grandfathered[/yellow]", "removed": "[dim]removed[/dim]"}[e.Status]
		rows = append(rows, []string{e.LegacyPath, canon(e.CanonicalImport), e.RemovalTargetRelease, st})
	}
	plainTable(out, []string{"Legacy Path", "Canonical Import", "Removal Target", "Status"}, []int{24, 20, 14, 12}, rows)
	fmt.Fprintln(out)
	counts := map[string]int{}
	for _, e := range rep.Entries {
		counts[e.Status]++
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	cprintf(out, "Summary: %s", strings.Join(parts, ", "))
	if rep.HasOverdue() {
		fmt.Fprintln(out)
		cprint(out, "[bold red]Overdue shims must be resolved before release:[/bold red]")
		for _, e := range rep.Entries {
			if e.Status != "overdue" {
				continue
			}
			cprintf(out, "\n  [red]%s[/red]", e.LegacyPath)
			cprintf(out, "    Canonical import : %s", canon(e.CanonicalImport))
			cprintf(out, "    Removal target   : %s", e.RemovalTargetRelease)
			cprintf(out, "    Tracker          : %s", e.TrackerIssue)
			cprint(out, "    Remediation:")
			cprintf(out, "      Option A: Delete src/specify_cli/%s.py (or __init__.py)", strings.ReplaceAll(e.LegacyPath, ".", "/"))
			cprint(out, "      Option B: Extend removal_target_release in docs/migrations/shim-registry.yaml with extension_rationale")
		}
	}
	fmt.Fprintln(out)
	return exitCode(code)
}

func exitCode(code int) error {
	if code == 0 {
		return nil
	}
	return cli.Exit(code)
}

func runDoctorContracts(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 2)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	rep, err := healthcheck.CheckContractRegistry(root)
	if err != nil {
		if e, ok := err.(*healthcheck.SchemaError); ok {
			cprint(out, "[red]Contract registry schema error:[/red]")
			for _, x := range e.Errors {
				cprintf(out, "  %s", x)
			}
		} else {
			cprintf(out, "[red]Configuration error:[/red] %s", err.Error())
		}
		return cli.Exit(2)
	}
	adv, enf := rep.Counts()
	if jsonOut {
		recs := []any{}
		for _, r := range rep.Records {
			recs = append(recs, jsonx.NewObject("id", r.ID, "kind", r.Kind, "status", r.Status, "owner", r.Owner, "replaced_by", r.ReplacedBy,
				"enforcement", r.Enforcement, "scan_roots", strList(r.ScanRoots)))
		}
		printJSON(out, jsonx.NewObject("registry_path", rep.RegistryPath, "record_count", len(rep.Records), "advisory_count", adv,
			"enforcing_count", enf, "records", recs, "exit_code", 0))
		return nil
	}
	if len(rep.Records) == 0 {
		cprint(out, "[green]Contract Registry[/green]: registry is empty — nothing to validate.")
		return nil
	}
	cprintf(out, "\n[bold]Contract Registry[/bold] — %d record(s) (%d advisory, %d enforcing)\n", len(rep.Records), adv, enf)
	var rows [][]string
	for _, r := range rep.Records {
		rows = append(rows, []string{r.ID, r.Kind, r.Status, r.Enforcement, r.Owner})
	}
	plainTable(out, []string{"ID", "Kind", "Status", "Enforcement", "Owner"}, []int{24, 14, 10, 12, 8}, rows)
	fmt.Fprintln(out)
	cprint(out, "[green]Contract Registry[/green]: all records are well-formed.")
	fmt.Fprintln(out)
	return nil
}
