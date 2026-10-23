package maintenance

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/placement"
)

func newDoctorDecisionsCmd() *cli.Command {
	return &cli.Command{
		Name: "decisions", Short: short("decisions"), Long: longHelp["doctor_decisions"],
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Required: true, Help: "Mission handle (mission_id / mid8 / slug)"},
			boolFlag("json", machineJSON),
			boolFlag("repair", "Rebuild decisions/index.json from the event log (run offline; not against live decision traffic)"),
		},
		Run: runDoctorDecisions,
	}
}

func runDoctorDecisions(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	slug, err := resolveMissionDirName(ctx, root, ctx.String("mission"), jsonOut)
	if err != nil {
		return err
	}
	rep, err := healthcheck.DiagnoseDecisions(root, slug)
	if err != nil {
		return uncaught(ctx, decisionsErrType(err, "JSONDecodeError"), err)
	}
	repair := ctx.Bool("repair")
	if repair && !rep.Clean() {
		var malformed []string
		if rep.Fork.Forked {
			malformed = rep.MalformedFolds
		} else {
			universe := map[string]bool{}
			for _, s := range rep.Fork.Streams {
				for _, l := range [][]string{s.OnlyPrimary, s.OnlyCoord, s.OnBoth} {
					for _, id := range l {
						universe[id] = true
					}
				}
			}
			malformed, err = healthcheck.RepairDecisions(root, slug, universe)
			if err != nil {
				return uncaught(ctx, decisionsErrType(err, "OSError"), err)
			}
		}
		copied := rep.Fork.LedgerCoordinationOnly()
		rep, err = healthcheck.DiagnoseDecisions(root, slug)
		if err != nil {
			return uncaught(ctx, "JSONDecodeError", err)
		}
		rep.Repaired = true
		rep.MalformedFolds = malformed
		rep.LedgerCopied = copied
	}
	var remedy any
	if len(rep.MalformedFolds) > 0 {
		remedy = fmt.Sprintf("left in place: the pre-repair index entry is kept unchanged where one existed, otherwise the decision_id stays out of the index (never invented). Inspect with 'spec-kitty agent decision list --mission %s' and the raw event log (status.events.jsonl).", rep.MissionSlug)
	}
	out := ctx.Stdout
	if jsonOut {
		sm := []any{}
		for _, s := range rep.StatusMismatch {
			sm = append(sm, s)
		}
		o := jsonx.NewObject("mission_slug", rep.MissionSlug, "clean", rep.Clean(), "log_decision_ids", strList(rep.LogIDs),
			"index_decision_ids", strList(rep.IndexIDs), "missing_from_index", strList(rep.MissingFromIndex), "orphaned_in_index", strList(rep.Orphaned),
			"repaired", rep.Repaired, "lossy_attribution", strList(rep.LossyAttribution), "malformed_folds", strList(rep.MalformedFolds),
			"status_mismatch", sm, "remedy", remedy, "findings", strList(rep.Findings), "ledger_copied", rep.LedgerCopied)
		if rep.Fork != nil {
			o.Set("forked", rep.Fork.Forked)
			o.Set("streams", rep.Fork.StreamsJSON())
			o.Set("ledger", jsonx.NewObject("state", rep.Fork.LedgerState, "entries_only_on_coordination", strList(rep.Fork.LedgerOnlyEntries),
				"dm_files_only_on_coordination", strList(rep.Fork.LedgerOnlyDM)))
			o.Set("reconcile_steps", strList(rep.Fork.ReconcileSteps))
		}
		printJSON(out, o)
	} else {
		if rep.Clean() {
			suffix := ""
			if rep.Repaired {
				suffix = " (repaired)"
			}
			cprintf(out, "[green]ok[/green]: %s -- decisions index matches the event log (%d decision(s))%s.", rep.MissionSlug, len(rep.LogIDs), suffix)
		} else {
			cprintf(out, "[yellow]diverged[/yellow]: %s", rep.MissionSlug)
			if len(rep.MissingFromIndex) > 0 {
				cprintf(out, "  missing from index (%d): %s", len(rep.MissingFromIndex), strings.Join(rep.MissingFromIndex, ", "))
			}
			if len(rep.Orphaned) > 0 {
				cprintf(out, "  orphaned in index, no backing event (%d): %s", len(rep.Orphaned), strings.Join(rep.Orphaned, ", "))
			}
		}
		if f := rep.Fork; f != nil {
			for _, s := range f.Streams {
				if s.State == "forked" {
					pc, cc := 0, 0
					if s.Primary != nil {
						pc = len(s.Primary.EventIDs)
					}
					if s.Coordination != nil {
						cc = len(s.Coordination.EventIDs)
					}
					cprintf(out, "  [red]%s[/red]: forked (primary=%d event(s), coordination=%d event(s))", s.Stream, pc, cc)
				}
			}
			if f.LedgerCoordinationOnly() {
				cprintf(out, "  [red]decisions ledger[/red]: only on the coordination branch (%d entr(ies), %d DM file(s))", len(f.LedgerOnlyEntries), len(f.LedgerOnlyDM))
			}
			for _, s := range f.ReconcileSteps {
				cprintf(out, "  [yellow]reconcile:[/yellow] %s", s)
			}
		}
		if rep.LedgerCopied {
			cprintf(out, "  [green]copied[/green] decisions ledger from the coordination branch -- commit with `spec-kitty spec-commit -m \"...\" kitty-specs/%s/decisions/` (or `spec-kitty accept`); no commit was created.", rep.MissionSlug)
		}
		if len(rep.MalformedFolds) > 0 {
			cprintf(out, "  [red]could not fold[/red] (%d) decision(s) from a malformed event-log group -- the DecisionPointOpened/Resolved envelopes for these decision_ids do not satisfy the canonical fold's invariants (index_fold.FoldError); %v Affected: %s", len(rep.MalformedFolds), remedy, strings.Join(rep.MalformedFolds, ", "))
		}
		if len(rep.StatusMismatch) > 0 {
			var parts []string
			for _, s := range rep.StatusMismatch {
				parts = append(parts, fmt.Sprintf("%s (index=%s, log=%s)", s.GetString("decision_id"), s.GetString("index_status"), s.GetString("folded_status")))
			}
			cprintf(out, "  [red]stale status[/red] (%d) index entry disagrees with the folded event log: %s", len(rep.StatusMismatch), strings.Join(parts, ", "))
		}
	}
	if repair && rep.Fork != nil && rep.Fork.Forked {
		return cli.Exit(1)
	}
	if repair && len(rep.MalformedFolds) > 0 {
		return cli.Exit(1)
	}
	return nil
}

// decisionsErrType is the uncaught exception class of a decisions doctor
// failure (a coordination worktree on the wrong branch raises
// CoordinationWorkspaceBranchMismatch from the placement seam).
func decisionsErrType(err error, fallback string) string {
	if bm, ok := err.(*placement.BranchMismatchError); ok {
		return bm.TypeName()
	}
	return fallback
}
