package pyyaml

import (
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// PyError is a non-YAML Python exception raised while loading (ValueError
// from int()/float()/chr()/datetime, KeyError, IndexError, AssertionError):
// callers that only catch yaml.YAMLError let it propagate.
type PyError struct{ Type, Msg string }

func (e *PyError) Error() string { return e.Msg }

func valueError(format string, a ...any) *PyError {
	return &PyError{"ValueError", fmt.Sprintf(format, a...)}
}

// Set is a !!set value (its members' key strings, in document order).
type Set []string

// ---------------------------------------------------------------- constructed values

// The constructors build these internal values, which keep what Python's
// str()/hash() need (duplicate-key messages, key equality); export turns
// them into the package's public value space.
type (
	pySeq struct {
		items []any
		tuple bool // a sequence used as a mapping key (tuple / CommentedKeySeq)
	}
	pyMap struct {
		entries []mapEntry
		index   map[string]int
		merged  []*pyMap // round-trip merges (looked up after own keys)
		set     bool
		hashed  bool // CommentedKeyMap (round-trip mapping key)
	}
	mapEntry struct {
		key     any
		keyText string // the key as the yamlx view reports it
		val     any
	}
	pyDate struct {
		d   Date
		src string
	}
	pyDateTime struct {
		t   Timestamp
		src string
	}
	pyTagged struct {
		value, tag string
		style      rune
	} // ruamel TaggedScalar
	pyBytes struct {
		b   []byte
		src string
	}
)

func newPyMap() *pyMap { return &pyMap{index: map[string]int{}} }

func (m *pyMap) get(k string) (mapEntry, bool) {
	i, ok := m.index[k]
	if !ok {
		return mapEntry{}, false
	}
	return m.entries[i], true
}

// put is dict.__setitem__: an existing key keeps its position.
func (m *pyMap) put(hk string, e mapEntry) {
	if i, ok := m.index[hk]; ok {
		m.entries[i].val = e.val
		return
	}
	m.index[hk] = len(m.entries)
	m.entries = append(m.entries, e)
}

// hashKey is a key under Python equality (1 == 1.0 == True), "" when the
// value is unhashable.
func hashKey(v any) string {
	switch x := v.(type) {
	case nil:
		return "N"
	case bool:
		if x {
			return "i:1"
		}
		return "i:0"
	case int64:
		return fmt.Sprintf("i:%d", x)
	case *big.Int:
		return "i:" + x.String()
	case float64:
		if math.IsNaN(x) {
			return fmt.Sprintf("nan:%p", &x)
		}
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			b, _ := new(big.Float).SetFloat64(x).Int(nil)
			return "i:" + b.String()
		}
		return "f:" + pyjson.ReprFloat(x)
	case string:
		return "s:" + x
	case pyBytes:
		return "b:" + string(x.b)
	case pyDate:
		return "d:" + x.d.ISO()
	case pyDateTime:
		return "t:" + x.t.Repr
	case pyTagged:
		return fmt.Sprintf("tag:%p", &x)
	case *pySeq:
		if !x.tuple {
			return ""
		}
		parts := make([]string, len(x.items))
		for i, e := range x.items {
			parts[i] = hashKey(e)
			if parts[i] == "" {
				return ""
			}
		}
		return "(" + strings.Join(parts, ",") + ")"
	case *pyMap:
		if x.hashed {
			return fmt.Sprintf("m:%p", x)
		}
		return ""
	}
	return fmt.Sprintf("?:%v", v)
}

// pyStr is str(v).
func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case pyTagged:
		return x.value
	case pyDate:
		return x.d.ISO()
	case pyDateTime:
		return x.t.Repr
	}
	return pyRepr(v)
}

// pyRepr is repr(v) (dict/list/tuple/set displays use it for members).
func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int64:
		return fmt.Sprint(x)
	case *big.Int:
		return x.String()
	case float64:
		return pyjson.ReprFloat(x)
	case string:
		return pyjson.ReprString(x)
	case pyBytes:
		return bytesRepr(x.b)
	case pyTagged:
		style := "None"
		if x.style != 0 {
			style = pyjson.ReprString(string(x.style))
		}
		return "TaggedScalar(value=" + pyjson.ReprString(x.value) + ", style=" + style + ", tag=Tag(" + pyjson.ReprString(x.tag) + "))"
	case pyDate:
		return fmt.Sprintf("datetime.date(%d, %d, %d)", x.d.Y, x.d.M, x.d.D)
	case pyDateTime:
		return "datetime.datetime(" + x.t.Repr + ")"
	case *pySeq:
		parts := make([]string, len(x.items))
		for i, e := range x.items {
			parts[i] = pyRepr(e)
		}
		if x.tuple {
			if len(parts) == 1 {
				return "(" + parts[0] + ",)"
			}
			return "(" + strings.Join(parts, ", ") + ")"
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *pyMap:
		if x.set {
			if len(x.entries) == 0 {
				return "set()"
			}
			parts := make([]string, len(x.entries))
			for i, e := range x.entries {
				parts[i] = pyRepr(e.key)
			}
			return "{" + strings.Join(parts, ", ") + "}"
		}
		parts := []string{}
		for _, e := range x.allEntries() {
			parts = append(parts, pyRepr(e.key)+": "+pyRepr(e.val))
		}
		if x.hashed {
			if len(parts) == 0 {
				return "ordereddict()"
			}
			return "ordereddict({" + strings.Join(parts, ", ") + "})"
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func bytesRepr(b []byte) string {
	var sb strings.Builder
	quote := byte('\'')
	if strings.IndexByte(string(b), '\'') >= 0 && strings.IndexByte(string(b), '"') < 0 {
		quote = '"'
	}
	sb.WriteString("b")
	sb.WriteByte(quote)
	for _, c := range b {
		switch {
		case c == quote || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c == '\t':
			sb.WriteString(`\t`)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c < 0x20 || c >= 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte(quote)
	return sb.String()
}

// allEntries are the mapping's items as iteration sees them: own keys,
// then (round-trip merges) merged keys not already present.
func (m *pyMap) allEntries() []mapEntry {
	if len(m.merged) == 0 {
		return m.entries
	}
	out := append([]mapEntry(nil), m.entries...)
	seen := map[string]bool{}
	for k := range m.index {
		seen[k] = true
	}
	for _, mm := range m.merged {
		for _, e := range mm.allEntries() {
			hk := hashKey(e.key)
			if !seen[hk] {
				seen[hk] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- constructor

type constructor struct {
	dialect     Dialect
	version     func() [2]int
	constructed map[*Node]any
	recursive   map[*Node]bool // BaseConstructor.recursive_objects (nodes being constructed)
	deep        bool           // BaseConstructor.deep_construct
	pending     []func()
}

func (c *constructor) err(context string, cm *Mark, problem string, pm *Mark) {
	panic(&MarkedYAMLError{Class: "ConstructorError", Context: context, ContextMark: cm, Problem: problem, ProblemMark: pm, dialect: c.dialect})
}

func (c *constructor) v11() bool { return c.version() == [2]int{1, 1} }

func (c *constructor) v12() bool { return c.version() == [2]int{1, 2} }

func (c *constructor) above11() bool {
	v := c.version()
	return v[0] > 1 || (v[0] == 1 && v[1] > 1)
}

// document is construct_document.
func (c *constructor) document(n *Node) any {
	data := c.object(n, false)
	for len(c.pending) > 0 {
		round := c.pending
		c.pending = nil
		for _, fill := range round {
			fill()
		}
	}
	return data
}

// object is construct_object(node, deep). A node reached again while it is
// still being constructed (an alias to an ancestor met during a deep
// construction) is PyYAML's "found unconstructable recursive node"; ruamel
// returns recursive_objects[node], i.e. None (leaving deep_construct set,
// as upstream does). Outside deep construction the generator constructors
// finish later, so such an alias yields the object itself (a cycle).
func (c *constructor) object(n *Node, deep bool) any {
	if v, ok := c.constructed[n]; ok {
		return v
	}
	old := c.deep
	if deep {
		c.deep = true
	}
	if c.recursive[n] {
		if c.dialect == PyYAML {
			c.err("", nil, "found unconstructable recursive node", n.Start)
		}
		return nil
	}
	c.recursive[n] = true
	v, fill := c.byTag(n)
	if fill != nil {
		if c.deep {
			// constructed_objects is only set once the generator is exhausted
			fill()
		} else {
			c.pending = append(c.pending, fill)
		}
	}
	c.constructed[n] = v
	delete(c.recursive, n)
	if deep {
		c.deep = old
	}
	return v
}

func (c *constructor) scalarValue(n *Node) string {
	if n.Kind == MappingNode && c.dialect != Ruamel {
		for _, kv := range n.Pairs {
			if kv[0].Tag == TagValue {
				return c.scalarValue(kv[1])
			}
		}
	}
	if n.Kind != ScalarNode {
		c.err("", nil, "expected a scalar node, but found "+n.Kind.id(), n.Start)
	}
	if c.dialect == Ruamel && n.Style == '>' {
		return strings.ReplaceAll(n.Value, "\a", "")
	}
	return n.Value
}

var pyBoolValues = map[string]bool{"yes": true, "no": false, "true": true, "false": false, "on": true, "off": false}
var ruamelBoolValues = map[string]bool{"yes": true, "no": false, "y": true, "n": false, "true": true, "false": false, "on": true, "off": false}

// byTag dispatches on the node tag like construct_non_recursive_object; a
// non-nil fill is the rest of a generator constructor.
func (c *constructor) byTag(n *Node) (any, func()) {
	if c.dialect == Ruamel && n.TagHandle != "" && n.Tag == TagStr {
		return c.unknown(n)
	}
	switch n.Tag {
	case TagNull:
		c.scalarValue(n)
		return nil, nil
	case TagBool:
		v := strings.ToLower(c.scalarValue(n))
		table := pyBoolValues
		if c.dialect.ruamel() {
			table = ruamelBoolValues
		}
		b, ok := table[v]
		if !ok {
			panic(&PyError{"KeyError", pyjson.ReprString(v)})
		}
		return b, nil
	case TagInt:
		return c.intValue(n), nil
	case TagFloat:
		return c.floatValue(n), nil
	case TagTS:
		return c.timestamp(n), nil
	case TagStr:
		return c.scalarValue(n), nil
	case TagBinary:
		v := c.scalarValue(n)
		if e := asciiCheck(v); e != "" {
			c.err("", nil, "failed to convert base64 data into ascii: "+e, n.Start)
		}
		b, e := a2bBase64(v)
		if e != "" {
			c.err("", nil, "failed to decode base64 data: "+e, n.Start)
		}
		return pyBytes{b, n.Value}, nil
	case TagSeq:
		seq := &pySeq{}
		return seq, func() {
			if n.Kind != SequenceNode {
				c.err("", nil, "expected a sequence node, but found "+n.Kind.id(), n.Start)
			}
			for _, it := range n.Items {
				seq.items = append(seq.items, c.object(it, false))
			}
		}
	case TagMap:
		m := newPyMap()
		return m, func() { c.mapping(n, m) }
	case tagPrefix + "set":
		if c.dialect == Ruamel && n.Kind == ScalarNode {
			panic(&PyError{"AttributeError", "'ScalarNode' object has no attribute 'flow_style'"})
		}
		m := newPyMap()
		m.set = true
		return m, func() {
			if c.dialect == Ruamel {
				c.setting(n, m)
			} else {
				c.mapping(n, m)
			}
		}
	case tagPrefix + "omap", tagPrefix + "pairs":
		seq := &pySeq{}
		omap := n.Tag == tagPrefix+"omap"
		var om *pyMap
		var result any = seq
		if omap && c.dialect == Ruamel && n.Kind == ScalarNode {
			panic(&PyError{"AttributeError", "'ScalarNode' object has no attribute 'flow_style'"})
		}
		if omap && c.dialect.ruamel() {
			om = newPyMap() // ordereddict / CommentedOrderedMap
			result = om
		}
		what := "pairs"
		if omap {
			what = "an ordered map"
		}
		return result, func() {
			if n.Kind != SequenceNode {
				c.err("while constructing "+what, n.Start, "expected a sequence, but found "+n.Kind.id(), n.Start)
			}
			seen := map[string]bool{}
			for _, sub := range n.Items {
				if sub.Kind != MappingNode {
					c.err("while constructing "+what, n.Start, "expected a mapping of length 1, but found "+sub.Kind.id(), sub.Start)
				}
				if len(sub.Pairs) != 1 {
					c.err("while constructing "+what, n.Start, fmt.Sprintf("expected a single mapping item, but found %d items", len(sub.Pairs)), sub.Start)
				}
				k := c.object(sub.Pairs[0][0], false)
				if omap && c.dialect.ruamel() {
					if hk := hashKey(k); seen[hk] {
						panic(&PyError{"AssertionError", ""})
					} else {
						seen[hk] = true
					}
				}
				v := c.object(sub.Pairs[0][1], false)
				if om != nil {
					om.put(hashKey(k), mapEntry{k, keyText(sub.Pairs[0][0], k), v})
					continue
				}
				seq.items = append(seq.items, &pySeq{items: []any{k, v}, tuple: true})
			}
		}
	}
	if c.dialect == Ruamel {
		return c.unknown(n)
	}
	c.err("", nil, "could not determine a constructor for the tag "+reprS(n.Tag), n.Start)
	return nil, nil
}

// unknown is RoundTripConstructor.construct_unknown: any failure while
// building the tagged value becomes "could not determine a constructor".
func (c *constructor) unknown(n *Node) (any, func()) {
	fail := func() {
		c.err("", nil, "could not determine a constructor for the tag "+reprS(n.Tag), n.Start)
	}
	guard := func(fn func()) func() {
		return func() {
			defer func() {
				if r := recover(); r != nil {
					switch r.(type) {
					case *MarkedYAMLError, *PyError:
						fail()
					default:
						panic(r)
					}
				}
			}()
			fn()
		}
	}
	switch n.Kind {
	case MappingNode:
		m := newPyMap()
		return m, guard(func() { c.mapping(n, m) })
	case SequenceNode:
		seq := &pySeq{}
		return seq, guard(func() {
			for _, it := range n.Items {
				seq.items = append(seq.items, c.object(it, false))
			}
		})
	}
	var v string
	guard(func() { v = c.scalarValue(n) })()
	return pyTagged{v, n.Tag, n.Style}, nil
}

func (c *constructor) intValue(n *Node) any {
	v := c.scalarValue(n)
	if !c.dialect.ruamel() {
		return safeInt(v)
	}
	s := strings.ReplaceAll(v, "_", "")
	if s == "" {
		panic(&PyError{"IndexError", "string index out of range"})
	}
	sign := big.NewInt(1)
	if s[0] == '-' {
		sign = big.NewInt(-1)
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	conv := func(t string, base int) *big.Int {
		x, ok := pyInt(t, base)
		if !ok {
			panic(valueError("invalid literal for int() with base %d: %s", base, pyjson.ReprString(t)))
		}
		return x.Mul(x, sign)
	}
	rt := c.dialect == Ruamel
	widthIndex := func() {
		if rt && c.above11() && len(s) < 3 {
			panic(&PyError{"IndexError", "string index out of range"})
		}
	}
	sexagesimal := func() any {
		total := new(big.Int)
		base := big.NewInt(1)
		parts := strings.Split(s, ":")
		for _, p := range parts {
			if _, ok := pyInt(p, 10); !ok {
				panic(valueError("invalid literal for int() with base 10: %s", pyjson.ReprString(p)))
			}
		}
		for i := len(parts) - 1; i >= 0; i-- {
			d, ok := pyInt(parts[i], 10)
			if !ok {
				panic(valueError("invalid literal for int() with base 10: %s", pyjson.ReprString(parts[i])))
			}
			total.Add(total, new(big.Int).Mul(d, base))
			base.Mul(base, big.NewInt(60))
		}
		return intResult(total.Mul(total, sign))
	}
	switch {
	case !rt && s == "0":
		return int64(0)
	case strings.HasPrefix(s, "0b"):
		widthIndex()
		return intResult(conv(s[2:], 2))
	case strings.HasPrefix(s, "0x"):
		widthIndex()
		return intResult(conv(s[2:], 16))
	case strings.HasPrefix(s, "0o"):
		widthIndex()
		return intResult(conv(s[2:], 8))
	}
	if rt {
		switch {
		case !c.v12() && len(s) > 1 && s[0] == '0':
			return intResult(conv(s, 8))
		case !c.v12() && strings.Contains(s, ":"):
			return sexagesimal()
		}
		if s == "" {
			panic(&PyError{"IndexError", "string index out of range"})
		}
		return intResult(conv(s, 10))
	}
	switch {
	case c.v11() && s != "" && s[0] == '0':
		return intResult(conv(s, 8))
	case c.v11() && strings.Contains(s, ":"):
		return sexagesimal()
	}
	return intResult(conv(s, 10))
}

func (c *constructor) floatValue(n *Node) any {
	orig := c.scalarValue(n)
	if !c.dialect.ruamel() {
		return safeFloat(orig)
	}
	v := strings.ToLower(strings.ReplaceAll(orig, "_", ""))
	if v == "" {
		panic(&PyError{"IndexError", "string index out of range"})
	}
	sign := 1.0
	if v[0] == '-' {
		sign = -1
	}
	if v[0] == '+' || v[0] == '-' {
		v = v[1:]
	}
	conv := func(t string) float64 {
		f, ok := pyFloat(t)
		if !ok {
			panic(valueError("could not convert string to float: %s", pyjson.ReprString(t)))
		}
		return f
	}
	switch {
	case v == ".inf":
		return sign * math.Inf(1)
	case v == ".nan":
		return math.NaN()
	case !c.v12() && strings.Contains(v, ":"):
		parts := strings.Split(v, ":")
		for _, p := range parts {
			conv(p)
		}
		val, base := 0.0, 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			val += conv(parts[i]) * base
			base *= 60
		}
		return sign * val
	}
	if c.dialect == Ruamel && strings.Contains(v, "e") {
		// mantissa, exponent = value_so.split('e') (then 'E')
		unpack := func(sep string) (string, string, string) {
			parts := strings.Split(orig, sep)
			switch {
			case len(parts) < 2:
				return "", "", fmt.Sprintf("not enough values to unpack (expected 2, got %d)", len(parts))
			case len(parts) > 2:
				return "", "", "too many values to unpack (expected 2)"
			}
			return parts[0], parts[1], ""
		}
		_, exp, e := unpack("e")
		if e != "" {
			_, exp, e = unpack("E")
			if e != "" {
				panic(valueError("%s", e))
			}
		}
		if exp == "" {
			panic(&PyError{"IndexError", "string index out of range"})
		}
	}
	return sign * conv(v)
}

// timestamp is construct_yaml_timestamp (PyYAML's or ruamel's
// create_timestamp).
func (c *constructor) timestamp(n *Node) any {
	v := c.scalarValue(n)
	m := reTSParts.FindStringSubmatch(n.Value)
	if m == nil {
		if c.dialect.ruamel() {
			c.err("", nil, fmt.Sprintf("failed to construct timestamp from \"%s\"", n.Value), n.Start)
		}
		panic(&PyError{"AttributeError", "'NoneType' object has no attribute 'groupdict'"})
	}
	if err := checkTimestamp(n.Value); err != nil {
		panic(err)
	}
	t := constructTimestamp(n.Value)
	if c.dialect.ruamel() {
		t = roundFraction(t, m[7])
	}
	switch x := t.(type) {
	case Date:
		return pyDate{x, v}
	case Timestamp:
		return pyDateTime{x, v}
	}
	return v
}

// roundFraction applies ruamel's create_timestamp rounding of a 7th
// fractional digit (> 4 rounds the microseconds up, wrapping to 0).
func roundFraction(t any, frac string) any {
	ts, ok := t.(Timestamp)
	if !ok || len(frac) <= 6 || frac[6] <= '4' {
		return t
	}
	repr := ts.Repr
	us := frac[:6]
	n := 0
	fmt.Sscan(us, &n)
	n++
	tail := ""
	head := repr
	if i := strings.Index(repr, "."); i >= 0 {
		head = repr[:i]
		tail = repr[i+7:]
	} else if len(repr) > 19 {
		head, tail = repr[:19], repr[19:]
	}
	if n > 999999 {
		return Timestamp{Repr: head + tail}
	}
	return Timestamp{Repr: fmt.Sprintf("%s.%06d%s", head, n, tail)}
}

// keyText is the yamlx key string of a mapping key node.
func keyText(n *Node, k any) string {
	if n.Kind == ScalarNode {
		return n.Value
	}
	return pyStr(k)
}

func (c *constructor) dupKey(n *Node, kn *Node, key, value, original any) {
	panic(&MarkedYAMLError{Class: "DuplicateKeyError", Context: "while constructing a mapping", ContextMark: n.Start,
		Problem:     fmt.Sprintf("found duplicate key \"%s\" with value \"%s\" (original value: \"%s\")", pyStr(key), pyStr(value), pyStr(original)),
		ProblemMark: kn.Start, Note: dupNote, dialect: c.dialect})
}

const dupNote = "\nTo suppress this check see:\n    https://yaml.dev/doc/ruamel.yaml/api/#Duplicate_keys\n"

// mapKey constructs a mapping key and checks it is hashable.
// mapKey constructs a mapping key and checks it is hashable. terr is the
// TypeError a tuple key with unhashable members raises when it is first
// hashed (after the value is constructed).
func (c *constructor) mapKey(n, kn *Node) (k any, hk string, terr string) {
	switch c.dialect {
	case PyYAML:
		k = c.object(kn, false)
	default:
		k = c.object(kn, true)
		switch x := k.(type) {
		case *pySeq:
			k = &pySeq{items: x.items, tuple: true}
		case *pyMap:
			if c.dialect == Ruamel {
				cp := *x
				cp.hashed = true
				k = &cp
			}
		}
	}
	hk = hashKey(k)
	if hk == "" {
		if t, ok := k.(*pySeq); ok && t.tuple {
			return k, "", "unhashable type: '" + c.unhashableIn(t) + "'"
		}
		c.err("while constructing a mapping", n.Start, "found unhashable key", kn.Start)
	}
	return k, hk, ""
}

// unhashableIn is the type name hash() of a tuple trips over first.
func (c *constructor) unhashableIn(t *pySeq) string {
	rt := c.dialect == Ruamel
	for _, e := range t.items {
		if hashKey(e) != "" {
			continue
		}
		switch x := e.(type) {
		case *pySeq:
			if x.tuple {
				return c.unhashableIn(x)
			}
			if rt {
				return "CommentedSeq"
			}
			return "list"
		case *pyMap:
			switch {
			case x.set && rt:
				return "CommentedSet"
			case x.set:
				return "set"
			case rt:
				return "CommentedMap"
			}
			return "dict"
		}
	}
	return "list"
}

// mapping fills m from a mapping node (construct_mapping of each dialect).
func (c *constructor) mapping(n *Node, m *pyMap) {
	if n.Kind != MappingNode {
		c.err("", nil, "expected a mapping node, but found "+n.Kind.id(), n.Start)
	}
	switch c.dialect {
	case PyYAML:
		c.flatten(n)
		for _, kv := range n.Pairs {
			k, hk, _ := c.mapKey(n, kv[0])
			v := c.object(kv[1], false)
			m.put(hk, mapEntry{k, keyText(kv[0], k), v})
		}
	case RuamelSafe:
		merge := c.flatten(n)
		if merge == nil {
			for _, kv := range n.Pairs {
				k, hk, terr := c.mapKey(n, kv[0])
				v := c.object(kv[1], false)
				if terr != "" {
					panic(&PyError{"TypeError", terr})
				}
				if old, ok := m.get(hk); ok {
					c.dupKey(n, kv[0], k, v, old.val)
				}
				m.put(hk, mapEntry{k, keyText(kv[0], k), v})
			}
			return
		}
		// flatten prepended the merged pairs; neither part is checked.
		for _, kv := range n.Pairs {
			k, hk, terr := c.mapKey(n, kv[0])
			v := c.object(kv[1], false)
			if terr != "" {
				panic(&PyError{"TypeError", terr})
			}
			m.put(hk, mapEntry{k, keyText(kv[0], k), v})
		}
	case Ruamel:
		merges := c.flattenRT(n)
		for _, kv := range n.Pairs {
			k, hk, terr := c.mapKey(n, kv[0])
			v := c.object(kv[1], true)
			if terr != "" {
				panic(&PyError{"TypeError", terr})
			}
			if old, ok := m.get(hk); ok {
				c.dupKey(n, kv[0], k, v, old.val)
			}
			m.put(hk, mapEntry{k, keyText(kv[0], k), v})
		}
		m.merged = merges
	}
}

// setting is RoundTripConstructor.construct_setting.
func (c *constructor) setting(n *Node, m *pyMap) {
	if n.Kind != MappingNode {
		c.err("", nil, "expected a mapping node, but found "+n.Kind.id(), n.Start)
	}
	for _, kv := range n.Pairs {
		k := c.object(kv[0], true)
		if s, ok := k.(*pySeq); ok {
			k = &pySeq{items: s.items, tuple: true}
		}
		hk := hashKey(k)
		if hk == "" {
			c.err("while constructing a mapping", n.Start, "found unhashable key", kv[0].Start)
		}
		c.object(kv[1], false)
		if _, ok := m.get(hk); ok {
			panic(&MarkedYAMLError{Class: "DuplicateKeyError", Context: "while constructing a set", ContextMark: n.Start,
				Problem: fmt.Sprintf("found duplicate key \"%s\"", pyStr(k)), ProblemMark: kv[0].Start, Note: dupNote, dialect: c.dialect})
		}
		m.put(hk, mapEntry{k, keyText(kv[0], k), nil})
	}
}

// flatten is SafeConstructor.flatten_mapping (PyYAML and ruamel safe): the
// merged pairs are prepended; the returned slice is non-nil when there
// were any (ruamel's node.merge).
func (c *constructor) flatten(n *Node) [][2]*Node {
	var merge [][2]*Node
	for i := 0; i < len(n.Pairs); {
		k, v := n.Pairs[i][0], n.Pairs[i][1]
		switch k.Tag {
		case TagMerge:
			if c.dialect == RuamelSafe && len(merge) > 0 {
				panic(&MarkedYAMLError{Class: "DuplicateKeyError", Context: "while constructing a mapping", ContextMark: n.Start,
					Problem: "found duplicate merge key \"<<\"", ProblemMark: k.Start, Note: mergeNote, dialect: c.dialect})
			}
			n.Pairs = append(n.Pairs[:i:i], n.Pairs[i+1:]...)
			switch v.Kind {
			case MappingNode:
				c.flatten(v)
				merge = append(merge, v.Pairs...)
			case SequenceNode:
				var sub [][][2]*Node
				for _, s := range v.Items {
					if s.Kind != MappingNode {
						c.err("while constructing a mapping", n.Start, "expected a mapping for merging, but found "+s.Kind.id(), s.Start)
					}
					c.flatten(s)
					sub = append(sub, s.Pairs)
				}
				for j := len(sub) - 1; j >= 0; j-- {
					merge = append(merge, sub[j]...)
				}
			default:
				c.err("while constructing a mapping", n.Start, "expected a mapping or list of mappings for merging, but found "+v.Kind.id(), v.Start)
			}
		case TagValue:
			k.Tag = TagStr
			i++
		default:
			i++
		}
	}
	if len(merge) > 0 {
		n.Pairs = append(append([][2]*Node(nil), merge...), n.Pairs...)
		return merge
	}
	return nil
}

const mergeNote = "Duplicate merge keys are never allowed, not even when\n`.allow_duplicate_keys` is set to True\n"

// flattenRT is RoundTripConstructor.flatten_mapping: merge sources are
// constructed (deep) and kept aside; the mapping's own keys win.
func (c *constructor) flattenRT(n *Node) []*pyMap {
	var merges []*pyMap
	for i := 0; i < len(n.Pairs); {
		k, v := n.Pairs[i][0], n.Pairs[i][1]
		switch k.Tag {
		case TagMerge:
			if len(merges) > 0 {
				panic(&MarkedYAMLError{Class: "DuplicateKeyError", Context: "while constructing a mapping", ContextMark: n.Start,
					Problem: "found duplicate merge key \"<<\"", ProblemMark: k.Start, Note: mergeNote, dialect: c.dialect})
			}
			n.Pairs = append(n.Pairs[:i:i], n.Pairs[i+1:]...)
			switch v.Kind {
			case MappingNode:
				if m, ok := c.object(v, true).(*pyMap); ok {
					merges = append(merges, m)
				}
			case SequenceNode:
				c.object(v, true)
				for _, s := range v.Items {
					if s.Kind != MappingNode {
						c.err("while constructing a mapping", n.Start, "expected a mapping for merging, but found "+s.Kind.id(), s.Start)
					}
					if m, ok := c.object(s, true).(*pyMap); ok {
						merges = append(merges, m)
					}
				}
			default:
				c.err("while constructing a mapping", n.Start, "expected a mapping or list of mappings for merging, but found "+v.Kind.id(), v.Start)
			}
		case TagValue:
			k.Tag = TagStr
			i++
		default:
			i++
		}
	}
	return merges
}

// ---------------------------------------------------------------- export

// exportPy converts to pyyaml.Load's value space (yaml.safe_load).
func exportPy(v any, memo map[any]any) any {
	switch x := v.(type) {
	case *pySeq:
		if r, ok := memo[x]; ok {
			return r
		}
		out := make([]any, len(x.items))
		memo[x] = out
		for i, e := range x.items {
			out[i] = exportPy(e, memo)
		}
		return out
	case *pyMap:
		if r, ok := memo[x]; ok {
			return r
		}
		if x.set {
			keys := make(Set, len(x.entries))
			for i, e := range x.entries {
				keys[i] = pyStr(e.key)
			}
			return keys
		}
		o := &jsonx.Object{}
		memo[x] = o
		for _, e := range x.allEntries() {
			o.Set(pyStr(e.key), exportPy(e.val, memo))
		}
		return o
	case pyDate:
		return x.d
	case pyDateTime:
		return x.t
	case pyBytes:
		return x.b
	case pyTagged:
		return x.value
	}
	return v
}

// exportYAMLX converts to the yamlx value space (ordered objects keyed by
// the key's source text, Go ints, timestamps as their source text).
func exportYAMLX(v any, memo map[any]any) any {
	switch x := v.(type) {
	case *pySeq:
		if r, ok := memo[x]; ok {
			return r
		}
		out := make([]any, len(x.items))
		memo[x] = out
		for i, e := range x.items {
			out[i] = exportYAMLX(e, memo)
		}
		return out
	case *pyMap:
		if r, ok := memo[x]; ok {
			return r
		}
		o := &jsonx.Object{}
		memo[x] = o
		for _, e := range x.allEntries() {
			o.Set(e.keyText, exportYAMLX(e.val, memo))
		}
		return o
	case int64:
		return int(x)
	case *big.Int:
		return x
	case pyDate:
		return x.src
	case pyDateTime:
		return x.src
	case pyBytes:
		return x.src
	case pyTagged:
		return x.value
	}
	return v
}

// ---------------------------------------------------------------- loaders

// Load is yaml.safe_load(src) for a Python str: the PyYAML 6 scanner,
// parser, composer and SafeConstructor, with PyYAML's exact errors
// (*MarkedYAMLError, *ReaderError, or *PyError for non-YAML exceptions).
func Load(src string) (any, error) {
	return LoadInput(StringInput(src))
}

// LoadInput is Load for any Input (yaml.safe_load(open(path, "rb"))).
func LoadInput(in Input) (any, error) {
	v, err := load(in, PyYAML)
	if err != nil {
		return nil, err
	}
	return exportPy(v, map[any]any{}), nil
}

// LoadRuamel is ruamel.yaml's YAML(typ=...).load(src) for a Python str
// (dialect Ruamel = typ "rt", RuamelSafe = typ "safe"), returning the
// yamlx value space: *jsonx.Object keyed by the key's source text, []any,
// int (or *big.Int), float64, bool, nil, string; timestamps, !!binary and
// tagged scalars as their source text. Errors carry ruamel's exact text.
func LoadRuamel(src string, d Dialect) (any, error) {
	return LoadRuamelInput(StringInput(src), d)
}

// LoadRuamelInput is LoadRuamel for any Input (YAML().load(path) reads a
// binary stream: FileInput).
func LoadRuamelInput(in Input, d Dialect) (any, error) {
	v, err := load(in, d)
	if err != nil {
		return nil, err
	}
	return exportYAMLX(v, map[any]any{}), nil
}

// LoadRuamelPy is LoadRuamel returning pyyaml.Load's value space (Date,
// Timestamp, []byte, int64 / *big.Int, keys via str()).
func LoadRuamelPy(src string, d Dialect) (any, error) {
	v, err := load(StringInput(src), d)
	if err != nil {
		return nil, err
	}
	return exportPy(v, map[any]any{}), nil
}

func load(in Input, d Dialect) (any, error) {
	var out any
	err := run(func() {
		p := newParser(in, d)
		comp := &composer{parser: p, anchors: map[string]*Node{}}
		n := comp.getSingleNode()
		if n == nil {
			return
		}
		c := &constructor{dialect: d, constructed: map[*Node]any{}, recursive: map[*Node]bool{}, version: p.scanner.version}
		out = c.document(n)
	})
	return out, err
}
