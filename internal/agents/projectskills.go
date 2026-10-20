package agents

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// SkillsManifestFile is .kittify/skills-manifest.json.
const SkillsManifestFile = "skills-manifest.json"

// SkillEntry is one ManagedFileEntry.
type SkillEntry struct {
	SkillName         string
	SourceFile        string
	InstalledPath     string
	InstallationClass string
	AgentKey          string
	ContentHash       string // "sha256:<hex>"
	InstalledAt       string
	DeliveryMode      string
	extra             []jsonx.KV // provenance keys preserved verbatim
}

func (e SkillEntry) key() string { return e.InstalledPath + "\x00" + e.AgentKey }

func (e SkillEntry) object() *jsonx.Object {
	o := jsonx.NewObject(
		"skill_name", e.SkillName,
		"source_file", e.SourceFile,
		"installed_path", e.InstalledPath,
		"installation_class", e.InstallationClass,
		"agent_key", e.AgentKey,
		"content_hash", e.ContentHash,
		"installed_at", e.InstalledAt,
		"delivery_mode", e.DeliveryMode,
	)
	for _, kv := range e.extra {
		o.Set(kv.Key, kv.Value)
	}
	return o
}

// SkillsManifest is ManagedSkillManifest.
type SkillsManifest struct {
	CreatedAt        string
	UpdatedAt        string
	SpecKittyVersion string
	Entries          []SkillEntry
}

// SkillsManifestPath returns the manifest path for root.
func SkillsManifestPath(root string) string {
	return filepath.Join(root, ".kittify", SkillsManifestFile)
}

// LoadSkillsManifest reads the manifest (nil when absent or unparseable).
func LoadSkillsManifest(root string) *SkillsManifest {
	b, err := os.ReadFile(SkillsManifestPath(root))
	if err != nil {
		return nil
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil
	}
	m := &SkillsManifest{CreatedAt: o.GetString("created_at"), UpdatedAt: o.GetString("updated_at"), SpecKittyVersion: o.GetString("spec_kitty_version")}
	known := map[string]bool{"skill_name": true, "source_file": true, "installed_path": true, "installation_class": true, "agent_key": true, "content_hash": true, "installed_at": true, "delivery_mode": true}
	for _, x := range o.GetList("entries") {
		eo, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		e := SkillEntry{SkillName: eo.GetString("skill_name"), SourceFile: eo.GetString("source_file"), InstalledPath: eo.GetString("installed_path"),
			InstallationClass: eo.GetString("installation_class"), AgentKey: eo.GetString("agent_key"), ContentHash: eo.GetString("content_hash"),
			InstalledAt: eo.GetString("installed_at"), DeliveryMode: eo.GetString("delivery_mode")}
		for _, kv := range eo.Pairs() {
			if !known[kv.Key] {
				e.extra = append(e.extra, kv)
			}
		}
		m.Entries = append(m.Entries, e)
	}
	return m
}

func renderSkillsManifest(m *SkillsManifest) string {
	l := make([]any, len(m.Entries))
	for i, e := range m.Entries {
		l[i] = e.object()
	}
	return jsonx.Dumps(jsonx.NewObject("version", 1, "created_at", m.CreatedAt, "updated_at", m.UpdatedAt, "spec_kitty_version", m.SpecKittyVersion, "entries", l), jsonx.Pretty) + "\n"
}

// SaveSkillsManifest is prepare_manifest + save_manifest: with a previous
// manifest, created_at and unchanged entries' installed_at are retained,
// previous entry order is kept and new entries follow sorted by
// (installed_path, agent_key); an otherwise-identical manifest is not
// rewritten. now stamps updated_at.
func SaveSkillsManifest(root string, m *SkillsManifest, now string) error {
	prev := LoadSkillsManifest(root)
	desired := &SkillsManifest{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, SpecKittyVersion: m.SpecKittyVersion, Entries: append([]SkillEntry(nil), m.Entries...)}
	if prev != nil {
		desired.CreatedAt = prev.CreatedAt
		old := map[string]SkillEntry{}
		order := map[string]int{}
		for i, e := range prev.Entries {
			old[e.key()] = e
			if _, ok := order[e.key()]; !ok {
				order[e.key()] = i
			}
		}
		for i, e := range desired.Entries {
			if p, ok := old[e.key()]; ok && p.SkillName == e.SkillName && p.SourceFile == e.SourceFile && p.InstallationClass == e.InstallationClass {
				desired.Entries[i].InstalledAt = p.InstalledAt
			}
		}
		sort.SliceStable(desired.Entries, func(i, j int) bool {
			oi, iok := order[desired.Entries[i].key()]
			oj, jok := order[desired.Entries[j].key()]
			if !iok {
				oi = len(order)
			}
			if !jok {
				oj = len(order)
			}
			if oi != oj {
				return oi < oj
			}
			if desired.Entries[i].InstalledPath != desired.Entries[j].InstalledPath {
				return desired.Entries[i].InstalledPath < desired.Entries[j].InstalledPath
			}
			return desired.Entries[i].AgentKey < desired.Entries[j].AgentKey
		})
		cmp := *desired
		cmp.UpdatedAt = prev.UpdatedAt
		cmp.SpecKittyVersion = desired.SpecKittyVersion
		if renderSkillsManifest(&cmp) == renderSkillsManifest(prev) {
			return nil
		}
		desired.UpdatedAt = now
	}
	if desired.CreatedAt == "" {
		desired.CreatedAt = now
	}
	if desired.UpdatedAt == "" {
		desired.UpdatedAt = now
	}
	return writeAtomic(SkillsManifestPath(root), []byte(renderSkillsManifest(desired)), 0o600)
}

// globalSkillBytes returns the bytes the global installer writes for a skill
// file (SKILL.md gains host frontmatter when missing).
func globalSkillBytes(s Skill, rel string) ([]byte, error) {
	b, err := ReadSkillFile(s, rel)
	if err != nil {
		return nil, err
	}
	if rel == "SKILL.md" {
		b = []byte(EnsureSkillFrontmatter(string(b), s.Name))
	}
	return b, nil
}

// SkillOrder returns skills sorted like upstream's path-based discovery
// (name + "/"), so "spec-kitty" sorts after "spec-kitty-*".
func SkillOrder(skills []Skill) []Skill {
	out := append([]Skill(nil), skills...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name+"/" < out[j].Name+"/" })
	return out
}

// InstallProjectSkills projects the canonical doctrine skills into agent's
// primary project skill root (install_skills_for_agent). New files are
// written read-only (0444, the global source mode); identical files are
// recorded; differing files are replaced only when the previous manifest
// owns them unchanged, otherwise preserved. Returns the agent's manifest
// entries (path order) and the number of files written.
func InstallProjectSkills(root, agent, now string) ([]SkillEntry, int, error) {
	class := SkillClass(agent)
	skillRoot := ProjectSkillRoot(agent)
	if class == "" || class == SkillClassWrapper || skillRoot == "" {
		return nil, 0, nil
	}
	prev := LoadSkillsManifest(root)
	owned := map[string]string{}
	if prev != nil {
		for _, e := range prev.Entries {
			owned[e.InstalledPath] = e.ContentHash
		}
	}
	var entries []SkillEntry
	written := 0
	for _, s := range SkillOrder(Skills()) {
		for _, rel := range s.Files {
			content, err := globalSkillBytes(s, rel)
			if err != nil {
				return entries, written, err
			}
			installed := strings.TrimSuffix(skillRoot, "/") + "/" + s.Name + "/" + rel
			abs := filepath.Join(root, filepath.FromSlash(installed))
			hash := "sha256:" + sha256Hex(content)
			existing, rerr := os.ReadFile(abs)
			switch {
			case rerr == nil && string(existing) == string(content):
			case rerr == nil:
				if h, ok := owned[installed]; !ok || h != "sha256:"+sha256Hex(existing) {
					if prev != nil {
						for _, e := range prev.Entries {
							if e.InstalledPath == installed && e.AgentKey == agent {
								entries = append(entries, e)
							}
						}
					}
					continue
				}
				fallthrough
			default:
				if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
					return entries, written, rerr
				}
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return entries, written, err
				}
				if err := writeAtomic(abs, content, 0o444); err != nil {
					return entries, written, err
				}
				written++
			}
			entries = append(entries, SkillEntry{SkillName: s.Name, SourceFile: rel, InstalledPath: installed, InstallationClass: class,
				AgentKey: agent, ContentHash: hash, InstalledAt: now, DeliveryMode: "copy"})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].InstalledPath < entries[j].InstalledPath })
	return entries, written, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
