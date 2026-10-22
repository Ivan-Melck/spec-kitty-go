package rich

import (
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// center pads s to width with fill, extra going right (rich justify=center).
func center(s string, width int, fill string) string {
	ex := width - runeLen(s)
	if ex <= 0 {
		return s
	}
	l := ex / 2
	return strings.Repeat(fill, l) + s + strings.Repeat(fill, ex-l)
}

func pad(s string, width int) string {
	if n := runeLen(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// WrapWords greedily wraps s at width on spaces (rich Text.wrap, folding
// over-long words).
func WrapWords(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	return wrapLine(s, width)
}

// Table renders a rich.Table with the default HEAVY_HEAD box, padding (0, 1)
// and an optional centered (wrapped) title at the console width. Cell text
// must be markup-free.
func Table(w io.Writer, title string, headers []string, rows [][]string) {
	TableWidth(w, title, headers, rows, Width())
}

func maxLineLen(s string) int {
	m := 0
	for _, line := range strings.Split(s, "\n") {
		if n := runeLen(line); n > m {
			m = n
		}
	}
	return m
}

// pyRound is Python's round() (half to even).
func pyRound(f float64) int { return int(math.RoundToEven(f)) }

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// TableWidth is Table printed by a console of width maxWidth: when the
// table does not fit, columns collapse and cells word-wrap like rich.
func TableWidth(w io.Writer, title string, headers []string, rows [][]string, maxWidth int) {
	n := len(headers)
	extra := 2 + n - 1 // outer edges + column dividers
	avail := maxWidth - extra
	// widths include the cell padding (one space each side).
	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = maxLineLen(h)
	}
	for _, r := range rows {
		for i := 0; i < n && i < len(r); i++ {
			if l := maxLineLen(r[i]); l > widths[i] {
				widths[i] = l
			}
		}
	}
	for i := range widths {
		widths[i] += 2
		if widths[i] > avail {
			widths[i] = avail
		}
		if widths[i] < 1 {
			widths[i] = 1
		}
	}
	if sum(widths) > avail {
		all := make([]bool, n)
		for i := range all {
			all[i] = true
		}
		widths = CollapseWidths(widths, all, avail)
		if sum(widths) > avail {
			ones := make([]int, n)
			for i := range ones {
				ones[i] = 1
			}
			widths = RatioReduce(sum(widths)-avail, ones, widths, widths)
		}
	}
	inner := make([]int, n)
	for i := range widths {
		inner[i] = widths[i] - 2
		if inner[i] < 1 {
			inner[i] = 1
		}
	}
	total := sum(widths) + extra
	if title != "" {
		for _, line := range WrapWords(title, total) {
			fmt.Fprintln(w, center(strings.TrimRight(line, " "), total, " "))
		}
	}
	line := func(l, m, r, h string) {
		var b strings.Builder
		b.WriteString(l)
		for i, wd := range widths {
			if i > 0 {
				b.WriteString(m)
			}
			b.WriteString(strings.Repeat(h, wd))
		}
		b.WriteString(r)
		fmt.Fprintln(w, b.String())
	}
	cells := func(sep string, vals []string) {
		bottom := sep == "┃" // header cells align to the bottom
		wrapped := make([][]string, n)
		height := 1
		for i := 0; i < n; i++ {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			var ls []string
			for _, l := range strings.Split(v, "\n") {
				for _, wl := range WrapEllipsis(l, inner[i]) {
					ls = append(ls, strings.TrimRight(wl, " "))
				}
			}
			wrapped[i] = ls
			if len(ls) > height {
				height = len(ls)
			}
		}
		for row := 0; row < height; row++ {
			var b strings.Builder
			b.WriteString(sep)
			for i := 0; i < n; i++ {
				v := ""
				r := row
				if bottom {
					r = row - (height - len(wrapped[i]))
				}
				if r >= 0 && r < len(wrapped[i]) {
					v = wrapped[i][r]
				}
				b.WriteString(" " + pad(v, inner[i]) + " " + sep)
			}
			fmt.Fprintln(w, b.String())
		}
	}
	line("┏", "┳", "┓", "━")
	cells("┃", headers)
	line("┡", "╇", "┩", "━")
	for _, r := range rows {
		cells("│", r)
	}
	line("└", "┴", "┘", "─")
}

// Panel renders an expanded rich.Panel (ROUNDED box, padding (0, 1)) at the
// console width with a centered title. Content markup is stripped.
func Panel(w io.Writer, content, title string) {
	width := Width()
	inner := width - 4
	top := "─" + strings.Repeat("─", width-4) + "─"
	if title != "" {
		top = center(" "+title+" ", width-2, "─")
	}
	fmt.Fprintln(w, "╭"+top+"╮")
	for _, l := range strings.Split(Strip(content), "\n") {
		for _, wl := range WrapWords(l, inner) {
			fmt.Fprintln(w, "│ "+pad(wl, inner)+" │")
		}
	}
	fmt.Fprintln(w, "╰"+strings.Repeat("─", width-2)+"╯")
}
