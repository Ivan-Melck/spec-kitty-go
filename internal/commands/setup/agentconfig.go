package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

// LintHookCommand / LintHookMatcher are the lint_on_edit harness hook.
const (
	LintHookCommand = "spec-kitty lint --json"
	LintHookMatcher = "Edit|Write"
	enabledLabel    = "[green]enabled[/green]"
	disabledLabel   = "[yellow]disabled[/yellow]"
)

func init() {
	app.Register(func(root *cli.Command) {
		agent := app.Group(root, "agent", "Commands for AI agents to execute spec-kitty mission actions programmatically")
		g := &cli.Command{
			Name:  "config",
			Short: "Manage project AI agent configuration (add, remove, list agents)",
		}
		g.NoArgsIsHelp = true
		g.Add(
			&cli.Command{Name: "list", Short: "List configured agents and their status.", Run: runAgentConfigList},
			&cli.Command{
				Name:  "add",
				Short: "Add agents to the project.",
				Long:  "Add agents to the project.\n\nCreates agent directories and updates config.yaml.\n\nExample:\n    spec-kitty agent config add claude codex",
				Args:  []*cli.Arg{{Name: "agents", Metavar: "<str>", Help: "Agent keys to add (e.g., claude codex)", Required: true, Variadic: true}},
				Run:   runAgentConfigAdd,
			},
			&cli.Command{
				Name:  "remove",
				Short: "Remove agents from the project.",
				Long:  "Remove agents from the project.\n\nDeletes agent directories and updates config.yaml.\n\nExample:\n    spec-kitty agent config remove codex gemini",
				Args:  []*cli.Arg{{Name: "agents", Metavar: "<str>", Help: "Agent keys to remove", Required: true, Variadic: true}},
				Flags: []*cli.Flag{{Name: "keep-config", Kind: cli.Bool, Help: "Keep in config.yaml but delete directory"}},
				Run:   runAgentConfigRemove,
			},
			&cli.Command{
				Name:  "status",
				Short: "Show which agents are configured vs present on filesystem.",
				Long:  "Show which agents are configured vs present on filesystem.\n\nIdentifies:\n- Configured and present (✓)\n- Configured but missing (⚠)\n- Not configured but present (orphaned) (✗)",
				Run:   runAgentConfigStatus,
			},
			&cli.Command{
				Name:  "sync",
				Short: "Sync filesystem with config.yaml.",
				Long:  "Sync filesystem with config.yaml.\n\nBy default, removes orphaned directories (present but not configured).\nUse --create-missing to also create directories for configured agents.\nUse --sync-hooks to update harness-specific post-edit hooks.",
				Flags: []*cli.Flag{
					{Name: "create-missing", Kind: cli.Bool, Help: "Create directories for configured agents that are missing"},
					{Name: "remove-orphaned", Kind: cli.Bool, Negatable: true, NoName: "keep-orphaned", Default: true, Help: "Remove directories for agents not in config"},
					{Name: "sync-hooks", Kind: cli.Bool, Help: "Update AI harness hook configurations (Claude, Cursor, etc.)"},
					{Name: "json", Kind: cli.Bool, Help: "Emit a JSON summary listing every tracked-manifest mutation (#2691)."},
				},
				Run: runAgentConfigSync,
			},
			&cli.Command{
				Name:  "set",
				Short: "Set a project-level agent configuration value.",
				Long: "Set a project-level agent configuration value.\n\nCurrently supported keys:\n    auto_commit  - Enable/disable automatic commits by agents (true/false)\n" +
					"    lint_on_edit - Enable/disable auto-linting feedback loop (true/false)\n\nExamples:\n    spec-kitty agent config set auto_commit false\n    spec-kitty agent config set lint_on_edit true",
				Args: []*cli.Arg{
					{Name: "key", Metavar: "<str>", Help: "Configuration key (e.g., auto_commit)", Required: true},
					{Name: "value", Metavar: "<str>", Help: "Configuration value (e.g., true, false)", Required: true},
				},
				Run: runAgentConfigSet,
			},
		)
		agent.Add(g)
	})
}

// validAgents is VALID_AGENTS (AGENT_DIR_TO_KEY values ∪ skill-only agents).
func validAgents() []string {
	out := append([]string(nil), agents.Keys...)
	sort.Strings(out)
	return out
}

func isCommandAgent(key string) bool { _, ok := agents.CommandConfigs[key]; return ok }

// displayPath is agent config's _display_path: "~/..." under HOME, with a
// trailing slash.
func displayPath(p string) string {
	label := filepath.ToSlash(p)
	if rel, err := filepath.Rel(project.UserHome(), p); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		label = "~/" + filepath.ToSlash(rel)
	}
	return strings.TrimRight(label, "/") + "/"
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// agentLocation is _agent_location: (label, exists) with presence resolved
// from the static surface roots.
func agentLocation(root, key string) (string, bool) {
	switch {
	case isCommandAgent(key):
		p := agents.GlobalCommandDir(key)
		return displayPath(p) + " (global)", exists(p)
	case agents.IsCommandSkillAgent(key):
		return ".agents/skills/ (project skills)", exists(filepath.Join(root, ".agents", "skills"))
	}
	if d, ok := projectAgentDir(key); ok {
		return d[0] + "/" + d[1] + "/", exists(filepath.Join(root, d[0], d[1]))
	}
	return "unknown agent", false
}

func projectAgentDir(key string) ([2]string, bool) {
	for _, d := range agents.AgentDirs {
		if agents.AgentDirToKey[d[0]] == key {
			return d, true
		}
	}
	return [2]string{}, false
}

// projectSurfaceExists is _project_agent_surface(...)[1].exists().
func projectSurfaceExists(root, key string) bool {
	d, ok := projectAgentDir(key)
	return ok && exists(filepath.Join(root, d[0], d[1]))
}

// findRepoRoot is task_utils.find_repo_root.
func findRepoRoot(w io.Writer) (string, error) {
	if r := project.Locate(""); r != "" {
		return r, nil
	}
	out(w, "[red]Error:[/red] Unable to locate repository root (missing .git or .kittify).")
	return "", cli.Exit(1)
}

func loadAgentConfigOrExit(w io.Writer, root string, jsonOut bool) (kittify.AgentConfig, error) {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		if jsonOut {
			printJSONRaw(w, jsonError("agent_config_invalid", err.Error()))
		} else {
			out(w, "[red]Error:[/red] "+escapeMarkup(err.Error()))
		}
		return ac, cli.Exit(1)
	}
	return ac, nil
}

// jsonError is cli.json_contract.json_error.
func jsonError(code, message string) *jsonx.Object {
	return jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message))
}

// escapeMarkup is rich.markup.escape.
func escapeMarkup(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			j := i + 1
			for j < len(s) && (s[j] == '/' || s[j] == '@' || s[j] == '#' || (s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= '0' && s[j] <= '9') || s[j] == '_' || s[j] == '-' || s[j] == '.' || s[j] == ' ' || s[j] == '=') {
				j++
			}
			if j < len(s) && s[j] == ']' && j > i+1 && (s[i+1] < '0' || s[i+1] > '9') {
				b.WriteString(`\[`)
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func runAgentConfigList(ctx *cli.Context) error {
	w := ctx.Stdout
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	ac, err := loadAgentConfigOrExit(w, root, false)
	if err != nil {
		return err
	}
	if len(ac.Available) == 0 {
		out(w, "[yellow]No agents configured.[/yellow]")
		out(w, "\nRun 'spec-kitty init' or use 'spec-kitty agent config add' to add agents.")
		return nil
	}
	presence := toolsurface.Presence(root, ac.Available)
	out(w, "[cyan]Configured agents:[/cyan]")
	for _, k := range ac.Available {
		loc, _ := agentLocation(root, k)
		st := "✓"
		if !presence[k] {
			st = "⚠"
		}
		out(w, fmt.Sprintf("  %s %s (%s)", st, k, loc))
	}
	label := disabledLabel
	if ac.AutoCommit {
		label = enabledLabel
	}
	out(w, "\n[cyan]Auto-commit:[/cyan] "+label)
	if !ac.AutoCommit {
		out(w, "[dim]  Agents will stage changes but not create commits unless explicitly instructed.[/dim]")
		out(w, "[dim]  Override per-command with --auto-commit flag.[/dim]")
	}
	var notConfigured []string
	for _, k := range validAgents() {
		if !contains(ac.Available, k) {
			notConfigured = append(notConfigured, k)
		}
	}
	if len(notConfigured) > 0 {
		out(w, "\n[dim]Available but not configured:[/dim]")
		for _, k := range notConfigured {
			out(w, "  - "+k)
		}
	}
	return nil
}

func invalidAgentKeys(w io.Writer, keys []string) error {
	var invalid []string
	for _, a := range keys {
		if !agents.IsKnown(a) {
			invalid = append(invalid, a)
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	out(w, "[red]Error:[/red] Invalid agent keys: "+strings.Join(invalid, ", "))
	out(w, "\nValid agents: "+strings.Join(validAgents(), ", "))
	return cli.Exit(1)
}

// registerSkillAgent is _register_skill_agent. With sink non-nil the
// regenerated paths are collected instead of printed.
func registerSkillAgent(w io.Writer, root string, ac *kittify.AgentConfig, key string, updateConfig bool, sink *[]string) (bool, string) {
	// command_installer.install -> prepare_commands -> rendering_inputs
	// loads the agent config again.
	kittify.AgentConfigMissingWarning(root, 1)
	rep, err := agents.InstallCommandSkills(root, key, clock.NowUTCISO())
	if err == nil && key == "vibe" {
		err = agents.EnsureVibeSkillPath(root)
	}
	if err != nil {
		return false, fmt.Sprintf("Failed to install %s skills: %v", key, err)
	}
	if updateConfig {
		ac.Available = append(ac.Available, key)
	}
	out(w, fmt.Sprintf("[green]✓[/green] Registered %s (%d command skills in .agents/skills/)", key, rep.InstalledCount()))
	if sink != nil {
		*sink = append(*sink, rep.Regenerated...)
	} else {
		for _, p := range rep.Regenerated {
			plain(w, RegeneratedSurfaceLine(p))
		}
	}
	return true, ""
}

func runAgentConfigAdd(ctx *cli.Context) error {
	w := ctx.Stdout
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	ac, err := loadAgentConfigOrExit(w, root, false)
	if err != nil {
		return err
	}
	keys := ctx.ArgList("agents")
	if err := invalidAgentKeys(w, keys); err != nil {
		return err
	}
	var added, already, errs []string
	for _, k := range keys {
		if contains(ac.Available, k) {
			already = append(already, k)
			continue
		}
		if agents.IsCommandSkillAgent(k) {
			if ok, e := registerSkillAgent(w, root, &ac, k, true, nil); ok {
				added = append(added, k)
			} else if e != "" {
				errs = append(errs, e)
			}
			continue
		}
		if isCommandAgent(k) {
			ac.Available = append(ac.Available, k)
			out(w, fmt.Sprintf("[green]✓[/green] Registered %s (global commands at %s)", k, displayPath(agents.GlobalCommandDir(k))))
			added = append(added, k)
			continue
		}
		if ok, e := createProjectAgentDir(w, root, &ac, k); ok {
			added = append(added, k)
		} else if e != "" {
			errs = append(errs, e)
		}
	}
	if len(added) > 0 {
		if err := kittify.SaveAgentConfig(root, ac); err != nil {
			return err
		}
		out(w, "\n[cyan]Updated config.yaml:[/cyan] added "+strings.Join(added, ", "))
	}
	if len(already) > 0 {
		out(w, "\n[dim]Already configured:[/dim] "+strings.Join(already, ", "))
	}
	if len(errs) > 0 {
		out(w, "\n[red]Errors:[/red]")
		for _, e := range errs {
			out(w, "  - "+escapeMarkup(e))
		}
		return cli.Exit(1)
	}
	return nil
}

// createProjectAgentDir is _create_project_agent_dir (legacy project-local
// command agents; unreachable for the current registry).
func createProjectAgentDir(w io.Writer, root string, ac *kittify.AgentConfig, key string) (bool, string) {
	d, ok := projectAgentDir(key)
	if !ok {
		return false, "Unknown agent: " + key
	}
	if err := os.MkdirAll(filepath.Join(root, d[0], d[1]), 0o755); err != nil {
		return false, fmt.Sprintf("Failed to create %s/%s/: %v", d[0], d[1], err)
	}
	ac.Available = append(ac.Available, key)
	out(w, fmt.Sprintf("[green]✓[/green] Added %s/%s/", d[0], d[1]))
	return true, ""
}

// proveOwned is ManifestProver(check_command=True).prove: a file is owned
// when a skills-manifest (copy delivery) or command-skills-manifest entry
// records its current hash; a directory when every member file is owned.
func proveOwned(root, path string) bool {
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if st.IsDir() {
		members := 0
		owned := true
		filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				owned = false
				return filepath.SkipAll
			}
			if p == path || (info.IsDir() && info.Mode()&os.ModeSymlink == 0) {
				return nil
			}
			members++
			if !info.Mode().IsRegular() || !fileOwned(root, p) {
				owned = false
				return filepath.SkipAll
			}
			return nil
		})
		return owned && members > 0
	}
	return st.Mode().IsRegular() && fileOwned(root, path)
}

func fileOwned(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	h := sha256Hex(b)
	if m := agents.LoadSkillsManifest(root); m != nil {
		for _, e := range m.Entries {
			if e.InstalledPath == rel && e.DeliveryMode == "copy" && e.ContentHash == "sha256:"+h {
				return true
			}
		}
	}
	if e := agents.LoadCommandSkillsManifest(root)[rel]; e != nil && e.ContentHash == h {
		return true
	}
	return false
}

// removeProjectAgentSurface is _remove_project_agent_surface.
func removeProjectAgentSurface(root, key string) (bool, string) {
	d, ok := projectAgentDir(key)
	if !ok {
		return false, "Unknown agent: " + key
	}
	label := d[0] + "/" + d[1] + "/"
	surface := filepath.Join(root, d[0], d[1])
	if !exists(surface) {
		return false, label + " already removed"
	}
	rel := d[0] + "/" + d[1]
	if !proveOwned(root, surface) {
		return false, "Preserved " + rel + "; not package-owned (name is not ownership proof) — left in place"
	}
	if err := os.RemoveAll(surface); err != nil {
		return false, fmt.Sprintf("Failed to remove %s: %v", label, err)
	}
	if err := os.Remove(filepath.Join(root, d[0])); err == nil {
		return true, "Removed " + d[0] + "/"
	}
	return true, "Removed " + label
}

func runAgentConfigRemove(ctx *cli.Context) error {
	w := ctx.Stdout
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	ac, err := loadAgentConfigOrExit(w, root, false)
	if err != nil {
		return err
	}
	keys := ctx.ArgList("agents")
	if err := invalidAgentKeys(w, keys); err != nil {
		return err
	}
	keep := ctx.Bool("keep-config")
	var removedFromConfig, errs []string
	drop := func(k string) {
		if keep {
			return
		}
		for i, a := range ac.Available {
			if a == k {
				ac.Available = append(ac.Available[:i:i], ac.Available[i+1:]...)
				removedFromConfig = append(removedFromConfig, k)
				return
			}
		}
	}
	for _, k := range keys {
		if agents.IsCommandSkillAgent(k) {
			rep, err := agents.RemoveCommandSkills(root, k)
			if err != nil {
				errs = append(errs, fmt.Sprintf("Failed to remove skills for %s: %v", k, err))
			} else {
				out(w, fmt.Sprintf("[green]✓[/green] Removed skills for %s (%d files deleted)", k, len(rep.Deleted)))
			}
			drop(k)
			continue
		}
		if ok, msg := removeProjectAgentSurface(root, k); ok {
			out(w, "[green]✓[/green] "+escapeMarkup(msg))
		} else {
			out(w, "[dim]• "+escapeMarkup(msg)+"[/dim]")
		}
		drop(k)
	}
	if !keep && len(removedFromConfig) > 0 {
		if err := kittify.SaveAgentConfig(root, ac); err != nil {
			return err
		}
		out(w, "\n[cyan]Updated config.yaml:[/cyan] removed "+strings.Join(removedFromConfig, ", "))
	}
	if len(errs) > 0 {
		out(w, "\n[yellow]Warnings:[/yellow]")
		for _, e := range errs {
			out(w, "  - "+escapeMarkup(e))
		}
	}
	return nil
}

func runAgentConfigStatus(ctx *cli.Context) error {
	w := ctx.Stdout
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	ac, err := loadAgentConfigOrExit(w, root, false)
	if err != nil {
		return err
	}
	var rows [][]string
	var orphaned []string
	presence := toolsurface.Presence(root, validAgents())
	for _, k := range validAgents() {
		loc, _ := agentLocation(root, k)
		ok := presence[k]
		configured := contains(ac.Available, k)
		c, e := "✗", "✗"
		if configured {
			c = "✓"
		}
		if ok {
			e = "✓"
		}
		var status string
		switch {
		case configured && ok:
			status = "[green]OK[/green]"
		case configured:
			status = "[yellow]Missing[/yellow]"
		case projectSurfaceExists(root, k):
			status = "[red]Orphaned[/red]"
			orphaned = append(orphaned, k)
		default:
			status = "[dim]Not used[/dim]"
		}
		rows = append(rows, []string{k, loc, c, e, status})
	}
	t := rich.NewTable()
	t.Title = "Agent Status"
	t.AddColumn("Agent Key", rich.Column{Style: "cyan"})
	t.AddColumn("Directory", rich.Column{Style: "dim"})
	t.AddColumn("Configured", rich.Column{Justify: "center"})
	t.AddColumn("Exists", rich.Column{Justify: "center"})
	t.AddColumn("Status", rich.Column{})
	for _, r := range rows {
		t.AddRow(r[0], r[1], r[2], r[3], r[4])
	}
	rich.PrintTo(w, t)
	if len(orphaned) > 0 {
		out(w, fmt.Sprintf("\n[yellow]⚠ %d orphaned directories found[/yellow] (present but not configured)", len(orphaned)))
		out(w, "Run 'spec-kitty agent config sync --remove-orphaned' to clean up")
	}
	return nil
}

func removeOrphanedAgentDirs(w io.Writer, root string, ac kittify.AgentConfig) bool {
	out(w, "[cyan]Checking for orphaned directories...[/cyan]")
	changed := false
	keys := make([]string, 0, len(agents.AgentDirToKey))
	for _, k := range agents.AgentDirToKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if contains(ac.Available, k) || !projectSurfaceExists(root, k) {
			continue
		}
		ok, msg := removeProjectAgentSurface(root, k)
		switch {
		case ok:
			out(w, "  [green]✓[/green] Removed orphaned "+escapeMarkup(strings.TrimPrefix(msg, "Removed ")))
			changed = true
		case strings.HasPrefix(msg, "Preserved "):
			out(w, "  [yellow]⚠[/yellow] "+escapeMarkup(msg))
		default:
			out(w, "  [red]✗[/red] "+escapeMarkup(msg))
		}
	}
	return changed
}

func checkOrCreateConfiguredAgentDirs(w io.Writer, root string, ac *kittify.AgentConfig, sink *[]string) (bool, error) {
	out(w, "\n[cyan]Checking for missing directories...[/cyan]")
	changed, failed := false, false
	for _, k := range append([]string(nil), ac.Available...) {
		if isCommandAgent(k) {
			gd := agents.GlobalCommandDir(k)
			if exists(gd) {
				out(w, fmt.Sprintf("  [green]✓[/green] Global commands present for %s at %s", k, displayPath(gd)))
			} else {
				out(w, fmt.Sprintf("  [yellow]⚠[/yellow] Global commands missing for %s at %s", k, displayPath(gd)))
			}
			continue
		}
		if agents.IsCommandSkillAgent(k) {
			if skillAgentInstalled(root, k) {
				out(w, fmt.Sprintf("  [green]✓[/green] Skill commands present for %s in .agents/skills/", k))
				if k == "vibe" && !agents.VibeSkillPathConfigured(root) {
					if err := agents.EnsureVibeSkillPath(root); err != nil {
						out(w, "  [red]✗[/red] "+escapeMarkup("Failed to restore .vibe/config.toml skill_paths pointer: "+err.Error()))
						failed = true
					} else {
						out(w, "  [green]✓[/green] Restored .vibe/config.toml skill_paths pointer for vibe")
						changed = true
					}
				}
				continue
			}
			if ok, e := registerSkillAgent(w, root, ac, k, false, sink); ok {
				changed = true
			} else if e != "" {
				out(w, "  [red]✗[/red] "+escapeMarkup(e))
				failed = true
			}
			continue
		}
		d, ok := projectAgentDir(k)
		if !ok {
			out(w, "  [yellow]⚠[/yellow] Unknown agent: "+k)
			failed = true
			continue
		}
		dir := filepath.Join(root, d[0], d[1])
		if exists(dir) {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			out(w, fmt.Sprintf("  [red]✗[/red] Failed to create %s/%s/: %v", d[0], d[1], err))
			failed = true
			continue
		}
		out(w, fmt.Sprintf("  [green]✓[/green] Created %s/%s/", d[0], d[1]))
		changed = true
	}
	if failed {
		return changed, cli.Exit(1)
	}
	return changed, nil
}

// skillAgentInstalled is _skill_agent_already_installed.
func skillAgentInstalled(root, key string) bool {
	for _, e := range agents.LoadCommandSkillsManifest(root) {
		if contains(e.Agents, key) {
			return true
		}
	}
	return false
}

func syncHarnessHooks(w io.Writer, root string, ac kittify.AgentConfig) (bool, error) {
	out(w, "\n[cyan]Syncing harness hooks...[/cyan]")
	changed := false
	if contains(ac.Available, "claude") {
		c, err := syncClaudeHooks(w, root, ac.LintOnEdit)
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	if contains(ac.Available, "cursor") {
		c, err := syncCursorHooks(w, root, ac.LintOnEdit)
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	return changed, nil
}

func syncClaudeHooks(w io.Writer, root string, enabled bool) (bool, error) {
	already := agents.HookRegistered(root, "PostToolUse", LintHookCommand)
	changed := false
	var err error
	switch {
	case enabled && !already:
		_, err = agents.RegisterClaudeHook(root, "PostToolUse", LintHookCommand, LintHookMatcher)
		changed = true
	case !enabled && already:
		_, err = agents.UnregisterClaudeHook(root, "PostToolUse", LintHookCommand)
		changed = true
	}
	if err != nil {
		return false, err
	}
	if changed {
		verb := "Removed"
		if enabled {
			verb = "Updated"
		}
		out(w, "  [green]✓[/green] "+verb+" Claude Code lint hook")
	} else {
		out(w, "  [dim]• Claude Code hooks already in sync[/dim]")
	}
	return changed, nil
}

func syncCursorHooks(w io.Writer, root string, enabled bool) (bool, error) {
	p := filepath.Join(root, ".cursor", "hooks.json")
	data := jsonx.NewObject("version", 1, "hooks", &jsonx.Object{})
	if b, err := os.ReadFile(p); err == nil {
		v, derr := jsonx.Decode(b)
		if derr != nil {
			out(w, fmt.Sprintf("  [yellow]![/yellow] Cursor hooks file is unreadable (%s); leaving it untouched. Fix or remove it, then re-run.", escapeMarkup(derr.Error())))
			return false, nil
		}
		if o, ok := v.(*jsonx.Object); ok {
			data = o
		}
	}
	hooks := data.GetObject("hooks")
	if hooks == nil {
		hooks = &jsonx.Object{}
		data.Set("hooks", hooks)
	}
	after := hooks.GetList("afterFileEdit")
	idx := -1
	for i, h := range after {
		if ho, ok := h.(*jsonx.Object); ok && ho.GetString("command") == LintHookCommand {
			idx = i
			break
		}
	}
	changed := false
	switch {
	case enabled && idx == -1:
		after = append(after, jsonx.NewObject("command", LintHookCommand))
		changed = true
	case !enabled && idx != -1:
		after = append(after[:idx:idx], after[idx+1:]...)
		changed = true
	}
	if !changed {
		out(w, "  [dim]• Cursor hooks already in sync[/dim]")
		return false, nil
	}
	hooks.Set("afterFileEdit", after)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(p, []byte(jsonx.Dumps(data, jsonx.Pretty)+"\n"), 0o644); err != nil {
		return false, err
	}
	verb := "Removed"
	if enabled {
		verb = "Updated"
	}
	out(w, "  [green]✓[/green] "+verb+" Cursor lint hook")
	return true, nil
}

func runAgentConfigSync(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root := project.Locate("")
	if root == "" {
		msg := "Unable to locate repository root (missing .git or .kittify)."
		if jsonOut {
			printJSONRaw(w, jsonError("not_in_project", msg))
		} else {
			out(w, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	ac, err := loadAgentConfigOrExit(w, root, jsonOut)
	if err != nil {
		return err
	}
	mp := filepath.Join(root, ".kittify", agents.CommandSkillsManifestFile)
	before, beforeErr := os.ReadFile(mp)
	changed := false
	var regenerated []string
	var sink *[]string
	if jsonOut {
		sink = &regenerated
	}
	if ctx.Bool("remove-orphaned") {
		changed = removeOrphanedAgentDirs(w, root, ac) || changed
	}
	if ctx.Bool("create-missing") {
		c, err := checkOrCreateConfiguredAgentDirs(w, root, &ac, sink)
		if err != nil {
			return err
		}
		changed = c || changed
	}
	if ctx.Bool("sync-hooks") {
		c, err := syncHarnessHooks(w, root, ac)
		if err != nil {
			return err
		}
		changed = c || changed
	}
	after, afterErr := os.ReadFile(mp)
	mutations := []any{}
	if (beforeErr == nil) != (afterErr == nil) || string(before) != string(after) {
		mutations = append(mutations, ".kittify/command-skills-manifest.json")
	}
	if jsonOut {
		regen := []any{}
		for _, p := range regenerated {
			regen = append(regen, p)
		}
		// console.print(json.dumps(..., indent=2)): markup and highlighting.
		out(w, jsonx.Dumps(jsonx.NewObject("changes_made", changed, "tracked_mutations", mutations, "regenerated", regen), jsonx.Pretty))
		return nil
	}
	if !changed {
		out(w, "[dim]No changes needed - filesystem matches config[/dim]")
	} else {
		out(w, "\n[green]✓ Sync complete[/green]")
	}
	return nil
}

// parseBoolValue is _parse_bool_value.
func parseBoolValue(v string) (bool, bool) {
	if truthy(v) {
		return true, true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}

func runAgentConfigSet(ctx *cli.Context) error {
	w := ctx.Stdout
	root, err := findRepoRoot(w)
	if err != nil {
		return err
	}
	ac, err := loadAgentConfigOrExit(w, root, false)
	if err != nil {
		return err
	}
	key, value := ctx.Arg("key"), ctx.Arg("value")
	switch key {
	case "auto_commit", "lint_on_edit":
		v, ok := parseBoolValue(value)
		if !ok {
			out(w, fmt.Sprintf("[red]Error:[/red] Invalid value for %s: '%s'. Use 'true' or 'false'.", key, escapeMarkup(value)))
			return cli.Exit(1)
		}
		if key == "auto_commit" {
			ac.AutoCommit = v
		} else {
			ac.LintOnEdit = v
		}
		if err := kittify.SaveAgentConfig(root, ac); err != nil {
			return err
		}
		label := disabledLabel
		if v {
			label = enabledLabel
		}
		out(w, fmt.Sprintf("[green]✓[/green] %s set to %s", key, label))
		if key == "auto_commit" && !v {
			out(w, "[dim]Agents will stage changes but not create commits unless explicitly instructed.[/dim]")
			out(w, "[dim]Per-command flags (--auto-commit/--no-auto-commit) override this setting.[/dim]")
		}
		if key == "lint_on_edit" && v {
			out(w, "[cyan]Tip:[/cyan] Run 'spec-kitty agent config sync --sync-hooks' to update harness configurations.")
		}
		return nil
	}
	out(w, fmt.Sprintf("[red]Error:[/red] Unknown configuration key: '%s'", escapeMarkup(key)))
	out(w, "\nSupported keys:")
	out(w, "  auto_commit  - Enable/disable automatic commits by agents (true/false)")
	out(w, "  lint_on_edit - Enable/disable auto-linting feedback loop (true/false)")
	return cli.Exit(1)
}
