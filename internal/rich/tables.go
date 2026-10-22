package rich

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// AlignText pads s to n runes per justify ('l' default, 'c' centre with the
// extra space right, 'r').
func AlignText(s string, n int, justify byte) string {
	d := n - runeLen(s)
	if d <= 0 {
		return s
	}
	switch justify {
	case 'c':
		return strings.Repeat(" ", d/2) + s + strings.Repeat(" ", d-d/2)
	case 'r':
		return strings.Repeat(" ", d) + s
	}
	return s + strings.Repeat(" ", d)
}

// HeavyTable renders a rich.Table (HEAVY_HEAD box, padding (0, 1)) sized to
// its content, with a centered title and per-column justification ('l',
// 'c', 'r'). Cells must be markup-free and are never wrapped.
func HeavyTable(w io.Writer, title string, headers []string, justify []byte, rows [][]string) {
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
		for _, l := range WrapWords(title, total) {
			fmt.Fprintln(w, AlignText(strings.TrimRight(l, " "), total, 'c'))
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
			b.WriteString(" " + AlignText(v, wd, j) + " " + sep)
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

// TableSpec configures TableX, a HEAVY_HEAD rich.Table with fixed column
// widths, show_lines separators, and width collapsing when the table is
// wider than the console (rich's _collapse_widths over wrapable columns),
// with cells word-wrapped and over-long words cropped with an ellipsis.
type TableSpec struct {
	Title     string
	Headers   []string
	Justify   []byte // 'l' (default), 'c', 'r'
	Width     []int  // fixed content width per column (0 = auto)
	ShowLines bool
	MaxWidth  int // 0 = console width
}

// wrapCellWords is rich Text.wrap(width, overflow="ellipsis") for cells of
// single-spaced words: greedy word wrap (no folding), then each line
// truncated to width with "…".
var cellWordRe = regexp.MustCompile(`\s*\S+\s*`)

func wrapCellWords(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		// rich divide_line: words are \s*\S+\s* runs; a word that does not
		// fit (measured without its trailing whitespace) starts a new line;
		// interior whitespace runs are kept.
		words := cellWordRe.FindAllString(para, -1)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur, pos := "", 0
		for _, wd := range words {
			wl := runeLen(strings.TrimRight(wd, " \t"))
			switch {
			case width-pos >= wl:
				cur += wd
				pos += runeLen(wd)
			case pos == 0:
				cur, pos = wd, runeLen(wd)
			default:
				out = append(out, strings.TrimRight(cur, " \t"))
				cur, pos = wd, runeLen(wd)
			}
		}
		out = append(out, strings.TrimRight(cur, " \t"))
	}
	for i, l := range out {
		if runeLen(l) > width {
			r := []rune(l)
			if width <= 0 {
				out[i] = ""
			} else {
				out[i] = string(r[:width-1]) + "…"
			}
		}
	}
	return out
}

// TableX renders spec and rows.
func TableX(w io.Writer, spec TableSpec, rows [][]string) {
	n := len(spec.Headers)
	maxW := spec.MaxWidth
	if maxW == 0 {
		maxW = Width()
	}
	widths := make([]int, n) // including padding (2)
	wrapable := make([]bool, n)
	for i := 0; i < n; i++ {
		if i < len(spec.Width) && spec.Width[i] > 0 {
			widths[i] = spec.Width[i] + 2
			continue
		}
		wrapable[i] = true
		m := maxLineLen(spec.Headers[i])
		for _, r := range rows {
			if i < len(r) {
				if l := maxLineLen(r[i]); l > m {
					m = l
				}
			}
		}
		widths[i] = m + 2
	}
	borders := n + 1
	total := borders
	for _, x := range widths {
		total += x
	}
	if total > maxW {
		widths = CollapseWidths(widths, wrapable, maxW-borders)
	}
	content := make([]int, n)
	tableW := borders
	for i, x := range widths {
		content[i] = x - 2
		if content[i] < 1 {
			content[i] = 1
		}
		tableW += content[i] + 2
	}
	if spec.Title != "" {
		for _, l := range WrapWords(spec.Title, tableW) {
			fmt.Fprintln(w, AlignText(strings.TrimRight(l, " "), tableW, 'c'))
		}
	}
	line := func(l, m, r, h string) {
		var b strings.Builder
		b.WriteString(l)
		for i, wd := range content {
			if i > 0 {
				b.WriteString(m)
			}
			b.WriteString(strings.Repeat(h, wd+2))
		}
		b.WriteString(r)
		fmt.Fprintln(w, b.String())
	}
	row := func(sep string, vals []string) {
		cells := make([][]string, n)
		height := 1
		for i := 0; i < n; i++ {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			cells[i] = wrapCellWords(v, content[i])
			if len(cells[i]) > height {
				height = len(cells[i])
			}
		}
		for h := 0; h < height; h++ {
			var b strings.Builder
			b.WriteString(sep)
			for i := 0; i < n; i++ {
				v := ""
				if h < len(cells[i]) {
					v = cells[i][h]
				}
				j := byte('l')
				if i < len(spec.Justify) {
					j = spec.Justify[i]
				}
				b.WriteString(" " + AlignText(v, content[i], j) + " " + sep)
			}
			fmt.Fprintln(w, b.String())
		}
	}
	line("┏", "┳", "┓", "━")
	row("┃", spec.Headers)
	line("┡", "╇", "┩", "━")
	for i, r := range rows {
		if i > 0 && spec.ShowLines {
			line("├", "┼", "┤", "─")
		}
		row("│", r)
	}
	line("└", "┴", "┘", "─")
}
