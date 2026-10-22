package rich

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Port of rich.text.Text (rich 15): text with style spans, wrapped,
// justified and rendered to segments exactly like rich. Offsets are code
// points (Python str indices).

// Span is rich.text.Span. Style is a style name/definition (string) or a
// *Style.
type Span struct {
	Start, End int
	Style      any
}

// Text is rich.text.Text.
type Text struct {
	plain    []rune
	Style    any    // base style: string or *Style ("" / nil = none)
	Justify  string // "" = None
	Overflow string // "" = None
	NoWrap   *bool  // nil = None
	End      string
	TabSize  int // 0 = None
	Spans    []Span
}

// styleTruthy is Python truthiness of a str/Style value.
func styleTruthy(st any) bool {
	switch s := st.(type) {
	case nil:
		return false
	case string:
		return s != ""
	case *Style:
		return s.truthy()
	}
	return false
}

// stripControlCodes is rich.control.strip_control_codes.
func stripControlCodes(s string) string {
	if !strings.ContainsAny(s, "\a\b\v\f\r") {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '\a', '\b', '\v', '\f', '\r':
			return -1
		}
		return r
	}, s)
}

// NewText is Text(plain) (control codes stripped, end "\n").
func NewText(plain string) *Text {
	return &Text{plain: []rune(stripControlCodes(plain)), End: "\n"}
}

// NewStyledText is Text(plain, style=style).
func NewStyledText(plain string, style any) *Text {
	t := NewText(plain)
	t.Style = style
	return t
}

// TextStyled is Text.styled(plain, style): the style is a span, so it is
// not used to pad the text when it is justified.
func TextStyled(plain string, style any) *Text {
	t := NewText(plain)
	t.Stylize(style, 0, nil)
	return t
}

// FromMarkup is Text.from_markup (a markup error leaves the text plain).
func FromMarkup(markup string) *Text { return FromMarkupStyle(markup, "") }

// FromMarkupStyle is Text.from_markup(markup, style=style).
func FromMarkupStyle(markup string, style any) *Text {
	t, err := renderMarkup(markup, style)
	if err != nil {
		t = NewStyledText(markup, style)
	}
	return t
}

// AssemblePart is one part of Assemble: a Text, or a string with a style.
type AssemblePart struct {
	Text  *Text
	Plain string
	Style any
}

// Assemble is Text.assemble.
func Assemble(parts ...AssemblePart) *Text {
	t := &Text{End: "\n"}
	for _, p := range parts {
		if p.Text != nil {
			t.AppendText(p.Text)
		} else {
			t.Append(p.Plain, p.Style)
		}
	}
	return t
}

// Plain is the text as a string.
func (t *Text) Plain() string { return string(t.plain) }

// Len is len(text) in code points.
func (t *Text) Len() int { return len(t.plain) }

// CellLen is Text.cell_len.
func (t *Text) CellLen() int { return CellLen(string(t.plain)) }

// setPlain is the plain setter.
func (t *Text) setPlain(s string) {
	if s == string(t.plain) {
		return
	}
	n := []rune(stripControlCodes(s))
	old := len(t.plain)
	t.plain = n
	if old > len(n) {
		t.trimSpans()
	}
}

func (t *Text) blankCopy(plain string) *Text {
	return &Text{plain: []rune(stripControlCodes(plain)), Style: t.Style, Justify: t.Justify,
		Overflow: t.Overflow, NoWrap: t.NoWrap, End: t.End, TabSize: t.TabSize}
}

// Copy is Text.copy.
func (t *Text) Copy() *Text {
	c := t.blankCopy("")
	c.plain = append([]rune(nil), t.plain...)
	c.Spans = append([]Span(nil), t.Spans...)
	return c
}

// Stylize is Text.stylize (end nil = end of text; negative offsets index
// from the end).
func (t *Text) Stylize(style any, start int, end *int) {
	if !styleTruthy(style) {
		return
	}
	s, e, ok := t.spanRange(start, end)
	if ok {
		t.Spans = append(t.Spans, Span{s, e, style})
	}
}

// StylizeBefore is Text.stylize_before.
func (t *Text) StylizeBefore(style any, start int, end *int) {
	if !styleTruthy(style) {
		return
	}
	s, e, ok := t.spanRange(start, end)
	if ok {
		t.Spans = append([]Span{{s, e, style}}, t.Spans...)
	}
}

func (t *Text) spanRange(start int, end *int) (int, int, bool) {
	length := len(t.plain)
	if start < 0 {
		start = length + start
	}
	e := length
	if end != nil {
		e = *end
		if e < 0 {
			e = length + e
		}
	}
	if start >= length || e <= start {
		return 0, 0, false
	}
	return start, min(length, e), true
}

// Append is Text.append(str, style).
func (t *Text) Append(s string, style any) *Text {
	r := []rune(stripControlCodes(s))
	if len(r) == 0 {
		return t
	}
	off := len(t.plain)
	t.plain = append(t.plain, r...)
	if styleTruthy(style) {
		t.Spans = append(t.Spans, Span{off, off + len(r), style})
	}
	return t
}

// AppendText is Text.append_text.
func (t *Text) AppendText(o *Text) *Text {
	off := len(t.plain)
	if styleTruthy(o.Style) {
		t.Spans = append(t.Spans, Span{off, off + len(o.plain), o.Style})
	}
	t.plain = append(t.plain, o.plain...)
	for _, sp := range o.Spans {
		t.Spans = append(t.Spans, Span{sp.Start + off, sp.End + off, sp.Style})
	}
	return t
}

func (t *Text) trimSpans() {
	maxOff := len(t.plain)
	out := t.Spans[:0:0]
	for _, sp := range t.Spans {
		if sp.Start >= maxOff {
			continue
		}
		if sp.End >= maxOff {
			sp.End = min(maxOff, sp.End)
		}
		out = append(out, sp)
	}
	t.Spans = out
}

// rightCrop is Text.right_crop.
func (t *Text) rightCrop(amount int) {
	maxOff := len(t.plain) - amount
	out := t.Spans[:0:0]
	for _, sp := range t.Spans {
		if sp.Start >= maxOff {
			continue
		}
		if sp.End >= maxOff {
			sp.End = min(maxOff, sp.End)
		}
		out = append(out, sp)
	}
	t.Spans = out
	if amount <= 0 {
		t.plain = nil // Python plain[:-0]
		return
	}
	t.plain = append([]rune(nil), t.plain[:max(0, len(t.plain)-amount)]...)
}

var reTrailingSpace = regexp.MustCompile(`\s+$`)

// rstrip is Text.rstrip.
func (t *Text) rstrip() {
	t.setPlain(strings.TrimRightFunc(string(t.plain), unicode.IsSpace))
}

// rstripEnd is Text.rstrip_end.
func (t *Text) rstripEnd(size int) {
	n := len(t.plain)
	if n <= size {
		return
	}
	excess := n - size
	end := len(t.plain)
	k := end
	for k > 0 && unicode.IsSpace(t.plain[k-1]) {
		k--
	}
	if ws := end - k; ws > 0 {
		t.rightCrop(min(ws, excess))
	}
}

// setLength is Text.set_length.
func (t *Text) setLength(n int) {
	l := len(t.plain)
	if l < n {
		t.padRight(n - l)
	} else if l > n {
		t.rightCrop(l - n)
	}
}

// truncate is Text.truncate.
func (t *Text) truncate(maxWidth int, overflow string, pad bool) {
	ov := firstNonEmpty(overflow, t.Overflow, "fold")
	if ov == "ignore" {
		return
	}
	plain := string(t.plain)
	length := CellLen(plain)
	if length > maxWidth {
		if ov == "ellipsis" {
			t.setPlain(SetCellSize(plain, maxWidth-1) + "…")
		} else {
			t.setPlain(SetCellSize(plain, maxWidth))
		}
	}
	if pad && length < maxWidth {
		t.plain = append(t.plain, []rune(strings.Repeat(" ", maxWidth-length))...)
	}
}

// pad is Text.pad (both sides).
func (t *Text) pad(count int) {
	if count <= 0 {
		return
	}
	p := strings.Repeat(" ", count)
	t.setPlain(p + string(t.plain) + p)
	for i := range t.Spans {
		t.Spans[i].Start += count
		t.Spans[i].End += count
	}
}

func (t *Text) padLeft(count int) {
	if count <= 0 {
		return
	}
	t.setPlain(strings.Repeat(" ", count) + string(t.plain))
	for i := range t.Spans {
		t.Spans[i].Start += count
		t.Spans[i].End += count
	}
}

func (t *Text) padRight(count int) {
	if count <= 0 {
		return
	}
	t.setPlain(string(t.plain) + strings.Repeat(" ", count))
}

// extendStyle is Text.extend_style.
func (t *Text) extendStyle(spaces int) {
	if spaces <= 0 {
		return
	}
	if len(t.Spans) > 0 {
		end := len(t.plain)
		for i, sp := range t.Spans {
			if sp.End >= end {
				t.Spans[i].End += spaces
			}
		}
		t.plain = append(t.plain, []rune(strings.Repeat(" ", spaces))...)
		return
	}
	t.setPlain(string(t.plain) + strings.Repeat(" ", spaces))
}

// expandTabs is Text.expand_tabs.
func (t *Text) expandTabs(tabSize int) {
	if !strings.ContainsRune(string(t.plain), '\t') {
		return
	}
	if tabSize == 0 {
		tabSize = t.TabSize
	}
	if tabSize == 0 {
		tabSize = 8
	}
	var parts []*Text
	for _, line := range t.split("\n", true, false) {
		if !strings.ContainsRune(string(line.plain), '\t') {
			parts = append(parts, line)
			continue
		}
		pos := 0
		for _, part := range line.split("\t", true, false) {
			if n := len(part.plain); n > 0 && part.plain[n-1] == '\t' {
				part.plain[n-1] = ' '
				pos += part.CellLen()
				if rem := pos % tabSize; rem != 0 {
					part.extendStyle(tabSize - rem)
					pos += tabSize - rem
				}
			} else {
				pos += part.CellLen()
			}
			parts = append(parts, part)
		}
	}
	res := (&Text{End: "\n"}).join(parts)
	t.plain = res.plain
	t.Spans = res.Spans
}

// join is Text.join (t is the separator).
func (t *Text) join(lines []*Text) *Text {
	n := t.blankCopy("")
	var items []*Text
	if len(t.plain) > 0 {
		for i, l := range lines {
			items = append(items, l)
			if i < len(lines)-1 {
				items = append(items, t)
			}
		}
	} else {
		items = lines
	}
	off := 0
	for _, x := range items {
		n.plain = append(n.plain, x.plain...)
		if styleTruthy(x.Style) {
			n.Spans = append(n.Spans, Span{off, off + len(x.plain), x.Style})
		}
		for _, sp := range x.Spans {
			n.Spans = append(n.Spans, Span{off + sp.Start, off + sp.End, sp.Style})
		}
		off += len(x.plain)
	}
	return n
}

// split is Text.split.
func (t *Text) split(sep string, includeSep, allowBlank bool) []*Text {
	text := string(t.plain)
	if !strings.Contains(text, sep) {
		return []*Text{t.Copy()}
	}
	sepLen := len([]rune(sep))
	var offsets []int
	runeIdx := 0
	bytePos := 0
	for {
		i := strings.Index(text[bytePos:], sep)
		if i < 0 {
			break
		}
		runeIdx += len([]rune(text[bytePos : bytePos+i]))
		if includeSep {
			offsets = append(offsets, runeIdx+sepLen)
		} else {
			offsets = append(offsets, runeIdx, runeIdx+sepLen)
		}
		runeIdx += sepLen
		bytePos += i + len(sep)
	}
	var lines []*Text
	if includeSep {
		lines = t.divide(offsets)
	} else {
		for _, l := range t.divide(offsets) {
			if string(l.plain) != sep {
				lines = append(lines, l)
			}
		}
	}
	if !allowBlank && strings.HasSuffix(text, sep) && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// divide is Text.divide.
func (t *Text) divide(offsets []int) []*Text {
	if len(offsets) == 0 {
		return []*Text{t.Copy()}
	}
	bounds := append(append([]int{0}, offsets...), len(t.plain))
	lines := make([]*Text, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		a, b := bounds[i], bounds[i+1]
		seg := ""
		if a < b {
			seg = string(t.plain[a:b])
		}
		lines = append(lines, &Text{plain: []rune(stripControlCodes(seg)), Style: t.Style,
			Justify: t.Justify, Overflow: t.Overflow, End: "\n"})
	}
	for _, sp := range t.Spans {
		for li := 0; li+1 < len(bounds); li++ {
			ls, le := bounds[li], bounds[li+1]
			ns := max(0, sp.Start-ls)
			ne := min(sp.End-ls, le-ls)
			if ne > ns {
				lines[li].Spans = append(lines[li].Spans, Span{ns, ne, sp.Style})
			}
		}
	}
	return lines
}

// wrap is Text.wrap.
func (t *Text) wrap(c *Console, width int, justify, overflow string, tabSize int, noWrap *bool) []*Text {
	wj := firstNonEmpty(justify, t.Justify, "default")
	wo := firstNonEmpty(overflow, t.Overflow, "fold")
	nw := pickBool(noWrap, t.NoWrap, false) || overflow == "ignore"
	var lines []*Text
	for _, line := range t.split("\n", false, true) {
		if strings.ContainsRune(string(line.plain), '\t') {
			line.expandTabs(tabSize)
		}
		var nl []*Text
		if nw {
			if overflow == "ignore" {
				lines = append(lines, line)
				continue
			}
			nl = []*Text{line}
		} else {
			offs := divideLineRunes(line.plain, width, wo == "fold")
			nl = line.divide(offs)
			for _, l := range nl {
				l.rstripEnd(width)
			}
		}
		if wj != "" {
			justifyLines(c, nl, width, wj, wo)
		}
		for _, l := range nl {
			l.truncate(width, wo, false)
		}
		lines = append(lines, nl...)
	}
	return lines
}

func pickBool(vals ...any) bool {
	for _, v := range vals {
		switch b := v.(type) {
		case *bool:
			if b != nil {
				return *b
			}
		case bool:
			return b
		}
	}
	return false
}

// justifyLines is Lines.justify.
func justifyLines(c *Console, lines []*Text, width int, justify, overflow string) {
	switch justify {
	case "left":
		for _, l := range lines {
			l.truncate(width, overflow, true)
		}
	case "center":
		for _, l := range lines {
			l.rstrip()
			l.truncate(width, overflow, false)
			l.padLeft((width - l.CellLen()) / 2)
			l.padRight(width - l.CellLen())
		}
	case "right":
		for _, l := range lines {
			l.rstrip()
			l.truncate(width, overflow, false)
			l.padLeft(width - l.CellLen())
		}
	case "full":
		for i, l := range lines {
			if i == len(lines)-1 {
				break
			}
			words := l.split(" ", false, false)
			size := 0
			for _, w := range words {
				size += w.CellLen()
			}
			numSpaces := len(words) - 1
			spaces := make([]int, max(0, numSpaces))
			for k := range spaces {
				spaces[k] = 1
			}
			idx := 0
			if len(spaces) > 0 {
				for size+numSpaces < width {
					spaces[len(spaces)-idx-1]++
					numSpaces++
					idx = (idx + 1) % len(spaces)
				}
			}
			var tokens []*Text
			for k, w := range words {
				tokens = append(tokens, w)
				if k < len(spaces) {
					st := w.styleAt(c, -1)
					var next *Text
					if k+1 < len(words) {
						next = words[k+1]
					}
					var ns *Style
					if next != nil {
						ns = next.styleAt(c, 0)
					}
					var spaceStyle any = l.Style
					if ns != nil && st.equal(ns) {
						spaceStyle = st
					}
					tokens = append(tokens, NewStyledText(strings.Repeat(" ", spaces[k]), spaceStyle))
				}
			}
			lines[i] = (&Text{End: "\n"}).join(tokens)
		}
	}
}

// styleAt is Text.get_style_at_offset.
func (t *Text) styleAt(c *Console, offset int) *Style {
	if offset < 0 {
		offset = len(t.plain) + offset
	}
	st := c.GetStyle(t.Style)
	for _, sp := range t.Spans {
		if sp.End > offset && offset >= sp.Start {
			st = st.Add(c.GetStyle(sp.Style))
		}
	}
	return st
}

// RichRender is Text.__rich_console__.
func (t *Text) RichRender(c *Console, o Options) []Segment {
	tab := t.TabSize
	if tab == 0 {
		tab = c.tabSize()
	}
	justify := firstNonEmpty(t.Justify, o.Justify, "default")
	overflow := firstNonEmpty(t.Overflow, o.Overflow, "fold")
	lines := t.wrap(c, o.MaxWidth, justify, overflow, tab, boolp(pickBool(t.NoWrap, o.NoWrap, false)))
	all := NewText("\n").join(lines)
	return all.render(c, t.End)
}

// RichMeasure is Text.__rich_measure__.
func (t *Text) RichMeasure(c *Console, o Options) Measurement {
	text := string(t.plain)
	lines := reSplitLines.Split(text, -1)
	if strings.HasSuffix(text, "\n") || text == "" {
		lines = lines[:len(lines)-1]
	}
	maxW := 0
	for _, l := range lines {
		maxW = max(maxW, CellLen(l))
	}
	minW := maxW
	if words := strings.Fields(text); len(words) > 0 {
		minW = 0
		for _, w := range words {
			minW = max(minW, CellLen(w))
		}
	}
	return Measurement{minW, maxW}
}

// render is Text.render.
func (t *Text) render(c *Console, end string) []Segment {
	text := string(t.plain)
	if len(t.Spans) == 0 {
		out := []Segment{{Text: text}}
		if end != "" {
			out = append(out, Segment{Text: end})
		}
		return out
	}
	styles := make([]*Style, len(t.Spans)+1)
	styles[0] = c.GetStyle(t.Style)
	for i, sp := range t.Spans {
		styles[i+1] = c.GetStyle(sp.Style)
	}
	type ev struct {
		off   int
		leave bool
		id    int
	}
	evs := make([]ev, 0, 2*len(t.Spans)+2)
	evs = append(evs, ev{0, false, 0})
	for i, sp := range t.Spans {
		evs = append(evs, ev{sp.Start, false, i + 1})
	}
	for i, sp := range t.Spans {
		evs = append(evs, ev{sp.End, true, i + 1})
	}
	evs = append(evs, ev{len(t.plain), true, 0})
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].off != evs[j].off {
			return evs[i].off < evs[j].off
		}
		return !evs[i].leave && evs[j].leave
	})
	var stack []int
	var out []Segment
	for k := 0; k+1 < len(evs); k++ {
		e := evs[k]
		if e.leave {
			for j, id := range stack {
				if id == e.id {
					stack = append(stack[:j], stack[j+1:]...)
					break
				}
			}
		} else {
			stack = append(stack, e.id)
		}
		next := evs[k+1].off
		if next > e.off {
			ids := append([]int(nil), stack...)
			sort.Ints(ids)
			var cur *Style
			for n, id := range ids {
				if n == 0 {
					cur = styles[id]
				} else {
					cur = cur.Add(styles[id])
				}
			}
			if cur == nil {
				cur = NullStyle()
			}
			a, b := max(0, e.off), min(next, len(t.plain))
			s := ""
			if a < b {
				s = string(t.plain[a:b])
			}
			out = append(out, Segment{s, cur})
		}
	}
	if end != "" {
		out = append(out, Segment{Text: end})
	}
	return out
}

// highlightWords is Text.highlight_words (case sensitive).
func (t *Text) highlightWords(words []string, style any) {
	var parts []string
	for _, w := range words {
		parts = append(parts, regexp.QuoteMeta(w))
	}
	re := regexp.MustCompile(strings.Join(parts, "|"))
	t.highlightRegexWhole(re, style)
}

// highlightRegexWhole adds a span for every match of re.
func (t *Text) highlightRegexWhole(re *regexp.Regexp, style any) {
	s := string(t.plain)
	ra := runeOffsets(s)
	for _, m := range re.FindAllStringIndex(s, -1) {
		a, b := ra[m[0]], ra[m[1]]
		if b > a {
			t.Spans = append(t.Spans, Span{a, b, style})
		}
	}
}

// highlightRegex is Text.highlight_regex for an RE2-compatible pattern:
// a span per named group that matched non-empty text.
func (t *Text) highlightRegex(re *regexp.Regexp, prefix string) {
	s := string(t.plain)
	ra := runeOffsets(s)
	names := re.SubexpNames()
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		for gi, name := range names {
			if name == "" || m[2*gi] < 0 {
				continue
			}
			a, b := ra[m[2*gi]], ra[m[2*gi+1]]
			if b > a {
				t.Spans = append(t.Spans, Span{a, b, prefix + name})
			}
		}
	}
}

// runeOffsets maps byte offsets of s to code point offsets.
func runeOffsets(s string) []int {
	out := make([]int, len(s)+1)
	for i := range out {
		out[i] = -1
	}
	ri := 0
	for bi := range s {
		out[bi] = ri
		ri++
	}
	out[len(s)] = ri
	// continuation bytes map to the next rune start (never match bounds)
	for i := len(s) - 1; i >= 0; i-- {
		if out[i] < 0 {
			out[i] = out[i+1]
		}
	}
	return out
}

// divideLineRunes is rich._wrap.divide_line over code points (offsets are
// code point indices).
func divideLineRunes(text []rune, width int, fold bool) []int {
	s := string(text)
	byteOffs := divideLine(s, width, fold)
	if len(byteOffs) == 0 {
		return nil
	}
	ra := runeOffsets(s)
	out := make([]int, len(byteOffs))
	for i, b := range byteOffs {
		out[i] = ra[b]
	}
	return out
}

func boolp(b bool) *bool { return &b }

// equal reports whether two styles render identically (Style.__eq__).
func (s *Style) equal(o *Style) bool {
	if s == nil || o == nil {
		return s == o
	}
	if s.null || o.null {
		return s.null == o.null
	}
	return s.attrs&s.set == o.attrs&o.set && s.set == o.set && s.link == o.link &&
		colorEq(s.color, o.color) && colorEq(s.bgcolor, o.bgcolor)
}

func colorEq(a, b *Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
