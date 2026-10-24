package toolsurface

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// CatalogSkill is skills.registry.CanonicalSkill: a built-in doctrine skill
// or a rendered charter-pack skill.
type CatalogSkill struct {
	Name  string
	Files []string // SKILL.md first, then references/, scripts/, assets/
	// Read returns the raw source bytes of one file.
	Read func(rel string) ([]byte, error)
	// SourceMode is the source file mode (package files are 0644).
	SourceMode int
	Origin     string // builtin | pack
	SourceRef  string
	SourceHash string
}

// BuiltinCatalog returns the shipped doctrine skills (sorted by name).
func BuiltinCatalog() []CatalogSkill {
	var out []CatalogSkill
	for _, s := range agents.Skills() {
		s := s
		out = append(out, CatalogSkill{Name: s.Name, Files: s.Files, Read: func(rel string) ([]byte, error) { return agents.ReadSkillFile(s, rel) },
			SourceMode: 0o644, Origin: "builtin"})
	}
	return out
}

// ProjectSkillWrite is PreparedProjectSkillWrite.
type ProjectSkillWrite struct {
	Effect  Effect
	Content []byte
	Order   int
}

// PreparedProjectSkills is the retained managed-skills payload.
type PreparedProjectSkills struct {
	Writes []ProjectSkillWrite
}

type skillPlan struct {
	root         Root
	writes       []ProjectSkillWrite
	dispositions []Disposition
	provParents  map[string]bool // directories the provisioning YAML writer creates
}

func (p *skillPlan) disposition(rel, state, reason string) {
	p.dispositions = append(p.dispositions, NewDisposition("managed_skills", p.root.ID, rel, true, state, reason))
}

func skillSurfaceID(e agents.SkillEntry) string {
	return e.AgentKey + ".charter_skill." + e.SkillName + "." + strings.ReplaceAll(e.SourceFile, "/", ".")
}

func (p *skillPlan) write(rel string, before, after FileState, content []byte, owners []agents.SkillEntry, order int, reason, proofKind string, consumers []agents.SkillEntry) {
	if before.Kind == "absent" && after.Kind == "absent" {
		return
	}
	act := Action(before, after)
	if act == "" {
		return
	}
	var proofs []Proof
	for _, e := range owners {
		if proofKind == "manifest" {
			proofs = append(proofs, Proof{"manifest", ".kittify/skills-manifest.json:" + e.AgentKey + ":" + e.InstalledPath})
		} else {
			proofs = append(proofs, Proof{proofKind, "canonical-skill:" + e.SkillName + "/" + e.SourceFile + ":" + e.ContentHash})
		}
	}
	if len(proofs) == 0 {
		proofs = []Proof{{"managed_path", "managed-skills:" + rel}}
	}
	affected := owners
	if consumers != nil {
		affected = consumers
	}
	var logical, ids []string
	for _, e := range affected {
		logical = append(logical, e.AgentKey)
		ids = append(ids, skillSurfaceID(e))
	}
	if len(logical) == 0 {
		logical = []string{"managed_skills"}
	}
	e := NewEffect("managed_skills", "surface_repair", p.root, rel, act, before, after, reason, proofs, logical, ids)
	p.writes = append(p.writes, ProjectSkillWrite{Effect: e, Content: content, Order: order})
}

func (p *skillPlan) observe(rel string) (FileState, error) { return observeConfined(p.root.Path, rel) }

func (p *skillPlan) parents() error {
	existing := append([]ProjectSkillWrite(nil), p.writes...)
	dirs := map[string]Effect{}
	var order []string
	for _, w := range existing {
		if w.Effect.After.Kind == "absent" {
			continue
		}
		for _, parent := range parentsOf(w.Effect.Path) {
			before, err := p.observe(parent)
			if err != nil {
				return err
			}
			if before.Kind != "absent" || p.provParents[parent] {
				continue
			}
			mode := 0o755
			if strings.HasPrefix(parent, ".kittify/.migration-backup") {
				mode = 0o700
			}
			e := w.Effect
			e.Path, e.Action, e.Before, e.After, e.Reason = parent, "create", before, DirOf(mode), "Create managed-skill parent"
			e.normalize()
			if prev, ok := dirs[parent]; ok {
				merged, err := Coalesce([]Effect{prev, e})
				if err != nil {
					return err
				}
				e = merged[0]
			} else {
				order = append(order, parent)
			}
			dirs[parent] = e
		}
	}
	for _, d := range order {
		p.writes = append(p.writes, ProjectSkillWrite{Effect: dirs[d], Order: 0})
	}
	return nil
}

type expectedSkill struct {
	content []byte
	state   FileState
	entries []agents.SkillEntry
}

func expectedProjectEntries(skills []CatalogSkill, agentKeys []string, now string) (map[string]*expectedSkill, error) {
	expected := map[string]*expectedSkill{}
	for _, agent := range agentKeys {
		if _, ok := agents.SkillConfigs[agent]; !ok {
			return nil, fmt.Errorf("Unknown agent key: '%s'", agent)
		}
		root := agents.ProjectSkillRoot(agent)
		if root == "" {
			continue
		}
		for _, s := range skills {
			for _, rel := range s.Files {
				p := strings.TrimSuffix(root, "/") + "/" + s.Name + "/" + rel
				content, err := s.Read(rel)
				if err != nil {
					return nil, err
				}
				if rel == "SKILL.md" {
					content = []byte(agents.EnsureSkillFrontmatter(string(content), s.Name))
				}
				state := FileOf(Digest(content), s.SourceMode&^0o222)
				entry := agents.SkillEntry{SkillName: s.Name, SourceFile: rel, InstalledPath: p, InstallationClass: agents.SkillClass(agent),
					AgentKey: agent, ContentHash: "sha256:" + state.SHA256, InstalledAt: now, DeliveryMode: "copy"}.WithProvenance(s.Origin, s.SourceRef, s.SourceHash)
				if prev, ok := expected[p]; ok {
					if string(prev.content) != string(content) || prev.state != state {
						return nil, fmt.Errorf("Conflicting shared skill sources: %s", p)
					}
					prev.entries = append(prev.entries, entry)
				} else {
					expected[p] = &expectedSkill{content, state, []agents.SkillEntry{entry}}
				}
			}
		}
	}
	return expected, nil
}

func validSkillEntry(e agents.SkillEntry) bool {
	root := agents.ProjectSkillRoot(e.AgentKey)
	if root == "" || e.SkillName == "" || strings.ContainsAny(e.SkillName, "/") || e.SkillName == ".." || filepath.IsAbs(e.SkillName) {
		return false
	}
	if filepath.IsAbs(e.SourceFile) || e.SourceFile == "" {
		return false
	}
	for _, part := range strings.Split(e.SourceFile, "/") {
		if part == ".." {
			return false
		}
	}
	return path.Join(root, e.SkillName, e.SourceFile) == e.InstalledPath
}

// SkillAssessOptions narrows AssessProjectSkills.
type SkillAssessOptions struct {
	SelectedPaths   []string // nil = every expected path
	NoRetire        bool
	NoPersist       bool
	Provisioning    *Provisioning
	Now             string
	ManifestDecoded func(*agents.SkillsManifest)
}

// AssessProjectSkills is skills.installer.assess_project_skills: the
// complete project-side managed charter-skill batch for agentKeys.
func AssessProjectSkills(root Root, skills []CatalogSkill, agentKeys []string, opts SkillAssessOptions) *Assessment {
	plan := &skillPlan{root: root, provParents: map[string]bool{}}
	if opts.Provisioning != nil {
		for _, d := range opts.Provisioning.AbsentParents {
			plan.provParents[d] = true
		}
	}
	a, err := prepareProjectSkills(plan, skills, uniqueSorted(agentKeys), opts)
	if err != nil {
		return Incomplete("managed_skills", root, "skill_assessment_failed", err.Error())
	}
	return a
}

func prepareProjectSkills(plan *skillPlan, skills []CatalogSkill, agentKeys []string, opts SkillAssessOptions) (*Assessment, error) {
	project := plan.root.Path
	cfg, err := plan.observe(".kittify/config.yaml")
	if err != nil {
		return nil, err
	}
	if cfg.Kind == "file" {
		if _, err := kittify.LoadAgentConfig(project); err != nil {
			return nil, err
		}
	} else if cfg.Kind != "absent" {
		return nil, errors.New("Project agent config is not a regular file")
	}
	if _, err := plan.observe(".kittify/skills-manifest.json"); err != nil {
		return nil, err
	}
	previous, err := loadSkillsManifestStrict(project)
	if err != nil {
		return nil, err
	}
	manifest := &agents.SkillsManifest{}
	if previous != nil {
		manifest = &agents.SkillsManifest{CreatedAt: previous.CreatedAt, UpdatedAt: previous.UpdatedAt, SpecKittyVersion: previous.SpecKittyVersion,
			Entries: append([]agents.SkillEntry(nil), previous.Entries...)}
	}
	for _, e := range manifest.Entries {
		if !validSkillEntry(e) {
			return nil, errors.New("Skills manifest contains an invalid ownership path")
		}
	}
	now := opts.Now
	expectedAll, err := expectedProjectEntries(skills, agentKeys, now)
	if err != nil {
		return nil, err
	}
	expected, err := dropUnownedPackDirectories(plan, expectedAll, manifest)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for p := range expected {
		paths[p] = true
	}
	if opts.SelectedPaths != nil {
		sel := map[string]bool{}
		for _, p := range opts.SelectedPaths {
			sel[p] = true
		}
		for p := range paths {
			if !sel[p] {
				delete(paths, p)
			}
		}
	}
	agentSet := map[string]bool{}
	for _, a := range agentKeys {
		agentSet[a] = true
	}
	if !opts.NoRetire {
		for _, e := range manifest.Entries {
			if _, ok := expected[e.InstalledPath]; !ok && agentSet[e.AgentKey] {
				if origin, _, _ := e.Provenance(); len(skills) > 0 || origin == "pack" {
					paths[e.InstalledPath] = true
				}
			}
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	type backup struct {
		rel               string
		before, after     FileState
		content           []byte
		owners, consumers []agents.SkillEntry
	}
	var backups []backup
	var retired []agents.SkillEntry
	for _, rel := range sorted {
		before, err := plan.observe(rel)
		if err != nil {
			return nil, err
		}
		var owners, selectedOwners []agents.SkillEntry
		for _, e := range manifest.Entries {
			if e.InstalledPath == rel {
				owners = append(owners, e)
				if agentSet[e.AgentKey] {
					selectedOwners = append(selectedOwners, e)
				}
			}
		}
		wanted := expected[rel]
		if wanted == nil {
			foreign := false
			for _, e := range owners {
				foreign = foreign || !agentSet[e.AgentKey]
			}
			if foreign {
				manifest.Entries = filterEntries(manifest.Entries, func(e agents.SkillEntry) bool { return !containsEntry(selectedOwners, e) })
				plan.disposition(rel, "preserve", "Unselected logical owners retain this shared file")
				continue
			}
		}
		var content []byte
		after := Absent()
		var newOwners []agents.SkillEntry
		if wanted != nil {
			content, after, newOwners = wanted.content, wanted.state, wanted.entries
		}
		managed := len(owners) > 0
		unchangedOwned := false
		if managed {
			switch before.Kind {
			case "file":
				unchangedOwned = true
				for _, e := range owners {
					unchangedOwned = unchangedOwned && e.DeliveryMode == "copy" && e.ContentHash == "sha256:"+before.SHA256
				}
			case "symlink":
				unchangedOwned = true
				for _, e := range owners {
					unchangedOwned = unchangedOwned && managedLink(e, filepath.Join(project, filepath.FromSlash(rel)), before)
				}
			}
		}
		canonical := wanted != nil && before.Kind == "file" && before.SHA256 == after.SHA256
		if preserveProjectPath(plan, rel, before, managed, canonical, unchangedOwned) {
			continue
		}
		effectOwners := owners
		if len(effectOwners) == 0 {
			effectOwners = newOwners
		}
		consumers := append(append([]agents.SkillEntry(nil), owners...), newOwners...)
		if (before.Kind == "file" || before.Kind == "symlink") && !canonical && (after.Kind != "absent" || !unchangedOwned) {
			var old []byte
			if before.Kind == "file" {
				old, _ = os.ReadFile(filepath.Join(project, filepath.FromSlash(rel)))
			}
			backups = append(backups, backup{rel, before, after, old, effectOwners, consumers})
		}
		proofKind := "canonical_content"
		if len(owners) > 0 {
			proofKind = "manifest"
		}
		plan.write(rel, before, after, content, effectOwners, 2, "Reconcile selected managed skill", proofKind, consumers)
		if wanted == nil {
			manifest.Entries = filterEntries(manifest.Entries, func(e agents.SkillEntry) bool { return e.InstalledPath != rel })
			retired = append(retired, selectedOwners...)
		} else {
			for _, e := range newOwners {
				manifest.AddEntry(e)
			}
			for _, e := range owners {
				if !agentSet[e.AgentKey] {
					e.ContentHash = "sha256:" + after.SHA256
					e.DeliveryMode = "copy"
					manifest.AddEntry(e)
				}
			}
		}
		written := false
		for _, w := range plan.writes {
			written = written || w.Effect.Path == rel
		}
		if !written {
			plan.disposition(rel, "unchanged", "Managed content is already current")
		}
	}
	if len(backups) > 0 {
		dir, err := allocateSkillBackup(project, func() [][3]any {
			var recs [][3]any
			for _, b := range backups {
				recs = append(recs, [3]any{b.rel, b.before, b.after})
			}
			return recs
		}())
		if err != nil {
			return nil, err
		}
		for _, b := range backups {
			rel := dir + "/" + b.rel
			plan.write(rel, Absent(), b.before, b.content, b.owners, 1, "Retain managed skill before replacement", "manifest", b.consumers)
		}
	}
	if err := plan.prune(retired); err != nil {
		return nil, err
	}
	pm, err := prepareSkillsManifest(manifest, project, now)
	if err != nil {
		return nil, err
	}
	if pm.changed && !opts.NoPersist && (len(manifest.Entries) > 0 || previous != nil) {
		owners := manifest.Entries
		if len(owners) == 0 && previous != nil {
			owners = previous.Entries
		}
		plan.write(".kittify/skills-manifest.json", pm.before, FileOf(Digest(pm.content), pm.mode), pm.content, owners, 4, "Persist prepared skills manifest", "manifest", nil)
	}
	if err := plan.parents(); err != nil {
		return nil, err
	}
	var effects []Effect
	for _, w := range plan.writes {
		effects = append(effects, w.Effect)
	}
	co, err := Coalesce(effects)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Effect{}
	for _, e := range co {
		byPath[e.Path] = e
	}
	writes := make([]ProjectSkillWrite, len(plan.writes))
	for i, w := range plan.writes {
		w.Effect = byPath[w.Effect.Path]
		writes[i] = w
	}
	sort.SliceStable(writes, func(i, j int) bool {
		wi, wj := writes[i], writes[j]
		if wi.Order != wj.Order {
			return wi.Order < wj.Order
		}
		di, dj := len(strings.Split(wi.Effect.Path, "/")), len(strings.Split(wj.Effect.Path, "/"))
		if wi.Order == 3 {
			di, dj = -di, -dj
		}
		if di != dj {
			return di < dj
		}
		return wi.Effect.Path < wj.Effect.Path
	})
	if opts.ManifestDecoded != nil {
		if m, err := parseSkillsManifest(string(pm.content)); err == nil {
			opts.ManifestDecoded(m)
		}
	}
	return &Assessment{OwnerKey: "managed_skills", Root: plan.root, Effects: co, Dispositions: plan.dispositions, Complete: true,
		Prepared: &PreparedProjectSkills{Writes: writes}}, nil
}

func containsEntry(list []agents.SkillEntry, e agents.SkillEntry) bool {
	for _, x := range list {
		if agents.SameEntry(x, e) {
			return true
		}
	}
	return false
}

func filterEntries(in []agents.SkillEntry, keep func(agents.SkillEntry) bool) []agents.SkillEntry {
	var out []agents.SkillEntry
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func managedLink(e agents.SkillEntry, dest string, before FileState) bool {
	root := agents.GlobalSkillRoot(e.AgentKey)
	if before.Kind != "symlink" || e.DeliveryMode != "symlink" || root == "" {
		return false
	}
	literal := filepath.Clean(filepath.Join(filepath.Dir(dest), before.Target))
	return literal == filepath.Join(root, e.SkillName, filepath.FromSlash(e.SourceFile))
}

func preserveProjectPath(plan *skillPlan, rel string, before FileState, managed, canonical, unchangedOwned bool) bool {
	switch {
	case before.Kind == "directory" || (before.Kind != "absent" && !managed && !canonical):
		plan.disposition(rel, "preserve", "Unknown content is not owned by the skill manager")
	case before.Kind == "symlink" && !unchangedOwned:
		plan.disposition(rel, "preserve", "Unverified link target is not managed")
	case before.Kind != "absent" && !unchangedOwned && !canonical:
		plan.disposition(rel, "consent_required", "Modified managed content requires exact-path overwrite consent")
	default:
		return false
	}
	return true
}

func isEmptyDir(p string) bool {
	ents, err := os.ReadDir(p)
	return err == nil && len(ents) == 0
}

func dropUnownedPackDirectories(plan *skillPlan, expected map[string]*expectedSkill, manifest *agents.SkillsManifest) (map[string]*expectedSkill, error) {
	owned := map[string]bool{}
	for _, e := range manifest.Entries {
		owned[path.Dir(e.InstalledPath)] = true
	}
	keys := make([]string, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kept := map[string]*expectedSkill{}
	for _, p := range keys {
		w := expected[p]
		dir := path.Dir(p)
		packOnly := true
		for _, e := range w.entries {
			o, _, _ := e.Provenance()
			packOnly = packOnly && o == "pack"
		}
		if packOnly && !owned[dir] {
			ds, err := plan.observe(dir)
			if err != nil {
				return nil, err
			}
			if ds.Kind == "directory" {
				fs, err := plan.observe(p)
				if err != nil {
					return nil, err
				}
				if fs.Kind == "absent" && !isEmptyDir(filepath.Join(plan.root.Path, filepath.FromSlash(dir))) {
					plan.disposition(p, "preserve", "Existing skill directory is not owned by the skill manager")
					continue
				}
			}
		}
		kept[p] = w
	}
	return kept, nil
}

func (p *skillPlan) prune(retired []agents.SkillEntry) error {
	deleted := map[string]bool{}
	for _, w := range p.writes {
		if w.Effect.After.Kind == "absent" {
			deleted[w.Effect.Path] = true
		}
	}
	candidates := map[string]bool{}
	for _, e := range retired {
		root := agents.ProjectSkillRoot(e.AgentKey)
		stop := path.Join(root, e.SkillName)
		cur := path.Dir(e.InstalledPath)
		for cur == stop || strings.HasPrefix(cur, stop+"/") {
			candidates[cur] = true
			if cur == stop {
				break
			}
			cur = path.Dir(cur)
		}
	}
	list := make([]string, 0, len(candidates))
	for c := range candidates {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		di, dj := len(strings.Split(list[i], "/")), len(strings.Split(list[j], "/"))
		if di != dj {
			return di > dj
		}
		return list[i] < list[j]
	})
	for _, rel := range list {
		before, err := p.observe(rel)
		if err != nil {
			return err
		}
		if before.Kind != "directory" {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(p.root.Path, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		all := true
		for _, e := range ents {
			all = all && deleted[rel+"/"+e.Name()]
		}
		if all {
			var owners []agents.SkillEntry
			for _, e := range retired {
				if strings.HasPrefix(e.InstalledPath, rel+"/") {
					owners = append(owners, e)
				}
			}
			p.write(rel, before, Absent(), nil, owners, 3, "Prune empty retired skill directory", "manifest", nil)
			deleted[rel] = true
		}
	}
	return nil
}

func stateList(s FileState) []any {
	var mode any
	if s.Mode >= 0 {
		mode = s.Mode
	}
	return []any{s.Kind, nullable(s.SHA256), nullable(s.Target), mode}
}

// allocateSkillBackup is prepare_skill_backup's directory choice:
// .kittify/.migration-backup/agent-skills/state-v1-<sha256>[-n] (RC7).
func allocateSkillBackup(project string, recs [][3]any) (string, error) {
	sort.SliceStable(recs, func(i, j int) bool { return recs[i][0].(string) < recs[j][0].(string) })
	var records []any
	for _, r := range recs {
		records = append(records, []any{r[0], stateList(r[1].(FileState)), stateList(r[2].(FileState))})
	}
	serialized := jsonx.Dumps([]any{"agent-skills-backup-v1", records}, jsonx.Options{Indent: -1, EnsureASCII: true, Compact: true})
	digest := Digest([]byte(serialized))
	parent := ".kittify/.migration-backup/agent-skills"
	candidate := parent + "/state-v1-" + digest
	for i := 1; ; i++ {
		st, err := observeConfined(project, candidate)
		if err != nil {
			return "", err
		}
		if st.Kind == "absent" {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s/state-v1-%s-%d", parent, digest, i)
	}
}
