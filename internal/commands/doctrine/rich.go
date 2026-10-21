package doctrine

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
)

// This file extends uconsole (D0) with the two rich renderables the
// doctrine commands need and uconsole lacks: a table whose columns
// collapse/wrap to the console width (optionally with row separators) and a
// non-expanded ("fit") panel. Markup must already be stripped.

func rlen(s string) int { return utf8.RuneCountInString(s) }

// rtable is a rich.Table (HEAVY_HEAD box, padding (0,1)).
type rtable struct {
	Title     string
	Headers   []string
	NoWrap    []bool
	ShowLines bool
	Right     []bool // right-justified columns
	Center    []bool // centered columns
	Rows      [][]string
}

func ratioReduce(total int, ratios, maximums, values []int) []int {
	for i := range ratios {
		if maximums[i] == 0 {
			ratios[i] = 0
		}
	}
	totalRatio := 0
	for _, r := range ratios {
		totalRatio += r
	}
	if totalRatio == 0 {
		return append([]int(nil), values...)
	}
	remaining := total
	out := make([]int, len(values))
	for i := range values {
		if ratios[i] != 0 && totalRatio > 0 {
			d := int(math.RoundToEven(float64(ratios[i]) * float64(remaining) / float64(totalRatio)))
			if d > maximums[i] {
				d = maximums[i]
			}
			out[i] = values[i] - d
			remaining -= d
			totalRatio -= ratios[i]
		} else {
			out[i] = values[i]
		}
	}
	return out
}

func collapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	total := 0
	for _, w := range widths {
		total += w
	}
	excess := total - maxWidth
	any := false
	for _, w := range wrapable {
		any = any || w
	}
	if !any {
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
		ratios := make([]int, len(widths))
		has := false
		for i, w := range widths {
			if w == maxCol && wrapable[i] {
				ratios[i] = 1
				has = true
			}
		}
		if !has || diff == 0 {
			break
		}
		red := excess
		if diff < red {
			red = diff
		}
		maxs := make([]int, len(widths))
		for i := range maxs {
			maxs[i] = red
		}
		widths = ratioReduce(excess, ratios, maxs, widths)
		total = 0
		for _, w := range widths {
			total += w
		}
		excess = total - maxWidth
	}
	return widths
}

func cellLines(s string, width int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, wrapEllipsis(l, width)...)
	}
	return out
}

var reCellWord = regexp.MustCompile(`\s*\S+\s*`)

// wrapEllipsis is rich Text.wrap with overflow="ellipsis" (a table cell's
// default): divide_line(fold=False), rstrip_end, then truncate overlong
// lines with "…".
func wrapEllipsis(line string, width int) []string {
	if width < 1 {
		width = 1
	}
	var breaks []int
	offset := 0
	for _, loc := range reCellWord.FindAllStringIndex(line, -1) {
		start, word := loc[0], line[loc[0]:loc[1]]
		wl := rlen(strings.TrimRight(word, " \t"))
		if width-offset >= wl {
			offset += rlen(word)
			continue
		}
		if wl > width {
			if start != 0 {
				breaks = append(breaks, start)
			}
			offset = rlen(word)
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = rlen(word)
		}
	}
	var lines []string
	prev := 0
	for _, b := range append(breaks, len(line)) {
		l := line[prev:b]
		for rlen(l) > width && strings.HasSuffix(l, " ") {
			l = l[:len(l)-1]
		}
		if rlen(l) > width {
			l = string([]rune(l)[:width-1]) + "…"
		}
		lines = append(lines, l)
		prev = b
	}
	return lines
}

func (t *rtable) render(w io.Writer, consoleWidth int) {
	n := len(t.Headers)
	widths := make([]int, n)
	for i, h := range t.Headers {
		widths[i] = rlen(h) + 2
	}
	for _, r := range t.Rows {
		for i := 0; i < n && i < len(r); i++ {
			for _, l := range strings.Split(r[i], "\n") {
				if rlen(l)+2 > widths[i] {
					widths[i] = rlen(l) + 2
				}
			}
		}
	}
	maxWidth := consoleWidth - (n + 1)
	wrapable := make([]bool, n)
	for i := range wrapable {
		wrapable[i] = i >= len(t.NoWrap) || !t.NoWrap[i]
	}
	widths = collapseWidths(widths, wrapable, maxWidth)
	total := n + 1
	for _, wd := range widths {
		total += wd
	}
	if t.Title != "" {
		for _, l := range uconsole.WrapWords(t.Title, total) {
			l = strings.TrimRight(l, " ")
			ex := total - rlen(l)
			left := ex / 2
			fmt.Fprintln(w, strings.Repeat(" ", left)+l+strings.Repeat(" ", ex-left))
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
	row := func(sep string, vals []string) {
		cols := make([][]string, n)
		height := 1
		for i := 0; i < n; i++ {
			v := ""
			if i < len(vals) {
				v = vals[i]
			}
			cols[i] = cellLines(v, widths[i]-2)
			if len(cols[i]) > height {
				height = len(cols[i])
			}
		}
		for k := 0; k < height; k++ {
			var b strings.Builder
			b.WriteString(sep)
			for i := 0; i < n; i++ {
				c := ""
				if k < len(cols[i]) {
					c = cols[i][k]
				}
				padding := strings.Repeat(" ", max(0, widths[i]-2-rlen(c)))
				if i < len(t.Right) && t.Right[i] {
					b.WriteString(" " + padding + c + " " + sep)
				} else if i < len(t.Center) && t.Center[i] {
					ex := len(padding)
					b.WriteString(" " + strings.Repeat(" ", ex/2) + c + strings.Repeat(" ", ex-ex/2) + " " + sep)
				} else {
					b.WriteString(" " + c + padding + " " + sep)
				}
			}
			fmt.Fprintln(w, b.String())
		}
	}
	line("┏", "┳", "┓", "━")
	row("┃", t.Headers)
	line("┡", "╇", "┩", "━")
	for i, r := range t.Rows {
		if i > 0 && t.ShowLines {
			line("├", "┼", "┤", "─")
		}
		row("│", r)
	}
	line("└", "┴", "┘", "─")
}

// fitPanel renders rich.Panel(..., expand=False): ROUNDED box sized to the
// content (capped at the console width), centered title.
func fitPanel(w io.Writer, content, title string) {
	width := uconsole.Width()
	lines := strings.Split(content, "\n")
	inner := 0
	for _, l := range lines {
		if rlen(l) > inner {
			inner = rlen(l)
		}
	}
	if title != "" && rlen(title)+2 > inner {
		inner = rlen(title) + 2
	}
	if inner > width-4 {
		inner = width - 4
	}
	var body []string
	for _, l := range lines {
		body = append(body, uconsole.WrapWords(l, inner)...)
	}
	top := strings.Repeat("─", inner+2)
	if title != "" {
		t := " " + title + " "
		ex := inner + 2 - rlen(t)
		left := ex / 2
		top = strings.Repeat("─", left) + t + strings.Repeat("─", ex-left)
	}
	fmt.Fprintln(w, "╭"+top+"╮")
	for _, l := range body {
		fmt.Fprintln(w, "│ "+l+strings.Repeat(" ", max(0, inner-rlen(l)))+" │")
	}
	fmt.Fprintln(w, "╰"+strings.Repeat("─", inner+2)+"╯")
}
