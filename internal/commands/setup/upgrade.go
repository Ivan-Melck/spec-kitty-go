package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/migrationledger"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
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
	if t.Compare(f) < 0 {
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

func cliModePlan(root string, dryRun, noNag bool) CompatPlan {
	raw := []string{"--cli"}
	if dryRun {
		raw = append(raw, "--dry-run")
	}
	inv := Invocation{CommandPath: []string{"upgrade"}, RawArgs: raw, FlagNoNag: noNag, EnvCI: isCIEnv(), StdoutTTY: stdoutIsTTY()}
	return PlanCompatWith(inv, root, CompatOptions{ReadOnly: true, QueryLatest: true})
}

func runUpgrade(ctx *cli.Context) error {
	w := ctx.Stdout
	dryRun, jsonOut, planJSON := ctx.Bool("dry-run"), ctx.Bool("json"), ctx.Bool("plan-json")
	if !jsonOut {
		toolsurface.LogWriter = ctx.Stderr
		defer func() { toolsurface.LogWriter = nil }()
	}
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
			p := PlanCompatWith(Invocation{CommandPath: []string{"upgrade"}, RawArgs: []string{"--cli", "--project"}, FlagNoNag: true, EnvCI: true}, cwd, CompatOptions{ReadOnly: true}).JSON()
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
		return runAgentCheck(w, jsonOut)
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
		recordAgentChoice(*agentChoice, *agentLatest)
		if jsonOut {
			fmt.Fprintln(w, prettyJSON(jsonx.NewObject("status", "recorded", "choice", *agentChoice, "latest_version", *agentLatest)))
		} else {
			fmt.Fprintln(w, "Recorded "+*agentChoice)
		}
		return nil
	}
	noNag := ctx.Bool("no-nag")
	if cliMode {
		return runCLIMode(w, cwd, jsonOut, dryRun, noNag)
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
		return runCLIMode(w, cwd, jsonOut, dryRun, noNag)
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
			fmt.Fprintln(w, prettyJSON(PlanCompat(Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: []string{"--project"}, FlagNoNag: true}, root).JSON()))
		} else {
			out(w, fmt.Sprintf("[red]Error:[/red] This project uses Spec Kitty project schema %d, but this CLI supports up to schema %d.", *v, kittify.MaxSupportedSchema))
			out(w, "[cyan]Upgrade your CLI:[/cyan] "+unknownInstallNote)
		}
		return cli.Exit(5)
	}
	if jsonOut && (projectMode || dryRun) {
		return runPlannerJSON(w, root, targetVersion, dryRun, noNag)
	}
	return runProjectUpgrade(ctx, root, targetVersion, upgradeOptions{dryRun: dryRun, jsonOut: jsonOut, verbose: ctx.Bool("verbose"), confirmed: confirmed, noWorktrees: noWorktrees})
}

// project0 resolves the project the way the read-only intent resolver does.
func project0(cwd string) string {
	if isInProject(cwd) {
		return cwd
	}
	return ""
}

func runCLIMode(w io.Writer, cwd string, jsonOut, dryRun, noNag bool) error {
	root := projectRootOrHome(cwd)
	p := cliModePlan(root, dryRun, noNag)
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

func runPlannerJSON(w io.Writer, root, targetVersion string, dryRun, noNag bool) error {
	raw := []string{"--project"}
	if dryRun {
		raw = append(raw, "--dry-run")
	}
	inv := Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: raw, FlagNoNag: noNag, EnvCI: isCIEnv(), StdoutTTY: stdoutIsTTY()}
	p := PlanCompatWith(inv, root, CompatOptions{ReadOnly: true, QueryLatest: true})
	if p.PendingError != nil {
		return p.PendingError
	}
	p.Pending = nil
	payload := p.JSON()
	payload.Set("rendered_human", "")
	semantic := p.ExitCode
	current := detectProjectVersion(root)
	verr := validateUpgradeTarget(current, targetVersion)
	if verr != "" {
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
		steps, err := migrationSteps(root, targetVersion)
		if err != nil {
			return err
		}
		payload.Set("pending_migrations", steps)
	}
	code := semantic
	if dryRun && semantic != 5 {
		code = 0
	}
	if dryRun && verr == "" && semantic != 2 && semantic != 5 && semantic != 6 {
		notice, incomplete := supportingRepairNotice(root)
		if notice != "" {
			notice = truncate(notice, 1023)
			compat := payload.GetString("rendered_human")
			keep := 1023 - len(notice)
			if keep < 0 {
				keep = 0
			}
			payload.Set("rendered_human", truncate(compat, keep)+"\n"+notice)
		}
		if incomplete {
			code = 1
			payload.Set("exit_code", code)
		}
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
	return PlanCompatWith(Invocation{CommandPath: []string{"__project_compat_check__"}, RawArgs: []string{"--project", "--plan-json"}, FlagNoNag: true, EnvCI: true}, root, CompatOptions{ReadOnly: true}).JSON()
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
			switch t.Compare(c) {
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
	prepared, err := toolsurface.PrepareUpgradeRepairs(root)
	var effects []any
	if err == nil {
		effects, err = prepared.PlanEffectsJSON()
	}
	if err != nil {
		p.Set("decision", "incomplete")
		p.Set("process_exit_code", 1)
		p.Set("diagnostics", []any{jsonx.NewObject("code", "assessment_failed", "owner", nil, "severity", "error", "message", preparationErrorText(err))})
		fmt.Fprintln(w, prettyJSON(p))
		return cli.Exit(1)
	}
	p.Set("roots", prepared.PlanRootsJSON())
	steps, err := migrationSteps(root, target)
	if err != nil {
		return err
	}
	p.Set("migrations", steps)
	p.Set("effects", effects)
	p.Set("dispositions", prepared.PlanDispositionsJSON())
	p.Set("diagnostics", prepared.PlanDiagnosticsJSON())
	p.Set("execution_artifacts", prepared.PlanArtifactsJSON())
	complete := prepared.Complete()
	p.Set("complete", complete)
	code := 0
	if complete {
		p.Set("decision", "ready")
	} else {
		p.Set("decision", "incomplete")
		code = 1
	}
	p.Set("process_exit_code", code)
	p.Set("commit_policy", jsonx.NewObject("project_enabled", kittify.AutoCommit(root), "worktrees_enabled", !noWorktrees, "mission_repair_included", false))
	fmt.Fprintln(w, prettyJSON(p))
	return exitOrNil(code)
}

// preparationErrorText is upgrade._preparation_error_text.
func preparationErrorText(err error) string {
	if e, ok := err.(*kittify.DefaultPresetError); ok {
		return fmt.Sprintf("Error (%s): %s", e.Code(), e.Body)
	}
	var ce *dct.ConfigError
	if errors.As(err, &ce) && ce.Body != "" {
		return ce.Body
	}
	return err.Error()
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
	dryRun, jsonOut, verbose, confirmed, noWorktrees bool
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
	resultOrder        []string
	manualReview       []string
	worktreeFailures   []string
	// previewIncomplete / surfaceRepairMessages are the dry-run preview's
	// SurfaceRepairReport (preview_incomplete, failure_messages).
	previewIncomplete     bool
	surfaceRepairMessages []string
	// The real-run surface repair (SurfaceRepairReport / UpgradeOutcome).
	repairPreparationErrors []string
	surfaceRepairFailed     bool
	driftedPaths            []string
	driftedReasons          map[string]string
	projectRoot             string
}

// collectManualReviewPaths is upgrade._collect_manual_review_paths.
func (o *upgradeOutcome) collectManualReviewPaths() {
	set := map[string]bool{}
	for _, id := range o.resultOrder {
		if r := o.results[id]; r.ManualReview {
			for _, p := range r.Preserved {
				set[p] = true
			}
		}
	}
	o.manualReview = sortedKeys(set)
}

func (o *upgradeOutcome) reasons() []string {
	var r []string
	if !o.success {
		r = append(r, "migration_failed")
	}
	if len(o.activationErrors) > 0 {
		r = append(r, "activation_error")
	}
	if len(o.worktreeFailures) > 0 {
		r = append(r, "worktree_failure")
	}
	if len(o.repairPreparationErrors) > 0 {
		r = append(r, "repair_preparation_failed")
	}
	if len(o.surfaceRepairFails) > 0 || o.surfaceRepairFailed {
		r = append(r, "surface_repair_failed")
	}
	if o.previewIncomplete {
		r = append(r, "preview_incomplete")
	}
	if len(o.driftedPaths) > 0 {
		r = append(r, "surface_drift")
	}
	return r
}

func (o *upgradeOutcome) kind() string {
	reasons := o.reasons()
	if len(reasons) == 0 {
		if o.hadMigrations {
			return "applied"
		}
		return "no_op"
	}
	if len(reasons) == 1 && reasons[0] == "surface_drift" {
		return "drift_unresolved"
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
	all = append(all, o.worktreeFailures...)
	all = append(all, o.repairPreparationErrors...)
	all = append(all, o.surfaceRepairFails...)
	all = append(all, o.surfaceRepairMessages...)
	if len(o.surfaceRepairMessages) == 0 {
		if len(o.surfaceRepairFails) > 0 || o.surfaceRepairFailed {
			all = append(all, "Tool-surface repair did not complete; re-run 'spec-kitty upgrade'.")
		}
		if o.previewIncomplete {
			all = append(all, "The upgrade preview could not be completed.")
		}
	}
	all = append(all, o.preservedFileMessages()...)
	seen := map[string]bool{}
	var out []string
	for _, e := range all {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		if r := o.reasons(); len(r) > 0 {
			switch r[0] {
			case "migration_failed":
				out = append(out, "The migrations did not complete.")
			case "commit_recovery_failed":
				out = append(out, "The upgrade changes could not be committed cleanly.")
			case "surface_repair_failed":
				out = append(out, "Tool-surface repair did not complete; re-run 'spec-kitty upgrade'.")
			case "preview_incomplete":
				out = append(out, "The upgrade preview could not be completed.")
			}
		}
	}
	return out
}

// preservedFileMessages is UpgradeOutcome._preserved_file_messages.
func (o *upgradeOutcome) preservedFileMessages() []string {
	if len(o.driftedPaths) == 0 {
		return nil
	}
	shown := o.driftedPaths
	if len(shown) > 20 {
		shown = shown[:20]
	}
	var lines []string
	for _, p := range shown {
		disp := p
		if o.projectRoot != "" {
			if r, err := filepath.Rel(o.projectRoot, p); err == nil && !strings.HasPrefix(r, "..") {
				disp = r
			}
		}
		if reason, ok := o.driftedReasons[p]; ok {
			lines = append(lines, "Not updated, your local edit was kept: "+disp+" ("+reason+")")
		} else {
			lines = append(lines, "Not updated, your local edit was kept: "+disp)
		}
	}
	if len(o.driftedPaths) > len(shown) {
		lines = append(lines, fmt.Sprintf("... and %d more", len(o.driftedPaths)-len(shown)))
	}
	return append(lines, "To take the current version of a file, delete it and run 'spec-kitty upgrade' again. "+
		"To keep your edit, leave the file as it is. 'spec-kitty doctor tool-surfaces' shows each file's state.")
}

func (o *upgradeOutcome) closingLine() string {
	switch o.kind() {
	case "drift_unresolved":
		return fmt.Sprintf("Upgrade finished, but %d managed file(s) with local edits were not updated.", len(o.driftedPaths))
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
	migrations, err := applicableMigrations(from, target, root)
	if err != nil {
		return err
	}
	o := &upgradeOutcome{from: current, to: target, dryRun: opt.dryRun, success: true, results: map[string]migrationResult{}, mergeDriverConfig: "skipped", projectRoot: root}
	now := clock.NowUTCISO()
	stamp := kittify.VersionStamp{}
	if !opt.dryRun {
		stamp = kittify.CaptureVersionStamp(root)
	}
	wtAutoCommit := !opt.dryRun && kittify.AutoCommit(root)
	if len(migrations) == 0 {
		if !opt.dryRun {
			if err := kittify.SetMetadataVersion(root, target, now); err != nil {
				return err
			}
			kittify.StampSchemaVersion(root)
		}
		if !opt.noWorktrees && current == target {
			wr := upgradeWorktrees(root, target, nil, opt.dryRun, wtAutoCommit)
			o.warnings = append(o.warnings, wr.warnings...)
			if len(wr.errors) > 0 {
				o.warnings = append(o.warnings, wr.errors...)
				o.warnings = append(o.warnings, "Some worktrees had issues - check errors above")
			}
			o.worktreeFailures = append(o.worktreeFailures, wr.failures...)
		}
	} else {
		o.hadMigrations = true
		if !opt.jsonOut {
			t := rich.NewTable()
			t.Title, t.HeaderStyle = "Migration Plan", "bold cyan"
			t.AddColumn("Migration", rich.Column{Style: "bright_white"})
			t.AddColumn("Description", rich.Column{Style: "dim"})
			t.AddColumn("Target", rich.Column{Style: "cyan"})
			for _, m := range migrations {
				t.AddRow(m.ID, m.Description, m.TargetVersion)
			}
			rich.PrintTo(w, t)
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
		if !opt.noWorktrees {
			wr := upgradeWorktrees(root, target, migrations, opt.dryRun, wtAutoCommit)
			o.warnings = append(o.warnings, wr.warnings...)
			if len(wr.errors) > 0 {
				o.errors = append(o.errors, wr.errors...)
				o.warnings = append(o.warnings, "Some worktrees had issues - check errors above")
			}
			o.worktreeFailures = append(o.worktreeFailures, wr.failures...)
		}
		o.collectManualReviewPaths()
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
			if n, incomplete := supportingRepairNotice(root); incomplete {
				o.previewIncomplete = true
				o.surfaceRepairMessages = append(o.surfaceRepairMessages, n)
			} else if n != "" && !opt.jsonOut {
				plain(w, n)
			}
		} else {
			finalizeSurfaceRepair(root, o)
		}
	}
	commitWarned := false
	if shouldCommit && len(o.activationErrors) == 0 && len(o.repairPreparationErrors) == 0 && !o.mainCheckoutRunFailed() {
		committed, paths, warn := commitUpgradeChurn(root, baseline, o.from, o.to)
		o.committed, o.commitPaths = committed, paths
		if warn != "" {
			o.warnings = append(o.warnings, warn)
			commitWarned = true
		}
	}
	if o.kind() == "failed" && stamp.Restore(root) && !opt.jsonOut {
		v := stamp.Version()
		if v == "" {
			v = "its previous version"
		}
		out(w, "[yellow].kittify/metadata.yaml restored to "+escapeMarkup(v)+"; the change is uncommitted[/yellow]")
	}
	if !opt.dryRun && o.success && len(o.manualReview) > 0 {
		joined := strings.Join(o.manualReview, ", ")
		if o.committed {
			o.warnings = append(o.warnings, fmt.Sprintf(heldForReviewNotCommittedFmt, joined))
		} else {
			o.warnings = append(o.warnings, fmt.Sprintf(heldForReviewSkippedCommitFmt, len(o.manualReview), joined))
		}
	}
	if !commitWarned {
		if reason := noCommitReason(root, o, baseline, opt.dryRun, shouldCommit); reason != "" {
			o.warnings = append(o.warnings, reason)
		}
	}
	leftUncommitted := !opt.jsonOut && !opt.dryRun && o.success && !shouldCommit && !o.committed && baseline != nil && len(upgradeCommitCandidates(root, baseline)) > 0
	if opt.jsonOut {
		fmt.Fprintln(w, compactJSON(upgradeJSON(o, migrations)))
	} else {
		renderUpgradeText(w, root, o, leftUncommitted, opt.dryRun)
	}
	if k := o.kind(); k != "applied" && k != "no_op" {
		return cli.Exit(1)
	}
	return nil
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
	var recordErr error
	record := func(id, result string, notes *string) {
		if o.dryRun {
			return
		}
		m, _ := migrationByID(id)
		records, err := readMigrationRecords(root, m.CloneLocal)
		if err != nil {
			recordErr = err
			return
		}
		for _, old := range records {
			if old.ID == id && old.Result == result {
				return
			}
		}
		if err := (migrationledger.Ledger{Root: root}).Append(migrationRecord(m, result, "")); err != nil {
			recordErr = err
			return
		}
		md.RecordMigration(id, result, notes)
		_, recordErr = md.Save(root)
	}
	var insideRepository *bool
	var repositoryErr error
	for _, m := range migrations {
		var res migrationResult
		status := ""
		needed, derr := false, error(nil)
		recorded := migrationSettled(root, m) && !m.reselectWhenRecorded(root)
		outside := false
		if !recorded && m.RequiresGit {
			if insideRepository == nil && repositoryErr == nil {
				inside, err := migrationRepositoryProbe(root)
				insideRepository, repositoryErr = &inside, err
			}
			outside = repositoryErr == nil && !*insideRepository
		}
		if !recorded && !outside && repositoryErr == nil {
			needed, derr = m.detectErr(root)
		}
		switch {
		case recorded:
			res = migrationResult{Success: true, Warnings: []string{fmt.Sprintf("Migration %s already applied, skipping", m.ID)}}
			status = "skipped"
		case m.RequiresGit && repositoryErr != nil:
			reason := "Cannot determine whether the project is inside a git repository: " + repositoryErr.Error()
			record(m.ID, "failed", &reason)
			res = migrationResult{Errors: []string{reason + " (" + m.ID + ")"}}
			status = "failed"
		case outside:
			res = migrationResult{Success: true, Warnings: []string{fmt.Sprintf("Migration %s not applicable here (not inside a git repository); skipping", m.ID)}}
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
				res = migrationResult{Success: true, Warnings: []string{fmt.Sprintf("Migration %s not applicable here (%s); skipping", m.ID, reason)}}
				status = "skipped"
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
		if recordErr != nil {
			res.Success = false
			res.Errors = append(res.Errors, recordErr.Error())
			status = "failed"
		}
		o.warnings = append(o.warnings, res.Warnings...)
		if _, seen := o.results[m.ID]; !seen {
			o.resultOrder = append(o.resultOrder, m.ID)
		}
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

// finalizeSurfaceRepair is the finalizer's preparation, provisioning and
// surface-repair steps (prepare_upgrade_repairs, the preflight,
// _finalizer_step_provision and _apply_prepared_surface_repairs).
func finalizeSurfaceRepair(root string, o *upgradeOutcome) {
	prepared, err := toolsurface.PrepareUpgradeRepairs(root)
	switch {
	case err != nil:
		o.repairPreparationErrors = []string{preparationErrorText(err)}
	case !prepared.Complete():
		msgs := prepared.ErrorMessages()
		if len(msgs) == 0 {
			msgs = []string{"Required repair assessment is incomplete"}
		}
		o.repairPreparationErrors = msgs
	}
	if len(o.repairPreparationErrors) > 0 {
		return
	}
	if err := prepared.ApplyProvisioning(); err != nil {
		o.activationErrors = append(o.activationErrors, err.Error())
		return
	}
	rep := prepared.Apply()
	o.errors = append(o.errors, rep.Errors...)
	o.created = append(o.created, rep.Created...)
	o.repaired = append(o.repaired, rep.Repaired...)
	o.regenerated = append(o.regenerated, rep.Regenerated...)
	o.driftedPaths = rep.DriftedPaths
	o.driftedReasons = rep.DriftedReasons
	o.surfaceRepairFailed = rep.Failed
	o.surfaceRepairMessages = rep.FailureMessages
}

// supportingRepairNotice is _supporting_repair_preview: the count of the
// canonical retained effects a real run would apply (incomplete=true when
// the preparation could not be completed).
func supportingRepairNotice(root string) (string, bool) {
	const hint = "Use --plan-json for full repair details."
	clause := func(s string) string { return strings.TrimRight(strings.TrimRight(s, " \t\n\r"), ".") }
	prepared, err := toolsurface.PrepareUpgradeRepairs(root)
	var effects []toolsurface.Effect
	if err == nil && !prepared.Complete() {
		detail := truncate(strings.Join(prepared.ErrorMessages(), "; "), 350)
		if c := clause(detail); c != "" {
			return "Supporting repair preview incomplete: " + c + ". " + hint, true
		}
		return "Supporting repair preview incomplete: Required owner assessment incomplete. " + hint, true
	}
	if err == nil {
		effects, err = prepared.Effects()
	}
	if err != nil {
		return "Supporting repair preview incomplete: " + clause(truncate(preparationErrorText(err), 350)) + ". " + hint, true
	}
	preserved := prepared.PreservedCount()
	if len(effects) == 0 && preserved == 0 {
		return "", false
	}
	var lines []string
	if len(effects) > 0 {
		manifests := 0
		for _, e := range effects {
			if e.After.Kind != "directory" && strings.Contains(strings.ToLower(path.Base(e.Path)), "manifest") {
				manifests++
			}
		}
		lines = append(lines, fmt.Sprintf("Would repair %d supporting surface paths (including %d manifests).", len(effects), manifests))
	}
	if preserved > 0 {
		lines = append(lines, fmt.Sprintf("Would preserve %d paths requiring separate consent.", preserved))
	}
	return strings.Join(append(lines, hint), " "), false
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
		"drifted_overwritten", []any{}, "drifted_reported", strList(o.driftedPaths), "skipped", []any{})
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
		mr, pp := false, []any{}
		if r, ok := o.results[m.ID]; ok {
			mr, pp = r.ManualReview, strList(r.Preserved)
		}
		detail = append(detail, jsonx.NewObject("id", m.ID, "description", m.Description, "target_version", m.TargetVersion, "status", st,
			"manual_review_required", mr, "preserved_paths", pp))
		if r, ok := o.results[m.ID]; ok && len(r.Changes) > 0 {
			if v, err := jsonx.Decode([]byte(r.Changes[0])); err == nil {
				reports.Set(m.ID, v)
			}
		}
	}
	return jsonx.NewObject("status", o.status(), "outcome", o.kind(), "failure_reasons", reasons, "current_version", o.from, "target_version", o.to,
		"dry_run", o.dryRun, "migrations", detail, "migrations_applied", strList(o.applied), "migrations_skipped", strList(o.skipped),
		"migration_reports", reports, "success", len(o.reasons()) == 0, "errors", strList(o.errorList()), "warnings", strList(o.warnings),
		"manual_review_required", len(o.manualReview) > 0, "manual_review_paths", strList(o.manualReview), "auto_committed", o.committed, "auto_commit_paths", strList(o.commitPaths),
		"merge_driver_config", o.mergeDriverConfig, "surface_repair", surface)
}

func printSection(w io.Writer, header string, items []string, prefix string) {
	if len(items) == 0 {
		return
	}
	out(w, header)
	for _, it := range items {
		softWrap(w, prefix+escapeMarkup(it))
	}
}

// closingStyle is _CLOSING_STYLES[kind] (a completed dry run: bold yellow).
func (o *upgradeOutcome) closingStyle() string {
	switch o.kind() {
	case "applied":
		if o.dryRun {
			return "bold yellow"
		}
		return "bold green"
	case "no_op":
		return "green"
	}
	return "bold red"
}

func renderUpgradeText(w io.Writer, root string, o *upgradeOutcome, leftUncommitted, dryRun bool) {
	if o.hadMigrations {
		fmt.Fprintln(w)
		if o.dryRun {
			panelStyled(w, "[yellow]DRY RUN[/yellow] - No changes were made", "", "yellow", 0, 1)
		}
		printSection(w, "[green]Migrations applied:[/green]", o.applied, "  [green]✓[/green] ")
		printSection(w, "[dim]Migrations skipped (already applied or not needed):[/dim]", o.skipped, "  [dim]○[/dim] ")
	}
	if o.mergeDriverConfig == "installed" {
		printStyled(w, "Installed clone-local merge-driver settings", "dim")
	}
	for _, p := range o.regenerated {
		printStyled(w, RegeneratedSurfaceLine(p), "")
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
		printStyled(w, o.closingLine(), o.closingStyle())
		for _, x := range o.warnings {
			softWrap(w, "[yellow]Warning:[/yellow] "+escapeMarkup(x))
		}
		for _, x := range o.errorList() {
			softWrap(w, "[red]Error:[/red] "+escapeMarkup(x))
		}
		commitLine()
	} else {
		for _, id := range o.resultOrder {
			ch := o.results[id].Changes
			if len(ch) == 0 {
				continue
			}
			if v, err := jsonx.Decode([]byte(ch[0])); err == nil {
				if _, ok := v.(*jsonx.Object); ok {
					printSection(w, "[cyan]"+escapeMarkup(id)+":[/cyan]", ch[1:], "  [cyan]•[/cyan] ")
				}
			}
		}
		printSection(w, "[yellow]Warnings:[/yellow]", o.warnings, "  [yellow]![/yellow] ")
		printSection(w, "[red]Errors:[/red]", o.errorList(), "  [red]✗[/red] ")
		printSection(w, "[yellow]Manual review required:[/yellow]", o.manualReview, "  [yellow]![/yellow] ")
		success := o.kind() == "applied"
		if !o.dryRun && !success {
			commitLine()
		}
		fmt.Fprintln(w)
		printStyled(w, o.closingLine(), o.closingStyle())
		if !o.dryRun && success {
			commitLine()
		}
	}
	if !o.hadMigrations && !noticeBefore {
		printDryRunProvisioningNotice(w, root, dryRun)
	}
}
