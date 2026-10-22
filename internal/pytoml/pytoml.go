// Package pytoml is a port of CPython 3.13's tomllib (tomllib.loads): the
// same acceptance rules and the same TOMLDecodeError messages
// ("<msg> (at line L, column C)" / "(at end of document)").
//
// Values: *jsonx.Object (tables, insertion ordered), []any, string, bool,
// int64 (*big.Int when it overflows), float64, Date, Time, DateTime.
package pytoml

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// DecodeError is tomllib.TOMLDecodeError (a ValueError subclass).
type DecodeError struct{ Msg string }

func (e *DecodeError) Error() string { return e.Msg }

// Repr is repr(exc): "TOMLDecodeError('...')".
func (e *DecodeError) Repr() string { return "TOMLDecodeError(" + pyjson.ReprString(e.Msg) + ")" }

// Date is datetime.date.
type Date struct{ Year, Month, Day int }

// ISO is date.isoformat().
func (d Date) ISO() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// Time is datetime.time (local time).
type Time struct{ Hour, Minute, Second, Micro int }

// ISO is time.isoformat().
func (t Time) ISO() string {
	s := fmt.Sprintf("%02d:%02d:%02d", t.Hour, t.Minute, t.Second)
	if t.Micro != 0 {
		s += fmt.Sprintf(".%06d", t.Micro)
	}
	return s
}

// DateTime is datetime.datetime; Offset (minutes east of UTC) is nil for a
// local date-time.
type DateTime struct {
	Date
	Time
	Offset *int
}

// ISO is datetime.isoformat().
func (d DateTime) ISO() string {
	s := d.Date.ISO() + "T" + d.Time.ISO()
	if d.Offset != nil {
		off := *d.Offset
		sign := "+"
		if off < 0 {
			sign, off = "-", -off
		}
		s += fmt.Sprintf("%s%02d:%02d", sign, off/60, off%60)
	}
	return s
}

// Loads is tomllib.loads(s).
func Loads(s string) (obj *jsonx.Object, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*DecodeError); ok {
				obj, err = nil, e
				return
			}
			panic(r)
		}
	}()
	return loads(s), nil
}

// ---------------------------------------------------------------- parser

type key []string

func (k key) repr() string {
	parts := make([]string, len(k))
	for i, p := range k {
		parts[i] = pyjson.ReprString(p)
	}
	if len(k) == 1 {
		return "(" + parts[0] + ",)"
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func concat(a, b key) key {
	out := make(key, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

type parser struct {
	src []rune
}

func (p *parser) at(pos int) (rune, bool) {
	if pos >= 0 && pos < len(p.src) {
		return p.src[pos], true
	}
	return 0, false
}

func (p *parser) startsWith(s string, pos int) bool {
	r := []rune(s)
	if pos < 0 || pos+len(r) > len(p.src) {
		return false
	}
	for i, c := range r {
		if p.src[pos+i] != c {
			return false
		}
	}
	return true
}

func (p *parser) slice(a, b int) string {
	a = max(0, min(a, len(p.src)))
	b = max(a, min(b, len(p.src)))
	return string(p.src[a:b])
}

// errAt is suffixed_err.
func (p *parser) errAt(pos int, msg string) *DecodeError {
	coord := "end of document"
	if pos < len(p.src) {
		line := 1
		last := -1
		for i := 0; i < pos; i++ {
			if p.src[i] == '\n' {
				line++
				last = i
			}
		}
		col := pos + 1
		if line != 1 {
			col = pos - last
		}
		coord = fmt.Sprintf("line %d, column %d", line, col)
	}
	return &DecodeError{fmt.Sprintf("%s (at %s)", msg, coord)}
}

func isASCIICtrl(r rune) bool { return r < 32 || r == 127 }

func illegalBasic(r rune) bool     { return isASCIICtrl(r) && r != '\t' }
func illegalMultiline(r rune) bool { return isASCIICtrl(r) && r != '\t' && r != '\n' }
func isWS(r rune) bool             { return r == ' ' || r == '\t' }
func isWSNL(r rune) bool           { return r == ' ' || r == '\t' || r == '\n' }
func isBareKey(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') || r == '-' || r == '_'
}
func isHexDigit(r rune) bool {
	return ('0' <= r && r <= '9') || ('a' <= r && r <= 'f') || ('A' <= r && r <= 'F')
}

func (p *parser) skip(pos int, pred func(rune) bool) int {
	for pos < len(p.src) && pred(p.src[pos]) {
		pos++
	}
	return pos
}

func (p *parser) index(sub string, pos int) int {
	r := []rune(sub)
	for i := max(pos, 0); i+len(r) <= len(p.src); i++ {
		match := true
		for j, c := range r {
			if p.src[i+j] != c {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// skipUntil is skip_until.
func (p *parser) skipUntil(pos int, expect string, errorOn func(rune) bool, errorOnEOF bool) int {
	newPos := p.index(expect, pos)
	if newPos < 0 {
		newPos = len(p.src)
		if errorOnEOF {
			panic(p.errAt(newPos, "Expected "+pyjson.ReprString(expect)))
		}
	}
	for i := pos; i < newPos; i++ {
		if errorOn(p.src[i]) {
			panic(p.errAt(i, "Found invalid character "+pyjson.ReprString(string(p.src[i]))))
		}
	}
	return newPos
}

func (p *parser) skipComment(pos int) int {
	if c, ok := p.at(pos); ok && c == '#' {
		return p.skipUntil(pos+1, "\n", illegalBasic, false)
	}
	return pos
}

func (p *parser) skipCommentsAndArrayWS(pos int) int {
	for {
		before := pos
		pos = p.skip(pos, isWSNL)
		pos = p.skipComment(pos)
		if pos == before {
			return pos
		}
	}
}

func loads(s string) *jsonx.Object {
	p := &parser{src: []rune(strings.ReplaceAll(s, "\r\n", "\n"))}
	out := &output{data: &jsonx.Object{}, flags: newFlags()}
	var header key
	pos := 0
	for {
		pos = p.skip(pos, isWS)
		c, ok := p.at(pos)
		if !ok {
			break
		}
		if c == '\n' {
			pos++
			continue
		}
		switch {
		case isBareKey(c) || c == '"' || c == '\'':
			pos = p.keyValueRule(pos, out, header)
			pos = p.skip(pos, isWS)
		case c == '[':
			second, _ := p.at(pos + 1)
			out.flags.finalizePending()
			if second == '[' {
				pos, header = p.createListRule(pos, out)
			} else {
				pos, header = p.createDictRule(pos, out)
			}
			pos = p.skip(pos, isWS)
		case c != '#':
			panic(p.errAt(pos, "Invalid statement"))
		}
		pos = p.skipComment(pos)
		c, ok = p.at(pos)
		if !ok {
			break
		}
		if c != '\n' {
			panic(p.errAt(pos, "Expected newline or end of document after a statement"))
		}
		pos++
	}
	return out.data
}

// ---------------------------------------------------------------- flags / nests

const (
	flagFrozen       = 0
	flagExplicitNest = 1
)

type flagNode struct {
	flags, recursive map[int]bool
	nested           map[string]*flagNode
}

func newFlagNode() *flagNode {
	return &flagNode{flags: map[int]bool{}, recursive: map[int]bool{}, nested: map[string]*flagNode{}}
}

type pendingFlag struct {
	k    key
	flag int
}

type flags struct {
	root    map[string]*flagNode
	pending []pendingFlag
}

func newFlags() *flags { return &flags{root: map[string]*flagNode{}} }

func (f *flags) addPending(k key, flag int) { f.pending = append(f.pending, pendingFlag{k, flag}) }

func (f *flags) finalizePending() {
	for _, pf := range f.pending {
		f.set(pf.k, pf.flag, false)
	}
	f.pending = nil
}

func (f *flags) unsetAll(k key) {
	cont := f.root
	for _, part := range k[:len(k)-1] {
		n, ok := cont[part]
		if !ok {
			return
		}
		cont = n.nested
	}
	delete(cont, k[len(k)-1])
}

func (f *flags) set(k key, flag int, recursive bool) {
	cont := f.root
	for _, part := range k[:len(k)-1] {
		if _, ok := cont[part]; !ok {
			cont[part] = newFlagNode()
		}
		cont = cont[part].nested
	}
	stem := k[len(k)-1]
	if _, ok := cont[stem]; !ok {
		cont[stem] = newFlagNode()
	}
	if recursive {
		cont[stem].recursive[flag] = true
	} else {
		cont[stem].flags[flag] = true
	}
}

func (f *flags) is(k key, flag int) bool {
	if len(k) == 0 {
		return false
	}
	cont := f.root
	for _, part := range k[:len(k)-1] {
		n, ok := cont[part]
		if !ok {
			return false
		}
		if n.recursive[flag] {
			return true
		}
		cont = n.nested
	}
	n, ok := cont[k[len(k)-1]]
	return ok && (n.flags[flag] || n.recursive[flag])
}

type output struct {
	data  *jsonx.Object
	flags *flags
}

type nestError struct{}

// getOrCreateNest is NestedDict.get_or_create_nest (ok=false: KeyError).
func getOrCreateNest(root *jsonx.Object, k key, accessLists bool) (*jsonx.Object, bool) {
	cont := root
	for _, part := range k {
		v, ok := cont.Get(part)
		if !ok {
			v = &jsonx.Object{}
			cont.Set(part, v)
		}
		if l, isList := v.([]any); accessLists && isList {
			v = l[len(l)-1]
		}
		o, isObj := v.(*jsonx.Object)
		if !isObj {
			return nil, false
		}
		cont = o
	}
	return cont, true
}

func appendNestToList(root *jsonx.Object, k key) bool {
	cont, ok := getOrCreateNest(root, k[:len(k)-1], true)
	if !ok {
		return false
	}
	last := k[len(k)-1]
	if v, ok := cont.Get(last); ok {
		l, isList := v.([]any)
		if !isList {
			return false
		}
		cont.Set(last, append(l, &jsonx.Object{}))
		return true
	}
	cont.Set(last, []any{&jsonx.Object{}})
	return true
}

// ---------------------------------------------------------------- rules

func (p *parser) createDictRule(pos int, out *output) (int, key) {
	pos++
	pos = p.skip(pos, isWS)
	pos, k := p.parseKey(pos)
	if out.flags.is(k, flagExplicitNest) || out.flags.is(k, flagFrozen) {
		panic(p.errAt(pos, fmt.Sprintf("Cannot declare %s twice", k.repr())))
	}
	out.flags.set(k, flagExplicitNest, false)
	if _, ok := getOrCreateNest(out.data, k, true); !ok {
		panic(p.errAt(pos, "Cannot overwrite a value"))
	}
	if !p.startsWith("]", pos) {
		panic(p.errAt(pos, "Expected ']' at the end of a table declaration"))
	}
	return pos + 1, k
}

func (p *parser) createListRule(pos int, out *output) (int, key) {
	pos += 2
	pos = p.skip(pos, isWS)
	pos, k := p.parseKey(pos)
	if out.flags.is(k, flagFrozen) {
		panic(p.errAt(pos, fmt.Sprintf("Cannot mutate immutable namespace %s", k.repr())))
	}
	out.flags.unsetAll(k)
	out.flags.set(k, flagExplicitNest, false)
	if !appendNestToList(out.data, k) {
		panic(p.errAt(pos, "Cannot overwrite a value"))
	}
	if !p.startsWith("]]", pos) {
		panic(p.errAt(pos, "Expected ']]' at the end of an array declaration"))
	}
	return pos + 2, k
}

func (p *parser) keyValueRule(pos int, out *output, header key) int {
	pos, k, value := p.parseKeyValuePair(pos)
	parent, stem := k[:len(k)-1], k[len(k)-1]
	absParent := concat(header, parent)
	for i := 1; i < len(k); i++ {
		contKey := concat(header, k[:i])
		if out.flags.is(contKey, flagExplicitNest) {
			panic(p.errAt(pos, fmt.Sprintf("Cannot redefine namespace %s", contKey.repr())))
		}
		out.flags.addPending(contKey, flagExplicitNest)
	}
	if out.flags.is(absParent, flagFrozen) {
		panic(p.errAt(pos, fmt.Sprintf("Cannot mutate immutable namespace %s", absParent.repr())))
	}
	nest, ok := getOrCreateNest(out.data, absParent, true)
	if !ok {
		panic(p.errAt(pos, "Cannot overwrite a value"))
	}
	if nest.Has(stem) {
		panic(p.errAt(pos, "Cannot overwrite a value"))
	}
	switch value.(type) {
	case *jsonx.Object, []any:
		out.flags.set(concat(header, k), flagFrozen, true)
	}
	nest.Set(stem, value)
	return pos
}

func (p *parser) parseKeyValuePair(pos int) (int, key, any) {
	pos, k := p.parseKey(pos)
	if c, ok := p.at(pos); !ok || c != '=' {
		panic(p.errAt(pos, "Expected '=' after a key in a key/value pair"))
	}
	pos++
	pos = p.skip(pos, isWS)
	pos, v := p.parseValue(pos)
	return pos, k, v
}

func (p *parser) parseKey(pos int) (int, key) {
	pos, part := p.parseKeyPart(pos)
	k := key{part}
	pos = p.skip(pos, isWS)
	for {
		if c, ok := p.at(pos); !ok || c != '.' {
			return pos, k
		}
		pos++
		pos = p.skip(pos, isWS)
		pos, part = p.parseKeyPart(pos)
		k = append(k, part)
		pos = p.skip(pos, isWS)
	}
}

func (p *parser) parseKeyPart(pos int) (int, string) {
	c, ok := p.at(pos)
	switch {
	case ok && isBareKey(c):
		start := pos
		pos = p.skip(pos, isBareKey)
		return pos, p.slice(start, pos)
	case ok && c == '\'':
		return p.parseLiteralStr(pos)
	case ok && c == '"':
		return p.parseBasicStr(pos+1, false)
	}
	panic(p.errAt(pos, "Invalid initial character for a key part"))
}

func (p *parser) parseArray(pos int) (int, []any) {
	pos++
	arr := []any{}
	pos = p.skipCommentsAndArrayWS(pos)
	if p.startsWith("]", pos) {
		return pos + 1, arr
	}
	for {
		var v any
		pos, v = p.parseValue(pos)
		arr = append(arr, v)
		pos = p.skipCommentsAndArrayWS(pos)
		c := p.slice(pos, pos+1)
		if c == "]" {
			return pos + 1, arr
		}
		if c != "," {
			panic(p.errAt(pos, "Unclosed array"))
		}
		pos++
		pos = p.skipCommentsAndArrayWS(pos)
		if p.startsWith("]", pos) {
			return pos + 1, arr
		}
	}
}

func (p *parser) parseInlineTable(pos int) (int, *jsonx.Object) {
	pos++
	nested := &jsonx.Object{}
	fl := newFlags()
	pos = p.skip(pos, isWS)
	if p.startsWith("}", pos) {
		return pos + 1, nested
	}
	for {
		var k key
		var v any
		pos, k, v = p.parseKeyValuePair(pos)
		parent, stem := k[:len(k)-1], k[len(k)-1]
		if fl.is(k, flagFrozen) {
			panic(p.errAt(pos, fmt.Sprintf("Cannot mutate immutable namespace %s", k.repr())))
		}
		nest, ok := getOrCreateNest(nested, parent, false)
		if !ok {
			panic(p.errAt(pos, "Cannot overwrite a value"))
		}
		if nest.Has(stem) {
			panic(p.errAt(pos, "Duplicate inline table key "+pyjson.ReprString(stem)))
		}
		nest.Set(stem, v)
		pos = p.skip(pos, isWS)
		c := p.slice(pos, pos+1)
		if c == "}" {
			return pos + 1, nested
		}
		if c != "," {
			panic(p.errAt(pos, "Unclosed inline table"))
		}
		switch v.(type) {
		case *jsonx.Object, []any:
			fl.set(k, flagFrozen, true)
		}
		pos++
		pos = p.skip(pos, isWS)
	}
}

var basicEscapes = map[string]string{
	`\b`: "\u0008", `\t`: "\u0009", `\n`: "\u000A", `\f`: "\u000C", `\r`: "\r", `\"`: `"`, `\\`: `\`,
}

func (p *parser) parseBasicStrEscape(pos int, multiline bool) (int, string) {
	id := p.slice(pos, pos+2)
	pos += 2
	if multiline && (id == "\\ " || id == "\\\t" || id == "\\\n") {
		if id != "\\\n" {
			pos = p.skip(pos, isWS)
			c, ok := p.at(pos)
			if !ok {
				return pos, ""
			}
			if c != '\n' {
				panic(p.errAt(pos, "Unescaped '\\' in a string"))
			}
			pos++
		}
		pos = p.skip(pos, isWSNL)
		return pos, ""
	}
	switch id {
	case `\u`:
		return p.parseHexChar(pos, 4)
	case `\U`:
		return p.parseHexChar(pos, 8)
	}
	if r, ok := basicEscapes[id]; ok {
		return pos, r
	}
	panic(p.errAt(pos, "Unescaped '\\' in a string"))
}

func (p *parser) parseHexChar(pos, n int) (int, string) {
	h := []rune(p.slice(pos, pos+n))
	valid := len(h) == n
	for _, c := range h {
		if !isHexDigit(c) {
			valid = false
		}
	}
	if !valid {
		panic(p.errAt(pos, "Invalid hex value"))
	}
	pos += n
	v, _ := strconv.ParseUint(string(h), 16, 64)
	if !(v <= 55295 || (57344 <= v && v <= 1114111)) {
		panic(p.errAt(pos, "Escaped character is not a Unicode scalar value"))
	}
	return pos, string(rune(v))
}

func (p *parser) parseLiteralStr(pos int) (int, string) {
	pos++
	start := pos
	pos = p.skipUntil(pos, "'", illegalBasic, true)
	return pos + 1, p.slice(start, pos)
}

func (p *parser) parseMultilineStr(pos int, literal bool) (int, string) {
	pos += 3
	if p.startsWith("\n", pos) {
		pos++
	}
	var result, delim string
	if literal {
		delim = "'"
		end := p.skipUntil(pos, "'''", illegalMultiline, true)
		result = p.slice(pos, end)
		pos = end + 3
	} else {
		delim = "\""
		pos, result = p.parseBasicStr(pos, true)
	}
	if !p.startsWith(delim, pos) {
		return pos, result
	}
	pos++
	if !p.startsWith(delim, pos) {
		return pos, result + delim
	}
	pos++
	return pos, result + delim + delim
}

func (p *parser) parseBasicStr(pos int, multiline bool) (int, string) {
	errorOn := illegalBasic
	if multiline {
		errorOn = illegalMultiline
	}
	var result strings.Builder
	start := pos
	for {
		c, ok := p.at(pos)
		if !ok {
			panic(p.errAt(pos, "Unterminated string"))
		}
		if c == '"' {
			if !multiline {
				result.WriteString(p.slice(start, pos))
				return pos + 1, result.String()
			}
			if p.startsWith(`"""`, pos) {
				result.WriteString(p.slice(start, pos))
				return pos + 3, result.String()
			}
			pos++
			continue
		}
		if c == '\\' {
			result.WriteString(p.slice(start, pos))
			var esc string
			pos, esc = p.parseBasicStrEscape(pos, multiline)
			result.WriteString(esc)
			start = pos
			continue
		}
		if errorOn(c) {
			panic(p.errAt(pos, "Illegal character "+pyjson.ReprString(string(c))))
		}
		pos++
	}
}

const timeRE = `([01][0-9]|2[0-3]):([0-5][0-9]):([0-5][0-9])(?:\.([0-9]{1,6})[0-9]*)?`

var (
	reNumber    = regexp.MustCompile(`^(?:0(?:x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*|b[01](?:_?[01])*|o[0-7](?:_?[0-7])*)|[+-]?(?:0|[1-9](?:_?[0-9])*)((?:\.[0-9](?:_?[0-9])*)?(?:[eE][+-]?[0-9](?:_?[0-9])*)?))`)
	reLocalTime = regexp.MustCompile(`^` + timeRE)
	reDateTime  = regexp.MustCompile(`^([0-9]{4})-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])(?:[Tt ]` + timeRE + `(?:([Zz])|([+-])([01][0-9]|2[0-3]):([0-5][0-9]))?)?`)
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func micros(s string) int {
	if s == "" {
		return 0
	}
	return atoi(s + strings.Repeat("0", 6-len(s)))
}

func validDate(y, m, d int) bool {
	dim := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
	if m == 2 && y%4 == 0 && (y%100 != 0 || y%400 == 0) {
		dim = 29
	}
	return y >= 1 && d >= 1 && d <= dim
}

func (p *parser) parseValue(pos int) (int, any) {
	c, ok := p.at(pos)
	switch {
	case ok && c == '"':
		if p.startsWith(`"""`, pos) {
			return p.parseMultilineStr(pos, false)
		}
		return p.parseBasicStr(pos+1, false)
	case ok && c == '\'':
		if p.startsWith("'''", pos) {
			return p.parseMultilineStr(pos, true)
		}
		return p.parseLiteralStr(pos)
	case ok && c == 't' && p.startsWith("true", pos):
		return pos + 4, true
	case ok && c == 'f' && p.startsWith("false", pos):
		return pos + 5, false
	case ok && c == '[':
		return p.parseArray(pos)
	case ok && c == '{':
		return p.parseInlineTable(pos)
	}
	rest := p.slice(pos, len(p.src))
	if m := reDateTime.FindStringSubmatchIndex(rest); m != nil {
		g := func(i int) string {
			if m[2*i] < 0 {
				return ""
			}
			return rest[m[2*i]:m[2*i+1]]
		}
		y, mo, d := atoi(g(1)), atoi(g(2)), atoi(g(3))
		if !validDate(y, mo, d) {
			panic(p.errAt(pos, "Invalid date or datetime"))
		}
		end := pos + len([]rune(rest[:m[1]]))
		date := Date{y, mo, d}
		if g(4) == "" {
			return end, date
		}
		dt := DateTime{Date: date, Time: Time{atoi(g(4)), atoi(g(5)), atoi(g(6)), micros(g(7))}}
		switch {
		case g(9) != "":
			off := atoi(g(10))*60 + atoi(g(11))
			if g(9) == "-" {
				off = -off
			}
			dt.Offset = &off
		case g(8) != "":
			zero := 0
			dt.Offset = &zero
		}
		return end, dt
	}
	if m := reLocalTime.FindStringSubmatch(rest); m != nil {
		return pos + len([]rune(m[0])), Time{atoi(m[1]), atoi(m[2]), atoi(m[3]), micros(m[4])}
	}
	if m := reNumber.FindStringSubmatchIndex(rest); m != nil {
		text := rest[:m[1]]
		end := pos + len([]rune(text))
		if m[2] >= 0 && m[3] > m[2] {
			f, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
			if err != nil && !math.IsInf(f, 0) {
				panic(p.errAt(pos, "Invalid value"))
			}
			return end, f
		}
		return end, pyIntBase0(text)
	}
	if three := p.slice(pos, pos+3); three == "inf" || three == "nan" {
		return pos + 3, pyFloat(three)
	}
	if four := p.slice(pos, pos+4); four == "-inf" || four == "+inf" || four == "-nan" || four == "+nan" {
		return pos + 4, pyFloat(four)
	}
	panic(p.errAt(pos, "Invalid value"))
}

func pyFloat(s string) float64 {
	switch strings.TrimLeft(s, "+-") {
	case "inf":
		if strings.HasPrefix(s, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	return math.NaN()
}

// pyIntBase0 is int(text, 0) for the RE_NUMBER integer forms.
func pyIntBase0(text string) any {
	t := strings.ReplaceAll(text, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(t, "0x"):
		base, t = 16, t[2:]
	case strings.HasPrefix(t, "0o"):
		base, t = 8, t[2:]
	case strings.HasPrefix(t, "0b"):
		base, t = 2, t[2:]
	}
	n, _ := new(big.Int).SetString(strings.TrimPrefix(t, "+"), base)
	if n.IsInt64() {
		return n.Int64()
	}
	return n
}
