package invocation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/eventlog"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repoWithWorktree builds R (main, one commit) and a linked worktree P.
func repoWithWorktree(t *testing.T) (r, p string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	r, p = filepath.Join(base, "r"), filepath.Join(base, "p")
	os.MkdirAll(r, 0o755)
	gitT(t, r, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(r, "a.txt"), []byte("x\n"), 0o644)
	gitT(t, r, "add", "a.txt")
	gitT(t, r, "commit", "-qm", "init")
	gitT(t, r, "worktree", "add", "-q", "-b", "topic", p)
	return r, p
}

func TestEventLogLockPathAndTornTail(t *testing.T) {
	r, p := repoWithWorktree(t)
	anchor := filepath.Join(p, EventsDir)
	got, err := eventlog.LockPath(anchor)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(anchor))
	want := filepath.Join(r, ".git", "spec-kitty-locks", "event-logs", hex.EncodeToString(sum[:])[:32]+".lock")
	if got != want {
		t.Fatalf("lock path %s, want %s", got, want)
	}
	os.MkdirAll(anchor, 0o755)
	log := filepath.Join(anchor, IndexFile)
	if err := appendNoFollow(log, `{"a": 1}`); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(log); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if st, err := os.Stat(filepath.Dir(want)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("lock dir: %v %v", st, err)
	}
	os.WriteFile(log, []byte(`{"a": 1}`+"\n"+`{"b"`), 0o600)
	err = appendNoFollow(log, `{"c": 3}`)
	if !eventlog.IsCode(err, eventlog.CodeTornTail) || !strings.HasSuffix(err.Error(), " Error code: EVENT_LOG_TORN_TAIL.") {
		t.Fatalf("torn tail: %v", err)
	}
	if b, _ := os.ReadFile(log); string(b) != `{"a": 1}`+"\n"+`{"b"` {
		t.Fatalf("torn log changed: %q", b)
	}
	os.Symlink(log, filepath.Join(anchor, "link.jsonl"))
	if err := appendNoFollow(filepath.Join(anchor, "link.jsonl"), "{}"); !eventlog.IsCode(err, eventlog.CodeSymlink) {
		t.Fatalf("symlink: %v", err)
	}
}

func TestResolveAndLocateRecordRoot(t *testing.T) {
	r, p := repoWithWorktree(t)
	if got := ResolveRecordRoot(r, filepath.Join(p)); got != p {
		t.Fatalf("resolve from P: %s", got)
	}
	if got := ResolveRecordRoot(r, r); got != r {
		t.Fatalf("resolve from R: %s", got)
	}
	other, _ := filepath.EvalSymlinks(t.TempDir())
	if got := ResolveRecordRoot(r, other); got != r {
		t.Fatalf("resolve outside: %s", got)
	}
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if got := LocateRecordRoot(r, id, r); got != "" {
		t.Fatalf("nothing held: %s", got)
	}
	os.MkdirAll(filepath.Join(p, EventsDir), 0o755)
	os.WriteFile(filepath.Join(p, EventsDir, id+".jsonl"), []byte("{}\n"), 0o600)
	if got := LocateRecordRoot(r, id, r); got != p {
		t.Fatalf("held in P: %s", got)
	}
	if got := LocateRecordRoot(r, "not-a-ulid", p); got != "" {
		t.Fatalf("invalid id: %s", got)
	}
}
