package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

const upgradeDoc = `Upgrade a Spec Kitty project to the current version.

Detects the project's current version and applies all necessary migrations
to bring it to the installed CLI version.

**New flags (WP09)**:
  ` + "``--cli``" + `     Emit CLI upgrade guidance only.  No project detection;
                succeeds outside any project (FR-014).
  ` + "``--project``" + ` Run project migrations only; suppresses CLI nag.
                Errors outside a project.
  ` + "``--yes``/``-y``" + `  Non-interactive confirmation (alias for ` + "``--force``" + `).
                    Does NOT bypass schema-incompatibility blocks (CHK037/A-006).
  ` + "``--no-nag``" + `  Suppress upgrade-nag banner even when a CLI update exists.

Mutual exclusion: ` + "``--cli`` and ``--project``" + ` together exit 2.

**Exit codes** (R-08):
  0  Success / ALLOW / ALLOW_WITH_NAG / a completed ` + "``--dry-run``" + ` preview
  2  ` + "``--cli --project``" + ` flag conflict
  4  Project migration required (BLOCK_PROJECT_MIGRATION)
  5  Project is too new for this CLI (BLOCK_CLI_UPGRADE) — not bypassable
  6  Project metadata corrupt (BLOCK_PROJECT_CORRUPT)
  1  General error, a managed file left for your review, or a ` + "``--dry-run``" + `
     preview that could not be completed

See also: ` + "``docs/guides/how-to/installation/install-and-upgrade.md``" + `

Examples:
    spec-kitty upgrade              # Upgrade to current version
    spec-kitty upgrade --dry-run    # Preview changes
    spec-kitty upgrade --target 0.6.5  # Upgrade to specific version
    spec-kitty upgrade --cli        # Show CLI upgrade hint, no project needed
    spec-kitty upgrade --project    # Project migrations only
    spec-kitty upgrade --yes        # Non-interactive (same as --force)
    spec-kitty upgrade --dry-run --json  # Machine-readable plan`

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:           "upgrade",
			NoShortHelp:    true,
			ParseUsagePath: "upgrade",
			Short:          "Upgrade a Spec Kitty project to the current version.",
			Long:           upgradeDoc,
			Flags: []*cli.Flag{
				{Name: "dry-run", Kind: cli.Bool, Help: "Preview changes without applying"},
				{Name: "force", Kind: cli.Bool, Help: "Skip confirmation prompts"},
				{Name: "target", Kind: cli.String, Metavar: "<str>", Help: "Target version (defaults to current CLI version)"},
				{Name: "json", Kind: cli.Bool, Help: "Output results as JSON"},
				{Name: "plan-json", Kind: cli.Bool, Help: "Output the complete preview plan as JSON"},
				{Name: "verbose", Short: "v", Kind: cli.Bool, Help: "Show detailed migration information"},
				{Name: "no-worktrees", Kind: cli.Bool, Help: "Skip upgrading worktrees"},
				{Name: "cli", Kind: cli.Bool, Help: "Restrict to CLI guidance only; works outside any project (FR-014)"},
				{Name: "project", Kind: cli.Bool, Help: "Restrict to current-project compat + migrations (FR-015)"},
				{Name: "yes", Short: "y", Kind: cli.Bool, Help: "Non-interactive confirmation; alias for --force (FR-017). Upgrade never runs the mission-state repair; use `spec-kitty doctor mission-state --fix`."},
				{Name: "no-nag", Kind: cli.Bool, Help: "Suppress upgrade-nag output explicitly"},
				{Name: "agent-check", Kind: cli.Bool, Hidden: true, Help: "Emit agent-host upgrade prompt JSON"},
				{Name: "agent-choice", Kind: cli.String, Hidden: true, Metavar: "<str>", Help: "Record an agent-host upgrade choice"},
				{Name: "agent-latest", Kind: cli.String, Hidden: true, Metavar: "<str>", Help: "Latest version for --agent-choice"},
			},
			Run: runUpgrade,
		})
	})
}

func appVersion() string { return app.Version }

func stepObject(m migrationInfo) *jsonx.Object {
	return jsonx.NewObject("migration_id", m.ID, "target_schema_version", m.targetSchema(), "description", m.Description, "files_modified", nil)
}

func compactJSON(v any) string { return jsonx.Dumps(v, jsonx.Options{Indent: -1, EnsureASCII: true}) }
func prettyJSON(v any) string  { return jsonx.Dumps(v, jsonx.Pretty) }

// isInProject is upgrade._is_in_project (cwd only, no walk-up).
func isInProject(dir string) bool {
	return exists(filepath.Join(dir, ".kittify")) || exists(filepath.Join(dir, ".specify"))
}

// validateUpgradeTarget is runner.validate_upgrade_target.
func validateUpgradeTarget(from, target string) string {
	t, ok := parsePEP440(target)
	if !ok {
		return "Invalid upgrade target version: " + displayVersion(target)
	}
	if from == "unknown" {
		return ""
	}
	f, ok := parsePEP440(from)
	if !ok {
		return "Invalid project metadata version: " + displayVersion(from)
	}
	if t.cmp(f) < 0 {
		return fmt.Sprintf("Refusing to downgrade project metadata from %s to %s", displayVersion(from), displayVersion(target))
	}
	return ""
}

func displayVersion(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r > 0x7e && r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r > 0xff && r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 256 {
		s = s[:253] + "..."
	}
	return s
}

func cliModePlan(root string) CompatPlan {
	return PlanCompat(Invocation{CommandPath: []string{"upgrade"}, RawArgs: []string{"--cli"}}, root)
}

func runUpgrade(ctx *cli.Context) error {
	w := ctx.Stdout
	dryRun, jsonOut, planJSON := ctx.Bool("dry-run"), ctx.Bool("json"), ctx.Bool("plan-json")
	cliMode, projectMode := ctx.Bool("cli"), ctx.Bool("project")
	noWorktrees := ctx.Bool("no-worktrees")
	confirmed := ctx.Bool("yes") || ctx.Bool("force")
	target := ctx.OptString("target")
	agentCheck := ctx.Bool("agent-check")
	agentChoice, agentLatest := ctx.OptString("agent-choice"), ctx.OptString("agent-latest")
	cwd, _ := os.Getwd()

	// upgrade.intent: parser-level conflicts.
	hidden := agentCheck || agentChoice != nil || agentLatest != nil
	standaloneHidden := hidden && !(dryRun || ctx.Bool("force") || target != nil || planJSON || noWorktrees || cliMode || projectMode || ctx.Bool("yes"))
	preview := planJSON || dryRun || (projectMode && jsonOut)
	projectAvailable := project0(cwd) != ""
	guidance := cliMode || (!projectAvailable && !projectMode && !planJSON && !standaloneHidden)
	var conflicts []string
	if cliMode && projectMode {
		conflicts = append(conflicts, "--cli and --project are mutually exclusive")
	}
	if planJSON && cliMode {
		conflicts = append(conflicts, "--plan-json and --cli are mutually exclusive")
	}
	if hidden && (preview || guidance) {
		conflicts = append(conflicts, "Hidden agent operations cannot accompany upgrade preview or guidance")
	}
	var tgtV any
	if target != nil {
		tgtV = *target
	}
	if len(conflicts) > 0 {
		msg := strings.Join(conflicts, "\n")
		if planJSON {
			return emitBlockedFullPlan(w, cwd, tgtV, projectMode, noWorktrees, 2, "incompatible_flags", msg)
		}
		if jsonOut {
			p := PlanCompat(Invocation{CommandPath: []string{"upgrade"}, RawArgs: []string{"--cli", "--project"}}, cwd).JSON()
			if len(msg) > 1024 {
				msg = msg[:1024]
			}
			p.Set("decision", "BLOCK_INCOMPATIBLE_FLAGS")
			p.Set("case", "none")
			p.Set("exit_code", 2)
			p.Set("pending_migrations", []any{})
			p.Set("rendered_human", msg)
			fmt.Fprintln(w, compactJSON(p))
		} else {
			plain(w, msg)
		}
		return cli.Exit(2)
	}
	if agentCheck && agentChoice != nil {
		out(w, "[red]Error:[/red] --agent-check and --agent-choice are mutually exclusive.")
		return cli.Exit(2)
	}
	if agentCheck {
		if jsonOut {
			fmt.Fprintln(w, prettyJSON(jsonx.NewObject("schema_version", 1, "action", "none", "installed_version", app.Version, "latest_version", nil,
				"latest_source", "none", "install_method", "unknown", "upgrade_command", nil, "upgrade_note", unknownInstallNote, "reason", "up_to_date")))
		}
		return nil
	}
	if agentChoice != nil {
		switch *agentChoice {
		case "upgrade_now", "always", "not_now", "never_ask":
		default:
			if jsonOut {
				fmt.Fprintln(w, compactJSON(jsonx.NewObject("status", "error", "error", "invalid_agent_choice", "choice", *agentChoice)))
			} else {
				fmt.Fprintln(w, "Error: invalid agent choice")
			}
			return cli.Exit(2)
		}
		if agentLatest == nil || *agentLatest == "" {
			if jsonOut {
				fmt.Fprintln(w, compactJSON(jsonx.NewObject("status", "error", "error", "missing_agent_latest")))
			} else {
				fmt.Fprintln(w, "Error: --agent-latest is required")
			}
			return cli.Exit(2)
		}
		if jsonOut {
			fmt.Fprintln(w, prettyJSON(jsonx.NewObject("status", "recorded", "choice", *agentChoice, "latest_version", *agentLatest)))
		} else {
			fmt.Fprintln(w, "Recorded "+*agentChoice)
		}
		return nil
	}
	if cliMode {
		return runCLIMode(w, cwd, jsonOut, dryRun)
	}
	if planJSON && !isInProject(cwd) {
		return emitBlockedFullPlan(w, cwd, tgtV, projectMode, noWorktrees, 1, "project_not_initialized", "Not a Spec Kitty project")
	}
	if !isInProject(cwd) {
		if projectMode {
			if jsonOut {
				fmt.Fprintln(w, compactJSON(jsonx.NewObject("error", "Not a Spec Kitty project", "case", "project_not_initialized")))
			} else {
				out(w, "[red]Error:[/red] Not a Spec Kitty project.")
				out(w, "[dim]Run 'spec-kitty init' to initialize a project.[/dim]")
				out(w, "[dim]Tip: use 'spec-kitty upgrade --cli' for CLI guidance outside a project.[/dim]")
			}
			return cli.Exit(1)
		}
		return runCLIMode(w, cwd, jsonOut, dryRun)
	}
	root := cwd
	targetVersion := app.Version
	if target != nil {
		targetVersion = *target
	}
	if planJSON {
		return runFullPlanJSON(w, root, tgtV, targetVersion, projectMode, noWorktrees)
	}
	// CHK037: a too-new project is never upgraded, whatever --yes says.
	if v := kittify.ProjectSchemaVersion(root); v != nil && *v > kittify.MaxSupportedSchema {
		if jsonOut {
			fmt.Fprintln(w, prettyJSON(PlanCompat(Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: []string{"--project"}}, root).JSON()))
		} else {
			out(w, fmt.Sprintf("[red]Error:[/red] This project uses Spec Kitty project schema %d, but this CLI supports up to schema %d.", *v, kittify.MaxSupportedSchema))
			out(w, "[cyan]Upgrade your CLI:[/cyan] "+unknownInstallNote)
		}
		return cli.Exit(5)
	}
	if jsonOut && (projectMode || dryRun) {
		return runPlannerJSON(w, root, targetVersion, dryRun)
	}
	return runProjectUpgrade(ctx, root, targetVersion, upgradeOptions{dryRun: dryRun, jsonOut: jsonOut, verbose: ctx.Bool("verbose"), confirmed: confirmed})
}

// project0 resolves the project the way the read-only intent resolver does.
func project0(cwd string) string {
	if isInProject(cwd) {
		return cwd
	}
	return ""
}

func runCLIMode(w io.Writer, cwd string, jsonOut, dryRun bool) error {
	root := projectRootOrHome(cwd)
	p := cliModePlan(root)
	if jsonOut {
		code := p.ExitCode
		if dryRun {
			code = 0
		}
		fmt.Fprintln(w, prettyJSON(p.JSON()))
		return exitOrNil(code)
	}
	if p.Human != "" {
		fmt.Fprintln(w, p.Human)
	}
	return nil
}

func exitOrNil(code int) error {
	if code == 0 {
		return nil
	}
	return cli.Exit(code)
}

func projectRootOrHome(cwd string) string {
	return locateProject(cwd)
}

func runPlannerJSON(w io.Writer, root, targetVersion string, dryRun bool) error {
	p := PlanCompat(Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: []string{"--project"}}, root)
	p.Pending = nil
	payload := p.JSON()
	payload.Set("rendered_human", "")
	semantic := p.ExitCode
	current := detectProjectVersion(root)
	if verr := validateUpgradeTarget(current, targetVersion); verr != "" {
		payload.Set("pending_migrations", []any{})
		switch p.Decision {
		case DecisionBlockCorrupt, DecisionBlockCLIUpgrade, DecisionBlockMigration:
			payload.Set("rendered_human", "\n"+truncate(verr, 511))
		default:
			payload.Set("decision", "BLOCK_INCOMPATIBLE_FLAGS")
			payload.Set("case", "none")
			payload.Set("exit_code", 2)
			payload.Set("rendered_human", verr)
			semantic = 2
		}
	} else {
		payload.Set("pending_migrations", migrationSteps(root, targetVersion))
		if dryRun && semantic != 2 && semantic != 5 && semantic != 6 {
			if notice := supportingRepairNotice(root); notice != "" {
				payload.Set("rendered_human", "\n"+notice)
			}
		}
	}
	code := semantic
	if dryRun && semantic != 5 {
		code = 0
	}
	fmt.Fprintln(w, prettyJSON(payload))
	return exitOrNil(code)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func emptyFullPlan(root string, target any, project, noWorktrees bool) *jsonx.Object {
	abs, _ := filepath.Abs(root)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return jsonx.NewObject(
		"schema_version", 1, "mode", "preview",
		"intent", jsonx.NewObject("project_mode", project, "requested_target", target, "include_worktrees", !noWorktrees, "confirm", false),
		"target", jsonx.NewObject("current", nil, "requested", target, "relation", "unknown", "valid", false, "reason", ""),
		"compatibility", &jsonx.Object{}, "complete", false, "decision", "blocked", "process_exit_code", 1,
		"roots", []any{jsonx.NewObject("id", "project", "scope", "project", "path", abs)},
		"migrations", []any{}, "effects", []any{}, "dispositions", []any{}, "diagnostics", []any{}, "execution_artifacts", []any{},
		"commit_policy", jsonx.NewObject("project_enabled", false, "worktrees_enabled", false, "mission_repair_included", false))
}

func fullPlanCompat(root string) *jsonx.Object {
	return PlanCompat(Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: []string{"--project", "--plan-json"}}, root).JSON()
}

func emitBlockedFullPlan(w io.Writer, root string, target any, project, noWorktrees bool, code int, diag, msg string) error {
	p := emptyFullPlan(root, target, project, noWorktrees)
	p.Set("process_exit_code", code)
	p.Set("compatibility", fullPlanCompat(locateProject(root)))
	p.Set("target", jsonx.NewObject("current", nil, "requested", target, "relation", "unknown", "valid", diag != "invalid_target", "reason", msg))
	p.Set("diagnostics", []any{jsonx.NewObject("code", diag, "owner", nil, "severity", "error", "message", msg)})
	fmt.Fprintln(w, prettyJSON(p))
	return cli.Exit(code)
}

func runFullPlanJSON(w io.Writer, root string, requested any, target string, projectMode, noWorktrees bool) error {
	current := detectProjectVersion(root)
	compat := fullPlanCompat(root)
	p := emptyFullPlan(root, requested, projectMode, noWorktrees)
	p.Set("compatibility", compat)
	relation := "unknown"
	if t, ok := parsePEP440(target); !ok {
		relation = "invalid"
	} else if current != "unknown" {
		if c, ok := parsePEP440(current); ok {
			switch t.cmp(c) {
			case -1:
				relation = "lower"
			case 0:
				relation = "equal"
			default:
				relation = "higher"
			}
		}
	}
	terr := validateUpgradeTarget(current, target)
	reason := terr
	if reason == "" {
		reason = "Target is valid"
	}
	p.Set("target", jsonx.NewObject("current", current, "requested", target, "relation", relation, "valid", terr == "", "reason", reason))
	block := func(code int, diag, msg string) error {
		p.Set("decision", "blocked")
		p.Set("process_exit_code", code)
		p.Set("diagnostics", []any{jsonx.NewObject("code", diag, "owner", nil, "severity", "error", "message", msg)})
		fmt.Fprintln(w, prettyJSON(p))
		return cli.Exit(code)
	}
	if compat.GetString("decision") == string(DecisionBlockCorrupt) {
		msg := compat.GetString("rendered_human")
		if msg == "" {
			msg = "Project metadata is corrupt"
		}
		return block(6, "project_corrupt", msg)
	}
	if v := kittify.ProjectSchemaVersion(root); v != nil && *v > kittify.MaxSupportedSchema {
		return block(5, "cli_upgrade_required", fmt.Sprintf("Project schema %d exceeds supported schema %d", *v, kittify.MaxSupportedSchema))
	}
	if terr != "" {
		return block(2, "invalid_target", terr)
	}
	roots := p.GetList("roots")
	p.Set("roots", append([]any{jsonx.NewObject("id", "global_assets", "scope", "global", "path", filepath.Dir(project.UserHome()))}, roots...))
	p.Set("migrations", migrationSteps(root, target))
	p.Set("complete", true)
	p.Set("decision", "ready")
	p.Set("process_exit_code", 0)
	p.Set("commit_policy", jsonx.NewObject("project_enabled", kittify.AutoCommit(root), "worktrees_enabled", !noWorktrees, "mission_repair_included", false))
	fmt.Fprintln(w, prettyJSON(p))
	return nil
}

func locateProject(cwd string) string {
	if r := project.Locate(cwd); r != "" {
		return r
	}
	return ""
}

// ---------------------------------------------------------------------------
// The project upgrade run
// ---------------------------------------------------------------------------

type upgradeOptions struct {
	dryRun, jsonOut, verbose, confirmed bool
}

// upgradeOutcome is upgrade.outcome.UpgradeOutcome (+ UpgradeResult).
type upgradeOutcome struct {
	from, to           string
	dryRun             bool
	success            bool
	applied, skipped   []string
	errors, warnings   []string
	results            map[string]migrationResult
	hadMigrations      bool
	activationErrors   []string
	committed          bool
	mergeDriverConfig  string
	surfaceRepairFails []string
	commitPaths        []string
	regenerated        []string
	created, repaired  []string
}

func (o *upgradeOutcome) reasons() []string {
	var r []string
	if !o.success {
		r = append(r, "migration_failed")
	}
	if len(o.activationErrors) > 0 {
		r = append(r, "activation_error")
	}
	if len(o.surfaceRepairFails) > 0 {
		r = append(r, "surface_repair_failed")
	}
	return r
}

func (o *upgradeOutcome) kind() string {
	if len(o.reasons()) == 0 {
		if o.hadMigrations {
			return "applied"
		}
		return "no_op"
	}
	return "failed"
}

func (o *upgradeOutcome) status() string {
	switch o.kind() {
	case "applied":
		return "success"
	case "no_op":
		return "up_to_date"
	}
	return "failed"
}

func (o *upgradeOutcome) errorList() []string {
	var all []string
	all = append(all, o.errors...)
	all = append(all, o.activationErrors...)
	all = append(all, o.surfaceRepairFails...)
	seen := map[string]bool{}
	var out []string
	for _, e := range all {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) == 0 && !o.success {
		out = append(out, "The migrations did not complete.")
	}
	return out
}

func (o *upgradeOutcome) closingLine() string {
	switch o.kind() {
	case "no_op":
		return "Project is already up to date!"
	case "applied":
		if o.dryRun {
			return fmt.Sprintf("Dry run complete — no changes applied. (%s -> %s previewed)", o.from, o.to)
		}
		return fmt.Sprintf("Upgrade complete! %s -> %s", o.from, o.to)
	}
	return "Upgrade failed."
}

func strList(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}

func runProjectUpgrade(ctx *cli.Context, root, target string, opt upgradeOptions) error {
	w := ctx.Stdout
	migrationStdout = w
	migrationStderr = ctx.Stderr
	baseline := captureGitBaseline(root)
	current := detectProjectVersion(root)
	if verr := validateUpgradeTarget(current, target); verr != "" {
		if opt.jsonOut {
			fmt.Fprintln(w, compactJSON(jsonx.NewObject("status", "failed", "current_version", current, "target_version", target, "success", false,
				"errors", []any{verr}, "warnings", []any{}, "auto_committed", false, "auto_commit_paths", []any{})))
		} else {
			out(w, "[red]Error:[/red] "+escapeMarkup(verr))
		}
		return cli.Exit(1)
	}
	if !opt.jsonOut {
		out(w, "[cyan]Current version:[/cyan] "+current)
		out(w, "[cyan]Target version:[/cyan]  "+target)
		fmt.Fprintln(w)
	}
	from := current
	if from == "unknown" {
		from = "0.0.0"
	}
	migrations := applicableMigrations(from, target, root)
	o := &upgradeOutcome{from: current, to: target, dryRun: opt.dryRun, success: true, results: map[string]migrationResult{}, mergeDriverConfig: "skipped"}
	now := clock.NowUTCISO()
	if len(migrations) == 0 {
		if !opt.dryRun {
			if err := kittify.SetMetadataVersion(root, target, now); err != nil {
				return err
			}
			kittify.StampSchemaVersion(root)
		}
	} else {
		o.hadMigrations = true
		if !opt.jsonOut {
			var rows [][]string
			for _, m := range migrations {
				rows = append(rows, []string{m.ID, rich.Strip(m.Description), m.TargetVersion})
			}
			rich.TableX(w, rich.TableSpec{Title: "Migration Plan", Headers: []string{"Migration", "Description", "Target"}}, rows)
			fmt.Fprintln(w)
			printDryRunProvisioningNotice(w, root, opt.dryRun)
			if opt.verbose {
				out(w, "[dim]Detection results:[/dim]")
				for _, m := range migrations {
					ok, reason := m.canApply(root)
					st := "[yellow]skipped[/yellow]"
					if m.detect(root) && ok {
						st = "[green]ready[/green]"
					}
					out(w, "  "+m.ID+": "+st)
					if !ok && reason != "" {
						out(w, "    [dim]"+escapeMarkup(reason)+"[/dim]")
					}
				}
				fmt.Fprintln(w)
			}
		}
		if !opt.dryRun && !opt.confirmed {
			fmt.Fprintf(w, "Apply %d migration(s)? [Y/n]: ", len(migrations))
			line, err := bufio.NewReader(ctx.Stdin).ReadString('\n')
			ans := strings.ToLower(strings.TrimSpace(line))
			if (err != nil && line == "") || (ans != "" && ans != "y" && ans != "yes") {
				if err != nil && line == "" {
					fmt.Fprintln(w)
				}
				out(w, "[yellow]Upgrade cancelled.[/yellow]")
				return nil
			}
		}
		runMigrations(root, target, migrations, o, now)
	}
	// Finalizer tail.
	shouldCommit := o.success && !opt.dryRun && kittify.AutoCommit(root)
	if !opt.dryRun && isGitCheckoutRoot(root) {
		state, warns, err := kittify.InstallMergeDriverConfig(root)
		if err != nil {
			o.warnings = append(o.warnings, "Could not install clone-local merge-driver settings: "+err.Error())
			o.mergeDriverConfig = "failed"
		} else {
			o.mergeDriverConfig = state
			if state == "customized" {
				o.warnings = append(o.warnings, warns...)
			}
		}
	}
	if o.success {
		if opt.dryRun {
			if n := supportingRepairNotice(root); n != "" && !opt.jsonOut {
				plain(w, n)
			}
		} else {
			if err := provisionMissionTypeActivations(root); err != nil {
				o.activationErrors = append(o.activationErrors, provisionErrorText(err))
			}
			if len(o.activationErrors) == 0 {
				applyUpgradeRepairs(root, o)
			}
		}
	}
	if shouldCommit && len(o.activationErrors) == 0 && o.success && len(o.surfaceRepairFails) == 0 {
		committed, paths, warn := commitUpgradeChurn(root, baseline, o.from, o.to)
		o.committed, o.commitPaths = committed, paths
		if warn != "" {
			o.warnings = append(o.warnings, warn)
		} else if reason := noCommitReason(root, o, baseline, opt.dryRun, shouldCommit); reason != "" {
			o.warnings = append(o.warnings, reason)
		}
	} else if reason := noCommitReason(root, o, baseline, opt.dryRun, shouldCommit); reason != "" {
		o.warnings = append(o.warnings, reason)
	}
	leftUncommitted := !opt.jsonOut && !opt.dryRun && o.success && !shouldCommit && !o.committed && baseline != nil && len(upgradeCommitCandidates(root, baseline)) > 0
	if opt.jsonOut {
		fmt.Fprintln(w, compactJSON(upgradeJSON(o, migrations)))
	} else {
		renderUpgradeText(w, root, o, leftUncommitted, opt.dryRun)
	}
	if o.kind() == "failed" {
		return cli.Exit(1)
	}
	return nil
}

func provisionErrorText(err error) string {
	if e, ok := err.(*kittify.DefaultPresetError); ok {
		return fmt.Sprintf("Error (%s): %s", e.Code(), e.Body)
	}
	return err.Error()
}

func runMigrations(root, target string, migrations []migrationInfo, o *upgradeOutcome, now string) {
	md := kittify.LoadMetadata(root)
	if md == nil {
		md = kittify.NewMetadata(o.from)
	}
	if !o.dryRun {
		o.warnings = append(o.warnings, md.NormalizeAndSaveLegacyIDs(root)...)
	}
	pre := kittify.CaptureVersionStamp(root)
	record := func(id, result string, notes *string) {
		if o.dryRun {
			return
		}
		if md.RecordMigration(id, result, notes) {
			md.Save(root)
		}
	}
	for _, m := range migrations {
		var res migrationResult
		status := ""
		needed, derr := false, error(nil)
		recorded := md.HasMigration(m.ID) && !m.reselectWhenRecorded(root)
		if !recorded {
			needed, derr = m.detectErr(root)
		}
		switch {
		case recorded:
			res = migrationResult{Success: true, Warnings: []string{fmt.Sprintf("Migration %s already applied, skipping", m.ID)}}
			status = "skipped"
		case derr != nil:
			n := "Cannot safely detect: " + derr.Error()
			record(m.ID, "failed", &n)
			res = migrationResult{Errors: []string{fmt.Sprintf("Cannot safely detect %s: %s", m.ID, derr.Error())}}
			status = "failed"
		case !needed:
			n := "Not applicable"
			record(m.ID, "skipped", &n)
			res = migrationResult{Success: true, Warnings: []string{fmt.Sprintf("Migration %s not needed (project already in target state)", m.ID)}}
			status = "skipped"
		default:
			if ok, reason := m.canApply(root); !ok {
				res = migrationResult{Errors: []string{fmt.Sprintf("Cannot apply %s: %s", m.ID, reason)}}
				status = "failed"
				break
			}
			var aerr error
			res, aerr = m.applyErr(root, o.dryRun)
			if aerr != nil {
				res = migrationResult{Errors: []string{fmt.Sprintf("Cannot apply %s: %s", m.ID, aerr.Error())}}
				status = "failed"
				break
			}
			if res.Success || !m.RunsFirst {
				var notes *string
				if len(res.Changes) > 0 {
					n := strings.Join(res.Changes, "; ")
					notes = &n
				}
				r := "success"
				if !res.Success {
					r = "failed"
				}
				record(m.ID, r, notes)
			}
			status = "applied"
			if !res.Success {
				status = "failed"
			}
		}
		o.warnings = append(o.warnings, res.Warnings...)
		o.results[m.ID] = res
		switch status {
		case "applied":
			o.applied = append(o.applied, m.ID)
		case "skipped":
			o.skipped = append(o.skipped, m.ID)
		default:
			o.success = false
			o.errors = append(o.errors, res.Errors...)
		}
		if !o.success {
			break
		}
	}
	if o.dryRun {
		return
	}
	if o.success {
		md.Version = target
		md.LastUpgradedAt = clock.NowUTCISO()
		md.Save(root)
		kittify.StampSchemaVersion(root)
	} else {
		pre.Restore(root)
	}
}

func isGitCheckoutRoot(root string) bool {
	t, err := gitx.Toplevel(root)
	if err != nil {
		return false
	}
	a, _ := filepath.EvalSymlinks(t)
	b, _ := filepath.EvalSymlinks(root)
	return a == b
}

// applyUpgradeRepairs runs the finalizer's tool-surface repair: global
// runtime/skills/commands, project skills and profiles, command skills for
// command-skill agents and orientation surfaces.
func applyUpgradeRepairs(root string, o *upgradeOutcome) {
	if _, err := bootstrap.EnsureAll(bootstrap.Options{}); err != nil {
		o.surfaceRepairFails = append(o.surfaceRepairFails, err.Error())
		return
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		o.surfaceRepairFails = append(o.surfaceRepairFails, err.Error())
		return
	}
	sum, err := RunSurfaceRepair(root)
	if err != nil {
		o.surfaceRepairFails = append(o.surfaceRepairFails, err.Error())
		return
	}
	o.created = append(o.created, sum.Created...)
	o.repaired = append(o.repaired, sum.Repaired...)
	for _, p := range sum.Pruned {
		o.repaired = append(o.repaired, filepath.Join(root, filepath.FromSlash(p)))
	}
	if len(sum.Pruned) > 0 {
		o.repaired = append(o.repaired, agents.ProfileManifestPath(root))
	}
	now := clock.NowUTCISO()
	for _, a := range ac.Available {
		if !agents.IsCommandSkillAgent(a) {
			continue
		}
		rep, err := agents.InstallCommandSkills(root, a, now)
		if err != nil {
			o.surfaceRepairFails = append(o.surfaceRepairFails, err.Error())
			continue
		}
		if a == "vibe" {
			agents.EnsureVibeSkillPath(root)
		}
		o.created = append(o.created, rep.Added...)
		o.regenerated = append(o.regenerated, rep.Regenerated...)
	}
	InstallPresence(root, ac.Available)
}

// supportingRepairNotice is _supporting_repair_preview's text: an estimate
// of the effects a real run's repair would apply.
func supportingRepairNotice(root string) string {
	effects, manifests := 0, 0
	dirs := map[string]bool{}
	countPath := func(rel string) {
		effects++
		d := filepath.Dir(filepath.FromSlash(rel))
		for d != "." && d != string(filepath.Separator) {
			if dirs[d] {
				break
			}
			if _, err := os.Stat(filepath.Join(root, d)); err == nil {
				break
			}
			dirs[d] = true
			effects++
			d = filepath.Dir(d)
		}
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return "Supporting repair preview incomplete: " + strings.TrimRight(err.Error(), ".") + ". Use --plan-json for full repair details."
	}
	prev := agents.LoadSkillsManifest(root)
	seen := map[string]bool{}
	for _, a := range ac.Available {
		st := classifySkills(root, a, prev)
		for _, p := range append(st.missing, st.stale...) {
			if seen[p] {
				continue
			}
			seen[p] = true
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				countPath(p)
			} else {
				effects++
			}
		}
		if agents.IsCommandSkillAgent(a) {
			for _, c := range agents.CommandSkillCommands {
				p := agents.CommandSkillPath(c)
				if seen[p] {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
					seen[p] = true
					countPath(p)
				}
			}
		}
	}
	if kittify.ConfigExists(root) && activationProvisioningPending(root) {
		effects++
	}
	if effects == 0 {
		return ""
	}
	return fmt.Sprintf("Would repair %d supporting surface paths (including %d manifests). Use --plan-json for full repair details.", effects, manifests)
}

func printDryRunProvisioningNotice(w io.Writer, root string, dryRun bool) {
	if !dryRun {
		return
	}
	if kittify.ConfigExists(root) && activationProvisioningPending(root) {
		out(w, "[dim]Would provision missing mission_type_activations (seeded on a real upgrade).[/dim]")
	}
}

func upgradeJSON(o *upgradeOutcome, migrations []migrationInfo) *jsonx.Object {
	surface := jsonx.NewObject("created", strList(o.created), "repaired", strList(o.repaired), "regenerated", strList(o.regenerated),
		"drifted_overwritten", []any{}, "drifted_reported", []any{}, "skipped", []any{})
	if o.dryRun {
		surface = jsonx.NewObject("created", []any{}, "repaired", []any{}, "regenerated", []any{}, "drifted_overwritten", []any{}, "drifted_reported", []any{}, "skipped", []any{})
	}
	reasons := strList(o.reasons())
	if !o.hadMigrations {
		return jsonx.NewObject("status", o.status(), "outcome", o.kind(), "failure_reasons", reasons, "current_version", o.from, "target_version", o.to,
			"success", len(o.reasons()) == 0, "errors", strList(o.errorList()), "auto_committed", o.committed, "auto_commit_paths", strList(o.commitPaths),
			"warnings", strList(o.warnings), "merge_driver_config", o.mergeDriverConfig, "surface_repair", surface)
	}
	var detail []any
	reports := &jsonx.Object{}
	appliedSet, skippedSet := map[string]bool{}, map[string]bool{}
	for _, id := range o.applied {
		appliedSet[id] = true
	}
	for _, id := range o.skipped {
		skippedSet[id] = true
	}
	for _, m := range migrations {
		st := "pending"
		if appliedSet[m.ID] {
			st = "applied"
		} else if skippedSet[m.ID] {
			st = "skipped"
		}
		detail = append(detail, jsonx.NewObject("id", m.ID, "description", m.Description, "target_version", m.TargetVersion, "status", st,
			"manual_review_required", false, "preserved_paths", []any{}))
		if r, ok := o.results[m.ID]; ok && len(r.Changes) > 0 {
			if v, err := jsonx.Decode([]byte(r.Changes[0])); err == nil {
				reports.Set(m.ID, v)
			}
		}
	}
	return jsonx.NewObject("status", o.status(), "outcome", o.kind(), "failure_reasons", reasons, "current_version", o.from, "target_version", o.to,
		"dry_run", o.dryRun, "migrations", detail, "migrations_applied", strList(o.applied), "migrations_skipped", strList(o.skipped),
		"migration_reports", reports, "success", len(o.reasons()) == 0, "errors", strList(o.errorList()), "warnings", strList(o.warnings),
		"manual_review_required", false, "manual_review_paths", []any{}, "auto_committed", o.committed, "auto_commit_paths", strList(o.commitPaths),
		"merge_driver_config", o.mergeDriverConfig, "surface_repair", surface)
}

func printSection(w io.Writer, header string, items []string, prefix string) {
	if len(items) == 0 {
		return
	}
	out(w, header)
	for _, it := range items {
		out(w, prefix+escapeMarkup(it))
	}
}

func renderUpgradeText(w io.Writer, root string, o *upgradeOutcome, leftUncommitted, dryRun bool) {
	if o.hadMigrations {
		fmt.Fprintln(w)
		if o.dryRun {
			rich.Panel(w, "[yellow]DRY RUN[/yellow] - No changes were made", "")
		}
		printSection(w, "[green]Migrations applied:[/green]", o.applied, "  [green]✓[/green] ")
		printSection(w, "[dim]Migrations skipped (already applied or not needed):[/dim]", o.skipped, "  [dim]○[/dim] ")
	}
	if o.mergeDriverConfig == "installed" {
		plain(w, "Installed clone-local merge-driver settings")
	}
	for _, p := range o.regenerated {
		plain(w, RegeneratedSurfaceLine(filepath.Join(root, filepath.FromSlash(p))))
	}
	commitLine := func() {
		if o.committed {
			if len(o.commitPaths) > 0 {
				out(w, fmt.Sprintf("[cyan]→ Auto-committed upgrade changes (%d files)[/cyan]", len(o.commitPaths)))
			} else {
				out(w, "[cyan]→ Auto-committed upgrade changes[/cyan]")
			}
		} else if leftUncommitted {
			out(w, "[yellow]⚠ Changes were left uncommitted (auto_commit is disabled) — commit them yourself.[/yellow]")
		}
	}
	noticeBefore := !o.hadMigrations && o.kind() != "no_op"
	if noticeBefore {
		printDryRunProvisioningNotice(w, root, dryRun)
	}
	if o.kind() == "no_op" {
		plain(w, o.closingLine())
		for _, x := range o.warnings {
			out(w, "[yellow]Warning:[/yellow] "+escapeMarkup(x))
		}
		for _, x := range o.errorList() {
			out(w, "[red]Error:[/red] "+escapeMarkup(x))
		}
		commitLine()
	} else {
		printSection(w, "[yellow]Warnings:[/yellow]", o.warnings, "  [yellow]![/yellow] ")
		printSection(w, "[red]Errors:[/red]", o.errorList(), "  [red]✗[/red] ")
		success := o.kind() == "applied"
		if !o.dryRun && !success {
			commitLine()
		}
		fmt.Fprintln(w)
		plain(w, o.closingLine())
		if !o.dryRun && success {
			commitLine()
		}
	}
	if !o.hadMigrations && !noticeBefore {
		printDryRunProvisioningNotice(w, root, dryRun)
	}
}

// ---------------------------------------------------------------------------
// Auto-commit (upgrade.autocommit)
// ---------------------------------------------------------------------------

type gitBaseline struct {
	paths   map[string]bool
	ignored []string
}

func porcelainPaths(root string, args ...string) (map[string]string, bool) {
	r, err := gitx.Exec(root, nil, nil, append([]string{"status", "--porcelain=v1", "-z"}, args...)...)
	if err != nil || r.Code != 0 {
		return nil, false
	}
	out := map[string]string{}
	fields := strings.Split(r.Stdout, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		xy, p := f[:2], f[3:]
		out[p] = xy
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the origin path follows
		}
	}
	return out, true
}

func captureGitBaseline(root string) *gitBaseline {
	ps, ok := porcelainPaths(root, "--untracked-files=all")
	if !ok {
		return nil
	}
	b := &gitBaseline{paths: map[string]bool{}}
	for p := range ps {
		b.paths[p] = true
	}
	if ig, ok := porcelainPaths(root, "--untracked-files=normal", "--ignored"); ok {
		for p, xy := range ig {
			if xy == "!!" {
				b.ignored = append(b.ignored, p)
			}
		}
	}
	return b
}

func underIgnored(p string, ignored []string) bool {
	for _, e := range ignored {
		if p == e || p+"/" == e || (strings.HasSuffix(e, "/") && strings.HasPrefix(p, e)) {
			return true
		}
	}
	return false
}

func upgradeCommitCandidates(root string, b *gitBaseline) []string {
	if b == nil {
		return nil
	}
	cur, ok := porcelainPaths(root, "--untracked-files=all")
	if !ok {
		return nil
	}
	home, _ := filepath.EvalSymlinks(os.Getenv("HOME"))
	rr, _ := filepath.EvalSymlinks(root)
	var out []string
	for p, xy := range cur {
		if b.paths[p] || underIgnored(p, b.ignored) || strings.HasPrefix(p, "../") {
			continue
		}
		if xy[0] == 'D' {
			if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
				continue
			}
		}
		if rr == home && (strings.HasPrefix(p, ".kittify/") || !strings.Contains(p, "/")) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func commitUpgradeChurn(root string, b *gitBaseline, from, to string) (bool, []string, string) {
	paths := upgradeCommitCandidates(root, b)
	if len(paths) == 0 {
		return false, nil, ""
	}
	r, err := gitx.Exec(root, nil, nil, "branch", "--show-current")
	if err != nil || r.Code != 0 {
		return false, paths, "Could not determine the current branch; skipped auto-committing upgrade changes — please review and commit manually."
	}
	if strings.TrimSpace(r.Stdout) == "" {
		return false, paths, "Checkout is on a detached HEAD; skipped auto-committing upgrade changes — please review and commit manually."
	}
	msg := fmt.Sprintf("chore: apply spec-kitty upgrade changes (%s -> %s)", from, to)
	if _, err := gitx.Run(root, append([]string{"add", "-f", "--"}, paths...)...); err != nil {
		return false, paths, "Could not auto-commit upgrade changes; please review and commit manually."
	}
	if _, err := gitx.Run(root, append([]string{"commit", "-q", "--no-verify", "-m", msg, "--"}, paths...)...); err != nil {
		return false, paths, "Could not auto-commit upgrade changes; please review and commit manually."
	}
	warn := ""
	if b.paths[".kittify/metadata.yaml"] {
		warn = ".kittify/metadata.yaml had uncommitted changes before the upgrade; its new schema stamp was left uncommitted — commit it yourself."
	}
	return true, paths, warn
}
