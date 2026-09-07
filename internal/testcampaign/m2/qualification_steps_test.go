package m2

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemovePrivateWorkingTreeHandlesReadOnlyToolchainCacheWithoutFollowingSymlinks(t *testing.T) {
	parent := t.TempDir()
	attempt := filepath.Join(parent, ".attempts", "run", "evidence")
	working := filepath.Join(attempt, ".working-absence")
	include := filepath.Join(working, "cache", "mod", "toolchain", "pkg", "include")
	if err := os.MkdirAll(include, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(include, "funcdata.h"), []byte("frozen"), 0o444); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(parent, "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(external, "preserved")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(working, "external-link")); err != nil {
		t.Fatal(err)
	}
	for path := include; path != working; path = filepath.Dir(path) {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(working, 0o555); err != nil {
		t.Fatal(err)
	}

	if err := removePrivateWorkingTree(attempt, working); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(working); !os.IsNotExist(err) {
		t.Fatalf("private working tree remains: %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "outside" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
	if err := removePrivateWorkingTree(attempt, working); err != nil {
		t.Fatalf("repeated cleanup failed: %v", err)
	}
	if err := os.Symlink(external, working); err != nil {
		t.Fatal(err)
	}
	if err := removePrivateWorkingTree(attempt, working); err == nil {
		t.Fatal("symlinked working root accepted")
	}
	if err := os.Remove(working); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range [][2]string{
		{"relative", "relative/.working-absence"},
		{string(filepath.Separator), filepath.Join(string(filepath.Separator), ".working-absence")},
		{os.TempDir(), filepath.Join(os.TempDir(), ".working-absence")},
		{filepath.Join(string(filepath.Separator), "home", "example", "repo"), filepath.Join(string(filepath.Separator), "home", "example", "repo", ".working-absence")},
		{attempt, filepath.Join(parent, "other")},
	} {
		if err := removePrivateWorkingTree(unsafe[0], unsafe[1]); err == nil {
			t.Fatalf("unsafe removal target %q accepted", unsafe)
		}
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "outside" {
		t.Fatalf("symlink target changed after root refusal: data=%q err=%v", data, err)
	}
}
