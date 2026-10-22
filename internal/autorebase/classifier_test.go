package autorebase

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type resolveCase struct {
	File string `json:"file"`
	Body string `json:"body"`
}

type resolveResult struct {
	Rebuilt string   `json:"rebuilt"`
	Rules   []string `json:"rules"`
	Halt    string   `json:"halt"`
}

func region(ours, theirs string) string {
	return "<<<<<<< HEAD\n" + ours + "=======\n" + theirs + ">>>>>>> kitty/mission-x\n"
}

var resolveCases = []resolveCase{
	{"/w/pyproject.toml", "[project]\nname = \"x\"\ndependencies = [\n" + region("    \"click>=8\",\n    \"rich\",\n", "    \"click>=8\",\n    \"httpx>=0.27\",\n") + "]\n"},
	{"/w/pyproject.toml", "[project]\ndependencies = [\n" + region("    \"rich\",\n    \"click\",\n", "    \"httpx\",\n") + "]\n"},
	{"/w/pyproject.toml", "[project]\ndependencies = [\n" + region("    \"rich>=13\",\n", "    \"rich>=14\",\n") + "]\n"},
	{"/w/pyproject.toml", "[project]\n" + region("version = \"1\"\n", "version = \"2\"\n")},
	{"/w/pyproject.toml", region("\"a\"\n", "\"b\"\n")},
	{"/w/pyproject.toml", "[dependency-groups]\ndev = [\n" + region("  # tools\n  \"pytest\",\n", "  'ruff',\n") + "]\n"},
	{"/w/pkg/__init__.py", "\"\"\"pkg.\"\"\"\n" + region("from .a import A\nimport os\n", "from .b import B\n") + "\n__all__ = []\n"},
	{"/w/pkg/__init__.py", region("from .a import A\n", "from .a import A, C\n")},
	{"/w/pkg/__init__.py", region("import a,\n", "import b\n")},
	{"/w/pkg/__init__.py", region("x = 1\n", "y = 2\n")},
	{"/w/app/urls.py", "urlpatterns = [\n" + region("    \"a/\",\n    \"b/\",\n", "    'c/',\n") + "]\n"},
	{"/w/app/urls.py", "urlpatterns = [\n" + region("    \"a/\"\n", "    \"a/\",  # same\n") + "]\n"},
	{"/w/app/routes.py", "X = [\n" + region("URLS = [\n", "URLS = [\n") + "]\n"},
	{"/w/uv.lock", region("version = 1\n", "version = 2\n")},
	{"/w/src/calc.py", region("def f():\n    return 1\n", "def f():\n    return 2\n")},
	{"/w/pyproject.toml", "[project]\ndependencies = [\n<<<<<<< HEAD\n    \"a\",\n||||||| base\n=======\n    \"b\",\n>>>>>>> theirs\n]\n"},
	{"/w/pkg/__init__.py", region("from .a import A\n", "from .b import B\n") + "\n" + region("from .c import C\n", "from .d import D\n")},
	{"/w/pkg/__init__.py", region("from .a import A\n", "from .b import B\n") + region("x = 1\n", "y = 2\n")},
	{"/w/README.md", "no markers here\n"},
	{"/w/pyproject.toml", "[project]\ndependencies = [\n<<<<<<< HEAD\r\n    \"a\",\r\n=======\r\n    \"b\",\r\n>>>>>>> t\r\n]\n"},
}

// TestResolveTextAgainstPython runs every case through the upstream
// classifier (SPEC_KITTY_PYTHON = the oracle venv's interpreter).
func TestResolveTextAgainstPython(t *testing.T) {
	py := os.Getenv("SPEC_KITTY_PYTHON")
	if py == "" {
		t.Skip("set SPEC_KITTY_PYTHON")
	}
	in, _ := json.Marshal(resolveCases)
	script, _ := filepath.Abs("testdata/resolve.py")
	cmd := exec.Command(py, script)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	var want []resolveResult
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, c := range resolveCases {
		rebuilt, cls, halt := ResolveText(c.File, c.Body)
		got := resolveResult{Rebuilt: rebuilt, Halt: halt, Rules: []string{}}
		for _, x := range cls {
			if x.Res.Auto {
				got.Rules = append(got.Rules, x.Res.RuleID)
			} else {
				got.Rules = append(got.Rules, "MANUAL")
			}
		}
		w := want[i]
		if w.Rules == nil {
			w.Rules = []string{}
		}
		const vf = "post-merge validation failed for "
		if strings.HasPrefix(w.Halt, vf) && strings.HasPrefix(got.Halt, vf) {
			// the exception repr differs; the verdict and path must not
			w.Halt = strings.SplitN(w.Halt, ": ", 2)[0]
			got.Halt = strings.SplitN(got.Halt, ": ", 2)[0]
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(w)
		if string(gj) != string(wj) {
			t.Errorf("case %d (%s):\n go:     %s\n python: %s", i, c.File, gj, wj)
		}
	}
}

var tomlCases = []string{
	"", "a = 1\n", "a = 1\na = 2\n", "[t]\nx = 1\n[t]\ny = 2\n", "[a.b]\n[a]\nx = 1\n", "[a]\nb.c = 1\n[a.b]\n",
	"a = [1, 2,\n  3, # c\n]\n", "a = {x = 1, y = \"s\"}\n", "a = {x = 1,}\n", "a = {x = 1}\n[a]\n", "s = \"\"\"\nmulti\\\n  line\"\"\"\n",
	"s = '''lit\n'''\n", "d = 1979-05-27T07:32:00Z\n", "d = 1979-05-27\nt = 07:32:00\n", "d = 1979-13-01\n", "n = 0x_1\n",
	"n = 1_000\nf = 6.626e-34\ni = inf\nm = -nan\n", "n = 007\n", "x = \"a\\qb\"\n", "x = \"\\u00e9\"\n", "[[p]]\nn = 1\n[[p]]\nn = 2\n",
	"p = []\n[[p]]\n", "\"quoted key\" = 1\n'lit' = 2\na.\"b\".c = 3\n", "a = 1 b = 2\n", "a =\n", "[project]\ndependencies = [\n    \"a\",\n    \"b\",\n]\n",
	"\"a\",\n", "x = \"unterminated\n", "x = true\ny = false\n", "x = True\n", "a = [ [1, 2], [\"a\"] ]\n", "# only comment\n\n",
	"[ spaced . table ]\nk = 1\n", "x = 1 # trailing\r\ny = 2\r\n", "x = 1.\n", "x = .5\n", "x = +1\ny = -0.0\n",
}

// TestValidateTOMLAgainstPython compares ValidateTOML's verdicts with
// tomllib (SPEC_KITTY_PYTHON).
func TestValidateTOMLAgainstPython(t *testing.T) {
	py := os.Getenv("SPEC_KITTY_PYTHON")
	if py == "" {
		t.Skip("set SPEC_KITTY_PYTHON")
	}
	in, _ := json.Marshal(tomlCases)
	script, _ := filepath.Abs("testdata/toml_check.py")
	cmd := exec.Command(py, script)
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []bool
	json.Unmarshal(out, &want)
	for i, doc := range tomlCases {
		err := ValidateTOML(doc)
		if (err == nil) != want[i] {
			t.Errorf("case %d %q: go err=%v, tomllib ok=%v", i, doc, err, want[i])
		}
	}
}

// TestClassifyGoOnly pins a few verdicts without the interpreter.
func TestClassifyGoOnly(t *testing.T) {
	rebuilt, cls, halt := ResolveText("/w/pkg/__init__.py", region("from .a import A\n", "from .b import B\n"))
	if halt != "" || len(cls) != 1 || cls[0].Res.RuleID != RuleInitImports || rebuilt != "from .a import A\nfrom .b import B\n" {
		t.Fatalf("init union: %q %v %q", rebuilt, cls, halt)
	}
	if _, _, halt := ResolveText("/w/src/x.py", region("a\n", "b\n")); halt != "no classifier rule matched /w/src/x.py" {
		t.Fatalf("default manual: %q", halt)
	}
	if ValidateTOML("a = 1\na = 2\n") == nil || ValidateTOML("[p]\nx = [1, 2]\n") != nil {
		t.Fatal("toml verdicts")
	}
}
