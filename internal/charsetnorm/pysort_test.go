package charsetnorm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"testing"
)

func TestPyListSortStableOnConsistentOrder(t *testing.T) {
	for n := 0; n < 300; n += 7 {
		a := make([][2]int, n)
		for i := range a {
			a[i] = [2]int{(i * 7919) % 13, i}
		}
		want := append([][2]int(nil), a...)
		sort.SliceStable(want, func(i, j int) bool { return want[i][0] < want[j][0] })
		pyListSort(a, func(x, y [2]int) bool { return x[0] < y[0] })
		for i := range a {
			if a[i] != want[i] {
				t.Fatalf("n=%d: mismatch at %d", n, i)
			}
		}
	}
}

// TestPyListSortMatchesCPython drives both sorts with the same inconsistent
// comparison table: only an exact port of the algorithm agrees.
func TestPyListSortMatchesCPython(t *testing.T) {
	py := oraclePython(t)
	out := runPython(t, py, "testdata/sort_fuzz.py", "300", "5")
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for k := 0; sc.Scan(); k++ {
		var c struct {
			Table [][]int `json:"table"`
			Perm  []int   `json:"perm"`
		}
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		a := make([]int, len(c.Table))
		for i := range a {
			a[i] = i
		}
		pyListSort(a, func(x, y int) bool { return c.Table[x][y] == 1 })
		for i := range a {
			if a[i] != c.Perm[i] {
				t.Fatalf("case %d (n=%d): got %v want %v", k, len(a), a, c.Perm)
			}
		}
	}
}
