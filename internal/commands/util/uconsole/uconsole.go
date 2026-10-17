// Package uconsole approximates rich.Console.print for the utility commands:
// it strips rich markup tags and soft-wraps on word boundaries at the console
// width (COLUMNS, else 80), like rich does when stdout is not a terminal.
package uconsole

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// markup matches rich style tags such as [red], [/red], [bold cyan], [/].
var markup = regexp.MustCompile(`\[(/?[a-zA-Z#][a-zA-Z0-9_ #.,=-]*|/)\]`)

// Strip removes rich markup tags (escaped "\[" becomes "[").
func Strip(s string) string {
	s = markup.ReplaceAllStringFunc(s, func(m string) string {
		if isStyle(m[1 : len(m)-1]) {
			return ""
		}
		return m
	})
	return strings.ReplaceAll(s, `\[`, "[")
}

var styleWords = map[string]bool{
	"bold": true, "dim": true, "italic": true, "underline": true, "red": true, "green": true,
	"yellow": true, "blue": true, "cyan": true, "magenta": true, "white": true, "black": true,
	"bright_red": true, "bright_green": true, "bright_yellow": true, "bright_blue": true,
	"bright_cyan": true, "bright_magenta": true, "bright_white": true, "bright_black": true,
	"grey50": true, "on": true, "not": true, "reverse": true, "strike": true, "link": true,
	"orange1": true, "grey62": true, "gray": true, "grey": true,
}

func isStyle(tag string) bool {
	tag = strings.TrimPrefix(tag, "/")
	if tag == "" {
		return true
	}
	for _, w := range strings.Fields(tag) {
		if strings.HasPrefix(w, "#") || strings.HasPrefix(w, "link=") {
			continue
		}
		if !styleWords[w] {
			return false
		}
	}
	return true
}

// Width is the console width.
func Width() int { return cli.Width() }

// Wrap soft-wraps each line of s at width on spaces (long words are split).
func Wrap(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}
	// Preserve leading indentation on the first line only (rich behaviour).
	words := strings.Split(line, " ")
	var lines []string
	cur := ""
	curLen := 0
	for i, w := range words {
		wl := utf8.RuneCountInString(w)
		switch {
		case i == 0:
			cur, curLen = w, wl
		case curLen+1+wl <= width:
			cur += " " + w
			curLen += 1 + wl
		default:
			lines = append(lines, strings.TrimRight(cur, " "))
			cur, curLen = w, wl
		}
		for curLen > width {
			r := []rune(cur)
			lines = append(lines, string(r[:width]))
			cur = string(r[width:])
			curLen = len(r) - width
		}
	}
	return append(lines, cur)
}

// Print writes markup-stripped, wrapped text plus a newline (console.print).
func Print(w io.Writer, s string) {
	fmt.Fprintln(w, Wrap(Strip(s), Width()))
}

// Printf is Print with formatting.
func Printf(w io.Writer, format string, a ...any) { Print(w, fmt.Sprintf(format, a...)) }
