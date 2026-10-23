package maintenance

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func newDoctorIdentityCmd() *cli.Command {
	return &cli.Command{
		Name: "identity", Short: short("identity"), Long: longHelp["doctor_identity"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit structured JSON output (suitable for CI)"),
			{Name: "mission", Kind: cli.String, Help: "Scope report to a single mission slug"},
			{Name: "fail-on", Kind: cli.String, Help: "Exit non-zero if any mission is in the given state(s). Comma-separated list of: assigned, pending, legacy, orphan."},
		},
		Run: runDoctorIdentity,
	}
}

func newDoctorTopologyCmd() *cli.Command {
	return &cli.Command{
		Name: "topology", Short: short("topology"), Long: longHelp["doctor_topology"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit structured JSON output (suitable for CI)"),
			{Name: "mission", Kind: cli.String, Help: "Scope report to a single mission slug"},
		},
		Run: runDoctorTopology,
	}
}

func statesJSON(ss []healthcheck.IdentityState) []any {
	out := []any{}
	for _, s := range ss {
		out = append(out, s.ToObject())
	}
	return out
}

func runDoctorIdentity(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	all := healthcheck.AuditRepo(root)
	scoped := ctx.IsSet("mission")
	m := ctx.String("mission")
	if scoped {
		var f []healthcheck.IdentityState
		for _, s := range all {
			if s.Slug == m {
				f = append(f, s)
			}
		}
		if len(f) == 0 {
			d, err := healthcheck.PrimaryDirForHandle(root, m)
			if err != nil {
				return seamCrash(ctx, err)
			}
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				f = []healthcheck.IdentityState{healthcheck.ClassifyMission(d)}
			}
		}
		if len(f) == 0 {
			cprintf(out, "[red]Error:[/red] Mission not found: %s", pyjson.ReprString(m))
			return cli.Exit(1)
		}
		all = f
	}
	counts := map[string]int{"assigned": 0, "pending": 0, "legacy": 0, "orphan": 0}
	var legacyPaths, orphanPaths []string
	for _, s := range all {
		counts[s.State]++
		if s.State == "legacy" {
			legacyPaths = append(legacyPaths, s.Path)
		} else if s.State == "orphan" {
			orphanPaths = append(orphanPaths, s.Path)
		}
	}
	dupOrder, dups := healthcheck.DuplicatePrefixes(root)
	if scoped {
		var keep []string
		if p := prefix3OfSlug(m); p != "" {
			for _, k := range dupOrder {
				if k == p {
					keep = []string{k}
				}
			}
		}
		dupOrder = keep
	}
	ambOrder, amb := healthcheck.AmbiguousSelectors(all)
	failStates := map[string]bool{}
	if f := ctx.String("fail-on"); f != "" {
		for _, s := range strings.Split(f, ",") {
			if s = strings.TrimSpace(s); s != "" {
				failStates[s] = true
			}
		}
	}
	triggered := false
	for _, s := range all {
		if failStates[s.State] {
			triggered = true
		}
	}
	code := 0
	if triggered {
		code = 1
	}
	if jsonOut {
		dp := &jsonx.Object{}
		for _, k := range dupOrder {
			dp.Set(k, statesJSON(dups[k]))
		}
		as := &jsonx.Object{}
		for _, k := range ambOrder {
			as.Set(k, statesJSON(amb[k]))
		}
		summary := jsonx.NewObject("assigned", counts["assigned"], "pending", counts["pending"], "legacy", counts["legacy"], "orphan", counts["orphan"])
		printJSON(out, jsonx.NewObject("summary", summary, "missions", statesJSON(all), "duplicate_prefixes", dp, "ambiguous_selectors", as, "fail_on_triggered", triggered))
		return exitCode(code)
	}
	cprintf(out, "\n[bold]Mission Identity Audit[/bold] — %d mission(s)\n", len(all))
	var rows [][]string
	for _, st := range []string{"assigned", "pending", "legacy", "orphan"} {
		rows = append(rows, []string{st, fmt.Sprint(counts[st])})
	}
	plainTableR(out, []string{"State", "Count"}, []int{10, 6}, []bool{false, true}, rows)
	fmt.Fprintln(out)
	if len(dupOrder) > 0 {
		cprint(out, "[bold yellow]Duplicate Prefixes[/bold yellow]")
		keys := append([]string(nil), dupOrder...)
		sort.Strings(keys)
		for _, p := range keys {
			cprintf(out, "  [yellow]%s[/yellow] — %d collision(s):", p, len(dups[p]))
			for _, s := range dups[p] {
				mid := "[dim]no mission_id[/dim]"
				if s.MissionID != nil {
					mid = pyjson.Str(s.MissionID)
				}
				cprintf(out, "    %s  mission_id=%s  state=%s", s.Slug, mid, s.State)
			}
		}
		fmt.Fprintln(out)
	}
	if len(ambOrder) > 0 {
		cprint(out, "[bold yellow]Ambiguous Selectors[/bold yellow]")
		keys := append([]string(nil), ambOrder...)
		sort.Strings(keys)
		for _, h := range keys {
			cprintf(out, "  [yellow]%s[/yellow] → %d candidate(s):", pyjson.ReprString(h), len(amb[h]))
			for _, s := range amb[h] {
				cprintf(out, "    %s", s.Slug)
			}
		}
		fmt.Fprintln(out)
	}
	if len(dupOrder) == 0 && len(ambOrder) == 0 {
		cprint(out, "[green]No duplicate prefixes or ambiguous selectors.[/green]\n")
	}
	if len(legacyPaths) > 0 {
		cprint(out, "[bold red]Legacy missions (need backfill):[/bold red]")
		for _, p := range legacyPaths {
			cprintf(out, "  %s", p)
		}
		fmt.Fprintln(out)
	}
	if len(orphanPaths) > 0 {
		cprint(out, "[bold red]Orphan missions (need triage):[/bold red]")
		for _, p := range orphanPaths {
			cprintf(out, "  %s", p)
		}
		fmt.Fprintln(out)
	}
	if triggered {
		cprintf(out, "[bold red]FAIL:[/bold red] --fail-on %s triggered (one or more missions in: %s)", pyjson.ReprString(ctx.String("fail-on")), strings.Join(sortedKeys(failStates), ", "))
	}
	return exitCode(code)
}

func prefix3OfSlug(s string) string {
	if len(s) >= 4 && s[3] == '-' && strings.Trim(s[:3], "0123456789") == "" {
		return s[:3]
	}
	return ""
}

func runDoctorTopology(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	var mp *string
	if ctx.IsSet("mission") {
		m := ctx.String("mission")
		mp = &m
		scoped, err := healthcheck.TopologyRows(root, mp)
		if err != nil {
			return seamCrash(ctx, err)
		}
		if len(scoped) == 0 {
			cprintf(out, "[red]Error:[/red] Mission not found: %s", pyjson.ReprString(m))
			return cli.Exit(1)
		}
	}
	rows, err := healthcheck.TopologyRows(root, mp)
	if err != nil {
		return seamCrash(ctx, err)
	}
	if jsonOut {
		ms := []any{}
		for _, r := range rows {
			ms = append(ms, r.ToObject())
		}
		printJSON(out, jsonx.NewObject("missions", ms))
		return nil
	}
	cprintf(out, "\n[bold]Mission Topology Audit[/bold] — %d mission(s)\n", len(rows))
	var trows [][]string
	for _, r := range rows {
		topo := "null"
		if r.Topology != nil {
			topo = pyjson.Str(r.Topology)
		}
		fl := ""
		if r.Flattened != nil {
			fl = pyjson.Str(r.Flattened)
		}
		f := ""
		if r.Finding != nil {
			f = *r.Finding
		}
		trows = append(trows, []string{r.Slug, topo, fl, f})
	}
	plainTable(out, []string{"Mission", "Topology", "Flattened", "Finding"}, nil, trows)
	for _, r := range rows {
		if r.Remedy != nil {
			fmt.Fprintf(out, "\nRemedy for %s (%s): %s\n", r.Slug, *r.Finding, *r.Remedy)
		}
	}
	return nil
}

// seamCrash is an uncaught placement-seam exception (an unsafe path segment
// or an ambiguous handle): the traceback on stderr, exit 1.
func seamCrash(ctx *cli.Context, err error) error {
	typ := "UnsafePathSegmentError"
	if _, ok := err.(*mission.SelectorAmbiguousError); ok {
		typ = "MissionSelectorAmbiguous"
	}
	rich.PrintTraceback(ctx.Stderr, &rich.Exception{Type: typ, Value: err.Error()})
	return cli.Exit(1)
}
