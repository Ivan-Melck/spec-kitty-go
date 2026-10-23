package maintenance

import (
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func newBackfillRuntimeStateCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-runtime-state",
		Short: "Seed legacy runtime state as events, verify fail-closed, and flip status_phase.",
		Long:  longHelp["migrate_backfill-runtime-state"],
		Flags: []*cli.Flag{
			boolFlag("dry-run", "Seed nothing and flip nothing; report per-mission would-seed counts and would-flip."),
			{Name: "mission", Kind: cli.String, Metavar: "HANDLE", Help: missionHandleHelp},
			boolFlag("json", "Emit the per-mission cutover result list as structured JSON."),
		},
		Run: runBackfillRuntimeState,
	}
}

func cutoverFailed(r migrate.CutoverResult, dry bool) bool {
	if r.Error != nil {
		return true
	}
	if dry {
		return false
	}
	return r.Verify != nil && !r.Verify.OK
}

func cutoverDetail(r migrate.CutoverResult) string {
	if r.Error != nil {
		return *r.Error
	}
	if r.Verify != nil {
		return strings.Join(r.Verify.Mismatches, "; ")
	}
	return "unknown failure"
}

func runBackfillRuntimeState(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	dry := groupBool(ctx, "dry-run")
	jsonOut := ctx.Bool("json")
	var results []migrate.CutoverResult
	if ctx.IsSet("mission") {
		r, err := resolveMissionHandle(ctx, root, ctx.String("mission"), jsonOut)
		if err != nil {
			return err
		}
		res := migrate.CutoverMission(r.Dir, dry)
		if res.PlacementMismatch {
			// cutover_mission raises PlacementMismatchError uncaught here
			rich.PrintTraceback(ctx.Stderr, &rich.Exception{Type: "PlacementMismatchError", Value: *res.Error})
			return cli.Exit(1)
		}
		results = []migrate.CutoverResult{res}
	} else {
		results = migrate.CutoverRepo(root, dry)
	}
	out := ctx.Stdout
	var flipped, already, wouldSeed, wouldFlip, seeded, failed int
	rows := []any{}
	for _, r := range results {
		f := r.Flipped && !r.AlreadyMigrated
		wf := r.WouldFlip && !r.AlreadyMigrated
		if f {
			flipped++
		}
		if r.AlreadyMigrated {
			already++
		}
		if r.Seeded > 0 {
			wouldSeed++
		}
		if wf {
			wouldFlip++
		}
		seeded += r.Seeded
		fl := cutoverFailed(r, dry)
		if fl {
			failed++
		}
		var vok any
		mism := []any{}
		if r.Verify != nil {
			vok = r.Verify.OK
			if fl {
				mism = strList(r.Verify.Mismatches)
			}
		}
		rows = append(rows, jsonx.NewObject("slug", r.Slug, "flipped", f, "already_migrated", r.AlreadyMigrated, "would_flip", wf,
			"would_seed", r.Seeded > 0, "seeded_count", r.Seeded, "verify_ok", vok, "failed", fl, "mismatches", mism, "error", migrate.OptStr(r.Error)))
	}
	if jsonOut {
		printJSON(out, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(results), "flipped", flipped,
			"already_migrated", already, "would_seed", wouldSeed, "would_flip", wouldFlip, "seeded", seeded, "failed", failed), "results", rows))
	} else {
		cprintf(out, "\n%s[bold]backfill-runtime-state summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Total missions scanned : %d", len(results))
		var active []migrate.CutoverResult
		for _, r := range results {
			if !cutoverFailed(r, dry) {
				active = append(active, r)
			}
		}
		skipped := 0
		if dry {
			wf, ws := 0, 0
			for _, r := range active {
				w := r.WouldFlip && !r.AlreadyMigrated
				if w {
					wf++
				}
				if r.Seeded > 0 {
					ws++
				}
				if !w && r.Seeded == 0 {
					skipped++
				}
			}
			cprintf(out, "  %-27s : %d", "Would flip", wf)
			cprintf(out, "  %-27s : %d", "Would seed (verify pending)", ws)
		} else {
			m := 0
			for _, r := range active {
				if r.Flipped && !r.AlreadyMigrated {
					m++
				} else {
					skipped++
				}
			}
			cprintf(out, "  %-27s : %d", "Flipped", m)
		}
		cprintf(out, "  %-27s : %d", "Skipped (already migrated)", skipped)
		cprintf(out, "  Seed events                 : %d", seeded)
		cprintf(out, "  %-27s : %d", "Failed", failed)
		if failed > 0 {
			cprint(out, "\n[red]Failed (status_phase left untouched):[/red]")
			for _, r := range results {
				if cutoverFailed(r, dry) {
					cprintf(out, "  [red]%s:[/red] %s", r.Slug, cutoverDetail(r))
				}
			}
		}
		if dry {
			cprint(out, "\n[dim]Dry run — no seeds or flips written; verify runs post-seed on a live run.[/dim]")
		}
	}
	if !dry && failed > 0 {
		return cli.Exit(1)
	}
	return nil
}
