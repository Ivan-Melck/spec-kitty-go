// Package validatetasks implements the LEGACY `spec-kitty validate-tasks`
// command: detect (and with --fix repair) WP files whose frontmatter lane
// disagrees with their tasks/<lane>/ directory.
package validatetasks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/encoding"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "validate-tasks",
			Short: "LEGACY: Validate and repair directory/frontmatter lane mismatches.",
			Long: "LEGACY: Validate and repair directory/frontmatter lane mismatches.\n\n" +
				"This command is for legacy projects that used directory-based lanes\n" +
				"(tasks/planned/, tasks/doing/, etc.). Modern projects (3.0+) use\n" +
				"flat tasks/ directories with canonical status in status.events.jsonl.\n\n" +
				"For modern projects, use `spec-kitty agent mission finalize-tasks`\n" +
				"to ensure canonical status state exists.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug to validate"},
				{Name: "fix", Kind: cli.Bool, Help: "Automatically repair metadata inconsistencies"},
				{Name: "all", Kind: cli.Bool, Help: "Check all features, not just one"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name for activity log"},
				{Name: "shell-pid", Kind: cli.String, Metavar: "<str>", Help: "Shell PID for activity log"},
			},
			Run: run,
		})
	})
}

// legacyLanes are the directory lanes, in detection order.
var legacyLanes = []string{"planned", "doing", "for_review", "done"}

// crash is an uncaught upstream exception (traceback, exit 1).
type crash struct{ msg string }

func (c *crash) Error() string { return c.msg }

// ParseFrontmatter is template.renderer.parse_frontmatter (metadata, body).
// A YAML error yields empty metadata (upstream logs a warning).
func ParseFrontmatter(content string, warn io.Writer) (*jsonx.Object, string) {
	normalized := strings.ReplaceAll(content, "\r", "")
	if !strings.HasPrefix(normalized, "---\n") {
		return &jsonx.Object{}, normalized
	}
	closing := strings.Index(normalized[4:], "\n---")
	if closing < 0 {
		return &jsonx.Object{}, normalized
	}
	closing += 4
	fmText := normalized[4:closing]
	bodyStart := closing + 4
	if bodyStart < len(normalized) && normalized[bodyStart] == '\n' {
		bodyStart++
	}
	body := normalized[bodyStart:]
	v, err := pyyaml.SafeLoad(fmText)
	if err != nil {
		if pe, ok := err.(*pyyaml.PyError); ok {
			// only yaml.YAMLError is caught upstream
			panic(&crash{pe.Type + ": " + pe.Msg})
		}
		if warn != nil {
			rich.LogWarning(warn, "YAML parse error in frontmatter (WP may vanish from dashboard): "+err.Error())
		}
		return &jsonx.Object{}, body
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return &jsonx.Object{}, body
	}
	return o, body
}

func readUTF8Sig(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return "", err
	}
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n"), nil
}

// DetectLaneMismatch is detect_lane_mismatch: (mismatch, expected, actual).
func DetectLaneMismatch(path string, warn io.Writer) (bool, string, string, error) {
	if _, err := os.Stat(path); err != nil {
		return false, "", "", nil
	}
	expected := ""
	for _, l := range legacyLanes {
		if strings.Contains(path, "/tasks/"+l+"/") {
			expected = l
			break
		}
	}
	if expected == "" {
		return false, "", "", nil
	}
	content, err := readUTF8Sig(path)
	if err != nil {
		return false, expected, "", nil
	}
	// read + parse_frontmatter run under `except Exception` upstream: a
	// non-YAMLError from the loader just means "no mismatch".
	meta, parsed := func() (m *jsonx.Object, ok bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, isCrash := r.(*crash); !isCrash {
					panic(r)
				}
				m, ok = nil, false
			}
		}()
		m, _ = ParseFrontmatter(content, warn)
		return m, true
	}()
	if !parsed {
		return false, expected, "", nil
	}
	raw, ok := meta.Get("lane")
	if !ok {
		raw = ""
	}
	s, isStr := raw.(string)
	if !isStr {
		return false, "", "", &crash{fmt.Sprintf("AttributeError: '%s' object has no attribute 'strip'", pyTypeName(raw))}
	}
	actual := strings.TrimSpace(s)
	return actual != expected, expected, actual, nil
}

func pyTypeName(v any) string {
	switch v.(type) {
	case pyyaml.Date:
		return "date"
	case pyyaml.Timestamp:
		return "datetime"
	}
	return pyjson.TypeName(v)
}

type mismatch struct{ rel, expected, actual string }

// scan is scan_all_tasks_for_mismatches.
func scan(featureDir string, warn io.Writer) ([]mismatch, error) {
	tasks := filepath.Join(featureDir, "tasks")
	if _, err := os.Stat(tasks); err != nil {
		return nil, nil
	}
	var out []mismatch
	for _, lane := range legacyLanes {
		laneDir := filepath.Join(tasks, lane)
		if _, err := os.Stat(laneDir); err != nil {
			continue
		}
		files := rglob(laneDir, func(name string) bool {
			return strings.HasPrefix(name, "WP") && strings.HasSuffix(name, ".md")
		})
		for _, f := range files {
			mm, exp, act, err := DetectLaneMismatch(f, warn)
			if err != nil {
				return nil, err
			}
			if mm {
				rel, rerr := filepath.Rel(featureDir, f)
				if rerr != nil {
					rel = f
				}
				out = append(out, mismatch{rel, exp, act})
			}
		}
	}
	return out, nil
}

// buildDocument is task_utils.build_document.
func buildDocument(frontmatter, body, padding string) string {
	doc := "---\n" + strings.TrimRight(frontmatter, "\n") + "\n---"
	if padding != "" || body != "" {
		if padding == "" {
			padding = "\n"
		}
		doc += padding
	}
	doc += body
	if !strings.HasSuffix(doc, "\n") {
		doc += "\n"
	}
	return doc
}

// RepairLaneMismatch is repair_lane_mismatch (add_history=True).
func RepairLaneMismatch(path, agent, shellPID string, warn io.Writer) (bool, string, error) {
	mm, expected, actual, err := DetectLaneMismatch(path, warn)
	if err != nil || !mm {
		return false, "", err
	}
	// The read, the repair and the write are one hold of the Mission write lock.
	release := missionWriteLock(missionDirOf(path))
	defer release()
	content, err := readUTF8Sig(path)
	if err != nil {
		return false, "Failed to parse frontmatter: " + err.Error(), nil
	}
	meta, body := ParseFrontmatter(content, warn)
	meta.Set("lane", expected)
	ts := clock.Now().UTC().Format("2006-01-02T15:04:05Z")
	note := fmt.Sprintf("Auto-repaired lane metadata (was: %s) [shell_pid %s]", actual, shellPID)
	entry := fmt.Sprintf("  - timestamp: \"%s\"\n    lane: \"%s\"\n    agent: \"%s\"\n    action: \"%s\"\n", ts, expected, agent, note)
	if existing, ok := meta.Get("activity_log"); ok {
		switch t := existing.(type) {
		case []any:
			meta.Set("activity_log", append(t, jsonx.NewObject("timestamp", ts, "lane", expected, "agent", agent, "action", note)))
		case string:
			meta.Set("activity_log", strings.TrimRight(t, " \t\n\r\v\f")+"\n"+entry)
		}
	} else {
		meta.Set("activity_log", entry)
	}
	doc := buildDocument(pyyaml.Dump(meta, pyyaml.Options{AllowUnicode: true}), body, "\n")
	if err := os.WriteFile(path, []byte("\ufeff"+doc), 0o644); err != nil {
		return false, "Failed to write file: " + err.Error(), nil
	}
	return true, "", nil
}

func run(ctx *cli.Context) error {
	out := ctx.Stdout
	p := func(s string) { rich.Print(out, s) }
	repoRoot := encoding.FindRepoRoot()
	if repoRoot == "" {
		p("[red]Error:[/red] Unable to locate repository root (missing .git or .kittify).")
		return cli.Exit(1)
	}
	if project.Locate(repoRoot) == "" {
		p("[red]Error:[/red] Unable to locate the Spec Kitty project root (.kittify directory not found).")
		p("[dim]Run this command from the project root or from a mission worktree under .worktrees/<mission>/.[/dim]")
		p("[dim]Tip: Initialize a project with 'spec-kitty init <name>' if one does not exist.[/dim]")
		return cli.Exit(1)
	}
	agent := ctx.String("agent")
	if agent == "" {
		agent = os.Getenv("SPEC_KITTY_AGENT")
		if _, set := os.LookupEnv("SPEC_KITTY_AGENT"); !set {
			agent = "system"
		}
	}
	shellPID := ctx.String("shell-pid")
	if shellPID == "" {
		shellPID = fmt.Sprint(os.Getpid())
	}
	fix := ctx.Bool("fix")
	err := func() (err error) {
		defer func() {
			// ParseFrontmatter panics with *crash for non-YAMLErrors
			if r := recover(); r != nil {
				c, ok := r.(*crash)
				if !ok {
					panic(r)
				}
				err = c
			}
		}()
		if ctx.Bool("all") {
			var dirs []string
			add := func(specs string) {
				names := scandir(specs)
				for _, n := range names {
					if st, err := os.Stat(filepath.Join(specs, n.Name())); err == nil && st.IsDir() {
						dirs = append(dirs, filepath.Join(specs, n.Name()))
					}
				}
			}
			add(project.Specs(repoRoot))
			wts := scandir(filepath.Join(repoRoot, ".worktrees"))
			for _, wt := range wts {
				if st, err := os.Stat(filepath.Join(repoRoot, ".worktrees", wt.Name())); err == nil && st.IsDir() {
					add(filepath.Join(repoRoot, ".worktrees", wt.Name(), "kitty-specs"))
				}
			}
			if len(dirs) == 0 {
				p("[yellow]No mission directories found.[/yellow]")
				return cli.Exit(0)
			}
			p(fmt.Sprintf("[cyan]Checking task metadata for %d missions...[/cyan]", len(dirs)))
			fmt.Fprintln(out)
			sort.SliceStable(dirs, func(i, j int) bool { return filepath.Base(dirs[i]) < filepath.Base(dirs[j]) })
			total, fixed := 0, 0
			for _, d := range dirs {
				m, f, err := validateFeature(ctx, d, fix, agent, shellPID)
				if err != nil {
					return err
				}
				total += m
				fixed += f
			}
			fmt.Fprintln(out)
			rich.Panel(out, fmt.Sprintf("Summary:\nTotal mismatches found: %d\nTotal mismatches fixed: %d", total, fixed), "Task Metadata Validation Complete")
			if total == 0 || fix {
				return cli.Exit(0)
			}
			return cli.Exit(1)
		}
		slug := strings.TrimSpace(ctx.String("mission"))
		if slug == "" {
			p("[red]Error:[/red] --mission <slug> is required (or use --all)")
			return cli.Exit(1)
		}
		dir := encoding.ResolveMissionDir(repoRoot, slug)
		if _, err := os.Stat(dir); err != nil {
			p("[red]Error:[/red] Mission directory not found: " + dir)
			return cli.Exit(1)
		}
		p("[cyan]Validating task metadata for mission:[/cyan] " + slug)
		fmt.Fprintln(out)
		m, f, err := validateFeature(ctx, dir, fix, agent, shellPID)
		if err != nil {
			return err
		}
		switch {
		case m == 0:
			p("[green]✓ All task metadata is consistent![/green]")
			return cli.Exit(0)
		case fix && f > 0:
			fmt.Fprintln(out)
			p(fmt.Sprintf("[green]✓ Fixed %d metadata mismatch(es).[/green]", f))
			return cli.Exit(0)
		}
		fmt.Fprintln(out)
		p(fmt.Sprintf("[yellow]Found %d metadata mismatch(es).[/yellow]", m))
		p("[dim]Run with --fix to automatically repair these mismatches.[/dim]")
		return cli.Exit(1)
	}()
	if c, ok := err.(*crash); ok {
		rich.PrintTraceback(ctx.Stderr, rich.ParseException(c.msg, rich.Frame{Name: "validate_tasks", Line: 131}))
		return cli.Exit(1)
	}
	return err
}

func validateFeature(ctx *cli.Context, dir string, fix bool, agent, shellPID string) (int, int, error) {
	out := ctx.Stdout
	rich.Print(out, "[cyan]Checking:[/cyan] "+filepath.Base(dir))
	mms, err := scan(dir, ctx.Stderr)
	if err != nil {
		return 0, 0, err
	}
	if len(mms) == 0 {
		rich.Print(out, "  [green]✓[/green] No metadata mismatches")
		return 0, 0, nil
	}
	fixed := 0
	var rows [][]string
	for _, m := range mms {
		status := "Needs Fix"
		if fix {
			ok, msg, err := RepairLaneMismatch(filepath.Join(dir, m.rel), agent, shellPID, ctx.Stderr)
			if err != nil {
				return 0, 0, err
			}
			if ok {
				status = "Fixed"
				fixed++
			} else if msg != "" {
				status = rich.Strip("Error: " + msg)
			}
		}
		exp, act := m.expected, m.actual
		if exp == "" {
			exp = "?"
		}
		if act == "" {
			act = "?"
		}
		rows = append(rows, []string{m.rel, exp, act, status})
	}
	rich.Table(out, "Task Metadata Mismatches: "+filepath.Base(dir), []string{"File", "Expected Lane", "Actual Lane", "Status"}, rows)
	if fix {
		fmt.Fprintln(out)
		rich.Print(out, fmt.Sprintf("  [green]Fixed %d of %d mismatches[/green]", fixed, len(mms)))
	}
	return len(mms), fixed, nil
}
