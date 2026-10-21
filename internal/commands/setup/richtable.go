package setup

import (
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// richTable renders a rich.Table (HEAVY_HEAD box, padding (0, 1)) with a
// centered title and per-column justification ('l', 'c', 'r'). Cells must be
// markup-free.
func richTable(w io.Writer, title string, headers []string, justify []byte, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runeLen(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && runeLen(c) > widths[i] {
				widths[i] = runeLen(c)
			}
		}
	}
	total := 1
	for _, wd := range widths {
		total += wd + 3
	}
	if title != "" {
		for _, l := range uconsole.WrapWords(title, total) {
			fmt.Fprintln(w, alignText(strings.TrimRight(l, " "), total, 'c'))
		}
	}
	line := func(l, m, r, h string) {
		var b strings.Builder
		b.WriteString(l)
		for i, wd := range widths {
			if i > 0 {
				b.WriteString(m)
			}
			b.WriteString(strings.Repeat(h, wd+2))
		}
		b.WriteString(r)
		fmt.Fprintln(w, b.String())
	}
	cells := func(sep string, vals []string) {
		var b strings.Builder
		b.WriteString(sep)
		for i, wd := range widths {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			j := byte('l')
			if i < len(justify) {
				j = justify[i]
			}
			b.WriteString(" " + alignText(v, wd, j) + " " + sep)
		}
		fmt.Fprintln(w, b.String())
	}
	line("┏", "┳", "┓", "━")
	cells("┃", headers)
	line("┡", "╇", "┩", "━")
	for _, r := range rows {
		cells("│", r)
	}
	line("└", "┴", "┘", "─")
}

func alignText(s string, n int, j byte) string {
	d := n - runeLen(s)
	if d <= 0 {
		return s
	}
	switch j {
	case 'c':
		return strings.Repeat(" ", d/2) + s + strings.Repeat(" ", d-d/2)
	case 'r':
		return strings.Repeat(" ", d) + s
	}
	return s + strings.Repeat(" ", d)
}

// printJSONRaw is console.emit_json (indent 2, ensure_ascii=False).
func printJSONRaw(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Options{Indent: 2}))
}
