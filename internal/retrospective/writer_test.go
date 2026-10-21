package retrospective

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// TestSerializeFolding pins ruamel's width-120 plain-scalar folding
// (observed with kernel.yaml_io.serialize_mapping).
func TestSerializeFolding(t *testing.T) {
	for _, c := range []struct {
		n    int
		want []int
	}{{105, []int{120}}, {106, []int{118, 4}}, {108, []int{120, 4}}, {109, []int{118, 7}}, {112, []int{8, 120}}, {119, []int{8, 121, 7}}} {
		out := Serialize(jsonx.NewObject("details", strings.Repeat("a", c.n)+" bb cc"))
		var got []int
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			got = append(got, len(l))
		}
		if len(got) != len(c.want) {
			t.Errorf("n=%d: lines %v want %v\n%s", c.n, got, c.want, out)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("n=%d: lines %v want %v", c.n, got, c.want)
				break
			}
		}
	}
}

func TestSerializeShapes(t *testing.T) {
	o := jsonx.NewObject("a", 1, "b", nil, "c", "2026-10-21T13:20:43.339287+00:00", "d", "<default>", "e", []any{},
		"f", []any{jsonx.NewObject("id", "h-001", "refs", []any{"e-1"})}, "g", jsonx.NewObject("x", "y"))
	want := "a: 1\nb:\nc: '2026-10-21T13:20:43.339287+00:00'\nd: <default>\ne: []\nf:\n- id: h-001\n  refs:\n  - e-1\ng:\n  x: y\n"
	if got := Serialize(o); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
