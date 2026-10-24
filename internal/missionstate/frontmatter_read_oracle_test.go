package missionstate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestDiffFrontmatterRootTypes(t *testing.T) {
	b, err := os.ReadFile(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	python := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "#!")
	s := testkit.NewSandbox(t)
	cases := []string{"", "null", "{}", "id: WP01", "[]", "[a]", "1", "1.5", ".inf", ".nan", "0xFF", "0xff", "0o12", "0b10", "1_000", "true", "false", "\"hello\"", "'hello'", "|\n  hello\n", ">\n  hello\n", "2026-01-01", "2026-01-01T12:00:00Z", "!custom hi", "!!set {a: null}", "!!binary aGk=", "&anchor hello", "&anchor 'hello'", "&anchor true", "&anchor 1", "!!omap [{a: 1}]", "id: 1\nid: 2", "[broken"}
	var paths []string
	var actual []string
	for i, content := range cases {
		name := fmt.Sprintf("case-%02d.md", i)
		s.WriteFile(name, "---\n"+content+"\n---\nBody\n")
		paths = append(paths, name)
		actual = append(actual, FrontmatterReadError(s.Dir+"/"+name))
	}
	s.WriteFile("cases.json", jsonx.Dumps(paths, jsonx.Line))
	oracle := s.Exec("", python, "-c", `import json
from pathlib import Path
from specify_cli.frontmatter import FrontmatterManager
result=[]
for name in json.loads(Path('cases.json').read_text()):
 try:
  FrontmatterManager().read(Path.cwd()/name)
  result.append('')
 except Exception as e:
  result.append(str(e))
print(json.dumps(result))`)
	if oracle.Code != 0 {
		t.Fatal(oracle.Stderr)
	}
	var expected []string
	if err := json.Unmarshal([]byte(oracle.Stdout), &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(actual) {
		t.Fatalf("oracle returned %d cases, want %d", len(expected), len(actual))
	}
	for i, got := range actual {
		if got != expected[i] {
			t.Errorf("%q: %s", cases[i], testkit.Diff(expected[i], got))
		}
	}
}
