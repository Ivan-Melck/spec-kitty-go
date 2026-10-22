package pyyaml

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// PyError is a non-YAML Python exception raised while loading (ValueError
// from int()/float()/chr()/datetime, KeyError, IndexError): callers that
// only catch yaml.YAMLError let it propagate.
type PyError struct{ Type, Msg string }

func (e *PyError) Error() string { return e.Msg }

func valueError(format string, a ...any) *PyError {
	return &PyError{"ValueError", fmt.Sprintf(format, a...)}
}

// ---------------------------------------------------------------- SafeConstructor

// Collections are filled the way PyYAML's generator constructors are: the
// empty container is returned first and its contents are constructed in a
// later round (BaseConstructor.state_generators), which fixes the order in
// which construction errors surface. Boxes keep containers addressable
// until the final conversion.
type seqBox struct{ items []any }

type mapBox struct {
	keys []string
	vals map[string]any
	set  bool
}

// Set is a !!set value (its members' key strings, in document order).
type Set []string

type constructor struct {
	dialect     Dialect
	constructed map[*Node]any
	pending     []func()
}

func (c *constructor) err(context string, cm *Mark, problem string, pm *Mark) {
	panic(&MarkedYAMLError{Class: "ConstructorError", Context: context, ContextMark: cm, Problem: problem, ProblemMark: pm, dialect: c.dialect})
}

func (c *constructor) document(n *Node) any {
	data := c.object(n)
	for len(c.pending) > 0 {
		round := c.pending
		c.pending = nil
		for _, fill := range round {
			fill()
		}
	}
	return unbox(data, map[any]any{})
}

func unbox(v any, memo map[any]any) any {
	switch b := v.(type) {
	case *seqBox:
		if r, ok := memo[b]; ok {
			return r
		}
		out := make([]any, len(b.items))
		memo[b] = out
		for i, e := range b.items {
			out[i] = unbox(e, memo)
		}
		return out
	case *mapBox:
		if r, ok := memo[b]; ok {
			return r
		}
		if b.set {
			return Set(append([]string(nil), b.keys...))
		}
		o := &jsonx.Object{}
		memo[b] = o
		for _, k := range b.keys {
			o.Set(k, unbox(b.vals[k], memo))
		}
		return o
	}
	return v
}

func (c *constructor) object(n *Node) any {
	if v, ok := c.constructed[n]; ok {
		return v
	}
	v := c.byTag(n)
	c.constructed[n] = v
	return v
}

func (c *constructor) scalarValue(n *Node) string {
	if n.Kind == MappingNode {
		for _, kv := range n.Pairs {
			if kv[0].Tag == TagValue {
				return c.scalarValue(kv[1])
			}
		}
	}
	if n.Kind != ScalarNode {
		c.err("", nil, "expected a scalar node, but found "+n.Kind.id(), n.Start)
	}
	return n.Value
}

// generated reports whether a node's constructor is a generator (its value
// is an empty, unhashable container when first returned).
func generated(tag string) bool {
	switch tag {
	case TagSeq, TagMap, tagPrefix + "set", tagPrefix + "omap", tagPrefix + "pairs":
		return true
	}
	return false
}

func (c *constructor) byTag(n *Node) any {
	switch n.Tag {
	case TagNull:
		c.scalarValue(n)
		return nil
	case TagBool:
		v := c.scalarValue(n)
		b, ok := boolValues[strings.ToLower(v)]
		if !ok {
			panic(&PyError{"KeyError", pyjson.ReprString(strings.ToLower(v))})
		}
		return b
	case TagInt:
		return safeInt(c.scalarValue(n))
	case TagFloat:
		return safeFloat(c.scalarValue(n))
	case TagTS:
		c.scalarValue(n)
		if err := checkTimestamp(n.Value); err != nil {
			panic(err)
		}
		return constructTimestamp(n.Value)
	case TagStr:
		return c.scalarValue(n)
	case TagBinary:
		v := c.scalarValue(n)
		if err := asciiCheck(v); err != "" {
			c.err("", nil, "failed to convert base64 data into ascii: "+err, n.Start)
		}
		b, err := a2bBase64(v)
		if err != "" {
			c.err("", nil, "failed to decode base64 data: "+err, n.Start)
		}
		return b
	case TagSeq:
		box := &seqBox{}
		c.pending = append(c.pending, func() {
			if n.Kind != SequenceNode {
				c.err("", nil, "expected a sequence node, but found "+n.Kind.id(), n.Start)
			}
			for _, it := range n.Items {
				box.items = append(box.items, c.object(it))
			}
		})
		return box
	case TagMap:
		box := &mapBox{vals: map[string]any{}}
		c.pending = append(c.pending, func() { c.fillMapping(n, box) })
		return box
	case tagPrefix + "set":
		box := &mapBox{vals: map[string]any{}, set: true}
		c.pending = append(c.pending, func() { c.fillMapping(n, box) })
		return box
	case tagPrefix + "omap", tagPrefix + "pairs":
		box := &seqBox{}
		what := "an ordered map"
		if n.Tag == tagPrefix+"pairs" {
			what = "pairs"
		}
		c.pending = append(c.pending, func() {
			if n.Kind != SequenceNode {
				c.err("while constructing "+what, n.Start, "expected a sequence, but found "+n.Kind.id(), n.Start)
			}
			for _, sub := range n.Items {
				if sub.Kind != MappingNode {
					c.err("while constructing "+what, n.Start, "expected a mapping of length 1, but found "+sub.Kind.id(), sub.Start)
				}
				if len(sub.Pairs) != 1 {
					c.err("while constructing "+what, n.Start, fmt.Sprintf("expected a single mapping item, but found %d items", len(sub.Pairs)), sub.Start)
				}
				k := c.object(sub.Pairs[0][0])
				v := c.object(sub.Pairs[0][1])
				box.items = append(box.items, &seqBox{items: []any{k, v}})
			}
		})
		return box
	}
	c.err("", nil, "could not determine a constructor for the tag "+reprS(n.Tag), n.Start)
	return nil
}

var boolValues = map[string]bool{"yes": true, "no": false, "true": true, "false": false, "on": true, "off": false}

func (c *constructor) flatten(n *Node) {
	var merge [][2]*Node
	for i := 0; i < len(n.Pairs); {
		k, v := n.Pairs[i][0], n.Pairs[i][1]
		switch k.Tag {
		case TagMerge:
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
		n.Pairs = append(merge, n.Pairs...)
	}
}

func (c *constructor) fillMapping(n *Node, box *mapBox) {
	if n.Kind == MappingNode {
		c.flatten(n)
	}
	if n.Kind != MappingNode {
		c.err("", nil, "expected a mapping node, but found "+n.Kind.id(), n.Start)
	}
	for _, kv := range n.Pairs {
		k := c.object(kv[0])
		switch k.(type) {
		case *seqBox, *mapBox:
			c.err("while constructing a mapping", n.Start, "found unhashable key", kv[0].Start)
		}
		v := c.object(kv[1])
		key := keyString(k)
		if _, ok := box.vals[key]; !ok {
			box.keys = append(box.keys, key)
		}
		box.vals[key] = v
	}
}

// ---------------------------------------------------------------- scalars

// asciiCheck is str.encode('ascii')'s UnicodeEncodeError text ("" = ok).
func asciiCheck(s string) string {
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] < 0x80 {
			continue
		}
		j := i
		for j+1 < len(r) && r[j+1] >= 0x80 {
			j++
		}
		if j == i {
			return fmt.Sprintf("'ascii' codec can't encode character %s in position %d: ordinal not in range(128)", pyjson.ReprString(string(r[i])), i)
		}
		return fmt.Sprintf("'ascii' codec can't encode characters in position %d-%d: ordinal not in range(128)", i, j)
	}
	return ""
}

// a2bBase64 is binascii.a2b_base64(strict_mode=False) ("" = ok).
func a2bBase64(s string) ([]byte, string) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := []byte{}
	quad, pads := 0, 0
	var left byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			if quad >= 2 {
				pads++
			}
			continue
		}
		v := strings.IndexByte(alphabet, c)
		if v < 0 {
			continue
		}
		pads = 0
		b := byte(v)
		switch quad {
		case 0:
			quad, left = 1, b
		case 1:
			quad = 2
			out = append(out, left<<2|b>>4)
			left = b & 0x0f
		case 2:
			quad = 3
			out = append(out, left<<4|b>>2)
			left = b & 0x03
		case 3:
			quad = 0
			out = append(out, left<<6|b)
			left = 0
		}
	}
	switch quad {
	case 0:
		return out, ""
	case 1:
		return nil, fmt.Sprintf("Invalid base64-encoded string: number of data characters (%d) cannot be 1 more than a multiple of 4", len(out)/3*4+1)
	}
	if quad+pads >= 4 {
		return out, ""
	}
	return nil, "Incorrect padding"
}

func pyStrip(s string) string { return strings.TrimFunc(s, unicode.IsSpace) }

// pyInt is int(s, base) (s already free of underscores).
func pyInt(s string, base int) (*big.Int, bool) {
	t := pyStrip(s)
	neg := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	lt := strings.ToLower(t)
	switch {
	case base == 16 && strings.HasPrefix(lt, "0x"), base == 8 && strings.HasPrefix(lt, "0o"), base == 2 && strings.HasPrefix(lt, "0b"):
		t = t[2:]
	}
	if t == "" {
		return nil, false
	}
	var digits strings.Builder
	for _, r := range t {
		if unicode.IsDigit(r) {
			d := r - '0'
			if r > '9' {
				d = rune(unicodeDigit(r))
			}
			if int(d) >= base {
				return nil, false
			}
			digits.WriteRune('0' + d)
			continue
		}
		if base > 10 {
			lr := unicode.ToLower(r)
			if lr >= 'a' && int(lr-'a')+10 < base {
				digits.WriteRune(lr)
				continue
			}
		}
		return nil, false
	}
	n, ok := new(big.Int).SetString(digits.String(), base)
	if !ok {
		return nil, false
	}
	if neg {
		n.Neg(n)
	}
	return n, true
}

func unicodeDigit(r rune) int {
	// Unicode decimal digits come in contiguous runs of ten starting at 0.
	for z := r; z >= r-9; z-- {
		if !unicode.IsDigit(z) {
			return int(r - z - 1)
		}
	}
	return int(r - (r - 9))
}

func intResult(n *big.Int) any {
	if n.IsInt64() {
		return n.Int64()
	}
	return n
}

// safeInt is SafeConstructor.construct_yaml_int, with int()'s ValueError.
func safeInt(value string) any {
	v := strings.ReplaceAll(value, "_", "")
	if v == "" {
		panic(&PyError{"IndexError", "string index out of range"})
	}
	sign := big.NewInt(1)
	if v[0] == '-' {
		sign = big.NewInt(-1)
	}
	if v[0] == '+' || v[0] == '-' {
		v = v[1:]
	}
	conv := func(s string, base int) *big.Int {
		n, ok := pyInt(s, base)
		if !ok {
			panic(valueError("invalid literal for int() with base %d: %s", base, pyjson.ReprString(s)))
		}
		return n
	}
	var n *big.Int
	switch {
	case v == "0":
		return int64(0)
	case strings.HasPrefix(v, "0b"):
		n = conv(v[2:], 2)
	case strings.HasPrefix(v, "0x"):
		n = conv(v[2:], 16)
	case v != "" && v[0] == '0':
		n = conv(v, 8)
	case strings.Contains(v, ":"):
		parts := strings.Split(v, ":")
		total := new(big.Int)
		base := big.NewInt(1)
		for i := len(parts) - 1; i >= 0; i-- {
			d := conv(parts[i], 10)
			total.Add(total, new(big.Int).Mul(d, base))
			base.Mul(base, big.NewInt(60))
		}
		n = total
	default:
		if v == "" {
			panic(valueError("invalid literal for int() with base 10: ''"))
		}
		n = conv(v, 10)
	}
	return intResult(n.Mul(n, sign))
}

// pyFloat is float(s).
func pyFloat(s string) (float64, bool) {
	t := pyStrip(s)
	switch strings.ToLower(strings.TrimLeft(t, "+-")) {
	case "inf", "infinity", "nan":
		if strings.Count(t[:len(t)-len(strings.TrimLeft(t, "+-"))], "") > 2 {
			return 0, false
		}
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	if t == "" || strings.ContainsAny(t, "xXpP_") {
		return 0, false
	}
	for _, r := range t {
		if !(('0' <= r && r <= '9') || r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-') {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// safeFloat is SafeConstructor.construct_yaml_float.
func safeFloat(value string) any {
	v := strings.ToLower(strings.ReplaceAll(value, "_", ""))
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
	conv := func(s string) float64 {
		f, ok := pyFloat(s)
		if !ok {
			panic(valueError("could not convert string to float: %s", pyjson.ReprString(s)))
		}
		return f
	}
	switch {
	case v == ".inf":
		return sign * math.Inf(1)
	case v == ".nan":
		return math.NaN()
	case strings.Contains(v, ":"):
		parts := strings.Split(v, ":")
		val, base := 0.0, 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			val += conv(parts[i]) * base
			base *= 60
		}
		return sign * val
	}
	return sign * conv(v)
}

// checkTimestamp reports datetime's ValueError for an out-of-range
// implicit timestamp.
func checkTimestamp(v string) error {
	m := reTSParts.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	if y < 1 {
		return valueError("year %d is out of range", y)
	}
	if mo < 1 || mo > 12 {
		return valueError("month must be in 1..12")
	}
	dim := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[mo-1]
	if mo == 2 && (y%4 == 0 && (y%100 != 0 || y%400 == 0)) {
		dim = 29
	}
	if d < 1 || d > dim {
		return valueError("day is out of range for month")
	}
	if m[4] != "" {
		if atoi(m[4]) > 23 {
			return valueError("hour must be in 0..23")
		}
		if atoi(m[5]) > 59 {
			return valueError("minute must be in 0..59")
		}
		if atoi(m[6]) > 59 {
			return valueError("second must be in 0..59")
		}
		if m[10] != "" {
			th, tm := atoi(m[10]), atoi(m[11])
			if th*60+tm >= 24*60 {
				return valueError("offset must be a timedelta strictly between -timedelta(hours=24) and timedelta(hours=24), not %s.", pyTimedeltaRepr(m[9] == "-", th, tm))
			}
		}
	}
	return nil
}

func pyTimedeltaRepr(neg bool, h, m int) string {
	secs := h*3600 + m*60
	if neg {
		secs = -secs
	}
	days := 0
	if secs < 0 {
		days = -1 + (secs+1)/86400
		secs -= days * 86400
	} else {
		days = secs / 86400
		secs %= 86400
	}
	var parts []string
	if days != 0 {
		parts = append(parts, fmt.Sprintf("days=%d", days))
	}
	if secs != 0 {
		parts = append(parts, fmt.Sprintf("seconds=%d", secs))
	}
	if len(parts) == 0 {
		return "datetime.timedelta(0)"
	}
	return "datetime.timedelta(" + strings.Join(parts, ", ") + ")"
}

// Load is yaml.safe_load(src) for a Python str: the PyYAML 6 scanner,
// parser, composer and SafeConstructor, with PyYAML's exact errors
// (*MarkedYAMLError, *ReaderError, or *PyError for non-YAML exceptions).
func Load(src string) (any, error) {
	var out any
	err := run(func() {
		n := (&composer{parser: newParser(src, PyYAML), anchors: map[string]*Node{}, resolve: resolve11}).getSingleNode()
		if n == nil {
			return
		}
		c := &constructor{dialect: PyYAML, constructed: map[*Node]any{}}
		out = c.document(n)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
