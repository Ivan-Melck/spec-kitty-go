package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "doctor", "Project health diagnostics")
		if g.Run == nil {
			g.InvokeWithoutCommand = true
			g.Run = func(ctx *cli.Context) error {
				cli.WriteHelp(ctx.Stdout, g)
				fmt.Fprintln(ctx.Stderr, "Error: no diagnostic ran. `spec-kitty doctor` needs a subcommand (for example `spec-kitty doctor skills`); see the list above or run `spec-kitty doctor --help`.")
				return cli.Exit(2)
			}
		}
		jsonFlag := func() *cli.Flag { return &cli.Flag{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"} }
		g.Add(
			newDoctorBytecodeCmd(),
			&cli.Command{
				Name:  "channel",
				Short: "Report the active release channel (stable vs. prerelease-opt-in).",
				Long: "Report the active release channel (stable vs. prerelease-opt-in).\n\nReads SPEC_KITTY_PRERELEASE (default OFF — stable channel). Never\nmutates state.\n\n" +
					"Examples:\n    spec-kitty doctor channel\n    spec-kitty doctor channel --json",
				Flags: []*cli.Flag{jsonFlag()},
				Run:   runDoctorChannel,
			},
			&cli.Command{
				Name:  "env-file",
				Short: "Report ``.kitty.env`` operator env-file health (presence/tier/ignore).",
				Long: "Report ``.kitty.env`` operator env-file health (presence/tier/ignore).\n\nReads the repo- and home-tier ``.kitty.env`` files and the\nconfig.yaml ``env_file`` pointer, and reports which governed vars\n" +
					"are set and from which tier -- names/presence only for anything not\non the printable-var allowlist (C-SEC-1), values never leaked.\nRead-only -- never mutates state.\n\n" +
					"Examples:\n    spec-kitty doctor env-file\n    spec-kitty doctor env-file --json",
				Flags: []*cli.Flag{jsonFlag()},
				Run:   runDoctorEnvFile,
			},
			&cli.Command{
				Name:  "command-files",
				Short: "Check all agent command files for correctness.",
				Long: "Check all agent command files for correctness.\n\nVerifies that every configured agent has the correct command files:\n- Full rendered prompts for prompt-driven commands (specify, plan, tasks, ...)\n" +
					"- Thin shims for CLI-driven commands (implement, review, merge, ...)\n- Current version markers on all files\n\nExamples:\n    spec-kitty doctor command-files\n    spec-kitty doctor command-files --json",
				Flags: []*cli.Flag{jsonFlag()},
				Run:   runDoctorCommandFiles,
			},
			&cli.Command{
				Name:  "skills",
				Short: "Check command-skill manifest drift for Codex, Vibe, Pi, Letta, and Antigravity.",
				Flags: []*cli.Flag{{Name: "fix", Kind: cli.Bool, Help: "Repair missing command-skill files"}, jsonFlag()},
				Run:   runDoctorSkills,
			},
			newDoctorToolSurfacesCmd(),
			newDoctorStateRootsCmd(),
		)
	})
}

const notInProjectMessage = "Not in a spec-kitty project"

// doctorProjectRoot is _doctor_shared.resolve_project_root_or_exit.
func doctorProjectRoot(w io.Writer, jsonOut bool, code int) (string, error) {
	if r := project.Locate(""); r != "" {
		return r, nil
	}
	if jsonOut {
		printJSONRaw(w, jsonError("not_in_project", notInProjectMessage))
	} else {
		out(w, "[red]Error:[/red] "+notInProjectMessage)
	}
	return "", cli.Exit(code)
}

func runDoctorChannel(ctx *cli.Context) error {
	pre := truthy(os.Getenv("SPEC_KITTY_PRERELEASE"))
	if ctx.Bool("json") {
		ch := "stable"
		if pre {
			ch = "prerelease-opt-in"
		}
		printJSONRaw(ctx.Stdout, jsonx.NewObject("channel", ch, "prerelease_opt_in", pre))
		return nil
	}
	if pre {
		out(ctx.Stdout, "[yellow]Release channel[/yellow]: prerelease-opt-in (SPEC_KITTY_PRERELEASE is set) — 'latest version' surfaces include release candidates.")
	} else {
		out(ctx.Stdout, "[green]Release channel[/green]: stable (default) — 'latest version' surfaces only stable releases.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// doctor env-file
// ---------------------------------------------------------------------------

var (
	governedVars  = []string{"SPEC_KITTY_HOME", "SPEC_KITTY_NON_INTERACTIVE", "SPEC_KITTY_FORCE_INTERACTIVE", "SPEC_KITTY_SKIP_PRE_REVIEW_GATE", "SPEC_KITTY_NO_BANNER", "SPEC_KITTY_NO_NAG", "SPEC_KITTY_NO_UPGRADE_CHECK", "SPEC_KITTY_ORG_TOKEN", "SPEC_KITTY_ORG_AUTH_HEADER"}
	printableVars = map[string]bool{"SPEC_KITTY_HOME": true, "SPEC_KITTY_TEMPLATE_ROOT": true, "SPEC_KITTY_NON_INTERACTIVE": true, "SPEC_KITTY_FORCE_INTERACTIVE": true,
		"SPEC_KITTY_SKIP_PRE_REVIEW_GATE": true, "SPEC_KITTY_NO_BANNER": true, "SPEC_KITTY_NO_NAG": true, "SPEC_KITTY_NO_UPGRADE_CHECK": true}
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// ParseEnvFile is bootstrap.env_file.parse_env_file.
func ParseEnvFile(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if !envKeyRe.MatchString(k) {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == v[len(v)-1] && (v[0] == '"' || v[0] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

// RuntimeStateRoot is kernel.paths.get_runtime_state_root (POSIX).
func RuntimeStateRoot() string {
	if v := os.Getenv("SPEC_KITTY_HOME"); v != "" {
		return v
	}
	return filepath.Join(project.UserHome(), ".spec-kitty")
}

func envFilePointer(root string) string {
	if root == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "env_file:") {
			continue
		}
		v := strings.TrimSpace(line[len("env_file:"):])
		v, _, _ = strings.Cut(v, " #")
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == v[len(v)-1] && (v[0] == '"' || v[0] == '\'') {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}

func homeEnvPath(root string) (string, string) {
	ptr := envFilePointer(root)
	source := "default (${SPEC_KITTY_HOME}/.kitty.env)"
	rawPath := "${SPEC_KITTY_HOME}/.kitty.env"
	if ptr != "" {
		source, rawPath = "config.yaml env_file key", ptr
	}
	expanded := envVarRe.ReplaceAllStringFunc(rawPath, func(tok string) string {
		m := envVarRe.FindStringSubmatch(tok)
		name := m[1] + m[2]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if name == "SPEC_KITTY_HOME" {
			return RuntimeStateRoot()
		}
		return tok
	})
	if strings.HasPrefix(expanded, "~/") || expanded == "~" {
		expanded = filepath.Join(project.UserHome(), strings.TrimPrefix(expanded, "~"))
	}
	return expanded, source
}

func ignoreEntries(path string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n") {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") {
			out[t] = true
		}
	}
	return out
}

func runDoctorEnvFile(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root, err := doctorProjectRoot(w, jsonOut, 1)
	if err != nil {
		return err
	}
	repoPath := filepath.Join(root, ".kittify", ".kitty.env")
	repoExists := exists(repoPath)
	homePath, homeSource := homeEnvPath(root)
	homeExists := exists(homePath)
	gitignored := ignoreEntries(filepath.Join(root, ".gitignore"))[".kittify/.kitty.env"]
	claudeignored := ignoreEntries(filepath.Join(root, ".claudeignore"))[".kittify/.kitty.env"]
	repoVals, homeVals := map[string]string{}, map[string]string{}
	if repoExists {
		if b, err := os.ReadFile(repoPath); err == nil {
			repoVals = ParseEnvFile(string(b))
		}
	}
	if homeExists {
		if b, err := os.ReadFile(homePath); err == nil {
			homeVals = ParseEnvFile(string(b))
		}
	}
	type report struct {
		name, tier string
		present    bool
		value      *string
	}
	var reports []report
	for _, v := range governedVars {
		envVal, realPresent := os.LookupEnv(v)
		repoVal, repoPresent := repoVals[v]
		homeVal, homePresent := homeVals[v]
		r := report{name: v, tier: "unset", present: realPresent || repoPresent || homePresent}
		val := ""
		switch {
		case realPresent:
			r.tier, val = "real_env", envVal
		case repoPresent:
			r.tier, val = "repo", repoVal
		case homePresent:
			r.tier, val = "home", homeVal
		}
		if r.present && printableVars[v] {
			x := val
			r.value = &x
		}
		reports = append(reports, r)
	}
	var issues []string
	if repoExists && !gitignored {
		issues = append(issues, ".kittify/.kitty.env exists but is not covered by .gitignore (C-SEC-2)")
	}
	if repoExists && !claudeignored {
		issues = append(issues, ".kittify/.kitty.env exists but is not covered by .claudeignore (C-SEC-2)")
	}
	if jsonOut {
		gv := []any{}
		for _, r := range reports {
			var val any
			if r.value != nil {
				val = *r.value
			}
			gv = append(gv, jsonx.NewObject("name", r.name, "tier", r.tier, "present", r.present, "value", val))
		}
		printJSONRaw(w, jsonx.NewObject(
			"repo_env_file", jsonx.NewObject("path", repoPath, "exists", repoExists),
			"home_env_file", jsonx.NewObject("path", homePath, "exists", homeExists, "source", homeSource),
			"gitignored", gitignored, "claudeignored", claudeignored, "governed_vars", gv, "issues", strList(issues)))
		if len(issues) > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	out(w, "\n[bold]Env-file health[/bold]\n")
	st := "[dim]not provisioned[/dim]"
	if repoExists {
		st = "[green]exists[/green]"
	}
	out(w, fmt.Sprintf("  Repo tier   (%s): %s", repoPath, st))
	if repoExists {
		yn := func(b bool) string {
			if b {
				return "[green]yes[/green]"
			}
			return "[red]no[/red]"
		}
		out(w, fmt.Sprintf("    gitignored: %s    claudeignored: %s", yn(gitignored), yn(claudeignored)))
	}
	hs := "[dim]not provisioned[/dim]"
	if homeExists {
		hs = "[green]exists[/green]"
	}
	out(w, fmt.Sprintf("  Home tier   (%s): %s [dim](%s)[/dim]", homePath, hs, homeSource))
	out(w, "\n  Governed vars:")
	for _, r := range reports {
		switch {
		case !r.present:
			out(w, fmt.Sprintf("    [dim]%s: unset[/dim]", r.name))
		case r.value != nil:
			out(w, fmt.Sprintf("    %s [%s]: %s", r.name, r.tier, *r.value))
		default:
			out(w, fmt.Sprintf("    %s [%s]: [dim]<redacted>[/dim]", r.name, r.tier))
		}
	}
	if len(issues) > 0 {
		out(w, "\n[bold yellow]Issue(s):[/bold yellow]")
		for _, i := range issues {
			out(w, "  • [yellow]"+i+"[/yellow]")
		}
		out(w, "\n  [dim]Fix with:[/dim] spec-kitty migrate  # applies m_3_2_8_provision_kitty_env\n")
		return cli.Exit(1)
	}
	fmt.Fprintln(w)
	return nil
}

// ---------------------------------------------------------------------------
// doctor command-files
// ---------------------------------------------------------------------------

func agentDirsForProject(root string) [][2]string {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil
	}
	if len(ac.Available) == 0 && !ac.ExplicitAgentList {
		return agents.AgentDirs
	}
	var out [][2]string
	for _, d := range agents.AgentDirs {
		if contains(ac.Available, agents.AgentDirToKey[d[0]]) {
			out = append(out, d)
		}
	}
	return out
}

func consumerCommandsSorted() []string {
	cmds := append(append([]string(nil), agents.PromptDrivenCommands...), agents.CLIDrivenCommands...)
	sort.Strings(cmds)
	return cmds
}

func isPromptDriven(c string) bool {
	for _, x := range agents.PromptDrivenCommands {
		if x == c {
			return true
		}
	}
	return false
}

// CommandFileIssues is runtime.doctor.check_command_file_health.
func CommandFileIssues(root string) []map[string]string {
	marker := "<!-- spec-kitty-command-version: " + app.Version + " -->"
	var issues []map[string]string
	add := func(a, c, f, issue, sev string) {
		issues = append(issues, map[string]string{"agent": a, "command": c, "file": f, "issue": issue, "severity": sev})
	}
	for _, d := range agentDirsForProject(root) {
		key := agents.AgentDirToKey[d[0]]
		if key == "" {
			continue
		}
		dir := agents.GlobalCommandDir(key)
		for _, c := range consumerCommandsSorted() {
			fp := filepath.Join(dir, agents.CommandFilename(c, key))
			b, err := os.ReadFile(fp)
			if err != nil {
				if _, serr := os.Stat(fp); serr != nil {
					add(key, c, fp, "missing", "error")
				} else {
					add(key, c, fp, "unreadable: "+err.Error(), "error")
				}
				continue
			}
			lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
			head := lines
			if len(head) > 20 {
				head = head[:20]
			}
			found := false
			for _, l := range head {
				if strings.TrimSpace(l) == marker {
					found = true
				}
			}
			if !found {
				add(key, c, fp, "stale or missing version marker (expected: "+marker+")", "warning")
			}
			n := 0
			for _, l := range lines {
				if strings.TrimSpace(l) != "" {
					n++
				}
			}
			if isPromptDriven(c) && n < 50 {
				add(key, c, fp, fmt.Sprintf("prompt-driven command has only %d non-empty lines (expected >50)", n), "warning")
			} else if !isPromptDriven(c) && n >= 15 {
				add(key, c, fp, fmt.Sprintf("CLI-driven command has %d non-empty lines (expected <15 for thin shim)", n), "warning")
			}
		}
	}
	return issues
}

// plainTable renders a rich Table(box=None, padding=(0, 2), show_edge=False)
// with per-column minimum widths and justification.
// plainTable is Table(box=None, padding=(0, 2), show_edge=False) with a
// cyan first column and the bold table.header row, at the console width.
func plainTable(w io.Writer, headers []string, minWidth []int, justify []byte, rows [][]string) {
	t := rich.PaddedTable{Headers: headers, MinWidths: minWidth, Justify: justify, Styles: []string{"cyan"}, HeaderStyle: "bold", Rows: rows}
	for _, l := range t.Lines(rich.Width(), rich.ColorSystemOf(w)) {
		fmt.Fprintln(w, l)
	}
}

func runDoctorCommandFiles(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root, err := doctorProjectRoot(w, jsonOut, 1)
	if err != nil {
		return err
	}
	if _, err := kittify.LoadAgentConfig(root); err != nil {
		return err
	}
	issues := CommandFileIssues(root)
	if jsonOut {
		l := []any{}
		for _, i := range issues {
			l = append(l, jsonx.NewObject("agent", i["agent"], "command", i["command"], "file", i["file"], "issue", i["issue"], "severity", i["severity"]))
		}
		printJSONRaw(w, l)
		if len(issues) > 0 {
			return cli.Exit(1)
		}
		return nil
	}
	if len(issues) == 0 {
		out(w, "[green]Command Files[/green]: all files healthy")
		return nil
	}
	out(w, fmt.Sprintf("\n[bold]Command Files[/bold] — %d issue(s) found\n", len(issues)))
	var rows [][]string
	for _, i := range issues {
		rows = append(rows, []string{i["agent"], i["command"], i["file"], i["severity"], i["issue"]})
	}
	plainTable(w, []string{"Agent", "Command", "File", "Severity", "Issue"}, []int{12, 16, 40, 8, 0}, nil, rows)
	fmt.Fprintln(w)
	return cli.Exit(1)
}

// ---------------------------------------------------------------------------
// doctor skills
// ---------------------------------------------------------------------------

// commandSkillVerify is command_installer.verify.
type commandSkillVerify = toolsurface.CommandVerifyReport

type commandManifestReadError struct{ error }

func canonicalCommandSkill(rel string) bool { return agents.ParseCommandSkillPath(rel, false) != "" }

type skillsState struct {
	entries                    int
	report                     commandSkillVerify
	configured, manifestAgents []string
	uninstalled                []string
	vibeMissing                bool
}

func loadSkillsState(root string) (skillsState, error) {
	var s skillsState
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return s, err
	}
	for _, a := range ac.Available {
		if agents.IsCommandSkillAgent(a) && !contains(s.configured, a) {
			s.configured = append(s.configured, a)
		}
	}
	sort.Strings(s.configured)
	m, err := toolsurface.LoadCommandManifest(root)
	if err != nil {
		return s, commandManifestReadError{fmt.Errorf("%s", commandManifestDetail(err))}
	}
	s.entries = len(m)
	set := map[string]bool{}
	for _, e := range m {
		for _, a := range e.Agents {
			set[a] = true
		}
	}
	s.manifestAgents = sortedKeys(set)
	for _, a := range s.configured {
		if !set[a] {
			s.uninstalled = append(s.uninstalled, a)
		}
	}
	s.vibeMissing = contains(s.configured, "vibe") && !agents.VibeSkillPathConfigured(root)
	report, e := toolsurface.VerifyCommandSkills(root)
	err = e
	if report != nil {
		s.report = *report
	}
	if err != nil {
		return s, commandManifestReadError{err}
	}
	return s, nil
}

type slashGap struct{ agent, command, path, status string }

func slashCommandState(root string) ([]string, []slashGap) {
	var configured []string
	ac, err := kittify.LoadAgentConfig(root)
	avail := map[string]bool{}
	if err != nil {
		for k := range agents.CommandConfigs {
			avail[k] = true
		}
	} else {
		for _, a := range ac.Available {
			avail[a] = true
		}
	}
	for k := range agents.CommandConfigs {
		if avail[k] && !agents.IsCommandSkillAgent(k) {
			configured = append(configured, k)
		}
	}
	sort.Strings(configured)
	var gaps []slashGap
	marker := agents.VersionMarkerPrefix + " " + app.Version
	for _, a := range configured {
		dir := agents.GlobalCommandDir(a)
		for _, c := range consumerCommandsSorted() {
			p := filepath.Join(dir, agents.CommandFilename(c, a))
			b, err := os.ReadFile(p)
			if err != nil {
				gaps = append(gaps, slashGap{a, c, p, "missing"})
				continue
			}
			lines := strings.Split(string(b), "\n")
			if len(lines) > 20 {
				lines = lines[:20]
			}
			if !strings.Contains(strings.Join(lines, "\n"), marker) {
				gaps = append(gaps, slashGap{a, c, p, "stale"})
			}
		}
	}
	return configured, gaps
}

func runDoctorSkills(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut, fix := ctx.Bool("json"), ctx.Bool("fix")
	root, err := doctorProjectRoot(w, jsonOut, 2)
	if err != nil {
		return err
	}
	if !jsonOut {
		toolsurface.LogWriter = ctx.Stderr
		defer func() { toolsurface.LogWriter = nil }()
	}
	st, err := loadSkillsState(root)
	if err != nil {
		code, message := "config_error", err.Error()
		if _, ok := err.(commandManifestReadError); ok {
			code = "manifest_error"
			message = "Could not read command-skill manifest: " + message
		}
		if jsonOut {
			printJSONRaw(w, jsonError(code, err.Error()))
		} else {
			out(w, "[red]Error:[/red] "+escapeMarkup(message))
		}
		return cli.Exit(2)
	}
	var repaired, pruned, repairErrors []string
	repairedVibe := false
	if fix {
		r := st.report
		if len(r.Gaps)+len(r.Stale)+len(st.uninstalled)+len(r.LegacyOwned)+len(r.LegacyRecorded) > 0 || st.vibeMissing {
			switch {
			case len(r.Drift) > 0:
				repairErrors = append(repairErrors, "Refusing --fix while managed skill files have edited-file drift.")
			case len(r.Unsafe) > 0:
				repairErrors = append(repairErrors, "Refusing --fix while managed skill paths resolve outside the project.")
			case len(r.Orphans) > 0:
				repairErrors = append(repairErrors, "Refusing --fix while unmanaged spec-kitty skill files exist.")
			default:
				all := map[string]bool{}
				for _, a := range append(append([]string(nil), st.manifestAgents...), st.uninstalled...) {
					all[a] = true
				}
				if len(r.Stale) > 0 {
					m := agents.LoadCommandSkillsManifest(root)
					for _, p := range r.Stale {
						if e := m[p]; e != nil {
							if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))); err != nil || sha256Hex(b) == e.ContentHash {
								os.Remove(filepath.Join(root, filepath.FromSlash(p)))
								os.Remove(filepath.Dir(filepath.Join(root, filepath.FromSlash(p))))
								delete(m, p)
								pruned = append(pruned, p)
							}
						}
					}
					agents.SaveCommandSkillsManifest(root, m)
				}
				if st.vibeMissing && all["vibe"] {
					if err := agents.EnsureVibeSkillPath(root); err != nil {
						repairErrors = append(repairErrors, "vibe-config: "+err.Error())
					} else {
						repairedVibe = true
					}
				}
				now := clock.NowUTCISO()
				for _, a := range sortedKeys(all) {
					if _, err := agents.InstallCommandSkills(root, a, now); err != nil {
						repairErrors = append(repairErrors, fmt.Sprintf("%s: %v", a, err))
						continue
					}
					if a == "vibe" {
						agents.EnsureVibeSkillPath(root)
						repairedVibe = true
					}
					repaired = append(repaired, a)
				}
			}
		}
		if (len(repaired)+len(pruned) > 0 || repairedVibe) && len(repairErrors) == 0 {
			if s2, err := loadSkillsState(root); err == nil {
				st = s2
			} else {
				repairErrors = append(repairErrors, "post-fix verify failed: "+err.Error())
			}
		}
	}
	r := st.report
	cmdOK := len(r.Drift)+len(r.Gaps)+len(r.Orphans)+len(r.Stale)+len(r.Unsafe)+len(r.LegacyOwned)+len(r.LegacyRecorded)+len(r.Collisions)+len(st.uninstalled)+len(repairErrors) == 0 && !st.vibeMissing
	configuredSlash, slashGaps := slashCommandState(root)
	var slashRepaired []string
	if fix && len(slashGaps) > 0 {
		for _, g := range slashGaps {
			slashRepaired = append(slashRepaired, g.path)
		}
		bootstrap.EnsureAll(bootstrap.Options{})
		configuredSlash, slashGaps = slashCommandState(root)
	}
	slashOK := len(slashGaps) == 0
	packFindings := toolsurface.FindPackSkillFindings(root)
	repairedPaths := []string{}
	if fix {
		missing := false
		for _, f := range packFindings {
			missing = missing || f.Kind == toolsurface.PackKindMissing
		}
		if missing {
			changed, err := toolsurface.ProjectPackSkills(root)
			if err != nil {
				repairErrors = append(repairErrors, "pack skill projection failed: "+err.Error())
			} else {
				repairedPaths = append(repairedPaths, changed...)
			}
			packFindings = toolsurface.FindPackSkillFindings(root)
		}
	}
	packList := []any{}
	for _, f := range packFindings {
		packList = append(packList, f.JSON())
	}
	toolFolders := toolFolderFindings(root)
	ok := cmdOK && slashOK && len(packFindings) == 0 && len(toolFolders) == 0
	if jsonOut {
		gaps := []any{}
		for _, g := range slashGaps {
			gaps = append(gaps, jsonx.NewObject("agent_key", g.agent, "command", g.command, "expected_path", g.path, "status", g.status))
		}
		printJSONRaw(w, jsonx.NewObject(
			"configured_agents", strList(st.configured), "manifest_agents", strList(st.manifestAgents), "entries", st.entries,
			"canonical_commands", len(agents.CommandSkillCommands), "drift", strList(r.Drift), "gaps", strList(r.Gaps),
			"orphans", strList(r.Orphans), "stale", strList(r.Stale), "unsafe", strList(r.Unsafe), "legacy", strList(r.Legacy), "collisions", strList(r.Collisions), "uninstalled_agents", strList(st.uninstalled),
			"vibe_config_missing", st.vibeMissing, "repaired_agents", strList(repaired), "repaired_paths", strList(repairedPaths), "pruned", strList(pruned),
			"repaired_vibe_config", repairedVibe, "repair_errors", strList(repairErrors), "ok", ok,
			"slash_commands", jsonx.NewObject("configured_agents", strList(configuredSlash), "gaps", gaps, "repaired", strList(slashRepaired), "errors", []any{}, "ok", slashOK),
			"pack_skills", packList, "tool_folders", toolFolders))
		if ok {
			return nil
		}
		return cli.Exit(1)
	}
	if ok {
		out(w, fmt.Sprintf("[green]Command Skills[/green]: all manifest entries healthy (%d file(s))", st.entries))
		renderCommandSkillGuidance(w, r)
	} else {
		out(w, "\n[bold]Command Skills[/bold] - issue(s) found\n")
		plainTable(w, []string{"Check", "Count"}, []int{20, 6}, []byte("lr"), [][]string{
			{"manifest entries", fmt.Sprint(st.entries)}, {"drift", fmt.Sprint(len(r.Drift))}, {"gaps", fmt.Sprint(len(r.Gaps))},
			{"orphans", fmt.Sprint(len(r.Orphans))}, {"stale", fmt.Sprint(len(r.Stale))}, {"unsafe", fmt.Sprint(len(r.Unsafe))},
			{"legacy", fmt.Sprint(len(r.Legacy))}, {"collisions", fmt.Sprint(len(r.Collisions))}, {"uninstalled agents", fmt.Sprint(len(st.uninstalled))}})
		section := func(title string, paths []string) {
			if len(paths) == 0 {
				return
			}
			out(w, "\n[bold yellow]"+title+"[/bold yellow]")
			for _, p := range paths {
				out(w, "  [yellow]![/yellow] "+p)
			}
		}
		section("Edited managed files (manual review required)", r.Drift)
		section("Missing managed files", r.Gaps)
		section("Unmanaged spec-kitty skill files", r.Orphans)
		section("Stale managed files", r.Stale)
		section("Unsafe managed paths", r.Unsafe)
		renderCommandSkillGuidance(w, r)
		if len(st.uninstalled) > 0 {
			out(w, "\n[bold yellow]Configured agents without command skills[/bold yellow]")
			for _, a := range st.uninstalled {
				out(w, "  [yellow]![/yellow] "+a)
			}
		}
		if len(repaired) > 0 {
			out(w, "\n[green]Repaired:[/green] "+strings.Join(repaired, ", "))
		}
		if len(pruned) > 0 {
			out(w, fmt.Sprintf("\n[green]Pruned stale entries:[/green] %d", len(pruned)))
		}
		if repairedVibe {
			out(w, "\n[green]Repaired:[/green] Vibe skill path config")
		}
		if len(repairErrors) > 0 {
			out(w, "\n[bold red]Repair errors[/bold red]")
			for _, e := range repairErrors {
				out(w, "  [red]![/red] "+escapeMarkup(e))
			}
		}
		if !fix && (len(r.Gaps)+len(st.uninstalled)+len(r.Stale) > 0 || st.vibeMissing) {
			out(w, "\nRun [cyan]spec-kitty doctor skills --fix[/cyan] to reinstall missing command skills.")
		}
	}
	if len(configuredSlash) > 0 {
		fmt.Fprintln(w)
		if slashOK {
			out(w, fmt.Sprintf("[green]✓ Slash Commands[/green]: all configured agents healthy (%d agent(s))", len(configuredSlash)))
		} else {
			out(w, "[bold]Slash Commands[/bold] — gap(s) found\n")
			for _, a := range configuredSlash {
				var ag []slashGap
				for _, g := range slashGaps {
					if g.agent == a {
						ag = append(ag, g)
					}
				}
				if len(ag) == 0 {
					out(w, "  [green]✓[/green] "+a+": all commands present")
					continue
				}
				out(w, fmt.Sprintf("  [red]✗[/red] %s: %d gap(s)", a, len(ag)))
				for i, g := range ag {
					if i == 5 {
						break
					}
					out(w, fmt.Sprintf("      %s: %s", g.status, filepath.Base(g.path)))
				}
				if len(ag) > 5 {
					out(w, fmt.Sprintf("      ... and %d more", len(ag)-5))
				}
			}
			if !fix {
				out(w, "\nRun [cyan]spec-kitty doctor skills --fix[/cyan] to reinstall.")
			}
		}
		if fix && len(slashRepaired) > 0 {
			out(w, fmt.Sprintf("\n[green]Repaired:[/green] %d slash command file(s)", len(slashRepaired)))
		}
	}
	if len(packFindings) > 0 {
		out(w, "\n[bold yellow]Pack skills[/bold yellow]")
		for _, f := range packFindings {
			plain(w, fmt.Sprintf("  ! [%s] %s", f.Kind, f.Message()))
		}
	}
	for _, f := range toolFolders {
		o := f.(*jsonx.Object)
		plain(w, fmt.Sprintf("\n  ! [%s] %s", o.GetString("kind"), o.GetString("message")))
	}
	if ok {
		return nil
	}
	return cli.Exit(1)
}

// toolFolderFindings is _tool_folder_findings (FR-006).
func toolFolderFindings(root string) []any {
	roots := map[string]bool{}
	for _, d := range agentDirsForProject(root) {
		roots[d[0]] = true
	}
	if len(roots) == 0 {
		return []any{}
	}
	for r := range roots {
		if st, err := os.Stat(filepath.Join(root, r)); err == nil && st.IsDir() {
			return []any{}
		}
	}
	ac, _ := kittify.LoadAgentConfig(root)
	ag := append([]string(nil), ac.Available...)
	sort.Strings(ag)
	msg := fmt.Sprintf("none of the configured tool folders exists (%s) for agents %s; run `spec-kitty upgrade` to recreate configured folders, or `spec-kitty agent config remove <agent>` to drop a tool",
		strings.Join(sortedKeys(roots), ", "), strings.Join(ag, ", "))
	return []any{jsonx.NewObject("kind", "no_tool_folder", "configured_agents", strList(ag), "message", msg)}
}

func renderCommandSkillGuidance(w io.Writer, r commandSkillVerify) {
	for _, path := range r.Legacy {
		message := "Legacy dotted command skills (no longer managed): " + path + " — remove it when you no longer need your edits"
		if contains(r.LegacyRecorded, path) {
			message = "Legacy dotted command skill (still recorded by Spec Kitty): " + path + " — run `spec-kitty doctor skills --fix` to retire it"
		} else if contains(r.LegacyOwned, path) {
			message = "Legacy dotted command skill (Spec Kitty's unmodified rendering): " + path + " — run `spec-kitty doctor skills --fix` to retire it"
		}
		out(w, "\n  [yellow]![/yellow] "+escapeMarkup(message))
	}
	for _, path := range r.Collisions {
		for _, warning := range agents.CommandSkillWarnings(agents.CommandSkillReport{Collisions: []string{path}}) {
			out(w, "\n  [yellow]![/yellow] "+escapeMarkup(warning))
		}
	}
}
