package placement

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/lanes"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// TestReadDirOwnedStates walks the owned read arm through the coordination
// states: deleted, unmaterialized (refused / tolerated), materialized, a
// worktree on the wrong branch, and an empty worktree.
func TestReadDirOwnedStates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	const slug, mid8 = "calc-01ABCDEF", "01ABCDEF"
	const branch = "kitty/mission-calc-01ABCDEF"
	mdir := filepath.Join(root, "kitty-specs", slug)
	os.MkdirAll(mdir, 0o755)
	os.WriteFile(filepath.Join(mdir, "meta.json"), []byte(`{"mission_id": "01ABCDEFGHJKMNPQRSTVWXYZ00", "coordination_branch": "`+branch+`", "topology": "coord"}`+"\n"), 0o644)
	run(t, root, "init", "-q", "-b", "main")
	run(t, root, "commit", "-q", "--allow-empty", "-m", "root")
	run(t, root, "add", ".")
	run(t, root, "commit", "-q", "-m", "init")
	o := Owned{RepositoryRoot: root, MissionDir: mdir, Topology: "coord"}

	if d, err := ReadDirOwned(o, slug, Primary, false); err != nil || d != mdir {
		t.Fatalf("primary: %q %v", d, err)
	}
	if _, err := ReadDirOwned(o, slug, Coord, false); err == nil || !err.(*ReadPathNotFound).Deleted {
		t.Fatalf("deleted: %v", err)
	}
	run(t, root, "branch", branch, "HEAD~1")
	_, err := ReadDirOwned(o, slug, Coord, false)
	ue, ok := err.(*OwnedCoordUnavailableError)
	if !ok || !strings.Contains(ue.Msg, "(state=unmaterialized)") || ue.ErrorCode() != "OWNED_COORDINATION_WORKSPACE_UNAVAILABLE" {
		t.Fatalf("unmaterialized: %v", err)
	}
	coordDir := lanes.CoordMissionDir(root, slug, mid8)
	if d, err := ReadDirOwned(o, slug, Coord, true); err != nil || d != coordDir {
		t.Fatalf("tolerated: %q %v", d, err)
	}
	wt := lanes.CoordWorktreePath(root, slug, mid8)
	run(t, root, "worktree", "add", "-q", wt, branch)
	if _, err := ReadDirOwned(o, slug, Coord, false); err == nil || !strings.Contains(err.Error(), "(state=empty)") {
		t.Fatalf("empty: %v", err)
	}
	os.MkdirAll(coordDir, 0o755)
	if d, err := ReadDirOwned(o, slug, Coord, false); err != nil || d != coordDir {
		t.Fatalf("materialized: %q %v", d, err)
	}
	run(t, wt, "switch", "-q", "-c", "elsewhere")
	_, err = ReadDirOwned(o, slug, Coord, false)
	bm, ok := err.(*BranchMismatchError)
	if !ok || bm.Error() != "Coordination worktree at "+wt+" is on 'refs/heads/elsewhere', expected '"+branch+"'. Manual intervention required." {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := ReadDir(root, slug, Coord); err == nil {
		t.Fatal("non-owned read_dir accepted a worktree on the wrong branch")
	}
	run(t, wt, "switch", "-q", "--detach")
	if err := RequireCoordWorktreeBranch(wt, branch); err == nil || !strings.Contains(err.Error(), "'<detached or unreadable HEAD>'") {
		t.Fatalf("detached: %v", err)
	}
	// a non-coordination topology reads the owned mission dir
	if d, err := ReadDirOwned(Owned{RepositoryRoot: root, MissionDir: mdir, Topology: "single_branch"}, slug, Coord, false); err != nil || d != mdir {
		t.Fatalf("single_branch: %q %v", d, err)
	}
}
