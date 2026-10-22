// Package rtyaml is a port of ruamel.yaml 0.19's round trip: YAML() with
// preserve_quotes loads a document into commented containers (comments,
// quote and block styles, anchors, merges, number formats kept), callers
// edit it, and dump re-emits it the way RoundTripRepresenter, Serializer
// and RoundTripEmitter do (indent 2, width 80, comment columns kept).
//
// The load side reuses internal/pyyaml's ruamel scanner/parser/composer.
package rtyaml

import (
	"fmt"
	"math"
	"math/big"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// Anchor is ruamel.yaml.anchor.Anchor.
type Anchor struct {
	Value      string
	AlwaysDump bool
}

// Tag is the subset of ruamel.yaml.tag.Tag the round trip needs.
type Tag struct {
	Handle string // "" = None
	Suffix string
	Value  string // trval (the resolved tag)
}

// CommentAttr is ruamel.yaml.comments.Comment (version 0 comments).
type CommentAttr struct {
	Comment *pyyaml.PyList            // [post, pre-list] of the container
	Items   map[string]*pyyaml.PyList // per key / index: [key post, key pre, value post, value pre]
	End     []*pyyaml.CommentToken    // end-of-document comments
}

func (c *CommentAttr) item(k string) (*pyyaml.PyList, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Items[k]
	return v, ok
}

// Map is CommentedMap (also used for CommentedKeyMap / CommentedOrderedMap).
type Map struct {
	entries []entry
	index   map[string]int
	own     map[string]bool // _ok
	CA      *CommentAttr
	Anchor  *Anchor
	Flow    *bool // fa._flow_style
	Tag     *Tag  // a tag kept from loading (unknown tags)
	Merge   *Merge
	IsSet   bool              // CommentedSet
	Ordered bool              // CommentedOrderedMap (!!omap)
	keyMap  bool              // CommentedKeyMap
	texts   map[string]string // source text of scalar keys (the yamlx key)
}

// setText records the source text of a scalar key node.
func (m *Map) setText(key any, kn *pyyaml.Node) {
	if kn.Kind != pyyaml.ScalarNode {
		return
	}
	if m.texts == nil {
		m.texts = map[string]string{}
	}
	m.texts[hashKey(key)] = kn.Value
}

type entry struct {
	key any
	hk  string
	val any
}

// Merge is ruamel's MergeValue: the merged maps, the position of the "<<"
// key and the sequence of a list merge.
type Merge struct {
	Maps     []*Map
	Pos      int
	Sequence *Seq
}

// NewMap returns an empty CommentedMap.
func NewMap() *Map { return &Map{index: map[string]int{}, own: map[string]bool{}} }

func (m *Map) ca() *CommentAttr {
	if m.CA == nil {
		m.CA = &CommentAttr{Items: map[string]*pyyaml.PyList{}}
	}
	return m.CA
}

// Len is the number of keys (merged keys included).
func (m *Map) Len() int { return len(m.entries) }

// Keys returns the keys in order (merged keys included).
func (m *Map) Keys() []any {
	out := make([]any, len(m.entries))
	for i, e := range m.entries {
		out[i] = e.key
	}
	return out
}

// Get returns the value of a key (merged maps consulted).
func (m *Map) Get(key any) (any, bool) {
	if i, ok := m.index[hashKey(key)]; ok {
		return m.entries[i].val, true
	}
	return nil, false
}

// Has reports whether key is present.
func (m *Map) Has(key any) bool { _, ok := m.index[hashKey(key)]; return ok }

// rawSet is ordereddict.__setitem__.
func (m *Map) rawSet(key, val any) {
	hk := hashKey(key)
	if i, ok := m.index[hk]; ok {
		m.entries[i].val = val
		return
	}
	m.index[hk] = len(m.entries)
	m.entries = append(m.entries, entry{key, hk, val})
}

func (m *Map) rawDelete(hk string) {
	i, ok := m.index[hk]
	if !ok {
		return
	}
	m.entries = append(m.entries[:i:i], m.entries[i+1:]...)
	delete(m.index, hk)
	for j := i; j < len(m.entries); j++ {
		m.index[m.entries[j].hk] = j
	}
}

// Set is CommentedMap.__setitem__: a plain string replacing a quoted /
// block string keeps that string type (style).
func (m *Map) Set(key, val any) {
	if old, ok := m.Get(key); ok {
		if s, isStr := val.(string); isStr {
			if os, oldStr := old.(*Str); oldStr {
				val = &Str{V: s, Style: os.Style}
			}
		}
	}
	m.rawSet(key, val)
	m.own[hashKey(key)] = true
}

// Delete is CommentedMap.__delitem__ (the key's comments are kept in the
// comment attribute but no longer emitted).
func (m *Map) Delete(key any) {
	hk := hashKey(key)
	if m.Merge != nil {
		if i, ok := m.index[hk]; ok && m.Merge.Pos > i {
			m.Merge.Pos--
		}
	}
	delete(m.own, hk)
	m.rawDelete(hk)
}

// nonMergedItems are the own keys (non_merged_items).
func (m *Map) nonMergedItems() []entry {
	var out []entry
	for _, e := range m.entries {
		if m.own[e.hk] {
			out = append(out, e)
		}
	}
	return out
}

// addMerge is add_yaml_merge.
func (m *Map) addMerge(mv *Merge) {
	m.Merge = mv
	for _, v := range mv.Maps {
		for _, e := range v.entries {
			if _, ok := m.index[e.hk]; ok {
				continue
			}
			m.index[e.hk] = len(m.entries)
			m.entries = append(m.entries, entry{e.key, e.hk, e.val})
			if t, ok := v.texts[e.hk]; ok {
				if m.texts == nil {
					m.texts = map[string]string{}
				}
				m.texts[e.hk] = t
			}
		}
	}
}

// Seq is CommentedSeq (also CommentedKeySeq when used as a key).
type Seq struct {
	Items  []any
	CA     *CommentAttr
	Anchor *Anchor
	Flow   *bool
	Tag    *Tag
	keySeq bool
}

func (s *Seq) ca() *CommentAttr {
	if s.CA == nil {
		s.CA = &CommentAttr{Items: map[string]*pyyaml.PyList{}}
	}
	return s.CA
}

// ---------------------------------------------------------------- scalars

// Str is a ScalarString (LiteralScalarString '|', FoldedScalarString '>',
// SingleQuotedScalarString '\”, DoubleQuotedScalarString '"',
// PlainScalarString 'p').
type Str struct {
	V       string
	Style   rune
	FoldPos []int
	Anchor  *Anchor
	Comment string // a block scalar's indicator-line comment (.comment)
}

// IntKind is the ScalarInt subclass.
type IntKind int

const (
	IntScalar IntKind = iota
	IntBinary
	IntOctal
	IntHex
	IntHexCaps
)

// Int is ScalarInt and friends (width / underscore positions kept).
type Int struct {
	V          *big.Int
	Kind       IntKind
	Width      int // 0 = None
	Underscore *[3]any
	Anchor     *Anchor
}

// Float is ScalarFloat.
type Float struct {
	V      float64
	Width  int
	Prec   int
	MSign  string // "" = False
	MLead0 int
	Exp    string // "" = None
	EWidth int
	ESign  bool
	Anchor *Anchor
}

// Bool is ScalarBoolean (a bool with an anchor).
type Bool struct {
	V      bool
	Anchor *Anchor
}

// Date is datetime.date.
type Date struct{ Y, M, D int }

// TimeStamp is ruamel's TimeStamp (datetime with _yaml format info).
type TimeStamp struct {
	Y, Mo, D, H, Mi, S, Micro int
	Offset                    *int   // minutes; nil = naive
	TZName                    string // str(tzinfo)
	T                         bool   // _yaml['t']
	Plain                     bool   // a plain datetime (not a TimeStamp)
}

// Tagged is TaggedScalar.
type Tagged struct {
	Value  string
	Style  rune
	Tag    Tag
	Anchor *Anchor
}

// hashKey is the key under Python equality.
func hashKey(v any) string {
	switch x := v.(type) {
	case nil:
		return "N"
	case bool:
		if x {
			return "i:1"
		}
		return "i:0"
	case *Bool:
		return hashKey(x.V)
	case int:
		return fmt.Sprintf("i:%d", x)
	case int64:
		return fmt.Sprintf("i:%d", x)
	case *big.Int:
		return "i:" + x.String()
	case *Int:
		return "i:" + x.V.String()
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) && !math.IsNaN(x) {
			b, _ := new(big.Float).SetFloat64(x).Int(nil)
			return "i:" + b.String()
		}
		return "f:" + pyjson.ReprFloat(x)
	case *Float:
		return hashKey(x.V)
	case string:
		return "s:" + x
	case *Str:
		return "s:" + x.V
	case *Tagged:
		return fmt.Sprintf("t:%p", x)
	case Date:
		return fmt.Sprintf("d:%04d-%02d-%02d", x.Y, x.M, x.D)
	case *TimeStamp:
		return fmt.Sprintf("ts:%p", x)
	case *Seq:
		return fmt.Sprintf("seq:%p", x)
	case *Map:
		return fmt.Sprintf("map:%p", x)
	}
	return fmt.Sprintf("?:%v", v)
}

// keyStr is the CommentAttr.Items key of a mapping key / sequence index.
func itemKey(v any) string { return hashKey(v) }
