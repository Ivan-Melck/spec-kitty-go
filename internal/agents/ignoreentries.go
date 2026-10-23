package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const AgentBlockBegin = "# >>> Spec Kitty agent files (auto-managed; lines in this block are replaced) >>>"
const AgentBlockEnd = "# <<< Spec Kitty agent files <<<"

var SecretIgnoreEntries = []string{".codex/auth.json", ".codex/history.jsonl", ".codex/sessions/", ".codex/log/", ".claude/settings.local.json", ".gemini/.env", ".qwen/.env", ".llxprt/.env"}
var StateDirIgnoreEntries = []string{".vibe/", ".pi/", ".letta/", ".github/copilot/"}
var reservedSkillPatterns = []string{"spec-kitty/", "spec-kitty-*/", "spec-kitty.*/", "spk-*/"}

func IgnoredAgentKeys(root string) ([]string, error) {
	config, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil, err
	}
	if len(config.Available) > 0 || config.ExplicitAgentList {
		return config.Available, nil
	}
	return append([]string{}, Keys...), nil
}

func AgentIgnoreEntries(root string, keys []string) []string {
	generated := map[string]bool{}
	for _, pattern := range reservedSkillPatterns {
		generated[".agents/skills/"+pattern] = true
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		if !IsKnown(key) {
			continue
		}
		wanted[key] = true
		if command, ok := CommandConfigs[key]; ok {
			generated[command.Dir+"/spec-kitty.*"] = true
		}
		if skillRoot := ProjectSkillRoot(key); skillRoot != "" {
			skillRoot = strings.TrimSuffix(skillRoot, "/") + "/"
			for _, pattern := range reservedSkillPatterns {
				generated[skillRoot+pattern] = true
			}
			for _, skill := range Skills() {
				name := skill.Name
				if name == "spec-kitty" || strings.HasPrefix(name, "spec-kitty-") || strings.HasPrefix(name, "spec-kitty.") || strings.HasPrefix(name, "spk-") {
					continue
				}
				generated[skillRoot+name+"/"] = true
			}
		}
		switch key {
		case "cursor":
			generated[".cursor/rules/spec-kitty.mdc"] = true
		case "windsurf":
			generated[".windsurf/rules/spec-kitty.md"] = true
		case "kiro":
			generated[".kiro/steering/spec-kitty.md"] = true
		}
	}
	if manifest := LoadSkillsManifest(root); manifest != nil {
		for _, entry := range manifest.Entries {
			if !wanted[entry.AgentKey] {
				continue
			}
			origin := ""
			for _, kv := range entry.extra {
				if kv.Key == "origin" {
					origin, _ = kv.Value.(string)
				}
			}
			if origin != "pack" {
				continue
			}
			for _, skillRoot := range SkillConfigs[entry.AgentKey].Roots {
				prefix := strings.TrimSuffix(skillRoot, "/") + "/"
				if rest, ok := strings.CutPrefix(entry.InstalledPath, prefix); ok {
					parts := strings.Split(rest, "/")
					if len(parts) > 1 {
						generated[prefix+parts[0]+"/"] = true
						break
					}
				}
			}
		}
	}
	for output := range loadProfileManifest(root) {
		if filepath.IsAbs(output) {
			rel, err := filepath.Rel(root, output)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			output = rel
		}
		generated[filepath.ToSlash(output)] = true
	}
	fixed := append(append([]string{}, SecretIgnoreEntries...), StateDirIgnoreEntries...)
	for _, entry := range fixed {
		delete(generated, entry)
	}
	var tail []string
	for entry := range generated {
		tail = append(tail, entry)
	}
	sort.Strings(tail)
	return append(fixed, tail...)
}

func ReplaceAgentBlock(text string, entries []string, create bool) (string, bool, error) {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := SplitLines(text)
	var begins, ends []int
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case AgentBlockBegin:
			begins = append(begins, i)
		case AgentBlockEnd:
			ends = append(ends, i)
		}
	}
	block := append(append([]string{AgentBlockBegin}, entries...), AgentBlockEnd)
	var updated []string
	switch {
	case len(begins) == 0 && len(ends) == 0:
		if !create {
			return text, false, nil
		}
		updated = append(updated, lines...)
		if len(updated) > 0 && strings.TrimSpace(updated[len(updated)-1]) != "" {
			updated = append(updated, "")
		}
		updated = append(updated, block...)
	case len(begins) == 1 && len(ends) == 1 && begins[0] < ends[0]:
		updated = append(updated, lines[:begins[0]]...)
		updated = append(updated, block...)
		updated = append(updated, lines[ends[0]+1:]...)
	default:
		return "", false, fmt.Errorf(".gitignore has a damaged Spec Kitty agent-files block (%d begin and %d end line(s)); keep exactly one '%s' ... '%s' pair, or delete both lines, then re-run", len(begins), len(ends), AgentBlockBegin, AgentBlockEnd)
	}
	out := strings.Join(updated, newline) + newline
	return out, out != text, nil
}

func SyncAgentBlock(root string, keys []string, create bool) (bool, error) {
	if keys == nil {
		var err error
		keys, err = IgnoredAgentKeys(root)
		if err != nil {
			return false, err
		}
	}
	path := filepath.Join(root, ".gitignore")
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		return false, fmt.Errorf(".gitignore is a symlink to '%s'; refusing to read or write through it: %s", target, path)
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	out, changed, err := ReplaceAgentBlock(string(b), AgentIgnoreEntries(root, keys), create)
	if err != nil || !changed {
		return changed, err
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
}

func RefreshAgentBlock(root string) string {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return ""
	}
	if _, err := SyncAgentBlock(root, nil, false); err != nil {
		return ".gitignore agent-files block not refreshed: " + err.Error()
	}
	return ""
}
