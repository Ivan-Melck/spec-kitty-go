package maintenance

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

func newDoctorCoordinationCmd() *cli.Command {
	return &cli.Command{
		Name: "coordination", Short: short("coordination"), Long: longHelp["doctor_coordination"],
		Flags: []*cli.Flag{
			boolFlag("fix", "Repair coordination topology: materialize a missing coordination worktree whose branch exists, remove stale `coordination_branch` keys for never-created branches (then `migrate backfill-topology`), and revert stranded coordination state."),
			boolFlag("json", machineJSON),
			boolFlag("check-staleness", "Also report coord-branch-vs-target-branch staleness (Gap-1, FR-008): non-blocking, whether the coord branch is behind or has diverged from its mission's target_branch."),
			{Name: "mission", Kind: cli.String, Help: "Scope the checks to a single mission handle (mission_id / mid8 / slug), resolved via the same resolver as `doctor mission-state`."},
		},
		Run: runDoctorCoordination,
	}
}

func runDoctorCoordination(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	var names []string
	if migrate.SpecsExist(root) {
		if ctx.IsSet("mission") {
			h := ctx.String("mission")
			r, rerr := mission.ResolveIdentity(root, h)
			if rerr != nil {
				code, label := "MISSION_NOT_FOUND", "Mission not found"
				if _, ok := rerr.(*mission.AmbiguousError); ok {
					code, label = "AMBIGUOUS_HANDLE", "Ambiguous handle"
				}
				msg := fmt.Sprintf("%s: %s", label, pyjson.ReprString(h))
				if jsonOut {
					o := jsonError(code, msg)
					o.Set("handle", h)
					emitJSON(out, o)
				} else {
					cprint(out, "[red]Error:[/red] "+msg)
				}
				return cli.Exit(1)
			}
			names = []string{r.Slug}
		} else {
			names = migrate.MissionDirs(root)
		}
	}
	stale := ctx.Bool("check-staleness")
	findings := healthcheck.CollectCoordination(root, names, stale)
	if ctx.Bool("fix") {
		extra := healthcheck.FixCoordination(root, names, findings, func(s string) { cprint(out, s) })
		findings = healthcheck.CollectCoordination(root, names, stale)
		findings = append(findings, extra...)
	}
	failed := false
	if jsonOut {
		rows := []any{}
		for _, f := range findings {
			rows = append(rows, f.ToObject())
		}
		printJSON(out, rows)
	}
	for _, f := range findings {
		if f.Severity == "error" {
			failed = true
		}
		if jsonOut {
			continue
		}
		colour := map[string]string{"ok": "green", "warning": "yellow", "error": "red"}[f.Severity]
		if colour == "" {
			colour = "white"
		}
		cprintf(out, "[%s]%s[/%s]: %s", colour, f.Severity, colour, f.Message)
		if f.NextStep != nil {
			cprintf(out, "  → %s", *f.NextStep)
		}
	}
	if failed {
		return cli.Exit(1)
	}
	return nil
}
