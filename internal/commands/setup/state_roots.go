package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

type stateSurface struct {
	name, pattern, root, authority, gitClass string
}

func newDoctorStateRootsCmd() *cli.Command {
	return &cli.Command{
		Name:  "state-roots",
		Short: "Show state roots, surface classification, and safety warnings.",
		Long: "Show state roots, surface classification, and safety warnings.\n\nDisplays the three state roots with resolved paths, all registered\nstate surfaces grouped by root with authority and Git classification,\n" +
			"and warnings for any runtime surfaces not covered by .gitignore.\n\nExamples:\n    spec-kitty doctor state-roots\n    spec-kitty doctor state-roots --json",
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"}},
		Run:   runDoctorStateRoots,
	}
}

type surfaceCheck struct {
	s         stateSurface
	present   bool
	covered   bool
	warning   string
	hasWarned bool
}

func surfacePresent(root string, s stateSurface) bool {
	var p string
	switch s.root {
	case "project", "feature":
		p = filepath.Join(root, filepath.FromSlash(s.pattern))
	case "global_runtime":
		if strings.HasPrefix(s.pattern, "~/") {
			p = filepath.Join(project.UserHome(), filepath.FromSlash(s.pattern[2:]))
		} else {
			p = filepath.Join(project.GlobalKittify(), filepath.FromSlash(s.pattern))
		}
	case "global_sync":
		rel := strings.TrimPrefix(s.pattern, "~/.spec-kitty/")
		p = filepath.Join(RuntimeStateRoot(), filepath.FromSlash(rel))
	case "git_internal":
		common := gitCommonDir(root)
		if common == "" {
			common = filepath.Join(root, ".git")
		}
		p = filepath.Join(common, filepath.FromSlash(strings.ReplaceAll(s.pattern, ".git/", "")))
	default:
		return false
	}
	if strings.Contains(p, "*") {
		parent, pattern := filepath.Dir(p), filepath.Base(p)
		for strings.ContainsAny(parent, "<*") {
			pattern = filepath.Base(parent) + "/" + pattern
			parent = filepath.Dir(parent)
		}
		m, _ := filepath.Glob(filepath.Join(parent, pattern))
		return len(m) > 0
	}
	if strings.Contains(p, "<") {
		parent := filepath.Dir(p)
		for strings.Contains(parent, "<") {
			parent = filepath.Dir(parent)
		}
		st, err := os.Stat(parent)
		return err == nil && st.IsDir()
	}
	_, err := os.Stat(p)
	return err == nil
}

func surfaceCovered(root string, s stateSurface) bool {
	if s.root != "project" || s.gitClass == "tracked" || s.gitClass == "outside_repo" || s.gitClass == "git_internal" {
		return true
	}
	r, err := gitx.Exec(root, nil, nil, "check-ignore", "-q", s.pattern)
	if err != nil {
		return true
	}
	return r.Code == 0
}

func runDoctorStateRoots(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root, err := doctorProjectRoot(w, jsonOut, 1)
	if err != nil {
		return err
	}
	isDir := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	type rootInfo struct{ name, label, path string }
	roots := []rootInfo{
		{"project", "Project-local state", filepath.Join(root, ".kittify")},
		{"global_runtime", "Global runtime home", project.GlobalKittify()},
		{"global_sync", "Global sync/auth home", RuntimeStateRoot()},
	}
	var checks []surfaceCheck
	var warnings []string
	for _, s := range stateSurfaces {
		c := surfaceCheck{s: s, present: surfacePresent(root, s), covered: surfaceCovered(root, s)}
		if s.root == "project" && s.gitClass != "tracked" && s.gitClass != "outside_repo" && s.gitClass != "git_internal" &&
			(s.authority == "local_runtime" || s.authority == "derived") && c.present && !c.covered {
			c.warning = fmt.Sprintf("%s (%s) is present but not gitignored. Authority: %s. Risk: accidental commit.", s.name, s.pattern, s.authority)
			c.hasWarned = true
			warnings = append(warnings, c.warning)
		}
		checks = append(checks, c)
	}
	healthy := len(warnings) == 0
	if jsonOut {
		rl := []any{}
		for _, r := range roots {
			rl = append(rl, jsonx.NewObject("name", r.name, "label", r.label, "resolved_path", r.path, "exists", isDir(r.path)))
		}
		sl := []any{}
		for _, c := range checks {
			var wv any
			if c.hasWarned {
				wv = c.warning
			}
			sl = append(sl, jsonx.NewObject("name", c.s.name, "path_pattern", c.s.pattern, "root", c.s.root, "authority", c.s.authority,
				"git_class", c.s.gitClass, "present", c.present, "gitignore_covered", c.covered, "warning", wv))
		}
		printJSONRaw(w, jsonx.NewObject("healthy", healthy, "roots", rl, "surfaces", sl, "warnings", strList(warnings)))
		return exitOrNil(boolCode(!healthy))
	}
	out(w, "\n[bold]State Roots[/bold]")
	for _, r := range roots {
		st := "[dim]absent[/dim]"
		if isDir(r.path) {
			st = "[green]exists[/green]"
		}
		out(w, fmt.Sprintf("  %-20s %s  %s", r.name, r.path, st))
	}
	fmt.Fprintln(w)
	labels := map[string]string{
		"project": "Project Surfaces (.kittify/)", "feature": "Feature Surfaces (kitty-specs/)",
		"global_runtime": "Global Runtime (" + tildePath(project.GlobalKittify()) + ")",
		"global_sync":    "Global Sync (" + tildePath(RuntimeStateRoot()) + ")", "git_internal": "Git-Internal (.git/spec-kitty/)",
	}
	for _, rk := range []string{"project", "feature", "global_runtime", "global_sync", "git_internal"} {
		var rows [][]string
		for _, c := range checks {
			if c.s.root != rk {
				continue
			}
			pres := "N"
			if c.present {
				pres = "Y"
			}
			rows = append(rows, []string{c.s.name, c.s.authority, c.s.gitClass, pres})
		}
		if len(rows) == 0 {
			continue
		}
		out(w, "[bold]"+labels[rk]+"[/bold]")
		plainTable(w, []string{"Name", "Authority", "Git Policy", "Present"}, []int{28, 16, 22, 8}, []byte("lllc"), rows)
		fmt.Fprintln(w)
	}
	printStateWarnings(w, warnings)
	return exitOrNil(boolCode(!healthy))
}

func printStateWarnings(w io.Writer, warnings []string) {
	if len(warnings) > 0 {
		out(w, "[bold yellow]Warnings[/bold yellow]")
		for _, x := range warnings {
			out(w, "  [yellow]![/yellow] "+escapeMarkup(x))
		}
	} else {
		out(w, "[green]No warnings -- all runtime surfaces are properly covered.[/green]")
	}
	fmt.Fprintln(w)
}

func boolCode(b bool) int {
	if b {
		return 1
	}
	return 0
}

// tildePath is render_runtime_path on POSIX.
func tildePath(p string) string {
	home := project.UserHome()
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}
