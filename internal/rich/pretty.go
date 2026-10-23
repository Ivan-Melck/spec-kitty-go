package rich

import (
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Port of rich.pretty (traverse / Node / _Line) for the JSON-like values
// console.print(<dict>) renders: *jsonx.Object (dict), []any / []string
// (list), and scalars rendered with Python's repr.

type prettyNode struct {
	keyRepr, valueRepr      string
	openBrace, closeBrace   string
	empty                   string
	last                    bool
	children                []*prettyNode
	hasChildren             bool // children is not None
	keySeparator, separator string
}

func newNode() *prettyNode { return &prettyNode{keySeparator: ": ", separator: ", "} }

func (n *prettyNode) tokens(yield func(string) bool) bool {
	if n.keyRepr != "" {
		if !yield(n.keyRepr) || !yield(n.keySeparator) {
			return false
		}
	}
	if n.valueRepr != "" {
		return yield(n.valueRepr)
	}
	if n.hasChildren {
		if len(n.children) > 0 {
			if !yield(n.openBrace) {
				return false
			}
			for _, c := range n.children {
				if !c.tokens(yield) {
					return false
				}
				if !c.last && !yield(n.separator) {
					return false
				}
			}
			return yield(n.closeBrace)
		}
		return yield(n.empty)
	}
	return true
}

func (n *prettyNode) String() string {
	var b strings.Builder
	n.tokens(func(s string) bool { b.WriteString(s); return true })
	return b.String()
}

func (n *prettyNode) checkLength(start, max int) bool {
	total := start
	ok := true
	n.tokens(func(s string) bool {
		total += CellLen(s)
		if total > max {
			ok = false
			return false
		}
		return true
	})
	return ok
}

func traverse(v any, root bool) *prettyNode {
	n := newNode()
	container := func(open, close, empty string, items []*prettyNode) {
		if len(items) == 0 {
			n.empty, n.hasChildren, n.last = empty, true, root
			return
		}
		n.openBrace, n.closeBrace, n.children, n.hasChildren, n.last = open, close, items, true, root
		for i, c := range items {
			c.last = i == len(items)-1
		}
	}
	switch t := v.(type) {
	case *jsonx.Object:
		if t == nil {
			n.valueRepr, n.last = "None", root
			return n
		}
		var items []*prettyNode
		for _, kv := range t.Pairs() {
			c := traverse(kv.Value, false)
			c.keyRepr = pyjson.ReprString(kv.Key)
			items = append(items, c)
		}
		container("{", "}", "{}", items)
	case []any:
		var items []*prettyNode
		for _, x := range t {
			items = append(items, traverse(x, false))
		}
		container("[", "]", "[]", items)
	case []string:
		var items []*prettyNode
		for _, x := range t {
			items = append(items, traverse(x, false))
		}
		container("[", "]", "[]", items)
	default:
		n.valueRepr, n.last = pyjson.Repr(v), root
	}
	return n
}

type prettyLine struct {
	node                     *prettyNode
	text, suffix, whitespace string
	last                     bool
}

func (l *prettyLine) expandable() bool { return l.node != nil && len(l.node.children) > 0 }

func (l *prettyLine) String() string {
	node := ""
	if l.node != nil {
		node = l.node.String()
	}
	if l.last {
		return l.whitespace + l.text + node
	}
	return l.whitespace + l.text + node + strings.TrimRight(l.suffix, " \t\n\r\v\f")
}

func (l *prettyLine) expand(indent int) []*prettyLine {
	n := l.node
	var out []*prettyLine
	if n.keyRepr != "" {
		out = append(out, &prettyLine{text: n.keyRepr + n.keySeparator + n.openBrace, whitespace: l.whitespace})
	} else {
		out = append(out, &prettyLine{text: n.openBrace, whitespace: l.whitespace})
	}
	ws := l.whitespace + strings.Repeat(" ", indent)
	for i, c := range n.children {
		out = append(out, &prettyLine{node: c, whitespace: ws, suffix: n.separator, last: i == len(n.children)-1})
	}
	out = append(out, &prettyLine{text: n.closeBrace, whitespace: l.whitespace, suffix: l.suffix, last: l.last})
	return out
}

// PrettyRepr is rich.pretty.pretty_repr(v, max_width=maxWidth).
func PrettyRepr(v any, maxWidth int) string {
	lines := []*prettyLine{{node: traverse(v, true), last: true}}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l.expandable() && !l.checkLength(maxWidth) {
			exp := l.expand(4)
			lines = append(lines[:i], append(exp, lines[i+1:]...)...)
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.String()
	}
	return strings.Join(out, "\n")
}

func (l *prettyLine) checkLength(max int) bool {
	start := len(l.whitespace) + CellLen(l.text) + CellLen(l.suffix)
	return l.node.checkLength(start, max)
}

// WithIndentGuides is Text.with_indent_guides(4) ("│   " per full indent).
func WithIndentGuides(s string) string {
	const size = 4
	guide := "│" + strings.Repeat(" ", size-1)
	var out []string
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		rest := strings.TrimLeft(line, " ")
		if rest == "" {
			blank++
			continue
		}
		indent := len(line) - len(rest)
		full, rem := indent/size, indent%size
		newIndent := strings.Repeat(guide, full) + strings.Repeat(" ", rem)
		r := []rune(line)
		ni := []rune(newIndent)
		line = newIndent + string(r[len(ni):])
		for ; blank > 0; blank-- {
			out = append(out, newIndent)
		}
		out = append(out, line)
	}
	for ; blank > 0; blank-- {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// PrintPretty is console.print(v) for a dict/list (rich.pretty.Pretty,
// indent guides off): the pretty repr at the console width, soft-wrapped.
// WithIndentGuides is available for pprint-style callers.
func PrintPretty(w io.Writer, v any) {
	c := CliConsoleFor(w, 0)
	if c.Width <= 0 {
		return
	}
	t := NewStyledText(PrettyRepr(v, c.Width), "pretty")
	ReprHighlighter{}.Highlight(t)
	c.Print(w, t)
}
