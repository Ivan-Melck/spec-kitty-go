package setup

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/missionstate"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

// The tool-surface contract (specify_cli.tool_surface) lives in
// internal/toolsurface; this file is the `doctor tool-surfaces` shell.

func newDoctorToolSurfacesCmd() *cli.Command {
	return &cli.Command{
		Name:  "tool-surfaces",
		Short: "Audit (and optionally repair) every configured tool surface.",
		Long: "Audit (and optionally repair) every configured tool surface.\n\nExamples:\n    spec-kitty doctor tool-surfaces --json\n" +
			"    spec-kitty doctor tool-surfaces --kind command-skill --json\n    spec-kitty doctor tool-surfaces --tool codex --fix",
		Flags: []*cli.Flag{
			{Name: "kind", Kind: cli.StringList, Metavar: "<str>", Help: "Filter to surface kind(s), e.g. command-skill"},
			{Name: "tool", Kind: cli.String, Metavar: "<str>", Help: "Filter to a single configured tool key"},
			{Name: "fix", Kind: cli.Bool, Help: "Repair missing or stale surfaces"},
			{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
		},
		Run: runDoctorToolSurfaces,
	}
}

// CollectToolSurfaceCount is the surface_repair_wiring migration probe:
// the number of surfaces collected for tools plus the plugin bundle.
func CollectToolSurfaceCount(root string, tools []string) int {
	return toolsurface.CountSurfaces(root, tools)
}

func runDoctorToolSurfaces(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root, err := doctorProjectRoot(w, jsonOut, 2)
	if err != nil {
		return err
	}
	if ctx.Bool("fix") {
		cwd, _ := os.Getwd()
		if msg := missionstate.WriteRefusal(cwd, root); msg != "" {
			if jsonOut {
				printJSONRaw(w, jsonError("foreign_checkout_write_refused", msg))
			} else {
				out(w, "[red]Error:[/red] "+escapeMarkup(msg))
			}
			return cli.Exit(2)
		}
	}
	var kinds map[string]bool
	for _, tok := range ctx.Strings("kind") {
		k, err := toolsurface.KindFromToken(tok)
		if err != nil {
			if jsonOut {
				printJSONRaw(w, jsonError("unknown_kind", err.Error()))
			} else {
				out(w, "[red]Error:[/red] "+escapeMarkup(err.Error()))
			}
			return cli.Exit(2)
		}
		if kinds == nil {
			kinds = map[string]bool{}
		}
		kinds[k] = true
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		if jsonOut {
			printJSONRaw(w, jsonError("config_error", err.Error()))
		} else {
			out(w, "[red]Error:[/red] "+escapeMarkup(err.Error()))
		}
		return cli.Exit(2)
	}
	configured := append([]string(nil), ac.Available...)
	sort.Strings(configured)
	configured = dedupe(configured)
	outcome := toolsurface.RunToolSurfaces(root, configured, ctx.OptString("tool"), kinds, ctx.Bool("fix"))
	if jsonOut {
		printJSONRaw(w, outcome.JSON())
		return exitOrNil(boolCode(!outcome.Report.OK))
	}
	printToolSurfacesHuman(w, outcome)
	return exitOrNil(boolCode(!outcome.Report.OK))
}

func printToolSurfacesHuman(w io.Writer, o toolsurface.Outcome) {
	r := o.Report
	if r.OK && len(r.Findings) == 0 {
		out(w, fmt.Sprintf("[green]Tool Surfaces[/green]: all surfaces healthy (%d checked)", len(r.Surfaces)))
		return
	}
	out(w, "\n[bold]Tool Surfaces[/bold] - issue(s) found\n")
	for _, f := range r.Findings {
		colour := "yellow"
		if f.Severity == "error" {
			colour = "red"
		}
		out(w, fmt.Sprintf("  [%s]![/%s] [%s] %s", colour, colour, f.Code, f.Message))
	}
	if o.Repair != nil && len(o.Repair.Repaired) > 0 {
		out(w, fmt.Sprintf("\n[green]Repaired:[/green] %d surface(s)", len(o.Repair.Repaired)))
	}
}

func dedupe(s []string) []string {
	var out []string
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}
