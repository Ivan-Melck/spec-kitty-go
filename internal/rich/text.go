package rich

import (
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// span is rich.text.Span over rune offsets.
type span struct {
	start, end int
	style      string
}

// richText is the subset of rich.text.Text console.print needs.
type richText struct {
	plain []rune
	spans []span
}

// renderMarkup is rich.markup.render with the spans kept.
func renderMarkup(s string) (*richText, error) {
	t := &richText{}
	if !strings.Contains(s, "[") {
		t.plain = []rune(s)
		return t, nil
	}
	type open struct {
		pos       int
		name, tag string
	}
	var stack []open
	var b []rune
	pos := 0
	emit := func(x string) { b = append(b, []rune(x)...) }
	for _, m := range reTags.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[0], m[1]
		escapes := s[m[2]:m[3]]
		tag := s[m[4]:m[5]]
		if start > pos {
			emit(strings.ReplaceAll(s[pos:start], `\[`, "["))
		}
		pos = end
		if escapes != "" {
			n := len(escapes)
			emit(strings.Repeat(`\`, n/2))
			if n%2 == 1 {
				emit(s[m[4]-1 : end])
				continue
			}
		}
		name, params, hasParams := strings.Cut(tag, "=")
		if strings.HasPrefix(name, "/") {
			style := strings.TrimSpace(name[1:])
			idx := -1
			if style == "" {
				idx = len(stack) - 1
			} else {
				style = normalizeStyle(style)
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i].name == style {
						idx = i
						break
					}
				}
			}
			if idx < 0 {
				if style == "" {
					return nil, &MarkupError{"closing tag '[/]' has nothing to close"}
				}
				return nil, &MarkupError{"closing tag '[" + tag + "]' doesn't match any open tag"}
			}
			o := stack[idx]
			stack = append(stack[:idx], stack[idx+1:]...)
			if !strings.HasPrefix(o.tag, "@") {
				t.spans = append(t.spans, span{o.pos, len(b), o.tag})
			}
			continue
		}
		full := normalizeStyle(name)
		if hasParams {
			full += " " + params
		}
		stack = append(stack, open{len(b), normalizeStyle(name), full})
	}
	if pos < len(s) {
		emit(strings.ReplaceAll(s[pos:], `\[`, "["))
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].tag != "" && !strings.HasPrefix(stack[i].tag, "@") {
			t.spans = append(t.spans, span{stack[i].pos, len(b), stack[i].tag})
		}
	}
	t.plain = b
	// text.spans = sorted(spans[::-1], key=attrgetter("start"))
	for i, j := 0, len(t.spans)-1; i < j; i, j = i+1, j-1 {
		t.spans[i], t.spans[j] = t.spans[j], t.spans[i]
	}
	sort.SliceStable(t.spans, func(i, j int) bool { return t.spans[i].start < t.spans[j].start })
	return t, nil
}

// ---------------------------------------------------------------- highlighter

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.N)
}

// hlAlt is one alternative of ReprHighlighter's combined regex: an
// RE2-compatible pattern anchored at the match start plus the lookbehind /
// word-boundary checks Python evaluates against the preceding character.
type hlAlt struct {
	re   *regexp.Regexp
	pre  func(prev rune, has bool) bool
	post func(text []rune, end int) bool
	// str matches rich's str alternative by hand (lookbehinds inside).
	str bool
}

func notWordBefore(prev rune, has bool) bool { return !has || !isWord(prev) }

func boundaryAfter(text []rune, end int) bool {
	return end >= len(text) || !isWord(text[end])
}

var reprSimple = []string{
	`^(?P<tag_start><)(?P<tag_name>[-` + `\p{L}\p{N}_` + `.:|]*)(?P<tag_contents>[\s\S]*)(?P<tag_end>>)`,
	`^(?P<attrib_name>[\p{L}\p{N}_]{1,50})=(?P<attrib_value>"?[\p{L}\p{N}_]+"?)?`,
	`^(?P<brace>[][{}()])`,
}

var reprAlts []hlAlt

var reprSimpleRe = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range reprSimple {
		out = append(out, regexp.MustCompile(p))
	}
	return out
}()

func init() {
	mk := func(p string) *regexp.Regexp { return regexp.MustCompile("^(?:" + p + ")") }
	reprAlts = []hlAlt{
		{re: mk(`(?P<ipv4>[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})`)},
		{re: mk(`(?P<ipv6>([A-Fa-f0-9]{1,4}::?){1,7}[A-Fa-f0-9]{1,4})`)},
		{re: mk(`(?P<eui64>(?:[0-9A-Fa-f]{1,2}-){7}[0-9A-Fa-f]{1,2}|(?:[0-9A-Fa-f]{1,2}:){7}[0-9A-Fa-f]{1,2}|(?:[0-9A-Fa-f]{4}\.){3}[0-9A-Fa-f]{4})`)},
		{re: mk(`(?P<eui48>(?:[0-9A-Fa-f]{1,2}-){5}[0-9A-Fa-f]{1,2}|(?:[0-9A-Fa-f]{1,2}:){5}[0-9A-Fa-f]{1,2}|(?:[0-9A-Fa-f]{4}\.){2}[0-9A-Fa-f]{4})`)},
		{re: mk(`(?P<uuid>[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})`)},
		{re: mk(`(?P<call>[\p{L}\p{N}_.]*?)\(`)},
		{re: mk(`(?P<bool_true>True)`), pre: notWordBefore, post: boundaryAfter},
		{re: mk(`(?P<bool_false>False)`), pre: notWordBefore, post: boundaryAfter},
		{re: mk(`(?P<none>None)`), pre: notWordBefore, post: boundaryAfter},
		{re: mk(`(?P<ellipsis>\.\.\.)`)},
		{re: mk(`(?P<number_complex>(?:\-?[0-9]+\.?[0-9]*(?:e[-+]?[0-9]+?)?)(?:[-+](?:[0-9]+\.?[0-9]*(?:e[-+]?[0-9]+)?))?j)`), pre: notWordBefore},
		{re: mk(`(?P<number>\-?[0-9]+\.?[0-9]*(e[-+]?[0-9]+?)?)`), pre: notWordBefore, post: boundaryAfter},
		{re: mk(`(?P<number>0x[0-9a-fA-F]*)`)},
		{re: mk(`(?P<path>(/[-\p{L}\p{N}_.+]+)*\/)(?P<filename>[-\p{L}\p{N}_.+]*)?`), pre: func(prev rune, has bool) bool {
			// \B before '/': the previous character is not a word character
			return !has || !isWord(prev)
		}},
		{str: true},
		{re: mk(`(?P<url>(file|https|http|ws|wss)://[-0-9a-zA-Z$_+!` + "`" + `(),.?/;:&=%#~@]*)`)},
	}
}

// matchStr is `(?<![\\\w])(?P<str>b?”'.*?(?<!\\)”'|b?'.*?(?<!\\)'|
// b?""".*?(?<!\\)"""|b?".*?(?<!\\)")` at i (no newlines in '.').
func matchStr(text []rune, i int) (int, bool) {
	if i > 0 && (text[i-1] == '\\' || isWord(text[i-1])) {
		return 0, false
	}
	j := i
	if j < len(text) && text[j] == 'b' {
		j++
	}
	try := func(q string) (int, bool) {
		qr := []rune(q)
		if j+len(qr) > len(text) || string(text[j:j+len(qr)]) != q {
			return 0, false
		}
		for k := j + len(qr); k+len(qr) <= len(text); k++ {
			if string(text[k:k+len(qr)]) == q && text[k-1] != '\\' {
				return k + len(qr), true
			}
			if text[k] == '\n' {
				return 0, false
			}
		}
		return 0, false
	}
	for _, q := range []string{"'''", "'", `"""`, `"`} {
		if e, ok := try(q); ok {
			return e, true
		}
	}
	return 0, false
}

// highlight is ReprHighlighter.highlight: each pattern's finditer, spans for
// every named group that matched non-empty text.
func (t *richText) highlight() {
	text := t.plain
	s := string(text)
	// byte offset -> rune offset
	runeAt := make([]int, len(s)+1)
	{
		ri := 0
		for bi := range s {
			runeAt[bi] = ri
			ri++
		}
		runeAt[len(s)] = len(text)
	}
	byteAt := make([]int, len(text)+1)
	{
		ri := 0
		for bi := range s {
			byteAt[ri] = bi
			ri++
		}
		byteAt[len(text)] = len(s)
	}
	addGroups := func(re *regexp.Regexp, m []int, base int) {
		for gi, name := range re.SubexpNames() {
			if name == "" || m[2*gi] < 0 {
				continue
			}
			a, b := runeAt[base+m[2*gi]], runeAt[base+m[2*gi+1]]
			if b > a {
				t.spans = append(t.spans, span{a, b, "repr." + name})
			}
		}
	}
	for _, re := range reprSimpleRe {
		for i := 0; i <= len(text); {
			m := re.FindStringSubmatchIndex(s[byteAt[i]:])
			if m == nil {
				i++
				continue
			}
			addGroups(re, m, byteAt[i])
			end := runeAt[byteAt[i]+m[1]]
			if end == i {
				end++
			}
			i = end
		}
	}
	for i := 0; i <= len(text); {
		end := -1
		for _, a := range reprAlts {
			if a.str {
				if e, ok := matchStr(text, i); ok {
					t.spans = append(t.spans, span{i, e, "repr.str"})
					end = e
					break
				}
				continue
			}
			if a.pre != nil {
				var prev rune
				if i > 0 {
					prev = text[i-1]
				}
				if !a.pre(prev, i > 0) {
					continue
				}
			}
			m := a.re.FindStringSubmatchIndex(s[byteAt[i]:])
			if m == nil {
				continue
			}
			e := runeAt[byteAt[i]+m[1]]
			if a.post != nil && !a.post(text, e) {
				// Python backtracks to a shorter match; the patterns that
				// carry a trailing \b only shrink by digits, retry shorter
				ok := false
				for k := m[1] - 1; k > m[0]; k-- {
					mm := a.re.FindStringSubmatchIndex(s[byteAt[i] : byteAt[i]+k])
					if mm != nil && mm[1] == k && a.post(text, runeAt[byteAt[i]+k]) {
						m, e, ok = mm, runeAt[byteAt[i]+k], true
						break
					}
				}
				if !ok {
					continue
				}
			}
			addGroups(a.re, m, byteAt[i])
			end = e
			break
		}
		if end < 0 || end == i {
			i++
			continue
		}
		i = end
	}
}

// ---------------------------------------------------------------- output

type segment struct {
	text  string
	style *Style
}

// segments is Text.render for the rune range [from, to).
func (t *richText) segments(from, to int, keepEmpty bool) []segment {
	type ev struct {
		off   int
		leave bool
		id    int
	}
	var evs []ev
	evs = append(evs, ev{from, false, 0})
	for i, sp := range t.spans {
		a, b := max(sp.start, from), min(sp.end, to)
		if a > b || (a == b && !keepEmpty) {
			continue
		}
		evs = append(evs, ev{a, false, i + 1}, ev{b, true, i + 1})
	}
	evs = append(evs, ev{to, true, 0})
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].off != evs[j].off {
			return evs[i].off < evs[j].off
		}
		return !evs[i].leave && evs[j].leave
	})
	styles := map[int]*Style{0: NullStyle()}
	for i, sp := range t.spans {
		styles[i+1] = themeStyle(sp.style)
	}
	var stack []int
	var out []segment
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
		if next := evs[k+1].off; next > e.off {
			ids := append([]int(nil), stack...)
			sort.Ints(ids)
			cur := NullStyle()
			for _, id := range ids {
				cur = cur.Add(styles[id])
			}
			out = append(out, segment{string(t.plain[e.off:next]), cur})
		}
	}
	return out
}

func fdOf(w io.Writer) int {
	switch w {
	case os.Stdout:
		return 1
	case os.Stderr:
		return 2
	}
	return 0
}

// printColor is console.print(s) on a colour console: markup and repr
// highlighting rendered as ANSI SGR sequences, wrapped like Print.
func printColor(w io.Writer, s string, width int, sys ColorSystem) bool {
	t, err := renderMarkup(s)
	if err != nil {
		return false
	}
	writeHighlighted(w, t, width, sys)
	return true
}

// writeHighlighted writes t highlighted (render_str), wrapped at width.
func writeHighlighted(w io.Writer, t *richText, width int, sys ColorSystem) {
	// render_str: highlighter(str(text)) then copy_styles(markup text): the
	// markup spans follow (and override) the highlight spans
	markup := t.spans
	t.spans = nil
	t.highlight()
	t.spans = append(t.spans, markup...)
	plain := string(t.plain)
	wrapped := strings.Split(Wrap(plain, width), "\n")
	var b strings.Builder
	cursor := 0
	noColor := noColorEnv()
	for li, line := range wrapped {
		lr := []rune(line)
		// locate the line in the plain text (wrapping drops break spaces)
		for cursor < len(t.plain) && len(lr) > 0 && !hasPrefixAt(t.plain, cursor, lr) {
			cursor++
		}
		// Text.divide (wrapping, newlines) drops empty spans; an undivided
		// single line keeps them (they still split segments)
		keep := len(wrapped) == 1
		for _, seg := range t.segments(cursor, cursor+len(lr), keep) {
			st := seg.style
			if noColor {
				st = st.withoutColor()
			}
			b.WriteString(st.Render(seg.text, sys))
		}
		cursor += len(lr)
		if cursor < len(t.plain) && t.plain[cursor] == '\n' {
			cursor++
		}
		if li < len(wrapped)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	io.WriteString(w, b.String())
}

func hasPrefixAt(text []rune, i int, p []rune) bool {
	if i+len(p) > len(text) {
		return false
	}
	for k, r := range p {
		if text[i+k] != r {
			return false
		}
	}
	return true
}
