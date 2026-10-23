package encoding

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "validate-encoding",
			Short: "Validate and optionally fix file encoding in feature artifacts.",
			Long: "Validate and optionally fix file encoding in feature artifacts.\n\n" +
				"Scans markdown files for Windows-1252 smart quotes and other problematic\n" +
				"characters that cause UTF-8 encoding errors. Can automatically fix issues\n" +
				"by replacing problematic characters with safe alternatives.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Help: "Mission slug to validate"},
				{Name: "fix", Kind: cli.Bool, Help: "Automatically fix encoding errors by sanitizing files"},
				{Name: "all", Kind: cli.Bool, Help: "Check all features, not just one"},
				{Name: "backup", Kind: cli.Bool, Negatable: true, Default: true, Help: "Create .bak files before fixing"},
			},
			Run: run,
		})
	})
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// FindRepoRoot is task_utils.find_repo_root (cwd start); "" when not found.
func FindRepoRoot() string {
	if r := project.Locate(""); r != "" {
		return project.MainRepoRoot(r)
	}
	cwd, _ := os.Getwd()
	cur, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		cur = cwd
	}
	for {
		g := filepath.Join(cur, ".git")
		if isDir(g) && isFile(filepath.Join(g, "HEAD")) {
			return project.MainRepoRoot(cur)
		}
		if isFile(g) {
			if m := project.MainRepoRoot(cur); m != cur {
				return m
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// ResolveMissionDir is placement_seam(root, handle).read_dir(<PRIMARY kind>)
// (PRIMARY_METADATA / WORK_PACKAGE_TASK): the canonical primary mission dir,
// possibly nonexistent. An unsafe or ambiguous handle is the seam's error.
func ResolveMissionDir(root, handle string) (string, error) {
	return placement.PrimaryDir(root, handle)
}

// SeamCrash prints the uncaught seam exception (an unsafe path segment or an
// ambiguous handle) the way the upstream CLI's traceback does, exit 1.
func SeamCrash(w io.Writer, err error) error {
	typ := "UnsafePathSegmentError"
	if _, ok := err.(*mission.SelectorAmbiguousError); ok {
		typ = "MissionSelectorAmbiguous"
	}
	rich.PrintTraceback(w, &rich.Exception{Type: typ, Value: err.Error()})
	return cli.Exit(1)
}

func run(ctx *cli.Context) error {
	out := ctx.Stdout
	p := func(s string) { rich.Print(out, s) }
	repoRoot := FindRepoRoot()
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
	fix, backup := ctx.Bool("fix"), ctx.Bool("backup")
	if ctx.Bool("all") {
		specs := project.Specs(repoRoot)
		if _, err := os.Stat(specs); err != nil {
			p("[yellow]No kitty-specs directory found.[/yellow]")
			return cli.Exit(0)
		}
		var dirs []string
		names, _ := readdirRaw(specs)
		for _, n := range names {
			if isDir(filepath.Join(specs, n)) {
				dirs = append(dirs, filepath.Join(specs, n))
			}
		}
		if len(dirs) == 0 {
			p("[yellow]No mission directories found.[/yellow]")
			return cli.Exit(0)
		}
		sort.Strings(dirs)
		p(fmt.Sprintf("[cyan]Checking encoding for %d missions...[/cyan]", len(dirs)))
		fmt.Fprintln(out)
		totalIssues, totalFixed := 0, 0
		for _, d := range dirs {
			i, f := validateDir(out, d, fix, backup)
			totalIssues += i
			totalFixed += f
		}
		fmt.Fprintln(out)
		rich.Panel(out, fmt.Sprintf("Summary:\nTotal files with issues: %d\nTotal files fixed: %d", totalIssues, totalFixed), "Encoding Validation Complete")
		if totalIssues == 0 || fix {
			return cli.Exit(0)
		}
		return cli.Exit(1)
	}
	mission := strings.TrimSpace(ctx.String("mission"))
	if mission == "" {
		p("[red]Error:[/red] --mission <slug> is required (or use --all)")
		return cli.Exit(1)
	}
	dir, err := ResolveMissionDir(repoRoot, mission)
	if err != nil {
		return SeamCrash(ctx.Stderr, err)
	}
	if _, err := os.Stat(dir); err != nil {
		p("[red]Error:[/red] Mission directory not found: " + dir)
		return cli.Exit(1)
	}
	p("[cyan]Validating encoding for mission:[/cyan] " + mission)
	fmt.Fprintln(out)
	issues, fixed := validateDir(out, dir, fix, backup)
	switch {
	case issues == 0:
		p("[green]✓ All files are properly UTF-8 encoded![/green]")
		return cli.Exit(0)
	case fix && fixed > 0:
		fmt.Fprintln(out)
		p(fmt.Sprintf("[green]✓ Fixed %d file(s) with encoding issues.[/green]", fixed))
		if backup {
			p("[dim]Backup files (.bak) were created.[/dim]")
		}
		return cli.Exit(0)
	default:
		fmt.Fprintln(out)
		p(fmt.Sprintf("[yellow]Found %d file(s) with encoding issues.[/yellow]", issues))
		p("[dim]Run with --fix to automatically repair these files.[/dim]")
		return cli.Exit(1)
	}
}

func relTo(path, base string) string {
	if r, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func validateDir(out io.Writer, dir string, fix, backup bool) (int, int) {
	name := filepath.Base(dir)
	rich.Print(out, "[cyan]Checking:[/cyan] "+name)
	results := SanitizeDirectory(dir, backup, !fix)
	var withIssues, fixed []string
	type ferr struct{ path, msg string }
	var errs []ferr
	for _, r := range results {
		rel := relTo(r.Path, dir)
		switch {
		case r.Err != "":
			errs = append(errs, ferr{rel, r.Err})
		case r.Modified:
			withIssues = append(withIssues, rel)
			if fix {
				fixed = append(fixed, rel)
			}
		}
	}
	if len(withIssues) > 0 {
		var rows [][]string
		for _, f := range withIssues {
			st := "Needs Fix"
			if fix {
				st = "Fixed"
			}
			rows = append(rows, []string{f, st})
		}
		rich.Table(out, "Files with Encoding Issues: "+name, []string{"File", "Status"}, rows)
		if !fix {
			raw, err := os.ReadFile(filepath.Join(dir, withIssues[0]))
			if err == nil {
				content, derr := decodeUTF8Sig(raw)
				if derr != nil {
					var ok bool
					if content, ok = decodeCP1252(raw); !ok {
						content = decodeLatin1(raw)
					}
				}
				if issues := DetectProblematic(content); len(issues) > 0 {
					fmt.Fprintln(out)
					rich.Print(out, fmt.Sprintf("[yellow]Example issues in %s:[/yellow]", withIssues[0]))
					for i, is := range issues {
						if i == 5 {
							break
						}
						rich.Print(out, fmt.Sprintf("  Line %d, col %d: '%c' (U+%04X) → '%s'", is.Line, is.Col, is.Char, is.Char, is.Replacement))
					}
					if len(issues) > 5 {
						rich.Print(out, fmt.Sprintf("  ... and %d more", len(issues)-5))
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		fmt.Fprintln(out)
		rich.Print(out, "[red]Errors encountered:[/red]")
		for _, e := range errs {
			rich.Print(out, fmt.Sprintf("  [red]✗[/red] %s: %s", e.path, e.msg))
		}
	}
	return len(withIssues), len(fixed)
}
