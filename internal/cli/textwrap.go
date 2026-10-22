package cli

import (
	"strings"
	"unicode"
)

// textWrap is click's TextWrapper.wrap (textwrap.TextWrapper with
// replace_whitespace=False): text is split into chunks by textwrap's
// wordsep_re (whitespace runs, hyphenated-word pieces, em-dashes) and
// packed greedily; long words are broken, at a hyphen when possible.
// Lengths are counted in characters, as Python's len does. An empty result
// is returned as the bare initial indent.
func textWrap(text string, width int, initial, subsequent string) []string {
	chunks := wrapChunks([]rune(expandTabs(text)))
	var lines []string
	for len(chunks) > 0 {
		indent := initial
		if len(lines) > 0 {
			indent = subsequent
		}
		avail := width - runeLen(indent)
		if len(lines) > 0 && isBlankChunk(chunks[0]) {
			chunks = chunks[1:]
		}
		var cur []string
		curLen := 0
		for len(chunks) > 0 && curLen+runeLen(chunks[0]) <= avail {
			cur = append(cur, chunks[0])
			curLen += runeLen(chunks[0])
			chunks = chunks[1:]
		}
		if len(chunks) > 0 && runeLen(chunks[0]) > avail {
			// _handle_long_word
			spaceLeft := 1
			if avail >= 1 {
				spaceLeft = avail - curLen
			}
			chunk := []rune(chunks[0])
			end := spaceLeft
			if len(chunk) > spaceLeft {
				if h := lastHyphen(chunk[:spaceLeft]); h > 0 && strings.Trim(string(chunk[:h]), "-") != "" {
					end = h + 1
				}
			}
			cur = append(cur, string(chunk[:end]))
			chunks[0] = string(chunk[end:])
		}
		if n := len(cur); n > 0 && isBlankChunk(cur[n-1]) {
			cur = cur[:n-1]
		}
		if len(cur) > 0 {
			lines = append(lines, indent+strings.Join(cur, ""))
		}
	}
	if len(lines) == 0 {
		lines = []string{initial}
	}
	return lines
}

// wrapChunks is TextWrapper._split with break_on_hyphens: re.split by
// textwrap's wordsep_re, empty pieces dropped.
func wrapChunks(t []rune) []string {
	var out []string
	for p := 0; p < len(t); {
		if isWrapSpace(t[p]) {
			e := p
			for e < len(t) && isWrapSpace(t[e]) {
				e++
			}
			out = append(out, string(t[p:e]))
			p = e
			continue
		}
		// em-dash between words: (?<=word_punct) -{2,} (?=\w)
		if t[p] == '-' && p > 0 && isWordPunct(t[p-1]) {
			e := p
			for e < len(t) && t[e] == '-' {
				e++
			}
			if e-p >= 2 && e < len(t) && isWordRune(t[e]) {
				out = append(out, string(t[p:e]))
				p = e
				continue
			}
		}
		// word, possibly hyphenated: the shortest non-space prefix that ends
		// at a breakable hyphen, the end of the word, or before an em-dash.
		e := p + 1
		for ; e <= len(t); e++ {
			if e == len(t) || isWrapSpace(t[e]) || emDashAhead(t, e) {
				break
			}
			if t[e] == '-' && hyphenBreak(t, e) {
				e++
				break
			}
		}
		out = append(out, string(t[p:e]))
		p = e
	}
	return out
}

// hyphenBreak reports whether the hyphen at i ends a chunk:
// (?<=lt{2}-) or (?<=lt-lt-), then (?=lt -? lt).
func hyphenBreak(t []rune, i int) bool {
	behind := (i >= 2 && isLetterRune(t[i-2]) && isLetterRune(t[i-1])) ||
		(i >= 3 && isLetterRune(t[i-3]) && t[i-2] == '-' && isLetterRune(t[i-1]))
	if !behind || i+1 >= len(t) || !isLetterRune(t[i+1]) {
		return false
	}
	j := i + 2
	if j < len(t) && t[j] == '-' {
		j++
	}
	return j < len(t) && isLetterRune(t[j])
}

// emDashAhead is (?<=word_punct)(?=-{2,}\w) at position i.
func emDashAhead(t []rune, i int) bool {
	if i == 0 || !isWordPunct(t[i-1]) {
		return false
	}
	e := i
	for e < len(t) && t[e] == '-' {
		e++
	}
	return e-i >= 2 && e < len(t) && isWordRune(t[e])
}

func lastHyphen(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == '-' {
			return i
		}
	}
	return -1
}

func isWrapSpace(r rune) bool { return strings.ContainsRune("\t\n\x0b\x0c\r ", r) }

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// isLetterRune is [^\d\W]: word characters other than digits.
func isLetterRune(r rune) bool { return isWordRune(r) && !unicode.IsDigit(r) }

func isWordPunct(r rune) bool { return isWordRune(r) || strings.ContainsRune("!\"'&.,?", r) }

func isBlankChunk(s string) bool { return strings.TrimSpace(s) == "" }

func runeLen(s string) int { return len([]rune(s)) }
