package maintenance

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const (
	missionHandleHelp = "Scope to a single mission (mission_id / mid8 / slug). Omit to process the whole corpus."
	completeFirstRun  = "Terminal evidence only applies to WPs seeded in the same run; WPs a previous run seeded `planned` are not driven to done later. The evidence manifest must be complete on the first live run."
)

func newBackfillWPStatusCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-wp-status",
		Short: "Seed the lane events a Mission's WP files lack, so the status snapshot counts every WP (#5579).",
		Long:  longHelp["migrate_backfill-wp-status"],
		Flags: []*cli.Flag{
			boolFlag("dry-run", "Report per-Mission would-seed plans; write nothing."),
			{Name: "mission", Kind: cli.String, Metavar: "HANDLE", Help: missionHandleHelp},
			{Name: "evidence-manifest", Kind: cli.String, Metavar: "FILE", Help: "YAML file marking Missions finished: missions: {<slug>: {reason: <text>}}. Applies only to WPs seeded in the same run, so it must be complete on the first live run."},
			boolFlag("json", "Emit the per-Mission repair result and summary as structured JSON."),
		},
		Run: runBackfillWPStatus,
	}
}

type wpsErr struct{ code, msg string }

func loadEvidenceManifest(path string) (map[string]string, *wpsErr) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, &wpsErr{"EVIDENCE_MANIFEST_UNREADABLE", fmt.Sprintf("Cannot read evidence manifest %s: %s", path, pyOSError(err))}
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, &wpsErr{"EVIDENCE_MANIFEST_INVALID", fmt.Sprintf("Evidence manifest %s is not valid YAML: %v", path, err)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &wpsErr{"EVIDENCE_MANIFEST_INVALID", "Evidence manifest must be a mapping with a 'missions' key"}
	}
	var problems []string
	for _, k := range o.Keys() {
		if k != "missions" {
			problems = append(problems, fmt.Sprintf("unknown top-level key %s (only 'missions' is allowed)", pyjson.ReprString(k)))
		}
	}
	missions, ok := valueGet(o, "missions").(*jsonx.Object)
	if !ok {
		problems = append(problems, "'missions' must be a mapping of Mission slug to {reason: <text>}")
		missions = &jsonx.Object{}
	}
	out := map[string]string{}
	for _, kv := range missions.Pairs() {
		slug := kv.Key
		var found []string
		if strings.TrimSpace(slug) == "" {
			found = append(found, fmt.Sprintf("Mission key %s must be a non-empty string (the exact kitty-specs directory name)", pyjson.ReprString(slug)))
		} else if e, ok := kv.Value.(*jsonx.Object); !ok {
			found = append(found, slug+": entry must be a mapping with a 'reason' key")
		} else {
			for _, k := range e.Keys() {
				if k != "reason" {
					found = append(found, fmt.Sprintf("%s: unknown key %s (only 'reason' is allowed)", slug, pyjson.ReprString(k)))
				}
			}
			r, isStr := valueGet(e, "reason").(string)
			if !isStr || strings.TrimSpace(r) == "" {
				found = append(found, slug+": 'reason' is required and must be a non-empty string")
			} else if len(found) == 0 {
				out[slug] = strings.TrimSpace(r)
			}
		}
		problems = append(problems, found...)
	}
	if len(problems) > 0 {
		return nil, &wpsErr{"EVIDENCE_MANIFEST_INVALID", "Invalid evidence manifest: " + strings.Join(problems, "; ")}
	}
	return out, nil
}

func valueGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func pyOSError(err error) string {
	if os.IsNotExist(err) {
		if pe, ok := err.(*os.PathError); ok {
			return fmt.Sprintf("[Errno 2] No such file or directory: %s", pyjson.ReprString(pe.Path))
		}
	}
	return err.Error()
}

func runBackfillWPStatus(ctx *cli.Context) error {
	wireWarn(ctx)
	jsonOut := ctx.Bool("json")
	out := ctx.Stdout
	root := locateRootQuiet()
	if root == "" {
		if jsonOut {
			emitJSON(out, jsonx.NewObject("success", false, "error_code", "NO_PROJECT_ROOT", "error", noProjectRoot))
		} else {
			errorLine(ctx, noProjectRoot)
		}
		return cli.Exit(1)
	}
	dry := groupBool(ctx, "dry-run")
	fail := func(e *wpsErr) error {
		if jsonOut {
			emitJSON(out, jsonx.NewObject("success", false, "error_code", e.code, "error", e.msg))
		} else {
			cprint(out, "[red]Error:[/red] "+escapeMarkup(e.msg))
		}
		return cli.Exit(1)
	}
	manifest := map[string]string{}
	var manifestPath any
	if ctx.IsSet("evidence-manifest") {
		p := ctx.String("evidence-manifest")
		manifestPath = p
		m, e := loadEvidenceManifest(p)
		if e != nil {
			return fail(e)
		}
		manifest = m
	}
	known := map[string]bool{}
	for _, l := range mission.List(root) {
		known[l.Slug] = true
	}
	var unknown []string
	for s := range manifest {
		if !known[s] {
			unknown = append(unknown, s)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return fail(&wpsErr{"EVIDENCE_MANIFEST_INVALID", fmt.Sprintf("Evidence manifest names Mission(s) that do not resolve: %s. Use the exact kitty-specs directory name.", strings.Join(unknown, ", "))})
	}
	var scope *string
	var missionArg any
	if ctx.IsSet("mission") {
		h := ctx.String("mission")
		missionArg = h
		if known[h] {
			scope = &h
		} else {
			r, err := resolveMissionHandle(ctx, root, h, jsonOut)
			if err != nil {
				return err
			}
			scope = &r.Slug
		}
	}
	results, err := migrate.ApplyWPStatusBackfillRepo(root, scope, dry, manifest)
	if u, ok := err.(*migrate.UncaughtError); ok {
		return uncaught(ctx, u.Type, u)
	}
	if err != nil {
		return fail(&wpsErr{"MISSION_SELECTOR_REJECTED", err.Error()})
	}
	newEvents := func(r migrate.WPStatusResult) int {
		if r.Seeded > r.WouldSeed {
			return r.Seeded
		}
		return r.WouldSeed
	}
	isLive := func(r migrate.WPStatusResult) bool {
		return r.SkipReason != nil && *r.SkipReason == migrate.CoordSurfaceLive
	}
	sum := map[string]int{}
	for _, r := range results {
		if r.Seeded > 0 {
			sum["missions_seeded"]++
		}
		if r.WouldSeed > 0 && r.Seeded == 0 {
			sum["missions_would_seed"]++
			sum["events_would_seed"] += r.WouldSeed
		}
		sum["events_seeded"] += r.Seeded
		if r.TerminalReason != nil && newEvents(r) > 0 {
			sum["finished_missions"]++
		}
		if len(r.SnapshotOnly) > 0 {
			sum["snapshot_only_missions"]++
		}
		if len(r.Malformed) > 0 {
			sum["malformed_missions"]++
		}
		if isLive(r) {
			sum["coord_surface_live_missions"]++
		}
		if r.RefreshError != nil {
			sum["refresh_warnings"]++
		}
		if r.Error == nil && !isLive(r) && newEvents(r) == 0 {
			sum["skipped"]++
		}
		if r.Error != nil {
			sum["errors"]++
		}
	}
	bySlug := map[string]migrate.WPStatusResult{}
	for _, r := range results {
		bySlug[r.Slug] = r
	}
	var manifestSlugs []string
	for s := range manifest {
		manifestSlugs = append(manifestSlugs, s)
	}
	sort.Strings(manifestSlugs)
	unused := []any{}
	var unusedPairs [][2]string
	for _, s := range manifestSlugs {
		r, ok := bySlug[s]
		reason := ""
		switch {
		case !ok:
			reason = "not in scope"
		case isLive(r):
			reason = "coordination surface live"
		case r.Error == nil && newEvents(r) == 0:
			reason = "nothing to seed"
		}
		if reason != "" {
			unused = append(unused, jsonx.NewObject("mission", s, "reason", reason))
			unusedPairs = append(unusedPairs, [2]string{s, reason})
		}
	}
	result := "success"
	if sum["errors"] > 0 {
		result = "errors_present"
	}
	if jsonOut {
		rows := []any{}
		for _, r := range results {
			rows = append(rows, jsonx.NewObject("slug", r.Slug, "seeded", r.Seeded, "would_seed", r.WouldSeed, "files_only", strList(r.FilesOnly),
				"snapshot_only", strList(r.SnapshotOnly), "malformed", strList(r.Malformed), "terminal_reason", migrate.OptStr(r.TerminalReason),
				"status_json_refreshed", r.StatusJSONRefreshed, "refresh_error", migrate.OptStr(r.RefreshError), "skip_reason", migrate.OptStr(r.SkipReason),
				"error", migrate.OptStr(r.Error)))
		}
		summary := &jsonx.Object{}
		summary.Set("scanned", len(results))
		for _, k := range []string{"missions_seeded", "missions_would_seed", "events_seeded", "events_would_seed", "finished_missions", "snapshot_only_missions", "malformed_missions", "coord_surface_live_missions", "refresh_warnings", "skipped", "errors"} {
			summary.Set(k, sum[k])
		}
		emitJSON(out, jsonx.NewObject("dry_run", dry, "result", result, "mission", missionArg, "summary", summary,
			"manifest", jsonx.NewObject("path", manifestPath, "entries", len(manifest), "unused", unused), "missions", rows))
	} else {
		title := "backfill-wp-status summary"
		if dry {
			title += " (dry-run)"
		}
		cprintf(out, "[bold]%s[/bold]", title)
		lines := [][2]string{
			{"Scanned", fmt.Sprintf("%d mission(s)", len(results))},
			{"Seeded", fmt.Sprintf("%d event(s) in %d mission(s)", sum["events_seeded"], sum["missions_seeded"])},
			{"Would seed", fmt.Sprintf("%d event(s) in %d mission(s)", sum["events_would_seed"], sum["missions_would_seed"])},
			{"Finished (WPs seeded -> done)", fmt.Sprintf("%d mission(s)", sum["finished_missions"])},
			{"Snapshot-only WPs reported", fmt.Sprintf("%d mission(s)", sum["snapshot_only_missions"])},
			{"Malformed WP files", fmt.Sprintf("%d mission(s)", sum["malformed_missions"])},
			{"Refused (live coord surface)", fmt.Sprintf("%d mission(s)", sum["coord_surface_live_missions"])},
			{"Refresh warnings (status.json)", fmt.Sprint(sum["refresh_warnings"])},
			{"Skipped (nothing to seed)", fmt.Sprint(sum["skipped"])},
			{"Errors", fmt.Sprint(sum["errors"])},
		}
		for _, l := range lines {
			cprintf(out, "  %-31s: %s", l[0], l[1])
		}
		verb := "seeded"
		if dry {
			verb = "would seed"
		}
		esc := escapeMarkup
		for _, r := range results {
			switch {
			case r.Error != nil:
				cprintf(out, "  [red]error[/red] %s: %s", esc(r.Slug), esc(*r.Error))
			case isLive(r):
				cprintf(out, "  [yellow]refused[/yellow] %s: %s -- %s", esc(r.Slug), migrate.CoordSurfaceLive, esc(migrate.CoordSurfaceLiveMsg))
			case newEvents(r) > 0:
				done := ""
				if r.TerminalReason != nil && *r.TerminalReason != "" {
					done = fmt.Sprintf(" -> done (%s)", esc(*r.TerminalReason))
				}
				cprintf(out, "  [green]%s[/green] %s: %d event(s) for %s%s", verb, esc(r.Slug), newEvents(r), strings.Join(r.FilesOnly, ", "), done)
			}
			if r.RefreshError != nil {
				cprintf(out, "  [yellow]warning[/yellow] %s: %s (events are written; run `spec-kitty materialize`)", esc(r.Slug), esc(*r.RefreshError))
			}
			if len(r.SnapshotOnly) > 0 {
				cprintf(out, "  [yellow]snapshot-only[/yellow] %s: %s (no WP file; not repaired)", esc(r.Slug), strings.Join(r.SnapshotOnly, ", "))
			}
			if len(r.Malformed) > 0 {
				cprintf(out, "  [yellow]malformed[/yellow] %s: %s (skipped)", esc(r.Slug), strings.Join(r.Malformed, ", "))
			}
		}
		for _, u := range unusedPairs {
			cprintf(out, "[yellow]WARNING:[/yellow] evidence manifest entry for %s had no effect (%s).", esc(u[0]), u[1])
		}
		if len(unusedPairs) > 0 {
			cprint(out, completeFirstRun)
		}
		if dry {
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		}
	}
	if result == "errors_present" {
		return cli.Exit(1)
	}
	return nil
}
