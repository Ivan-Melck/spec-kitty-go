package uconsole

import (
	"fmt"
	"io"
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
// and an optional centered (wrapped) title. Cell text must be markup-free.
func Table(w io.Writer, title string, headers []string, rows [][]string) {
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
			b.WriteString(" " + pad(v, wd) + " " + sep)
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
