package healthcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSpecKittyRepository(t *testing.T) {
	for in, want := range map[string]bool{
		"https://github.com/spec-kitty/spec-kitty":      true,
		"https://github.com/spec-kitty/spec-kitty.git/": true,
		"git@github.com:spec-kitty/spec-kitty.git":      true,
		"ssh://git@GitHub.com/Spec-Kitty/spec-kitty":    true,
		"http://github.com/spec-kitty/spec-kitty":       false,
		"https://example.com/spec-kitty/spec-kitty":     false,
		"https://github.com/spec-kitty/other":           false,
	} {
		if got := isSpecKittyRepository(in); got != want {
			t.Errorf("%s: got %v", in, got)
		}
	}
}

func TestBuiltInPackPathAndShape(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "built-in", "missions", "x"), 0o755)
	t.Setenv("SPEC_KITTY_PACKS_ROOT", dir)
	p := filepath.Join(dir, "built-in", "missions", "x", "mission.yaml")
	if !IsBuiltInPackPath(p) || IsBuiltInPackPath("/elsewhere/built-in/x") {
		t.Fatal("classification")
	}
	suffix := MissionSuffix("x")
	if !matchesCanonicalShape("/a/packs/built-in/missions/x/mission.yaml", suffix) || matchesCanonicalShape("/a/built-in/missions/x/mission.yaml", suffix) {
		t.Fatal("shape")
	}
	if !IsAbsolutePathAnyOS(`C:\a`) || IsAbsolutePathAnyOS(`C:a`) || !IsAbsolutePathAnyOS(`\\srv\share\x`) {
		t.Fatal("windows abs")
	}
	t.Setenv("SPEC_KITTY_PACKS_ROOT", filepath.Join(dir, "missing"))
	if BuiltInPackRoot() != "" {
		t.Fatal("misconfigured root must be ignored")
	}
}
