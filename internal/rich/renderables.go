package rich

import (
	"strings"
)

// Ports of rich's layout renderables (rich 15): Padding, Align, Constrain,
// Group, Styled, Columns, Table, Panel, Tree. Children are any of: a
// string (console markup, rendered with render_str), a *Text, a
// Renderable, or nil (an empty cell).

// ---------------------------------------------------------------- Group

// Group is rich.console.Group (fit=True).
type Group []any

// RichMeasure is measure_renderables.
func (g Group) RichMeasure(c *Console, o Options) Measurement { return c.measureAll(o, g) }

// RichRender renders the members one after another.
func (g Group) RichRender(c *Console, o Options) []Segment {
	var out []Segment
	for _, r := range g {
		out = append(out, c.Render(r, o)...)
	}
	return out
}

// ---------------------------------------------------------------- Padding

// Padding is rich.padding.Padding.
type Padding struct {
	R                        any
	Top, Right, Bottom, Left int
	Style                    any // default "none"
	NoExpand                 bool
}

// Pad builds a Padding from CSS-style dimensions (1, 2 or 4 values).
func Pad(r any, dims ...int) *Padding {
	p := &Padding{R: r}
	p.Top, p.Right, p.Bottom, p.Left = unpackPad(dims...)
	return p
}

func unpackPad(dims ...int) (int, int, int, int) {
	switch len(dims) {
	case 1:
		return dims[0], dims[0], dims[0], dims[0]
	case 2:
		return dims[0], dims[1], dims[0], dims[1]
	case 4:
		return dims[0], dims[1], dims[2], dims[3]
	}
	return 0, 0, 0, 0
}

// RichMeasure is Padding.__rich_measure__.
func (p *Padding) RichMeasure(c *Console, o Options) Measurement {
	extra := p.Left + p.Right
	if o.MaxWidth-extra < 1 {
		return Measurement{o.MaxWidth, o.MaxWidth}
	}
	m := c.Measure(p.R, o)
	return Measurement{m.Min + extra, m.Max + extra}.withMaximum(o.MaxWidth)
}

// RichRender is Padding.__rich_console__.
func (p *Padding) RichRender(c *Console, o Options) []Segment {
	st := p.Style
	if st == nil {
		st = "none"
	}
	style := c.GetStyle(st)
	width := o.MaxWidth
	if p.NoExpand {
		width = min(c.Measure(p.R, o).Max+p.Left+p.Right, o.MaxWidth)
	}
	ro := o.withWidth(width - p.Left - p.Right)
	if ro.Height != nil {
		h := *ro.Height - p.Top - p.Bottom
		ro.Height = &h
	}
	lines := c.RenderLines(p.R, ro, style, true, false)
	var out []Segment
	blank := Segment{strings.Repeat(" ", width) + "\n", style}
	for i := 0; i < p.Top; i++ {
		out = append(out, blank)
	}
	for _, l := range lines {
		if p.Left > 0 {
			out = append(out, Segment{strings.Repeat(" ", p.Left), style})
		}
		out = append(out, l...)
		if p.Right > 0 {
			out = append(out, Segment{strings.Repeat(" ", p.Right), style})
		}
		out = append(out, newLineSegment)
	}
	for i := 0; i < p.Bottom; i++ {
		out = append(out, blank)
	}
	return out
}

// ---------------------------------------------------------------- Constrain

// Constrain is rich.constrain.Constrain (Width 0 = None).
type Constrain struct {
	R     any
	Width int
}

// RichRender renders R at min(Width, max width).
func (k *Constrain) RichRender(c *Console, o Options) []Segment {
	if k.Width <= 0 {
		return c.Render(k.R, o)
	}
	return c.Render(k.R, o.withWidth(min(k.Width, o.MaxWidth)))
}

// RichMeasure measures R at Width.
func (k *Constrain) RichMeasure(c *Console, o Options) Measurement {
	if k.Width > 0 {
		o = o.withWidth(k.Width)
	}
	return c.Measure(k.R, o)
}

// ---------------------------------------------------------------- Styled

// Styled is rich.styled.Styled.
type Styled struct {
	R     any
	Style any
}

// RichRender applies Style to every segment of R.
func (s *Styled) RichRender(c *Console, o Options) []Segment {
	return applyStyle(c.Render(s.R, o), c.GetStyle(s.Style), nil)
}

// RichMeasure measures R.
func (s *Styled) RichMeasure(c *Console, o Options) Measurement { return c.Measure(s.R, o) }

// ---------------------------------------------------------------- Align

// Align is rich.align.Align (vertical alignment not supported).
type Align struct {
	R     any
	Align string // "left", "center", "right"
	Style any    // nil = None
	Pad   bool   // rich default True
	Width int    // 0 = None
}

// AlignLeft is Align(r, "left", pad=False).
func AlignLeft(r any) *Align { return &Align{R: r, Align: "left"} }

// RichMeasure measures R.
func (a *Align) RichMeasure(c *Console, o Options) Measurement { return c.Measure(a.R, o) }

// RichRender is Align.__rich_console__.
func (a *Align) RichRender(c *Console, o Options) []Segment {
	width := c.Measure(a.R, o).Max
	if a.Width > 0 {
		width = min(width, a.Width)
	}
	ro := o
	ro.Height = nil
	rendered := c.Render(&Constrain{R: a.R, Width: width}, ro)
	lines := splitLines(rendered)
	w, h := shape(lines)
	lines = setShape(lines, w, h, nil)
	excess := o.MaxWidth - w
	var style *Style
	if a.Style != nil {
		style = c.GetStyle(a.Style)
	}
	var out []Segment
	for _, l := range lines {
		switch {
		case excess <= 0:
			out = append(out, l...)
		case a.Align == "center":
			left := excess / 2
			if left > 0 {
				out = append(out, Segment{strings.Repeat(" ", left), style})
			}
			out = append(out, l...)
			if a.Pad {
				out = append(out, Segment{strings.Repeat(" ", excess-left), style})
			}
		case a.Align == "right":
			out = append(out, Segment{strings.Repeat(" ", excess), style})
			out = append(out, l...)
		default:
			out = append(out, l...)
			if a.Pad {
				out = append(out, Segment{strings.Repeat(" ", excess), style})
			}
		}
		out = append(out, newLineSegment)
	}
	if styleTruthy(a.Style) {
		out = applyStyle(out, c.GetStyle(a.Style), nil)
	}
	return out
}

// ---------------------------------------------------------------- Columns

// Columns is rich.columns.Columns (left to right, padding (0, 1) unless
// set, not expanded, not equal).
type Columns struct {
	Items   []any
	Padding []int // CSS-style; nil = (0, 1)
	Expand  bool
}

// RichRender is Columns.__rich_console__.
func (cl *Columns) RichRender(c *Console, o Options) []Segment {
	var items []any
	for _, r := range cl.Items {
		if s, ok := r.(string); ok {
			items = append(items, c.RenderStr(s, StrOptions{}))
		} else {
			items = append(items, r)
		}
	}
	if len(items) == 0 {
		return nil
	}
	pad := cl.Padding
	if pad == nil {
		pad = []int{0, 1}
	}
	_, right, _, left := unpackPad(pad...)
	widthPadding := max(left, right)
	maxWidth := o.MaxWidth
	n := len(items)
	itemWidths := make([]int, n)
	for i, r := range items {
		itemWidths[i] = c.Measure(r, o).Max
	}
	columnCount := n
	iterWidths := func(cc int) []int {
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
		for _, w := range iterWidths(columnCount) {
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
	t := NewGrid(pad...)
	t.CollapsePadding, t.PadEdge = true, false
	t.Expand = cl.Expand
	all := append([]any(nil), items...)
	if n%columnCount != 0 {
		for i := 0; i < columnCount-n%columnCount; i++ {
			all = append(all, nil)
		}
	}
	for start := 0; start < len(all); start += columnCount {
		t.AddRow(all[start:min(start+columnCount, len(all))]...)
	}
	return c.Render(t, o)
}

// ---------------------------------------------------------------- Table

// Column is rich.table.Column.
type Column struct {
	Header, Footer                   any
	HeaderStyle, FooterStyle, Style  any
	Justify                          string // default "left"
	Vertical                         string // default "top"
	Overflow                         string // default "ellipsis"
	Width, MinWidth, MaxWidth, Ratio *int
	NoWrap, Highlight                bool
	index                            int
	cells                            []any
}

// IntP returns a pointer to n (optional column widths).
func IntP(n int) *int { return &n }

type tableRow struct {
	style      any
	endSection bool
}

// RichTable is rich.table.Table.
type RichTable struct {
	Columns         []*Column
	rows            []tableRow
	Title, Caption  any // string (markup) or *Text; nil = none
	Width, MinWidth *int
	Box             *Box
	Padding         [4]int
	CollapsePadding bool
	PadEdge         bool
	Expand          bool
	ShowHeader      bool
	ShowFooter      bool
	ShowEdge        bool
	ShowLines       bool
	Leading         int
	Style           any
	RowStyles       []any
	HeaderStyle     any
	FooterStyle     any
	BorderStyle     any
	TitleStyle      any
	CaptionStyle    any
	TitleJustify    string
	CaptionJustify  string
	Highlight       bool
}

// NewTable is Table() with rich's defaults; headers add columns.
func NewTable(headers ...string) *RichTable {
	t := &RichTable{Box: BoxHeavyHead, Padding: [4]int{0, 1, 0, 1}, PadEdge: true,
		ShowHeader: true, ShowEdge: true, Style: "none", HeaderStyle: "table.header",
		FooterStyle: "table.footer", TitleJustify: "center", CaptionJustify: "center"}
	for _, h := range headers {
		t.AddColumn(h, Column{})
	}
	return t
}

// NewGrid is Table.grid(padding=...).
func NewGrid(padding ...int) *RichTable {
	t := NewTable()
	t.Box = nil
	t.Padding = [4]int{}
	if len(padding) > 0 {
		a, b, cc, d := unpackPad(padding...)
		t.Padding = [4]int{a, b, cc, d}
	}
	t.CollapsePadding, t.ShowHeader, t.ShowEdge, t.PadEdge = true, false, false, false
	return t
}

// SetPadding sets CSS-style cell padding.
func (t *RichTable) SetPadding(dims ...int) {
	a, b, c, d := unpackPad(dims...)
	t.Padding = [4]int{a, b, c, d}
}

// AddColumn is Table.add_column(header, ...): the zero Column fields take
// rich's defaults.
func (t *RichTable) AddColumn(header any, col Column) *Column {
	c := col
	c.Header = header
	if c.Justify == "" {
		c.Justify = "left"
	}
	if c.Vertical == "" {
		c.Vertical = "top"
	}
	if c.Overflow == "" {
		c.Overflow = "ellipsis"
	}
	if !col.Highlight {
		c.Highlight = t.Highlight
	}
	c.index = len(t.Columns)
	c.cells = nil
	t.Columns = append(t.Columns, &c)
	return &c
}

// AddRow is Table.add_row(*cells).
func (t *RichTable) AddRow(cells ...any) { t.AddRowStyled(nil, false, cells...) }

// AddRowStyled is Table.add_row(*cells, style=style, end_section=...).
func (t *RichTable) AddRowStyled(style any, endSection bool, cells ...any) {
	for len(cells) < len(t.Columns) {
		cells = append(cells, nil)
	}
	for i, r := range cells {
		if i == len(t.Columns) {
			col := &Column{index: i, Highlight: t.Highlight, Justify: "left", Vertical: "top", Overflow: "ellipsis"}
			for range t.rows {
				col.cells = append(col.cells, NewText(""))
			}
			t.Columns = append(t.Columns, col)
		}
		if r == nil {
			r = ""
		}
		t.Columns[i].cells = append(t.Columns[i].cells, r)
	}
	t.rows = append(t.rows, tableRow{style, endSection})
}

// AddSection is Table.add_section.
func (t *RichTable) AddSection() {
	if len(t.rows) > 0 {
		t.rows[len(t.rows)-1].endSection = true
	}
}

// RowCount is Table.row_count.
func (t *RichTable) RowCount() int { return len(t.rows) }

func (t *RichTable) expand() bool { return t.Expand || t.Width != nil }

func (t *RichTable) extraWidth() int {
	w := 0
	if t.Box != nil && t.ShowEdge {
		w += 2
	}
	if t.Box != nil {
		w += len(t.Columns) - 1
	}
	return w
}

func (t *RichTable) rowStyle(c *Console, i int) *Style {
	st := NullStyle()
	if len(t.RowStyles) > 0 {
		st = st.Add(c.GetStyle(t.RowStyles[i%len(t.RowStyles)]))
	}
	if rs := t.rows[i].style; rs != nil {
		st = st.Add(c.GetStyle(rs))
	}
	return st
}

// RichMeasure is Table.__rich_measure__.
func (t *RichTable) RichMeasure(c *Console, o Options) Measurement {
	maxWidth := o.MaxWidth
	if t.Width != nil {
		maxWidth = *t.Width
	}
	if maxWidth < 0 {
		return Measurement{}
	}
	extra := t.extraWidth()
	maxWidth = sum(t.columnWidths(c, o.withWidth(maxWidth-extra)))
	minW, maxW := extra, extra
	for _, col := range t.Columns {
		m := t.measureColumn(c, o.withWidth(maxWidth), col)
		minW += m.Min
		maxW += m.Max
	}
	if t.Width != nil {
		maxW = *t.Width
	}
	m := Measurement{minW, maxW}
	if t.MinWidth != nil {
		m = m.withMinimum(*t.MinWidth)
	}
	return m
}

type tcell struct {
	style    *Style
	r        any
	vertical string
}

func (t *RichTable) cellPadding(col int, firstRow, lastRow bool) (int, int, int, int) {
	top, right, bottom, left := t.Padding[0], t.Padding[1], t.Padding[2], t.Padding[3]
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
	return top, right, bottom, left
}

func (t *RichTable) cells(c *Console, col *Column) []tcell {
	type raw struct {
		style *Style
		r     any
	}
	var raws []raw
	if t.ShowHeader {
		raws = append(raws, raw{c.GetStyle(orEmpty(t.HeaderStyle)).Add(c.GetStyle(orEmpty(col.HeaderStyle))), orEmpty(col.Header)})
	}
	cs := c.GetStyle(orEmpty(col.Style))
	for _, x := range col.cells {
		raws = append(raws, raw{cs, x})
	}
	if t.ShowFooter {
		raws = append(raws, raw{c.GetStyle(orEmpty(t.FooterStyle)).Add(c.GetStyle(orEmpty(col.FooterStyle))), orEmpty(col.Footer)})
	}
	anyPad := t.Padding != [4]int{}
	out := make([]tcell, len(raws))
	for i, x := range raws {
		r := x.r
		if anyPad {
			tp, rp, bp, lp := t.cellPadding(col.index, i == 0, i == len(raws)-1)
			r = &Padding{R: x.r, Top: tp, Right: rp, Bottom: bp, Left: lp}
		}
		out[i] = tcell{x.style, r, col.Vertical}
	}
	return out
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func (t *RichTable) paddingWidth(col int) int {
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

func (t *RichTable) measureColumn(c *Console, o Options, col *Column) Measurement {
	if o.MaxWidth < 1 {
		return Measurement{}
	}
	pw := t.paddingWidth(col.index)
	if col.Width != nil {
		return Measurement{*col.Width + pw, *col.Width + pw}.withMaximum(o.MaxWidth)
	}
	var m Measurement
	cells := t.cells(c, col)
	for i, cell := range cells {
		x := c.Measure(cell.r, o)
		if i == 0 {
			m = x
			continue
		}
		m.Min = max(m.Min, x.Min)
		m.Max = max(m.Max, x.Max)
	}
	if len(cells) == 0 {
		m = Measurement{1, o.MaxWidth}
	}
	m = m.withMaximum(o.MaxWidth)
	if col.MinWidth != nil {
		m = m.withMinimum(*col.MinWidth + pw)
	}
	if col.MaxWidth != nil {
		m = m.withMaximum(*col.MaxWidth + pw)
	}
	return m
}

func (t *RichTable) columnWidths(c *Console, o Options) []int {
	maxWidth := o.MaxWidth
	n := len(t.Columns)
	ranges := make([]Measurement, n)
	widths := make([]int, n)
	for i, col := range t.Columns {
		ranges[i] = t.measureColumn(c, o, col)
		widths[i] = ranges[i].Max
		if widths[i] == 0 {
			widths[i] = 1
		}
	}
	extra := t.extraWidth()
	if t.expand() {
		var ratios, flexMin []int
		anyRatio := false
		for _, col := range t.Columns {
			if col.Ratio != nil {
				ratios = append(ratios, *col.Ratio)
				anyRatio = anyRatio || *col.Ratio != 0
				w := 1
				if col.Width != nil && *col.Width != 0 {
					w = *col.Width
				}
				flexMin = append(flexMin, w+t.paddingWidth(col.index))
			}
		}
		if anyRatio {
			fixed := make([]int, n)
			for i, col := range t.Columns {
				if col.Ratio == nil {
					fixed[i] = ranges[i].Max
				}
			}
			flex := RatioDistribute(maxWidth-sum(fixed), ratios, flexMin)
			k := 0
			for i, col := range t.Columns {
				if col.Ratio != nil {
					widths[i] = fixed[i] + flex[k]
					k++
				}
			}
		}
	}
	tableWidth := sum(widths)
	if tableWidth > maxWidth {
		wrapable := make([]bool, n)
		for i, col := range t.Columns {
			wrapable[i] = col.Width == nil && !col.NoWrap
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
		for i, col := range t.Columns {
			widths[i] = t.measureColumn(c, o.withWidth(widths[i]), col).Max
		}
	}
	if (tableWidth < maxWidth && t.expand()) || (t.MinWidth != nil && tableWidth < *t.MinWidth-extra) {
		mw := maxWidth
		if t.MinWidth != nil {
			mw = min(*t.MinWidth-extra, maxWidth)
		}
		pads := RatioDistribute(mw-tableWidth, widths, nil)
		for i := range widths {
			widths[i] += pads[i]
		}
	}
	return widths
}

// RichRender is Table.__rich_console__.
func (t *RichTable) RichRender(c *Console, o Options) []Segment {
	if len(t.Columns) == 0 {
		return []Segment{newLineSegment}
	}
	maxWidth := o.MaxWidth
	if t.Width != nil {
		maxWidth = *t.Width
	}
	extra := t.extraWidth()
	widths := t.columnWidths(c, o.withWidth(maxWidth-extra))
	tableWidth := sum(widths) + extra
	ro := o.withWidth(tableWidth)
	ro.Highlight = boolp(t.Highlight)
	ro.Height = nil
	annotation := func(text any, style any, justify string) []Segment {
		var r any = text
		if s, ok := text.(string); ok {
			r = c.RenderStr(s, StrOptions{Style: style, Highlight: boolp(false)})
		}
		ao := ro
		ao.Justify = justify
		return c.Render(r, ao)
	}
	var out []Segment
	if annotationTruthy(t.Title) {
		out = append(out, annotation(t.Title, pickFirst(t.TitleStyle, "table.title"), t.TitleJustify)...)
	}
	out = append(out, t.render(c, ro, widths)...)
	if annotationTruthy(t.Caption) {
		out = append(out, annotation(t.Caption, pickFirst(t.CaptionStyle, "table.caption"), t.CaptionJustify)...)
	}
	return out
}

func annotationTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case *Text:
		return x != nil && x.Len() > 0
	}
	return true
}

// pickFirst is Style.pick_first (first value that is not None).
func pickFirst(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func (t *RichTable) render(c *Console, o Options, widths []int) []Segment {
	tableStyle := c.GetStyle(orEmpty(t.Style))
	borderStyle := tableStyle.Add(c.GetStyle(orEmpty(t.BorderStyle)))
	colCells := make([][]tcell, len(t.Columns))
	for i, col := range t.Columns {
		colCells[i] = t.cells(c, col)
	}
	nRows := len(colCells[0])
	for _, cc := range colCells {
		nRows = min(nRows, len(cc))
	}
	box := t.Box
	if box != nil && !t.ShowHeader {
		box = box.plainHeaded()
	}
	var out []Segment
	seg := func(s string, st *Style) { out = append(out, Segment{s, st}) }
	if box != nil && t.ShowEdge {
		seg(box.getTop(widths), borderStyle)
		out = append(out, newLineSegment)
	}
	for index := 0; index < nRows; index++ {
		first, last := index == 0, index == nRows-1
		headerRow := first && t.ShowHeader
		footerRow := last && t.ShowFooter
		var row *tableRow
		if !headerRow && !footerRow {
			ri := index
			if t.ShowHeader {
				ri--
			}
			row = &t.rows[ri]
		}
		maxHeight := 1
		var rowStyle *Style
		if headerRow || footerRow {
			rowStyle = NullStyle()
		} else {
			ri := index
			if t.ShowHeader {
				ri = index - 1
			}
			rowStyle = t.rowStyle(c, ri)
		}
		cells := make([][][]Segment, len(t.Columns))
		for ci, col := range t.Columns {
			cell := colCells[ci][index]
			co := o.withWidth(widths[ci])
			co.Justify, co.NoWrap, co.Overflow = col.Justify, boolp(col.NoWrap), col.Overflow
			co.Height, co.Highlight = nil, boolp(col.Highlight)
			lines := c.RenderLines(cell.r, co, cell.style.Add(rowStyle), true, false)
			maxHeight = max(maxHeight, len(lines))
			cells[ci] = lines
		}
		rowHeight := 0
		for _, cl := range cells {
			rowHeight = max(rowHeight, len(cl))
		}
		for ci := range t.Columns {
			cell := colCells[ci][index]
			st := cell.style.Add(rowStyle)
			vertical := cell.vertical
			if headerRow {
				vertical = "bottom"
			} else if footerRow {
				vertical = "top"
			}
			var aligned [][]Segment
			switch vertical {
			case "top":
				aligned = alignTop(cells[ci], widths[ci], rowHeight, st)
			case "middle":
				aligned = alignMiddle(cells[ci], widths[ci], rowHeight, st)
			default:
				aligned = alignBottom(cells[ci], widths[ci], rowHeight, st)
			}
			cells[ci] = setShape(aligned, widths[ci], maxHeight, nil)
		}
		if box != nil {
			if last && t.ShowFooter {
				seg(box.getRow(widths, "foot", t.ShowEdge), borderStyle)
				out = append(out, newLineSegment)
			}
			var left, right, div string
			switch {
			case first:
				left, right, div = box.headLeft, box.headRight, box.headVertical
			case last:
				left, right, div = box.footLeft, box.footRight, box.footVertical
			default:
				left, right, div = box.midLeft, box.midRight, box.midVertical
			}
			divider := Segment{div, borderStyle}
			if strings.TrimSpace(div) == "" {
				divider = Segment{div, rowStyle.backgroundStyle().Add(borderStyle)}
			}
			for line := 0; line < maxHeight; line++ {
				if t.ShowEdge {
					seg(left, borderStyle)
				}
				for ci, rc := range cells {
					out = append(out, rc[line]...)
					if ci < len(cells)-1 {
						out = append(out, divider)
					}
				}
				if t.ShowEdge {
					seg(right, borderStyle)
				}
				out = append(out, newLineSegment)
			}
		} else {
			for line := 0; line < maxHeight; line++ {
				for _, rc := range cells {
					out = append(out, rc[line]...)
				}
				out = append(out, newLineSegment)
			}
		}
		if box != nil && first && t.ShowHeader {
			seg(box.getRow(widths, "head", t.ShowEdge), borderStyle)
			out = append(out, newLineSegment)
		}
		endSection := row != nil && row.endSection
		if box != nil && (t.ShowLines || t.Leading > 0 || endSection) {
			if !last && !(t.ShowFooter && index >= nRows-2) && !(t.ShowHeader && headerRow) {
				if t.Leading > 0 {
					seg(strings.Repeat(box.getRow(widths, "mid", t.ShowEdge), t.Leading), borderStyle)
				} else {
					seg(box.getRow(widths, "row", t.ShowEdge), borderStyle)
				}
				out = append(out, newLineSegment)
			}
		}
	}
	if box != nil && t.ShowEdge {
		seg(box.getBottom(widths), borderStyle)
		out = append(out, newLineSegment)
	}
	return out
}

// backgroundStyle is Style.background_style.
func (s *Style) backgroundStyle() *Style {
	if s == nil || s.bgcolor == nil {
		return NullStyle()
	}
	return &Style{bgcolor: s.bgcolor}
}

func (m Measurement) withMinimum(w int) Measurement {
	w = max(0, w)
	return Measurement{max(m.Min, w), max(m.Max, w)}
}

// ---------------------------------------------------------------- Panel

// RichPanel is rich.panel.Panel.
type RichPanel struct {
	R           any
	Box         *Box // nil = ROUNDED
	Title       any  // string (markup) or *Text
	TitleAlign  string
	Subtitle    any
	SubAlign    string
	Fit         bool // expand=False
	Style       any
	BorderStyle any
	Width       int // 0 = None
	Height      int // 0 = None
	Padding     []int
	Highlight   bool
}

func (p *RichPanel) box() *Box {
	if p.Box == nil {
		return BoxRounded
	}
	return p.Box
}

func (p *RichPanel) pad() (int, int, int, int) {
	if p.Padding == nil {
		return 0, 1, 0, 1
	}
	return unpackPad(p.Padding...)
}

func panelTitle(title any) *Text {
	var t *Text
	switch x := title.(type) {
	case nil:
		return nil
	case string:
		if x == "" {
			return nil
		}
		t = FromMarkup(x)
	case *Text:
		if x == nil || x.Len() == 0 {
			return nil
		}
		t = x.Copy()
	default:
		return nil
	}
	t.End = ""
	t.setPlain(strings.ReplaceAll(t.Plain(), "\n", " "))
	t.NoWrap = boolp(true)
	t.expandTabs(0)
	t.pad(1)
	return t
}

// RichMeasure is Panel.__rich_measure__.
func (p *RichPanel) RichMeasure(c *Console, o Options) Measurement {
	title := panelTitle(p.Title)
	_, right, _, left := p.pad()
	padding := left + right
	rs := []any{p.R}
	if title != nil {
		rs = append(rs, title)
	}
	width := p.Width
	if width == 0 {
		width = c.measureAll(o.withWidth(o.MaxWidth-padding-2), rs).Max + padding + 2
	}
	return Measurement{width, width}
}

// RichRender is Panel.__rich_console__.
func (p *RichPanel) RichRender(c *Console, o Options) []Segment {
	top, right, bottom, left := p.pad()
	var r any = p.R
	if top != 0 || right != 0 || bottom != 0 || left != 0 {
		r = &Padding{R: p.R, Top: top, Right: right, Bottom: bottom, Left: left}
	}
	style := c.GetStyle(orNone(p.Style))
	borderStyle := style.Add(c.GetStyle(orNone(p.BorderStyle)))
	width := o.MaxWidth
	if p.Width > 0 {
		width = min(o.MaxWidth, p.Width)
	}
	box := p.box()
	alignText := func(t *Text, width int, align, ch string, st *Style) *Text {
		t = t.Copy()
		t.truncate(width, "", false)
		excess := width - t.CellLen()
		if styleTruthy(t.Style) {
			t.Stylize(c.GetStyle(t.Style), 0, nil)
		}
		if excess <= 0 {
			return t
		}
		var res *Text
		switch align {
		case "left":
			res = Assemble(AssemblePart{Text: t}, AssemblePart{Plain: strings.Repeat(ch, excess), Style: st})
		case "center":
			l := excess / 2
			res = Assemble(AssemblePart{Plain: strings.Repeat(ch, l), Style: st}, AssemblePart{Text: t},
				AssemblePart{Plain: strings.Repeat(ch, excess-l), Style: st})
		default:
			res = Assemble(AssemblePart{Plain: strings.Repeat(ch, excess), Style: st}, AssemblePart{Text: t})
		}
		res.NoWrap, res.End = boolp(true), ""
		return res
	}
	title := panelTitle(p.Title)
	if title != nil {
		title.StylizeBefore(borderStyle, 0, nil)
	}
	childWidth := width - 2
	if p.Fit {
		childWidth = c.Measure(r, o.withWidth(width-2)).Max
	}
	var childHeight *int
	if p.Height > 0 {
		h := p.Height - 2
		childHeight = &h
	} else if o.Height != nil && *o.Height > 0 {
		h := *o.Height - 2
		childHeight = &h
	}
	if title != nil {
		childWidth = min(o.MaxWidth-2, max(childWidth, title.CellLen()+2))
	}
	width = childWidth + 2
	co := o.withWidth(childWidth)
	co.Height, co.Highlight = childHeight, boolp(p.Highlight)
	lines := c.RenderLines(r, co, style, true, false)
	var out []Segment
	if title == nil || width <= 4 {
		out = append(out, Segment{box.getTop([]int{width - 2}), borderStyle})
	} else {
		ta := firstNonEmpty(p.TitleAlign, "center")
		title = alignText(title, width-4, ta, box.top, borderStyle)
		out = append(out, Segment{box.topLeft + box.top, borderStyle})
		out = append(out, c.Render(title, co.withWidth(width-4))...)
		out = append(out, Segment{box.top + box.topRight, borderStyle})
	}
	out = append(out, newLineSegment)
	for _, l := range lines {
		out = append(out, Segment{box.midLeft, borderStyle})
		out = append(out, l...)
		out = append(out, Segment{box.midRight, borderStyle})
		out = append(out, newLineSegment)
	}
	sub := panelTitle(p.Subtitle)
	if sub != nil {
		sub.StylizeBefore(borderStyle, 0, nil)
	}
	if sub == nil || width <= 4 {
		out = append(out, Segment{box.getBottom([]int{width - 2}), borderStyle})
	} else {
		sa := firstNonEmpty(p.SubAlign, "center")
		sub = alignText(sub, width-4, sa, box.bottom, borderStyle)
		out = append(out, Segment{box.bottomLeft + box.bottom, borderStyle})
		out = append(out, c.Render(sub, co.withWidth(width-4))...)
		out = append(out, Segment{box.bottom + box.bottomRight, borderStyle})
	}
	out = append(out, newLineSegment)
	return out
}

func orNone(v any) any {
	if v == nil {
		return "none"
	}
	return v
}

// ---------------------------------------------------------------- Tree

// Tree is rich.tree.Tree.
type Tree struct {
	Label      any
	Style      any // nil = "tree"
	GuideStyle any // nil = "tree.line"
	Children   []*Tree
	Collapsed  bool
	Highlight  bool
	HideRoot   bool
}

// Add is Tree.add (style and guide style inherited).
func (t *Tree) Add(label any) *Tree {
	n := &Tree{Label: label, Style: t.Style, GuideStyle: t.GuideStyle, Highlight: t.Highlight}
	t.Children = append(t.Children, n)
	return n
}

var treeGuides = [3][4]string{
	{"    ", "│   ", "├── ", "└── "},
	{"    ", "┃   ", "┣━━ ", "┗━━ "},
	{"    ", "║   ", "╠══ ", "╚══ "},
}

func (t *Tree) style() any {
	if t.Style == nil {
		return "tree"
	}
	return t.Style
}

func (t *Tree) guideStyle() any {
	if t.GuideStyle == nil {
		return "tree.line"
	}
	return t.GuideStyle
}

const (
	gSpace = iota
	gContinue
	gFork
	gEnd
)

func makeGuide(index int, st *Style) Segment {
	g := 0
	if st.hasAttr(0) { // bold
		g = 1
	} else if st.hasAttr(9) { // underline2
		g = 2
	}
	return Segment{treeGuides[g][index], st}
}

func (s *Style) hasAttr(bit int) bool {
	return s != nil && s.set&(1<<bit) != 0 && s.attrs&(1<<bit) != 0
}

type treeIter struct {
	nodes []*Tree
	i     int
}

// RichRender is Tree.__rich_console__.
func (t *Tree) RichRender(c *Console, o Options) []Segment {
	null := NullStyle()
	guideStyle := c.GetStyle(t.guideStyle())
	if !guideStyle.truthy() {
		guideStyle = null
	}
	levels := []Segment{makeGuide(gContinue, guideStyle)}
	stack := []*treeIter{{nodes: []*Tree{t}}}
	guideStack := []*Style{c.GetStyle(t.guideStyle())}
	styleStack := []*Style{c.GetStyle(t.style())}
	remove := &Style{set: 1<<0 | 1<<9}
	segStyle := func(s Segment) *Style {
		if s.Style.truthy() {
			return s.Style
		}
		return null
	}
	var out []Segment
	depth := 0
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.i >= len(it.nodes) {
			levels = levels[:len(levels)-1]
			if len(levels) > 0 {
				gs := segStyle(levels[len(levels)-1])
				levels[len(levels)-1] = makeGuide(gFork, gs)
				guideStack = guideStack[:len(guideStack)-1]
				styleStack = styleStack[:len(styleStack)-1]
			}
			continue
		}
		node := it.nodes[it.i]
		last := it.i == len(it.nodes)-1
		it.i++
		stack = append(stack, it)
		if last {
			levels[len(levels)-1] = makeGuide(gEnd, segStyle(levels[len(levels)-1]))
		}
		gStyle := guideStack[len(guideStack)-1].Add(c.GetStyle(node.guideStyle()))
		style := styleStack[len(styleStack)-1].Add(c.GetStyle(node.style()))
		skip := 1
		if t.HideRoot {
			skip = 2
		}
		var prefix []Segment
		if len(levels) > skip {
			prefix = append(prefix, levels[skip:]...)
		}
		pw := 0
		for _, l := range prefix {
			pw += l.cellLen()
		}
		lo := o.withWidth(o.MaxWidth - pw)
		lo.Highlight, lo.Height = boolp(t.Highlight), nil
		lines := c.RenderLines(&Styled{R: node.Label, Style: style}, lo, nil, o.Justify != "", false)
		if !(depth == 0 && t.HideRoot) {
			for li, line := range lines {
				if len(prefix) > 0 {
					out = append(out, applyStyle(prefix, style.backgroundStyle(), remove)...)
				}
				out = append(out, line...)
				out = append(out, newLineSegment)
				if li == 0 && len(prefix) > 0 {
					g := gContinue
					if last {
						g = gSpace
					}
					prefix[len(prefix)-1] = makeGuide(g, segStyle(prefix[len(prefix)-1]))
				}
			}
		}
		if !node.Collapsed && len(node.Children) > 0 {
			g := gContinue
			if last {
				g = gSpace
			}
			levels[len(levels)-1] = makeGuide(g, segStyle(levels[len(levels)-1]))
			ng := gFork
			if len(node.Children) == 1 {
				ng = gEnd
			}
			levels = append(levels, makeGuide(ng, gStyle))
			styleStack = append(styleStack, styleStack[len(styleStack)-1].Add(c.GetStyle(node.style())))
			guideStack = append(guideStack, guideStack[len(guideStack)-1].Add(c.GetStyle(node.guideStyle())))
			stack = append(stack, &treeIter{nodes: node.Children})
			depth++
		}
	}
	return out
}

// RichMeasure is Tree.__rich_measure__.
func (t *Tree) RichMeasure(c *Console, o Options) Measurement {
	var mn, mx int
	var walk func(n *Tree, level int)
	walk = func(n *Tree, level int) {
		m := c.Measure(n.Label, o)
		indent := level * 4
		mn = max(m.Min+indent, mn)
		mx = max(m.Max+indent, mx)
		if !n.Collapsed {
			for _, ch := range n.Children {
				walk(ch, level+1)
			}
		}
	}
	walk(t, 0)
	return Measurement{mn, mx}
}
