package toolsurface

import (
	"github.com/spec-kitty/go-kitty3/internal/agents"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCommandRetirementPreservesEdits(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kittify"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kittify/config.yaml"), []byte("agents:\n  available: [codex]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	content, err := agents.RenderCommandSkill(root, "plan")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := agents.LegacyCommandSkillRendering(content, "plan")
	if err != nil {
		t.Fatal(err)
	}
	rel := agents.LegacyCommandSkillPath("plan")
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, append(legacy, []byte("\nMy local edits.\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	entry := CommandEntry{Path: rel, ContentHash: Digest(legacy), Agents: []string{"codex"}, InstalledAt: "2026-10-23T00:00:00+00:00", SpecKittyVersion: "4.0.0rc8"}
	if err := os.WriteFile(filepath.Join(root, commandManifestRel), SerializeCommandManifest([]CommandEntry{entry}), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := InstallCommandsForAgents(root, []string{"codex"}, "2026-10-23T00:00:00+00:00", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.LegacyKept) != 1 || report.LegacyKept[0] != rel {
		t.Fatalf("report=%+v", report)
	}
	b, err := os.ReadFile(abs)
	if err != nil || !strings.Contains(string(b), "My local edits.") {
		t.Fatal("edited legacy file lost")
	}
	entries, err := LoadCommandManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == rel {
			t.Fatal("edited legacy entry still managed")
		}
	}
	if err := os.WriteFile(abs, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	report, err = InstallCommandsForAgents(root, []string{"codex"}, "2026-10-23T00:00:00+00:00", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.LegacyRemoved) != 1 || report.LegacyRemoved[0] != rel {
		t.Fatalf("unrecorded proof report=%+v", report)
	}
	if _, err := os.Lstat(filepath.Dir(abs)); !os.IsNotExist(err) {
		t.Fatalf("empty legacy folder retained: %v", err)
	}
}

func TestLegacyCommandRetirementRechecksFolderMembership(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kittify"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kittify/config.yaml"), []byte("agents:\n  available: [codex]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	content, err := agents.RenderCommandSkill(root, "plan")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := agents.LegacyCommandSkillRendering(content, "plan")
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, ".agents/skills/spec-kitty.plan")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), legacy, 0644); err != nil {
		t.Fatal(err)
	}
	assessment := PrepareCommands(Root{"project", "project", root}, []string{"codex"}, CommandOptions{Now: "2026-10-23T00:00:00+00:00"})
	if !assessment.Complete {
		t.Fatalf("prepare: %+v", assessment.Diagnostics)
	}
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("Added during review.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result := ApplyCommands(assessment)
	if result.Outcome != "precondition_changed" || len(result.Succeeded) > 0 {
		t.Fatalf("apply: %+v", result)
	}
	if got, err := os.ReadFile(filepath.Join(folder, "SKILL.md")); err != nil || string(got) != string(legacy) {
		t.Fatalf("legacy file changed before refusal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, agents.CommandSkillPath("plan"))); !os.IsNotExist(err) {
		t.Fatalf("new command written before refusal: %v", err)
	}
}
