package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestPyPathStr(t *testing.T) {
	for in, want := range map[string]string{"./a.py": "a.py", "a//b/": "a/b", "": ".", "/x/./y": "/x/y", "../a": "../a", ".": "."} {
		if got := PyPathStr(in); got != want {
			t.Errorf("PyPathStr(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"a.py": ".py", ".py": "", "a.tar.py": ".py", "a.py.": "", "dir.py/x": ""} {
		if got := pySuffix(in); got != want {
			t.Errorf("pySuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

const fakeRuff = `#!/bin/sh
case "$*" in
  *clean.py*) exit 0 ;;
  *noout.py*) exit 1 ;;
esac
echo "x.py:1:1: F401 [*] ` + "`os`" + ` imported but unused"
echo "Found 1 error."
echo "[*] 1 fixable with the --fix option."
exit 1
`

const fakeMypy = `#!/bin/sh
case "$*" in
  *clean.py*) echo "Success: no issues found in 1 source file"; exit 0 ;;
  *noout.py*) echo "Success: x" ; exit 1 ;;
esac
echo "x.py:3: error: Function is missing a return type annotation  [no-untyped-def]" >&2
echo "Found 1 error in 1 file (checked 1 source file)" >&2
exit 1
`

func fixture(withTools bool) func(s *testkit.Sandbox) {
	return func(s *testkit.Sandbox) {
		s.WriteFile("pkg/bad.py", "import os\n")
		s.WriteFile("pkg/clean.py", "x = 1\n")
		s.WriteFile("pkg/noout.py", "x = 1\n")
		s.WriteFile("notes.md", "# n\n")
		bin := filepath.Join(s.Base, "bin")
		os.MkdirAll(bin, 0o755)
		if withTools {
			os.WriteFile(filepath.Join(bin, "ruff"), []byte(fakeRuff), 0o755)
			os.WriteFile(filepath.Join(bin, "mypy"), []byte(fakeMypy), 0o755)
		}
		// Without the tools PATH keeps only system dirs (no ruff/mypy there).
		s.Env = append(s.Env, "PATH="+bin+":/usr/bin:/bin")
	}
}

func TestLintParity(t *testing.T) {
	testkit.Oracle(t)
	for _, tools := range []bool{true, false} {
		tools := tools
		name := "with-tools"
		if !tools {
			name = "without-tools"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := testkit.NewPair(t)
			p.Both(fixture(tools))
			for _, args := range [][]string{
				{"lint", "pkg/bad.py"},
				{"lint", "./pkg//bad.py", "--json"},
				{"lint", "pkg/clean.py"},
				{"lint", "pkg/clean.py", "--json", "--fix"},
				{"lint", "pkg/noout.py", "--json"},
				{"lint", "notes.md"},
				{"lint", "notes.md", "--json"},
				{"lint", "missing.py"},
				{"lint", "missing.py", "--json"},
			} {
				p.Compare(true, args...)
			}
		})
	}
}

func TestLintStdinPayload(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	fixture(true)(s)
	for payload, want := range map[string]string{
		"":         "Skipping: no file path provided.",
		"not json": "Skipping: no file path provided.",
		`{"tool_input": {"file_path": "notes.md"}}`: "is not a Python file",
		`{"file_paths": ["", "pkg/clean.py"]}`:      "passed all checks",
	} {
		r := testkit.StripANSI(runWithStdin(t, s, payload, "lint"))
		if !strings.Contains(r, want) {
			t.Errorf("payload %q: got %q, want %q", payload, r, want)
		}
	}
}

func runWithStdin(t *testing.T, s *testkit.Sandbox, stdin string, args ...string) string {
	t.Helper()
	f := filepath.Join(s.Base, "stdin.txt")
	os.WriteFile(f, []byte(stdin), 0o644)
	r := s.Exec("", "/bin/sh", "-c", `"$0" "$@" < "`+f+`"`, testkit.Binary(t), args[0])
	return r.Stdout
}
