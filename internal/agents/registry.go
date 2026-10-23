// Package agents is the registry of the 17 supported AI agent harnesses and
// everything Spec Kitty renders for them: user-global slash commands,
// command skills (codex/vibe/pi/letta), doctrine skill installs, native agent
// profile projections and session-presence (orientation) surfaces.
//
// It mirrors specify_cli.core.config, runtime/agent_commands.py,
// shims/generator.py, template/asset_generator.py, skills/* and
// session_presence/* of the upstream CLI.
package agents

import "sort"

// Keys lists AI_CHOICES in upstream (dict insertion) order.
var Keys = []string{
	"copilot", "claude", "gemini", "cursor", "qwen", "opencode", "codex", "windsurf",
	"kilocode", "auggie", "q", "kiro", "antigravity", "vibe", "pi", "letta", "llxprt",
}

// DisplayNames maps agent keys to AI_CHOICES labels.
var DisplayNames = map[string]string{
	"copilot":     "GitHub Copilot",
	"claude":      "Claude Code",
	"gemini":      "Gemini CLI",
	"cursor":      "Cursor",
	"qwen":        "Qwen Code",
	"opencode":    "opencode",
	"codex":       "Codex CLI",
	"windsurf":    "Windsurf",
	"kilocode":    "Kilo Code",
	"auggie":      "Auggie CLI",
	"q":           "Amazon Q Developer CLI (legacy; use 'kiro')",
	"kiro":        "Kiro CLI (formerly Amazon Q Developer CLI)",
	"antigravity": "Google Antigravity",
	"vibe":        "Mistral Vibe",
	"pi":          "Pi",
	"letta":       "Letta Code",
	"llxprt":      "LLxprt Code",
}

// IsKnown reports whether key is a valid agent key.
func IsKnown(key string) bool { _, ok := DisplayNames[key]; return ok }

// Display returns the human label for key (the key itself when unknown).
func Display(key string) string {
	if d, ok := DisplayNames[key]; ok {
		return d
	}
	return key
}

// CommandConfig is one AGENT_COMMAND_CONFIG entry.
type CommandConfig struct {
	Dir       string // legacy project-relative dir (also the user-global relative dir)
	Ext       string // md, toml, prompt.md
	ArgFormat string // $ARGUMENTS or {{args}}
}

// CommandAgents lists AGENT_COMMAND_CONFIG keys in upstream order (13 agents
// that receive user-global slash command files).
var CommandAgents = []string{
	"claude", "gemini", "copilot", "cursor", "qwen", "opencode", "windsurf",
	"kilocode", "auggie", "q", "kiro", "llxprt",
}

// CommandConfigs is AGENT_COMMAND_CONFIG.
var CommandConfigs = map[string]CommandConfig{
	"claude":   {".claude/commands", "md", "$ARGUMENTS"},
	"gemini":   {".gemini/commands", "toml", "{{args}}"},
	"copilot":  {".github/prompts", "prompt.md", "$ARGUMENTS"},
	"cursor":   {".cursor/commands", "md", "$ARGUMENTS"},
	"qwen":     {".qwen/commands", "toml", "{{args}}"},
	"opencode": {".opencode/command", "md", "$ARGUMENTS"},
	"windsurf": {".windsurf/workflows", "md", "$ARGUMENTS"},
	"kilocode": {".kilocode/workflows", "md", "$ARGUMENTS"},
	"auggie":   {".augment/commands", "md", "$ARGUMENTS"},
	"q":        {".amazonq/prompts", "md", "$ARGUMENTS"},
	"kiro":     {".kiro/prompts", "md", "$ARGUMENTS"},
	"llxprt":   {".llxprt/commands", "toml", "{{args}}"},
}

// SortedCommandAgents returns CommandAgents sorted alphabetically.
func SortedCommandAgents() []string {
	out := append([]string(nil), CommandAgents...)
	sort.Strings(out)
	return out
}

// Skill installation classes.
const (
	SkillClassShared  = "shared-root-capable"
	SkillClassNative  = "native-root-required"
	SkillClassWrapper = "wrapper-only"
)

// SkillConfig is one AGENT_SKILL_CONFIG entry.
type SkillConfig struct {
	Class string
	Roots []string // project-relative, with trailing slash
}

// SkillConfigs is AGENT_SKILL_CONFIG (iteration order = Keys order below).
var SkillConfigs = map[string]SkillConfig{
	"claude":      {SkillClassNative, []string{".claude/skills/"}},
	"copilot":     {SkillClassShared, []string{".agents/skills/", ".github/skills/"}},
	"gemini":      {SkillClassShared, []string{".agents/skills/", ".gemini/skills/"}},
	"cursor":      {SkillClassShared, []string{".agents/skills/", ".cursor/skills/"}},
	"qwen":        {SkillClassNative, []string{".qwen/skills/"}},
	"opencode":    {SkillClassShared, []string{".agents/skills/", ".opencode/skills/"}},
	"windsurf":    {SkillClassShared, []string{".agents/skills/", ".windsurf/skills/"}},
	"codex":       {SkillClassShared, []string{".agents/skills/"}},
	"vibe":        {SkillClassShared, []string{".agents/skills/"}},
	"pi":          {SkillClassShared, []string{".agents/skills/", ".pi/skills/"}},
	"letta":       {SkillClassShared, []string{".agents/skills/"}},
	"kilocode":    {SkillClassNative, []string{".kilocode/skills/"}},
	"auggie":      {SkillClassShared, []string{".agents/skills/", ".augment/skills/"}},
	"q":           {SkillClassWrapper, nil},
	"kiro":        {SkillClassShared, []string{".agents/skills/", ".kiro/skills/"}},
	"antigravity": {SkillClassShared, []string{".agents/skills/", ".agent/skills/"}},
	"llxprt":      {SkillClassShared, []string{".agents/skills/", ".llxprt/skills/"}},
}

// SkillConfigOrder is the AGENT_SKILL_CONFIG dict order.
var SkillConfigOrder = []string{
	"claude", "copilot", "gemini", "cursor", "qwen", "opencode", "windsurf", "codex",
	"vibe", "pi", "letta", "kilocode", "auggie", "q", "kiro", "antigravity", "llxprt",
}

// SkillClass returns the installation class of key ("" when unknown).
func SkillClass(key string) string { return SkillConfigs[key].Class }

// CommandSkillAgents receive .agents/skills/spec-kitty.<cmd>/SKILL.md command skills.
var CommandSkillAgents = []string{"codex", "vibe", "pi", "letta", "antigravity"}

// IsCommandSkillAgent reports whether key consumes command skills.
func IsCommandSkillAgent(key string) bool {
	for _, k := range CommandSkillAgents {
		if k == key {
			return true
		}
	}
	return false
}

// ToolRequirements is AGENT_TOOL_REQUIREMENTS: agent -> (executable, install URL).
var ToolRequirements = map[string][2]string{
	"claude":   {"claude", "https://docs.anthropic.com/en/docs/claude-code/setup"},
	"gemini":   {"gemini", "https://github.com/google-gemini/gemini-cli"},
	"qwen":     {"qwen", "https://github.com/QwenLM/qwen-code"},
	"opencode": {"opencode", "https://opencode.ai"},
	"codex":    {"codex", "https://github.com/openai/codex"},
	"auggie":   {"auggie", "https://docs.augmentcode.com/cli/setup-auggie/install-auggie-cli"},
	"q":        {"q", "https://aws.amazon.com/developer/learning/q-developer-cli/"},
	"vibe":     {"vibe", "https://github.com/mistralai/mistral-vibe"},
	"kiro":     {"kiro-cli", "https://kiro.dev/docs/cli/"},
	"pi":       {"pi", "https://pi.dev/docs/latest"},
	"letta":    {"letta", "https://docs.letta.com/letta-code/cli/"},
	"llxprt":   {"llxprt", "https://github.com/vybestack/llxprt-code"},
}

// IDEAgents do not require a CLI installation.
var IDEAgents = map[string]bool{"cursor": true, "windsurf": true, "copilot": true, "kilocode": true, "antigravity": true}

// FolderNotice is init's "Agent Folder Security" map.
var FolderNotice = map[string]string{
	"claude": ".claude/", "gemini": ".gemini/", "cursor": ".cursor/", "qwen": ".qwen/",
	"opencode": ".opencode/", "codex": ".agents/skills/", "vibe": ".vibe/", "windsurf": ".windsurf/",
	"kilocode": ".kilocode/", "auggie": ".augment/", "copilot": ".github/", "antigravity": ".agents/skills/",
	"q": ".amazonq/", "kiro": ".kiro/", "pi": ".agents/skills/", "letta": ".agents/skills/", "llxprt": ".llxprt/",
}

// AgentDirectory is one gitignore_manager.AGENT_DIRECTORIES entry.
type AgentDirectory struct {
	Name, Directory, Description string
	Special                      bool
}

// GitignoreDirectories is AGENT_DIRECTORIES (order matters for .gitignore).
var GitignoreDirectories = []AgentDirectory{
	{"claude", ".claude/", "Claude Code CLI", false},
	{"codex", ".codex/", "Codex (contains auth.json)", false},
	{"vibe", ".vibe/", "Mistral Vibe (runtime state, config, session logs)", false},
	{"pi", ".pi/", "Pi (runtime state, auth, session logs)", false},
	{"letta", ".letta/", "Letta Code (runtime state, auth, memory)", false},
	{"opencode", ".opencode/", "opencode CLI", false},
	{"windsurf", ".windsurf/", "Windsurf", false},
	{"gemini", ".gemini/", "Google Gemini", false},
	{"llxprt", ".llxprt/", "LLxprt Code", false},
	{"cursor", ".cursor/rules/spec-kitty.mdc", "Cursor (Spec Kitty orientation rule)", false},
	{"cursor", ".cursor/commands/", "Cursor (Spec Kitty slash commands)", false},
	{"cursor", ".cursor/skills/", "Cursor (declared Spec Kitty skill root; AGENT_SKILL_CONFIG)", false},
	{"qwen", ".qwen/", "Qwen", false},
	{"kilocode", ".kilocode/", "Kilocode", false},
	{"auggie", ".augment/", "Auggie", false},
	{"amazonq", ".amazonq/", "Amazon Q", false},
	{"kiro", ".kiro/", "Kiro CLI (rebrand of Amazon Q — registered in PR #626)", false},
	{"antigravity", ".agent/", "Google Antigravity", false},
	{"copilot", ".github/copilot/", "GitHub Copilot (user settings)", true},
}

// AgentDirs is agent_utils.directories.AGENT_DIRS (legacy project command dirs).
var AgentDirs = [][2]string{
	{".claude", "commands"}, {".github", "prompts"}, {".gemini", "commands"}, {".cursor", "commands"},
	{".qwen", "commands"}, {".opencode", "command"}, {".windsurf", "workflows"}, {".kilocode", "workflows"},
	{".augment", "commands"}, {".amazonq", "prompts"}, {".kiro", "prompts"}, {".agent", "workflows"},
	{".llxprt", "commands"},
}

// AgentDirToKey is agent_utils.directories.AGENT_DIR_TO_KEY.
var AgentDirToKey = map[string]string{
	".claude": "claude", ".github": "copilot", ".gemini": "gemini", ".cursor": "cursor",
	".qwen": "qwen", ".opencode": "opencode", ".windsurf": "windsurf", ".kilocode": "kilocode",
	".augment": "auggie", ".amazonq": "q", ".kiro": "kiro", ".agent": "antigravity",
	".llxprt": "llxprt",
}

// ParseSelection splits an --ai value on ',' / ';', lowercases, trims and
// dedupes preserving order. Unknown keys are returned separately.
func ParseSelection(raw string) (selected, invalid []string) {
	seen := map[string]bool{}
	cur := ""
	flush := func() {
		k := lowerTrim(cur)
		cur = ""
		if k == "" {
			return
		}
		if !IsKnown(k) {
			invalid = append(invalid, k)
			return
		}
		if !seen[k] {
			seen[k] = true
			selected = append(selected, k)
		}
	}
	for _, r := range raw {
		if r == ',' || r == ';' {
			flush()
			continue
		}
		cur += string(r)
	}
	flush()
	return selected, invalid
}

func lowerTrim(s string) string {
	b := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	out := string(b)
	// strip whitespace
	start, end := 0, len(out)
	for start < end && isSpace(out[start]) {
		start++
	}
	for end > start && isSpace(out[end-1]) {
		end--
	}
	return out[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
