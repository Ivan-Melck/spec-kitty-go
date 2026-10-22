package dossier

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var wpPydanticCases = []string{
	"work_package_id: WP01\ntitle: A\n",
	"work_package_id: WP1\nlane: sideways\nbogus: 1\n",
	"title: '   '\ndependencies: 5\nshell_pid: abc\n",
	"work_package_id: WP01\ndependencies: [WP02, 3, 1.5]\nowned_files: x\n",
	"work_package_id: WP01\nwork_package_title: ''\n",
	"work_package_id: 7\ntitle: 5\nbase_commit: xyz\n",
	"work_package_id: WP01\ncreated_at: 2025-01-01T00:00:00Z\nbase_branch: 2025-02-03\n",
	"work_package_id: WP01\nlane: genesis\n",
	"work_package_id: WP01\nlane: 5\n",
	"work_package_id: WP01\nlane: doing\nphase: 3\nshell_pid: ' 42 '\n",
	"work_package_id: WP01\nshell_pid: '4.0'\n",
	"work_package_id: WP01\nshell_pid: [1]\n",
	"work_package_id: WP01\nsubtasks: {a: 1}\nhistory: x\ntags: [a, {b: 1}]\n",
	"work_package_id: WP01\nreviewer_shell_pid: 42\nmission_number: 3\n",
	"work_package_id: WP01\ndescription: |\n  block\nextra_a: >\n  folded\nextra_b: 1.5\nextra_c: 0x1f\n",
	"work_package_id: WP01\nrequirement_refs: FR-001 FR-002\ntracker_refs: 'A, B'\n",
	"work_package_id: WP01\nrequirement_refs: [1, FR-2]\ntracker_refs: 7\n",
	"title: x\nwork_package_title: y\nsome: 2025-01-01 10:20:30\nother: 2025-01-01T10:20:30.5+02:00\n",
	"agent: {tool: claude}\nreviewer: [a]\nfoo: [1, {b: c}]\n",
	"work_package_id: \"WP01\\n\"\n",
	"work_package_id: WP01\nshell_pid: 2025-01-01\n",
	"work_package_id: WP01\nshell_pid: {a: 1}\ntitle: ''\n",
	"",
	"work_package_id: WP01\ntitle: null\nlane: ''\nshell_pid: ''\nphase: null\n",
}

// TestWPMetadataValidationAgainstPython compares the pydantic error text
// with read_authored_wp_frontmatter in the oracle venv, when present.
func TestWPMetadataValidationAgainstPython(t *testing.T) {
	py := filepath.Join(os.Getenv("HOME"), ".cache/spec-kitty-parity/oracle-venv/bin/python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("oracle venv not available")
	}
	dir := t.TempDir()
	var paths []string
	for i, c := range wpPydanticCases {
		p := filepath.Join(dir, "WP"+string(rune('A'+i))+".md")
		os.WriteFile(p, []byte("---\n"+c+"---\nbody\n"), 0o644)
		paths = append(paths, p)
	}
	script := `
import json, sys
from pydantic import ValidationError
from specify_cli.status.wp_metadata import read_authored_wp_frontmatter
out = []
for p in sys.argv[1:]:
    try:
        read_authored_wp_frontmatter(__import__("pathlib").Path(p))
        out.append("")
    except ValidationError as e:
        out.append(str(e))
    except Exception as e:
        out.append("EXC " + type(e).__name__ + ": " + str(e))
print(json.dumps(out))
`
	cmd := exec.Command(py, append([]string{"-I", "-c", script}, paths...)...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("python: %v", err)
	}
	var want []string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for i, p := range paths {
		src, meta, ok := readFrontmatter(p)
		if !ok {
			t.Fatalf("case %d: unparsed", i)
		}
		got := WPMetadataValidationError(src, meta)
		if te := WPMetadataTypeError(src, meta); te != "" {
			got = "EXC TypeError: " + te
		}
		if got != want[i] {
			t.Errorf("case %d %q:\n got: %s\nwant: %s", i, wpPydanticCases[i], strings.ReplaceAll(got, "\n", "\n      "), strings.ReplaceAll(want[i], "\n", "\n      "))
		}
		if _, err := StaticProjectionHash(src, meta); (err == nil) != (want[i] == "") && !strings.HasPrefix(want[i], "EXC") {
			t.Errorf("case %d: StaticProjectionHash err=%v, python valid=%v", i, err, want[i] == "")
		}
	}
}
