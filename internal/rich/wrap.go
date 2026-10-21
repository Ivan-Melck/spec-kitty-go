package rich

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var reWord = regexp.MustCompile(`\s*\S+\s*`)

// wrapLine is rich's divide_line (fold=True) followed by rstrip_end(width):
// a line keeps its trailing whitespace unless that exceeds the width.
func wrapLine(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}
	var breaks []int // byte offsets
	offset := 0
	for _, loc := range reWord.FindAllStringIndex(line, -1) {
		start := loc[0]
		word := line[loc[0]:loc[1]]
		wl := utf8.RuneCountInString(strings.TrimRight(word, " \t"))
		if width-offset >= wl {
			offset += utf8.RuneCountInString(word)
			continue
		}
		if wl > width {
			r := []rune(word)
			for i := 0; i < len(r); i += width {
				end := i + width
				if end > len(r) {
					end = len(r)
				}
				chunk := string(r[i:end])
				if start != 0 {
					breaks = append(breaks, start)
				}
				if end == len(r) {
					offset = utf8.RuneCountInString(chunk)
				} else {
					start += len(chunk)
				}
			}
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = utf8.RuneCountInString(word)
		}
	}
	var lines []string
	prev := 0
	for _, b := range append(breaks, len(line)) {
		l := line[prev:b]
		for utf8.RuneCountInString(l) > width && strings.HasSuffix(l, " ") {
			l = l[:len(l)-1]
		}
		lines = append(lines, l)
		prev = b
	}
	return lines
}

// WrapEllipsis wraps a line like a rich table cell (Column overflow
// "ellipsis"): divide_line without folding, so a word wider than width
// keeps its own line and is truncated to width-1 cells plus "…".
func WrapEllipsis(line string, width int) []string {
	if width <= 0 || utf8.RuneCountInString(line) <= width {
		return []string{line}
	}
	var breaks []int
	offset := 0
	for _, loc := range reWord.FindAllStringIndex(line, -1) {
		start := loc[0]
		word := line[loc[0]:loc[1]]
		wl := utf8.RuneCountInString(strings.TrimRight(word, " \t\n\r\v\f"))
		if width-offset >= wl {
			offset += utf8.RuneCountInString(word)
			continue
		}
		if wl > width {
			if start != 0 {
				breaks = append(breaks, start)
			}
			offset = utf8.RuneCountInString(word)
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = utf8.RuneCountInString(word)
		}
	}
	var out []string
	prev := 0
	for _, b := range append(breaks, len(line)) {
		l := line[prev:b]
		prev = b
		// rstrip_end: drop trailing whitespace only as far as the excess.
		if n := utf8.RuneCountInString(l); n > width {
			ws := n - utf8.RuneCountInString(strings.TrimRight(l, " \t\n\r\v\f"))
			cut := n - width
			if ws < cut {
				cut = ws
			}
			r := []rune(l)
			l = string(r[:len(r)-cut])
		}
		if r := []rune(l); len(r) > width {
			l = string(r[:width-1]) + "…"
		}
		out = append(out, l)
	}
	return out
}
