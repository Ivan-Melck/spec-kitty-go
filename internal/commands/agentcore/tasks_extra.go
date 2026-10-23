package agentcore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

func init() {
	addCommands(func(_, tasksG, _ *cli.Command) {
		tasksG.Add(&cli.Command{
			Name:  "list-dependents",
			Short: "Find all WPs that depend on a given WP (downstream dependents).",
			Long: "Find all WPs that depend on a given WP (downstream dependents).\n\n" +
				"This answers \"who depends on me?\" - useful when reviewing a WP to understand\nthe impact of requested changes on downstream work packages.\n\n" +
				"Also shows what the WP itself depends on (upstream dependencies).\n\n" +
				"Examples:\n    spec-kitty agent tasks list-dependents WP13\n    spec-kitty agent tasks list-dependents WP01 --mission 001-my-feature --json",
			Args: []*cli.Arg{{Name: "wp_id", Metavar: "{wp_id}", Required: true, Help: "Work package ID (e.g., WP01)"}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runListDependents,
		})
		tasksG.Add(&cli.Command{
			Name:  "validate-workflow",
			Short: "Validate task metadata structure and workflow consistency.",
			Long:  "Validate task metadata structure and workflow consistency.\n\nExamples:\nspec-kitty agent tasks validate-workflow WP01 --json",
			Args:  []*cli.Arg{{Name: "task_id", Metavar: "{task_id}", Required: true, Help: "Task ID (e.g., WP01)"}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runValidateWorkflow,
		})
		tasksG.Add(&cli.Command{
			Name:  "check-terminability",
			Short: "Advisory scan for work packages that can only be terminated post-integration.",
			Long: "Advisory scan for work packages that can only be terminated post-integration.\n\n" +
				"Warns when a WP's acceptance criteria contain a post-integration trigger\nphrase (the #3590 trap) — content that cannot be verified in the WP's own\n" +
				"diff. This is **advisory only**: it never refuses or fails authoring\n(FR-008) — it exits 0 even when warnings fire. It does not touch the\nfinalize / lane-compute path (C-005).\n\n" +
				"Examples:\n    spec-kitty agent tasks check-terminability --mission my-mission --json",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runCheckTerminability,
		})
	})
}

// splitFrontmatterText is task_utils.split_frontmatter: (frontmatter, body).
func splitFrontmatterText(text string) (string, string) {
	n := strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(n, "---\n") {
		return "", n
	}
	i := strings.Index(n[4:], "\n---")
	if i < 0 {
		return "", n
	}
	front := n[4 : 4+i]
	tail := strings.TrimLeft(n[4+i+4:], "\n")
	return front, tail
}

// extractScalar is task_utils.extract_scalar (a line regex over the raw
// frontmatter text; returns ok=false for None).
func extractScalar(front, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^(` + regexp.QuoteMeta(key) + `:\s*)("[^\n]*?"|'[^\n]*?'|[^#\n]*)(.*)$`)
	m := re.FindStringSubmatch(front)
	if m == nil {
		return "", false
	}
	raw := strings.TrimSpace(m[2])
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		return raw[1 : len(raw)-1], true
	}
	raw = strings.TrimSpace(raw)
	return raw, raw != ""
}

func readWPFront(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	f, body := splitFrontmatterText(strings.TrimPrefix(string(b), "\ufeff"))
	return f, body, nil
}

func (m *missionCtx) tasksPreamble(ctx *cli.Context, jsonMode bool) error {
	if _, err := m.banner(ctx, jsonMode); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	return nil
}

var wp2Re = regexp.MustCompile(`^(WP\d{2})`)

// reraiseExit mirrors list-dependents' catch-all `except Exception`: a
// typer.Exit(code) raised inside the try is re-reported as str(exc) (the
// code) and the command exits 1.
func reraiseExit(ctx *cli.Context, jsonMode bool, err error) error {
	var ee *cli.ExitError
	if errors.As(err, &ee) && ee.Msg == "" {
		outputError(ctx, jsonMode, strconv.Itoa(ee.Code))
		return cli.Exit(1)
	}
	return err
}

func runListDependents(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return reraiseExit(ctx, jsonMode, err)
	}
	if _, err := m.banner(ctx, jsonMode); err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	dir := m.PrimaryDir()
	if _, err := os.Stat(dir); err != nil {
		outputError(ctx, jsonMode, "Mission directory not found: "+dir)
		return reraiseExit(ctx, jsonMode, cli.Exit(1))
	}
	wpID := ctx.Arg("wp_id")
	files, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	sort.Strings(files)
	var order []string
	graph := map[string][]string{}
	for _, f := range files {
		fid := wp2Re.FindString(filepath.Base(f))
		if fid == "" {
			continue
		}
		w, err := wp.Read(f)
		if err != nil {
			outputError(ctx, jsonMode, err.Error())
			return cli.Exit(1)
		}
		front := strings.TrimSpace(w.Doc.Meta.GetString("work_package_id"))
		if front != "" && front != fid {
			outputError(ctx, jsonMode, fmt.Sprintf("WP ID mismatch: filename %s vs frontmatter %s in %s", fid, front, f))
			return cli.Exit(1)
		}
		id := fid
		if front != "" {
			id = front
		}
		if _, ok := graph[id]; !ok {
			order = append(order, id)
		}
		graph[id] = w.Dependencies()
	}
	dependents := []string{}
	for _, id := range order {
		for _, d := range graph[id] {
			if d == wpID {
				dependents = append(dependents, id)
			}
		}
	}
	own := []string{}
	if w, err := locateWorkPackage(m, wpID); err == nil {
		if front, _, err := readWPFront(w.Path); err == nil {
			if v, ok := extractScalar(front, "dependencies"); ok {
				own = []string{v}
			}
		}
	}
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("wp_id", wpID, "depends_on", own, "dependents", dependents))
		return nil
	}
	dep, by := "[dim](none)[/dim]", "[dim](none)[/dim]"
	if len(own) > 0 {
		dep = strings.Join(own, ", ")
	}
	if len(dependents) > 0 {
		by = strings.Join(dependents, ", ")
	}
	consolePrint(ctx.Stdout, fmt.Sprintf("\n[bold]%s Dependency Info:[/bold]", wpID))
	consolePrint(ctx.Stdout, "  Depends on: "+dep)
	consolePrint(ctx.Stdout, "  Depended on by: "+by)
	if len(dependents) > 0 {
		consolePrint(ctx.Stdout, fmt.Sprintf("\n[yellow]⚠️  Changes to %s may impact: %s[/yellow]", wpID, by))
	}
	consolePrint(ctx.Stdout, "")
	return nil
}

func runValidateWorkflow(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	if err := m.tasksPreamble(ctx, jsonMode); err != nil {
		return err
	}
	taskID := ctx.Arg("task_id")
	w, err := locateWorkPackage(m, taskID)
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	front, _, err := readWPFront(w.Path)
	if err != nil {
		outputError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	errs, warns := []string{}, []string{}
	for _, f := range []string{"work_package_id", "title"} {
		if _, ok := extractScalar(front, f); !ok {
			errs = append(errs, "Missing required field: "+f)
		}
	}
	lane := string(status.Planned)
	if snap, err := status.ReduceDir(m.ReadDir()); err == nil {
		if s := snap.WP(taskID); s != nil && s.GetString("lane") != "" {
			lane = s.GetString("lane")
		}
	}
	if id, ok := extractScalar(front, "work_package_id"); ok && !strings.HasPrefix(filepath.Base(w.Path), id) {
		warns = append(warns, fmt.Sprintf("Work package ID '%s' doesn't match filename '%s'", id, filepath.Base(w.Path)))
	}
	valid := len(errs) == 0
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("valid", valid, "errors", errs, "warnings", warns, "task_id", taskID, "lane", lane))
		return nil
	}
	if valid {
		consolePrint(ctx.Stdout, fmt.Sprintf("[green]✓[/green] %s validation passed", taskID))
	} else {
		consolePrint(ctx.Stdout, fmt.Sprintf("[red]✗[/red] %s validation failed", taskID))
		for _, e := range errs {
			consolePrint(ctx.Stdout, "  [red]Error:[/red] "+e)
		}
	}
	if len(warns) > 0 {
		consolePrint(ctx.Stdout, "\n[yellow]Warnings:[/yellow]")
		for _, w := range warns {
			consolePrint(ctx.Stdout, "  [yellow]•[/yellow] "+w)
		}
	}
	return nil
}

var terminabilityTriggers = []string{"after merge", "post-merge", "once merged", "on a branch the forge will run", "in CI once enabled", "consecutive runs", "merge-blocked-when-absent"}

const terminabilityGuidance = "Re-home post-integration content to a tracked post-merge obligations document; a work package's acceptance criteria must be verifiable in its own diff."

func normalizeTrigger(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func cleanExcerpt(line string) string {
	c := strings.TrimSpace(line)
	c = strings.TrimLeft(c, " \t-*#>[]x")
	c = strings.TrimSpace(c)
	if utf8.RuneCountInString(c) > 200 {
		r := []rune(c)
		return strings.TrimRight(string(r[:199]), " \t\n\r\v\f") + "…"
	}
	return c
}

func pySplitLines(s string) []string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func runCheckTerminability(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	m, err := resolveRequiredMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	if err := m.tasksPreamble(ctx, jsonMode); err != nil {
		return err
	}
	tasks := m.TasksDir()
	if _, err := os.Stat(tasks); err != nil {
		outputError(ctx, jsonMode, "Tasks directory not found: "+tasks)
		return cli.Exit(1)
	}
	files, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	sort.Strings(files)
	var warnings []*jsonx.Object
	for _, f := range files {
		front, body, err := readWPFront(f)
		if err != nil {
			outputError(ctx, jsonMode, err.Error())
			return cli.Exit(1)
		}
		id, ok := extractScalar(front, "work_package_id")
		if !ok {
			id = strings.TrimSuffix(filepath.Base(f), ".md")
		}
		for _, line := range pySplitLines(body) {
			nl := normalizeTrigger(line)
			if nl == "" {
				continue
			}
			for _, trig := range terminabilityTriggers {
				if strings.Contains(nl, normalizeTrigger(trig)) {
					warnings = append(warnings, jsonx.NewObject("wp_id", id, "matched_phrase", trig, "criterion_excerpt", cleanExcerpt(line)))
				}
			}
		}
	}
	if jsonMode {
		list := []any{}
		for _, w := range warnings {
			list = append(list, w)
		}
		var guidance any
		if len(warnings) > 0 {
			guidance = terminabilityGuidance
		}
		printJSON(ctx.Stdout, jsonx.NewObject("mission", m.Slug, "trigger_set_version", 1, "warnings", list, "count", len(warnings), "guidance", guidance))
		return nil
	}
	if len(warnings) == 0 {
		consolePrint(ctx.Stdout, fmt.Sprintf("[green]✓[/green] No un-terminable-work warnings for %s.", m.Slug))
		return nil
	}
	consolePrint(ctx.Stdout, fmt.Sprintf("[yellow]⚠️  %d un-terminable-work warning(s) for %s (advisory — authoring is not blocked):[/yellow]\n", len(warnings), m.Slug))
	for _, w := range warnings {
		consolePrint(ctx.Stdout, fmt.Sprintf("  [bold]%s[/bold] matched [cyan]%s[/cyan]: %s", w.GetString("wp_id"), status.PyRepr(w.GetString("matched_phrase")), w.GetString("criterion_excerpt")))
	}
	consolePrint(ctx.Stdout, "\n[dim]"+terminabilityGuidance+"[/dim]")
	return nil
}
