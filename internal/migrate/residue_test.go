package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// is_kitty_specs_residue (#6627, #6669): file-presence mode outside git, and
// git-aware mode once git tracks something under kitty-specs/.
func TestIsKittySpecsResidue(t *testing.T) {
	root := t.TempDir()
	specs := filepath.Join(root, "kitty-specs")
	writeFile(t, filepath.Join(specs, "mission", "spec.md"), "# m\n")
	writeFile(t, filepath.Join(specs, "notes", "tasks.md"), "# t\n")
	writeFile(t, filepath.Join(specs, "leftover", "decisions", "a.md"), "x\n")
	writeFile(t, filepath.Join(specs, "identified", "meta.json"), `{"mission_id": "01ABCDEFGHJKMNPQRSTVWXYZ00"}`)

	// File presence (no git): only a folder holding no Mission content is residue.
	tracked := ProbeTrackedMissionPaths(root)
	if tracked != nil {
		t.Fatalf("a non-git root must probe as None, got %v", tracked)
	}
	for name, want := range map[string]bool{"mission": false, "notes": false, "leftover": true, "identified": false} {
		if got := IsKittySpecsResidue(filepath.Join(specs, name), root, tracked); got != want {
			t.Errorf("file presence: %s residue=%v, want %v", name, got, want)
		}
	}

	// Git-aware: a committed folder with only tasks.md is residue; a Mission
	// whose tracked spec.md was deleted on disk is still a Mission.
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")
	os.Remove(filepath.Join(specs, "mission", "spec.md"))
	tracked = ProbeTrackedMissionPaths(root)
	if len(tracked) == 0 {
		t.Fatal("git-aware probe found no tracked paths")
	}
	for name, want := range map[string]bool{"mission": false, "notes": true, "leftover": true, "identified": false} {
		if got := IsKittySpecsResidue(filepath.Join(specs, name), root, tracked); got != want {
			t.Errorf("git-aware: %s residue=%v, want %v", name, got, want)
		}
		if got := IsKittySpecsResidueLazy(filepath.Join(specs, name)); got != want {
			t.Errorf("git-aware lazy: %s residue=%v, want %v", name, got, want)
		}
	}
	if IsKittySpecsResidue(filepath.Join(root, "other", "x"), root, tracked) {
		t.Error("a folder outside kitty-specs/ is never residue")
	}
}

func TestResidueNoticeRoundTrip(t *testing.T) {
	line := "Worktree wt1: " + ResidueNotice("kitty-specs/old notes")
	if got, ok := ResidueNoticePath(line); !ok || got != "kitty-specs/old notes" {
		t.Fatalf("ResidueNoticePath(%q) = %q, %v", line, got, ok)
	}
	if _, ok := ResidueNoticePath("Migration x not needed"); ok {
		t.Fatal("a plain warning is not a residue notice")
	}
}
