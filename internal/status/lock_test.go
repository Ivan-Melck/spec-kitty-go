package status

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

func writeMeta(t *testing.T, dir string, kv ...any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(jsonx.Dumps(jsonx.NewObject(kv...), jsonx.Line)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMissionLockPathKeysEveryDirectoryOfOneMission(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	common := filelock.RealPath(filepath.Join(root, ".git"))
	primary := filepath.Join(root, "kitty-specs", "demo-01ABCDEF")
	writeMeta(t, primary, "mission_slug", "demo-01ABCDEF", "mission_id", "01ABCDEFGHJKMNPQRSTVWXYZ00", "coordination_branch", "kitty/coord")
	coordDir := filepath.Join(root, ".worktrees", "demo-01ABCDEF-coord", "kitty-specs", "demo-01ABCDEF")
	if err := os.MkdirAll(coordDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(common, "spec-kitty-locks", "demo-01ABCDEF.status.lock")
	if got := MissionLockPath(primary, ""); got != want {
		t.Errorf("primary: %q, want %q", got, want)
	}
	if got := MissionLockPath(coordDir, ""); got != want {
		t.Errorf("coordination dir: %q, want %q", got, want)
	}

	legacy := filepath.Join(root, "kitty-specs", "001-legacy")
	writeMeta(t, legacy, "mission_slug", "001-legacy", "coordination_branch", "kitty/coord")
	if got := MissionLockPath(legacy, ""); got != filepath.Join(common, "spec-kitty-locks", "001-legacy.status.lock") {
		t.Errorf("legacy carve-out: %q", got)
	}

	routed := filepath.Join(root, "kitty-specs", "feature")
	writeMeta(t, routed, "mission_slug", "feature", "mission_id", "01KZZZZZAAAAAAAAAAAAAAAAAA", "coordination_branch", "kitty/coord")
	if got := MissionLockPath(routed, ""); got != filepath.Join(common, "spec-kitty-locks", "feature-01KZZZZZ.status.lock") {
		t.Errorf("coordination key: %q", got)
	}
	plain := filepath.Join(root, "kitty-specs", "002-plain")
	writeMeta(t, plain, "mission_slug", "002-plain")
	if got := MissionLockPath(plain, ""); got != filepath.Join(common, "spec-kitty-locks", "002-plain.status.lock") {
		t.Errorf("directory key: %q", got)
	}
}

func TestMissionLockPathOutsideGit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".kittify"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "kitty-specs", "001-x")
	if got, want := MissionLockPath(dir, ""), filepath.Join(root, ".kittify", "spec-kitty-locks", "001-x.status.lock"); got != want {
		t.Errorf("non-git project: %q, want %q", got, want)
	}
	bare := filepath.Join(t.TempDir(), "kitty-specs", "001-y")
	if got := MissionLockPath(bare, ""); !strings.HasSuffix(got, filepath.Join(".spec-kitty", "spec-kitty-locks", "001-y.status.lock")) {
		t.Errorf("bare tree: %q", got)
	}
}

func TestLockMissionIsReentrantAndRecordsTheHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kitty-specs", "001-x")
	outer, err := LockMission(dir, "", filelock.Forever)
	if err != nil {
		t.Fatal(err)
	}
	holder := outer.Path() + ".holder"
	if _, err := os.Stat(holder); err != nil {
		t.Fatalf("holder sidecar missing: %v", err)
	}
	// AppendRows re-enters the held lock instead of deadlocking.
	if err := AppendRows(EventsPath(dir), jsonx.NewObject("event_id", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(holder); err != nil {
		t.Fatalf("a nested release cleared the holder: %v", err)
	}
	outer.Release()
	if _, err := os.Stat(holder); err == nil {
		t.Fatal("holder sidecar left after release")
	}
}

func TestLockMissionTimeoutNamesTheHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kitty-specs", "001-x")
	lockPath := MissionLockPath(dir, "")
	other, err := filelock.Acquire(lockPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	os.WriteFile(lockPath+".holder", []byte(`{"acquired_at": "T", "pid": 42, "thread": "MainThread"}`), 0o644)
	_, err = LockMission(dir, "", 150*time.Millisecond)
	var te *LockTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("want *LockTimeoutError, got %v", err)
	}
	if want := "Timed out acquiring status lock " + lockPath + " after 0.15s: held by pid 42 (thread MainThread) since T"; te.Error() != want {
		t.Fatalf("message:\n got %q\nwant %q", te.Error(), want)
	}
	if te.ErrorCode() != "STATUS_LOCK_HELD" {
		t.Fatal(te.ErrorCode())
	}
}
