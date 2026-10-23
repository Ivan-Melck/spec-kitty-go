package toolsurface

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const (
	cmdSkillRepairHint = "spec-kitty doctor tool-surfaces --kind command-skill --fix"
	cmdSkillDriftHint  = "spec-kitty doctor tool-surfaces --fix"
)

// resolveRejectingLoops resolves p like Path.resolve() (non-strict).
func resolveRejectingLoops(p string) (string, error) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	// Non-strict: resolve the longest existing prefix.
	dir, rest := p, ""
	for {
		parent := filepath.Dir(dir)
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest), nil
		}
		if parent == dir {
			return p, nil
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func confined(root, abs string) bool {
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		rr = root
	}
	r, err := resolveRejectingLoops(abs)
	if err != nil {
		return false
	}
	return r == rr || strings.HasPrefix(r, rr+string(filepath.Separator))
}

func (c *collectCtx) commandVerify(root string) *CommandVerify {
	if v, ok := c.verify[root]; ok {
		return v
	}
	v, err := VerifyCommandSkills(root)
	if err != nil {
		v = nil
	}
	c.verify[root] = v
	return v
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type commandSkillsProvider struct{}

func (commandSkillsProvider) key() string              { return "command_skills" }
func (commandSkillsProvider) handles(kind string) bool { return kind == KindCommandSkill }
func (commandSkillsProvider) definitions() []Definition {
	return []Definition{{Kind: KindCommandSkill, SourceKind: "generated", Scope: "project", PathPattern: ".agents/skills/spec-kitty-{command}/SKILL.md",
		Required: "repairable_required", Activation: "skills_invocable", Provider: "command_skills", RepairHint: cmdSkillRepairHint}}
}

func (p commandSkillsProvider) expand(def Definition, tool, root string) []Instance {
	if !agents.IsCommandSkillAgent(tool) {
		return nil
	}
	manifest, _ := LoadCommandManifest(root)
	var out []Instance
	expected := map[string]bool{}
	for _, c := range agents.CommandSkillCommands {
		rel := agents.CommandSkillPath(c)
		expected[rel] = true
		inst := Instance{Def: def, Path: filepath.Join(root, filepath.FromSlash(rel)), Owner: tool, SurfaceID: tool + ".command_skill." + agents.CommandSkillName(c)}
		for _, e := range manifest {
			if e.Path == rel {
				inst.FileHash, inst.HasHash = e.ContentHash, true
			}
		}
		out = append(out, inst)
	}
	v, err := VerifyCommandSkills(root)
	if err != nil {
		return out
	}
	extra := map[string]bool{}
	for _, l := range [][]string{v.Orphans, v.Stale, v.Unsafe, v.Legacy} {
		for _, r := range l {
			if !expected[r] {
				extra[r] = true
			}
		}
	}
	var rels []string
	for r := range extra {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		safe := strings.ReplaceAll(strings.TrimPrefix(rel, ".agents/skills/"), "/", ".")
		out = append(out, Instance{Def: def, Path: filepath.Join(root, filepath.FromSlash(rel)), Owner: tool, SurfaceID: tool + ".command_skill." + safe})
	}
	return out
}

func projectRootAndRel(p string) (string, string, bool) {
	parts := strings.Split(p, string(filepath.Separator))
	for i, part := range parts {
		if part == ".agents" {
			root := strings.Join(parts[:i], string(filepath.Separator))
			if root == "" {
				root = string(filepath.Separator)
			}
			return root, strings.Join(parts[i:], "/"), true
		}
	}
	return "", "", false
}

func (p commandSkillsProvider) probe(c *collectCtx, inst Instance) Status {
	path := inst.Path
	root, rel, ok := projectRootAndRel(path)
	if ok {
		if v := c.commandVerify(root); v != nil {
			switch {
			case containsStr(v.Unsafe, rel):
				return Status{inst, StateUnsafe, []Finding{instFinding("unsafe-managed-path", "error", "Command skill path is unsafe or escapes the project root: "+path, inst, true, "")}}
			case containsStr(v.Collisions, rel):
				message := agents.CommandSkillWarnings(agents.CommandSkillReport{Collisions: []string{path}})[0]
				return Status{inst, StatePresent, []Finding{instFinding("unmanaged-spec-kitty-surface", "warning", message, inst, true, "")}}
			case containsStr(v.Legacy, rel), containsStr(v.Legacy, rel+"/"):
				if containsStr(v.LegacyOwned, rel) || containsStr(v.LegacyRecorded, rel) {
					return Status{inst, StateStale, []Finding{instFinding("stale-generated-surface", "warning", "Legacy dotted command skill (renamed to spec-kitty-<command>): "+path, inst, true, "")}}
				}
				leftover := rel
				if containsStr(v.Legacy, rel+"/") {
					leftover += "/"
				}
				message := agents.CommandSkillWarnings(agents.CommandSkillReport{LegacyKept: []string{leftover}})[0]
				return Status{inst, StatePresent, []Finding{instFinding("stale-generated-surface", "warning", message, inst, true, "")}}
			case containsStr(v.Stale, rel):
				return Status{inst, StateStale, []Finding{instFinding("stale-generated-surface", "warning", "Command skill is no longer canonical: "+path, inst, true, "")}}
			case containsStr(v.Orphans, rel):
				return Status{inst, StateOrphaned, []Finding{instFinding("unmanaged-spec-kitty-surface", "warning", "Spec Kitty-looking command skill is not owned by the manifest: "+path, inst, true, "")}}
			case containsStr(v.Gaps, rel):
				return p.missing(inst)
			}
		}
		manifest, _ := LoadCommandManifest(root)
		for _, e := range manifest {
			if e.Path == rel && !containsStr(e.Agents, inst.Owner) {
				return Status{inst, StateMissing, []Finding{instFinding("configured-tool-surface-uninstalled", "error", "Configured tool is not recorded as an owner of command skill: "+path, inst, true, cmdSkillRepairHint)}}
			}
		}
	}
	b, err := os.ReadFile(path)
	if _, serr := os.Stat(path); serr != nil {
		return p.missing(inst)
	}
	if inst.HasHash && err == nil {
		onDisk := Digest(b)
		if onDisk != inst.FileHash && !isCanonicalBytes(root, rel, onDisk, ok) {
			return Status{inst, StateDrifted, []Finding{instFinding("managed-file-drift", "warning",
				"Command skill drifted from manifest hash: "+path+"; run '"+cmdSkillDriftHint+"' to restore it", inst, true, cmdSkillDriftHint)}}
		}
	}
	return Status{inst, StatePresent, nil}
}

func (commandSkillsProvider) missing(inst Instance) Status {
	return Status{inst, StateMissing, []Finding{instFinding("generated-surface-missing", "error", "Command skill for "+inst.Owner+" is missing: "+inst.Path, inst, true, cmdSkillRepairHint)}}
}

// isCanonicalBytes is command_skills._is_canonical_bytes.
func isCanonicalBytes(root, rel, onDisk string, ok bool) bool {
	if !ok || !isCanonicalRelPath(rel) {
		return false
	}
	command := agents.ParseCommandSkillPath(rel, false)
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		return false
	}
	n := 0
	for _, a := range ac.Available {
		if agents.IsCommandSkillAgent(a) {
			n++
		}
	}
	if n == 0 {
		return false
	}
	b, err := agents.RenderCommandSkill(root, command)
	if err != nil {
		return false
	}
	return Digest(b) == onDisk
}

func (p commandSkillsProvider) assess(c *collectCtx, in AssessInputs, statuses []Status, sels []Selection) *Assessment {
	var keys []string
	for _, s := range sels {
		if p.handles(s.Def.Kind) && s.Def.enabled() {
			keys = append(keys, s.Tool)
		}
	}
	keys = uniqueSorted(keys)
	if len(keys) == 0 {
		return &Assessment{OwnerKey: "command_skills", Root: in.Root, Complete: true,
			Dispositions: []Disposition{NewDisposition("command_skills", in.Root.ID, "", false, "not_applicable", "No enabled command-skill selection")}}
	}
	prov, _ := in.Projected.(*Provisioning)
	a := PrepareCommands(in.Root, keys, CommandOptions{Prune: true, Provisioning: prov, Now: nowISO(), Overwrite: in.Overwrite})
	for i, e := range a.Effects {
		var ids []string
		for _, s := range statuses {
			if s.Inst.Path == e.Destination() && containsStr(keys, s.Inst.Owner) && s.Inst.Def.enabled() {
				ids = append(ids, s.Inst.ID())
			}
		}
		e.SurfaceIDs = ids
		e.normalize()
		a.Effects[i] = e
	}
	return a
}

func (p commandSkillsProvider) repair(c *collectCtx, root string, statuses []Status) RepairResult {
	var affected []string
	for _, s := range statuses {
		switch s.State {
		case StateMissing, StateDrifted, StateStale, StateOrphaned, StateUnsafe:
			affected = append(affected, s.Inst.Owner)
		}
	}
	affected = uniqueSorted(affected)
	if len(affected) == 0 {
		return RepairResult{}
	}
	var sels []Selection
	for _, a := range affected {
		for _, s := range statuses {
			if s.Inst.Owner == a {
				sels = append(sels, Selection{a, s.Inst.Def})
				break
			}
		}
	}
	var overwrite []string
	for _, s := range statuses {
		if s.State == StateDrifted {
			overwrite = append(overwrite, relPosix(root, s.Inst.Path))
		}
	}
	sort.Strings(overwrite)
	in := AssessInputs{Root: Root{"project", "project", absPath(root)}, Overwrite: overwrite, Automatic: true}
	a := p.assess(c, in, statuses, sels)
	if !a.Complete {
		var msgs []string
		for _, d := range a.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		return RepairResult{Failed: msgs}
	}
	unresolved := map[string]bool{}
	for _, d := range a.Dispositions {
		if d.State == "preserve" || d.State == "consent_required" {
			unresolved[d.Path] = true
		}
	}
	skipPaths := map[string]bool{}
	deleted := map[string]bool{}
	for _, e := range a.Effects {
		if e.After.Kind == "absent" {
			deleted[e.Path] = true
		}
	}
	for _, d := range a.Dispositions {
		if d.State == "collision" || d.State == "legacy_kept" {
			skipPaths[d.Path] = true
		}
	}
	for _, s := range statuses {
		rel := relPosix(root, s.Inst.Path)
		if s.State == StateStale && strings.Contains(rel, "/spec-kitty.") && !deleted[rel] {
			skipPaths[rel] = true
		}
	}
	var eligible, skipped []string
	for _, s := range statuses {
		rel := relPosix(root, s.Inst.Path)
		if containsStr(affected, s.Inst.Owner) && skipPaths[rel] {
			skipped = append(skipped, s.Inst.ID())
		}
		if containsStr(affected, s.Inst.Owner) && !unresolved[rel] && !skipPaths[rel] {
			eligible = append(eligible, s.Inst.ID())
		}
	}
	var failed []string
	for d := range unresolved {
		if d != "" {
			failed = append(failed, d)
		}
	}
	sort.Strings(failed)
	res := ApplyCommands(a)
	if res.Outcome != "applied" {
		ok := map[string]bool{}
		for _, id := range res.Succeeded {
			ok[id] = true
		}
		succeededPaths := map[string]bool{}
		for _, e := range a.Effects {
			if ok[e.ID()] {
				succeededPaths[e.Destination()] = true
			}
		}
		var repaired []string
		for _, s := range statuses {
			if succeededPaths[s.Inst.Path] {
				repaired = append(repaired, s.Inst.ID())
			}
		}
		for _, d := range res.Diagnostics {
			failed = append(failed, d.Message)
		}
		return RepairResult{Repaired: repaired, Skipped: skipped, Failed: failed}
	}
	return RepairResult{Repaired: eligible, Skipped: skipped, Failed: failed}
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
