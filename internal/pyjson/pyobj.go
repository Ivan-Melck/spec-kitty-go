package pyjson

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Repr renders v like Python's repr().
func Repr(v any) string {
	var b strings.Builder
	r := &reprState{active: map[any]bool{}}
	r.repr(&b, v)
	return b.String()
}

// reprState tracks the containers being rendered: a container met again
// inside itself renders as "[...]" / "{...}" (Py_ReprEnter).
type reprState struct{ active map[any]bool }

// identity is a container's object identity (nil for an empty list, which
// cannot contain itself).
func identity(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		return t
	case []any:
		if len(t) > 0 {
			return &t[0]
		}
	}
	return nil
}


func (r *reprState) repr(b *strings.Builder, v any) {
	if id := identity(v); id != nil {
		if r.active[id] {
			if _, isList := v.([]any); isList {
				b.WriteString("[...]")
			} else {
				b.WriteString("{...}")
			}
			return
		}
		r.active[id] = true
		defer delete(r.active, id)
	}
	switch t := v.(type) {
	case nil:
		b.WriteString("None")
	case bool:
		if t {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case string:
		b.WriteString(ReprString(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case json.Number:
		b.WriteString(t.String())
	case float64:
		b.WriteString(ReprFloat(t))
	case *jsonx.Object:
		if t == nil {
			b.WriteString("None")
			return
		}
		b.WriteByte('{')
		for i, kv := range t.Pairs() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(ReprString(kv.Key))
			b.WriteString(": ")
			r.repr(b, kv.Value)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			r.repr(b, x)
		}
		b.WriteByte(']')
	case []string:
		b.WriteString(ReprStrings(t))
	default:
		fmt.Fprintf(b, "%v", t)
	}
}

// ReprStrings renders a list of str like repr(list[str]).
func ReprStrings(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = ReprString(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// ReprFloat is repr(float).
func ReprFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	return jsonx.FormatFloat(f)
}

func isPrintable(r rune) bool {
	if r == ' ' {
		return true
	}
	if unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
		return false
	}
	if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cs, r) || unicode.Is(unicode.Co, r) {
		return false
	}
	// Unassigned code points (Cn) are not printable.
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S)
}

// ReprString is repr(str).
func ReprString(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case isPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// Str renders v like Python's str().
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}

// Truthy is bool(v).
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case json.Number:
		return t.String() != "0" && t.String() != "-0"
	case float64:
		return t != 0
	case *jsonx.Object:
		return t != nil && t.Len() > 0
	case []any:
		return len(t) > 0
	}
	return true
}

// numeric converts Python numbers (bool, int, float) to a comparable form.
func numeric(v any) (*big.Float, bool, bool) { // value, isNaN, ok
	switch t := v.(type) {
	case bool:
		if t {
			return big.NewFloat(1), false, true
		}
		return big.NewFloat(0), false, true
	case int:
		return new(big.Float).SetInt64(int64(t)), false, true
	case int64:
		return new(big.Float).SetInt64(t), false, true
	case json.Number:
		i, ok := new(big.Int).SetString(t.String(), 10)
		if !ok {
			return nil, false, false
		}
		return new(big.Float).SetPrec(0).SetInt(i), false, true
	case float64:
		if math.IsNaN(t) {
			return nil, true, true
		}
		if math.IsInf(t, 0) {
			return new(big.Float).SetInf(t < 0), false, true
		}
		return new(big.Float).SetFloat64(t), false, true
	}
	return nil, false, false
}

// Equal is Python's a == b over JSON values.
func Equal(a, b any) bool {
	if na, nanA, okA := numeric(a); okA {
		nb, nanB, okB := numeric(b)
		if !okB || nanA || nanB {
			return false
		}
		return na.Cmp(nb) == 0
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		if !ok {
			return false
		}
		if x == nil || y == nil {
			return x == y
		}
		if x.Len() != y.Len() {
			return false
		}
		for _, kv := range x.Pairs() {
			yv, ok := y.Get(kv.Key)
			if !ok || !Equal(kv.Value, yv) {
				return false
			}
		}
		return true
	}
	return false
}

// HashKey returns a key such that HashKey(a) == HashKey(b) iff a == b for
// hashable values (None, bool, numbers, str). ok is false for unhashable
// values (list, dict), which raise TypeError in Python.
func HashKey(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "N", true
	case string:
		return "s" + t, true
	case []any, *jsonx.Object:
		return "", false
	}
	n, nan, ok := numeric(v)
	if !ok {
		return fmt.Sprintf("?%v", v), true
	}
	if nan {
		return fmt.Sprintf("nan%p", &v), true
	}
	return "n" + n.Text('g', 1000), true
}

// TypeName is type(v).__name__.
func TypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case int, int64, json.Number:
		return "int"
	case float64:
		return "float"
	case *jsonx.Object:
		return "dict"
	case []any:
		return "list"
	}
	return fmt.Sprintf("%T", v)
}

// SortStrings sorts like sorted(list[str]) (code point order).
func SortStrings(xs []string) { sort.Strings(xs) }

// Copy returns a deep copy of a JSON value.
func Copy(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		if t == nil {
			return t
		}
		return t.Clone()
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Copy(x)
		}
		return out
	}
	return v
}
