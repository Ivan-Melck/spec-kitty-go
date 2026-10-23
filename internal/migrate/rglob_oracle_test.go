package migrate

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOraclePyRglobAll compares PyRglobAll with the oracle interpreter's
// Path.rglob("*") over the same tree (nested and symlinked directories,
// hidden entries, files created out of name order).
func TestOraclePyRglobAll(t *testing.T) {
	bin := os.Getenv("SPEC_KITTY_ORACLE")
	if bin == "" {
		t.Skip("SPEC_KITTY_ORACLE not set")
	}
	f, err := os.Open(bin)
	if err != nil {
		t.Skip(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	py := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	root := t.TempDir()
	for _, rel := range []string{"z/notes.md", "a/b/c/notes.md", "spec.md", "m/notes.md", "a/x.md", ".hidden/notes.md",
		"tasks/WP02.md", "tasks/WP01.md", "a/b/y.md", "z/q/r/notes.md", "b/notes.md"} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x\n"), 0o644)
	}
	os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link"))
	out, err := exec.Command(py, "-I", "-c", "import sys\nfrom pathlib import Path\nfor p in Path(sys.argv[1]).rglob('*'):\n    print(p)", root).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(out))
	got := strings.Join(PyRglobAll(root), "\n")
	if got != want {
		t.Errorf("rglob order differs\n--- oracle:\n%s\n--- go:\n%s", want, got)
	}
}
