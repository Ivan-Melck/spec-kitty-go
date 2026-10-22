package tracker

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// TOMLDatetime is a TOML date/time value kept as its source text.
type TOMLDatetime string

// ParseTOML decodes a TOML document (tomllib.loads) into insertion-ordered
// values: *jsonx.Object, []any, string, int64, float64, bool, TOMLDatetime.
func ParseTOML(src string) (*jsonx.Object, error) {
	p := &tomlParser{src: src, line: 1}
	root := &jsonx.Object{}
	cur := root
	defined := map[*jsonx.Object]bool{}
	for {
		p.skipWSCommentsNL()
		if p.eof() {
			return root, nil
		}
		switch {
		case strings.HasPrefix(p.src[p.pos:], "[["):
			p.pos += 2
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipWS()
			if !strings.HasPrefix(p.src[p.pos:], "]]") {
				return nil, p.errf("Expected ']]' at the end of an array declaration")
			}
			p.pos += 2
			parent, err := walkTables(root, keys[:len(keys)-1])
			if err != nil {
				return nil, p.errf("%s", err)
			}
			last := keys[len(keys)-1]
			var arr []any
			if v, ok := parent.Get(last); ok {
				l, ok := v.([]any)
				if !ok {
					return nil, p.errf("Cannot overwrite a value")
				}
				arr = l
			}
			t := &jsonx.Object{}
			parent.Set(last, append(arr, t))
			cur = t
		case p.peek() == '[':
			p.pos++
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipWS()
			if p.peek() != ']' {
				return nil, p.errf("Expected ']' at the end of a table declaration")
			}
			p.pos++
			t, err := walkTables(root, keys)
			if err != nil {
				return nil, p.errf("%s", err)
			}
			if defined[t] {
				return nil, p.errf("Cannot declare %s twice", strings.Join(keys, "."))
			}
			defined[t] = true
			cur = t
		default:
			if err := p.keyValue(cur); err != nil {
				return nil, err
			}
		}
		p.skipWS()
		p.skipComment()
		if !p.eof() && p.peek() != '\n' && !strings.HasPrefix(p.src[p.pos:], "\r\n") {
			return nil, p.errf("Expected newline or end of document after a statement")
		}
	}
}

func walkTables(root *jsonx.Object, keys []string) (*jsonx.Object, error) {
	cur := root
	for _, k := range keys {
		v, ok := cur.Get(k)
		if !ok {
			t := &jsonx.Object{}
			cur.Set(k, t)
			cur = t
			continue
		}
		switch t := v.(type) {
		case *jsonx.Object:
			cur = t
		case []any:
			if len(t) == 0 {
				return nil, fmt.Errorf("Cannot overwrite a value")
			}
			o, ok := t[len(t)-1].(*jsonx.Object)
			if !ok {
				return nil, fmt.Errorf("Cannot overwrite a value")
			}
			cur = o
		default:
			return nil, fmt.Errorf("Cannot overwrite a value")
		}
	}
	return cur, nil
}

type tomlParser struct {
	src  string
	pos  int
	line int
}

func (p *tomlParser) eof() bool { return p.pos >= len(p.src) }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *tomlParser) errf(format string, a ...any) error {
	line := 1 + strings.Count(p.src[:min(p.pos, len(p.src))], "\n")
	col := p.pos - strings.LastIndex(p.src[:min(p.pos, len(p.src))], "\n")
	return fmt.Errorf("%s (at line %d, column %d)", fmt.Sprintf(format, a...), line, col)
}

func (p *tomlParser) skipWS() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.pos++
	}
}

func (p *tomlParser) skipComment() {
	if p.peek() == '#' {
		for !p.eof() && p.peek() != '\n' {
			p.pos++
		}
	}
}

func (p *tomlParser) skipWSCommentsNL() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\n', '\r':
			p.pos++
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+`)

func (p *tomlParser) key() ([]string, error) {
	var keys []string
	for {
		p.skipWS()
		switch p.peek() {
		case '"':
			s, err := p.basicString()
			if err != nil {
				return nil, err
			}
			keys = append(keys, s)
		case '\'':
			s, err := p.literalString()
			if err != nil {
				return nil, err
			}
			keys = append(keys, s)
		default:
			m := bareKeyRe.FindString(p.src[p.pos:])
			if m == "" {
				return nil, p.errf("Invalid initial character for a key part")
			}
			p.pos += len(m)
			keys = append(keys, m)
		}
		p.skipWS()
		if p.peek() != '.' {
			return keys, nil
		}
		p.pos++
	}
}

func (p *tomlParser) keyValue(target *jsonx.Object) error {
	keys, err := p.key()
	if err != nil {
		return err
	}
	p.skipWS()
	if p.peek() != '=' {
		return p.errf("Expected '=' after a key in a key/value pair")
	}
	p.pos++
	p.skipWS()
	v, err := p.value()
	if err != nil {
		return err
	}
	t := target
	for _, k := range keys[:len(keys)-1] {
		if existing, ok := t.Get(k); ok {
			o, ok := existing.(*jsonx.Object)
			if !ok {
				return p.errf("Cannot overwrite a value")
			}
			t = o
			continue
		}
		o := &jsonx.Object{}
		t.Set(k, o)
		t = o
	}
	last := keys[len(keys)-1]
	if t.Has(last) {
		return p.errf("Cannot overwrite a value")
	}
	t.Set(last, v)
	return nil
}

var (
	tomlDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([Tt ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?([Zz]|[+-]\d{2}:\d{2})?)?`)
	tomlTimeRe  = regexp.MustCompile(`^\d{2}:\d{2}(:\d{2}(\.\d+)?)?`)
	tomlNumRe   = regexp.MustCompile(`^[+-]?(0x[0-9A-Fa-f_]+|0o[0-7_]+|0b[01_]+|inf|nan|[0-9_]+(\.[0-9_]+)?([eE][+-]?[0-9_]+)?)`)
	tomlFloatRe = regexp.MustCompile(`[.eE]|inf|nan`)
)

func (p *tomlParser) value() (any, error) {
	rest := p.src[p.pos:]
	switch {
	case strings.HasPrefix(rest, `"""`):
		return p.mlBasicString()
	case strings.HasPrefix(rest, "'''"):
		return p.mlLiteralString()
	case p.peek() == '"':
		return p.basicString()
	case p.peek() == '\'':
		return p.literalString()
	case strings.HasPrefix(rest, "true"):
		p.pos += 4
		return true, nil
	case strings.HasPrefix(rest, "false"):
		p.pos += 5
		return false, nil
	case p.peek() == '[':
		return p.array()
	case p.peek() == '{':
		return p.inlineTable()
	}
	if m := tomlDateRe.FindString(rest); m != "" {
		p.pos += len(m)
		return TOMLDatetime(m), nil
	}
	if m := tomlTimeRe.FindString(rest); m != "" && !tomlNumRe.MatchString(rest[len(m):]) {
		if len(m) >= 5 && m[2] == ':' {
			p.pos += len(m)
			return TOMLDatetime(m), nil
		}
	}
	m := tomlNumRe.FindString(rest)
	if m == "" {
		return nil, p.errf("Invalid value")
	}
	p.pos += len(m)
	clean := strings.ReplaceAll(m, "_", "")
	sign := ""
	body := clean
	if body != "" && (body[0] == '+' || body[0] == '-') {
		sign, body = body[:1], body[1:]
	}
	switch {
	case strings.HasPrefix(body, "0x"), strings.HasPrefix(body, "0o"), strings.HasPrefix(body, "0b"):
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[body[1]]
		n, err := strconv.ParseInt(body[2:], base, 64)
		if err != nil {
			return nil, p.errf("Invalid value")
		}
		return n, nil
	case body == "inf":
		if sign == "-" {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case body == "nan":
		return math.NaN(), nil
	case tomlFloatRe.MatchString(body):
		f, err := strconv.ParseFloat(sign+body, 64)
		if err != nil {
			return nil, p.errf("Invalid value")
		}
		return f, nil
	}
	n, err := strconv.ParseInt(sign+body, 10, 64)
	if err != nil {
		return nil, p.errf("Invalid value")
	}
	return n, nil
}

func (p *tomlParser) array() (any, error) {
	p.pos++
	out := []any{}
	for {
		p.skipWSCommentsNL()
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipWSCommentsNL()
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out, nil
		default:
			return nil, p.errf("Unclosed array")
		}
	}
}

func (p *tomlParser) inlineTable() (any, error) {
	p.pos++
	t := &jsonx.Object{}
	p.skipWS()
	if p.peek() == '}' {
		p.pos++
		return t, nil
	}
	for {
		if err := p.keyValue(t); err != nil {
			return nil, err
		}
		p.skipWS()
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return t, nil
		default:
			return nil, p.errf("Unclosed inline table")
		}
	}
}

func (p *tomlParser) literalString() (string, error) {
	p.pos++
	end := strings.IndexAny(p.src[p.pos:], "'\n")
	if end < 0 || p.src[p.pos+end] != '\'' {
		return "", p.errf("Unterminated string")
	}
	s := p.src[p.pos : p.pos+end]
	p.pos += end + 1
	return s, nil
}

func (p *tomlParser) mlLiteralString() (string, error) {
	p.pos += 3
	if strings.HasPrefix(p.src[p.pos:], "\r\n") {
		p.pos += 2
	} else if p.peek() == '\n' {
		p.pos++
	}
	end := strings.Index(p.src[p.pos:], "'''")
	if end < 0 {
		return "", p.errf("Unterminated string")
	}
	// Up to two extra quotes may close the string.
	for end+3 < len(p.src[p.pos:]) && p.src[p.pos+end+3] == '\'' && end < len(p.src) {
		end++
	}
	s := p.src[p.pos : p.pos+end]
	p.pos += end + 3
	return s, nil
}

func (p *tomlParser) basicString() (string, error) {
	p.pos++
	var b strings.Builder
	for {
		if p.eof() || p.peek() == '\n' {
			return "", p.errf("Unterminated string")
		}
		c := p.peek()
		if c == '"' {
			p.pos++
			return b.String(), nil
		}
		if c == '\\' {
			if err := p.escape(&b, false); err != nil {
				return "", err
			}
			continue
		}
		b.WriteByte(c)
		p.pos++
	}
}

func (p *tomlParser) mlBasicString() (string, error) {
	p.pos += 3
	if strings.HasPrefix(p.src[p.pos:], "\r\n") {
		p.pos += 2
	} else if p.peek() == '\n' {
		p.pos++
	}
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.errf("Unterminated string")
		}
		if strings.HasPrefix(p.src[p.pos:], `"""`) {
			extra := 0
			for extra < 2 && p.pos+3+extra < len(p.src) && p.src[p.pos+3+extra] == '"' {
				extra++
			}
			b.WriteString(strings.Repeat(`"`, extra))
			p.pos += 3 + extra
			return b.String(), nil
		}
		c := p.peek()
		if c == '\\' {
			if err := p.escape(&b, true); err != nil {
				return "", err
			}
			continue
		}
		b.WriteByte(c)
		p.pos++
	}
}

func (p *tomlParser) escape(b *strings.Builder, multiline bool) error {
	p.pos++
	if p.eof() {
		return p.errf("Unterminated string")
	}
	c := p.peek()
	p.pos++
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case 'u', 'U', 'x':
		n := map[byte]int{'u': 4, 'U': 8, 'x': 2}[c]
		if p.pos+n > len(p.src) {
			return p.errf("Unterminated string")
		}
		v, err := strconv.ParseUint(p.src[p.pos:p.pos+n], 16, 32)
		if err != nil || !utf8.ValidRune(rune(v)) {
			return p.errf("Invalid hex value")
		}
		b.WriteRune(rune(v))
		p.pos += n
	case ' ', '\t', '\r', '\n':
		if !multiline {
			return p.errf("Unescaped '\\' in a string")
		}
		// line-ending backslash: trim whitespace and newlines
		p.pos--
		for !p.eof() && strings.IndexByte(" \t\r\n", p.peek()) >= 0 {
			p.pos++
		}
	default:
		return p.errf("Unescaped '\\' in a string")
	}
	return nil
}

// DumpTOML renders v like the python `toml` package's toml.dumps.
func DumpTOML(o *jsonx.Object) (string, error) {
	var ret strings.Builder
	add, sections, err := dumpSections(o, "")
	if err != nil {
		return "", err
	}
	ret.WriteString(add)
	for sections.Len() > 0 {
		next := &jsonx.Object{}
		for _, kv := range sections.Pairs() {
			add, sub, err := dumpSections(kv.Value.(*jsonx.Object), kv.Key)
			if err != nil {
				return "", err
			}
			if add != "" || sub.Len() == 0 {
				s := ret.String()
				if s != "" && !strings.HasSuffix(s, "\n\n") {
					ret.WriteString("\n")
				}
				ret.WriteString("[" + kv.Key + "]\n")
				ret.WriteString(add)
			}
			for _, skv := range sub.Pairs() {
				next.Set(kv.Key+"."+skv.Key, skv.Value)
			}
		}
		sections = next
	}
	return ret.String(), nil
}

var tomlBareSectionRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func dumpSections(o *jsonx.Object, sup string) (string, *jsonx.Object, error) {
	var ret, arrays strings.Builder
	if sup != "" && !strings.HasSuffix(sup, ".") {
		sup += "."
	}
	tables := &jsonx.Object{}
	for _, kv := range o.Pairs() {
		q := kv.Key
		if !tomlBareSectionRe.MatchString(q) {
			s, err := dumpTOMLStr(q)
			if err != nil {
				return "", nil, err
			}
			q = s
		}
		if sub, ok := kv.Value.(*jsonx.Object); ok {
			tables.Set(q, sub)
			continue
		}
		if l, ok := kv.Value.([]any); ok && hasTable(l) {
			for _, a := range l {
				at, _ := a.(*jsonx.Object)
				if at == nil {
					at = &jsonx.Object{}
				}
				arrTab := "\n"
				arrays.WriteString("[[" + sup + q + "]]\n")
				s, d, err := dumpSections(at, sup+q)
				if err != nil {
					return "", nil, err
				}
				if s != "" {
					if s[0] == '[' {
						arrTab += s
					} else {
						arrays.WriteString(s)
					}
				}
				for d.Len() > 0 {
					nd := &jsonx.Object{}
					for _, dkv := range d.Pairs() {
						s1, d1, err := dumpSections(dkv.Value.(*jsonx.Object), sup+q+"."+dkv.Key)
						if err != nil {
							return "", nil, err
						}
						if s1 != "" {
							arrTab += "[" + sup + q + "." + dkv.Key + "]\n" + s1
						}
						for _, k1 := range d1.Pairs() {
							nd.Set(dkv.Key+"."+k1.Key, k1.Value)
						}
					}
					d = nd
				}
				arrays.WriteString(arrTab)
			}
			continue
		}
		if kv.Value == nil {
			continue
		}
		s, err := dumpTOMLValue(kv.Value)
		if err != nil {
			return "", nil, err
		}
		ret.WriteString(q + " = " + s + "\n")
	}
	ret.WriteString(arrays.String())
	return ret.String(), tables, nil
}

func hasTable(l []any) bool {
	for _, a := range l {
		if _, ok := a.(*jsonx.Object); ok {
			return true
		}
	}
	return false
}

func dumpTOMLValue(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return dumpTOMLStr(t)
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int:
		return strconv.Itoa(t), nil
	case float64:
		s := pyjson.ReprFloat(t)
		s = strings.ReplaceAll(s, "e+0", "e+")
		return strings.ReplaceAll(s, "e-0", "e-"), nil
	case TOMLDatetime:
		return string(t), nil
	case []any:
		var b strings.Builder
		b.WriteString("[")
		for _, u := range t {
			s, err := dumpTOMLValue(u)
			if err != nil {
				return "", err
			}
			b.WriteString(" " + s + ",")
		}
		b.WriteString("]")
		return b.String(), nil
	case *jsonx.Object:
		// Only reachable inside arrays: python's toml treats a dict as an
		// iterable of its keys.
		l := make([]any, 0, t.Len())
		for _, k := range t.Keys() {
			l = append(l, k)
		}
		return dumpTOMLValue(l)
	}
	return dumpTOMLStr(pyjson.Str(v))
}

// dumpTOMLStr ports toml.encoder._dump_str (including its quirks).
func dumpTOMLStr(s string) (string, error) {
	v := pyjson.ReprString(s)
	single := strings.HasPrefix(v, "'")
	if single || strings.HasPrefix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	if single {
		v = strings.ReplaceAll(v, `\'`, "'")
		v = strings.ReplaceAll(v, `"`, `\"`)
	}
	parts := strings.Split(v, `\x`)
	for len(parts) > 1 {
		if parts[0] == "" {
			parts = parts[1:]
		}
		parts[0] = strings.ReplaceAll(parts[0], `\\`, `\`)
		p0 := parts[0]
		if p0 == "" || len(parts) < 2 {
			return "", fmt.Errorf("list index out of range")
		}
		i := -1
		joinx := p0[len(p0)-1] != '\\'
		for len(p0) > -i && p0[len(p0)+i] == '\\' {
			joinx = !joinx
			i--
		}
		joiner := "u00"
		if joinx {
			joiner = "x"
		}
		parts = append([]string{p0 + joiner + parts[1]}, parts[2:]...)
	}
	return `"` + parts[0] + `"`, nil
}
