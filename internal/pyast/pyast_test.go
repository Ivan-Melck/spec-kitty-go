package pyast

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestParseBasics(t *testing.T) {
	src := `import os, pkg.sub as s
from .mod import (a as b, c)

__all__ = ["x", 'y']

@decorator(arg="v")
def foo(a, b=1, *args, c: int = 2, **kw) -> "R":
    """Doc string."""
    s = f"hello {name!r:>{width}} {{lit}}" "tail"
    assert foo(a) == "x\ny", "msg"
    self.assertEqual(bar.baz, 'it''s')
    with open(p) as fh, ctx():
        assert "needle" not in str(exc)
    return [i for i in range(3) if i]

class K(Base, metaclass=M):
    async def run(self):
        await x
`
	mod, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	defs := Defs(mod)
	want := []Def{{"foo", 7}, {"K", 16}, {"run", 17}}
	if !reflect.DeepEqual(defs, want) {
		t.Errorf("defs = %v", defs)
	}
	var asserts []string
	Walk(mod, func(n *Node) {
		if IsAssertionBearing(n) {
			asserts = append(asserts, fmt.Sprintf("%s:%d:%v:%v", n.Kind, n.Line, SortedKeys(NamesIn(n)), SortedKeys(StrValuesIn(n))))
		}
	})
	wantA := []string{
		"Assert:10:[a foo]:[msg x\ny]",
		"Call:11:[assertEqual bar baz self]:[its]",
		"Assert:13:[exc str]:[needle]",
	}
	if !reflect.DeepEqual(asserts, wantA) {
		t.Errorf("asserts =\n%v", asserts)
	}
	found := map[string]bool{}
	for _, c := range StrConstants(mod) {
		found[c.Value] = true
	}
	for _, v := range []string{"hello ", " {lit}tail", "Doc string.", ">"} {
		if !found[v] {
			t.Errorf("missing constant %q in %v", v, found)
		}
	}
}

// TestAgainstCPython compares the parser with CPython's ast on real Python
// files (SPEC_KITTY_PYTHON = interpreter, SPEC_KITTY_PY_CORPUS = directory).
func TestAgainstCPython(t *testing.T) {
	py, corpus := os.Getenv("SPEC_KITTY_PYTHON"), os.Getenv("SPEC_KITTY_PY_CORPUS")
	if py == "" || corpus == "" {
		t.Skip("set SPEC_KITTY_PYTHON and SPEC_KITTY_PY_CORPUS")
	}
	var files []string
	filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(p) == ".py" {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	dump, _ := filepath.Abs("testdata/dump.py")
	type fileFacts struct { //nolint
		Defs    [][]any  `json:"defs"`
		Strs    [][]any  `json:"strs"`
		Asserts [][]any  `json:"asserts"`
		Exports []string `json:"exports"`
	}
	failures := 0
	for start := 0; start < len(files); start += 200 {
		end := start + 200
		if end > len(files) {
			end = len(files)
		}
		out, err := exec.Command(py, append([]string{"-I", dump}, files[start:end]...)...).Output()
		if err != nil {
			t.Fatal(err)
		}
		var ref map[string]*fileFacts
		if err := json.Unmarshal(out, &ref); err != nil {
			t.Fatal(err)
		}
		for _, f := range files[start:end] {
			want := ref[f]
			b, _ := os.ReadFile(f)
			mod, err := Parse(string(b))
			if want == nil {
				continue
			}
			if err != nil {
				failures++
				t.Errorf("%s: parse failed: %v", f, err)
				continue
			}
			got := &fileFacts{Defs: [][]any{}, Strs: [][]any{}, Asserts: [][]any{}, Exports: []string{}}
			for _, d := range Defs(mod) {
				got.Defs = append(got.Defs, []any{d.Name, float64(d.Line)})
			}
			seen := map[[2]any]bool{}
			for _, c := range StrConstants(mod) {
				k := [2]any{c.Value, float64(c.Line)}
				if !seen[k] {
					seen[k] = true
					got.Strs = append(got.Strs, []any{c.Value, float64(c.Line)})
				}
			}
			sort.Slice(got.Strs, func(i, j int) bool {
				a, b := got.Strs[i], got.Strs[j]
				if a[0].(string) != b[0].(string) {
					return a[0].(string) < b[0].(string)
				}
				return a[1].(float64) < b[1].(float64)
			})
			Walk(mod, func(n *Node) {
				if IsAssertionBearing(n) {
					names, consts := []any{}, []any{}
					for _, x := range SortedKeys(NamesIn(n)) {
						names = append(names, x)
					}
					for _, x := range SortedKeys(StrValuesIn(n)) {
						consts = append(consts, x)
					}
					got.Asserts = append(got.Asserts, []any{n.Kind, float64(n.Line), names, consts})
				}
			})
			for _, s := range mod.Body {
				switch s.Kind {
				case "Import":
					for _, a := range s.Aliases {
						bound := a.AsName
						if bound == "" {
							bound = firstPart(a.Name)
						}
						got.Exports = append(got.Exports, "import:"+bound)
					}
				case "ImportFrom":
					for _, a := range s.Aliases {
						got.Exports = append(got.Exports, "from:"+a.Name+":"+a.AsName)
					}
				case "Assign":
					for _, tg := range s.Targets {
						if tg.Kind == "Name" && tg.Name == "__all__" && s.Value != nil && (s.Value.Kind == "List" || s.Value.Kind == "Tuple" || s.Value.Kind == "Set") {
							for _, e := range s.Value.Elts {
								if e.Kind == "Constant" && e.IsStr {
									got.Exports = append(got.Exports, "all:"+e.Str)
								}
							}
						}
					}
				}
			}
			norm := func(v any) string { b, _ := json.Marshal(v); return string(b) }
			if norm(got.Defs) != norm(want.Defs) {
				failures++
				t.Errorf("%s: defs differ", f)
			} else if norm(got.Strs) != norm(want.Strs) {
				failures++
				t.Errorf("%s: strs differ\n got %.600s\nwant %.600s", f, diffFirst(got.Strs, want.Strs), diffFirst(want.Strs, got.Strs))
			} else if norm(got.Asserts) != norm(want.Asserts) {
				failures++
				t.Errorf("%s: asserts differ\n got %.800s\nwant %.800s", f, diffFirst(got.Asserts, want.Asserts), diffFirst(want.Asserts, got.Asserts))
			} else if norm(got.Exports) != norm(want.Exports) {
				failures++
				t.Errorf("%s: exports differ\n got %v\nwant %v", f, got.Exports, want.Exports)
			}
			if failures > 30 {
				t.Fatal("too many failures")
			}
		}
	}
	t.Logf("compared %d files", len(files))
}

func firstPart(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i]
		}
	}
	return s
}

func diffFirst(a, b [][]any) string {
	set := map[string]bool{}
	for _, x := range b {
		j, _ := json.Marshal(x)
		set[string(j)] = true
	}
	for _, x := range a {
		j, _ := json.Marshal(x)
		if !set[string(j)] {
			return string(j)
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("<len %d vs %d>", len(a), len(b))
	}
	return "<order differs>"
}
