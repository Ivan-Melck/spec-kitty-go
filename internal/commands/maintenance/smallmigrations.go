package maintenance

import (
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
)

func newRepinHooksCmd() *cli.Command {
	return &cli.Command{
		Name:  "repin-hooks",
		Short: "Re-pin this repository's pre-commit hook to the current interpreter (#254).",
		Long:  longHelp["migrate_repin-hooks"],
		Flags: []*cli.Flag{boolFlag("json", "Emit a JSON-stable summary report on stdout.")},
		Run: func(ctx *cli.Context) error {
			root, err := locateOrError(ctx)
			if err != nil {
				return err
			}
			rec, err := migrate.InstallPreCommitHook(root)
			if err != nil {
				errorLine(ctx, err.Error())
				return cli.Exit(1)
			}
			if ctx.Bool("json") {
				printJSON(ctx.Stdout, jsonx.NewObject("result", "repinned", "hook_path", rec.HookPath, "interpreter", rec.Interpreter))
				return nil
			}
			out := ctx.Stdout
			cprint(out, "\n[bold]repin-hooks summary[/bold]")
			cprintf(out, "  Hook        : %s", rec.HookPath)
			cprintf(out, "  Interpreter : %s", rec.Interpreter)
			cprint(out, "\n[green]Done.[/green] Pre-commit hook re-pinned to the current interpreter.")
			return nil
		},
	}
}

func newRebaselineDossierHashesCmd() *cli.Command {
	return &cli.Command{
		Name:  "rebaseline-dossier-hashes",
		Short: "One-time re-baseline of recorded dossier snapshot hashes (FR-009, WP05).",
		Long:  longHelp["migrate_rebaseline-dossier-hashes"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit a structured per-mission re-baseline report"),
			boolFlag("dry-run", "Preview which recorded snapshot hashes would be re-baselined, without writing"),
		},
		Run: func(ctx *cli.Context) error {
			wireWarn(ctx)
			root, err := locateOrError(ctx)
			if err != nil {
				return err
			}
			dry := groupBool(ctx, "dry-run")
			outcomes, err := migrate.RebaselineSnapshots(root, dry)
			if u, ok := err.(*migrate.UncaughtError); ok {
				return uncaught(ctx, u.Type, u)
			}
			changed, errored := 0, 0
			rows := []any{}
			for _, o := range outcomes {
				if o.Changed {
					changed++
				}
				if o.Error != nil {
					errored++
				}
				rows = append(rows, jsonx.NewObject("mission_slug", o.MissionSlug, "snapshot_path", o.SnapshotPath, "old_hash", o.OldHash,
					"new_hash", o.NewHash, "changed", o.Changed, "error", migrate.OptStr(o.Error)))
			}
			if ctx.Bool("json") {
				printJSON(ctx.Stdout, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(outcomes), "rebaselined", changed,
					"skipped", len(outcomes)-changed-errored, "error", errored), "results", rows))
				return nil
			}
			prefix := ""
			if dry {
				prefix = "(dry-run) "
			}
			cprintf(ctx.Stdout, "%sRe-baselined %d / %d recorded snapshot(s); %d error(s).", prefix, changed, len(outcomes), errored)
			for _, o := range outcomes {
				if o.Error != nil {
					cprintf(ctx.Stderr, "[yellow]skip[/yellow] %s: %s", o.MissionSlug, *o.Error)
				}
			}
			return nil
		},
	}
}
