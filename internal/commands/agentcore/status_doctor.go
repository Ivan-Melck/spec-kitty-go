package agentcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func init() {
	addCommands(func(_, _, statusG *cli.Command) {
		statusG.Add(&cli.Command{
			Name:  "doctor",
			Short: "Run health checks for status hygiene and global runtime.",
			Long: "Run health checks for status hygiene and global runtime.\n\nDetects global runtime issues (missing runtime directory, version mismatch,\n" +
				"corrupted missions) and project-level issues (stale claims, orphan\nworkspaces, drift).\nExit code 0 = healthy, 1 = issues found.\n\n" +
				"Examples:\n    spec-kitty agent status doctor\n    spec-kitty agent status doctor --mission 034-my-feature\n    spec-kitty agent status doctor --stale-claimed-days 3 --json",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "stale-claimed-days", Kind: cli.Int, Default: 7, Metavar: "<int>", Help: "Threshold for stale claims (days)"},
				{Name: "stale-in-progress-days", Kind: cli.Int, Default: 14, Metavar: "<int>", Help: "Threshold for stale in-progress (days)"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: runStatusDoctor,
		})
	})
}

type doctorCheck struct {
	name     string
	passed   bool
	message  string
	severity string
}

func globalRuntimeChecks(root string) []doctorCheck {
	home := project.GlobalKittify()
	var out []doctorCheck
	if st, err := os.Stat(home); err == nil && st.IsDir() {
		out = append(out, doctorCheck{"global_runtime_exists", true, home + " exists", "info"})
	} else {
		out = append(out, doctorCheck{"global_runtime_exists", false, "Missing global runtime: " + home, "error"})
	}
	lock := filepath.Join(home, "cache", "version.lock")
	if b, err := os.ReadFile(lock); err != nil {
		out = append(out, doctorCheck{"version_lock", false, "version.lock missing (incomplete update?)", "warning"})
	} else if v := strings.TrimSpace(string(b)); v == app.Version {
		out = append(out, doctorCheck{"version_lock", true, fmt.Sprintf("Version lock: %s (matches CLI)", v), "info"})
	} else {
		out = append(out, doctorCheck{"version_lock", false, fmt.Sprintf("Version mismatch: lock=%s, CLI=%s", v, app.Version), "warning"})
	}
	var missing []string
	for _, d := range []string{"missions/software-dev", "missions/research", "missions/documentation"} {
		if st, err := os.Stat(filepath.Join(home, d)); err != nil || !st.IsDir() {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		out = append(out, doctorCheck{"mission_integrity", true, "3 mission dirs present", "info"})
	} else {
		out = append(out, doctorCheck{"mission_integrity", false, "Missing: " + strings.Join(missing, ", "), "error"})
	}
	kittify := project.Kittify(root)
	if _, err := os.Stat(kittify); err != nil {
		out = append(out, doctorCheck{"stale_legacy", true, "No .kittify/ directory", "info"})
	} else {
		count := 0
		entries, _ := os.ReadDir(kittify)
		for _, e := range entries {
			switch {
			case e.IsDir() && (e.Name() == "templates" || e.Name() == "missions" || e.Name() == "scripts" || e.Name() == "command-templates"):
				filepath.Walk(filepath.Join(kittify, e.Name()), func(_ string, info os.FileInfo, err error) error {
					if err == nil && !info.IsDir() {
						count++
					}
					return nil
				})
			case !e.IsDir() && e.Name() == "AGENTS.md":
				count++
			}
		}
		if count == 0 {
			out = append(out, doctorCheck{"stale_legacy", true, "No stale shared assets", "info"})
		} else {
			out = append(out, doctorCheck{"stale_legacy", false, fmt.Sprintf("%d shared assets could be migrated. Run 'spec-kitty migrate'.", count), "warning"})
		}
	}
	g, err := charter.ResolveGovernance(root)
	switch {
	case err != nil:
		out = append(out, doctorCheck{"governance_resolution", false, "Could not resolve governance: " + err.Error(), "warning"})
	case charter.LoadSelection(root).TemplateSet == "":
		out = append(out, doctorCheck{"governance_resolution", true, fmt.Sprintf("Resolved governance with template fallback '%s'. Set governance.charter.template_set in .kittify/charter/charter.yaml to make this explicit.", g.TemplateSet), "warning"})
	default:
		out = append(out, doctorCheck{"governance_resolution", true, fmt.Sprintf("Resolved governance: %d paradigm(s), %d directive(s), %d tool(s), template_set=%s", len(g.Paradigms), len(g.Directives), len(g.Tools), g.TemplateSet), "info"})
	}
	return out
}

func runStatusDoctor(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := statusSurface(ctx, jsonMode)
	if err != nil {
		return err
	}
	checks := globalRuntimeChecks(m.Root)
	globalIssues := false
	for _, c := range checks {
		if !c.passed {
			globalIssues = true
		}
	}
	findings, err := status.RunDoctor(m.ReadDir(), m.Slug, m.Root, ctx.Int("stale-claimed-days"), ctx.Int("stale-in-progress-days"), doctorHooks(m))
	slugResolverWarn(ctx, jsonMode, m.ReadDir(), 2)
	if err != nil {
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("error", err.Error(), "healthy", false))
		} else {
			consolePrint(ctx.Stdout, "[red]Error:[/red] "+err.Error())
		}
		return cli.Exit(1)
	}
	healthy := len(findings) == 0 && !globalIssues
	if jsonMode {
		gr := []any{}
		for _, c := range checks {
			gr = append(gr, jsonx.NewObject("name", c.name, "passed", c.passed, "message", c.message, "severity", c.severity))
		}
		fs := []any{}
		for _, f := range findings {
			fs = append(fs, f.ToObject())
		}
		printJSON(ctx.Stdout, jsonx.NewObject("mission_slug", m.Slug, "healthy", healthy, "global_runtime", gr, "findings", fs))
	} else {
		consolePrint(ctx.Stdout, "\n[bold]Global Runtime:[/bold]")
		for _, c := range checks {
			icon, color := "✓", "green"
			if !c.passed {
				icon, color = "✗", "red"
				if c.severity == "warning" {
					icon, color = "⚠", "yellow"
				}
			}
			consolePrint(ctx.Stdout, "  ["+color+"]"+icon+"[/"+color+"] "+c.message)
		}
		consolePrint(ctx.Stdout, "\n[bold]Mission Status: "+m.Slug+"[/bold]")
		if len(findings) == 0 {
			consolePrint(ctx.Stdout, "  [green]Healthy[/green]")
		} else {
			consolePrint(ctx.Stdout, "  [yellow]Issues found[/yellow]")
			t := rich.NewTable()
			t.Title = "Doctor Findings"
			t.AddColumn("Severity", rich.Column{Style: "bold"})
			for _, h := range []string{"Category", "WP", "Message", "Action"} {
				t.AddColumn(h, rich.Column{})
			}
			for _, f := range findings {
				wpID := f.WPID
				if wpID == "" {
					wpID = "-"
				}
				sev := "yellow"
				if f.Severity == "error" {
					sev = "red"
				}
				t.AddRow("["+sev+"]"+f.Severity+"[/"+sev+"]", f.Category, wpID, f.Message, f.Action)
			}
			rich.PrintTo(ctx.Stdout, t)
		}
	}
	if !healthy {
		return cli.Exit(1)
	}
	return nil
}
