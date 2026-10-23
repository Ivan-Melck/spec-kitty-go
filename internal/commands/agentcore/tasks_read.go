package agentcore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const tasksStatusLong = "Display kanban status board for all work packages in a mission.\n\n" +
	"Shows a beautiful overview of work package statuses, progress metrics,\nand next steps based on dependencies.\n\n" +
	"WPs in \"doing\" with no commits for --stale-threshold minutes are flagged\nas potentially stale (agent may have stopped).\n\n" +
	"Example:\n    spec-kitty agent tasks status                  # the sole active mission\n" +
	"    spec-kitty agent tasks status --mission 012-documentation-mission\n" +
	"    spec-kitty agent tasks status --json\n    spec-kitty agent tasks status --stale-threshold 15"

const materializeLong = "Rebuild status.json from the canonical event log.\n\n" +
	"Reads all events from status.events.jsonl, applies the deterministic\nreducer to produce a snapshot, writes status.json, and updates legacy\ncompatibility views.\n\n" +
	"Examples:\n    spec-kitty agent status materialize\n    spec-kitty agent status materialize --mission 034-my-feature\n    spec-kitty agent status materialize --json"

func init() {
	addCommands(func(_, tasksG, statusG *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "status",
			Short: "Display kanban status board for all work packages in a mission.",
			Long:  tasksStatusLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (optional when exactly one active mission exists)"},
				{Name: "json", Kind: cli.Bool, Help: "Output as JSON"},
				{Name: "stale-threshold", Kind: cli.Int, Default: 10, Metavar: "<int>", Help: "Minutes of inactivity before a WP is considered stale"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."},
			},
			Run: runTasksStatus,
		})
		tasksG.Add(&cli.Command{
			Name:  "list-tasks",
			Short: "List tasks with optional lane filtering.",
			Long:  "List tasks with optional lane filtering.\n\nExamples:\nspec-kitty agent tasks list-tasks --json\nspec-kitty agent tasks list-tasks --lane doing --json",
			Flags: []*cli.Flag{
				{Name: "lane", Kind: cli.String, Metavar: "<str>", Help: "Filter by lane"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runListTasks,
		})
		statusG.Add(&cli.Command{
			Name:  "materialize",
			Short: "Rebuild status.json from the canonical event log.",
			Long:  materializeLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
			},
			Run: runMaterialize,
		})
	})
}

// --- status materialize -------------------------------------------------

func runMaterialize(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		outputError(ctx, jsonMode, "Could not locate project root")
		return cli.Exit(1)
	}
	slug, err := resolveTasksMission(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	m := newMissionCtx(root, slug)
	dir := m.ReadDir()
	events := status.EventsPath(dir)
	if _, err := os.Stat(events); err != nil {
		outputError(ctx, jsonMode, fmt.Sprintf("No event log found at %s\nRun 'spec-kitty agent status emit' to create the first event, or run a migration to initialize the event log.", events))
		return cli.Exit(1)
	}
	snap, err := status.Materialize(dir)
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	if jsonMode {
		printJSON(ctx.Stdout, snap.ToObject())
		return nil
	}
	consolePrint(ctx.Stdout, fmt.Sprintf("[green]Materialized[/green] %s: %d events -> %d WPs", slug, snap.EventCount, snap.WorkPackages.Len()))
	var parts []string
	keys := snap.Summary.Keys()
	sort.Strings(keys)
	for _, k := range keys {
		if n, _ := snap.Summary.GetInt(k); n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", k, n))
		}
	}
	if len(parts) > 0 {
		consolePrint(ctx.Stdout, "  "+strings.Join(parts, ", "))
	}
	return nil
}

// --- list-tasks ----------------------------------------------------------

// laneMap reduces the transition-only snapshot of dir to wp -> lane.
func laneMap(dir string) map[string]string {
	out := map[string]string{}
	st, err := status.ReadStream(dir, "")
	if err != nil || len(st.Transitions) == 0 {
		return out
	}
	snap := status.Reduce(st.Transitions, nil, true)
	for _, kv := range snap.WorkPackages.Pairs() {
		l := kv.Value.(*jsonx.Object).GetString("lane")
		if l == "" {
			l = string(status.Planned)
		}
		out[kv.Key] = l
	}
	return out
}

func wpGlob(tasksDir string) []string {
	matches, _ := filepath.Glob(filepath.Join(tasksDir, "WP*.md"))
	sort.Strings(matches)
	return matches
}

func runListTasks(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	if _, err := m.banner(ctx, jsonMode); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	tasksDir := m.TasksDir()
	if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
		outputError(ctx, jsonMode, "Tasks directory not found: "+tasksDir)
		return cli.Exit(1)
	}
	lanesByWP := laneMap(m.ReadDir())
	filter := ctx.String("lane")
	type row struct{ id, title, lane, path string }
	var rows []row
	for _, p := range wpGlob(tasksDir) {
		w, err := wp.Read(p)
		if err != nil {
			outputError(ctx, jsonMode, err.Error())
			return cli.Exit(1)
		}
		id := strings.TrimSpace(w.String("work_package_id"))
		if id == "" {
			id = w.Stem()
		}
		l, ok := lanesByWP[id]
		if !ok {
			l = string(status.Planned)
		}
		if filter != "" && l != filter {
			continue
		}
		rows = append(rows, row{id, w.String("title"), l, p})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	if jsonMode {
		list := []any{}
		for _, r := range rows {
			list = append(list, jsonx.NewObject("work_package_id", r.id, "title", r.title, "lane", r.lane, "path", r.path))
		}
		printJSON(ctx.Stdout, jsonx.NewObject("tasks", list, "count", len(rows)))
		return nil
	}
	in := ""
	if filter != "" {
		in = " in lane " + filter
	}
	if len(rows) == 0 {
		consolePrint(ctx.Stdout, "[yellow]No tasks found"+in+"[/yellow]")
		return nil
	}
	consolePrint(ctx.Stdout, "[bold]Tasks"+in+":[/bold]\n")
	for _, r := range rows {
		consolePrint(ctx.Stdout, fmt.Sprintf("  %s: %s [%s]", r.id, r.title, r.lane))
	}
	return nil
}

// --- tasks status ----------------------------------------------------------

var laneWeights = map[string]float64{"claimed": 0.05, "in_progress": 0.3, "for_review": 0.6, "in_review": 0.7, "approved": 0.8, "done": 1.0}

func statusSelectorError(ctx *cli.Context, code, msg string, exit int, details *jsonx.Object) error {
	o := jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", msg))
	if details != nil {
		for _, kv := range details.Pairs() {
			o.Set(kv.Key, kv.Value)
		}
	}
	printJSONPretty(ctx.Stdout, o)
	return cli.Exit(exit)
}

func statusError(ctx *cli.Context, jsonMode bool, msg string) error {
	if jsonMode {
		printJSONPretty(ctx.Stdout, jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", "status_failed", "message", msg)))
	} else {
		consolePrint(ctx.Stdout, "[red]Error:[/red] "+msg)
	}
	return cli.Exit(1)
}

// soleActiveMission is _sole_active_mission_slug_or_none.
func soleActiveMission(root string) string {
	var active []string
	for _, l := range mission.List(root) {
		m := newMissionCtx(root, l.Slug)
		snap, err := status.ReduceDir(m.ReadDir())
		completed := false
		if err == nil && snap.WorkPackages.Len() > 0 {
			completed = true
			for _, kv := range snap.WorkPackages.Pairs() {
				if !status.IsTerminal(status.Lane(kv.Value.(*jsonx.Object).GetString("lane"))) {
					completed = false
				}
			}
		}
		if !completed {
			active = append(active, l.Slug)
		}
	}
	if len(active) != 1 {
		return ""
	}
	return active[0]
}

type boardRow struct {
	obj   *jsonx.Object
	id    string
	title string
	lane  string
	deps  []string
	stale bool // _stale_verdict
	// arbiterCat is the category of the WP's complete event-sourced
	// ReviewOverride ("" when none).
	arbiterCat string
	stallLabel string // _stall_label
	staleRes   *staleResult
	marker     string // _get_hic_marker ("👤 " for sentinel profiles)
}

func (r *boardRow) disp() string { return r.marker + r.id }

func runTasksStatus(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		return statusError(ctx, jsonMode, "Not in a spec-kitty project")
	}
	// Owned resolution runs on the raw --mission value, before the
	// sole-active-mission default (resolve_owned_or_adopt).
	owned, err := resolveOwned(ctx, root, ctx.String("mission"), jsonMode, ownedcheckout.JSONErrorEnvelope, ownedcheckout.Options{})
	if err != nil {
		if _, ok := err.(*ownedcheckout.MetaReadError); ok {
			return statusError(ctx, jsonMode, err.Error())
		}
		return err
	}
	if owned != nil {
		return runTasksStatusFor(ctx, jsonMode, owned.OwnedRoot, owned.MissionSlug, owned)
	}
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		cwd, _ := os.Getwd()
		if c := soleActiveMission(root); c != "" && !ownedcheckout.WouldAdopt(root, cwd, nil) {
			handle = c
		}
	}
	if handle == "" {
		msg := "--mission <slug> is required"
		if jsonMode {
			return statusSelectorError(ctx, "mission_required", msg, 1, nil)
		}
		consolePrint(ctx.Stdout, "[red]Error:[/red] "+msg)
		return cli.Exit(1)
	}
	slug, err := mission.ResolveHandle(root, handle)
	if err != nil {
		if jsonMode {
			switch e := err.(type) {
			case *mission.SelectorAmbiguousError:
				return statusSelectorError(ctx, e.Code(), e.Error(), 2, jsonx.NewObject("handle", e.Handle, "candidates", strList(e.Candidates)))
			case *mission.AmbiguousError:
				return statusSelectorError(ctx, "MISSION_AMBIGUOUS_SELECTOR", e.Error(), 2, jsonx.NewObject("handle", e.Handle, "candidates", e.CandidatesJSON()))
			case *mission.NotFoundError:
				return statusSelectorError(ctx, "MISSION_NOT_FOUND", e.Error(), 2, jsonx.NewObject("handle", e.Handle))
			}
		}
		return renderResolveError(ctx, err, false)
	}
	return runTasksStatusFor(ctx, jsonMode, root, slug, nil)
}

// runTasksStatusFor renders the board for slug under root (the owned
// checkout P when owned is set: no branch banner, owned_checkout
// workspaces, the stale repository-root copy channel).
func runTasksStatusFor(ctx *cli.Context, jsonMode bool, root, slug string, owned *ownedcheckout.Fact) error {
	m := newMissionCtx(root, slug)
	readDir := m.ReadDir()
	tasksDir := m.TasksDir()
	if owned != nil {
		readDir = owned.MissionDir
		tasksDir = filepath.Join(owned.MissionDir, "tasks")
	} else {
		if _, err := m.banner(ctx, jsonMode); err != nil {
			return statusError(ctx, jsonMode, err.Error())
		}
		if _, err := os.Stat(readDir); err != nil {
			return statusError(ctx, jsonMode, "Mission directory not found: "+readDir)
		}
		if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
			return statusError(ctx, jsonMode, "Tasks directory not found: "+tasksDir)
		}
	}
	var transitions *status.Snapshot
	var events []*status.Event
	full := &status.Snapshot{WorkPackages: &jsonx.Object{}}
	// A corrupt log degrades the board to no events, but the per-WP runtime
	// row (reconstruct_wp_view -> wp_snapshot_state) re-reads it and fails
	// the command with the exception text.
	stream, streamErr := status.ReadStream(readDir, "")
	if streamErr == nil {
		events = stream.Transitions
		if len(stream.Transitions) > 0 {
			transitions = status.Reduce(stream.Transitions, nil, true)
		}
		full = status.Reduce(stream.Transitions, stream.Annotations, true)
	}
	manifest, _ := lanes.Read(m.PrimaryDir())
	var rows []*boardRow
	staleVerdicts := []any{}
	for _, p := range wpGlob(tasksDir) {
		w, err := wp.Read(p)
		if err != nil {
			return statusError(ctx, jsonMode, err.Error())
		}
		idRaw, hasID := w.Meta().Get("work_package_id")
		id := ""
		if hasID && idRaw != nil {
			id = w.String("work_package_id")
		}
		if id != "" && streamErr != nil {
			return statusError(ctx, jsonMode, streamErr.Error())
		}
		state := full.WP(id)
		lane := string(status.Genesis)
		get := func(k string) any {
			if state == nil {
				return ""
			}
			v, ok := state.Get(k)
			if !ok || v == nil || v == "" {
				return ""
			}
			sv := status.PyStr(v)
			switch {
			case k == "agent_profile" && sv == status.ResolvedProfileAbsent,
				k == "model" && sv == status.ResolvedModelAbsent:
				return ""
			}
			return sv
		}
		if state != nil && state.GetString("lane") != "" {
			lane = state.GetString("lane")
		}
		if lane == "doing" {
			lane = string(status.InProgress)
		}
		phase := w.String("phase")
		if phase == "" {
			phase = "Unknown Phase"
		}
		execMode, kind := workspaceFor(w, manifest, id)
		if owned != nil && id != "" {
			kind = lanes.OwnedCheckoutKind
		}
		var idVal any
		if id != "" {
			idVal = id
		}
		var titleVal any
		if t, ok := w.Meta().Get("title"); ok && t != nil {
			titleVal = w.String("title")
		}
		spca := get("shell_pid_created_at")
		if spca == "" {
			spca = nil
		}
		o := jsonx.NewObject("id", idVal, "title", titleVal, "lane", lane, "phase", phase, "file", filepath.Base(p),
			"agent", get("agent"), "agent_profile", w.String("agent_profile"),
			"resolved_agent_profile", get("agent_profile"), "resolved_role", get("role"), "resolved_model", get("model"),
			"shell_pid", get("shell_pid"), "shell_pid_created_at", spca,
			"execution_mode", execMode, "workspace_kind", kind)
		key := id
		if key == "" {
			key = w.Stem()
		}
		row := &boardRow{obj: o, id: id, title: w.String("title"), lane: lane, deps: w.Dependencies()}
		if id != "" && state != nil {
			if rv := state.GetObject("review"); rv != nil {
				if rv.GetString("at") != "" && rv.GetString("actor") != "" && rv.GetString("wp_id") != "" && rv.GetString("reason") != "" {
					row.arbiterCat, _ = parseArbiterNote(rv.GetString("reason"))
				}
			}
		}
		if id != "" {
			// _st_runtime_row: reconstruct_wp_view reads the log per WP.
			extraReadPasses(readDir, 1)
		}
		if id != "" && (lane == string(status.Approved) || lane == string(status.Done)) {
			// _apply_wp_review_verdict_flag: event_sourced_review_result.
			extraReadPasses(readDir, 1)
			if warn := reviewVerdictFlag(row, state); warn != nil {
				staleVerdicts = append(staleVerdicts, warn)
			}
		}
		rows = append(rows, row)
		_ = key
	}
	if !jsonMode {
		// _st_render_arbiter: get_arbiter_overrides_for_wp per WP.
		for _, r := range rows {
			if r.id != "" {
				extraReadPasses(readDir, 1)
			}
		}
	}
	stalledWPs := []any{}
	stallThreshold := reviewStallThreshold(root)
	now := clock.Now().UTC()
	for _, r := range rows {
		if r.id == "" || r.lane != string(status.InReview) {
			continue
		}
		var last time.Time
		for _, e := range events {
			if e.WPID != r.id || e.At == "" {
				continue
			}
			t, err := clock.ParseISO(e.At)
			if err != nil {
				continue
			}
			if t.UTC().After(last) {
				last = t.UTC()
			}
		}
		if last.IsZero() {
			continue
		}
		age := int(now.Sub(last).Seconds() / 60)
		if age > stallThreshold {
			r.stallLabel = fmt.Sprintf("STALLED — no move-task in %dm", age)
			warn := jsonx.NewObject("wp_id", r.id, "age_minutes", age, "threshold_minutes", stallThreshold)
			stalledWPs = append(stalledWPs, warn)
			r.obj.Set("_stall_label", r.stallLabel)
			r.obj.Set("review_stall", warn)
		}
	}
	// staleness for in_progress rows (check_doing_wps_for_staleness)
	var doing []*boardRow
	for _, r := range rows {
		if r.lane == string(status.InProgress) {
			doing = append(doing, r)
		}
	}
	// Claim liveness reads the WORK_PACKAGE_TASK (primary) dir's snapshot,
	// which is empty under a coordination topology (as the oracle does).
	livenessSnap, _ := status.ReduceDir(m.PrimaryDir())
	var staleResults map[string]*staleResult
	if owned != nil {
		staleResults = map[string]*staleResult{}
		for _, r := range doing {
			if r.id != "" {
				staleResults[r.id] = &staleResult{status: "not_applicable", reason: planningRepoRootReason, workspaceExists: true, workspaceKind: lanes.OwnedCheckoutKind}
			}
		}
	} else {
		staleResults = checkDoingStaleness(root, slug, doing, ctx.Int("stale-threshold"), livenessSnap)
	}
	staleCount := 0
	for _, r := range doing {
		if res := staleResults[r.id]; res != nil && r.id != "" {
			res.apply(r.obj)
			r.staleRes = res
			if res.isStale() {
				staleCount++
			}
		}
	}
	total := len(rows)
	byLane := map[string][]*boardRow{}
	counts := &jsonx.Object{}
	for _, r := range rows {
		byLane[r.lane] = append(byLane[r.lane], r)
		n, _ := counts.GetInt(r.lane)
		counts.Set(r.lane, n+1)
	}
	done := len(byLane["done"])
	donePct := 0.0
	if total > 0 {
		donePct = pyRound(float64(done)/float64(total)*100.0, 1)
	}
	var progress any = 0
	progressF := 0.0
	if transitions != nil {
		sum, n := 0.0, 0.0
		ids := transitions.WorkPackages.Keys()
		sort.Strings(ids)
		for _, id := range ids {
			sum += laneWeights[transitions.WorkPackages.GetObject(id).GetString("lane")]
			n++
		}
		pct := 0.0
		if n > 0 {
			pct = sum / n * 100.0
		}
		progressF = pyRound(pct, 1)
		progress = progressF
	}
	autoCommit := mission.AutoCommit(root)
	if jsonMode {
		list := []any{}
		for _, r := range rows {
			list = append(list, r.obj)
		}
		res := identityPayload(readDir)
		res.Set("total_wps", total)
		res.Set("by_lane", counts)
		res.Set("work_packages", list)
		res.Set("progress_percentage", progress)
		res.Set("progress_semantics", "weighted_readiness")
		res.Set("weighted_percentage", progress)
		res.Set("done_count", done)
		res.Set("done_percentage", donePct)
		res.Set("stale_wps", staleCount)
		res.Set("stale_verdicts", staleVerdicts)
		res.Set("stalled_wps", stalledWPs)
		res.Set("auto_commit", autoCommit)
		if owned != nil {
			res.Set("stale_repository_root_copy", ownedcheckout.StaleCopyValue(owned, nil))
		}
		printJSONPretty(ctx.Stdout, res)
		return nil
	}
	if len(rows) == 0 {
		consolePrint(ctx.Stdout, "[yellow]No work packages found in "+tasksDir+"[/yellow]")
		return nil
	}
	if owned != nil {
		ownedcheckout.EchoStaleCopyWarning(ctx.Stderr, owned)
	}
	pctText := "0"
	if transitions != nil {
		pctText = pyFloat(progressF)
	}
	applyHICMarkers(root, rows)
	renderBoard(ctx.Stdout, slug, rows, byLane, done, total, donePct, pctText, progressF, autoCommit)
	return nil
}

// workspaceFor resolves (execution_mode, workspace_kind) for a WP row.
func workspaceFor(w *wp.WP, manifest *lanes.Manifest, id string) (string, string) {
	mode := w.String("execution_mode")
	if id == "" {
		if mode == "" {
			mode = "code_change"
		}
		return mode, "unknown"
	}
	if mode == "" {
		mode = "code_change"
	}
	if mode == "planning_artifact" {
		return mode, "repo_root"
	}
	if manifest == nil {
		return mode, "unknown"
	}
	l := manifest.LaneForWP(id)
	if l == nil {
		return mode, "unknown"
	}
	if l.LaneID == lanes.PlanningLaneID {
		return mode, "repo_root"
	}
	return mode, "lane_workspace"
}

func renderBoard(w io.Writer, slug string, rows []*boardRow, byLane map[string][]*boardRow, done, total int, donePct float64, pctText string, pct float64, autoCommit bool) {
	human := func(r any) { rich.PrintTo(w, r) }
	// _st_render_overview
	human("")
	title := rich.NewText("")
	title.Append("📊 Work Package Status: ", "bold cyan")
	title.Append(slug, "bold white")
	human(&rich.RichPanel{R: title, BorderStyle: "cyan"})
	prog := rich.NewText("")
	prog.Append("Done progress: ", "bold")
	prog.Append(fmt.Sprintf("%d/%d", done, total), "bold green")
	prog.Append(fmt.Sprintf(" (%s%%)", pyFloat(donePct)), "dim")
	prog.Append("\nWeighted readiness: ", "bold")
	prog.Append(pctText+"%", "bold cyan")
	filled := floorInt(40 * pct / 100)
	prog.Append("\n"+strings.Repeat("█", filled)+strings.Repeat("░", 40-filled), "green")
	human(prog)
	human("")

	// _st_render_board
	cell := func(r *boardRow, kind string) string {
		t := r.title
		if len([]rune(t)) > 22 {
			t = truncateRunes(t, 22) + "..."
		}
		switch {
		case r.stale:
			return "[yellow]⚠ " + r.disp() + "[/yellow]\n" + t
		case r.lane == string(status.InProgress) && r.staleRes != nil && r.staleRes.isStale():
			return "[red]⚠️ " + r.disp() + "[/red]\n" + t
		case r.stallLabel != "":
			return "[yellow]⚠ " + r.disp() + " (review)[/yellow]\n" + t
		case kind == "claimed":
			return "[blue]" + r.disp() + " (claimed)[/blue]\n" + t
		case kind == "review":
			return "[bright_cyan]" + r.disp() + " (review)[/bright_cyan]\n" + t
		}
		return r.disp() + "\n" + t
	}
	cols := make([][]string, 5)
	for _, r := range byLane["planned"] {
		cols[0] = append(cols[0], cell(r, ""))
	}
	doing := 0
	for _, r := range byLane["claimed"] {
		cols[1] = append(cols[1], cell(r, "claimed"))
		doing++
	}
	for _, r := range byLane["in_progress"] {
		cols[1] = append(cols[1], cell(r, ""))
		doing++
	}
	for _, r := range byLane["in_review"] {
		cols[1] = append(cols[1], cell(r, "review"))
		doing++
	}
	for _, r := range byLane["for_review"] {
		cols[2] = append(cols[2], cell(r, ""))
	}
	for _, r := range byLane["approved"] {
		cols[3] = append(cols[3], cell(r, ""))
	}
	for _, r := range byLane["done"] {
		cols[4] = append(cols[4], cell(r, ""))
	}
	t := rich.NewTable()
	t.Title, t.HeaderStyle, t.BorderStyle = "Kanban Board", "bold magenta", "dim"
	for i, h := range []string{"📋 Planned", "🔄 Doing", "👀 For Review", "👍 Approved", "✅ Done"} {
		t.AddColumn(h, rich.Column{Style: []string{"yellow", "blue", "cyan", "magenta", "green"}[i], Width: rich.IntP(25)})
	}
	maxRows := 0
	for _, c := range cols {
		maxRows = max(maxRows, len(c))
	}
	for i := 0; i < maxRows; i++ {
		var cells []any
		for _, c := range cols {
			if i < len(c) {
				cells = append(cells, c[i])
			} else {
				cells = append(cells, "")
			}
		}
		t.AddRow(cells...)
	}
	t.AddRowStyled("dim", false,
		fmt.Sprintf("[bold]%d WPs[/bold]", len(byLane["planned"])), fmt.Sprintf("[bold]%d WPs[/bold]", doing),
		fmt.Sprintf("[bold]%d WPs[/bold]", len(byLane["for_review"])), fmt.Sprintf("[bold]%d WPs[/bold]", len(byLane["approved"])),
		fmt.Sprintf("[bold]%d WPs[/bold]", len(byLane["done"])))
	human(t)
	human("")

	// _st_render_arbiter
	bullet := func(r *boardRow) string { return fmt.Sprintf("  • %s - %s", r.disp(), r.title) }
	var arbiterLines []string
	for _, r := range rows {
		if r.arbiterCat != "" {
			arbiterLines = append(arbiterLines, fmt.Sprintf("  • %s Cycle 1: rejected → [yellow]overridden[/yellow] (%s)", r.id, r.arbiterCat))
		}
	}
	if len(arbiterLines) > 0 {
		human("[bold yellow]⚖️  Arbiter Override History:[/bold yellow]")
		for _, l := range arbiterLines {
			human(l)
		}
		human("")
	}
	// _st_render_review_queues
	if l := byLane["for_review"]; len(l) > 0 {
		human("[bold cyan]👀 Ready for Review:[/bold cyan]")
		for _, r := range l {
			human(bullet(r))
		}
		human("")
	}
	if l := byLane["approved"]; len(l) > 0 {
		human("[bold magenta]👍 Approved (merge when all WPs approved):[/bold magenta]")
		for _, r := range l {
			line := bullet(r)
			if r.stale {
				line += "  [bold yellow]⚠ review artifact: verdict=rejected[/bold yellow]"
			}
			human(line)
		}
		human("[dim]   Approved WPs stay here until feature merge. Dependents can start immediately.[/dim]")
		human("")
	}
	var doneStale []*boardRow
	for _, r := range byLane["done"] {
		if r.stale {
			doneStale = append(doneStale, r)
		}
	}
	if len(doneStale) > 0 {
		human("[bold green]✅ Done (with stale verdict warnings):[/bold green]")
		for _, r := range doneStale {
			human(bullet(r) + "  [bold yellow]⚠ review artifact: verdict=rejected[/bold yellow]")
		}
		human("")
	}
	// _st_render_active
	if l := byLane["claimed"]; len(l) > 0 {
		human("[bold blue]🔄 Claimed (shown in Doing column):[/bold blue]")
		for _, r := range l {
			agent, ok := r.obj.Get("agent")
			if !ok {
				agent = "unknown"
			}
			human(fmt.Sprintf("%s [dim](agent: %v)[/dim]", bullet(r), status.PyStr(agent)))
		}
		human("")
	}
	if l := byLane["in_progress"]; len(l) > 0 {
		human("[bold blue]🔄 In Progress:[/bold blue]")
		staleN := 0
		for _, r := range l {
			agent, ok := r.obj.Get("agent")
			if !ok {
				agent = "unknown"
			}
			label := r.staleRes.label()
			switch {
			case r.staleRes != nil && r.staleRes.isStale():
				human(fmt.Sprintf("  • [red]⚠️ %s[/red] - %s [dim](%s, agent: %v)[/dim]", r.disp(), r.title, label, status.PyStr(agent)))
				staleN++
			case label != "":
				human(fmt.Sprintf("%s [dim](%s, agent: %v)[/dim]", bullet(r), label, status.PyStr(agent)))
			default:
				human(bullet(r))
			}
		}
		human("")
		if staleN > 0 {
			human(fmt.Sprintf("[yellow]⚠️  %d stale WP(s) detected - agents may have stopped without transitioning[/yellow]", staleN))
			human("[dim]   Run: spec-kitty agent tasks move-task <WP_ID> --to for_review[/dim]")
			human("")
		}
	}
	if l := byLane["in_review"]; len(l) > 0 {
		human("[bold bright_cyan]🔍 In Review (shown in Doing column):[/bold bright_cyan]")
		for _, r := range l {
			line := bullet(r)
			if r.stallLabel != "" {
				line += "  [bold yellow]⚠ " + r.stallLabel + "[/bold yellow]"
			}
			human(line)
		}
		human("")
	}
	// _st_render_planned
	if l := byLane["planned"]; len(l) > 0 {
		human("[bold yellow]📋 Next Up (Planned):[/bold yellow]")
		for i, r := range l {
			if i >= 3 {
				break
			}
			human(bullet(r))
		}
		if len(l) > 3 {
			human(fmt.Sprintf("  [dim]... and %d more[/dim]", len(l)-3))
		}
		human("")
	}
	// _st_render_summary
	inProg := len(byLane["claimed"]) + len(byLane["in_progress"]) + len(byLane["in_review"]) + len(byLane["for_review"])
	ac := "[yellow]disabled[/yellow]"
	if autoCommit {
		ac = "[green]enabled[/green]"
	}
	g := rich.NewGrid(0, 2)
	g.AddColumn("", rich.Column{Style: "bold"})
	g.AddColumn("", rich.Column{})
	g.AddRow("Total WPs:", fmt.Sprint(total))
	g.AddRow("Completed:", fmt.Sprintf("[green]%d[/green] (%s%%)", done, pyFloat(donePct)))
	g.AddRow("Weighted readiness:", "[cyan]"+pctText+"%[/cyan]")
	g.AddRow("In Progress:", fmt.Sprintf("[blue]%d[/blue]", inProg))
	g.AddRow("Planned:", fmt.Sprintf("[yellow]%d[/yellow]", len(byLane["planned"])))
	g.AddRow("Auto-commit:", ac)
	human(&rich.RichPanel{R: g, Title: "[bold]Summary[/bold]", BorderStyle: "dim"})
	human("[bold]▶ Next action:[/bold]")
	human("  [cyan]spec-kitty next --agent <your-name> --mission " + slug + "[/cyan]")
	human("[dim]  This command tells you exactly what to do next based on the dependency graph.[/dim]")
	human("")
}

// reviewVerdictFlag is _apply_wp_review_verdict_flag: annotate an
// approved/done row whose event-sourced review_result is a rejection
// (stale) or damaged. Returns the stale_verdicts entry, if any.
func reviewVerdictFlag(row *boardRow, state *jsonx.Object) *jsonx.Object {
	if state == nil {
		return nil
	}
	raw, ok := state.Get("review_result")
	if !ok || raw == nil {
		return nil
	}
	rr, isObj := raw.(*jsonx.Object)
	var verdict, ref any
	valid := false
	if isObj {
		_, hasR := rr.Get("reviewer")
		v, hasV := rr.Get("verdict")
		r, hasRef := rr.Get("reference")
		valid = hasR && hasV && hasRef
		verdict, ref = v, r
	}
	if !valid {
		warn := jsonx.NewObject("wp_id", row.id, "artifact", nil, "verdict", nil, "damaged", true)
		row.obj.Set("_damaged_verdict", true)
		row.obj.Set("damaged_review_artifact", warn)
		return warn
	}
	if verdict != "changes_requested" {
		return nil
	}
	warn := jsonx.NewObject("wp_id", row.id, "artifact", ref, "verdict", "rejected")
	row.stale = true
	row.obj.Set("_stale_verdict", true)
	row.obj.Set("stale_review_artifact", warn)
	return warn
}

// reviewStallThreshold is _review_stall_threshold_minutes:
// review.stall_threshold_minutes from .kittify/config.yaml, default 30.
func reviewStallThreshold(root string) int {
	rv := mission.ProjectConfig(root).GetObject("review")
	if rv == nil {
		return 30
	}
	v, ok := rv.Get("stall_threshold_minutes")
	if !ok {
		return 30
	}
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	default:
		if n, err := strconv.Atoi(fmt.Sprint(t)); err == nil {
			return n
		}
	}
	return 30
}

// applyHICMarkers is _get_hic_marker over the rows: a WP whose frontmatter
// agent_profile resolves to a sentinel profile (human-in-charge) is shown
// with a "👤 " prefix. The catalog is only loaded when a row names a profile.
func applyHICMarkers(root string, rows []*boardRow) {
	var cat *doctrine.ProfileCatalog
	for _, r := range rows {
		v, ok := r.obj.Get("agent_profile")
		id, isStr := v.(string)
		if !ok || !isStr || id == "" {
			continue
		}
		if cat == nil {
			cat = doctrine.LoadProfileCatalog(root)
		}
		if p := cat.ByID[id]; p != nil {
			if sv, ok := p.Data.Get("sentinel"); ok && sv == true {
				r.marker = "👤 "
			}
		}
	}
}
