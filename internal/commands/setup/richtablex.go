package setup

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
)

// tableSpec configures richTableX, a closer rich.Table model: fixed column
// widths, show_lines separators, and width collapsing when the table is
// wider than the console (rich's _collapse_widths over wrapable columns),
// with cells word-wrapped and over-long words cropped with an ellipsis.
type tableSpec struct {
	Title     string
	Headers   []string
	Justify   []byte // 'l' (default), 'c', 'r'
	Width     []int  // fixed content width per column (0 = auto)
	ShowLines bool
	MaxWidth  int // 0 = console width
}

func cellWords(s string) []string { return strings.Fields(s) }

func maxLineLen(s string) int {
	m := 0
	for _, l := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(l); n > m {
			m = n
		}
	}
	return m
}

func minWordLen(s string) int {
	m := 0
	for _, w := range cellWords(s) {
		if n := utf8.RuneCountInString(w); n > m {
			m = n
		}
	}
	return m
}

// wrapCell is rich Text.wrap(width, overflow="ellipsis"): greedy word wrap
// (no folding), then each line truncated to width with "…".
func wrapCell(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := cellWords(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, wd := range words {
			switch {
			case cur == "":
				cur = wd
			case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(wd) <= width:
				cur += " " + wd
			default:
				out = append(out, cur)
				cur = wd
			}
		}
		out = append(out, cur)
	}
	for i, l := range out {
		if utf8.RuneCountInString(l) > width {
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

// collapseWidths is rich.table.Table._collapse_widths.
func collapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	total := 0
	for _, w := range widths {
		total += w
	}
	excess := total - maxWidth
	anyWrap := false
	for _, b := range wrapable {
		anyWrap = anyWrap || b
	}
	if !anyWrap {
		return widths
	}
	for total > 0 && excess > 0 {
		maxCol := 0
		for i, w := range widths {
			if wrapable[i] && w > maxCol {
				maxCol = w
			}
		}
		second := 0
		for i, w := range widths {
			if wrapable[i] && w != maxCol && w > second {
				second = w
			}
		}
		diff := maxCol - second
		var idx []int
		for i, w := range widths {
			if wrapable[i] && w == maxCol {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 || diff == 0 {
			break
		}
		reduce := excess
		if diff < reduce {
			reduce = diff
		}
		// ratio_reduce with equal ratios over idx, each capped at reduce.
		remaining := excess
		totalRatio := len(idx)
		for _, i := range idx {
			if remaining <= 0 {
				break
			}
			d := int(roundHalfEven(float64(remaining) / float64(totalRatio)))
			if d > reduce {
				d = reduce
			}
			widths[i] -= d
			remaining -= d
			totalRatio--
		}
		total = 0
		for _, w := range widths {
			total += w
		}
		excess = total - maxWidth
	}
	return widths
}

func roundHalfEven(f float64) float64 {
	i := float64(int64(f))
	d := f - i
	switch {
	case d > 0.5:
		return i + 1
	case d < 0.5:
		return i
	}
	if int64(i)%2 == 0 {
		return i
	}
	return i + 1
}

func richTableX(w io.Writer, spec tableSpec, rows [][]string) {
	n := len(spec.Headers)
	maxW := spec.MaxWidth
	if maxW == 0 {
		maxW = uconsole.Width()
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
		widths = collapseWidths(widths, wrapable, maxW-borders)
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
		for _, l := range uconsole.WrapWords(spec.Title, tableW) {
			fmt.Fprintln(w, alignText(strings.TrimRight(l, " "), tableW, 'c'))
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
			cells[i] = wrapCell(v, content[i])
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
				b.WriteString(" " + alignText(v, content[i], j) + " " + sep)
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
