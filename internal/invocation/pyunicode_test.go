package invocation

import (
	"reflect"
	"strings"
	"testing"
)

// Expected values captured from CPython 3.13 (the oracle's interpreter):
// t.lower() and [p for p in re.split(r'[\s\W]+', t.lower()) if p].
func TestPyLowerAndWordSplit(t *testing.T) {
	cases := []struct {
		in, lower string
		tokens    []string
	}{
		{"Straße ÉCOLE ΣΊΣΥΦΟΣ İstanbul café́ ①② x_y-z", "straße école σίσυφος i̇stanbul café́ ①② x_y-z",
			[]string{"straße", "école", "σίσυφος", "i", "stanbul", "café", "①②", "x_y", "z"}},
		{"ΟΔΟΣ Σ ΑΣ.", "οδος σ ας.", []string{"οδος", "σ", "ας"}},
		{"Review the Ǆ dǅ code", "review the ǆ dǆ code", []string{"review", "the", "ǆ", "dǆ", "code"}},
	}
	for _, c := range cases {
		if got := pyLower(c.in); got != c.lower {
			t.Errorf("pyLower(%q) = %q, want %q", c.in, got, c.lower)
		}
		got := strings.FieldsFunc(pyLower(c.in), func(r rune) bool { return !isWordRune(r) })
		if !reflect.DeepEqual(got, c.tokens) {
			t.Errorf("split(%q) = %q, want %q", c.in, got, c.tokens)
		}
	}
}
