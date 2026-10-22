package charsetnorm

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type recoverJSON struct {
	Error                string          `json:"error,omitempty"`
	Text                 []rune          `json:"text"`
	SourceEncoding       *string         `json:"source_encoding"`
	Confidence           float64         `json:"confidence"`
	Ambiguous            bool            `json:"ambiguous"`
	Candidates           [][]interface{} `json:"candidates"`
	NormalizationApplied bool            `json:"normalization_applied"`
	BypassUsed           bool            `json:"bypass_used"`
}

func recoverGo(data []byte, unsafe bool) recoverJSON {
	r, err := Recover(data, unsafe)
	if err != nil {
		return recoverJSON{Error: err.Error()}
	}
	out := recoverJSON{Confidence: r.Confidence, Ambiguous: r.Ambiguous, NormalizationApplied: r.NormalizationApplied, BypassUsed: r.BypassUsed}
	if !r.Ambiguous {
		out.Text = []rune(r.Text)
		if out.Text == nil {
			out.Text = []rune{}
		}
		s := r.SourceEncoding
		out.SourceEncoding = &s
	}
	out.Candidates = [][]interface{}{}
	for _, c := range r.Candidates {
		out.Candidates = append(out.Candidates, []interface{}{c.Encoding, c.Confidence})
	}
	return out
}

// TestRecoverMatchesUpstream compares Recover with the oracle venv's
// charter.encoding_recovery.recover (safe and unsafe) on the corpus and
// generated samples.
func TestRecoverMatchesUpstream(t *testing.T) {
	py := oraclePython(t)
	n := 300
	if s := os.Getenv("CHARSETNORM_DETECT_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	files, _ := filepath.Glob("testdata/corpus/*")
	out := runPython(t, py, append([]string{"testdata/detect_fuzz.py", strconv.Itoa(n), "23", "--recover"}, files...)...)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	bad, total := 0, 0
	for sc.Scan() {
		total++
		var s struct {
			Data   string      `json:"data"`
			Safe   recoverJSON `json:"safe"`
			Unsafe recoverJSON `json:"unsafe"`
		}
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		data, _ := hex.DecodeString(s.Data)
		for _, c := range []struct {
			unsafe bool
			want   recoverJSON
		}{{false, s.Safe}, {true, s.Unsafe}} {
			if c.want.Candidates == nil {
				c.want.Candidates = [][]interface{}{}
			}
			if c.want.Error != "" {
				c.want.Candidates = nil
			}
			got := recoverGo(data, c.unsafe)
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(c.want)
			if string(gj) != string(wj) {
				bad++
				if bad <= 5 {
					d := s.Data
					if len(d) > 200 {
						d = d[:200] + "..."
					}
					t.Errorf("unsafe=%v sample %s\n got  %.1500s\n want %.1500s", c.unsafe, d, gj, wj)
				}
			}
		}
	}
	if total < len(files) {
		t.Fatalf("only %d samples", total)
	}
	if bad > 0 {
		t.Errorf("%d mismatches", bad)
	}
}
