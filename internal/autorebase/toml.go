package autorebase

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ValidateTOML reports whether src is a valid TOML 1.0 document (the
// subset of tomllib.loads validation the classifier relies on): syntax,
// strings and escapes, numbers, dates, arrays, inline tables, and the
// duplicate-key / table-redefinition rules.
func ValidateTOML(src string) error {
	p := &tomlParser{src: src, line: 1, root: newTable(tblExplicit)}
	p.cur = p.root
	return p.document()
}

type tblKind int

const (
	tblImplicit tblKind = iota // created as a parent of a header
	tblExplicit                // defined by a [header]
	tblDotted                  // created by a dotted key
	tblInline                  // inline table (frozen)
)

type tomlNode struct {
	table   map[string]*tomlNode // non-nil for tables
	kind    tblKind
	aot     []*tomlNode // array of tables
	isAOT   bool
	isValue bool
}

func newTable(k tblKind) *tomlNode { return &tomlNode{table: map[string]*tomlNode{}, kind: k} }

type tomlParser struct {
	src  string
	pos  int
	line int
	root *tomlNode
	cur  *tomlNode
}

func (p *tomlParser) errf(format string, a ...any) error {
	col := 1
	for i := p.pos - 1; i >= 0 && i < len(p.src) && p.src[i] != '\n'; i-- {
		col++
	}
	return fmt.Errorf("%s (at line %d, column %d)", fmt.Sprintf(format, a...), p.line, col)
}

func (p *tomlParser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *tomlParser) eof() bool { return p.pos >= len(p.src) }

func (p *tomlParser) skipWS() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *tomlParser) skipComment() error {
	if p.peek() != '#' {
		return nil
	}
	for !p.eof() && p.src[p.pos] != '\n' {
		c := p.src[p.pos]
		if (c < 0x20 && c != '\t' && c != '\r') || c == 0x7f {
			return p.errf("Illegal character %s", strconv.Quote(string(c)))
		}
		if c == '\r' && !strings.HasPrefix(p.src[p.pos:], "\r\n") {
			return p.errf("Illegal character '\\r'")
		}
		p.pos++
	}
	return nil
}

// newline consumes a newline (or EOF); false when something else follows.
func (p *tomlParser) newline() bool {
	if p.eof() {
		return true
	}
	if p.src[p.pos] == '\n' {
		p.pos++
		p.line++
		return true
	}
	if strings.HasPrefix(p.src[p.pos:], "\r\n") {
		p.pos += 2
		p.line++
		return true
	}
	return false
}

func (p *tomlParser) document() error {
	for !p.eof() {
		p.skipWS()
		if err := p.skipComment(); err != nil {
			return err
		}
		if p.newline() {
			continue
		}
		var err error
		switch {
		case strings.HasPrefix(p.src[p.pos:], "[["):
			err = p.arrayTableHeader()
		case p.peek() == '[':
			err = p.tableHeader()
		default:
			err = p.keyval(p.cur)
		}
		if err != nil {
			return err
		}
		p.skipWS()
		if err := p.skipComment(); err != nil {
			return err
		}
		if !p.newline() {
			return p.errf("Expected newline or end of document after a statement")
		}
	}
	return nil
}

func isBareKeyChar(c byte) bool {
	return c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (p *tomlParser) simpleKey() (string, error) {
	switch p.peek() {
	case '"':
		if strings.HasPrefix(p.src[p.pos:], `"""`) {
			return "", p.errf("Multiline strings are not allowed as keys")
		}
		return p.basicString()
	case '\'':
		if strings.HasPrefix(p.src[p.pos:], "'''") {
			return "", p.errf("Multiline strings are not allowed as keys")
		}
		return p.literalString()
	}
	start := p.pos
	for !p.eof() && isBareKeyChar(p.src[p.pos]) {
		p.pos++
	}
	if start == p.pos {
		return "", p.errf("Invalid initial character for a key part")
	}
	return p.src[start:p.pos], nil
}

func (p *tomlParser) key() ([]string, error) {
	var parts []string
	for {
		p.skipWS()
		k, err := p.simpleKey()
		if err != nil {
			return nil, err
		}
		parts = append(parts, k)
		p.skipWS()
		if p.peek() != '.' {
			return parts, nil
		}
		p.pos++
	}
}

// descend walks/creates the parent tables of a header key.
func (p *tomlParser) descend(parts []string) (*tomlNode, error) {
	t := p.root
	for _, k := range parts {
		n, ok := t.table[k]
		if !ok {
			n = newTable(tblImplicit)
			t.table[k] = n
		}
		if n.isAOT {
			n = n.aot[len(n.aot)-1]
		} else if n.isValue || n.kind == tblInline {
			return nil, p.errf("Cannot declare %s twice", strings.Join(parts, "."))
		}
		t = n
	}
	return t, nil
}

func (p *tomlParser) tableHeader() error {
	p.pos++
	parts, err := p.key()
	if err != nil {
		return err
	}
	if p.peek() != ']' {
		return p.errf("Expected ']' at the end of a table declaration")
	}
	p.pos++
	parent, err := p.descend(parts[:len(parts)-1])
	if err != nil {
		return err
	}
	last := parts[len(parts)-1]
	n, ok := parent.table[last]
	switch {
	case !ok:
		n = newTable(tblExplicit)
		parent.table[last] = n
	case n.isAOT || n.isValue || n.kind == tblExplicit || n.kind == tblDotted || n.kind == tblInline:
		return p.errf("Cannot declare %s twice", strings.Join(parts, "."))
	default:
		n.kind = tblExplicit
	}
	p.cur = n
	return nil
}

func (p *tomlParser) arrayTableHeader() error {
	p.pos += 2
	parts, err := p.key()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(p.src[p.pos:], "]]") {
		return p.errf("Expected ']]' at the end of an array declaration")
	}
	p.pos += 2
	parent, err := p.descend(parts[:len(parts)-1])
	if err != nil {
		return err
	}
	last := parts[len(parts)-1]
	n, ok := parent.table[last]
	if !ok {
		n = &tomlNode{isAOT: true}
		parent.table[last] = n
	} else if !n.isAOT {
		return p.errf("Cannot mutate immutable namespace %s", strings.Join(parts, "."))
	}
	t := newTable(tblExplicit)
	n.aot = append(n.aot, t)
	p.cur = t
	return nil
}

// keyval parses `key = value` into t.
func (p *tomlParser) keyval(t *tomlNode) error {
	parts, err := p.key()
	if err != nil {
		return err
	}
	if p.peek() != '=' {
		return p.errf("Expected '=' after a key in a key/value pair")
	}
	p.pos++
	p.skipWS()
	for _, k := range parts[:len(parts)-1] {
		n, ok := t.table[k]
		if !ok {
			n = newTable(tblDotted)
			t.table[k] = n
		} else if n.isValue || n.isAOT || n.kind == tblInline || n.kind == tblExplicit {
			return p.errf("Cannot overwrite a value")
		}
		t = n
	}
	last := parts[len(parts)-1]
	if _, ok := t.table[last]; ok {
		return p.errf("Cannot overwrite a value")
	}
	v, err := p.value()
	if err != nil {
		return err
	}
	t.table[last] = v
	return nil
}

var (
	reTOMLDateTime = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[Tt ](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:[Zz]|[+-](\d{2}):(\d{2}))?)?`)
	reTOMLTime     = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?`)
	reTOMLFloat    = regexp.MustCompile(`^[+-]?(?:0|[1-9](?:_?\d)*)(?:\.\d(?:_?\d)*)?(?:[eE][+-]?\d(?:_?\d)*)?`)
	reTOMLSpecial  = regexp.MustCompile(`^[+-]?(?:inf|nan)`)
	reTOMLHex      = regexp.MustCompile(`^0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*`)
	reTOMLOct      = regexp.MustCompile(`^0o[0-7](?:_?[0-7])*`)
	reTOMLBin      = regexp.MustCompile(`^0b[01](?:_?[01])*`)
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func validDate(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 {
		return false
	}
	days := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
	if m == 2 && (y%4 == 0 && (y%100 != 0 || y%400 == 0)) {
		days = 29
	}
	return d <= days
}

func (p *tomlParser) value() (*tomlNode, error) {
	rest := p.src[p.pos:]
	val := &tomlNode{isValue: true}
	switch {
	case strings.HasPrefix(rest, `"""`):
		_, err := p.mlBasicString()
		return val, err
	case strings.HasPrefix(rest, "'''"):
		_, err := p.mlLiteralString()
		return val, err
	case strings.HasPrefix(rest, `"`):
		_, err := p.basicString()
		return val, err
	case strings.HasPrefix(rest, "'"):
		_, err := p.literalString()
		return val, err
	case strings.HasPrefix(rest, "true"):
		p.pos += 4
		return val, nil
	case strings.HasPrefix(rest, "false"):
		p.pos += 5
		return val, nil
	case strings.HasPrefix(rest, "["):
		return val, p.array()
	case strings.HasPrefix(rest, "{"):
		return p.inlineTable()
	}
	if m := reTOMLDateTime.FindStringSubmatch(rest); m != nil {
		if !validDate(atoi(m[1]), atoi(m[2]), atoi(m[3])) {
			return nil, p.errf("Invalid date or datetime")
		}
		if m[4] != "" && (atoi(m[4]) > 23 || atoi(m[5]) > 59 || atoi(m[6]) > 59) {
			return nil, p.errf("Invalid date or datetime")
		}
		if m[7] != "" && (atoi(m[7]) > 23 || atoi(m[8]) > 59) {
			return nil, p.errf("Invalid date or datetime")
		}
		p.pos += len(m[0])
		return val, nil
	}
	if m := reTOMLTime.FindStringSubmatch(rest); m != nil {
		if atoi(m[1]) > 23 || atoi(m[2]) > 59 || atoi(m[3]) > 59 {
			return nil, p.errf("Invalid time")
		}
		p.pos += len(m[0])
		return val, nil
	}
	for _, re := range []*regexp.Regexp{reTOMLHex, reTOMLOct, reTOMLBin, reTOMLSpecial} {
		if m := re.FindString(rest); m != "" {
			p.pos += len(m)
			return val, nil
		}
	}
	if m := reTOMLFloat.FindString(rest); m != "" {
		p.pos += len(m)
		return val, nil
	}
	return nil, p.errf("Invalid value")
}

// skipArrayWS skips whitespace, comments and newlines inside an array.
func (p *tomlParser) skipArrayWS() error {
	for {
		p.skipWS()
		if err := p.skipComment(); err != nil {
			return err
		}
		if p.eof() || !p.newline() {
			return nil
		}
	}
}

func (p *tomlParser) array() error {
	p.pos++
	for {
		if err := p.skipArrayWS(); err != nil {
			return err
		}
		if p.peek() == ']' {
			p.pos++
			return nil
		}
		if p.eof() {
			return p.errf("Unclosed array")
		}
		if _, err := p.value(); err != nil {
			return err
		}
		if err := p.skipArrayWS(); err != nil {
			return err
		}
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return nil
		default:
			return p.errf("Unclosed array")
		}
	}
}

func (p *tomlParser) inlineTable() (*tomlNode, error) {
	p.pos++
	t := newTable(tblInline)
	p.skipWS()
	if p.peek() == '}' {
		p.pos++
		return t, nil
	}
	for {
		if err := p.keyval(t); err != nil {
			return nil, err
		}
		p.skipWS()
		switch p.peek() {
		case '}':
			p.pos++
			freeze(t)
			return t, nil
		case ',':
			p.pos++
			p.skipWS()
		default:
			return nil, p.errf("Unclosed inline table")
		}
	}
}

func freeze(t *tomlNode) {
	t.kind = tblInline
	for _, n := range t.table {
		if n.table != nil {
			freeze(n)
		}
	}
}

func (p *tomlParser) escape() error {
	p.pos++ // backslash
	if p.eof() {
		return p.errf("Unterminated string")
	}
	c := p.src[p.pos]
	switch c {
	case 'b', 't', 'n', 'f', 'r', '"', '\\':
		p.pos++
		return nil
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		hex := p.src[p.pos+1:]
		if len(hex) < n {
			return p.errf("Invalid hex value")
		}
		v, err := strconv.ParseUint(hex[:n], 16, 32)
		if err != nil || (v >= 0xd800 && v <= 0xdfff) || v > 0x10ffff {
			return p.errf("Invalid hex value")
		}
		p.pos += 1 + n
		return nil
	}
	return p.errf("Unescaped '\\' in a string")
}

func illegal(c byte) bool { return (c < 0x20 && c != '\t') || c == 0x7f }

func (p *tomlParser) basicString() (string, error) {
	p.pos++
	start := p.pos
	for {
		if p.eof() {
			return "", p.errf("Unterminated string")
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			s := p.src[start:p.pos]
			p.pos++
			return s, nil
		case c == '\\':
			if err := p.escape(); err != nil {
				return "", err
			}
		case c == '\n' || c == '\r':
			return "", p.errf("Illegal character %s", strconv.Quote(string(c)))
		case illegal(c):
			return "", p.errf("Illegal character %s", strconv.Quote(string(c)))
		default:
			p.pos++
		}
	}
}

func (p *tomlParser) literalString() (string, error) {
	p.pos++
	start := p.pos
	for {
		if p.eof() {
			return "", p.errf("Unterminated string")
		}
		c := p.src[p.pos]
		if c == '\'' {
			s := p.src[start:p.pos]
			p.pos++
			return s, nil
		}
		if illegal(c) {
			return "", p.errf("Illegal character %s", strconv.Quote(string(c)))
		}
		p.pos++
	}
}

func (p *tomlParser) mlBasicString() (string, error) {
	p.pos += 3
	p.newline() // a newline right after the delimiter is trimmed
	for {
		if p.eof() {
			return "", p.errf("Unterminated string")
		}
		if strings.HasPrefix(p.src[p.pos:], `"""`) {
			p.pos += 3
			for i := 0; i < 2 && p.peek() == '"'; i++ {
				p.pos++
			}
			return "", nil
		}
		c := p.src[p.pos]
		switch {
		case c == '\\':
			// line-ending backslash
			j := p.pos + 1
			for j < len(p.src) && (p.src[j] == ' ' || p.src[j] == '\t') {
				j++
			}
			if j < len(p.src) && (p.src[j] == '\n' || strings.HasPrefix(p.src[j:], "\r\n")) {
				p.pos = j
				for !p.eof() {
					if p.newline() {
						continue
					}
					if p.peek() == ' ' || p.peek() == '\t' {
						p.pos++
						continue
					}
					break
				}
				continue
			}
			if err := p.escape(); err != nil {
				return "", err
			}
		case p.newline():
		case illegal(c):
			return "", p.errf("Illegal character %s", strconv.Quote(string(c)))
		default:
			p.pos++
		}
	}
}

func (p *tomlParser) mlLiteralString() (string, error) {
	p.pos += 3
	p.newline()
	for {
		if p.eof() {
			return "", p.errf("Unterminated string")
		}
		if strings.HasPrefix(p.src[p.pos:], "'''") {
			p.pos += 3
			for i := 0; i < 2 && p.peek() == '\''; i++ {
				p.pos++
			}
			return "", nil
		}
		if p.newline() {
			continue
		}
		if illegal(p.src[p.pos]) {
			return "", p.errf("Illegal character %s", strconv.Quote(string(p.src[p.pos])))
		}
		p.pos++
	}
}
