package maintenance

import (
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

func newDoctorReviewCycleReconcileCmd() *cli.Command {
	return &cli.Command{
		Name: "review-cycle-reconcile", Short: short("review-cycle-reconcile"), Long: longHelp["doctor_review-cycle-reconcile"],
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Help: "Scope to a single mission (mission_id / mid8 / slug)"},
			boolFlag("json", machineJSON),
		},
		Run: runDoctorReviewCycleReconcile,
	}
}

// resolveMissionDirName is resolve_mission_dir_with_bare_modern_fold: the
// bare-modern fold first, else the identity resolver (rendering its errors).
func resolveMissionDirName(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	if name, err := mission.ResolveHandle(root, handle); err == nil {
		return name, nil
	}
	r, err := resolveMissionHandle(ctx, root, handle, jsonMode)
	if err != nil {
		return "", err
	}
	return r.Slug, nil
}

func runDoctorReviewCycleReconcile(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	var names []string
	if ctx.IsSet("mission") {
		n, err := resolveMissionDirName(ctx, root, ctx.String("mission"), jsonOut)
		if err != nil {
			return err
		}
		names = []string{n}
	} else {
		names = migrate.MissionDirs(root)
	}
	reports := healthcheck.ReviewCycleReconcile(root, names)
	if jsonOut {
		rows := []any{}
		for _, r := range reports {
			rows = append(rows, r.ToObject())
		}
		printJSON(out, rows)
		return nil
	}
	if len(reports) == 0 {
		cprint(out, "[green]ok[/green]: no coordination-topology review-cycle mission needs reconciliation.")
		return nil
	}
	for _, r := range reports {
		if len(r.Findings) == 0 {
			cprintf(out, "[green]ok[/green]: %s (%s) -- no stranded records found.", r.MissionSlug, r.StrandedClass)
			continue
		}
		for _, f := range r.Findings {
			cprintf(out, "[yellow]warning[/yellow]: %s %s -- %d record(s) stranded under %s (retired by %s, class=%s); canonical directory is now %s",
				f.MissionSlug, f.WPID, len(f.RecordPaths), f.RetiredResolver, f.RetiringFR, f.StrandedClass, f.ResolvedDirectory)
			for _, p := range f.RecordPaths {
				cprintf(out, "    -> %s", p)
			}
		}
	}
	return nil
}
