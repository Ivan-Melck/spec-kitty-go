package rich

import (
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

// BoxTable is a rich.Table (HEAVY_HEAD box, padding (0,1)). Header and
// cell strings are console markup.
type BoxTable struct {
	Title     string
	Headers   []string
	NoWrap    []bool
	ShowLines bool
	Right     []bool   // right-justified columns
	Center    []bool   // centered columns
	Styles    []string // column styles ("" = none)
	Overflow  []string // column overflow ("" = ellipsis)
	Rows      [][]string
}

func atBool(xs []bool, i int) bool { return i < len(xs) && xs[i] }

func atStr(xs []string, i int) string {
	if i < len(xs) {
		return xs[i]
	}
	return ""
}

// Table returns the rich renderable.
func (t *BoxTable) Table() *RichTable {
	rt := NewTable()
	if t.Title != "" {
		rt.Title = t.Title
	}
	rt.ShowLines = t.ShowLines
	for i, h := range t.Headers {
		c := Column{NoWrap: atBool(t.NoWrap, i), Style: atStr(t.Styles, i), Overflow: atStr(t.Overflow, i)}
		if atBool(t.Right, i) {
			c.Justify = "right"
		} else if atBool(t.Center, i) {
			c.Justify = "center"
		}
		rt.AddColumn(h, c)
	}
	for _, r := range t.Rows {
		cells := make([]any, len(r))
		for i, x := range r {
			cells[i] = x
		}
		rt.AddRow(cells...)
	}
	return rt
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

// Render prints the table on a CliConsole of the given width.
func (t *BoxTable) Render(w io.Writer, consoleWidth int) {
	CliConsoleFor(w, consoleWidth).Print(w, t.Table())
}

// FitPanel prints Panel(content, title=title, expand=False) on
// CliConsole(): a ROUNDED box sized to the content (markup) with a centred
// title.
func FitPanel(w io.Writer, content, title string) { FitPanelStyled(w, content, title, "") }

// FitPanelStyled is FitPanel with border_style.
func FitPanelStyled(w io.Writer, content, title, borderStyle string) {
	p := &RichPanel{R: content, Title: title, Fit: true}
	if borderStyle != "" {
		p.BorderStyle = borderStyle
	}
	PrintTo(w, p)
}

// GridTable prints rich Table(box=None, padding=(0, 2), show_edge=False)
// on CliConsole(): a header row and data rows (markup), columns at least
// minWidths wide, right-justified where right[i].
func GridTable(w io.Writer, headers []string, minWidths []int, right []bool, rows [][]string) {
	GridTableStyled(w, headers, minWidths, right, nil, rows)
}

// GridTableStyled is GridTable with per-column styles.
func GridTableStyled(w io.Writer, headers []string, minWidths []int, right []bool, styles []string, rows [][]string) {
	PrintTo(w, NewPaddedGrid(headers, minWidths, right, styles, rows))
}

// NewPaddedGrid builds Table(box=None, padding=(0, 2), show_edge=False).
func NewPaddedGrid(headers []string, minWidths []int, right []bool, styles []string, rows [][]string) *RichTable {
	t := NewTable()
	t.Box, t.ShowEdge = nil, false
	t.SetPadding(0, 2)
	for i, h := range headers {
		c := Column{Style: atStr(styles, i)}
		if i < len(minWidths) && minWidths[i] > 0 {
			c.MinWidth = IntP(minWidths[i])
		}
		if atBool(right, i) {
			c.Justify = "right"
		}
		t.AddColumn(h, c)
	}
	for _, r := range rows {
		cells := make([]any, len(r))
		for i, x := range r {
			cells[i] = x
		}
		t.AddRow(cells...)
	}
	return t
}
