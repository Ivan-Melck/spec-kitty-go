package pydifflib

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/golden.json is produced by CPython's difflib (work-DOC/gen_difflib.py).
func TestMatchesCPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Seq []struct {
			A, B   string
			Ratio  float64
			Blocks [][3]int
		}
		Close []struct {
			Word   string
			N      int
			Cutoff float64
			Out    []string
		}
		Order []string
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Seq {
		m := New(c.A, c.B)
		if r := m.Ratio(); r != c.Ratio {
			t.Errorf("ratio(%q, %q) = %v, want %v", c.A, c.B, r, c.Ratio)
		}
		var bl [][3]int
		for _, b := range m.MatchingBlocks() {
			bl = append(bl, [3]int{b.I, b.J, b.Size})
		}
		if !reflect.DeepEqual(bl, c.Blocks) {
			t.Errorf("blocks(%q, %q) = %v, want %v", c.A, c.B, bl, c.Blocks)
		}
	}
	for i, c := range g.Close {
		poss := g.Order
		if i%2 == 1 {
			poss = make([]string, len(g.Order))
			for j := range g.Order {
				poss[j] = g.Order[len(g.Order)-1-j]
			}
		}
		got := GetCloseMatches(c.Word, poss, c.N, c.Cutoff)
		if len(got) == 0 && len(c.Out) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.Out) {
			t.Errorf("get_close_matches(%q) = %v, want %v", c.Word, got, c.Out)
		}
	}
}
