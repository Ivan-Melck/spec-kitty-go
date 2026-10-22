package agentcore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

const markStatusLong = "Record subtask status for one or more tasks.\n\n" +
	"Accepts multiple task IDs separated by spaces. Status is recorded in the\nmission event log. Use --wp when the same ID appears in more than one WP.\n\n" +
	"Examples:\n    # Single task:\n    spec-kitty agent tasks mark-status T001 --status done\n\n" +
	"    # Multiple tasks (space-separated):\n    spec-kitty agent tasks mark-status T001 T002 T003 --status done\n\n" +
	"    # Many tasks at once:\n    spec-kitty agent tasks mark-status T040 T041 T042 T043 T044 T045 --status done --mission 001-my-feature\n\n" +
	"    # With JSON output:\n    spec-kitty agent tasks mark-status T001 T002 --status done --json\n\n" +
	"    # When IDs repeat across work packages:\n    spec-kitty agent tasks mark-status T001 --wp WP02 --status skipped"

func init() {
	addCommands(func(_, tasksG, _ *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "mark-status",
			Short: "Record subtask status for one or more tasks.",
			Long:  markStatusLong,
			Args:  []*cli.Arg{{Name: "task_ids", Metavar: "{task_ids}", Required: true, Variadic: true, Help: "Task ID(s) - space-separated (e.g., T001 T002 T003)"}},
			Flags: []*cli.Flag{
				{Name: "status", Kind: cli.String, Required: true, Metavar: "<str>", Help: "Status: done/pending/skipped/not_applicable"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "wp", Kind: cli.String, Metavar: "<str>", Help: "Work package containing the subtasks (e.g., WP02)"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Explicit single-branch checkout root."},
				{Name: "auto-commit", Kind: cli.Bool, Negatable: true, Help: "Automatically commit status events when supported (default: from project config)"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runMarkStatus,
		})
		tasksG.Add(&cli.Command{
			Name:  "add-history",
			Short: "Record a history note in the WP status event log.",
			Long:  "Record a history note in the WP status event log.\n\nExamples:\nspec-kitty agent tasks add-history WP01 --note \"Completed implementation\" --json",
			Args:  []*cli.Arg{{Name: "task_id", Metavar: "{task_id}", Required: true, Help: "Task ID (e.g., WP01)"}},
			Flags: []*cli.Flag{
				{Name: "note", Kind: cli.String, Required: true, Metavar: "<str>", Help: "History note"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name"},
				{Name: "shell-pid", Kind: cli.String, Metavar: "<str>", Help: "Shell PID"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runAddHistory,
		})
	})
}

// emitAnnotation is emit_inner_state_changed: append one annotation and
// re-materialize status.json in dir.
func emitAnnotation(dir, wpID string, delta status.Delta, actor string) (*status.Annotation, error) {
	if delta.IsEmpty() {
		return nil, fmt.Errorf("InnerStateChanged requires a non-empty delta")
	}
	a := &status.Annotation{EventID: ulid.New(), WPID: wpID, At: clock.NowUTCISO(), Actor: status.StrActor(actor), Delta: delta}
	if err := status.AppendEvents(dir, a); err != nil {
		return nil, err
	}
	if _, err := status.Materialize(dir); err != nil {
		a.MaterializeFailed = true
	}
	return a, nil
}

// warnLog renders a Python logging WARNING through rich's log handler
// (level column + message wrapped and padded to the console width).
func warnLog(w io.Writer, msg string) {
	width := cli.Width()
	inner := width - 9
	for i, l := range rich.WrapWords(msg, inner) {
		prefix := "         "
		if i == 0 {
			prefix = "WARNING  "
		}
		fmt.Fprintln(w, prefix+padRight(l, inner))
	}
}

func materializeWarning(ctx *cli.Context, jsonMode bool, a *status.Annotation) {
	if a != nil && a.MaterializeFailed && !jsonMode {
		warnLog(ctx.Stderr, fmt.Sprintf("Materialization failed after annotation %s was persisted; run 'status materialize' to recover", a.EventID))
	}
}

func utcSecondStamp() string { return clock.Now().UTC().Format("2006-01-02T15:04:05Z") }

// locateWorkPackage is task_utils.locate_work_package (primary tasks/).
func locateWorkPackage(m *missionCtx, wpID string) (*wp.WP, error) {
	tasksRoot := m.TasksDir()
	if st, err := os.Stat(tasksRoot); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("Mission '%s' has no tasks directory at %s.", m.Slug, tasksRoot)
	}
	matches := wp.Match(m.PrimaryDir(), wpID)
	if len(matches) == 0 {
		return nil, fmt.Errorf("Work package '%s' not found under kitty-specs/%s/tasks (resolved: %s under %s).", wpID, m.Slug, tasksRoot, m.Root)
	}
	if len(matches) > 1 {
		var rel []string
		for _, p := range matches {
			r, _ := filepath.Rel(m.Root, p)
			rel = append(rel, r)
		}
		return nil, fmt.Errorf("Multiple files matched '%s'. Refine the ID or clean duplicates:\n%s", wpID, strings.Join(rel, "\n"))
	}
	return wp.Read(matches[0])
}

func runAddHistory(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	if _, err := m.banner(ctx, jsonMode); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	taskID := ctx.Arg("task_id")
	if _, err := locateWorkPackage(m, taskID); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	snap, _ := status.ReduceDir(m.ReadDir())
	state := snap.WP(taskID)
	agent := ctx.String("agent")
	if agent == "" && state != nil {
		agent = state.GetString("agent")
	}
	if agent == "" {
		agent = "unknown"
	}
	pid := ctx.String("shell-pid")
	if pid == "" && state != nil {
		if v, ok := state.Get("shell_pid"); ok && v != nil && v != "" {
			pid = status.PyStr(v)
		}
	}
	shellPart := ""
	if pid != "" {
		shellPart = "shell_pid=" + pid + " – "
	}
	entry := fmt.Sprintf("- %s – %s – %s%s", utcSecondStamp(), agent, shellPart, ctx.String("note"))
	dir, err := m.Surface.WriteDir()
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	a, err := emitAnnotation(dir, taskID, status.Delta{Note: &entry}, agent)
	materializeWarning(ctx, jsonMode, a)
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("result", "success", "task_id", taskID, "note", ctx.String("note")))
	} else {
		consolePrint(ctx.Stdout, "[green]✓[/green] Added history entry to "+taskID)
	}
	return nil
}

// --- mark-status -----------------------------------------------------------

type taskResult struct {
	id, outcome, format, message string
}

var (
	qualifiedTaskRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*[/:]([A-Za-z]+\d+)$`)
	inlineSubtaskRe = regexp.MustCompile(`(?i)(?:Subtasks|\*\*Subtasks\*\*):\s*((?:T|WP)\d+(?:\s*,\s*(?:T|WP)\d+)*)`)
	wpHeadingRe     = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+?)\s*$`)
	wpIDTitleRe     = regexp.MustCompile(`(?i)^(?:Work Package\s+)?(WP\d+)(?:\b|:)`)
	separatorRowRe  = regexp.MustCompile(`^\s*\|[\s\-:]+\|`)
	bareWPRe        = regexp.MustCompile(`(?i)^WP\d+$`)
	wpWordRe        = regexp.MustCompile(`(?i)\b(WP\d+)\b`)
)

func normalizeTaskID(raw string) string {
	c := strings.TrimSpace(raw)
	if m := qualifiedTaskRe.FindStringSubmatch(c); m != nil {
		return strings.ToUpper(m[1])
	}
	return c
}

func checkboxRe(id string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)-\s*\[[ x]\]\s*` + regexp.QuoteMeta(id) + `\b`)
}

func isPipeRow(line, id string) bool {
	if separatorRowRe.MatchString(line) {
		return false
	}
	return regexp.MustCompile(`\|\s*` + regexp.QuoteMeta(id) + `\s*\|`).MatchString(line)
}

func pipeHeader(lines []string, idx int) map[string]int {
	for i := idx - 1; i >= 0; i-- {
		c := strings.TrimSpace(lines[i])
		if regexp.MustCompile(`^\|[\s\-:]+\|`).MatchString(c) {
			continue
		}
		if strings.Contains(c, "|") {
			cells := strings.Split(c, "|")
			out := map[string]int{}
			if len(cells) >= 2 {
				for j, x := range cells[1 : len(cells)-1] {
					if n := strings.ToLower(strings.TrimSpace(x)); n != "" {
						out[n] = j
					}
				}
			}
			return out
		}
		break
	}
	return map[string]int{}
}

func headingWP(line string) string {
	m := wpHeadingRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	title := strings.TrimSpace(m[1])
	if w := wpIDTitleRe.FindStringSubmatch(title); w != nil {
		return strings.ToUpper(w[1])
	}
	return ""
}

func pipeRowWP(line string, header map[string]int) string {
	parts := strings.Split(line, "|")
	var cells []string
	if len(parts) >= 2 {
		for _, c := range parts[1 : len(parts)-1] {
			cells = append(cells, strings.TrimSpace(c))
		}
	}
	for _, name := range []string{"wp", "work package", "work_package", "work package id", "work_package_id"} {
		if i, ok := header[name]; ok && i < len(cells) {
			if m := wpWordRe.FindStringSubmatch(strings.ToUpper(cells[i])); m != nil {
				return m[1]
			}
		}
	}
	for _, c := range cells {
		if bareWPRe.MatchString(strings.ToUpper(c)) {
			return strings.ToUpper(c)
		}
	}
	return ""
}

// historyOwners yields the owning WP of every structured tasks.md row naming id.
func historyOwners(content, id string) []string {
	norm := strings.ToUpper(id)
	cur := ""
	lines := strings.Split(content, "\n")
	cb := checkboxRe(norm)
	var out []string
	for i, line := range lines {
		if h := headingWP(line); h != "" {
			cur = h
		}
		switch {
		case isPipeRow(line, norm):
			w := pipeRowWP(line, pipeHeader(lines, i))
			if w == "" {
				w = cur
			}
			out = append(out, w)
		case cb.MatchString(line):
			w := cur
			if w == "" {
				if m := wpWordRe.FindStringSubmatch(line); m != nil {
					w = strings.ToUpper(m[1])
				}
			}
			out = append(out, w)
		default:
			if m := inlineSubtaskRe.FindStringSubmatch(line); m != nil {
				for _, v := range strings.Split(m[1], ",") {
					if strings.ToUpper(strings.TrimSpace(v)) == norm {
						out = append(out, cur)
						break
					}
				}
			}
		}
	}
	return out
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// rosterOwnerMap maps each WP to its authored subtasks roster.
func rosterOwnerMap(primaryDir string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, p := range wp.Files(primaryDir) {
		id := strings.ToUpper(wp.IDFromFilename(p))
		w, err := wp.Read(p)
		if err != nil {
			return nil, fmt.Errorf("%s: Cannot resolve subtask roster for %s: %s is unreadable", filepath.Base(p), id, filepath.Base(p))
		}
		set := map[string]bool{}
		for _, s := range w.Subtasks() {
			set[strings.ToUpper(s)] = true
		}
		out[id] = set
	}
	return out, nil
}

func rosterOwners(m map[string]map[string]bool, id string) []string {
	var out []string
	for w, set := range m {
		if set[strings.ToUpper(id)] {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

func requestedSubtaskValue(s string) string {
	switch s {
	case "skipped", "not_applicable":
		return "skipped"
	case "done":
		return "done"
	}
	return "planned"
}

func markStatusPayload(results []taskResult) *jsonx.Object {
	list := []any{}
	u, a, n := 0, 0, 0
	for _, r := range results {
		var f any
		if r.format != "" {
			f = r.format
		}
		list = append(list, jsonx.NewObject("id", r.id, "outcome", r.outcome, "format", f, "message", r.message))
		switch r.outcome {
		case "updated":
			u++
		case "already_satisfied":
			a++
		case "not_found":
			n++
		}
	}
	return jsonx.NewObject("results", list, "summary", jsonx.NewObject("updated", u, "already_satisfied", a, "not_found", n))
}

func runMarkStatus(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	st := ctx.String("status")
	switch st {
	case "done", "pending", "skipped", "not_applicable":
	default:
		outputError(ctx, jsonMode, fmt.Sprintf("Invalid status '%s'. Must be 'done', 'pending', 'skipped', or 'not_applicable'.", st))
		return cli.Exit(1)
	}
	wpFlag := ""
	if ctx.IsSet("wp") {
		v := strings.ToUpper(strings.TrimSpace(ctx.String("wp")))
		if !regexp.MustCompile(`^WP\d+$`).MatchString(v) {
			outputError(ctx, jsonMode, fmt.Sprintf("Invalid --wp value: Invalid work package id %s", fmtRepr(ctx.String("wp"))))
			return cli.Exit(1)
		}
		wpFlag = v
	}
	var ids []string
	for _, id := range ctx.ArgList("task_ids") {
		ids = append(ids, normalizeTaskID(id))
	}
	if ctx.IsSet("owned-checkout") {
		outputError(ctx, jsonMode, "--owned-checkout is not supported by this build.")
		return cli.Exit(1)
	}
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	if _, err := m.banner(ctx, jsonMode); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	statusDir, err := m.Surface.WriteDir()
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	tasksMD := filepath.Join(m.PrimaryDir(), "tasks.md")
	b, err := os.ReadFile(tasksMD)
	if err != nil {
		outputError(ctx, jsonMode, "tasks.md not found: "+tasksMD)
		return cli.Exit(1)
	}
	content := string(b)
	ownerMap, err := rosterOwnerMap(m.PrimaryDir())
	if err != nil {
		outputError(ctx, jsonMode, "Cannot determine which work package owns the subtasks: "+err.Error())
		return cli.Exit(1)
	}
	taskWPs := map[string]string{}
	for _, id := range ids {
		if bareWPRe.MatchString(id) {
			continue
		}
		owners := rosterOwners(ownerMap, id)
		if len(owners) == 0 {
			owners = sortedUnique(historyOwners(content, id))
		}
		if wpFlag == "" && len(owners) > 1 {
			outputError(ctx, jsonMode, fmt.Sprintf("%s is a subtask of %s in Mission %s; pass --wp to select one.", id, strings.Join(owners, ", "), m.Slug))
			return cli.Exit(1)
		}
		if wpFlag != "" && !contains(owners, wpFlag) {
			head := fmt.Sprintf("%s is not a subtask of %s in Mission %s", id, wpFlag, m.Slug)
			if len(owners) == 0 {
				outputError(ctx, jsonMode, head+"; no work package lists it.")
			} else {
				var rerun []string
				for _, o := range owners {
					rerun = append(rerun, "--wp "+o)
				}
				outputError(ctx, jsonMode, fmt.Sprintf("%s (owners: %s). Re-run with %s.", head, strings.Join(owners, ", "), strings.Join(rerun, " or ")))
			}
			return cli.Exit(1)
		}
		if len(owners) > 0 {
			if wpFlag != "" {
				taskWPs[id] = wpFlag
			} else {
				taskWPs[id] = owners[0]
			}
		}
	}
	lines := strings.Split(content, "\n")
	snap, _ := status.ReduceDir(statusDir)
	requested := requestedSubtaskValue(st)
	var results []taskResult
	for _, id := range ids {
		r := resolveTaskID(id, st, lines, content, m.PrimaryDir())
		if w, ok := taskWPs[id]; ok && r.outcome == "updated" {
			cur := ""
			if s := snap.WP(w); s != nil {
				if sub := s.GetObject("subtasks"); sub != nil {
					cur = sub.GetString(id)
				}
			}
			if cur == requested {
				r = taskResult{id, "already_satisfied", r.format, fmt.Sprintf("%s in %s is already %s; no event written.", id, w, requested)}
			} else {
				r = taskResult{id, "updated", r.format, fmt.Sprintf("Recorded %s for %s in %s.", requested, id, w)}
			}
		}
		results = append(results, r)
	}
	var updated, notFound, resolved []string
	for _, r := range results {
		switch r.outcome {
		case "updated":
			updated = append(updated, r.id)
		case "not_found":
			notFound = append(notFound, r.id)
		}
		if r.outcome != "not_found" {
			resolved = append(resolved, r.id)
		}
	}
	if len(resolved) == 0 {
		if jsonMode {
			printJSON(ctx.Stdout, markStatusPayload(results))
		} else {
			wpMsg := false
			var msgs []string
			for _, r := range results {
				if r.format == "wp_id" {
					wpMsg = true
				}
				if r.message != "" {
					msgs = append(msgs, r.message)
				}
			}
			if wpMsg {
				outputError(ctx, false, strings.Join(msgs, "; "))
			} else {
				outputError(ctx, false, "No task IDs found in tasks.md: "+strings.Join(notFound, ", "))
			}
		}
		return cli.Exit(1)
	}
	// group by owning WP (one annotation per WP, first-seen order)
	var order []string
	byWP := map[string][]string{}
	var unresolved []string
	for _, id := range updated {
		w := taskWPs[id]
		if w == "" {
			if o := historyOwners(content, id); len(o) > 0 && o[0] != "" {
				w = o[0]
			}
		}
		if w == "" {
			if o := rosterOwners(ownerMap, id); len(o) > 0 {
				w = o[0]
			}
		}
		if w == "" {
			unresolved = append(unresolved, id)
			continue
		}
		if _, ok := byWP[w]; !ok {
			order = append(order, w)
		}
		byWP[w] = append(byWP[w], id)
	}
	if len(unresolved) > 0 {
		outputError(ctx, jsonMode, "Could not resolve owning work package for subtask event: "+strings.Join(unresolved, ", "))
		return cli.Exit(1)
	}
	for _, w := range order {
		sub := &jsonx.Object{}
		for _, id := range byWP[w] {
			sub.Set(id, requested)
		}
		a, err := emitAnnotation(statusDir, w, status.Delta{Subtasks: sub}, "user")
		materializeWarning(ctx, jsonMode, a)
		if err != nil {
			outputError(ctx, jsonMode, err.Error())
			return cli.Exit(1)
		}
	}
	if jsonMode {
		printJSON(ctx.Stdout, markStatusPayload(results))
		return nil
	}
	if len(notFound) > 0 {
		consolePrint(ctx.Stdout, "[yellow]Warning:[/yellow] Not found: "+strings.Join(notFound, ", "))
	}
	switch {
	case len(updated) == 1:
		consolePrint(ctx.Stdout, fmt.Sprintf("[green]✓[/green] Marked %s as %s", updated[0], st))
	case len(updated) == 0:
		consolePrint(ctx.Stdout, "[green]✓[/green] Requested status already satisfied for: "+strings.Join(resolved, ", "))
	default:
		consolePrint(ctx.Stdout, fmt.Sprintf("[green]✓[/green] Marked %d subtasks as %s: %s", len(updated), st, strings.Join(updated, ", ")))
	}
	return nil
}

func fmtRepr(s string) string { return "'" + strings.ReplaceAll(s, "'", `\'`) + "'" }

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func resolveTaskID(id, st string, lines []string, content, primaryDir string) taskResult {
	cb := checkboxRe(id)
	for _, l := range lines {
		if cb.MatchString(l) {
			return taskResult{id, "updated", "checkbox", fmt.Sprintf("Marked %s as %s (checkbox row updated).", id, st)}
		}
	}
	for _, l := range lines {
		if isPipeRow(l, id) {
			return taskResult{id, "updated", "pipe_table", fmt.Sprintf("Marked %s as %s (pipe-table row updated).", id, st)}
		}
	}
	norm := strings.ToUpper(id)
	for _, m := range inlineSubtaskRe.FindAllStringSubmatch(content, -1) {
		for _, v := range strings.Split(m[1], ",") {
			if strings.ToUpper(strings.TrimSpace(v)) == norm {
				return taskResult{id, "updated", "inline_subtasks", fmt.Sprintf("Recorded inline Subtasks reference %s as %s (event-sourced).", id, st)}
			}
		}
	}
	if bareWPRe.MatchString(id) {
		u := strings.ToUpper(id)
		return taskResult{u, "not_found", "wp_id", fmt.Sprintf("%s: mark-status does not change work-package lanes. Use `spec-kitty agent tasks move-task <WP_ID> --to <lane>`.", u)}
	}
	for _, p := range wp.Files(primaryDir) {
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		for _, s := range w.Subtasks() {
			if strings.ToUpper(s) == norm {
				return taskResult{id, "updated", "authored_roster", fmt.Sprintf("%s resolved from %s's authored subtasks roster.", id, wp.IDFromFilename(p))}
			}
		}
	}
	return taskResult{id, "not_found", "", id + " was not found in any supported task format."}
}
