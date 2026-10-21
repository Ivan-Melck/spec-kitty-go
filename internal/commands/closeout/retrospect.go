package closeout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/retrospective"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func summaryFlags(project, json, jsonOut, limit, since, malformed, filter string) []*cli.Flag {
	return []*cli.Flag{
		{Name: "project", Kind: cli.String, Metavar: "<path>", Help: project},
		{Name: "json", Kind: cli.Bool, Help: json},
		{Name: "json-out", Kind: cli.String, Metavar: "<path>", Help: jsonOut},
		{Name: "limit", Kind: cli.Int, Metavar: "<int range>", Default: 20, Help: limit},
		{Name: "since", Kind: cli.String, Metavar: "<str>", Help: since},
		{Name: "include-malformed", Kind: cli.Bool, Help: malformed},
		{Name: "filter", Kind: cli.String, Metavar: "<str>", Help: filter},
	}
}

func init() {
	addCommands(func(root, agent, _ *cli.Command) {
		retro := app.Group(root, "retrospect", "Retrospective authoring and summary (create / backfill / summary)")
		retro.Add(&cli.Command{
			Name:  "create",
			Short: "Author a retrospective for one completed mission.",
			Long:  "Author a retrospective for one completed mission.\n\nValidates mission completion, resolves policy, runs the generator,\nand writes the record. Use --overwrite or --update to handle existing records.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (mission_id, mid8, or mission_slug)"},
				{Name: "overwrite", Kind: cli.Bool, Help: "Replace an existing record (mutually exclusive with --update)"},
				{Name: "update", Kind: cli.Bool, Help: "Merge into an existing record (mutually exclusive with --overwrite)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit structured JSON output instead of Rich rendering"},
			},
			Run: runRetroCreate,
		})
		retro.Add(&cli.Command{
			Name:  "backfill",
			Short: "Author retrospective records for historical missions in bulk.",
			Long:  "Author retrospective records for historical missions in bulk.\n\nIterates completed missions in the given time window and authors\nretrospective.yaml records for those that don't already have one.\n\nPer-mission failures are NOT fatal; aggregate report shows them.",
			Flags: []*cli.Flag{
				{Name: "since", Kind: cli.String, Metavar: "<str>", Help: "Only consider missions completed on or after this ISO date (default: 30 days ago)"},
				{Name: "until", Kind: cli.String, Metavar: "<str>", Help: "Only consider missions completed on or before this ISO date (default: now)"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Restrict backfill to a single mission handle"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Report what would be authored without writing"},
				{Name: "emit-skipped", Kind: cli.Bool, Help: "Append a RetrospectiveSkipped event for skipped missions"},
				{Name: "emit-failures", Kind: cli.Bool, Help: "Append RetrospectiveCaptureFailed events for failed missions"},
				{Name: "json", Kind: cli.Bool, Help: "Emit a single aggregate JSON object at the end"},
			},
			Run: runRetroBackfill,
		})
		retro.Add(&cli.Command{
			Name:  "summary",
			Short: "Cross-mission retrospective summary.",
			Long: "Cross-mission retrospective summary.\n\nReads kitty-specs/*/retrospective.yaml and kitty-specs/*/status.events.jsonl to produce a cross-mission view.\n\n" +
				"Distinguishes four record states: has_findings / ran_no_findings / missing / failed.\n\nNo mutation is performed.",
			Flags: summaryFlags("Project root (default: current working directory)", "Emit JSON to stdout instead of Rich rendering",
				"Also write JSON to this file path", "Top-N for ranked sections (default: 20)", "ISO-8601 date; only include missions started on or after DATE",
				"Include malformed record detail in output", "Only show missions in this record state (has_findings|ran_no_findings|missing|failed)"),
			Run: runRetroSummary,
		})
		ar := app.Group(agent, "retrospect", "Retrospective synthesis commands")
		ar.Add(&cli.Command{
			Name:  "synthesize",
			Short: "Apply staged proposals from a mission's retrospective record.",
			Long: "Apply staged proposals from a mission's retrospective record.\n\n--dry-run is the default; pass --apply to mutate project state.\nflag_not_helpful is the only auto-applied kind (Q2-A).\n" +
				"Conflict detection is fail-closed: any conflict blocks the whole batch.\n\nWhen no retrospective.yaml exists, the command errors with RETROSPECTIVE_RECORD_MISSING (exit 1) and points to 'spec-kitty retrospect create'.\nPass --fabricate-empty to use the legacy auto-fabrication path instead.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (mission_id / mid8 / mission_slug)"},
				{Name: "apply", Kind: cli.Bool, Help: "Execute application after checks pass (default is dry-run)"},
				{Name: "proposal-id", Kind: cli.StringList, Metavar: "<str>", Help: "Restrict the batch to the given proposal ids, which must be accepted (repeatable). Any id that is pending, rejected or not in the record is refused (exit 1) before anything is applied."},
				{Name: "json-out", Kind: cli.String, Metavar: "<path>", Help: "Write JSON envelope to PATH in addition to other output"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON to stdout (suppresses Rich rendering)"},
				{Name: "actor-id", Kind: cli.String, Metavar: "<str>", Help: "Override provenance actor id (default: inferred from environment)"},
				{Name: "fabricate-empty", Kind: cli.Bool, Help: "Legacy: auto-fabricate an empty record when none exists (synthesize_fabricate provenance)"},
			},
			Run: runRetroSynthesize,
		})
		ar.Add(&cli.Command{
			Name:  "summary",
			Short: "Cross-mission retrospective summary.",
			Long: "Cross-mission retrospective summary.\n\nBack-compat alias: equivalent to `spec-kitty retrospect summary`.\n\n" +
				"Distinguishes four record states: has_findings / ran_no_findings / missing / failed.\n\nNo mutation is performed.",
			Flags: summaryFlags("Project root (default: cwd)", "Emit JSON to stdout", "Also write JSON to this file", "Top-N for ranked sections",
				"ISO-8601 date filter", "Include malformed record detail", "Filter by record state (has_findings|ran_no_findings|missing|failed)"),
			Run: runRetroSummary,
		})
	})
}

// ---------------------------------------------------------------------------
// summary

func runRetroSummary(ctx *cli.Context) error {
	project := ctx.String("project")
	if project == "" {
		project, _ = os.Getwd()
	} else if abs, err := filepath.Abs(project); err == nil {
		project = abs
	}
	if r, err := filepath.EvalSymlinks(project); err == nil {
		project = r
	}
	if !dirOK(filepath.Join(project, ".kittify")) && !dirOK(filepath.Join(project, "kitty-specs")) {
		sayErrStderr(ctx, fmt.Sprintf("Project root invalid: neither .kittify/ nor kitty-specs/ found in %s", project))
		return cli.Exit(1)
	}
	limit := ctx.Int("limit")
	if limit < 1 || limit > 100 {
		return cli.Usage(ctx.Cmd, "Invalid value for '--limit': %d is not in the range 1<=x<=100.", limit)
	}
	var since *time.Time
	if s := ctx.String("since"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			sayErrStderr(ctx, fmt.Sprintf("Invalid --since date %s. Expected ISO-8601 format (YYYY-MM-DD).", mission.PyRepr(s)))
			return cli.Exit(1)
		}
		since = &t
	}
	filter := ctx.String("filter")
	if ctx.IsSet("filter") {
		switch filter {
		case "has_findings", "ran_no_findings", "missing", "failed":
		default:
			sayErrStderr(ctx, fmt.Sprintf("Invalid --filter value %s. Must be one of: failed, has_findings, missing, ran_no_findings", mission.PyRepr(filter)))
			return cli.Exit(1)
		}
	}
	snap := retrospective.BuildSummary(project, since, limit)
	missions, agg := retrospective.MissionStates(project, filter)
	env := jsonx.NewObject("schema_version", "1", "command", "retrospect.summary", "generated_at", clock.NowUTCISO(),
		"result", snap, "missions", missions, "aggregate", agg)
	if filter != "" {
		env.Set("filter", filter)
	}
	w := ctx.Stdout
	if ctx.Bool("json") {
		printJSON(w, env)
	} else {
		renderSummary(ctx, snap, ctx.Bool("include-malformed"))
		var rows [][]string
		for _, kv := range agg.Pairs() {
			rows = append(rows, []string{kv.Key, fmt.Sprint(kv.Value)})
		}
		simpleTable(w, "Record State Summary (4-state)", []string{"State", "Count"}, rows, map[int]bool{1: true})
	}
	if out := ctx.String("json-out"); out != "" {
		os.MkdirAll(filepath.Dir(out), 0o755)
		if err := os.WriteFile(out, []byte(jsonx.Dumps(env, jsonx.Pretty)), 0o644); err != nil {
			sayErrStderr(ctx, fmt.Sprintf("Could not write JSON to %s: %v", out, err))
			return cli.Exit(2)
		}
		if !ctx.Bool("json") {
			say(w, "\n[dim]JSON written to "+out+"[/dim]")
		}
	}
	return nil
}

func dirOK(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func sayErrStderr(ctx *cli.Context, msg string) { sayLiteral(ctx.Stderr, "Error: ", msg) }

func renderSummary(ctx *cli.Context, s *jsonx.Object, includeMalformed bool) {
	w := ctx.Stdout
	fitPanel(w, "Cross-Mission Retrospective Summary", "")
	n := func(k string) string { v, _ := s.GetInt(k); return fmt.Sprint(v) }
	malformed := s.GetList("malformed")
	simpleTable(w, "Counts", []string{"Category", "Count"}, [][]string{
		{"mission_count", n("mission_count")}, {"completed", n("completed_count")}, {"skipped", n("skipped_count")},
		{"failed", n("failed_count")}, {"in_flight", n("in_flight_count")}, {"legacy_no_retro", n("legacy_no_retro_count")},
		{"terminus_no_retro", n("terminus_no_retro_count")}, {"malformed", fmt.Sprint(len(malformed))}}, map[int]bool{1: true})
	section := func(key, title, col, field string) {
		l := s.GetList(key)
		if len(l) == 0 {
			return
		}
		var rows [][]string
		for _, x := range l {
			o := x.(*jsonx.Object)
			c, _ := o.GetInt("count")
			rows = append(rows, []string{o.GetString(field), fmt.Sprint(c)})
		}
		simpleTable(w, title, []string{col, "Count"}, rows, map[int]bool{1: true})
	}
	section("not_helpful_top", "Top Not-Helpful Targets", "URN", "urn")
	section("missing_terms_top", "Top Missing Glossary Terms", "Term URN", "key")
	section("missing_edges_top", "Top Missing DRG Edges", "Edge URN", "urn")
	section("over_inclusion_top", "Top Over-Inclusion Targets", "URN", "urn")
	section("under_inclusion_top", "Top Under-Inclusion Targets", "URN", "urn")
	pa := s.GetObject("proposal_acceptance")
	var rows [][]string
	for _, kv := range pa.Pairs() {
		rows = append(rows, []string{kv.Key, fmt.Sprint(kv.Value)})
	}
	simpleTable(w, "Proposal Acceptance Metrics", []string{"Metric", "Count"}, rows, map[int]bool{1: true})
	section("skip_reasons_top", "Top Skip Reasons", "Reason", "reason")
	if len(malformed) > 0 {
		say(w, fmt.Sprintf("\n[bold red]Malformed records:[/bold red] %d", len(malformed)))
		if includeMalformed {
			var mrows [][]string
			for _, x := range malformed {
				o := x.(*jsonx.Object)
				id := o.GetString("mission_id")
				if id == "" {
					id = "(unknown)"
				}
				mrows = append(mrows, []string{id, o.GetString("path"), o.GetString("reason")})
			}
			simpleTable(w, "Malformed Detail", []string{"mission_id", "path", "reason"}, mrows, nil)
		}
	}
}

// ---------------------------------------------------------------------------
// create

func retroBlocked(ctx *cli.Context, jsonMode bool, code, human string, o *jsonx.Object, exit int) error {
	if jsonMode {
		printJSON(ctx.Stdout, o)
	} else {
		say(ctx.Stderr, "[red]Error "+code+":[/red] "+human)
	}
	return cli.Exit(exit)
}

func resolveRetroMission(ctx *cli.Context, root, handle string, jsonMode bool) (*mission.Resolved, error) {
	r, err := mission.ResolveIdentity(root, handle)
	switch e := err.(type) {
	case nil:
		return r, nil
	case *mission.NotFoundError:
		return nil, retroBlocked(ctx, jsonMode, "MISSION_NOT_FOUND", fmt.Sprintf("No mission found for handle %s. Check the mission handle or run `spec-kitty agent mission list`.", mission.PyRepr(handle)),
			jsonx.NewObject("result", "blocked", "code", "MISSION_NOT_FOUND", "blocked_reason", fmt.Sprintf("No mission found for handle %s.", mission.PyRepr(e.Handle)), "exit_code", 1), 1)
	case *mission.AmbiguousError:
		return nil, retroBlocked(ctx, jsonMode, "MISSION_AMBIGUOUS_SELECTOR", e.Error(),
			jsonx.NewObject("result", "blocked", "code", "MISSION_AMBIGUOUS_SELECTOR", "blocked_reason", e.Error(), "candidates", e.CandidatesJSON(), "exit_code", 2), 2)
	}
	return nil, err
}

func openWPs(dir string) []any {
	snap, err := status.ReduceDir(dir)
	if err != nil || snap.WorkPackages == nil {
		return nil
	}
	var out []any
	for _, kv := range snap.WorkPackages.Pairs() {
		lane := kv.Value.(*jsonx.Object).GetString("lane")
		if lane != "done" && lane != "canceled" {
			out = append(out, jsonx.NewObject("wp_id", kv.Key, "lane", lane))
		}
	}
	return out
}

func retroCommit(root, slug, message string, paths ...string) {
	var ex []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			ex = append(ex, p)
		}
	}
	coord.CommitPaths(root, message, ex...)
}

func runRetroCreate(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	if ctx.Bool("overwrite") && ctx.Bool("update") {
		say(ctx.Stderr, "[red]Error:[/red] --overwrite and --update are mutually exclusive. Pass exactly one.")
		return cli.Usage(ctx.Cmd, "Invalid value: --overwrite and --update are mutually exclusive")
	}
	root := locateRoot()
	if root == "" {
		say(ctx.Stderr, "[red]Error:[/red] Could not locate project root. Ensure you are inside a spec-kitty project.")
		return cli.Exit(1)
	}
	r, err := resolveRetroMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	dir := projectSpecs(root, r.Slug)
	if open := openWPs(coord.Resolve(root, r.Slug).StatusDir()); len(open) > 0 {
		var parts []string
		for _, o := range open {
			oo := o.(*jsonx.Object)
			parts = append(parts, fmt.Sprintf("%s (%s)", oo.GetString("wp_id"), oo.GetString("lane")))
		}
		reason := fmt.Sprintf("Mission has WPs in non-terminal lanes: %s. Complete the mission before authoring a retrospective.", strings.Join(parts, ", "))
		return retroBlocked(ctx, jsonMode, "MISSION_NOT_COMPLETED", reason, jsonx.NewObject("result", "blocked", "code", "MISSION_NOT_COMPLETED",
			"mission_id", r.MissionID, "mission_slug", r.Slug, "blocked_reason", reason, "open_wps", open, "exit_code", 1), 1)
	}
	path := filepath.Join(dir, retrospective.FileName)
	if _, err := os.Stat(path); err == nil && !ctx.Bool("overwrite") && !ctx.Bool("update") {
		return retroBlocked(ctx, jsonMode, "RETROSPECTIVE_RECORD_EXISTS",
			fmt.Sprintf("A retrospective record already exists at %s. Pass --overwrite to replace it or --update to merge.", path),
			jsonx.NewObject("result", "blocked", "code", "RETROSPECTIVE_RECORD_EXISTS", "mission_id", r.MissionID, "mission_slug", r.Slug,
				"record_path", path, "blocked_reason", "A retrospective record already exists for this mission. Pass --overwrite to replace it or --update to merge.", "exit_code", 1), 1)
	}
	rec, path, err := retrospective.Author(root, r.Slug, "explicit_create", "spec-kitty retrospect create")
	if err != nil {
		say(ctx.Stderr, "[red]Error:[/red] Generator failed: "+err.Error())
		return cli.Exit(1)
	}
	retroCommit(root, r.Slug, "chore(retrospective): author retrospective for "+rec.Slug, path, status.EventsPath(dir))
	counts := jsonx.NewObject("helped", len(rec.Helped), "not_helpful", len(rec.NotHelpful), "gaps", len(rec.Gaps), "proposals", 0, "evidence_refs", len(rec.Evidence))
	next := fmt.Sprintf("Run `spec-kitty agent retrospect synthesize --mission %s` to review proposals (dry-run by default; add --apply to mutate).", r.Slug)
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("result", "success", "mission_id", r.MissionID, "mission_slug", r.Slug, "record_path", path,
			"findings_status", rec.FindingsStatus, "counts", counts, "provenance_kind", "explicit_create",
			"policy_source", jsonx.NewObject("enabled", "<default>", "timing", "<default>", "failure_policy", "<default>"), "next_step", next))
		return nil
	}
	fitPanel(ctx.Stdout, fmt.Sprintf("Retrospective authored\n\nMission: %s\nRecord path: %s\nFindings status: %s\nCounts: helped=%d not_helpful=%d gaps=%d proposals=0\n\n%s",
		r.Slug, path, rec.FindingsStatus, len(rec.Helped), len(rec.NotHelpful), len(rec.Gaps), next), "spec-kitty retrospect create")
	return nil
}

// ---------------------------------------------------------------------------
// backfill

func parseWindow(ctx *cli.Context, flag string, def time.Time) (time.Time, error) {
	v := ctx.String(flag)
	if v == "" {
		return def, nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	if t, err := clock.ParseISO(v); err == nil {
		return t, nil
	}
	return time.Time{}, cli.Usage(ctx.Cmd, "Invalid value: Invalid --%s value %s. Expected ISO-8601 date (YYYY-MM-DD) or datetime.", flag, mission.PyRepr(v))
}

func runRetroBackfill(ctx *cli.Context) error {
	now := clock.Now().UTC()
	since, err := parseWindow(ctx, "since", now.AddDate(0, 0, -30))
	if err != nil {
		return err
	}
	until, err := parseWindow(ctx, "until", now)
	if err != nil {
		return err
	}
	root := locateRoot()
	if root == "" {
		say(ctx.Stderr, "[red]Error:[/red] Could not locate project root. Ensure you are inside a spec-kitty project.")
		return cli.Exit(1)
	}
	dry := ctx.Bool("dry-run")
	filter := ctx.String("mission")
	skipped, failed := []any{}, []any{}
	created := 0
	scanned := 0
	regRoot := filepath.Join(root, ".kittify", "missions")
	entries, _ := os.ReadDir(regRoot)
	for _, e := range entries {
		dir := filepath.Join(regRoot, e.Name())
		if !dirOK(dir) {
			continue
		}
		meta := mission.ReadMetaOrEmpty(dir)
		if meta.Len() == 0 {
			continue
		}
		id := meta.GetString("mission_id")
		slug := meta.GetString("mission_slug")
		if slug == "" {
			slug = meta.GetString("slug")
		}
		if id == "" || slug == "" {
			continue
		}
		scanned++
		skip := func(reason string) {
			skipped = append(skipped, jsonx.NewObject("mission_id", id, "mission_slug", slug, "reason", reason))
		}
		if filter != "" && id != filter && !strings.HasPrefix(id, filter) && slug != filter {
			skip("mission_filter_excluded")
			continue
		}
		done := meta.GetString("completed_at")
		if done == "" {
			done = meta.GetString("mission_completed_at")
		}
		t, perr := clock.ParseISO(done)
		if done == "" || perr != nil {
			skip("not_completed")
			continue
		}
		if t.Before(since) || t.After(until) {
			skip("out_of_window")
			continue
		}
		path := filepath.Join(root, "kitty-specs", slug, retrospective.FileName)
		if _, err := os.Stat(path); err == nil {
			skipped = append(skipped, jsonx.NewObject("mission_id", id, "mission_slug", slug, "reason", "already_exists", "record_path", path))
			continue
		}
		if dry {
			created++
			continue
		}
		if _, p, err := retrospective.Author(root, slug, "backfill", "spec-kitty retrospect backfill"); err != nil {
			failed = append(failed, jsonx.NewObject("mission_id", id, "mission_slug", slug, "failure_category", "generator_exception", "missing", []any{}, "remediation_hint", err.Error()))
		} else {
			created++
			retroCommit(root, slug, "chore(retrospective): backfill retrospective for "+slug, p, status.EventsPath(projectSpecs(root, slug)))
		}
	}
	next := []any{}
	if created > 0 && !dry {
		next = append(next, "Run `spec-kitty agent retrospect synthesize --mission <handle>` on newly authored records (dry-run by default; add --apply to mutate).")
	}
	if len(failed) > 0 {
		next = append(next, fmt.Sprintf("Inspect the %d failed mission(s) listed above.", len(failed)))
	}
	window := jsonx.NewObject("since", since.Format("2006-01-02"), "until", until.Format("2006-01-02"))
	if ctx.Bool("json") {
		printJSON(ctx.Stdout, jsonx.NewObject("result", "success", "window", window, "scanned", scanned, "created", created,
			"skipped", skipped, "failed", failed, "next_actions", next))
		return nil
	}
	suffix := ""
	if dry {
		suffix = " (dry-run — no files written)"
	}
	fmt.Fprintln(ctx.Stdout)
	fitPanel(ctx.Stdout, fmt.Sprintf("Backfill complete\n\nWindow: %s to %s\nScanned: %d | Created: %d | Skipped: %d | Failed: %d%s",
		window.GetString("since"), window.GetString("until"), scanned, created, len(skipped), len(failed), suffix), "spec-kitty retrospect backfill")
	return nil
}

// ---------------------------------------------------------------------------
// synthesize

func runRetroSynthesize(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		say(ctx.Stderr, "[red]Error:[/red] Could not locate project root. Ensure you are inside a spec-kitty project (has .kittify/ or kitty-specs/).")
		return cli.Exit(1)
	}
	handle := ctx.String("mission")
	r, err := mission.ResolveIdentity(root, handle)
	switch e := err.(type) {
	case nil:
	case *mission.NotFoundError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "status", "error",
				"outcome", "MISSION_NOT_FOUND", "error", "MISSION_NOT_FOUND", "handle", e.Handle,
				"next_action", "Check the mission handle or run `spec-kitty agent mission list`."))
		} else {
			say(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		}
		return cli.Exit(1)
	default:
		sayLiteral(ctx.Stderr, "", err.Error())
		return cli.Exit(1)
	}
	path := filepath.Join(projectSpecs(root, r.Slug), retrospective.FileName)
	if _, err := os.Stat(path); err != nil {
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("result", "blocked", "code", "RETROSPECTIVE_RECORD_MISSING", "mission_id", r.MissionID,
				"mission_slug", r.Slug, "blocked_reason", "No retrospective record found for this mission. Author one with: spec-kitty retrospect create --mission "+r.Slug, "exit_code", 1))
		} else {
			say(ctx.Stderr, "[red]Error RETROSPECTIVE_RECORD_MISSING:[/red] No retrospective record found for this mission.\nAuthor one with: [bold]spec-kitty retrospect create --mission "+r.Slug+"[/bold]")
		}
		return cli.Exit(1)
	}
	rec, reason := retrospective.LoadRecord(path)
	if rec == nil {
		if jsonMode {
			printJSON(ctx.Stderr, jsonx.NewObject("error", "record_malformed", "detail", reason))
		} else {
			sayLiteral(ctx.Stderr, "Error: ", "Retrospective record malformed: "+reason)
		}
		return cli.Exit(3)
	}
	if len(rec.GetList("proposals")) > 0 {
		say(ctx.Stderr, "[yellow]Warning:[/yellow] This retrospective uses the generator record schema; proposal application is not available for generator-shape proposals yet, so synthesize will run as an empty dry-run batch.")
	}
	if ids := ctx.Strings("proposal-id"); len(ids) > 0 {
		var uniq []string
		seen := map[string]bool{}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				uniq = append(uniq, id)
			}
		}
		detail := fmt.Sprintf("--proposal-id cannot pick proposals in this retrospective record: it uses the generator record schema, and proposal application does not support generator-shaped proposals yet. Requested: %s. Nothing was applied.", strings.Join(uniq, ", "))
		if jsonMode {
			st := &jsonx.Object{}
			idl := []any{}
			for _, id := range uniq {
				st.Set(id, "unsupported_generator_record")
				idl = append(idl, id)
			}
			printJSON(ctx.Stderr, jsonx.NewObject("error", "unsupported_generator_record", "detail", detail, "proposal_ids", idl, "statuses", st))
		} else {
			fmt.Fprintln(ctx.Stderr, "Error: "+detail)
		}
		return cli.Exit(1)
	}
	dry := !ctx.Bool("apply")
	result := jsonx.NewObject("dry_run", dry, "planned", []any{}, "applied", []any{}, "conflicts", []any{}, "rejected", []any{}, "events_emitted", []any{})
	env := jsonx.NewObject("schema_version", "1", "command", "agent.retrospect.synthesize", "generated_at", clock.NowUTCISO(),
		"dry_run", dry, "status", "ok", "outcome", "retrospective_synthesized", "result", result,
		"mission_id", r.MissionID, "mission_slug", r.Slug, "retrospective_path", path)
	if jsonMode {
		printJSON(ctx.Stdout, env)
	} else {
		label := "[yellow]DRY-RUN[/yellow]"
		if !dry {
			label = "[green]APPLY[/green]"
		}
		say(ctx.Stdout, "\n[bold]agent retrospect synthesize[/bold]  ("+label+")\n")
		say(ctx.Stdout, "[dim]No planned applications.[/dim]")
		say(ctx.Stdout, "\n[bold]Summary:[/bold] planned=0 applied=0 conflicts=0 rejected=0 events_emitted=0")
	}
	if out := ctx.String("json-out"); out != "" {
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(jsonx.Dumps(env, jsonx.Pretty)), 0o644)
		if !jsonMode {
			say(ctx.Stdout, "\n[dim]JSON written to "+out+"[/dim]")
		}
	}
	return nil
}
