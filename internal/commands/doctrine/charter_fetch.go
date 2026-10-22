package doctrine

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// charter fetch (cli/commands/charter/authoring.py fetch).

func init() { registerCharterLeaf(charterFetchCmd) }

func charterFetchCmd() *cli.Command {
	short, long := helpK("charter_fetch")
	return &cli.Command{
		Name: "fetch", Short: short, Long: long,
		Flags: []*cli.Flag{
			{Name: "pack", Kind: cli.String, Help: "Fetch only the named pack (default: fetch all configured packs)."},
			{Name: "dry-run", Kind: cli.Bool, Help: "Show what would be fetched without contacting any remote."},
		},
		Run: runCharterFetch,
	}
}

func runCharterFetch(ctx *cli.Context) error {
	w := ctx.Stdout
	root := project.Locate("")
	if root == "" {
		cprint(w, "[red]Could not locate spec-kitty project root.[/red] Run from inside a project containing .kittify/.")
		return cli.Exit(1)
	}
	reg := charter.LoadPackRegistry(root)
	if len(reg.Packs) == 0 {
		cprint(w, "[red]No org charter packs configured.[/red]")
		cprint(w, "Add an entry to the [bold]charter_packs.org.packs[/bold] list in .kittify/config.yaml.")
		return cli.Exit(1)
	}
	targets := reg.Packs
	if name := ctx.OptString("pack"); name != nil {
		targets = nil
		for _, p := range reg.Packs {
			if p.Name == *name {
				targets = append(targets, p)
			}
		}
		if len(targets) == 0 {
			names := strings.Join(reg.Names(), ", ")
			if names == "" {
				names = "(none)"
			}
			cprint(w, "[red]Pack '%s' not found.[/red] Configured packs: %s", *name, names)
			return cli.Exit(1)
		}
	}
	if ctx.Bool("dry-run") {
		for _, p := range targets {
			origin := p.LocalPath
			if p.URL != nil && *p.URL != "" {
				origin = *p.URL
			}
			target, err := p.LocalPathRoot(root)
			if err != nil {
				cprint(w, "Would fetch pack '[bold]%s[/bold]' from %s — [red]cannot resolve target: %s[/red]", p.Name, origin, err.Error())
				continue
			}
			cprint(w, "Would fetch pack '[bold]%s[/bold]' from %s into %s", p.Name, origin, target)
		}
		return nil
	}
	failed := false
	for i := range targets {
		p := &targets[i]
		res := charter.FetchPack(p, root)
		if res.OK {
			suffix := ""
			if res.Unchanged {
				suffix = " (unchanged)"
			}
			cprint(w, "[green]Pack '%s': %d artifacts%s[/green]", p.Name, res.ArtifactsWritten, suffix)
			if res.PackVersion != nil && *res.PackVersion != "" {
				cprint(w, "  Version: %s", *res.PackVersion)
			}
		} else {
			cprint(w, "[red]Pack '%s' failed:[/red]", p.Name)
			for _, e := range res.Errors {
				cprint(w, "  %s", e)
			}
			failed = true
		}
	}
	if failed {
		return cli.Exit(1)
	}
	return nil
}
