package rich

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// This file hosts the rich renderables the doctrine/charter commands use
// (moved from internal/commands/doctrine/rich.go without behaviour change):
//
//   - BoxTable: rich.Table with the default HEAVY_HEAD box and padding (0,1),
//     columns collapsing/wrapping to the console width, optional row
//     separators (show_lines) and per-column justification.
//   - FitPanel: rich.Panel(..., expand=False) — a ROUNDED box sized to its
//     content with a centered title.
//   - GridTable: rich.Table(box=None, padding=(0, 2), show_edge=False) with
//     per-column min_width and right justification.
//
// Cells must already be markup-free unless stated otherwise.

func boxLen(s string) int { return utf8.RuneCountInString(s) }

// BoxTable is a rich.Table (HEAVY_HEAD box, padding (0,1)).
type BoxTable struct {
	Title     string
	Headers   []string
	NoWrap    []bool
	ShowLines bool
	Right     []bool // right-justified columns
	Center    []bool // centered columns
	Rows      [][]string
}

func boxRatioReduce(total int, ratios, maximums, values []int) []int {
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

func boxCollapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	total := 0
	for _, w := range widths {
		total += w
	}
	excess := total - maxWidth
	anyWrap := false
	for _, w := range wrapable {
		anyWrap = anyWrap || w
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
		widths = boxRatioReduce(excess, ratios, maxs, widths)
		total = 0
		for _, w := range widths {
			total += w
		}
		excess = total - maxWidth
	}
	return widths
}

// BoxCellLines wraps a (possibly multi-line) cell to width with the table
// cell's ellipsis overflow.
func BoxCellLines(s string, width int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		out = append(out, BoxWrapEllipsis(l, width)...)
	}
	return out
}

var reBoxCellWord = regexp.MustCompile(`\s*\S+\s*`)

// BoxWrapEllipsis is rich Text.wrap with overflow="ellipsis" (a table
// cell's default): divide_line(fold=False), rstrip_end, then truncate
// overlong lines with "…".
func BoxWrapEllipsis(line string, width int) []string {
	if width < 1 {
		width = 1
	}
	var breaks []int
	offset := 0
	for _, loc := range reBoxCellWord.FindAllStringIndex(line, -1) {
		start, word := loc[0], line[loc[0]:loc[1]]
		wl := boxLen(strings.TrimRight(word, " \t"))
		if width-offset >= wl {
			offset += boxLen(word)
			continue
		}
		if wl > width {
			if start != 0 {
				breaks = append(breaks, start)
			}
			offset = boxLen(word)
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = boxLen(word)
		}
	}
	var lines []string
	prev := 0
	for _, b := range append(breaks, len(line)) {
		l := line[prev:b]
		for boxLen(l) > width && strings.HasSuffix(l, " ") {
			l = l[:len(l)-1]
		}
		if boxLen(l) > width {
			l = string([]rune(l)[:width-1]) + "…"
		}
		lines = append(lines, l)
		prev = b
	}
	return lines
}

// Render writes the table at the given console width.
func (t *BoxTable) Render(w io.Writer, consoleWidth int) {
	n := len(t.Headers)
	widths := make([]int, n)
	for i, h := range t.Headers {
		widths[i] = boxLen(h) + 2
	}
	for _, r := range t.Rows {
		for i := 0; i < n && i < len(r); i++ {
			for _, l := range strings.Split(r[i], "\n") {
				if boxLen(l)+2 > widths[i] {
					widths[i] = boxLen(l) + 2
				}
			}
		}
	}
	maxWidth := consoleWidth - (n + 1)
	wrapable := make([]bool, n)
	for i := range wrapable {
		wrapable[i] = i >= len(t.NoWrap) || !t.NoWrap[i]
	}
	widths = boxCollapseWidths(widths, wrapable, maxWidth)
	total := n + 1
	for _, wd := range widths {
		total += wd
	}
	if t.Title != "" {
		for _, l := range WrapWords(t.Title, total) {
			l = strings.TrimRight(l, " ")
			ex := total - boxLen(l)
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
			cols[i] = BoxCellLines(v, widths[i]-2)
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
				padding := strings.Repeat(" ", max(0, widths[i]-2-boxLen(c)))
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

// FitPanel renders rich.Panel(..., expand=False): ROUNDED box sized to the
// content (capped at the console width), centered title.
func FitPanel(w io.Writer, content, title string) {
	width := Width()
	lines := strings.Split(content, "\n")
	inner := 0
	for _, l := range lines {
		if boxLen(l) > inner {
			inner = boxLen(l)
		}
	}
	if title != "" && boxLen(title)+2 > inner {
		inner = boxLen(title) + 2
	}
	if inner > width-4 {
		inner = width - 4
	}
	var body []string
	for _, l := range lines {
		body = append(body, WrapWords(l, inner)...)
	}
	top := strings.Repeat("─", inner+2)
	if title != "" {
		t := " " + title + " "
		ex := inner + 2 - boxLen(t)
		left := ex / 2
		top = strings.Repeat("─", left) + t + strings.Repeat("─", ex-left)
	}
	fmt.Fprintln(w, "╭"+top+"╮")
	for _, l := range body {
		fmt.Fprintln(w, "│ "+l+strings.Repeat(" ", max(0, inner-boxLen(l)))+" │")
	}
	fmt.Fprintln(w, "╰"+strings.Repeat("─", inner+2)+"╯")
}

// GridTable renders rich Table(box=None, padding=(0, 2), show_edge=False):
// a header row and data rows, each cell padded two spaces on both sides,
// columns at least minWidths wide, right-justified where right[i]. Cells may
// carry markup (stripped). Over-wide columns collapse (down to their longest
// word) until the table fits the console, widest first.
func GridTable(w io.Writer, headers []string, minWidths []int, right []bool, rows [][]string) {
	n := len(headers)
	width := make([]int, n)
	for i, h := range headers {
		width[i] = boxLen(h)
		if i < len(minWidths) && minWidths[i] > width[i] {
			width[i] = minWidths[i]
		}
	}
	plain := make([][]string, len(rows))
	for r, row := range rows {
		plain[r] = make([]string, n)
		for i := range headers {
			c := ""
			if i < len(row) {
				c = Strip(row[i])
			}
			plain[r][i] = c
			if l := boxLen(c); l > width[i] {
				width[i] = l
			}
		}
	}
	gridMaxWord := func(s string) int {
		m := 0
		for _, f := range strings.Fields(s) {
			if l := boxLen(f); l > m {
				m = l
			}
		}
		return m
	}
	minw := make([]int, n)
	for i := range headers {
		minw[i] = gridMaxWord(headers[i])
		for _, r := range plain {
			if m := gridMaxWord(r[i]); m > minw[i] {
				minw[i] = m
			}
		}
	}
	total := func() int {
		t := 4 + 4*(n-1)
		for _, x := range width {
			t += x
		}
		return t
	}
	for excess := total() - Width(); excess > 0; excess = total() - Width() {
		best := -1
		for i := range width {
			if width[i] > minw[i] && (best < 0 || width[i] > width[best]) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		second := 0
		for i := range width {
			if i != best && width[i] > second && width[i] > minw[i] {
				second = width[i]
			}
		}
		target := width[best] - excess
		if second > target && second < width[best] {
			target = second
		}
		if target < minw[best] {
			target = minw[best]
		}
		width[best] = target
	}
	emit := func(cells []string) {
		wrapped := make([][]string, n)
		height := 1
		for i, c := range cells {
			wrapped[i] = WrapWords(c, width[i])
			if len(wrapped[i]) == 0 {
				wrapped[i] = []string{""}
			}
			if len(wrapped[i]) > height {
				height = len(wrapped[i])
			}
		}
		for l := 0; l < height; l++ {
			var b strings.Builder
			for i := range cells {
				c := ""
				if l < len(wrapped[i]) {
					c = strings.TrimRight(wrapped[i][l], " ")
				}
				if i > 0 {
					b.WriteString("    ")
				}
				fill := strings.Repeat(" ", max(0, width[i]-boxLen(c)))
				if i < len(right) && right[i] {
					b.WriteString(fill + c)
				} else {
					b.WriteString(c + fill)
				}
			}
			fmt.Fprintln(w, "  "+b.String()+"  ")
		}
	}
	emit(headers)
	for _, r := range plain {
		emit(r)
	}
}
