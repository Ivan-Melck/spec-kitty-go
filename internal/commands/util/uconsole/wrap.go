package uconsole

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
