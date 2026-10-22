package toolsurface

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// Pack-skill finding kinds (pack_skill_drift).
const (
	PackKindDrift        = "drift"
	PackKindStale        = "stale"
	PackKindOrphaned     = "orphaned"
	PackKindMissing      = "missing"
	PackKindUnresolvable = "unresolvable"
)

// PackSkillFinding is skills.pack_skill_drift.PackSkillFinding.
type PackSkillFinding struct {
	Kind          string
	SkillName     string
	InstalledPath string
	SourceRef     string
	Detail        string
}

// Message is PackSkillFinding.message.
func (f PackSkillFinding) Message() string {
	switch f.Kind {
	case PackKindUnresolvable:
		return "the pack skills in force could not be resolved, so no pack skill was projected or checked for staleness or orphaning: " + f.Detail
	case PackKindMissing:
		return fmt.Sprintf("%s: pack skill '%s' is in force but not installed; run `spec-kitty doctor skills --fix` or `spec-kitty upgrade` to project it", f.InstalledPath, f.SkillName)
	case PackKindDrift:
		return fmt.Sprintf("%s: rendered pack skill '%s' was edited locally; edit its source '%s' and re-project instead", f.InstalledPath, f.SkillName, f.SourceRef)
	case PackKindOrphaned:
		return fmt.Sprintf("%s: pack skill '%s' (source '%s') is no longer provided by the current pack catalog "+
			"(namespace changed, or the skill was deactivated or removed); run `spec-kitty upgrade` to retire it or re-activate the skill", f.InstalledPath, f.SkillName, f.SourceRef)
	}
	return fmt.Sprintf("%s: pack skill '%s' is stale, its source '%s' changed since install; re-project (`spec-kitty upgrade`) to refresh it", f.InstalledPath, f.SkillName, f.SourceRef)
}

// JSON is PackSkillFinding.to_dict.
func (f PackSkillFinding) JSON() *jsonx.Object {
	return jsonx.NewObject("kind", f.Kind, "skill_name", f.SkillName, "installed_path", f.InstalledPath, "source_ref", f.SourceRef, "message", f.Message())
}

// installableAgentKeys is installer.installable_agent_keys.
func installableAgentKeys(root string) ([]string, error) {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range ac.Available {
		if c := agents.SkillClass(a); c != "" && c != agents.SkillClassWrapper {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FindPackSkillFindings is pack_skill_drift.find_pack_skill_findings.
func FindPackSkillFindings(root string) []PackSkillFinding {
	m, err := loadSkillsManifestStrict(root)
	if err != nil {
		return nil
	}
	var entries []agents.SkillEntry
	if m != nil {
		for _, e := range m.Entries {
			if o, _, _ := e.Provenance(); o == "pack" {
				entries = append(entries, e)
			}
		}
	}
	current := map[string]string{}
	refusal := ""
	resolved := true
	if rendered, err := PreparePackSkills(root); err != nil {
		var pe *PackSkillCatalogError
		if !errors.As(err, &pe) {
			return nil
		}
		resolved, refusal = false, err.Error()
	} else {
		for _, r := range rendered {
			current[r.RenderedName] = r.SourceHash
		}
	}
	var out []PackSkillFinding
	for _, e := range entries {
		_, ref, srcHash := e.Provenance()
		if resolved {
			if _, ok := current[e.SkillName]; !ok {
				out = append(out, PackSkillFinding{Kind: PackKindOrphaned, SkillName: e.SkillName, InstalledPath: e.InstalledPath, SourceRef: ref})
				continue
			}
		}
		st, err := observeConfined(root, e.InstalledPath)
		if err == nil && st.Kind == "absent" {
			out = append(out, PackSkillFinding{Kind: PackKindMissing, SkillName: e.SkillName, InstalledPath: e.InstalledPath, SourceRef: ref})
			continue
		}
		if err == nil && st.Kind == "file" && "sha256:"+st.SHA256 != e.ContentHash {
			out = append(out, PackSkillFinding{Kind: PackKindDrift, SkillName: e.SkillName, InstalledPath: e.InstalledPath, SourceRef: ref})
		}
		if fresh, ok := current[e.SkillName]; resolved && ok && srcHash != "" && fresh != srcHash {
			out = append(out, PackSkillFinding{Kind: PackKindStale, SkillName: e.SkillName, InstalledPath: e.InstalledPath, SourceRef: ref})
		}
	}
	if !resolved {
		return append(out, PackSkillFinding{Kind: PackKindUnresolvable, Detail: refusal})
	}
	if len(current) == 0 {
		return out
	}
	covered := map[string]bool{}
	for _, e := range entries {
		if _, ok := current[e.SkillName]; ok {
			covered[e.InstalledPath] = true
		}
	}
	keys, err := installableAgentKeys(root)
	if err != nil {
		return out
	}
	names := make([]string, 0, len(current))
	for n := range current {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, a := range keys {
		r := agents.ProjectSkillRoot(a)
		if r == "" {
			continue
		}
		if !isDir(filepath.Join(root, strings.Split(r, "/")[0])) {
			continue
		}
		for _, n := range names {
			p := strings.TrimSuffix(r, "/") + "/" + n + "/SKILL.md"
			if !covered[p] && !seen[p] {
				seen[p] = true
				out = append(out, PackSkillFinding{Kind: PackKindMissing, SkillName: n, InstalledPath: p})
			}
		}
	}
	return out
}

// ProjectPackSkills is installer.project_pack_skills: re-project the pack
// skills in force (project roots only); returns the changed paths.
func ProjectPackSkills(root string) ([]string, error) {
	p, err := ReprojectPackSkills(root)
	return p.Changed, err
}

// PackSkillProjection is installer.PackSkillProjection: project-relative
// skill files created, updated or retired, and (path, reason) pairs left
// alone (unowned same-name directories, locally modified managed files).
type PackSkillProjection struct {
	Changed   []string
	Preserved [][2]string
}

// ReprojectPackSkills is installer.project_pack_skills with its full
// outcome (charter activate/deactivate of a skill).
func ReprojectPackSkills(root string) (PackSkillProjection, error) {
	var proj PackSkillProjection
	agentKeys, err := installableAgentKeys(root)
	if err != nil {
		return proj, err
	}
	if len(agentKeys) == 0 {
		return proj, nil
	}
	catalog, err := ProjectSkillCatalog(root, true)
	if err != nil {
		return proj, err
	}
	m, err := loadSkillsManifestStrict(root)
	if err != nil {
		return proj, err
	}
	selected := map[string]bool{}
	if m != nil {
		for _, e := range m.Entries {
			if o, _, _ := e.Provenance(); o == "pack" && containsStr(agentKeys, e.AgentKey) {
				selected[e.InstalledPath] = true
			}
		}
	}
	for _, a := range agentKeys {
		r := agents.ProjectSkillRoot(a)
		if r == "" {
			continue
		}
		for _, s := range catalog {
			if s.Origin != "pack" {
				continue
			}
			for _, f := range s.Files {
				selected[strings.TrimSuffix(r, "/")+"/"+s.Name+"/"+f] = true
			}
		}
	}
	if len(selected) == 0 {
		return proj, nil
	}
	sel := make([]string, 0, len(selected))
	for p := range selected {
		sel = append(sel, p)
	}
	sort.Strings(sel)
	a := AssessProjectSkills(Root{"project", "project", absPath(root)}, catalog, agentKeys, SkillAssessOptions{SelectedPaths: sel, Now: nowISO()})
	if !a.Complete {
		var msgs []string
		for _, d := range a.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		if len(msgs) == 0 {
			return proj, errors.New("pack skill assessment is incomplete")
		}
		return proj, errors.New(strings.Join(msgs, "; "))
	}
	res := ApplyProjectSkills(a)
	if res.Outcome != "applied" && res.Outcome != "skipped" {
		var msgs []string
		for _, d := range res.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		return proj, errors.New(strings.Join(msgs, "; "))
	}
	ok := map[string]bool{}
	for _, id := range res.Succeeded {
		ok[id] = true
	}
	var changed []string
	for _, e := range a.Effects {
		if ok[e.ID()] && selected[e.Path] {
			changed = append(changed, e.Path)
		}
	}
	sort.Strings(changed)
	proj.Changed = changed
	for _, d := range a.Dispositions {
		if d.State == "preserve" || d.State == "consent_required" {
			proj.Preserved = append(proj.Preserved, [2]string{d.Path, d.Reason})
		}
	}
	return proj, nil
}
