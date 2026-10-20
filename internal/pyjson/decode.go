// Package pyjson reproduces the observable behaviour of CPython's json module
// and a few builtins over decoded JSON values:
//
//   - Loads is json.loads: same accepted grammar (NaN/Infinity, duplicate
//     keys keep the first position with the last value) and byte-identical
//     JSONDecodeError messages ("Expecting value: line 1 column 1 (char 0)").
//   - Repr / Str render values like repr() / str().
//   - Equal is Python == (1 == 1.0 == True, order-insensitive dicts).
//   - Truthy is bool(x).
//
// Values use the jsonx value space: nil, bool, string, int, json.Number (ints
// outside the int64 range), float64, *jsonx.Object and []any, so they can be
// re-encoded with jsonx.Dumps (json.dumps).
package pyjson

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// DecodeError is json.JSONDecodeError. Error() returns str(exc).
type DecodeError struct {
	Msg    string // bare message, e.g. "Expecting value"
	Pos    int    // character (code point) offset
	Lineno int
	Colno  int
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.Msg, e.Lineno, e.Colno, e.Pos)
}

func newErr(msg string, doc []rune, pos int) *DecodeError {
	line := 1
	last := -1
	for i := 0; i < pos && i < len(doc); i++ {
		if doc[i] == '\n' {
			line++
			last = i
		}
	}
	return &DecodeError{Msg: msg, Pos: pos, Lineno: line, Colno: pos - last}
}

// Loads parses s like json.loads(s) for a str argument.
func Loads(s string) (any, error) {
	doc := []rune(s)
	if len(doc) > 0 && doc[0] == 0xfeff {
		return nil, newErr("Unexpected UTF-8 BOM (decode using utf-8-sig)", doc, 0)
	}
	d := &decoder{s: doc}
	idx := d.ws(0)
	v, end, err := d.scan(idx)
	if err != nil {
		return nil, err
	}
	end = d.ws(end)
	if end != len(doc) {
		return nil, newErr("Extra data", doc, end)
	}
	return v, nil
}

// LoadsBytes decodes b as UTF-8 (like Path.read_text) and then parses it.
// A non-UTF-8 input returns a *UnicodeError.
func LoadsBytes(b []byte) (any, error) {
	s, err := DecodeUTF8(b)
	if err != nil {
		return nil, err
	}
	return Loads(s)
}

type decoder struct {
	s []rune
}

func isWS(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func (d *decoder) ws(i int) int {
	for i < len(d.s) && isWS(d.s[i]) {
		i++
	}
	return i
}

// stop mirrors StopIteration(idx) -> "Expecting value".
func (d *decoder) stop(idx int) error { return newErr("Expecting value", d.s, idx) }

func (d *decoder) has(idx int, lit string) bool {
	r := []rune(lit)
	if idx+len(r) > len(d.s) {
		return false
	}
	for i, c := range r {
		if d.s[idx+i] != c {
			return false
		}
	}
	return true
}

func (d *decoder) scan(idx int) (any, int, error) {
	if idx >= len(d.s) {
		return nil, 0, d.stop(idx)
	}
	switch d.s[idx] {
	case '"':
		return d.str(idx + 1)
	case '{':
		return d.object(idx + 1)
	case '[':
		return d.array(idx + 1)
	case 'n':
		if d.has(idx, "null") {
			return nil, idx + 4, nil
		}
	case 't':
		if d.has(idx, "true") {
			return true, idx + 4, nil
		}
	case 'f':
		if d.has(idx, "false") {
			return false, idx + 5, nil
		}
	case 'N':
		if d.has(idx, "NaN") {
			return math.NaN(), idx + 3, nil
		}
	case 'I':
		if d.has(idx, "Infinity") {
			return math.Inf(1), idx + 8, nil
		}
	case '-':
		if d.has(idx, "-Infinity") {
			return math.Inf(-1), idx + 9, nil
		}
	}
	return d.number(idx)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func (d *decoder) number(start int) (any, int, error) {
	s := d.s
	end := len(s) - 1
	idx := start
	if s[idx] == '-' {
		idx++
		if idx > end {
			return nil, 0, d.stop(start)
		}
	}
	if s[idx] >= '1' && s[idx] <= '9' {
		idx++
		for idx <= end && isDigit(s[idx]) {
			idx++
		}
	} else if s[idx] == '0' {
		idx++
	} else {
		return nil, 0, d.stop(start)
	}
	isFloat := false
	if idx < end && s[idx] == '.' && isDigit(s[idx+1]) {
		isFloat = true
		idx += 2
		for idx <= end && isDigit(s[idx]) {
			idx++
		}
	}
	if idx < end && (s[idx] == 'e' || s[idx] == 'E') {
		eStart := idx
		idx++
		if idx < end && (s[idx] == '-' || s[idx] == '+') {
			idx++
		}
		for idx <= end && isDigit(s[idx]) {
			idx++
		}
		if isDigit(s[idx-1]) {
			isFloat = true
		} else {
			idx = eStart
		}
	}
	lit := string(s[start:idx])
	if isFloat {
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			// Overflow: Python float() yields +/-inf, underflow 0.0.
			if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
				return f, idx, nil
			}
		}
		return f, idx, nil
	}
	if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
		if i == int64(int(i)) {
			return int(i), idx, nil
		}
		return i, idx, nil
	}
	return json.Number(lit), idx, nil
}

func hexVal(r rune) (rune, bool) {
	switch {
	case r >= '0' && r <= '9':
		return r - '0', true
	case r >= 'a' && r <= 'f':
		return r - 'a' + 10, true
	case r >= 'A' && r <= 'F':
		return r - 'A' + 10, true
	}
	return 0, false
}

// str scans a string body starting after the opening quote (strict mode).
func (d *decoder) str(end int) (any, int, error) {
	s := d.s
	n := len(s)
	begin := end - 1
	var b strings.Builder
	for {
		next := end
		var c rune
		for ; next < n; next++ {
			c = s[next]
			if c == '"' || c == '\\' {
				break
			}
			if c <= 0x1f {
				return nil, 0, newErr("Invalid control character at", s, next)
			}
		}
		if next >= n {
			return nil, 0, newErr("Unterminated string starting at", s, begin)
		}
		b.WriteString(string(s[end:next]))
		if c == '"' {
			return b.String(), next + 1, nil
		}
		next++ // skip backslash
		if next == n {
			return nil, 0, newErr("Unterminated string starting at", s, begin)
		}
		c = s[next]
		if c != 'u' {
			end = next + 1
			switch c {
			case '"', '\\', '/':
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			default:
				return nil, 0, newErr("Invalid \\escape", s, end-2)
			}
			b.WriteRune(c)
			continue
		}
		c = 0
		next++
		end = next + 4
		if end >= n {
			return nil, 0, newErr("Invalid \\uXXXX escape", s, next-1)
		}
		for ; next < end; next++ {
			h, ok := hexVal(s[next])
			if !ok {
				return nil, 0, newErr("Invalid \\uXXXX escape", s, end-5)
			}
			c = c<<4 | h
		}
		if c >= 0xd800 && c <= 0xdbff && end+6 < n && s[next] == '\\' && s[next+1] == 'u' {
			next += 2
			var c2 rune
			end += 6
			for ; next < end; next++ {
				h, ok := hexVal(s[next])
				if !ok {
					return nil, 0, newErr("Invalid \\uXXXX escape", s, end-5)
				}
				c2 = c2<<4 | h
			}
			if c2 >= 0xdc00 && c2 <= 0xdfff {
				c = 0x10000 + (c-0xd800)<<10 + (c2 - 0xdc00)
			} else {
				end -= 6
			}
		}
		if c >= 0xd800 && c <= 0xdfff {
			// Lone surrogate: not representable in a Go UTF-8 string.
			c = utf8.RuneError
		}
		b.WriteRune(c)
	}
}

func (d *decoder) object(idx int) (any, int, error) {
	s := d.s
	end := len(s) - 1
	o := &jsonx.Object{}
	idx = d.ws(idx)
	if idx > end || s[idx] != '}' {
		for {
			if idx > end || s[idx] != '"' {
				return nil, 0, newErr("Expecting property name enclosed in double quotes", s, idx)
			}
			k, next, err := d.str(idx + 1)
			if err != nil {
				return nil, 0, err
			}
			idx = d.ws(next)
			if idx > end || s[idx] != ':' {
				return nil, 0, newErr("Expecting ':' delimiter", s, idx)
			}
			idx = d.ws(idx + 1)
			v, next, err := d.scan(idx)
			if err != nil {
				return nil, 0, err
			}
			o.Set(k.(string), v)
			idx = d.ws(next)
			if idx <= end && s[idx] == '}' {
				break
			}
			if idx > end || s[idx] != ',' {
				return nil, 0, newErr("Expecting ',' delimiter", s, idx)
			}
			comma := idx
			idx = d.ws(idx + 1)
			if idx <= end && s[idx] == '}' {
				return nil, 0, newErr("Illegal trailing comma before end of object", s, comma)
			}
		}
	}
	return o, idx + 1, nil
}

func (d *decoder) array(idx int) (any, int, error) {
	s := d.s
	end := len(s) - 1
	l := []any{}
	idx = d.ws(idx)
	if idx > end || s[idx] != ']' {
		for {
			v, next, err := d.scan(idx)
			if err != nil {
				return nil, 0, err
			}
			l = append(l, v)
			idx = d.ws(next)
			if idx <= end && s[idx] == ']' {
				break
			}
			if idx > end || s[idx] != ',' {
				return nil, 0, newErr("Expecting ',' delimiter", s, idx)
			}
			comma := idx
			idx = d.ws(idx + 1)
			if idx <= end && s[idx] == ']' {
				return nil, 0, newErr("Illegal trailing comma before end of array", s, comma)
			}
		}
	}
	return l, idx + 1, nil
}
