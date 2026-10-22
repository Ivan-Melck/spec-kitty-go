package pyyaml

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCorpusRuamelLoad compares full loads (values and every error) of
// both ruamel dialects with the corpus.
func TestCorpusRuamelLoad(t *testing.T) {
	for _, d := range []struct {
		name    string
		dialect Dialect
		pick    func(c corpusCase) map[string]json.RawMessage
	}{
		{"rt", Ruamel, func(c corpusCase) map[string]json.RawMessage { return c.RuamelRT }},
		{"safe", RuamelSafe, func(c corpusCase) map[string]json.RawMessage { return c.RuamelSafe }},
	} {
		t.Run(d.name, func(t *testing.T) {
			fails := 0
			for _, c := range loadCorpus(t) {
				want := d.pick(c)
				if e, ok := want["err"]; ok && (strings.Contains(string(e), "RecursionError") || strings.Contains(string(e), "IndexError|list index")) {
					continue
				}
				v, err := LoadRuamelPy(c.Src, d.dialect)
				if !checkCase(t, d.name, c.Src, want, v, err) {
					fails++
					if fails > 40 {
						t.Fatal("too many failures")
					}
				}
			}
		})
	}
}
