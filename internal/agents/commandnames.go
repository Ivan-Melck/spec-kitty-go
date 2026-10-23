package agents

import (
	"regexp"
	"strings"
)

func CommandSkillName(command string) string       { return "spec-kitty-" + command }
func LegacyCommandSkillName(command string) string { return "spec-kitty." + command }
func LegacyCommandSkillPath(command string) string {
	return ".agents/skills/" + LegacyCommandSkillName(command) + "/SKILL.md"
}

// ParseCommandSkillPath uses the explicit command set: the prefix is also
// shared by charter skills, which must never be mistaken for command skills.
func ParseCommandSkillPath(rel string, legacy bool) string {
	for _, c := range CommandSkillCommands {
		path := CommandSkillPath(c)
		if legacy {
			path = LegacyCommandSkillPath(c)
		}
		if rel == path {
			return c
		}
	}
	return ""
}

var commandInvocation = regexp.MustCompile(`(/skill:|/|\$)spec-kitty\.([a-z0-9-]+)`)
var hyphenInvocation = regexp.MustCompile(`(/skill:|/|\$)spec-kitty-([a-z0-9-]+)`)

func RewriteCommandInvocations(text string) string {
	return rewriteInvocations(text, false)
}

func rewriteInvocations(text string, legacy bool) string {
	known := map[string]bool{}
	for _, c := range CommandSkillCommands {
		known[c] = true
	}
	var out strings.Builder
	pos := 0
	pattern := commandInvocation
	if legacy {
		pattern = hyphenInvocation
	}
	for _, m := range pattern.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[0], m[1]
		command := text[m[4]:m[5]]
		if !known[command] {
			continue
		}
		if start > 0 {
			c := text[start-1]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("_.:/$-", rune(c)) {
				continue
			}
		}
		if end < len(text) {
			c := text[end]
			if c == '_' || (c >= 'A' && c <= 'Z') {
				continue
			}
			if c == '.' && end+1 < len(text) {
				c = text[end+1]
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
					continue
				}
			}
		}
		out.WriteString(text[pos:start])
		out.WriteString(text[m[2]:m[3]])
		name := CommandSkillName(command)
		if legacy {
			name = LegacyCommandSkillName(command)
		}
		out.WriteString(name)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
}

func LegacyCommandSkillRendering(content []byte, command string) ([]byte, error) {
	text, err := ProjectSkillForRoot(string(content), ".claude/skills", CommandSkillName(command))
	if err != nil {
		return nil, err
	}
	text = strings.Replace(text, "name: "+CommandSkillName(command)+"\n", "name: "+LegacyCommandSkillName(command)+"\n", 1)
	text = rewriteInvocations(text, true)
	text = strings.ReplaceAll(text, "spec-kitty-<command>`", "spec-kitty.<command>`")
	return []byte(text), nil
}

func SkillInvocationToken(agent, command string) string {
	if !IsCommandSkillAgent(agent) {
		return "/" + LegacyCommandSkillName(command)
	}
	sigil := "/"
	switch agent {
	case "codex":
		sigil = "$"
	case "pi":
		sigil = "/skill:"
	}
	return sigil + CommandSkillName(command)
}
