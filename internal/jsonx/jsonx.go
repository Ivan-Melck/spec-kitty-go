// Package jsonx provides insertion-ordered JSON objects and an encoder that
// reproduces Python's json.dumps byte-for-byte (indent, sort_keys,
// ensure_ascii, separators), so files written by the Go port are identical to
// files written by the upstream CLI.
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// KV is one member of an ordered object.
type KV struct {
	Key   string
	Value any
}

// Object is an insertion-ordered JSON object (a Python dict).
type Object struct {
	kvs []KV
	idx map[string]int
}

// NewObject builds an object from alternating key/value pairs.
func NewObject(pairs ...any) *Object {
	o := &Object{}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

func (o *Object) ensure() {
	if o.idx == nil {
		o.idx = map[string]int{}
		for i, kv := range o.kvs {
			o.idx[kv.Key] = i
		}
	}
}

// Set inserts or replaces key (replacement keeps the original position).
func (o *Object) Set(key string, v any) *Object {
	o.ensure()
	if i, ok := o.idx[key]; ok {
		o.kvs[i].Value = v
		return o
	}
	o.idx[key] = len(o.kvs)
	o.kvs = append(o.kvs, KV{key, v})
	return o
}

// Get returns the value for key.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	o.ensure()
	i, ok := o.idx[key]
	if !ok {
		return nil, false
	}
	return o.kvs[i].Value, true
}

// Has reports whether key exists.
func (o *Object) Has(key string) bool { _, ok := o.Get(key); return ok }

// GetString returns key as a string ("" if absent or not a string).
func (o *Object) GetString(key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// GetObject returns key as an object (nil if absent or not an object).
func (o *Object) GetObject(key string) *Object {
	v, _ := o.Get(key)
	m, _ := v.(*Object)
	return m
}

// GetList returns key as a list.
func (o *Object) GetList(key string) []any {
	v, _ := o.Get(key)
	l, _ := v.([]any)
	return l
}

// GetBool returns key as a bool.
func (o *Object) GetBool(key string) bool {
	v, _ := o.Get(key)
	b, _ := v.(bool)
	return b
}

// GetInt returns key as an int (accepting json numbers and floats).
func (o *Object) GetInt(key string) (int, bool) {
	v, ok := o.Get(key)
	if !ok {
		return 0, false
	}
	return toInt(v)
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == math.Trunc(n)
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// Delete removes key.
func (o *Object) Delete(key string) {
	o.ensure()
	i, ok := o.idx[key]
	if !ok {
		return
	}
	o.kvs = append(o.kvs[:i], o.kvs[i+1:]...)
	o.idx = nil
}

// Keys returns keys in insertion order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	ks := make([]string, len(o.kvs))
	for i, kv := range o.kvs {
		ks[i] = kv.Key
	}
	return ks
}

// Pairs returns the members in insertion order.
func (o *Object) Pairs() []KV {
	if o == nil {
		return nil
	}
	return o.kvs
}

// Len returns the member count.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.kvs)
}

// Clone returns a deep copy.
func (o *Object) Clone() *Object {
	if o == nil {
		return nil
	}
	c := &Object{}
	for _, kv := range o.kvs {
		c.Set(kv.Key, cloneValue(kv.Value))
	}
	return c
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case *Object:
		return t.Clone()
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = cloneValue(x)
		}
		return out
	}
	return v
}

// MarshalJSON lets *Object participate in encoding/json.
func (o *Object) MarshalJSON() ([]byte, error) {
	return Marshal(o, Options{Indent: -1, Compact: true, EnsureASCII: false})
}

// UnmarshalJSON decodes preserving key order.
func (o *Object) UnmarshalJSON(b []byte) error {
	v, err := Decode(b)
	if err != nil {
		return err
	}
	m, ok := v.(*Object)
	if !ok {
		return fmt.Errorf("jsonx: not an object")
	}
	*o = *m
	return nil
}

// Options mirrors json.dumps keyword arguments.
type Options struct {
	Indent      int  // <0 means no newlines (Python indent=None)
	SortKeys    bool // sort_keys=True
	EnsureASCII bool // ensure_ascii (Python default True)
	Compact     bool // separators=(",", ":") ; otherwise (", ", ": ")
}

// Pretty is json.dumps(x, indent=2) (ensure_ascii=True).
var Pretty = Options{Indent: 2, EnsureASCII: true}

// PrettyUTF8 is json.dumps(x, indent=2, ensure_ascii=False).
var PrettyUTF8 = Options{Indent: 2}

// SortedPretty is json.dumps(x, indent=2, sort_keys=True).
var SortedPretty = Options{Indent: 2, SortKeys: true, EnsureASCII: true}

// Line is json.dumps(x, sort_keys=True) (one line, ", " separators).
var Line = Options{Indent: -1, SortKeys: true, EnsureASCII: true}

// Dumps encodes v with opts.
func Dumps(v any, opts Options) string {
	b, err := Marshal(v, opts)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Marshal encodes v with opts.
func Marshal(v any, opts Options) ([]byte, error) {
	var buf bytes.Buffer
	e := encoder{opts: opts, buf: &buf}
	if err := e.encode(normalize(v), 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type encoder struct {
	opts Options
	buf  *bytes.Buffer
}

func (e *encoder) newline(level int) {
	if e.opts.Indent < 0 {
		return
	}
	e.buf.WriteByte('\n')
	e.buf.WriteString(strings.Repeat(" ", e.opts.Indent*level))
}

func (e *encoder) itemSep() {
	if e.opts.Indent >= 0 || e.opts.Compact {
		e.buf.WriteByte(',')
	} else {
		e.buf.WriteString(", ")
	}
}

func (e *encoder) keySep() {
	if e.opts.Compact {
		e.buf.WriteByte(':')
	} else {
		e.buf.WriteString(": ")
	}
}

func (e *encoder) encode(v any, level int) error {
	switch t := v.(type) {
	case nil:
		e.buf.WriteString("null")
	case bool:
		if t {
			e.buf.WriteString("true")
		} else {
			e.buf.WriteString("false")
		}
	case string:
		e.buf.WriteString(QuoteString(t, e.opts.EnsureASCII))
	case int:
		e.buf.WriteString(strconv.Itoa(t))
	case int64:
		e.buf.WriteString(strconv.FormatInt(t, 10))
	case uint64:
		e.buf.WriteString(strconv.FormatUint(t, 10))
	case json.Number:
		e.buf.WriteString(t.String())
	case float64:
		e.buf.WriteString(FormatFloat(t))
	case Float:
		e.buf.WriteString(FormatFloat(float64(t)))
	case *Object:
		if t == nil {
			e.buf.WriteString("null")
			return nil
		}
		kvs := t.kvs
		if e.opts.SortKeys {
			kvs = append([]KV(nil), kvs...)
			sort.SliceStable(kvs, func(i, j int) bool { return kvs[i].Key < kvs[j].Key })
		}
		if len(kvs) == 0 {
			e.buf.WriteString("{}")
			return nil
		}
		e.buf.WriteByte('{')
		for i, kv := range kvs {
			if i > 0 {
				e.itemSep()
			}
			e.newline(level + 1)
			e.buf.WriteString(QuoteString(kv.Key, e.opts.EnsureASCII))
			e.keySep()
			if err := e.encode(normalize(kv.Value), level+1); err != nil {
				return err
			}
		}
		e.newline(level)
		e.buf.WriteByte('}')
	case []any:
		if len(t) == 0 {
			e.buf.WriteString("[]")
			return nil
		}
		e.buf.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				e.itemSep()
			}
			e.newline(level + 1)
			if err := e.encode(normalize(x), level+1); err != nil {
				return err
			}
		}
		e.newline(level)
		e.buf.WriteByte(']')
	default:
		return fmt.Errorf("jsonx: unsupported type %T", v)
	}
	return nil
}

// Float forces Python float rendering (1.0 instead of 1).
type Float float64

// FormatFloat renders like Python repr(float).
func FormatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if math.IsNaN(f) {
		return "NaN"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	// Python repr: shortest round-trip digits, fixed notation when the
	// decimal exponent is in [-4, 16), scientific otherwise.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	x, _ := strconv.Atoi(exp)
	if x >= -4 && x < 16 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	sign := exp[0]
	digits := strings.TrimLeft(exp[1:], "0")
	if len(digits) < 2 {
		digits = strings.Repeat("0", 2-len(digits)) + digits
	}
	return mant + "e" + string(sign) + digits
}

// QuoteString renders a JSON string the way Python does.
func QuoteString(s string, ensureASCII bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 || (ensureASCII && r > 0x7e) {
				if r == utf8.RuneError && size == 1 {
					r = 0xfffd
				}
				if r > 0xffff {
					r1, r2 := utf16.EncodeRune(r)
					fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// normalize converts Go values into the encoder's value space.
func normalize(v any) any {
	switch t := v.(type) {
	case nil, bool, string, int, int64, uint64, float64, json.Number, *Object, []any, Float:
		return v
	case Object:
		return &t
	case int32:
		return int(t)
	case int8:
		return int(t)
	case int16:
		return int(t)
	case uint:
		return uint64(t)
	case uint32:
		return uint64(t)
	case float32:
		return float64(t)
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case []*Object:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := &Object{}
		for _, k := range keys {
			o.Set(k, t[k])
		}
		return o
	case map[string]string:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		o := &Object{}
		for _, k := range keys {
			o.Set(k, t[k])
		}
		return o
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return []any{}
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out
	}
	// Structs and other types: round-trip through encoding/json preserving
	// field order.
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	d, err := Decode(b)
	if err != nil {
		panic(err)
	}
	return d
}

// Decode parses JSON preserving object key order. Numbers become int when
// integral and representable, json.Number otherwise preserved as float64.
func Decode(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("jsonx: trailing data")
		}
		return nil, err
	}
	return v, nil
}

// DecodeObject parses a JSON object.
func DecodeObject(b []byte) (*Object, error) {
	v, err := Decode(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, fmt.Errorf("jsonx: expected object")
	}
	return o, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &Object{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			l := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				l = append(l, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return l, nil
		}
	case json.Number:
		s := t.String()
		if !strings.ContainsAny(s, ".eE") {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				if i == int64(int(i)) {
					return int(i), nil
				}
				return i, nil
			}
			return t, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return t, nil
		}
		return f, nil
	default:
		return tok, nil
	}
	return nil, fmt.Errorf("jsonx: unexpected token %v", tok)
}

// ToObject converts arbitrary values (structs, maps) to an ordered object.
func ToObject(v any) *Object {
	n := normalize(v)
	o, _ := n.(*Object)
	return o
}
