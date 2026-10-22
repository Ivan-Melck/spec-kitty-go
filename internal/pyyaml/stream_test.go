package pyyaml

import "testing"

// Expected texts captured from PyYAML 6.0.3 yaml.safe_load(open(f, "rb"))
// and ruamel.yaml 0.19.1 YAML().load(Path(f)) / YAML(typ="safe").
func TestStreamInputErrors(t *testing.T) {
	cases := []struct {
		data, py, ruamel string
	}{
		{"a: b: c\n", "ScannerError|mapping values are not allowed here\n  in \"s.yaml\", line 1, column 5", "same"},
		{"a: 1\na: 2\n", "", "DuplicateKeyError|while constructing a mapping\n  in \"s.yaml\", line 1, column 1\nfound duplicate key \"a\" with value \"2\" (original value: \"1\")\n  in \"s.yaml\", line 2, column 1\n\nTo suppress this check see:\n    https://yaml.dev/doc/ruamel.yaml/api/#Duplicate_keys\n"},
		{"a: \xff\n", "ReaderError|unacceptable character #x00ff: invalid start byte\n  in \"s.yaml\", position 3", "same"},
		{"a: \x01\n", "ReaderError|unacceptable character #x0001: special characters are not allowed\n  in \"s.yaml\", position 3", "same"},
		{"\xef\xbb\xbfa: [1\n", "ParserError|while parsing a flow sequence\n  in \"s.yaml\", line 1, column 4\nexpected ',' or ']', but got '<stream end>'\n  in \"s.yaml\", line 2, column 1", "same"},
		{"k: \"\xc3\n", "ReaderError|unacceptable character #x00c3: invalid continuation byte\n  in \"s.yaml\", position 4", "same"},
	}
	for _, c := range cases {
		in := FileInput([]byte(c.data), "s.yaml")
		_, err := LoadInput(in)
		got := ""
		if err != nil {
			got = errString(err)
		}
		if got != c.py {
			t.Errorf("pyyaml %q:\nwant %q\ngot  %q", c.data, c.py, got)
		}
		want := c.ruamel
		if want == "same" {
			want = c.py
		}
		for _, d := range []Dialect{Ruamel, RuamelSafe} {
			_, err := LoadRuamelInput(in, d)
			got := ""
			if err != nil {
				got = errString(err)
			}
			if got != want {
				t.Errorf("ruamel %d %q:\nwant %q\ngot  %q", d, c.data, want, got)
			}
		}
	}
}
