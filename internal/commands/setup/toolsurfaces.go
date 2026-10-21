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
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// The tool-surface contract (specify_cli.tool_surface): every configured
// tool expands into surfaces (command skills, slash commands, session
// presence, native config, charter skills, agent profiles, plugin bundle
// manifests) which are probed against disk and reported with findings.

func newDoctorToolSurfacesCmd() *cli.Command {
	return &cli.Command{
		Name:  "tool-surfaces",
		Short: "Audit (and optionally repair) every configured tool surface.",
		Long: "Audit (and optionally repair) every configured tool surface.\n\nExamples:\n    spec-kitty doctor tool-surfaces --json\n" +
			"    spec-kitty doctor tool-surfaces --kind command-skill --json\n    spec-kitty doctor tool-surfaces --tool codex --fix",
		Flags: []*cli.Flag{
			{Name: "kind", Kind: cli.StringList, Metavar: "<str>", Help: "Filter to surface kind(s), e.g. command-skill"},
			{Name: "tool", Kind: cli.String, Metavar: "<str>", Help: "Filter to a single configured tool key"},
			{Name: "fix", Kind: cli.Bool, Help: "Repair missing or stale surfaces"},
			{Name: "json", Kind: cli.Bool, Help: "Machine-readable JSON output"},
		},
		Run: runDoctorToolSurfaces,
	}
}

var surfaceKindTokens = map[string]string{
	"agent-profile": "agent_profile", "charter-skill": "charter_skill", "command-file": "command_file", "command-skill": "command_skill",
	"context-file": "context_file", "context_file": "context_file", "hook": "hook", "native-config": "native_config",
	"native_config": "native_config", "plugin-manifest": "plugin_manifest", "plugin_manifest": "plugin_manifest", "rule": "rule",
}

// profileCapability is tool_surface.profiles.capability_matrix.
var profileCapability = map[string]struct {
	native bool
	reason string
}{
	"claude": {true, "Native: .claude/agents/<id>.md"}, "copilot": {true, "Native: .github/agents/<id>.agent.md"},
	"codex": {true, "Native: .codex/agents/<id>.toml"}, "auggie": {true, "Native: .augment/agents/<id>.md"},
	"q": {true, "User-global: ~/.aws/amazonq/cli-agents/<id>.json"}, "windsurf": {false, "No native agent primitive; use workflow/rule surfaces"},
	"cursor": {false, "No native agent primitive; use rule surfaces"}, "kiro": {false, "No native agent primitive; use prompt surfaces"},
	"gemini": {false, "No native agent primitive; use command surfaces"}, "llxprt": {false, "No native agent primitive; use command surfaces"},
	"qwen": {false, "No native agent primitive; use command surfaces"}, "opencode": {false, "No native agent primitive; use command surfaces"},
	"kilocode": {false, "No native agent primitive; use workflow surfaces"}, "antigravity": {false, "No native agent primitive; use workflow surfaces"},
	"vibe": {false, "No native agent primitive; use skill surfaces"}, "pi": {false, "No native agent primitive; use skill surfaces"},
	"letta": {false, "No native agent primitive; use skill surfaces"},
}

type surfaceFinding struct {
	code, severity, message, tool, surfaceID, path, repair string
	details                                                *jsonx.Object
}

func (f surfaceFinding) object() *jsonx.Object {
	nz := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	d := f.details
	if d == nil {
		d = &jsonx.Object{}
	}
	return jsonx.NewObject("code", f.code, "severity", f.severity, "message", f.message, "tool_key", nz(f.tool), "surface_id", nz(f.surfaceID),
		"path", nz(f.path), "repair_command", nz(f.repair), "docs_ref", nil, "details", d)
}

type surfaceStatus struct {
	id, tool, kind, provider, path, sourceKind, state string
	findings                                          []surfaceFinding
}

func (s surfaceStatus) object() *jsonx.Object {
	var repair any
	for _, f := range s.findings {
		if f.repair != "" {
			repair = f.repair
			break
		}
	}
	return jsonx.NewObject("id", s.id, "tool", s.tool, "kind", s.kind, "provider", s.provider, "path", s.path, "state", s.state,
		"source_kind", s.sourceKind, "manifest", nil, "repair_command", repair)
}

const (
	repairCommandSkill = "spec-kitty doctor tool-surfaces --kind command-skill --fix"
	repairCommandFile  = "spec-kitty doctor tool-surfaces --kind command-file --fix"
	repairContextFile  = "spec-kitty doctor tool-surfaces --kind context_file --fix"
	repairHook         = "spec-kitty doctor tool-surfaces --kind hook --fix"
	repairNative       = "spec-kitty doctor tool-surfaces --kind native_config --fix"
	repairCharterSkill = "spec-kitty doctor tool-surfaces --kind charter-skill --fix"
	repairProfile      = "spec-kitty doctor tool-surfaces --kind agent-profile --fix"
)

func one(code, sev, msg, tool, id, path, repair string) []surfaceFinding {
	return []surfaceFinding{{code: code, severity: sev, message: msg, tool: tool, surfaceID: id, path: path, repair: repair}}
}

func commandSkillSurfaces(root, tool string) []surfaceStatus {
	if !agents.IsCommandSkillAgent(tool) {
		return nil
	}
	m := agents.LoadCommandSkillsManifest(root)
	v := verifyCommandSkills(root)
	in := func(l []string, s string) bool { return contains(l, s) }
	var out []surfaceStatus
	probe := func(rel, id string) surfaceStatus {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		s := surfaceStatus{id: id, tool: tool, kind: "command_skill", provider: "command_skills", path: abs, sourceKind: "generated", state: "present"}
		switch {
		case in(v.unsafe, rel):
			s.state, s.findings = "unsafe", one("unsafe-managed-path", "error", "Command skill manifest path resolves outside the project: "+abs, tool, id, abs, "")
		case in(v.stale, rel):
			s.state, s.findings = "stale", one("stale-generated-surface", "warning", "Command skill is no longer canonical: "+abs, tool, id, abs, "")
		case in(v.orphans, rel):
			s.state, s.findings = "orphaned", one("unmanaged-spec-kitty-surface", "warning", "Spec Kitty-looking command skill is not owned by the manifest: "+abs, tool, id, abs, "")
		case in(v.gaps, rel) || !exists(abs):
			s.state, s.findings = "missing", one("generated-surface-missing", "error", fmt.Sprintf("Command skill for %s is missing: %s", tool, abs), tool, id, abs, repairCommandSkill)
		case m[rel] != nil && !contains(m[rel].Agents, tool):
			s.state, s.findings = "missing", one("configured-tool-surface-uninstalled", "error", "Configured tool is not recorded as an owner of command skill: "+abs, tool, id, abs, repairCommandSkill)
		case m[rel] != nil:
			if b, err := os.ReadFile(abs); err == nil && sha256Hex(b) != m[rel].ContentHash {
				c := strings.TrimSuffix(strings.TrimPrefix(rel, ".agents/skills/spec-kitty."), "/SKILL.md")
				if want, err := agents.RenderCommandSkill(root, c); err != nil || string(want) != string(b) {
					s.state, s.findings = "drifted", one("managed-file-drift", "warning",
						fmt.Sprintf("Command skill drifted from manifest hash: %s; run 'spec-kitty doctor tool-surfaces --fix' to restore it", abs), tool, id, abs, "spec-kitty doctor tool-surfaces --fix")
				}
			}
		}
		return s
	}
	expected := map[string]bool{}
	for _, c := range agents.CommandSkillCommands {
		rel := agents.CommandSkillPath(c)
		expected[rel] = true
		out = append(out, probe(rel, tool+".command_skill.spec-kitty."+c))
	}
	extra := map[string]bool{}
	for _, l := range [][]string{v.orphans, v.stale, v.unsafe} {
		for _, r := range l {
			if !expected[r] {
				extra[r] = true
			}
		}
	}
	for _, rel := range sortedKeys(extra) {
		id := tool + ".command_skill." + strings.ReplaceAll(strings.TrimPrefix(rel, ".agents/skills/"), "/", ".")
		out = append(out, probe(rel, id))
	}
	return out
}

func slashCommandSurfaces(tool string) []surfaceStatus {
	if _, ok := agents.CommandConfigs[tool]; !ok {
		id := tool + ".command_file.<unsupported>"
		return []surfaceStatus{{id: id, tool: tool, kind: "command_file", provider: "slash_commands", path: "<unsupported>", sourceKind: "user_global", state: "not_applicable",
			findings: []surfaceFinding{{code: "research-gap-surface", severity: "info", message: "No known slash-command implementation for " + tool + ".", tool: tool, surfaceID: id}}}}
	}
	dir := agents.GlobalCommandDir(tool)
	marker := agents.VersionMarkerPrefix + " " + app.Version
	var out []surfaceStatus
	for _, c := range consumerCommandsSorted() {
		name := agents.CommandFilename(c, tool)
		p := filepath.Join(dir, name)
		id := tool + ".command_file." + name
		s := surfaceStatus{id: id, tool: tool, kind: "command_file", provider: "slash_commands", path: p, sourceKind: "user_global", state: "present"}
		b, err := os.ReadFile(p)
		switch {
		case err != nil && !exists(p):
			s.state, s.findings = "missing", one("generated-surface-missing", "error", "User-global slash command missing: "+p, tool, id, p, repairCommandFile)
		case err == nil:
			lines := strings.Split(string(b), "\n")
			if len(lines) > 20 {
				lines = lines[:20]
			}
			if !strings.Contains(strings.Join(lines, "\n"), marker) {
				s.state, s.findings = "stale", one("stale-generated-surface", "warning", "User-global slash command is stale: "+p, tool, id, p, repairCommandFile)
			}
		}
		out = append(out, s)
	}
	return out
}

var reOrientationVersion = regexp.MustCompile(`\*\*Spec Kitty v([^*\n]+)\*\*`)

func presenceSurfaces(root, tool, kind string) []surfaceStatus {
	w := agents.WriterFor(tool)
	if w.RulesPath == "" {
		id := tool + ".context_file.<unsupported>"
		return []surfaceStatus{{id: id, tool: tool, kind: "context_file", provider: "session_presence", path: "<unsupported>", sourceKind: "generated", state: "not_applicable",
			findings: []surfaceFinding{{code: "research-gap-surface", severity: "info", message: "No known session-presence mechanism for " + tool + ".", tool: tool, surfaceID: id}}}}
	}
	fileKind := "context_file"
	if strings.Contains("/"+w.RulesPath, "/rules/") || strings.Contains("/"+w.RulesPath, "/steering/") || strings.Contains(w.RulesPath, ".mdc") {
		fileKind = "rule"
	}
	var out []surfaceStatus
	if kind == fileKind {
		p := filepath.Join(root, filepath.FromSlash(w.RulesPath))
		id := tool + "." + fileKind + "." + filepath.Base(p)
		s := surfaceStatus{id: id, tool: tool, kind: fileKind, provider: "session_presence", path: p, sourceKind: "generated", state: "present"}
		b, err := os.ReadFile(p)
		switch {
		case err != nil || !strings.Contains(string(b), agents.SectionOpen):
			if fileKind == "context_file" {
				s.state, s.findings = "missing", one("context-file-missing", "error", fmt.Sprintf("Always-on context file missing for %s: %s", tool, p), tool, id, p, repairContextFile)
			} else {
				s.state, s.findings = "missing", one("session-presence-incomplete", "error", fmt.Sprintf("Session-presence rule file missing for %s: %s", tool, p), tool, id, p, repairContextFile)
			}
		default:
			if m := reOrientationVersion.FindStringSubmatch(string(b)); m != nil && m[1] != app.Version {
				s.state, s.findings = "stale", one("stale-generated-surface", "warning", fmt.Sprintf("Orientation version stale for %s: %s", tool, p), tool, id, p, repairContextFile)
			}
		}
		out = append(out, s)
	}
	if kind == "hook" && w.Claude {
		p := filepath.Join(root, ".claude", "settings.json")
		ok := agents.HookRegistered(root, "SessionStart", "spec-kitty session-start") && agents.HookRegistered(root, "Stop", "spec-kitty session-stop")
		for i := 0; i < 2; i++ {
			s := surfaceStatus{id: tool + ".hook.settings.json", tool: tool, kind: "hook", provider: "session_presence", path: p, sourceKind: "generated", state: "present"}
			if !ok {
				s.state, s.findings = "missing", one("session-presence-incomplete", "error", fmt.Sprintf("Session-presence hook entry missing for %s: %s", tool, p), tool, s.id, p, repairHook)
			}
			out = append(out, s)
		}
	}
	return out
}

func nativeConfigSurfaces(root, tool string) []surfaceStatus {
	if tool != "vibe" {
		id := tool + ".native_config.<unsupported>"
		return []surfaceStatus{{id: id, tool: tool, kind: "native_config", provider: "native_config", path: "<unsupported>", sourceKind: "generated", state: "not_applicable",
			findings: []surfaceFinding{{code: "research-gap-surface", severity: "info", message: "No known native-config glue for " + tool + ".", tool: tool, surfaceID: id}}}}
	}
	p := filepath.Join(root, ".vibe", "config.toml")
	s := surfaceStatus{id: "vibe.native_config.config.toml", tool: tool, kind: "native_config", provider: "native_config", path: p, sourceKind: "generated", state: "present"}
	if !agents.VibeSkillPathConfigured(root) {
		s.state, s.findings = "missing", one("native-config-missing", "error", fmt.Sprintf("Native-config glue missing for %s: %s", tool, p), tool, s.id, p, repairNative)
	}
	return []surfaceStatus{s}
}

func charterSkillSurfaces(root, tool string) []surfaceStatus {
	skillRoot := agents.ProjectSkillRoot(tool)
	if skillRoot == "" {
		return nil
	}
	skills := append([]agents.Skill(nil), agents.Skills()...)
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	type entry struct{ rel, id, hash string }
	var entries []entry
	idx := map[string]int{}
	for _, s := range skills {
		for _, f := range s.Files {
			b, err := agents.ReadSkillFile(s, f)
			if err != nil {
				continue
			}
			if f == "SKILL.md" {
				b = []byte(agents.EnsureSkillFrontmatter(string(b), s.Name))
			}
			rel := strings.TrimSuffix(skillRoot, "/") + "/" + s.Name + "/" + f
			idx[rel] = len(entries)
			entries = append(entries, entry{rel, tool + ".charter_skill." + s.Name + "." + strings.ReplaceAll(f, "/", "."), "sha256:" + sha256Hex(b)})
		}
	}
	if m := agents.LoadSkillsManifest(root); m != nil {
		for _, e := range m.Entries {
			if e.AgentKey != tool {
				continue
			}
			if _, ok := idx[e.InstalledPath]; !ok {
				idx[e.InstalledPath] = len(entries)
				entries = append(entries, entry{e.InstalledPath, tool + ".charter_skill." + e.SkillName + "." + strings.ReplaceAll(e.SourceFile, "/", "."), e.ContentHash})
			}
		}
	}
	var out []surfaceStatus
	for _, e := range entries {
		p := filepath.Join(root, filepath.FromSlash(e.rel))
		s := surfaceStatus{id: e.id, tool: tool, kind: "charter_skill", provider: "managed_skills", path: p, sourceKind: "generated", state: "present"}
		st, err := os.Lstat(p)
		drift := func() {
			s.state, s.findings = "drifted", one("managed-file-drift", "warning", "Managed charter skill drifted from manifest hash: "+p, tool, e.id, p, repairCharterSkill)
		}
		switch {
		case err != nil:
			s.state, s.findings = "missing", one("generated-surface-missing", "error", fmt.Sprintf("Managed charter skill for %s is missing: %s", tool, p), tool, e.id, p, repairCharterSkill)
		case st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular():
			drift()
		default:
			if b, err := os.ReadFile(p); err != nil || "sha256:"+sha256Hex(b) != e.hash {
				drift()
			}
		}
		out = append(out, s)
	}
	return out
}

func profileManifestHashes(root string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(agents.ProfileManifestPath(root))
	if err != nil {
		return out
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return out
	}
	for _, e := range o.GetList("entries") {
		if eo, ok := e.(*jsonx.Object); ok {
			out[eo.GetString("output_path")] = eo.GetString("file_hash")
		}
	}
	return out
}

func profileSurfaces(root, tool string) []surfaceStatus {
	if c, ok := profileCapability[tool]; ok && !c.native {
		id := tool + ".agent_profile.<not-applicable>"
		return []surfaceStatus{{id: id, tool: tool, kind: "agent_profile", provider: "agent_profiles", path: "<not-applicable>", sourceKind: "generated", state: "not_applicable",
			findings: []surfaceFinding{{code: "profile-projection-unsupported", severity: "info", tool: tool, surfaceID: id,
				message: fmt.Sprintf("%s does not support native agent profile projection; profiles are exposed through other surfaces instead. Reason: %s", tool, c.reason),
				details: jsonx.NewObject("status", "not_applicable", "reason", c.reason)}}}}
	}
	h, ok := agents.ProfileHarnesses[tool]
	profiles, err := agents.BuiltinProfiles()
	if !ok || err != nil || len(profiles) == 0 {
		id := tool + ".agent_profile.<unsupported>"
		return []surfaceStatus{{id: id, tool: tool, kind: "agent_profile", provider: "agent_profiles", path: "<unsupported>", sourceKind: "generated", state: "not_applicable",
			findings: []surfaceFinding{{code: "research-gap-surface", severity: "info", tool: tool, surfaceID: id,
				message: fmt.Sprintf("No verified native agent-profile primitive for %s; profiles are not projected.", tool), details: jsonx.NewObject("status", "research_gap")}}}}
	}
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	hashes := profileManifestHashes(root)
	var out []surfaceStatus
	for _, p := range profiles {
		rel := h.OutputPath(p)
		abs := filepath.Join(root, filepath.FromSlash(rel))
		id := tool + ".agent_profile." + filepath.Base(abs)
		s := surfaceStatus{id: id, tool: tool, kind: "agent_profile", provider: "agent_profiles", path: abs, sourceKind: "generated", state: "present"}
		b, err := os.ReadFile(abs)
		switch {
		case err != nil:
			s.state, s.findings = "missing", one("native-agent-profile-missing", "error", "Native agent profile is missing: "+abs, tool, id, abs, repairProfile)
		case hashes[rel] != "" && sha256Hex(b) != hashes[rel]:
			s.state, s.findings = "drifted", one("native-agent-profile-drift", "warning", "Native agent profile drifted from manifest hash: "+abs, tool, id, abs, repairProfile)
		}
		out = append(out, s)
	}
	out = append(out, surfaceStatus{id: tool + ".agent_profile.<profile-diagnostics>", tool: tool, kind: "agent_profile", provider: "agent_profiles", path: root,
		sourceKind: "generated", state: "not_applicable", findings: []surfaceFinding{{code: "profile-sentinel-skipped", severity: "info", tool: tool,
			surfaceID: "agent_profile:human-in-charge", message: "Sentinel profile 'human-in-charge' is a workflow routing signal and is intentionally not projected."}}})
	return out
}

func pluginBundleSurfaces(root string) []surfaceStatus {
	var out []surfaceStatus
	for _, t := range [][2]string{{"claude_code_plugin", ".claude-plugin/plugin.json"}, {"copilot_skill_package", "plugin.json"}, {"vscode_extension", "plugin.json"}} {
		p := filepath.Join(root, "dist", "spec-kitty-plugins", t[0], filepath.FromSlash(t[1]))
		s := surfaceStatus{id: t[0] + ".plugin_manifest.plugin.json", tool: t[0], kind: "plugin_manifest", provider: "plugin_bundle", path: p, sourceKind: "generated", state: "present"}
		if !exists(p) {
			s.state = "missing"
		}
		out = append(out, s)
	}
	return out
}

// CollectToolSurfaces is SurfaceStatusService.collect over the plans of
// tools (+ the plugin bundle when unfiltered), optionally kind-filtered.
func CollectToolSurfaces(root string, tools []string, bundle bool, kinds map[string]bool) []surfaceStatus {
	var all []surfaceStatus
	for _, t := range tools {
		all = append(all, commandSkillSurfaces(root, t)...)
		all = append(all, slashCommandSurfaces(t)...)
		for _, k := range []string{"context_file", "hook", "rule"} {
			all = append(all, presenceSurfaces(root, t, k)...)
		}
		all = append(all, nativeConfigSurfaces(root, t)...)
		all = append(all, charterSkillSurfaces(root, t)...)
		all = append(all, profileSurfaces(root, t)...)
	}
	if bundle {
		all = append(all, pluginBundleSurfaces(root)...)
	}
	if kinds == nil {
		return all
	}
	var out []surfaceStatus
	for _, s := range all {
		if kinds[s.kind] {
			out = append(out, s)
		}
	}
	return out
}

func runDoctorToolSurfaces(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonOut := ctx.Bool("json")
	root, err := doctorProjectRoot(w, jsonOut, 2)
	if err != nil {
		return err
	}
	var kinds map[string]bool
	for _, tok := range ctx.Strings("kind") {
		k, ok := surfaceKindTokens[tok]
		if !ok {
			var known []string
			for t := range surfaceKindTokens {
				known = append(known, t)
			}
			sort.Strings(known)
			msg := fmt.Sprintf("Unknown surface kind '%s'. Known kinds: %s.", tok, strings.Join(known, ", "))
			if jsonOut {
				printJSONRaw(w, jsonError("unknown_kind", msg))
			} else {
				out(w, "[red]Error:[/red] "+msg)
			}
			return cli.Exit(2)
		}
		if kinds == nil {
			kinds = map[string]bool{}
		}
		kinds[k] = true
	}
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		if jsonOut {
			printJSONRaw(w, jsonError("config_error", err.Error()))
		} else {
			out(w, "[red]Error:[/red] "+escapeMarkup(err.Error()))
		}
		return cli.Exit(2)
	}
	configured := append([]string(nil), ac.Available...)
	sort.Strings(configured)
	configured = dedupe(configured)
	tools := configured
	filter := ctx.OptString("tool")
	if filter != nil {
		tools = nil
		for _, t := range configured {
			if t == *filter {
				tools = append(tools, t)
			}
		}
	}
	bundle := len(tools) > 0 && filter == nil
	statuses := CollectToolSurfaces(root, tools, bundle, kinds)
	var repair *jsonx.Object
	if ctx.Bool("fix") {
		var repaired, skipped []string
		needs := false
		for _, s := range statuses {
			if s.state == "missing" || s.state == "drifted" || s.state == "stale" {
				needs = true
			}
		}
		if needs {
			o := &upgradeOutcome{}
			bootstrap.EnsureAll(bootstrap.Options{})
			kittify.ProvisionDefaultMissionTypeActivations(root)
			applyUpgradeRepairs(root, o)
			if contains(tools, "claude") {
				if b, err := agents.PrepareClaudeHooks(root); err == nil && b != nil {
					os.MkdirAll(filepath.Join(root, ".claude"), 0o755)
					os.WriteFile(filepath.Join(root, ".claude", "settings.json"), b, 0o644)
				}
			}
		}
		after := CollectToolSurfaces(root, tools, bundle, kinds)
		byID := map[string]string{}
		for _, s := range after {
			byID[s.id+"\x00"+s.path] = s.state
		}
		for _, s := range statuses {
			switch s.state {
			case "missing", "drifted", "stale":
				if byID[s.id+"\x00"+s.path] == "present" {
					repaired = append(repaired, s.id)
				} else if s.kind == "plugin_manifest" {
					skipped = append(skipped, s.id)
				}
			}
		}
		statuses = after
		var fa []any
		for _, s := range after {
			for _, f := range s.findings {
				fa = append(fa, f.object())
			}
		}
		if fa == nil {
			fa = []any{}
		}
		repair = jsonx.NewObject("repaired", strList(repaired), "skipped", strList(skipped), "failed", []any{}, "dry_run", false, "findings_after", fa)
	}
	present, missing, drifted, warnings, errors := 0, 0, 0, 0, 0
	var findings []surfaceFinding
	for _, s := range statuses {
		switch s.state {
		case "present":
			present++
		case "missing":
			missing++
		case "drifted":
			drifted++
		}
		for _, f := range s.findings {
			findings = append(findings, f)
			switch f.severity {
			case "error":
				errors++
			case "warning":
				warnings++
			}
		}
	}
	ok := errors == 0
	if jsonOut {
		sl, fl := []any{}, []any{}
		for _, s := range statuses {
			sl = append(sl, s.object())
		}
		for _, f := range findings {
			fl = append(fl, f.object())
		}
		payload := jsonx.NewObject("ok", ok, "schema_version", 1, "project_root", root, "configured_tools", strList(tools),
			"summary", jsonx.NewObject("surfaces", len(statuses), "present", present, "missing", missing, "drifted", drifted, "warnings", warnings, "errors", errors),
			"surfaces", sl, "findings", fl)
		if repair != nil {
			payload.Set("repair", repair)
		}
		printJSONRaw(w, payload)
		return exitOrNil(boolCode(!ok))
	}
	printToolSurfacesHuman(w, ok, len(statuses), findings, repair)
	return exitOrNil(boolCode(!ok))
}

func printToolSurfacesHuman(w io.Writer, ok bool, n int, findings []surfaceFinding, repair *jsonx.Object) {
	if ok && len(findings) == 0 {
		out(w, fmt.Sprintf("[green]Tool Surfaces[/green]: all surfaces healthy (%d checked)", n))
		return
	}
	out(w, "\n[bold]Tool Surfaces[/bold] - issue(s) found\n")
	for _, f := range findings {
		colour := "yellow"
		if f.severity == "error" {
			colour = "red"
		}
		out(w, fmt.Sprintf("  [%s]![/%s] [%s] %s", colour, colour, f.code, f.message))
	}
	if repair != nil {
		if r := repair.GetList("repaired"); len(r) > 0 {
			out(w, fmt.Sprintf("\n[green]Repaired:[/green] %d surface(s)", len(r)))
		}
	}
}

func dedupe(s []string) []string {
	var out []string
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}
