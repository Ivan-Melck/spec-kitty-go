package agents

import (
	"fmt"
	"regexp"
	"strings"
)

// CommandSkillCommands is command_installer.CANONICAL_COMMANDS (sorted).
var CommandSkillCommands = ConsumerCommands

// cliWrapperSkills are the two command skills not backed by a prompt.md.
var cliWrapperSkills = map[string][2]string{
	"consolidate": {"Consolidate an accepted mission", "spec-kitty consolidate"},
	"status":      {"Show mission and work package status", "spec-kitty agent tasks status"},
}

// UserInputReplacement is _user_input_block.REPLACEMENT_BLOCK.
const UserInputReplacement = "## User Input\n\n" +
	"The content of the user's message that invoked this skill " +
	"(everything after the skill invocation token, e.g. after " +
	"`/spec-kitty-<command>` or `$spec-kitty-<command>`) is the User Input " +
	"referenced elsewhere in these instructions.\n\n" +
	"You **MUST** consider this user input before proceeding (if not empty).\n\n"

var (
	reSkillFrontmatter = regexp.MustCompile(`(?s)^\s*---\n(.*?)---\n?`)
	reUserInputStart   = regexp.MustCompile(`(?m)^## +User Input[ \t\r\f\v]*$`)
	reTermHeading      = regexp.MustCompile(`(?m)^#{1,2} +\S`)
	rePurpose          = regexp.MustCompile(`(?m)^## +Purpose[ \t\r\f\v]*$`)
	reAnyHeading       = regexp.MustCompile(`(?m)^#{1,6} +\S`)
	reSentenceEnd      = regexp.MustCompile(`[.?!\n]`)
)

func userInputSpan(body string) (int, int, bool) {
	m := reUserInputStart.FindStringIndex(body)
	if m == nil {
		return 0, 0, false
	}
	end := len(body)
	if t := reTermHeading.FindStringIndex(body[m[1]:]); t != nil {
		end = m[1] + t[0]
	}
	return m[0], end, true
}

func frontmatterDescription(text string) string {
	m := reSkillFrontmatter.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	for _, line := range SplitLines(m[1]) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			v := strings.TrimSpace(line[len("description:"):])
			if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
				v = v[1 : len(v)-1]
			}
			return truncRunes(v, 140)
		}
	}
	return ""
}

func purposeDescription(body string) string {
	m := rePurpose.FindStringIndex(body)
	if m == nil {
		return ""
	}
	end := len(body)
	if h := reAnyHeading.FindStringIndex(body[m[1]:]); h != nil {
		end = m[1] + h[0]
	}
	section := strings.TrimSpace(body[m[1]:end])
	if section == "" {
		return ""
	}
	var sentence string
	if e := reSentenceEnd.FindStringIndex(section); e != nil {
		sentence = strings.TrimRight(strings.TrimSpace(section[:e[1]]), ".")
	} else {
		sentence = strings.TrimSpace(section)
	}
	sentence = strings.TrimSpace(sentence)
	return truncRunes(sentence, 140)
}

// RenderCommandSkill returns the SKILL.md bytes of .agents/skills/spec-kitty.<cmd>/
// (command_installer._render_command_skill). root is the project whose
// charter activation gates SPDD/REASONS blocks ("" = inactive).
func RenderCommandSkill(root, command string) ([]byte, error) {
	if w, ok := cliWrapperSkills[command]; ok {
		desc, cli := w[0], w[1]
		body := "# /spec-kitty." + command + " - " + desc + "\n\n" +
			"## Purpose\n\n" +
			"Run the canonical Spec Kitty CLI command for this workflow and treat its output as authoritative.\n\n" +
			"Do not rediscover mission context from branches, files, prompt contents, or separate charter loads. If mission selection is required, pass `--mission <handle>` where `<handle>` is a mission_id, mid8, or mission_slug.\n\n" +
			"## User Input\n\n" +
			"The content of the user's message that invoked this skill is the User Input. Consider it before proceeding. If it contains CLI arguments, append them to the command below.\n\n" +
			"## Steps\n\n" +
			"Run this command from the repository root:\n\n" +
			"```bash\n" + cli + " <user-provided-args-if-any>\n```\n\n" +
			"Report the command output and follow any next-step instructions it prints.\n"
		body = PrependUpgradeCheck(body)
		if !strings.HasPrefix(body, "\n") {
			body = "\n" + body
		}
		return []byte(commandSkillFrontmatter(command, desc) + "\n" + RewriteCommandInvocations(body)), nil
	}
	raw, err := ReadTemplate(command)
	if err != nil {
		return nil, fmt.Errorf("template_not_found: %s", command)
	}
	raw = NormalizeNewlines(raw)
	active := false
	if strings.Contains(raw, reasonsStart) || strings.Contains(raw, reasonsEnd) {
		if active, err = IsSPDDActive(root); err != nil {
			return nil, err
		}
	}
	raw, err = ProcessSPDDBlocks(raw, active)
	if err != nil {
		return nil, err
	}
	stripped := raw
	if loc := reSkillFrontmatter.FindStringIndex(raw); loc != nil {
		stripped = raw[:loc[0]] + raw[loc[1]:]
	}
	s, e, ok := userInputSpan(stripped)
	if !ok {
		return nil, fmt.Errorf("user_input_block_missing: %s", command)
	}
	body := stripped[:s] + UserInputReplacement + stripped[e:]
	if strings.Contains(body, "$ARGUMENTS") {
		return nil, fmt.Errorf("stray_arguments_token: %s", command)
	}
	descBody := stripped[:s] + stripped[e:]
	desc := frontmatterDescription(raw)
	if desc == "" {
		desc = purposeDescription(descBody)
	}
	if desc == "" {
		desc = firstParagraphDescription(descBody)
	}
	if desc == "" {
		desc = "Spec Kitty " + command + " workflow"
	}
	body = PrependUpgradeCheck(body)
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	fm := commandSkillFrontmatter(command, desc)
	return []byte(fm + RewriteCommandInvocations(body)), nil
}

func commandSkillFrontmatter(command, description string) string {
	return "---\nname: " + skillYAMLScalar(CommandSkillName(command)) + "\ndescription: " + skillYAMLScalar(description) + "\nmetadata:\n  user-invocable: \"true\"\n---"
}
