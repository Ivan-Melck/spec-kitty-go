package yamlx

import (
	"strings"
	"unicode/utf8"
)

// column is the current output column (runes since the last newline).
func (e *emitter) column() int {
	s := e.b.String()
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return utf8.RuneCountInString(s)
}

// wrapScalar renders " <scalar>" the way ruamel.yaml's emitter folds plain
// (write_plain) and single-quoted (write_single_quoted) scalars at
// best_width, continuing at indent. col is the column after the key colon
// or sequence dash.
func wrapScalar(text string, quoted bool, col, indent, width int) string {
	var b strings.Builder
	rs := []rune(text)
	write := func(s string) {
		b.WriteString(s)
		col += utf8.RuneCountInString(s)
	}
	newline := func() {
		b.WriteString("\n" + strings.Repeat(" ", indent))
		col = indent
	}
	if quoted {
		write(" '")
	} else {
		write(" ")
	}
	spaces := false
	start := 0
	for end := 0; end <= len(rs); end++ {
		var ch rune = -1
		if end < len(rs) {
			ch = rs[end]
		}
		if spaces {
			if ch != ' ' {
				brk := start+1 == end && col >= width
				if quoted {
					brk = start+1 == end && col > width && start != 0 && end != len(rs)
				}
				if brk {
					newline()
				} else {
					write(string(rs[start:end]))
				}
				start = end
			}
		} else {
			stop := ch == -1 || ch == ' '
			if quoted {
				stop = stop || ch == '\''
			}
			if stop && start < end {
				data := string(rs[start:end])
				if !quoted && utf8.RuneCountInString(data)+col > width && col > indent {
					newline()
				}
				write(data)
				start = end
			}
			if quoted && ch == '\'' {
				write("''")
				start = end + 1
			}
		}
		spaces = ch == ' '
	}
	if quoted {
		write("'")
	}
	return b.String()
}
