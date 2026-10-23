package maintenance

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const noProjectRoot = "Could not locate project root. No .kittify/ directory found in any parent directory."

const groupFlagForwardingHelp = "Before a subcommand, forwarded if the subcommand declares it, else rejected (exit 2) — place it after the subcommand when unsure."

// registerMigrate builds the `migrate` group (registration order is the
// help listing order upstream).
func registerMigrate(root *cli.Command) {
	g := app.Group(root, "migrate", longHelp["migrate"])
	g.Long = longHelp["migrate"]
	g.InvokeWithoutCommand = true
	g.Flags = []*cli.Flag{
		boolFlag("dry-run", "Show what would change without modifying the filesystem. "+groupFlagForwardingHelp),
		{Name: "verbose", Short: "v", Kind: cli.Bool, Help: "Show file-by-file detail. " + groupFlagForwardingHelp},
		boolFlag("force", "Skip confirmation prompt. "+groupFlagForwardingHelp),
	}
	g.Callback = migrateCallback
	g.Run = runMigrateLayout
	g.Add(
		newBackfillIdentityCmd(),
		newBackfillMergeCommitCmd(),
		newBackfillTopologyCmd(),
		newBackfillMissionTypeCmd(),
		newCharterEncodingCmd(),
		newBackfillProvenanceCmd(),
		newNormalizeLifecycleCmd(),
		newRewriteOpposedByCmd(),
		newBackfillRuntimeStateCmd(),
		newBackfillWPStatusCmd(),
		newRebaselineDossierHashesCmd(),
		newRepinHooksCmd(),
	)
}

// migrateCallback is _forward_or_refuse_group_flags (#4964): a group flag
// given before a subcommand is forwarded when the subcommand declares it,
// else refused with a usage error.
func migrateCallback(ctx *cli.Context) error {
	sub := ctx.InvokedSubcommand
	if sub == "" {
		return nil
	}
	subCmd := ctx.Cmd.Find(sub)
	if subCmd == nil {
		return nil
	}
	declared := map[string]bool{}
	for _, f := range subCmd.Flags {
		declared[f.Name] = true
	}
	for _, name := range []string{"dry-run", "verbose", "force"} {
		if !ctx.IsSet(name) || !ctx.Bool(name) {
			continue
		}
		if declared[name] {
			continue
		}
		disp := "--" + name
		if name == "verbose" {
			disp = "-v/--verbose"
		}
		return cli.Usage(ctx.Cmd, "'%s' is not supported by 'migrate %s': that subcommand declares no such option, so it cannot be forwarded. Drop the flag, or run 'spec-kitty migrate %s --help' to see the options it supports.", disp, sub, sub)
	}
	return nil
}

// groupBool reads a leaf flag, honouring a value forwarded from the group.
func groupBool(ctx *cli.Context, name string) bool {
	if ctx.Bool(name) {
		return true
	}
	if ctx.Parent != nil && ctx.Parent.IsSet(name) {
		return ctx.Parent.Bool(name)
	}
	return false
}

func readYes(ctx *cli.Context) (bool, bool) {
	r := bufio.NewReader(ctx.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false, false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", true
}

func runMigrateLayout(ctx *cli.Context) error {
	out := ctx.Stdout
	dryRun, verbose, force := ctx.Bool("dry-run"), ctx.Bool("verbose"), ctx.Bool("force")
	root := project.Locate("")
	if root == "" {
		cprint(out, "[red]"+noProjectRoot+"[/red]")
		return cli.Exit(1)
	}
	if st, err := os.Stat(project.Kittify(root)); err != nil || !st.IsDir() {
		cprint(out, "[red]No .kittify/ directory found in current project.[/red]")
		return cli.Exit(1)
	}
	if !dryRun && !force {
		fmt.Fprint(out, "Migrate .kittify/ to centralized model? [y/N]: ")
		yes, _ := readYes(ctx)
		if !yes {
			rich.TyperAbort(ctx.Stderr)
			return cli.Exit(1)
		}
	}
	if dryRun {
		cprint(out, "[bold]Step 1:[/bold] Global runtime check (no changes in dry-run)")
	} else {
		cprintf(out, "[bold]Step 1:[/bold] Ensuring global runtime (%s/) is up to date...", tildePath(runtimeRootBase()))
		if err := bootstrap.EnsureRuntime(); err != nil {
			return err
		}
		cprint(out, "  [green]Global runtime is current.[/green]")
	}
	cprint(out, "[bold]Step 2:[/bold] Per-project .kittify/ cleanup...")
	rep := migrate.ExecuteLayoutMigration(root, dryRun)
	if dryRun {
		cprint(out, "[bold]Dry run -- no changes made[/bold]")
	}
	actRemoved, actMoved, actPreserved := "removed", "moved", "preserved"
	if dryRun {
		actRemoved, actMoved, actPreserved = "would remove", "would move", "would preserve"
	}
	cprintf(out, "  %d files identical to global -- %s", len(rep.Removed), actRemoved)
	if len(rep.Superseded) > 0 {
		cprintf(out, "  %d files differ from package defaults (customised or outdated) -- %s in place", len(rep.Superseded), actPreserved)
	}
	cprintf(out, "  %d files customized -- %s to overrides/", len(rep.Moved), actMoved)
	cprintf(out, "  %d files project-specific -- kept", len(rep.Kept))
	if n := len(rep.Conflicts); n > 0 {
		if dryRun {
			cprintf(out, "  [yellow]%d files would NOT be moved to overrides/ -- would be kept in place (review the legacy copies)[/yellow]", n)
		} else {
			cprintf(out, "  [yellow]%d files NOT moved to overrides/ -- kept in place (review the legacy copies)[/yellow]", n)
		}
	}
	if len(rep.Unknown) > 0 {
		cprintf(out, "  [yellow]%d files unknown -- kept with warning[/yellow]", len(rep.Unknown))
	}
	if verbose {
		for _, p := range rep.Removed {
			cprintf(out, "    [dim]removed: %s[/dim]", p)
		}
		for _, p := range rep.Superseded {
			cprintf(out, "    [dim]preserved (differs from package default): %s[/dim]", p)
		}
		for _, m := range rep.Moved {
			cprintf(out, "    [blue]moved: %s -> %s[/blue]", m[0], m[1])
		}
		reasons := map[string]string{
			migrate.ConflictDestinationExists:        "override already exists",
			migrate.ConflictDestinationParentBlocked: "a non-folder blocks the overrides path",
			migrate.ConflictSourceIsSymlink:          "legacy file is a symbolic link",
		}
		for _, c := range rep.Conflicts {
			cprintf(out, "    [yellow]not moved (%s): %s -> %s[/yellow]", reasons[c.Reason], c.Source, c.Destination)
		}
		for _, p := range rep.Kept {
			cprintf(out, "    [green]kept: %s[/green]", p)
		}
		for _, p := range rep.Unknown {
			cprintf(out, "    [yellow]unknown: %s[/yellow]", p)
		}
	}
	if !dryRun {
		if len(rep.Conflicts) == 0 {
			cprint(out, "\n[green]Migration complete.[/green] Zero legacy warnings expected. Run `spec-kitty config --show-origin` to verify resolution tiers.")
		} else {
			cprintf(out, "\n[yellow]Migration complete with %d file(s) not moved.[/yellow] The legacy copies were left in place because an override already exists, a non-folder blocks the overrides path, or the file is a symbolic link; run `spec-kitty migrate --dry-run --verbose` to list them, then delete the ones you no longer need. Run `spec-kitty config --show-origin` to verify resolution tiers.", len(rep.Conflicts))
		}
	}
	return nil
}

// runtimeRootBase is get_runtime_root().base: $SPEC_KITTY_HOME, else
// ~/.spec-kitty.
func runtimeRootBase() string {
	if h := os.Getenv("SPEC_KITTY_HOME"); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spec-kitty")
}

// wireWarn routes the migration library's logger.warning records to stderr
// (RichHandler layout) unless --json is in effect.
func wireWarn(ctx *cli.Context) {
	if ctx.Bool("json") {
		migrate.Warn = func(string) {}
		return
	}
	w := ctx.Stderr
	migrate.Warn = func(msg string) { rich.LogWarning(w, msg) }
}

// tildePath is render_runtime_path on POSIX: "~/..." under $HOME.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~/" + strings.TrimPrefix(p, home+"/")
	}
	return p
}

// locateOrError is the shared `locate_project_root() is None` guard.
func locateOrError(ctx *cli.Context) (string, error) {
	root := project.Locate("")
	if root == "" {
		errorLine(ctx, noProjectRoot)
		return "", cli.Exit(1)
	}
	return root, nil
}

func optMission(ctx *cli.Context) *string {
	if !ctx.IsSet("mission") {
		return nil
	}
	s := ctx.String("mission")
	return &s
}

func dryPrefix(dry bool) string {
	if dry {
		return "[dim](dry-run)[/dim] "
	}
	return ""
}

// ---------------------------------------------------------------------------
// backfill-identity
// ---------------------------------------------------------------------------

func missionSlugFlag() *cli.Flag {
	return &cli.Flag{Name: "mission", Kind: cli.String, Metavar: "SLUG", Help: "Scope to a single mission slug (e.g. 083-foo-bar). Omit to process all."}
}

const dryRunSameShape = "Report what would change without writing any files. The JSON shape is identical to a live run."

func newBackfillIdentityCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-identity",
		Short: "Write a ULID mission_id into any meta.json that lacks one.",
		Long:  longHelp["migrate_backfill-identity"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit per-mission result list as structured JSON"),
			boolFlag("dry-run", dryRunSameShape),
			missionSlugFlag(),
		},
		Run: runBackfillIdentity,
	}
}

func runBackfillIdentity(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	dry := groupBool(ctx, "dry-run")
	results, err := migrate.BackfillIdentity(root, dry, optMission(ctx))
	if err != nil {
		return uncaught(ctx, "ValueError", err)
	}
	var wrote, skipped, errored, coerced int
	rows := []any{}
	var errs []migrate.IdentityResult
	for _, r := range results {
		switch r.Action {
		case "wrote":
			wrote++
		case "skip":
			skipped++
		case "error":
			errored++
			errs = append(errs, r)
		}
		if r.NumberCoerced {
			coerced++
		}
		rows = append(rows, jsonx.NewObject("slug", r.Slug, "action", r.Action, "mission_id", migrate.OptStr(r.MissionID),
			"number_coerced", r.NumberCoerced, "reason", migrate.OptStr(r.Reason)))
	}
	out := ctx.Stdout
	if ctx.Bool("json") {
		printJSON(out, jsonx.NewObject("dry_run", dry,
			"summary", jsonx.NewObject("total", len(results), "wrote", wrote, "skip", skipped, "error", errored, "number_coerced", coerced),
			"results", rows))
	} else {
		cprintf(out, "\n%s[bold]backfill-identity summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Total missions scanned : %d", len(results))
		cprintf(out, "  Written (mission_id)   : %d", wrote)
		cprintf(out, "  Skipped (already set)  : %d", skipped)
		cprintf(out, "  Errors                 : %d", errored)
		cprintf(out, "  Number coerced         : %d", coerced)
		if len(errs) > 0 {
			cprint(out, "\n[red]Errors:[/red]")
			for _, r := range errs {
				cprintf(out, "  [red]%s:[/red] %s", r.Slug, deref(r.Reason))
			}
		}
		switch {
		case dry:
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		case wrote > 0:
			cprintf(out, "\n[green]Done.[/green] %d mission(s) received a ``mission_id``.", wrote)
		default:
			cprint(out, "\n[green]Done.[/green] All missions already have a ``mission_id``.")
		}
	}
	if errored > 0 {
		return cli.Exit(1)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

// uncaughtFrames are the upstream typer command functions (name, def line)
// shown in the traceback panel; the deeper site-packages frames rich would
// print with source excerpts are irreducible (docs/parity/util.md).
var uncaughtFrames = map[string]rich.Frame{
	"rebaseline-dossier-hashes": {Name: "rebaseline_dossier_hashes", Line: 1533},
	"backfill-wp-status":        {Name: "backfill_wp_status_cmd", Line: 1449},
	"rewrite-opposed-by":        {Name: "rewrite_opposed_by", Line: 1253},
	"charter-encoding":          {Name: "charter_encoding", Line: 1014},
	"backfill-provenance":       {Name: "backfill_provenance", Line: 1095},
	"mission-state":             {Name: "mission_state", Line: 1004},
	"decisions":                 {Name: "decisions_reconcile", Line: 1516},
}

// uncaught renders an exception upstream lets escape (typer's rich
// traceback: framed panel, then the exception line) and exits 1.
func uncaught(ctx *cli.Context, typ string, err error) error {
	var frames []rich.Frame
	if ctx.Cmd != nil {
		if f, ok := uncaughtFrames[ctx.Cmd.Name]; ok {
			frames = append(frames, f)
		}
	}
	// PEP 678 notes render as rich's "[NOTE]" lines, not as message text.
	var noted *charsetnorm.NotedError
	if errors.As(err, &noted) {
		rich.PrintTraceback(ctx.Stderr, &rich.Exception{Type: typ, Value: noted.Err.Error(), Frames: frames, Notes: noted.Notes})
		return cli.Exit(1)
	}
	rich.Traceback(ctx.Stderr, typ, err.Error(), frames...)
	return cli.Exit(1)
}

// ---------------------------------------------------------------------------
// backfill-topology
// ---------------------------------------------------------------------------

func newBackfillTopologyCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-topology",
		Short: "Persist each legacy mission's MissionTopology into its meta.json.",
		Long:  longHelp["migrate_backfill-topology"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit per-mission result list as structured JSON"),
			boolFlag("dry-run", dryRunSameShape),
			missionSlugFlag(),
			boolFlag("restamp-single-branch", "Re-stamp every single_branch mission whose lanes.json has a code lane to topology: lanes (Invariant T-1 repair, #5100). Mutually exclusive with the ordinary backfill this command otherwise runs; combine with --dry-run to preview."),
		},
		Run: runBackfillTopology,
	}
}

func runBackfillTopology(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	dry := groupBool(ctx, "dry-run")
	out := ctx.Stdout
	if ctx.Bool("restamp-single-branch") {
		results := migrate.RestampSingleBranch(root, dry, optMission(ctx))
		var re, sk, er int
		rows := []any{}
		for _, r := range results {
			switch r.Action {
			case "restamped":
				re++
			case "skip":
				sk++
			case "error":
				er++
			}
			rows = append(rows, jsonx.NewObject("slug", r.Slug, "action", r.Action, "reason", migrate.OptStr(r.Reason)))
		}
		if ctx.Bool("json") {
			printJSON(out, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(results), "restamped", re, "skip", sk, "error", er), "results", rows))
		} else {
			cprintf(out, "\n%s[bold]backfill-topology --restamp-single-branch summary[/bold]", dryPrefix(dry))
			cprintf(out, "  Total missions scanned      : %d", len(results))
			cprintf(out, "  Re-stamped (single_branch -> lanes) : %d", re)
			cprintf(out, "  Skipped (no violation)      : %d", sk)
			cprintf(out, "  Errors                      : %d", er)
			if er > 0 {
				cprint(out, "\n[red]Errors:[/red]")
				for _, r := range results {
					if r.Action == "error" {
						cprintf(out, "  [red]%s:[/red] %s", r.Slug, deref(r.Reason))
					}
				}
			}
			switch {
			case dry:
				cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
			case re > 0:
				cprintf(out, "\n[green]Done.[/green] %d mission(s) re-stamped to ``lanes``.", re)
			default:
				cprint(out, "\n[green]Done.[/green] No un-migrated single_branch mission found.")
			}
		}
		if er > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	results := migrate.BackfillTopology(root, dry, optMission(ctx), false)
	var wrote, sk, er int
	rows := []any{}
	for _, r := range results {
		switch r.Action {
		case "wrote":
			wrote++
		case "skip":
			sk++
		case "error":
			er++
		}
		rows = append(rows, jsonx.NewObject("slug", r.Slug, "action", r.Action, "topology", migrate.OptStr(r.Topology), "reason", migrate.OptStr(r.Reason)))
	}
	if ctx.Bool("json") {
		printJSON(out, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(results), "wrote", wrote, "skip", sk, "error", er), "results", rows))
	} else {
		cprintf(out, "\n%s[bold]backfill-topology summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Total missions scanned : %d", len(results))
		cprintf(out, "  Written (topology)     : %d", wrote)
		cprintf(out, "  Skipped (already set)  : %d", sk)
		cprintf(out, "  Errors                 : %d", er)
		if er > 0 {
			cprint(out, "\n[red]Errors:[/red]")
			for _, r := range results {
				if r.Action == "error" {
					cprintf(out, "  [red]%s:[/red] %s", r.Slug, deref(r.Reason))
				}
			}
		}
		switch {
		case dry:
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		case wrote > 0:
			cprintf(out, "\n[green]Done.[/green] %d mission(s) received a ``topology``.", wrote)
		default:
			cprint(out, "\n[green]Done.[/green] All missions already have a ``topology``.")
		}
	}
	if er > 0 {
		return cli.Exit(1)
	}
	return nil
}

// ---------------------------------------------------------------------------
// backfill-mission-type
// ---------------------------------------------------------------------------

func newBackfillMissionTypeCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-mission-type",
		Short: "Mint a profile-resolving ``mission_type`` into legacy ``meta.json`` (rc3 M0).",
		Long:  longHelp["migrate_backfill-mission-type"],
		Flags: []*cli.Flag{
			boolFlag("json", "Emit the per-mission backfill result list as structured JSON."),
			boolFlag("dry-run", dryRunSameShape),
			{Name: "mission", Kind: cli.String, Metavar: "SLUG", Help: "Scope to a single mission slug (e.g. 083-foo). Omit to process all missions."},
		},
		Run: runBackfillMissionType,
	}
}

func runBackfillMissionType(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	dry := groupBool(ctx, "dry-run")
	results, err := migrate.BackfillMissionType(root, dry, optMission(ctx))
	if err != nil {
		errorLine(ctx, err.Error())
		return cli.Exit(1)
	}
	counts := map[string]int{}
	rows := []any{}
	for _, r := range results {
		counts[r.Action]++
		rows = append(rows, jsonx.NewObject("slug", r.Slug, "action", r.Action, "mission_type", migrate.OptStr(r.MissionType),
			"legacy_value", migrate.OptStr(r.LegacyValue), "reason", migrate.OptStr(r.Reason)))
	}
	out := ctx.Stdout
	if ctx.Bool("json") {
		printJSON(out, jsonx.NewObject("dry_run", dry, "summary", jsonx.NewObject("total", len(results), "wrote", counts["wrote"],
			"skip", counts["skip"], "needs_manual_resolution", counts["needs_manual_resolution"], "error", counts["error"]), "results", rows))
	} else {
		cprintf(out, "\n%s[bold]backfill-mission-type summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Total missions scanned  : %d", len(results))
		cprintf(out, "  Written (mission_type)  : %d", counts["wrote"])
		cprintf(out, "  Skipped (already set)   : %d", counts["skip"])
		cprintf(out, "  Needs manual resolution : %d", counts["needs_manual_resolution"])
		cprintf(out, "  Errors                  : %d", counts["error"])
		if counts["error"] > 0 {
			cprint(out, "\n[red]Errors:[/red]")
			for _, r := range results {
				if r.Action == "error" {
					cprintf(out, "  [red]%s:[/red] %s", r.Slug, deref(r.Reason))
				}
			}
		}
		if counts["needs_manual_resolution"] > 0 {
			cprint(out, "\n[yellow]Needs manual resolution:[/yellow]")
			for _, r := range results {
				if r.Action == "needs_manual_resolution" {
					cprintf(out, "  [yellow]%s:[/yellow] %s", r.Slug, deref(r.Reason))
				}
			}
			cprint(out, "[dim]Fix: assign a mission type whose governance profile resolves at some layer (built-in / org / project), or author/activate that type. Not necessarily a typo.[/dim]")
		}
		switch {
		case dry:
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		case counts["wrote"] > 0:
			cprintf(out, "\n[green]Done.[/green] %d mission(s) received a ``mission_type``.", counts["wrote"])
		default:
			cprint(out, "\n[green]Done.[/green] All missions already have a ``mission_type``.")
		}
	}
	if !dry && counts["error"] > 0 {
		return cli.Exit(1)
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
