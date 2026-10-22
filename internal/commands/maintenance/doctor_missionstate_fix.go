package maintenance

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/missionstate"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// softPrint is console.print(..., soft_wrap=True): markup rendered, no
// wrapping.
func softPrint(w io.Writer, s string) { fmt.Fprintln(w, rich.Strip(s)) }

// runMissionStateFix is _run_mission_repair (doctor mission-state --fix).
func runMissionStateFix(ctx *cli.Context, root, fixtureDir string, jsonOut bool) error {
	out := ctx.Stdout
	cwd, _ := os.Getwd()
	if fixtureDir == "" {
		if msg := missionstate.WriteRefusal(cwd, root); msg != "" {
			cprint(out, "[red]Refusing --fix:[/red] "+msg)
			return cli.Exit(1)
		}
	}
	argv := append([]string(nil), os.Args[1:]...)
	allowDirty := ctx.Bool("allow-dirty")
	healed, err := missionstate.RepairDuplicateKeysIn(root, fixtureDir, allowDirty, argv, app.Version)
	if err != nil {
		if jsonOut {
			fmt.Fprintln(out, jsonx.Dumps(jsonx.NewObject("error", "DUPLICATE_KEY_REPAIR_FAILED", "message", err.Error()), compactJSON))
		} else {
			fmt.Fprintln(ctx.Stderr, "Error: "+err.Error())
		}
		return cli.Exit(1)
	}
	n := 0
	for _, m := range healed.Missions {
		n += len(m.FileChanges)
	}
	if n > 0 && !jsonOut {
		cprintf(out, "[green]Healed %d duplicate-key artifact(s)[/green] (keep-last-non-empty). Manifest: %s", n, healed.ManifestPath)
	}
	rep, err := missionstate.RepairRepo(root, missionstate.Options{
		Mission: optString(ctx, "mission"), ManifestPath: ctx.String("manifest-path"), AllowDirty: allowDirty,
		Argv: argv, CLIVersion: app.Version, ScanRoot: fixtureDir,
	})
	if err != nil {
		if jsonOut {
			fmt.Fprintln(out, jsonx.Dumps(jsonx.NewObject("error", "MISSION_STATE_REPAIR_FAILED", "message", err.Error()), compactJSON))
		} else {
			fmt.Fprintln(ctx.Stderr, "Error: "+err.Error())
		}
		return cli.Exit(1)
	}
	if jsonOut {
		fmt.Fprint(out, rep.ToJSON())
	} else {
		cprintf(out, "[green]Mission-state repair complete[/green] (updated=%d, unchanged=%d, errors=%d).",
			rep.Count("updated"), rep.Count("unchanged"), rep.Count("error"))
		var errored, normalized, rebuilt []*missionstate.MissionResult
		for _, m := range rep.Missions {
			if m.Status == "error" {
				errored = append(errored, m)
			}
			if len(m.MetaActions) > 0 {
				normalized = append(normalized, m)
			}
			for _, a := range m.MetaActions {
				if a == missionstate.LanesRebuiltAction {
					rebuilt = append(rebuilt, m)
					break
				}
			}
		}
		if len(errored) > 0 {
			cprint(out, "Errored missions:")
			for _, m := range errored {
				reason := strings.Join(m.ValidationErrors, "; ")
				if reason == "" {
					reason = "<no detail recorded>"
				}
				softPrint(out, fmt.Sprintf("  - %s: %s", m.Slug, reason))
			}
		}
		if len(normalized) > 0 {
			cprint(out, "Normalized missions:")
			for _, m := range normalized {
				softPrint(out, fmt.Sprintf("  - %s: %s", m.Slug, strings.Join(m.MetaActions, ", ")))
			}
		}
		if len(rebuilt) > 0 {
			cprint(out, "[green]Rebuilt lanes.json from the event log for:[/green]")
			for _, m := range rebuilt {
				softPrint(out, "  - "+m.Slug)
			}
		}
		cprintf(out, "Audit trail written to %s (tracked, uncommitted).", rep.ManifestPath)
		cprint(out, "Commit it to preserve the record of this repair.")
		if q := rep.QuarantinedRows(); q > 0 && rep.QuarantineRootPath != nil {
			cprintf(out, "%d row(s) quarantined verbatim to %s.", q, *rep.QuarantineRootPath)
			cprint(out, "Commit the audit trail before running 'git clean'.")
		}
	}
	if rep.Count("error") > 0 {
		return cli.Exit(1)
	}
	return nil
}

// runTeamspaceDryRun is _run_teamspace_dry_run_mode.
func runTeamspaceDryRun(ctx *cli.Context, root, fixtureDir string, jsonOut bool) error {
	out := ctx.Stdout
	rep, err := missionstate.TeamspaceDryRunIn(root, fixtureDir, optString(ctx, "mission"))
	if err != nil {
		// upstream only catches MissionStateDryRunError; a selection error
		// (MissionStateRepairError) escapes as an uncaught exception.
		return uncaught(ctx, "MissionStateRepairError", err)
	}
	if jsonOut {
		fmt.Fprint(out, rep.ToJSON())
	} else if rep.Valid {
		cprintf(out, "[green]TeamSpace dry-run valid[/green] (%d envelopes, spec-kitty-events %s).", rep.EnvelopeCount, missionstate.EventsPackageVersion)
	} else {
		cprintf(out, "[red]TeamSpace dry-run failed[/red] (%d validation issues).", len(rep.Errors))
		for _, e := range rep.Errors {
			softPrint(out, formatDryRunError(e))
		}
	}
	if !rep.Valid {
		return cli.Exit(1)
	}
	return nil
}

// formatDryRunError is _format_dry_run_error.
func formatDryRunError(e *jsonx.Object) string {
	get := func(k string, def any) any {
		if v, ok := e.Get(k); ok {
			return v
		}
		return def
	}
	slug := pyjson.Str(get("mission_slug", "<unknown>"))
	artifact := pyjson.Str(get("artifact_path", ""))
	line := get("line_number", get("line", nil))
	code := pyjson.Str(get("error", get("finding_code", "")))
	msg := pyjson.Str(get("message", ""))
	loc := artifact
	if line != nil {
		loc = artifact + ":" + pyjson.Str(line)
	}
	return fmt.Sprintf("  - %s (%s): %s — %s", slug, loc, code, msg)
}
