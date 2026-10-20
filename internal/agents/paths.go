package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/project"
)

// GlobalCommandDir returns the user-global slash-command directory for a
// command agent (get_global_command_dir), honouring OPENCODE_CONFIG_DIR,
// LLXPRT_CONFIG_HOME, XDG_CONFIG_HOME and the macOS/Windows llxprt roots.
func GlobalCommandDir(key string) string {
	home := project.UserHome()
	switch key {
	case "opencode":
		if v := os.Getenv("OPENCODE_CONFIG_DIR"); v != "" {
			return filepath.Join(expandUser(v), "commands")
		}
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(expandUser(v), "opencode", "commands")
		}
		return filepath.Join(home, ".config", "opencode", "commands")
	case "llxprt":
		if v := os.Getenv("LLXPRT_CONFIG_HOME"); v != "" {
			return filepath.Join(expandUser(v), "commands")
		}
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library", "Preferences", "llxprt-code", "commands")
		}
		if runtime.GOOS == "windows" {
			app := os.Getenv("APPDATA")
			if app == "" {
				app = filepath.Join(home, "AppData", "Roaming")
			}
			return filepath.Join(app, "llxprt-code", "Config", "commands")
		}
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(expandUser(v), "llxprt-code", "commands")
		}
		return filepath.Join(home, ".config", "llxprt-code", "commands")
	}
	cfg, ok := CommandConfigs[key]
	if !ok {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(cfg.Dir))
}

func expandUser(p string) string {
	if p == "~" {
		return project.UserHome()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(project.UserHome(), p[2:])
	}
	return p
}

// CommandFilename returns spec-kitty.<cmd>.<ext> for agent key.
func CommandFilename(command, key string) string {
	cfg, ok := CommandConfigs[key]
	if !ok {
		return "spec-kitty." + command + ".md"
	}
	if cfg.Ext == "" {
		return "spec-kitty." + command
	}
	return "spec-kitty." + command + "." + cfg.Ext
}

// ProjectSkillRoot returns the primary project skill root (e.g. ".claude/skills/")
// or "" for wrapper/unknown agents.
func ProjectSkillRoot(key string) string {
	c, ok := SkillConfigs[key]
	if !ok || c.Class == SkillClassWrapper || len(c.Roots) == 0 {
		return ""
	}
	return c.Roots[0]
}

// GlobalSkillRoot returns ~/<primary root> (no trailing slash) or "".
func GlobalSkillRoot(key string) string {
	r := ProjectSkillRoot(key)
	if r == "" {
		return ""
	}
	return filepath.Join(project.UserHome(), filepath.FromSlash(strings.Trim(r, "/")))
}

// InstallableSkillAgents returns agents with a skill root, in AGENT_SKILL_CONFIG order.
func InstallableSkillAgents() []string {
	var out []string
	for _, k := range SkillConfigOrder {
		if SkillConfigs[k].Class != SkillClassWrapper {
			out = append(out, k)
		}
	}
	return out
}

// UniqueGlobalSkillRoots returns the distinct global skill roots in agent order.
func UniqueGlobalSkillRoots() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range InstallableSkillAgents() {
		r := GlobalSkillRoot(k)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}
