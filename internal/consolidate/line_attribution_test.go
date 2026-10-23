package consolidate

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestLineAttributionMatchesOracle(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	for _, scenario := range []string{"partial", "overwrite", "delete-stays", "delete-restored", "delete-reworked", "rename", "indent", "indent-undone", "punctuation", "empty-create", "mode", "binary"} {
		t.Run(scenario, func(t *testing.T) {
			s := testkit.NewSandbox(t)
			s.Git("init", "-q", "-b", "main")
			s.WriteFile("file.txt", "start anchor\nremoved value\nend anchor\n")
			s.Git("add", ".")
			s.Git("commit", "-qm", "base")
			content := "start anchor\nremoved value\ncanceled one\ncanceled two\nend anchor\n"
			switch scenario {
			case "delete-stays", "delete-restored", "delete-reworked":
				content = "start anchor\nend anchor\n"
			case "indent", "indent-undone":
				content = "    start anchor\nremoved value\nend anchor\n"
			case "punctuation":
				content = "start anchor\nremoved value\nend anchor\n}\n"
			case "empty-create":
				content = ""
			case "mode":
				content = s.ReadFile("file.txt")
			case "binary":
				content = "canceled\x00binary"
			}
			if scenario == "empty-create" {
				s.WriteFile("empty.txt", "")
			} else {
				s.WriteFile("file.txt", content)
			}
			if scenario == "mode" {
				os.Chmod(filepath.Join(s.Dir, "file.txt"), 0o755)
			}
			s.Git("add", ".")
			s.Git("commit", "-qm", "canceled")
			sha := s.Git("rev-parse", "HEAD")
			path := "file.txt"
			if scenario == "empty-create" {
				path = "empty.txt"
			}
			switch scenario {
			case "partial":
				s.WriteFile(path, "start anchor\nremoved value\ncanceled one\napproved replacement\nend anchor\n")
			case "overwrite":
				s.WriteFile(path, "approved first\napproved second\n")
			case "delete-stays":
				s.WriteFile(path, "start anchor\nend anchor\napproved tail\n")
			case "delete-restored":
				s.WriteFile(path, "start anchor\nremoved value\nend anchor\napproved tail\n")
			case "delete-reworked":
				s.WriteFile(path, "start anchor\napproved replacement\nend anchor\n")
			case "rename":
				s.Git("mv", path, "renamed.txt")
				s.WriteFile("renamed.txt", content+"approved tail\n")
			case "indent-undone":
				s.WriteFile(path, "start anchor\nremoved value\nend anchor\napproved tail\n")
			case "binary":
				s.WriteFile(path, "approved\x00binary")
			default:
				s.WriteFile(path, s.ReadFile(path)+"approved tail\n")
			}
			s.Git("add", ".")
			s.Git("commit", "-qm", "approved")
			got, e := newLineAttribution(s.Dir).survival([]string{sha}, path, "HEAD", sha, "HEAD", nil)
			var result any
			if e != nil {
				result = e.Error()
			} else {
				var p any
				if got.path != "" {
					p = got.path
				}
				result = jsonx.NewObject("path", p, "ref", got.ref, "added", got.added, "deletion", got.deletion)
			}
			script := "import json\nfrom pathlib import Path\nfrom specify_cli.consolidation.line_attribution import LineAttribution\ntry:\n s=LineAttribution(Path.cwd()).survival([" + jsonx.Dumps(sha, jsonx.Line) + "]," + jsonx.Dumps(path, jsonx.Line) + ",'HEAD',floor=" + jsonx.Dumps(sha, jsonx.Line) + ")\n r=dict(path=s.final_path,ref=s.final_ref,added=s.added,deletion=s.deletion)\nexcept Exception as e:r=str(e)\nprint(json.dumps(r,sort_keys=True,indent=2))"
			oracle := s.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatal(oracle.Stderr)
			}
			actual := jsonx.Dumps(result, jsonx.SortedPretty) + "\n"
			if actual != oracle.Stdout {
				t.Error(testkit.Diff(oracle.Stdout, actual))
			}
		})
	}
}
