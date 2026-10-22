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

type detectResult struct {
	Encoding  string          `json:"encoding"`
	Chaos     float64         `json:"chaos"`
	Coherence float64         `json:"coherence"`
	Languages [][]interface{} `json:"languages"`
	CouldBe   []string        `json:"could_be"`
	Language  string          `json:"language"`
}

type detectSample struct {
	Data    string         `json:"data"`
	Results []detectResult `json:"results"`
	Best    *string        `json:"best"`
}

func describeGo(data []byte) []detectResult {
	var out []detectResult
	for _, m := range FromBytes(data).All() {
		r := detectResult{Encoding: m.Encoding(), Chaos: m.Chaos(), Coherence: m.Coherence(), CouldBe: m.CouldBeFromCharset(), Language: m.Language()}
		r.Languages = [][]interface{}{}
		for _, l := range m.Languages() {
			r.Languages = append(r.Languages, []interface{}{l.Language, l.Ratio})
		}
		out = append(out, r)
	}
	if out == nil {
		out = []detectResult{}
	}
	return out
}

// TestFromBytesMatchesCharsetNormalizer compares every CharsetMatch (order,
// encoding, chaos, coherence, languages, submatches, language) with the
// oracle venv's charset_normalizer on the committed corpus plus generated
// samples in every candidate encoding.
func TestFromBytesMatchesCharsetNormalizer(t *testing.T) {
	py := oraclePython(t)
	n := 300
	if s := os.Getenv("CHARSETNORM_DETECT_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	seed := "11"
	if s := os.Getenv("CHARSETNORM_SEED"); s != "" {
		seed = s
	}
	files, _ := filepath.Glob("testdata/corpus/*")
	out := runPython(t, py, append([]string{"testdata/detect_fuzz.py", strconv.Itoa(n), seed}, files...)...)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	bad, total := 0, 0
	for sc.Scan() {
		total++
		var s detectSample
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		data, _ := hex.DecodeString(s.Data)
		got := describeGo(data)
		for i := range s.Results {
			if s.Results[i].Languages == nil {
				s.Results[i].Languages = [][]interface{}{}
			}
		}
		if s.Results == nil {
			s.Results = []detectResult{}
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(s.Results)
		if string(gj) != string(wj) {
			bad++
			if bad <= 5 {
				d := s.Data
				if len(d) > 300 {
					d = d[:300] + "..."
				}
				t.Errorf("sample %s\n got  %s\n want %s", d, gj, wj)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d samples differ", bad, total)
	}
}
