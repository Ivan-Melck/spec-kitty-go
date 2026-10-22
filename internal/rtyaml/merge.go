package rtyaml

import (
	"math/big"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Snapshot records a plain (jsonx) view of a loaded document before a
// writer edits it: a deep copy of the values, and the identity of every
// mapping object, so Apply can tell a mapping edited in place (Python:
// `data["a"]["b"] = x` on the loaded CommentedMap, which keeps its comments)
// from one replaced by a new object (Python: `data["a"] = {...}`, a plain
// dict that dumps without the old comments).
type Snapshot struct {
	before any
	ids    map[*jsonx.Object]bool
}

// Snap snapshots v (typically the *jsonx.Object a writer is about to edit).
func Snap(v any) *Snapshot {
	s := &Snapshot{ids: map[*jsonx.Object]bool{}}
	s.before = s.walk(v)
	return s
}

func (s *Snapshot) walk(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		s.ids[t] = true
		c := &jsonx.Object{}
		for _, kv := range t.Pairs() {
			c.Set(kv.Key, s.walk(kv.Value))
		}
		return c
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = s.walk(x)
		}
		return out
	}
	return v
}

// ContentSnapshot is a Snapshot without identities, for a writer whose
// edited value did not come from the document being rewritten (e.g. a
// freshly parsed copy): an unchanged mapping keeps its comments, a changed
// one is replaced.
func ContentSnapshot(before any) *Snapshot { return &Snapshot{before: before} }

// Apply merges the edits that turned the snapshot into after onto the
// round-trip tree, the way the same edits made by Python on the loaded
// CommentedMap would: untouched entries keep their comments, quote style
// and formatting; replaced entries become plain values (a replaced string
// keeps the old quote style, as CommentedMap.__setitem__ does); removed
// keys are deleted; new keys are appended.
//
// A root that is not a mapping is replaced by after.
func (d *Doc) Apply(after *jsonx.Object, s *Snapshot) {
	if s == nil {
		s = &Snapshot{}
	}
	before, _ := s.before.(*jsonx.Object)
	if m, ok := d.Root.(*Map); ok && !m.IsSet && !m.Ordered {
		mergeMap(m, before, after, s)
		return
	}
	d.Root = after
}

func mergeMap(m *Map, before, after *jsonx.Object, s *Snapshot) {
	// keys removed by the writer (string keys only: jsonx keys are text)
	for _, k := range m.Keys() {
		ks, ok := m.keyText(k)
		if ok && !after.Has(ks) {
			m.Delete(k)
		}
	}
	for _, kv := range after.Pairs() {
		rk, found := findKey(m, kv.Key)
		if !found {
			m.Set(kv.Key, kv.Value)
			continue
		}
		rv, _ := m.Get(rk)
		var bv any
		hasB := false
		if before != nil {
			bv, hasB = before.Get(kv.Key)
		}
		nv, keep := mergeValue(rv, bv, hasB, kv.Value, s)
		if !keep {
			m.Set(rk, nv)
		}
	}
	reorder(m, after)
}

// mergeValue returns the value for an existing key and whether the
// round-trip value rv is kept as is.
func mergeValue(rv, bv any, hasB bool, av any, s *Snapshot) (any, bool) {
	switch a := av.(type) {
	case *jsonx.Object:
		inPlace := s.ids[a]
		if !inPlace && len(s.ids) == 0 && hasB && plainEqual(bv, a) {
			inPlace = true
		}
		if inPlace {
			if rm, ok := rv.(*Map); ok && !rm.IsSet && !rm.Ordered {
				b, _ := bv.(*jsonx.Object)
				mergeMap(rm, b, a, s)
				return rm, true
			}
			if hasB && plainEqual(bv, a) {
				return rv, true
			}
		}
		return a, false
	default:
		if hasB && plainEqual(bv, av) {
			return rv, true
		}
		return av, false
	}
}

// keyText is the yamlx key of a round-trip key: the scalar key's source
// text, else the string itself.
func (m *Map) keyText(k any) (string, bool) {
	if t, ok := m.texts[hashKey(k)]; ok {
		return t, true
	}
	switch x := k.(type) {
	case string:
		return x, true
	case *Str:
		return x.V, true
	}
	return "", false
}

func findKey(m *Map, key string) (any, bool) {
	for _, k := range m.Keys() {
		if ks, ok := m.keyText(k); ok && ks == key {
			return k, true
		}
	}
	return nil, false
}

// reorder puts the string keys of m in after's order (Python dicts keep
// insertion order; a key the writer deleted and re-added moves to the end).
func reorder(m *Map, after *jsonx.Object) {
	pos := map[string]int{}
	for i, k := range after.Keys() {
		pos[k] = i
	}
	var slots []int
	var keyed []entry
	for i, e := range m.entries {
		if ks, ok := m.keyText(e.key); ok {
			if _, in := pos[ks]; in {
				slots = append(slots, i)
				keyed = append(keyed, e)
			}
		}
	}
	sorted := make([]entry, len(keyed))
	copy(sorted, keyed)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			a, _ := m.keyText(sorted[j-1].key)
			b, _ := m.keyText(sorted[j].key)
			if pos[a] <= pos[b] {
				break
			}
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	for i, slot := range slots {
		m.entries[slot] = sorted[i]
		m.index[sorted[i].hk] = slot
	}
}

// PlainEqual compares two jsonx values (order-sensitive for mappings).
func PlainEqual(a, b any) bool { return plainEqual(a, b) }

// plainEqual compares two jsonx values (order-sensitive for mappings).
func plainEqual(a, b any) bool {
	switch x := a.(type) {
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		xp, yp := x.Pairs(), y.Pairs()
		for i := range xp {
			if xp[i].Key != yp[i].Key || !plainEqual(xp[i].Value, yp[i].Value) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !plainEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case *big.Int:
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	case nil:
		return b == nil
	case string, bool, int, int64, float64:
		return a == b
	}
	return false
}
