package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

var legacyAgentIgnoreEntries = map[string]bool{
	".claude/": true, ".codex/": true, ".vibe/": true, ".pi/": true, ".letta/": true, ".opencode/": true, ".windsurf/": true, ".gemini/": true, ".llxprt/": true, ".cursor/": true,
	".cursor/rules/spec-kitty.mdc": true, ".cursor/commands/": true, ".cursor/skills/": true, ".qwen/": true, ".kilocode/": true, ".augment/": true, ".roo/": true, ".amazonq/": true, ".kiro/": true, ".agent/": true, ".github/copilot/": true, ".agents/skills/": true,
}
var appendedAgentBackfills = map[string]bool{".agents/skills/": true, ".cursor/rules/spec-kitty.mdc": true, ".cursor/commands/": true, ".cursor/skills/": true, ".pi/": true, ".letta/": true}

const oldAgentIgnoreHeader = "# AI Agent directories (added by Spec Kitty CLI)"
const oldAgentIgnoreNote = "# These contain auth tokens and should NEVER be committed"

type narrowIgnorePlan struct {
	before, updated string
	removed, rules  []string
	forms           map[string]string
}

func planNarrowAgentGitignore(root string) (*narrowIgnorePlan, error) {
	content, _, err := readGitignoreText(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil, err
	}
	lines := agents.SplitLines(content)
	owned := map[int]bool{}
	forms := map[string]string{}
	for entry := range legacyAgentIgnoreEntries {
		if !strings.HasSuffix(entry, "/") {
			continue
		}
		bare := strings.TrimSuffix(entry, "/")
		for _, prefix := range []string{"", "/", "**/"} {
			for _, suffix := range []string{"", "/", "/*", "/**"} {
				forms[prefix+bare+suffix] = entry
			}
		}
	}
	inRun, afterMarker := false, false
	var headers []int
	rules := []string{}
	for index, line := range lines {
		stripped := strings.TrimSpace(line)
		if stripped == agents.GitignoreMarker || stripped == oldAgentIgnoreHeader || stripped == oldAgentIgnoreNote {
			inRun = true
			afterMarker = afterMarker || stripped == agents.GitignoreMarker
			if stripped != agents.GitignoreMarker {
				headers = append(headers, index)
			}
			continue
		}
		if stripped == "" {
			continue
		}
		runEntry := legacyAgentIgnoreEntries[stripped]
		for _, prefix := range []string{".kittify/", "kitty-specs/", "kitty-ops/", ".worktrees/"} {
			runEntry = runEntry || strings.HasPrefix(stripped, prefix)
		}
		if strings.HasPrefix(stripped, "#") || !runEntry {
			inRun = false
		}
		ours := inRun && legacyAgentIgnoreEntries[stripped] || afterMarker && appendedAgentBackfills[stripped]
		if stripped == ".cursor/" {
			ours = inRun && index > 0 && index < len(lines)-1 && strings.TrimSpace(lines[index-1]) == ".gemini/" && strings.TrimSpace(lines[index+1]) == ".qwen/"
		}
		if ours {
			owned[index] = true
		} else if folder, ok := forms[stripped]; ok && !slices.Contains(agents.StateDirIgnoreEntries, folder) {
			rules = append(rules, stripped)
		}
	}
	for _, index := range headers {
		next := -1
		for i := index + 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line != "" && line != oldAgentIgnoreHeader && line != oldAgentIgnoreNote {
				next = i
				break
			}
		}
		if next < 0 || owned[next] {
			owned[index] = true
		}
	}
	for index, line := range lines {
		if strings.TrimSpace(line) != agents.GitignoreMarker {
			continue
		}
		next := -1
		for i := index + 1; i < len(lines); i++ {
			if !owned[i] && strings.TrimSpace(lines[i]) != "" {
				next = i
				break
			}
		}
		if next < 0 || strings.HasPrefix(strings.TrimSpace(lines[next]), "#") {
			owned[index] = true
		}
	}
	var kept, removed []string
	justRemoved := false
	for index, line := range lines {
		if owned[index] {
			justRemoved = true
			if stripped := strings.TrimSpace(line); strings.HasPrefix(stripped, ".") {
				removed = append(removed, stripped)
			}
			continue
		}
		if justRemoved && strings.TrimSpace(line) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		justRemoved = false
		kept = append(kept, line)
	}
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	narrowed := ""
	if len(kept) > 0 {
		narrowed = strings.Join(kept, newline) + newline
	}
	keys, err := agents.IgnoredAgentKeys(root)
	if err != nil {
		return nil, err
	}
	updated, _, err := agents.ReplaceAgentBlock(narrowed, agents.AgentIgnoreEntries(root, keys), true)
	if err != nil {
		return nil, err
	}
	return &narrowIgnorePlan{content, updated, removed, rules, forms}, nil
}

func newlyVisibleAgentPaths(root string, removed []string) []string {
	if len(removed) == 0 {
		return nil
	}
	paths := append([]string{}, removed...)
	sort.Strings(paths)
	args := append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}, paths...)
	r, err := gitx.Exec(root, []string{"GIT_OPTIONAL_LOCKS=0"}, nil, args...)
	if err != nil || r.Code != 0 {
		return nil
	}
	var out []string
	for _, entry := range strings.Split(r.Stdout, "\x00") {
		if strings.HasPrefix(entry, "?? ") {
			out = append(out, filepath.ToSlash(entry[3:]))
		}
	}
	sort.Strings(out)
	return out
}

func init() {
	migrationImpls["4.0.0rc8_narrow_agent_gitignore"] = migrationImpl{
		detect: func(root string) bool {
			p, e := planNarrowAgentGitignore(root)
			return e != nil || p.before != p.updated
		},
		canApply: func(root string) (bool, string) {
			if _, e := os.Stat(root); e != nil {
				return false, "Project path does not exist: " + root
			}
			if _, e := planNarrowAgentGitignore(root); e != nil {
				return false, ".gitignore cannot be narrowed: " + e.Error()
			}
			return true, ""
		},
		apply: func(root string, dry bool) migrationResult {
			p, e := planNarrowAgentGitignore(root)
			if e != nil {
				return migrationResult{Errors: []string{e.Error()}}
			}
			r := migrationResult{Success: true}
			rules := map[string]bool{}
			for _, rule := range p.rules {
				rules[rule] = true
			}
			var ordered []string
			for rule := range rules {
				ordered = append(ordered, rule)
			}
			sort.Strings(ordered)
			for _, rule := range ordered {
				r.Warnings = append(r.Warnings, fmt.Sprintf("Kept your own ignore rule '%s' (Spec Kitty did not write it); remove it if your team commits files under %s", rule, p.forms[rule]))
			}
			verb := "Removed"
			if dry {
				verb = "Would remove"
			}
			for _, entry := range p.removed {
				r.Changes = append(r.Changes, fmt.Sprintf("%s agent ignore line: '%s'", verb, entry))
			}
			if p.before != p.updated {
				writeVerb := "Wrote"
				if dry {
					writeVerb = "Would write"
				}
				r.Changes = append(r.Changes, writeVerb+" the Spec Kitty agent-files block in .gitignore")
				if !dry {
					path := filepath.Join(root, ".gitignore")
					if st, e := os.Lstat(path); e == nil && st.Mode()&os.ModeSymlink != 0 {
						return migrationResult{Errors: []string{"Refusing to write symlinked .gitignore: " + path}}
					}
					if e := os.WriteFile(path, []byte(p.updated), 0o644); e != nil {
						return migrationResult{Errors: []string{e.Error()}}
					}
					visible := newlyVisibleAgentPaths(root, p.removed)
					if len(visible) > 0 {
						n := len(visible)
						extra := ""
						if n > 20 {
							visible = visible[:20]
							extra = fmt.Sprintf(" and %d more", n-20)
						}
						r.Warnings = append(r.Warnings, fmt.Sprintf("Git now shows %d file(s) that the old agent ignore rules hid: %s%s. Review them, then commit the ones your team shares or add your own ignore rule for the rest.", n, strings.Join(visible, ", "), extra))
					}
				}
			}
			if len(r.Changes) == 0 {
				r.Changes = []string{"Agent ignore rules already narrow"}
			}
			r.ManualReview = len(r.Warnings) > 0
			if r.ManualReview {
				r.Preserved = []string{filepath.Join(root, ".gitignore")}
			}
			return r
		}}
}
