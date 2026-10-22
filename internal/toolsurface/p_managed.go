package toolsurface

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/bootstrap"
)

const charterSkillRepairHint = "spec-kitty doctor tool-surfaces --kind charter-skill --fix"

// SkillInstallation is installer.SkillInstallationAssessment.
type SkillInstallation struct {
	Global    *Assessment
	Project   *Assessment
	AgentKeys []string
}

// AssessSkillInstallation is installer.assess_skill_installation.
func AssessSkillInstallation(root Root, catalog []CatalogSkill, agentKeys []string, runtime, commands bool, commandKeys []string, opts SkillAssessOptions) *SkillInstallation {
	keys := uniqueSorted(agentKeys)
	project := AssessProjectSkills(root, catalog, keys, opts)
	global := AssessGlobalAssets(runtime, commands, commandKeys, keys)
	return &SkillInstallation{Global: global, Project: project, AgentKeys: keys}
}

type managedSkillsProvider struct{}

func (managedSkillsProvider) key() string              { return "managed_skills" }
func (managedSkillsProvider) handles(kind string) bool { return kind == KindCharterSkill }
func (managedSkillsProvider) definitions() []Definition {
	return []Definition{{Kind: KindCharterSkill, SourceKind: "generated", Scope: "project", PathPattern: ".kittify/skills-manifest.json:{installed_path}",
		Required: "repairable_required", Activation: "skills_invocable", Provider: "managed_skills", RepairHint: charterSkillRepairHint}}
}

func managedSurfaceID(tool string, e agents.SkillEntry) string {
	safe := strings.ReplaceAll(strings.ReplaceAll(e.SourceFile, "/", "."), `\`, ".")
	return tool + ".charter_skill." + e.SkillName + "." + safe
}

func (p managedSkillsProvider) expand(def Definition, tool, root string) []Instance {
	type ent struct {
		e    agents.SkillEntry
		hash string
	}
	var order []string
	byPath := map[string]ent{}
	if cls := agents.SkillClass(tool); cls != "" && cls != agents.SkillClassWrapper && agents.ProjectSkillRoot(tool) != "" {
		catalog, err := ProjectSkillCatalog(root, false)
		if err == nil && len(catalog) > 0 {
			skillRoot := strings.TrimSuffix(agents.ProjectSkillRoot(tool), "/")
			for _, s := range catalog {
				for _, rel := range s.Files {
					content, err := s.Read(rel)
					if err != nil {
						continue
					}
					if rel == "SKILL.md" {
						content = []byte(agents.EnsureSkillFrontmatter(string(content), s.Name))
					}
					installed := skillRoot + "/" + s.Name + "/" + rel
					e := agents.SkillEntry{SkillName: s.Name, SourceFile: rel, InstalledPath: installed, AgentKey: tool}
					if _, ok := byPath[installed]; !ok {
						order = append(order, installed)
					}
					byPath[installed] = ent{e, "sha256:" + Digest(content)}
				}
			}
		}
	}
	if m := agents.LoadSkillsManifest(root); m != nil {
		for _, e := range m.Entries {
			if e.AgentKey != tool {
				continue
			}
			if prev, ok := byPath[e.InstalledPath]; ok {
				byPath[e.InstalledPath] = ent{e, prev.hash}
				continue
			}
			order = append(order, e.InstalledPath)
			byPath[e.InstalledPath] = ent{e, e.ContentHash}
		}
	}
	var out []Instance
	for _, rel := range order {
		x := byPath[rel]
		out = append(out, Instance{Def: def, Path: filepath.Join(root, filepath.FromSlash(rel)), Owner: tool, SurfaceID: managedSurfaceID(tool, x.e),
			FileHash: x.hash, HasHash: x.hash != ""})
	}
	return out
}

func (p managedSkillsProvider) probe(c *collectCtx, inst Instance) Status {
	drift := func() Status {
		return Status{inst, StateDrifted, []Finding{instFinding("managed-file-drift", "warning", "Managed charter skill drifted from manifest hash: "+inst.Path, inst, true, charterSkillRepairHint)}}
	}
	st, err := os.Lstat(inst.Path)
	if err == nil && st.Mode()&os.ModeSymlink != 0 {
		return drift()
	}
	if _, err := os.Stat(inst.Path); err != nil {
		return Status{inst, StateMissing, []Finding{instFinding("generated-surface-missing", "error",
			fmt.Sprintf("Managed charter skill for %s is missing: %s", inst.Owner, inst.Path), inst, true, charterSkillRepairHint)}}
	}
	if !st.Mode().IsRegular() {
		return drift()
	}
	if inst.HasHash {
		b, err := os.ReadFile(inst.Path)
		if err != nil || "sha256:"+Digest(b) != inst.FileHash {
			return drift()
		}
	}
	return Status{inst, StatePresent, nil}
}

func (p managedSkillsProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	var keys []string
	for _, s := range sels {
		if p.handles(s.Def.Kind) {
			keys = append(keys, s.Tool)
		}
	}
	keys = uniqueSorted(keys)
	if inst, ok := in.Projected.(*SkillInstallation); ok {
		if strings.Join(inst.AgentKeys, "\x00") == strings.Join(keys, "\x00") && inst.Project.Root == in.Root {
			if inst.Global.Complete {
				return inst.Project
			}
			cp := *inst.Project
			cp.Complete = false
			cp.Diagnostics = append(append([]Diagnostic(nil), cp.Diagnostics...), inst.Global.Diagnostics...)
			return &cp
		}
		return Incomplete("managed_skills", in.Root, "skill_context_mismatch", "Coordinated skill context differs from selected project inputs")
	}
	catalog, err := ProjectSkillCatalog(in.Root.Path, false)
	if err != nil {
		return Incomplete("managed_skills", in.Root, "pack_skill_catalog_failed", err.Error())
	}
	a := AssessProjectSkills(in.Root, catalog, keys, SkillAssessOptions{Now: nowISO()})
	cp := *a
	cp.Complete = false
	cp.Diagnostics = append(append([]Diagnostic(nil), cp.Diagnostics...), Diag("managed_skills_global_context_required", "managed_skills", "error",
		"Prepare and dispatch the separate coordinated global owner using assess_skill_installation"))
	return &cp
}

func (p managedSkillsProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	actionable := statusesOf(statuses, StateMissing, StateDrifted)
	if len(actionable) == 0 {
		return RepairResult{}
	}
	ids := idsOf(actionable)
	var tools []string
	for _, s := range actionable {
		tools = append(tools, s.Inst.Owner)
	}
	tools = uniqueSorted(tools)
	catalog, err := ProjectSkillCatalog(root, true)
	if err != nil || len(catalog) == 0 {
		return RepairResult{Failed: append([]string{"managed_skills: no canonical skill registry"}, ids...)}
	}
	r := Root{"project", "project", absPath(root)}
	inst := AssessSkillInstallation(r, catalog, tools, false, false, nil, SkillAssessOptions{Now: nowISO()})
	if !inst.Global.Complete || !inst.Project.Complete {
		return RepairResult{Failed: ids}
	}
	var errs []string
	if err := bootstrap.EnsureSkillsSelection(tools); err != nil {
		errs = append(errs, "managed_skills: "+err.Error())
	}
	succeeded := map[string]bool{}
	if len(errs) == 0 {
		res := ApplyProjectSkills(inst.Project)
		for _, id := range res.Succeeded {
			succeeded[id] = true
		}
		for _, d := range res.Diagnostics {
			errs = append(errs, "managed_skills: "+d.Message)
		}
	}
	var repaired, skipped, failed []string
	for _, s := range actionable {
		var effects []Effect
		for _, e := range inst.Project.Effects {
			if e.Destination() == absPath(s.Inst.Path) {
				effects = append(effects, e)
			}
		}
		if len(effects) == 0 {
			skipped = append(skipped, s.Inst.ID())
			continue
		}
		all := true
		for _, e := range effects {
			all = all && succeeded[e.ID()]
		}
		if all {
			repaired = append(repaired, s.Inst.ID())
		} else {
			failed = append(failed, s.Inst.ID())
		}
	}
	return RepairResult{Repaired: repaired, Skipped: skipped, Failed: append(failed, errs...)}
}

// ApplyProjectSkills is installer.apply_project_skills for a fresh batch.
func ApplyProjectSkills(a *Assessment) ApplyResult {
	prep, ok := a.Prepared.(*PreparedProjectSkills)
	if !ok || !a.Complete {
		return ApplyResult{Owner: "managed_skills", Skipped: a.EffectIDs(), Outcome: "precondition_changed",
			Diagnostics: []Diagnostic{Diag("precondition_changed", "managed_skills", "error", "Project skill batch was not successfully rechecked")}}
	}
	var succeeded []string
	for i, w := range prep.Writes {
		if err := applyProjectSkillWrite(w); err != nil {
			var rest []string
			for _, x := range prep.Writes[i+1:] {
				rest = append(rest, x.Effect.ID())
			}
			out := "failed"
			if len(succeeded) > 0 {
				out = "partial"
			}
			return ApplyResult{Owner: "managed_skills", Succeeded: succeeded, Failed: []string{w.Effect.ID()}, Skipped: rest, Outcome: out,
				Diagnostics: []Diagnostic{Diag("skill_write_failed", "managed_skills", "error", err.Error())}}
		}
		succeeded = append(succeeded, w.Effect.ID())
	}
	return ApplyResult{Owner: "managed_skills", Succeeded: succeeded, Outcome: "applied"}
}

func applyProjectSkillWrite(w ProjectSkillWrite) error {
	e := w.Effect
	dest := e.Destination()
	cur, err := observeNode(dest)
	if err != nil {
		return err
	}
	if cur.Kind != e.Before.Kind || cur.SHA256 != e.Before.SHA256 {
		return fmt.Errorf("Managed-skill destination changed during apply: %s", dest)
	}
	mode := os.FileMode(0o644)
	if e.After.Mode >= 0 {
		mode = os.FileMode(e.After.Mode)
	}
	switch {
	case e.After.Kind == "absent":
		return os.Remove(dest)
	case e.After.Kind == "directory":
		if err := os.Mkdir(dest, mode); err != nil {
			return err
		}
		return os.Chmod(dest, mode)
	case e.Action == "chmod":
		return os.Chmod(dest, mode)
	case e.After.Kind == "symlink":
		return os.Symlink(e.After.Target, dest)
	}
	if cur.Kind == "absent" {
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		if _, err := f.Write(w.Content); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Chmod(dest, mode)
	}
	if cur.Kind == "file" && cur.Mode&0o200 == 0 {
		os.Chmod(dest, os.FileMode(cur.Mode)|0o200)
	}
	return writeFileAtomic(dest, w.Content, mode)
}
