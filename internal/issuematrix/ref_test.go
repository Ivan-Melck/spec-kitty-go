package issuematrix

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReadRefContentCodes pins the typed fail-closed legs of the
// coordination-ref read, including #6653's ISSUE_MATRIX_NOT_AUTHORED.
func TestReadRefContentCodes(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("kitty-specs/m/spec.md", "# m\n")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")
	code := func(ref string) string {
		_, err := ReadRefContent(root, "m", ref)
		if e, ok := err.(*RefReadError); ok {
			return e.Code
		}
		if err != nil {
			return err.Error()
		}
		return ""
	}
	if c := code("main"); c != NotAuthored {
		t.Fatalf("no matrix at ref: %q", c)
	}
	if c := code("gone"); c != RefAbsent {
		t.Fatalf("deleted ref: %q", c)
	}
	write("kitty-specs/m/issue-matrix.md", "  \n")
	git("add", "-A")
	git("commit", "-q", "-m", "empty md")
	if c := code("main"); c != EmptyContent {
		t.Fatalf("empty authored matrix: %q", c)
	}
	write("kitty-specs/m/issue-matrix.json", `{"rows": {}}`)
	git("add", "-A")
	git("commit", "-q", "-m", "json")
	if got, err := ReadRefContent(root, "m", "main"); err != nil || got != `{"rows": {}}` {
		t.Fatalf("json content: %q %v", got, err)
	}
}
