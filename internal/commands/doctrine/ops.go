package doctrine

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

var gitUndoAlternatives = []string{
	"git reset --soft HEAD~1  (undo last commit, keep changes)",
	"git reset --hard HEAD~1  (undo last commit, discard changes)",
	"git revert <commit>      (create reverting commit)",
	"git reflog               (find previous states)",
}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "ops", "Operation history (git reflog)")
		g.Add(&cli.Command{
			Name:  "log",
			Short: "Show operation history.",
			Long:  "Show operation history.\n\nShows the git reflog (read-only history).\n\nExamples:\n    # Show recent operations\n    spec-kitty ops log\n\n    # Show last 5 operations\n    spec-kitty ops log --limit 5\n\n    # Show with full details\n    spec-kitty ops log --verbose",
			Flags: []*cli.Flag{
				{Name: "limit", Short: "n", Kind: cli.Int, Default: 20, Help: "Number of operations to show"},
				{Name: "verbose", Short: "v", Kind: cli.Bool, Help: "Show full operation IDs and details"},
			},
			Run: runOpsLog,
		}, &cli.Command{
			Name:  "undo",
			Short: "Undo is not supported for git.",
			Long:  "Undo is not supported for git.\n\nGit does not have reversible operation history.\nConsider using these alternatives manually:\n  - git reset --soft HEAD~1  (undo last commit, keep changes)\n  - git reset --hard HEAD~1  (undo last commit, discard changes)\n  - git revert <commit>      (create reverting commit)\n  - git reflog               (find previous states)",
			Run: func(ctx *cli.Context) error {
				w := ctx.Stdout
				cprint(w, "\n[red]Undo not supported for git[/red]")
				fmt.Fprintln(w)
				cprint(w, "[dim]Git does not have reversible operation history.[/dim]")
				cprint(w, "[dim]Consider using these alternatives manually:[/dim]")
				for _, a := range gitUndoAlternatives {
					cprint(w, "  %s", a)
				}
				fmt.Fprintln(w)
				return cli.Exit(1)
			},
		})
	})
}

type reflogOp struct {
	id, desc string
	ts       time.Time
}

func gitReflog(dir string, limit int) []reflogOp {
	res, err := gitx.Exec(dir, nil, nil, "-C", dir, "reflog", fmt.Sprintf("-%d", limit), "--format=%H|%gD|%gs|%ci")
	if err != nil || res.Code != 0 {
		return nil
	}
	var out []reflogOp
	for i, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		ts, perr := time.Parse("2006-01-02 15:04:05 -0700", parts[3])
		if perr != nil {
			ts = time.Now().UTC()
		}
		out = append(out, reflogOp{id: fmt.Sprintf("reflog-%d", i), desc: parts[2], ts: ts})
	}
	return out
}

func runOpsLog(ctx *cli.Context) error {
	w := ctx.Stdout
	dir := cwd()
	// get_vcs only needs git on PATH (not a repository): outside one the
	// reflog is simply empty.
	if _, err := exec.LookPath("git"); err != nil {
		cprint(w, "[red]Error:[/red] Failed to detect VCS: git is not available. Please install git: https://git-scm.com/downloads")
		return cli.Exit(1)
	}
	cprint(w, "\n[cyan]Backend:[/cyan] git")
	fmt.Fprintln(w)
	ops := gitReflog(dir, ctx.Int("limit"))
	if len(ops) == 0 {
		cprint(w, "[dim]No operations found[/dim]")
	} else {
		t := &rtable{Title: "Operation History (git reflog)", Headers: []string{"ID", "Time", "Description"}, NoWrap: []bool{true}, Styles: []string{"cyan", "dim"}}
		for _, op := range ops {
			id := op.id
			if len([]rune(id)) > 12 {
				id = string([]rune(id)[:12])
			}
			desc := op.desc
			if len([]rune(desc)) > 60 {
				desc = string([]rune(desc)[:60]) + "..."
			}
			t.Rows = append(t.Rows, []string{id, op.ts.Format("2006-01-02 15:04"), desc})
		}
		t.render(w, rich.Width())
	}
	if ctx.Bool("verbose") && len(ops) > 0 {
		cprint(w, "\n[dim]Full operation IDs:[/dim]")
		for i, op := range ops {
			if i >= 5 {
				break
			}
			cprint(w, "  %s", op.id)
		}
	}
	fmt.Fprintln(w)
	return nil
}
