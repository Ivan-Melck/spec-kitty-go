package assetpreserve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOverwriteTruthTable pins guard_destructive_overwrite's table.
func TestOverwriteTruthTable(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "kitty-specs", "m", "research.md")
	cases := []struct {
		exists, substantive, authorized bool
		proceed                         bool
		reason, diag                    string
	}{
		{false, false, false, false, "absent-empty-replacement", "Refused to fabricate an empty kitty-specs/m/research.md; destination is absent and replacement is not substantive"},
		{false, true, false, true, "absent", "Proceeding; kitty-specs/m/research.md is absent"},
		{true, false, true, false, "would-truncate-to-empty", "Refused to truncate kitty-specs/m/research.md to empty; existing bytes are not proven package-owned"},
		{true, true, false, false, "unauthorized", "Refused to overwrite kitty-specs/m/research.md; not proven package-owned and not authorized"},
		{true, true, true, true, "authorized-overwrite", "Proceeding to overwrite kitty-specs/m/research.md; authorized"},
	}
	for _, c := range cases {
		os.Remove(dest)
		if c.exists {
			write(t, dest, "mine")
		}
		v, err := GuardDestructiveOverwrite(dest, root, c.substantive, c.authorized, nil, "")
		if err != nil || v.Proceed != c.proceed || v.Reason != c.reason || v.Diagnostic != c.diag || v.BackupPath != "" {
			t.Errorf("%+v: got %+v %v", c, v, err)
		}
	}
	// a proven package-owned destination always proceeds
	write(t, dest, "x <!-- spec-kitty-command-version: 1 --> y")
	v, _ := GuardDestructiveOverwrite(dest, root, false, false, CanonicalContentProver{}, "")
	if !v.Proceed || v.Reason != "package-owned (canonical_content)" || v.Diagnostic != "Proceeding over package-owned kitty-specs/m/research.md (proof: canonical_content)" {
		t.Errorf("owned: %+v", v)
	}
	// an authorized overwrite archives first when a backup parent is given
	write(t, dest, "mine")
	v, err := GuardDestructiveOverwrite(dest, root, true, true, nil, filepath.Join(root, ".kittify", "backups"))
	if err != nil || !v.Proceed || v.BackupPath == "" {
		t.Fatalf("archive: %+v %v", v, err)
	}
	if b, _ := os.ReadFile(v.BackupPath); string(b) != "mine" || !strings.Contains(v.Diagnostic, "archived to .kittify/backups/.backup-") {
		t.Errorf("archive content/diag: %q %q", b, v.Diagnostic)
	}
}

func TestRemovalAndProvers(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, ".kittify", "templates")
	write(t, filepath.Join(owned, "a.md"), "a")
	v, err := GuardDestructiveRemoval(owned, root, ManagedPathProver{ManagedRelPaths: []string{".kittify/templates"}}, true, "", false)
	if err != nil || !v.Owned || v.Diagnostic != "Removed package-owned .kittify/templates (proof: managed_path)" {
		t.Fatalf("owned removal: %+v %v", v, err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Error("owned path not removed")
	}
	user := filepath.Join(root, ".kittify", "command-templates", "x.md")
	write(t, user, "user")
	v, _ = GuardDestructiveRemoval(user, root, AnyProver{ManagedPathProver{}, CanonicalContentProver{}}, false, "", false)
	if v.Owned || v.Diagnostic != "Preserved .kittify/command-templates/x.md; not package-owned (name is not ownership proof) — left in place" {
		t.Errorf("preserve: %+v", v)
	}
	v, _ = GuardDestructiveRemoval(user, root, ManagedPathProver{}, false, filepath.Join(root, "bk"), true)
	if !strings.HasPrefix(v.Diagnostic, "Would preserve") {
		t.Errorf("dry-run: %+v", v)
	}
	v, err = GuardDestructiveRemoval(user, root, ManagedPathProver{}, false, filepath.Join(root, "bk"), false)
	if err != nil || v.BackupPath == "" {
		t.Fatalf("archive removal: %+v %v", v, err)
	}
	if b, _ := os.ReadFile(v.BackupPath); string(b) != "user" {
		t.Error("archived bytes differ")
	}
	if _, err := os.Stat(user); !os.IsNotExist(err) {
		t.Error("archived original not removed")
	}
	v, _ = GuardDestructiveRemoval(user, root, ManagedPathProver{}, false, "", false)
	if v.Reason != "absent" || v.Diagnostic != "Skipped .kittify/command-templates/x.md; path is absent" {
		t.Errorf("absent: %+v", v)
	}
	// manifest prover: copy delivery + matching sha256
	skill := filepath.Join(root, ".claude", "skills", "s", "SKILL.md")
	write(t, skill, "skill")
	write(t, filepath.Join(root, ".kittify", "skills-manifest.json"),
		`{"version": 1, "entries": [{"installed_path": ".claude/skills/s/SKILL.md", "delivery_mode": "copy", "content_hash": "sha256:`+sha256Hex([]byte("skill"))+`"}]}`)
	if p := (ManifestProver{}).Prove(filepath.Dir(skill), root); p == nil || p.Detail != "manifest-dir:.claude/skills/s" {
		t.Errorf("manifest dir proof: %+v", p)
	}
	write(t, skill, "edited")
	if p := (ManifestProver{}).Prove(skill, root); p != nil {
		t.Errorf("edited file proven: %+v", p)
	}
}
