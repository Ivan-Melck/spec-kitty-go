package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// charter lint (cli/commands/charter/lint.py).

func init() { registerCharterLeaf(charterLintCmd) }

func charterLintCmd() *cli.Command {
	short, long := helpK("charter_lint")
	return &cli.Command{
		Name: "lint", Short: short, Long: long,
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Help: "Scope lint to a specific mission slug"},
			{Name: "orphans", Kind: cli.Bool, Help: "Run only orphan checks"},
			{Name: "contradictions", Kind: cli.Bool, Help: "Run only contradiction checks"},
			{Name: "stale", Kind: cli.Bool, Help: "Run only staleness checks"},
			{Name: "json", Kind: cli.Bool, Help: "Output findings as JSON"},
			{Name: "severity", Kind: cli.String, Default: "low", Help: "Minimum severity (low/medium/high/critical)"},
		},
		Run: runCharterLint,
	}
}

// findRepoRootStrict is task_utils.find_repo_root (main repository root
// from the cwd; "" when no .git is found).
func findRepoRootStrict() string {
	wd, _ := os.Getwd()
	cur, err := filepath.EvalSymlinks(wd)
	if err != nil {
		cur = wd
	}
	for {
		g := filepath.Join(cur, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				if h, err := os.Stat(filepath.Join(g, "HEAD")); err == nil && h.Mode().IsRegular() {
					return project.MainRepoRoot(cur)
				}
			} else if r := project.MainRepoRoot(cur); r != cur {
				return r
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

const repoRootMissing = "Unable to locate repository root (missing .git or .kittify)."

func runCharterLint(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	root := findRepoRootStrict()
	if root == "" {
		emitCharterError(w, jsonMode, repoRootMissing, "", nil)
		return cli.Exit(1)
	}
	var checks map[string]bool
	for flag, name := range map[string]string{"orphans": "orphans", "contradictions": "contradictions", "stale": "staleness"} {
		if ctx.Bool(flag) {
			if checks == nil {
				checks = map[string]bool{}
			}
			checks[name] = true
		}
	}
	rep := charter.RunLint(root, ctx.OptString("mission"), checks, ctx.String("severity"))

	var summary []string
	frags, err := charter.LoadOrgDRG(root)
	if err != nil {
		switch err.(type) {
		case *charter.OrgPackMissingError, *charter.OrgConflictError:
			emitCharterError(w, jsonMode, "Charter Lint: org-layer load failed: "+err.Error(), "", nil)
			return cli.Exit(1)
		}
		if !jsonMode {
			cprintRaw(w, "[yellow]warning:[/yellow] org-layer skipped (load error): "+err.Error())
		}
		frags = nil
	}
	for _, f := range frags {
		summary = append(summary, "org:"+f.PackName)
	}
	if len(frags) > 0 {
		if _, err := charter.MergeThreeLayers(charter.EmptyGraph(), frags, nil); err != nil {
			ce, ok := err.(*charter.OrgConflictError)
			if !ok {
				return err
			}
			if jsonMode {
				var details []string
				for _, c := range ce.Conflicts {
					details = append(details, fmt.Sprintf("kind=%s target_id=%s layers=%s", c.Kind, c.TargetID, c.LayersRepr()))
				}
				msg := fmt.Sprintf("Charter Lint: %d org-layer conflict(s) detected", len(ce.Conflicts))
				if len(details) > 0 {
					msg += ": " + strings.Join(details, "; ")
				}
				emitCharterError(w, true, msg, "", nil)
				return cli.Exit(1)
			}
			cprint(w, "[red]Charter Lint:[/red] %d org-layer conflict(s) detected", len(ce.Conflicts))
			for _, c := range ce.Conflicts {
				cprintRaw(w, fmt.Sprintf("  - kind=%s target_id=%s layers=%s", c.Kind, c.TargetID, c.LayersRepr()))
			}
			return cli.Exit(1)
		}
	}
	if jsonMode {
		fmt.Fprintln(w, rep.JSON())
		return nil
	}
	if rep.GraphState == charter.GraphMissing {
		cprint(w, "[bold]Charter Lint:[/bold] no lintable graph found — run `spec-kitty charter synthesize`")
		return nil
	}
	cprint(w, "[bold]Charter Lint - layers:[/bold]")
	cprint(w, `  [dim]\[built-in][/dim]`)
	if rep.GraphState == charter.GraphBuiltInOnly && len(summary) == 0 {
		cprint(w, "  [dim]\\[no project overlay — run `spec-kitty charter synthesize`][/dim]")
	} else {
		for _, m := range summary {
			cprint(w, `  [dim]\[%s][/dim]`, m)
		}
		if rep.GraphState != charter.GraphBuiltInOnly {
			cprint(w, `  [dim]\[project][/dim]`)
		}
	}
	if len(rep.Findings) == 0 {
		if rep.GraphState == charter.GraphBuiltInOnly {
			cprint(w, "[green]No decay detected[/green] [dim](in built-in graph)[/dim]")
		} else {
			cprint(w, "[green]No decay detected[/green]")
		}
		cprint(w, "[dim]Scanned %d nodes in %.2fs[/dim]", rep.NodeCount, rep.Duration)
		return nil
	}
	cprint(w, "\n[bold]Charter Lint[/bold] — %d finding(s) in %.2fs\n", len(rep.Findings), rep.Duration)
	colors := map[string]string{"low": "dim", "medium": "yellow", "high": "red", "critical": "bold red"}
	for _, f := range rep.Findings {
		c := colors[f.Severity]
		if c == "" {
			c = "white"
		}
		cprint(w, "  [%s][%s][/%s] [%s] %s: %s", c, strings.ToUpper(f.Severity), c, f.Category, f.Type, f.ID)
		cprint(w, "    %s", f.Message)
		if f.Remediation != nil && *f.Remediation != "" {
			cprint(w, "    [dim]→ %s[/dim]", *f.Remediation)
		}
	}
	return nil
}
