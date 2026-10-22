package closeout

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/consolidate"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

const consolidateLong = "Consolidate a lane-based mission into its target branch.\n\n" +
	"Exit codes: 0 on success. 75 (error code COORD_MOVED_AFTER_LANDING) when the\nlanding stands but the coordination or mission branch moved after it and was\n" +
	"kept instead of deleted; for a coordination branch, --resume lands the late\ncommits and finishes the teardown. Any other non-zero code is a refusal or\nfailure."

func init() {
	addCommands(func(root, _, missionG *cli.Command) {
		root.Add(&cli.Command{
			Name:  "consolidate",
			Short: "Consolidate a lane-based mission into its target branch.",
			Long:  consolidateLong,
			Flags: []*cli.Flag{
				{Name: "strategy", Kind: cli.String, Metavar: "<merge|squash|rebase>", Choices: []string{"merge", "squash", "rebase"}, Help: "Strategy for the branch-integration step (git merge of mission→target): merge | squash | rebase. Default: squash."},
				{Name: "delete-branch", Kind: cli.Bool, Negatable: true, NoName: "keep-branch", Help: "Delete lane branches after merge. Unset (the default) defers to the mission's meta.json retention policy (#3131), falling back to delete when no policy is recorded. Passing either flag explicitly always wins over the mission's policy."},
				{Name: "remove-worktree", Kind: cli.Bool, Negatable: true, NoName: "keep-worktree", Help: "Remove lane worktrees after merge. Unset (the default) defers to the mission's meta.json retention policy (#3131), falling back to remove when no policy is recorded. Passing either flag explicitly always wins over the mission's policy."},
				{Name: "push", Kind: cli.Bool, Help: "Publish to origin after the local merge (the operator publish step; distinct from local lane consolidation)"},
				{Name: "target", Kind: cli.String, Metavar: "<str>", Help: "Target branch for the branch-integration step (auto-detected)"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Show what would be done without executing"},
				{Name: "json", Kind: cli.Bool, Help: "Output deterministic JSON (dry-run mode)"},
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug when merging from main branch"},
				{Name: "resume", Kind: cli.Bool, Help: "Resume an interrupted merge from the last incomplete WP"},
				{Name: "abort", Kind: cli.Bool, Help: "Abort an in-progress merge, cleaning up state and worktrees"},
				{Name: "context", Kind: cli.String, Metavar: "<str>", Help: "Unused compatibility flag"},
				{Name: "keep-workspace", Kind: cli.Bool, Help: "Unused compatibility flag"},
				{Name: "allow-sparse-checkout", Kind: cli.Bool, Help: "Proceed even if legacy sparse-checkout state is detected. Use of this override is logged. Does not bypass the commit-time data-loss backstop."},
				{Name: "yes", Short: "y", Kind: cli.Bool, Help: "Proceed after merge warnings without prompts"},
				{Name: "skip-review-artifact-check", Kind: cli.Bool, Help: "Bypass the review-artifact consistency gate (the #2959 escape hatch). Requires --note; the skip is recorded as durable override evidence in the status log, never a silent bypass."},
				{Name: "note", Kind: cli.String, Metavar: "<str>", Help: "Reason recorded as override evidence when using --skip-review-artifact-check (required with it)."},
				{Name: "skip-lanes", Aliases: []string{"no-lanes"}, Kind: cli.Bool, Help: "Complete a merge-ready direct-on-target mission that has no lane branch (#2745, FR-012): when lanes.json is genuinely absent, synthesize a no-lane manifest instead of hard-failing with the missing-lanes error. A mission that has real lanes is unaffected -- an existing lanes.json is always honored as-is. Does NOT bypass the merge-ready precondition: a not-merge-ready mission still refuses before any mutation."},
				{Name: "attest-canceled-superseded", Kind: cli.StringList, Metavar: "<str>", Help: "Operator attestation (repeatable, one WP id each): the named canceled WP's content is absent or superseded, verified by hand. Lifts, for that WP only, a REFUSE whose attribution evidence can never appear later (a mixed lane, or an unstamped canceled WP on a canceled dependency lane); never lifts a FAIL. Requires --attest-reason; recorded durably in the status event log."},
				{Name: "attest-approved-reviewed", Kind: cli.StringList, Metavar: "<str>", Help: "Operator attestation (repeatable, one WP id each): the named approved work package, whose approval recorded no lane head, was reviewed as it stands on its lane. Requires --attest-reason. Does not lift a refusal for commits made after a recorded approval."},
				{Name: "attest-reason", Kind: cli.String, Metavar: "<str>", Help: "What you checked, recorded with --attest-canceled-superseded or --attest-approved-reviewed (required with either); one reason serves every attestation of the run."},
				{Name: "release-branch", Kind: cli.StringList, Metavar: "<str>", Help: "With --abort (repeatable): keep this branch at its current commit instead of refusing because it cannot be restored. The branch keeps all of its current commits, including this consolidation's unverified changes; review them afterwards. Only the record's target, mission or coordination branch can be released. Requires --release-reason."},
				{Name: "release-reason", Kind: cli.String, Metavar: "<str>", Help: "Why the released branches are kept, printed in the --abort report (required with --release-branch)."},
				{Name: "origin-check", Kind: cli.String, Metavar: "<function>", Choices: []string{"enforce", "warn", "off"}, Help: "Origin freshness check before anything lands: enforce (the default) refuses when the remote's status log or an approved lane branch of this Mission is ahead of this clone, or the remote cannot be reached; warn reports the same findings and continues; off contacts nothing and accepts stale evidence. Overrides SPEC_KITTY_ORIGIN_CHECK."},
			},
			Run: runConsolidate,
		})
		missionG.Add(&cli.Command{
			Name:  "merge",
			Short: "Merge mission branch into target branch.",
			Long: "Merge mission branch into target branch.\n\nThis command:\n1. Validates the mission is accepted\n2. Merges the mission branch into target (usually 'main')\n3. Cleans up worktree\n4. Deletes the mission branch\n\n" +
				"Auto-retry logic:\nIf current branch doesn't match feature pattern and auto-retry is enabled,\nit retries only when --mission is provided so worktree selection is deterministic.\n\n" +
				"Delegates to existing tasks_cli.py merge implementation.\n\nExamples:\n    # Merge into main branch\n    spec-kitty agent mission merge --mission 077-my-mission\n\n" +
				"    # Merge into specific branch with push\n    spec-kitty agent mission merge --mission 077-my-mission --target develop --push\n\n" +
				"    # Dry-run mode\n    spec-kitty agent mission merge --mission 077-my-mission --dry-run\n\n" +
				"    # Keep worktree and branch after merge\n    spec-kitty agent mission merge --mission 077-my-mission --keep-worktree --keep-branch",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "target", Kind: cli.String, Metavar: "<str>", Help: "Target branch for the branch-integration step (required in multi-mission repos)"},
				{Name: "strategy", Kind: cli.String, Metavar: "<str>", Default: "merge", Help: "Strategy for the branch-integration step: merge, squash, rebase"},
				{Name: "push", Kind: cli.Bool, Help: "Publish to origin after the local merge (the operator publish step)"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Show actions without executing"},
				{Name: "keep-branch", Kind: cli.Bool, Negatable: true, NoName: "delete-branch", Help: "Keep or delete mission branch after merge (default: retain-gate choice)"},
				{Name: "keep-worktree", Kind: cli.Bool, Negatable: true, NoName: "remove-worktree", Help: "Keep or remove worktree after merge (default: retain-gate choice)"},
				{Name: "auto-retry", Kind: cli.Bool, Negatable: true, Default: false, Help: "Auto-navigate to a deterministic mission worktree if in the wrong location"},
			},
			Run: runAgentMerge,
		})
	})
}

func optBool(ctx *cli.Context, name string) *bool {
	if !ctx.IsSet(name) {
		return nil
	}
	v := ctx.Bool(name)
	return &v
}

func exitFrom(err error) error {
	var e *consolidate.Exit
	if errors.As(err, &e) {
		return cli.Exit(e.Code)
	}
	return err
}

func printNext(ctx *cli.Context, slug string) {
	say(ctx.Stdout, "\n[cyan]Next:[/cyan] Run [bold]/spec-kitty-mission-review[/bold] to audit the merged mission for spec→code fidelity, drift, risks, and security.")
	say(ctx.Stdout, "[cyan]Then, while context is fresh, review the retrospective that was captured at terminus:[/cyan]\n"+
		"  [bold]spec-kitty retrospect summary[/bold] — cross-mission view\n"+
		"  [bold]spec-kitty agent retrospect synthesize --mission "+slug+"[/bold] — apply staged proposals (dry-run; add --apply to mutate)")
}

// requireMainRepo is core.context_validation.require_main_repo: refuse to
// run from inside a .worktrees/<name> checkout.
func requireMainRepo(ctx *cli.Context, name string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	parts := strings.Split(filepath.ToSlash(cwd), "/")
	for i, part := range parts {
		if part != ".worktrees" || i+1 >= len(parts) {
			continue
		}
		root := strings.Join(parts[:i], "/")
		if root == "" {
			root = "/"
		}
		_, e1 := os.Stat(filepath.Join(root, ".kittify"))
		_, e2 := os.Stat(filepath.Join(root, ".git"))
		if e1 != nil && e2 != nil {
			continue
		}
		say(ctx.Stdout, fmt.Sprintf("[bold red]Error:[/bold red] '%s' must run from the main repository\n\n"+
			"[yellow]Current location:[/yellow] Inside worktree [cyan]%s[/cyan]\n[yellow]Required location:[/yellow] Main repository\n\n"+
			"[bold]Change to main repository:[/bold]\n  cd %s\n\n"+
			"[dim]This command creates/manages worktrees and must run from the main repository.\n"+
			"Running from inside a worktree would create nested worktrees, corrupting git state.[/dim]", name, parts[i+1], root))
		return cli.Exit(1)
	}
	return nil
}

func runConsolidate(ctx *cli.Context) error {
	if err := requireMainRepo(ctx, "consolidate"); err != nil {
		return err
	}
	canceledWPs, approvedWPs := cleanIDs(ctx.Strings("attest-canceled-superseded")), cleanIDs(ctx.Strings("attest-approved-reviewed"))
	attestReason := strings.TrimSpace(ctx.String("attest-reason"))
	if len(canceledWPs) > 0 && attestReason == "" {
		say(ctx.Stdout, `[red]Error:[/red] --attest-canceled-superseded requires --attest-reason "<what you checked>" so the attestation records why.`)
		return cli.Exit(2)
	}
	if attestReason != "" && len(canceledWPs) == 0 && len(approvedWPs) == 0 {
		say(ctx.Stdout, "[yellow]Note:[/yellow] --attest-reason has no effect without --attest-canceled-superseded or --attest-approved-reviewed.")
	}
	if len(approvedWPs) > 0 && attestReason == "" {
		say(ctx.Stdout, `[red]Error:[/red] --attest-approved-reviewed requires --attest-reason "<what you checked>" so the attestation records why.`)
		return cli.Exit(2)
	}
	release := cleanIDs(ctx.Strings("release-branch"))
	releaseReason := strings.TrimSpace(ctx.String("release-reason"))
	switch {
	case len(release) == 0 && releaseReason != "":
		say(ctx.Stdout, "[yellow]Note:[/yellow] --release-reason has no effect without --release-branch.")
	case len(release) > 0 && !ctx.Bool("abort"):
		return refuseRelease(ctx, "--release-branch only applies to `spec-kitty consolidate --abort`.")
	case len(release) > 0 && releaseReason == "":
		return refuseRelease(ctx, `--release-branch requires --release-reason "<why the branch is kept>" so the release records why.`)
	}
	note := strings.TrimSpace(ctx.String("note"))
	if ctx.Bool("skip-review-artifact-check") && note == "" {
		say(ctx.Stdout, `[red]Error:[/red] --skip-review-artifact-check requires --note "<reason>" so the bypass is recorded as override evidence.`)
		return cli.Exit(2)
	}
	if note != "" && !ctx.Bool("skip-review-artifact-check") {
		say(ctx.Stdout, "[yellow]Note:[/yellow] --note has no effect without --skip-review-artifact-check; it is only recorded when the review-artifact gate is bypassed.")
	}
	root := locateRoot()
	if root == "" {
		sayLiteral(ctx.Stdout, "Error: ", "Could not locate project root")
		return cli.Exit(1)
	}
	if ctx.Bool("abort") {
		return exitFrom(consolidate.Abort(ctx.Stdout, root, ctx.String("mission"), release, releaseReason))
	}
	var notes []string
	if ctx.Bool("dry-run") && !ctx.Bool("json") {
		if len(canceledWPs) > 0 {
			notes = append(notes, "--attest-canceled-superseded is not applied with --dry-run: nothing is recorded, and the forecast does not evaluate mixed-lane attribution.")
		}
		if len(approvedWPs) > 0 {
			notes = append(notes, "--attest-approved-reviewed is not applied with --dry-run: nothing is recorded, and the forecast does not evaluate the approved bound.")
		}
	}
	o := consolidate.Options{Mission: ctx.String("mission"), Target: ctx.String("target"), Strategy: ctx.String("strategy"),
		DeleteBranch: optBool(ctx, "delete-branch"), RemoveWorktree: optBool(ctx, "remove-worktree"),
		Push: ctx.Bool("push"), DryRun: ctx.Bool("dry-run"), JSON: ctx.Bool("json"), Yes: ctx.Bool("yes"), Resume: ctx.Bool("resume"),
		OriginCheck: ctx.OptString("origin-check"), SkipLanes: ctx.Bool("skip-lanes"), DryRunNotes: notes}
	if err := consolidate.Run(ctx.Stdout, root, o); err != nil {
		return exitFrom(err)
	}
	if !o.DryRun {
		slug, _ := consolidate.ResolveMissionSlug(root, o.Mission)
		printNext(ctx, slug)
	}
	return nil
}

func runAgentMerge(ctx *cli.Context) error {
	root := locateRoot()
	if root == "" {
		printJSON(ctx.Stdout, jsonx.NewObject("error", "Could not locate project root", "success", false))
		return cli.Exit(1)
	}
	handle := ctx.String("mission")
	slug := handle
	if handle != "" {
		r, err := mission.ResolveIdentity(root, handle)
		switch e := err.(type) {
		case nil:
			slug = r.Slug
		case *mission.NotFoundError:
			say(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		default:
			sayLiteral(ctx.Stderr, "", err.Error())
		}
	}
	target := ctx.String("target")
	if target == "" && slug != "" {
		target = strings.TrimSpace(mission.ReadMetaOrEmpty(projectSpecs(root, slug)).GetString("target_branch"))
	}
	inv := func(name string) *bool {
		v := optBool(ctx, name)
		if v == nil {
			return nil
		}
		n := !*v
		return &n
	}
	if ctx.Bool("auto-retry") && os.Getenv("SPEC_KITTY_AUTORETRY") == "" {
		retryTarget := target
		if retryTarget == "" {
			retryTarget = consolidate.PrimaryBranch(root)
		}
		if code, err := autoRetry(ctx, root, slug, retryTarget); err != nil {
			printJSON(ctx.Stdout, jsonx.NewObject("error", err.Error(), "success", false))
			return cli.Exit(1)
		} else if code >= 0 {
			return cli.Exit(code)
		}
	}
	if err := requireMainRepo(ctx, "consolidate"); err != nil {
		return err
	}
	o := consolidate.Options{Mission: slug, Target: target, Strategy: ctx.String("strategy"),
		DeleteBranch: inv("keep-branch"), RemoveWorktree: inv("keep-worktree"), Push: ctx.Bool("push"), DryRun: ctx.Bool("dry-run")}
	if err := consolidate.Run(ctx.Stdout, root, o); err != nil {
		return exitFrom(err)
	}
	if !o.DryRun {
		printNext(ctx, slug)
	}
	_ = fmt.Sprint
	return nil
}

var reNumberedBranch = regexp.MustCompile(`^\d{3}-`)

// autoRetry is _maybe_auto_retry_in_worktree: re-run the merge inside the
// mission's deterministic worktree. Returns the child's exit code, or -1
// when no retry applies.
func autoRetry(ctx *cli.Context, root, slug, target string) (int, error) {
	cur := ""
	if out, err := gitx.Run(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		cur = strings.TrimSpace(out)
	} else {
		cur = consolidate.PrimaryBranch(root)
	}
	if reNumberedBranch.MatchString(cur) {
		return -1, nil
	}
	if slug == "" {
		return 0, fmt.Errorf("Not on mission branch (%s). Auto-retry requires --mission to choose a deterministic worktree.", cur)
	}
	wt := ""
	for _, c := range lanes.ListContexts(root) {
		if c.MissionSlug != slug {
			continue
		}
		cand := filepath.Join(root, c.WorktreePath)
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			wt = cand
			break
		}
	}
	if wt == "" {
		if m, err := lanes.Read(projectSpecs(root, slug)); err == nil && m != nil {
			for _, l := range m.Lanes {
				cand := lanes.WorktreePath(root, slug, l.LaneID)
				if st, err := os.Stat(cand); err == nil && st.IsDir() {
					wt = cand
					break
				}
			}
		}
	}
	if wt == "" {
		return 0, fmt.Errorf("Could not find worktree for mission %s under %s.", slug, filepath.Join(root, ".worktrees"))
	}
	say(ctx.Stdout, fmt.Sprintf("[yellow]Auto-retry:[/yellow] Not on mission branch (%s). Running merge in %s", cur, filepath.Base(wt)))
	args := []string{"agent", "mission", "merge", "--mission", slug, "--target", target, "--strategy", ctx.String("strategy")}
	if ctx.Bool("push") {
		args = append(args, "--push")
	}
	if ctx.Bool("dry-run") {
		args = append(args, "--dry-run")
	}
	if ctx.IsSet("keep-branch") {
		if ctx.Bool("keep-branch") {
			args = append(args, "--keep-branch")
		} else {
			args = append(args, "--delete-branch")
		}
	}
	if ctx.IsSet("keep-worktree") {
		if ctx.Bool("keep-worktree") {
			args = append(args, "--keep-worktree")
		} else {
			args = append(args, "--remove-worktree")
		}
	}
	args = append(args, "--no-auto-retry")
	exe, err := exec.LookPath("spec-kitty")
	if err != nil {
		return 0, fmt.Errorf("[Errno 2] No such file or directory: 'spec-kitty'")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = wt
	cmd.Env = append(os.Environ(), "SPEC_KITTY_AUTORETRY=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, ctx.Stdout, ctx.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// cleanIDs is the attestation/release flag normalisation: stripped,
// de-duplicated, blanks dropped.
func cleanIDs(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// refuseRelease is _refuse_release (exit 2, nothing changed).
func refuseRelease(ctx *cli.Context, msg string) error {
	say(ctx.Stdout, "[red]Error:[/red] "+msg+" Nothing was changed. Error code: RELEASE_BRANCH_INVALID.")
	return cli.Exit(2)
}
