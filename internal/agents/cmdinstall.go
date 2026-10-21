package agents

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// CommandSkillsManifestFile is .kittify/command-skills-manifest.json.
const CommandSkillsManifestFile = "command-skills-manifest.json"

// CommandSkillPath is the project-relative command skill path.
func CommandSkillPath(command string) string {
	return ".agents/skills/spec-kitty." + command + "/SKILL.md"
}

// CommandSkillEntry is one command-skills manifest entry.
type CommandSkillEntry struct {
	Path             string
	Agents           []string
	ContentHash      string
	InstalledAt      string
	SpecKittyVersion string
}

// LoadCommandSkillsManifest reads entries keyed by path (empty when absent).
func LoadCommandSkillsManifest(root string) map[string]*CommandSkillEntry {
	out := map[string]*CommandSkillEntry{}
	b, err := os.ReadFile(filepath.Join(root, ".kittify", CommandSkillsManifestFile))
	if err != nil {
		return out
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return out
	}
	for _, x := range o.GetList("entries") {
		eo, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		e := &CommandSkillEntry{Path: eo.GetString("path"), ContentHash: eo.GetString("content_hash"), InstalledAt: eo.GetString("installed_at"), SpecKittyVersion: eo.GetString("spec_kitty_version")}
		for _, a := range eo.GetList("agents") {
			if s, ok := a.(string); ok {
				e.Agents = append(e.Agents, s)
			}
		}
		out[e.Path] = e
	}
	return out
}

// SaveCommandSkillsManifest writes the manifest (sort_keys, indent 2, UTF-8).
func SaveCommandSkillsManifest(root string, entries map[string]*CommandSkillEntry) error {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	l := []any{}
	for _, k := range keys {
		e := entries[k]
		if len(e.Agents) == 0 {
			continue
		}
		ag := make([]any, len(e.Agents))
		for i, a := range e.Agents {
			ag[i] = a
		}
		l = append(l, jsonx.NewObject("agents", ag, "content_hash", e.ContentHash, "installed_at", e.InstalledAt, "path", e.Path, "spec_kitty_version", e.SpecKittyVersion))
	}
	p := filepath.Join(root, ".kittify", CommandSkillsManifestFile)
	content := jsonx.Dumps(jsonx.NewObject("entries", l, "schema_version", 1), jsonx.Options{Indent: 2, SortKeys: true}) + "\n"
	if b, err := os.ReadFile(p); err == nil && string(b) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeAtomic(p, []byte(content), 0o644)
}

// VibeSkillPath is the shared skills root vibe must discover.
const VibeSkillPath = ".agents/skills"

var reSkillPathsLine = regexp.MustCompile(`(?m)^skill_paths\s*=`)

// VibeSkillPathConfigured is vibe_config.skill_path_configured (approximate
// TOML read: a top-level skill_paths naming ".agents/skills").
func VibeSkillPathConfigured(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".vibe", "config.toml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			return false
		}
		if strings.HasPrefix(t, "skill_paths") && strings.Contains(t, `"`+VibeSkillPath+`"`) {
			return true
		}
	}
	return false
}

// EnsureVibeSkillPath writes/extends .vibe/config.toml so vibe discovers
// .agents/skills.
func EnsureVibeSkillPath(root string) error {
	if VibeSkillPathConfigured(root) {
		return nil
	}
	p := filepath.Join(root, ".vibe", "config.toml")
	b, err := os.ReadFile(p)
	line := `skill_paths = [".agents/skills"]` + "\n"
	var out string
	switch {
	case err != nil:
		out = line
	case reSkillPathsLine.Match(b):
		loc := reSkillPathsLine.FindIndex(b)
		rest := string(b[loc[1]:])
		end := strings.IndexByte(rest, '\n')
		val := strings.TrimSpace(rest)
		if end >= 0 {
			val = strings.TrimSpace(rest[:end])
		}
		var nv string
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(val[1 : len(val)-1])
			if inner == "" {
				nv = `[".agents/skills"]`
			} else {
				nv = "[" + strings.TrimRight(inner, ", ") + `, ".agents/skills"]`
			}
		} else {
			nv = "[" + val + `, ".agents/skills"]`
		}
		tail := ""
		if end >= 0 {
			tail = rest[end:]
		}
		out = string(b[:loc[0]]) + "skill_paths = " + nv + tail
	default:
		out = line + string(b)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writePresenceFile(p, []byte(out))
}
