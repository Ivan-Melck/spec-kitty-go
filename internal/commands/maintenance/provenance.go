package maintenance

import (
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

func projectRootFlag() *cli.Flag {
	return &cli.Flag{Name: "project-root", Kind: cli.String, Metavar: "DIR", Default: ".", Help: "Root of the Spec Kitty project (default: current working directory)."}
}

// resolvedPath is Path(p).resolve().
func resolvedPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

func newBackfillProvenanceCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-provenance",
		Short: "FR-014: backfill provenance onto legacy acceptance-matrix.json invariants.",
		Long:  longHelp["migrate_backfill-provenance"],
		Flags: []*cli.Flag{
			boolFlag("dry-run", "Report what would be stamped without writing any files. The JSON shape is identical to a live run."),
			boolFlag("json", "Emit a JSON-stable summary report on stdout."),
			projectRootFlag(),
		},
		Run: runBackfillProvenance,
	}
}

func runBackfillProvenance(ctx *cli.Context) error {
	dry := groupBool(ctx, "dry-run")
	root := resolvedPath(ctx.String("project-root"))
	sum, err := migrate.BackfillProvenance(root, dry)
	if err != nil {
		if u, ok := err.(*migrate.UncaughtError); ok {
			return uncaught(ctx, u.Type, u)
		}
		return uncaught(ctx, "OSError", err)
	}
	out := ctx.Stdout
	if ctx.Bool("json") {
		migrated := []any{}
		for _, r := range sum.Migrated {
			migrated = append(migrated, jsonx.NewObject("path", r.Path, "invariants_stamped", r.Stamped))
		}
		errs := []any{}
		for _, e := range sum.Errors {
			errs = append(errs, jsonx.NewObject("path", e.Path, "message", e.Message))
		}
		printJSON(out, jsonx.NewObject("dry_run", sum.DryRun, "result", sum.Result(),
			"summary", jsonx.NewObject("files_inspected", sum.FilesInspected, "migrated", len(sum.Migrated), "unchanged", len(sum.Unchanged),
				"errors", len(sum.Errors), "invariants_stamped", sum.StampedTotal()),
			"migrated", migrated, "errors", errs))
	} else {
		cprintf(out, "\n%s[bold]backfill-provenance summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Matrices scanned   : %d", sum.FilesInspected)
		cprintf(out, "  Migrated (files)   : %d", len(sum.Migrated))
		cprintf(out, "  Invariants stamped : %d", sum.StampedTotal())
		cprintf(out, "  Unchanged          : %d", len(sum.Unchanged))
		cprintf(out, "  Errors             : %d", len(sum.Errors))
		if len(sum.Errors) > 0 {
			cprint(out, "\n[red]Errors:[/red]")
			for _, e := range sum.Errors {
				cprintf(out, "  [red]%s:[/red] %s", e.Path, e.Message)
			}
		}
		switch {
		case dry:
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		case len(sum.Migrated) > 0:
			cprintf(out, "\n[green]Done.[/green] %d matrix file(s) received the legacy_unrecorded sentinel.", len(sum.Migrated))
		default:
			cprint(out, "\n[green]Done.[/green] Corpus already carries provenance.")
		}
	}
	if len(sum.Errors) > 0 {
		return cli.Exit(1)
	}
	return nil
}
