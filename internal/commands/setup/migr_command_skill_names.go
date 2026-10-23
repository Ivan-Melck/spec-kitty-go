package setup

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
)

func legacyCommandFolders(root string) []string {
	var out []string
	for _, command := range agents.CommandSkillCommands {
		rel := agents.LegacyCommandSkillPath(command)
		if lexists(filepath.Join(root, filepath.FromSlash(path.Dir(rel)))) {
			out = append(out, rel)
		}
	}
	return out
}

func commandManifestDetail(err error) string {
	if e, ok := err.(*agents.InstallerError); ok && e.Code == "manifest_parse_failed" && e.Context != nil {
		return e.Context.GetString("detail")
	}
	return err.Error()
}

func legacyCommandLeftovers(root string, result migrationResult, retiring []string) migrationResult {
	covered := map[string]bool{}
	for _, p := range result.Preserved {
		covered[p] = true
		covered[path.Dir(p)+"/"] = true
	}
	for _, rel := range legacyCommandFolders(root) {
		if contains(retiring, rel) {
			continue
		}
		left := rel
		if !lexists(filepath.Join(root, filepath.FromSlash(rel))) {
			left = path.Dir(rel) + "/"
		}
		if covered[left] || covered[rel] {
			continue
		}
		result.Warnings = append(result.Warnings, agents.CommandSkillWarnings(agents.CommandSkillReport{LegacyUnrecorded: []string{left}})...)
		result.Preserved = append(result.Preserved, left)
	}
	return result
}

func mergeCommandMigrationReport(r *migrationResult, report agents.CommandSkillReport, dry bool) {
	verb := "Removed"
	if dry {
		verb = "Would remove"
	}
	for _, rel := range report.LegacyRemoved {
		r.Changes = append(r.Changes, verb+" legacy dotted command skill "+rel)
	}
	if !dry {
		for _, rel := range append(append([]string{}, report.Added...), report.Regenerated...) {
			r.Changes = append(r.Changes, "Installed command skill "+rel)
		}
	}
	for _, warning := range agents.CommandSkillWarnings(report) {
		if !contains(r.Warnings, warning) {
			r.Warnings = append(r.Warnings, warning)
		}
	}
	for _, rel := range append(append(append([]string{}, report.Collisions...), report.LegacyKept...), report.LegacyUnrecorded...) {
		if !contains(r.Preserved, rel) {
			r.Preserved = append(r.Preserved, rel)
		}
	}
}

func applyCommandSkillNames(root string, dry bool) migrationResult {
	r := migrationResult{Success: true}
	entries, err := toolsurface.LoadCommandManifest(root)
	if err != nil {
		r.Warnings = []string{fmt.Sprintf("Command-skill manifest .kittify/command-skills-manifest.json could not be read (%s); legacy dotted command skills were left in place — run `spec-kitty doctor skills`", commandManifestDetail(err))}
		return legacyCommandLeftovers(root, r, nil)
	}
	config, err := kittify.LoadAgentConfig(root)
	if err != nil {
		r.Warnings = []string{fmt.Sprintf("Agent config could not be read (%s); legacy dotted command skills were left in place — run `spec-kitty doctor skills`", err)}
		return legacyCommandLeftovers(root, r, nil)
	}
	var keys []string
	for _, key := range agents.CommandSkillAgents {
		if contains(config.Available, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		var next []toolsurface.CommandEntry
		changed := false
		verb := "Dropped"
		if dry {
			verb = "Would drop"
		}
		for _, e := range entries {
			if agents.ParseCommandSkillPath(e.Path, true) == "" {
				next = append(next, e)
				continue
			}
			if !lexists(filepath.Join(root, filepath.FromSlash(e.Path))) {
				r.Changes = append(r.Changes, verb+" manifest entry of absent legacy command skill "+e.Path)
				changed = true
			} else {
				next = append(next, e)
				r.Warnings = append(r.Warnings, "Legacy dotted command skill "+e.Path+" is still recorded but no command-skill agent is configured; it was left in place — run `spec-kitty doctor skills`")
				r.Preserved = append(r.Preserved, e.Path)
			}
		}
		if changed && !dry {
			if err := os.WriteFile(filepath.Join(root, ".kittify", agents.CommandSkillsManifestFile), toolsurface.SerializeCommandManifest(next), 0o644); err != nil {
				r.Success = false
				r.Errors = append(r.Errors, err.Error())
			}
		}
		return legacyCommandLeftovers(root, r, nil)
	}
	selected := keys
	if dry {
		selected = keys[:1]
	}
	report, err := toolsurface.InstallCommandsForAgents(root, selected, clock.NowUTCISO(), dry)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Command-skill install for %s was refused (%s); legacy dotted command skills were left in place — run `spec-kitty doctor skills`", strings.Join(selected, ", "), err))
		return legacyCommandLeftovers(root, r, nil)
	}
	mergeCommandMigrationReport(&r, report, dry)
	var retiring []string
	if dry {
		retiring = report.LegacyRemoved
	}
	return legacyCommandLeftovers(root, r, retiring)
}

func init() {
	migrationImpls["4.0.0rc8_command_skill_hyphen_names"] = migrationImpl{
		detect: func(root string) bool {
			if !isDir(filepath.Join(root, ".kittify")) {
				return false
			}
			entries, err := toolsurface.LoadCommandManifest(root)
			if err != nil {
				return true
			}
			for _, entry := range entries {
				if agents.ParseCommandSkillPath(entry.Path, true) != "" {
					return true
				}
			}
			return len(legacyCommandFolders(root)) > 0
		}, apply: applyCommandSkillNames,
	}
}
