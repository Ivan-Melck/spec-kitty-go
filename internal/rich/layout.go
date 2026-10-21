package rich

import (
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is a plain-text port of the parts of rich's layout engine the
// CLI output depends on (rich 15: Text wrapping, Padding, Align, Columns,
// Table, Panel), so renderers can be composed the way upstream composes
// them and produce byte-identical non-terminal output (no ANSI styles).

// Measurement is rich.measure.Measurement: the minimum and maximum cell
// widths a renderable needs.
type Measurement struct{ Min, Max int }

func (m Measurement) normalize() Measurement {
	mn := min(max(0, m.Min), m.Max)
	return Measurement{max(0, mn), max(0, max(mn, m.Max))}
}

func (m Measurement) withMaximum(w int) Measurement {
	return Measurement{min(m.Min, w), min(m.Max, w)}
}

// Options is the subset of rich.console.ConsoleOptions that affects layout.
type Options struct {
	MaxWidth int
	Justify  string // "", "default", "left", "center", "right", "full"
	Overflow string // "", "fold", "crop", "ellipsis", "ignore"
	NoWrap   bool
}

func (o Options) withWidth(w int) Options { o.MaxWidth = w; return o }

// Renderable is anything that renders to lines of text.
type Renderable interface {
	// Render returns the rendered lines (not necessarily padded).
	Render(o Options) []string
}

type measurer interface {
	Measure(o Options) Measurement
}

// Measure is rich Measurement.get.
func Measure(r Renderable, o Options) Measurement {
	if o.MaxWidth < 1 {
		return Measurement{}
	}
	m, ok := r.(measurer)
	if !ok {
		return Measurement{0, o.MaxWidth}
	}
	w := m.Measure(o).normalize().withMaximum(o.MaxWidth)
	if w.Max < 1 {
		return Measurement{}
	}
	return w.normalize()
}

// RenderLines is console.render_lines(pad=True): every line cropped or
// padded to exactly o.MaxWidth cells.
func RenderLines(r Renderable, o Options) []string {
	lines := r.Render(o)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = SetCellSize(l, o.MaxWidth)
	}
	return out
}

// ---------------------------------------------------------------- Text

// Text is rich.text.Text without styles. Justify/Overflow/NoWrap override
// the render options when set.
type Text struct {
	Plain    string
	Justify  string
	Overflow string
	NoWrap   bool
}

// NewText returns a Text for plain (no markup processing).
func NewText(plain string) *Text { return &Text{Plain: plain} }

// FromMarkup is Text.from_markup: rich markup tags are removed.
func FromMarkup(markup string) *Text { return &Text{Plain: Strip(markup)} }

var reSplitLines = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)

// Measure is Text.__rich_measure__.
func (t *Text) Measure(o Options) Measurement {
	text := t.Plain
	lines := reSplitLines.Split(text, -1)
	if strings.HasSuffix(text, "\n") || text == "" {
		// str.splitlines drops the final empty piece.
		lines = lines[:len(lines)-1]
	}
	maxW := 0
	for _, l := range lines {
		maxW = max(maxW, CellLen(l))
	}
	minW := maxW
	if words := strings.Fields(text); len(words) > 0 {
		minW = 0
		for _, w := range words {
			minW = max(minW, CellLen(w))
		}
	}
	return Measurement{minW, maxW}
}

// Render is Text.__rich_console__ (wrap at the available width).
func (t *Text) Render(o Options) []string {
	justify := firstNonEmpty(t.Justify, o.Justify, "default")
	overflow := firstNonEmpty(t.Overflow, o.Overflow, "fold")
	return WrapText(t.Plain, o.MaxWidth, justify, overflow, t.NoWrap || o.NoWrap)
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// WrapText is rich Text.wrap: each line is word-wrapped (folding or
// cropping over-long words per overflow), justified and truncated.
func WrapText(text string, width int, justify, overflow string, noWrap bool) []string {
	if overflow == "ignore" {
		noWrap = true
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "\t") {
			line = expandTabs(line, 8)
		}
		var lines []string
		if noWrap {
			if overflow == "ignore" {
				out = append(out, line)
				continue
			}
			lines = []string{line}
		} else {
			offsets := divideLine(line, width, overflow == "fold")
			prev := 0
			for _, off := range append(offsets, len(line)) {
				lines = append(lines, rstripEnd(line[prev:off], width))
				prev = off
			}
		}
		for i, l := range lines {
			switch justify {
			case "left":
				l = truncate(l, width, overflow, true)
			case "center":
				l = truncate(strings.TrimRight(l, " \t\n\r\v\f"), width, overflow, false)
				n := CellLen(l)
				l = strings.Repeat(" ", max(0, (width-n)/2)) + l
				l += strings.Repeat(" ", max(0, width-CellLen(l)))
			case "right":
				l = truncate(strings.TrimRight(l, " \t\n\r\v\f"), width, overflow, false)
				l = strings.Repeat(" ", max(0, width-CellLen(l))) + l
			}
			lines[i] = truncate(l, width, overflow, false)
		}
		out = append(out, lines...)
	}
	return out
}

func expandTabs(s string, tab int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tab - col%tab
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += CharWidth(r)
	}
	return b.String()
}

// truncate is Text.truncate.
func truncate(s string, width int, overflow string, pad bool) string {
	if overflow == "ignore" {
		return s
	}
	n := CellLen(s)
	if n > width {
		if overflow == "ellipsis" {
			s = SetCellSize(s, width-1) + "…"
		} else {
			s = SetCellSize(s, width)
		}
	}
	if pad && n < width {
		s += strings.Repeat(" ", width-n)
	}
	return s
}

var reTrailingWS = regexp.MustCompile(`\s+$`)

// rstripEnd is Text.rstrip_end: drop trailing whitespace beyond size
// characters.
func rstripEnd(s string, size int) string {
	n := utf8.RuneCountInString(s)
	if n <= size {
		return s
	}
	excess := n - size
	if m := reTrailingWS.FindString(s); m != "" {
		cut := min(utf8.RuneCountInString(m), excess)
		r := []rune(s)
		return string(r[:len(r)-cut])
	}
	return s
}

var reWordSpan = regexp.MustCompile(`\s*\S+\s*`)

// divideLine is rich._wrap.divide_line: byte offsets to break text at so
// every piece fits width cells.
func divideLine(text string, width int, fold bool) []int {
	var breaks []int
	offset := 0
	pos := 0
	for pos <= len(text) {
		loc := reWordSpan.FindStringIndex(text[pos:])
		if loc == nil || loc[0] != 0 {
			break
		}
		start, end := pos, pos+loc[1]
		word := text[start:end]
		pos = end
		wordLen := CellLen(strings.TrimRightFunc(word, unicode.IsSpace))
		remaining := width - offset
		if remaining >= wordLen {
			offset += CellLen(word)
		} else if wordLen > width {
			if fold {
				chunks := chopCells(word, width)
				for i, chunk := range chunks {
					if start != 0 {
						breaks = append(breaks, start)
					}
					if i == len(chunks)-1 {
						offset = CellLen(chunk)
					} else {
						start += len(chunk)
					}
				}
			} else {
				if start != 0 {
					breaks = append(breaks, start)
				}
				offset = CellLen(word)
			}
		} else if offset != 0 && start != 0 {
			breaks = append(breaks, start)
			offset = CellLen(word)
		}
		if end == len(text) {
			break
		}
	}
	return breaks
}

// ---------------------------------------------------------------- Group

// Group renders renderables one after another (rich.console.Group).
type Group []Renderable

// Measure is the widest member measurement.
func (g Group) Measure(o Options) Measurement {
	if len(g) == 0 {
		return Measurement{}
	}
	var m Measurement
	for _, r := range g {
		x := Measure(r, o)
		m.Min = max(m.Min, x.Min)
		m.Max = max(m.Max, x.Max)
	}
	return m
}

// Render concatenates the members' lines.
func (g Group) Render(o Options) []string {
	var out []string
	for _, r := range g {
		out = append(out, r.Render(o)...)
	}
	return out
}

// ---------------------------------------------------------------- Padding

// Padding is rich.padding.Padding with expand=True.
type Padding struct {
	R                        Renderable
	Top, Right, Bottom, Left int
}

// Pad builds a Padding from CSS-style dimensions (1, 2 or 4 values).
func Pad(r Renderable, dims ...int) *Padding {
	p := &Padding{R: r}
	switch len(dims) {
	case 1:
		p.Top, p.Right, p.Bottom, p.Left = dims[0], dims[0], dims[0], dims[0]
	case 2:
		p.Top, p.Bottom = dims[0], dims[0]
		p.Left, p.Right = dims[1], dims[1]
	case 4:
		p.Top, p.Right, p.Bottom, p.Left = dims[0], dims[1], dims[2], dims[3]
	}
	return p
}

// Measure is Padding.__rich_measure__.
func (p *Padding) Measure(o Options) Measurement {
	extra := p.Left + p.Right
	if o.MaxWidth-extra < 1 {
		return Measurement{o.MaxWidth, o.MaxWidth}
	}
	m := Measure(p.R, o)
	return Measurement{m.Min + extra, m.Max + extra}.withMaximum(o.MaxWidth)
}

// Render is Padding.__rich_console__.
func (p *Padding) Render(o Options) []string {
	width := o.MaxWidth
	inner := RenderLines(p.R, o.withWidth(width-p.Left-p.Right))
	var out []string
	blank := strings.Repeat(" ", max(0, width))
	for i := 0; i < p.Top; i++ {
		out = append(out, blank)
	}
	l, r := strings.Repeat(" ", p.Left), strings.Repeat(" ", p.Right)
	for _, line := range inner {
		out = append(out, l+line+r)
	}
	for i := 0; i < p.Bottom; i++ {
		out = append(out, blank)
	}
	return out
}

// ---------------------------------------------------------------- Align

// AlignLeft is rich.align.Align(renderable, "left", pad=False).
type AlignLeft struct{ R Renderable }

// Measure delegates to the wrapped renderable.
func (a *AlignLeft) Measure(o Options) Measurement { return Measure(a.R, o) }

// Render renders at the measured width and squares the lines.
func (a *AlignLeft) Render(o Options) []string {
	w := min(Measure(a.R, o).Max, o.MaxWidth)
	lines := a.R.Render(o.withWidth(w))
	shape := 0
	for _, l := range lines {
		shape = max(shape, CellLen(l))
	}
	for i, l := range lines {
		lines[i] = SetCellSize(l, shape)
	}
	return lines
}

// ---------------------------------------------------------------- Table

// Column is rich.table.Column (layout attributes only).
type Column struct {
	Width    int // fixed content width; 0 means auto
	Ratio    int
	NoWrap   bool
	Overflow string // default "ellipsis"
	Justify  string // default "left"
	flexible bool
}

// Grid is a rich.table.Table without box, header or footer (the grid
// form used by help panels and Columns).
type Grid struct {
	Columns         []*Column
	Rows            [][]Renderable
	Padding         [4]int // top, right, bottom, left
	CollapsePadding bool
	PadEdge         bool
	Expand          bool
}

// AddRow appends a row, creating default columns as needed (nil cells are
// empty text).
func (t *Grid) AddRow(cells ...Renderable) {
	for len(t.Columns) < len(cells) {
		t.Columns = append(t.Columns, &Column{})
		for i := range t.Rows {
			t.Rows[i] = append(t.Rows[i], nil)
		}
	}
	row := make([]Renderable, len(t.Columns))
	copy(row, cells)
	t.Rows = append(t.Rows, row)
}

func (t *Grid) cell(row, col int) Renderable {
	if col < len(t.Rows[row]) && t.Rows[row][col] != nil {
		return t.Rows[row][col]
	}
	return NewText("")
}

func (t *Grid) cellPadding(col int, firstRow, lastRow bool) (top, right, bottom, left int) {
	top, right, bottom, left = t.Padding[0], t.Padding[1], t.Padding[2], t.Padding[3]
	firstCol, lastCol := col == 0, col == len(t.Columns)-1
	if t.CollapsePadding {
		if !firstCol {
			left = max(0, left-right)
		}
		if !lastRow {
			bottom = max(0, top-bottom)
		}
	}
	if !t.PadEdge {
		if firstCol {
			left = 0
		}
		if lastCol {
			right = 0
		}
		if firstRow {
			top = 0
		}
		if lastRow {
			bottom = 0
		}
	}
	return
}

func (t *Grid) paddingWidth(col int) int {
	right, left := t.Padding[1], t.Padding[3]
	if t.CollapsePadding {
		left = 0
		right = abs(left - right)
	}
	if !t.PadEdge {
		if col == 0 {
			left = 0
		}
		if col == len(t.Columns)-1 {
			right = 0
		}
	}
	return left + right
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (t *Grid) paddedCell(row, col int) Renderable {
	r := t.cell(row, col)
	if t.Padding == [4]int{} {
		return r
	}
	top, right, bottom, left := t.cellPadding(col, row == 0, row == len(t.Rows)-1)
	return &Padding{R: r, Top: top, Right: right, Bottom: bottom, Left: left}
}

func (t *Grid) measureColumn(o Options, col int) Measurement {
	if o.MaxWidth < 1 {
		return Measurement{}
	}
	pw := t.paddingWidth(col)
	c := t.Columns[col]
	if c.Width > 0 {
		return Measurement{c.Width + pw, c.Width + pw}.withMaximum(o.MaxWidth)
	}
	if len(t.Rows) == 0 {
		return Measurement{1, o.MaxWidth}.withMaximum(o.MaxWidth)
	}
	var m Measurement
	for i := range t.Rows {
		x := Measure(t.paddedCell(i, col), o)
		if i == 0 {
			m = x
			continue
		}
		m.Min = max(m.Min, x.Min)
		m.Max = max(m.Max, x.Max)
	}
	return m.withMaximum(o.MaxWidth)
}

func (t *Grid) columnWidths(o Options) []int {
	maxWidth := o.MaxWidth
	n := len(t.Columns)
	ranges := make([]Measurement, n)
	widths := make([]int, n)
	for i := range t.Columns {
		ranges[i] = t.measureColumn(o, i)
		widths[i] = ranges[i].Max
		if widths[i] == 0 {
			widths[i] = 1
		}
	}
	if t.Expand {
		var ratios, flexMin []int
		any := false
		for i, c := range t.Columns {
			c.flexible = c.Ratio > 0
			if c.flexible {
				ratios = append(ratios, c.Ratio)
				w := c.Width
				if w == 0 {
					w = 1
				}
				flexMin = append(flexMin, w+t.paddingWidth(i))
				any = true
			}
		}
		if any {
			fixed := make([]int, n)
			sumFixed := 0
			for i, c := range t.Columns {
				if !c.flexible {
					fixed[i] = ranges[i].Max
				}
				sumFixed += fixed[i]
			}
			flex := RatioDistribute(maxWidth-sumFixed, ratios, flexMin)
			k := 0
			for i, c := range t.Columns {
				if c.flexible {
					widths[i] = fixed[i] + flex[k]
					k++
				}
			}
		}
	}
	tableWidth := sum(widths)
	if tableWidth > maxWidth {
		wrapable := make([]bool, n)
		for i, c := range t.Columns {
			wrapable[i] = c.Width == 0 && !c.NoWrap
		}
		widths = CollapseWidths(widths, wrapable, maxWidth)
		tableWidth = sum(widths)
		if tableWidth > maxWidth {
			ones := make([]int, n)
			for i := range ones {
				ones[i] = 1
			}
			widths = RatioReduce(tableWidth-maxWidth, ones, widths, widths)
			tableWidth = sum(widths)
		}
		for i := range t.Columns {
			widths[i] = t.measureColumn(o.withWidth(widths[i]), i).Max
		}
		// rich keeps table_width from before the re-measure.
	}
	if tableWidth < maxWidth && t.Expand {
		pads := RatioDistribute(maxWidth-tableWidth, widths, nil)
		for i := range widths {
			widths[i] += pads[i]
		}
	}
	return widths
}

// Render is Table.__rich_console__ for a box-less table.
func (t *Grid) Render(o Options) []string {
	if len(t.Columns) == 0 {
		return []string{""}
	}
	widths := t.columnWidths(o)
	var out []string
	for ri := range t.Rows {
		cells := make([][]string, len(t.Columns))
		height := 1
		for ci, c := range t.Columns {
			co := Options{MaxWidth: widths[ci], Justify: firstNonEmpty(c.Justify, "left"),
				Overflow: firstNonEmpty(c.Overflow, "ellipsis"), NoWrap: c.NoWrap}
			cells[ci] = RenderLines(t.paddedCell(ri, ci), co)
			height = max(height, len(cells[ci]))
		}
		for li := 0; li < height; li++ {
			var b strings.Builder
			for ci := range t.Columns {
				if li < len(cells[ci]) {
					b.WriteString(cells[ci][li])
				} else {
					b.WriteString(strings.Repeat(" ", widths[ci]))
				}
			}
			out = append(out, b.String())
		}
	}
	return out
}

// ---------------------------------------------------------------- Columns

// Columns is rich.columns.Columns(padding=(0, 1)) laying items out left to
// right in as many columns as fit.
type Columns struct{ Items []Renderable }

// Render is Columns.__rich_console__.
func (c *Columns) Render(o Options) []string {
	if len(c.Items) == 0 {
		return nil
	}
	const widthPadding = 1
	maxWidth := o.MaxWidth
	n := len(c.Items)
	itemWidths := make([]int, n)
	for i, r := range c.Items {
		itemWidths[i] = Measure(r, o).Max
	}
	columnCount := n
	iter := func(cc int) []int { // widths in layout order, padded
		ws := append([]int(nil), itemWidths...)
		if n%cc != 0 {
			for i := 0; i < cc-n%cc; i++ {
				ws = append(ws, 0)
			}
		}
		return ws
	}
	for columnCount > 1 {
		widths := map[int]int{}
		colNo := 0
		fits := true
		for _, w := range iter(columnCount) {
			widths[colNo] = max(widths[colNo], w)
			total := widthPadding * (len(widths) - 1)
			for _, x := range widths {
				total += x
			}
			if total > maxWidth {
				columnCount = len(widths) - 1
				fits = false
				break
			}
			colNo = (colNo + 1) % columnCount
		}
		if fits {
			break
		}
	}
	grid := &Grid{Padding: [4]int{0, 1, 0, 1}, CollapsePadding: true}
	items := append([]Renderable(nil), c.Items...)
	if n%columnCount != 0 {
		for i := 0; i < columnCount-n%columnCount; i++ {
			items = append(items, nil)
		}
	}
	for start := 0; start < len(items); start += columnCount {
		grid.AddRow(items[start:min(start+columnCount, len(items))]...)
	}
	return grid.Render(o)
}

// ---------------------------------------------------------------- Panel

// BoxPanel is an expanded rich.panel.Panel with the ROUNDED box, padding
// (0, 1) and a left-aligned title.
type BoxPanel struct {
	R     Renderable
	Title string
}

// Render is Panel.__rich_console__.
func (p *BoxPanel) Render(o Options) []string {
	width := o.MaxWidth
	childWidth := width - 2
	title := ""
	if p.Title != "" {
		title = " " + strings.ReplaceAll(Strip(p.Title), "\n", " ") + " "
		childWidth = min(o.MaxWidth-2, max(childWidth, CellLen(title)+2))
	}
	width = childWidth + 2
	lines := RenderLines(&Padding{R: p.R, Left: 1, Right: 1}, o.withWidth(childWidth))
	var out []string
	if title == "" || width <= 4 {
		out = append(out, "╭"+strings.Repeat("─", max(0, width-2))+"╮")
	} else {
		t := truncate(title, width-4, "fold", false)
		t += strings.Repeat("─", max(0, width-4-CellLen(t)))
		out = append(out, "╭─"+t+"─╮")
	}
	for _, l := range lines {
		out = append(out, "│"+l+"│")
	}
	out = append(out, "╰"+strings.Repeat("─", max(0, width-2))+"╯")
	return out
}

// ---------------------------------------------------------------- ratios

// RatioReduce is rich._ratio.ratio_reduce.
func RatioReduce(total int, ratios, maximums, values []int) []int {
	rs := make([]int, len(ratios))
	totalRatio := 0
	for i, r := range ratios {
		if maximums[i] != 0 {
			rs[i] = r
		}
		totalRatio += rs[i]
	}
	out := append([]int(nil), values...)
	if totalRatio == 0 {
		return out
	}
	remaining := total
	for i, r := range rs {
		if r != 0 && totalRatio > 0 {
			d := min(maximums[i], pyRound(float64(r)*float64(remaining)/float64(totalRatio)))
			out[i] = values[i] - d
			remaining -= d
			totalRatio -= r
		}
	}
	return out
}

// RatioDistribute is rich._ratio.ratio_distribute (minimums may be nil).
func RatioDistribute(total int, ratios, minimums []int) []int {
	rs := append([]int(nil), ratios...)
	if minimums != nil {
		for i := range rs {
			if minimums[i] == 0 {
				rs[i] = 0
			}
		}
	}
	totalRatio := sum(rs)
	remaining := total
	out := make([]int, len(rs))
	for i, r := range rs {
		mn := 0
		if minimums != nil {
			mn = minimums[i]
		}
		var d int
		if totalRatio > 0 {
			d = max(mn, int(math.Ceil(float64(r)*float64(remaining)/float64(totalRatio))))
		} else {
			d = remaining
		}
		out[i] = d
		totalRatio -= r
		remaining -= d
	}
	return out
}

// CollapseWidths is rich.table.Table._collapse_widths.
func CollapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	widths = append([]int(nil), widths...)
	total := sum(widths)
	excess := total - maxWidth
	anyWrap := false
	for _, b := range wrapable {
		anyWrap = anyWrap || b
	}
	if !anyWrap {
		return widths
	}
	for total != 0 && excess > 0 {
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
		anyRatio := false
		for i, w := range widths {
			if w == maxCol && wrapable[i] {
				ratios[i] = 1
				anyRatio = true
			}
		}
		if !anyRatio || diff == 0 {
			break
		}
		red := min(excess, diff)
		maxReduce := make([]int, len(widths))
		for i := range maxReduce {
			maxReduce[i] = red
		}
		widths = RatioReduce(excess, ratios, maxReduce, widths)
		total = sum(widths)
		excess = total - maxWidth
	}
	return widths
}
