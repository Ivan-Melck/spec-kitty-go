package agentcore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
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
	consolePrint(ctx.Stdout, fmt.Sprintf("Materialized %s: %d events -> %d WPs", slug, snap.EventCount, snap.WorkPackages.Len()))
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
		consolePrint(ctx.Stdout, "No tasks found"+in)
		return nil
	}
	consolePrint(ctx.Stdout, "Tasks"+in+":\n")
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
		consolePrint(ctx.Stdout, "Error: "+msg)
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
}

func runTasksStatus(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		return statusError(ctx, jsonMode, "Not in a spec-kitty project")
	}
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		handle = soleActiveMission(root)
	}
	if handle == "" {
		msg := "--mission <slug> is required"
		if jsonMode {
			return statusSelectorError(ctx, "mission_required", msg, 1, nil)
		}
		consolePrint(ctx.Stdout, "Error: "+msg)
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
	m := newMissionCtx(root, slug)
	if _, err := m.banner(ctx, jsonMode); err != nil {
		return statusError(ctx, jsonMode, err.Error())
	}
	readDir := m.ReadDir()
	if _, err := os.Stat(readDir); err != nil {
		return statusError(ctx, jsonMode, "Mission directory not found: "+readDir)
	}
	tasksDir := m.TasksDir()
	if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
		return statusError(ctx, jsonMode, "Tasks directory not found: "+tasksDir)
	}
	var transitions *status.Snapshot
	full := &status.Snapshot{WorkPackages: &jsonx.Object{}}
	if stream, err := status.ReadStream(readDir, ""); err == nil {
		if len(stream.Transitions) > 0 {
			transitions = status.Reduce(stream.Transitions, nil, true)
		}
		full = status.Reduce(stream.Transitions, stream.Annotations, true)
	}
	manifest, _ := lanes.Read(m.PrimaryDir())
	var rows []*boardRow
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
		rows = append(rows, &boardRow{obj: o, id: id, title: w.String("title"), lane: lane, deps: w.Dependencies()})
		_ = key
	}
	// staleness for in_progress rows (no lane worktree → fresh)
	for _, r := range rows {
		if r.lane != string(status.InProgress) {
			continue
		}
		wtExists := false
		if manifest != nil {
			if l := manifest.LaneForWP(r.id); l != nil && l.LaneID != lanes.PlanningLaneID {
				if st, err := os.Stat(lanes.WorktreePath(root, slug, l.LaneID)); err == nil && st.IsDir() {
					wtExists = true
				}
			}
		}
		r.obj.Set("stale", jsonx.NewObject("status", "fresh", "reason", nil, "minutes_since_commit", nil, "last_commit_time", nil))
		r.obj.Set("is_stale", false)
		r.obj.Set("minutes_since_commit", nil)
		r.obj.Set("worktree_exists", wtExists)
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
		res.Set("stale_wps", 0)
		res.Set("stale_verdicts", []any{})
		res.Set("stalled_wps", []any{})
		res.Set("auto_commit", autoCommit)
		printJSONPretty(ctx.Stdout, res)
		return nil
	}
	if len(rows) == 0 {
		consolePrint(ctx.Stdout, "No work packages found in "+tasksDir)
		return nil
	}
	pctText := "0"
	if transitions != nil {
		pctText = pyFloat(progressF)
	}
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

func boxLine(left, fill, mid, right string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		if i > 0 {
			b.WriteString(mid)
		}
		b.WriteString(strings.Repeat(fill, w+2))
	}
	b.WriteString(right)
	return b.String()
}

func boxRow(sep string, cells []string, widths []int) string {
	var b strings.Builder
	b.WriteString(sep)
	for i, c := range cells {
		b.WriteString(" " + padRight(c, widths[i]) + " " + sep)
	}
	return b.String()
}

func panelLines(width int, title string, body []string) []string {
	inner := width - 2
	top := "╭" + strings.Repeat("─", inner) + "╮"
	if title != "" {
		t := " " + title + " "
		left := (inner - cellWidth(t)) / 2
		top = "╭" + strings.Repeat("─", left) + t + strings.Repeat("─", inner-left-cellWidth(t)) + "╮"
	}
	out := []string{top}
	for _, l := range body {
		out = append(out, "│ "+padRight(l, inner-2)+" │")
	}
	return append(out, "╰"+strings.Repeat("─", inner)+"╯")
}

func renderBoard(w io.Writer, slug string, rows []*boardRow, byLane map[string][]*boardRow, done, total int, donePct float64, pctText string, pct float64, autoCommit bool) {
	width := cli.Width()
	fmt.Fprintln(w)
	for _, l := range panelLines(width, "", []string{"📊 Work Package Status: " + slug}) {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintf(w, "Done progress: %d/%d (%s%%)\n", done, total, pyFloat(donePct))
	fmt.Fprintf(w, "Weighted readiness: %s%%\n", pctText)
	filled := floorInt(40 * pct / 100)
	fmt.Fprintln(w, strings.Repeat("█", filled)+strings.Repeat("░", 40-filled))
	fmt.Fprintln(w)

	cell := func(r *boardRow, suffix string) []string {
		t := r.title
		if len([]rune(t)) > 22 {
			t = truncateRunes(t, 22) + "..."
		}
		return []string{r.id + suffix, t}
	}
	type col struct{ cells [][]string }
	cols := make([]col, 5)
	for _, r := range byLane["planned"] {
		cols[0].cells = append(cols[0].cells, cell(r, ""))
	}
	doing := 0
	for _, r := range byLane["claimed"] {
		cols[1].cells = append(cols[1].cells, cell(r, " (claimed)"))
		doing++
	}
	for _, r := range byLane["in_progress"] {
		cols[1].cells = append(cols[1].cells, cell(r, ""))
		doing++
	}
	for _, r := range byLane["in_review"] {
		cols[1].cells = append(cols[1].cells, cell(r, " (review)"))
		doing++
	}
	for _, r := range byLane["for_review"] {
		cols[2].cells = append(cols[2].cells, cell(r, ""))
	}
	for _, r := range byLane["approved"] {
		cols[3].cells = append(cols[3].cells, cell(r, ""))
	}
	for _, r := range byLane["done"] {
		cols[4].cells = append(cols[4].cells, cell(r, ""))
	}
	widths := []int{25, 25, 25, 25, 25}
	tableWidth := 5*27 + 6
	fmt.Fprintln(w, center("Kanban Board", tableWidth))
	fmt.Fprintln(w, boxLine("┏", "━", "┳", "┓", widths))
	fmt.Fprintln(w, boxRow("┃", []string{"📋 Planned", "🔄 Doing", "👀 For Review", "👍 Approved", "✅ Done"}, widths))
	fmt.Fprintln(w, boxLine("┡", "━", "╇", "┩", widths))
	maxRows := 0
	for _, c := range cols {
		if len(c.cells) > maxRows {
			maxRows = len(c.cells)
		}
	}
	for i := 0; i < maxRows; i++ {
		for line := 0; line < 2; line++ {
			var cells []string
			for _, c := range cols {
				if i < len(c.cells) {
					cells = append(cells, c.cells[i][line])
				} else {
					cells = append(cells, "")
				}
			}
			fmt.Fprintln(w, boxRow("│", cells, widths))
		}
	}
	fmt.Fprintln(w, boxRow("│", []string{
		fmt.Sprintf("%d WPs", len(byLane["planned"])), fmt.Sprintf("%d WPs", doing),
		fmt.Sprintf("%d WPs", len(byLane["for_review"])), fmt.Sprintf("%d WPs", len(byLane["approved"])),
		fmt.Sprintf("%d WPs", len(byLane["done"]))}, widths))
	fmt.Fprintln(w, boxLine("└", "─", "┴", "┘", widths))
	fmt.Fprintln(w)

	bullet := func(r *boardRow) string { return fmt.Sprintf("  • %s - %s", r.id, r.title) }
	if l := byLane["for_review"]; len(l) > 0 {
		fmt.Fprintln(w, "👀 Ready for Review:")
		for _, r := range l {
			consolePrint(w, bullet(r))
		}
		fmt.Fprintln(w)
	}
	if l := byLane["approved"]; len(l) > 0 {
		fmt.Fprintln(w, "👍 Approved (merge when all WPs approved):")
		for _, r := range l {
			consolePrint(w, bullet(r))
		}
		fmt.Fprintln(w, "   Approved WPs stay here until feature merge. Dependents can start immediately.")
		fmt.Fprintln(w)
	}
	if l := byLane["claimed"]; len(l) > 0 {
		fmt.Fprintln(w, "🔄 Claimed (shown in Doing column):")
		for _, r := range l {
			agent, _ := r.obj.Get("agent")
			consolePrint(w, fmt.Sprintf("%s (agent: %v)", bullet(r), agent))
		}
		fmt.Fprintln(w)
	}
	if l := byLane["in_progress"]; len(l) > 0 {
		fmt.Fprintln(w, "🔄 In Progress:")
		for _, r := range l {
			consolePrint(w, bullet(r))
		}
		fmt.Fprintln(w)
	}
	if l := byLane["in_review"]; len(l) > 0 {
		fmt.Fprintln(w, "🔍 In Review (shown in Doing column):")
		for _, r := range l {
			consolePrint(w, bullet(r))
		}
		fmt.Fprintln(w)
	}
	if l := byLane["planned"]; len(l) > 0 {
		fmt.Fprintln(w, "📋 Next Up (Planned):")
		for i, r := range l {
			if i >= 3 {
				break
			}
			consolePrint(w, bullet(r))
		}
		if len(l) > 3 {
			fmt.Fprintf(w, "  ... and %d more\n", len(l)-3)
		}
		fmt.Fprintln(w)
	}
	inProg := len(byLane["claimed"]) + len(byLane["in_progress"]) + len(byLane["in_review"]) + len(byLane["for_review"])
	ac := "disabled"
	if autoCommit {
		ac = "enabled"
	}
	grid := [][2]string{
		{"Total WPs:", fmt.Sprint(total)},
		{"Completed:", fmt.Sprintf("%d (%s%%)", done, pyFloat(donePct))},
		{"Weighted readiness:", pctText + "%"},
		{"In Progress:", fmt.Sprint(inProg)},
		{"Planned:", fmt.Sprint(len(byLane["planned"]))},
		{"Auto-commit:", ac},
	}
	var body []string
	for _, g := range grid {
		body = append(body, padRight(g[0], 21)+g[1])
	}
	for _, l := range panelLines(width, "Summary", body) {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w, "▶ Next action:")
	consolePrint(w, "  spec-kitty next --agent <your-name> --mission "+slug)
	fmt.Fprintln(w, "  This command tells you exactly what to do next based on the dependency graph.")
	fmt.Fprintln(w)
}
