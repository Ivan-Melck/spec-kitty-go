package rich

import (
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// renderMarkup is rich.markup.render(markup, style) (emoji codes are not
// replaced).
func renderMarkup(s string, style any) (*Text, error) {
	t := &Text{Style: style, End: "\n"}
	if !strings.Contains(s, "[") {
		t.plain = []rune(stripControlCodes(emojiReplace(s)))
		return t, nil
	}
	type open struct {
		pos       int
		name, tag string
	}
	var stack []open
	var b []rune
	pos := 0
	emit := func(x string) { b = append(b, []rune(stripControlCodes(emojiReplace(x)))...) }
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
				t.Spans = append(t.Spans, Span{o.pos, len(b), o.tag})
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
			t.Spans = append(t.Spans, Span{stack[i].pos, len(b), stack[i].tag})
		}
	}
	t.plain = b
	// text.spans = sorted(spans[::-1], key=attrgetter("start"))
	for i, j := 0, len(t.Spans)-1; i < j; i, j = i+1, j-1 {
		t.Spans[i], t.Spans[j] = t.Spans[j], t.Spans[i]
	}
	sort.SliceStable(t.Spans, func(i, j int) bool { return t.Spans[i].Start < t.Spans[j].Start })
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

// boundaryAfter is Python's \b at end: the characters on either side differ
// in wordness (text edges count as non-word).
func boundaryAfter(text []rune, end int) bool {
	before := end > 0 && isWord(text[end-1])
	after := end < len(text) && isWord(text[end])
	return before != after
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

// ReprHighlighter is rich.highlighter.ReprHighlighter.
type ReprHighlighter struct{}

// Highlight is ReprHighlighter.highlight: each pattern's finditer, spans
// for every named group that matched non-empty text.
func (ReprHighlighter) Highlight(t *Text) {
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
				t.Spans = append(t.Spans, Span{a, b, "repr." + name})
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
					t.Spans = append(t.Spans, Span{i, e, "repr.str"})
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

func fdOf(w io.Writer) int {
	switch w {
	case os.Stdout:
		return 1
	case os.Stderr:
		return 2
	}
	return 0
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
