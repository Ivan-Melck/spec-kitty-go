package maintenance

import (
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

func newNormalizeLifecycleCmd() *cli.Command {
	return &cli.Command{
		Name:  "normalize-lifecycle",
		Short: "Normalize legacy ``kitty-specs`` missions for the MVP lifecycle model.",
		Long:  longHelp["migrate_normalize-lifecycle"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit a structured per-mission normalization report"),
			boolFlag("dry-run", "Preview lifecycle normalization without modifying the filesystem"),
			missionSlugFlag(),
		},
		Run: runNormalizeLifecycle,
	}
}

func runNormalizeLifecycle(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	dry := groupBool(ctx, "dry-run")
	results := migrate.NormalizeLifecycle(root, dry, optMission(ctx))
	counts := map[string]int{}
	warned := 0
	rows := []any{}
	for _, r := range results {
		counts[r.Status]++
		if len(r.Warnings) > 0 {
			warned++
		}
		rows = append(rows, jsonx.NewObject("slug", r.Slug, "status", r.Status, "lifecycle_state", migrate.OptStr(r.LifecycleState),
			"actions", strList(r.Actions), "warnings", strList(r.Warnings), "error", migrate.OptStr(r.Error)))
	}
	out := ctx.Stdout
	if ctx.Bool("json") {
		printJSON(out, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(results), "normalized", counts["normalized"],
			"skipped", counts["skipped"], "error", counts["error"], "warnings", warned), "results", rows))
	} else {
		cprintf(out, "\n%s[bold]normalize-lifecycle summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Total missions scanned : %d", len(results))
		cprintf(out, "  Normalized             : %d", counts["normalized"])
		cprintf(out, "  Skipped                : %d", counts["skipped"])
		cprintf(out, "  Errors                 : %d", counts["error"])
		if warned > 0 {
			cprintf(out, "  [yellow]Warnings               : %d[/yellow]", warned)
		}
		for _, r := range results {
			if r.Status != "normalized" {
				continue
			}
			lc := ""
			if r.LifecycleState != nil && *r.LifecycleState != "" {
				lc = " (" + *r.LifecycleState + ")"
			}
			cprintf(out, "  [green]%s[/green]%s", r.Slug, lc)
			for _, a := range r.Actions {
				cprintf(out, "    - %s", a)
			}
			for _, w := range r.Warnings {
				cprintf(out, "    [yellow]- %s[/yellow]", w)
			}
		}
		if counts["skipped"] > 0 {
			cprint(out, "\n[dim]Skipped:[/dim]")
			for _, r := range results {
				if r.Status == "skipped" {
					cprintf(out, "  %s", r.Slug)
					for _, w := range r.Warnings {
						cprintf(out, "    [yellow]- %s[/yellow]", w)
					}
				}
			}
		}
		if counts["error"] > 0 {
			cprint(out, "\n[red]Errors:[/red]")
			for _, r := range results {
				if r.Status == "error" {
					cprintf(out, "  [red]%s:[/red] %s", r.Slug, deref(r.Error))
					for _, w := range r.Warnings {
						cprintf(out, "    [yellow]- %s[/yellow]", w)
					}
				}
			}
		}
		if dry {
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		} else if counts["error"] == 0 {
			cprint(out, "\n[green]Done.[/green] Lifecycle normalization is current.")
		}
	}
	if counts["error"] > 0 {
		return cli.Exit(1)
	}
	return nil
}
