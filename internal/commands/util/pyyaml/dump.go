package pyyaml

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Options mirror yaml.dump keyword arguments.
type Options struct {
	SortKeys     bool // sort_keys (yaml.dump default True)
	AllowUnicode bool // allow_unicode
}

// emitter ports yaml.emitter.Emitter for block-style output.
type emitter struct {
	b          strings.Builder
	opts       Options
	indent     int // -1 = None
	indents    []int
	column     int
	whitespace bool
	indention  bool
	flowLevel  int
	openEnded  bool

	// expect_node context
	rootCtx, seqCtx, mapCtx, simpleKeyCtx bool
}

const bestIndent, bestWidth = 2, 80

// Dump is yaml.dump(v, default_flow_style=False, ...).
func Dump(v any, opts Options) string {
	e := &emitter{opts: opts, indent: -1, whitespace: true, indention: true}
	// expect_document_start (implicit, first document)
	e.node(v, true, false, false, false)
	// expect_document_end: write_indent
	e.writeIndent()
	// stream end
	if e.openEnded {
		e.writeIndicator("...", true, false, false)
		e.writeIndent()
	}
	return e.b.String()
}

func (e *emitter) write(s string) {
	e.b.WriteString(s)
	e.column += utf8.RuneCountInString(s)
}

func (e *emitter) increaseIndent(flow, indentless bool) {
	e.indents = append(e.indents, e.indent)
	if e.indent < 0 {
		if flow {
			e.indent = bestIndent
		} else {
			e.indent = 0
		}
	} else if !indentless {
		e.indent += bestIndent
	}
}

func (e *emitter) popIndent() {
	e.indent = e.indents[len(e.indents)-1]
	e.indents = e.indents[:len(e.indents)-1]
}

func (e *emitter) writeIndicator(ind string, needWS, whitespace, indention bool) {
	data := ind
	if !e.whitespace && needWS {
		data = " " + ind
	}
	e.whitespace = whitespace
	e.indention = e.indention && indention
	e.openEnded = false
	e.write(data)
}

func (e *emitter) writeIndent() {
	indent := e.indent
	if indent < 0 {
		indent = 0
	}
	if !e.indention || e.column > indent || (e.column == indent && !e.whitespace) {
		e.writeLineBreak("")
	}
	if e.column < indent {
		e.whitespace = true
		e.b.WriteString(strings.Repeat(" ", indent-e.column))
		e.column = indent
	}
}

func (e *emitter) writeLineBreak(data string) {
	if data == "" {
		data = "\n"
	}
	e.whitespace = true
	e.indention = true
	e.column = 0
	e.b.WriteString(data)
}

// scalar is a ScalarEvent: value text, implicit flags.
type scalar struct {
	value    string
	implicit [2]bool // (plain resolves to tag, quoted resolves to tag)
}

func represent(v any) (scalar, bool) {
	switch t := v.(type) {
	case nil:
		return scalar{"null", [2]bool{true, false}}, true
	case bool:
		if t {
			return scalar{"true", [2]bool{true, false}}, true
		}
		return scalar{"false", [2]bool{true, false}}, true
	case int:
		return scalar{fmt.Sprint(t), [2]bool{true, false}}, true
	case int64:
		return scalar{fmt.Sprint(t), [2]bool{true, false}}, true
	case *big.Int:
		return scalar{t.String(), [2]bool{true, false}}, true
	case float64:
		return scalar{representFloat(t), [2]bool{true, false}}, true
	case Date:
		return scalar{t.ISO(), [2]bool{true, false}}, true
	case Timestamp:
		return scalar{t.Repr, [2]bool{ResolvePlain(t.Repr) == "timestamp", false}}, true
	case string:
		return scalar{t, [2]bool{ResolvePlain(t) == "str", true}}, true
	}
	return scalar{}, false
}

func representFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return ".nan"
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	}
	value := strings.ToLower(pyjson.ReprFloat(f))
	if !strings.Contains(value, ".") && strings.Contains(value, "e") {
		i := strings.Index(value, "e")
		value = value[:i] + ".0" + value[i:]
	}
	return value
}

func (e *emitter) node(v any, root, seq, mapping, simpleKey bool) {
	e.rootCtx, e.seqCtx, e.mapCtx, e.simpleKeyCtx = root, seq, mapping, simpleKey
	switch t := v.(type) {
	case *jsonx.Object:
		if e.flowLevel > 0 || t.Len() == 0 {
			e.flowMapping(t)
		} else {
			e.blockMapping(t)
		}
	case []any:
		if e.flowLevel > 0 || len(t) == 0 {
			e.flowSequence(t)
		} else {
			e.blockSequence(t)
		}
	default:
		sc, ok := represent(v)
		if !ok {
			sc = scalar{fmt.Sprint(v), [2]bool{true, true}}
		}
		e.increaseIndent(true, false)
		e.processScalar(sc)
		e.popIndent()
	}
}

func (e *emitter) keys(o *jsonx.Object) []string {
	ks := o.Keys()
	if e.opts.SortKeys {
		pyjson.SortStrings(ks)
	}
	return ks
}

func (e *emitter) flowMapping(o *jsonx.Object) {
	e.writeIndicator("{", true, true, false)
	e.flowLevel++
	e.increaseIndent(true, false)
	for i, k := range e.keys(o) {
		if i > 0 {
			e.writeIndicator(",", false, false, false)
		}
		if e.column > bestWidth {
			e.writeIndent()
		}
		e.node(k, false, false, true, true)
		e.writeIndicator(":", false, false, false)
		v, _ := o.Get(k)
		e.node(v, false, false, true, false)
	}
	e.popIndent()
	e.flowLevel--
	e.writeIndicator("}", false, false, false)
}

func (e *emitter) flowSequence(l []any) {
	e.writeIndicator("[", true, true, false)
	e.flowLevel++
	e.increaseIndent(true, false)
	for i, v := range l {
		if i > 0 {
			e.writeIndicator(",", false, false, false)
		}
		if e.column > bestWidth {
			e.writeIndent()
		}
		e.node(v, false, true, false, false)
	}
	e.popIndent()
	e.flowLevel--
	e.writeIndicator("]", false, false, false)
}

func (e *emitter) blockSequence(l []any) {
	indentless := e.mapCtx && !e.indention
	e.increaseIndent(false, indentless)
	for _, v := range l {
		e.writeIndent()
		e.writeIndicator("-", true, false, true)
		e.node(v, false, true, false, false)
	}
	e.popIndent()
}

func (e *emitter) checkSimpleKey(k string) bool {
	a := analyze(k, e.opts.AllowUnicode)
	return utf8.RuneCountInString(k) < 128 && !a.empty && !a.multiline
}

func (e *emitter) blockMapping(o *jsonx.Object) {
	e.increaseIndent(false, false)
	for _, k := range e.keys(o) {
		e.writeIndent()
		v, _ := o.Get(k)
		if e.checkSimpleKey(k) {
			e.node(k, false, false, true, true)
			e.writeIndicator(":", false, false, false)
			e.node(v, false, false, true, false)
		} else {
			e.writeIndicator("?", true, false, true)
			e.node(k, false, false, true, false)
			e.writeIndent()
			e.writeIndicator(":", true, false, true)
			e.node(v, false, false, true, false)
		}
	}
	e.popIndent()
}

type analysis struct {
	empty, multiline                                               bool
	allowFlowPlain, allowBlockPlain, allowSingleQuoted, allowBlock bool
}

func isBreak(r rune) bool { return r == '\n' || r == 0x85 || r == 0x2028 || r == 0x2029 }

func isWSorNUL(r rune) bool { return r == 0 || r == ' ' || r == '\t' || r == '\r' || isBreak(r) }

func analyze(s string, allowUnicode bool) analysis {
	if s == "" {
		return analysis{empty: true, allowBlockPlain: true, allowSingleQuoted: true}
	}
	rs := []rune(s)
	var blockInd, flowInd, lineBreaks, special bool
	var leadingSpace, leadingBreak, trailingSpace, trailingBreak, breakSpace, spaceBreak bool
	if strings.HasPrefix(s, "---") || strings.HasPrefix(s, "...") {
		blockInd, flowInd = true, true
	}
	preceded := true
	followed := len(rs) == 1 || isWSorNUL(rs[1])
	prevSpace, prevBreak := false, false
	for i, ch := range rs {
		if i == 0 {
			if strings.ContainsRune("#,[]{}&*!|>'\"%@`", ch) {
				flowInd, blockInd = true, true
			}
			if ch == '?' || ch == ':' {
				flowInd = true
				if followed {
					blockInd = true
				}
			}
			if ch == '-' && followed {
				flowInd, blockInd = true, true
			}
		} else {
			if strings.ContainsRune(",?[]{}", ch) {
				flowInd = true
			}
			if ch == ':' {
				flowInd = true
				if followed {
					blockInd = true
				}
			}
			if ch == '#' && preceded {
				flowInd, blockInd = true, true
			}
		}
		if isBreak(ch) {
			lineBreaks = true
		}
		if !(ch == '\n' || (ch >= 0x20 && ch <= 0x7e)) {
			if (ch == 0x85 || (ch >= 0xa0 && ch <= 0xd7ff) || (ch >= 0xe000 && ch <= 0xfffd) || (ch >= 0x10000 && ch < 0x10ffff)) && ch != 0xfeff {
				if !allowUnicode {
					special = true
				}
			} else {
				special = true
			}
		}
		switch {
		case ch == ' ':
			if i == 0 {
				leadingSpace = true
			}
			if i == len(rs)-1 {
				trailingSpace = true
			}
			if prevBreak {
				breakSpace = true
			}
			prevSpace, prevBreak = true, false
		case isBreak(ch):
			if i == 0 {
				leadingBreak = true
			}
			if i == len(rs)-1 {
				trailingBreak = true
			}
			if prevSpace {
				spaceBreak = true
			}
			prevSpace, prevBreak = false, true
		default:
			prevSpace, prevBreak = false, false
		}
		preceded = isWSorNUL(ch)
		followed = i+2 >= len(rs) || isWSorNUL(rs[i+2])
	}
	a := analysis{multiline: lineBreaks, allowFlowPlain: true, allowBlockPlain: true, allowSingleQuoted: true, allowBlock: true}
	if leadingSpace || leadingBreak || trailingSpace || trailingBreak {
		a.allowFlowPlain, a.allowBlockPlain = false, false
	}
	if trailingSpace {
		a.allowBlock = false
	}
	if breakSpace {
		a.allowFlowPlain, a.allowBlockPlain, a.allowSingleQuoted = false, false, false
	}
	if spaceBreak || special {
		a.allowFlowPlain, a.allowBlockPlain, a.allowSingleQuoted, a.allowBlock = false, false, false, false
	}
	if lineBreaks {
		a.allowFlowPlain, a.allowBlockPlain = false, false
	}
	if flowInd {
		a.allowFlowPlain = false
	}
	if blockInd {
		a.allowBlockPlain = false
	}
	return a
}

func (e *emitter) chooseStyle(sc scalar, a analysis) byte {
	if sc.implicit[0] {
		if !(e.simpleKeyCtx && (a.empty || a.multiline)) &&
			((e.flowLevel > 0 && a.allowFlowPlain) || (e.flowLevel == 0 && a.allowBlockPlain)) {
			return 0
		}
	}
	if a.allowSingleQuoted && !(e.simpleKeyCtx && a.multiline) {
		return '\''
	}
	return '"'
}

func (e *emitter) processScalar(sc scalar) {
	a := analyze(sc.value, e.opts.AllowUnicode)
	style := e.chooseStyle(sc, a)
	// process_tag: implicit plain/quoted resolution never needs a tag for
	// the value space handled here.
	split := !e.simpleKeyCtx
	switch style {
	case '"':
		e.writeDoubleQuoted([]rune(sc.value), split)
	case '\'':
		e.writeSingleQuoted([]rune(sc.value), split)
	default:
		e.writePlain([]rune(sc.value), split)
	}
}

func (e *emitter) writeSingleQuoted(text []rune, split bool) {
	e.writeIndicator("'", true, false, false)
	spaces, breaks := false, false
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		if spaces {
			if ch == -1 || ch != ' ' {
				if start+1 == end && e.column > bestWidth && split && start != 0 && end != len(text) {
					e.writeIndent()
				} else {
					e.write(string(text[start:end]))
				}
				start = end
			}
		} else if breaks {
			if ch == -1 || !isBreak(ch) {
				if text[start] == '\n' {
					e.writeLineBreak("")
				}
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				e.writeIndent()
				start = end
			}
		} else {
			if ch == -1 || ch == ' ' || isBreak(ch) || ch == '\'' {
				if start < end {
					e.write(string(text[start:end]))
					start = end
				}
			}
		}
		if ch == '\'' {
			e.write("''")
			start = end + 1
		}
		if ch != -1 {
			spaces = ch == ' '
			breaks = isBreak(ch)
		}
		end++
	}
	e.writeIndicator("'", false, false, false)
}

var escapes = map[rune]string{0: "0", 7: "a", 8: "b", 9: "t", 10: "n", 11: "v", 12: "f", 13: "r", 27: "e",
	'"': "\"", '\\': "\\", 0x85: "N", 0xa0: "_", 0x2028: "L", 0x2029: "P"}

func (e *emitter) writeDoubleQuoted(text []rune, split bool) {
	e.writeIndicator("\"", true, false, false)
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		if ch == -1 || ch == '"' || ch == '\\' || ch == 0x85 || ch == 0x2028 || ch == 0x2029 || ch == 0xfeff ||
			!((ch >= 0x20 && ch <= 0x7e) || (e.opts.AllowUnicode && ((ch >= 0xa0 && ch <= 0xd7ff) || (ch >= 0xe000 && ch <= 0xfffd)))) {
			if start < end {
				e.write(string(text[start:end]))
				start = end
			}
			if ch != -1 {
				var data string
				if r, ok := escapes[ch]; ok {
					data = "\\" + r
				} else if ch <= 0xff {
					data = fmt.Sprintf("\\x%02X", ch)
				} else if ch <= 0xffff {
					data = fmt.Sprintf("\\u%04X", ch)
				} else {
					data = fmt.Sprintf("\\U%08X", ch)
				}
				e.write(data)
				start = end + 1
			}
		}
		if end > 0 && end < len(text)-1 && (ch == ' ' || start >= end) && e.column+(end-start) > bestWidth && split {
			data := string(text[start:end]) + "\\"
			if start < end {
				start = end
			}
			e.write(data)
			e.writeIndent()
			e.whitespace = false
			e.indention = false
			if text[start] == ' ' {
				e.write("\\")
			}
		}
		end++
	}
	e.writeIndicator("\"", false, false, false)
}

func (e *emitter) writePlain(text []rune, split bool) {
	if e.rootCtx {
		e.openEnded = true
	}
	if len(text) == 0 {
		return
	}
	if !e.whitespace {
		e.write(" ")
	}
	e.whitespace = false
	e.indention = false
	spaces, breaks := false, false
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		if spaces {
			if ch != ' ' {
				if start+1 == end && e.column > bestWidth && split {
					e.writeIndent()
					e.whitespace = false
					e.indention = false
				} else {
					e.write(string(text[start:end]))
				}
				start = end
			}
		} else if breaks {
			if !isBreak(ch) {
				if text[start] == '\n' {
					e.writeLineBreak("")
				}
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				e.writeIndent()
				e.whitespace = false
				e.indention = false
				start = end
			}
		} else {
			if ch == -1 || ch == ' ' || isBreak(ch) {
				e.write(string(text[start:end]))
				start = end
			}
		}
		if ch != -1 {
			spaces = ch == ' '
			breaks = isBreak(ch)
		}
		end++
	}
}
