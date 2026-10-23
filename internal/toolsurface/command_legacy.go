package toolsurface

import (
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/agents"
)

func (b *commandBatch) legacyFolderLeft(folder string) {
	abs := filepath.Join(b.root.Path, filepath.FromSlash(folder))
	if _, err := b.observeInput(abs, true); err != nil {
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != "SKILL.md" {
			b.commands = append(b.commands, PreparedCommand{Path: folder + "/", ReportKind: "legacy_unrecorded"})
			b.disposition(folder, "legacy_kept", "Dotted command-skill folder holds other files; left in place")
			return
		}
	}
}

func (b *commandBatch) retireLegacyFile(rel string, owners []string, proof Proof) error {
	if err := b.effect(rel, Absent(), owners, proof); err != nil {
		return err
	}
	folder := path.Dir(rel)
	abs := filepath.Join(b.root.Path, filepath.FromSlash(folder))
	if _, err := b.observeInput(abs, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(abs)
	if err == nil && len(entries) == 1 && entries[0].Name() == "SKILL.md" {
		if err := b.effect(folder, Absent(), owners, proof); err != nil {
			return err
		}
	} else {
		b.legacyFolderLeft(folder)
	}
	p := proof
	b.commands = append(b.commands, PreparedCommand{Path: rel, ReportKind: "legacy_removed", Proof: &p})
	return nil
}

func (b *commandBatch) migrateLegacyEntry(entry CommandEntry) error {
	before, err := b.observeDestination(entry.Path)
	unsafe := false
	if err != nil {
		var ie *agents.InstallerError
		if !errors.As(err, &ie) || ie.Code != "unsafe_path" {
			return err
		}
		unsafe = true
	}
	b.removePath(entry.Path)
	if !unsafe && before.Kind == "absent" {
		b.commands = append(b.commands, PreparedCommand{Path: entry.Path, ReportKind: "legacy_removed"})
		b.legacyFolderLeft(path.Dir(entry.Path))
		return nil
	}
	owned := !unsafe && before.Kind == "file" && before.SHA256 == entry.ContentHash
	if !owned && !unsafe && before.Kind == "file" {
		command := agents.ParseCommandSkillPath(entry.Path, true)
		if content, e := agents.RenderCommandSkill(b.root.Path, command); e == nil {
			if legacy, e := agents.LegacyCommandSkillRendering(content, command); e == nil {
				owned = Digest(legacy) == before.SHA256
			}
		}
	}
	if !owned {
		b.commands = append(b.commands, PreparedCommand{Path: entry.Path, ReportKind: "legacy_kept"})
		b.disposition(entry.Path, "legacy_kept", "Edited legacy dotted command skill kept on disk; it is no longer managed")
		return nil
	}
	return b.retireLegacyFile(entry.Path, entry.Agents, Proof{"manifest", commandManifestRel + "#" + entry.Path})
}

func (b *commandBatch) retireUnrecordedLegacy(command string, content []byte) error {
	rel := agents.LegacyCommandSkillPath(command)
	folder := path.Dir(rel)
	folderState, err := b.observeDestination(folder)
	unsafe := false
	if err != nil {
		var ie *agents.InstallerError
		if !errors.As(err, &ie) || ie.Code != "unsafe_path" {
			return err
		}
		unsafe = true
	}
	if !unsafe && folderState.Kind == "absent" {
		return nil
	}
	before := Absent()
	if !unsafe && folderState.Kind == "directory" {
		before, err = b.observeDestination(rel)
		if err != nil {
			var ie *agents.InstallerError
			if !errors.As(err, &ie) || ie.Code != "unsafe_path" {
				return err
			}
			unsafe = true
		}
		if before.Kind == "absent" && !unsafe {
			b.legacyFolderLeft(folder)
			return nil
		}
	} else {
		unsafe = true
	}
	legacy, err := agents.LegacyCommandSkillRendering(content, command)
	if err != nil {
		return err
	}
	digest := Digest(legacy)
	if unsafe || before.Kind != "file" || before.SHA256 != digest {
		b.commands = append(b.commands, PreparedCommand{Path: rel, ReportKind: "legacy_unrecorded"})
		b.disposition(rel, "legacy_kept", "Unrecorded dotted command skill is not Spec Kitty's legacy rendering; left in place")
		return nil
	}
	return b.retireLegacyFile(rel, b.agents, Proof{"canonical_content", "legacy_rendering:" + command + ":" + digest})
}
