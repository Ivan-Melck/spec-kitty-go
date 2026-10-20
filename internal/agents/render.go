package agents

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// CommandTemplatesDir is the asset directory holding the software-dev step
// prompts used to render slash commands and command skills.
const CommandTemplatesDir = "packs/built-in/missions/mission-steps/software-dev"

// PromptDrivenCommands are rendered from <cmd>/prompt.md (sorted).
var PromptDrivenCommands = []string{"analyze", "charter", "plan", "research", "specify", "tasks", "tasks-outline", "tasks-packages"}

// CLIDrivenCommands get generated thin shims (sorted).
var CLIDrivenCommands = []string{"accept", "consolidate", "implement", "review", "status", "tasks-finalize"}

// ConsumerCommands is the sorted union (the 14 consumer skills).
var ConsumerCommands = func() []string {
	out := append(append([]string(nil), PromptDrivenCommands...), CLIDrivenCommands...)
	sort.Strings(out)
	return out
}()

// ShimDescriptions is shims.generator.SHIM_DESCRIPTIONS.
var ShimDescriptions = map[string]string{
	"implement":      "Execute a work package implementation",
	"review":         "Review a work package implementation",
	"accept":         "Validate an approved mission before merge",
	"consolidate":    "Consolidate an accepted mission",
	"status":         "Show mission and work package status",
	"tasks-finalize": "Finalize a mission's work packages",
}

// VersionMarkerPrefix opens the managed command-file marker line.
const VersionMarkerPrefix = "<!-- spec-kitty-command-version:"

// VersionMarker returns the marker line (without newline) for version.
func VersionMarker(version string) string {
	return VersionMarkerPrefix + " " + version + " -->"
}

// UpgradeCheckBlock is agent_upgrade_prompt.AGENT_UPGRADE_CHECK_BLOCK.
const UpgradeCheckBlock = "## Startup Upgrade Check\n\n" +
	"Run this at most once per active agent session before the first Spec Kitty command workflow.\n" +
	"If you already ran `spec-kitty upgrade --agent-check --json` in this session, reuse that result and skip this block.\n" +
	"Do not run or announce an upgrade check again for later Spec Kitty commands in the same session.\n" +
	"Otherwise, before continuing, run:\n\n" +
	"```bash\nspec-kitty upgrade --agent-check --json\n```\n\n" +
	"If JSON `action` is `none`, continue.\n" +
	"If `action` is `auto_upgrade`, run `upgrade_command` before continuing. If it fails, tell the user and continue with the current Spec Kitty version.\n" +
	"If `action` is `guidance`, show `upgrade_note` briefly, then continue.\n" +
	"If `action` is `prompt`, ask the user with the host-native question UI when available:\n\n" +
	"`Spec Kitty {latest_version} is available. You are on {installed_version}. Upgrade now?`\n\n" +
	"Use these choices:\n\n" +
	"1. Upgrade now (recommended) - record `upgrade_now`, run `upgrade_command`, then continue.\n" +
	"2. Always keep me up to date - record `always`, run `upgrade_command`, then continue.\n" +
	"3. Not now - record `not_now`, then continue.\n" +
	"4. Never ask again - record `never_ask`, then continue.\n\n" +
	"Record the selected choice before continuing:\n\n" +
	"```bash\nspec-kitty upgrade --agent-choice <upgrade_now|always|not_now|never_ask> --agent-latest <latest_version> --json\n```\n\n" +
	"If no host-native question UI is available, present the same four choices in plain text and wait for the user.\n" +
	"In non-interactive hosts, choose `not_now` and continue.\n"

// PrependUpgradeCheck is prepend_agent_upgrade_check.
func PrependUpgradeCheck(body string) string {
	if strings.Contains(body, "spec-kitty upgrade --agent-check --json") {
		return body
	}
	if body == "" {
		return UpgradeCheckBlock
	}
	sep := "\n\n"
	if strings.HasPrefix(body, "\n") {
		sep = "\n"
	}
	return UpgradeCheckBlock + sep + body
}

// ---------------------------------------------------------------------------
// Python text helpers
// ---------------------------------------------------------------------------

// SplitLines mimics Python str.splitlines() (no keepends).
func SplitLines(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		brk := 0
		switch r {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\x85', ' ', ' ':
			brk = size
		case '\r':
			brk = size
			if i+1 < len(s) && s[i+1] == '\n' {
				out = append(out, s[start:i])
				i += 2
				start = i
				continue
			}
		}
		if brk > 0 {
			out = append(out, s[start:i])
			i += brk
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// NormalizeNewlines converts CRLF and lone CR to LF.
func NormalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func stripBOM(b []byte) []byte { return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) }

// ---------------------------------------------------------------------------
// SPDD/REASONS blocks
// ---------------------------------------------------------------------------

const (
	reasonsStart = "<!-- spdd:reasons-block:start -->"
	reasonsEnd   = "<!-- spdd:reasons-block:end -->"
)

// ProcessSPDDBlocks is charter.offering.spdd_reasons.process_spdd_blocks.
func ProcessSPDDBlocks(text string, active bool) (string, error) {
	if !strings.Contains(text, reasonsStart) && !strings.Contains(text, reasonsEnd) {
		return text, nil
	}
	depth := 0
	for _, line := range SplitLines(text) {
		st := strings.TrimSpace(line)
		if st == reasonsStart {
			if depth != 0 {
				return "", fmt.Errorf("Nested spdd:reasons-block start marker; flat blocks only.")
			}
			depth++
		} else if st == reasonsEnd {
			if depth == 0 {
				return "", fmt.Errorf("spdd:reasons-block end marker has no matching start.")
			}
			depth--
		}
	}
	if depth != 0 {
		return "", fmt.Errorf("spdd:reasons-block start marker has no matching end.")
	}
	trailing := strings.HasSuffix(text, "\n")
	lines := SplitLines(text)
	var out []string
	if active {
		for _, l := range lines {
			st := strings.TrimSpace(l)
			if st == reasonsStart || st == reasonsEnd {
				continue
			}
			out = append(out, l)
		}
	} else {
		for i := 0; i < len(lines); {
			if strings.TrimSpace(lines[i]) == reasonsStart {
				if len(out) > 0 && out[len(out)-1] == "" {
					out = out[:len(out)-1]
				}
				i++
				for i < len(lines) && strings.TrimSpace(lines[i]) != reasonsEnd {
					i++
				}
				if i < len(lines) {
					i++
				}
				continue
			}
			out = append(out, lines[i])
			i++
		}
	}
	r := strings.Join(out, "\n")
	if trailing {
		r += "\n"
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Prompt-driven slash commands
// ---------------------------------------------------------------------------

// ParseFrontmatter is template.renderer.parse_frontmatter: returns metadata,
// body and the raw frontmatter text.
func ParseFrontmatter(content string) (*jsonx.Object, string, string) {
	normalized := strings.ReplaceAll(content, "\r", "")
	if !strings.HasPrefix(normalized, "---\n") {
		return &jsonx.Object{}, normalized, ""
	}
	closing := strings.Index(normalized[4:], "\n---")
	if closing < 0 {
		return &jsonx.Object{}, normalized, ""
	}
	closing += 4
	fmText := normalized[4:closing]
	bodyStart := closing + len("\n---")
	if bodyStart < len(normalized) && normalized[bodyStart] == '\n' {
		bodyStart++
	}
	body := normalized[bodyStart:]
	meta := &jsonx.Object{}
	if v, err := yamlx.Parse([]byte(fmText)); err == nil {
		if o, ok := v.(*jsonx.Object); ok {
			meta = o
		}
	}
	return meta, body, fmText
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }

// RewritePaths applies template.renderer.DEFAULT_PATH_PATTERNS in order.
func RewritePaths(s string) string {
	s = replaceNotPreceded(s, "scripts/", ".kittify/scripts/", func(pre string) bool { return strings.HasSuffix(pre, ".kittify/") })
	s = replaceNotPreceded(s, "templates/", ".kittify/templates/", func(pre string) bool {
		if !strings.HasSuffix(pre, "/") {
			return false
		}
		p := pre[:len(pre)-1]
		if p == "" {
			return false
		}
		r, _ := utf8.DecodeLastRuneInString(p)
		return isWordRune(r) || r == '.' || r == '-'
	})
	s = replaceNotPreceded(s, "memory/", ".kittify/memory/", func(pre string) bool { return strings.HasSuffix(pre, ".kittify/") })
	return s
}

// replaceNotPreceded replaces non-overlapping occurrences of needle unless
// blocked(prefix-of-original) is true (a negative lookbehind).
func replaceNotPreceded(s, needle, repl string, blocked func(pre string) bool) string {
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		j += i
		b.WriteString(s[i:j])
		if blocked(s[:j]) {
			b.WriteString(needle)
		} else {
			b.WriteString(repl)
		}
		i = j + len(needle)
	}
}

func filterFrontmatter(fm string) string {
	var out []string
	skipping := false
	for _, line := range SplitLines(fm) {
		st := strings.TrimSpace(line)
		if skipping {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			skipping = false
		}
		if st == "scripts:" || st == "agent_scripts:" {
			skipping = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return yamlx.FormatScalar(v)
	}
}

// ReadTemplate reads <dir>/<command>/prompt.md from the embedded assets.
func ReadTemplate(command string) (string, error) {
	b, err := assets.ReadFile(CommandTemplatesDir + "/" + command + "/prompt.md")
	if err != nil {
		return "", err
	}
	return string(stripBOM(b)), nil
}

// RenderCommandTemplate renders a prompt-driven command for agent
// (render_command_template with repo_root=None, i.e. SPDD inactive).
func RenderCommandTemplate(templateText, agentKey, version string) (string, error) {
	cfg, ok := CommandConfigs[agentKey]
	if !ok {
		cfg = CommandConfig{Ext: "md", ArgFormat: "$ARGUMENTS"}
	}
	text := strings.ReplaceAll(templateText, "\r", "")
	text, err := ProcessSPDDBlocks(text, false)
	if err != nil {
		return "", err
	}
	requiresScript := strings.Contains(text, "{SCRIPT}")
	meta, body, rawFM := ParseFrontmatter(text)
	scriptCmd, agentScript := "", ""
	if s := meta.GetObject("scripts"); s != nil {
		scriptCmd = s.GetString("sh")
	}
	if s := meta.GetObject("agent_scripts"); s != nil {
		agentScript = s.GetString("sh")
	}
	if requiresScript && scriptCmd == "" {
		return "", fmt.Errorf("Template requires scripts.sh but none was provided.")
	}
	for _, kv := range [][2]string{{"{SCRIPT}", scriptCmd}, {"{AGENT_SCRIPT}", agentScript}, {"{ARGS}", cfg.ArgFormat}, {"__AGENT__", agentKey}} {
		body = strings.ReplaceAll(body, kv[0], kv[1])
	}
	body = RewritePaths(body)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	description := ""
	if v, ok := meta.Get("description"); ok {
		description = strings.TrimSpace(pyStr(v))
	}
	fmClean := filterFrontmatter(rawFM)
	if fmClean != "" {
		fmClean = RewritePaths(fmClean)
	}
	marker := VersionMarker(version) + "\n"
	if _, known := CommandConfigs[agentKey]; known {
		body = PrependUpgradeCheck(body)
	}
	if cfg.Ext == "toml" {
		body = strings.ReplaceAll(body, "$ARGUMENTS", "{{args}}")
		dv := description
		if strings.HasPrefix(dv, `"`) && strings.HasSuffix(dv, `"`) && len(dv) >= 2 {
			dv = dv[1 : len(dv)-1]
		}
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body = strings.ReplaceAll(body, `\`, `\\`)
		body = strings.ReplaceAll(body, `"""`, `""\"`)
		return "description = " + jsonx.QuoteString(dv, false) + "\n\nprompt = \"\"\"\n" + marker + body + "\"\"\"\n", nil
	}
	var result string
	if fmClean != "" {
		result = "---\n" + fmClean + "\n---\n" + marker + "\n" + body
	} else {
		result = marker + "\n" + body
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// CLI-driven shims
// ---------------------------------------------------------------------------

var shimCommandMap = map[string]string{
	"implement":      "spec-kitty agent action implement {args} --agent {agent}",
	"review":         "spec-kitty agent action review {args} --agent {agent}",
	"accept":         "spec-kitty agent mission accept {args}",
	"status":         "spec-kitty agent tasks status {args}",
	"consolidate":    "spec-kitty consolidate {args}",
	"tasks-finalize": "spec-kitty agent mission finalize-tasks {args}",
}

// ShimContent is generate_shim_content_for_agent.
func ShimContent(command, agentKey, version string) (string, error) {
	cfg, ok := CommandConfigs[agentKey]
	if !ok {
		cfg = CommandConfig{Ext: "md", ArgFormat: "$ARGUMENTS"}
	}
	tpl, ok := shimCommandMap[command]
	if !ok {
		return "", fmt.Errorf("Unknown CLI-driven command '%s'.", command)
	}
	call := strings.ReplaceAll(strings.ReplaceAll(tpl, "{args}", cfg.ArgFormat), "{agent}", agentKey)
	desc, ok := ShimDescriptions[command]
	if !ok {
		desc = "spec-kitty " + command
	}
	body := "Run this exact command and treat its output as authoritative.\n" +
		"Do not rediscover context from branches, files, prompt contents, or separate charter loads.\n" +
		"When mission selection is required, pass --mission <handle> (mission_id, mid8, or mission_slug).\n" +
		"\n" +
		"`" + call + "`\n"
	marker := VersionMarker(version) + "\n"
	if cfg.Ext == "toml" {
		b := strings.ReplaceAll(marker+body, `"""`, `""\"`)
		return "description = \"" + desc + "\"\n\nprompt = \"\"\"\n" + b + "\"\"\"\n", nil
	}
	return "---\ndescription: " + desc + "\n---\n" + marker + body, nil
}

// RenderedFile is one rendered output.
type RenderedFile struct {
	Name    string
	Content []byte
}

// RenderAgentCommands renders the complete 14-file bundle for a command agent
// (prompt-driven first, then shims, each sorted), as _render_agent_commands.
func RenderAgentCommands(agentKey string) ([]RenderedFile, error) {
	version := app.Version
	var out []RenderedFile
	for _, cmd := range PromptDrivenCommands {
		tpl, err := ReadTemplate(cmd)
		if err != nil {
			return nil, err
		}
		c, err := RenderCommandTemplate(tpl, agentKey, version)
		if err != nil {
			return nil, err
		}
		out = append(out, RenderedFile{CommandFilename(cmd, agentKey), []byte(c)})
	}
	for _, cmd := range CLIDrivenCommands {
		c, err := ShimContent(cmd, agentKey, version)
		if err != nil {
			return nil, err
		}
		out = append(out, RenderedFile{CommandFilename(cmd, agentKey), []byte(c)})
	}
	return out, nil
}

var reMarkerLine = regexp.MustCompile(`(?m)^<!-- spec-kitty-command-version: ([^\r\n]+?) -->\r?\n`)

// IsCanonicalPredecessor is _is_canonical_predecessor: existing bytes carry
// a managed marker from another release, or this release with otherwise
// identical bytes.
func IsCanonicalPredecessor(existing, desired []byte, version string) bool {
	m := reMarkerLine.FindSubmatch(existing)
	if m == nil {
		return false
	}
	if strings.TrimSpace(string(m[1])) != version {
		return true
	}
	return bytes.Equal(reMarkerLine.ReplaceAll(existing, nil), reMarkerLine.ReplaceAll(desired, nil))
}

// HasCurrentVersionMarker checks the first 20 lines for this version's marker.
func HasCurrentVersionMarker(content []byte, version string) bool {
	want := VersionMarker(version)
	lines := SplitLines(string(content))
	if len(lines) > 20 {
		lines = lines[:20]
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}
