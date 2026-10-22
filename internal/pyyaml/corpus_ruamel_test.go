package pyyaml

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCorpusRuamelSyntax checks the scanner/parser/composer errors of both
// ruamel dialects (constructor errors are covered by TestCorpusRuamelLoad).
func TestCorpusRuamelSyntax(t *testing.T) {
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
				var w string
				if raw, ok := d.pick(c)["err"]; ok {
					json.Unmarshal(raw, &w)
				}
				cls, _, _ := strings.Cut(w, "|")
				_, err := Compose(c.Src, ComposeOptions{Dialect: d.dialect, Resolve: resolve11})
				switch cls {
				case "ScannerError", "ParserError", "ComposerError", "ReaderError":
					if err == nil || errString(err) != w {
						t.Errorf("%q:\nwant: %q\ngot:  %v", c.Src, w, err)
						fails++
					}
				default:
					if err != nil && !(cls != "" && errString(err) == w) {
						t.Errorf("%q: want no syntax error (%q), got %q", c.Src, w, errString(err))
						fails++
					}
				}
				if fails > 30 {
					t.Fatal("too many failures")
				}
			}
		})
	}
}
