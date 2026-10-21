package maintenance

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func newDoctorWorkspacesCmd() *cli.Command {
	return &cli.Command{
		Name: "workspaces", Short: short("workspaces"), Long: longHelp["doctor_workspaces"],
		Flags: []*cli.Flag{
			boolFlag("fix", "Remove husks that are NOT registered in `git worktree list` (registered worktrees are never removed)"),
			boolFlag("json", machineJSON),
		},
		Run: runDoctorWorkspaces,
	}
}

func newDoctorSparseCheckoutCmd() *cli.Command {
	return &cli.Command{
		Name: "sparse-checkout", Short: short("sparse-checkout"), Long: longHelp["doctor_sparse-checkout"],
		Flags: []*cli.Flag{boolFlag("fix", "Apply remediation (disable sparse-checkout on primary + worktrees).")},
		Run:   runDoctorSparseCheckout,
	}
}

func huskLabel(reg *bool) string {
	switch {
	case reg == nil:
		return "unknown registration state (fix refused until git succeeds)"
	case *reg:
		return "registered (manual repair needed)"
	}
	return "unregistered (safe to --fix)"
}

func mergeObjects(objs ...*jsonx.Object) *jsonx.Object {
	out := &jsonx.Object{}
	for _, o := range objs {
		for _, kv := range o.Pairs() {
			out.Set(kv.Key, kv.Value)
		}
	}
	return out
}

func runDoctorWorkspaces(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	root, err := doctorRoot(ctx, jsonOut, 1)
	if err != nil {
		return err
	}
	out := ctx.Stdout
	if ctx.Bool("fix") {
		rep, fix, regErr := healthcheck.FixWorkspaceHusks(root)
		if regErr != nil {
			if jsonOut {
				printJSON(out, jsonx.NewObject("healthy", false, "fix_refused", true, "error", *regErr))
				return cli.Exit(1)
			}
			cprintf(out, "[red]Error:[/red] %s", *regErr)
			cprint(out, "[yellow]Workspace husks were not modified.[/yellow]")
			return cli.Exit(1)
		}
		remaining := len(fix.SkippedRegistered) + len(fix.SkippedAppearedValid) + len(fix.SkippedUnsafe)
		code := 0
		if remaining > 0 {
			code = 1
		}
		if jsonOut {
			o := mergeObjects(rep.ToObject(), fix.ToObject())
			o.Set("healthy", remaining == 0)
			printJSON(out, o)
			return exitCode(code)
		}
		for _, p := range fix.Removed {
			cprintf(out, "[green]Removed husk:[/green] %s", p)
		}
		for _, p := range fix.SkippedRegistered {
			cprintf(out, "[yellow]Preserved registered worktree:[/yellow] %s (repair manually: `git worktree repair` or `git worktree remove <path>`)", p)
		}
		for _, p := range fix.SkippedAppearedValid {
			cprintf(out, "[yellow]Skipped path that became a git worktree:[/yellow] %s", p)
		}
		for _, p := range fix.SkippedUnsafe {
			cprintf(out, "[yellow]Preserved husk holding local work:[/yellow] %s (inspect or move its contents, then remove it by hand)", p)
		}
		if len(rep.Husks) == 0 {
			cprint(out, "[green]No workspace husks found.[/green]")
		}
		for _, r := range healthcheck.RefreshStaleCoordWorktrees(root) {
			switch r[1] {
			case "refreshed":
				cprintf(out, "[green]Refreshed stale coord worktree:[/green] %s", r[0])
			case "failed":
				cprintf(out, "[yellow]Could not refresh coord worktree:[/yellow] %s", r[0])
			}
		}
		return exitCode(code)
	}
	rep := healthcheck.ScanWorkspaceHusks(root)
	if jsonOut {
		printJSON(out, rep.ToObject())
		if rep.Healthy() {
			return nil
		}
		return cli.Exit(1)
	}
	if rep.Healthy() {
		cprint(out, "[green]No workspace husks found.[/green]")
		return nil
	}
	if rep.RegistrationError != nil && len(rep.Husks) == 0 {
		cprintf(out, "[red]Error:[/red] %s", *rep.RegistrationError)
		return cli.Exit(1)
	}
	cprintf(out, "[bold]Workspace husks under %s[/bold]", rep.WorktreesDir)
	for _, h := range rep.Husks {
		cprintf(out, "  [yellow]![/yellow] %s  [dim]%s[/dim]", h.Path, huskLabel(h.Registered))
	}
	fmt.Fprintln(out)
	if rep.RegistrationError != nil {
		cprintf(out, "[red]Error:[/red] %s", *rep.RegistrationError)
	}
	cprint(out, "Remove unregistered husks with: spec-kitty doctor workspaces --fix")
	return cli.Exit(1)
}

func truthyEnv(k string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}

// interactiveEnvironment is _doctor_shared._is_interactive_environment.
func interactiveEnvironment() bool {
	if truthyEnv("SPEC_KITTY_NON_INTERACTIVE") {
		return false
	}
	inter := truthyEnv("SPEC_KITTY_FORCE_INTERACTIVE")
	if !inter {
		inter = stdinIsTTY()
	}
	if !inter {
		return false
	}
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "JENKINS_URL", "CIRCLECI"} {
		if truthyEnv(v) {
			return false
		}
	}
	return true
}

const sparseFixHint = "spec-kitty doctor sparse-checkout --fix"

func runDoctorSparseCheckout(ctx *cli.Context) error {
	out := ctx.Stdout
	root := locateRootQuiet()
	if root == "" {
		cprint(out, "[red]Error:[/red] Not in a spec-kitty project")
		return cli.Exit(1)
	}
	fix := ctx.Bool("fix")
	rep := healthcheck.ScanSparse(root)
	if !rep.AnyBlocking() {
		if fix {
			cprint(out, "No sparse-checkout state to remediate.")
		} else {
			cprint(out, "[green]✓ No legacy sparse-checkout state detected.[/green]")
		}
		return nil
	}
	if !fix {
		p := func(s string) { fmt.Fprintln(out, strings.TrimRight(stripMarkup(s), " ")) }
		p("[yellow]⚠ Legacy sparse-checkout state detected[/yellow]")
		if rep.Primary.ConfigEnabled {
			p("  Primary: " + rep.Primary.Path)
			p("    core.sparseCheckout = true")
			if rep.Primary.PatternFilePresent {
				p(fmt.Sprintf("    pattern file: %s (%d lines)", rep.Primary.PatternFile, rep.Primary.PatternLines))
			}
		}
		var active []healthcheck.SparseState
		for _, w := range rep.Worktrees {
			if w.Blocking() {
				active = append(active, w)
			}
		}
		if len(active) > 0 {
			p(fmt.Sprintf("  Lane worktrees: %d affected", len(active)))
			for _, w := range active {
				p("    " + w.Path)
			}
		}
		fmt.Fprintln(out)
		p("  Why this matters:")
		p("    spec-kitty v3.0+ removed sparse-checkout support but does not ship a")
		p("    migration. This state can cause silent data loss during mission merge")
		p("    and broken lane worktrees on agent action implement.")
		p("    See spec-kitty/spec-kitty#588.")
		fmt.Fprintln(out)
		p("  Fix:")
		p("    " + sparseFixHint)
		return cli.Exit(1)
	}
	if !interactiveEnvironment() {
		fmt.Fprintf(out, "sparse-checkout --fix requires an interactive terminal; run '%s' from a local TTY to remediate.\n", sparseFixHint)
		return cli.Exit(1)
	}
	cprint(out, "Proceed? This will:")
	cprintf(out, "  1. git sparse-checkout disable (primary)")
	cprintf(out, "  2. git config --unset core.sparseCheckout (primary)")
	cprintf(out, "  3. rm %s/.git/info/sparse-checkout (primary)", rep.Primary.Path)
	cprintf(out, "  4. git checkout HEAD -- . (primary)")
	step := 4
	var targets []string
	if rep.Primary.Blocking() {
		targets = append(targets, rep.Primary.Path)
	}
	for _, w := range rep.Worktrees {
		if w.Blocking() {
			step++
			cprintf(out, "  %d. repeat steps 1–4 in %s", step, w.Path)
			targets = append(targets, w.Path)
		}
	}
	fmt.Fprint(out, "[y/N] ")
	line, _ := bufio.NewReader(ctx.Stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		cprint(out, "Aborted — no changes made.")
		return nil
	}
	var dirty []string
	for _, t := range targets {
		if gitx.IsDirty(t) {
			dirty = append(dirty, t)
		}
	}
	if len(dirty) > 0 {
		cprint(out, "[red]✗ Cannot remediate: uncommitted changes detected.[/red]")
		for _, d := range dirty {
			cprintf(out, "  %s", d)
		}
		fmt.Fprintln(out)
		cprint(out, "  Commit or stash your changes and retry:")
		cprint(out, "    git stash push -u")
		cprintf(out, "    %s", sparseFixHint)
		return cli.Exit(1)
	}
	failed := false
	for _, t := range targets {
		gitx.OK(t, "sparse-checkout", "disable")
		gitx.OK(t, "config", "--unset", "core.sparseCheckout")
		if gd, err := gitx.Run(t, "rev-parse", "--git-dir"); err == nil {
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(t, gd)
			}
			os.Remove(filepath.Join(gd, "info", "sparse-checkout"))
		}
		if gitx.OK(t, "checkout", "HEAD", "--", ".") {
			cprintf(out, "[green]✓[/green] %s: remediated (4 steps, clean verify)", t)
		} else {
			failed = true
			cprintf(out, "[red]✗[/red] %s: failed at checkout — git checkout HEAD -- . failed", t)
		}
	}
	if failed {
		return cli.Exit(1)
	}
	return nil
}

func stripMarkup(s string) string {
	var b strings.Builder
	cprint(&b, s)
	return strings.TrimSuffix(b.String(), "\n")
}
