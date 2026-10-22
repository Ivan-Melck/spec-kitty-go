package charsetnorm

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// oraclePython returns the oracle venv's interpreter (CPython 3.13 with
// charset_normalizer 3.5.2), or skips the test.
func oraclePython(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CHARSETNORM_PYTHON")
	if p == "" {
		if o := os.Getenv("SPEC_KITTY_ORACLE"); o != "" {
			p = filepath.Join(filepath.Dir(filepath.Dir(o)), "oracle-venv", "bin", "python")
		} else if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, ".cache", "spec-kitty-parity", "oracle-venv", "bin", "python")
		}
	}
	if _, err := os.Stat(p); err != nil {
		t.Skip("oracle venv python not available (set CHARSETNORM_PYTHON)")
	}
	return p
}

func runPython(t *testing.T, py string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(py, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python %v: %v\n%s", args, err, stderr.String())
	}
	return out
}

type codecSample struct {
	Enc    string `json:"enc"`
	Data   string `json:"data"`
	Strict []rune `json:"strict"`
	Ignore []rune `json:"ignore"`
}

func runeEq(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCodecsMatchCPython(t *testing.T) {
	py := oraclePython(t)
	var encs []string
	seen := map[string]bool{}
	for _, e := range ianaSupportedMBFirst {
		if !seen[e] {
			seen[e] = true
			encs = append(encs, e)
		}
	}
	n := 400
	if s := os.Getenv("CHARSETNORM_FUZZ_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	out := runPython(t, py, "testdata/codec_fuzz.py", strings.Join(encs, ","), strconv.Itoa(n))
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	fails := map[string]int{}
	for sc.Scan() {
		var s codecSample
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		data, _ := hex.DecodeString(s.Data)
		c := LookupCodec(s.Enc)
		if c == nil {
			t.Fatalf("no codec %s", s.Enc)
		}
		got, ok := c.Decode(data, false)
		if ok != (s.Strict != nil) || (ok && !runeEq(got, s.Strict)) {
			if fails[s.Enc] < 3 {
				t.Errorf("%s strict %x: got %v %q want %q", s.Enc, data, ok, string(got), string(s.Strict))
			}
			fails[s.Enc]++
		}
		got, _ = c.Decode(data, true)
		if !runeEq(got, s.Ignore) {
			if fails[s.Enc] < 3 {
				t.Errorf("%s ignore %x: got %q want %q", s.Enc, data, string(got), string(s.Ignore))
			}
			fails[s.Enc]++
		}
	}
	for e, n := range fails {
		t.Logf("%s: %d mismatches", e, n)
	}
}
