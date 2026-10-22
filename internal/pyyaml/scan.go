package pyyaml

// A line-by-line port of PyYAML 6's reader.py and scanner.py (string input),
// so that parse errors carry the exact MarkedYAMLError text and are raised
// at the same point of the token stream. Errors are raised by panicking
// with *MarkedYAMLError / *ReaderError and recovered by the public API.

import (
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

type tokenKind int

const (
	tStreamStart tokenKind = iota
	tStreamEnd
	tDirective
	tDocumentStart
	tDocumentEnd
	tBlockSequenceStart
	tBlockMappingStart
	tBlockEnd
	tFlowSequenceStart
	tFlowMappingStart
	tFlowSequenceEnd
	tFlowMappingEnd
	tKey
	tValue
	tBlockEntry
	tFlowEntry
	tAlias
	tAnchor
	tTag
	tScalar
)

var tokenIDs = map[tokenKind]string{
	tStreamStart: "<stream start>", tStreamEnd: "<stream end>", tDirective: "<directive>",
	tDocumentStart: "<document start>", tDocumentEnd: "<document end>",
	tBlockSequenceStart: "<block sequence start>", tBlockMappingStart: "<block mapping start>",
	tBlockEnd: "<block end>", tFlowSequenceStart: "[", tFlowMappingStart: "{",
	tFlowSequenceEnd: "]", tFlowMappingEnd: "}", tKey: "?", tValue: ":", tBlockEntry: "-",
	tFlowEntry: ",", tAlias: "<alias>", tAnchor: "<anchor>", tTag: "<tag>", tScalar: "<scalar>",
}

type token struct {
	kind       tokenKind
	start, end *Mark
	value      string // scalar / alias / anchor value; directive name
	plain      bool
	style      rune // 0 (plain), '\'', '"', '|', '>'
	// directives
	major, minor int
	hasVersion   bool
	tagHandle    string
	tagPrefix    string
	// tags: handle ("" with hasHandle=false is None)
	hasHandle bool
	suffix    string
}

func (t *token) id() string { return tokenIDs[t.kind] }

type simpleKey struct {
	tokenNumber int
	required    bool
	index       int
	line        int
	column      int
	mark        *Mark
}

type scanner struct {
	dialect Dialect
	name    string
	buffer  []rune
	pointer int
	index   int
	line    int
	column  int

	done           bool
	flowLevel      int
	tokens         []*token
	tokensTaken    int
	indent         int
	indents        []int
	allowSimpleKey bool
	// possibleSimpleKeys keeps Python dict semantics (insertion order).
	possibleSimpleKeys map[int]*simpleKey
	keyOrder           []int
}

func newScanner(src string, d Dialect) *scanner {
	s := &scanner{dialect: d, name: "<unicode string>"}
	runes := []rune(src)
	s.checkPrintable(runes)
	s.buffer = append(runes, 0)
	s.indent = -1
	s.allowSimpleKey = true
	s.possibleSimpleKeys = map[int]*simpleKey{}
	s.fetchStreamStart()
	return s
}

func (s *scanner) fail(context string, contextMark *Mark, problem string, problemMark *Mark) {
	panic(&MarkedYAMLError{Class: "ScannerError", Context: context, ContextMark: contextMark, Problem: problem, ProblemMark: problemMark, dialect: s.dialect})
}

func repr(r rune) string { return pyjson.ReprString(string(r)) }

// ---------------------------------------------------------------- reader

func isPrintable(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D || (r >= 0x20 && r <= 0x7E) || r == 0x85 ||
		(r >= 0xA0 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF)
}

func (s *scanner) checkPrintable(data []rune) {
	for i, r := range data {
		if !isPrintable(r) {
			panic(&ReaderError{Name: s.name, Position: i, Character: r, Byte: -1, Encoding: "unicode", Reason: "special characters are not allowed"})
		}
	}
}

func (s *scanner) peek(i int) rune {
	if p := s.pointer + i; p < len(s.buffer) {
		return s.buffer[p]
	}
	return 0
}

func (s *scanner) prefix(n int) string {
	end := min(s.pointer+n, len(s.buffer))
	return string(s.buffer[s.pointer:end])
}

func (s *scanner) forward(n int) {
	for ; n > 0 && s.pointer < len(s.buffer); n-- {
		ch := s.buffer[s.pointer]
		s.pointer++
		s.index++
		if ch == '\n' || ch == 0x85 || ch == 0x2028 || ch == 0x2029 || (ch == '\r' && s.peek(0) != '\n') {
			s.line++
			s.column = 0
		} else if ch != 0xFEFF {
			s.column++
		}
	}
}

func (s *scanner) getMark() *Mark {
	return &Mark{Name: s.name, Index: s.index, Line: s.line, Column: s.column, Buffer: s.buffer, Pointer: s.pointer, dialect: s.dialect}
}

// in reports whether r is one of the runes in set.
func in(r rune, set string) bool { return strings.ContainsRune(set, r) }

const (
	breakZ      = "\x00\r\n\u0085  "
	blankBreakZ = "\x00 \t\r\n\u0085  "
	spaceBreakZ = "\x00 \r\n\u0085  "
	breaks      = "\r\n\u0085  "
)

// in0 is `ch in set` where set contains NUL: Go strings.ContainsRune
// handles 0 as a rune fine, kept for readability.
func isAlnumDash(ch rune) bool {
	return ('0' <= ch && ch <= '9') || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z') || ch == '-' || ch == '_'
}

// ---------------------------------------------------------------- public token API

func (s *scanner) checkToken(kinds ...tokenKind) bool {
	for s.needMoreTokens() {
		s.fetchMoreTokens()
	}
	if len(s.tokens) > 0 {
		if len(kinds) == 0 {
			return true
		}
		for _, k := range kinds {
			if s.tokens[0].kind == k {
				return true
			}
		}
	}
	return false
}

func (s *scanner) peekToken() *token {
	for s.needMoreTokens() {
		s.fetchMoreTokens()
	}
	if len(s.tokens) > 0 {
		return s.tokens[0]
	}
	return nil
}

func (s *scanner) getToken() *token {
	for s.needMoreTokens() {
		s.fetchMoreTokens()
	}
	if len(s.tokens) > 0 {
		s.tokensTaken++
		t := s.tokens[0]
		s.tokens = s.tokens[1:]
		return t
	}
	return nil
}

func (s *scanner) needMoreTokens() bool {
	if s.done {
		return false
	}
	if len(s.tokens) == 0 {
		return true
	}
	s.staleePossibleSimpleKeys()
	n, ok := s.nextPossibleSimpleKey()
	return ok && n == s.tokensTaken
}

func (s *scanner) fetchMoreTokens() {
	s.scanToNextToken()
	s.staleePossibleSimpleKeys()
	s.unwindIndent(s.column)
	ch := s.peek(0)
	switch {
	case ch == 0:
		s.fetchStreamEnd()
		return
	case ch == '%' && s.checkDirective():
		s.fetchDirective()
		return
	case ch == '-' && s.checkDocumentStart():
		s.fetchDocumentIndicator(tDocumentStart)
		return
	case ch == '.' && s.checkDocumentEnd():
		s.fetchDocumentIndicator(tDocumentEnd)
		return
	case ch == '[':
		s.fetchFlowCollectionStart(tFlowSequenceStart)
		return
	case ch == '{':
		s.fetchFlowCollectionStart(tFlowMappingStart)
		return
	case ch == ']':
		s.fetchFlowCollectionEnd(tFlowSequenceEnd)
		return
	case ch == '}':
		s.fetchFlowCollectionEnd(tFlowMappingEnd)
		return
	case ch == ',':
		s.fetchFlowEntry()
		return
	case ch == '-' && s.checkBlockEntry():
		s.fetchBlockEntry()
		return
	case ch == '?' && s.checkKey():
		s.fetchKey()
		return
	case ch == ':' && s.checkValue():
		s.fetchValue()
		return
	case ch == '*':
		s.fetchAnchor(tAlias)
		return
	case ch == '&':
		s.fetchAnchor(tAnchor)
		return
	case ch == '!':
		s.saveSimpleKey()
		s.allowSimpleKey = false
		s.tokens = append(s.tokens, s.scanTag())
		return
	case ch == '|' && s.flowLevel == 0:
		s.fetchBlockScalar('|')
		return
	case ch == '>' && s.flowLevel == 0:
		s.fetchBlockScalar('>')
		return
	case ch == '\'':
		s.fetchFlowScalar('\'')
		return
	case ch == '"':
		s.fetchFlowScalar('"')
		return
	}
	if s.checkPlain() {
		s.saveSimpleKey()
		s.allowSimpleKey = false
		s.tokens = append(s.tokens, s.scanPlain())
		return
	}
	s.fail("while scanning for the next token", nil, "found character "+repr(ch)+" that cannot start any token", s.getMark())
}

// ---------------------------------------------------------------- simple keys

func (s *scanner) nextPossibleSimpleKey() (int, bool) {
	found := false
	minN := 0
	for _, level := range s.keyOrder {
		k := s.possibleSimpleKeys[level]
		if !found || k.tokenNumber < minN {
			minN, found = k.tokenNumber, true
		}
	}
	return minN, found
}

func (s *scanner) delKey(level int) {
	delete(s.possibleSimpleKeys, level)
	for i, l := range s.keyOrder {
		if l == level {
			s.keyOrder = append(s.keyOrder[:i:i], s.keyOrder[i+1:]...)
			break
		}
	}
}

func (s *scanner) staleePossibleSimpleKeys() {
	for _, level := range append([]int(nil), s.keyOrder...) {
		k := s.possibleSimpleKeys[level]
		if k.line != s.line || s.index-k.index > 1024 {
			if k.required {
				s.fail("while scanning a simple key", k.mark, "could not find expected ':'", s.getMark())
			}
			s.delKey(level)
		}
	}
}

func (s *scanner) saveSimpleKey() {
	required := s.flowLevel == 0 && s.indent == s.column
	if s.allowSimpleKey {
		s.removePossibleSimpleKey()
		k := &simpleKey{tokenNumber: s.tokensTaken + len(s.tokens), required: required,
			index: s.index, line: s.line, column: s.column, mark: s.getMark()}
		s.possibleSimpleKeys[s.flowLevel] = k
		s.keyOrder = append(s.keyOrder, s.flowLevel)
	}
}

func (s *scanner) removePossibleSimpleKey() {
	if k, ok := s.possibleSimpleKeys[s.flowLevel]; ok {
		if k.required {
			s.fail("while scanning a simple key", k.mark, "could not find expected ':'", s.getMark())
		}
		s.delKey(s.flowLevel)
	}
}

// ---------------------------------------------------------------- indentation

func (s *scanner) unwindIndent(column int) {
	if s.flowLevel > 0 {
		return
	}
	for s.indent > column {
		m := s.getMark()
		s.indent = s.indents[len(s.indents)-1]
		s.indents = s.indents[:len(s.indents)-1]
		s.tokens = append(s.tokens, &token{kind: tBlockEnd, start: m, end: m})
	}
}

func (s *scanner) addIndent(column int) bool {
	if s.indent < column {
		s.indents = append(s.indents, s.indent)
		s.indent = column
		return true
	}
	return false
}

// ---------------------------------------------------------------- fetchers

func (s *scanner) fetchStreamStart() {
	m := s.getMark()
	s.tokens = append(s.tokens, &token{kind: tStreamStart, start: m, end: m})
}

func (s *scanner) fetchStreamEnd() {
	s.unwindIndent(-1)
	s.removePossibleSimpleKey()
	s.allowSimpleKey = false
	s.possibleSimpleKeys = map[int]*simpleKey{}
	s.keyOrder = nil
	m := s.getMark()
	s.tokens = append(s.tokens, &token{kind: tStreamEnd, start: m, end: m})
	s.done = true
}

func (s *scanner) fetchDirective() {
	s.unwindIndent(-1)
	s.removePossibleSimpleKey()
	s.allowSimpleKey = false
	s.tokens = append(s.tokens, s.scanDirective())
}

func (s *scanner) fetchDocumentIndicator(kind tokenKind) {
	s.unwindIndent(-1)
	s.removePossibleSimpleKey()
	s.allowSimpleKey = false
	start := s.getMark()
	s.forward(3)
	s.tokens = append(s.tokens, &token{kind: kind, start: start, end: s.getMark()})
}

func (s *scanner) fetchFlowCollectionStart(kind tokenKind) {
	s.saveSimpleKey()
	s.flowLevel++
	s.allowSimpleKey = true
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: kind, start: start, end: s.getMark()})
}

func (s *scanner) fetchFlowCollectionEnd(kind tokenKind) {
	s.removePossibleSimpleKey()
	s.flowLevel--
	s.allowSimpleKey = false
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: kind, start: start, end: s.getMark()})
}

func (s *scanner) fetchFlowEntry() {
	s.allowSimpleKey = true
	s.removePossibleSimpleKey()
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: tFlowEntry, start: start, end: s.getMark()})
}

func (s *scanner) fetchBlockEntry() {
	if s.flowLevel == 0 {
		if !s.allowSimpleKey {
			s.fail("", nil, "sequence entries are not allowed here", s.getMark())
		}
		if s.addIndent(s.column) {
			m := s.getMark()
			s.tokens = append(s.tokens, &token{kind: tBlockSequenceStart, start: m, end: m})
		}
	}
	s.allowSimpleKey = true
	s.removePossibleSimpleKey()
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: tBlockEntry, start: start, end: s.getMark()})
}

func (s *scanner) fetchKey() {
	if s.flowLevel == 0 {
		if !s.allowSimpleKey {
			s.fail("", nil, "mapping keys are not allowed here", s.getMark())
		}
		if s.addIndent(s.column) {
			m := s.getMark()
			s.tokens = append(s.tokens, &token{kind: tBlockMappingStart, start: m, end: m})
		}
	}
	s.allowSimpleKey = s.flowLevel == 0
	s.removePossibleSimpleKey()
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: tKey, start: start, end: s.getMark()})
}

func (s *scanner) insertToken(at int, t *token) {
	s.tokens = append(s.tokens, nil)
	copy(s.tokens[at+1:], s.tokens[at:])
	s.tokens[at] = t
}

func (s *scanner) fetchValue() {
	if k, ok := s.possibleSimpleKeys[s.flowLevel]; ok {
		s.delKey(s.flowLevel)
		s.insertToken(k.tokenNumber-s.tokensTaken, &token{kind: tKey, start: k.mark, end: k.mark})
		if s.flowLevel == 0 && s.addIndent(k.column) {
			s.insertToken(k.tokenNumber-s.tokensTaken, &token{kind: tBlockMappingStart, start: k.mark, end: k.mark})
		}
		s.allowSimpleKey = false
	} else {
		if s.flowLevel == 0 && !s.allowSimpleKey {
			s.fail("", nil, "mapping values are not allowed here", s.getMark())
		}
		if s.flowLevel == 0 && s.addIndent(s.column) {
			m := s.getMark()
			s.tokens = append(s.tokens, &token{kind: tBlockMappingStart, start: m, end: m})
		}
		s.allowSimpleKey = s.flowLevel == 0
		s.removePossibleSimpleKey()
	}
	start := s.getMark()
	s.forward(1)
	s.tokens = append(s.tokens, &token{kind: tValue, start: start, end: s.getMark()})
}

func (s *scanner) fetchAnchor(kind tokenKind) {
	s.saveSimpleKey()
	s.allowSimpleKey = false
	s.tokens = append(s.tokens, s.scanAnchor(kind))
}

func (s *scanner) fetchBlockScalar(style rune) {
	s.allowSimpleKey = true
	s.removePossibleSimpleKey()
	s.tokens = append(s.tokens, s.scanBlockScalar(style))
}

func (s *scanner) fetchFlowScalar(style rune) {
	s.saveSimpleKey()
	s.allowSimpleKey = false
	s.tokens = append(s.tokens, s.scanFlowScalar(style))
}

// ---------------------------------------------------------------- checkers

func (s *scanner) checkDirective() bool { return s.column == 0 }

func (s *scanner) checkDocumentStart() bool {
	return s.column == 0 && s.prefix(3) == "---" && in(s.peek(3), blankBreakZ)
}

func (s *scanner) checkDocumentEnd() bool {
	return s.column == 0 && s.prefix(3) == "..." && in(s.peek(3), blankBreakZ)
}

func (s *scanner) checkBlockEntry() bool { return in(s.peek(1), blankBreakZ) }

func (s *scanner) checkKey() bool { return s.flowLevel > 0 || in(s.peek(1), blankBreakZ) }

func (s *scanner) checkValue() bool { return s.flowLevel > 0 || in(s.peek(1), blankBreakZ) }

func (s *scanner) checkPlain() bool {
	ch := s.peek(0)
	return !in(ch, "\x00 \t\r\n\u0085  -?:,[]{}#&*!|>'\"%@`") ||
		(!in(s.peek(1), blankBreakZ) && (ch == '-' || (s.flowLevel == 0 && in(ch, "?:"))))
}

// ---------------------------------------------------------------- scanners

func (s *scanner) scanToNextToken() {
	if s.index == 0 && s.peek(0) == 0xFEFF {
		s.forward(1)
	}
	for {
		for s.peek(0) == ' ' {
			s.forward(1)
		}
		if s.peek(0) == '#' {
			for !in(s.peek(0), breakZ) {
				s.forward(1)
			}
		}
		if s.scanLineBreak() != "" {
			if s.flowLevel == 0 {
				s.allowSimpleKey = true
			}
		} else {
			return
		}
	}
}

func (s *scanner) scanDirective() *token {
	start := s.getMark()
	s.forward(1)
	name := s.scanDirectiveName(start)
	t := &token{kind: tDirective, value: name, start: start}
	switch name {
	case "YAML":
		t.major, t.minor = s.scanYAMLDirectiveValue(start)
		t.hasVersion = true
		t.end = s.getMark()
	case "TAG":
		t.tagHandle, t.tagPrefix = s.scanTagDirectiveValue(start)
		t.end = s.getMark()
	default:
		t.end = s.getMark()
		for !in(s.peek(0), breakZ) {
			s.forward(1)
		}
	}
	s.scanDirectiveIgnoredLine(start)
	return t
}

func (s *scanner) scanDirectiveName(start *Mark) string {
	length := 0
	ch := s.peek(length)
	for isAlnumDash(ch) {
		length++
		ch = s.peek(length)
	}
	if length == 0 {
		s.fail("while scanning a directive", start, "expected alphabetic or numeric character, but found "+repr(ch), s.getMark())
	}
	value := s.prefix(length)
	s.forward(length)
	ch = s.peek(0)
	if !in(ch, spaceBreakZ) {
		s.fail("while scanning a directive", start, "expected alphabetic or numeric character, but found "+repr(ch), s.getMark())
	}
	return value
}

func (s *scanner) scanYAMLDirectiveValue(start *Mark) (int, int) {
	for s.peek(0) == ' ' {
		s.forward(1)
	}
	major := s.scanYAMLDirectiveNumber(start)
	if s.peek(0) != '.' {
		s.fail("while scanning a directive", start, "expected a digit or '.', but found "+repr(s.peek(0)), s.getMark())
	}
	s.forward(1)
	minor := s.scanYAMLDirectiveNumber(start)
	if !in(s.peek(0), spaceBreakZ) {
		s.fail("while scanning a directive", start, "expected a digit or ' ', but found "+repr(s.peek(0)), s.getMark())
	}
	return major, minor
}

func (s *scanner) scanYAMLDirectiveNumber(start *Mark) int {
	ch := s.peek(0)
	if !('0' <= ch && ch <= '9') {
		s.fail("while scanning a directive", start, "expected a digit, but found "+repr(ch), s.getMark())
	}
	length := 0
	for c := s.peek(length); '0' <= c && c <= '9'; c = s.peek(length) {
		length++
	}
	v, _ := strconv.Atoi(s.prefix(length))
	s.forward(length)
	return v
}

func (s *scanner) scanTagDirectiveValue(start *Mark) (string, string) {
	for s.peek(0) == ' ' {
		s.forward(1)
	}
	handle := s.scanTagHandle("directive", start)
	if ch := s.peek(0); ch != ' ' {
		s.fail("while scanning a directive", start, "expected ' ', but found "+repr(ch), s.getMark())
	}
	for s.peek(0) == ' ' {
		s.forward(1)
	}
	prefix := s.scanTagURI("directive", start)
	if ch := s.peek(0); !in(ch, spaceBreakZ) {
		s.fail("while scanning a directive", start, "expected ' ', but found "+repr(ch), s.getMark())
	}
	return handle, prefix
}

func (s *scanner) scanDirectiveIgnoredLine(start *Mark) {
	for s.peek(0) == ' ' {
		s.forward(1)
	}
	if s.peek(0) == '#' {
		for !in(s.peek(0), breakZ) {
			s.forward(1)
		}
	}
	if ch := s.peek(0); !in(ch, breakZ) {
		s.fail("while scanning a directive", start, "expected a comment or a line break, but found "+repr(ch), s.getMark())
	}
	s.scanLineBreak()
}

func (s *scanner) scanAnchor(kind tokenKind) *token {
	start := s.getMark()
	name := "anchor"
	if s.peek(0) == '*' {
		name = "alias"
	}
	s.forward(1)
	length := 0
	ch := s.peek(length)
	if s.dialect == Ruamel {
		// ruamel accepts any non-space, non-flow-indicator character.
		for !in(ch, "\x00 \t\r\n\u0085  ,[]{}%@`") {
			if ch == ':' && in(s.peek(length+1), blankBreakZ+",[]{}") {
				break
			}
			length++
			ch = s.peek(length)
		}
	} else {
		for isAlnumDash(ch) {
			length++
			ch = s.peek(length)
		}
	}
	if length == 0 {
		s.fail("while scanning an "+name, start, "expected alphabetic or numeric character, but found "+repr(ch), s.getMark())
	}
	value := s.prefix(length)
	s.forward(length)
	ch = s.peek(0)
	if !in(ch, "\x00 \t\r\n\u0085  ?:,]}%@`") {
		s.fail("while scanning an "+name, start, "expected alphabetic or numeric character, but found "+repr(ch), s.getMark())
	}
	return &token{kind: kind, value: value, start: start, end: s.getMark()}
}

func (s *scanner) scanTag() *token {
	start := s.getMark()
	ch := s.peek(1)
	t := &token{kind: tTag, start: start}
	switch {
	case ch == '<':
		s.forward(2)
		t.suffix = s.scanTagURI("tag", start)
		if s.peek(0) != '>' {
			s.fail("while parsing a tag", start, "expected '>', but found "+repr(s.peek(0)), s.getMark())
		}
		s.forward(1)
	case in(ch, blankBreakZ):
		t.suffix = "!"
		s.forward(1)
	default:
		length := 1
		useHandle := false
		for !in(ch, spaceBreakZ) {
			if ch == '!' {
				useHandle = true
				break
			}
			length++
			ch = s.peek(length)
		}
		t.hasHandle = true
		if useHandle {
			t.tagHandle = s.scanTagHandle("tag", start)
		} else {
			t.tagHandle = "!"
			s.forward(1)
		}
		t.suffix = s.scanTagURI("tag", start)
	}
	if ch := s.peek(0); !in(ch, spaceBreakZ) {
		s.fail("while scanning a tag", start, "expected ' ', but found "+repr(ch), s.getMark())
	}
	t.end = s.getMark()
	return t
}

func (s *scanner) scanBlockScalar(style rune) *token {
	folded := style == '>'
	var chunks strings.Builder
	start := s.getMark()
	s.forward(1)
	chomping, increment := s.scanBlockScalarIndicators(start)
	s.scanBlockScalarIgnoredLine(start)
	minIndent := s.indent + 1
	if minIndent < 1 {
		minIndent = 1
	}
	var brks []string
	var end *Mark
	var indent int
	if increment == 0 {
		var maxIndent int
		brks, maxIndent, end = s.scanBlockScalarIndentation()
		indent = max(minIndent, maxIndent)
	} else {
		indent = minIndent + increment - 1
		brks, end = s.scanBlockScalarBreaks(indent)
	}
	lineBreak := ""
	for s.column == indent && s.peek(0) != 0 {
		for _, b := range brks {
			chunks.WriteString(b)
		}
		leadingNonSpace := !in(s.peek(0), " \t")
		length := 0
		for !in(s.peek(length), breakZ) {
			length++
		}
		chunks.WriteString(s.prefix(length))
		s.forward(length)
		lineBreak = s.scanLineBreak()
		brks, end = s.scanBlockScalarBreaks(indent)
		if s.column == indent && s.peek(0) != 0 {
			if folded && lineBreak == "\n" && leadingNonSpace && !in(s.peek(0), " \t") {
				if len(brks) == 0 {
					chunks.WriteString(" ")
				}
			} else {
				chunks.WriteString(lineBreak)
			}
		} else {
			break
		}
	}
	if chomping != 'F' {
		chunks.WriteString(lineBreak)
	}
	if chomping == 'T' {
		for _, b := range brks {
			chunks.WriteString(b)
		}
	}
	return &token{kind: tScalar, value: chunks.String(), style: style, start: start, end: end}
}

// scanBlockScalarIndicators returns chomping ('T' keep, 'F' strip, 0 clip)
// and the increment (0 = none).
func (s *scanner) scanBlockScalarIndicators(start *Mark) (rune, int) {
	var chomping rune
	increment := 0
	ch := s.peek(0)
	zero := func() {
		if s.dialect == Ruamel {
			s.fail("while scanning a block scalar", start, "expected indentation indicator in the range 1-9, but found 0", s.getMark())
		}
		s.fail("while scanning a block scalar", start, "expected indentation indicator in the range 1-9, but found 0", s.getMark())
	}
	if ch == '+' || ch == '-' {
		chomping = 'F'
		if ch == '+' {
			chomping = 'T'
		}
		s.forward(1)
		ch = s.peek(0)
		if '0' <= ch && ch <= '9' {
			increment = int(ch - '0')
			if increment == 0 {
				zero()
			}
			s.forward(1)
		}
	} else if '0' <= ch && ch <= '9' {
		increment = int(ch - '0')
		if increment == 0 {
			zero()
		}
		s.forward(1)
		ch = s.peek(0)
		if ch == '+' || ch == '-' {
			chomping = 'F'
			if ch == '+' {
				chomping = 'T'
			}
			s.forward(1)
		}
	}
	if ch := s.peek(0); !in(ch, spaceBreakZ) {
		s.fail("while scanning a block scalar", start, "expected chomping or indentation indicators, but found "+repr(ch), s.getMark())
	}
	return chomping, increment
}

func (s *scanner) scanBlockScalarIgnoredLine(start *Mark) {
	for s.peek(0) == ' ' {
		s.forward(1)
	}
	if s.peek(0) == '#' {
		for !in(s.peek(0), breakZ) {
			s.forward(1)
		}
	}
	if ch := s.peek(0); !in(ch, breakZ) {
		s.fail("while scanning a block scalar", start, "expected a comment or a line break, but found "+repr(ch), s.getMark())
	}
	s.scanLineBreak()
}

func (s *scanner) scanBlockScalarIndentation() ([]string, int, *Mark) {
	var chunks []string
	maxIndent := 0
	end := s.getMark()
	for in(s.peek(0), " \r\n\u0085  ") {
		if s.peek(0) != ' ' {
			chunks = append(chunks, s.scanLineBreak())
			end = s.getMark()
		} else {
			s.forward(1)
			if s.column > maxIndent {
				maxIndent = s.column
			}
		}
	}
	return chunks, maxIndent, end
}

func (s *scanner) scanBlockScalarBreaks(indent int) ([]string, *Mark) {
	var chunks []string
	end := s.getMark()
	for s.column < indent && s.peek(0) == ' ' {
		s.forward(1)
	}
	for in(s.peek(0), breaks) {
		chunks = append(chunks, s.scanLineBreak())
		end = s.getMark()
		for s.column < indent && s.peek(0) == ' ' {
			s.forward(1)
		}
	}
	return chunks, end
}

func (s *scanner) scanFlowScalar(style rune) *token {
	double := style == '"'
	var chunks strings.Builder
	start := s.getMark()
	quote := s.peek(0)
	s.forward(1)
	s.scanFlowScalarNonSpaces(&chunks, double, start)
	for s.peek(0) != quote {
		s.scanFlowScalarSpaces(&chunks, double, start)
		s.scanFlowScalarNonSpaces(&chunks, double, start)
	}
	s.forward(1)
	return &token{kind: tScalar, value: chunks.String(), style: style, start: start, end: s.getMark()}
}

var escapeReplacements = map[rune]string{
	'0': "\x00", 'a': "\x07", 'b': "\x08", 't': "\x09", '\t': "\x09", 'n': "\x0A", 'v': "\x0B",
	'f': "\x0C", 'r': "\x0D", 'e': "\x1B", ' ': "\x20", '"': "\"", '\\': "\\", '/': "/",
	'N': "\u0085", '_': " ", 'L': " ", 'P': " ",
}

var escapeCodes = map[rune]int{'x': 2, 'u': 4, 'U': 8}

func isHex(r rune) bool {
	return ('0' <= r && r <= '9') || ('A' <= r && r <= 'F') || ('a' <= r && r <= 'f')
}

func (s *scanner) scanFlowScalarNonSpaces(chunks *strings.Builder, double bool, start *Mark) {
	for {
		length := 0
		for !in(s.peek(length), "'\"\\\x00 \t\r\n\u0085  ") {
			length++
		}
		if length > 0 {
			chunks.WriteString(s.prefix(length))
			s.forward(length)
		}
		ch := s.peek(0)
		switch {
		case !double && ch == '\'' && s.peek(1) == '\'':
			chunks.WriteString("'")
			s.forward(2)
		case (double && ch == '\'') || (!double && in(ch, "\"\\")):
			chunks.WriteRune(ch)
			s.forward(1)
		case double && ch == '\\':
			s.forward(1)
			ch = s.peek(0)
			if rep, ok := escapeReplacements[ch]; ok {
				chunks.WriteString(rep)
				s.forward(1)
			} else if n, ok := escapeCodes[ch]; ok {
				s.forward(1)
				for k := 0; k < n; k++ {
					if !isHex(s.peek(k)) {
						if s.dialect == Ruamel {
							s.fail("while scanning a double-quoted scalar", start,
								"expected escape sequence of "+strconv.Itoa(n)+" hexdecimal numbers, but found "+repr(s.peek(k)), s.getMark())
						}
						s.fail("while scanning a double-quoted scalar", start,
							"expected escape sequence of "+strconv.Itoa(n)+" hexadecimal numbers, but found "+repr(s.peek(k)), s.getMark())
					}
				}
				code, _ := strconv.ParseInt(s.prefix(n), 16, 64)
				if code > 0x10FFFF {
					panic(&PyError{"ValueError", "chr() arg not in range(0x110000)"})
				}
				chunks.WriteRune(rune(code))
				s.forward(n)
			} else if in(ch, breaks) {
				s.scanLineBreak()
				for _, b := range s.scanFlowScalarBreaks(double, start) {
					chunks.WriteString(b)
				}
			} else {
				s.fail("while scanning a double-quoted scalar", start, "found unknown escape character "+repr(ch), s.getMark())
			}
		default:
			return
		}
	}
}

func (s *scanner) scanFlowScalarSpaces(chunks *strings.Builder, double bool, start *Mark) {
	length := 0
	for in(s.peek(length), " \t") {
		length++
	}
	whitespaces := s.prefix(length)
	s.forward(length)
	ch := s.peek(0)
	switch {
	case ch == 0:
		s.fail("while scanning a quoted scalar", start, "found unexpected end of stream", s.getMark())
	case in(ch, breaks):
		lineBreak := s.scanLineBreak()
		brks := s.scanFlowScalarBreaks(double, start)
		if lineBreak != "\n" {
			chunks.WriteString(lineBreak)
		} else if len(brks) == 0 {
			chunks.WriteString(" ")
		}
		for _, b := range brks {
			chunks.WriteString(b)
		}
	default:
		chunks.WriteString(whitespaces)
	}
}

func (s *scanner) scanFlowScalarBreaks(double bool, start *Mark) []string {
	var chunks []string
	for {
		p := s.prefix(3)
		if (p == "---" || p == "...") && in(s.peek(3), blankBreakZ) {
			s.fail("while scanning a quoted scalar", start, "found unexpected document separator", s.getMark())
		}
		for in(s.peek(0), " \t") {
			s.forward(1)
		}
		if in(s.peek(0), breaks) {
			chunks = append(chunks, s.scanLineBreak())
		} else {
			return chunks
		}
	}
}

func (s *scanner) scanPlain() *token {
	var chunks strings.Builder
	start := s.getMark()
	end := start
	indent := s.indent + 1
	var spaces []string
	for {
		length := 0
		if s.peek(0) == '#' {
			break
		}
		for {
			ch := s.peek(length)
			stop := blankBreakZ
			if s.flowLevel > 0 {
				stop += ",[]{}"
			}
			if in(ch, blankBreakZ) || (ch == ':' && in(s.peek(length+1), stop)) || (s.flowLevel > 0 && in(ch, ",?[]{}")) {
				break
			}
			length++
		}
		if length == 0 {
			break
		}
		s.allowSimpleKey = false
		for _, sp := range spaces {
			chunks.WriteString(sp)
		}
		chunks.WriteString(s.prefix(length))
		s.forward(length)
		end = s.getMark()
		var ok bool
		spaces, ok = s.scanPlainSpaces(indent, start)
		if !ok || len(spaces) == 0 || s.peek(0) == '#' || (s.flowLevel == 0 && s.column < indent) {
			break
		}
	}
	return &token{kind: tScalar, value: chunks.String(), plain: true, start: start, end: end}
}

// scanPlainSpaces returns ok=false where Python returns None.
func (s *scanner) scanPlainSpaces(indent int, start *Mark) ([]string, bool) {
	var chunks []string
	length := 0
	for s.peek(length) == ' ' {
		length++
	}
	whitespaces := s.prefix(length)
	s.forward(length)
	ch := s.peek(0)
	if in(ch, breaks) {
		lineBreak := s.scanLineBreak()
		s.allowSimpleKey = true
		p := s.prefix(3)
		if (p == "---" || p == "...") && in(s.peek(3), blankBreakZ) {
			return nil, false
		}
		var brks []string
		for in(s.peek(0), " \r\n\u0085  ") {
			if s.peek(0) == ' ' {
				s.forward(1)
			} else {
				brks = append(brks, s.scanLineBreak())
				p := s.prefix(3)
				if (p == "---" || p == "...") && in(s.peek(3), blankBreakZ) {
					return nil, false
				}
			}
		}
		if lineBreak != "\n" {
			chunks = append(chunks, lineBreak)
		} else if len(brks) == 0 {
			chunks = append(chunks, " ")
		}
		chunks = append(chunks, brks...)
	} else if whitespaces != "" {
		chunks = append(chunks, whitespaces)
	}
	return chunks, true
}

func (s *scanner) scanTagHandle(name string, start *Mark) string {
	ch := s.peek(0)
	if ch != '!' {
		s.fail("while scanning a "+name, start, "expected '!', but found "+repr(ch), s.getMark())
	}
	length := 1
	ch = s.peek(length)
	if ch != ' ' {
		for isAlnumDash(ch) {
			length++
			ch = s.peek(length)
		}
		if ch != '!' {
			s.forward(length)
			s.fail("while scanning a "+name, start, "expected '!', but found "+repr(ch), s.getMark())
		}
		length++
	}
	value := s.prefix(length)
	s.forward(length)
	return value
}

func (s *scanner) scanTagURI(name string, start *Mark) string {
	var chunks []string
	length := 0
	ch := s.peek(length)
	for ('0' <= ch && ch <= '9') || ('A' <= ch && ch <= 'Z') || ('a' <= ch && ch <= 'z') || in(ch, "-;/?:@&=+$,_.!~*'()[]%") {
		if ch == '%' {
			chunks = append(chunks, s.prefix(length))
			s.forward(length)
			length = 0
			chunks = append(chunks, s.scanURIEscapes(name, start))
		} else {
			length++
		}
		ch = s.peek(length)
	}
	if length > 0 {
		chunks = append(chunks, s.prefix(length))
		s.forward(length)
	}
	if len(chunks) == 0 {
		s.fail("while parsing a "+name, start, "expected URI, but found "+repr(ch), s.getMark())
	}
	return strings.Join(chunks, "")
}

func (s *scanner) scanURIEscapes(name string, start *Mark) string {
	var codes []byte
	mark := s.getMark()
	for s.peek(0) == '%' {
		s.forward(1)
		for k := 0; k < 2; k++ {
			if !isHex(s.peek(k)) {
				msg := "expected URI escape sequence of 2 hexadecimal numbers, but found "
				if s.dialect == Ruamel {
					msg = "expected URI escape sequence of 2 hexdecimal numbers, but found "
				}
				s.fail("while scanning a "+name, start, msg+repr(s.peek(k)), s.getMark())
			}
		}
		v, _ := strconv.ParseUint(s.prefix(2), 16, 8)
		codes = append(codes, byte(v))
		s.forward(2)
	}
	str, err := pyjson.DecodeUTF8(codes)
	if err != nil {
		s.fail("while scanning a "+name, start, err.Error(), mark)
	}
	return str
}

func (s *scanner) scanLineBreak() string {
	ch := s.peek(0)
	if in(ch, "\r\n\u0085") && ch != 0 {
		if s.prefix(2) == "\r\n" {
			s.forward(2)
		} else {
			s.forward(1)
		}
		return "\n"
	} else if ch == 0x2028 || ch == 0x2029 {
		s.forward(1)
		return string(ch)
	}
	return ""
}
