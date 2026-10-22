package setup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const initDoc = `Initialize a new Spec Kitty project.

Creates project files only. Does not initialize a git repository.
Does not create any commits.

If PROJECT_NAME is omitted, init runs in the current directory.
Re-running init in an already-initialized directory is idempotent for project
state: it verifies the configured agents' skill surfaces, additively restoring
missing per-agent skill roots (e.g. .claude/skills/) through the canonical
installer, and exits 1 with the recovery command
` + "`spec-kitty agent config sync --create-missing --keep-orphaned`" + ` when shared
command skills (codex/vibe/pi/letta) are missing or empty. An existing
per-agent skill file that is empty, a directory, or a symlink exits 1 naming
its path and is preserved untouched — rename or remove it and re-run init.

Note: The --no-git flag from previous versions has been removed.
      init never touches git state regardless of flags.

Interactive Mode (default):
- Prompts you to select AI assistants

Non-Interactive Mode:
- Enabled with --non-interactive/--yes, SPEC_KITTY_NON_INTERACTIVE=1, or non-TTY
- Skips all prompts; --ai is required
- Perfect for CI/CD and automation

What Gets Created:
- .kittify/ - Project scaffold (memory, config)
- Agent command and skill surfaces (.claude/commands/, .agents/skills/, etc.)
- .gitignore and .claudeignore

Specifying AI Assistants (--ai flag):
Use comma-separated agent keys (no spaces). Valid keys include:
codex, claude, gemini, cursor, qwen, opencode, windsurf, kilocode,
auggie, copilot, q, kiro, antigravity, vibe, pi, letta, llxprt.

Template Discovery (Development Mode):
Set SPEC_KITTY_TEMPLATE_ROOT to override bundled templates for local development.

Examples:
  spec-kitty init --ai codex                    # Current directory (default)
  spec-kitty init my-project                    # Interactive mode
  spec-kitty init my-project --ai codex         # With Codex
  spec-kitty init my-project --ai codex,claude  # Multiple agents
  spec-kitty init --ai claude --non-interactive # Non-interactive

Canonical Next Steps (after init):
  spec-kitty next --agent <agent> --mission <slug>         # Enter mission loop
  spec-kitty agent action implement <WP> --agent <name>   # Implement a work package
  spec-kitty agent action review    <WP> --agent <name>   # Review a work package

Missions:
- Missions are selected per-feature during /spec-kitty.specify`

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "init",
			Short: "Initialize a new Spec Kitty project.",
			Long:  initDoc,
			Args:  []*cli.Arg{{Name: "project_name", Metavar: "project_name", Help: "Name for your new project directory (omit to initialize current directory)"}},
			Flags: []*cli.Flag{
				{Name: "ai", Kind: cli.String, Metavar: "<str>", Panel: "Selection", Help: "Comma-separated AI assistants (claude,codex,gemini,...)"},
				{Name: "non-interactive", Aliases: []string{"yes"}, Kind: cli.Bool, Help: "Run without interactive prompts (suitable for CI/CD)"},
			},
			Run: runInit,
		})
	})
}

var commandSkillAgentSet = map[string]bool{"codex": true, "vibe": true, "pi": true, "letta": true}

const pendingCommandSkills = ".kittify/init-command-skills.pending.json"

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func insideGitWorkTree(dir string) bool {
	r, err := gitx.Exec(dir, nil, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && r.Code == 0 && strings.TrimSpace(r.Stdout) == "true"
}

// parseAgentSelection splits, lowercases and dedupes an --ai value.
func parseAgentSelection(raw string) (selected, invalid []string, empty bool) {
	var parts []string
	for _, p := range strings.Split(strings.ReplaceAll(raw, ";", ","), ",") {
		if t := strings.ToLower(strings.TrimSpace(p)); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return nil, nil, true
	}
	seen := map[string]bool{}
	for _, k := range parts {
		if !agents.IsKnown(k) {
			invalid = append(invalid, k)
			continue
		}
		if !seen[k] {
			seen[k] = true
			selected = append(selected, k)
		}
	}
	return selected, invalid, false
}

func agentDisplayList(keys []string) string {
	var d []string
	for _, k := range keys {
		d = append(d, agents.Display(k))
	}
	return strings.Join(d, ", ")
}

func readPending(root string) ([]string, bool, error) {
	p := filepath.Join(root, pendingCommandSkills)
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, fmt.Errorf("Unsafe pending command-delivery record: %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	var data map[string]any
	if json.Unmarshal(b, &data) != nil || len(data) != 2 || data["schema_version"] != float64(1) {
		return nil, false, fmt.Errorf("Unrecognized pending command-delivery record: %s", p)
	}
	l, ok := data["agents"].([]any)
	if !ok || len(l) == 0 {
		return nil, false, fmt.Errorf("Invalid pending command-delivery agents: %s", p)
	}
	var out []string
	for _, x := range l {
		s, ok := x.(string)
		if !ok || !commandSkillAgentSet[s] {
			return nil, false, fmt.Errorf("Invalid pending command-delivery agents: %s", p)
		}
		out = append(out, s)
	}
	if !sort.StringsAreSorted(out) {
		return nil, false, fmt.Errorf("Noncanonical pending command-delivery agents: %s", p)
	}
	return out, true, nil
}

func writePending(root string, agentKeys []string) error {
	if len(agentKeys) == 0 {
		return nil
	}
	exp := append([]string(nil), agentKeys...)
	sort.Strings(exp)
	if cur, ok, err := readPending(root); err != nil {
		return err
	} else if ok {
		if strings.Join(cur, ",") != strings.Join(exp, ",") {
			return fmt.Errorf("Pending command delivery has a different agent selection")
		}
		return nil
	}
	quoted := make([]string, len(exp))
	for i, a := range exp {
		quoted[i] = `"` + a + `"`
	}
	content := `{"agents": [` + strings.Join(quoted, ", ") + `], "schema_version": 1}` + "\n"
	f, err := os.OpenFile(filepath.Join(root, pendingCommandSkills), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func finishPending(root string, agentKeys []string) error {
	if len(agentKeys) == 0 {
		return nil
	}
	exp := append([]string(nil), agentKeys...)
	sort.Strings(exp)
	cur, _, err := readPending(root)
	if err != nil {
		return err
	}
	if strings.Join(cur, ",") != strings.Join(exp, ",") {
		return fmt.Errorf("Pending command-delivery record changed; preserving it")
	}
	return os.Remove(filepath.Join(root, pendingCommandSkills))
}

// installCommandSkillAgents installs command skills for each agent,
// printing init's per-agent lines. Returns false on any failure.
func installCommandSkillAgents(w io.Writer, root string, keys []string) bool {
	ok := true
	now := clock.NowUTCISO()
	for _, k := range keys {
		rep, err := agents.InstallCommandSkills(root, k, now)
		if err == nil && k == "vibe" {
			err = agents.EnsureVibeSkillPath(root)
		}
		if err != nil {
			ok = false
			outf(w, "[yellow]Warning:[/yellow] Could not install skills for %s: %v", agents.Display(k), err)
			continue
		}
		outf(w, "[dim]%s: %d command skills installed[/dim]", agents.Display(k), rep.InstalledCount())
		for _, p := range rep.Regenerated {
			plain(w, RegeneratedSurfaceLine(p))
		}
	}
	return ok
}

func primaryNextStepAgent(selected []string) string {
	for _, a := range selected {
		if commandSkillAgentSet[a] {
			return a
		}
	}
	return selected[0]
}

func agentCommandToken(agent, command string) string {
	switch agent {
	case "codex":
		return "$spec-kitty." + command
	case "pi":
		return "/skill:spec-kitty." + command
	}
	return "/spec-kitty." + command
}

func runInit(ctx *cli.Context) error {
	w := ctx.Stdout
	showBanner(w)
	nonInteractive := ctx.Bool("non-interactive") || !isInteractive()
	projectName := ctx.Arg("project_name")
	if projectName == "." {
		projectName = ""
	}
	here := projectName == ""
	var projectPath string
	if here {
		cwd, err := os.Getwd()
		if err != nil {
			out(w, "[red]Error:[/red] Cannot access current directory")
			return cli.Exit(1)
		}
		projectPath = cwd
		projectName = filepath.Base(cwd)
	} else {
		abs, _ := filepath.Abs(projectName)
		projectPath = abs
		if _, err := os.Stat(projectPath); err == nil {
			fmt.Fprintln(w)
			panel(w, fmt.Sprintf("Directory '%s' already exists\nPlease choose a different project name or remove the existing directory.", projectName), "Directory Conflict")
			return cli.Exit(1)
		}
	}
	aiRaw := ctx.String("ai")
	var fromOption []string
	if aiRaw != "" {
		sel, invalid, empty := parseAgentSelection(aiRaw)
		if empty {
			out(w, "[red]Error:[/red] --ai flag did not contain any valid agent identifiers")
			return cli.Exit(1)
		}
		if len(invalid) > 0 {
			outf(w, "[red]Error:[/red] Invalid AI assistant(s): %s. Choose from: %s", strings.Join(invalid, ", "), strings.Join(agents.Keys, ", "))
			return cli.Exit(1)
		}
		fromOption = sel
	}

	if kittify.ConfigExists(projectPath) {
		return initRerun(ctx, projectPath, aiRaw, fromOption)
	}
	if hasOperatorAuthoredContent(projectPath) {
		out(w, "[yellow]Existing operator-authored .kittify/ content found (missions/ or memory/) without config.yaml.[/yellow]\n[dim]Rebuilding scaffolding; existing content will be preserved in a timestamped .kittify/.backup-<ts>/ directory.[/dim]")
	}

	cwd, _ := os.Getwd()
	setup := []string{"[cyan]Specify Project Setup[/cyan]", "", fmt.Sprintf("%-15s [green]%s[/green]", "Project", filepath.Base(projectPath)), fmt.Sprintf("%-15s [dim]%s[/dim]", "Working Path", cwd)}
	if !here {
		setup = append(setup, fmt.Sprintf("%-15s [dim]%s[/dim]", "Target Path", projectPath))
	}
	panel(w, strings.Join(setup, "\n"), "")

	vcs := gitAvailable()
	if vcs {
		fmt.Fprintln(w)
		out(w, "[green]✓ git detected[/green] - will be used for version control")
		fmt.Fprintln(w)
		probe := projectPath
		if _, err := os.Stat(probe); err != nil {
			probe = filepath.Dir(projectPath)
		}
		if !insideGitWorkTree(probe) {
			out(w, "[yellow]Target is not a git repository.[/yellow] After init, run `git init` in the target before using `spec-kitty agent`, `dispatch`, `next`, or `implement` commands.")
		}
	} else {
		out(w, "[yellow]ℹ git not detected[/yellow] - install git for version control")
	}

	selected := fromOption
	if aiRaw == "" {
		if nonInteractive {
			out(w, "[red]Error:[/red] --ai is required in non-interactive mode")
			return cli.Exit(1)
		}
		selected = promptAgents(ctx)
	}
	if len(selected) == 0 {
		out(w, "[red]Error:[/red] No AI assistants selected")
		return cli.Exit(1)
	}
	aiDisplay := agentDisplayList(selected)
	outf(w, "[cyan]Selected AI assistant(s):[/cyan] %s", aiDisplay)

	tr := &tracker{title: "Initialize Specify Project"}
	tr.add("precheck", "Check required tools")
	tr.complete("precheck", "ok")
	tr.add("ai-select", "Select AI assistant(s)")
	tr.complete("ai-select", aiDisplay)
	tr.add("runtime", "Bootstrap global runtime")
	for _, k := range selected {
		l := agents.Display(k)
		for _, s := range [][2]string{{"-fetch", ": fetch latest release"}, {"-download", ": download template"}, {"-extract", ": extract template"},
			{"-zip-list", ": archive contents"}, {"-extracted-summary", ": extraction summary"}, {"-cleanup", ": cleanup"}, {"-skills", ": install skill pack"}} {
			tr.add(k+s[0], l+s[1])
		}
	}
	tr.add("chmod", "Ensure scripts executable")
	tr.add("final", "Finalize")

	createdDir := false
	if !here {
		if err := os.MkdirAll(projectPath, 0o755); err != nil {
			return err
		}
		createdDir = true
	}
	var commandSkillAgents []string
	fail := func(err error) error {
		tr.fail("final", err.Error())
		panel(w, "Initialization failed: "+err.Error(), "Failure")
		if createdDir {
			os.RemoveAll(projectPath)
		}
		return cli.Exit(1)
	}

	if err := bootstrap.EnsureRuntime(); err != nil {
		tr.fail("runtime", err.Error())
		outf(w, "[red]Error:[/red] Failed to bootstrap global runtime: %v", err)
		return cli.Exit(1)
	}
	tr.complete("runtime", "ok")

	now := clock.NowUTCISO()
	manifest := &agents.SkillsManifest{CreatedAt: now, UpdatedAt: now, SpecKittyVersion: app.Version}
	base := false
	for _, k := range selected {
		tr.complete(k+"-fetch", "packaged data")
		tr.complete(k+"-download", "local files")
		if !base {
			if err := os.MkdirAll(filepath.Join(projectPath, ".kittify", "memory"), 0o755); err != nil {
				tr.fail(k+"-extract", err.Error())
				return fail(err)
			}
			base = true
		}
		tr.complete(k+"-extract", "agent configured (commands managed globally)")
		tr.complete(k+"-zip-list", "templates ready")
		tr.complete(k+"-extracted-summary", "commands ready")
		tr.complete(k+"-cleanup", "done")
		class := agents.SkillClass(k)
		switch {
		case class == agents.SkillClassWrapper:
			tr.complete(k+"-skills", "skipped (wrapper)")
		case commandSkillAgentSet[k]:
			commandSkillAgents = append(commandSkillAgents, k)
			tr.complete(k+"-skills", "queued until project configuration is saved")
		case class == agents.SkillClassShared:
			tr.complete(k+"-skills", "queued until project configuration is saved")
		default:
			entries, _, err := agents.InstallProjectSkills(projectPath, k, clock.NowUTCISO())
			if err != nil {
				tr.fail(k+"-skills", err.Error())
				continue
			}
			manifest.Entries = append(manifest.Entries, entries...)
			tr.complete(k+"-skills", fmt.Sprintf("%d skills installed", len(agents.Skills())))
		}
	}
	if len(manifest.Entries) > 0 {
		if err := agents.SaveSkillsManifest(projectPath, manifest, now); err != nil {
			return fail(err)
		}
	}
	res := agents.ProtectAllAgents(projectPath)
	if res.Modified {
		out(w, "[cyan]Updated .gitignore to exclude AI agent directories:[/cyan]")
		for _, e := range res.Added {
			raw(w, "  • "+e)
		}
		if len(res.Skipped) > 0 {
			outf(w, "  (%d already protected)", len(res.Skipped))
		}
	} else if len(res.Skipped) > 0 {
		outf(w, "[dim]All %d agent directories already in .gitignore[/dim]", len(res.Skipped))
	}
	for _, m := range res.Warnings {
		out(w, "[yellow]⚠️  "+m+"[/yellow]")
	}
	for _, m := range res.Errors {
		out(w, "[red]❌ "+m+"[/red]")
	}
	if !res.Success {
		return cli.Exit(1)
	}
	if err := writePending(projectPath, commandSkillAgents); err != nil {
		return fail(err)
	}
	if id, err := kittify.EnsureIdentity(projectPath); err == nil || id.UUID != "" {
		if err != nil {
			fmt.Fprintln(ctx.Stderr, "Warning: Config exists but could not be understood; using in-memory identity")
		}
		kittify.EmitProjectInitialized(projectPath, id.UUID, id.Slug, "spec-kitty init", app.Version)
	}
	tr.complete("final", "project ready")

	tr.render(w)
	fmt.Fprintln(w)
	out(w, "[bold green]Project ready.[/bold green]")

	var notice []string
	for _, k := range selected {
		if f, ok := agents.FolderNotice[k]; ok {
			notice = append(notice, "- "+agents.Display(k)+": [cyan]"+f+"[/cyan]")
		}
	}
	if len(notice) > 0 {
		body := append([]string{
			"Some agents may store credentials, auth tokens, or other identifying and private artifacts in the agent folder within your project.",
			"Consider adding the following folders (or subsets) to [cyan].gitignore[/cyan]:",
			"",
		}, notice...)
		fmt.Fprintln(w)
		panel(w, strings.Join(body, "\n"), "Agent Folder Security")
	}
	printNextSteps(w, projectPath, projectName, here, selected)

	if changed, err := kittify.EnsureGitattributes(projectPath); err == nil && changed {
		out(w, "[dim]Updated .gitattributes for Spec Kitty generated artifacts[/dim]")
	}
	wireMergeDrivers(w, projectPath)
	if _, err := kittify.ProvisionDefaultMissionTypeActivations(projectPath); err != nil {
		var dp *kittify.DefaultPresetError
		if errors.As(err, &dp) {
			raw(w, fmt.Sprintf("Error (%s): %s", dp.Code(), dp.Body))
			return cli.Exit(1)
		}
		return fail(err)
	}
	md := kittify.NewMetadata(app.Version)
	if _, err := md.Save(projectPath); err != nil {
		outf(w, "[dim]Note: Could not create project metadata: %v[/dim]", err)
	}
	if _, err := kittify.StampSchema(projectPath); err != nil {
		outf(w, "[dim]Note: Could not stamp schema metadata: %v[/dim]", err)
	}
	if vcs {
		if err := kittify.SaveVCSConfig(projectPath); err != nil {
			outf(w, "[dim]Note: Could not save VCS config: %v[/dim]", err)
		}
	}
	saved := false
	if err := kittify.SaveAgentConfig(projectPath, kittify.AgentConfig{Available: selected, AutoCommit: true}); err != nil {
		outf(w, "[dim]Note: Could not save agent config: %v[/dim]", err)
	} else {
		saved = true
		out(w, "[dim]Saved agent configuration[/dim]")
	}
	complete := installCommandSkillAgents(w, projectPath, commandSkillAgents)
	if saved && complete {
		finishPending(projectPath, commandSkillAgents)
	} else if len(commandSkillAgents) > 0 {
		out(w, "[yellow]Command delivery is incomplete; retry init after resolving the reported error.[/yellow]")
	}
	for _, c := range InstallPresence(projectPath, selected) {
		outf(w, "[dim]%s[/dim]", c)
	}
	summary, err := RunSurfaceRepair(projectPath)
	if err != nil {
		outf(w, "[dim]Note: Could not run tool surface repair: %v[/dim]", err)
	} else {
		for _, l := range summary.Lines() {
			out(w, l)
		}
		if len(summary.Drifted) > 0 && nonInteractive {
			return cli.Exit(1)
		}
	}
	return nil
}

func printNextSteps(w io.Writer, projectPath, projectName string, here bool, selected []string) {
	lines := []string{"Project: [green]" + projectPath + "[/green]", "Agents: [cyan]" + strings.Join(selected, ", ") + "[/cyan]"}
	inside := insideGitWorkTree(projectPath)
	if inside {
		lines = append(lines, "Git: [green]ready[/green]")
	} else {
		lines = append(lines, "Git: [yellow]not initialized[/yellow]")
	}
	lines = append(lines, "")
	n := 1
	if !here {
		lines = append(lines, fmt.Sprintf("%d. Enter the project: [cyan]cd %s[/cyan]", n, projectName))
	} else {
		lines = append(lines, fmt.Sprintf("%d. Stay in this project directory.", n))
	}
	n++
	if !inside {
		lines = append(lines, fmt.Sprintf("%d. [yellow]Required:[/yellow] run [cyan]git init[/cyan] here before agent, dispatch, next, and implement commands", n))
		n++
	}
	primary := primaryNextStepAgent(selected)
	heading := "Build with slash commands:"
	if commandSkillAgentSet[primary] {
		heading = "Build with command skills:"
	}
	var wf []string
	for _, c := range [][2]string{{"specify", "write the spec"}, {"plan", "write the plan"}, {"tasks", "create work packages"}} {
		wf = append(wf, "[cyan]"+agentCommandToken(primary, c[0])+"[/] ("+c[1]+")")
	}
	lines = append(lines, fmt.Sprintf("%d. %s %s", n, heading, strings.Join(wf, " -> ")))
	n++
	lines = append(lines, fmt.Sprintf("%d. Run the mission loop: [cyan]spec-kitty next --agent <agent> --mission <slug>[/cyan]", n))
	lines = append(lines, "", "[dim]Optional[/dim]",
		"- [cyan]"+agentCommandToken(primary, "charter")+"[/cyan] - add project governance when needed",
		"- [cyan]spec-kitty agent tasks status[/cyan] - show mission and work package status",
		"- [cyan]spec-kitty retrospect summary[/cyan] - review learning status after merge",
		"- [cyan]"+agentCommandToken(primary, "analyze")+"[/cyan] - check artifact alignment",
		"", "[dim]Docs: https://docs.spec-kitty.ai/[/dim]")
	fmt.Fprintln(w)
	panel(w, strings.Join(lines, "\n"), "Next Steps")
	has := func(k string) bool {
		for _, s := range selected {
			if s == k {
				return true
			}
		}
		return false
	}
	if has("vibe") {
		fmt.Fprintln(w)
		panel(w, strings.Join([]string{"1. Install Vibe if you haven't already:", "     [cyan]curl -LsSf https://mistral.ai/vibe/install.sh | bash[/cyan]", "   or", "     [cyan]uv tool install mistral-vibe[/cyan]", "2. Launch Vibe in this project:", "     [cyan]vibe[/cyan]", "3. Inside Vibe, invoke your first workflow:", "     [cyan]/spec-kitty.specify <describe what you want to build>[/cyan]"}, "\n"), "Next Steps for Mistral Vibe")
	}
	if has("pi") {
		fmt.Fprintln(w)
		panel(w, strings.Join([]string{"1. Install Pi if you haven't already:", "     [cyan]curl -fsSL https://pi.dev/install.sh | sh[/cyan]", "2. Launch Pi in this project:", "     [cyan]pi[/cyan]", "3. Invoke your first Spec Kitty command skill:", "     [cyan]/skill:spec-kitty.specify <describe what you want to build>[/cyan]"}, "\n"), "Next Steps for Pi")
	}
	if has("letta") {
		fmt.Fprintln(w)
		panel(w, strings.Join([]string{"1. Install Letta Code if you haven't already:", "     [cyan]npm install -g @letta-ai/letta-code[/cyan]", "2. Launch Letta Code in this project:", "     [cyan]letta[/cyan]", "3. Ask Letta Code to use the Spec Kitty specify skill:", "     [cyan]/spec-kitty.specify <describe what you want to build>[/cyan]"}, "\n"), "Next Steps for Letta Code")
	}
	enh := "Optional quality checks.\n\n○ [cyan]" + agentCommandToken(primary, "analyze") + "[/] [bright_black](optional)[/bright_black] - Cross-artifact consistency & alignment report (after [cyan]" + agentCommandToken(primary, "tasks") + "[/])"
	fmt.Fprintln(w)
	panel(w, enh, "Optional Enhancements")
}

func wireMergeDrivers(w io.Writer, root string) string {
	state, keys, err := kittify.InstallMergeDriverConfig(root)
	if err != nil {
		raw(w, fmt.Sprintf("Could not configure Spec Kitty merge drivers; continuing without local git configuration: %v", err))
		return ""
	}
	for _, k := range keys {
		raw(w, fmt.Sprintf("Kept your custom git config value for %s; Spec Kitty merge drivers may not run as shipped.", k))
	}
	return state
}

func hasOperatorAuthoredContent(root string) bool {
	for _, sub := range []string{"missions", "memory"} {
		d := filepath.Join(root, ".kittify", sub)
		ents, err := os.ReadDir(d)
		if err == nil && len(ents) > 0 {
			return true
		}
	}
	return false
}

// promptAgents is a plain-text stand-in for multi_select_with_arrows.
func promptAgents(ctx *cli.Context) []string {
	w := ctx.Stdout
	out(w, "Choose your AI assistant(s):")
	for i, k := range agents.Keys {
		mark := " "
		if k == "copilot" {
			mark = "*"
		}
		fmt.Fprintf(w, "  [%s] %2d. %s (%s)\n", mark, i+1, k, agents.Display(k))
	}
	fmt.Fprint(w, "Enter comma-separated keys or numbers [copilot]: ")
	line, _ := bufio.NewReader(ctx.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return []string{"copilot"}
	}
	var keys []string
	for _, p := range strings.Split(line, ",") {
		p = strings.TrimSpace(p)
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n >= 1 && n <= len(agents.Keys) {
			p = agents.Keys[n-1]
		}
		keys = append(keys, p)
	}
	sel, _, _ := parseAgentSelection(strings.Join(keys, ","))
	return sel
}
