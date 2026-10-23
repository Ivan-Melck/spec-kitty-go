package pyyaml

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// TestOracleRecursiveAliases compares documents whose aliases point back
// into the node being constructed: PyYAML and ruamel's safe loader build
// cyclic objects; ruamel's round-trip loader constructs mapping values
// deep, so such an alias yields None there. Values are compared through
// repr() (cycles render as [...] / {...}).
func TestOracleRecursiveAliases(t *testing.T) {
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

	docs := []string{
		"a: &x [x, *x]\n",
		"&m {k: *m}\n",
		"- &s [1, *s]\n",
		"a: &m {k: [*m]}\nb: *m\n",
		"&r [*r]\n",
		"a: &x {b: *x, c: [1, *x]}\nd: *x\n",
		"top: &t\n  child: &c\n    up: *t\n    self: *c\n",
	}
	type mode struct {
		name, py string
		load     func(string) (any, error)
	}
	modes := []mode{
		{"pyyaml", "yaml.safe_load(src)", func(s string) (any, error) { return Load(s) }},
		{"ruamel-rt", "ruamel.yaml.YAML().load(src)", func(s string) (any, error) { return LoadRuamelPy(s, Ruamel) }},
		{"ruamel-safe", "ruamel.yaml.YAML(typ='safe', pure=True).load(src)", func(s string) (any, error) { return LoadRuamelPy(s, RuamelSafe) }},
	}
	var script strings.Builder
	script.WriteString(`import json, warnings
warnings.simplefilter('ignore')
import yaml, ruamel.yaml
def plain(o, memo):
    if id(o) in memo:
        return memo[id(o)]
    if isinstance(o, dict):
        d = {}
        memo[id(o)] = d
        for k, v in o.items():
            d[str(k)] = plain(v, memo)
        return d
    if isinstance(o, list):
        l = []
        memo[id(o)] = l
        l.extend(plain(v, memo) for v in o)
        return l
    return o
out = {}
`)
	for i, d := range docs {
		for _, m := range modes {
			fmt.Fprintf(&script, "src = %q\ntry:\n    out[%q] = repr(plain(%s, {}))\nexcept Exception as e:\n    out[%q] = type(e).__name__ + ': ' + str(e)\n",
				d, fmt.Sprint(i, "/", m.name), m.py, fmt.Sprint(i, "/", m.name))
		}
	}
	script.WriteString("print(json.dumps(out))\n")
	out, err := exec.Command(py, "-I", "-c", script.String()).Output()
	if err != nil {
		t.Fatalf("oracle python: %v\n%s", err, out)
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for i, d := range docs {
		for _, m := range modes {
			v, err := m.load(d)
			got := ""
			if err != nil {
				got = ClassName(err) + ": " + err.Error()
			} else {
				got = pyjson.Repr(v)
			}
			if w := want[fmt.Sprint(i, "/", m.name)]; got != w {
				t.Errorf("%q / %s:\n  oracle: %s\n  go:     %s", d, m.name, w, got)
			}
		}
	}
}
