package pyyaml

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// TestOracleStreamReaderChunks compares stream loads with the oracle's
// PyYAML / ruamel.yaml on files larger than the reader's first chunks: a
// special character or an undecodable byte is reported only when the
// chunked reader reaches it (after an earlier scanner error), binary
// positions are absolute, and a text stream's UnicodeDecodeError is
// positioned within TextIOWrapper's 8192-byte chunk.
func TestOracleStreamReaderChunks(t *testing.T) {
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

	body := func(n int) string {
		var b strings.Builder
		for i := 0; b.Len() < n; i++ {
			fmt.Fprintf(&b, "k%d: v%d\n", i, i)
		}
		return b.String()
	}
	pad := func(s string, at int) string { // s padded with comment lines to byte offset at
		for len(s)+63 < at-1 {
			s += "# " + strings.Repeat("x", 60) + "\n"
		}
		return s + "#" + strings.Repeat("y", at-len(s)-2) + "\n"
	}
	cases := map[string][]byte{
		"syntax-then-late-char":   []byte(pad("a: b: c\n", 10000) + "z: \x07\n"),
		"syntax-then-early-char":  []byte(pad("a: b: c\n", 3000) + "z: \x07\n"),
		"late-char":               []byte(pad(body(100), 10000) + "z: \x07\n"),
		"syntax-then-late-byte":   []byte(pad("a: b: c\n", 20000) + "z: \xff\n"),
		"late-byte":               []byte(pad(body(100), 20000) + "z: \xff\n"),
		"late-continuation":       []byte(pad(body(100), 20000) + "z: \xe2\x41\n"),
		"truncated-at-eof":        []byte(pad(body(100), 9000) + "z: \xe2\x82"),
		"multibyte-on-boundaries": []byte(strings.Repeat("é", 2047) + ": 1\n" + body(9000) + strings.Repeat("ü", 5000) + ": 2\n"),
		"crlf-across-chunk":       []byte(strings.Repeat("#", 8191) + "\r\n" + "a: 1\r\nb: [\r\n"),
		"late-error-lines":        []byte(strings.ReplaceAll(body(30000), "\n", "\r\n") + "c: d: e\r\n"),
	}
	dir := t.TempDir()
	var names []string
	for name, data := range cases {
		os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0o644)
		names = append(names, name)
	}
	modes := []struct {
		name, py string
		goLoad   func(data []byte, path string) error
	}{
		{"pyyaml-bin", "yaml.safe_load(open(p, 'rb'))", func(b []byte, p string) error { _, err := LoadInput(FileInput(b, p)); return err }},
		{"ruamel-rt-text", "ruamel.yaml.YAML().load(open(p, encoding='utf-8'))", func(b []byte, p string) error {
			in, _ := TextFileInput(b, p)
			_, err := LoadRuamelInput(in, Ruamel)
			return err
		}},
		{"ruamel-safe-bin", "ruamel.yaml.YAML(typ='safe', pure=True).load(open(p, 'rb'))", func(b []byte, p string) error {
			_, err := LoadRuamelInput(FileInput(b, p), RuamelSafe)
			return err
		}},
	}
	var script strings.Builder
	script.WriteString("import json, sys, warnings\nwarnings.simplefilter('ignore')\nimport yaml, ruamel.yaml\nout = {}\n")
	for _, n := range names {
		for _, m := range modes {
			fmt.Fprintf(&script, "p = %q\ntry:\n    %s\n    out[%q] = 'OK'\nexcept Exception as e:\n    out[%q] = type(e).__name__ + ': ' + str(e)\n",
				filepath.Join(dir, n+".yaml"), m.py, n+"/"+m.name, n+"/"+m.name)
		}
	}
	script.WriteString("print(json.dumps(out))\n")
	cmd := exec.Command(py, "-I", "-c", script.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle python: %v\n%s", err, out)
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		for k, v := range want {
			t.Logf("%s => %s", k, v)
		}
	}
	for _, n := range names {
		for _, m := range modes {
			err := m.goLoad(cases[n], filepath.Join(dir, n+".yaml"))
			got := "OK"
			if err != nil {
				cls := ClassName(err)
				if _, ok := err.(*pyjson.UnicodeError); ok {
					cls = "UnicodeDecodeError"
				}
				got = cls + ": " + err.Error()
			}
			if w := want[n+"/"+m.name]; got != w {
				t.Errorf("%s/%s:\n  oracle: %q\n  go:     %q", n, m.name, w, got)
			}
		}
	}
}
