package m2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedAbsenceScriptPinsSourceBoundToolchain(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "m2-absence", "inspect.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{"SPEC_KITTY_M2_GO_VERSION", "!= go1.26.7", "GOTOOLCHAIN=$required_go_version"} {
		if !strings.Contains(text, required) {
			t.Fatalf("nested absence proof does not enforce source toolchain contract %q", required)
		}
	}
	for _, forbidden := range []string{"GOTOOLCHAIN=local", "-modcacherw"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("nested absence proof retains ineffective toolchain setting %q", forbidden)
		}
	}
}

func TestAbsenceQualificationEnvironmentRefusesMissingOrWrongToolchainAndAmbientSubstitution(t *testing.T) {
	for _, version := range []string{"", "go1.26.4", "go1.27.0"} {
		if _, err := absenceQualificationEnvironment(nil, "/sidecar", version); err == nil {
			t.Fatalf("toolchain %q accepted", version)
		}
	}
	environment, err := absenceQualificationEnvironment([]string{
		"PATH=/bin",
		"GOTOOLCHAIN=local",
		"SPEC_KITTY_M2_GO_VERSION=go1.26.4",
		"SPEC_KITTY_WP15_GAPDBD=/wrong",
	}, "/exact/sidecar", "go1.26.7")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"PATH=/bin":                             1,
		"SPEC_KITTY_M2_GO_VERSION=go1.26.7":     1,
		"SPEC_KITTY_WP15_GAPDBD=/exact/sidecar": 1,
	}
	for _, value := range environment {
		if strings.HasPrefix(value, "GOTOOLCHAIN=") {
			t.Fatalf("ambient toolchain leaked into nested proof: %q", value)
		}
		if _, ok := want[value]; ok {
			want[value]--
		}
	}
	for value, count := range want {
		if count != 0 {
			t.Fatalf("nested proof environment does not contain exactly one %q", value)
		}
	}
}

func TestNestedAbsenceScriptRefusesMissingOrWrongToolchainBeforeCreatingTarget(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "m2-absence", "inspect.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range [][]string{
		{"PATH=" + os.Getenv("PATH")},
		{"PATH=" + os.Getenv("PATH"), "SPEC_KITTY_M2_GO_VERSION=go1.26.4"},
	} {
		target := filepath.Join(t.TempDir(), "must-not-exist")
		command := exec.Command(script, target)
		command.Env = environment
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("invalid toolchain started nested absence proof: %s", output)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("invalid toolchain created target: %v", err)
		}
	}
}

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
