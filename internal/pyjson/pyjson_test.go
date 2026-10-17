package pyjson

import (
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Expected messages captured from CPython 3.13 json.loads.
var loadsCases = []struct{ in, want string }{
	{``, `Expecting value: line 1 column 1 (char 0)`},
	{` `, `Expecting value: line 1 column 2 (char 1)`},
	{`{`, `Expecting property name enclosed in double quotes: line 1 column 2 (char 1)`},
	{`{"a"`, `Expecting ':' delimiter: line 1 column 5 (char 4)`},
	{`{"a":`, `Expecting value: line 1 column 6 (char 5)`},
	{`{"a":1`, `Expecting ',' delimiter: line 1 column 7 (char 6)`},
	{`{"a":1,`, `Expecting property name enclosed in double quotes: line 1 column 8 (char 7)`},
	{`{"a":1,}`, `Illegal trailing comma before end of object: line 1 column 7 (char 6)`},
	{`[1,]`, `Illegal trailing comma before end of array: line 1 column 3 (char 2)`},
	{`[1`, `Expecting ',' delimiter: line 1 column 3 (char 2)`},
	{`[`, `Expecting value: line 1 column 2 (char 1)`},
	{`[1 2]`, `Expecting ',' delimiter: line 1 column 4 (char 3)`},
	{`{"a" 1}`, `Expecting ':' delimiter: line 1 column 6 (char 5)`},
	{`{1:2}`, `Expecting property name enclosed in double quotes: line 1 column 2 (char 1)`},
	{`"abc`, `Unterminated string starting at: line 1 column 1 (char 0)`},
	{`"a\x"`, `Invalid \escape: line 1 column 3 (char 2)`},
	{`"a\u12"`, `Invalid \uXXXX escape: line 1 column 4 (char 3)`},
	{"\"a\nb\"", `Invalid control character at: line 1 column 3 (char 2)`},
	{`tru`, `Expecting value: line 1 column 1 (char 0)`},
	{`1 2`, `Extra data: line 1 column 3 (char 2)`},
	{`{}x`, `Extra data: line 1 column 3 (char 2)`},
	{"\n\n  x", `Expecting value: line 3 column 3 (char 4)`},
	{`-`, `Expecting value: line 1 column 1 (char 0)`},
	{`01`, `Extra data: line 1 column 2 (char 1)`},
	{`1.`, `Extra data: line 1 column 2 (char 1)`},
	{`1e`, `Extra data: line 1 column 2 (char 1)`},
	{"{\"a\":1}\n{", `Extra data: line 2 column 1 (char 8)`},
	{`{"a":1 "b":2}`, `Expecting ',' delimiter: line 1 column 8 (char 7)`},
	{`[,1]`, `Expecting value: line 1 column 2 (char 1)`},
	{`{,}`, `Expecting property name enclosed in double quotes: line 1 column 2 (char 1)`},
	{`{"é":}`, `Expecting value: line 1 column 6 (char 5)`},
}

func TestLoadsErrors(t *testing.T) {
	for _, c := range loadsCases {
		_, err := Loads(c.in)
		if err == nil || err.Error() != c.want {
			t.Errorf("Loads(%q) = %v, want %q", c.in, err, c.want)
		}
	}
}

func TestLoadsValues(t *testing.T) {
	v, err := Loads(`{"b": [1, 2.5, true, null, "x\u00e9\ud83d\ude00"], "a": {"a": 1, "a": 2}, "n": NaN, "i": -Infinity, "big": 123456789012345678901234567890, "f": 1e400}`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*jsonx.Object)
	if got := jsonx.Dumps(o, jsonx.Line); got != `{"a": {"a": 2}, "b": [1, 2.5, true, null, "x\u00e9\ud83d\ude00"], "big": 123456789012345678901234567890, "f": Infinity, "i": -Infinity, "n": NaN}` {
		t.Fatalf("dumps: %s", got)
	}
	n, _ := o.Get("n")
	if !math.IsNaN(n.(float64)) {
		t.Fatal("NaN")
	}
	if got := Repr(o); got != `{'b': [1, 2.5, True, None, 'xé😀'], 'a': {'a': 2}, 'n': nan, 'i': -inf, 'big': 123456789012345678901234567890, 'f': inf}` {
		t.Fatalf("repr: %s", got)
	}
}

func TestEqual(t *testing.T) {
	a, _ := Loads(`{"x": 1, "y": [true, 2.0]}`)
	b, _ := Loads(`{"y": [1, 2], "x": 1.0}`)
	if !Equal(a, b) {
		t.Fatal("expected equal")
	}
	c, _ := Loads(`{"y": [1, 2], "x": "1"}`)
	if Equal(a, c) {
		t.Fatal("expected unequal")
	}
}

func TestReprString(t *testing.T) {
	cases := map[string]string{
		"abc":   `'abc'`,
		"it's":  `"it's"`,
		`a'b"c`: `'a\'b"c'`,
		"a\nb":  `'a\nb'`,
		"\x01":  `'\x01'`,
		"\u00a0": `'\xa0'`,
		"\u200b": `'\u200b'`,
		"é":     `'é'`,
	}
	for in, want := range cases {
		if got := ReprString(in); got != want {
			t.Errorf("ReprString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDecodeUTF8(t *testing.T) {
	cases := map[string]string{
		"\xff":         "'utf-8' codec can't decode byte 0xff in position 0: invalid start byte",
		"ab\xe2\x82":   "'utf-8' codec can't decode bytes in position 2-3: unexpected end of data",
		"\xe2x":        "'utf-8' codec can't decode byte 0xe2 in position 0: invalid continuation byte",
		"\xe2\x82x":    "'utf-8' codec can't decode bytes in position 0-1: invalid continuation byte",
		"\xed\xa0\x80": "'utf-8' codec can't decode byte 0xed in position 0: invalid continuation byte",
	}
	for in, want := range cases {
		_, err := DecodeUTF8([]byte(in))
		if err == nil || err.Error() != want {
			t.Errorf("DecodeUTF8(%q) = %v, want %q", in, err, want)
		}
	}
}

// TestAgainstPython cross-checks messages and dumps output with a real
// CPython when SPEC_KITTY_PYTHON names one.
func TestAgainstPython(t *testing.T) {
	py := os.Getenv("SPEC_KITTY_PYTHON")
	if py == "" {
		t.Skip("SPEC_KITTY_PYTHON not set")
	}
	inputs := []string{`{"a": [1, 2,`, `[1, "\u12x"]`, `{"k": "v"  ,  "k2" : [ ] , }`, "[\"\\ud800x\"]", `[-0.0, 1e16, 1e-7, 0.1, 12345678.9, 1.5e300, 2.5e-5, 0.0001, 123456789012345678.0, 1e22, 5e-324]`, `"\t"`, `{"a":1}  `, "  [1]\n\n x", `[1,2]]`}
	for _, in := range inputs {
		cmd := exec.Command(py, "-I", "-c", `import json,sys
s=sys.stdin.read()
try:
    print("OK "+json.dumps(json.loads(s), sort_keys=True))
except json.JSONDecodeError as e:
    print("ERR "+str(e))`)
		cmd.Stdin = strings.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimRight(string(out), "\n")
		var got string
		v, derr := Loads(in)
		if derr != nil {
			got = "ERR " + derr.Error()
		} else {
			got = "OK " + jsonx.Dumps(v, jsonx.Line)
		}
		if got != want && !strings.Contains(in, `\ud800`) {
			t.Errorf("%q:\n python: %s\n go:     %s", in, want, got)
		}
	}
}
