// Package pyast is a small Python 3.13 tokenizer and parser producing the
// subset of the stdlib `ast` tree that spec-kitty's static analyzers
// inspect (definitions, imports, assignments, assertions, calls, names,
// attributes, comparisons and string constants), with CPython's node
// field order so ast.walk order and line numbers match.
package pyast

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token kinds.
const (
	tEOF = iota
	tName
	tNumber
	tString
	tOp
	tNewline
	tIndent
	tDedent
)

type token struct {
	kind int
	val  string // raw text
	line int
	col  int
	// string pieces: for tString the literal (prefix+quotes+body)
}

// SyntaxError is a tokenize/parse failure.
type SyntaxError struct {
	Msg  string
	Line int
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("%s (line %d)", e.Msg, e.Line) }

var threeCharOps = []string{"**=", "//=", ">>=", "<<=", "...", "!=="}
var twoCharOps = []string{"**", "//", ">>", "<<", "<=", ">=", "==", "!=", "->", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "@=", ":="}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || r > 127 && (unicode.Is(unicode.Other_ID_Start, r) || unicode.IsLetter(r))
}

func isIdentChar(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r)
}

type lexer struct {
	src    string
	pos    int
	line   int
	col    int
	toks   []token
	indent []int
	depth  int // bracket depth
}

func (l *lexer) peekRune(off int) rune {
	p := l.pos
	for i := 0; i < off; i++ {
		if p >= len(l.src) {
			return 0
		}
		_, w := utf8.DecodeRuneInString(l.src[p:])
		p += w
	}
	if p >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[p:])
	return r
}

func (l *lexer) advance(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		r, w := utf8.DecodeRuneInString(l.src[l.pos:])
		l.pos += w
		if r == '\n' {
			l.line++
			l.col = 0
		} else {
			l.col++
		}
	}
}

func (l *lexer) emit(kind int, val string, line, col int) {
	l.toks = append(l.toks, token{kind: kind, val: val, line: line, col: col})
}

// stringPrefixLen returns the length of a valid string prefix at pos (0 if
// none) followed by a quote.
func stringPrefix(s string) (string, bool) {
	i := 0
	for i < len(s) && i < 3 && strings.ContainsRune("rRbBuUfFtT", rune(s[i])) {
		i++
	}
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		p := strings.ToLower(s[:i])
		switch p {
		case "", "r", "u", "b", "br", "rb", "f", "fr", "rf":
			return s[:i], true
		}
	}
	return "", false
}

// scanString scans a string literal starting at l.pos (prefix included);
// returns its raw text.
func (l *lexer) scanString(prefix string) (string, error) {
	start := l.pos
	startLine := l.line
	l.advance(len(prefix))
	q := l.src[l.pos]
	triple := strings.HasPrefix(l.src[l.pos:], string([]byte{q, q, q}))
	isF := strings.ContainsAny(prefix, "fF")
	raw := strings.ContainsAny(prefix, "rR")
	if triple {
		l.advance(3)
	} else {
		l.advance(1)
	}
	closer := string(q)
	if triple {
		closer = strings.Repeat(string(q), 3)
	}
	braces := 0
	for {
		if l.pos >= len(l.src) {
			return "", &SyntaxError{"unterminated string literal", startLine}
		}
		c := l.src[l.pos]
		if isF && braces > 0 {
			// inside an f-string replacement field: nested strings/brackets
			switch {
			case c == '{' || c == '[' || c == '(':
				braces++
				l.advance(1)
				continue
			case c == '}' || c == ']' || c == ')':
				braces--
				l.advance(1)
				continue
			case c == '\'' || c == '"':
				p, _ := stringPrefix(l.src[l.pos:])
				if _, err := l.scanString(p); err != nil {
					return "", err
				}
				continue
			case c == '#':
				// comments are not allowed inside single-line f-strings; skip to EOL
				l.advance(1)
				continue
			}
			if c == '\n' && !triple && braces == 0 {
				return "", &SyntaxError{"unterminated string literal", startLine}
			}
			l.advance(1)
			continue
		}
		if c == '\\' && !raw {
			l.advance(2)
			continue
		}
		if c == '\\' && raw {
			// a raw string still cannot end with an odd backslash before the quote
			l.advance(2)
			continue
		}
		if isF && c == '{' {
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '{' {
				l.advance(2)
				continue
			}
			braces = 1
			l.advance(1)
			continue
		}
		if strings.HasPrefix(l.src[l.pos:], closer) {
			l.advance(len(closer))
			return l.src[start:l.pos], nil
		}
		if c == '\n' && !triple {
			return "", &SyntaxError{"unterminated string literal", startLine}
		}
		l.advance(1)
	}
}

func tokenize(src string) ([]token, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	if strings.HasPrefix(src, "\xef\xbb\xbf") {
		src = src[3:]
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	l := &lexer{src: src, line: 1, indent: []int{0}}
	atLineStart := true
	for l.pos < len(l.src) {
		if atLineStart && l.depth == 0 {
			// measure indentation
			col := 0
			p := l.pos
			for p < len(l.src) {
				switch l.src[p] {
				case ' ':
					col++
				case '\t':
					col = (col/8 + 1) * 8
				case '\f':
					col = 0
				default:
					goto done
				}
				p++
			}
		done:
			if p >= len(l.src) {
				l.advance(p - l.pos)
				break
			}
			c := l.src[p]
			if c == '\n' || c == '#' || (c == '\\' && p+1 < len(l.src) && l.src[p+1] == '\n') {
				// blank / comment-only line: no indentation change
				if c == '\\' {
					l.advance(p - l.pos + 2)
					continue
				}
				l.advance(p - l.pos)
				for l.pos < len(l.src) && l.src[l.pos] != '\n' {
					l.advance(1)
				}
				l.advance(1)
				continue
			}
			l.advance(p - l.pos)
			cur := l.indent[len(l.indent)-1]
			if col > cur {
				l.indent = append(l.indent, col)
				l.emit(tIndent, "", l.line, 0)
			} else {
				for col < l.indent[len(l.indent)-1] {
					l.indent = l.indent[:len(l.indent)-1]
					l.emit(tDedent, "", l.line, 0)
				}
				if col != l.indent[len(l.indent)-1] {
					return nil, &SyntaxError{"unindent does not match any outer indentation level", l.line}
				}
			}
			atLineStart = false
		}
		c := l.src[l.pos]
		switch {
		case c == '\n':
			if l.depth == 0 {
				l.emit(tNewline, "\n", l.line, l.col)
				atLineStart = true
			}
			l.advance(1)
		case c == ' ' || c == '\t' || c == '\f':
			l.advance(1)
		case c == '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advance(1)
			}
		case c == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n':
			l.advance(2)
		default:
			line, col := l.line, l.col
			if p, ok := stringPrefix(l.src[l.pos:]); ok {
				s, err := l.scanString(p)
				if err != nil {
					return nil, err
				}
				l.emit(tString, s, line, col)
				continue
			}
			r := l.peekRune(0)
			if isIdentStart(r) {
				start := l.pos
				for l.pos < len(l.src) {
					rr, _ := utf8.DecodeRuneInString(l.src[l.pos:])
					if !isIdentChar(rr) {
						break
					}
					l.advance(1)
				}
				l.emit(tName, l.src[start:l.pos], line, col)
				continue
			}
			if c >= '0' && c <= '9' || (c == '.' && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9') {
				start := l.pos
				for l.pos < len(l.src) {
					ch := l.src[l.pos]
					if ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_' || ch == '.' {
						if (ch == 'e' || ch == 'E') && l.pos+1 < len(l.src) && (l.src[l.pos+1] == '+' || l.src[l.pos+1] == '-') &&
							!strings.HasPrefix(strings.ToLower(l.src[start:]), "0x") {
							l.advance(2)
							continue
						}
						l.advance(1)
						continue
					}
					break
				}
				l.emit(tNumber, l.src[start:l.pos], line, col)
				continue
			}
			op := ""
			for _, o := range threeCharOps {
				if strings.HasPrefix(l.src[l.pos:], o) {
					op = o
					break
				}
			}
			if op == "" {
				for _, o := range twoCharOps {
					if strings.HasPrefix(l.src[l.pos:], o) {
						op = o
						break
					}
				}
			}
			if op == "" {
				if strings.ContainsRune("+-*/%@&|^~<>()[]{},:;.=!", rune(c)) {
					op = string(c)
				} else {
					return nil, &SyntaxError{fmt.Sprintf("invalid character %q", r), line}
				}
			}
			switch op {
			case "(", "[", "{":
				l.depth++
			case ")", "]", "}":
				if l.depth > 0 {
					l.depth--
				}
			}
			l.advance(len(op))
			l.emit(tOp, op, line, col)
		}
	}
	if n := len(l.toks); n > 0 && l.toks[n-1].kind != tNewline && l.toks[n-1].kind != tDedent {
		l.emit(tNewline, "\n", l.line, 0)
	}
	for len(l.indent) > 1 {
		l.indent = l.indent[:len(l.indent)-1]
		l.emit(tDedent, "", l.line, 0)
	}
	l.emit(tEOF, "", l.line, 0)
	return l.toks, nil
}
