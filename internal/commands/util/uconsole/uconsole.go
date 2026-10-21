// Package uconsole approximates rich.Console.print for the utility commands:
// it strips rich markup tags and soft-wraps on word boundaries at the console
// width (COLUMNS, else 80), like rich does when stdout is not a terminal.
package uconsole

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
)

// reTags is rich.markup.RE_TAGS: any "[x...]" whose first character is a
// lowercase letter, '#', '/' or '@' is a tag, optionally preceded by
// backslash escapes.
var reTags = regexp.MustCompile(`(\\*)\[([a-z#/@][^\[]*?)\]`)

// MarkupError is rich.errors.MarkupError (an unmatched closing tag).
type MarkupError struct{ Msg string }

func (e *MarkupError) Error() string { return e.Msg }

// Render returns the plain text rich.markup.render produces for s (tags
// removed, "\[" escapes resolved), or a *MarkupError where rich raises one.
func Render(s string) (string, error) {
	if !strings.Contains(s, "[") {
		return s, nil
	}
	var b strings.Builder
	var stack []string
	pos := 0
	for _, m := range reTags.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[0], m[1]
		escapes := s[m[2]:m[3]]
		tag := s[m[4]:m[5]]
		if start > pos {
			b.WriteString(strings.ReplaceAll(s[pos:start], `\[`, "["))
		}
		pos = end
		if escapes != "" {
			n := len(escapes)
			b.WriteString(strings.Repeat(`\`, n/2))
			if n%2 == 1 {
				b.WriteString(s[m[4]-1 : end]) // the literal "[tag]"
				continue
			}
		}
		name, _, _ := strings.Cut(tag, "=")
		if strings.HasPrefix(name, "/") {
			style := strings.TrimSpace(name[1:])
			if style == "" {
				if len(stack) == 0 {
					return "", &MarkupError{fmt.Sprintf("closing tag '[/]' at position %d has nothing to close", start)}
				}
				stack = stack[:len(stack)-1]
				continue
			}
			style = normalizeStyle(style)
			found := -1
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == style {
					found = i
					break
				}
			}
			if found < 0 {
				return "", &MarkupError{fmt.Sprintf("closing tag '[%s]' at position %d doesn't match any open tag", tag, start)}
			}
			stack = append(stack[:found], stack[found+1:]...)
			continue
		}
		stack = append(stack, normalizeStyle(name))
	}
	if pos < len(s) {
		b.WriteString(strings.ReplaceAll(s[pos:], `\[`, "["))
	}
	return b.String(), nil
}

// normalizeStyle is Style.normalize (lower-cased, whitespace-collapsed).
func normalizeStyle(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// Strip removes rich markup tags like Render, keeping the input unchanged
// where rich would raise a MarkupError.
func Strip(s string) string {
	out, err := Render(s)
	if err != nil {
		return s
	}
	return out
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

// Print writes markup-stripped, wrapped text plus a newline (console.print).
func Print(w io.Writer, s string) {
	fmt.Fprintln(w, Wrap(Strip(s), Width()))
}

// Printf is Print with formatting.
func Printf(w io.Writer, format string, a ...any) { Print(w, fmt.Sprintf(format, a...)) }
