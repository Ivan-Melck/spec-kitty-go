package maintenance

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

func newRewriteOpposedByCmd() *cli.Command {
	return &cli.Command{
		Name:  "rewrite-opposed-by",
		Short: "Rewrite a pack's legacy ``opposed_by`` entries into DRG edges.",
		Long:  longHelp["migrate_rewrite-opposed-by"],
		Flags: []*cli.Flag{
			{Name: "pack", Kind: cli.String, Metavar: "PATH", Default: ".", Help: "Root directory of the target pack to migrate (org pack or any directory shaped like the built-in charter pack tree)."},
			boolFlag("dry-run", "Report planned rewrites without writing any files. The JSON shape is identical to a live run."),
			boolFlag("json", "Emit a structured JSON report on stdout."),
		},
		Run: runRewriteOpposedBy,
	}
}

func runRewriteOpposedBy(ctx *cli.Context) error {
	wireWarn(ctx)
	dry := groupBool(ctx, "dry-run")
	pack := resolvedPath(ctx.String("pack"))
	if st, err := os.Stat(pack); err != nil || !st.IsDir() {
		errorLine(ctx, "Pack root not found: "+pack)
		return cli.Exit(1)
	}
	res, err := migrate.RewriteOpposedBy(pack, dry)
	if err != nil {
		if u, ok := err.(*migrate.UncaughtError); ok {
			return uncaught(ctx, u.Type, u)
		}
		return uncaught(ctx, "Exception", err)
	}
	out := ctx.Stdout
	if ctx.Bool("json") {
		rw := []any{}
		for _, r := range res.Rewritten {
			rw = append(rw, jsonx.NewObject("source_file", r.SourceFile, "source", r.SourceType+":"+r.SourceID,
				"target", r.TargetType+":"+r.TargetID, "relation", r.Relation, "reason", migrate.OptStr(r.Reason),
				"created_anti_pattern_node", r.CreatedAntiPatternNode))
		}
		un := []any{}
		for _, u := range res.Unclassifiable {
			un = append(un, jsonx.NewObject("source_file", u.SourceFile, "source", u.SourceType+":"+u.SourceID,
				"target", u.TargetType+":"+u.TargetID, "message", u.Message))
		}
		printJSON(out, jsonx.NewObject("dry_run", dry, "pack_root", pack,
			"summary", jsonx.NewObject("rewritten", len(res.Rewritten), "unclassifiable", len(res.Unclassifiable)),
			"rewritten", rw, "unclassifiable", un))
	} else {
		cprintf(out, "\n%s[bold]rewrite-opposed-by summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Pack root      : %s", pack)
		cprintf(out, "  Rewritten      : %d", len(res.Rewritten))
		cprintf(out, "  Unclassifiable : %d", len(res.Unclassifiable))
		verb := "rewrote"
		if dry {
			verb = "would rewrite"
		}
		for _, r := range res.Rewritten {
			note := ""
			if r.CreatedAntiPatternNode {
				note = " (creates anti_pattern node)"
			}
			cprintf(out, "  [green]%s[/green] %s:%s --%s--> %s:%s%s", verb, r.SourceType, r.SourceID, r.Relation, r.TargetType, r.TargetID, note)
		}
		if len(res.Unclassifiable) > 0 {
			cprint(out, "\n[red]Unclassifiable entries (manual review required):[/red]")
			for _, u := range res.Unclassifiable {
				cprintf(out, "  [red]%s:[/red] %s", u.SourceFile, u.Message)
			}
		}
		switch {
		case dry:
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		case len(res.Rewritten) > 0:
			cprintf(out, "\n[green]Done.[/green] %d opposed_by entry(ies) rewritten to DRG edges.", len(res.Rewritten))
		default:
			cprint(out, "\n[green]Done.[/green] No opposed_by entries found.")
		}
	}
	if len(res.Unclassifiable) > 0 {
		return cli.Exit(1)
	}
	return nil
}
