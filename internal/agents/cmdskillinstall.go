package agents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// CommandSkillReport mirrors command_installer.InstallReport.
type CommandSkillReport struct {
	Added            []string // written (first time or refreshed render)
	AlreadyInstalled []string // canonical bytes, manifest entry unchanged
	ReusedShared     []string // canonical bytes, entry gained the agent / hash
	Regenerated      []string // drifted manifest-owned projection rewritten
}

// InstalledCount is added + reused_shared + regenerated.
func (r CommandSkillReport) InstalledCount() int {
	return len(r.Added) + len(r.ReusedShared) + len(r.Regenerated)
}

// InstallerError is command_installer.InstallerError; Error() renders
// upstream's f"{code}: {context}".
type InstallerError struct {
	Code    string
	Context *jsonx.Object
}

func (e *InstallerError) Error() string {
	var b strings.Builder
	b.WriteString(e.Code + ": {")
	if e.Context != nil {
		for i, kv := range e.Context.Pairs() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(pyjson.Repr(kv.Key) + ": " + pyjson.Repr(kv.Value))
		}
	}
	b.WriteString("}")
	return b.String()
}

func addAgent(list []string, a string) []string {
	for _, x := range list {
		if x == a {
			return list
		}
	}
	list = append(append([]string(nil), list...), a)
	sort.Strings(list)
	return list
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pathError(code, rel string) error {
	return &InstallerError{Code: code, Context: jsonx.NewObject("path", rel)}
}

type cmdSkillPlan struct {
	rel, kind string
	content   []byte // nil when the bytes on disk are already canonical
	entry     *CommandSkillEntry
}

// InstallCommandSkills is command_installer.install for one command-skill
// agent: the complete canonical batch is assessed first and any unowned or
// unknown node (different bytes, a directory, a link) aborts the whole
// install with unexpected_collision before anything is written. Absent files
// are written (0644); manifest-owned drifted files are regenerated; identical
// files gain the agent as an owner.
func InstallCommandSkills(root, agent, now string) (CommandSkillReport, error) {
	var rep CommandSkillReport
	if !IsCommandSkillAgent(agent) {
		return rep, &InstallerError{Code: "unsupported_agent", Context: jsonx.NewObject("agents", []any{agent})}
	}
	manifest := LoadCommandSkillsManifest(root)
	var plans []cmdSkillPlan
	for _, c := range CommandSkillCommands {
		content, err := RenderCommandSkill(root, c)
		if err != nil {
			return rep, err
		}
		rel := CommandSkillPath(c)
		abs := filepath.Join(root, filepath.FromSlash(rel))
		digest := sha256Hex(content)
		existing := manifest[rel]
		if st, err := os.Lstat(filepath.Dir(abs)); err == nil && st.Mode()&os.ModeSymlink != 0 && existing == nil {
			return rep, pathError("unexpected_collision", rel)
		}
		var before []byte
		beforeKind := "absent"
		if st, err := os.Lstat(abs); err == nil {
			switch {
			case st.IsDir():
				return rep, pathError("unexpected_collision", rel)
			case st.Mode()&os.ModeSymlink != 0:
				if existing == nil {
					return rep, pathError("unexpected_collision", rel)
				}
				beforeKind = "symlink"
			default:
				beforeKind = "file"
				if before, err = os.ReadFile(abs); err != nil {
					return rep, err
				}
			}
		}
		same := beforeKind == "file" && sha256Hex(before) == digest
		regenerated := false
		if beforeKind == "file" && !same {
			if existing == nil {
				return rep, pathError("unexpected_collision", rel)
			}
			if sha256Hex(before) != existing.ContentHash {
				regenerated = true
			}
		}
		var entry *CommandSkillEntry
		if existing != nil && same {
			entry = &CommandSkillEntry{Path: rel, ContentHash: digest, Agents: addAgent(existing.Agents, agent), InstalledAt: existing.InstalledAt, SpecKittyVersion: existing.SpecKittyVersion}
		} else {
			var owners []string
			installedAt := now
			if existing != nil {
				owners, installedAt = existing.Agents, existing.InstalledAt
			}
			entry = &CommandSkillEntry{Path: rel, ContentHash: digest, Agents: addAgent(owners, agent), InstalledAt: installedAt, SpecKittyVersion: app.Version}
		}
		kind := "added"
		switch {
		case regenerated:
			kind = "regenerated"
		case same && existing != nil && existing.ContentHash == entry.ContentHash && sameStrings(existing.Agents, entry.Agents):
			kind = "already_installed"
		case same:
			kind = "reused_shared"
		}
		p := cmdSkillPlan{rel: rel, kind: kind, entry: entry}
		if !same {
			p.content = content
		}
		plans = append(plans, p)
	}
	for _, p := range plans {
		if p.content != nil {
			abs := filepath.Join(root, filepath.FromSlash(p.rel))
			mode := os.FileMode(0o644)
			if st, err := os.Lstat(abs); err == nil && st.Mode().IsRegular() {
				mode = st.Mode().Perm()
			} else if err == nil {
				os.Remove(abs)
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return rep, err
			}
			if err := writeAtomic(abs, p.content, mode); err != nil {
				return rep, err
			}
		}
		manifest[p.rel] = p.entry
		switch p.kind {
		case "added":
			rep.Added = append(rep.Added, p.rel)
		case "already_installed":
			rep.AlreadyInstalled = append(rep.AlreadyInstalled, p.rel)
		case "reused_shared":
			rep.ReusedShared = append(rep.ReusedShared, p.rel)
		case "regenerated":
			rep.Regenerated = append(rep.Regenerated, p.rel)
		}
	}
	return rep, SaveCommandSkillsManifest(root, manifest)
}

// CommandSkillRemoveReport is command_installer.RemoveReport.
type CommandSkillRemoveReport struct {
	Deref, Deleted, Kept []string
}

// RemoveCommandSkills is command_installer.remove: releases agent's
// ownership; the final owner's unchanged file (and its then-empty skill
// directory) is deleted. An edited owned file aborts with
// file_mutation_detected before anything changes.
func RemoveCommandSkills(root, agent string) (CommandSkillRemoveReport, error) {
	var rep CommandSkillRemoveReport
	if !IsCommandSkillAgent(agent) {
		return rep, &InstallerError{Code: "unsupported_agent", Context: jsonx.NewObject("agents", []any{agent})}
	}
	manifest := LoadCommandSkillsManifest(root)
	paths := make([]string, 0, len(manifest))
	for rel := range manifest {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	remaining := map[string][]string{}
	for _, rel := range paths {
		e := manifest[rel]
		var keep []string
		for _, a := range e.Agents {
			if a != agent {
				keep = append(keep, a)
			}
		}
		if len(keep) == len(e.Agents) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if st, err := os.Lstat(abs); err == nil {
			if st.IsDir() {
				return rep, pathError("file_mutation_detected", rel)
			}
			if st.Mode().IsRegular() {
				if b, err := os.ReadFile(abs); err == nil && sha256Hex(b) != e.ContentHash {
					return rep, pathError("file_mutation_detected", rel)
				}
			}
		}
		rep.Deref = append(rep.Deref, rel)
		remaining[rel] = keep
		if len(keep) > 0 {
			rep.Kept = append(rep.Kept, rel)
		} else {
			rep.Deleted = append(rep.Deleted, rel)
		}
	}
	for rel, keep := range remaining {
		if len(keep) > 0 {
			manifest[rel].Agents = keep
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		os.Remove(abs)
		dir := filepath.Dir(abs)
		if ents, err := os.ReadDir(dir); err == nil && len(ents) == 0 {
			os.Remove(dir)
		}
		delete(manifest, rel)
	}
	return rep, SaveCommandSkillsManifest(root, manifest)
}
